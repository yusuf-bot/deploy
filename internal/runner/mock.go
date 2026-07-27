package runner

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"deploy/internal/types"
)

// MockDocker implements Interface for testing.
type MockDocker struct {
	Mu           sync.Mutex
	Containers   map[string]*mockContainer
	ShouldFail   map[string]error // keyed by "CreateContainer", "StartContainer", etc.
	CreatedCount int
}

type mockContainer struct {
	ID       string
	App      *mockAppData
	Running  bool
	Exited   bool
	ExitCode int
	Logs     string
}

type mockAppData struct {
	Name  string
	Image string
	Port  int
	Env   map[string]string
	Dev   bool
	Version string
}

// NewMockDocker creates a new MockDocker.
func NewMockDocker() *MockDocker {
	return &MockDocker{
		Containers: make(map[string]*mockContainer),
		ShouldFail: make(map[string]error),
	}
}

func (m *MockDocker) shouldFail(op string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if err, ok := m.ShouldFail[op]; ok {
		return err
	}
	return nil
}

// PullImage implements Interface.
func (m *MockDocker) PullImage(ctx context.Context, image string) error {
	if err := m.shouldFail("PullImage"); err != nil {
		return err
	}
	if image == "" {
		return fmt.Errorf("image required")
	}
	if image == "nonexistent:latest" {
		return fmt.Errorf("image not found")
	}
	return nil
}

// CreateContainer implements Interface.
func (m *MockDocker) CreateContainer(ctx context.Context, app *types.App, version string) (string, error) {
	if err := m.shouldFail("CreateContainer"); err != nil {
		return "", err
	}

	m.Mu.Lock()
	defer m.Mu.Unlock()

	m.CreatedCount++
	id := fmt.Sprintf("container-%s-%d", app.Name, m.CreatedCount)

	m.Containers[id] = &mockContainer{
		ID: id,
		App: &mockAppData{
			Name:  app.Name,
			Image: app.Image,
			Port:  app.Port,
			Env:   app.Env,
			Dev:   app.Dev,
			Version: version,
		},
		Running: false,
		Logs:    "",
	}
	return id, nil
}

// StartContainer implements Interface.
func (m *MockDocker) StartContainer(ctx context.Context, containerID string) error {
	if err := m.shouldFail("StartContainer"); err != nil {
		return err
	}

	m.Mu.Lock()
	defer m.Mu.Unlock()

	c, ok := m.Containers[containerID]
	if !ok {
		return fmt.Errorf("container %s not found", containerID)
	}
	c.Running = true
	return nil
}

// StopContainer implements Interface.
func (m *MockDocker) StopContainer(ctx context.Context, containerID string) error {
	if err := m.shouldFail("StopContainer"); err != nil {
		return err
	}

	m.Mu.Lock()
	defer m.Mu.Unlock()

	c, ok := m.Containers[containerID]
	if !ok {
		return fmt.Errorf("container %s not found", containerID)
	}
	c.Running = false
	c.Exited = true
	c.ExitCode = 0
	return nil
}

// RemoveContainer implements Interface.
func (m *MockDocker) RemoveContainer(ctx context.Context, containerID string) error {
	if err := m.shouldFail("RemoveContainer"); err != nil {
		return err
	}

	m.Mu.Lock()
	defer m.Mu.Unlock()

	if _, ok := m.Containers[containerID]; !ok {
		return fmt.Errorf("container %s not found", containerID)
	}
	delete(m.Containers, containerID)
	return nil
}

// GetContainerLogs implements Interface.
func (m *MockDocker) GetContainerLogs(ctx context.Context, containerID string, tail int, follow bool) (io.ReadCloser, error) {
	if err := m.shouldFail("GetContainerLogs"); err != nil {
		return nil, err
	}

	m.Mu.Lock()
	c, ok := m.Containers[containerID]
	m.Mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("container %s not found", containerID)
	}

	logStr := c.Logs
	if logStr == "" {
		logStr = "container startup log\n"
	}

	if tail > 0 {
		lines := strings.Split(strings.TrimSuffix(logStr, "\n"), "\n")
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		logStr = strings.Join(lines, "\n") + "\n"
	}

	return io.NopCloser(strings.NewReader(logStr)), nil
}

// InspectContainer implements Interface.
func (m *MockDocker) InspectContainer(ctx context.Context, containerID string) (*ContainerState, error) {
	if err := m.shouldFail("InspectContainer"); err != nil {
		return nil, err
	}

	m.Mu.Lock()
	defer m.Mu.Unlock()

	c, ok := m.Containers[containerID]
	if !ok {
		return nil, fmt.Errorf("container %s not found", containerID)
	}

	status := "exited"
	if c.Running {
		status = "running"
	}

	return &ContainerState{
		ID:       c.ID,
		Running:  c.Running,
		ExitCode: c.ExitCode,
		Status:   status,
	}, nil
}

// ListContainers implements Interface.
func (m *MockDocker) ListContainers(ctx context.Context) ([]ContainerInfo, error) {
	if err := m.shouldFail("ListContainers"); err != nil {
		return nil, err
	}

	m.Mu.Lock()
	defer m.Mu.Unlock()

	var result []ContainerInfo
	for id, c := range m.Containers {
		status := "exited"
		if c.Running {
			status = "running"
		}
		result = append(result, ContainerInfo{
			ID:     id,
			Name:   fmt.Sprintf("deploy-%s-%s", c.App.Name, c.App.Version),
			Image:  c.App.Image,
			Status: status,
			AppID:  c.App.Name,
			Port:   c.App.Port,
			IsDev:  c.App.Dev,
		})
	}
	return result, nil
}

// HealthCheck implements Interface. It checks the container is running
// in the mock state (skip HTTP check in tests).
func (m *MockDocker) HealthCheck(ctx context.Context, containerID string, port int, path string, initialDelay, interval, timeout time.Duration, retries int) error {
	if err := m.shouldFail("HealthCheck"); err != nil {
		return err
	}

	select {
	case <-time.After(initialDelay):
	case <-ctx.Done():
		return ctx.Err()
	}

	// Verify container exists and is running
	state, err := m.InspectContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect container: %w", err)
	}
	if !state.Running {
		return fmt.Errorf("container %s is not running", state.ID[:12])
	}

	return nil
}

// FindContainerByLabel implements Interface. Returns empty string if not found.
func (m *MockDocker) FindDevContainer(ctx context.Context, appName string) (string, error) {
	if err := m.shouldFail("FindDevContainer"); err != nil {
		return "", err
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	for _, c := range m.Containers {
		if c.App != nil && c.App.Name == appName && c.App.Dev {
			return c.ID, nil
		}
	}
	return "", nil
}

func (m *MockDocker) FindContainerByLabel(ctx context.Context, key, value string) (string, error) {
	if err := m.shouldFail("FindContainerByLabel"); err != nil {
		return "", err
	}
	// Check containers for matching app name label
	m.Mu.Lock()
	defer m.Mu.Unlock()
	for _, c := range m.Containers {
		if c.App != nil && key == "deploy.app.name" && c.App.Name == value {
			return c.ID, nil
		}
	}
	return "", nil
}
