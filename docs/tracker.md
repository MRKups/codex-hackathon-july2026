# Test Verifier — Implementation Tracker

*Identifier counter: the next new items are **F32, C7, B4**.
Branch strategy: `main` (active development and production during the hackathon).*

Status markers: `[ ]` not started · `[~]` in progress.

This file lists open work only. Completed work is not recorded here — read Git history for what
changed and `docs/go-repair-loop.md` / `docs/app-architecture.md` for the contracts that resulted.
Decisions are made here first, then implemented; when a decision becomes a standing rule, move it
into the design docs and drop it from this file.

---

## Features (F)

- [ ] **F5 — Tune repair prompts on real tasks.** Feed previous code + verifier output cleanly;
  run 3–4 different tricky tasks; tune until it converges *repeatably*, not just once.
  Caution: tune for a clear, honest demo first. For later comparisons, keep prompts fixed so
  prompt changes are not confused with the diagnostic signal (see C5).

- [ ] **F18 — Failure mode signal.** On `gaveup`, intersect failing test names across attempts
  and record `persistent` (the same frozen assertion kept failing) or `varied` (ordinary
  difficulty). Do not interpret `persistent` as a specification mismatch: an internally
  inconsistent generated oracle is a counterexample. Surface it on the `Run` as an optional
  diagnostic; it is not a blocker.

- [ ] **F13 — Broader verification guidance.** Do not add a generic verifier language or task
  family registry. If a future trusted authored task needs property or metamorphic checks, keep
  them in its human-owned Go source and first show a second concrete use case. Properties can
  narrow the gap between "passed the oracle" and "met the spec," but an optimality claim still
  needs a trusted reference, a bounded exhaustive check, a lower-bound proof, or a certificate.

- [ ] **F10 — Attempt diffs.** Highlight what changed between consecutive attempts.

- [ ] **F11 — SSE streaming.** Push attempts the instant they land instead of polling.

- [ ] **F14 — SQLite persistence.** `modernc.org/sqlite` (zero-CGO) so runs survive a restart.

---

## Concerns (C)

- [ ] **C1 — Enforce the blind-oracle separation.** Three structural invariants:
  1. No coder prompt builder may accept test source. `FirstPrompt`/`RepairPrompt` take spec,
     signature, previous code, verifier output — nothing else.
  2. `TestPrompt` may not accept candidate code, and `oracle.Resolver.Resolve` runs before any
     coder call exists to produce some. The ordering is the guarantee.
  3. The oracle is frozen once per run. Nothing may regenerate it after attempt 1, and a
     generated oracle is never written back into `templates/`.
  Re-check whenever the task-authoring path or a prompt signature changes. **Widening one of
  those parameter lists voids the project's premise — flag it, don't do it.**

- [ ] **C4 — Correlated misreading in `generated` mode.** Coder and test-writer read the same
  ambiguous prose; the same model family can misread it identically, producing agreement on the
  wrong behaviour and a green run for the wrong reason. Mitigate with distinct role models and
  stronger oracles (F13). Never report a green `generated` run as "verified." Keep
  `authored` mode working as the control condition that makes this measurable.

- [ ] **C5 — Keep later comparisons interpretable.** During an optional spec-comparison run, a
  repair prompt tuned hard enough to rescue any spec can hide disagreement. Keep prompts fixed
  while measuring; treat prompt-quality work and spec-quality work as separate experiments and
  never vary both at once.

- [~] **C6 — Generated oracle semantic validity and answer-key provenance.** Preflight proves
  only that generated source is structurally admissible and compiles against a signature-derived
  stub. It cannot establish that a hard-coded expected value satisfies the specification. An
  observed generated oracle contained an impossible arithmetic answer key and falsely rejected a
  plausible candidate on every attempt. Distinguish structural admission, deterministic validity
  rules, and trusted-reference answer keys. An LLM committee may reject suspicious source before
  freezing, but is a filter rather than evidence of truth. Free-form generated tasks remain
  explicitly unaudited semantically.

- [ ] **C2 — Harden provider-adapter transport and retry policy before production use.** The
  current OpenAI adapter permits `http://` for local development and delegates one configured
  retry to the official SDK, which can use provider backoff/`Retry-After` guidance. Before
  production or repeated non-local use, restrict plaintext HTTP or require an explicit opt-in,
  decide exactly which transport/status failures may retry, and account for the possibility that
  a paid POST completed before a retryable connection failure. Preserve the whole-call context
  deadline, response-size bound, and sanitized error contract across every future adapter.

---

## Bugs (B)

### Critical
*None.*

### High

- [~] **B3 — Generated oracle can falsely reject a candidate with an internally inconsistent
  answer key.** An observed run froze an impossible expected result, so every candidate attempt
  failed the same invalid assertion. Do not route future browser tasks through task-specific
  semantic code. Current mitigation is honest labeling, structural preflight, and human/trusted
  review when a task needs stronger semantic verification.

### Medium
*None.*

### Low
*None.*
