package oracle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"codex-hackathon-july2026/internal/domain"
)

const (
	maxPreflightOutputBytes   = 128 << 10
	preflightOutputTruncation = "\n[oracle preflight output truncated after 131072 bytes]\n"
)

// Admitter is the deterministic structural gate used by a Resolver. Its job is limited to
// source shape, signature compatibility, and compilation; it makes no semantic-correctness claim.
type Admitter interface {
	Admit(context.Context, domain.Task, string, time.Duration) (Admission, error)
}

// Admission distinguishes a rejected source candidate from an infrastructure failure. Output is
// safe compiler/structural feedback for a generated author retry before any candidate exists.
type Admission struct {
	Accepted bool
	Output   string
}

// StructuralAdmitter is the generic Go source/preflight implementation. It contains no task
// family logic, expected values, reference implementation, or executable natural-language rules.
type StructuralAdmitter struct{}

// NewStructuralAdmitter returns the concrete deterministic admission component used by default.
func NewStructuralAdmitter() StructuralAdmitter {
	return StructuralAdmitter{}
}

// Admit type-checks the pinned signature, performs structural source checks, and compiles a test
// binary against a panic-only stub without executing test bodies.
func (StructuralAdmitter) Admit(ctx context.Context, task domain.Task, testCode string, timeout time.Duration) (Admission, error) {
	stub, functionName, err := signatureStub(task.Signature)
	if err != nil {
		return Admission{}, fmt.Errorf("task signature is invalid: %w", err)
	}
	accepted, output, err := preflightOracleForFunction(ctx, stub, testCode, timeout, functionName)
	return Admission{Accepted: accepted, Output: output}, err
}

// signatureStub parses and type-checks one pinned function signature, then renders a complete
// package solution file containing a panic-only implementation. The stub is compiled but never
// run by oracle preflight, so it checks generated tests against the exact API without spending a
// candidate attempt.
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

// preflightOracle compiles a test file with go test -c. It never runs the test binary; a false
// nil-error result means the candidate oracle was rejected and may be retried before any code
// writer call. Filesystem, process-launch, and caller-context failures remain infrastructure
// errors.
func preflightOracle(ctx context.Context, stub, testCode string, timeout time.Duration) (accepted bool, output string, infraErr error) {
	return preflightOracleForFunction(ctx, stub, testCode, timeout, "")
}

// preflightOracleForFunction additionally makes the low-cost structural checks needed for an
// interactive generated oracle. It does not attempt to prove test quality, but it rejects obvious
// no-op or bypassed source before any candidate code can be charged with its outcome.
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

	fset := token.NewFileSet()
	testFile, err := parser.ParseFile(fset, "solution_test.go", testCode, parser.ParseComments)
	if err != nil {
		return false, fmt.Sprintf("oracle source does not parse: %v", err), nil
	}
	if testFile.Name == nil || testFile.Name.Name != "solution" {
		return false, "oracle source must declare package solution", nil
	}
	if hasBuildConstraint(testFile) {
		return false, "oracle source must not use build constraints", nil
	}
	if hasEmbedDirective(testFile) {
		return false, "oracle source must not use go:embed directives", nil
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
	if requiredFunction != "" {
		analysis, err := analyzeRunnableTests(fset, stub, testFile, requiredFunction)
		if err != nil {
			return false, fmt.Sprintf("oracle source does not type-check: %v", err), nil
		}
		if !analysis.requiredCall {
			return false, fmt.Sprintf("oracle source must call required function %s", requiredFunction), nil
		}
		if !analysis.failureCall {
			return false, "oracle source must use a testing failure method", nil
		}
		if !analysis.coupled {
			return false, fmt.Sprintf("oracle source must call required function %s and use a testing failure method from the same runnable Test", requiredFunction), nil
		}
	} else if !hasTestingFailureCall(testFile) {
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

	if err := writePreflightFile(dir, "go.mod", "module solution\n\ngo 1.26\n"); err != nil {
		return false, "", err
	}
	if err := writePreflightFile(dir, "solution.go", stub); err != nil {
		return false, "", err
	}
	if err := writePreflightFile(dir, "solution_test.go", testCode); err != nil {
		return false, "", err
	}

	preflightCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(preflightCtx, "go", "test", "-c", "./...")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	commandOutput, err := limitedPreflightOutput(cmd)
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
		return false, commandOutput, nil
	}
	return false, "", err
}

