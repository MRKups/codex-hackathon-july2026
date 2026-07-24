package rulebook

// minCoinsTestSource intentionally contains inputs but no hand-calculated nontrivial output
// literals. It checks output validity directly and computes bounded optimality/tie expectations
// with the reviewed reference helper embedded in the frozen test source.
const minCoinsTestSource = `// Rulebook: mincoins/v1
// Evidence tier: reference-backed bounded cases.
package solution

import (
	"sort"
	"testing"
)

type minCoinsRulebookCase struct {
	name          string
	amount        int
	denominations []int
}

func TestMinCoinsRulebookRejectsInvalidInput(t *testing.T) {
	cases := []minCoinsRulebookCase{
		{name: "negative amount", amount: -1, denominations: []int{1, 2, 5}},
		{name: "zero denomination", amount: 5, denominations: []int{1, 0, 5}},
		{name: "negative denomination", amount: 5, denominations: []int{1, -2, 5}},
		{name: "duplicate denomination", amount: 5, denominations: []int{1, 2, 1}},
		{name: "invalid denomination with zero amount", amount: 0, denominations: []int{0}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			input := cloneMinCoinsInts(tt.denominations)
			before := cloneMinCoinsInts(input)
			got, err := MinCoins(tt.amount, input)
			if !sameMinCoinsInts(input, before) {
				t.Fatalf("MinCoins modified denominations: got %v, want %v", input, before)
			}
			if err == nil {
				t.Fatalf("MinCoins(%d, %v) returned nil error for invalid input", tt.amount, tt.denominations)
			}
			if got != nil {
				t.Fatalf("MinCoins(%d, %v) returned %v with error; want nil slice", tt.amount, tt.denominations, got)
			}
		})
	}
}

func TestMinCoinsRulebookHandlesValidZero(t *testing.T) {
	cases := []minCoinsRulebookCase{
		{name: "nil denominations", amount: 0, denominations: nil},
		{name: "empty denominations", amount: 0, denominations: []int{}},
		{name: "ordinary denominations", amount: 0, denominations: []int{7, 1, 3}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			input := cloneMinCoinsInts(tt.denominations)
			before := cloneMinCoinsInts(input)
			got, err := MinCoins(tt.amount, input)
			if !sameMinCoinsInts(input, before) {
				t.Fatalf("MinCoins modified denominations: got %v, want %v", input, before)
			}
			if err != nil {
				t.Fatalf("MinCoins(0, %v) returned unexpected error: %v", tt.denominations, err)
			}
			if got == nil || len(got) != 0 {
				t.Fatalf("MinCoins(0, %v) = %v; want non-nil empty slice", tt.denominations, got)
			}
		})
	}
}

func TestMinCoinsRulebookChecksOutputs(t *testing.T) {
	cases := []minCoinsRulebookCase{
		{name: "empty cannot make positive amount", amount: 1, denominations: nil},
		{name: "unreachable amount", amount: 7, denominations: []int{4, 6}},
		{name: "uses only supplied denomination", amount: 4, denominations: []int{2, 5}},
		{name: "greedy choice is not minimum", amount: 6, denominations: []int{1, 3, 4}},
		{name: "lexicographic tie", amount: 6, denominations: []int{1, 2, 3, 4}},
		{name: "unsorted denominations", amount: 12, denominations: []int{4, 7, 10}},
		{name: "larger first coin wins tie", amount: 12, denominations: []int{1, 5, 6, 7}},
		{name: "two large coins", amount: 18, denominations: []int{1, 3, 8, 9}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			input := cloneMinCoinsInts(tt.denominations)
			before := cloneMinCoinsInts(input)
			got, err := MinCoins(tt.amount, input)
			if !sameMinCoinsInts(input, before) {
				t.Fatalf("MinCoins modified denominations: got %v, want %v", input, before)
			}

			want, reachable := minCoinsRulebookReference(tt.amount, before)
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
			checkMinCoinsRulebookValidity(t, tt.amount, before, got)
			if !sameMinCoinsInts(got, want) {
				t.Fatalf("MinCoins(%d, %v) = %v; want reference-backed minimum result %v", tt.amount, tt.denominations, got, want)
			}
		})
	}
}

func checkMinCoinsRulebookValidity(t *testing.T, amount int, denominations, got []int) {
	t.Helper()
	for _, coin := range got {
		if !containsMinCoin(denominations, coin) {
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

func minCoinsRulebookReference(amount int, denominations []int) ([]int, bool) {
	if amount == 0 {
		return []int{}, true
	}
	if len(denominations) == 0 {
		return nil, false
	}

	coins := cloneMinCoinsInts(denominations)
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

func containsMinCoin(denominations []int, want int) bool {
	for _, denomination := range denominations {
		if denomination == want {
			return true
		}
	}
	return false
}

func cloneMinCoinsInts(in []int) []int {
	if in == nil {
		return nil
	}
	out := make([]int, len(in))
	copy(out, in)
	return out
}

func sameMinCoinsInts(left, right []int) bool {
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
