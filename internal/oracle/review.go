package oracle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	maxReviewBytes       = 16 << 10
	maxReviewFindings    = 6
	maxFindingSummaryLen = 600
)

type reviewDecision string

const (
	reviewAccept reviewDecision = "accept"
	reviewRevise reviewDecision = "revise"
	reviewReject reviewDecision = "reject"
)

// ReviewVerdict describes the completed accepted resolution path. A rejection is represented by
// OracleFailureError because no verification bundle was frozen.
type ReviewVerdict string

const (
	ReviewVerdictAccepted ReviewVerdict = "accepted"
	ReviewVerdictRevised  ReviewVerdict = "revised"
	ReviewVerdictRejected ReviewVerdict = "rejected"
)

// FindingCategory is a small, universal set rather than task-specific review policy.
type FindingCategory string

const (
	FindingAnswerKeyProvenance       FindingCategory = "answer_key_provenance"
	FindingBoundaryErrorCoverage     FindingCategory = "boundary_error_coverage"
	FindingValidityInvariantCoverage FindingCategory = "validity_invariant_coverage"
	FindingUnsupportedSemanticClaim  FindingCategory = "unsupported_semantic_claim"
)

// ReviewFinding is bounded reviewer evidence. Summary is untrusted text and must always be
// rendered as text by callers.
type ReviewFinding struct {
	Category FindingCategory `json:"category"`
	Summary  string          `json:"summary"`
}

type reviewResponse struct {
	Verdict  reviewDecision  `json:"verdict"`
	Findings []ReviewFinding `json:"findings"`
}

func parseReview(raw string) (reviewResponse, error) {
	if len(raw) > maxReviewBytes {
		return reviewResponse{}, fmt.Errorf("review exceeds %d bytes", maxReviewBytes)
	}

	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var review reviewResponse
	if err := decoder.Decode(&review); err != nil {
		return reviewResponse{}, fmt.Errorf("review must be one JSON object: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return reviewResponse{}, errors.New("review must contain one JSON value")
		}
		return reviewResponse{}, fmt.Errorf("review must be one JSON object: %w", err)
	}

	review.Verdict = reviewDecision(strings.TrimSpace(string(review.Verdict)))
	if review.Verdict != reviewAccept && review.Verdict != reviewRevise && review.Verdict != reviewReject {
		return reviewResponse{}, errors.New("review verdict must be accept, revise, or reject")
	}
	if len(review.Findings) > maxReviewFindings {
		return reviewResponse{}, fmt.Errorf("review may contain at most %d findings", maxReviewFindings)
	}
	if review.Verdict == reviewAccept && len(review.Findings) != 0 {
		return reviewResponse{}, errors.New("accepted review must not contain findings")
	}
	if review.Verdict != reviewAccept && len(review.Findings) == 0 {
		return reviewResponse{}, errors.New("revised or rejected review requires a finding")
	}

	seen := make(map[FindingCategory]bool, len(review.Findings))
	for index := range review.Findings {
		finding := &review.Findings[index]
		finding.Category = FindingCategory(strings.TrimSpace(string(finding.Category)))
		finding.Summary = strings.TrimSpace(finding.Summary)
		if !validFindingCategory(finding.Category) {
			return reviewResponse{}, fmt.Errorf("review finding %d has an unknown category", index+1)
		}
		if finding.Summary == "" || len(finding.Summary) > maxFindingSummaryLen {
			return reviewResponse{}, fmt.Errorf("review finding %d summary must contain 1 to %d bytes", index+1, maxFindingSummaryLen)
		}
		if seen[finding.Category] {
			return reviewResponse{}, fmt.Errorf("review repeats finding category %q", finding.Category)
		}
		seen[finding.Category] = true
	}
	return review, nil
}

func validFindingCategory(category FindingCategory) bool {
	switch category {
	case FindingAnswerKeyProvenance, FindingBoundaryErrorCoverage, FindingValidityInvariantCoverage, FindingUnsupportedSemanticClaim:
		return true
	default:
		return false
	}
}

func reviewFindingsText(findings []ReviewFinding) string {
	lines := make([]string, len(findings))
	for index, finding := range findings {
		lines[index] = string(finding.Category) + ": " + finding.Summary
	}
	return strings.Join(lines, "\n")
}
