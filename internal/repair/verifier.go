package repair

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/verification"
)

const (
	verifierTimeoutOutput    = "verifier timeout — generated code may have hung"
	maxVerifierOutputBytes   = 128 << 10
	verifierOutputTruncation = "\n[verifier output truncated after 131072 bytes]\n"
)

// runBundleTests verifies code against one frozen bundle. It compiles a test binary, removes the
// source-bearing directory, then executes from a clean working directory. A non-zero Go command
// exit is failed-attempt feedback; setup, cancellation, cleanup, and command-launch failures are
// errors.
func runBundleTests(ctx context.Context, bundle domain.VerificationBundle, code string, timeout time.Duration) (passed bool, output string, infraErr error) {
	if timeout <= 0 {
		return false, "", errors.New("verifier timeout must be greater than zero")
	}
	if strings.TrimSpace(bundle.TestCode) == "" {
		return false, "", errors.New("verification test source is required")
	}
	if sourceUsesEmbedDirective(code) {
		return false, "candidate source must not use go:embed directives", nil
	}
	if err := ctx.Err(); err != nil {
		return false, "", err
	}

	compileDir, err := os.MkdirTemp("", "repair-compile-*")
	if err != nil {
		return false, "", err
	}
	executionDir, err := os.MkdirTemp("", "repair-execute-*")
	if err != nil {
		if cleanupErr := os.RemoveAll(compileDir); cleanupErr != nil {
			return false, "", errors.Join(err, cleanupErr)
		}
		return false, "", err
	}
	defer func() {
		for _, dir := range []string{compileDir, executionDir} {
			if dir == "" {
				continue
			}
			if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
				passed = false
				output = ""
				infraErr = errors.Join(infraErr, cleanupErr)
			}
		}
	}()

	completionToken, err := newCompletionToken()
	if err != nil {
		return false, "", err
	}
	completionPath := filepath.Join(executionDir, ".repair-verification-complete")
	if err := write(compileDir, "go.mod", "module solution\n\ngo 1.26\n"); err != nil {
		return false, "", err
	}
	if err := write(compileDir, "solution.go", code); err != nil {
		return false, "", err
	}
	if err := write(compileDir, "solution_test.go", bundle.TestCode); err != nil {
		return false, "", err
	}
	if err := write(compileDir, "repair_harness_test.go", verifierHarnessSource(completionPath, completionToken)); err != nil {
		return false, "", err
	}

	verifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for _, args := range [][]string{{"build", "./..."}, {"test", "-c", "-o", filepath.Join(executionDir, "verification.test"), "./..."}} {
		cmd := exec.CommandContext(verifyCtx, "go", args...)
		cmd.Dir = compileDir
		// Bound inherited child pipes after cancellation. This is not process isolation.
		cmd.WaitDelay = time.Second
		out, err := limitedCombinedOutput(cmd)

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
		return false, out, nil
	}

	// The compiled binary runs from a different, source-free directory. Removing the compilation
	// directory before execution prevents ordinary candidate/test code from reading solution.go or
	// solution_test.go and feeding the frozen oracle back into a repair prompt. This is a blind
	// boundary mitigation, not an OS sandbox against deliberately hostile local code.
	if err := os.RemoveAll(compileDir); err != nil {
		return false, "", err
	}
	compileDir = ""

	testBinary := filepath.Join(executionDir, "verification.test")
	cmd := exec.CommandContext(verifyCtx, testBinary, "-test.timeout", timeout.String())
	cmd.Dir = executionDir
	cmd.Env = verificationExecutionEnv(executionDir)
	cmd.WaitDelay = time.Second
	out, err := limitedCombinedOutput(cmd)
	if callerErr := ctx.Err(); callerErr != nil {
		return false, "", callerErr
	}
	if verifyCtx.Err() != nil {
		return false, verifierTimeoutOutput, nil
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return false, "", err
		}
		return false, out, nil
	}
	if err := verifyCompletion(completionPath, completionToken); err != nil {
		return false, fmt.Sprintf("verification test process did not complete: %v", err), nil
	}

	return true, "", nil
}

func newCompletionToken() (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := cryptorand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("create verification completion token: %w", err)
	}
	return hex.EncodeToString(tokenBytes), nil
}

func verifierHarnessSource(completionPath, completionToken string) string {
	return fmt.Sprintf(`package solution

import (
	"fmt"
	"os"
	"testing"
)

const repairCompletionPath = %q
const repairCompletionToken = %q

func TestMain(m *testing.M) {
	code := m.Run()
	if err := os.WriteFile(repairCompletionPath, []byte(repairCompletionToken), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "repair verifier completion sentinel:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
`, completionPath, completionToken)
}

func verifyCompletion(path, token string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if string(contents) != token {
		return errors.New("completion sentinel did not match")
	}
	return nil
}

func verificationExecutionEnv(directory string) []string {
	env := []string{"TMPDIR=" + directory}
	if path := os.Getenv("PATH"); path != "" {
		env = append(env, "PATH="+path)
	}
	return env
}

type limitedOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	truncated bool
}

func (output *limitedOutput) Write(contents []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()

	remaining := maxVerifierOutputBytes - output.buffer.Len()
	if remaining > 0 {
		if len(contents) > remaining {
			_, _ = output.buffer.Write(contents[:remaining])
			output.truncated = true
			return len(contents), nil
		}
		_, _ = output.buffer.Write(contents)
		return len(contents), nil
	}
	if len(contents) > 0 {
		output.truncated = true
	}
	return len(contents), nil
}

func (output *limitedOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()

	if !output.truncated {
		return output.buffer.String()
	}
	return output.buffer.String() + verifierOutputTruncation
}

func limitedCombinedOutput(cmd *exec.Cmd) (string, error) {
	output := &limitedOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	return output.String(), err
}

func sourceUsesEmbedDirective(source string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), "solution.go", source, parser.ParseComments)
	return err == nil && hasEmbedDirective(file)
}

func hasEmbedDirective(file *ast.File) bool {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if strings.HasPrefix(text, "go:embed") {
				return true
			}
		}
	}
	return false
}

// runTests preserves the focused verifier helper used by authored-task tests. Repair itself
// always calls runBundleTests after resolving and freezing one bundle.
func runTests(ctx context.Context, task domain.Task, code string, timeout time.Duration) (bool, string, error) {
	bundle, err := verification.AuthoredSource(task, task.TestCode)
	if err != nil {
		return false, "", err
	}
	return runBundleTests(ctx, bundle, code, timeout)
}

func write(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
