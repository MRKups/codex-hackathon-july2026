//go:build integration

package llm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
)

const (
	liveTestEnv   = "LLM_LIVE_TEST"
	liveTestOptIn = "run"
)

// TestLiveCompletion verifies a configured provider only when explicitly enabled. It is
// excluded from normal project test runs so developers do not accidentally make a paid request.
func TestLiveCompletion(t *testing.T) {
	if os.Getenv(liveTestEnv) != liveTestOptIn {
		t.Skipf("set %s=%s to run the configured-provider smoke test", liveTestEnv, liveTestOptIn)
	}

	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatal("configured provider settings are invalid")
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatal("configured provider client could not be created")
	}

	endpoint, err := url.Parse(config.BaseURL)
	if err != nil {
		t.Fatal("configured provider base URL could not be parsed")
	}
	if endpoint.Scheme != "https" {
		t.Fatal("configured-provider smoke test requires an https LLM_BASE_URL")
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
	var statusError *httpStatusError
	if errors.As(err, &statusError) {
		return fmt.Sprintf("configured provider returned HTTP %d", statusError.statusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "configured provider request timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "configured provider request was canceled"
	}

	return "configured provider completion failed before returning usable text"
}
