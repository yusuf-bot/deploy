package state

import (
	"database/sql"
	"testing"
	"time"

	"deploy/internal/types"

	"github.com/google/uuid"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func TestCreateApp(t *testing.T) {
	db := setupTestDB(t)
	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "test-app",
		Port:  8080,
		Image: "nginx:latest",
		Env:   map[string]string{"KEY": "value"},
	}
	created, err := CreateApp(db, app)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if created.Name != "test-app" {
		t.Errorf("expected name test-app, got %s", created.Name)
	}
	if created.Port != 8080 {
		t.Errorf("expected port 8080, got %d", created.Port)
	}
}

func TestDuplicateAppName(t *testing.T) {
	db := setupTestDB(t)
	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "test-app",
		Port:  8080,
		Image: "nginx:latest",
	}
	_, err := CreateApp(db, app)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	app2 := &types.App{
		ID:    uuid.New().String(),
		Name:  "test-app",
		Port:  9090,
		Image: "nginx:latest",
	}
	_, err = CreateApp(db, app2)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestGetAppByName(t *testing.T) {
	db := setupTestDB(t)
	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "get-test",
		Port:  8080,
		Image: "alpine:latest",
	}
	CreateApp(db, app)

	got, err := GetAppByName(db, "get-test")
	if err != nil {
		t.Fatalf("GetAppByName: %v", err)
	}
	if got == nil {
		t.Fatal("expected app, got nil")
	}
	if got.Name != "get-test" {
		t.Errorf("expected get-test, got %s", got.Name)
	}
}

func TestGetAppByNameNotFound(t *testing.T) {
	db := setupTestDB(t)
	got, err := GetAppByName(db, "nonexistent")
	if err != nil {
		t.Fatalf("GetAppByName: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent app")
	}
}

func TestListApps(t *testing.T) {
	db := setupTestDB(t)
	apps := []*types.App{
		{ID: uuid.New().String(), Name: "app-a", Port: 8080, Image: "nginx:latest", Status: types.StatusCreated},
		{ID: uuid.New().String(), Name: "app-b", Port: 8081, Image: "nginx:latest", Status: types.StatusRunning},
		{ID: uuid.New().String(), Name: "app-c", Port: 8082, Image: "nginx:latest", Status: types.StatusStopped},
	}
	for _, app := range apps {
		CreateApp(db, app)
	}

	all, err := ListApps(db, "")
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 apps, got %d", len(all))
	}

	running, err := ListApps(db, types.StatusRunning)
	if err != nil {
		t.Fatalf("ListApps with status: %v", err)
	}
	if len(running) != 1 {
		t.Errorf("expected 1 running app, got %d", len(running))
	}
}

func TestEnvSerialization(t *testing.T) {
	db := setupTestDB(t)
	env := map[string]string{
		"NODE_ENV": "production",
		"PORT":     "3000",
		"DB_URL":   "postgres://localhost/mydb",
	}
	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "env-test",
		Port:  8080,
		Image: "node:20",
		Env:   env,
	}
	CreateApp(db, app)

	got, err := GetAppByName(db, "env-test")
	if err != nil {
		t.Fatalf("GetAppByName: %v", err)
	}
	if len(got.Env) != 3 {
		t.Errorf("expected 3 env vars, got %d", len(got.Env))
	}
	if got.Env["NODE_ENV"] != "production" {
		t.Errorf("expected production, got %s", got.Env["NODE_ENV"])
	}
	if got.Env["DB_URL"] != "postgres://localhost/mydb" {
		t.Errorf("expected postgres://localhost/mydb, got %s", got.Env["DB_URL"])
	}
}

func TestUpdateAppStatus(t *testing.T) {
	db := setupTestDB(t)
	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "status-test",
		Port:  8080,
		Image: "nginx:latest",
	}
	CreateApp(db, app)

	if err := UpdateAppStatus(db, "status-test", types.StatusRunning); err != nil {
		t.Fatalf("UpdateAppStatus: %v", err)
	}

	got, err := GetAppByName(db, "status-test")
	if err != nil {
		t.Fatalf("GetAppByName: %v", err)
	}
	if got.Status != types.StatusRunning {
		t.Errorf("expected running, got %s", got.Status)
	}
}

