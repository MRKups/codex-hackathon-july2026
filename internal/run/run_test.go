package run

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/domain"
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
	coder := &scriptedLLM{responses: []scriptedResponse{{text: wrongCode}, {text: correctCode}}}
	store := newStore(t, Config{MaxAttempts: 2, OracleAttempts: 1, TestTimeout: 10 * time.Second, RunTimeout: time.Minute})

	id, err := store.StartRun(generatedIncrementTask(), Roles{
		Coder:       coder,
		Tester:      tester,
		CoderModel:  "code-model",
		TesterModel: "test-model",
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
	store := newStore(t, Config{MaxAttempts: 1, OracleAttempts: 1, TestTimeout: 10 * time.Second, RunTimeout: time.Minute})
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
	if got.TesterModel != "" {
		t.Fatalf("authored tester model = %q, want empty", got.TesterModel)
	}
}

func TestStorePublishesWritingOracleAndCancelsTestWriter(t *testing.T) {
	tester := newBlockingLLM()
	store := newStore(t, Config{MaxAttempts: 1, OracleAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute})

	id, err := store.StartRun(generatedIncrementTask(), Roles{Coder: failLLM{}, Tester: tester})
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
	store := newStore(t, Config{MaxAttempts: 1, OracleAttempts: 1, TestTimeout: time.Second, RunTimeout: 100 * time.Millisecond})
	id, err := store.StartRun(generatedIncrementTask(), Roles{Coder: failLLM{}, Tester: tester})
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
	store := newStore(t, Config{MaxAttempts: 1, OracleAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute})
	id, err := store.StartRun(generatedIncrementTask(), Roles{Coder: coder, Tester: tester})
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
	store := newStore(t, Config{MaxAttempts: 1, OracleAttempts: 1, TestTimeout: 10 * time.Second, RunTimeout: time.Minute})
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
	store := newStore(t, Config{MaxAttempts: 1, OracleAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute})
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

func TestStoreRejectsInvalidInputs(t *testing.T) {
	if _, err := NewStore(Config{}); err == nil {
		t.Fatal("NewStore(Config{}) error = nil, want validation error")
	}
	store := newStore(t, Config{MaxAttempts: 1, OracleAttempts: 1, TestTimeout: time.Second, RunTimeout: time.Minute})
	if _, err := store.StartRun(domain.Task{}, Roles{}); err == nil {
		t.Fatal("StartRun(empty task) error = nil, want validation error")
	}
	if _, err := store.StartRun(generatedIncrementTask(), Roles{Coder: failLLM{}}); err == nil {
		t.Fatal("StartRun(generated without tester) error = nil, want validation error")
	}
}

func newStore(t *testing.T, config Config) *Store {
	t.Helper()
	store, err := NewStore(config)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
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
}

func (model *scriptedLLM) Complete(_ context.Context, _ string) (string, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls++
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
