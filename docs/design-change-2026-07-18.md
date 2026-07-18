# Design Change — Blind Oracle Modes (2026-07-18)

**Status:** adopted. The authored-oracle path F1–F4 and browser path F7–F9 are now implemented;
F15–F18 remain future work.
**Landed after:** F1, C3. **Landed before:** F2.
**Scope:** build the hackathon flow first; the research-inspired diagnostics come after it works.
**Files changed:** `AGENTS.md`, `README.md`, `docs/go-repair-loop.md`,
`docs/app-architecture.md`, `docs/file-structure.md`, `docs/tracker.md`.
**New tracker items:** F15, F16, F17, F18, C4, C5. **Amended:** F3, F4, F6, F7, F9, F12, F13, C1, C3.

This document explains *why* the project's central rule changed and the research-inspired
question the completed flow can later help explore. It does not turn the hackathon into a
separate research platform. Read it before picking up F15 or later generated-mode work. If this document
and an older comment in the code disagree, this document wins.

---

## 1. What the rule used to be

> **The code-writer never writes the tests.** Tests are human-authored and fixed for a given
> task, fed to the loop as a given.

The reasoning was sound: if one model context writes both the solution and the tests that
judge it, a misunderstanding of the task corrupts both, and the tests rubber-stamp the bug. A
green run would mean nothing.

## 2. Why that rule was too narrow

The rule conflated two different things:

1. **Who authored the oracle** (a human).
2. **What the oracle had seen** (not the code).

Only the second one is load-bearing. An oracle is trustworthy because it was written without
sight of the solution it judges — not because a human's hand was on the keyboard. Requiring a
human author bought no extra integrity and cost three things:

- **It didn't scale.** Every task needed a hand-written test file. In practice that caps the
  project at a handful of tasks, which is too few to measure anything.
- **It discarded the most interesting signal.** With a fixed human oracle, a failure has
  exactly one meaning: the code is wrong. That is a fact about the model. It tells you nothing
  about the artifact the project actually cares about — the spec.
- **It made the spec invisible.** The spec was just prompt material on the way to code. Nothing
  in the system ever tested whether the spec was any good.

## 3. What the rule is now

> **The oracle is written blind to the code.** Whatever produces the tests must never have seen
> the solution they judge.

Two legal modes, both frozen before attempt 1:

| Mode | Oracle author | Sees | A green run means |
|---|---|---|---|
| `authored` | a human, ahead of the run | the task | the code satisfies an independent oracle — evidence of correctness |
| `generated` | a second LLM context | spec + signature **only** | two independent readings of the spec agreed — evidence of *spec convergence* |

`generated` is the later showcase and research-inspired variation. `authored` is retained as
the **control condition** and the first mode to build: the same harness with a fixed external
oracle. Keeping both makes comparison possible, so `authored` mode must not be deleted or left
to rot.

## 4. Why this is the interesting change

Under the old rule, "tests failed" had one meaning. Under `generated` mode it has two:

1. The coder got it wrong, **or**
2. **The spec was ambiguous**, and the coder and the test-writer resolved that ambiguity
   differently.

Case 2 is why generated mode is interesting. Because both artifacts derive from the same spec
without seeing each other, a *persistent* disagreement can surface an interpretation mismatch
worth inspecting. It does not automatically prove that the spec is underspecified: coder ability,
oracle quality, and repair feedback are confounders too. The harness remains an end-to-end demo
first, with a useful diagnostic lens once both modes work.

One later question the completed flow can help explore is:

> **How precise does a specification have to be before two independent readers agree on what it
> means?**

That reframes several things that were previously cosmetic:

- **Attempts-to-convergence can become a diagnostic**, alongside model ability, repair quality,
  and the attempt cap. Later comparisons should vary one factor at a time.
- **Giving up can be informative.** A run that exhausts its attempts with the *same* assertion
  failing every time, while the coder visibly changes approach, merits inspection. F18 records
  this as `FailureMode: "persistent"`; it is a signal, not an automatic conclusion.
- **Property-based oracles (F13) remain a later strengthening option**, not a prerequisite for
  the hackathon flow.

## 5. The three invariants (non-negotiable)

