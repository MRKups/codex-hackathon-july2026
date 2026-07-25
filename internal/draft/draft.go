// Package draft proposes human-confirmed Go signatures without creating a run or oracle.
package draft

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/prompt"
)

const maxResponseBytes = 2 << 10

var (
	// ErrResponseTooLarge means a model reply exceeded the narrow draft-response limit.
	ErrResponseTooLarge = errors.New("signature draft response is too large")
	// ErrInvalidSignature means a model reply did not contain one valid bodyless Go signature.
	ErrInvalidSignature = errors.New("signature draft response is invalid")
)

// Service performs the narrow signature-drafting operation. It has no dependency on oracle,
// repair, run, or server packages.
type Service struct{}

// NewService constructs the default stateless drafting service.
func NewService() *Service {
	return &Service{}
}

// Suggest returns one syntactically valid bodyless signature. The caller remains responsible for
// displaying it as a draft and applying it only after explicit human action.
func (service *Service) Suggest(ctx context.Context, author llm.LLM, spec string) (string, error) {
	if service == nil {
		return "", errors.New("signature draft service is required")
	}
	if ctx == nil {
		return "", errors.New("signature draft context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if author == nil {
		return "", errors.New("signature draft model is required")
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", errors.New("task specification is required")
	}
	raw, err := author.Complete(ctx, prompt.SignatureDraftPrompt(spec))
	if err != nil {
		return "", err
	}
	if len(raw) > maxResponseBytes {
		return "", fmt.Errorf("%w: exceeds %d bytes", ErrResponseTooLarge, maxResponseBytes)
	}
	signature := strings.TrimSpace(prompt.ExtractGoCode(raw))
	if err := domain.ValidateSignature(signature); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return signature, nil
}
