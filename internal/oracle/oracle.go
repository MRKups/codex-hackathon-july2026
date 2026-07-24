package oracle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/prompt"
	"codex-hackathon-july2026/internal/verification"
)

// Resolver is the pre-candidate boundary. Its request deliberately contains no coder, candidate
// source, verifier feedback, or run-store reference.
type Resolver interface {
	Resolve(context.Context, Request, ProgressReporter) (Resolution, error)
}

// Config supplies the fixed policy and limits for one resolver instance. The composition root
// chooses each concrete dependency explicitly; no package reads configuration from the environment.
type Config struct {
	MaxAttempts      int
	PreflightTimeout time.Duration
	Rulebook         Rulebook
	Admitter         Admitter
}

// Request is the complete input accepted by an oracle resolver. Author is needed only for a
// generated task; authored source comes from Task.TestCode.
type Request struct {
	Task   domain.Task
	Author llm.LLM
}

// ProgressReporter contains typed lifecycle notifications for the caller to map into UI/run
// state. They do not let oracle code mutate caller-owned state.
type ProgressReporter struct {
	WritingSource      func()
	PreflightingSource func()
}

// Resolution is the only artifact passed from pre-freeze work into candidate repair. Evidence is
// intentionally separate from the immutable executable bundle manifest.
type Resolution struct {
	Bundle   domain.VerificationBundle
	Evidence Evidence
}

// Evidence records generic facts about the resolution process without claiming semantic truth or
// retaining rejected source, prompts, model wrapper text, or reasoning.
type Evidence struct {
	RulebookVersion string `json:"rulebookVersion,omitempty"`
	RulebookDigest  string `json:"rulebookDigest,omitempty"`
	AuthorAttempts  int    `json:"authorAttempts"`
}

// OracleFailureError means a generated-source resolution exhausted its structural admission cap
// before any candidate code existed. It is distinct from provider, cancellation, and filesystem
// failures, which remain infrastructure errors.
type OracleFailureError struct {
	Attempts int
	Output   string
}

func (err *OracleFailureError) Error() string {
	if strings.TrimSpace(err.Output) == "" {
		return fmt.Sprintf("generated oracle was rejected after %d attempt(s)", err.Attempts)
	}
	return fmt.Sprintf("generated oracle was rejected after %d attempt(s): %s", err.Attempts, err.Output)
}

// DefaultResolver composes the generic generated/authored source flow. It is immutable after
// construction and can be used as the concrete default behind the Resolver boundary.
type DefaultResolver struct {
	config Config
}

// NewResolver validates explicit dependencies and constructs the standard blind source resolver.
func NewResolver(config Config) (*DefaultResolver, error) {
	if config.MaxAttempts <= 0 {
		return nil, errors.New("oracle attempt cap must be greater than zero")
	}
	if config.PreflightTimeout <= 0 {
		return nil, errors.New("oracle preflight timeout must be greater than zero")
	}
	if err := config.Rulebook.Validate(); err != nil {
		return nil, err
	}
	if config.Admitter == nil {
		return nil, errors.New("oracle structural admitter is required")
	}
	return &DefaultResolver{config: config}, nil
}

// ValidateResolution checks the generic handoff contract for any Resolver implementation before
// a caller publishes or executes its bundle. It verifies digest/task binding and makes the
// declared oracle mode agree with the bundle's only legal source origin. This is provenance
// validation, not a semantic claim about the test source.
func ValidateResolution(task domain.Task, resolution Resolution) error {
	if err := verification.ValidateBundle(task, resolution.Bundle); err != nil {
		return fmt.Errorf("validate verification bundle: %w", err)
	}

	mode := task.Oracle
	if mode == "" {
		mode = domain.OracleAuthored
	}
	switch mode {
	case domain.OracleAuthored:
		if resolution.Bundle.Manifest.Origin != domain.VerificationOriginAuthored {
			return fmt.Errorf("authored task requires an authored verification bundle, got %q", resolution.Bundle.Manifest.Origin)
		}
		if resolution.Evidence != (Evidence{}) {
			return errors.New("authored resolution must not contain generated-oracle evidence")
		}
	case domain.OracleGenerated:
		if resolution.Bundle.Manifest.Origin != domain.VerificationOriginGenerated {
			return fmt.Errorf("generated task requires a generated verification bundle, got %q", resolution.Bundle.Manifest.Origin)
		}
		if strings.TrimSpace(resolution.Evidence.RulebookVersion) == "" ||
			strings.TrimSpace(resolution.Evidence.RulebookDigest) == "" ||
			resolution.Evidence.AuthorAttempts <= 0 {
			return errors.New("generated resolution requires Rulebook provenance and at least one source-author attempt")
		}
	default:
		return fmt.Errorf("unknown oracle mode %q", task.Oracle)
	}

	return nil
}

