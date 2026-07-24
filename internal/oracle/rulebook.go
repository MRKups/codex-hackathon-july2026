// Package oracle resolves blind verification source before candidate generation.
package oracle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// RulebookVersionV1 identifies the first checked-in, non-executable oracle policy.
const RulebookVersionV1 = "oracle-rulebook/v1"

// Rulebook is human-owned prompt and review guidance. It is deliberately data, not executable
// verifier logic: it cannot select a task, supply an answer key, or change bundle semantics.
type Rulebook struct {
	Version string
	Text    string
}

const defaultRulebookText = `1. Derive every check only from the submitted specification and pinned signature. Do not invent unstated behavior.
2. Prefer validity, invalid-input/error, boundary, mutation, determinism, round-trip, and metamorphic checks when the contract supports them.
3. Use an exact expected value only when it is trivial or comes from a trustworthy source stated in the task.
4. Do not hand-calculate a non-trivial answer, embed a generated reference solver, or disguise one as test support.
5. If exactness, optimality, or tie-breaking cannot be checked without independently solving the task, check only mechanically supported consequences and do not assert a guessed answer.
6. Keep tests deterministic, standard-library only, and focused on the pinned function.`

// DefaultRulebook returns a fresh copy of the checked-in generic policy. It has no task-specific
// terms, matchers, expected values, or executable clauses.
func DefaultRulebook() Rulebook {
	return Rulebook{Version: RulebookVersionV1, Text: defaultRulebookText}
}

// Validate checks that a Rulebook is usable as immutable oracle guidance.
func (rulebook Rulebook) Validate() error {
	if strings.TrimSpace(rulebook.Version) == "" {
		return errors.New("oracle Rulebook version is required")
	}
	if strings.TrimSpace(rulebook.Text) == "" {
		return errors.New("oracle Rulebook text is required")
	}
	return nil
}

// Digest identifies the exact version/text payload supplied to an oracle author or reviewer.
// It intentionally has no connection to VerificationBundle's executable-source digest.
func (rulebook Rulebook) Digest() string {
	sum := sha256.Sum256([]byte(rulebook.Version + "\n" + rulebook.Text))
	return hex.EncodeToString(sum[:])
}

// PromptText renders the policy explicitly for a blind oracle-author prompt. Prompt construction
// remains pure because this method only turns immutable values into text.
func (rulebook Rulebook) PromptText() string {
	return "Oracle Rulebook (" + rulebook.Version + "):\n" + rulebook.Text + "\n"
}
