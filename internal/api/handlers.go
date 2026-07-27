package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"deploy/internal/config"
	"deploy/internal/build"
	"deploy/internal/state"
	"deploy/internal/types"

	"github.com/google/uuid"
)

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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// --- List Apps ---

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")

	apps, err := state.ListApps(s.db, statusFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if app.Status == types.StatusRunning || app.Status == types.StatusUnknown {
		writeError(w, http.StatusConflict, ErrorBody(types.ErrAppRunning,
			fmt.Sprintf("app %q is running; stop it first", name)))
		return
	}

	if app.ContainerID != "" {
		if err := s.runner.RemoveContainer(r.Context(), app.ContainerID); err != nil {
			log.Printf("warning: remove container %s: %v", app.ContainerID, err)
		}
	}

	if err := state.DeleteApp(s.db, name); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	// 1. Check deploy lock to prevent races with concurrent deploys
	if s.deployer != nil && s.deployer.IsLocked(app.ID) {
		writeError(w, http.StatusConflict, ErrorBody(types.ErrAppRunning, "deploy in progress for this app, cannot remove"))
		return
	}

	// 2. Get domains before DB delete (for Caddy cleanup)
	domains, _ := state.ListDomainsByApp(s.db, app.ID)

	// 3. Delete app record from DB FIRST — if this fails, nothing is lost
	// ponytail: DB delete first, cleanup is best-effort
	if err := state.DeleteApp(s.db, name); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrDocker, err.Error()))
		return
	}

	logs := s.getContainerLogs(r.Context(), app.ContainerID)
	if logs != "" {
		// Check health
		_ = s.healthCheckContainer(r.Context(), app.ContainerID)
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

	containerID, err := s.runner.CreateContainer(ctx, app)
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
		return fmt.Sprintf("(failed to get logs: %v)", err)
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if app.Status != types.StatusRunning && app.Status != types.StatusUnknown {
		writeError(w, http.StatusConflict, ErrorBody(types.ErrAppNotRunning,
			fmt.Sprintf("app %q is not running", name)))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrDocker, err.Error()))
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

// --- Get Logs ---

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	app, err := state.GetAppByName(s.db, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrDocker, err.Error()))
		return
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, "streaming not supported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	reader, err := s.runner.GetContainerLogs(r.Context(), containerID, tail, true)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %v\n\n", err)
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
					fmt.Fprintf(w, "event: error\ndata: %v\n\n", err)
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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

	wait := r.URL.Query().Get("wait") != "false"

	if !wait {
		// Async: run via scheduler
		jobResult := s.scheduler.StartJob("promote", name, func(ctx context.Context) (string, error) {
			if s.deployer == nil {
				return "", fmt.Errorf("deployer not available")
			}
			resp, err := s.deployer.Promote(ctx, &types.PromoteRequest{}, name, req.Dir)
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, "deployer not available"))
		return
	}

	resp, err := s.deployer.Promote(r.Context(), &types.PromoteRequest{}, name, req.Dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrDocker, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, "deployer not available"))
		return
	}

	resp, err := s.deployer.Rollback(r.Context(), name, req.Version, ".")
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrDocker, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, "deployer not available"))
		return
	}

	resp, err := s.deployer.Status(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Global Status ---

func (s *Server) handleGlobalStatus(w http.ResponseWriter, r *http.Request) {
	apps, err := state.ListApps(s.db, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}

	var summaries []types.AppStatusSummary
	for _, app := range apps {
		activeDep, _ := state.GetActiveDeployment(s.db, app.ID)
		inProgress := false
		if s.deployer != nil {
			inProgress = s.deployer.IsLocked(app.ID)
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

	images, err := build.ListTarballs(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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

	if err := build.RemoveTarball(name, version); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	secrets, err := state.ListSecrets(s.db, app.ID, s.masterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	secret, err := state.GetSecret(s.db, app.ID, key, s.masterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if err := state.DeleteSecret(s.db, app.ID, key); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
			writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
			return
		}
		if app == nil {
			writeError(w, http.StatusNotFound, NotFoundError("app"))
			return
		}

		domains, err := state.ListDomainsByApp(s.db, app.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, NotFoundError("app"))
		return
	}

	if err := state.DeleteDomainByDomain(s.db, domainName); err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
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
	settings, err := state.GetAllSettings(s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
		return
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
	if len(req) != 1 {
		writeError(w, http.StatusBadRequest, BadRequestError("exactly one key=value pair required"))
		return
	}
	for k, v := range req {
		if err := state.SetSetting(s.db, k, v); err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(types.ErrInternal, err.Error()))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "settings updated"})
}
