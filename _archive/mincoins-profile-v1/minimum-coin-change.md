# Minimum Coin Change

## Go signature

    func MinCoins(amount int, denominations []int) ([]int, error)

## Specification

Implement `MinCoins`.

Return a selection of the fewest possible coins whose values add up to `amount` exactly. Each
element of the returned slice is one selected denomination. Return the selected coins in
non-increasing order; equal adjacent coins are allowed. A denomination is a coin type and may be
used any number of times.

`amount` must be non-negative. Every entry in `denominations` must be positive, and duplicate
denominations are invalid. For invalid input, return a nil slice and a non-nil error. Do not
panic.

When `amount` is zero, return a non-nil empty slice and a nil error. When the denominations are
valid but cannot make `amount` exactly, return a nil slice and a non-nil error.

There can be more than one way to use the minimum number of coins. To make the result
deterministic, compare each possible result after sorting its coins in non-increasing order. Return
the lexicographically largest result: at the first differing position, prefer the result with the
larger coin. For example, with denominations `[1, 2, 3, 4]` and amount `6`, return `[4, 2]`
rather than `[3, 3]`.

The input slice may be in any order and must not be modified. An empty denomination slice is
valid: it can satisfy only amount zero. Use only the supplied denominations; do not invent a
denomination.

For example, with denominations `[1, 3, 4]` and amount `6`, the result is `[3, 3]`, not
`[4, 1, 1]`: the function minimizes the number of coins, so a greedy choice is not always
correct.

The function must not print anything or depend on external state.

## Verification-profile regression fixture

The exact browser preset for this task selects the server-owned authored `mincoins/v1`
verification profile. It compiles one frozen bundle with fixed edge partitions plus hundreds of
seed-deterministic bounded cases. The bundle checks output validity mechanically and computes
bounded optimality/tie cases with a reviewed local reference instead of freezing model-calculated
output literals. It exists to exercise the generic platform and preserve the historical bad
answer-key regression; edited or free-form tasks do not inherit this profile automatically.
