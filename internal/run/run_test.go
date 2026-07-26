package run

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/oracle"
	"codex-hackathon-july2026/internal/repair"
	"codex-hackathon-july2026/internal/verification"
)

func TestStoreRecordsGeneratedOracleAndRepairAttempts(t *testing.T) {
	wrongCode := `package solution

func Increment(value int) int {
	return value - 1
}
`
	correctCode := `package solution

func Increment(value int) int {
	return value + 1
}
`
	tester := &scriptedLLM{responses: []scriptedResponse{{text: incrementTestCode}}}
	reviewer := &scriptedLLM{responses: []scriptedResponse{{text: `{"verdict":"accept","findings":[]}`}}}
	coder := &scriptedLLM{responses: []scriptedResponse{{text: wrongCode}, {text: correctCode}}}
	store := newStore(t, Config{MaxAttempts: 2, TestTimeout: 10 * time.Second, RunTimeout: time.Minute})

	id, err := store.StartRun(generatedIncrementTask(), Roles{
		Coder:         coder,
		Tester:        tester,
		Reviewer:      reviewer,
		CoderModel:    "code-model",
		TesterModel:   "test-model",
		ReviewerModel: "review-model",
	})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	got := waitForTerminalRun(t, store, id)

	if got.Status != StatusPassed {
		t.Fatalf("run status = %q, want %q; error = %q", got.Status, StatusPassed, got.Error)
	}
	if got.Oracle != string(domain.OracleGenerated) {
		t.Fatalf("run oracle = %q, want generated", got.Oracle)
	}
	if got.TestCode != incrementTestCode {
		t.Fatalf("frozen oracle = %q, want generated test source", got.TestCode)
	}
	if got.Verification.Origin != domain.VerificationOriginGenerated || got.Verification.TaskDigest == "" || got.Verification.Digest == "" {
		t.Fatalf("generated verification manifest = %#v, want generated origin and non-empty digests", got.Verification)
	}
	if got.OracleEvidence.RulebookVersion == "" || got.OracleEvidence.RulebookDigest == "" || got.OracleEvidence.AuthorAttempts != 1 || got.OracleEvidence.ReviewerAttempts != 1 || got.OracleEvidence.AuthorModel != "test-model" || got.OracleEvidence.ReviewerModel != "review-model" || got.OracleEvidence.ReviewVerdict != oracle.ReviewVerdictAccepted {
		t.Fatalf("generated oracle evidence = %#v, want Rulebook provenance and one author attempt", got.OracleEvidence)
	}
	if got.CoderModel != "code-model" || got.TesterModel != "test-model" {
		t.Fatalf("run role models = coder %q tester %q, want selected values", got.CoderModel, got.TesterModel)
	}
	if len(got.Attempts) != 2 {
		t.Fatalf("attempt count = %d, want 2", len(got.Attempts))
	}
	if got.Attempts[0].Passed || !strings.Contains(got.Attempts[0].Output, "RUN_TEST_FAILURE_MARKER") {
		t.Fatalf("first attempt = %#v, want verifier failure", got.Attempts[0])
	}
	if !got.Attempts[1].Passed || got.Attempts[1].Code != correctCode {
		t.Fatalf("second attempt = %#v, want passing corrected code", got.Attempts[1])
	}
	if got.CurrentAttempt != 2 || got.Stage != PhaseComplete {
		t.Fatalf("terminal progress = attempt %d stage %q, want 2/complete", got.CurrentAttempt, got.Stage)
	}
	if tester.callCount() != 1 {
		t.Fatalf("tester calls = %d, want 1 frozen oracle", tester.callCount())
	}

	got.Attempts[0].Code = "mutated snapshot"
	again, found := store.GetRun(id)
	if !found {
		t.Fatal("GetRun() did not find started run")
	}
	if again.Attempts[0].Code != wrongCode {
		t.Fatalf("store leaked mutable attempts: got %q, want %q", again.Attempts[0].Code, wrongCode)
	}
}

