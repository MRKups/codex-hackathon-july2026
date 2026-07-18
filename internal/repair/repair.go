// Package repair verifies generated Go candidates and runs the repair loop.
package repair

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/prompt"
)

const verifierTimeoutOutput = "verifier timeout — generated code may have hung"

// ProgressReporter receives the two meaningful kinds of repair-loop progress synchronously.
// Nil callbacks disable the corresponding notification. A callback error stops the loop and is
// returned to the caller.
//
// OracleResolved receives exactly one frozen test source after the oracle is accepted and before
// any coder call. AttemptFinished receives each completed verification in order.
type ProgressReporter struct {
	OracleResolved  func(testCode string) error
	AttemptFinished func(domain.Attempt) error
}

// OracleFailureError means a generated-oracle run exhausted its accepted preflight budget
// before any coder call. Callers can recognize it with errors.As and present oraclefailed as a
// distinct outcome rather than blaming the coder.
type OracleFailureError struct {
	Attempts int
	Output   string
}

func (err *OracleFailureError) Error() string {
	if strings.TrimSpace(err.Output) == "" {
		return fmt.Sprintf("generated oracle was rejected after %d attempt(s)", err.Attempts)
	}
	return fmt.Sprintf("generated oracle was rejected after %d attempt(s): %s", err.Attempts, err.Output)
}

// Repair resolves one frozen oracle, then generates and verifies up to maxAttempts candidates.
// In authored mode tester may be nil. In generated mode tester is called only before attempt 1,
// using a prompt with spec and signature only. It returns the last completed attempt and only
// returns an error for caller or infrastructure failures. A failed verifier result is a normal
// attempt, not an error.
func Repair(
	ctx context.Context,
	coder llm.LLM,
	tester llm.LLM,
	task domain.Task,
	maxAttempts int,
	testTimeout time.Duration,
	maxOracleAttempts int,
	report ProgressReporter,
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
	if err := ctx.Err(); err != nil {
		return domain.Attempt{}, err
	}

	resolvedTask, err := resolveOracle(ctx, tester, task, maxOracleAttempts, testTimeout)
	if err != nil {
		return domain.Attempt{}, err
	}
	if report.OracleResolved != nil {
		if err := report.OracleResolved(resolvedTask.TestCode); err != nil {
			return domain.Attempt{}, err
		}
	}

	var last domain.Attempt
	for attemptNumber := 1; attemptNumber <= maxAttempts; attemptNumber++ {
		code, err := generate(ctx, coder, resolvedTask.Spec, resolvedTask.Signature, last)
		if err != nil {
			return last, err
		}

		passed, output, err := runTests(ctx, resolvedTask, code, testTimeout)
		if err != nil {
			return last, err
		}

		last = domain.Attempt{
			N:      attemptNumber,
			Code:   code,
			Passed: passed,
			Output: output,
		}
		if report.AttemptFinished != nil {
			if err := report.AttemptFinished(last); err != nil {
				return last, err
			}
		}
		if passed {
			return last, nil
		}
	}

	return last, nil
}

// resolveOracle fixes one test source for the run before any candidate solution exists. An
// authored oracle is validated and retained. A generated oracle is requested with spec and
// signature only, preflighted against a signature-derived stub, and then frozen in Task.TestCode.
func resolveOracle(ctx context.Context, tester llm.LLM, task domain.Task, maxOracleAttempts int, preflightTimeout time.Duration) (domain.Task, error) {
	switch task.Oracle {
	case "", domain.OracleAuthored:
		if strings.TrimSpace(task.TestCode) == "" {
			return domain.Task{}, errors.New("task test code is required")
		}
		task.Oracle = domain.OracleAuthored
		return task, nil

	case domain.OracleGenerated:
		if tester == nil {
			return domain.Task{}, errors.New("tester is required for generated oracle tasks")
		}
		if maxOracleAttempts <= 0 {
			return domain.Task{}, errors.New("oracle attempt cap must be greater than zero")
		}

		stub, functionName, err := signatureStub(task.Signature)
		if err != nil {
			return domain.Task{}, fmt.Errorf("task signature is invalid: %w", err)
		}

		// Never accept stale caller-provided test source for generated mode. The only legal source
		// is an accepted blind tester response from this resolution step.
		task.TestCode = ""
		testPrompt := prompt.TestPrompt(task.Spec, task.Signature)
		var lastOutput string
		for attempt := 1; attempt <= maxOracleAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return domain.Task{}, err
			}

			raw, err := tester.Complete(ctx, testPrompt)
			if err != nil {
				return domain.Task{}, err
			}

			candidate := prompt.ExtractGoCode(raw)
			accepted, output, err := preflightOracleForFunction(ctx, stub, candidate, preflightTimeout, functionName)
			if err != nil {
				return domain.Task{}, err
			}
			if accepted {
				task.TestCode = candidate
				return task, nil
			}
			lastOutput = output
		}

		return domain.Task{}, &OracleFailureError{Attempts: maxOracleAttempts, Output: lastOutput}

	default:
		return domain.Task{}, fmt.Errorf("unknown oracle mode %q", task.Oracle)
	}
}

