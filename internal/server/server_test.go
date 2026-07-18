package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/run"
)

func TestNewServesBrowserAndRunAPI(t *testing.T) {
	store, err := run.NewStore(staticLLM{response: `package solution

func Increment(value int) int {
	return value + 1
}
`}, 1, 10*time.Second)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	task := incrementTask()
	handler, err := New(store, task)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", page.Code, http.StatusOK)
	}
	if !strings.Contains(page.Body.String(), "Repair Loop") {
		t.Fatalf("GET / body did not contain page title: %q", page.Body.String())
	}

	taskPage := httptest.NewRecorder()
	handler.ServeHTTP(taskPage, httptest.NewRequest(http.MethodGet, "/task", nil))
	if taskPage.Code != http.StatusOK {
		t.Fatalf("GET /task status = %d, want %d", taskPage.Code, http.StatusOK)
	}
	if got := taskPage.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET /task Cache-Control = %q, want no-store", got)
	}
	var taskSnapshot taskResponse
	if err := json.NewDecoder(taskPage.Body).Decode(&taskSnapshot); err != nil {
		t.Fatalf("decode task response: %v", err)
	}
	if taskSnapshot.Task != task.Name || taskSnapshot.Spec != task.Spec || taskSnapshot.Signature != task.Signature || taskSnapshot.TestCode != task.TestCode {
		t.Fatalf("GET /task = %#v, want injected task %#v", taskSnapshot, task)
	}
	if taskSnapshot.Oracle != "authored" {
		t.Fatalf("GET /task oracle = %q, want authored", taskSnapshot.Oracle)
	}

	start := httptest.NewRecorder()
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/run", nil))
	if start.Code != http.StatusAccepted {
		t.Fatalf("POST /run status = %d, want %d; body = %s", start.Code, http.StatusAccepted, start.Body.String())
	}

	var started startResponse
	if err := json.NewDecoder(start.Body).Decode(&started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if started.ID == "" {
		t.Fatal("POST /run returned an empty id")
	}

	got := waitForRun(t, handler, started.ID)
	if got.Status != run.StatusPassed {
		t.Fatalf("GET /run status = %q, want %q; error = %q", got.Status, run.StatusPassed, got.Error)
	}
	if len(got.Attempts) != 1 || !got.Attempts[0].Passed {
		t.Fatalf("GET /run attempts = %#v, want one passing attempt", got.Attempts)
	}
}

func TestRunAPIRejectsUnknownRun(t *testing.T) {
	store, err := run.NewStore(staticLLM{}, 1, time.Second)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	handler, err := New(store, incrementTask())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/run/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /run/missing status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestNewRejectsNilStore(t *testing.T) {
	if _, err := New(nil, incrementTask()); err == nil {
		t.Fatal("New(nil, task) error = nil, want validation error")
	}
}

func waitForRun(t *testing.T, handler http.Handler, id string) run.Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/run/"+id, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET /run/%s status = %d, want %d", id, response.Code, http.StatusOK)
		}

		var got run.Run
		if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
			t.Fatalf("decode run response: %v", err)
		}
		if got.Status != run.StatusRunning {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %q did not finish within the test deadline", id)
	return run.Run{}
}

type staticLLM struct {
	response string
}

func (model staticLLM) Complete(_ context.Context, _ string) (string, error) {
	if model.response == "" {
		return "", errors.New("unexpected completion request")
	}
	return model.response, nil
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
		t.Fatalf("Increment(2) = %d, want 3", got)
	}
}
`,
	}
}
