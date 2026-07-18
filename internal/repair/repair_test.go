package repair

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
)

const oracleSourceSentinel = "F4_ORACLE_SOURCE_SENTINEL"

func TestRepairRetriesWithVerifierFeedback(t *testing.T) {
	task := repairTask()
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
	coder := &scriptedLLM{responses: []scriptedResponse{
		{text: "```go\n" + wrongCode + "```\n"},
		{text: correctCode},
	}}

	var reported []domain.Attempt
	final, err := Repair(context.Background(), coder, task, 2, 10*time.Second, func(attempt domain.Attempt) error {
		reported = append(reported, attempt)
		return nil
	})
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if !final.Passed {
		t.Fatalf("Repair() final attempt = %#v, want pass", final)
	}
	if final.N != 2 {
		t.Fatalf("Repair() final attempt number = %d, want 2", final.N)
	}
	if final.Code != correctCode {
		t.Fatalf("Repair() final code = %q, want %q", final.Code, correctCode)
	}
	if final.Output != "" {
		t.Fatalf("Repair() final output = %q, want empty", final.Output)
	}
	if len(reported) != 2 {
		t.Fatalf("reporter calls = %d, want 2", len(reported))
	}
	if reported[0].N != 1 || reported[0].Passed {
		t.Fatalf("first reported attempt = %#v, want failed attempt 1", reported[0])
	}
	if reported[0].Code != wrongCode {
		t.Fatalf("first reported code = %q, want extracted fenced code %q", reported[0].Code, wrongCode)
	}
	if !strings.Contains(reported[0].Output, "F4_TEST_FAILURE_MARKER") {
		t.Fatalf("first reported output = %q, want verifier failure marker", reported[0].Output)
	}
	if reported[1] != final {
		t.Fatalf("second reported attempt = %#v, want final %#v", reported[1], final)
	}
	if len(coder.prompts) != 2 {
		t.Fatalf("coder calls = %d, want 2", len(coder.prompts))
	}
	if !strings.Contains(coder.prompts[0], task.Spec) || !strings.Contains(coder.prompts[0], task.Signature) {
		t.Fatalf("first prompt did not contain task details:\n%s", coder.prompts[0])
	}
	if !strings.Contains(coder.prompts[1], reported[0].Code) {
		t.Fatalf("repair prompt did not contain prior candidate:\n%s", coder.prompts[1])
	}
	if !strings.Contains(coder.prompts[1], reported[0].Output) {
		t.Fatalf("repair prompt did not contain exact verifier output:\n%s", coder.prompts[1])
	}
	for number, promptText := range coder.prompts {
		if strings.Contains(promptText, oracleSourceSentinel) {
			t.Fatalf("coder prompt %d included authored oracle source:\n%s", number+1, promptText)
		}
	}
}

func TestRepairReturnsLastFailureWhenAttemptsAreExhausted(t *testing.T) {
	coder := &scriptedLLM{responses: []scriptedResponse{{text: `package solution

func Increment(value int) int {
	return value - 1
}
`}}}

	final, err := Repair(context.Background(), coder, repairTask(), 1, 10*time.Second, nil)
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if final.N != 1 || final.Passed {
		t.Fatalf("Repair() final attempt = %#v, want failed attempt 1", final)
	}
	if !strings.Contains(final.Output, "F4_TEST_FAILURE_MARKER") {
		t.Fatalf("Repair() final output = %q, want verifier failure marker", final.Output)
	}
	if len(coder.prompts) != 1 {
		t.Fatalf("coder calls = %d, want 1", len(coder.prompts))
	}
}

func TestRepairReturnsLastCompletedAttemptOnProviderError(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	coder := &scriptedLLM{responses: []scriptedResponse{
		{text: `package solution

func Increment(value int) int {
	return value - 1
}
`},
		{err: providerErr},
	}}

	var reported []domain.Attempt
	final, err := Repair(context.Background(), coder, repairTask(), 2, 10*time.Second, func(attempt domain.Attempt) error {
		reported = append(reported, attempt)
		return nil
	})
	if !errors.Is(err, providerErr) {
		t.Fatalf("Repair() error = %v, want provider error", err)
	}
	if final.N != 1 || final.Passed {
		t.Fatalf("Repair() final attempt = %#v, want prior failed attempt", final)
	}
	if len(reported) != 1 || reported[0] != final {
		t.Fatalf("reported attempts = %#v, want only %#v", reported, final)
	}
	if len(coder.prompts) != 2 {
		t.Fatalf("coder calls = %d, want 2", len(coder.prompts))
	}
}

