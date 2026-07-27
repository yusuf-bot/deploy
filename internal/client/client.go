// Package client provides a thin HTTP client for the deploy daemon over Unix socket.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
			Timeout: 30 * time.Second,
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
func (c *Client) StartApp(name string, wait bool, async bool) (*types.StartStopResponse, error) {
	path := "/api/v1/apps/" + name + "/start"
	params := ""
	if !wait {
		params += "wait=false"
	}
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
func (c *Client) StopApp(name string, wait bool, async bool) (*types.StartStopResponse, error) {
	path := "/api/v1/apps/" + name + "/stop"
	params := ""
	if !wait {
		params += "wait=false"
	}
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

// AddDomain adds a custom domain to an application.
func (c *Client) AddDomain(appName string, domain string) error {
	req := types.AddDomainRequest{Domain: domain}
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

// SetConfig sets a daemon setting.
func (c *Client) SetConfig(key, value string) error {
	body := map[string]string{key: value}
	return c.doRequest("PUT", "/api/v1/config", body, nil)
}
