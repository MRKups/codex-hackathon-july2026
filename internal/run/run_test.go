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

func TestStoreRecordsRepairAttemptsAndPasses(t *testing.T) {
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
	store, err := NewStore(&scriptedLLM{responses: []scriptedResponse{
		{text: wrongCode},
		{text: correctCode},
	}}, 2, 10*time.Second, time.Minute)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	id, err := store.StartRun(incrementTask())
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	got := waitForTerminalRun(t, store, id)

	if got.Status != StatusPassed {
		t.Fatalf("run status = %q, want %q; error = %q", got.Status, StatusPassed, got.Error)
	}
	if got.Stage != PhaseComplete {
		t.Fatalf("run stage = %q, want %q", got.Stage, PhaseComplete)
	}
	if got.StartedAt.IsZero() || got.DeadlineAt.IsZero() || !got.DeadlineAt.After(got.StartedAt) {
		t.Fatalf("run timing = started %v, deadline %v, want a valid deadline", got.StartedAt, got.DeadlineAt)
	}
	if got.Oracle != "authored" {
		t.Fatalf("run oracle = %q, want authored", got.Oracle)
	}
	if len(got.Attempts) != 2 {
		t.Fatalf("attempt count = %d, want 2", len(got.Attempts))
	}
	if got.Attempts[0].Passed {
		t.Fatalf("first attempt = %#v, want failure", got.Attempts[0])
	}
	if !strings.Contains(got.Attempts[0].Output, "RUN_TEST_FAILURE_MARKER") {
		t.Fatalf("first attempt output = %q, want verifier failure marker", got.Attempts[0].Output)
	}
	if !got.Attempts[1].Passed || got.Attempts[1].Code != correctCode {
		t.Fatalf("second attempt = %#v, want passing corrected code", got.Attempts[1])
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

func TestStoreRecordsInfrastructureError(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	store, err := NewStore(&scriptedLLM{responses: []scriptedResponse{{err: providerErr}}}, 1, time.Second, time.Minute)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	id, err := store.StartRun(incrementTask())
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	got := waitForTerminalRun(t, store, id)

	if got.Status != StatusInfrastructureFailed {
		t.Fatalf("run status = %q, want %q", got.Status, StatusInfrastructureFailed)
	}
	if !strings.Contains(got.Error, providerErr.Error()) {
		t.Fatalf("run error = %q, want provider error", got.Error)
	}
	if len(got.Attempts) != 0 {
		t.Fatalf("attempt count = %d, want 0", len(got.Attempts))
	}
}

func TestStoreRejectsInvalidInputs(t *testing.T) {
	if _, err := NewStore(nil, 1, time.Second, time.Second); err == nil {
		t.Fatal("NewStore(nil, ...) error = nil, want validation error")
	}
	if _, err := NewStore(&scriptedLLM{}, 1, time.Second, 0); err == nil {
		t.Fatal("NewStore(..., zero run timeout) error = nil, want validation error")
	}

	store, err := NewStore(&scriptedLLM{}, 1, time.Second, time.Minute)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.StartRun(domain.Task{}); err == nil {
		t.Fatal("StartRun(empty task) error = nil, want validation error")
	}
}

func TestStorePublishesVerifyingPhase(t *testing.T) {
	correctCode := `package solution

func Increment(value int) int {
	return value + 1
}
`
	store, err := NewStore(&scriptedLLM{responses: []scriptedResponse{{text: correctCode}}}, 1, 10*time.Second, time.Minute)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	id, err := store.StartRun(slowIncrementTask())
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	active := waitForStage(t, store, id, PhaseVerifying)
	if active.Status != StatusRunning {
		t.Fatalf("active status = %q, want %q", active.Status, StatusRunning)
	}
	if active.CurrentAttempt != 1 {
		t.Fatalf("active attempt = %d, want 1", active.CurrentAttempt)
	}

	completed := waitForTerminalRun(t, store, id)
	if completed.Status != StatusPassed {
		t.Fatalf("completed status = %q, want %q; error = %q", completed.Status, StatusPassed, completed.Error)
	}
}

func TestStoreCancelsActiveProviderCall(t *testing.T) {
	coder := newBlockingLLM()
	store, err := NewStore(coder, 1, time.Second, time.Minute)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	id, err := store.StartRun(incrementTask())
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	waitForSignal(t, coder.started, "provider call")
	active := waitForStage(t, store, id, PhaseWaitingForProvider)
	if active.CurrentAttempt != 1 {
		t.Fatalf("active attempt = %d, want 1", active.CurrentAttempt)
	}

	found, canceled := store.CancelRun(id)
	if !found || !canceled {
		t.Fatalf("CancelRun() = (%t, %t), want (true, true)", found, canceled)
	}
	got := waitForTerminalRun(t, store, id)
	if got.Status != StatusCanceled {
		t.Fatalf("run status = %q, want %q; error = %q", got.Status, StatusCanceled, got.Error)
	}
	if got.Stage != PhaseComplete {
		t.Fatalf("run stage = %q, want %q", got.Stage, PhaseComplete)
	}
	if len(got.Attempts) != 0 {
		t.Fatalf("attempt count = %d, want 0", len(got.Attempts))
	}

	found, canceled = store.CancelRun(id)
	if !found || canceled {
		t.Fatalf("second CancelRun() = (%t, %t), want (true, false)", found, canceled)
	}
}

func TestStoreTimesOutActiveProviderCall(t *testing.T) {
	coder := newBlockingLLM()
	store, err := NewStore(coder, 1, time.Second, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	id, err := store.StartRun(incrementTask())
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	waitForSignal(t, coder.started, "provider call")
	got := waitForTerminalRun(t, store, id)
	if got.Status != StatusTimedOut {
		t.Fatalf("run status = %q, want %q; error = %q", got.Status, StatusTimedOut, got.Error)
	}
	if !strings.Contains(got.Error, "run timed out after") {
		t.Fatalf("run error = %q, want timeout explanation", got.Error)
	}
	if len(got.Attempts) != 0 {
		t.Fatalf("attempt count = %d, want 0", len(got.Attempts))
	}
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
	responses []scriptedResponse
}

func (coder *scriptedLLM) Complete(_ context.Context, _ string) (string, error) {
	if len(coder.responses) == 0 {
		return "", errors.New("unexpected completion request")
	}

	response := coder.responses[0]
	coder.responses = coder.responses[1:]
	return response.text, response.err
}

type blockingLLM struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingLLM() *blockingLLM {
	return &blockingLLM{started: make(chan struct{})}
}

func (coder *blockingLLM) Complete(ctx context.Context, _ string) (string, error) {
	coder.once.Do(func() {
		close(coder.started)
	})
	<-ctx.Done()
	return "", ctx.Err()
}

func incrementTask() domain.Task {
	return domain.Task{
		Name:      "increment",
		Spec:      "Return the input integer increased by one.",
		Signature: "func Increment(value int) int",
		TestCode: `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if got := Increment(2); got != 3 {
		t.Fatalf("RUN_TEST_FAILURE_MARKER: Increment(2) = %d, want 3", got)
	}
}
`,
	}
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