func TestStoreRetainsAuthoredControlPath(t *testing.T) {
	correctCode := `package solution

func Increment(value int) int {
	return value + 1
}
`
	store := newStore(t, Config{MaxAttempts: 1, TestTimeout: 10 * time.Second, RunTimeout: time.Minute})
	id, err := store.StartRun(incrementTask(), Roles{Coder: &scriptedLLM{responses: []scriptedResponse{{text: correctCode}}}, CoderModel: "control-model"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	got := waitForTerminalRun(t, store, id)
	if got.Status != StatusPassed {
		t.Fatalf("authored run status = %q, want passed; error = %q", got.Status, got.Error)
	}
	if got.Oracle != string(domain.OracleAuthored) || got.TestCode != incrementTestCode {
		t.Fatalf("authored snapshot = oracle %q test %q, want fixed authored test", got.Oracle, got.TestCode)
	}
	if got.Verification.Origin != domain.VerificationOriginAuthored || got.Verification.TaskDigest == "" || got.Verification.Digest == "" {
		t.Fatalf("authored verification manifest = %#v, want authored origin and non-empty digests", got.Verification)
	}
	if !got.OracleEvidence.IsZero() {
		t.Fatalf("authored oracle evidence = %#v, want zero generated-policy evidence", got.OracleEvidence)
	}
	if got.TesterModel != "" {
		t.Fatalf("authored tester model = %q, want empty", got.TesterModel)
	}
}

func TestStoreUsesTheInjectedResolverBeforeCandidateRepair(t *testing.T) {
	task := incrementTask()
	bundle, err := verification.AuthoredSource(task, task.TestCode)
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}
	resolver := &recordingResolver{resolution: oracle.Resolution{Bundle: bundle}}
	executor := &recordingExecutor{result: domain.Attempt{N: 1, Passed: true}}
	store, err := NewStore(testConfig(Config{MaxAttempts: 1, TestTimeout: 10 * time.Second, RunTimeout: time.Minute}), resolver, executor)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	id, err := store.StartRun(task, Roles{Coder: failLLM{}})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	got := waitForTerminalRun(t, store, id)
	if got.Status != StatusPassed {
		t.Fatalf("run status = %q, want passed; error = %q", got.Status, got.Error)
	}
	calls, request := resolver.snapshot()
	if calls != 1 || request.Task != task || request.Author != nil {
		t.Fatalf("resolver request = %#v calls=%d, want one authored request without source author", request, calls)
	}
	executorCalls, candidateRequest := executor.snapshot()
	wantRequest := repair.CandidateRequest{Spec: task.Spec, Signature: task.Signature, Bundle: bundle}
	if executorCalls != 1 || candidateRequest != wantRequest {
		t.Fatalf("executor received calls=%d request=%#v, want one exact narrow candidate handoff %#v", executorCalls, candidateRequest, wantRequest)
	}
}

func TestStoreKeepsCandidateAttemptZeroUntilResolution(t *testing.T) {
	authoredTask := incrementTask()
	authoredBundle, err := verification.AuthoredSource(authoredTask, authoredTask.TestCode)
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}
	generatedTask := generatedIncrementTask()
	generatedBundle, err := verification.GeneratedSource(generatedTask, incrementTestCode)
	if err != nil {
		t.Fatalf("GeneratedSource() error = %v", err)
	}

	tests := []struct {
		name       string
		task       domain.Task
		resolution oracle.Resolution
		roles      Roles
	}{
		{
			name:       "authored",
			task:       authoredTask,
			resolution: oracle.Resolution{Bundle: authoredBundle},
			roles:      Roles{Coder: failLLM{}},
		},
		{
			name: "generated",
			task: generatedTask,
			resolution: oracle.Resolution{
				Bundle:   generatedBundle,
				Evidence: generatedEvidence(),
			},
			roles: Roles{Coder: failLLM{}, Tester: failLLM{}, Reviewer: failLLM{}, TesterModel: "test-model", ReviewerModel: "review-model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := newGatedResolver(tt.resolution)
			executor := &recordingExecutor{result: domain.Attempt{N: 1, Passed: true}}
			store, err := NewStore(testConfig(Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute}), resolver, executor)
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			id, err := store.StartRun(tt.task, tt.roles)
			if err != nil {
				t.Fatalf("StartRun() error = %v", err)
			}
			waitForSignal(t, resolver.started, "resolver call")
			active, found := store.GetRun(id)
			if !found {
				t.Fatal("GetRun() did not find started run")
			}
			if active.CurrentAttempt != 0 || active.TestCode != "" || active.Verification != (domain.VerificationManifest{}) {
				t.Fatalf("pre-resolution snapshot = %#v, want candidate attempt 0 and no published bundle", active)
			}
			if calls, _ := executor.snapshot(); calls != 0 {
				t.Fatalf("executor calls before resolution = %d, want 0", calls)
			}

			close(resolver.release)
			if got := waitForTerminalRun(t, store, id); got.Status != StatusPassed {
				t.Fatalf("run status = %q, want passed; error = %q", got.Status, got.Error)
			} else if len(got.TestInventory.TopLevelTests) == 0 {
				t.Fatalf("frozen run test inventory = %#v, want top-level test names", got.TestInventory)
			}
		})
	}
}

