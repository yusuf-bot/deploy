package scheduler

import (
	"context"
	"errors"
	"testing"

	"deploy/internal/state"
)

func TestJobExecution(t *testing.T) {
	db, err := state.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := state.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	s := New(db)
	defer s.Stop()

	result := s.StartJob("test", "", func(ctx context.Context) (string, error) {
		return "hello", nil
	})

	<-result.Done

	if result.Error != "" {
		t.Errorf("expected no error, got %s", result.Error)
	}
	if result.Result != "hello" {
		t.Errorf("expected 'hello', got %s", result.Result)
	}
	if result.Job.Status != "done" {
		t.Errorf("expected status done, got %s", result.Job.Status)
	}
}

func TestJobErrorCapture(t *testing.T) {
	db, err := state.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := state.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	s := New(db)
	defer s.Stop()

	result := s.StartJob("test", "", func(ctx context.Context) (string, error) {
		return "", errors.New("something went wrong")
	})

	<-result.Done

	if result.Error != "something went wrong" {
		t.Errorf("expected 'something went wrong', got %s", result.Error)
	}
	if result.Job.Status != "failed" {
		t.Errorf("expected status failed, got %s", result.Job.Status)
	}
}

func TestGetJobResult(t *testing.T) {
	db, err := state.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := state.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	s := New(db)
	defer s.Stop()

	result := s.StartJob("test", "", func(ctx context.Context) (string, error) {
		return "done", nil
	})

	<-result.Done

	// Should find it in-memory
	job, err := s.GetJob(result.Job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job == nil {
		t.Fatal("expected job, got nil")
	}
	if job.Result != "done" {
		t.Errorf("expected 'done', got %s", job.Result)
	}
}

func TestJobPersistence(t *testing.T) {
	db, err := state.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := state.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	s := New(db)
	result := s.StartJob("test", "", func(ctx context.Context) (string, error) {
		return "persisted", nil
	})

	<-result.Done
	s.Stop()

	// Verify it's in SQLite
	job, err := state.GetJob(db, result.Job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job == nil {
		t.Fatal("expected persisted job, got nil")
	}
	if job.Result != "persisted" {
		t.Errorf("expected 'persisted', got %s", job.Result)
	}
}
