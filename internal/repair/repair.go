// Package repair verifies generated Go candidates and will own the repair loop.
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
)

const verifierTimeoutOutput = "verifier timeout — generated code may have hung"

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
