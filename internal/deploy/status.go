package deploy

import (
	"context"
	"fmt"

	"deploy/internal/state"
	"deploy/internal/types"
)

// Status returns the deployment status for an app.
func (d *Deployer) Status(ctx context.Context, appName string) (*types.DeployStatusResponse, error) {
	app, err := state.GetAppByName(d.db, appName)
	if err != nil {
		return nil, fmt.Errorf("get app: %w", err)
	}
	if app == nil {
		return nil, fmt.Errorf("app %q not found", appName)
	}

	resp := &types.DeployStatusResponse{
		App:              *app,
		DeployInProgress: d.lockManager.IsLocked(appName),
	}

	// Get active deployment
	activeDep, err := state.GetActiveDeployment(d.db, app.ID)
	if err != nil {
		return nil, fmt.Errorf("get active deployment: %w", err)
	}
	resp.ActiveDeployment = activeDep

	// Get last 5 deployments
	deps, err := state.ListDeploymentsByApp(d.db, app.ID)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}

	// Only keep the last 5
	if len(deps) > 5 {
		deps = deps[:5]
	}
	resp.RecentDeployments = deps

	return resp, nil
}

// IsLocked returns whether the given app is currently being deployed.
func (d *Deployer) IsLocked(appID string) bool {
	return d.lockManager.IsLocked(appID)
}
