# Repair Loop — Implementation Tracker

*Identifier counter: the next new items are **F19, C6, B2**.
Branch strategy: `main` (active development and production during the hackathon).*

> **Design change, 2026-07-18 (after F1/C3, before F2).** The oracle is no longer
> human-authored-only. It is now written *blind to the code* in one of two modes: `authored`
> (human) or `generated` (a separate test-writer LLM context reading only spec + signature).
> `generated` is the later showcase and research-inspired variation; `authored` is the control
> condition and the immediate demo path. New work: **F15–F18, C4, C5**. Amended: **F3, F4,
> F6, C1**. Phase 0 stays `authored`-only on purpose — prove the coder loop against a
> known-good fixture before adding a second unproven half.

Status markers: `[ ]` not started · `[~]` in progress · `[x]` done.
Work the phases in order. **Phase 0 must be green before anything in Phase 3+ begins.**

## Features (F)

### Phase 0 — Core loop, terminal (critical path)

- [x] **F1 — LLM client.** `internal/llm`: the `LLM` interface + one concrete client for an
  OpenAI-compatible endpoint (`Complete(ctx, prompt) (string, error)`). Base URL, key, model,
  and timeout from env. Per-call timeout + one retry. Verify in isolation — a single completion
  returning text — before wiring anything else.
  Local verification (2026-07-18; rerun after adding the live test): `go build ./...`,
  `go test ./...`, `go test -race ./...`, `go test -cover ./...` (75.0%), `go vet ./...`,
  and `gofmt` passed. Tests include a standard-library `httptest` completion, retry, and
  timeout coverage. Non-success provider responses expose their HTTP status without retaining
  their body.
  The tracked `.env.example` and ignored local `.env` provide the four variables. The
  configured-provider smoke test is opt-in: `LLM_LIVE_TEST=run go test -tags=integration
  ./internal/llm -run '^TestLiveCompletion$' -count=1 -v`. It requires HTTPS, is excluded
  from ordinary test runs, and must pass before F1 is marked done.
  Live verification passed on 2026-07-18: the opt-in HTTPS probe returned non-empty text.
  An initial HTTP 401 was traced to and resolved by correcting the local API key; no secret is
  tracked. The completed C3 contract below unblocks F2.

- [x] **F2 — Prompt + extraction.** `internal/prompt` (pure, no I/O):
  `FirstPrompt(spec, signature)`, `RepairPrompt(spec, signature, previousCode, verifierOutput)`,
  and `ExtractGoCode(raw)`. Prompts require a complete `package solution` source file with the
  specified signature, stdlib only, source only, and never tests. Repair prompts include the
  previous candidate and exact output from the first failed verifier stage. Extraction removes
  only one unambiguous complete backtick fence when it is the whole reply apart from outer
  whitespace, and never repairs the source. Verification (2026-07-18): `gofmt -l $(rg --files
  -g '*.go')`, `go build ./...`, `go test ./...`, `go test -race ./...`, `go test -cover ./...`
  (97.4% for `internal/prompt`), `go vet ./...`, and `git diff --check` passed. No live provider
  call was required.

- [x] **F3 — verifier.** `internal/repair`: write `go.mod`, model-produced `solution.go`, and
  the frozen `solution_test.go` from `domain.Task` into an `os.MkdirTemp` module. Write source
  verbatim; do not run `go mod tidy`, install dependencies, or repair it. Run `go build ./...`,
  then `go test ./...` only after a successful build, under one injected verifier timeout.
  A non-zero command exit is feedback with that command's raw combined output, not an infra
  error. Verifier timeout is failed-attempt feedback; caller cancellation, temp/write/cleanup,
  and command-launch failures bubble as errors. Verification (2026-07-18): passing code,
  build failure, test failure, malformed test source, verifier timeout, caller cancellation,
  missing `go`, and invalid-input tests passed. `gofmt -l $(rg --files -g '*.go')`, `go build
  ./...`, `go test ./...`, `go test -race ./...`, `go test -cover ./...` (79.5% for
  `internal/repair`), `go vet ./...`, and `git diff --check` passed. No live provider call was
  required.