func writePreflightFile(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

type preflightOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	truncated bool
}

func (output *preflightOutput) Write(contents []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()

	remaining := maxPreflightOutputBytes - output.buffer.Len()
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

func (output *preflightOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()

	if !output.truncated {
		return output.buffer.String()
	}
	return output.buffer.String() + preflightOutputTruncation
}

func limitedPreflightOutput(cmd *exec.Cmd) (string, error) {
	output := &preflightOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	return output.String(), err
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

// runnableTestAnalysis describes what is reachable from runnable Test functions after type
// checking against the pinned stub. It deliberately remains a structural admission gate, not a
// proof that a generated assertion is meaningful.
type runnableTestAnalysis struct {
	requiredCall bool
	failureCall  bool
	coupled      bool
}

func analyzeRunnableTests(fset *token.FileSet, stub string, testFile *ast.File, requiredFunction string) (runnableTestAnalysis, error) {
	stubFile, err := parser.ParseFile(fset, "solution.go", stub, 0)
	if err != nil {
		return runnableTestAnalysis{}, err
	}

	info := &types.Info{
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Types:      make(map[ast.Expr]types.TypeAndValue),
	}
	config := types.Config{Importer: importer.Default()}
	if _, err := config.Check("solution", fset, []*ast.File{stubFile, testFile}, info); err != nil {
		return runnableTestAnalysis{}, err
	}

	helperDeclarations := make(map[*types.Func]*ast.FuncDecl)
	for _, declaration := range testFile.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name == nil {
			continue
		}
		if object, ok := info.Defs[function.Name].(*types.Func); ok {
			helperDeclarations[object] = function
		}
	}

	var result runnableTestAnalysis
	for _, test := range runnableTestFunctions(testFile) {
		analyzer := oracleTestAnalyzer{
			info:               info,
			requiredFunction:   requiredFunction,
			helperDeclarations: helperDeclarations,
			activeFunctions:    make(map[*ast.FuncDecl]bool),
		}
		analyzer.scanFunction(test)
		result.requiredCall = result.requiredCall || analyzer.facts.requiredCall
		result.failureCall = result.failureCall || analyzer.facts.failureCall
		result.coupled = result.coupled || (analyzer.facts.requiredCall && analyzer.facts.failureCall)
	}
	return result, nil
}

type oracleTestFacts struct {
	requiredCall bool
	failureCall  bool
}

type oracleTestAnalyzer struct {
	info               *types.Info
	requiredFunction   string
	helperDeclarations map[*types.Func]*ast.FuncDecl
	activeFunctions    map[*ast.FuncDecl]bool
	facts              oracleTestFacts
}

func (analyzer *oracleTestAnalyzer) scanFunction(function *ast.FuncDecl) {
	if function == nil || function.Body == nil || analyzer.activeFunctions[function] {
		return
	}
	analyzer.activeFunctions[function] = true
	analyzer.scanBlock(function.Body)
	delete(analyzer.activeFunctions, function)
}

func (analyzer *oracleTestAnalyzer) scanBlock(block *ast.BlockStmt) (terminated bool) {
	if block == nil {
		return false
	}
	for _, statement := range block.List {
		if analyzer.scanStatement(statement) {
			return true
		}
	}
	return false
}

func (analyzer *oracleTestAnalyzer) scanStatement(statement ast.Stmt) bool {
	switch value := statement.(type) {
	case *ast.BlockStmt:
		return analyzer.scanBlock(value)
	case *ast.ExprStmt:
		analyzer.scanExpression(value.X)
	case *ast.AssignStmt:
		for _, expression := range value.Rhs {
			analyzer.scanExpression(expression)
		}
	case *ast.DeclStmt:
		analyzer.scanDeclaration(value.Decl)
	case *ast.ReturnStmt:
		for _, expression := range value.Results {
			analyzer.scanExpression(expression)
		}
		return true
	case *ast.IfStmt:
		if value.Init != nil {
			analyzer.scanStatement(value.Init)
		}
		analyzer.scanExpression(value.Cond)
		if truth, known := analyzer.constantBool(value.Cond); known {
			if truth {
				return analyzer.scanBlock(value.Body)
			}
			if value.Else != nil {
				return analyzer.scanStatement(value.Else)
			}
			return false
		}
		analyzer.scanBlock(value.Body)
		if value.Else != nil {
			analyzer.scanStatement(value.Else)
		}
	case *ast.ForStmt:
		if value.Init != nil {
			analyzer.scanStatement(value.Init)
		}
		if value.Cond != nil {
			analyzer.scanExpression(value.Cond)
			if truth, known := analyzer.constantBool(value.Cond); known && !truth {
				return false
			}
		}
		analyzer.scanBlock(value.Body)
		if value.Post != nil {
			analyzer.scanStatement(value.Post)
		}
	case *ast.RangeStmt:
		analyzer.scanExpression(value.X)
		analyzer.scanBlock(value.Body)
	case *ast.SwitchStmt:
		if value.Init != nil {
			analyzer.scanStatement(value.Init)
		}
		if value.Tag != nil {
			analyzer.scanExpression(value.Tag)
		}
		for _, clause := range value.Body.List {
			caseClause, ok := clause.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expression := range caseClause.List {
				analyzer.scanExpression(expression)
			}
			for _, bodyStatement := range caseClause.Body {
				analyzer.scanStatement(bodyStatement)
			}
		}
	case *ast.TypeSwitchStmt:
		if value.Init != nil {
			analyzer.scanStatement(value.Init)
		}
		analyzer.scanStatement(value.Assign)
		for _, clause := range value.Body.List {
			caseClause, ok := clause.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, bodyStatement := range caseClause.Body {
				analyzer.scanStatement(bodyStatement)
			}
		}
	case *ast.SelectStmt:
		for _, clause := range value.Body.List {
			communication, ok := clause.(*ast.CommClause)
			if !ok {
				continue
			}
			if communication.Comm != nil {
				analyzer.scanStatement(communication.Comm)
			}
			for _, bodyStatement := range communication.Body {
				analyzer.scanStatement(bodyStatement)
			}
		}
	case *ast.GoStmt:
		analyzer.recordCall(value.Call)
	case *ast.DeferStmt:
		analyzer.recordCall(value.Call)
	case *ast.LabeledStmt:
		return analyzer.scanStatement(value.Stmt)
	case *ast.SendStmt:
		analyzer.scanExpression(value.Chan)
		analyzer.scanExpression(value.Value)
	case *ast.IncDecStmt:
		analyzer.scanExpression(value.X)
	}
	return false
}

func (analyzer *oracleTestAnalyzer) scanDeclaration(declaration ast.Decl) {
	general, ok := declaration.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, specification := range general.Specs {
		value, ok := specification.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, expression := range value.Values {
			analyzer.scanExpression(expression)
		}
	}
}

func (analyzer *oracleTestAnalyzer) scanExpression(expression ast.Expr) {
	if expression == nil {
		return
	}
	ast.Inspect(expression, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.FuncLit:
			// A local closure is not evidence unless a reachable call below invokes it.
			return false
		case *ast.CallExpr:
			analyzer.recordCall(value)
		}
		return true
	})
}

