// Package api provides the HTTP API server over Unix socket.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"deploy/internal/caddyfile"
	"deploy/internal/deploy"
	"deploy/internal/runner"
	"deploy/internal/scheduler"
	"deploy/internal/types"
)

// Server holds the API server dependencies.
type Server struct {
	db           *sql.DB
	runner       runner.Interface
	scheduler    *scheduler.Scheduler
	deployer     *deploy.Deployer
	caddyManager *caddyfile.CaddyManager
	httpServer   *http.Server
	mux          http.Handler
	masterKey    []byte
	startedAt    time.Time
	socketPath   string
}

// NewServer creates a new API server.
func NewServer(db *sql.DB, r runner.Interface, sched *scheduler.Scheduler, d *deploy.Deployer, cm *caddyfile.CaddyManager, socketPath string, masterKey []byte) *Server {
	s := &Server{
		db:           db,
		runner:       r,
		scheduler:    sched,
		deployer:     d,
		caddyManager: cm,
		masterKey:    masterKey,
		startedAt:    time.Now(),
		socketPath:   socketPath,
	}
	s.registerRoutes()
	s.reconcileDevContainers()
	return s
}

// reconcileDevContainers scans Docker for existing dev containers and rebuilds the sync.Map.
func (s *Server) reconcileDevContainers() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containers, err := s.runner.ListContainers(ctx)
	if err != nil {
		log.Printf("dev reconcile: list containers: %v", err)
		return
	}

	count := 0
	for _, c := range containers {
		if c.IsDev {
			trackDevContainer(c.AppID, c.ID)
			count++
		}
	}
	if count > 0 {
		log.Printf("dev reconcile: tracked %d existing dev container(s)", count)
	}
}

// ListenAndServe starts the Unix socket listener.
func (s *Server) ListenAndServe() error {
	if err := os.Remove(s.socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old socket: %w", err)
	}

	// Ensure socket directory exists
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0770); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.socketPath, err)
	}

	if err := os.Chmod(s.socketPath, 0770); err != nil {
		listener.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	s.httpServer = &http.Server{Handler: s.mux}
	log.Printf("API server listening on %s", s.socketPath)
	return s.httpServer.Serve(listener)
}

// registerRoutes sets up the HTTP router with middleware.
func (s *Server) registerRoutes() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// Usage
	mux.HandleFunc("GET /api/v1/usage", s.handleUsage)

	// Shutdown
	mux.HandleFunc("POST /api/v1/shutdown", s.handleShutdown)

	mux.HandleFunc("POST /api/v1/apps", s.handleCreateApp)
	mux.HandleFunc("GET /api/v1/apps", s.handleListApps)
	mux.HandleFunc("GET /api/v1/apps/{name}", s.handleGetApp)
	mux.HandleFunc("DELETE /api/v1/apps/{name}", s.handleDeleteApp)

	mux.HandleFunc("POST /api/v1/apps/{name}/start", s.handleStartApp)
	mux.HandleFunc("POST /api/v1/apps/{name}/stop", s.handleStopApp)
	mux.HandleFunc("GET /api/v1/apps/{name}/logs", s.handleGetLogs)
	mux.HandleFunc("POST /api/v1/apps/{name}/exec", s.handleExec)

	mux.HandleFunc("GET /api/v1/jobs/{id}", s.handleGetJob)

	// Phase 2: deploy commands
	mux.HandleFunc("POST /api/v1/apps/{name}/promote", s.handlePromote)
	mux.HandleFunc("POST /api/v1/apps/{name}/rollback", s.handleRollback)
	mux.HandleFunc("GET /api/v1/apps/{name}/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/apps/{name}/images", s.handleListImages)
	mux.HandleFunc("DELETE /api/v1/apps/{name}/images/{version}", s.handleRemoveImage)
	mux.HandleFunc("POST /api/v1/apps/{name}/secrets", s.handleSetSecret)
	mux.HandleFunc("GET /api/v1/apps/{name}/secrets", s.handleListSecrets)
	mux.HandleFunc("GET /api/v1/apps/{name}/secrets/{key}", s.handleGetSecret)
	mux.HandleFunc("DELETE /api/v1/apps/{name}/secrets/{key}", s.handleRemoveSecret)
	mux.HandleFunc("GET /api/v1/status", s.handleGlobalStatus)

	// Phase 8: prune old image tarballs
	mux.HandleFunc("POST /api/v1/prune", s.handlePrune)

	// Phase 3: domain management
	mux.HandleFunc("POST /api/v1/apps/{name}/domains", s.handleAddDomain)
	mux.HandleFunc("GET /api/v1/apps/{name}/domains", s.handleListDomains)
	mux.HandleFunc("GET /api/v1/domains", s.handleListDomains)
	mux.HandleFunc("DELETE /api/v1/apps/{name}/domains/{domain}", s.handleRemoveDomain)

	// Phase 4: clean teardown
	mux.HandleFunc("POST /api/v1/apps/{name}/remove", s.handleRemoveApp)

	// Phase 4: config/settings
	mux.HandleFunc("GET /api/v1/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/v1/config", s.handleSetConfig)

	// Phase 6: backup
	mux.HandleFunc("POST /api/v1/backup", s.handleBackup)
	mux.HandleFunc("POST /api/v1/backup/{name}", s.handleAppBackup)
	mux.HandleFunc("POST /api/v1/apps/{name}/restore", s.handleAppRestore)

	// Phase 7: dev containers
	mux.HandleFunc("POST /api/v1/apps/{name}/dev/start", s.handleDevStart)
	mux.HandleFunc("POST /api/v1/apps/{name}/dev/stop", s.handleDevStop)

	s.mux = panicRecoveryMiddleware(loggingMiddleware(mux))
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("error encoding JSON response: %v", err)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, errResp types.ErrorResponse) {
	writeJSON(w, status, errResp)
}

// Shutdown gracefully stops the HTTP server, draining in-flight requests.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}
