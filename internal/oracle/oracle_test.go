package oracle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/verification"
)

var _ Resolver = (*DefaultResolver)(nil)

func TestDefaultRulebookIsStableAndGeneric(t *testing.T) {
	rulebook := DefaultRulebook()
	if err := rulebook.Validate(); err != nil {
		t.Fatalf("DefaultRulebook().Validate() error = %v", err)
	}
	if rulebook.Version != RulebookVersionV1 {
		t.Fatalf("Rulebook version = %q, want %q", rulebook.Version, RulebookVersionV1)
	}
	const wantDigest = "19f935c5bb2a23489196b2d4c6e1768812f4fcbe0a99c8eb89f4d3fc703dcea7"
	if got := rulebook.Digest(); got != wantDigest {
		t.Fatalf("Rulebook digest = %q, want %q", got, wantDigest)
	}
	if repeated := DefaultRulebook(); repeated != rulebook || repeated.Digest() != rulebook.Digest() {
		t.Fatalf("DefaultRulebook() was not deterministic: %#v then %#v", rulebook, repeated)
	}
	for _, phrase := range []string{
		"submitted specification and pinned signature",
		"validity, invalid-input/error, boundary, mutation, determinism, round-trip, and metamorphic",
		"trustworthy source",
		"non-trivial answer",
		"exactness, optimality, or tie-breaking",
	} {
		if !strings.Contains(rulebook.Text, phrase) {
			t.Fatalf("Rulebook text omitted universal rule %q", phrase)
		}
	}
	changedText := rulebook
	changedText.Text += "\nExtra general guidance."
	if changedText.Digest() == rulebook.Digest() {
		t.Fatal("changed Rulebook text retained digest")
	}
	changedVersion := rulebook
	changedVersion.Version = "oracle-rulebook/test"
	if changedVersion.Digest() == rulebook.Digest() {
		t.Fatal("changed Rulebook version retained digest")
	}
}

func TestNewResolverRequiresExplicitComponents(t *testing.T) {
	valid := Config{
		MaxAttempts:      1,
		PreflightTimeout: time.Second,
		Rulebook:         DefaultRulebook(),
		Admitter:         NewStructuralAdmitter(),
	}
	if _, err := NewResolver(valid); err != nil {
		t.Fatalf("NewResolver(valid) error = %v", err)
	}

	for _, mutate := range []func(*Config){
		func(config *Config) { config.MaxAttempts = 0 },
		func(config *Config) { config.PreflightTimeout = 0 },
		func(config *Config) { config.Rulebook = Rulebook{} },
		func(config *Config) { config.Admitter = nil },
	} {
		config := valid
		mutate(&config)
		if _, err := NewResolver(config); err == nil {
			t.Fatal("NewResolver(incomplete config) error = nil, want validation error")
		}
	}
}

