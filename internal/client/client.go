// Package client provides a thin HTTP client for the deploy daemon over Unix socket.
package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"deploy/internal/types"
)

// Client communicates with the deploy daemon over a Unix socket.
type Client struct {
	socketPath string
	http       http.Client
}

// New creates a new client connected to the given Unix socket.
func New(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		http: http.Client{
			Timeout: 20 * time.Minute,
			Transport: &http.Transport{
				Dial: func(network, addr string) (net.Conn, error) {
					return net.DialTimeout("unix", socketPath, 5*time.Second)
				},
			},
		},
	}
}

// doRequest performs an HTTP request and decodes the JSON response.
func (c *Client) doRequest(method, path string, body interface{}, result interface{}) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, "http://unix"+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp types.ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Code != "" {
			return fmt.Errorf("%s: %s", errResp.Code, errResp.Detail)
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

// Health checks if the daemon is running.
func (c *Client) Health() (*types.HealthResponse, error) {
	var result types.HealthResponse
	if err := c.doRequest("GET", "/api/v1/health", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateApp creates a new app.
func (c *Client) CreateApp(req types.CreateAppRequest) (*types.App, error) {
	var result types.App
	if err := c.doRequest("POST", "/api/v1/apps", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListApps lists all apps, optionally filtered by status.
func (c *Client) ListApps(status string) ([]types.App, error) {
	path := "/api/v1/apps"
	if status != "" {
		path += "?status=" + status
	}
	var result types.ListAppsResponse
	if err := c.doRequest("GET", path, nil, &result); err != nil {
		return nil, err
	}
	return result.Apps, nil
}

// GetApp gets an app by name.
func (c *Client) GetApp(name string) (*types.App, error) {
	var result types.App
	if err := c.doRequest("GET", "/api/v1/apps/"+name, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteApp deletes an app by name.
func (c *Client) DeleteApp(name string) error {
	return c.doRequest("DELETE", "/api/v1/apps/"+name, nil, nil)
}

// StartApp starts an app.
func (c *Client) StartApp(name string, async bool) (*types.StartStopResponse, error) {
	path := "/api/v1/apps/" + name + "/start"
	params := ""
	if async {
		if params != "" {
			params += "&"
		}
		params += "async=true"
	}
	if params != "" {
		path += "?" + params
	}

	var result types.StartStopResponse
	if err := c.doRequest("POST", path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// StopApp stops an app.
func (c *Client) StopApp(name string, async bool) (*types.StartStopResponse, error) {
	path := "/api/v1/apps/" + name + "/stop"
	params := ""
	if async {
		if params != "" {
			params += "&"
		}
		params += "async=true"
	}
	if params != "" {
		path += "?" + params
	}

	var result types.StartStopResponse
	if err := c.doRequest("POST", path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetLogs returns logs for an app.
func (c *Client) GetLogs(name string, tail int, follow bool) (io.ReadCloser, error) {
	path := fmt.Sprintf("/api/v1/apps/%s/logs?tail=%d&follow=%v", name, tail, follow)

	req, err := http.NewRequest("GET", "http://unix"+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

// GetJob gets a job by ID.
func (c *Client) GetJob(id string) (*types.Job, error) {
	var result types.Job
	if err := c.doRequest("GET", "/api/v1/jobs/"+id, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetSocketPath returns the socket path.
func (c *Client) GetSocketPath() string {
	return c.socketPath
}

// Promote promotes a new deployment for an app.
func (c *Client) Promote(appName string, dir string, wait bool) (*types.PromoteResponse, error) {
	path := "/api/v1/apps/" + appName + "/promote"
	if !wait {
		path += "?wait=false"
	}
	req := map[string]string{"dir": dir}
	var result types.PromoteResponse
	if err := c.doRequest("POST", path, req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// PromoteStream promotes a new deployment with streaming progress events.
func (c *Client) PromoteStream(appName string, dir string, progressFn func(types.ProgressEvent)) (*types.PromoteResponse, error) {
	path := "/api/v1/apps/" + appName + "/promote"
	reqBody := map[string]string{"dir": dir}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", "http://unix"+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		var errResp types.ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Code != "" {
			return nil, fmt.Errorf("%s: %s", errResp.Code, errResp.Detail)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Read SSE stream
	scanner := bufio.NewScanner(resp.Body)
	var currentEvent string
	var result types.PromoteResponse

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			currentEvent = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			switch currentEvent {
			case "progress":
				var evt types.ProgressEvent
				if json.Unmarshal([]byte(data), &evt) == nil && progressFn != nil && evt.Step != "" {
					progressFn(evt)
				}
			case "result":
				json.Unmarshal([]byte(data), &result)
			case "error":
				var errData map[string]string
				if json.Unmarshal([]byte(data), &errData) != nil {
					return nil, fmt.Errorf("%s", data)
				}
				if msg, ok := errData["error"]; ok {
					return nil, fmt.Errorf("%s", msg)
				}
				return nil, fmt.Errorf("%s", data)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	if result.Message == "" {
		return nil, fmt.Errorf("unexpected end of stream")
	}
	return &result, nil
}

// Rollback rolls back an app to a previous version.
func (c *Client) Rollback(appName string, version string) (*types.PromoteResponse, error) {
	req := map[string]string{"version": version}
	var result types.PromoteResponse
	if err := c.doRequest("POST", "/api/v1/apps/"+appName+"/rollback", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Status returns the deployment status for an app.
func (c *Client) Status(appName string) (*types.DeployStatusResponse, error) {
	var result types.DeployStatusResponse
	if err := c.doRequest("GET", "/api/v1/apps/"+appName+"/status", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GlobalStatus returns the deployment status for all apps.
func (c *Client) GlobalStatus() (*types.GlobalStatusResponse, error) {
	var result types.GlobalStatusResponse
	if err := c.doRequest("GET", "/api/v1/status", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListImages lists tarball versions for an app. If appName is empty, lists all apps.
func (c *Client) ListImages(appName string) ([]string, error) {
	path := "/api/v1/apps/" + appName + "/images"
	var result types.ListImagesResponse
	if err := c.doRequest("GET", path, nil, &result); err != nil {
		return nil, err
	}
	return result.Images, nil
}

// RemoveImage removes a specific tarball for an app.
func (c *Client) RemoveImage(appName string, version string) error {
	return c.doRequest("DELETE", "/api/v1/apps/"+appName+"/images/"+version, nil, nil)
}

// SetSecret sets a secret for an app.
func (c *Client) SetSecret(appName string, key string, value string) error {
	req := types.SetSecretRequest{Key: key, Value: value}
	return c.doRequest("POST", "/api/v1/apps/"+appName+"/secrets", req, nil)
}

// GetSecret gets a secret for an app.
func (c *Client) GetSecret(appName string, key string) (*types.Secret, error) {
	var result types.Secret
	if err := c.doRequest("GET", "/api/v1/apps/"+appName+"/secrets/"+key, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RemoveSecret removes a secret for an app.
func (c *Client) RemoveSecret(appName string, key string) error {
	return c.doRequest("DELETE", "/api/v1/apps/"+appName+"/secrets/"+key, nil, nil)
}

// ListSecrets lists all secrets for an app (values masked).
func (c *Client) ListSecrets(appName string) ([]*types.Secret, error) {
	var result types.ListSecretsResponse
	if err := c.doRequest("GET", "/api/v1/apps/"+appName+"/secrets", nil, &result); err != nil {
		return nil, err
	}
	secrets := make([]*types.Secret, len(result.Secrets))
	for i := range result.Secrets {
		secrets[i] = &result.Secrets[i]
	}
	return secrets, nil
}

// AddDomain adds a custom domain to an application. When httpOnly is true the
// domain is served over plain HTTP only (no TLS/https block) — used for domains
// not covered by an origin cert.
func (c *Client) AddDomain(appName string, domain string, httpOnly bool) error {
	req := types.AddDomainRequest{Domain: domain, HTTPOnly: httpOnly}
	return c.doRequest("POST", "/api/v1/apps/"+appName+"/domains", req, nil)
}

// RemoveDomain removes a custom domain from an application.
func (c *Client) RemoveDomain(appName string, domain string) error {
	return c.doRequest("DELETE", "/api/v1/apps/"+appName+"/domains/"+domain, nil, nil)
}

// ListDomains lists domains for an application, or all if appName is empty.
func (c *Client) ListDomains(appName string) ([]types.Domain, error) {
	path := "/api/v1/domains"
	if appName != "" {
		path = "/api/v1/apps/" + appName + "/domains"
	}
	var result types.ListDomainsResponse
	if err := c.doRequest("GET", path, nil, &result); err != nil {
		return nil, err
	}
	if result.Domains == nil {
		return []types.Domain{}, nil
	}
	return result.Domains, nil
}

// RemoveApp removes an app with full cleanup (container, tarballs, domains, secrets).
func (c *Client) RemoveApp(name string) error {
	var result types.RemoveAppResponse
	return c.doRequest("POST", "/api/v1/apps/"+name+"/remove", nil, &result)
}

// GetConfig returns all daemon settings.
func (c *Client) GetConfig() (map[string]string, error) {
	var result map[string]string
	if err := c.doRequest("GET", "/api/v1/config", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetConfigKey returns a single daemon setting by key. When reveal is true,
// secret setting values are returned decrypted instead of masked.
func (c *Client) GetConfigKey(key string, reveal bool) (map[string]string, error) {
	path := "/api/v1/config?key=" + url.QueryEscape(key)
	if reveal {
		path += "&reveal=true"
	}
	var result map[string]string
	if err := c.doRequest("GET", path, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// SetConfig sets a daemon setting.
func (c *Client) SetConfig(key, value string) error {
	body := map[string]string{key: value}
	return c.doRequest("PUT", "/api/v1/config", body, nil)
}

// CreateBackup creates a full system backup and returns the path to the archive.
func (c *Client) CreateBackup() (string, error) {
	var result struct {
		Path string `json:"path"`
	}
	if err := c.doRequest("POST", "/api/v1/backup", nil, &result); err != nil {
		return "", err
	}
	return result.Path, nil
}

// CreateAppBackup creates a per-app backup archive and returns the path to it.
func (c *Client) CreateAppBackup(name string) (string, error) {
	var result struct {
		Path string `json:"path"`
	}
	if err := c.doRequest("POST", "/api/v1/backup/"+name, nil, &result); err != nil {
		return "", err
	}
	return result.Path, nil
}

// RestoreAppBackup restores a single app from a per-app backup archive while
// the daemon is running. Returns the promote-style response (version, port,
// container).
func (c *Client) RestoreAppBackup(name, file string) (*types.PromoteResponse, error) {
	var result types.PromoteResponse
	body := map[string]string{"file": file}
	if err := c.doRequest("POST", "/api/v1/apps/"+name+"/restore", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DevStart starts a development container for an app.
func (c *Client) DevStart(name string) (*types.StartStopResponse, error) {
	var result types.StartStopResponse
	if err := c.doRequest("POST", "/api/v1/apps/"+name+"/dev/start", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DevStop stops and removes a development container for an app.
func (c *Client) DevStop(name string) (*types.StartStopResponse, error) {
	var result types.StartStopResponse
	if err := c.doRequest("POST", "/api/v1/apps/"+name+"/dev/stop", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Prune deletes old image tarballs, keeping the newest `keep` per app
// (default 3). When dryRun is true nothing is deleted — only the plan is
// returned. App name empty prunes all apps. The tarball of the currently
// running version is never deleted.
func (c *Client) Prune(appName string, keep int, dryRun bool) (*types.PruneResponse, error) {
	req := types.PruneRequest{App: appName, Keep: keep, DryRun: dryRun}
	var result types.PruneResponse
	if err := c.doRequest("POST", "/api/v1/prune", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ExecStream streams the output of a container exec and resolves its exit code.
type ExecStream struct {
	// Output streams the combined stdout+stderr lines of the exec process.
	// Consume until EOF, then Close.
	Output io.ReadCloser
	// ExitCode returns the process exit code. Only valid after Output has
	// been fully consumed.
	ExitCode func() (int, error)
}

// Exec runs a non-interactive command inside an app's running container and
// returns a stream of its combined stdout+stderr output. The command runs via
// docker exec; pass user as "" to use the container default.
func (c *Client) Exec(appName string, user string, cmd []string) (*ExecStream, error) {
	body, err := json.Marshal(map[string]interface{}{"cmd": cmd, "user": user})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	path := "/api/v1/apps/" + appName + "/exec"
	req, err := http.NewRequest("POST", "http://unix"+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		var errResp types.ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Code != "" {
			return nil, fmt.Errorf("%s: %s", errResp.Code, errResp.Detail)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	pr, pw := io.Pipe()
	stream := &ExecStream{Output: pr}
	var mu sync.Mutex
	exitCode := 0
	var exitErr error
	done := make(chan struct{})

	stream.ExitCode = func() (int, error) {
		<-done
		mu.Lock()
		defer mu.Unlock()
		return exitCode, exitErr
	}

	go func() {
		defer pw.Close()
		defer close(done)

		scanner := bufio.NewScanner(resp.Body)
		event := ""
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data := strings.TrimPrefix(line, "data: ")
				switch event {
				case "exit":
					if code, convErr := strconv.Atoi(strings.TrimSpace(data)); convErr == nil {
						mu.Lock()
						exitCode = code
						mu.Unlock()
					} else {
						mu.Lock()
						exitErr = fmt.Errorf("invalid exit code %q", data)
						mu.Unlock()
					}
				case "error":
					mu.Lock()
					exitErr = fmt.Errorf("%s", data)
					mu.Unlock()
				default:
					if _, werr := pw.Write([]byte(data + "\n")); werr != nil {
						return // reader closed (client cancelled)
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			mu.Lock()
			exitErr = err
			mu.Unlock()
		}
	}()

	return stream, nil
}

// Usage returns per-app usage (CPU/mem + image disk) and docker system totals.
// An empty appName returns all apps; otherwise only that app (404 if unknown).
func (c *Client) Usage(appName string) (*types.UsageResponse, error) {
	path := "/api/v1/usage"
	if appName != "" {
		path += "?app=" + url.QueryEscape(appName)
	}
	var result types.UsageResponse
	if err := c.doRequest("GET", path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Env Group methods ---

// EnvGroupSummary is the API response for an env group with its variable count.
type EnvGroupSummary struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Client    string `json:"client"`
	VarCount  int    `json:"var_count"`
	CreatedAt string `json:"created_at"`
}

// CreateEnvGroup creates a new environment group and returns it.
func (c *Client) CreateEnvGroup(name, client string) (*EnvGroupSummary, error) {
	body := map[string]string{"name": name}
	if client != "" {
		body["client"] = client
	}
	var result EnvGroupSummary
	if err := c.doRequest("POST", "/api/v1/env-groups", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetEnvGroupVar sets a variable in an environment group.
func (c *Client) SetEnvGroupVar(groupName, key, value string) error {
	body := map[string]string{"key": key, "value": value}
	return c.doRequest("POST", "/api/v1/env-groups/"+groupName+"/vars", body, nil)
}

// SetAppEnvGroup assigns an app to an environment group.
func (c *Client) SetAppEnvGroup(appName, groupName string) error {
	body := map[string]string{"group": groupName}
	return c.doRequest("POST", "/api/v1/apps/"+appName+"/env-group", body, nil)
}

// ClearAppEnvGroup removes an app from its environment group.
func (c *Client) ClearAppEnvGroup(appName string) error {
	return c.doRequest("DELETE", "/api/v1/apps/"+appName+"/env-group", nil, nil)
}

// ListEnvGroups returns all environment groups with their variable counts.
func (c *Client) ListEnvGroups() ([]EnvGroupSummary, error) {
	var result []EnvGroupSummary
	if err := c.doRequest("GET", "/api/v1/env-groups", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Shutdown tells the daemon to shut down gracefully.
func (c *Client) Shutdown() error {
	return c.doRequest("POST", "/api/v1/shutdown", nil, nil)
}
