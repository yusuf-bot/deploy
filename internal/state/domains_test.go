package state

import (
	"testing"

	"deploy/internal/types"

	"github.com/google/uuid"
)

func TestCreateAndGetDomain(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "domain-create-get")

	d := &types.Domain{
		AppID:  app.ID,
		Domain: "example.com",
	}
	if err := CreateDomain(db, d); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	if d.ID == "" {
		t.Fatal("expected domain ID to be set")
	}
	if d.Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", d.Domain)
	}

	got, err := GetDomain(db, d.ID)
	if err != nil {
		t.Fatalf("GetDomain: %v", err)
	}
	if got.Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", got.Domain)
	}
	if got.AppID != app.ID {
		t.Errorf("expected app_id %s, got %s", app.ID, got.AppID)
	}
	if got.CreatedAt == "" {
		t.Error("expected created_at to be set")
	}
	if got.UpdatedAt == "" {
		t.Error("expected updated_at to be set")
	}
}

func TestCreateDomainInvalidAppID(t *testing.T) {
	db := setupTestDB(t)

	d := &types.Domain{
		AppID:  uuid.New().String(),
		Domain: "invalid-app.example.com",
	}
	err := CreateDomain(db, d)
	if err == nil {
		t.Fatal("expected error for invalid app_id (FK violation), got nil")
	}
}

func TestGetDomainNotFound(t *testing.T) {
	db := setupTestDB(t)

	got, err := GetDomain(db, "nonexistent-id")
	if err != nil {
		t.Fatalf("GetDomain: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent domain")
	}
}

func TestGetDomainByDomain(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "domain-by-domain")

	d := &types.Domain{
		AppID:  app.ID,
		Domain: "get-by-domain.example.com",
	}
	if err := CreateDomain(db, d); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	got, err := GetDomainByDomain(db, "get-by-domain.example.com")
	if err != nil {
		t.Fatalf("GetDomainByDomain: %v", err)
	}
	if got.Domain != "get-by-domain.example.com" {
		t.Errorf("expected domain get-by-domain.example.com, got %s", got.Domain)
	}
}

func TestGetDomainByDomainNotFound(t *testing.T) {
	db := setupTestDB(t)

	got, err := GetDomainByDomain(db, "nonexistent.example.com")
	if err != nil {
		t.Fatalf("GetDomainByDomain: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent domain")
	}
}

func TestListDomains(t *testing.T) {
	db := setupTestDB(t)
	app1 := createTestApp(t, db, "domain-list-a")
	app2 := createTestApp(t, db, "domain-list-b")

	domains := []*types.Domain{
		{AppID: app1.ID, Domain: "alpha.example.com"},
		{AppID: app1.ID, Domain: "beta.example.com"},
		{AppID: app2.ID, Domain: "gamma.example.com"},
	}
	for _, d := range domains {
		if err := CreateDomain(db, d); err != nil {
			t.Fatalf("CreateDomain: %v", err)
		}
	}

	got, err := ListDomains(db)
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 domains, got %d", len(got))
	}
	// Verify ordering by domain
	if got[0].Domain != "alpha.example.com" {
		t.Errorf("expected alpha.example.com first, got %s", got[0].Domain)
	}
	if got[2].Domain != "gamma.example.com" {
		t.Errorf("expected gamma.example.com last, got %s", got[2].Domain)
	}
}

func TestListDomainsByApp(t *testing.T) {
	db := setupTestDB(t)
	app1 := createTestApp(t, db, "domain-byapp-a")
	app2 := createTestApp(t, db, "domain-byapp-b")

	domains := []*types.Domain{
		{AppID: app1.ID, Domain: "app1-only.example.com"},
		{AppID: app2.ID, Domain: "app2-only.example.com"},
		{AppID: app1.ID, Domain: "app1-another.example.com"},
	}
	for _, d := range domains {
		if err := CreateDomain(db, d); err != nil {
			t.Fatalf("CreateDomain: %v", err)
		}
	}

	got, err := ListDomainsByApp(db, app1.ID)
	if err != nil {
		t.Fatalf("ListDomainsByApp: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 domains for app1, got %d", len(got))
	}
	if got[0].Domain != "app1-another.example.com" {
		t.Errorf("expected app1-another.example.com first, got %s", got[0].Domain)
	}
	if got[1].Domain != "app1-only.example.com" {
		t.Errorf("expected app1-only.example.com second, got %s", got[1].Domain)
	}
}

func TestListDomainsByAppNoDomains(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "domain-no-domains")

	got, err := ListDomainsByApp(db, app.ID)
	if err != nil {
		t.Fatalf("ListDomainsByApp: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 domains, got %d", len(got))
	}
}

func TestDeleteDomain(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "domain-delete")

	d := &types.Domain{
		AppID:  app.ID,
		Domain: "delete-me.example.com",
	}
	if err := CreateDomain(db, d); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	if err := DeleteDomain(db, d.ID); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}

	got, err := GetDomain(db, d.ID)
	if err != nil {
		t.Fatalf("GetDomain after delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestDeleteDomainNonExistent(t *testing.T) {
	db := setupTestDB(t)

	// Deleting a non-existent domain ID should not error.
	err := DeleteDomain(db, "nonexistent-id")
	if err != nil {
		t.Fatalf("DeleteDomain on non-existent: %v", err)
	}
}

func TestDeleteDomainByDomain(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "domain-del-by-domain")

	d := &types.Domain{
		AppID:  app.ID,
		Domain: "delete-by-domain.example.com",
	}
	if err := CreateDomain(db, d); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	if err := DeleteDomainByDomain(db, "delete-by-domain.example.com"); err != nil {
		t.Fatalf("DeleteDomainByDomain: %v", err)
	}

	got, err := GetDomainByDomain(db, "delete-by-domain.example.com")
	if err != nil {
		t.Fatalf("GetDomainByDomain after delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete by domain")
	}
}

func TestDomainUniqueness(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "domain-unique")

	d1 := &types.Domain{
		AppID:  app.ID,
		Domain: "unique.example.com",
	}
	if err := CreateDomain(db, d1); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	d2 := &types.Domain{
		AppID:  app.ID,
		Domain: "unique.example.com",
	}
	err := CreateDomain(db, d2)
	if err == nil {
		t.Fatal("expected error for duplicate domain, got nil")
	}
}

func TestDomainCascadeOnAppDelete(t *testing.T) {
	db := setupTestDB(t)
	app := createTestApp(t, db, "domain-cascade")

	d := &types.Domain{
		AppID:  app.ID,
		Domain: "cascade.example.com",
	}
	if err := CreateDomain(db, d); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}

	// Delete the app — domains should cascade.
	if err := DeleteApp(db, "domain-cascade"); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	got, err := GetDomain(db, d.ID)
	if err != nil {
		t.Fatalf("GetDomain after app delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after app cascade delete")
	}
}
