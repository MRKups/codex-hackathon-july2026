// Package repair generates Go candidates and verifies them against an already frozen bundle.
package repair

import (
	"context"
	"errors"
	"fmt"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/prompt"
	"codex-hackathon-july2026/internal/verification"
)

// ProgressReporter receives completed candidate-verification attempts synchronously. A callback
// error stops the loop and is returned to the caller. Oracle-resolution progress belongs to the
// lower oracle component and is intentionally not part of candidate repair.
type ProgressReporter struct {
	AttemptFinished func(domain.Attempt) error
}

// Config contains the injected limits for one candidate-repair run.
type Config struct {
	MaxAttempts int
	TestTimeout time.Duration
}

// CandidateRequest is the narrow task contract for candidate-side work. The sealed bundle
// necessarily carries frozen test source for verification, but the original Task, oracle mode,
// and oracle-resolution evidence are intentionally absent. Default candidate prompts use only
// Spec, Signature, and later verifier feedback; they never receive Bundle.TestCode.
type CandidateRequest struct {
	Spec      string
	Signature string
	Bundle    domain.VerificationBundle
}

// Executor is the candidate-side component consumed by run. It receives only an already sealed
// candidate request, so the caller has no API route for oracle-authoring inputs into it. The
// platform injects one concrete executor explicitly rather than assembling a dynamic pipeline.
type Executor interface {
	Execute(context.Context, llm.LLM, CandidateRequest, Config, ProgressReporter) (domain.Attempt, error)
}

// DefaultExecutor runs the standard Go candidate generation and verification loop.
type DefaultExecutor struct{}

// NewExecutor returns the stateless default candidate-side component.
func NewExecutor() DefaultExecutor {
	return DefaultExecutor{}
}

// Execute implements Executor with the standard bounded repair loop.
func (DefaultExecutor) Execute(
	ctx context.Context,
	coder llm.LLM,
	request CandidateRequest,
	config Config,
	report ProgressReporter,
) (domain.Attempt, error) {
	return RepairWithConfig(ctx, coder, request, config, report)
}

// RepairWithConfig generates and verifies up to MaxAttempts candidates against one already
// frozen VerificationBundle. It never accepts a test writer, source-resolution configuration, or
// separately supplied test source; its candidate prompts use only request spec/signature and
// prior verifier feedback. It returns the last completed attempt and only returns an error for
// caller or infrastructure failures. A failed verifier result is a normal attempt, not an error.
func RepairWithConfig(
	ctx context.Context,
	coder llm.LLM,
	request CandidateRequest,
	config Config,
	report ProgressReporter,
) (domain.Attempt, error) {
	if ctx == nil {
		return domain.Attempt{}, errors.New("repair context is required")
	}
	if coder == nil {
		return domain.Attempt{}, errors.New("coder is required")
	}
	if config.MaxAttempts <= 0 {
		return domain.Attempt{}, errors.New("max attempts must be greater than zero")
	}
	if config.TestTimeout <= 0 {
		return domain.Attempt{}, errors.New("verifier timeout must be greater than zero")
	}
	if err := ctx.Err(); err != nil {
		return domain.Attempt{}, err
	}
	if err := verification.ValidateBundle(domain.Task{Spec: request.Spec, Signature: request.Signature}, request.Bundle); err != nil {
		return domain.Attempt{}, fmt.Errorf("validate frozen verification bundle: %w", err)
	}

	var last domain.Attempt
	for attemptNumber := 1; attemptNumber <= config.MaxAttempts; attemptNumber++ {
		code, err := generate(ctx, coder, request.Spec, request.Signature, last)
		if err != nil {
			return last, err
		}

		passed, output, err := runBundleTests(ctx, request.Bundle, code, config.TestTimeout)
		if err != nil {
			return last, err
		}

		last = domain.Attempt{
			N:      attemptNumber,
			Code:   code,
			Passed: passed,
			Output: output,
		}
		if report.AttemptFinished != nil {
			if err := report.AttemptFinished(last); err != nil {
				return last, err
			}
		}
		if passed {
			return last, nil
		}
	}

	return last, nil
}

// generate asks the coder for candidate source. Its primitive inputs deliberately prevent
// verification-bundle source, Rulebook text, and oracle review material from entering a coder
// prompt.
func generate(ctx context.Context, coder llm.LLM, spec, signature string, previous domain.Attempt) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var promptText string
	if previous.N == 0 {
		promptText = prompt.FirstPrompt(spec, signature)
	} else {
		promptText = prompt.RepairPrompt(spec, signature, previous.Code, previous.Output)
	}

	raw, err := coder.Complete(ctx, promptText)
	if err != nil {
		return "", err
	}
	return prompt.ExtractGoCode(raw), nil
}
