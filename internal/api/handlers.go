package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"deploy/internal/config"
	"deploy/internal/build"
	"deploy/internal/dns"
	"deploy/internal/state"
	"deploy/internal/types"

	"gopkg.in/yaml.v3"

	"github.com/google/uuid"
)

// secretSettings are settings keys whose values should be encrypted at rest
// and masked in API responses unless explicitly revealed.
var secretSettings = map[string]bool{
	"dns_token":  true,
	"dns_secret": true,
}

var validAppName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// --- Health ---


// shortID safely truncates a container ID to 12 characters for display.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	apps, err := state.ListApps(s.db, "")
	count := 0
	if err == nil {
		count = len(apps)
	}

	writeJSON(w, http.StatusOK, types.HealthResponse{
		Status:    "ok",
		Version:   config.Version,
		Uptime:    time.Since(s.startedAt).Round(time.Second).String(),
		AppsCount: count,
	})
}

// --- Create App ---

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req types.CreateAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid JSON body"))
		return
	}

	if err := config.ValidateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, BadRequestError(err.Error()))
		return
	}
	if req.Image == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("image is required"))
		return
	}
	if err := config.ValidatePort(req.Port); err != nil {
		writeError(w, http.StatusBadRequest, BadRequestError(err.Error()))
		return
	}

	if req.Env == nil {
		req.Env = make(map[string]string)
	}

	app := &types.App{
		ID:     uuid.New().String(),
		Name:   req.Name,
		Status: types.StatusCreated,
		Port:   req.Port,
		Image:  req.Image,
		Env:    req.Env,
	}

	created, err := state.CreateApp(s.db, app)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusConflict, ConflictError(fmt.Sprintf("app %q already exists", req.Name)))
			return
		}
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// --- List Apps ---

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")

	apps, err := state.ListApps(s.db, statusFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	if apps == nil {
		apps = []types.App{}
	}

	writeJSON(w, http.StatusOK, types.ListAppsResponse{Apps: apps})
}

// --- Get App ---

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	writeJSON(w, http.StatusOK, app)
}

// --- Delete App ---

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if app.Status == types.StatusRunning || app.Status == types.StatusUnknown {
		writeError(w, http.StatusConflict, ErrorBody(appRunningError(fmt.Sprintf("app %q is running; stop it first", name))))
		return
	}

	if app.ContainerID != "" {
		if err := s.runner.RemoveContainer(r.Context(), app.ContainerID); err != nil {
			log.Printf("warning: remove container %s: %v", app.ContainerID, err)
		}
	}

	if err := state.DeleteApp(s.db, name); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("app %q deleted", name)})
}

// --- Remove App (clean teardown) ---

func (s *Server) handleRemoveApp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	// 1. Check deploy lock to prevent races with concurrent deploys
	if s.deployer != nil && s.deployer.IsLocked(app.Name) {
		writeError(w, http.StatusConflict, ErrorBody(appRunningError("deploy in progress for this app, cannot remove")))
		return
	}

	// 2. Get domains before DB delete (for Caddy cleanup)
	domains, _ := state.ListDomainsByApp(s.db, app.ID)

	// 3. Delete app record from DB FIRST — if this fails, nothing is lost
	// ponytail: DB delete first, cleanup is best-effort
	if err := state.DeleteApp(s.db, name); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	// 4. Stop + remove container — BEST EFFORT
	if app.ContainerID != "" {
		if err := s.runner.StopContainer(r.Context(), app.ContainerID); err != nil {
			log.Printf("warning: stop container %s: %v", shortID(app.ContainerID), err)
		}
		if err := s.runner.RemoveContainer(r.Context(), app.ContainerID); err != nil {
			log.Printf("warning: remove container %s: %v", shortID(app.ContainerID), err)
		}
	}

	// 5. Remove all tarballs for this app — BEST EFFORT
	if err := build.RemoveAllTarballs(name); err != nil {
		log.Printf("warning: remove tarballs: %v", err)
	}

	// 6. Remove Caddy snippets for this app's domains — BEST EFFORT
	if s.caddyManager != nil && s.caddyManager.IsRunning() {
		for _, d := range domains {
			if err := s.caddyManager.RemoveDomainSnippet(d.Domain); err != nil {
				log.Printf("warning: remove domain snippet %s: %v", d.Domain, err)
			}
		}
	}

	writeJSON(w, http.StatusOK, types.RemoveAppResponse{
		Message: fmt.Sprintf("app %q removed", name),
	})
}

