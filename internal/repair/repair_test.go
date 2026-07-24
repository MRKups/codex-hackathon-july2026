package repair

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/verification"
)

const oracleSourceSentinel = "F4_ORACLE_SOURCE_SENTINEL"

func TestRepairRetriesWithVerifierFeedbackWithoutLeakingBundle(t *testing.T) {
	task := repairTask()
	bundle := sealedBundle(t, task)
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
	final, err := RepairWithConfig(context.Background(), coder, candidateRequest(task, bundle), Config{
		MaxAttempts: 2,
		TestTimeout: 10 * time.Second,
	}, ProgressReporter{
		AttemptFinished: func(attempt domain.Attempt) error {
			reported = append(reported, attempt)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RepairWithConfig() error = %v", err)
	}
	if !final.Passed || final.N != 2 || final.Code != correctCode || final.Output != "" {
		t.Fatalf("RepairWithConfig() final = %#v, want passing second candidate", final)
	}
	if len(reported) != 2 || reported[0].Passed || !reported[1].Passed {
		t.Fatalf("reported attempts = %#v, want failed then passed", reported)
	}
	if !strings.Contains(reported[0].Output, "F4_TEST_FAILURE_MARKER") {
		t.Fatalf("first output = %q, want verifier failure marker", reported[0].Output)
	}
	if len(coder.prompts) != 2 {
		t.Fatalf("coder calls = %d, want 2", len(coder.prompts))
	}
	if !strings.Contains(coder.prompts[0], task.Spec) || !strings.Contains(coder.prompts[0], task.Signature) {
		t.Fatalf("first candidate prompt omitted task details:\n%s", coder.prompts[0])
	}
	if !strings.Contains(coder.prompts[1], wrongCode) || !strings.Contains(coder.prompts[1], reported[0].Output) {
		t.Fatalf("repair prompt omitted prior candidate or feedback:\n%s", coder.prompts[1])
	}
	for number, promptText := range coder.prompts {
		if strings.Contains(promptText, oracleSourceSentinel) || strings.Contains(promptText, "Oracle Rulebook") {
			t.Fatalf("candidate prompt %d leaked oracle material:\n%s", number+1, promptText)
		}
	}
}

func TestRepairReturnsLastFailureWhenAttemptsAreExhausted(t *testing.T) {
	task := repairTask()
	coder := &scriptedLLM{responses: []scriptedResponse{{text: `package solution

func Increment(value int) int {
	return value - 1
}
`}}}

	final, err := RepairWithConfig(context.Background(), coder, candidateRequest(task, sealedBundle(t, task)), Config{
		MaxAttempts: 1,
		TestTimeout: 10 * time.Second,
	}, ProgressReporter{})
	if err != nil {
		t.Fatalf("RepairWithConfig() error = %v", err)
	}
	if final.N != 1 || final.Passed || !strings.Contains(final.Output, "F4_TEST_FAILURE_MARKER") {
		t.Fatalf("final attempt = %#v, want failed first attempt", final)
	}
}

func TestRepairReturnsLastCompletedAttemptOnProviderOrReporterFailure(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	task := repairTask()
	coder := &scriptedLLM{responses: []scriptedResponse{
		{text: `package solution

func Increment(value int) int { return value - 1 }
`},
		{err: providerErr},
	}}
	final, err := RepairWithConfig(context.Background(), coder, candidateRequest(task, sealedBundle(t, task)), Config{
		MaxAttempts: 2,
		TestTimeout: 10 * time.Second,
	}, ProgressReporter{})
	if !errors.Is(err, providerErr) {
		t.Fatalf("provider failure error = %v, want %v", err, providerErr)
	}
	if final.N != 1 || final.Passed {
		t.Fatalf("provider failure final = %#v, want first failed attempt", final)
	}

	reporterErr := errors.New("snapshot write failed")
	coder = &scriptedLLM{responses: []scriptedResponse{{text: `package solution

func Increment(value int) int { return value - 1 }
`}}}
	final, err = RepairWithConfig(context.Background(), coder, candidateRequest(task, sealedBundle(t, task)), Config{
		MaxAttempts: 1,
		TestTimeout: 10 * time.Second,
	}, ProgressReporter{AttemptFinished: func(domain.Attempt) error { return reporterErr }})
	if !errors.Is(err, reporterErr) {
		t.Fatalf("reporter failure error = %v, want %v", err, reporterErr)
	}
	if final.N != 1 || final.Passed {
		t.Fatalf("reporter failure final = %#v, want first failed attempt", final)
	}
}

func TestRepairReturnsNoAttemptBeforeAnyCompletedVerification(t *testing.T) {
	task := repairTask()
	providerErr := errors.New("provider unavailable")
	coder := &scriptedLLM{responses: []scriptedResponse{{err: providerErr}}}
	final, err := RepairWithConfig(context.Background(), coder, candidateRequest(task, sealedBundle(t, task)), Config{
		MaxAttempts: 1,
		TestTimeout: time.Second,
	}, ProgressReporter{})
	if !errors.Is(err, providerErr) || final != (domain.Attempt{}) {
		t.Fatalf("initial provider result = (%#v, %v), want zero attempt and provider error", final, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	coder = &scriptedLLM{}
	final, err = RepairWithConfig(ctx, coder, candidateRequest(task, sealedBundle(t, task)), Config{
		MaxAttempts: 1,
		TestTimeout: time.Second,
	}, ProgressReporter{})
	if !errors.Is(err, context.Canceled) || final != (domain.Attempt{}) || len(coder.prompts) != 0 {
		t.Fatalf("canceled repair result = (%#v, %v), prompts=%d; want zero/no prompt/context canceled", final, err, len(coder.prompts))
	}
}

func TestRepairRejectsInvalidInputsAndBundleBeforeCoder(t *testing.T) {
	task := repairTask()
	validBundle := sealedBundle(t, task)
	tampered := validBundle
	tampered.TestCode += "// drift\n"

	tests := []struct {
		name   string
		ctx    context.Context
		coder  llmLike
		bundle domain.VerificationBundle
		config Config
	}{
		{
			name:   "nil context",
			ctx:    nil,
			coder:  &scriptedLLM{},
			bundle: validBundle,
			config: Config{MaxAttempts: 1, TestTimeout: time.Second},
		},
		{
			name:   "nil coder",
			ctx:    context.Background(),
			coder:  nil,
			bundle: validBundle,
			config: Config{MaxAttempts: 1, TestTimeout: time.Second},
		},
		{
			name:   "zero candidate cap",
			ctx:    context.Background(),
			coder:  &scriptedLLM{},
			bundle: validBundle,
			config: Config{TestTimeout: time.Second},
		},
		{
			name:   "bad verifier timeout",
			ctx:    context.Background(),
			coder:  &scriptedLLM{},
			bundle: validBundle,
			config: Config{MaxAttempts: 1},
		},
		{
			name:   "tampered frozen bundle",
			ctx:    context.Background(),
			coder:  &scriptedLLM{},
			bundle: tampered,
			config: Config{MaxAttempts: 1, TestTimeout: time.Second},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var coder interface {
				Complete(context.Context, string) (string, error)
			} = tt.coder
			final, err := RepairWithConfig(tt.ctx, coder, candidateRequest(task, tt.bundle), tt.config, ProgressReporter{})
			if err == nil {
				t.Fatal("RepairWithConfig() error = nil, want validation error")
			}
			if final != (domain.Attempt{}) {
				t.Fatalf("final = %#v, want zero attempt", final)
			}
			if model, ok := tt.coder.(*scriptedLLM); ok && len(model.prompts) != 0 {
				t.Fatalf("coder calls = %d, want 0", len(model.prompts))
			}
		})
	}
}

// llmLike keeps the input-validation table readable while still matching llm.LLM structurally.
type llmLike interface {
	Complete(context.Context, string) (string, error)
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

func Solve(left, right int) int { return left + right }
`

	passed, output, err := runTests(context.Background(), task, code, 10*time.Second)
	if err != nil || !passed || output != "" {
		t.Fatalf("runTests() = (%t, %q, %v), want (true, empty, nil)", passed, output, err)
	}
}

func TestRunBundleTestsExecutesWithoutSourcesOrProviderEnvironment(t *testing.T) {
	t.Setenv("LLM_API_KEY", "must-not-reach-candidate")
	task := domain.Task{Spec: "Return two.", Signature: "func Solve() int"}
	bundle, err := verification.AuthoredSource(task, `package solution

import (
	"os"
	"testing"
)

func TestSolve(t *testing.T) {
	if _, err := os.Stat("solution.go"); err == nil {
		t.Fatal("test source could read candidate source")
	}
	if _, err := os.Stat("solution_test.go"); err == nil {
		t.Fatal("test source could read its own oracle source")
	}
	if got := Solve(); got != 2 {
		t.Fatalf("Solve() = %d, want 2", got)
	}
}
`)
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}
	code := `package solution

import "os"

func Solve() int {
	if _, err := os.Stat("solution_test.go"); err == nil || os.Getenv("LLM_API_KEY") != "" {
		return 1
	}
	return 2
}
`

	passed, output, err := runBundleTests(context.Background(), bundle, code, 10*time.Second)
	if err != nil || !passed || output != "" {
		t.Fatalf("runBundleTests() = (%t, %q, %v), want (true, empty, nil)", passed, output, err)
	}
}

func TestRunBundleTestsRejectsSourceBypasses(t *testing.T) {
	task := domain.Task{Spec: "Return one.", Signature: "func Solve() int"}
	bundle, err := verification.AuthoredSource(task, `package solution

import "testing"

func TestSolve(t *testing.T) {
	if got := Solve(); got != 1 {
		t.Fatalf("Solve() = %d, want 1", got)
	}
}
`)
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}

	tests := []struct {
		name       string
		code       string
		wantOutput string
	}{
		{
			name: "init exit",
			code: `package solution

import "os"

func init() { os.Exit(0) }
func Solve() int { return 1 }
`,
			wantOutput: "did not complete",
		},
		{
			name: "embed directive",
			code: `package solution

import _ "embed"

//go:embed solution_test.go
var frozenOracle string

func Solve() int { return 1 }
`,
			wantOutput: "go:embed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed, output, err := runBundleTests(context.Background(), bundle, tt.code, 10*time.Second)
			if err != nil {
				t.Fatalf("runBundleTests() error = %v", err)
			}
			if passed || !strings.Contains(output, tt.wantOutput) {
				t.Fatalf("runBundleTests() = (%t, %q), want failed %q", passed, output, tt.wantOutput)
			}
		})
	}
}

func TestVerifierFeedbackAndTimeoutContracts(t *testing.T) {
	buildTask := authoredTask(`package solution

import "testing"

func TestVerifierShouldNotRun(t *testing.T) { t.Fatal("TEST_RAN_MARKER") }
`)
	passed, output, err := runTests(context.Background(), buildTask, "package solution\n\nfunc Solve( {\n", 10*time.Second)
	if err != nil || passed || !strings.Contains(output, "solution.go") || strings.Contains(output, "TEST_RAN_MARKER") {
		t.Fatalf("build failure = (%t, %q, %v)", passed, output, err)
	}

	failureTask := authoredTask(`package solution

import "testing"

func TestSolve(t *testing.T) {
	if got := Solve(2, 3); got != 5 { t.Fatalf("TEST_FAILURE_MARKER: got %d", got) }
}
`)
	passed, output, err = runTests(context.Background(), failureTask, "package solution\n\nfunc Solve(left, right int) int { return left - right }\n", 10*time.Second)
	if err != nil || passed || !strings.Contains(output, "TEST_FAILURE_MARKER") {
		t.Fatalf("test failure = (%t, %q, %v)", passed, output, err)
	}

	malformedTask := authoredTask(`package solution

func TestBroken(t *testing.T) {}
`)
	passed, output, err = runTests(context.Background(), malformedTask, "package solution\n\nfunc Solve() {}\n", 10*time.Second)
	if err != nil || passed || !strings.Contains(output, "solution_test.go") {
		t.Fatalf("malformed frozen test = (%t, %q, %v)", passed, output, err)
	}

	timeoutTask := authoredTask(`package solution

import "testing"

func TestSolve(t *testing.T) { Solve() }
`)
	passed, output, err = runTests(context.Background(), timeoutTask, "package solution\n\nfunc Solve() { for {} }\n", 5*time.Second)
	if err != nil || passed || output != verifierTimeoutOutput {
		t.Fatalf("timeout = (%t, %q, %v), want failed timeout feedback", passed, output, err)
	}
}

func TestLimitedOutputTruncatesVerifierFeedback(t *testing.T) {
	output := &limitedOutput{}
	contents := strings.Repeat("x", maxVerifierOutputBytes+1)
	if written, err := output.Write([]byte(contents)); err != nil || written != len(contents) {
		t.Fatalf("limitedOutput.Write() = (%d, %v), want (%d, nil)", written, err, len(contents))
	}
	got := output.String()
	if !strings.HasSuffix(got, verifierOutputTruncation) || len(got) != maxVerifierOutputBytes+len(verifierOutputTruncation) {
		t.Fatalf("limited output = %d bytes, want capped output plus marker", len(got))
	}
}

func TestRunTestsReturnsCallerCancellationAndInvalidInputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	passed, output, err := runTests(ctx, authoredTask("package solution\n"), "package solution\n", time.Second)
	if !errors.Is(err, context.Canceled) || passed || output != "" {
		t.Fatalf("canceled runTests() = (%t, %q, %v)", passed, output, err)
	}
	passed, output, err = runTests(context.Background(), domain.Task{}, "package solution\n", time.Second)
	if err == nil || passed || output != "" {
		t.Fatalf("invalid runTests() = (%t, %q, %v), want error", passed, output, err)
	}
	passed, output, err = runTests(context.Background(), authoredTask("package solution\n"), "package solution\n", 0)
	if err == nil || passed || output != "" {
		t.Fatalf("zero-timeout runTests() = (%t, %q, %v), want error", passed, output, err)
	}
}

func TestRunTestsReturnsCommandLaunchError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	passed, output, err := runTests(context.Background(), authoredTask("package solution\n"), "package solution\n", time.Second)
	if err == nil || passed || output != "" {
		t.Fatalf("missing-go runTests() = (%t, %q, %v), want launch error", passed, output, err)
	}
}

func sealedBundle(t *testing.T, task domain.Task) domain.VerificationBundle {
	t.Helper()
	bundle, err := verification.AuthoredSource(task, task.TestCode)
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}
	return bundle
}

func candidateRequest(task domain.Task, bundle domain.VerificationBundle) CandidateRequest {
	return CandidateRequest{Spec: task.Spec, Signature: task.Signature, Bundle: bundle}
}

func authoredTask(testCode string) domain.Task {
	return domain.Task{TestCode: testCode}
}

func repairTask() domain.Task {
	return domain.Task{
		Name:      "increment",
		Spec:      "Return the input integer increased by one.",
		Signature: "func Increment(value int) int",
		Oracle:    domain.OracleAuthored,
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

type scriptedResponse struct {
	text string
	err  error
}

type scriptedLLM struct {
	responses []scriptedResponse
	prompts   []string
}

func (model *scriptedLLM) Complete(_ context.Context, promptText string) (string, error) {
	model.prompts = append(model.prompts, promptText)
	if len(model.responses) == 0 {
		return "", errors.New("unexpected completion request")
	}
	response := model.responses[0]
	model.responses = model.responses[1:]
	return response.text, response.err
}
