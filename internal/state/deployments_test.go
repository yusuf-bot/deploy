package state

import (
	"database/sql"
	"testing"

	"deploy/internal/types"

	"github.com/google/uuid"
)

func TestCreateDeployment(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "dep-test-app")

	dep := &types.Deployment{
		ID:      uuid.New().String(),
		AppID:   app.ID,
		Version: "v1.0.0-abc1234",
		Status:  types.DeployStatusPending,
		Port:    8080,
	}
	created, err := CreateDeployment(db, dep)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if created.Version != "v1.0.0-abc1234" {
		t.Errorf("expected version v1.0.0-abc1234, got %s", created.Version)
	}
	if created.Status != types.DeployStatusPending {
		t.Errorf("expected status pending, got %s", created.Status)
	}
	if created.Port != 8080 {
		t.Errorf("expected port 8080, got %d", created.Port)
	}
}

func TestGetDeployment(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "get-dep-test")

	dep := &types.Deployment{
		ID:      uuid.New().String(),
		AppID:   app.ID,
		Version: "v2.0.0-def5678",
		Status:  types.DeployStatusActive,
		Port:    9090,
	}
	CreateDeployment(db, dep)

	got, err := GetDeployment(db, dep.ID)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if got == nil {
		t.Fatal("expected deployment, got nil")
	}
	if got.Version != "v2.0.0-def5678" {
		t.Errorf("expected v2.0.0-def5678, got %s", got.Version)
	}
	if got.Status != types.DeployStatusActive {
		t.Errorf("expected active, got %s", got.Status)
	}
}

func TestGetDeploymentNotFound(t *testing.T) {
	db := setupTestDB(t)
	got, err := GetDeployment(db, "nonexistent-id")
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent deployment")
	}
}

func TestListDeploymentsByApp(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "list-dep-app")

	deps := []*types.Deployment{
		{ID: uuid.New().String(), AppID: app.ID, Version: "v1", Status: types.DeployStatusActive, Port: 8080},
		{ID: uuid.New().String(), AppID: app.ID, Version: "v2", Status: types.DeployStatusFailed, Port: 8080},
		{ID: uuid.New().String(), AppID: app.ID, Version: "v3", Status: types.DeployStatusRolledBack, Port: 8080},
	}
	for _, d := range deps {
		CreateDeployment(db, d)
	}

	got, err := ListDeploymentsByApp(db, app.ID)
	if err != nil {
		t.Fatalf("ListDeploymentsByApp: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 deployments, got %d", len(got))
	}

	// Verify all versions are present (order may vary with same-second timestamps)
	versions := make(map[string]bool)
	for _, d := range got {
		versions[d.Version] = true
	}
	for _, v := range []string{"v1", "v2", "v3"} {
		if !versions[v] {
			t.Errorf("expected version %s in results", v)
		}
	}
}

func TestUpdateDeploymentStatus(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "update-dep")

	dep := &types.Deployment{
		ID:      uuid.New().String(),
		AppID:   app.ID,
		Version: "v1.0.0",
		Status:  types.DeployStatusPending,
	}
	CreateDeployment(db, dep)

	if err := UpdateDeploymentStatus(db, dep.ID, types.DeployStatusBuilding, ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}

	got, err := GetDeployment(db, dep.ID)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if got.Status != types.DeployStatusBuilding {
		t.Errorf("expected building, got %s", got.Status)
	}
}

func TestUpdateDeploymentStatusWithLog(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "update-dep-log")

	dep := &types.Deployment{
		ID:      uuid.New().String(),
		AppID:   app.ID,
		Version: "v1.0.0",
		Status:  types.DeployStatusPending,
	}
	CreateDeployment(db, dep)

	if err := UpdateDeploymentStatus(db, dep.ID, types.DeployStatusFailed, "Build failed: timeout"); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}

	got, err := GetDeployment(db, dep.ID)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if got.DeployLog != "Build failed: timeout" {
		t.Errorf("expected log 'Build failed: timeout', got %q", got.DeployLog)
	}
}

func TestGetActiveDeployment(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "active-dep")

	// Create multiple deployments, last one is active
	d1 := &types.Deployment{ID: uuid.New().String(), AppID: app.ID, Version: "v1", Status: types.DeployStatusRolledBack}
	d2 := &types.Deployment{ID: uuid.New().String(), AppID: app.ID, Version: "v2", Status: types.DeployStatusActive}
	CreateDeployment(db, d1)
	CreateDeployment(db, d2)

	got, err := GetActiveDeployment(db, app.ID)
	if err != nil {
		t.Fatalf("GetActiveDeployment: %v", err)
	}
	if got == nil {
		t.Fatal("expected active deployment, got nil")
	}
	if got.Version != "v2" {
		t.Errorf("expected v2, got %s", got.Version)
	}
}

func TestGetActiveDeploymentNone(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "no-active-dep")

	dep := &types.Deployment{ID: uuid.New().String(), AppID: app.ID, Version: "v1", Status: types.DeployStatusFailed}
	CreateDeployment(db, dep)

	got, err := GetActiveDeployment(db, app.ID)
	if err != nil {
		t.Fatalf("GetActiveDeployment: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil when no active deployment")
	}
}

func TestSetAllDeployingToFailed(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "recovery-dep")

	deps := []*types.Deployment{
		{ID: uuid.New().String(), AppID: app.ID, Version: "v1", Status: types.DeployStatusDeploying},
		{ID: uuid.New().String(), AppID: app.ID, Version: "v2", Status: types.DeployStatusActive},
		{ID: uuid.New().String(), AppID: app.ID, Version: "v3", Status: types.DeployStatusDeploying},
	}
	for _, d := range deps {
		CreateDeployment(db, d)
	}

	if err := SetAllDeployingToFailed(db); err != nil {
		t.Fatalf("SetAllDeployingToFailed: %v", err)
	}

	// Check deploying -> failed
	all, _ := ListDeploymentsByApp(db, app.ID)
	for _, dep := range all {
		if dep.Version == "v1" || dep.Version == "v3" {
			if dep.Status != types.DeployStatusFailed {
				t.Errorf("expected failed for %s, got %s", dep.Version, dep.Status)
			}
		}
		if dep.Version == "v2" && dep.Status != types.DeployStatusActive {
			t.Errorf("expected active to remain for %s, got %s", dep.Version, dep.Status)
		}
	}
}

// createTestApp is a helper to create an app for deployment tests.
func createTestApp(t *testing.T, db *sql.DB, name string) *types.App {
	t.Helper()
	app := &types.App{
		ID:    uuid.New().String(),
		Name:  name,
		Port:  8080,
		Image: "nginx:latest",
	}
	created, err := CreateApp(db, app)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return created
}