- [x] **F4 — Repair loop + one hardcoded task.** `internal/repair` `Repair(...)` wiring
  `llm.LLM` + `domain.Task` through generate → verify → feedback → retry, plus
  `cmd/repair/main.go` running ONE hardcoded, **authored-mode** tricky task and printing each
  attempt's number, pass/fail, and output through the attempt reporter.
  Depends on: F1, F2, F3.
  Scope note: `authored` mode only. F4 takes an explicit coder; F15 deliberately extends the
  loop with a test-writer after the generated-oracle contracts exist.
  **Milestone: `attempt 1 fail → … → attempt N pass` in the terminal. Protect this.**
  Verification (2026-07-18): a scripted LLM proves fail → exact verifier feedback → pass,
  extraction of fenced code, synchronous reporting, no oracle-source leak into coder prompts,
  exhaustion, provider failure, reporter failure, and pre-provider input validation. The fixed
  split-cents CLI oracle accepts a reference implementation. `gofmt -l $(rg --files -g '*.go')`,
  `go build ./...`, `go test ./...`, `go test -race ./...`, `go test -cover ./...` (86.8% for
  `internal/repair`), `go vet ./...`, and `git diff --check` passed. No live provider call was
  required.

### Phase 1 — The test-writer, then loop quality (still terminal)

- [ ] **F15 — Generated oracle (the design change).** Add `domain.OracleMode` +
  `Task.Oracle`; add pure `prompt.TestPrompt(spec, signature)` requiring a complete
  `package solution` test file, stdlib `testing` only, table-driven, no implementation; add
  `repair.resolveOracle` to obtain and freeze one accepted oracle before attempt 1; extend
  `Repair` to take coder + tester `llm.LLM` values. F16 may retry rejected oracle candidates,
  but only before any coder call exists.
  Depends on: F4.
  Non-negotiable: `TestPrompt` takes spec and signature only — no parameter may carry candidate
  code — and nothing after `resolveOracle` may regenerate the oracle. Add a unit test asserting
  the coder prompt builders never receive `TestCode`.
  Verify — run the same task in both modes; both reach a verdict.

- [ ] **F16 — Oracle preflight + fault attribution.** A non-compiling generated oracle must
  never be charged to the coder. `go build` ignores `*_test.go`, so preflight the generated
  oracle before any coder call by compiling it with `go test -c` against a signature-derived
  stub (built, never run). A preflight failure is an oracle fault; regenerate up to an injected
  cap, then end `oraclefailed` if no candidate is accepted. Use `go/parser` if needed to derive
  the stub safely from the pinned signature.
  Depends on: F15.

- [ ] **F17 — Split coder and test-writer models.** `LLM_MODEL_CODER` / `LLM_MODEL_TESTER`,
  both falling back to `LLM_MODEL`. Construct two `llm.LLM` values at the composition root;
  `internal/llm` stays role-agnostic. Mitigates correlated misreading of an ambiguous spec.
  Depends on: F15.

- [ ] **F18 — Failure mode signal.** On `gaveup`, intersect failing test names across attempts
  and record `persistent` (a possible interpretation mismatch worth inspecting) or `varied`
  (ordinary difficulty). Surface it on the `Run` as an optional diagnostic after the dual-mode
  flow works; it is not a blocker for the hackathon demo.
  Depends on: F15, F7.

- [ ] **F5 — Tune repair prompts on real tasks.** Feed previous code + test output cleanly;
  run 3–4 different tricky tasks; tune until it converges *repeatably*, not just once.
  Depends on: F4.
  Caution: tune for a clear, honest demo first. For later comparisons, keep prompts fixed so
  prompt changes are not confused with the diagnostic signal (see C5).

- [ ] **F6 — Task loading.** `internal/task`: load and return `domain.Task` values from `tasks/`
  (each subdir = one task). `spec.md` is required and pins the signature; `solution_test.go` is
  **optional** and its presence selects the mode — present → `OracleAuthored`, absent →
  `OracleGenerated`. Auto-discovered, no registration. Never write a generated oracle back into
  `tasks/`.
  Depends on: F4.

### Phase 2 — State (the bridge)

