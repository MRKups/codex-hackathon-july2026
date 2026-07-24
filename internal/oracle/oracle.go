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
	Task          domain.Task
	Author        llm.LLM
	AuthorModel   string
	Reviewer      llm.LLM
	ReviewerModel string
}

// ProgressReporter contains typed lifecycle notifications for the caller to map into UI/run
// state. They do not let oracle code mutate caller-owned state.
type ProgressReporter struct {
	WritingSource      func()
	PreflightingSource func()
	ReviewingSource    func()
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
	RulebookVersion  string          `json:"rulebookVersion,omitempty"`
	RulebookDigest   string          `json:"rulebookDigest,omitempty"`
	AuthorModel      string          `json:"authorModel,omitempty"`
	ReviewerModel    string          `json:"reviewerModel,omitempty"`
	AuthorAttempts   int             `json:"authorAttempts"`
	ReviewerAttempts int             `json:"reviewerAttempts"`
	ReviewVerdict    ReviewVerdict   `json:"reviewVerdict,omitempty"`
	ReviewFindings   []ReviewFinding `json:"reviewFindings,omitempty"`
}

// Clone returns an evidence copy whose findings cannot mutate a published run snapshot.
func (evidence Evidence) Clone() Evidence {
	clone := evidence
	clone.ReviewFindings = append([]ReviewFinding(nil), evidence.ReviewFindings...)
	return clone
}

// IsZero reports whether no generated-oracle evidence is present.
func (evidence Evidence) IsZero() bool {
	return evidence.RulebookVersion == "" && evidence.RulebookDigest == "" &&
		evidence.AuthorModel == "" && evidence.ReviewerModel == "" &&
		evidence.AuthorAttempts == 0 && evidence.ReviewerAttempts == 0 &&
		evidence.ReviewVerdict == "" && len(evidence.ReviewFindings) == 0
}

// OracleFailureError means a generated-source resolution exhausted its structural admission cap
// before any candidate code existed. It is distinct from provider, cancellation, and filesystem
// failures, which remain infrastructure errors.
type OracleFailureError struct {
	Attempts int
	Output   string
	Evidence Evidence
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
		if !resolution.Evidence.IsZero() {
			return errors.New("authored resolution must not contain generated-oracle evidence")
		}
	case domain.OracleGenerated:
		if resolution.Bundle.Manifest.Origin != domain.VerificationOriginGenerated {
			return fmt.Errorf("generated task requires a generated verification bundle, got %q", resolution.Bundle.Manifest.Origin)
		}
		if err := validateSuccessfulEvidence(resolution.Evidence); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown oracle mode %q", task.Oracle)
	}

	return nil
}

// ValidateFailureEvidence admits optional non-source generated-oracle evidence attached to a
// typed failure. It deliberately permits zero evidence for injected test resolvers, while the
// default F25 resolver records all completed author/reviewer work.
func ValidateFailureEvidence(task domain.Task, evidence Evidence) error {
	if evidence.IsZero() {
		return nil
	}
	if task.Oracle != domain.OracleGenerated {
		return errors.New("only generated oracle failures may contain evidence")
	}
	if err := validateEvidenceCore(evidence); err != nil {
		return err
	}
	if evidence.ReviewerAttempts == 0 {
		if evidence.ReviewVerdict != "" || len(evidence.ReviewFindings) != 0 {
			return errors.New("unreviewed oracle failure contains review verdict or findings")
		}
		return nil
	}
	if evidence.ReviewerAttempts != 1 || strings.TrimSpace(evidence.ReviewerModel) == "" {
		return errors.New("reviewed oracle failure requires one reviewer attempt and reviewer model provenance")
	}
	switch evidence.ReviewVerdict {
	case "":
		if len(evidence.ReviewFindings) != 0 {
			return errors.New("unparsed failed review must not contain findings")
		}
		return nil
	case ReviewVerdictRejected, ReviewVerdictRevised:
		return validateFindings(evidence.ReviewFindings, true)
	default:
		return errors.New("failed oracle review has an invalid verdict")
	}
}

func validateSuccessfulEvidence(evidence Evidence) error {
	if err := validateEvidenceCore(evidence); err != nil {
		return err
	}
	if evidence.ReviewerAttempts != 1 || strings.TrimSpace(evidence.ReviewerModel) == "" {
		return errors.New("generated resolution requires one reviewer attempt and reviewer model provenance")
	}
	switch evidence.ReviewVerdict {
	case ReviewVerdictAccepted:
		if len(evidence.ReviewFindings) != 0 {
			return errors.New("accepted review evidence must not contain findings")
		}
	case ReviewVerdictRevised:
		if err := validateFindings(evidence.ReviewFindings, true); err != nil {
			return err
		}
	default:
		return errors.New("generated resolution requires an accepted or revised review verdict")
	}
	return nil
}

func validateEvidenceCore(evidence Evidence) error {
	if strings.TrimSpace(evidence.RulebookVersion) == "" || strings.TrimSpace(evidence.RulebookDigest) == "" ||
		strings.TrimSpace(evidence.AuthorModel) == "" || evidence.AuthorAttempts <= 0 {
		return errors.New("generated evidence requires Rulebook provenance, source-author model, and at least one source-author attempt")
	}
	return nil
}

