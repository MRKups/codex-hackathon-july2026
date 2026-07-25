package draft

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex-hackathon-july2026/internal/llm"
)

func TestSuggestReturnsOnlyValidatedSignature(t *testing.T) {
	model := &recordingLLM{response: "```go\nfunc Increment(value int) int\n```"}
	got, err := NewService().Suggest(context.Background(), model, "Return the input plus one.")
	if err != nil || got != "func Increment(value int) int" {
		t.Fatalf("Suggest() = (%q, %v)", got, err)
	}
	if len(model.prompts) != 1 || !strings.Contains(model.prompts[0], "Return the input plus one.") || strings.Contains(model.prompts[0], "ORACLE_SENTINEL") || strings.Contains(model.prompts[0], "CANDIDATE_SENTINEL") {
		t.Fatalf("draft prompt = %#v", model.prompts)
	}
}

func TestSuggestRejectsInvalidOrOversizedOutput(t *testing.T) {
	for _, test := range []struct {
		response string
		want     error
	}{
		{response: "func Broken(value Missing) int", want: ErrInvalidSignature},
		{response: "package solution\nfunc Solve() int { return 1 }", want: ErrInvalidSignature},
		{response: strings.Repeat("x", maxResponseBytes+1), want: ErrResponseTooLarge},
	} {
		if _, err := NewService().Suggest(context.Background(), staticLLM{response: test.response}, "Return one."); !errors.Is(err, test.want) {
			t.Fatalf("Suggest(%q) error = %v, want errors.Is(_, %v)", test.response[:min(len(test.response), 32)], err, test.want)
		}
	}
}

func TestSuggestPropagatesCancellationAndProviderErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewService().Suggest(ctx, staticLLM{}, "Return one."); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Suggest() error = %v", err)
	}
	providerErr := errors.New("provider failed")
	if _, err := NewService().Suggest(context.Background(), staticLLM{err: providerErr}, "Return one."); !errors.Is(err, providerErr) {
		t.Fatalf("provider Suggest() error = %v", err)
	}
}

type recordingLLM struct {
	response string
	prompts  []string
}

func (model *recordingLLM) Complete(_ context.Context, prompt string) (string, error) {
	model.prompts = append(model.prompts, prompt)
	return model.response, nil
}

type staticLLM struct {
	response string
	err      error
}

func (model staticLLM) Complete(_ context.Context, _ string) (string, error) {
	return model.response, model.err
}

var _ llm.LLM = staticLLM{}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
