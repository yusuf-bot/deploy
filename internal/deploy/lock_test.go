package deploy

import (
	"sync"
	"testing"
)

func TestLockAcquireRelease(t *testing.T) {
	lm := NewLockManager()

	lock, err := lm.Acquire("app-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !lm.IsLocked("app-1") {
		t.Error("expected app-1 to be locked")
	}

	lock.Release()
	if lm.IsLocked("app-1") {
		t.Error("expected app-1 to be unlocked after release")
	}
}

func TestLockConcurrentAcquireBlocks(t *testing.T) {
	lm := NewLockManager()

	lock, err := lm.Acquire("app-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Second acquire should fail
	_, err = lm.Acquire("app-1")
	if err == nil {
		t.Fatal("expected error for double acquire")
	}
	lock.Release()
}

func TestLockDifferentAppsIndependent(t *testing.T) {
	lm := NewLockManager()

	lka, err := lm.Acquire("app-a")
	if err != nil {
		t.Fatalf("Acquire app-a: %v", err)
	}
	lkb, err := lm.Acquire("app-b")
	if err != nil {
		t.Fatalf("Acquire app-b: %v", err)
	}

	if !lm.IsLocked("app-a") {
		t.Error("expected app-a locked")
	}
	if !lm.IsLocked("app-b") {
		t.Error("expected app-b locked")
	}

	lka.Release()
	if lm.IsLocked("app-a") {
		t.Error("expected app-a unlocked")
	}
	if !lm.IsLocked("app-b") {
		t.Error("expected app-b still locked")
	}

	lkb.Release()
}

func TestLockReleaseUnlocksMap(t *testing.T) {
	lm := NewLockManager()
	lk, _ := lm.Acquire("app-1")
	lk.Release()

	// Should be able to acquire again
	lk2, err := lm.Acquire("app-1")
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	lk2.Release()
}

func TestLockConcurrentGoroutines(t *testing.T) {
	lm := NewLockManager()
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := lm.Acquire("shared-app"); err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
				// Hold the lock — do NOT release inside the goroutine.
				// This simulates concurrent deploy attempts where only
				// the first caller should acquire the lock.
			}
		}()
	}
	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful acquire, got %d", successCount)
	}

	// Clean up the held lock
	// Acquire and release to clean up (lock was never released in goroutines)
	cl, _ := lm.Acquire("shared-app")
	if cl != nil {
		cl.Release()
	}
}
