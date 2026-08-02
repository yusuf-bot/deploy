package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"deploy/internal/build"
	"deploy/internal/state"
	"deploy/internal/types"
)

// handlePrune deletes old image tarballs, keeping the newest N per app.
// The tarball of the currently-running version is never deleted.
// POST /api/v1/prune
func (s *Server) handlePrune(w http.ResponseWriter, r *http.Request) {
	var req types.PruneRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, BadRequestError("invalid JSON body"))
			return
		}
	}

	keep := req.Keep
	if keep == 0 {
		keep = 3
	}
	if keep < 1 {
		writeError(w, http.StatusBadRequest, BadRequestError("keep must be at least 1"))
		return
	}

	if req.App != "" && !validAppName.MatchString(req.App) {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid app name"))
		return
	}

	var apps []types.App
	var err error
	if req.App != "" {
		app, err := state.GetAppByName(s.db, req.App)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
			return
		}
		if app == nil {
			writeError(w, http.StatusNotFound, NotFoundError("app"))
			return
		}
		apps = []types.App{*app}
	} else {
		apps, err = state.ListApps(s.db, "")
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
			return
		}
	}

	resp := types.PruneResponse{
		Apps:   []types.PruneAppResult{},
		DryRun: req.DryRun,
	}

	for _, app := range apps {
		versions, err := build.ListTarballs(app.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
			return
		}
		if len(versions) == 0 {
			continue
		}

		protected := s.runningTarballVersions(app, versions)
		result, err := build.PruneTarballs(app.Name, keep, protected, req.DryRun)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
			return
		}

		resp.Apps = append(resp.Apps, toPruneAppResult(app.Name, result))
		resp.TotalFreedBytes += result.FreedBytes
	}

	writeJSON(w, http.StatusOK, resp)
}

// runningTarballVersions returns the set of tarball versions that back the
// currently-running deployment of an app. These must never be pruned.
func (s *Server) runningTarballVersions(app types.App, tarballVersions []string) map[string]bool {
	protected := map[string]bool{}

	active, err := state.GetActiveDeployment(s.db, app.ID)
	if err == nil && active != nil && active.Version != "" {
		protected[active.Version] = true
	}

	// Fallback: if the app's own image tag resolves to a saved tarball
	// (e.g. a manual start reused a saved version), protect it too.
	if app.Image != "" {
		_, tag, ok := strings.Cut(app.Image, ":")
		if ok && tag != "" {
			for _, v := range tarballVersions {
				if v == tag {
					protected[v] = true
					break
				}
			}
		}
	}
	return protected
}

// toPruneAppResult converts a build.PruneResult into the API response shape.
func toPruneAppResult(appName string, res *build.PruneResult) types.PruneAppResult {
	toFiles := func(infos []build.TarballInfo) []types.PruneFile {
		files := make([]types.PruneFile, 0, len(infos))
		for _, i := range infos {
			files = append(files, types.PruneFile{Version: i.Version, SizeBytes: i.SizeBytes})
		}
		return files
	}
	return types.PruneAppResult{
		App:        appName,
		Removed:    toFiles(res.Removed),
		Kept:       toFiles(res.Kept),
		Protected:  toFiles(res.Protected),
		FreedBytes: res.FreedBytes,
	}
}

