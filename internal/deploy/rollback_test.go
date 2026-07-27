package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deploy/internal/build"
	"deploy/internal/state"
	"deploy/internal/types"

	"github.com/google/uuid"
)

func TestRollbackToSpecificVersion(t *testing.T) {
	mocks, appDir := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	app, _ := state.GetAppByName(mocks.db, "test-app")

	// Create a previous active deployment
	prevVersion := "v1.0.0"
	prevDep := &types.Deployment{
		ID:      uuid.New().String(),
		AppID:   app.ID,
		Version: prevVersion,
		Status:  types.DeployStatusActive,
		Port:    8080,
	}
	if _, err := state.CreateDeployment(mocks.db, prevDep); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// Create the tarball on disk so LoadImage can find it
	tarballDir := filepath.Dir(build.TarballPath("test-app", prevVersion))
	if err := os.MkdirAll(tarballDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(build.TarballPath("test-app", prevVersion), []byte("prev-image-data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resp, err := dep.Rollback(context.Background(), "test-app", prevVersion, appDir)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Version != prevVersion {
		t.Errorf("expected version %s, got %s", prevVersion, resp.Version)
	}
	if resp.NewContainerID == "" {
		t.Error("expected non-empty container ID")
	}
}

func TestRollbackToLatest(t *testing.T) {
	mocks, appDir := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	app, _ := state.GetAppByName(mocks.db, "test-app")

	// Create deployments: v1 (active), v2 (active, newer)
	versions := []string{"v1.0.0", "v2.0.0"}
	for _, v := range versions {
		dep := &types.Deployment{
			ID:      uuid.New().String(),
			AppID:   app.ID,
			Version: v,
			Status:  types.DeployStatusActive,
			Port:    8080,
		}
		if _, err := state.CreateDeployment(mocks.db, dep); err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		state.SetActiveDeployment(mocks.db, dep)

		// Create tarball
		tarballDir := filepath.Dir(build.TarballPath("test-app", v))
		if err := os.MkdirAll(tarballDir, 0700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(build.TarballPath("test-app", v), []byte("image-"+v), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		time.Sleep(time.Second)
	}

	// Rollback to latest (empty targetVersion should find previous active)
	resp, err := dep.Rollback(context.Background(), "test-app", "", appDir)
	if err != nil {
		t.Fatalf("Rollback to latest: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Version != "v1.0.0" {
		t.Errorf("expected v1.0.0, got %s", resp.Version)
	}
}

func TestRollbackNoPreviousDeployment(t *testing.T) {
	mocks, appDir := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	// No deployments exist
	_, err := dep.Rollback(context.Background(), "test-app", "", appDir)
	if err == nil {
		t.Fatal("expected error when no previous deployment exists")
	}
}

func TestRollbackMissingTarball(t *testing.T) {
	mocks, appDir := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	app, _ := state.GetAppByName(mocks.db, "test-app")
	depVersion := "v99.0.0"
	rec := &types.Deployment{
		ID:      uuid.New().String(),
		AppID:   app.ID,
		Version: depVersion,
		Status:  types.DeployStatusActive,
		Port:    8080,
	}
	if _, err := state.CreateDeployment(mocks.db, rec); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	state.SetActiveDeployment(mocks.db, rec)

	// Don't create tarball
	_, err := dep.Rollback(context.Background(), "test-app", depVersion, appDir)
	if err == nil {
		t.Fatal("expected error for missing tarball")
	}
}

func TestRollbackAppNotFound(t *testing.T) {
	mocks, appDir := setupDeployTest(t)
	dep := newTestDeployer(mocks)

	_, err := dep.Rollback(context.Background(), "nonexistent", "v1.0.0", appDir)
	if err == nil {
		t.Fatal("expected error for nonexistent app")
	}
}
