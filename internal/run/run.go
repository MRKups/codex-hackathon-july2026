// Package run owns asynchronous repair runs and their in-memory snapshots.
package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/oracle"
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
	PhaseReviewingOracle    Phase = "reviewingoracle"
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
	MaxAttempts int
	TestTimeout time.Duration
	RunTimeout  time.Duration
	Logger      *slog.Logger
}

// Roles is the per-run set of independently selected model clients. Tester and reviewer are
// required only for generated-oracle tasks. Model names are captured in oracle evidence.
type Roles struct {
	Coder         llm.LLM
	Tester        llm.LLM
	Reviewer      llm.LLM
	CoderModel    string
	TesterModel   string
	ReviewerModel string
}

// TemplateProvenance identifies the immutable source-free template used to start a browser run.
// It remains outside the verification manifest because template persistence is authoring input,
// not executable oracle evidence.
type TemplateProvenance struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Run is the JSON snapshot consumed by the browser. TestCode is the accepted frozen oracle
// shown to the viewer; Repair never passes it to the coder prompt builders.
type Run struct {
	ID             string                      `json:"id"`
	Task           string                      `json:"task"`
	Spec           string                      `json:"spec"`
	Signature      string                      `json:"signature"`
	Oracle         string                      `json:"oracle"`
	Verification   domain.VerificationManifest `json:"verification"`
	OracleEvidence oracle.Evidence             `json:"oracleEvidence"`
	TestCode       string                      `json:"testCode"`
	CoderModel     string                      `json:"coderModel"`
	TesterModel    string                      `json:"testerModel"`
	Template       TemplateProvenance          `json:"template"`
	MaxAttempts    int                         `json:"maxAttempts"`
	Status         Status                      `json:"status"`
	Stage          Phase                       `json:"stage"`
	CurrentAttempt int                         `json:"currentAttempt"`
	StartedAt      time.Time                   `json:"startedAt"`
	DeadlineAt     time.Time                   `json:"deadlineAt"`
	FailureMode    string                      `json:"failureMode"`
	Error          string                      `json:"error"`
	Attempts       []domain.Attempt            `json:"attempts"`
}

// Store starts repair loops and retains their snapshots for the lifetime of the process.
type Store struct {
	config   Config
	resolver oracle.Resolver
	executor repair.Executor
	logger   *slog.Logger

	mu          sync.RWMutex
	nextID      uint64
	runs        map[string]*Run
	cancels     map[string]context.CancelFunc
	activeRunID string
}

// NewStore constructs an in-memory store with caller-selected execution limits, an explicit
// pre-freeze resolver, and a candidate-side executor. Both components are injected at the
// composition root rather than discovered by name, task text, or global configuration.
func NewStore(config Config, resolver oracle.Resolver, executor repair.Executor) (*Store, error) {
	if config.MaxAttempts <= 0 {
		return nil, errors.New("max attempts must be greater than zero")
	}
	if config.TestTimeout <= 0 {
		return nil, errors.New("verifier timeout must be greater than zero")
	}
	if config.RunTimeout <= 0 {
		return nil, errors.New("run timeout must be greater than zero")
	}
	if config.Logger == nil {
		return nil, errors.New("run logger is required")
	}
	if resolver == nil {
		return nil, errors.New("oracle resolver is required")
	}
	if executor == nil {
		return nil, errors.New("repair executor is required")
	}

	return &Store{
		config:   config,
		resolver: resolver,
		executor: executor,
		logger:   config.Logger,
		runs:     make(map[string]*Run),
		cancels:  make(map[string]context.CancelFunc),
	}, nil
}

// StartRun records a new task and starts its repair loop in a goroutine. The store accepts the
// already-resolved role clients instead of holding a global coder, which makes model selection
// part of the immutable record for this particular run.
func (store *Store) StartRun(task domain.Task, roles Roles) (string, error) {
	return store.StartRunWithTemplate(task, roles, TemplateProvenance{})
}

