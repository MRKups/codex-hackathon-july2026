package llm

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// EnvProvider is the environment variable selecting the configured provider factory.
	EnvProvider = "LLM_PROVIDER"
	// EnvModel is the environment variable containing the default model ID.
	EnvModel = "LLM_MODEL"
	// EnvTimeout is the environment variable containing a Go duration for one completion call.
	EnvTimeout = "LLM_TIMEOUT"
)

// Config contains provider-neutral runtime configuration. Provider credentials and endpoints
// are owned by the selected provider package.
type Config struct {
	Provider string
	Model    string
	Timeout  time.Duration
}

// ConfigFromEnv loads provider-neutral runtime configuration from the documented environment.
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
		Provider: strings.TrimSpace(os.Getenv(EnvProvider)),
		Model:    strings.TrimSpace(os.Getenv(EnvModel)),
		Timeout:  timeout,
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}

	return config, nil
}

// Validate checks that the composition root has enough provider-neutral configuration.
func (config Config) Validate() error {
	if strings.TrimSpace(config.Provider) == "" {
		return fmt.Errorf("%s must be set", EnvProvider)
	}
	if strings.TrimSpace(config.Model) == "" {
		return fmt.Errorf("%s must be set", EnvModel)
	}
	if config.Timeout <= 0 {
		return fmt.Errorf("LLM timeout must be greater than zero")
	}
	return nil
}