func TestStoreDoesNotLeakOracleMaterialThroughCandidatePath(t *testing.T) {
	task := generatedIncrementTask()
	const oracleSourceSentinel = "RUN_ORACLE_SOURCE_SENTINEL"
	source := `package solution

import "testing"

// RUN_ORACLE_SOURCE_SENTINEL must never enter a candidate prompt.
func TestIncrement(t *testing.T) {
	if got := Increment(2); got != 3 {
		t.Fatalf("Increment(2) = %d, want 3", got)
	}
}
`
	bundle, err := verification.GeneratedSource(task, source)
	if err != nil {
		t.Fatalf("GeneratedSource() error = %v", err)
	}
	evidence := generatedEvidence()
	evidence.ReviewVerdict = oracle.ReviewVerdictRevised
	evidence.ReviewFindings = []oracle.ReviewFinding{{
		Category: oracle.FindingBoundaryErrorCoverage,
		Summary:  "RUN_REVIEW_FINDING_SENTINEL",
	}}
	resolver := newGatedResolver(oracle.Resolution{Bundle: bundle, Evidence: evidence})
	correctCode := `package solution

func Increment(value int) int { return value + 1 }
`
	coder := &scriptedLLM{responses: []scriptedResponse{{text: correctCode}}}
	store, err := NewStore(testConfig(Config{MaxAttempts: 1, TestTimeout: 10 * time.Second, RunTimeout: time.Minute}), resolver, repair.NewExecutor())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	id, err := store.StartRun(task, Roles{Coder: coder, Tester: failLLM{}, Reviewer: failLLM{}, TesterModel: "test-model", ReviewerModel: "review-model"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	waitForSignal(t, resolver.started, "resolver call")
	if coder.callCount() != 0 {
		t.Fatalf("coder calls before resolution = %d, want 0", coder.callCount())
	}

	close(resolver.release)
	if got := waitForTerminalRun(t, store, id); got.Status != StatusPassed {
		t.Fatalf("run status = %q, want passed; error = %q", got.Status, got.Error)
	}
	prompts := coder.promptSnapshot()
	if len(prompts) != 1 {
		t.Fatalf("coder prompts = %d, want 1", len(prompts))
	}
	for _, forbidden := range []string{oracleSourceSentinel, oracle.DefaultRulebook().PromptText(), "RUN_REVIEW_FINDING_SENTINEL"} {
		if strings.Contains(prompts[0], forbidden) {
			t.Fatalf("candidate prompt leaked oracle material %q:\n%s", forbidden, prompts[0])
		}
	}
}

func TestStoreRejectsInvalidInjectedResolutionBeforeSnapshotOrCandidate(t *testing.T) {
	task := generatedIncrementTask()
	generatedBundle, err := verification.GeneratedSource(task, incrementTestCode)
	if err != nil {
		t.Fatalf("GeneratedSource() error = %v", err)
	}
	authoredBundle, err := verification.AuthoredSource(task, incrementTestCode)
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}
	tamperedBundle := generatedBundle
	tamperedBundle.TestCode += "// digest drift\n"

	tests := []struct {
		name       string
		resolution oracle.Resolution
	}{
		{
			name:       "wrong origin",
			resolution: oracle.Resolution{Bundle: authoredBundle, Evidence: generatedEvidence()},
		},
		{
			name:       "tampered source",
			resolution: oracle.Resolution{Bundle: tamperedBundle, Evidence: generatedEvidence()},
		},
		{
			name:       "missing generated evidence",
			resolution: oracle.Resolution{Bundle: generatedBundle},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingExecutor{result: domain.Attempt{N: 1, Passed: true}}
			store, err := NewStore(testConfig(Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute}), &recordingResolver{resolution: tt.resolution}, executor)
			if err != nil {
				t.Fatalf("NewStore() error = %v", err)
			}
			coder := &countingFailLLM{}
			id, err := store.StartRun(task, Roles{Coder: coder, Tester: failLLM{}, Reviewer: failLLM{}, TesterModel: "test-model", ReviewerModel: "review-model"})
			if err != nil {
				t.Fatalf("StartRun() error = %v", err)
			}
			got := waitForTerminalRun(t, store, id)
			if got.Status != StatusInfrastructureFailed {
				t.Fatalf("run status = %q, want infrastructurefailed; error = %q", got.Status, got.Error)
			}
			if got.TestCode != "" || got.Verification != (domain.VerificationManifest{}) || !got.OracleEvidence.IsZero() {
				t.Fatalf("invalid resolution leaked into snapshot: %#v", got)
			}
			if calls, _ := executor.snapshot(); calls != 0 || coder.callCount() != 0 {
				t.Fatalf("candidate work started after invalid resolution: executor=%d coder=%d", calls, coder.callCount())
			}
		})
	}
}

