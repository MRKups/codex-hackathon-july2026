package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// EnvBaseURL is the environment variable containing an OpenAI-compatible API base URL.
	EnvBaseURL = "LLM_BASE_URL"
	// EnvAPIKey is the environment variable containing the provider API key.
	EnvAPIKey = "LLM_API_KEY"
	// EnvModel is the environment variable containing the model name.
	EnvModel = "LLM_MODEL"
	// EnvTimeout is the environment variable containing a Go duration for one completion call.
	EnvTimeout = "LLM_TIMEOUT"

	maxCompletionAttempts = 2
	maxResponseBytes      = 1 << 20
)

// Config supplies all provider-specific settings to a Client.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// ConfigFromEnv loads the client configuration from the documented environment variables.
func ConfigFromEnv() (Config, error) {
	timeoutValue := strings.TrimSpace(os.Getenv(EnvTimeout))
	if timeoutValue == "" {
		return Config{}, fmt.Errorf("%s must be set", EnvTimeout)
	}

	timeout, err := time.ParseDuration(timeoutValue)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", EnvTimeout, err)
	}

	config := Config{
		BaseURL: strings.TrimSpace(os.Getenv(EnvBaseURL)),
		APIKey:  strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Model:   strings.TrimSpace(os.Getenv(EnvModel)),
		Timeout: timeout,
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}

	return config, nil
}

// Validate checks that Config has everything needed to make a completion request.
func (config Config) Validate() error {
	if config.BaseURL == "" {
		return fmt.Errorf("%s must be set", EnvBaseURL)
	}
	if config.APIKey == "" {
		return fmt.Errorf("%s must be set", EnvAPIKey)
	}
	if config.Model == "" {
		return fmt.Errorf("%s must be set", EnvModel)
	}
	if config.Timeout <= 0 {
		return fmt.Errorf("LLM timeout must be greater than zero")
	}

	parsedURL, err := url.Parse(config.BaseURL)
	if err != nil {
		return fmt.Errorf("parse LLM base URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("LLM base URL must use http or https")
	}
	if parsedURL.Host == "" {
		return fmt.Errorf("LLM base URL must include a host")
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return fmt.Errorf("LLM base URL must not include a query or fragment")
	}

	return nil
}

// Client sends prompts to an OpenAI-compatible chat completions endpoint.
type Client struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	model      string
	timeout    time.Duration
}

// NewClient constructs a client for the configured API base URL. The base URL may include
// a version path such as /v1; the chat completions path is added by the client.
func NewClient(config Config) (*Client, error) {
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.Model = strings.TrimSpace(config.Model)
	if err := config.Validate(); err != nil {
		return nil, err
	}

	endpoint, err := url.JoinPath(config.BaseURL, "chat/completions")
	if err != nil {
		return nil, fmt.Errorf("build chat completions URL: %w", err)
	}

	return &Client{
		apiKey:     config.APIKey,
		endpoint:   endpoint,
		httpClient: &http.Client{},
		model:      config.Model,
		timeout:    config.Timeout,
	}, nil
}

// Complete submits one prompt and returns the first response choice. It retries once after
// a transient network, rate-limit, or server failure.
func (client *Client) Complete(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("completion prompt must not be empty")
	}

	callContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()

	var lastErr error
	for attempt := 1; attempt <= maxCompletionAttempts; attempt++ {
		content, retry, err := client.completeOnce(callContext, prompt)
		if err == nil {
			return content, nil
		}

		lastErr = err
		if !retry || callContext.Err() != nil {
			break
		}
	}

	if err := callContext.Err(); err != nil {
		return "", fmt.Errorf("LLM completion context ended: %w", err)
	}
	return "", lastErr
}

func (client *Client) completeOnce(ctx context.Context, prompt string) (string, bool, error) {
	body, err := json.Marshal(completionRequest{
		Model: client.model,
		Messages: []chatMessage{{
			Role:    "user",
			Content: prompt,
		}},
	})
	if err != nil {
		return "", false, fmt.Errorf("marshal completion request: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return "", false, fmt.Errorf("create completion request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Content-Type", "application/json")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", ctx.Err() == nil, fmt.Errorf("send completion request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		retry := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError
		return "", retry, &httpStatusError{statusCode: response.StatusCode}
	}

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return "", ctx.Err() == nil, fmt.Errorf("read completion response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return "", false, fmt.Errorf("completion response exceeds %d bytes", maxResponseBytes)
	}

	var completion completionResponse
	if err := json.Unmarshal(responseBody, &completion); err != nil {
		return "", false, fmt.Errorf("decode completion response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", false, fmt.Errorf("completion response has no choices")
	}

	content := completion.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return "", false, fmt.Errorf("completion response has an empty first choice")
	}

	return content, false, nil
}

type httpStatusError struct {
	statusCode int
}

func (err *httpStatusError) Error() string {
	return fmt.Sprintf("completion request returned HTTP %d", err.statusCode)
}

type completionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionResponse struct {
	Choices []completionChoice `json:"choices"`
}

type completionChoice struct {
	Message chatMessage `json:"message"`
}