// Resolve fixes one VerificationBundle before any candidate solution exists. Generated source is
// requested with spec, signature, and the checked-in Rulebook only; authored source is admitted
// from trusted local Task.TestCode. Both paths seal the same generic bundle format.
func (resolver *DefaultResolver) Resolve(ctx context.Context, request Request, report ProgressReporter) (Resolution, error) {
	if resolver == nil {
		return Resolution{}, errors.New("oracle resolver is required")
	}
	if ctx == nil {
		return Resolution{}, errors.New("oracle context is required")
	}
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}

	task := request.Task
	switch task.Oracle {
	case "", domain.OracleAuthored:
		task.Oracle = domain.OracleAuthored
		if strings.TrimSpace(task.TestCode) == "" {
			return Resolution{}, errors.New("task test code is required")
		}
		callPreflighting(report)
		admission, err := resolver.config.Admitter.Admit(ctx, task, task.TestCode, resolver.config.PreflightTimeout)
		if err != nil {
			return Resolution{}, err
		}
		if !admission.Accepted {
			return Resolution{}, fmt.Errorf("authored verification source did not pass oracle preflight: %s", admission.Output)
		}
		bundle, err := verification.AuthoredSource(task, task.TestCode)
		if err != nil {
			return Resolution{}, err
		}
		resolution := Resolution{Bundle: bundle}
		if err := ValidateResolution(task, resolution); err != nil {
			return Resolution{}, err
		}
		return resolution, nil

	case domain.OracleGenerated:
		if request.Author == nil {
			return Resolution{}, errors.New("test writer is required for generated oracle tasks")
		}

		// Caller-provided test source is never legal evidence for generated mode. The author prompt
		// has no route for it, and only an admitted response below may become the frozen bundle.
		task.TestCode = ""
		authorPrompt := prompt.TestPrompt(task.Spec, task.Signature) + "\n" + resolver.config.Rulebook.PromptText()
		evidence := Evidence{
			RulebookVersion: resolver.config.Rulebook.Version,
			RulebookDigest:  resolver.config.Rulebook.Digest(),
		}
		var lastOutput string
		for attempt := 1; attempt <= resolver.config.MaxAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return Resolution{}, err
			}

			callWriting(report)
			raw, err := request.Author.Complete(ctx, authorPrompt)
			evidence.AuthorAttempts = attempt
			if err != nil {
				return Resolution{}, err
			}

			candidate := prompt.ExtractGoCode(raw)
			callPreflighting(report)
			admission, err := resolver.config.Admitter.Admit(ctx, task, candidate, resolver.config.PreflightTimeout)
			if err != nil {
				return Resolution{}, err
			}
			if admission.Accepted {
				bundle, err := verification.GeneratedSource(task, candidate)
				if err != nil {
					return Resolution{}, err
				}
				resolution := Resolution{Bundle: bundle, Evidence: evidence}
				if err := ValidateResolution(task, resolution); err != nil {
					return Resolution{}, err
				}
				return resolution, nil
			}
			lastOutput = admission.Output
		}

		return Resolution{}, &OracleFailureError{Attempts: resolver.config.MaxAttempts, Output: lastOutput}

	default:
		return Resolution{}, fmt.Errorf("unknown oracle mode %q", task.Oracle)
	}
}

func callWriting(report ProgressReporter) {
	if report.WritingSource != nil {
		report.WritingSource()
	}
}

func callPreflighting(report ProgressReporter) {
	if report.PreflightingSource != nil {
		report.PreflightingSource()
	}
}
