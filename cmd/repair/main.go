// Command repair runs the Test Verifier authored terminal control or browser application.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/draft"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/llm/openai"
	"codex-hackathon-july2026/internal/oracle"
	"codex-hackathon-july2026/internal/repair"
	"codex-hackathon-july2026/internal/run"
	"codex-hackathon-july2026/internal/server"
	"codex-hackathon-july2026/internal/template"
)

const (
	envModelCatalog  = "LLM_MODELS"
	envModelCoder    = "LLM_MODEL_CODER"
	envModelTester   = "LLM_MODEL_TESTER"
	envModelReviewer = "LLM_MODEL_REVIEWER"
)

func main() {
	var address string
	var maxAttempts int
	var oracleAttempts int
	var runTimeout time.Duration
	var serve bool
	var templatesDir string
	var verifierTimeout time.Duration
	flag.StringVar(&address, "addr", "127.0.0.1:8080", "address for the browser demo server")
	flag.IntVar(&maxAttempts, "attempts", 3, "maximum number of coder attempts")
	flag.IntVar(&oracleAttempts, "oracle-attempts", 2, "maximum generated-oracle attempts before oraclefailed")
	flag.DurationVar(&runTimeout, "run-timeout", 150*time.Second, "maximum duration of one browser verification run")
	flag.BoolVar(&serve, "serve", false, "serve the browser demo instead of running once in the terminal")
	flag.StringVar(&templatesDir, "templates-dir", "templates", "project-root directory for source-free task templates")
	flag.DurationVar(&verifierTimeout, "verifier-timeout", 10*time.Second, "timeout for one oracle preflight or candidate verification")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "usage: %s [-serve] [-addr ADDRESS] [-attempts N] [-oracle-attempts N] [-run-timeout DURATION] [-templates-dir DIRECTORY] [-verifier-timeout DURATION]\n", os.Args[0])
		os.Exit(2)
	}
	if maxAttempts <= 0 {
		exitFailure("configuration error", fmt.Errorf("attempts must be greater than zero"))
	}
	if oracleAttempts <= 0 {
		exitFailure("configuration error", fmt.Errorf("oracle attempts must be greater than zero"))
	}
	if verifierTimeout <= 0 {
		exitFailure("configuration error", fmt.Errorf("verifier timeout must be greater than zero"))
	}
	if runTimeout <= 0 {
		exitFailure("configuration error", fmt.Errorf("run timeout must be greater than zero"))
	}
	if strings.TrimSpace(address) == "" {
		exitFailure("configuration error", fmt.Errorf("server address must not be empty"))
	}
	resolver, err := oracle.NewResolver(oracle.Config{
		MaxAttempts:      oracleAttempts,
		PreflightTimeout: verifierTimeout,
		Rulebook:         oracle.DefaultRulebook(),
		Admitter:         oracle.NewStructuralAdmitter(),
	})
	if err != nil {
		exitFailure("configuration error", err)
	}
	executor := repair.NewExecutor()

	config, err := llm.ConfigFromEnv()
	if err != nil {
		exitFailure("configuration error", err)
	}
	factory, err := configuredClientFactory(config)
	if err != nil {
		exitFailure("configuration error", err)
	}
	models, err := configuredModels(config, factory)
	if err != nil {
		exitFailure("configuration error", err)
	}
	if serve {
		serveBrowser(address, templatesDir, models, resolver, executor, maxAttempts, verifierTimeout, runTimeout)
		return
	}
	coder, err := models.catalog.Resolve(models.coder)
	if err != nil {
		exitFailure("configuration error", err)
	}

	task := splitCentsTask()
	resolution, err := resolver.Resolve(context.Background(), oracle.Request{Task: task}, oracle.ProgressReporter{})
	if err != nil {
		exitFailure("oracle or verifier infrastructure failure", err)
	}

	final, err := executor.Execute(
		context.Background(),
		coder,
		repair.CandidateRequest{
			Spec:      task.Spec,
			Signature: task.Signature,
			Bundle:    resolution.Bundle,
		},
		repair.Config{
			MaxAttempts: maxAttempts,
			TestTimeout: verifierTimeout,
		},
		repair.ProgressReporter{AttemptFinished: printAttempt},
	)
	if err != nil {
		exitFailure("provider or verifier infrastructure failure", err)
	}
	if final.Passed {
		fmt.Printf("passed after %d attempt(s)\n", final.N)
		return
	}

	fmt.Printf("gave up after %d attempt(s)\n", final.N)
}