- [x] **F7 — Run orchestration + store.** `internal/run.Store` starts authored-oracle
  `Repair` calls in goroutines and returns stable IDs immediately. Its synchronous reporter
  appends `domain.Attempt` values under a mutex; `GetRun` returns a copied snapshot. State is an
  in-memory `map[string]*Run` for the lifetime of the process. The JSON shape is frozen with
  `signature`, `maxAttempts`, `oracle`, `testCode`, `stage`, `currentAttempt`, `startedAt`,
  `deadlineAt`, `failureMode`, and `error`. Statuses are `running`, `passed`, `gaveup`,
  `canceled`, `timedout`, `infrastructurefailed`, and the F15/F16-reserved `oraclefailed`.
  Depends on: F4.
  Current runs set `oracle` to `authored`; `failureMode` remains empty until F18. Each browser
  run owns a bounded context and private cancel function. Its live stage moves from `starting`
  to `waitingforprovider` and `verifying`; cancellation is `canceling`, and all terminal runs
  are `complete`. A total deadline becomes `timedout`, while a per-call provider timeout remains
  an infrastructure outcome.
  Verification (2026-07-18): scripted coder tests exercised real Go verification through
  failed → passed, provider infrastructure failure, input validation, immutable snapshots,
  provider-wait and verifier stages, explicit cancellation, and whole-run timeout.

### Phase 3 — Web server

- [x] **F8 — HTTP layer.** `internal/server` + `cmd/repair -serve` use Go 1.22+ `net/http`
  routing. `GET /task` returns the injected fixed authored task for display; `POST /run` returns
  `202 {"id":"run_..."}` and starts it; `POST /run/{id}/cancel` accepts a live cancellation;
  and `GET /run/{id}` returns the live `Run` snapshot. Unknown IDs return `404
  {"error":"run not found"}`; cancel requests after a terminal outcome return `409`. JSON
  responses are `Cache-Control: no-store`. There is no task request body or task selector until
  F6/F12.
  Depends on: F7.
  Verification (2026-07-18): handler tests serve the embedded page, fixed task context, start a
  real verifier-backed scripted run and poll it to `passed`; a context-aware scripted provider
  proves the cancel endpoint reaches `canceled` and handles unknown/terminal IDs correctly.

### Phase 4 — UI (the flashy payoff)

- [x] **F9 — Embedded UI.** `internal/server/index.html`, embedded by
  `internal/server/assets.go`, is plain HTML/CSS/vanilla JS—no framework, CDN, npm, or build
  step. It preloads the fixed task and frozen **authored** oracle, polls live runs every 500ms,
  and uses DOM text nodes for all source/output. Its comparison-first desktop layout places a
  rejected candidate, exact verifier feedback, and a later candidate side by side; selectors
  support other attempt pairs and terminal runs can be downloaded as JSON. It names the actual
  live stage (provider wait, Go verification, or cancellation), shows elapsed time against the
  server-owned run deadline, binds polls to one run ID/generation so late responses cannot
  overwrite a later run, and keeps a second run disabled while server state is uncertain. It
  shows first-attempt passes, exhaustion, cancellation, timeout, and infrastructure failure
  honestly; it does not fabricate a live red → green result.
  Depends on: F8.
  Verification (2026-07-18): the inline script parsed successfully, static checks found no
  external asset/framework or `innerHTML` usage, and the server test serves the embedded page.
  A real browser/provider rehearsal remains opt-in and was not run during this change.

### Phase 5 — Stretch (only if the above is solid)

- [ ] **F10 — Attempt diffs.** Highlight what changed between consecutive attempts.
- [ ] **F11 — SSE streaming.** Push attempts the instant they land instead of polling.
- [ ] **F12 — Task input in UI.** Let the user type a spec (and optionally an authored oracle)
  rather than picking a file. Typing a spec and getting a generated oracle back is the natural
  demo of the whole idea. The UI must never let one submission produce both the code and its
  own tests from a single context (see C1).
- [ ] **F13 — Property-test check.** Offer property/metamorphic tests as the oracle instead of
  example tests — a stronger check and the more novel angle. Especially valuable in `generated`
  mode: properties are harder to satisfy by special-casing than examples, which narrows the gap
  between "passed the oracle" and "met the spec."
- [ ] **F14 — SQLite persistence.** `modernc.org/sqlite` (zero-CGO) so runs survive a restart.

## Concerns (C)

