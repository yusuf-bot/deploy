package deploy

import (
	"fmt"
	"sync"
)

// LockManager manages per-app locks to prevent concurrent deployments.
type LockManager struct {
	mu    sync.Mutex
	locks map[string]bool
}

// Lock is an acquired lock that auto-releases via Release().
type Lock struct {
	manager *LockManager
	appID   string
}

// Release releases this lock.
func (l *Lock) Release() {
	l.manager.ReleaseLock(l.appID)
}

// NewLockManager creates a new LockManager.
func NewLockManager() *LockManager {
	return &LockManager{
		locks: make(map[string]bool),
	}
}

// Acquire acquires a lock for the given app ID.
// Returns a Lock that must be released.
func (l *LockManager) Acquire(appID string) (*Lock, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.locks[appID] {
		return nil, fmt.Errorf("app %s is already being deployed", appID)
	}
	l.locks[appID] = true
	return &Lock{manager: l, appID: appID}, nil
}

// ReleaseLock releases the lock for the given app ID.
func (l *LockManager) ReleaseLock(appID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.locks, appID)
}

// IsLocked returns whether the given app ID is currently locked.
func (l *LockManager) IsLocked(appID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.locks[appID]
}
