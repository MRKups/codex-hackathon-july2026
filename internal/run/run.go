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
// PhaseComplete. Oracle phases deliberately use CurrentAttempt == 0: no candidate exists yet.
type Phase string

const (
	PhaseStarting           Phase = "starting"
	PhaseWritingOracle      Phase = "writingoracle"
	PhasePreflightingOracle Phase = "preflightingoracle"
	PhaseWaitingForProvider Phase = "waitingforprovider"
	PhaseVerifying          Phase = "verifying"
	PhaseCanceling          Phase = "canceling"
	PhaseComplete           Phase = "complete"
)

// ErrRunActive is returned when a caller tries to start a second live run in one store. The UI
// also disables its start control, but the store is the authority that prevents parallel paid
// provider calls through the HTTP API.
var ErrRunActive = errors.New("a repair run is already active")

// Config holds injected execution limits for every run in a Store.
type Config struct {
	MaxAttempts    int
	OracleAttempts int
	TestTimeout    time.Duration
	RunTimeout     time.Duration
}

// Roles is the per-run pair of independently selected model clients. Tester is required only
// for generated-oracle tasks. Model names are display metadata captured in the run record.
type Roles struct {
	Coder       llm.LLM
	Tester      llm.LLM
	CoderModel  string
	TesterModel string
}

// Run is the JSON snapshot consumed by the browser. TestCode is the accepted frozen oracle
// shown to the viewer; Repair never passes it to the coder prompt builders.
type Run struct {
	ID             string           `json:"id"`
	Task           string           `json:"task"`
	Spec           string           `json:"spec"`
	Signature      string           `json:"signature"`
	Oracle         string           `json:"oracle"`
	TestCode       string           `json:"testCode"`
	CoderModel     string           `json:"coderModel"`
	TesterModel    string           `json:"testerModel"`
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
	config Config

	mu          sync.RWMutex
	nextID      uint64
	runs        map[string]*Run
	cancels     map[string]context.CancelFunc
	activeRunID string
}

// NewStore constructs an in-memory store with caller-selected execution limits.
func NewStore(config Config) (*Store, error) {
	if config.MaxAttempts <= 0 {
		return nil, errors.New("max attempts must be greater than zero")
	}
	if config.OracleAttempts <= 0 {
		return nil, errors.New("oracle attempts must be greater than zero")
	}
	if config.TestTimeout <= 0 {
		return nil, errors.New("verifier timeout must be greater than zero")
	}
	if config.RunTimeout <= 0 {
		return nil, errors.New("run timeout must be greater than zero")
	}

	return &Store{
		config:  config,
		runs:    make(map[string]*Run),
		cancels: make(map[string]context.CancelFunc),
	}, nil
}

// StartRun records a new task and starts its repair loop in a goroutine. The store accepts the
// already-resolved role clients instead of holding a global coder, which makes model selection
// part of the immutable record for this particular run.
func (store *Store) StartRun(task domain.Task, roles Roles) (string, error) {
	mode, err := validateStart(task, roles)
	if err != nil {
		return "", err
	}
	task.Oracle = mode

	startedAt := time.Now().UTC()
	deadlineAt := startedAt.Add(store.config.RunTimeout)

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.activeRunID != "" {
		return "", ErrRunActive
	}

	ctx, cancel := context.WithDeadline(context.Background(), deadlineAt)
	store.nextID++
	id := fmt.Sprintf("run_%06d", store.nextID)
	currentAttempt := 1
	if mode == domain.OracleGenerated {
		currentAttempt = 0
	}
	testCode := ""
	if mode == domain.OracleAuthored {
		testCode = task.TestCode
	}
	store.runs[id] = &Run{
		ID:             id,
		Task:           task.Name,
		Spec:           task.Spec,
		Signature:      task.Signature,
		Oracle:         string(mode),
		TestCode:       testCode,
		CoderModel:     roles.CoderModel,
		TesterModel:    roles.TesterModel,
		MaxAttempts:    store.config.MaxAttempts,
		Status:         StatusRunning,
		Stage:          PhaseStarting,
		CurrentAttempt: currentAttempt,
		StartedAt:      startedAt,
		DeadlineAt:     deadlineAt,
		Attempts:       make([]domain.Attempt, 0),
	}
	store.cancels[id] = cancel
	store.activeRunID = id

	go store.execute(ctx, cancel, id, task, roles)
	return id, nil
}

func validateStart(task domain.Task, roles Roles) (domain.OracleMode, error) {
	if strings.TrimSpace(task.Name) == "" {
		return "", errors.New("task name is required")
	}
	if strings.TrimSpace(task.Spec) == "" {
		return "", errors.New("task spec is required")
	}
	if strings.TrimSpace(task.Signature) == "" {
		return "", errors.New("task signature is required")
	}
	if err := domain.ValidateSignature(task.Signature); err != nil {
		return "", fmt.Errorf("task signature is invalid: %w", err)
	}
	if roles.Coder == nil {
		return "", errors.New("coder is required")
	}

	mode := task.Oracle
	if mode == "" {
		mode = domain.OracleAuthored
	}
	switch mode {
	case domain.OracleAuthored:
		if strings.TrimSpace(task.TestCode) == "" {
			return "", errors.New("task test code is required")
		}
	case domain.OracleGenerated:
		if roles.Tester == nil {
			return "", errors.New("test writer is required for a generated oracle")
		}
	default:
		return "", fmt.Errorf("unknown oracle mode %q", task.Oracle)
	}

	return mode, nil
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

func (store *Store) execute(ctx context.Context, cancel context.CancelFunc, id string, task domain.Task, roles Roles) {
	defer cancel()

	var tester llm.LLM
	if roles.Tester != nil {
		tester = progressTester{tester: roles.Tester, store: store, id: id}
	}
	final, err := repair.Repair(
		ctx,
		progressCoder{coder: roles.Coder, store: store, id: id},
		tester,
		task,
		store.config.MaxAttempts,
		store.config.TestTimeout,
		store.config.OracleAttempts,
		repair.ProgressReporter{
			OracleResolved: func(testCode string) error {
				store.setOracle(id, testCode)
				return nil
			},
			AttemptFinished: func(attempt domain.Attempt) error {
				store.appendAttempt(id, attempt)
				return nil
			},
		},
	)

	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.cancels, id)
	if store.activeRunID == id {
		store.activeRunID = ""
	}

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
		stored.Error = fmt.Sprintf("run timed out after %s", store.config.RunTimeout)
		return
	}
	var oracleFailure *repair.OracleFailureError
	if errors.As(err, &oracleFailure) {
		stored.Status = StatusOracleFailed
		stored.Error = oracleFailure.Error()
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
	switch phase {
	case PhaseWritingOracle, PhasePreflightingOracle:
		stored.CurrentAttempt = 0
	default:
		stored.CurrentAttempt = len(stored.Attempts) + 1
	}
}

func (store *Store) setOracle(id, testCode string) {
	store.mu.Lock()
	defer store.mu.Unlock()

	stored, found := store.runs[id]
	if !found || stored.Status != StatusRunning || stored.Stage == PhaseCanceling {
		return
	}
	stored.TestCode = testCode
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

type progressTester struct {
	id     string
	store  *Store
	tester llm.LLM
}

func (tester progressTester) Complete(ctx context.Context, prompt string) (string, error) {
	tester.store.setPhase(tester.id, PhaseWritingOracle)
	text, err := tester.tester.Complete(ctx, prompt)
	if err == nil {
		tester.store.setPhase(tester.id, PhasePreflightingOracle)
	}
	return text, err
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