func TestRepairReturnsNoAttemptOnInitialProviderError(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	coder := &scriptedLLM{responses: []scriptedResponse{{err: providerErr}}}

	final, err := Repair(context.Background(), coder, repairTask(), 1, 10*time.Second, nil)
	if !errors.Is(err, providerErr) {
		t.Fatalf("Repair() error = %v, want provider error", err)
	}
	if final != (domain.Attempt{}) {
		t.Fatalf("Repair() final attempt = %#v, want zero attempt", final)
	}
	if len(coder.prompts) != 1 {
		t.Fatalf("coder calls = %d, want 1", len(coder.prompts))
	}
}

func TestRepairReturnsReporterErrorAfterCompletedAttempt(t *testing.T) {
	reporterErr := errors.New("reporter failed")
	coder := &scriptedLLM{responses: []scriptedResponse{{text: `package solution

func Increment(value int) int {
	return value - 1
}
`}}}

	var reported domain.Attempt
	final, err := Repair(context.Background(), coder, repairTask(), 2, 10*time.Second, func(attempt domain.Attempt) error {
		reported = attempt
		return reporterErr
	})
	if !errors.Is(err, reporterErr) {
		t.Fatalf("Repair() error = %v, want reporter error", err)
	}
	if final != reported {
		t.Fatalf("Repair() final attempt = %#v, want reported attempt %#v", final, reported)
	}
	if final.N != 1 || final.Passed {
		t.Fatalf("Repair() final attempt = %#v, want failed attempt 1", final)
	}
	if len(coder.prompts) != 1 {
		t.Fatalf("coder calls = %d, want 1", len(coder.prompts))
	}
}

func TestRepairRejectsInvalidInputsBeforeCallingCoder(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		coder       bool
		task        domain.Task
		maxAttempts int
		timeout     time.Duration
	}{
		{
			name:        "nil context",
			ctx:         nil,
			coder:       true,
			task:        repairTask(),
			maxAttempts: 1,
			timeout:     time.Second,
		},
		{
			name:        "nil coder",
			ctx:         context.Background(),
			coder:       false,
			task:        repairTask(),
			maxAttempts: 1,
			timeout:     time.Second,
		},
		{
			name:        "zero attempt cap",
			ctx:         context.Background(),
			coder:       true,
			task:        repairTask(),
			maxAttempts: 0,
			timeout:     time.Second,
		},
		{
			name:        "negative verifier timeout",
			ctx:         context.Background(),
			coder:       true,
			task:        repairTask(),
			maxAttempts: 1,
			timeout:     -time.Second,
		},
		{
			name:        "missing authored oracle",
			ctx:         context.Background(),
			coder:       true,
			task:        domain.Task{Spec: "unused", Signature: "func Unused()"},
			maxAttempts: 1,
			timeout:     time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coder := &scriptedLLM{}
			var model llm.LLM = coder
			if !tt.coder {
				model = nil
			}

			final, err := Repair(tt.ctx, model, tt.task, tt.maxAttempts, tt.timeout, nil)
			if err == nil {
				t.Fatal("Repair() error = nil, want validation error")
			}
			if final != (domain.Attempt{}) {
				t.Fatalf("Repair() final attempt = %#v, want zero attempt", final)
			}
			if len(coder.prompts) != 0 {
				t.Fatalf("coder calls = %d, want 0", len(coder.prompts))
			}
		})
	}
}

func TestRepairReturnsCallerCancellationBeforeCallingCoder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	coder := &scriptedLLM{}

	final, err := Repair(ctx, coder, repairTask(), 1, time.Second, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Repair() error = %v, want context.Canceled", err)
	}
	if final != (domain.Attempt{}) {
		t.Fatalf("Repair() final attempt = %#v, want zero attempt", final)
	}
	if len(coder.prompts) != 0 {
		t.Fatalf("coder calls = %d, want 0", len(coder.prompts))
	}
}

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

type scriptedResponse struct {
	text string
	err  error
}

type scriptedLLM struct {
	responses []scriptedResponse
	prompts   []string
}

func (coder *scriptedLLM) Complete(_ context.Context, promptText string) (string, error) {
	coder.prompts = append(coder.prompts, promptText)
	if len(coder.responses) == 0 {
		return "", errors.New("unexpected completion request")
	}

	response := coder.responses[0]
	coder.responses = coder.responses[1:]
	return response.text, response.err
}

func repairTask() domain.Task {
	return domain.Task{
		Name:      "increment",
		Spec:      "Return the input integer increased by one.",
		Signature: "func Increment(value int) int",
		TestCode: `package solution

import "testing"

// F4_ORACLE_SOURCE_SENTINEL
func TestIncrement(t *testing.T) {
	if got := Increment(2); got != 3 {
		t.Fatalf("F4_TEST_FAILURE_MARKER: Increment(2) = %d, want 3", got)
	}
}
`,
	}
}