// signatureStub parses and type-checks one pinned function signature, then renders a complete
// package solution file containing a panic-only implementation. The stub is compiled but never
// run by oracle preflight, so it checks generated tests against the exact API without spending a
// coder attempt.
func signatureStub(signature string) (string, string, error) {
	if err := domain.ValidateSignature(signature); err != nil {
		return "", "", err
	}

	source := "package solution\n\n" + strings.TrimSpace(signature) + " {\n\tpanic(\"oracle preflight stub\")\n}\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "signature.go", source, 0)
	if err != nil {
		return "", "", err
	}
	if len(file.Decls) != 1 {
		return "", "", errors.New("signature must contain exactly one function declaration")
	}

	function, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || function.Name == nil {
		return "", "", errors.New("signature must declare a function")
	}
	if function.Recv != nil {
		return "", "", errors.New("signature must declare a top-level function")
	}

	function.Body = &ast.BlockStmt{List: []ast.Stmt{
		&ast.ExprStmt{X: &ast.CallExpr{
			Fun: ast.NewIdent("panic"),
			Args: []ast.Expr{&ast.BasicLit{
				Kind:  token.STRING,
				Value: strconv.Quote("oracle preflight stub"),
			}},
		}},
	}}
	var output bytes.Buffer
	if err := format.Node(&output, fset, file); err != nil {
		return "", "", err
	}
	return output.String(), function.Name.Name, nil
}

// preflightOracle compiles a generated test file with go test -c. It never runs the test binary;
// a false nil-error result means the candidate oracle was rejected and may be retried before any
// coder call. Filesystem, process-launch, and caller-context failures remain infrastructure
// errors.
func preflightOracle(ctx context.Context, stub, testCode string, timeout time.Duration) (accepted bool, output string, infraErr error) {
	return preflightOracleForFunction(ctx, stub, testCode, timeout, "")
}

// preflightOracleForFunction additionally makes the low-cost structural checks needed for an
// interactive generated oracle. It does not attempt to prove test quality, but it rejects
// obvious no-op or bypassed source before any candidate code can be charged with its outcome.
func preflightOracleForFunction(ctx context.Context, stub, testCode string, timeout time.Duration, requiredFunction string) (accepted bool, output string, infraErr error) {
	if timeout <= 0 {
		return false, "", errors.New("oracle preflight timeout must be greater than zero")
	}
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	if strings.TrimSpace(testCode) == "" {
		return false, "oracle source is empty", nil
	}

	testFile, err := parser.ParseFile(token.NewFileSet(), "solution_test.go", testCode, parser.ParseComments)
	if err != nil {
		return false, fmt.Sprintf("oracle source does not parse: %v", err), nil
	}
	if testFile.Name == nil || testFile.Name.Name != "solution" {
		return false, "oracle source must declare package solution", nil
	}
	if hasBuildConstraint(testFile) {
		return false, "oracle source must not use build constraints", nil
	}
	if hasForbiddenOracleFunction(testFile) {
		return false, "oracle source must not declare TestMain or init", nil
	}
	if !hasTestFunction(testFile) {
		return false, "oracle source must define at least one runnable Test function using *testing.T", nil
	}
	if hasForbiddenOracleCall(testFile) {
		return false, "oracle source must not skip tests or call os.Exit", nil
	}
	if requiredFunction != "" && !hasDirectFunctionCall(testFile, requiredFunction) {
		return false, fmt.Sprintf("oracle source must call required function %s", requiredFunction), nil
	}
	if !hasTestingFailureCall(testFile) {
		return false, "oracle source must use a testing failure method", nil
	}

	dir, err := os.MkdirTemp("", "repair-oracle-*")
	if err != nil {
		return false, "", err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			accepted = false
			output = ""
			infraErr = errors.Join(infraErr, cleanupErr)
		}
	}()

	if err := write(dir, "go.mod", "module solution\n\ngo 1.26\n"); err != nil {
		return false, "", err
	}
	if err := write(dir, "solution.go", stub); err != nil {
		return false, "", err
	}
	if err := write(dir, "solution_test.go", testCode); err != nil {
		return false, "", err
	}

	preflightCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(preflightCtx, "go", "test", "-c", "./...")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	commandOutput, err := cmd.CombinedOutput()
	if callerErr := ctx.Err(); callerErr != nil {
		return false, "", callerErr
	}
	if preflightCtx.Err() != nil {
		return false, "", fmt.Errorf("oracle preflight timed out after %s", timeout)
	}
	if err == nil {
		return true, "", nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, string(commandOutput), nil
	}
	return false, "", err
}

