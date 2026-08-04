// Package runner provides an abstraction over Docker container operations.
package runner

import (
	"context"
	"io"
	"time"

	"deploy/internal/types"
)

// Interface defines the contract for container management.
// All methods accept a context for cancellation and timeouts.
type Interface interface {
	// PullImage ensures the Docker image is available locally.
	PullImage(ctx context.Context, image string) error

	// CreateContainer creates a container but does not start it.
	// Returns the container ID.
	CreateContainer(ctx context.Context, app *types.App, version string) (string, error)

	// StartContainer starts an existing container by ID.
	StartContainer(ctx context.Context, containerID string) error

	// StopContainer stops a running container by ID.
	StopContainer(ctx context.Context, containerID string) error

	// RemoveContainer removes a container by ID.
	RemoveContainer(ctx context.Context, containerID string) error

	// GetContainerLogs returns logs for a container.
	// tail: number of lines from the end (0 for all).
	// follow: stream logs live.
	GetContainerLogs(ctx context.Context, containerID string, tail int, follow bool) (io.ReadCloser, error)

	// InspectContainer returns the container state.
	InspectContainer(ctx context.Context, containerID string) (*ContainerState, error)

	// ListContainers returns all containers managed by deploy.
	ListContainers(ctx context.Context) ([]ContainerInfo, error)

	// HealthCheck verifies the container is running and responds to HTTP
	// health checks. Returns nil when healthy, error otherwise.
	HealthCheck(ctx context.Context, containerID string, port int, path string, initialDelay, interval, timeout time.Duration, retries int) error

	// FindContainerByLabel finds a container by label key=value pair.
	// Returns the container ID or empty string if not found.
	FindContainerByLabel(ctx context.Context, key, value string) (string, error)

	// FindDevContainer finds a dev container by app name (checks deploy.dev=true label).
	FindDevContainer(ctx context.Context, appName string) (string, error)

	// ExecContainer runs cmd non-interactively inside the running container,
	// optionally as user (empty string = container default). It returns a
	// stream of the combined stdout+stderr output, or an error if the
	// container is not running.
	ExecContainer(ctx context.Context, containerID, user string, cmd []string) (*ExecResult, error)

	// GetUsage returns a snapshot of Docker system disk usage plus live
	// per-container CPU/memory stats for all deploy-managed containers.
	GetUsage(ctx context.Context) (types.DockerUsage, error)
}

// ExecResult carries the streamed output of a container exec plus a Wait
// function that resolves the process exit code once the stream is consumed.
type ExecResult struct {
	// Output streams the combined stdout+stderr of the exec process.
	// Consume until EOF, then Close to release the underlying connection.
	Output io.ReadCloser
	// Wait returns the process exit code. Only valid after Output has been
	// fully consumed (EOF reached).
	Wait func() (int, error)
}

// ContainerState holds key container runtime state.
type ContainerState struct {
	ID       string
	Running  bool
	ExitCode int
	Status   string
}

// ContainerInfo holds summary info about a container.
type ContainerInfo struct {
	ID     string
	Name   string
	Image  string
	Status string
	AppID  string
	Port   int
	IsDev  bool `json:"is_dev,omitempty"`
}
