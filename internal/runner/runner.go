package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"deploy/internal/types"

	"github.com/docker/go-units"
	"github.com/moby/moby/api/pkg/stdcopy"
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

// containerName generates a unique container name scoped by app ID and version.
func containerName(appID, version string) string {
	return fmt.Sprintf("deploy-%s-%s", appID[:8], version)
}

// buildContainerLabels returns the standard labels for deploy-managed containers.
func buildContainerLabels(app *types.App) map[string]string {
	labels := map[string]string{
		"deploy.managed":  "true",
		"deploy.app.id":   app.ID,
		"deploy.app.name": app.Name,
	}
	if app.Dev {
		labels["deploy.dev"] = "true"
	}
	return labels
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
func (d *DockerRunner) CreateContainer(ctx context.Context, app *types.App, version string) (string, error) {
	// Use ServicePort as container port if set, otherwise same as host port
	containerPort := app.Port
	if app.ServicePort > 0 {
		containerPort = app.ServicePort
	}
	portStr := fmt.Sprintf("%d/tcp", containerPort)
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
	if app.Command != "" {
		cfg.Cmd = []string{"sh", "-c", app.Command}
	}

	hostCfg := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
	}
	// Optional docker network (deploy.yml `network:`). When absent the
	// default bridge is used, matching previous behavior.
	if app.Network != "" {
		hostCfg.NetworkMode = container.NetworkMode(app.Network)
	}

	// Apply resource limits from app config
	if app.Resources != nil {
		if app.Resources.Memory != "" {
			if memBytes, err := units.RAMInBytes(app.Resources.Memory); err == nil {
				hostCfg.Memory = memBytes
			}
		}
		if app.Resources.CPUs != "" {
			if cpuFloat, err := strconv.ParseFloat(app.Resources.CPUs, 64); err == nil {
				hostCfg.NanoCPUs = int64(cpuFloat * 1e9)
			}
		}
	}

	if len(app.Volumes) > 0 {
		binds := make([]string, 0, len(app.Volumes))
		for _, v := range app.Volumes {
			binds = append(binds, v.Source+":"+v.Target)
		}
		hostCfg.Binds = binds
	}

	name := containerName(app.ID, version)
	// Remove any zombie container with the same name (best-effort)
	_, _ = d.cli.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})

	opts := client.ContainerCreateOptions{
		Config:     cfg,
		HostConfig: hostCfg,
		Name:       name,
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
		if _, ok := c.Labels["deploy.dev"]; ok {
			info.IsDev = true
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// FindContainerByLabel finds a container by a label key=value pair.
// Returns the container ID or empty string if not found.
func (d *DockerRunner) FindDevContainer(ctx context.Context, appName string) (string, error) {
	f := make(client.Filters)
	f.Add("label", "deploy.app.name="+appName)
	f.Add("label", "deploy.dev=true")
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
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)

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

// ExecContainer implements Interface using the Docker SDK exec API.
func (d *DockerRunner) ExecContainer(ctx context.Context, containerID, user string, cmd []string) (*ExecResult, error) {
	if containerID == "" {
		return nil, fmt.Errorf("exec: container ID is required")
	}
	if len(cmd) == 0 {
		return nil, fmt.Errorf("exec: command is required")
	}

	createResp, err := d.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		User:         user,
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create exec: %w", err)
	}

	attachResp, err := d.cli.ExecAttach(ctx, createResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("attach exec: %w", err)
	}

	// Demultiplex the multiplexed stdout+stderr stream into one combined
	// output. The hijacked connection is closed when the returned reader is
	// closed, which also unblocks this goroutine on client disconnect.
	pr, pw := io.Pipe()
	go func() {
		_, cpErr := stdcopy.StdCopy(pw, pw, attachResp.Reader)
		if cpErr != nil {
			pw.CloseWithError(cpErr)
			return
		}
		pw.Close()
	}()

	var (
		once    sync.Once
		code    int
		waitErr error
	)
	wait := func() (int, error) {
		once.Do(func() {
			code, waitErr = d.execExitCode(ctx, createResp.ID)
		})
		return code, waitErr
	}

	return &ExecResult{
		Output: &execOutput{pr: pr, closeFn: attachResp.Close},
		Wait:   wait,
	}, nil
}

// execOutput adapts the demultiplexed pipe so closing it also closes the
// hijacked exec connection.
type execOutput struct {
	pr      *io.PipeReader
	closeFn func()
}

func (e *execOutput) Read(p []byte) (int, error) { return e.pr.Read(p) }
func (e *execOutput) Close() error {
	e.closeFn()
	return e.pr.Close()
}

// execExitCode resolves an exec process's exit code, polling briefly for the
// exec to transition from running to exited.
func (d *DockerRunner) execExitCode(ctx context.Context, execID string) (int, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, err := d.cli.ExecInspect(ctx, execID, client.ExecInspectOptions{})
		if err != nil {
			return 0, fmt.Errorf("inspect exec: %w", err)
		}
		if !info.Running {
			return info.ExitCode, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("exec %s still running", execID)
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// containerCPUStats holds a computed CPU% and memory usage for a container.
type containerCPUStats struct {
	cpuPct   float64
	memUsage uint64
	memLimit uint64
}

// containerStatsOnce fetches a single stats sample for a running container and
// computes a CPU percentage from the previous-sample delta (the daemon takes
// two samples ~1s apart when IncludePreviousSample is set).
func (d *DockerRunner) containerStatsOnce(ctx context.Context, containerID string) (*containerCPUStats, error) {
	resp, err := d.cli.ContainerStats(ctx, containerID, client.ContainerStatsOptions{
		Stream:               false,
		IncludePreviousSample: true,
	})
	if err != nil {
		return nil, fmt.Errorf("container stats: %w", err)
	}
	defer resp.Body.Close()

	var s container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode container stats: %w", err)
	}

	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage - s.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(s.CPUStats.SystemUsage - s.PreCPUStats.SystemUsage)
	onlineCPUs := s.CPUStats.OnlineCPUs
	if onlineCPUs == 0 {
		onlineCPUs = 1
	}
	cpuPct := 0.0
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPct = (cpuDelta / systemDelta) * float64(onlineCPUs) * 100.0
	}

	return &containerCPUStats{
		cpuPct:   cpuPct,
		memUsage: s.MemoryStats.Usage,
		memLimit: s.MemoryStats.Limit,
	}, nil
}

// GetUsage implements Interface. It returns docker system df totals plus live
// CPU/memory stats for every deploy-managed container.
func (d *DockerRunner) GetUsage(ctx context.Context) (types.DockerUsage, error) {
	usage := types.DockerUsage{}

	du, err := d.cli.DiskUsage(ctx, client.DiskUsageOptions{
		Containers: true,
		Images:     true,
		Volumes:    true,
		BuildCache: true,
	})
	if err != nil {
		return usage, fmt.Errorf("disk usage: %w", err)
	}
	usage.System = types.SystemUsage{
		ImagesTotalBytes:           du.Images.TotalSize,
		ContainersTotalBytes:       du.Containers.TotalSize,
		VolumesTotalBytes:          du.Volumes.TotalSize,
		BuildCacheTotalBytes:       du.BuildCache.TotalSize,
		ImagesReclaimableBytes:     du.Images.Reclaimable,
		ContainersReclaimableBytes: du.Containers.Reclaimable,
		VolumesReclaimableBytes:    du.Volumes.Reclaimable,
		BuildCacheReclaimableBytes: du.BuildCache.Reclaimable,
		ImagesTotalCount:           du.Images.TotalCount,
		ContainersTotalCount:       du.Containers.TotalCount,
		VolumesTotalCount:          du.Volumes.TotalCount,
		BuildCacheTotalCount:       du.BuildCache.TotalCount,
	}

	f := make(client.Filters)
	f.Add("label", "deploy.managed=true")
	containers, err := d.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return usage, fmt.Errorf("list containers: %w", err)
	}

	for _, c := range containers.Items {
		cu := types.ContainerUsage{
			AppID:     c.Labels["deploy.app.id"],
			Container: c.ID,
			Running:   c.State == "running",
		}
		if c.State == "running" {
			if stats, statsErr := d.containerStatsOnce(ctx, c.ID); statsErr == nil {
				cu.CPUPct = stats.cpuPct
				cu.MemBytes = stats.memUsage
				cu.MemLimit = stats.memLimit
			}
		}
		usage.Containers = append(usage.Containers, cu)
	}

	return usage, nil
}