These are structural, enforced by function signatures and call ordering rather than by
discipline or by a comment asking nicely:

1. **No coder prompt builder may accept test source.** `FirstPrompt(spec, signature)` and
   `RepairPrompt(spec, signature, previousCode, verifierOutput)` have no parameter through
   which the oracle could arrive. The coder sees *verifier output* from running the tests —
   never the test file itself.
2. **`TestPrompt(spec, signature)` may not accept candidate code**, and `resolveOracle` runs
   *before the first coder call exists to produce any*. The ordering is the guarantee: at the
   moment the oracle is written, there is no solution in the program to leak.
3. **The oracle is frozen once per run.** Nothing after `resolveOracle` may regenerate it. A
   moving oracle cannot converge, and "make the failing test pass by rewriting the test" is the
   precise failure this project exists to rule out. Generated oracles are never written back
   into `tasks/`.

**Widening one of those parameter lists voids the project's premise.** If a future change seems
to need it, the design is wrong — stop and flag it rather than changing the signature.

## 6. Known weakness: correlated misreading (C4)

The honest caveat, which must stay in the docs and out of any claim made about results:

In `generated` mode the coder and the test-writer read the **same ambiguous prose**. The same
model family can misread it the same way twice — producing code and tests that agree on the
wrong behaviour, and a green run for the wrong reason.

Mitigations, in order of strength:

- **Different models per role** (F17): `LLM_MODEL_CODER` and `LLM_MODEL_TESTER`, each falling
  back to `LLM_MODEL`. Decorrelates the reading.
- **Stronger oracles** (F13): properties are harder to satisfy by accident than examples.
- **`authored` mode as control**: run the same task both ways and compare.

Language discipline: **never describe a green `generated` run as "verified."** It is evidence
that two readings agreed. A green `authored` run is the stronger claim. This distinction is not
pedantry — it is the difference between a result and a demo.

## 7. Second known weakness: tuning away the signal (C5)

A repair prompt tuned hard enough to rescue any spec destroys the measurement, because
ambiguity stops showing up as attempts-to-convergence. The harness must stay **transparent**,
not maximally clever.

For later comparison runs, keep prompts plain and fixed. Treat prompt-quality work and
spec-quality work as **separate experiments**, and never vary both at once. This does not block
ordinary hackathon prompt work: F5's immediate target is a clear, honest, working loop.

## 8. What this changes in the build

### Phase 0 (F2, F3, F4) — `authored` mode only. Deliberately.

Do **not** implement the test-writer as part of Phase 0. Bringing up the coder loop and the
oracle generator simultaneously means two unproven halves and no way to tell which one is
broken. The authored oracle is a known-good fixture; earn the loop against it first.

The one forward-looking requirement: `Repair` should already take its coder as an explicit
parameter, so F15 can deliberately extend the API with a test-writer after its contracts exist.

### Phase 1 — the test-writer arrives

- **F15 — Generated oracle.** Add `domain.OracleMode` + `Task.Oracle`; add pure
  `prompt.TestPrompt(spec, signature)`; add `repair.resolveOracle` to obtain and freeze one
  accepted oracle before attempt 1; extend `Repair` to take coder + tester `llm.LLM` values.
  F16 may retry rejected oracle candidates only before any coder call exists. Add a unit test
  asserting the coder prompt builders never receive `TestCode`.
- **F16 — Oracle preflight + fault attribution.** A non-compiling generated oracle must never be
  charged to the coder. `go build` ignores `*_test.go`, so compile the generated test with
  `go test -c` against a signature-derived stub before spending a coder generation. A preflight
  failure is an oracle fault; regenerate up to an injected cap; exhausting it ends the run
  `oraclefailed`, which is neither a pass nor a coder failure. Use `go/parser` if needed to
  derive the stub safely from the pinned signature; the stub is built, never run.
- **F17 — Split role models.** Two `llm.LLM` values at the composition root.
  `internal/llm` stays role-agnostic — role separation is the caller's job.
- **F18 — Failure mode signal.** On `gaveup`, intersect failing test names across attempts;
  record `persistent` or `varied`. This is an optional diagnostic after the dual-mode flow
  works, not a dependency for the hackathon demo.