func hasBuildConstraint(file *ast.File) bool {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if strings.HasPrefix(text, "go:build") || strings.HasPrefix(text, "+build") {
				return true
			}
		}
	}
	return false
}

func hasForbiddenOracleFunction(file *ast.File) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Name == nil {
			continue
		}
		if function.Name.Name == "TestMain" || function.Name.Name == "init" {
			return true
		}
	}
	return false
}

func hasForbiddenOracleCall(file *ast.File) bool {
	forbiddenTestingMethods := map[string]bool{
		"Skip":    true,
		"Skipf":   true,
		"SkipNow": true,
	}

	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil {
			return true
		}
		if forbiddenTestingMethods[selector.Sel.Name] {
			found = true
			return false
		}
		qualifier, ok := selector.X.(*ast.Ident)
		if ok && qualifier.Name == "os" && selector.Sel.Name == "Exit" {
			found = true
			return false
		}
		return true
	})
	return found
}

func hasDirectFunctionCall(file *ast.File, requiredFunction string) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if calledFunctionName(call.Fun) == requiredFunction {
			found = true
			return false
		}
		return true
	})
	return found
}

func calledFunctionName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.IndexExpr:
		return calledFunctionName(value.X)
	case *ast.IndexListExpr:
		return calledFunctionName(value.X)
	case *ast.ParenExpr:
		return calledFunctionName(value.X)
	default:
		return ""
	}
}

func hasTestingFailureCall(file *ast.File) bool {
	failureMethods := map[string]bool{
		"Error":   true,
		"Errorf":  true,
		"Fail":    true,
		"FailNow": true,
		"Fatal":   true,
		"Fatalf":  true,
	}

	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel != nil && failureMethods[selector.Sel.Name] {
			found = true
			return false
		}
		return true
	})
	return found
}

func hasTestFunction(file *ast.File) bool {
	testingNames, dotTesting := testingImportNames(file)
	if len(testingNames) == 0 && !dotTesting {
		return false
	}

	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Name == nil || !isTestName(function.Name.Name) {
			continue
		}
		if function.Type.Params == nil || len(function.Type.Params.List) != 1 || (function.Type.Results != nil && len(function.Type.Results.List) != 0) {
			continue
		}
		if function.Type.Params.List[0].Type == nil {
			continue
		}
		if isTestingT(function.Type.Params.List[0].Type, testingNames, dotTesting) {
			return true
		}
	}
	return false
}

func testingImportNames(file *ast.File) (map[string]bool, bool) {
	names := make(map[string]bool)
	dotTesting := false
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != "testing" {
			continue
		}
		if imported.Name == nil {
			names["testing"] = true
			continue
		}
		switch imported.Name.Name {
		case "_":
			continue
		case ".":
			dotTesting = true
		default:
			names[imported.Name.Name] = true
		}
	}
	return names, dotTesting
}

func isTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	if len(name) == len("Test") {
		return true
	}
	runeAfterPrefix, _ := utf8.DecodeRuneInString(name[len("Test"):])
	return !unicode.IsLower(runeAfterPrefix)
}

func isTestingT(expression ast.Expr, testingNames map[string]bool, dotTesting bool) bool {
	star, ok := expression.(*ast.StarExpr)
	if !ok {
		return false
	}

	switch value := star.X.(type) {
	case *ast.SelectorExpr:
		qualifier, ok := value.X.(*ast.Ident)
		return ok && testingNames[qualifier.Name] && value.Sel.Name == "T"
	case *ast.Ident:
		return dotTesting && value.Name == "T"
	default:
		return false
	}
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

// runTests verifies code against the frozen oracle in task. A non-zero Go command exit is
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

	for _, args := range [][]string{{"build", "./..."}, {"test", "-timeout", timeout.String(), "./..."}} {
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