func (analyzer *oracleTestAnalyzer) recordCall(call *ast.CallExpr) {
	if call == nil {
		return
	}
	if analyzer.isRequiredCall(call) {
		analyzer.facts.requiredCall = true
	}
	if analyzer.isTestingFailure(call) {
		analyzer.facts.failureCall = true
	}
	if literal, ok := call.Fun.(*ast.FuncLit); ok {
		analyzer.scanBlock(literal.Body)
	}
	if analyzer.isTestingRun(call) {
		for _, argument := range call.Args {
			if literal, ok := argument.(*ast.FuncLit); ok {
				analyzer.scanBlock(literal.Body)
			}
		}
	}

	identifier := calledFunctionIdentifier(call.Fun)
	if identifier == nil {
		return
	}
	helper, ok := analyzer.info.Uses[identifier].(*types.Func)
	if !ok {
		return
	}
	if declaration := analyzer.helperDeclarations[helper]; declaration != nil {
		analyzer.scanFunction(declaration)
	}
}

func (analyzer *oracleTestAnalyzer) isRequiredCall(call *ast.CallExpr) bool {
	identifier := calledFunctionIdentifier(call.Fun)
	if identifier == nil {
		return false
	}
	function, ok := analyzer.info.Uses[identifier].(*types.Func)
	if !ok || function.Name() != analyzer.requiredFunction || function.Pkg() == nil {
		return false
	}
	return function.Pkg().Path() == "solution"
}

