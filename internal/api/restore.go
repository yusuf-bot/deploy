package api

import (
	"encoding/json"
	"net/http"

	"deploy/internal/types"
)

// handleAppRestore restores a single app from a per-app backup archive while
// the daemon is running. POST /api/v1/apps/{name}/restore
func (s *Server) handleAppRestore(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || !validAppName.MatchString(name) {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid app name"))
		return
	}

	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid JSON body"))
		return
	}
	if req.File == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("file is required"))
		return
	}

	if s.deployer == nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(&types.SystemError{Code: types.ErrInternal, Message: "deployer not available"}))
		return
	}

	resp, err := s.deployer.RestoreApp(r.Context(), req.File, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrorBody(systemError(err)))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
