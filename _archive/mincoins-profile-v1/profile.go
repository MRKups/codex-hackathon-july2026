// Package mincoins supplies the first reviewed verification profile. It is intentionally a
// profile implementation, not a branch in the verification platform or repair loop.
package mincoins

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/verification"
)

const (
	// ID is server-owned profile metadata. Browser callers cannot select it directly.
	ID domain.VerificationProfileID = "mincoins/v1"

	signature      = "func MinCoins(amount int, denominations []int) ([]int, error)"
	minimumCases   = 200
	maximumCases   = 500
	fixedCaseCount = 15
	referenceName  = "mincoins/dp-v1"
)

// Specification is the reviewed MinCoins contract this profile is allowed to verify. Keeping it
// beside the compiler prevents a server-side caller from attaching mincoins/v1 to unrelated prose
// that happens to reuse the same Go signature.
const Specification = `Implement MinCoins.

Return a selection of the fewest possible coins whose values add up to amount exactly. Each
element of the returned slice is one selected denomination. Return the selected coins in
non-increasing order; equal adjacent coins are allowed. A denomination is a coin type and may be
used any number of times.

amount must be non-negative. Every entry in denominations must be positive, and duplicate
denominations are invalid. For invalid input, return a nil slice and a non-nil error. Do not
panic.

When amount is zero, return a non-nil empty slice and a nil error. When the denominations are
valid but cannot make amount exactly, return a nil slice and a non-nil error.

There can be more than one way to use the minimum number of coins. To make the result
deterministic, compare each possible result after sorting its coins in non-increasing order. Return
the lexicographically largest result: at the first differing position, prefer the result with the
larger coin. For example, with denominations [1, 2, 3, 4] and amount 6, return [4, 2] rather than
[3, 3].

The input slice may be in any order and must not be modified. An empty denomination slice is
valid: it can satisfy only amount zero. Use only the supplied denominations; do not invent a
denomination.

For example, with denominations [1, 3, 4] and amount 6, the result is [3, 3], not [4, 1, 1]: the
function minimizes the number of coins, so a greedy choice is not always correct.

The function must not print anything or depend on external state.`

var planFamilies = []string{
	"invalid-input",
	"zero",
	"unreachable",
	"reachable",
	"tie-breaking",
}

// Profile is a stateless reviewed compiler for the MinCoins contract.
type Profile struct{}

func (Profile) ID() domain.VerificationProfileID { return ID }

// ValidateTask binds this profile to its reviewed prose and pinned signature, not merely a
// function-shaped coincidence. Browser exact-preset matching is one guard; this is the lower
// layer guard for terminal and direct callers too.
func (Profile) ValidateTask(task domain.Task) error {
	if strings.TrimSpace(task.Signature) != signature {
		return fmt.Errorf("requires signature %q", signature)
	}
	if strings.TrimSpace(task.Spec) != strings.TrimSpace(Specification) {
		return errors.New("requires the reviewed MinCoins specification")
	}
	return nil
}

// DefaultPlan keeps the server-owned seed and budget intact. The fixed corpus plus this bounded
// generated corpus reaches hundreds of reproducible cases without accepting an LLM's answer key.
func (Profile) DefaultPlan(config verification.FreezeConfig) (domain.VerificationPlan, error) {
	if config.CaseCount < minimumCases || config.CaseCount > maximumCases {
		return domain.VerificationPlan{}, fmt.Errorf("case count must be between %d and %d", minimumCases, maximumCases)
	}
	return domain.VerificationPlan{
		Seed:      config.Seed,
		CaseCount: config.CaseCount,
		Families:  append([]string(nil), planFamilies...),
	}, nil
}