// --- Start App ---

func (s *Server) handleStartApp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	async := r.URL.Query().Get("async") == "true"

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if async {
		jobResult := s.scheduler.StartJob("start", app.ID, func(ctx context.Context) (string, error) {
			return s.startAppContainer(ctx, app)
		})

		writeJSON(w, http.StatusAccepted, types.StartStopResponse{
			JobID:   jobResult.Job.ID,
			Message: fmt.Sprintf("starting app %q (async)", name),
		})
		return
	}

	// Wait mode: synchronous with health check
	result, err := s.startAppContainer(r.Context(), app)
	if err != nil {
		errStr := err.Error()
	if strings.Contains(errStr, "not found") {
		writeError(w, http.StatusNotFound, ErrorBody(notFoundAsError(errStr)))
	} else {
		writeError(w, http.StatusInternalServerError, ErrorBody(dockerError(errStr)))
	}
		return
	}

	logs := s.getContainerLogs(r.Context(), app.ContainerID)
	if logs != "" {
		// Check health
		if err := s.healthCheckContainer(r.Context(), app.ContainerID); err != nil {
			log.Printf("warning: health check: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, types.StartStopResponse{
		App:       app,
		Message:   result,
		Container: app.ContainerID,
		Logs:      logs,
	})
}

func (s *Server) startAppContainer(ctx context.Context, app *types.App) (string, error) {
	if err := s.runner.PullImage(ctx, app.Image); err != nil {
		return "", fmt.Errorf("pull image: %w", err)
	}

	ver := fmt.Sprintf("manual-%d", time.Now().Unix())
	containerID, err := s.runner.CreateContainer(ctx, app, ver)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	if err := s.runner.StartContainer(ctx, containerID); err != nil {
		s.runner.RemoveContainer(ctx, containerID)
		return "", fmt.Errorf("start container: %w", err)
	}

	if err := state.UpdateAppContainer(s.db, app.Name, containerID); err != nil {
		return "", fmt.Errorf("update app container: %w", err)
	}
	if err := state.UpdateAppStatus(s.db, app.Name, types.StatusRunning); err != nil {
		return "", fmt.Errorf("update app status: %w", err)
	}

	app.ContainerID = containerID
	app.Status = types.StatusRunning
	return fmt.Sprintf("started container %s", shortID(containerID)), nil
}

func (s *Server) healthCheckContainer(ctx context.Context, containerID string) error {
	if containerID == "" {
		return nil
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cs, err := s.runner.InspectContainer(ctx, containerID)
		if err != nil {
			return fmt.Errorf("inspect container: %w", err)
		}
		if !cs.Running && cs.ExitCode != 0 {
			return fmt.Errorf("container exited with code %d", cs.ExitCode)
		}
		if cs.Running {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	cs, err := s.runner.InspectContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect container: %w", err)
	}
	if !cs.Running && cs.ExitCode != 0 {
		return fmt.Errorf("container exited with code %d after 3s", cs.ExitCode)
	}
	return nil
}

func (s *Server) getContainerLogs(ctx context.Context, containerID string) string {
	if containerID == "" {
		return ""
	}
	reader, err := s.runner.GetContainerLogs(ctx, containerID, 50, false)
	if err != nil {
		log.Printf("get container logs: %v", err)
		return "(failed to get logs: internal server error)"
	}
	defer reader.Close()
	data, _ := io.ReadAll(reader)
	return string(data)
}

// --- Stop App ---

func (s *Server) handleStopApp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	async := r.URL.Query().Get("async") == "true"

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if app.Status != types.StatusRunning && app.Status != types.StatusUnknown && app.Status != types.StatusCreated {
		writeError(w, http.StatusConflict, ErrorBody(appNotRunningError(fmt.Sprintf("app %q is not running", name))))
		return
	}

	if async {
		jobResult := s.scheduler.StartJob("stop", app.ID, func(ctx context.Context) (string, error) {
			return s.stopAppContainer(ctx, app)
		})

		writeJSON(w, http.StatusAccepted, types.StartStopResponse{
			JobID:   jobResult.Job.ID,
			Message: fmt.Sprintf("stopping app %q (async)", name),
		})
		return
	}

	result, err := s.stopAppContainer(r.Context(), app)
	if err != nil {
		errStr := err.Error()
	if strings.Contains(errStr, "not found") {
		writeError(w, http.StatusNotFound, ErrorBody(notFoundAsError(errStr)))
	} else {
		writeError(w, http.StatusInternalServerError, ErrorBody(dockerError(errStr)))
	}
		return
	}

	writeJSON(w, http.StatusOK, types.StartStopResponse{
		App:     app,
		Message: result,
	})
}

