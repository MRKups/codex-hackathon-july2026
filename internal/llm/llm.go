// Package llm defines the boundary between the repair loop and a text-completion provider.
package llm

import "context"

// LLM completes a prompt with model-generated text.
type LLM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}
