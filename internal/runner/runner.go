package runner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"deploy/internal/types"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// DockerRunner implements Interface using the official Docker SDK.
type DockerRunner struct {
	cli *client.Client
}

// NewDockerRunner creates a new DockerRunner.
func NewDockerRunner() (*DockerRunner, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &DockerRunner{cli: cli}, nil
}

// containerName generates a unique container name with random suffix.
func containerName(appID string) string {
	return fmt.Sprintf("deploy-%s", appID[:8])
}

// buildContainerLabels returns the standard labels for deploy-managed containers.
func buildContainerLabels(app *types.App) map[string]string {
	return map[string]string{
		"deploy.managed":  "true",
		"deploy.app.id":   app.ID,
		"deploy.app.name": app.Name,
	}
}

// PullImage ensures the Docker image is available locally.
func (d *DockerRunner) PullImage(ctx context.Context, imageStr string) error {
	reader, err := d.cli.ImagePull(ctx, imageStr, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", imageStr, err)
	}
	defer reader.Close()
	io.Copy(io.Discard, reader)
	return nil
}

// CreateContainer creates a container but does not start it.
func (d *DockerRunner) CreateContainer(ctx context.Context, app *types.App) (string, error) {
	portStr := fmt.Sprintf("%d/tcp", app.Port)
	np := network.MustParsePort(portStr)

	exposedPorts := network.PortSet{
		np: struct{}{},
	}
	portBindings := network.PortMap{
		np: []network.PortBinding{
			{
				HostIP:   netip.MustParseAddr("0.0.0.0"),
				HostPort: fmt.Sprintf("%d", app.Port),
			},
		},
	}

	envVars := make([]string, 0, len(app.Env))
	for k, v := range app.Env {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	cfg := &container.Config{
		Image:        app.Image,
		ExposedPorts: exposedPorts,
		Env:          envVars,
		Labels:       buildContainerLabels(app),
	}

	hostCfg := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
	}

	opts := client.ContainerCreateOptions{
		Config:     cfg,
		HostConfig: hostCfg,
		Name:       fmt.Sprintf("deploy-%s", app.ID[:8]),
	}

	resp, err := d.cli.ContainerCreate(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	return resp.ID, nil
}

// StartContainer starts an existing container by ID.
func (d *DockerRunner) StartContainer(ctx context.Context, containerID string) error {
	_, err := d.cli.ContainerStart(ctx, containerID, client.ContainerStartOptions{})
	if err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

// StopContainer stops a running container by ID.
func (d *DockerRunner) StopContainer(ctx context.Context, containerID string) error {
	timeout := 30
	opts := client.ContainerStopOptions{Timeout: &timeout}
	_, err := d.cli.ContainerStop(ctx, containerID, opts)
	if err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// RemoveContainer removes a container by ID.
func (d *DockerRunner) RemoveContainer(ctx context.Context, containerID string) error {
	_, err := d.cli.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true})
	if err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// GetContainerLogs returns logs for a container.
func (d *DockerRunner) GetContainerLogs(ctx context.Context, containerID string, tail int, follow bool) (io.ReadCloser, error) {
	opts := client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
	}
	if tail > 0 {
		opts.Tail = fmt.Sprintf("%d", tail)
	} else {
		opts.Tail = "all"
	}
	reader, err := d.cli.ContainerLogs(ctx, containerID, opts)
	if err != nil {
		return nil, fmt.Errorf("get container logs: %w", err)
	}
	return reader, nil
}

// InspectContainer returns the container state.
func (d *DockerRunner) InspectContainer(ctx context.Context, containerID string) (*ContainerState, error) {
	result, err := d.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}

	return &ContainerState{
		ID:       result.Container.ID,
		Running:  result.Container.State.Running,
		ExitCode: result.Container.State.ExitCode,
		Status:   string(result.Container.State.Status),
	}, nil
}

// ListContainers returns all deploy-managed containers.
func (d *DockerRunner) ListContainers(ctx context.Context) ([]ContainerInfo, error) {
	f := make(client.Filters)
	f.Add("label", "deploy.managed=true")

	result, err := d.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	infos := make([]ContainerInfo, 0, len(result.Items))
	for _, c := range result.Items {
		info := ContainerInfo{
			ID:     c.ID[:12],
			Image:  c.Image,
			Status: c.Status,
		}
		if len(c.Names) > 0 {
			info.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		if appID, ok := c.Labels["deploy.app.id"]; ok {
			info.AppID = appID
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// FindContainerByLabel finds a container by a label key=value pair.
// Returns the container ID or empty string if not found.
func (d *DockerRunner) FindContainerByLabel(ctx context.Context, key, value string) (string, error) {
	f := make(client.Filters)
	f.Add("label", key+"="+value)

	containers, err := d.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return "", err
	}
	if len(containers.Items) == 0 {
		return "", nil
	}
	return containers.Items[0].ID, nil
}

// HealthCheck verifies the container is running and responds to HTTP health
// checks. It waits for initialDelay, checks the container is running via
// Docker, then performs HTTP GET requests with the given interval and timeout
// up to retries times. Returns nil when healthy, error otherwise.
func (d *DockerRunner) HealthCheck(ctx context.Context, containerID string, port int, path string, initialDelay, interval, timeout time.Duration, retries int) error {
	select {
	case <-time.After(initialDelay):
	case <-ctx.Done():
		return ctx.Err()
	}

	// 1. Check Docker state
	state, err := d.InspectContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect container: %w", err)
	}
	if !state.Running {
		id := containerID
		if len(id) > 12 {
			id = id[:12]
		}
		return fmt.Errorf("container %s is not running", id)
	}

	// 2. HTTP health check
	httpClient := &http.Client{Timeout: timeout}
	url := fmt.Sprintf("http://localhost:%d%s", port, path)

	for i := 0; i < retries; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("create health check request: %w", err)
		}

		resp, err := httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			}
		}

		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf("health check failed after %d retries", retries)
}