func (s *Server) stopAppContainer(ctx context.Context, app *types.App) (string, error) {
	if app.ContainerID != "" {
		if err := s.runner.StopContainer(ctx, app.ContainerID); err != nil {
			return "", fmt.Errorf("stop container: %w", err)
		}
		if err := s.runner.RemoveContainer(ctx, app.ContainerID); err != nil {
			return "", fmt.Errorf("remove container: %w", err)
		}
	}

	if err := state.UpdateAppContainer(s.db, app.Name, ""); err != nil {
		return "", fmt.Errorf("update app container: %w", err)
	}
	if err := state.UpdateAppStatus(s.db, app.Name, types.StatusStopped); err != nil {
		return "", fmt.Errorf("update app status: %w", err)
	}

	app.ContainerID = ""
	app.Status = types.StatusStopped
	return fmt.Sprintf("stopped app %q", app.Name), nil
}

// handleDevStart starts a development container for an app.
func (s *Server) handleDevStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}
	if !validAppName.MatchString(name) {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid app name"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	// Build dev config
	devPort := app.Port + 1000
	if devPort > 65535 {
		writeError(w, http.StatusBadRequest, BadRequestError("port overflow: app port too high"))
		return
	}

	// Try to parse deploy.yml for dev config
	deployDir := config.DeployDirPath()
	checkoutPath := filepath.Join(deployDir, "git", "checkouts", name, "deploy.yml")
	var devVolumes []types.VolumeMapping
	var devCmd string
	if data, err := os.ReadFile(checkoutPath); err == nil {
		var dc types.DeployConfig
		if yaml.Unmarshal(data, &dc) == nil && dc.Dev != nil {
			devVolumes = dc.Dev.Volumes
			devCmd = dc.Dev.Command
			if dc.Dev.Port > 0 {
				devPort = dc.Dev.Port
			}
		}
	}

	// Validate dev volumes
	var validatedVolumes []types.VolumeMapping
	for _, v := range devVolumes {
		if !filepath.IsAbs(v.Source) {
			log.Printf("warning: dev volume source %q is not absolute, skipping", v.Source)
			continue
		}
		if _, err := os.Stat(v.Source); os.IsNotExist(err) {
			log.Printf("warning: dev volume source %q does not exist, skipping", v.Source)
			continue
		}
		validatedVolumes = append(validatedVolumes, v)
	}

	// Create a dev copy of the app
	devApp := *app
	devApp.Dev = true
	devApp.Port = devPort
	devApp.Volumes = validatedVolumes
	devApp.Command = devCmd

	if err := s.runner.PullImage(r.Context(), app.Image); err != nil {
		errStr := err.Error()
	if strings.Contains(errStr, "not found") {
		writeError(w, http.StatusNotFound, ErrorBody(notFoundAsError(errStr)))
	} else {
		writeError(w, http.StatusInternalServerError, ErrorBody(dockerError(errStr)))
	}
		return
	}

	ver := fmt.Sprintf("dev-%d", time.Now().Unix())
	containerID, err := s.runner.CreateContainer(r.Context(), &devApp, ver)
	if err != nil {
		errStr := err.Error()
	if strings.Contains(errStr, "not found") {
		writeError(w, http.StatusNotFound, ErrorBody(notFoundAsError(errStr)))
	} else {
		writeError(w, http.StatusInternalServerError, ErrorBody(dockerError(errStr)))
	}
		return
	}

	if err := s.runner.StartContainer(r.Context(), containerID); err != nil {
		s.runner.RemoveContainer(r.Context(), containerID)
		errStr := err.Error()
	if strings.Contains(errStr, "not found") {
		writeError(w, http.StatusNotFound, ErrorBody(notFoundAsError(errStr)))
	} else {
		writeError(w, http.StatusInternalServerError, ErrorBody(dockerError(errStr)))
	}
		return
	}

	trackDevContainer(name, containerID)

	writeJSON(w, http.StatusOK, types.StartStopResponse{
		Container: shortID(containerID),
		Message:   fmt.Sprintf("dev container for %q started on port %d", name, devPort),
	})
}