func TestStoreDoesNotPublishLateResolutionAfterTimeout(t *testing.T) {
	task := incrementTask()
	bundle, err := verification.AuthoredSource(task, task.TestCode)
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}
	resolver := newLateResolver(oracle.Resolution{Bundle: bundle})
	executor := &recordingExecutor{result: domain.Attempt{N: 1, Passed: true}}
	store, err := NewStore(testConfig(Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: 75 * time.Millisecond}), resolver, executor)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	id, err := store.StartRun(task, Roles{Coder: failLLM{}})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	waitForSignal(t, resolver.started, "resolver call")
	waitForSignal(t, resolver.contextDone, "run deadline")
	close(resolver.release)

	got := waitForTerminalRun(t, store, id)
	if got.Status != StatusTimedOut {
		t.Fatalf("run status = %q, want timedout; error = %q", got.Status, got.Error)
	}
	if got.TestCode != "" || got.Verification != (domain.VerificationManifest{}) || !got.OracleEvidence.IsZero() {
		t.Fatalf("late resolution leaked into timed-out snapshot: %#v", got)
	}
	if calls, _ := executor.snapshot(); calls != 0 {
		t.Fatalf("executor calls after late resolution = %d, want 0", calls)
	}
}

func TestStorePublishesWritingOracleAndCancelsTestWriter(t *testing.T) {
	tester := newBlockingLLM()
	store := newStore(t, Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute})

	id, err := store.StartRun(generatedIncrementTask(), Roles{Coder: failLLM{}, Tester: tester, Reviewer: failLLM{}, TesterModel: "test-model", ReviewerModel: "review-model"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	waitForSignal(t, tester.started, "test-writer call")
	active := waitForStage(t, store, id, PhaseWritingOracle)
	if active.CurrentAttempt != 0 {
		t.Fatalf("oracle stage current attempt = %d, want 0", active.CurrentAttempt)
	}

	found, canceled := store.CancelRun(id)
	if !found || !canceled {
		t.Fatalf("CancelRun() = (%t, %t), want (true, true)", found, canceled)
	}
	got := waitForTerminalRun(t, store, id)
	if got.Status != StatusCanceled {
		t.Fatalf("run status = %q, want canceled; error = %q", got.Status, got.Error)
	}
	if len(got.Attempts) != 0 || got.TestCode != "" {
		t.Fatalf("canceled pre-oracle run = attempts %#v test %q, want no candidate or oracle", got.Attempts, got.TestCode)
	}
}

func TestStoreTimesOutWhileWritingOracle(t *testing.T) {
	tester := newBlockingLLM()
	store := newStore(t, Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: 100 * time.Millisecond})
	id, err := store.StartRun(generatedIncrementTask(), Roles{Coder: failLLM{}, Tester: tester, Reviewer: failLLM{}, TesterModel: "test-model", ReviewerModel: "review-model"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	waitForSignal(t, tester.started, "test-writer call")
	got := waitForTerminalRun(t, store, id)
	if got.Status != StatusTimedOut {
		t.Fatalf("run status = %q, want timedout; error = %q", got.Status, got.Error)
	}
	if !strings.Contains(got.Error, "run timed out after") {
		t.Fatalf("timeout explanation = %q", got.Error)
	}
}

func TestStoreClassifiesRejectedGeneratedOracle(t *testing.T) {
	tester := &scriptedLLM{responses: []scriptedResponse{{text: `package solution

import "testing"
`}}}
	coder := &countingFailLLM{}
	store := newStore(t, Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute})
	id, err := store.StartRun(generatedIncrementTask(), Roles{Coder: coder, Tester: tester, Reviewer: failLLM{}, TesterModel: "test-model", ReviewerModel: "review-model"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	got := waitForTerminalRun(t, store, id)
	if got.Status != StatusOracleFailed {
		t.Fatalf("run status = %q, want oraclefailed; error = %q", got.Status, got.Error)
	}
	if coder.callCount() != 0 {
		t.Fatalf("coder calls = %d, want 0 after rejected oracle", coder.callCount())
	}
	if len(got.Attempts) != 0 || got.TestCode != "" {
		t.Fatalf("oracle failure snapshot = attempts %#v test %q, want no accepted oracle/candidate", got.Attempts, got.TestCode)
	}
}

func TestStorePublishesVerifyingPhase(t *testing.T) {
	correctCode := `package solution

func Increment(value int) int {
	return value + 1
}
`
	store := newStore(t, Config{MaxAttempts: 1, TestTimeout: 10 * time.Second, RunTimeout: time.Minute})
	id, err := store.StartRun(slowIncrementTask(), Roles{Coder: &scriptedLLM{responses: []scriptedResponse{{text: correctCode}}}})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	active := waitForStage(t, store, id, PhaseVerifying)
	if active.CurrentAttempt != 1 {
		t.Fatalf("active attempt = %d, want 1", active.CurrentAttempt)
	}
	if got := waitForTerminalRun(t, store, id); got.Status != StatusPassed {
		t.Fatalf("completed status = %q, want passed; error = %q", got.Status, got.Error)
	}
}

func TestStoreRejectsSecondLiveRun(t *testing.T) {
	coder := newBlockingLLM()
	store := newStore(t, Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute})
	id, err := store.StartRun(incrementTask(), Roles{Coder: coder})
	if err != nil {
		t.Fatalf("first StartRun() error = %v", err)
	}
	waitForSignal(t, coder.started, "coder call")
	if _, err := store.StartRun(incrementTask(), Roles{Coder: failLLM{}}); !errors.Is(err, ErrRunActive) {
		t.Fatalf("second StartRun() error = %v, want errors.Is(_, ErrRunActive)", err)
	}
	store.CancelRun(id)
	_ = waitForTerminalRun(t, store, id)
}

func TestStoreLogsSafeProviderFailure(t *testing.T) {
	var output lockedLogBuffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	store := newStore(t, Config{MaxAttempts: 1, TestTimeout: 10 * time.Second, RunTimeout: time.Minute, Logger: logger})
	task := incrementTask()
	task.Spec = "RUN_SPEC_LOG_SENTINEL must never be logged"
	task.TestCode = strings.ReplaceAll(task.TestCode, "RUN_TEST_FAILURE_MARKER", "RUN_ORACLE_LOG_SENTINEL")

	id, err := store.StartRun(task, Roles{Coder: &scriptedLLM{responses: []scriptedResponse{{err: &llm.HTTPStatusError{StatusCode: 500}}}}, CoderModel: "code-model"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if got := waitForTerminalRun(t, store, id); got.Status != StatusInfrastructureFailed {
		t.Fatalf("run status = %q, want infrastructurefailed; error = %q", got.Status, got.Error)
	}
	waitForLog(t, &output, "run finished")

	logs := output.String()
	for _, want := range []string{"run finished", "run_id=" + id, "failure_kind=provider_http", "provider_status=500"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs = %q, want %q", logs, want)
		}
	}
	for _, forbidden := range []string{"RUN_SPEC_LOG_SENTINEL", "RUN_ORACLE_LOG_SENTINEL"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("logs exposed forbidden source material %q: %s", forbidden, logs)
		}
	}
}

type lockedLogBuffer struct {
	mu     sync.Mutex
	buffer strings.Builder
}

func (buffer *lockedLogBuffer) Write(contents []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(contents)
}

func (buffer *lockedLogBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func waitForLog(t *testing.T, buffer *lockedLogBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buffer.String(), want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("log output %q did not contain %q", buffer.String(), want)
}

func TestStoreRejectsInvalidInputs(t *testing.T) {
	if _, err := NewStore(Config{}, nil, nil); err == nil {
		t.Fatal("NewStore(Config{}) error = nil, want validation error")
	}
	if _, err := NewStore(Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute}, nil, repair.NewExecutor()); err == nil {
		t.Fatal("NewStore(nil resolver) error = nil, want validation error")
	}
	resolver, err := oracle.NewResolver(oracle.Config{
		MaxAttempts:      1,
		PreflightTimeout: time.Second,
		Rulebook:         oracle.DefaultRulebook(),
		Admitter:         oracle.NewStructuralAdmitter(),
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	if _, err := NewStore(Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute}, resolver, nil); err == nil {
		t.Fatal("NewStore(nil executor) error = nil, want validation error")
	}
	store := newStore(t, Config{MaxAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute})
	if _, err := store.StartRun(domain.Task{}, Roles{}); err == nil {
		t.Fatal("StartRun(empty task) error = nil, want validation error")
	}
	if _, err := store.StartRun(generatedIncrementTask(), Roles{Coder: failLLM{}}); err == nil {
		t.Fatal("StartRun(generated without tester) error = nil, want validation error")
	}
}

func newStore(t *testing.T, config Config) *Store {
	t.Helper()
	config = testConfig(config)
	resolver, err := oracle.NewResolver(oracle.Config{
		MaxAttempts:      1,
		PreflightTimeout: config.TestTimeout,
		Rulebook:         oracle.DefaultRulebook(),
		Admitter:         oracle.NewStructuralAdmitter(),
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	store, err := NewStore(config, resolver, repair.NewExecutor())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func testConfig(config Config) Config {
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return config
}

func waitForTerminalRun(t *testing.T, store *Store, id string) Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		run, found := store.GetRun(id)
		if !found {
			t.Fatalf("GetRun(%q) did not find run", id)
		}
		if run.Status != StatusRunning {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %q did not finish within the test deadline", id)
	return Run{}
}

func waitForStage(t *testing.T, store *Store, id string, want Phase) Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		run, found := store.GetRun(id)
		if !found {
			t.Fatalf("GetRun(%q) did not find run", id)
		}
		if run.Status == StatusRunning && run.Stage == want {
			return run
		}
		if run.Status != StatusRunning {
			t.Fatalf("run reached terminal status %q before stage %q; error = %q", run.Status, want, run.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %q did not reach stage %q within the test deadline", id, want)
	return Run{}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

type scriptedResponse struct {
	text string
	err  error
}

type scriptedLLM struct {
	mu        sync.Mutex
	responses []scriptedResponse
	calls     int
	prompts   []string
}

func (model *scriptedLLM) Complete(_ context.Context, promptText string) (string, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls++
	model.prompts = append(model.prompts, promptText)
	if len(model.responses) == 0 {
		return "", errors.New("unexpected completion request")
	}
	response := model.responses[0]
	model.responses = model.responses[1:]
	return response.text, response.err
}

func (model *scriptedLLM) callCount() int {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.calls
}

func (model *scriptedLLM) promptSnapshot() []string {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]string(nil), model.prompts...)
}

type blockingLLM struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingLLM() *blockingLLM {
	return &blockingLLM{started: make(chan struct{})}
}

func (model *blockingLLM) Complete(ctx context.Context, _ string) (string, error) {
	model.once.Do(func() {
		close(model.started)
	})
	<-ctx.Done()
	return "", ctx.Err()
}

type failLLM struct{}

func (failLLM) Complete(_ context.Context, _ string) (string, error) {
	return "", errors.New("unexpected completion request")
}

type countingFailLLM struct {
	mu    sync.Mutex
	calls int
}

func (model *countingFailLLM) Complete(_ context.Context, _ string) (string, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls++
	return "", errors.New("coder should not have been called")
}

func (model *countingFailLLM) callCount() int {
	model.mu.Lock()
	defer model.mu.Unlock()
	return model.calls
}

type recordingResolver struct {
	mu          sync.Mutex
	resolution  oracle.Resolution
	calls       int
	lastRequest oracle.Request
}

func (resolver *recordingResolver) Resolve(_ context.Context, request oracle.Request, report oracle.ProgressReporter) (oracle.Resolution, error) {
	if report.PreflightingSource != nil {
		report.PreflightingSource()
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.calls++
	resolver.lastRequest = request
	return resolver.resolution, nil
}

func (resolver *recordingResolver) snapshot() (int, oracle.Request) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	return resolver.calls, resolver.lastRequest
}

type recordingExecutor struct {
	mu          sync.Mutex
	result      domain.Attempt
	err         error
	calls       int
	lastRequest repair.CandidateRequest
}

func (executor *recordingExecutor) Execute(_ context.Context, _ llm.LLM, request repair.CandidateRequest, _ repair.Config, report repair.ProgressReporter) (domain.Attempt, error) {
	executor.mu.Lock()
	executor.calls++
	executor.lastRequest = request
	result := executor.result
	err := executor.err
	executor.mu.Unlock()

	if result.N != 0 && report.AttemptFinished != nil {
		if reportErr := report.AttemptFinished(result); reportErr != nil {
			return result, reportErr
		}
	}
	return result, err
}

func (executor *recordingExecutor) snapshot() (int, repair.CandidateRequest) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.calls, executor.lastRequest
}

type gatedResolver struct {
	resolution oracle.Resolution
	started    chan struct{}
	release    chan struct{}
	once       sync.Once
}

func newGatedResolver(resolution oracle.Resolution) *gatedResolver {
	return &gatedResolver{
		resolution: resolution,
		started:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (resolver *gatedResolver) Resolve(ctx context.Context, _ oracle.Request, report oracle.ProgressReporter) (oracle.Resolution, error) {
	if report.PreflightingSource != nil {
		report.PreflightingSource()
	}
	resolver.once.Do(func() {
		close(resolver.started)
	})
	select {
	case <-resolver.release:
		return resolver.resolution, nil
	case <-ctx.Done():
		return oracle.Resolution{}, ctx.Err()
	}
}

type lateResolver struct {
	resolution   oracle.Resolution
	started      chan struct{}
	contextDone  chan struct{}
	release      chan struct{}
	startedOnce  sync.Once
	deadlineOnce sync.Once
}

func newLateResolver(resolution oracle.Resolution) *lateResolver {
	return &lateResolver{
		resolution:  resolution,
		started:     make(chan struct{}),
		contextDone: make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (resolver *lateResolver) Resolve(ctx context.Context, _ oracle.Request, report oracle.ProgressReporter) (oracle.Resolution, error) {
	if report.PreflightingSource != nil {
		report.PreflightingSource()
	}
	resolver.startedOnce.Do(func() {
		close(resolver.started)
	})
	<-ctx.Done()
	resolver.deadlineOnce.Do(func() {
		close(resolver.contextDone)
	})
	<-resolver.release
	return resolver.resolution, nil
}

func generatedEvidence() oracle.Evidence {
	rulebook := oracle.DefaultRulebook()
	return oracle.Evidence{
		RulebookVersion:  rulebook.Version,
		RulebookDigest:   rulebook.Digest(),
		AuthorModel:      "test-model",
		ReviewerModel:    "review-model",
		AuthorAttempts:   1,
		ReviewerAttempts: 1,
		ReviewVerdict:    oracle.ReviewVerdictAccepted,
	}
}

const incrementTestCode = `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if got := Increment(2); got != 3 {
		t.Fatalf("RUN_TEST_FAILURE_MARKER: Increment(2) = %d, want 3", got)
	}
}
`

func incrementTask() domain.Task {
	return domain.Task{
		Name:      "increment",
		Spec:      "Return the input integer increased by one.",
		Signature: "func Increment(value int) int",
		Oracle:    domain.OracleAuthored,
		TestCode:  incrementTestCode,
	}
}

func generatedIncrementTask() domain.Task {
	task := incrementTask()
	task.Oracle = domain.OracleGenerated
	task.TestCode = ""
	return task
}

func slowIncrementTask() domain.Task {
	task := incrementTask()
	task.TestCode = `package solution

import (
	"testing"
	"time"
)

func TestIncrement(t *testing.T) {
	time.Sleep(300 * time.Millisecond)
	if got := Increment(2); got != 3 {
		t.Fatalf("Increment(2) = %d, want 3", got)
	}
}
`
	return task
}