// Compile validates the pinned contract, renders the deterministic Go suite, and declares the
// check/evidence families that become part of the sealed bundle manifest.
func (Profile) Compile(task domain.Task, plan domain.VerificationPlan) (verification.Compiled, error) {
	if err := (Profile{}).ValidateTask(task); err != nil {
		return verification.Compiled{}, err
	}
	if plan.CaseCount < minimumCases || plan.CaseCount > maximumCases {
		return verification.Compiled{}, fmt.Errorf("case count must be between %d and %d", minimumCases, maximumCases)
	}
	if !sameFamilies(plan.Families, planFamilies) {
		return verification.Compiled{}, errors.New("plan must declare the required input families")
	}

	totalCases := fixedCaseCount + plan.CaseCount
	return verification.Compiled{
		Plan: plan,
		Checks: []domain.VerificationCheck{
			{
				ID:          "input-contract",
				Kind:        domain.VerificationCheckInvariant,
				Evidence:    domain.VerificationEvidenceMechanical,
				Cases:       totalCases,
				Description: "Across the declared corpus: invalid-input, nil/error, and input-immutability requirements.",
			},
			{
				ID:          "output-invariants",
				Kind:        domain.VerificationCheckInvariant,
				Evidence:    domain.VerificationEvidenceMechanical,
				Cases:       totalCases,
				Description: "Across applicable successful cases in the declared corpus: supplied denominations, non-increasing order, and exact sum.",
			},
			{
				ID:          "minimum-and-tie-reference",
				Kind:        domain.VerificationCheckReference,
				Evidence:    domain.VerificationEvidenceReferenceBacked,
				Cases:       totalCases,
				Description: "Across applicable valid cases in the declared corpus: bounded dynamic-programming reachability, minimum count, and tie-breaking.",
				Reference:   referenceName,
			},
		},
		TestCode: renderTestSource(plan),
	}, nil
}

