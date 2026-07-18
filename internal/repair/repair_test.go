package repair

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/domain"
)

func TestRunTestsPasses(t *testing.T) {
	task := authoredTask(`package solution

import "testing"

func TestSolve(t *testing.T) {
	if got := Solve(2, 3); got != 5 {
		t.Fatalf("Solve(2, 3) = %d, want 5", got)
	}
}
`)
	code := `package solution

func Solve(left, right int) int {
	return left + right
}
`

	passed, output, err := runTests(context.Background(), task, code, 10*time.Second)
	if err != nil {
		t.Fatalf("runTests() error = %v", err)
	}
	if !passed {
		t.Fatalf("runTests() passed = false, output = %q", output)
	}
	if output != "" {
		t.Fatalf("runTests() output = %q, want empty", output)
	}
}

func TestRunTestsReturnsBuildFailureWithoutRunningTests(t *testing.T) {
	task := authoredTask(`package solution

import "testing"

func TestVerifierShouldNotRun(t *testing.T) {
	t.Fatal("TEST_RAN_MARKER")
}
`)
	code := `package solution

func Solve( {
`

	passed, output, err := runTests(context.Background(), task, code, 10*time.Second)
	if err != nil {
		t.Fatalf("runTests() error = %v", err)
	}
	if passed {
		t.Fatal("runTests() passed = true, want false")
	}
	if !strings.Contains(output, "solution.go") {
		t.Fatalf("build output = %q, want solution.go diagnostic", output)
	}
	if strings.Contains(output, "TEST_RAN_MARKER") {
		t.Fatalf("build failure unexpectedly ran task tests: %q", output)
	}
}

func TestRunTestsReturnsTestFailure(t *testing.T) {
	task := authoredTask(`package solution

import "testing"

func TestSolve(t *testing.T) {
	if got := Solve(2, 3); got != 5 {
		t.Fatalf("TEST_FAILURE_MARKER: got %d, want 5", got)
	}
}
`)
	code := `package solution

func Solve(left, right int) int {
	return left - right
}
`

	passed, output, err := runTests(context.Background(), task, code, 10*time.Second)
	if err != nil {
		t.Fatalf("runTests() error = %v", err)
	}
	if passed {
		t.Fatal("runTests() passed = true, want false")
	}
	if !strings.Contains(output, "TEST_FAILURE_MARKER") {
		t.Fatalf("test output = %q, want failure marker", output)
	}
}

func TestRunTestsReturnsMalformedTestFailure(t *testing.T) {
	task := authoredTask(`package solution

func TestBroken(t *testing.T) {}
`)
	code := `package solution

func Solve() {}
`

	passed, output, err := runTests(context.Background(), task, code, 10*time.Second)
	if err != nil {
		t.Fatalf("runTests() error = %v", err)
	}
	if passed {
		t.Fatal("runTests() passed = true, want false")
	}
	if !strings.Contains(output, "solution_test.go") {
		t.Fatalf("test output = %q, want solution_test.go diagnostic", output)
	}
}

func TestRunTestsReturnsVerifierTimeoutAsFeedback(t *testing.T) {
	task := authoredTask(`package solution

import "testing"

func TestSolve(t *testing.T) {
	Solve()
}
`)
	code := `package solution

func Solve() {
	for {
	}
}
`

	passed, output, err := runTests(context.Background(), task, code, 5*time.Second)
	if err != nil {
		t.Fatalf("runTests() error = %v", err)
	}
	if passed {
		t.Fatal("runTests() passed = true, want false")
	}
	if output != verifierTimeoutOutput {
		t.Fatalf("runTests() output = %q, want %q", output, verifierTimeoutOutput)
	}
}

func TestRunTestsReturnsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	passed, output, err := runTests(ctx, authoredTask("package solution\n"), "package solution\n", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runTests() error = %v, want context.Canceled", err)
	}
	if passed {
		t.Fatal("runTests() passed = true, want false")
	}
	if output != "" {
		t.Fatalf("runTests() output = %q, want empty", output)
	}
}

func TestRunTestsReturnsCommandLaunchError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	passed, output, err := runTests(context.Background(), authoredTask("package solution\n"), "package solution\n", time.Second)
	if err == nil {
		t.Fatal("runTests() error = nil, want command-launch error")
	}
	if passed {
		t.Fatal("runTests() passed = true, want false")
	}
	if output != "" {
		t.Fatalf("runTests() output = %q, want empty", output)
	}
}

func TestRunTestsRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name    string
		task    domain.Task
		timeout time.Duration
	}{
		{
			name:    "non-positive timeout",
			task:    authoredTask("package solution\n"),
			timeout: 0,
		},
		{
			name:    "missing task tests",
			task:    domain.Task{},
			timeout: time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed, output, err := runTests(context.Background(), tt.task, "package solution\n", tt.timeout)
			if err == nil {
				t.Fatal("runTests() error = nil, want validation error")
			}
			if passed {
				t.Fatal("runTests() passed = true, want false")
			}
			if output != "" {
				t.Fatalf("runTests() output = %q, want empty", output)
			}
		})
	}
}

func authoredTask(testCode string) domain.Task {
	return domain.Task{TestCode: testCode}
}