func serveBrowser(address, templatesDir string, models modelSettings, resolver oracle.Resolver, executor repair.Executor, maxAttempts int, verifierTimeout, runTimeout time.Duration) {
	store, err := run.NewStore(run.Config{
		MaxAttempts: maxAttempts,
		TestTimeout: verifierTimeout,
		RunTimeout:  runTimeout,
	}, resolver, executor)
	if err != nil {
		exitFailure("configuration error", err)
	}
	templates, err := template.New(template.Config{Root: templatesDir})
	if err != nil {
		exitFailure("configuration error", err)
	}
	handler, err := server.New(server.Config{
		Store:     store,
		Models:    models.catalog,
		Draft:     draft.NewService(),
		Templates: templates,
		Defaults: server.ModelDefaults{
			CoderModel:  models.coder,
			TesterModel: models.tester,
		},
		ReviewerModel: models.reviewer,
		Presets:       browserPresets(),
	})
	if err != nil {
		exitFailure("server configuration error", err)
	}

	fmt.Printf("Test Verifier browser application: http://%s\n", address)
	if err := http.ListenAndServe(address, handler); err != nil {
		exitFailure("browser server failure", err)
	}
}

type modelSettings struct {
	catalog  *llm.ModelCatalog
	coder    string
	tester   string
	reviewer string
}

func configuredClientFactory(config llm.Config) (llm.ClientFactory, error) {
	switch config.Provider {
	case openai.ProviderID:
		providerConfig, err := openai.ConfigFromEnv(config.Timeout)
		if err != nil {
			return nil, fmt.Errorf("configure %s provider: %w", openai.ProviderID, err)
		}
		factory, err := openai.NewFactory(providerConfig)
		if err != nil {
			return nil, fmt.Errorf("create %s provider factory: %w", openai.ProviderID, err)
		}
		return factory, nil
	default:
		return nil, fmt.Errorf("unsupported %s %q", llm.EnvProvider, config.Provider)
	}
}

func configuredModels(config llm.Config, factory llm.ClientFactory) (modelSettings, error) {
	coder := configuredModel(envModelCoder, config.Model)
	tester := configuredModel(envModelTester, config.Model)
	reviewer := configuredModel(envModelReviewer, tester)
	modelIDs, err := llm.ParseModelIDs(os.Getenv(envModelCatalog))
	if err != nil {
		return modelSettings{}, fmt.Errorf("parse %s: %w", envModelCatalog, err)
	}
	if len(modelIDs) == 0 {
		modelIDs = uniqueModels(config.Model, coder, tester, reviewer)
	} else if !containsModel(modelIDs, coder) || !containsModel(modelIDs, tester) || !containsModel(modelIDs, reviewer) {
		return modelSettings{}, fmt.Errorf("%s must include the configured coder, test-writer, and reviewer defaults", envModelCatalog)
	}

	catalog, err := llm.NewModelCatalog(factory, config.Model, modelIDs)
	if err != nil {
		return modelSettings{}, err
	}
	return modelSettings{catalog: catalog, coder: coder, tester: tester, reviewer: reviewer}, nil
}

func uniqueModels(modelIDs ...string) []string {
	unique := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		if !containsModel(unique, modelID) {
			unique = append(unique, modelID)
		}
	}
	return unique
}

