// Package rulebook owns reviewed, versioned local oracle profiles.
package rulebook

import (
	"fmt"
	"strings"

	"codex-hackathon-july2026/internal/domain"
)

// EvidenceTierReferenceBacked identifies a profile whose bounded answer-key checks are computed
// by a reviewed local reference implementation rather than an LLM-calculated literal.
const EvidenceTierReferenceBacked = "reference-backed"

// Source is one rendered, frozen local oracle profile.
type Source struct {
	ID           domain.RulebookID
	EvidenceTier string
	TestCode     string
}

// Compile renders the reviewed profile selected by task. It accepts only explicit registered
// IDs; it never infers executable semantics from the task's prose or generated test source.
func Compile(task domain.Task) (Source, error) {
	if task.Oracle != domain.OracleRulebook {
		return Source{}, fmt.Errorf("rulebook compiler requires oracle mode %q, got %q", domain.OracleRulebook, task.Oracle)
	}

	switch task.Rulebook {
	case domain.RulebookMinCoinsV1:
		return compileMinCoinsV1(task)
	case "":
		return Source{}, fmt.Errorf("rulebook ID is required for oracle mode %q", domain.OracleRulebook)
	default:
		return Source{}, fmt.Errorf("unknown rulebook %q", task.Rulebook)
	}
}

const minCoinsSignature = "func MinCoins(amount int, denominations []int) ([]int, error)"

func compileMinCoinsV1(task domain.Task) (Source, error) {
	if strings.TrimSpace(task.Signature) != minCoinsSignature {
		return Source{}, fmt.Errorf("rulebook %q requires signature %q", domain.RulebookMinCoinsV1, minCoinsSignature)
	}

	return Source{
		ID:           domain.RulebookMinCoinsV1,
		EvidenceTier: EvidenceTierReferenceBacked,
		TestCode:     minCoinsTestSource,
	}, nil
}