- [ ] **C1 — Enforce the blind-oracle separation.** *(amended 2026-07-18 — the rule is no
  longer "human-authored", it is "written without sight of the code".)* Three invariants, all
  structural rather than procedural:
  1. No coder prompt builder may accept test source. `FirstPrompt`/`RepairPrompt` take spec,
     signature, previous code, verifier output — nothing else.
  2. `TestPrompt` may not accept candidate code, and `resolveOracle` runs before any coder call
     exists to produce some. The ordering is the guarantee.
  3. The oracle is frozen once per run. Nothing may regenerate it after attempt 1, and a
     generated oracle is never written back into `tasks/`.
  Re-check whenever the task-authoring path or a prompt signature changes. **Widening one of
  those parameter lists voids the project's premise — flag it, don't do it.**

- [ ] **C4 — Correlated misreading in `generated` mode.** Coder and test-writer read the same
  ambiguous prose; the same model family can misread it identically, producing agreement on the
  wrong behaviour and a green run for the wrong reason. Mitigate with distinct role models
  (F17) and stronger oracles (F13). Never report a green `generated` run as "verified" — it is
  evidence that two readings agreed. Keep `authored` mode working as the control condition that
  makes this measurable.

- [ ] **C5 — Keep later comparisons interpretable.** During an optional spec-comparison run,
  a repair prompt tuned hard enough to rescue any spec can hide disagreement. Keep prompts fixed
  while measuring; treat prompt-quality work and spec-quality work as separate experiments and
  never vary both at once. This does not block ordinary hackathon prompt work or the core demo.

- [ ] **C2 — Harden F1 transport and retry policy before production use.** The current client
  permits `http://` for local development and makes one immediate retry after request/read
  errors, HTTP 429, or HTTP 5xx. Before production or repeated non-local use, restrict plaintext
  HTTP or require an explicit opt-in, honor `Retry-After`/backoff, and narrow retries to known
  temporary failures so a paid POST is not duplicated unnecessarily.

- [x] **C3 — Resolve Phase 0 data and error contracts before F2/F3.** `internal/domain` owns
  dependency-free `Task` and `Attempt`; `internal/task` is a future loader only. F2 will create
  a type-free `prompt` package with the three exported primitive APIs named above. The repair
  generation helper receives only spec and signature, so `Task.TestCode` has no route to the
  LLM. The future `repair` package will use the existing `llm.LLM` interface and import
  `domain`, `prompt`, and `llm` without an upward dependency.
  Contract: any nil-error completion string is candidate source; provider/transport/protocol
  errors are infrastructure errors. Non-zero build/test exits and verifier-owned timeout are
  failed attempts; caller cancellation/deadline, invalid cap/timeout, temp/write/cleanup,
  process-launch, and reporter failures are errors. Exhaustion returns the last failed attempt
  and nil.
  Verification (2026-07-18): `gofmt -l $(rg --files -g '*.go')`, `go build ./...`, `go test ./...`,
  `go test -race ./...`, `go test -cover ./...` (75.0% for `internal/llm`), `go vet ./...`,
  and `git diff --check` passed. No live provider call was required.
  *Amendment (2026-07-18, post-design-change):* "`Task.TestCode` has no route to the LLM" now
  reads "no route to the **coder**." In `generated` mode `TestCode` is produced by the
  test-writer before the loop begins and is then frozen. The contract that matters is unchanged
  and still holds: no coder prompt builder can receive test source. `Task` gains an `Oracle`
  field under F15.

## Bugs (B)

### Critical
*None yet.*

### High

- [x] **B1 — Make browser runs observable and bounded.** A browser run previously exposed only
  `running` plus completed attempts, so a delayed provider response or Go check looked frozen;
  the page could also lose its poller without stopping the server-side job. Add an explicit live
  stage, current attempt, start/deadline timestamps, a server-owned whole-run context, and a
  real `POST /run/{id}/cancel` path. Do not relabel a user cancellation or configured whole-run
  deadline as a candidate failure. On ambiguous poll failure, keep the start action disabled and
  reconnect rather than orphaning a run in the UI; ignore callbacks tied to an earlier run.
  Verification (2026-07-18): `go build ./...`, `go test ./...`, server cancel-route tests, and
  run-store provider-wait/verifier/cancel/timeout tests passed. No live provider call was needed.

### Medium
*None yet.*

### Low
*None yet.*
