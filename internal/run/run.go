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
	StatusCanceled             Status = "canceled"
	StatusTimedOut             Status = "timedout"
	StatusOracleFailed         Status = "oraclefailed"
	StatusInfrastructureFailed Status = "infrastructurefailed"
)

// Phase describes the active operation within a running repair loop. Terminal runs always use
// PhaseComplete. Attempts are only appended after their Go verification has finished, so phase
// makes the period before the first completed attempt observable to API clients.
type Phase string

const (
	PhaseStarting           Phase = "starting"
	PhaseWaitingForProvider Phase = "waitingforprovider"
	PhaseVerifying          Phase = "verifying"
	PhaseCanceling          Phase = "canceling"
	PhaseComplete           Phase = "complete"
)

// Run is the JSON snapshot consumed by the browser. TestCode is the frozen oracle shown to the
// viewer; Repair never passes it to the coder prompt builders.
type Run struct {
	ID             string           `json:"id"`
	Task           string           `json:"task"`
	Spec           string           `json:"spec"`
	Signature      string           `json:"signature"`
	Oracle         string           `json:"oracle"`
	TestCode       string           `json:"testCode"`
	MaxAttempts    int              `json:"maxAttempts"`
	Status         Status           `json:"status"`
	Stage          Phase            `json:"stage"`
	CurrentAttempt int              `json:"currentAttempt"`
	StartedAt      time.Time        `json:"startedAt"`
	DeadlineAt     time.Time        `json:"deadlineAt"`
	FailureMode    string           `json:"failureMode"`
	Error          string           `json:"error"`
	Attempts       []domain.Attempt `json:"attempts"`
}

// Store starts repair loops and retains their snapshots for the lifetime of the process.
type Store struct {
	coder       llm.LLM
	maxAttempts int
	testTimeout time.Duration
	runTimeout  time.Duration

	mu      sync.RWMutex
	nextID  uint64
	runs    map[string]*Run
	cancels map[string]context.CancelFunc
}

// NewStore constructs an in-memory store with the repair-loop settings chosen by the caller.
func NewStore(coder llm.LLM, maxAttempts int, testTimeout, runTimeout time.Duration) (*Store, error) {
	if coder == nil {
		return nil, errors.New("coder is required")
	}
	if maxAttempts <= 0 {
		return nil, errors.New("max attempts must be greater than zero")
	}
	if testTimeout <= 0 {
		return nil, errors.New("verifier timeout must be greater than zero")
	}
	if runTimeout <= 0 {
		return nil, errors.New("run timeout must be greater than zero")
	}

	return &Store{
		coder:       coder,
		maxAttempts: maxAttempts,
		testTimeout: testTimeout,
		runTimeout:  runTimeout,
		runs:        make(map[string]*Run),
		cancels:     make(map[string]context.CancelFunc),
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

	startedAt := time.Now().UTC()
	deadlineAt := startedAt.Add(store.runTimeout)
	ctx, cancel := context.WithDeadline(context.Background(), deadlineAt)

	store.mu.Lock()
	store.nextID++
	id := fmt.Sprintf("run_%06d", store.nextID)
	store.runs[id] = &Run{
		ID:             id,
		Task:           task.Name,
		Spec:           task.Spec,
		Signature:      task.Signature,
		Oracle:         "authored",
		TestCode:       task.TestCode,
		MaxAttempts:    store.maxAttempts,
		Status:         StatusRunning,
		Stage:          PhaseStarting,
		CurrentAttempt: 1,
		StartedAt:      startedAt,
		DeadlineAt:     deadlineAt,
		Attempts:       make([]domain.Attempt, 0),
	}
	store.cancels[id] = cancel
	store.mu.Unlock()

	go store.execute(ctx, cancel, id, task)
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

// CancelRun asks a live repair loop to stop. It returns whether the run exists and whether a
// cancellation request was accepted. A terminal or already-canceling run is found but cannot be
// canceled again.
func (store *Store) CancelRun(id string) (found, canceled bool) {
	store.mu.Lock()
	stored, found := store.runs[id]
	if !found {
		store.mu.Unlock()
		return false, false
	}
	if stored.Status != StatusRunning || stored.Stage == PhaseCanceling {
		store.mu.Unlock()
		return true, false
	}

	cancel := store.cancels[id]
	if cancel == nil {
		store.mu.Unlock()
		return true, false
	}
	stored.Stage = PhaseCanceling
	cancel()
	store.mu.Unlock()

	return true, true
}

func (store *Store) execute(ctx context.Context, cancel context.CancelFunc, id string, task domain.Task) {
	defer cancel()

	final, err := repair.Repair(
		ctx,
		progressCoder{coder: store.coder, store: store, id: id},
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
	delete(store.cancels, id)

	stored, found := store.runs[id]
	if !found {
		return
	}
	stored.Stage = PhaseComplete
	if ctx.Err() == context.Canceled {
		stored.Status = StatusCanceled
		stored.Error = "run canceled"
		return
	}
	if ctx.Err() == context.DeadlineExceeded {
		stored.Status = StatusTimedOut
		stored.Error = fmt.Sprintf("run timed out after %s", store.runTimeout)
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

func (store *Store) setPhase(id string, phase Phase) {
	store.mu.Lock()
	defer store.mu.Unlock()

	stored, found := store.runs[id]
	if !found || stored.Status != StatusRunning || stored.Stage == PhaseCanceling {
		return
	}
	stored.Stage = phase
	stored.CurrentAttempt = len(stored.Attempts) + 1
}

func (store *Store) appendAttempt(id string, attempt domain.Attempt) {
	store.mu.Lock()
	defer store.mu.Unlock()

	stored, found := store.runs[id]
	if !found || stored.Status != StatusRunning || stored.Stage == PhaseCanceling {
		return
	}
	stored.Attempts = append(stored.Attempts, attempt)
}

type progressCoder struct {
	coder llm.LLM
	id    string
	store *Store
}

func (coder progressCoder) Complete(ctx context.Context, prompt string) (string, error) {
	coder.store.setPhase(coder.id, PhaseWaitingForProvider)
	text, err := coder.coder.Complete(ctx, prompt)
	if err == nil {
		coder.store.setPhase(coder.id, PhaseVerifying)
	}
	return text, err
}