func TestUpdateAppContainer(t *testing.T) {
	db := setupTestDB(t)
	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "container-test",
		Port:  8080,
		Image: "nginx:latest",
	}
	CreateApp(db, app)

	cid := "abc123def456"
	if err := UpdateAppContainer(db, "container-test", cid); err != nil {
		t.Fatalf("UpdateAppContainer: %v", err)
	}

	got, err := GetAppByName(db, "container-test")
	if err != nil {
		t.Fatalf("GetAppByName: %v", err)
	}
	if got.ContainerID != cid {
		t.Errorf("expected %s, got %s", cid, got.ContainerID)
	}
}

func TestUpdateAppServicePort(t *testing.T) {
	db := setupTestDB(t)
	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "svc-port",
		Port:  20018,
		Image: "alpine:latest",
	}
	CreateApp(db, app)

	if err := UpdateAppServicePort(db, "svc-port", 3000); err != nil {
		t.Fatalf("UpdateAppServicePort: %v", err)
	}

	got, err := GetAppByName(db, "svc-port")
	if err != nil {
		t.Fatalf("GetAppByName: %v", err)
	}
	if got == nil {
		t.Fatal("expected app, got nil")
	}
	if got.ServicePort != 3000 {
		t.Errorf("expected ServicePort 3000, got %d", got.ServicePort)
	}
	if got.Port != 20018 {
		t.Errorf("expected host Port 20018 unchanged, got %d", got.Port)
	}
}

func TestDeleteApp(t *testing.T) {
	db := setupTestDB(t)
	app := &types.App{
		ID:    uuid.New().String(),
		Name:  "delete-test",
		Port:  8080,
		Image: "nginx:latest",
	}
	CreateApp(db, app)

	if err := DeleteApp(db, "delete-test"); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	got, err := GetAppByName(db, "delete-test")
	if err != nil {
		t.Fatalf("GetAppByName: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestCreateAndGetJob(t *testing.T) {
	db := setupTestDB(t)
	now := time.Now().UTC()
	job := &types.Job{
		ID:          uuid.New().String(),
		Type:        "start",
		Status:      "done",
		Result:      "started container abc",
		CreatedAt:   now,
		CompletedAt: &now,
	}

	if err := CreateJob(db, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	got, err := GetJob(db, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got == nil {
		t.Fatal("expected job, got nil")
	}
	if got.Type != "start" {
		t.Errorf("expected start, got %s", got.Type)
	}
	if got.Result != "started container abc" {
		t.Errorf("expected 'started container abc', got %s", got.Result)
	}
}

func TestSetAllRunningToUnknown(t *testing.T) {
	db := setupTestDB(t)
	apps := []*types.App{
		{ID: uuid.New().String(), Name: "running-1", Port: 8080, Image: "nginx:latest", Status: types.StatusRunning},
		{ID: uuid.New().String(), Name: "stopped-1", Port: 8081, Image: "nginx:latest", Status: types.StatusStopped},
		{ID: uuid.New().String(), Name: "running-2", Port: 8082, Image: "nginx:latest", Status: types.StatusRunning},
	}
	for _, app := range apps {
		CreateApp(db, app)
	}

	if err := SetAllRunningToUnknown(db); err != nil {
		t.Fatalf("SetAllRunningToUnknown: %v", err)
	}

	all, _ := ListApps(db, "")
	for _, app := range all {
		if app.Name == "running-1" || app.Name == "running-2" {
			if app.Status != types.StatusUnknown {
				t.Errorf("expected unknown for %s, got %s", app.Name, app.Status)
			}
		}
		if app.Name == "stopped-1" && app.Status != types.StatusStopped {
			t.Errorf("expected stopped to remain, got %s", app.Status)
		}
	}
}