// handleDevStop stops and removes a development container.
func (s *Server) handleDevStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	containerID, found := getDevContainer(name)
	if !found {
		// Try to find dev container by labels as fallback
		var err error
		containerID, err = s.runner.FindDevContainer(r.Context(), name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
			return
		}
		if containerID == "" {
			writeJSON(w, http.StatusOK, types.StartStopResponse{
				Message: fmt.Sprintf("no dev container found for %q", name),
			})
			return
		}
	}

	if err := s.runner.StopContainer(r.Context(), containerID); err != nil {
		// If container already gone, clean up tracking and return success
		untrackDevContainer(name)
		log.Printf("dev stop: container %s already stopped/removed: %v", shortID(containerID), err)
		writeJSON(w, http.StatusOK, types.StartStopResponse{
			Message: fmt.Sprintf("dev container for %q stopped (was already gone)", name),
		})
		return
	}
	if err := s.runner.RemoveContainer(r.Context(), containerID); err != nil {
		// Clean up tracking even if remove fails
		untrackDevContainer(name)
		log.Printf("dev stop: container %s remove error: %v", shortID(containerID), err)
		writeJSON(w, http.StatusOK, types.StartStopResponse{
			Message: fmt.Sprintf("dev container for %q stopped (remove warning)", name),
		})
		return
	}

	untrackDevContainer(name)

	writeJSON(w, http.StatusOK, types.StartStopResponse{
		Message: fmt.Sprintf("dev container for %q stopped", name),
	})
}


// --- Get Logs ---

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if app.ContainerID == "" {
		writeJSON(w, http.StatusOK, []types.LogEntry{})
		return
	}

	tailStr := r.URL.Query().Get("tail")
	tail := 100
	if tailStr != "" {
		tail, err = strconv.Atoi(tailStr)
		if err != nil || tail < 0 {
			tail = 100
		}
	}

	follow := r.URL.Query().Get("follow") == "true"

	if follow {
		s.streamLogsSSE(w, r, app.ContainerID, tail)
		return
	}

	reader, err := s.runner.GetContainerLogs(r.Context(), app.ContainerID, tail, false)
	if err != nil {
		errStr := err.Error()
	if strings.Contains(errStr, "not found") {
		writeError(w, http.StatusNotFound, ErrorBody(notFoundAsError(errStr)))
	} else {
		writeError(w, http.StatusInternalServerError, ErrorBody(dockerError(errStr)))
	}
		return
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	entries := make([]types.LogEntry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		entries = append(entries, types.LogEntry{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Line:      line,
			Stream:    "stdout",
		})
	}

	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) streamLogsSSE(w http.ResponseWriter, r *http.Request, containerID string, tail int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, ErrorBody(&types.SystemError{Code: types.ErrInternal, Message: "streaming not supported"}))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	reader, err := s.runner.GetContainerLogs(r.Context(), containerID, tail, true)
	if err != nil {
		log.Printf("SSE error: %v", err)
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", "internal server error")
		flusher.Flush()
		return
	}
	defer reader.Close()

	buf := make([]byte, 4096)
	for {
		select {
		case <-r.Context().Done():
			return
		default:
			n, err := reader.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("SSE error: %v", err)
					fmt.Fprintf(w, "event: error\ndata: %s\n\n", "internal server error")
					flusher.Flush()
				}
				return
			}
			if n > 0 {
				line := strings.TrimRight(string(buf[:n]), "\n\r")
				for _, l := range strings.Split(line, "\n") {
					if l != "" {
						fmt.Fprintf(w, "data: %s\n\n", l)
					}
				}
				flusher.Flush()
			}
		}
	}
}

// --- Get Job ---

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("job id required"))
		return
	}

	job, err := s.scheduler.GetJob(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if job == nil {
		writeError(w, http.StatusNotFound, NotFoundError("job"))
		return
	}

	writeJSON(w, http.StatusOK, job)
}




// --- Promote ---

