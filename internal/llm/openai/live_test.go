//go:build integration

package openai

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"codex-hackathon-july2026/internal/llm"
)

const (
	liveTestEnv   = "LLM_LIVE_TEST"
	liveTestOptIn = "run"
)

// TestLiveCompletion verifies the configured OpenAI adapter only when explicitly enabled. It is
// excluded from normal project test runs so developers do not accidentally make a paid request.
func TestLiveCompletion(t *testing.T) {
	if os.Getenv(liveTestEnv) != liveTestOptIn {
		t.Skipf("set %s=%s to run the configured-provider smoke test", liveTestEnv, liveTestOptIn)
	}

	runtimeConfig, err := llm.ConfigFromEnv()
	if err != nil {
		t.Fatal("configured provider settings are invalid")
	}
	if runtimeConfig.Provider != ProviderID {
		t.Fatalf("configured-provider smoke test requires %s=%q", llm.EnvProvider, ProviderID)
	}
	providerConfig, err := ConfigFromEnv(runtimeConfig.Timeout)
	if err != nil {
		t.Fatal("configured OpenAI provider settings are invalid")
	}
	endpoint, err := url.Parse(providerConfig.BaseURL)
	if err != nil {
		t.Fatal("configured provider base URL could not be parsed")
	}
	if endpoint.Scheme != "https" {
		t.Fatalf("configured-provider smoke test requires an https %s", EnvBaseURL)
	}

	factory, err := NewFactory(providerConfig)
	if err != nil {
		t.Fatal("configured OpenAI provider factory could not be created")
	}
	client, err := factory.New(runtimeConfig.Model)
	if err != nil {
		t.Fatal("configured OpenAI provider client could not be created")
	}
	content, err := client.Complete(context.Background(), "Reply with exactly: ok")
	if err != nil {
		t.Fatal(liveFailureDescription(err))
	}
	if strings.TrimSpace(content) == "" {
		t.Fatal("Complete() returned empty content")
	}

	t.Logf("received a non-empty completion (%d bytes)", len(content))
}

func liveFailureDescription(err error) string {
	var statusError *llm.HTTPStatusError
	if errors.As(err, &statusError) {
		return fmt.Sprintf("configured provider returned HTTP %d", statusError.StatusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "configured provider request timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "configured provider request was canceled"
	}

	return "configured provider completion failed before returning usable text"
}
