package deploy

import (
	"context"
	"testing"

	"deploy/internal/state"
	"deploy/internal/types"

	"github.com/google/uuid"
)

func TestStatusWithNoDeployments(t *testing.T) {
	mocks, _ := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	resp, err := dep.Status(context.Background(), "test-app")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.App.Name != "test-app" {
		t.Errorf("expected app name test-app, got %s", resp.App.Name)
	}
	if resp.ActiveDeployment != nil {
		t.Errorf("expected no active deployment, got %+v", resp.ActiveDeployment)
	}
	if len(resp.RecentDeployments) != 0 {
		t.Errorf("expected no recent deployments, got %d", len(resp.RecentDeployments))
	}
	if resp.DeployInProgress {
		t.Error("expected no deploy in progress")
	}
}

func TestStatusWithActiveDeployment(t *testing.T) {
	mocks, _ := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	app, _ := state.GetAppByName(mocks.db, "test-app")

	// Create an active deployment
	activeDep := &types.Deployment{
		ID:      uuid.New().String(),
		AppID:   app.ID,
		Version: "v1.0.0",
		Status:  types.DeployStatusActive,
		Port:    8081,
	}
	if _, err := state.CreateDeployment(mocks.db, activeDep); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	resp, err := dep.Status(context.Background(), "test-app")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.ActiveDeployment == nil {
		t.Fatal("expected active deployment")
	}
	if resp.ActiveDeployment.Version != "v1.0.0" {
		t.Errorf("expected version v1.0.0, got %s", resp.ActiveDeployment.Version)
	}
	if resp.ActiveDeployment.Port != 8081 {
		t.Errorf("expected port 8081, got %d", resp.ActiveDeployment.Port)
	}
}

func TestStatusWithRecentDeployments(t *testing.T) {
	mocks, _ := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	app, _ := state.GetAppByName(mocks.db, "test-app")

	// Create several deployments
	for i := 0; i < 7; i++ {
		d := &types.Deployment{
			ID:      uuid.New().String(),
			AppID:   app.ID,
			Version: "v1.0." + string(rune('0'+i)),
			Status:  types.DeployStatusActive,
			Port:    8080 + i,
		}
		if _, err := state.CreateDeployment(mocks.db, d); err != nil {
			t.Fatalf("CreateDeployment %d: %v", i, err)
		}
	}

	resp, err := dep.Status(context.Background(), "test-app")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(resp.RecentDeployments) > 5 {
		t.Errorf("expected at most 5 recent deployments, got %d", len(resp.RecentDeployments))
	}
}

func TestStatusWithDeployInProgress(t *testing.T) {
	mocks, _ := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	// Acquire a lock
	lk, err := mocks.lockManager.Acquire("test-app")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lk.Release()

	resp, err := dep.Status(context.Background(), "test-app")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !resp.DeployInProgress {
		t.Error("expected DeployInProgress to be true")
	}
}

func TestStatusAppNotFound(t *testing.T) {
	mocks, _ := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	_, err := dep.Status(context.Background(), "nonexistent-app")
	if err == nil {
		t.Fatal("expected error for nonexistent app")
	}
}