func (s *Server) handlePromote(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	var req struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Dir = "."
	}

	dir := req.Dir
	if dir == "" {
		dir = "."
	}
	// TODO: path sandboxing when multi-user support lands

	wait := r.URL.Query().Get("wait") != "false"

	if !wait {
		// Async: run via scheduler
		jobResult := s.scheduler.StartJob("promote", name, func(ctx context.Context) (string, error) {
			if s.deployer == nil {
				return "", fmt.Errorf("deployer not available")
			}
			resp, err := s.deployer.Promote(ctx, &types.PromoteRequest{}, name, dir)
			if err != nil {
				return "", err
			}
			return resp.Message, nil
		})
		writeJSON(w, http.StatusAccepted, types.StartStopResponse{
			JobID:   jobResult.Job.ID,
			Message: fmt.Sprintf("promoting app %q (async)", name),
		})
		return
	}

	if s.deployer == nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(&types.SystemError{Code: types.ErrInternal, Message: "deployer not available"}))
		return
	}

	resp, err := s.deployer.Promote(r.Context(), &types.PromoteRequest{}, name, dir)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "not found") {
			writeError(w, http.StatusNotFound, ErrorBody(notFoundAsError(errStr)))
		} else {
			writeError(w, http.StatusInternalServerError, ErrorBody(dockerError(errStr)))
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Rollback ---

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	var req types.RollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Version = ""
	}

	if s.deployer == nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(&types.SystemError{Code: types.ErrInternal, Message: "deployer not available"}))
		return
	}

	resp, err := s.deployer.Rollback(r.Context(), name, req.Version, ".")
	if err != nil {
		errStr := err.Error()
	if strings.Contains(errStr, "not found") {
		writeError(w, http.StatusNotFound, ErrorBody(notFoundAsError(errStr)))
	} else {
		writeError(w, http.StatusInternalServerError, ErrorBody(dockerError(errStr)))
	}
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Status ---

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	if s.deployer == nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(&types.SystemError{Code: types.ErrInternal, Message: "deployer not available"}))
		return
	}

	resp, err := s.deployer.Status(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Global Status ---

func (s *Server) handleGlobalStatus(w http.ResponseWriter, r *http.Request) {
	apps, err := state.ListApps(s.db, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	var summaries []types.AppStatusSummary
	for _, app := range apps {
		activeDep, err := state.GetActiveDeployment(s.db, app.ID)
		if err != nil {
			log.Printf("warning: get active deployment for %s: %v", app.Name, err)
		}
		inProgress := false
		if s.deployer != nil {
			inProgress = s.deployer.IsLocked(app.Name)
		}

		summaries = append(summaries, types.AppStatusSummary{
			App:              app,
			ActiveDeployment: activeDep,
			DeployInProgress: inProgress,
		})
	}

	if summaries == nil {
		summaries = []types.AppStatusSummary{}
	}

	writeJSON(w, http.StatusOK, types.GlobalStatusResponse{Apps: summaries})
}

// --- List Images ---

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}
	if !validAppName.MatchString(name) {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid app name"))
		return
	}

	images, err := build.ListTarballs(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	if images == nil {
		images = []string{}
	}

	writeJSON(w, http.StatusOK, types.ListImagesResponse{Images: images})
}

// --- Remove Image ---

func (s *Server) handleRemoveImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	version := r.PathValue("version")
	if name == "" || version == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name and version required"))
		return
	}
	if !validAppName.MatchString(name) {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid app name"))
		return
	}

	if err := build.RemoveTarball(name, version); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("removed image %s:%s", name, version),
	})
}

// --- Set Secret ---

func (s *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	var req types.SetSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid JSON body"))
		return
	}
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("key is required"))
		return
	}
	if req.Value == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("value is required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	secret := &types.Secret{
		AppID: app.ID,
		Key:   req.Key,
		Value: req.Value,
	}
	if _, err := state.SetSecret(s.db, secret, s.masterKey); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("secret %q set for app %q", req.Key, name),
	})
}

// --- List Secrets ---

func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	secrets, err := state.ListSecrets(s.db, app.ID, s.masterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	// Mask values for listing
	masked := make([]types.Secret, len(secrets))
	for i, sec := range secrets {
		masked[i] = types.Secret{
			AppID: sec.AppID,
			Key:   sec.Key,
			Value: "<masked>",
		}
		masked[i].CreatedAt = sec.CreatedAt
		masked[i].UpdatedAt = sec.UpdatedAt
	}

	if masked == nil {
		masked = []types.Secret{}
	}

	writeJSON(w, http.StatusOK, types.ListSecretsResponse{Secrets: masked})
}

// --- Get Secret ---

func (s *Server) handleGetSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	key := r.PathValue("key")
	if name == "" || key == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name and key required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	secret, err := state.GetSecret(s.db, app.ID, key, s.masterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if secret == nil {
		writeError(w, http.StatusNotFound, NotFoundError("secret"))
		return
	}

	writeJSON(w, http.StatusOK, secret)
}