### Data shape changes

```go
// internal/domain
type OracleMode string
const (
    OracleAuthored  OracleMode = "authored"
    OracleGenerated OracleMode = "generated"
)

type Task struct {
    Name      string
    Spec      string
    Signature string
    Oracle    OracleMode // NEW
    TestCode  string     // required when OracleAuthored; empty when OracleGenerated
                         // until resolveOracle fills it once, before attempt 1
}
```

```go
// internal/run — frozen at F7, even though F15/F18 land later.
// Adding fields after the freeze breaks the browser contract.
type Run struct {
    ID          string           `json:"id"`
    Task        string           `json:"task"`
    Spec        string           `json:"spec"`
    Signature   string           `json:"signature"`
    Oracle      string           `json:"oracle"`      // NEW: "authored" | "generated"
    TestCode    string           `json:"testCode"`    // NEW: the frozen oracle, for display
    MaxAttempts int              `json:"maxAttempts"`
    Status      Status           `json:"status"`      // running | passed | gaveup | infrastructurefailed | oraclefailed
    FailureMode string           `json:"failureMode"` // NEW: "" | "varied" | "persistent"
    Error       string           `json:"error"`
    Attempts    []domain.Attempt `json:"attempts"`
}
```

`TestCode` sits on the `Run` because in `generated` mode the oracle is a **result** of the run,
not an input to it.

### Task directory layout (F6)

Each subdirectory of `tasks/` is one task. `spec.md` is required and pins the signature.
`solution_test.go` is **optional**, and its presence selects the mode:

- `solution_test.go` present → `OracleAuthored`
- `solution_test.go` absent → `OracleGenerated`

Never write a generated oracle back into `tasks/`. Promoting a generated oracle to an authored
one is a deliberate human act — read it, judge it correct, commit it — never something the loop
does on its own.

### Configuration

| Variable | Role |
|---|---|
| `LLM_MODEL` | default for any role without an override |
| `LLM_MODEL_CODER` | writes candidate solutions |
| `LLM_MODEL_TESTER` | writes oracles in `generated` mode |

`.env.example` has not yet been updated with the two role variables. It should be, alongside F17.

### UI (F9)

The implemented page shows the spec and frozen **authored** oracle side by side before a provider
call, then compares real candidate source and verifier feedback. Final state is green on
`passed`, red on `gaveup`, and neutral on `infrastructurefailed`; `oraclefailed` and
`failureMode` are reserved for F15/F16 and F18. In generated mode, the same placement will let a
viewer inspect the frozen independently derived oracle without claiming it is verification.

## 9. What "done" looks like now

Phase 0's milestone is unchanged and still the thing to protect: `attempt 1 fail → … →
attempt N pass` in the terminal, in `authored` mode.

Phase 1's optional extended milestone is: **the same task runs in both oracle modes, and the
comparison surfaces a concrete interpretation mismatch worth discussing.**

An optional later experiment, once the harness is solid: take one task, write three specs for it
at increasing precision — vague, moderate, airtight — and run each several times, varying only
the spec. Compare attempts-to-convergence and failure modes as observations, not guaranteed
outcomes. That can produce a useful plot, but it is not a prerequisite for the demo.

Task material should have *natural* ambiguity — cases where a reasonable person could go either
way and most specs never say which: date arithmetic (what is January 31 plus one month?),
remainder allocation (split $100 three ways — who gets the extra cent?), semver prerelease
ordering, word-wrap when a single word exceeds the line. Pure functions, stdlib only, fast,
deterministic.

## 10. Summary for the impatient

- The rule changed from *"a human writes the tests"* to *"whatever writes the tests never sees
  the code."*
- The oracle can now be generated by a second, independent LLM context that reads only the spec
  and the signature.
- That makes a new diagnostic available: persistent code/oracle disagreement can expose an
  interpretation mismatch worth inspecting.
- `authored` mode stays as the control condition. It is not legacy.
- Phase 0 remains `authored`-only on purpose. The test-writer is F15, in Phase 1, after the
  end-to-end terminal flow is working.
- A green `generated` run means *two readings agreed*, not *verified*. Keep saying it that way.