func TestResolverGeneratesBlindBundleWithRulebookEvidence(t *testing.T) {
	task := generatedTask()
	validSource := incrementOracleSource()
	author := &scriptedAuthor{responses: []authorResponse{{text: "```go\n" + validSource + "```\n"}}}
	resolver := newResolver(t, 1, DefaultRulebook())

	var events []string
	resolution, err := resolver.Resolve(context.Background(), Request{Task: task, Author: author}, ProgressReporter{
		WritingSource:      func() { events = append(events, "writing") },
		PreflightingSource: func() { events = append(events, "preflighting") },
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Bundle.Manifest.Origin != domain.VerificationOriginGenerated || resolution.Bundle.TestCode != validSource {
		t.Fatalf("generated resolution bundle = %#v, want exact sealed generated source", resolution.Bundle)
	}
	if resolution.Evidence.RulebookVersion != RulebookVersionV1 || resolution.Evidence.RulebookDigest != DefaultRulebook().Digest() || resolution.Evidence.AuthorAttempts != 1 {
		t.Fatalf("resolution evidence = %#v, want Rulebook provenance and one author call", resolution.Evidence)
	}
	if strings.Join(events, ",") != "writing,preflighting" {
		t.Fatalf("progress events = %v, want writing then preflighting", events)
	}
	if len(author.prompts) != 1 {
		t.Fatalf("author calls = %d, want 1", len(author.prompts))
	}
	gotPrompt := author.prompts[0]
	for _, want := range []string{task.Spec, task.Signature, DefaultRulebook().PromptText()} {
		if strings.Count(gotPrompt, want) != 1 {
			t.Fatalf("author prompt contained %q %d times, want 1\n%s", want, strings.Count(gotPrompt, want), gotPrompt)
		}
	}
	for _, forbidden := range []string{task.TestCode, "CANDIDATE_SOURCE_SENTINEL", validSource} {
		if strings.Contains(gotPrompt, forbidden) {
			t.Fatalf("author prompt leaked forbidden source %q:\n%s", forbidden, gotPrompt)
		}
	}
}

func TestResolverRetriesOnlyStructuralRejectionsBeforeFreeze(t *testing.T) {
	task := generatedTask()
	rejectedSource := `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if got := Increment(1); got == 99 { t.Fatal("unreachable sentinel") }
	UndefinedOracleSymbol()
}
`
	acceptedSource := incrementOracleSource()
	author := &scriptedAuthor{responses: []authorResponse{{text: rejectedSource}, {text: acceptedSource}}}
	resolver := newResolver(t, 2, DefaultRulebook())
	var events []string
	resolution, err := resolver.Resolve(context.Background(), Request{Task: task, Author: author}, ProgressReporter{
		WritingSource:      func() { events = append(events, "writing") },
		PreflightingSource: func() { events = append(events, "preflighting") },
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Bundle.TestCode != acceptedSource || resolution.Evidence.AuthorAttempts != 2 {
		t.Fatalf("resolution = %#v, want second accepted source", resolution)
	}
	if strings.Join(events, ",") != "writing,preflighting,writing,preflighting" {
		t.Fatalf("progress events = %v", events)
	}
	if len(author.prompts) != 2 || author.prompts[0] != author.prompts[1] {
		t.Fatalf("author prompts = %#v, want the same blind task/Rulebook prompt twice", author.prompts)
	}
}

func TestResolverReportsTypedOracleFailureAndNeverUsesStaleSource(t *testing.T) {
	task := generatedTask()
	badSource := `package solution

import "testing"
`
	author := &scriptedAuthor{responses: []authorResponse{{text: badSource}, {text: badSource}}}
	resolver := newResolver(t, 2, DefaultRulebook())
	resolution, err := resolver.Resolve(context.Background(), Request{Task: task, Author: author}, ProgressReporter{})
	if resolution != (Resolution{}) {
		t.Fatalf("failed resolution = %#v, want zero value", resolution)
	}
	var oracleErr *OracleFailureError
	if !errors.As(err, &oracleErr) || oracleErr.Attempts != 2 {
		t.Fatalf("Resolve() error = %v, want typed two-attempt OracleFailureError", err)
	}
	if !strings.Contains(oracleErr.Output, "runnable Test function") {
		t.Fatalf("OracleFailureError output = %q, want structural rejection", oracleErr.Output)
	}
	for _, promptText := range author.prompts {
		if strings.Contains(promptText, task.TestCode) {
			t.Fatalf("author prompt included stale caller test source:\n%s", promptText)
		}
	}
}

func TestResolverAuthoredSourceNeedsNoAuthorAndHasNoRulebookEvidence(t *testing.T) {
	task := domain.Task{
		Name:      "increment",
		Spec:      "Return the input plus one.",
		Signature: "func Increment(value int) int",
		Oracle:    domain.OracleAuthored,
		TestCode:  incrementOracleSource(),
	}
	resolver := newResolver(t, 1, DefaultRulebook())
	resolution, err := resolver.Resolve(context.Background(), Request{Task: task}, ProgressReporter{})
	if err != nil {
		t.Fatalf("Resolve(authored) error = %v", err)
	}
	if resolution.Bundle.Manifest.Origin != domain.VerificationOriginAuthored || resolution.Bundle.TestCode != task.TestCode {
		t.Fatalf("authored bundle = %#v", resolution.Bundle)
	}
	if resolution.Evidence != (Evidence{}) {
		t.Fatalf("authored evidence = %#v, want no generated-policy evidence", resolution.Evidence)
	}
}

func TestRulebookProvenanceDoesNotChangeExecutableBundleDigest(t *testing.T) {
	task := generatedTask()
	source := incrementOracleSource()
	firstRulebook := DefaultRulebook()
	secondRulebook := firstRulebook
	secondRulebook.Version = "oracle-rulebook/test-v2"

	first, err := newResolver(t, 1, firstRulebook).Resolve(context.Background(), Request{
		Task: task, Author: &scriptedAuthor{responses: []authorResponse{{text: source}}},
	}, ProgressReporter{})
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}
	second, err := newResolver(t, 1, secondRulebook).Resolve(context.Background(), Request{
		Task: task, Author: &scriptedAuthor{responses: []authorResponse{{text: source}}},
	}, ProgressReporter{})
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if first.Bundle.Manifest.Digest != second.Bundle.Manifest.Digest {
		t.Fatalf("Rulebook provenance changed executable bundle digest: %q vs %q", first.Bundle.Manifest.Digest, second.Bundle.Manifest.Digest)
	}
	if first.Evidence.RulebookDigest == second.Evidence.RulebookDigest {
		t.Fatal("changed Rulebook provenance retained digest")
	}
}

func TestValidateResolutionEnforcesBundleProvenance(t *testing.T) {
	authoredTask := domain.Task{
		Name:      "increment",
		Spec:      "Return the input plus one.",
		Signature: "func Increment(value int) int",
		Oracle:    domain.OracleAuthored,
		TestCode:  incrementOracleSource(),
	}
	generatedTask := authoredTask
	generatedTask.Oracle = domain.OracleGenerated
	generatedTask.TestCode = ""

	authoredBundle, err := verification.AuthoredSource(authoredTask, incrementOracleSource())
	if err != nil {
		t.Fatalf("AuthoredSource() error = %v", err)
	}
	generatedBundle, err := verification.GeneratedSource(generatedTask, incrementOracleSource())
	if err != nil {
		t.Fatalf("GeneratedSource() error = %v", err)
	}
	generatedEvidence := Evidence{RulebookVersion: "test-rulebook", RulebookDigest: "test-digest", AuthorAttempts: 1}

	tests := []struct {
		name       string
		task       domain.Task
		resolution Resolution
		wantErr    bool
	}{
		{
			name:       "valid authored",
			task:       authoredTask,
			resolution: Resolution{Bundle: authoredBundle},
		},
		{
			name:       "valid generated",
			task:       generatedTask,
			resolution: Resolution{Bundle: generatedBundle, Evidence: generatedEvidence},
		},
		{
			name:       "generated task cannot claim authored source",
			task:       generatedTask,
			resolution: Resolution{Bundle: authoredBundle, Evidence: generatedEvidence},
			wantErr:    true,
		},
		{
			name:       "authored task cannot claim generated source",
			task:       authoredTask,
			resolution: Resolution{Bundle: generatedBundle},
			wantErr:    true,
		},
		{
			name:       "generated source requires evidence",
			task:       generatedTask,
			resolution: Resolution{Bundle: generatedBundle},
			wantErr:    true,
		},
		{
			name: "tampered bundle",
			task: generatedTask,
			resolution: Resolution{
				Bundle:   domain.VerificationBundle{Manifest: generatedBundle.Manifest, TestCode: generatedBundle.TestCode + "// drift\n"},
				Evidence: generatedEvidence,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResolution(tt.task, tt.resolution)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateResolution() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestPreflightOracleStructuralAdmission(t *testing.T) {
	stub, functionName, err := signatureStub("func Increment(value int) int")
	if err != nil {
		t.Fatalf("signatureStub() error = %v", err)
	}

	tests := []struct {
		name       string
		source     string
		wantOutput string
	}{
		{
			name: "requires runnable Test function",
			source: `package solution

import "testing"

func Testlowercase(t *testing.T) {}
`,
			wantOutput: "runnable Test function",
		},
		{
			name: "rejects build constraints",
			source: `//go:build ignore

package solution

import "testing"

func TestIncrement(t *testing.T) {}
`,
			wantOutput: "build constraints",
		},
		{
			name: "rejects legacy build constraints",
			source: `// +build ignore

package solution

import "testing"

func TestIncrement(t *testing.T) {}
`,
			wantOutput: "build constraints",
		},
		{
			name: "rejects process hooks",
			source: `package solution

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
			name: "rejects init hook",
			source: `package solution

import "testing"

func init() {}
func TestIncrement(t *testing.T) {}
`,
			wantOutput: "TestMain or init",
		},
		{
			name: "requires a testing failure method",
			source: `package solution

import "testing"

func TestIncrement(t *testing.T) { Increment(1) }
`,
			wantOutput: "testing failure method",
		},
		{
			name: "rejects skipped test",
			source: `package solution

import "testing"

func TestIncrement(t *testing.T) {
	Increment(1)
	t.Skip("not a real assertion")
}
`,
			wantOutput: "must not skip tests",
		},
		{
			name: "rejects direct os exit",
			source: `package solution

import (
	"os"
	"testing"
)

func TestIncrement(t *testing.T) {
	Increment(1)
	if false { t.Fatal("unreachable") }
	os.Exit(0)
}
`,
			wantOutput: "call os.Exit",
		},
		{
			name: "rejects embed directive",
			source: `package solution

import (
	_ "embed"
	"testing"
)

//go:embed solution.go
var candidateSource string

func TestIncrement(t *testing.T) {
	if got := Increment(1); got != 2 { t.Fatal(candidateSource) }
}
`,
			wantOutput: "go:embed",
		},
		{
			name: "rejects uncalled assertion helper",
			source: `package solution

import "testing"

func TestIncrement(t *testing.T) {}
func hidden(t *testing.T) {
	if got := Increment(1); got != 2 { t.Fatal("hidden") }
}
`,
			wantOutput: "call required function Increment",
		},
		{
			name: "requires target and failure in the same runnable test",
			source: `package solution

import "testing"

func TestCallsIncrement(t *testing.T) { Increment(1) }
func TestFailureWithoutIncrement(t *testing.T) { t.Fatal("not coupled") }
`,
			wantOutput: "same runnable Test",
		},
		{
			name: "rejects constant false assertion",
			source: `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if false {
		if got := Increment(1); got != 2 { t.Fatal("hidden") }
	}
}
`,
			wantOutput: "call required function Increment",
		},
		{
			name: "rejects shadowed target name",
			source: `package solution

import "testing"

func TestIncrement(t *testing.T) {
	Increment := func(value int) int { return value + 1 }
	if got := Increment(1); got != 2 { t.Fatal("shadowed") }
}
`,
			wantOutput: "call required function Increment",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accepted, output, err := preflightOracleForFunction(context.Background(), stub, tt.source, time.Second, functionName)
			if err != nil {
				t.Fatalf("preflightOracleForFunction() error = %v", err)
			}
			if accepted || !strings.Contains(output, tt.wantOutput) {
				t.Fatalf("preflight = (%t, %q), want rejection containing %q", accepted, output, tt.wantOutput)
			}
		})
	}
}

func TestPreflightOracleCompilesWithoutRunningAndAcceptsReachableHelper(t *testing.T) {
	stub, functionName, err := signatureStub("func Increment(value int) int")
	if err != nil {
		t.Fatalf("signatureStub() error = %v", err)
	}
	notRun := `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if false { panic("ORACLE_TEST_BODY_RAN") }
	if got := Increment(1); got != 2 { t.Fatal("ORACLE_TEST_FAILURE_METHOD_PRESENT") }
}
`
	accepted, output, err := preflightOracle(context.Background(), stub, notRun, time.Second)
	if err != nil || !accepted || output != "" {
		t.Fatalf("preflightOracle() = (%t, %q, %v), want accepted compilation without execution", accepted, output, err)
	}

	reachable := `package solution

import "testing"

func assertIncrement(t *testing.T, value, want int) {
	t.Helper()
	if got := Increment(value); got != want { t.Fatalf("Increment(%d) = %d, want %d", value, got, want) }
}

func TestIncrement(t *testing.T) {
	t.Run("one", func(t *testing.T) { assertIncrement(t, 1, 2) })
}
`
	accepted, output, err = preflightOracleForFunction(context.Background(), stub, reachable, time.Second, functionName)
	if err != nil || !accepted || output != "" {
		t.Fatalf("reachable helper preflight = (%t, %q, %v), want accepted", accepted, output, err)
	}

	accepted, output, err = preflightOracle(context.Background(), "package solution\n", "package solution\n", 0)
	if err == nil || accepted || output != "" {
		t.Fatalf("non-positive timeout preflight = (%t, %q, %v), want validation error", accepted, output, err)
	}
}

func newResolver(t *testing.T, attempts int, rulebook Rulebook) *DefaultResolver {
	t.Helper()
	resolver, err := NewResolver(Config{
		MaxAttempts:      attempts,
		PreflightTimeout: 10 * time.Second,
		Rulebook:         rulebook,
		Admitter:         NewStructuralAdmitter(),
	})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	return resolver
}

func generatedTask() domain.Task {
	return domain.Task{
		Name:      "increment",
		Spec:      "Return the input integer increased by one.",
		Signature: "func Increment(value int) int",
		Oracle:    domain.OracleGenerated,
		TestCode:  "STALE_ORACLE_SOURCE_SENTINEL",
	}
}

func incrementOracleSource() string {
	return `package solution

import "testing"

func TestIncrement(t *testing.T) {
	if got := Increment(2); got != 3 {
		t.Fatalf("Increment(2) = %d, want 3", got)
	}
}
`
}

type authorResponse struct {
	text string
	err  error
}

type scriptedAuthor struct {
	responses []authorResponse
	prompts   []string
}

func (author *scriptedAuthor) Complete(_ context.Context, promptText string) (string, error) {
	author.prompts = append(author.prompts, promptText)
	if len(author.responses) == 0 {
		return "", errors.New("unexpected author completion")
	}
	response := author.responses[0]
	author.responses = author.responses[1:]
	return response.text, response.err
}
