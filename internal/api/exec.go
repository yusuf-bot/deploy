package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"deploy/internal/state"
	"deploy/internal/types"
)

// execRequest is the JSON body for POST /api/v1/apps/{name}/exec.
type execRequest struct {
	Cmd  []string `json:"cmd"`
	User string   `json:"user,omitempty"`
}

// handleExec runs a non-interactive command inside an app's running container
// via docker exec and streams the combined stdout+stderr output to the client
// as an SSE stream. The stream ends with an exit event carrying the process
// exit code:
//
//	event: exit
//	data: 0
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, BadRequestError("name required"))
		return
	}

	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, BadRequestError("invalid JSON body"))
		return
	}
	if len(req.Cmd) == 0 {
		writeError(w, http.StatusBadRequest, BadRequestError("cmd is required"))
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
	if app.ContainerID == "" || app.Status != types.StatusRunning {
		writeError(w, http.StatusConflict, ErrorBody(appNotRunningError(fmt.Sprintf("app %q is not running", name))))
		return
	}

	result, err := s.runner.ExecContainer(r.Context(), app.ContainerID, req.User, req.Cmd)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "not found") {
			writeError(w, http.StatusNotFound, ErrorBody(notFoundAsError(errStr)))
		} else {
			writeError(w, http.StatusInternalServerError, ErrorBody(dockerError(errStr)))
		}
		return
	}
	defer result.Output.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, ErrorBody(&types.SystemError{Code: types.ErrInternal, Message: "streaming not supported"}))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// If the client disconnects, stop the exec and release the stream.
	go func() {
		<-r.Context().Done()
		result.Output.Close()
	}()

	scanner := bufio.NewScanner(result.Output)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		if err != io.ErrClosedPipe {
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
			flusher.Flush()
		}
		return
	}

	code, err := result.Wait()
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	fmt.Fprintf(w, "event: exit\ndata: %d\n\n", code)
	flusher.Flush()
}