func sameFamilies(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func renderTestSource(plan domain.VerificationPlan) string {
	return strings.NewReplacer(
		"{{SEED}}", strconv.FormatUint(plan.Seed, 10),
		"{{CASE_COUNT}}", strconv.Itoa(plan.CaseCount),
	).Replace(testSourceTemplate)
}

// testSourceTemplate contains only fixed inputs and a deterministic generator. In particular,
// it contains no model-calculated nontrivial expected coin selections.
const testSourceTemplate = `// Verification profile: mincoins/v1
// This source is deterministic. The manifest records its frozen seed and case budget.
package solution

import (
	"fmt"
	"sort"
	"testing"
)

const minCoinsProfileSeed uint64 = {{SEED}}
const minCoinsProfileGeneratedCases = {{CASE_COUNT}}

type minCoinsProfileCase struct {
	name          string
	amount        int
	denominations []int
}

func TestMinCoinsVerificationProfile(t *testing.T) {
	for _, tt := range minCoinsProfileFixedCases() {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			checkMinCoinsProfileCase(t, tt)
		})
	}

	for index := 0; index < minCoinsProfileGeneratedCases; index++ {
		tt := minCoinsProfileGeneratedCase(minCoinsProfileSeed, index)
		t.Run(fmt.Sprintf("generated/%03d/%s", index, tt.name), func(t *testing.T) {
			checkMinCoinsProfileCase(t, tt)
		})
	}
}

func minCoinsProfileFixedCases() []minCoinsProfileCase {
	return []minCoinsProfileCase{
		{name: "negative amount", amount: -1, denominations: []int{1, 2, 5}},
		{name: "zero denomination", amount: 5, denominations: []int{1, 0, 5}},
		{name: "negative denomination", amount: 5, denominations: []int{1, -2, 5}},
		{name: "duplicate denomination", amount: 5, denominations: []int{1, 2, 1}},
		{name: "invalid denomination with zero amount", amount: 0, denominations: []int{0}},
		{name: "zero with nil denominations", amount: 0, denominations: nil},
		{name: "zero with ordinary denominations", amount: 0, denominations: []int{7, 1, 3}},
		{name: "empty cannot make positive amount", amount: 1, denominations: nil},
		{name: "unreachable amount", amount: 7, denominations: []int{4, 6}},
		{name: "uses only supplied denomination", amount: 4, denominations: []int{2, 5}},
		{name: "greedy choice is not minimum", amount: 6, denominations: []int{1, 3, 4}},
		{name: "lexicographic tie", amount: 6, denominations: []int{1, 2, 3, 4}},
		{name: "historical impossible answer key regression", amount: 12, denominations: []int{4, 7, 10}},
		{name: "larger first coin wins tie", amount: 12, denominations: []int{1, 5, 6, 7}},
		{name: "two large coins", amount: 18, denominations: []int{1, 3, 8, 9}},
	}
}

func checkMinCoinsProfileCase(t *testing.T, tt minCoinsProfileCase) {
	t.Helper()
	input := cloneMinCoinsProfileInts(tt.denominations)
	before := cloneMinCoinsProfileInts(input)
	got, err := MinCoins(tt.amount, input)
	if !sameMinCoinsProfileInts(input, before) {
		t.Fatalf("MinCoins modified denominations for %s: got %v, want %v", tt.name, input, before)
	}

	if !validMinCoinsProfileInput(tt.amount, before) {
		if err == nil {
			t.Fatalf("MinCoins(%d, %v) returned nil error for invalid input", tt.amount, tt.denominations)
		}
		if got != nil {
			t.Fatalf("MinCoins(%d, %v) returned %v with error; want nil slice", tt.amount, tt.denominations, got)
		}
		return
	}

	want, reachable := minCoinsProfileReference(tt.amount, before)
	if !reachable {
		if err == nil {
			t.Fatalf("MinCoins(%d, %v) returned nil error for unreachable amount", tt.amount, tt.denominations)
		}
		if got != nil {
			t.Fatalf("MinCoins(%d, %v) returned %v with error; want nil slice", tt.amount, tt.denominations, got)
		}
		return
	}
	if err != nil {
		t.Fatalf("MinCoins(%d, %v) returned unexpected error: %v", tt.amount, tt.denominations, err)
	}
	if got == nil {
		t.Fatalf("MinCoins(%d, %v) returned nil result for reachable amount", tt.amount, tt.denominations)
	}
	checkMinCoinsProfileValidity(t, tt.amount, before, got)
	if !sameMinCoinsProfileInts(got, want) {
		t.Fatalf("MinCoins(%d, %v) = %v; want reference-backed minimum result %v", tt.amount, tt.denominations, got, want)
	}
}

func validMinCoinsProfileInput(amount int, denominations []int) bool {
	if amount < 0 {
		return false
	}
	seen := make(map[int]struct{}, len(denominations))
	for _, denomination := range denominations {
		if denomination <= 0 {
			return false
		}
		if _, exists := seen[denomination]; exists {
			return false
		}
		seen[denomination] = struct{}{}
	}
	return true
}

func checkMinCoinsProfileValidity(t *testing.T, amount int, denominations, got []int) {
	t.Helper()
	for _, coin := range got {
		if !containsMinCoinsProfile(denominations, coin) {
			t.Fatalf("MinCoins returned coin %d that is not in supplied denominations %v", coin, denominations)
		}
	}
	for index := 1; index < len(got); index++ {
		if got[index-1] < got[index] {
			t.Fatalf("MinCoins returned coins in increasing order: %v", got)
		}
	}
	sum := 0
	for _, coin := range got {
		sum += coin
	}
	if sum != amount {
		t.Fatalf("MinCoins returned values sum to %d, want amount %d: %v", sum, amount, got)
	}
}

func minCoinsProfileReference(amount int, denominations []int) ([]int, bool) {
	if amount == 0 {
		return []int{}, true
	}
	if len(denominations) == 0 {
		return nil, false
	}

	coins := cloneMinCoinsProfileInts(denominations)
	sort.Sort(sort.Reverse(sort.IntSlice(coins)))
	maxCount := int(^uint(0) >> 1)
	counts := make([]int, amount+1)
	for value := 1; value <= amount; value++ {
		counts[value] = maxCount
	}
	for value := 1; value <= amount; value++ {
		for _, coin := range coins {
			if coin > value || counts[value-coin] == maxCount {
				continue
			}
			candidate := counts[value-coin] + 1
			if candidate < counts[value] {
				counts[value] = candidate
			}
		}
	}
	if counts[amount] == maxCount {
		return nil, false
	}

	result := make([]int, 0, counts[amount])
	for remaining := amount; remaining > 0; {
		chosen := 0
		for _, coin := range coins {
			if coin <= remaining && counts[remaining-coin] != maxCount && counts[remaining-coin]+1 == counts[remaining] {
				chosen = coin
				break
			}
		}
		if chosen == 0 {
			return nil, false
		}
		result = append(result, chosen)
		remaining -= chosen
	}
	return result, true
}

func minCoinsProfileGeneratedCase(seed uint64, index int) minCoinsProfileCase {
	random := newMinCoinsProfilePRNG(seed + uint64(index+1)*0x9e3779b97f4a7c15)
	switch index % 6 {
	case 0:
		return minCoinsProfileCase{name: "negative-amount", amount: -1 - random.next(9), denominations: minCoinsProfileValidDenominations(&random, true)}
	case 1:
		denominations := minCoinsProfileValidDenominations(&random, true)
		return minCoinsProfileCase{name: "duplicate-denomination", amount: 1 + random.next(80), denominations: append(denominations, denominations[0])}
	case 2:
		denominations := minCoinsProfileValidDenominations(&random, true)
		return minCoinsProfileCase{name: "zero-denomination", amount: 1 + random.next(80), denominations: append([]int{0}, denominations...)}
	case 3:
		return minCoinsProfileCase{name: "zero-amount", amount: 0, denominations: minCoinsProfileValidDenominations(&random, random.next(2) == 0)}
	case 4:
		return minCoinsProfileCase{name: "unreachable-odd-with-even-coins", amount: 1 + 2*random.next(40), denominations: minCoinsProfileEvenDenominations(&random)}
	default:
		return minCoinsProfileCase{name: "reachable", amount: 1 + random.next(90), denominations: minCoinsProfileValidDenominations(&random, true)}
	}
}

type minCoinsProfilePRNG struct {
	state uint64
}

func newMinCoinsProfilePRNG(seed uint64) minCoinsProfilePRNG {
	if seed == 0 {
		seed = 1
	}
	return minCoinsProfilePRNG{state: seed}
}

func (random *minCoinsProfilePRNG) next(limit int) int {
	random.state = random.state*6364136223846793005 + 1442695040888963407
	return int((random.state >> 32) % uint64(limit))
}

func minCoinsProfileValidDenominations(random *minCoinsProfilePRNG, includeOne bool) []int {
	count := 1 + random.next(5)
	denominations := make([]int, 0, count)
	if includeOne {
		denominations = append(denominations, 1)
	}
	for len(denominations) < count {
		candidate := 2 + random.next(19)
		if !containsMinCoinsProfile(denominations, candidate) {
			denominations = append(denominations, candidate)
		}
	}
	return denominations
}

func minCoinsProfileEvenDenominations(random *minCoinsProfilePRNG) []int {
	count := 1 + random.next(4)
	denominations := make([]int, 0, count)
	for len(denominations) < count {
		candidate := 2 * (1 + random.next(10))
		if !containsMinCoinsProfile(denominations, candidate) {
			denominations = append(denominations, candidate)
		}
	}
	return denominations
}

func containsMinCoinsProfile(denominations []int, want int) bool {
	for _, denomination := range denominations {
		if denomination == want {
			return true
		}
	}
	return false
}

func cloneMinCoinsProfileInts(in []int) []int {
	if in == nil {
		return nil
	}
	out := make([]int, len(in))
	copy(out, in)
	return out
}

func sameMinCoinsProfileInts(left, right []int) bool {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
`
