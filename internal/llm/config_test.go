package llm

import (
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv(EnvProvider, "openai")
	t.Setenv(EnvModel, "test-model")
	t.Setenv(EnvTimeout, "2s")

	config, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}
	if config.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", config.Provider)
	}
	if config.Model != "test-model" {
		t.Errorf("Model = %q, want test-model", config.Model)
	}
	if config.Timeout != 2*time.Second {
		t.Errorf("Timeout = %s, want 2s", config.Timeout)
	}
}

func TestConfigFromEnvRequiresEverySetting(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		timeout  string
		want     string
	}{
		{name: "provider", model: "model", timeout: "1s", want: EnvProvider},
		{name: "model", provider: "openai", timeout: "1s", want: EnvModel},
		{name: "timeout", provider: "openai", model: "model", want: EnvTimeout},
		{name: "bad timeout", provider: "openai", model: "model", timeout: "nope", want: "parse " + EnvTimeout},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(EnvProvider, test.provider)
			t.Setenv(EnvModel, test.model)
			t.Setenv(EnvTimeout, test.timeout)
			_, err := ConfigFromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ConfigFromEnv() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}
