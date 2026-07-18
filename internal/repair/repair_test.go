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

const (
	oracleSourceSentinel          = "F4_ORACLE_SOURCE_SENTINEL"
	generatedOracleSourceSentinel = "F15_GENERATED_ORACLE_SOURCE_SENTINEL"
)

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
	final, err := Repair(context.Background(), coder, nil, task, 2, 10*time.Second, 0, ProgressReporter{
		AttemptFinished: func(attempt domain.Attempt) error {
			reported = append(reported, attempt)
			return nil
		},
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

	final, err := Repair(context.Background(), coder, nil, repairTask(), 1, 10*time.Second, 0, ProgressReporter{})
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
	final, err := Repair(context.Background(), coder, nil, repairTask(), 2, 10*time.Second, 0, ProgressReporter{
		AttemptFinished: func(attempt domain.Attempt) error {
			reported = append(reported, attempt)
			return nil
		},
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

	final, err := Repair(context.Background(), coder, nil, repairTask(), 1, 10*time.Second, 0, ProgressReporter{})
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
	final, err := Repair(context.Background(), coder, nil, repairTask(), 2, 10*time.Second, 0, ProgressReporter{
		AttemptFinished: func(attempt domain.Attempt) error {
			reported = attempt
			return reporterErr
		},
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

			final, err := Repair(tt.ctx, model, nil, tt.task, tt.maxAttempts, tt.timeout, 0, ProgressReporter{})
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

	final, err := Repair(ctx, coder, nil, repairTask(), 1, time.Second, 0, ProgressReporter{})
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

func TestRepairResolvesGeneratedOracleBeforeCoderAndFreezesIt(t *testing.T) {
	task := generatedRepairTask()
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
	oracle := generatedIncrementOracle()

	var order []string
	tester := &scriptedLLM{
		responses: []scriptedResponse{
			{text: "```go\n" + oracle + "```\n"},
			{text: "this response must stay unused after the oracle is frozen"},
		},
		onComplete: func(string) { order = append(order, "tester") },
	}
	coder := &scriptedLLM{
		responses:  []scriptedResponse{{text: wrongCode}, {text: correctCode}},
		onComplete: func(string) { order = append(order, "coder") },
	}

	var resolved []string
	var reported []domain.Attempt
	final, err := Repair(context.Background(), coder, tester, task, 2, 10*time.Second, 2, ProgressReporter{
		OracleResolved: func(testCode string) error {
			order = append(order, "oracle")
			resolved = append(resolved, testCode)
			return nil
		},
		AttemptFinished: func(attempt domain.Attempt) error {
			order = append(order, "attempt")
			reported = append(reported, attempt)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if !final.Passed || final.N != 2 {
		t.Fatalf("Repair() final = %#v, want passing attempt 2", final)
	}
	if len(resolved) != 1 || resolved[0] != oracle {
		t.Fatalf("resolved oracle = %#v, want exactly %q", resolved, oracle)
	}
	if len(reported) != 2 {
		t.Fatalf("reported attempts = %d, want 2", len(reported))
	}
	wantOrder := []string{"tester", "oracle", "coder", "attempt", "coder", "attempt"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("operation order = %v, want %v", order, wantOrder)
	}
	if len(tester.prompts) != 1 {
		t.Fatalf("tester calls = %d, want one accepted frozen oracle", len(tester.prompts))
	}
	if !strings.Contains(tester.prompts[0], task.Spec) || !strings.Contains(tester.prompts[0], task.Signature) {
		t.Fatalf("tester prompt did not contain task details:\n%s", tester.prompts[0])
	}
	if strings.Contains(tester.prompts[0], wrongCode) || strings.Contains(tester.prompts[0], task.TestCode) {
		t.Fatalf("tester prompt included code or stale test source:\n%s", tester.prompts[0])
	}
	for number, promptText := range coder.prompts {
		if strings.Contains(promptText, generatedOracleSourceSentinel) || strings.Contains(promptText, task.TestCode) {
			t.Fatalf("coder prompt %d included generated oracle source:\n%s", number+1, promptText)
		}
	}
}

func TestRepairRetriesRejectedGeneratedOracleBeforeCallingCoder(t *testing.T) {
	task := generatedRepairTask()
	rejectedOracle := `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if got := Increment(1); got == 99 {
		t.Fatal("unexpected sentinel")
	}
	UndefinedOracleSymbol()
}
`
	acceptedOracle := generatedIncrementOracle()
	correctCode := `package solution

func Increment(value int) int {
	return value + 1
}
`

	var order []string
	tester := &scriptedLLM{
		responses:  []scriptedResponse{{text: rejectedOracle}, {text: acceptedOracle}},
		onComplete: func(string) { order = append(order, "tester") },
	}
	coder := &scriptedLLM{
		responses:  []scriptedResponse{{text: correctCode}},
		onComplete: func(string) { order = append(order, "coder") },
	}

	var resolved string
	final, err := Repair(context.Background(), coder, tester, task, 1, 10*time.Second, 2, ProgressReporter{
		OracleResolved: func(testCode string) error {
			order = append(order, "oracle")
			resolved = testCode
			return nil
		},
		AttemptFinished: func(domain.Attempt) error {
			order = append(order, "attempt")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if !final.Passed {
		t.Fatalf("Repair() final = %#v, want pass", final)
	}
	if resolved != acceptedOracle {
		t.Fatalf("resolved oracle = %q, want accepted candidate %q", resolved, acceptedOracle)
	}
	wantOrder := []string{"tester", "tester", "oracle", "coder", "attempt"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("operation order = %v, want %v", order, wantOrder)
	}
	if len(tester.prompts) != 2 {
		t.Fatalf("tester calls = %d, want 2", len(tester.prompts))
	}
	for number, promptText := range tester.prompts {
		if strings.Contains(promptText, rejectedOracle) || strings.Contains(promptText, correctCode) {
			t.Fatalf("tester prompt %d leaked a rejected oracle or candidate:\n%s", number+1, promptText)
		}
	}
	if len(coder.prompts) != 1 {
		t.Fatalf("coder calls = %d, want 1 after oracle acceptance", len(coder.prompts))
	}
}

func TestRepairReturnsTypedOracleFailureBeforeCallingCoder(t *testing.T) {
	task := generatedRepairTask()
	badOracle := `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if got := Increment(1); got == 99 {
		t.Fatal("unexpected sentinel")
	}
	UndefinedOracleSymbol()
}
`
	tester := &scriptedLLM{responses: []scriptedResponse{{text: badOracle}, {text: badOracle}}}
	coder := &scriptedLLM{}

	oracleNotifications := 0
	attemptNotifications := 0
	final, err := Repair(context.Background(), coder, tester, task, 1, 10*time.Second, 2, ProgressReporter{
		OracleResolved: func(string) error {
			oracleNotifications++
			return nil
		},
		AttemptFinished: func(domain.Attempt) error {
			attemptNotifications++
			return nil
		},
	})
	if final != (domain.Attempt{}) {
		t.Fatalf("Repair() final = %#v, want zero attempt", final)
	}
	var oracleErr *OracleFailureError
	if !errors.As(err, &oracleErr) {
		t.Fatalf("Repair() error = %v, want OracleFailureError", err)
	}
	if oracleErr.Attempts != 2 {
		t.Fatalf("OracleFailureError attempts = %d, want 2", oracleErr.Attempts)
	}
	if !strings.Contains(oracleErr.Output, "UndefinedOracleSymbol") {
		t.Fatalf("OracleFailureError output = %q, want preflight compiler diagnostic", oracleErr.Output)
	}
	if len(tester.prompts) != 2 {
		t.Fatalf("tester calls = %d, want 2", len(tester.prompts))
	}
	if len(coder.prompts) != 0 {
		t.Fatalf("coder calls = %d, want 0", len(coder.prompts))
	}
	if oracleNotifications != 0 || attemptNotifications != 0 {
		t.Fatalf("progress callbacks = oracle %d, attempts %d, want none", oracleNotifications, attemptNotifications)
	}
}

func TestRepairReturnsTesterProviderErrorWithoutCoder(t *testing.T) {
	providerErr := errors.New("tester provider unavailable")
	tester := &scriptedLLM{responses: []scriptedResponse{{err: providerErr}}}
	coder := &scriptedLLM{}

	final, err := Repair(context.Background(), coder, tester, generatedRepairTask(), 1, 10*time.Second, 1, ProgressReporter{})
	if final != (domain.Attempt{}) {
		t.Fatalf("Repair() final = %#v, want zero attempt", final)
	}
	if !errors.Is(err, providerErr) {
		t.Fatalf("Repair() error = %v, want tester provider error", err)
	}
	var oracleErr *OracleFailureError
	if errors.As(err, &oracleErr) {
		t.Fatalf("Repair() error = %v, must not classify a tester provider error as OracleFailureError", err)
	}
	if len(coder.prompts) != 0 {
		t.Fatalf("coder calls = %d, want 0", len(coder.prompts))
	}
}

func TestRepairRejectsInvalidGeneratedOracleConfigurationBeforeModels(t *testing.T) {
	tests := []struct {
		name               string
		tester             llm.LLM
		maxOracleAttempts  int
		mutateTask         func(*domain.Task)
		wantTesterRequests int
	}{
		{
			name:              "nil tester",
			maxOracleAttempts: 1,
			mutateTask:        func(*domain.Task) {},
		},
		{
			name:              "zero oracle attempt cap",
			tester:            &scriptedLLM{},
			maxOracleAttempts: 0,
			mutateTask:        func(*domain.Task) {},
		},
		{
			name:              "invalid signature",
			tester:            &scriptedLLM{},
			maxOracleAttempts: 1,
			mutateTask: func(task *domain.Task) {
				task.Signature = "this is not a Go function signature"
			},
		},
		{
			name:              "signature with an unknown type",
			tester:            &scriptedLLM{},
			maxOracleAttempts: 1,
			mutateTask: func(task *domain.Task) {
				task.Signature = "func Increment(value MissingType) int"
			},
		},
		{
			name:              "unknown oracle mode",
			tester:            &scriptedLLM{},
			maxOracleAttempts: 1,
			mutateTask: func(task *domain.Task) {
				task.Oracle = domain.OracleMode("unknown")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := generatedRepairTask()
			tt.mutateTask(&task)
			coder := &scriptedLLM{}

			final, err := Repair(context.Background(), coder, tt.tester, task, 1, time.Second, tt.maxOracleAttempts, ProgressReporter{})
			if err == nil {
				t.Fatal("Repair() error = nil, want validation error")
			}
			if final != (domain.Attempt{}) {
				t.Fatalf("Repair() final = %#v, want zero attempt", final)
			}
			if len(coder.prompts) != 0 {
				t.Fatalf("coder calls = %d, want 0", len(coder.prompts))
			}
			if tester, ok := tt.tester.(*scriptedLLM); ok && len(tester.prompts) != tt.wantTesterRequests {
				t.Fatalf("tester calls = %d, want %d", len(tester.prompts), tt.wantTesterRequests)
			}
		})
	}
}

func TestRepairReturnsOracleReporterErrorBeforeCallingCoder(t *testing.T) {
	reporterErr := errors.New("could not persist frozen oracle")
	coder := &scriptedLLM{}

	final, err := Repair(context.Background(), coder, nil, repairTask(), 1, time.Second, 0, ProgressReporter{
		OracleResolved: func(string) error { return reporterErr },
	})
	if !errors.Is(err, reporterErr) {
		t.Fatalf("Repair() error = %v, want reporter error", err)
	}
	if final != (domain.Attempt{}) {
		t.Fatalf("Repair() final = %#v, want zero attempt", final)
	}
	if len(coder.prompts) != 0 {
		t.Fatalf("coder calls = %d, want 0", len(coder.prompts))
	}
}

func TestRepairAuthoredModeAllowsNilTesterAndReportsFrozenOracle(t *testing.T) {
	task := repairTask()
	task.Oracle = domain.OracleAuthored
	correctCode := `package solution

func Increment(value int) int {
	return value + 1
}
`
	coder := &scriptedLLM{responses: []scriptedResponse{{text: correctCode}}}

	var resolved []string
	final, err := Repair(context.Background(), coder, nil, task, 1, 10*time.Second, 0, ProgressReporter{
		OracleResolved: func(testCode string) error {
			resolved = append(resolved, testCode)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Repair() error = %v", err)
	}
	if !final.Passed {
		t.Fatalf("Repair() final = %#v, want pass", final)
	}
	if len(resolved) != 1 || resolved[0] != task.TestCode {
		t.Fatalf("resolved authored oracle = %#v, want one fixed test source", resolved)
	}
}

func TestPreflightOracleRequiresRunnableTestFunction(t *testing.T) {
	stub, _, err := signatureStub("func Increment(value int) int")
	if err != nil {
		t.Fatalf("signatureStub() error = %v", err)
	}
	oracle := `package solution

import "testing"

func Testlowercase(t *testing.T) {}
`
	accepted, output, err := preflightOracle(context.Background(), stub, oracle, time.Second)
	if err != nil {
		t.Fatalf("preflightOracle() error = %v", err)
	}
	if accepted {
		t.Fatal("preflightOracle() accepted = true, want rejection")
	}
	if !strings.Contains(output, "runnable Test function") {
		t.Fatalf("preflightOracle() output = %q, want missing-test explanation", output)
	}
}

func TestPreflightOracleCompilesWithoutRunningTestBodies(t *testing.T) {
	stub, _, err := signatureStub("func Increment(value int) int")
	if err != nil {
		t.Fatalf("signatureStub() error = %v", err)
	}
	oracle := `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if false {
		t.Fatal("ORACLE_TEST_FAILURE_METHOD_PRESENT")
	}
	panic("ORACLE_TEST_BODY_RAN")
}
`
	accepted, output, err := preflightOracle(context.Background(), stub, oracle, time.Second)
	if err != nil {
		t.Fatalf("preflightOracle() error = %v", err)
	}
	if !accepted {
		t.Fatalf("preflightOracle() accepted = false, output = %q; want compilation without executing the test", output)
	}
}

func TestPreflightOracleRejectsBuildConstraintsAndProcessHooks(t *testing.T) {
	stub, _, err := signatureStub("func Increment(value int) int")
	if err != nil {
		t.Fatalf("signatureStub() error = %v", err)
	}

	tests := []struct {
		name       string
		oracle     string
		wantOutput string
	}{
		{
			name: "go build constraint",
			oracle: `//go:build ignore

package solution

import "testing"

func TestIncrement(t *testing.T) {}
`,
			wantOutput: "build constraints",
		},
		{
			name: "legacy build constraint",
			oracle: `// +build ignore

package solution

import "testing"

func TestIncrement(t *testing.T) {}
`,
			wantOutput: "build constraints",
		},
		{
			name: "test main",
			oracle: `package solution

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(0) }

func TestIncrement(t *testing.T) {}
`,
			wantOutput: "TestMain",
		},
		{
			name: "init hook",
			oracle: `package solution

import "testing"

func init() {}

func TestIncrement(t *testing.T) {}
`,
			wantOutput: "TestMain or init",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accepted, output, err := preflightOracle(context.Background(), stub, tt.oracle, time.Second)
			if err != nil {
				t.Fatalf("preflightOracle() error = %v", err)
			}
			if accepted {
				t.Fatal("preflightOracle() accepted = true, want rejection")
			}
			if !strings.Contains(output, tt.wantOutput) {
				t.Fatalf("preflightOracle() output = %q, want %q", output, tt.wantOutput)
			}
		})
	}
}

func TestPreflightGeneratedOracleRejectsObviousBypasses(t *testing.T) {
	stub, functionName, err := signatureStub("func Increment(value int) int")
	if err != nil {
		t.Fatalf("signatureStub() error = %v", err)
	}

	tests := []struct {
		name       string
		oracle     string
		wantOutput string
	}{
		{
			name: "does not call the required function",
			oracle: `package solution

import "testing"

func TestSomething(t *testing.T) {
	if 1 != 1 {
		t.Fatal("impossible")
	}
}
`,
			wantOutput: "call required function Increment",
		},
		{
			name: "does not use a testing failure method",
			oracle: `package solution

import "testing"

func TestIncrement(t *testing.T) {
	Increment(1)
}
`,
			wantOutput: "testing failure method",
		},
		{
			name: "skips the test",
			oracle: `package solution

import "testing"

func TestIncrement(t *testing.T) {
	Increment(1)
	t.Skip("not a real assertion")
}
`,
			wantOutput: "must not skip tests",
		},
		{
			name: "calls os exit",
			oracle: `package solution

import (
	"os"
	"testing"
)

func TestIncrement(t *testing.T) {
	Increment(1)
	if false {
		t.Fatal("unreachable")
	}
	os.Exit(0)
}
`,
			wantOutput: "call os.Exit",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			accepted, output, err := preflightOracleForFunction(context.Background(), stub, test.oracle, time.Second, functionName)
			if err != nil {
				t.Fatalf("preflightOracleForFunction() error = %v", err)
			}
			if accepted {
				t.Fatal("preflightOracleForFunction() accepted = true, want rejection")
			}
			if !strings.Contains(output, test.wantOutput) {
				t.Fatalf("preflightOracleForFunction() output = %q, want %q", output, test.wantOutput)
			}
		})
	}
}

func TestPreflightOracleRejectsNonPositiveTimeout(t *testing.T) {
	accepted, output, err := preflightOracle(context.Background(), "package solution\n", "package solution\n", 0)
	if err == nil {
		t.Fatal("preflightOracle() error = nil, want validation error")
	}
	if accepted || output != "" {
		t.Fatalf("preflightOracle() = (%t, %q, %v), want false, empty output, error", accepted, output, err)
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
	responses  []scriptedResponse
	prompts    []string
	onComplete func(prompt string)
}

func (coder *scriptedLLM) Complete(_ context.Context, promptText string) (string, error) {
	coder.prompts = append(coder.prompts, promptText)
	if coder.onComplete != nil {
		coder.onComplete(promptText)
	}
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

func generatedRepairTask() domain.Task {
	task := repairTask()
	task.Oracle = domain.OracleGenerated
	task.TestCode = "STALE_CALLER_PROVIDED_ORACLE_MUST_NOT_BE_USED"
	return task
}

func generatedIncrementOracle() string {
	return `package solution

import "testing"

// F15_GENERATED_ORACLE_SOURCE_SENTINEL
func TestIncrement(t *testing.T) {
	if got := Increment(2); got != 3 {
		t.Fatalf("F15_TEST_FAILURE_MARKER: Increment(2) = %d, want 3", got)
	}
}
`
}
