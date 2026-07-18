package run

import (
	"context"
	"errors"
	"strings"
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
	}}, 2, 10*time.Second)
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
	store, err := NewStore(&scriptedLLM{responses: []scriptedResponse{{err: providerErr}}}, 1, time.Second)
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
	if _, err := NewStore(nil, 1, time.Second); err == nil {
		t.Fatal("NewStore(nil, ...) error = nil, want validation error")
	}

	store, err := NewStore(&scriptedLLM{}, 1, time.Second)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.StartRun(domain.Task{}); err == nil {
		t.Fatal("StartRun(empty task) error = nil, want validation error")
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
