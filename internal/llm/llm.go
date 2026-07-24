// Package llm defines the boundary between the repair loop and a text-completion provider.
package llm

import "context"

// LLM completes a prompt with model-generated text.
type LLM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// ClientFactory creates a reusable LLM for one configured provider model ID. It is the only
// provider-facing dependency of ModelCatalog; provider SDK types stay below this boundary.
type ClientFactory interface {
	New(modelID string) (LLM, error)
}

// ClientFactoryFunc adapts a function into a ClientFactory.
type ClientFactoryFunc func(modelID string) (LLM, error)

// New creates an LLM for modelID.
func (factory ClientFactoryFunc) New(modelID string) (LLM, error) {
	return factory(modelID)
}