// --- Remove Secret ---

func (s *Server) handleRemoveSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	key := r.PathValue("key")
	if name == "" || key == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name and key required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if err := state.DeleteSecret(s.db, app.ID, key); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("secret %q removed from app %q", key, name),
	})
}

// --- Domain Management ---

func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	var req types.AddDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid JSON body"))
		return
	}

	if req.Domain == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("domain is required"))
		return
	}
	if !strings.Contains(req.Domain, ".") {
		writeError(w, http.StatusBadRequest, BadRequestError("domain must contain at least one dot"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	domain := &types.Domain{
		AppID:  app.ID,
		Domain: req.Domain,
	}
	if err := state.CreateDomain(s.db, domain); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	domain.AppName = app.Name

	// Write Caddy snippet and reload
	if s.caddyManager != nil && s.caddyManager.IsRunning() {
		if err := s.caddyManager.AddDomainSnippet(app.Name, req.Domain, app.Port); err != nil {
			log.Printf("warning: caddy add domain snippet: %v", err)
		}
	}

	writeJSON(w, http.StatusCreated, domain)
}

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if name != "" {
		app, err := state.GetAppByName(s.db, name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
			return
		}
		if app == nil {
			writeError(w, http.StatusNotFound, NotFoundError("app"))
			return
		}

		domains, err := state.ListDomainsByApp(s.db, app.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
			return
		}

		result := make([]types.Domain, len(domains))
		for i, d := range domains {
			result[i] = *d
			result[i].AppName = app.Name
		}
		if result == nil {
			result = []types.Domain{}
		}

		writeJSON(w, http.StatusOK, types.ListDomainsResponse{Domains: result})
		return
	}

	// List all domains across all apps
	domains, err := state.ListDomains(s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	result := make([]types.Domain, len(domains))
	for i, d := range domains {
		result[i] = *d
		// Enrich with app name
		app, err := state.GetApp(s.db, d.AppID)
		if err == nil && app != nil {
			result[i].AppName = app.Name
		}
	}
	if result == nil {
		result = []types.Domain{}
	}

	writeJSON(w, http.StatusOK, types.ListDomainsResponse{Domains: result})
}

func (s *Server) handleRemoveDomain(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	domainName := r.PathValue("domain")
	if name == "" || domainName == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name and domain required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if err := state.DeleteDomainByDomain(s.db, domainName); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	// Remove Caddy snippet and reload
	if s.caddyManager != nil && s.caddyManager.IsRunning() {
		if err := s.caddyManager.RemoveDomainSnippet(domainName); err != nil {
			log.Printf("warning: caddy remove domain snippet: %v", err)
		}
	}

	writeJSON(w, http.StatusNoContent, nil)
}

// --- Config (settings) ---

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	reveal := r.URL.Query().Get("reveal") == "true"

	if key != "" {
		if secretSettings[key] && reveal {
			val, err := state.EncryptedGetSetting(s.db, key, s.masterKey)
			if err != nil {
				writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{key: val})
			return
		}

		val, err := state.GetSetting(s.db, key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
			return
		}
		if secretSettings[key] {
			val = "***"
		}
		writeJSON(w, http.StatusOK, map[string]string{key: val})
		return
	}

	settings, err := state.GetAllSettings(s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	for k := range settings {
		if secretSettings[k] {
			settings[k] = "***"
		}
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid JSON body"))
		return
	}
	// ponytail: single key-value at a time, no transaction needed
	// Validate key is non-empty
	for key := range req {
		if strings.TrimSpace(key) == "" {
			writeError(w, http.StatusBadRequest, BadRequestError("key cannot be empty"))
			return
		}
	}
	if len(req) != 1 {
		writeError(w, http.StatusBadRequest, BadRequestError("exactly one key=value pair required"))
		return
	}
	for k, v := range req {
		if secretSettings[k] {
			if err := state.EncryptedSetSetting(s.db, k, v, s.masterKey); err != nil {
				writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
				return
			}
		} else {
			if err := state.SetSetting(s.db, k, v); err != nil {
				writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "settings updated"})
}

// --- DNS Automation (Phase 8) ---

// DNSSyncRequest is the JSON body for POST /api/v1/apps/{name}/dns/sync.
type DNSSyncRequest struct {
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

// DNSSyncResult records the result of syncing a single DNS record.
type DNSSyncResult struct {
	Domain  string `json:"domain"`
	Type    string `json:"type"`
	Value   string `json:"value"`
	Existed bool   `json:"existed"`
}

// DNSSyncResponse is the response for a DNS sync operation.
type DNSSyncResponse struct {
	Results []DNSSyncResult `json:"results"`
	Errors  []string        `json:"errors,omitempty"`
}

func (s *Server) handleDNSSync(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	var req DNSSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid JSON body"))
		return
	}
	if req.IPv4 == "" && req.IPv6 == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("at least one of ipv4 or ipv6 required"))
		return
	}

	// Get DNS provider config from settings
	providerName, _ := state.GetSetting(s.db, "dns_provider")
	if providerName == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("dns_provider not configured; set via 'deploy config set dns_provider <name>'"))
		return
	}
	dnsToken, err := state.EncryptedGetSetting(s.db, "dns_token", s.masterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if dnsToken == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("dns_token not configured"))
		return
	}
	dnsSecret, err := state.EncryptedGetSetting(s.db, "dns_secret", s.masterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	prov, err := dns.Get(providerName, dns.Config{Token: dnsToken, Secret: dnsSecret})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	// Get domains for this app
	domains, err := state.ListDomainsByApp(s.db, app.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	if len(domains) == 0 {
		writeJSON(w, http.StatusOK, DNSSyncResponse{Results: []DNSSyncResult{}})
		return
	}

	var results []DNSSyncResult
	var errs []string

	for _, d := range domains {
		zone, err := dns.ExtractZone(r.Context(), prov, d.Domain)
		if err != nil {
			log.Printf("dns sync %s: %v", d.Domain, err)
			continue
		}
		name := dns.ExtractName(d.Domain, zone)
		if req.IPv4 != "" {
			id, existed, err := prov.EnsureRecord(r.Context(), zone, name, "A", req.IPv4, 300)
			if err != nil {
				log.Printf("DNS sync A record error for %s: %v", d.Domain, err)
				errs = append(errs, fmt.Sprintf("%s A: internal server error", d.Domain))
			} else {
				results = append(results, DNSSyncResult{
					Domain:  d.Domain,
					Type:    "A",
					Value:   req.IPv4,
					Existed: existed,
				})
			}
			_ = id // record ID available if needed
		}
		if req.IPv6 != "" {
			id, existed, err := prov.EnsureRecord(r.Context(), zone, name, "AAAA", req.IPv6, 300)
			if err != nil {
				log.Printf("DNS sync AAAA record error for %s: %v", d.Domain, err)
				errs = append(errs, fmt.Sprintf("%s AAAA: internal server error", d.Domain))
			} else {
				results = append(results, DNSSyncResult{
					Domain:  d.Domain,
					Type:    "AAAA",
					Value:   req.IPv6,
					Existed: existed,
				})
			}
			_ = id
		}
	}

	writeJSON(w, http.StatusOK, DNSSyncResponse{
		Results: results,
		Errors:  errs,
	})
}

func (s *Server) handleDNSList(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	// Get DNS provider config from settings
	providerName, _ := state.GetSetting(s.db, "dns_provider")
	if providerName == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("dns_provider not configured"))
		return
	}
	dnsToken, err := state.EncryptedGetSetting(s.db, "dns_token", s.masterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}
	if dnsToken == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("dns_token not configured"))
		return
	}
	dnsSecret, err := state.EncryptedGetSetting(s.db, "dns_secret", s.masterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	prov, err := dns.Get(providerName, dns.Config{Token: dnsToken, Secret: dnsSecret})
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	// Get domains for this app
	domains, err := state.ListDomainsByApp(s.db, app.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	var allRecords []dns.Record
	for _, d := range domains {
		zone, err := dns.ExtractZone(r.Context(), prov, d.Domain)
		if err != nil {
			log.Printf("warning: list records for %s: %v", d.Domain, err)
			continue
		}
		records, err := prov.ListRecords(r.Context(), zone)
		if err != nil {
			log.Printf("warning: list records for %s: %v", d.Domain, err)
			continue
		}
		allRecords = append(allRecords, records...)
	}

	if allRecords == nil {
		allRecords = []dns.Record{}
	}

	writeJSON(w, http.StatusOK, allRecords)
}

// --- Shutdown ---

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"message": "shutting down"})
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()
}