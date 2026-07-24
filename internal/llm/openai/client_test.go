package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex-hackathon-july2026/internal/llm"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://llm.example/v1")
	t.Setenv(EnvAPIKey, "test-key")

	config, err := ConfigFromEnv(2 * time.Second)
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}
	if config.BaseURL != "https://llm.example/v1" {
		t.Errorf("BaseURL = %q, want configured URL", config.BaseURL)
	}
	if config.APIKey != "test-key" {
		t.Errorf("APIKey = %q, want configured key", config.APIKey)
	}
	if config.Timeout != 2*time.Second {
		t.Errorf("Timeout = %s, want 2s", config.Timeout)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "missing base URL", config: Config{APIKey: "key", Timeout: time.Second}},
		{name: "missing API key", config: Config{BaseURL: "https://llm.example", Timeout: time.Second}},
		{name: "invalid scheme", config: Config{BaseURL: "ftp://llm.example", APIKey: "key", Timeout: time.Second}},
		{name: "embedded user credentials", config: Config{BaseURL: "https://user:password@llm.example", APIKey: "key", Timeout: time.Second}},
		{name: "query", config: Config{BaseURL: "https://llm.example/v1?x=1", APIKey: "key", Timeout: time.Second}},
		{name: "non-positive timeout", config: Config{BaseURL: "https://llm.example", APIKey: "key"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.config.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
		})
	}
}

func TestClientCompleteSendsExplicitStatelessChatRequest(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-key-that-must-not-be-used")
	t.Setenv("OPENAI_BASE_URL", "https://ambient.example/v1")

	client := newTestClient(t, time.Second, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.String() != "https://llm.example/v1/chat/completions" {
			t.Errorf("URL = %q, want configured chat endpoint", request.URL)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer test-key" {
			t.Errorf("Authorization = %q, want configured bearer token", authorization)
		}
		if contentType := request.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", contentType)
		}

		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if body.Model != "test-model" {
			t.Errorf("model = %q, want test-model", body.Model)
		}
		if len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "write Solve" {
			t.Errorf("messages = %#v, want one user prompt", body.Messages)
		}
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"package solution"}}]}`), nil
	}))

	content, err := client.Complete(context.Background(), "write Solve")
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if content != "package solution" {
		t.Errorf("Complete() = %q, want package solution", content)
	}
}

func TestClientCompleteRetriesOnce(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, time.Second, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return jsonResponse(http.StatusInternalServerError, `{"error":{"message":"temporary"}}`), nil
		}
		return jsonResponse(http.StatusOK, `{"choices":[{"message":{"content":"recovered"}}]}`), nil
	}))

	content, err := client.Complete(context.Background(), "retry me")
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if content != "recovered" {
		t.Errorf("Complete() = %q, want recovered", content)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestClientCompleteRedactsProviderErrorBody(t *testing.T) {
	const secret = "provider detail that must not escape the client"
	client := newTestClient(t, time.Second, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, `{"error":{"message":"`+secret+`"}}`), nil
	}))

	_, err := client.Complete(context.Background(), "reject me")
	if err == nil {
		t.Fatal("Complete() error = nil, want HTTP status error")
	}
	var statusError *llm.HTTPStatusError
	if !errors.As(err, &statusError) {
		t.Fatalf("Complete() error = %T, want *llm.HTTPStatusError", err)
	}
	if statusError.StatusCode != http.StatusUnauthorized {
		t.Errorf("HTTP status = %d, want %d", statusError.StatusCode, http.StatusUnauthorized)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("Complete() error leaked provider response body: %q", err)
	}
	if errors.Unwrap(err) != nil {
		t.Errorf("Complete() error wraps an SDK error that could expose provider data: %v", errors.Unwrap(err))
	}
}

func TestClientCompleteHonorsContextDeadlineAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		client := newTestClient(t, 10*time.Millisecond, blockingTransport{})
		_, err := client.Complete(context.Background(), "wait forever")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Complete() error = %v, want deadline exceeded", err)
		}
	})

	t.Run("caller cancellation avoids request", func(t *testing.T) {
		var calls atomic.Int32
		client := newTestClient(t, time.Second, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("request should not be sent")
		}))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.Complete(ctx, "already canceled")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Complete() error = %v, want canceled", err)
		}
		if got := calls.Load(); got != 0 {
			t.Errorf("requests = %d, want 0", got)
		}
	})
}

func TestClientCompleteRejectsInvalidOutput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "no choices", body: `{}`, want: "no choices"},
		{name: "empty first choice", body: `{"choices":[{"message":{"content":" \n "}}]}`, want: "empty first choice"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, time.Second, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, test.body), nil
			}))
			_, err := client.Complete(context.Background(), "produce output")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Complete() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestClientCompleteRetainsResponseSizeLimit(t *testing.T) {
	body := `{"choices":[{"message":{"content":"` + strings.Repeat("x", maxResponseBytes) + `"}}]}`
	client := newTestClient(t, time.Second, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, body), nil
	}))

	_, err := client.Complete(context.Background(), "large response")
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Complete() error = %v, want errors.Is(_, ErrResponseTooLarge)", err)
	}
}

func TestFactoryRejectsEmptyModelID(t *testing.T) {
	factory, err := NewFactory(Config{BaseURL: "https://llm.example/v1", APIKey: "test-key", Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	if _, err := factory.New("  "); !errors.Is(err, llm.ErrEmptyModelID) {
		t.Fatalf("Factory.New(empty) error = %v, want errors.Is(_, ErrEmptyModelID)", err)
	}
}

func newTestClient(t *testing.T, timeout time.Duration, transport http.RoundTripper) llm.LLM {
	t.Helper()
	factory, err := NewFactory(Config{
		BaseURL:    "https://llm.example/v1",
		APIKey:     "test-key",
		Timeout:    timeout,
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	client, err := factory.New("test-model")
	if err != nil {
		t.Fatalf("Factory.New() error = %v", err)
	}
	return client
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type blockingTransport struct{}

func (blockingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	<-request.Context().Done()
	return nil, request.Context().Err()
}