func (analyzer *oracleTestAnalyzer) isTestingFailure(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || !testingFailureMethods[selector.Sel.Name] {
		return false
	}
	selection := analyzer.info.Selections[selector]
	if selection == nil || !isTestingTPointer(selection.Recv()) {
		return false
	}
	_, ok = selection.Obj().(*types.Func)
	return ok
}

func (analyzer *oracleTestAnalyzer) isTestingRun(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel == nil || selector.Sel.Name != "Run" {
		return false
	}
	selection := analyzer.info.Selections[selector]
	if selection == nil || !isTestingTPointer(selection.Recv()) {
		return false
	}
	function, ok := selection.Obj().(*types.Func)
	return ok && function.Name() == "Run"
}

func (analyzer *oracleTestAnalyzer) constantBool(expression ast.Expr) (bool, bool) {
	typeAndValue, found := analyzer.info.Types[expression]
	if !found || typeAndValue.Value == nil || typeAndValue.Value.Kind() != constant.Bool {
		return false, false
	}
	return constant.BoolVal(typeAndValue.Value), true
}

func calledFunctionIdentifier(expression ast.Expr) *ast.Ident {
	switch value := expression.(type) {
	case *ast.Ident:
		return value
	case *ast.IndexExpr:
		return calledFunctionIdentifier(value.X)
	case *ast.IndexListExpr:
		return calledFunctionIdentifier(value.X)
	case *ast.ParenExpr:
		return calledFunctionIdentifier(value.X)
	default:
		return nil
	}
}

func isTestingTPointer(value types.Type) bool {
	pointer, ok := value.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := pointer.Elem().(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Pkg().Path() == "testing" && named.Obj().Name() == "T"
}

var testingFailureMethods = map[string]bool{
	"Error":   true,
	"Errorf":  true,
	"Fail":    true,
	"FailNow": true,
	"Fatal":   true,
	"Fatalf":  true,
}

func hasTestingFailureCall(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel != nil && testingFailureMethods[selector.Sel.Name] {
			found = true
			return false
		}
		return true
	})
	return found
}

func hasTestFunction(file *ast.File) bool {
	return len(runnableTestFunctions(file)) > 0
}

func runnableTestFunctions(file *ast.File) []*ast.FuncDecl {
	testingNames, dotTesting := testingImportNames(file)
	if len(testingNames) == 0 && !dotTesting {
		return nil
	}

	functions := make([]*ast.FuncDecl, 0)
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
			functions = append(functions, function)
		}
	}
	return functions
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