func validateFindings(findings []ReviewFinding, requireOne bool) error {
	if requireOne && len(findings) == 0 {
		return errors.New("review evidence requires a finding")
	}
	if len(findings) > maxReviewFindings {
		return fmt.Errorf("review evidence may contain at most %d findings", maxReviewFindings)
	}
	seen := make(map[FindingCategory]bool, len(findings))
	for index, finding := range findings {
		if !validFindingCategory(finding.Category) || strings.TrimSpace(finding.Summary) == "" || len(finding.Summary) > maxFindingSummaryLen {
			return fmt.Errorf("review evidence finding %d is invalid", index+1)
		}
		if seen[finding.Category] {
			return fmt.Errorf("review evidence repeats finding category %q", finding.Category)
		}
		seen[finding.Category] = true
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
		if strings.TrimSpace(request.AuthorModel) == "" {
			return Resolution{}, errors.New("test-writer model ID is required for generated oracle tasks")
		}
		if request.Reviewer == nil {
			return Resolution{}, errors.New("oracle reviewer is required for generated oracle tasks")
		}
		if strings.TrimSpace(request.ReviewerModel) == "" {
			return Resolution{}, errors.New("oracle reviewer model ID is required for generated oracle tasks")
		}

		// Caller-provided test source is never legal evidence for generated mode. The author prompt
		// has no route for it, and only an admitted response below may become the frozen bundle.
		task.TestCode = ""
		authorPrompt := prompt.TestPrompt(task.Spec, task.Signature) + "\n" + resolver.config.Rulebook.PromptText()
		evidence := Evidence{
			RulebookVersion: resolver.config.Rulebook.Version,
			RulebookDigest:  resolver.config.Rulebook.Digest(),
			AuthorModel:     request.AuthorModel,
			ReviewerModel:   request.ReviewerModel,
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
				return resolver.reviewAndFreeze(ctx, task, request, candidate, evidence, report)
			}
			lastOutput = admission.Output
		}

		return Resolution{}, &OracleFailureError{Attempts: evidence.AuthorAttempts, Output: lastOutput, Evidence: evidence}

	default:
		return Resolution{}, fmt.Errorf("unknown oracle mode %q", task.Oracle)
	}
}

func (resolver *DefaultResolver) reviewAndFreeze(ctx context.Context, task domain.Task, request Request, candidate string, evidence Evidence, report ProgressReporter) (Resolution, error) {
	callReview(report)
	rawReview, err := request.Reviewer.Complete(ctx, prompt.ReviewPrompt(task.Spec, task.Signature, candidate)+"\n"+resolver.config.Rulebook.PromptText())
	evidence.ReviewerAttempts = 1
	if err != nil {
		return Resolution{}, err
	}
	review, err := parseReview(rawReview)
	if err != nil {
		return Resolution{}, &OracleFailureError{Attempts: evidence.AuthorAttempts, Output: "oracle reviewer returned invalid review data: " + err.Error(), Evidence: evidence}
	}

	switch review.Verdict {
	case reviewAccept:
		evidence.ReviewVerdict = ReviewVerdictAccepted
		return sealGenerated(task, candidate, evidence)
	case reviewReject:
		evidence.ReviewVerdict = ReviewVerdictRejected
		evidence.ReviewFindings = append([]ReviewFinding(nil), review.Findings...)
		return Resolution{}, &OracleFailureError{Attempts: evidence.AuthorAttempts, Output: "oracle reviewer rejected the proposed source", Evidence: evidence}
	case reviewRevise:
		evidence.ReviewVerdict = ReviewVerdictRevised
		evidence.ReviewFindings = append([]ReviewFinding(nil), review.Findings...)
		callWriting(report)
		revisedRaw, err := request.Author.Complete(ctx, prompt.RevisionPrompt(task.Spec, task.Signature, candidate, reviewFindingsText(review.Findings))+"\n"+resolver.config.Rulebook.PromptText())
		evidence.AuthorAttempts++
		if err != nil {
			return Resolution{}, err
		}
		revised := prompt.ExtractGoCode(revisedRaw)
		callPreflighting(report)
		admission, err := resolver.config.Admitter.Admit(ctx, task, revised, resolver.config.PreflightTimeout)
		if err != nil {
			return Resolution{}, err
		}
		if !admission.Accepted {
			return Resolution{}, &OracleFailureError{Attempts: evidence.AuthorAttempts, Output: admission.Output, Evidence: evidence}
		}
		return sealGenerated(task, revised, evidence)
	default:
		return Resolution{}, errors.New("unreachable review verdict")
	}
}

func sealGenerated(task domain.Task, source string, evidence Evidence) (Resolution, error) {
	bundle, err := verification.GeneratedSource(task, source)
	if err != nil {
		return Resolution{}, err
	}
	resolution := Resolution{Bundle: bundle, Evidence: evidence.Clone()}
	if err := ValidateResolution(task, resolution); err != nil {
		return Resolution{}, err
	}
	return resolution, nil
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

func callReview(report ProgressReporter) {
	if report.ReviewingSource != nil {
		report.ReviewingSource()
	}
}
