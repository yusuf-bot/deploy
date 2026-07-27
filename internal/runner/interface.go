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
	IsDev  bool   `json:"is_dev,omitempty"`
}
