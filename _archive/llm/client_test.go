package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://llm.example/v1")
	t.Setenv(EnvAPIKey, "test-key")
	t.Setenv(EnvModel, "test-model")
	t.Setenv(EnvTimeout, "2s")

	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}

	if config.BaseURL != "https://llm.example/v1" {
		t.Errorf("BaseURL = %q, want %q", config.BaseURL, "https://llm.example/v1")
	}
	if config.APIKey != "test-key" {
		t.Errorf("APIKey = %q, want %q", config.APIKey, "test-key")
	}
	if config.Model != "test-model" {
		t.Errorf("Model = %q, want %q", config.Model, "test-model")
	}
	if config.Timeout != 2*time.Second {
		t.Errorf("Timeout = %s, want %s", config.Timeout, 2*time.Second)
	}
}

func TestConfigFromEnvRequiresTimeout(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://llm.example/v1")
	t.Setenv(EnvAPIKey, "test-key")
	t.Setenv(EnvModel, "test-model")
	t.Setenv(EnvTimeout, "")

	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("ConfigFromEnv() error = nil, want missing timeout error")
	}
}

func TestClientCompleteSendsOpenAICompatibleRequest(t *testing.T) {
	var requestBody completionRequest
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want %s", request.Method, http.MethodPost)
		}
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want %s", request.URL.Path, "/v1/chat/completions")
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", authorization, "Bearer test-key")
		}
		if contentType := request.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}

		response.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(response).Encode(completionResponse{
			Choices: []completionChoice{{
				Message: chatMessage{Role: "assistant", Content: "func Solve() {}"},
			}},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL: server.URL + "/v1",
		APIKey:  "test-key",
		Model:   "test-model",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	content, err := client.Complete(context.Background(), "write Solve")
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if content != "func Solve() {}" {
		t.Errorf("Complete() = %q, want %q", content, "func Solve() {}")
	}
	if requestBody.Model != "test-model" {
		t.Errorf("request model = %q, want %q", requestBody.Model, "test-model")
	}
	if len(requestBody.Messages) != 1 {
		t.Fatalf("request messages = %d, want 1", len(requestBody.Messages))
	}
	if message := requestBody.Messages[0]; message.Role != "user" || message.Content != "write Solve" {
		t.Errorf("request message = %#v, want user prompt", message)
	}
}

func TestClientCompleteRetriesOnceAfterTransientFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(response, "temporary provider failure", http.StatusInternalServerError)
			return
		}

		response.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(response).Encode(completionResponse{
			Choices: []completionChoice{{
				Message: chatMessage{Role: "assistant", Content: "recovered"},
			}},
		}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	content, err := client.Complete(context.Background(), "retry me")
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if content != "recovered" {
		t.Errorf("Complete() = %q, want %q", content, "recovered")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestClientCompleteReturnsHTTPStatusWithoutProviderBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, "provider detail that must not escape the client", http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.Complete(context.Background(), "reject me")
	if err == nil {
		t.Fatal("Complete() error = nil, want HTTP status error")
	}

	var statusError *httpStatusError
	if !errors.As(err, &statusError) {
		t.Fatalf("Complete() error = %T, want *httpStatusError", err)
	}
	if statusError.statusCode != http.StatusUnauthorized {
		t.Errorf("HTTP status = %d, want %d", statusError.statusCode, http.StatusUnauthorized)
	}
	if strings.Contains(err.Error(), "provider detail") {
		t.Errorf("Complete() error leaked provider response body: %q", err)
	}
}

func TestClientCompleteHonorsTimeout(t *testing.T) {
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		<-releaseHandler
	}))
	defer server.Close()
	defer close(releaseHandler)

	client, err := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
		Timeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.Complete(context.Background(), "wait forever")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Complete() error = %v, want deadline exceeded", err)
	}
}
