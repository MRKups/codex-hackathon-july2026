// Package repair verifies generated Go candidates and runs the repair loop.
package repair

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/prompt"
)

const verifierTimeoutOutput = "verifier timeout — generated code may have hung"

// AttemptReporter receives each completed verification synchronously. A nil reporter disables
// progress reporting; a reporter error stops the loop and is returned to the caller.
type AttemptReporter func(domain.Attempt) error

// Repair generates and verifies up to maxAttempts candidates for an authored-oracle task. It
// returns the last completed attempt and only returns an error for caller or infrastructure
// failures. A failed verifier result is a normal attempt, not an error.
func Repair(
	ctx context.Context,
	coder llm.LLM,
	task domain.Task,
	maxAttempts int,
	testTimeout time.Duration,
	report AttemptReporter,
) (domain.Attempt, error) {
	if ctx == nil {
		return domain.Attempt{}, errors.New("repair context is required")
	}
	if coder == nil {
		return domain.Attempt{}, errors.New("coder is required")
	}
	if maxAttempts <= 0 {
		return domain.Attempt{}, errors.New("max attempts must be greater than zero")
	}
	if testTimeout <= 0 {
		return domain.Attempt{}, errors.New("verifier timeout must be greater than zero")
	}
	if strings.TrimSpace(task.TestCode) == "" {
		return domain.Attempt{}, errors.New("task test code is required")
	}
	if err := ctx.Err(); err != nil {
		return domain.Attempt{}, err
	}

	var last domain.Attempt
	for attemptNumber := 1; attemptNumber <= maxAttempts; attemptNumber++ {
		code, err := generate(ctx, coder, task.Spec, task.Signature, last)
		if err != nil {
			return last, err
		}

		passed, output, err := runTests(ctx, task, code, testTimeout)
		if err != nil {
			return last, err
		}

		last = domain.Attempt{
			N:      attemptNumber,
			Code:   code,
			Passed: passed,
			Output: output,
		}
		if report != nil {
			if err := report(last); err != nil {
				return last, err
			}
		}
		if passed {
			return last, nil
		}
	}

	return last, nil
}

// generate asks the coder for candidate source. Its primitive inputs deliberately prevent task
// test source from entering a coder prompt.
func generate(ctx context.Context, coder llm.LLM, spec, signature string, previous domain.Attempt) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var promptText string
	if previous.N == 0 {
		promptText = prompt.FirstPrompt(spec, signature)
	} else {
		promptText = prompt.RepairPrompt(spec, signature, previous.Code, previous.Output)
	}

	raw, err := coder.Complete(ctx, promptText)
	if err != nil {
		return "", err
	}
	return prompt.ExtractGoCode(raw), nil
}

// runTests verifies code against the authored oracle in task. A non-zero Go command exit is
// failed-attempt feedback; setup, cancellation, cleanup, and command-launch failures are errors.
func runTests(ctx context.Context, task domain.Task, code string, timeout time.Duration) (passed bool, output string, infraErr error) {
	if timeout <= 0 {
		return false, "", errors.New("verifier timeout must be greater than zero")
	}
	if strings.TrimSpace(task.TestCode) == "" {
		return false, "", errors.New("task test code is required")
	}
	if err := ctx.Err(); err != nil {
		return false, "", err
	}

	dir, err := os.MkdirTemp("", "repair-*")
	if err != nil {
		return false, "", err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			passed = false
			output = ""
			infraErr = errors.Join(infraErr, cleanupErr)
		}
	}()

	if err := write(dir, "go.mod", "module solution\n\ngo 1.26\n"); err != nil {
		return false, "", err
	}
	if err := write(dir, "solution.go", code); err != nil {
		return false, "", err
	}
	if err := write(dir, "solution_test.go", task.TestCode); err != nil {
		return false, "", err
	}

	verifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for _, args := range [][]string{{"build", "./..."}, {"test", "./..."}} {
		cmd := exec.CommandContext(verifyCtx, "go", args...)
		cmd.Dir = dir
		// Bound inherited child pipes after cancellation. This is not process isolation.
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()

		if callerErr := ctx.Err(); callerErr != nil {
			return false, "", callerErr
		}
		if verifyCtx.Err() != nil {
			return false, verifierTimeoutOutput, nil
		}
		if err == nil {
			continue
		}

		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return false, "", err
		}
		return false, string(out), nil
	}

	return true, "", nil
}

func write(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
