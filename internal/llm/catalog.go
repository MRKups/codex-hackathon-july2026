package llm

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrEmptyModelID reports an empty model identifier where a concrete configured model is required.
	ErrEmptyModelID = errors.New("model ID must not be empty")
	// ErrDuplicateModelID reports a repeated model identifier in one configured allowlist.
	ErrDuplicateModelID = errors.New("model ID is configured more than once")
	// ErrUnknownModelID reports a selection outside the catalog's configured allowlist.
	ErrUnknownModelID = errors.New("model ID is not configured")
)

// ModelOption is the safe public description of a model that may be selected for a run.
// It deliberately contains no provider configuration or credentials.
type ModelOption struct {
	ID string `json:"id"`
}

// ModelCatalog owns one reusable LLM client for each configured model identifier. It is safe
// for concurrent reads after construction.
type ModelCatalog struct {
	options []ModelOption
	clients map[string]LLM
}

// ParseModelIDs parses a comma-separated model allowlist. Whitespace around an ID is ignored,
// while empty or duplicate entries are rejected. An entirely empty value returns no IDs so the
// composition root can use its configured default model.
func ParseModelIDs(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	return normalizeModelIDs(strings.Split(value, ","))
}

// NewModelCatalog constructs one reusable Client per allowlisted model. Each client inherits
// the provider base URL, API key, and timeout from base, while its model is set to the matching
// allowlisted ID. If modelIDs is empty, base.Model is the catalog's sole allowed model.
func NewModelCatalog(base Config, modelIDs []string) (*ModelCatalog, error) {
	base.BaseURL = strings.TrimSpace(base.BaseURL)
	base.APIKey = strings.TrimSpace(base.APIKey)
	base.Model = strings.TrimSpace(base.Model)
	if err := base.Validate(); err != nil {
		return nil, fmt.Errorf("validate model catalog base config: %w", err)
	}

	if len(modelIDs) == 0 {
		modelIDs = []string{base.Model}
	}

	ids, err := normalizeModelIDs(modelIDs)
	if err != nil {
		return nil, fmt.Errorf("validate model catalog IDs: %w", err)
	}

	catalog := &ModelCatalog{
		options: make([]ModelOption, 0, len(ids)),
		clients: make(map[string]LLM, len(ids)),
	}
	for _, id := range ids {
		config := base
		config.Model = id

		client, err := NewClient(config)
		if err != nil {
			return nil, fmt.Errorf("create client for configured model: %w", err)
		}

		catalog.options = append(catalog.options, ModelOption{ID: id})
		catalog.clients[id] = client
	}

	return catalog, nil
}

// Options returns a copy of the configured model options in their configured order.
func (catalog *ModelCatalog) Options() []ModelOption {
	if catalog == nil {
		return nil
	}

	options := make([]ModelOption, len(catalog.options))
	copy(options, catalog.options)
	return options
}

// Resolve returns the reusable LLM for an allowed model ID. It rejects blank and unknown
// selections rather than allowing a caller to select an arbitrary provider model.
func (catalog *ModelCatalog) Resolve(modelID string) (LLM, error) {
	id := strings.TrimSpace(modelID)
	if id == "" {
		return nil, ErrEmptyModelID
	}
	if catalog == nil {
		return nil, ErrUnknownModelID
	}

	client, ok := catalog.clients[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownModelID, id)
	}

	return client, nil
}

func normalizeModelIDs(modelIDs []string) ([]string, error) {
	ids := make([]string, 0, len(modelIDs))
	seen := make(map[string]struct{}, len(modelIDs))
	for _, modelID := range modelIDs {
		id := strings.TrimSpace(modelID)
		if id == "" {
			return nil, ErrEmptyModelID
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateModelID, id)
		}

		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	return ids, nil
}