func configuredModel(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func containsModel(modelIDs []string, want string) bool {
	for _, modelID := range modelIDs {
		if modelID == want {
			return true
		}
	}
	return false
}

func browserPresets() []server.Preset {
	return []server.Preset{
		{
			Name: "split-cents",
			Spec: `Implement SplitCents(total, recipients int) ([]int, error).

Split a non-negative number of cents among a positive number of recipients. Return a slice
whose length is exactly recipients. Every recipient gets total / recipients cents, and the
remaining total % recipients cents go one at a time to the earliest recipients (lower index).

For total < 0 or recipients <= 0, return a nil slice and a non-nil error. Do not panic.`,
			Signature: "func SplitCents(total, recipients int) ([]int, error)",
		},
		{
			Name: "word-wrap",
			Spec: `Implement WrapWords(text string, width int) ([]string, error).

Treat each maximal run of Unicode whitespace as one separator. Return lines containing words in
their original order, separated by one ASCII space. Pack as many whole words as fit on each line
without exceeding width. A word longer than width must occupy a line by itself. Empty or
whitespace-only text returns an empty slice. Return a non-nil error when width is less than one.
Do not split words and do not panic.`,
			Signature: "func WrapWords(text string, width int) ([]string, error)",
		},
		{
			Name: "semver-compare",
			Spec: `Implement CompareSemver(left, right string) (int, error) for semantic versions.

Accept only MAJOR.MINOR.PATCH with optional prerelease suffix -identifier.identifier. Each core
component is a non-negative decimal integer with no leading zero unless it is exactly zero.
Prerelease identifiers are non-empty ASCII letters, digits, or hyphens. Compare core components
numerically. A release is greater than its prerelease. Compare numeric prerelease identifiers
numerically; numeric identifiers sort before non-numeric ones; otherwise compare identifiers
lexicographically by ASCII. Return -1, 0, or 1. Return a non-nil error for invalid input.`,
			Signature: "func CompareSemver(left, right string) (int, error)",
		},
	}
}

func exitFailure(kind string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", kind, err)
	os.Exit(1)
}

func printAttempt(attempt domain.Attempt) error {
	status := "failed"
	if attempt.Passed {
		status = "passed"
	}
	fmt.Printf("attempt %d: %s\n", attempt.N, status)
	if attempt.Output == "" {
		fmt.Println("verifier output: (none)")
		return nil
	}

	fmt.Println("verifier output:")
	fmt.Print(attempt.Output)
	if !strings.HasSuffix(attempt.Output, "\n") {
		fmt.Println()
	}
	return nil
}

func splitCentsTask() domain.Task {
	return domain.Task{
		Name:   "split-cents",
		Oracle: domain.OracleAuthored,
		Spec: `Implement SplitCents(total, recipients int) ([]int, error).

Split a non-negative number of cents among a positive number of recipients. Return a slice
whose length is exactly recipients. Every recipient gets total / recipients cents, and the
remaining total % recipients cents go one at a time to the earliest recipients (lower index).

For total < 0 or recipients <= 0, return a nil slice and a non-nil error. Do not panic.`,
		Signature: "func SplitCents(total, recipients int) ([]int, error)",
		TestCode: `package solution

import (
	"reflect"
	"testing"
)

func TestSplitCentsDistributesRemainderToEarliestRecipients(t *testing.T) {
	tests := []struct {
		name       string
		total      int
		recipients int
		want       []int
	}{
		{name: "even split", total: 12, recipients: 3, want: []int{4, 4, 4}},
		{name: "remainder goes to earliest recipients", total: 100, recipients: 3, want: []int{34, 33, 33}},
		{name: "more recipients than cents", total: 2, recipients: 5, want: []int{1, 1, 0, 0, 0}},
		{name: "zero total", total: 0, recipients: 3, want: []int{0, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitCents(tt.total, tt.recipients)
			if err != nil {
				t.Fatalf("SplitCents(%d, %d) error = %v", tt.total, tt.recipients, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SplitCents(%d, %d) = %v, want %v", tt.total, tt.recipients, got, tt.want)
			}
		})
	}
}

func TestSplitCentsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name       string
		total      int
		recipients int
	}{
		{name: "negative total", total: -1, recipients: 2},
		{name: "zero recipients", total: 1, recipients: 0},
		{name: "negative recipients", total: 1, recipients: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitCents(tt.total, tt.recipients)
			if err == nil {
				t.Fatalf("SplitCents(%d, %d) error = nil, want non-nil", tt.total, tt.recipients)
			}
			if got != nil {
				t.Fatalf("SplitCents(%d, %d) result = %v, want nil", tt.total, tt.recipients, got)
			}
		})
	}
}
`,
	}
}
