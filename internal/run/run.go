// Package run owns asynchronous repair runs and their in-memory snapshots.
package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/repair"
)

// Status describes the current outcome of a repair run.
type Status string

const (
	StatusRunning              Status = "running"
	StatusPassed               Status = "passed"
	StatusGaveUp               Status = "gaveup"
	StatusOracleFailed         Status = "oraclefailed"
	StatusInfrastructureFailed Status = "infrastructurefailed"
)

// Run is the JSON snapshot consumed by the browser. TestCode is the frozen oracle shown to the
// viewer; Repair never passes it to the coder prompt builders.
type Run struct {
	ID          string           `json:"id"`
	Task        string           `json:"task"`
	Spec        string           `json:"spec"`
	Signature   string           `json:"signature"`
	Oracle      string           `json:"oracle"`
	TestCode    string           `json:"testCode"`
	MaxAttempts int              `json:"maxAttempts"`
	Status      Status           `json:"status"`
	FailureMode string           `json:"failureMode"`
	Error       string           `json:"error"`
	Attempts    []domain.Attempt `json:"attempts"`
}

// Store starts repair loops and retains their snapshots for the lifetime of the process.
type Store struct {
	coder       llm.LLM
	maxAttempts int
	testTimeout time.Duration

	mu     sync.RWMutex
	nextID uint64
	runs   map[string]*Run
}

// NewStore constructs an in-memory store with the repair-loop settings chosen by the caller.
func NewStore(coder llm.LLM, maxAttempts int, testTimeout time.Duration) (*Store, error) {
	if coder == nil {
		return nil, errors.New("coder is required")
	}
	if maxAttempts <= 0 {
		return nil, errors.New("max attempts must be greater than zero")
	}
	if testTimeout <= 0 {
		return nil, errors.New("verifier timeout must be greater than zero")
	}

	return &Store{
		coder:       coder,
		maxAttempts: maxAttempts,
		testTimeout: testTimeout,
		runs:        make(map[string]*Run),
	}, nil
}

// StartRun records a new authored-oracle run and starts the repair loop in a goroutine.
func (store *Store) StartRun(task domain.Task) (string, error) {
	if strings.TrimSpace(task.Name) == "" {
		return "", errors.New("task name is required")
	}
	if strings.TrimSpace(task.TestCode) == "" {
		return "", errors.New("task test code is required")
	}

	store.mu.Lock()
	store.nextID++
	id := fmt.Sprintf("run_%06d", store.nextID)
	store.runs[id] = &Run{
		ID:          id,
		Task:        task.Name,
		Spec:        task.Spec,
		Signature:   task.Signature,
		Oracle:      "authored",
		TestCode:    task.TestCode,
		MaxAttempts: store.maxAttempts,
		Status:      StatusRunning,
		Attempts:    make([]domain.Attempt, 0),
	}
	store.mu.Unlock()

	go store.execute(id, task)
	return id, nil
}

// GetRun returns a copy of the latest snapshot for id.
func (store *Store) GetRun(id string) (Run, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	stored, found := store.runs[id]
	if !found {
		return Run{}, false
	}

	snapshot := *stored
	snapshot.Attempts = make([]domain.Attempt, len(stored.Attempts))
	copy(snapshot.Attempts, stored.Attempts)
	return snapshot, true
}

func (store *Store) execute(id string, task domain.Task) {
	final, err := repair.Repair(
		context.Background(),
		store.coder,
		task,
		store.maxAttempts,
		store.testTimeout,
		func(attempt domain.Attempt) error {
			store.appendAttempt(id, attempt)
			return nil
		},
	)

	store.mu.Lock()
	defer store.mu.Unlock()

	stored, found := store.runs[id]
	if !found {
		return
	}
	if err != nil {
		stored.Status = StatusInfrastructureFailed
		stored.Error = err.Error()
		return
	}
	if final.Passed {
		stored.Status = StatusPassed
		return
	}
	stored.Status = StatusGaveUp
}

func (store *Store) appendAttempt(id string, attempt domain.Attempt) {
	store.mu.Lock()
	defer store.mu.Unlock()

	stored, found := store.runs[id]
	if !found {
		return
	}
	stored.Attempts = append(stored.Attempts, attempt)
}
