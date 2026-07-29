// Package scheduler provides an in-memory job scheduler using Go channels.
// Jobs run asynchronously in goroutines and results are persisted to SQLite.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"deploy/internal/state"
	"deploy/internal/types"

	"github.com/google/uuid"
)

// JobResult holds the outcome of a scheduled job.
type JobResult struct {
	Job    *types.Job
	Done   chan struct{}
	Result string
	Error  string
}

// Scheduler manages in-memory job execution with channel-based queue.
type Scheduler struct {
	mu     sync.Mutex
	jobs   map[string]*JobResult
	db     *sql.DB
	queue  chan *jobTask
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type jobTask struct {
	ID       string
	TaskType string
	AppID    string
	Result   *JobResult
	Fn       func(ctx context.Context) (string, error)
}

// New creates a new Scheduler.
func New(db *sql.DB) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{
		jobs:   make(map[string]*JobResult),
		db:     db,
		queue:  make(chan *jobTask, 100),
		ctx:    ctx,
		cancel: cancel,
	}
	s.startWorkers(5)
	return s
}

func (s *Scheduler) startWorkers(n int) {
	for i := 0; i < n; i++ {
		s.wg.Add(1)
		go s.worker()
	}
}

func (s *Scheduler) worker() {
	defer s.wg.Done()
	for {
		select {
		case task := <-s.queue:
			s.executeTask(task)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Scheduler) executeTask(task *jobTask) {
	start := time.Now().UTC()
	result := task.Result

	s.mu.Lock()
	result.Job.Status = "running"
	result.Job.CreatedAt = start
	s.mu.Unlock()

	execCtx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	res, err := task.Fn(execCtx)
	cancel()

	now := time.Now().UTC()

	s.mu.Lock()
	if err != nil {
		result.Error = err.Error()
		result.Job.Status = "failed"
		result.Job.Error = err.Error()
	} else {
		result.Result = res
		result.Job.Status = "done"
		result.Job.Result = res
	}
	result.Job.CompletedAt = &now
	s.mu.Unlock()

	close(result.Done)

	// Persist to SQLite
	if dbErr := state.CreateJob(s.db, result.Job); dbErr != nil {
		log.Printf("ERROR: persist job %s: %v", task.ID, dbErr)
	}

	// Clean up from in-memory map after delay
	go func(jobID string) {
		time.Sleep(30 * time.Second)
		s.mu.Lock()
		delete(s.jobs, jobID)
		s.mu.Unlock()
	}(task.ID)
}

// StartJob schedules a task to run asynchronously. Returns a JobResult with a Done channel.
func (s *Scheduler) StartJob(taskType string, appID string, fn func(ctx context.Context) (string, error)) *JobResult {
	id := uuid.New().String()

	jobResult := &JobResult{
		Job: &types.Job{
			ID:    id,
			Type:  taskType,
			AppID: appID,
		},
		Done: make(chan struct{}),
	}

	s.mu.Lock()
	s.jobs[id] = jobResult
	s.mu.Unlock()

	s.queue <- &jobTask{
		ID:       id,
		TaskType: taskType,
		AppID:    appID,
		Result:   jobResult,
		Fn:       fn,
	}

	return jobResult
}

// GetJobResult returns a JobResult by ID.
func (s *Scheduler) GetJobResult(id string) *JobResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id]
}

// GetJob returns a job by ID — checks in-memory first, then SQLite.
func (s *Scheduler) GetJob(id string) (*types.Job, error) {
	s.mu.Lock()
	if jr, ok := s.jobs[id]; ok {
		jobCopy := *jr.Job
		s.mu.Unlock()
		return &jobCopy, nil
	}
	s.mu.Unlock()

	job, err := state.GetJob(s.db, id)
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	return job, nil
}

// Stop gracefully shuts down the scheduler.
func (s *Scheduler) Stop() {
	s.cancel()
	s.wg.Wait()
}
