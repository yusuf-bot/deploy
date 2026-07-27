// Package types defines shared types, constants, and error codes for the deploy system.
package types

import "time"

// App status constants.
const (
	StatusCreated  = "created"
	StatusRunning  = "running"
	StatusStopped  = "stopped"
	StatusFailed   = "failed"
	StatusUnknown  = "unknown"
)

// Deployment status constants.
const (
	DeployStatusPending    = "pending"
	DeployStatusBuilding   = "building"
	DeployStatusDeploying  = "deploying"
	DeployStatusActive     = "active"
	DeployStatusFailed     = "failed"
	DeployStatusRolledBack = "rolled_back"
	DeployStatusInactive = "inactive"
)

// Error codes for structured error responses.
const (
	ErrNotFound      = "NOT_FOUND"
	ErrConflict      = "CONFLICT"
	ErrBadRequest    = "BAD_REQUEST"
	ErrInternal      = "INTERNAL_ERROR"
	ErrValidation    = "VALIDATION_ERROR"
	ErrDocker        = "DOCKER_ERROR"
	ErrAppRunning    = "APP_RUNNING"
	ErrAppNotRunning = "APP_NOT_RUNNING"
)

// App represents a deployed application.
type App struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Port        int               `json:"port"`
	Image       string            `json:"image"`
	Env         map[string]string `json:"env"`
	ContainerID string            `json:"container_id,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Job represents a completed asynchronous operation.
type Job struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	AppID       string     `json:"app_id,omitempty"`
	Result      string     `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Deployment represents a single deployment of an application.
type Deployment struct {
	ID             string    `json:"id"`
	AppID          string    `json:"app_id"`
	Version        string    `json:"version"`
	ImageDigest    string    `json:"image_digest,omitempty"`
	Status         string    `json:"status"`
	OldContainerID string    `json:"old_container_id,omitempty"`
	NewContainerID string    `json:"new_container_id,omitempty"`
	Port           int       `json:"port,omitempty"`
	DeployLog      string    `json:"deploy_log,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Secret represents an encrypted secret for an application.
type Secret struct {
	AppID     string    `json:"app_id"`
	Key       string    `json:"key"`
	Value     string    `json:"value,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BuildResult is returned by the Builder after a successful build.
type BuildResult struct {
	Version     string
	ImageRef    string
	TarballPath string
	ImageDigest string
}

// DeployConfig represents the parsed deploy.yml configuration for a project.
type DeployConfig struct {
	App       string            `yaml:"app"`
	Stack     string            `yaml:"stack,omitempty"`
	Build     BuildConfig       `yaml:"build,omitempty"`
	Ports     []PortMapping     `yaml:"ports,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Health    HealthConfig      `yaml:"health,omitempty"`
	Resources ResourceConfig    `yaml:"resources,omitempty"`
	Volumes   []VolumeMapping   `yaml:"volumes,omitempty"`
}

// BuildConfig holds Docker build settings.
type BuildConfig struct {
	Context     string            `yaml:"context,omitempty"`
	Dockerfile  string            `yaml:"dockerfile,omitempty"`
	Args        map[string]string `yaml:"args,omitempty"`
	Target      string            `yaml:"target,omitempty"`
	TagTemplate string            `yaml:"tag-template,omitempty"`
}

// PortMapping maps a container port to a host port.
type PortMapping struct {
	Container int `yaml:"container"`
	Host      int `yaml:"host"`
}

// HealthConfig defines health check parameters.
type HealthConfig struct {
	Path         string `yaml:"path,omitempty"`
	InitialDelay string `yaml:"initial-delay,omitempty"`
	Interval     string `yaml:"interval,omitempty"`
	Timeout      string `yaml:"timeout,omitempty"`
	Retries      int    `yaml:"retries,omitempty"`
}

// ResourceConfig specifies container resource limits.
type ResourceConfig struct {
	Memory string `yaml:"memory,omitempty"`
	CPUs   string `yaml:"cpus,omitempty"`
}

// VolumeMapping binds a host path to a container path.
type VolumeMapping struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// --- API Request/Response types ---

// CreateAppRequest is the JSON body for POST /api/v1/apps.
type CreateAppRequest struct {
	Name  string            `json:"name"`
	Port  int               `json:"port"`
	Image string            `json:"image"`
	Env   map[string]string `json:"env,omitempty"`
}

// UpdateAppRequest is the JSON body for updating an app (future use).
type UpdateAppRequest struct {
	Image *string           `json:"image,omitempty"`
	Port  *int              `json:"port,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
}

// PromoteRequest is the JSON body for POST /api/v1/apps/{name}/promote.
type PromoteRequest struct {
	Dir     string `json:"dir,omitempty"`
	Version string `json:"version"`
}

// PromoteResponse is the response for POST /api/v1/apps/{name}/promote.
type PromoteResponse struct {
	DeploymentID   string `json:"deployment_id"`
	Version        string `json:"version,omitempty"`
	OldContainerID string `json:"old_container_id,omitempty"`
	NewContainerID string `json:"new_container_id,omitempty"`
	Port           int    `json:"port,omitempty"`
	Message        string `json:"message"`
}

// DeployStatusResponse is the response for app deployment status.
type DeployStatusResponse struct {
	App               App           `json:"app"`
	ActiveDeployment  *Deployment   `json:"active_deployment,omitempty"`
	RecentDeployments []Deployment  `json:"recent_deployments,omitempty"`
	DeployInProgress  bool          `json:"deploy_in_progress"`
	Deployments       []Deployment  `json:"deployments,omitempty"`
}

// RollbackRequest is the body for POST /api/v1/apps/{name}/rollback.
type RollbackRequest struct {
	Version string `json:"version"`
}

// ListAppsResponse is the response for GET /api/v1/apps.
type ListAppsResponse struct {
	Apps []App `json:"apps"`
}

// ErrorResponse is the standard error response body.
type ErrorResponse struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// HealthResponse is the response for GET /api/v1/health.
type HealthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	Uptime    string `json:"uptime"`
	AppsCount int    `json:"apps_count"`
}