// StartRunWithTemplate starts a run whose task was loaded from a source-free template. The
// caller supplies plain provenance values rather than a template dependency, preserving package
// direction from server to run.
func (store *Store) StartRunWithTemplate(task domain.Task, roles Roles, provenance TemplateProvenance) (string, error) {
	mode, err := validateStart(task, roles)
	if err != nil {
		return "", err
	}
	if err := validateTemplateProvenance(provenance); err != nil {
		return "", err
	}
	task.Oracle = mode

	startedAt := time.Now().UTC()
	deadlineAt := startedAt.Add(store.config.RunTimeout)

	store.mu.Lock()
	if store.activeRunID != "" {
		store.mu.Unlock()
		return "", ErrRunActive
	}

	ctx, cancel := context.WithDeadline(context.Background(), deadlineAt)
	store.nextID++
	id := fmt.Sprintf("run_%06d", store.nextID)
	// No candidate exists until a bundle is resolved and candidate generation begins, regardless
	// of whether the source is authored or generated.
	currentAttempt := 0
	// Every oracle source enters the snapshot through one oracle Resolution only, after preflight
	// and sealing. This keeps authored and generated evidence equally atomic.
	testCode := ""
	store.runs[id] = &Run{
		ID:             id,
		Task:           task.Name,
		Spec:           task.Spec,
		Signature:      task.Signature,
		Oracle:         string(mode),
		Verification:   domain.VerificationManifest{},
		OracleEvidence: oracle.Evidence{},
		TestCode:       testCode,
		CoderModel:     roles.CoderModel,
		TesterModel:    roles.TesterModel,
		Template:       provenance,
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
	store.mu.Unlock()

	store.logger.Info("run started",
		"run_id", id,
		"oracle", mode,
		"template_id", provenance.ID,
		"template_digest", provenance.Digest,
		"coder_model", roles.CoderModel,
		"tester_model", roles.TesterModel,
		"reviewer_model", roles.ReviewerModel,
		"max_attempts", store.config.MaxAttempts,
		"spec_bytes", len(task.Spec),
		"signature_bytes", len(task.Signature),
	)

	go store.execute(ctx, cancel, id, task, roles)
	return id, nil
}

func validateTemplateProvenance(provenance TemplateProvenance) error {
	id := strings.TrimSpace(provenance.ID)
	digest := strings.TrimSpace(provenance.Digest)
	if id == "" && digest == "" {
		return nil
	}
	if id == "" || digest == "" {
		return errors.New("template provenance requires both ID and digest")
	}
	if len(id) > 64 || len(digest) != 64 {
		return errors.New("template provenance is invalid")
	}
	for _, character := range digest {
		if (character >= 'a' && character <= 'f') || (character >= '0' && character <= '9') {
			continue
		}
		return errors.New("template provenance is invalid")
	}
	return nil
}

// ListRuns returns immutable snapshots in stable chronological order for the current process.
func (store *Store) ListRuns() []Run {
	store.mu.RLock()
	defer store.mu.RUnlock()
	runs := make([]Run, 0, len(store.runs))
	for _, stored := range store.runs {
		snapshot := *stored
		snapshot.Verification = (domain.VerificationBundle{Manifest: stored.Verification}).Clone().Manifest
		snapshot.OracleEvidence = stored.OracleEvidence.Clone()
		snapshot.Attempts = append([]domain.Attempt(nil), stored.Attempts...)
		runs = append(runs, snapshot)
	}
	sort.Slice(runs, func(left, right int) bool {
		return runs[left].StartedAt.After(runs[right].StartedAt)
	})
	return runs
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
		if roles.Reviewer == nil {
			return "", errors.New("oracle reviewer is required for a generated oracle")
		}
		if strings.TrimSpace(roles.TesterModel) == "" || strings.TrimSpace(roles.ReviewerModel) == "" {
			return "", errors.New("test-writer and reviewer model IDs are required for a generated oracle")
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
	snapshot.Verification = (domain.VerificationBundle{Manifest: stored.Verification}).Clone().Manifest
	snapshot.OracleEvidence = stored.OracleEvidence.Clone()
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
	store.logger.Info("run cancellation requested", "run_id", id)

	return true, true
}

func (store *Store) execute(ctx context.Context, cancel context.CancelFunc, id string, task domain.Task, roles Roles) {
	defer cancel()
	resolution, err := store.resolver.Resolve(ctx, oracle.Request{
		Task:          task,
		Author:        roles.Tester,
		AuthorModel:   roles.TesterModel,
		Reviewer:      roles.Reviewer,
		ReviewerModel: roles.ReviewerModel,
	}, oracle.ProgressReporter{
		WritingSource: func() {
			store.setPhase(id, PhaseWritingOracle)
		},
		PreflightingSource: func() {
			store.setPhase(id, PhasePreflightingOracle)
		},
		ReviewingSource: func() {
			store.setPhase(id, PhaseReviewingOracle)
		},
	})

	var final domain.Attempt
	if err == nil {
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		} else if resolutionErr := oracle.ValidateResolution(task, resolution); resolutionErr != nil {
			err = fmt.Errorf("validate oracle resolution: %w", resolutionErr)
		} else if !store.setResolution(ctx, id, resolution) {
			if contextErr := ctx.Err(); contextErr != nil {
				err = contextErr
			} else {
				err = errors.New("run stopped before oracle resolution could be published")
			}
		}
	}
	if err == nil {
		final, err = store.executor.Execute(
			ctx,
			progressCoder{coder: roles.Coder, store: store, id: id},
			repair.CandidateRequest{
				Spec:      task.Spec,
				Signature: task.Signature,
				Bundle:    resolution.Bundle,
			},
			repair.Config{
				MaxAttempts: store.config.MaxAttempts,
				TestTimeout: store.config.TestTimeout,
			},
			repair.ProgressReporter{
				AttemptFinished: func(attempt domain.Attempt) error {
					store.appendAttempt(id, attempt)
					return nil
				},
			},
		)
	}

	store.mu.Lock()
	delete(store.cancels, id)
	if store.activeRunID == id {
		store.activeRunID = ""
	}

	stored, found := store.runs[id]
	if !found {
		store.mu.Unlock()
		return
	}
	stored.Stage = PhaseComplete
	failureKind := ""
	if ctx.Err() == context.Canceled {
		stored.Status = StatusCanceled
		stored.Error = "run canceled"
		failureKind = "canceled"
	} else if ctx.Err() == context.DeadlineExceeded {
		stored.Status = StatusTimedOut
		stored.Error = fmt.Sprintf("run timed out after %s", store.config.RunTimeout)
		failureKind = "deadline_exceeded"
	} else {
		var oracleFailure *oracle.OracleFailureError
		if errors.As(err, &oracleFailure) {
			if evidenceErr := oracle.ValidateFailureEvidence(task, oracleFailure.Evidence); evidenceErr != nil {
				stored.Status = StatusInfrastructureFailed
				stored.Error = fmt.Sprintf("validate oracle failure evidence: %v", evidenceErr)
				failureKind = "invalid_oracle_failure_evidence"
			} else {
				stored.OracleEvidence = oracleFailure.Evidence.Clone()
				stored.Status = StatusOracleFailed
				stored.Error = oracleFailure.Error()
				failureKind = "oracle_failure"
			}
		} else if err != nil {
			stored.Status = StatusInfrastructureFailed
			stored.Error = err.Error()
			failureKind = infrastructureFailureKind(err)
		} else if final.Passed {
			stored.Status = StatusPassed
		} else {
			stored.Status = StatusGaveUp
		}
	}
	status := stored.Status
	attempts := len(stored.Attempts)
	duration := time.Since(stored.StartedAt)
	store.mu.Unlock()

	attrs := []any{
		"run_id", id,
		"status", status,
		"attempts", attempts,
		"duration", duration,
	}
	if failureKind != "" {
		attrs = append(attrs, "failure_kind", failureKind)
	}
	var providerErr *llm.HTTPStatusError
	if errors.As(err, &providerErr) {
		attrs = append(attrs, "provider_status", providerErr.StatusCode)
	}
	store.logger.Info("run finished", attrs...)
}

func (store *Store) setPhase(id string, phase Phase) {
	store.mu.Lock()

	stored, found := store.runs[id]
	if !found || stored.Status != StatusRunning || stored.Stage == PhaseCanceling {
		store.mu.Unlock()
		return
	}
	if stored.Stage == phase {
		store.mu.Unlock()
		return
	}
	stored.Stage = phase
	switch phase {
	case PhaseWritingOracle, PhasePreflightingOracle, PhaseReviewingOracle:
		stored.CurrentAttempt = 0
	default:
		stored.CurrentAttempt = len(stored.Attempts) + 1
	}
	attempt := stored.CurrentAttempt
	store.mu.Unlock()
	store.logger.Info("run phase changed", "run_id", id, "phase", phase, "current_attempt", attempt)
}

func (store *Store) setResolution(ctx context.Context, id string, resolution oracle.Resolution) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	stored, found := store.runs[id]
	if ctx.Err() != nil || !found || stored.Status != StatusRunning || stored.Stage == PhaseCanceling {
		return false
	}
	bundle := resolution.Bundle.Clone()
	stored.TestCode = bundle.TestCode
	stored.Verification = bundle.Manifest
	stored.OracleEvidence = resolution.Evidence.Clone()
	store.logger.Info("run oracle frozen",
		"run_id", id,
		"origin", bundle.Manifest.Origin,
		"bundle_digest", bundle.Manifest.Digest,
		"task_digest", bundle.Manifest.TaskDigest,
		"test_bytes", len(bundle.TestCode),
	)
	return true
}

func (store *Store) appendAttempt(id string, attempt domain.Attempt) {
	store.mu.Lock()

	stored, found := store.runs[id]
	if !found || stored.Status != StatusRunning || stored.Stage == PhaseCanceling {
		store.mu.Unlock()
		return
	}
	stored.Attempts = append(stored.Attempts, attempt)
	store.mu.Unlock()
	store.logger.Info("run attempt finished",
		"run_id", id,
		"attempt", attempt.N,
		"passed", attempt.Passed,
		"candidate_bytes", len(attempt.Code),
		"verifier_output_bytes", len(attempt.Output),
	)
}

func infrastructureFailureKind(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	var providerErr *llm.HTTPStatusError
	if errors.As(err, &providerErr) {
		return "provider_http"
	}
	return "infrastructure"
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
