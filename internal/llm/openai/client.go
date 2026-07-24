// Package openai adapts the official OpenAI Go SDK to the repair loop's text-completion boundary.
package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"codex-hackathon-july2026/internal/llm"
)

const (
	// ProviderID is the configured provider selector for this adapter.
	ProviderID = "openai"
	// EnvBaseURL is the environment variable containing the OpenAI-compatible Chat Completions base URL.
	EnvBaseURL = "LLM_BASE_URL"
	// EnvAPIKey is the environment variable containing the provider API key.
	EnvAPIKey = "LLM_API_KEY"

	maxCompletionRetries = 1
	maxResponseBytes     = 1 << 20
)

// ErrResponseTooLarge reports a completion response exceeding the retained safety bound.
var ErrResponseTooLarge = fmt.Errorf("completion response exceeds %d bytes", maxResponseBytes)

// Config supplies OpenAI-specific configuration. HTTPClient is optional and exists so tests can
// use the SDK with an in-memory transport; production callers leave it nil.
type Config struct {
	BaseURL    string
	APIKey     string
	Timeout    time.Duration
	HTTPClient *http.Client
}

// ConfigFromEnv loads this adapter's endpoint and credential, while timeout remains owned by the
// provider-neutral runtime configuration at the composition root.
func ConfigFromEnv(timeout time.Duration) (Config, error) {
	config := Config{
		BaseURL: strings.TrimSpace(os.Getenv(EnvBaseURL)),
		APIKey:  strings.TrimSpace(os.Getenv(EnvAPIKey)),
		Timeout: timeout,
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// Validate checks the explicit endpoint, credential, and whole-call timeout required by the
// OpenAI Chat Completions adapter.
func (config Config) Validate() error {
	if strings.TrimSpace(config.BaseURL) == "" {
		return fmt.Errorf("%s must be set", EnvBaseURL)
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("%s must be set", EnvAPIKey)
	}
	if config.Timeout <= 0 {
		return errors.New("LLM timeout must be greater than zero")
	}

	parsedURL, err := url.Parse(config.BaseURL)
	if err != nil {
		return fmt.Errorf("parse LLM base URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return errors.New("LLM base URL must use http or https")
	}
	if parsedURL.Host == "" {
		return errors.New("LLM base URL must include a host")
	}
	if parsedURL.User != nil {
		return errors.New("LLM base URL must not include user credentials")
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return errors.New("LLM base URL must not include a query or fragment")
	}
	return nil
}

// Factory creates one stateless SDK-backed client per configured model ID.
type Factory struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
}

// NewFactory constructs a factory whose SDK service receives only explicitly configured options.
// It intentionally uses NewChatCompletionService rather than NewClient so ambient OPENAI_* SDK
// environment settings cannot influence this application's configured provider connection.
func NewFactory(config Config) (*Factory, error) {
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	config.APIKey = strings.TrimSpace(config.APIKey)
	if err := config.Validate(); err != nil {
		return nil, err
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Factory{
		apiKey:     config.APIKey,
		baseURL:    config.BaseURL,
		httpClient: boundedHTTPClient(httpClient),
		timeout:    config.Timeout,
	}, nil
}

// New creates a reusable LLM for modelID.
func (factory *Factory) New(modelID string) (llm.LLM, error) {
	if factory == nil {
		return nil, errors.New("OpenAI client factory is required")
	}
	id := strings.TrimSpace(modelID)
	if id == "" {
		return nil, llm.ErrEmptyModelID
	}

	service := openaisdk.NewChatCompletionService(
		option.WithAPIKey(factory.apiKey),
		option.WithBaseURL(factory.baseURL),
		option.WithMaxRetries(maxCompletionRetries),
		option.WithHTTPClient(factory.httpClient),
	)
	return &Client{model: id, service: service, timeout: factory.timeout}, nil
}

// Client is a stateless, non-streaming Chat Completions adapter for one configured model.
type Client struct {
	model   string
	service openaisdk.ChatCompletionService
	timeout time.Duration
}

// Complete sends exactly one user message and returns the first text choice. The SDK owns the
// configured retry; the context deadline spans all of its attempts.
func (client *Client) Complete(ctx context.Context, prompt string) (string, error) {
	if ctx == nil {
		return "", errors.New("completion context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("completion prompt must not be empty")
	}

	callContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()

	completion, err := client.service.New(callContext, openaisdk.ChatCompletionNewParams{
		Model: client.model,
		Messages: []openaisdk.ChatCompletionMessageParamUnion{
			openaisdk.UserMessage(prompt),
		},
	})
	if err != nil {
		if contextErr := callContext.Err(); contextErr != nil {
			return "", fmt.Errorf("LLM completion context ended: %w", contextErr)
		}
		if errors.Is(err, ErrResponseTooLarge) {
			return "", ErrResponseTooLarge
		}
		var providerErr *openaisdk.Error
		if errors.As(err, &providerErr) {
			return "", &llm.HTTPStatusError{StatusCode: providerErr.StatusCode}
		}
		return "", fmt.Errorf("send completion request: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", errors.New("completion response has no choices")
	}

	content := completion.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return "", errors.New("completion response has an empty first choice")
	}
	return content, nil
}

func boundedHTTPClient(client *http.Client) *http.Client {
	bounded := *client
	transport := bounded.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	bounded.Transport = responseLimitTransport{next: transport, maxBytes: maxResponseBytes}
	return &bounded
}

type responseLimitTransport struct {
	next     http.RoundTripper
	maxBytes int64
}

func (transport responseLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	response.Body = &responseLimitBody{
		ReadCloser: response.Body,
		remaining:  transport.maxBytes + 1,
	}
	return response, nil
}

type responseLimitBody struct {
	io.ReadCloser
	remaining int64
}

func (body *responseLimitBody) Read(buffer []byte) (int, error) {
	if body.remaining <= 0 {
		return 0, ErrResponseTooLarge
	}
	if int64(len(buffer)) > body.remaining {
		buffer = buffer[:int(body.remaining)]
	}

	read, err := body.ReadCloser.Read(buffer)
	body.remaining -= int64(read)
	if read > 0 && body.remaining == 0 {
		return read, ErrResponseTooLarge
	}
	return read, err
}