// StartStopResponse is the response for start/stop operations.
type StartStopResponse struct {
	App       *App   `json:"app,omitempty"`
	JobID     string `json:"job_id,omitempty"`
	Message   string `json:"message"`
	Container string `json:"container_id,omitempty"`
	Logs      string `json:"logs,omitempty"`
}

// LogEntry is a single log line.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Line      string `json:"line"`
	Stream    string `json:"stream"`
}

// --- Query params ---

type StartStopParams struct {
	Wait  bool
	Async bool
}

type ListAppsParams struct {
	Status string
}

type LogsParams struct {
	Tail   int
	Follow bool
}

// GlobalStatusResponse is the response for GET /api/v1/status (all apps).
type GlobalStatusResponse struct {
	Apps []AppStatusSummary `json:"apps"`
}

// AppStatusSummary is a summary of an app's deployment status.
type AppStatusSummary struct {
	App              App         `json:"app"`
	ActiveDeployment *Deployment `json:"active_deployment,omitempty"`
	DeployInProgress bool        `json:"deploy_in_progress"`
}



// ListImagesResponse is the response for GET /api/v1/apps/{name}/images.
type ListImagesResponse struct {
	Images []string `json:"images"`
}


// SetSecretRequest is the JSON body for POST /api/v1/apps/{name}/secrets.
type SetSecretRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}


// ListSecretsResponse is the response for GET /api/v1/apps/{name}/secrets.
type ListSecretsResponse struct {
	Secrets []Secret `json:"secrets"`
}

// Domain represents a custom domain attached to an application.
type Domain struct {
	ID        string `json:"id"`
	AppID     string `json:"app_id"`
	AppName   string `json:"app_name,omitempty"`
	Domain    string `json:"domain"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// AddDomainRequest is the JSON body for POST /api/v1/apps/{name}/domains.
type AddDomainRequest struct {
	Domain string `json:"domain"`
}

// ListDomainsResponse is the response for GET /api/v1/apps/{name}/domains.
type ListDomainsResponse struct {
	Domains []Domain `json:"domains"`
}

// RemoveAppResponse is the response for POST /api/v1/apps/{name}/remove.
type RemoveAppResponse struct {
	Message string `json:"message"`
}
