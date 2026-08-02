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


// ErrCode is a typed error code for structured error responses.
type ErrCode string

const (
	ErrNotFound      ErrCode = "NOT_FOUND"
	ErrConflict      ErrCode = "CONFLICT"
	ErrBadRequest    ErrCode = "BAD_REQUEST"
	ErrInternal      ErrCode = "INTERNAL_ERROR"
	ErrValidation    ErrCode = "VALIDATION_ERROR"
	ErrDocker        ErrCode = "DOCKER_ERROR"
	ErrAppRunning    ErrCode = "APP_RUNNING"
	ErrAppNotRunning ErrCode = "APP_NOT_RUNNING"
	ErrBuild         ErrCode = "BUILD_ERROR"
	ErrInfra         ErrCode = "INFRA_ERROR"
	ErrConfig        ErrCode = "CONFIG_ERROR"
)

var ErrNotFoundSentinel = &SystemError{Code: ErrNotFound, Message: "not found"}

// ValidationError: user's deploy.yml or command input is wrong
type ValidationError struct {
	Code    ErrCode `json:"code"`
	Message string  `json:"message"`
	Detail  string  `json:"detail,omitempty"`
}

func (e *ValidationError) Error() string { return e.Message }

// BuildError: Docker build failed (user controls the Dockerfile)
type BuildError struct {
	Code    ErrCode `json:"code"`
	Message string  `json:"message"`
	Detail  string  `json:"detail,omitempty"`
}

func (e *BuildError) Error() string { return e.Message }

// InfraError: Infrastructure issue (Docker down, DNS API timeout, disk full)
type InfraError struct {
	Code    ErrCode `json:"code"`
	Message string  `json:"message"`
	Action  string  `json:"action,omitempty"`
}

func (e *InfraError) Error() string { return e.Message }

// ConfigError: Bad provider token, corrupted config (DO NOT expose credential values)
type ConfigError struct {
	Code    ErrCode `json:"code"`
	Message string  `json:"message"`
	Field   string  `json:"field,omitempty"`
}

func (e *ConfigError) Error() string { return e.Message }

// SystemError: Unexpected internal error (bug in deploy)
type SystemError struct {
	Code    ErrCode `json:"code"`
	Message string  `json:"message"`
	Err     error   `json:"-"` // Original error, logged not returned
}

func (e *SystemError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}
func (e *SystemError) Unwrap() error { return e.Err }


// App represents a deployed application.
type App struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Port        int               `json:"port"`
	ServicePort int               `json:"service_port,omitempty"`
	Image       string            `json:"image"`
	Env         map[string]string `json:"env"`
	Volumes []VolumeMapping `json:"volumes,omitempty"`
	Dev     bool            `json:"dev,omitempty"`
	Network string          `json:"network,omitempty"`
	Command string          `json:"command,omitempty"`
	Resources *ResourceConfig `json:"resources,omitempty"`
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
	Domains   []string          `yaml:"domains,omitempty"`
	Health    HealthConfig      `yaml:"health,omitempty"`
	Resources ResourceConfig    `yaml:"resources,omitempty"`
	Dev       *DevConfig        `yaml:"dev,omitempty"`
	Volumes   []VolumeMapping   `yaml:"volumes,omitempty"`
	Network   string            `yaml:"network,omitempty"`
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
	InitialDelay string `yaml:"initial_delay,omitempty"`
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

// DevConfig holds development container settings.
type DevConfig struct {
	Command string          `yaml:"command,omitempty"`
	Port    int             `yaml:"port,omitempty"`
	Volumes []VolumeMapping `yaml:"volumes,omitempty"`
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
	HTTPOnly  bool   `json:"http_only,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// AddDomainRequest is the JSON body for POST /api/v1/apps/{name}/domains.
type AddDomainRequest struct {
	Domain   string `json:"domain"`
	HTTPOnly bool   `json:"http_only,omitempty"`
}

// ListDomainsResponse is the response for GET /api/v1/apps/{name}/domains.
type ListDomainsResponse struct {
	Domains []Domain `json:"domains"`
}

// RemoveAppResponse is the response for POST /api/v1/apps/{name}/remove.
type RemoveAppResponse struct {
	Message string `json:"message"`
}

// PruneRequest is the JSON body for POST /api/v1/prune.
type PruneRequest struct {
	App    string `json:"app,omitempty"`
	Keep   int    `json:"keep,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// PruneFile describes one tarball in a prune report.
type PruneFile struct {
	Version   string `json:"version"`
	SizeBytes int64  `json:"size_bytes"`
}

// PruneAppResult is the prune report for a single app.
type PruneAppResult struct {
	App         string      `json:"app"`
	Removed     []PruneFile `json:"removed"`
	Kept        []PruneFile `json:"kept"`
	Protected   []PruneFile `json:"protected"`
	FreedBytes  int64       `json:"freed_bytes"`
}

// PruneResponse is the response for POST /api/v1/prune.
type PruneResponse struct {
	Apps           []PruneAppResult `json:"apps"`
	TotalFreedBytes int64           `json:"total_freed_bytes"`
	DryRun         bool             `json:"dry_run"`
}

// ProgressEvent is a streaming progress event for long-running operations.
type ProgressEvent struct {
	Step    string `json:"step"`
	Message string `json:"message"`
	Status  string `json:"status"` // "running", "done", "error"
}
