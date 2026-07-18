# Repair Loop — Implementation Tracker

*Identifier counter: the next new items are **F19, C6, B2**.
Branch strategy: `main` (active development and production during the hackathon).*

> **Design change, 2026-07-18 (after F1/C3, before F2).** The oracle is no longer
> human-authored-only. It is now written *blind to the code* in one of two modes: `authored`
> (human) or `generated` (a separate test-writer LLM context reading only spec + signature).
> `generated` is now the interactive browser showcase; `authored` remains the terminal control
> condition. New work identifiers: **F15–F18, C4, C5**. Amended: **F3, F4, F6, C1**. Phase 0
> stayed `authored`-only on purpose — prove the coder loop against a known-good fixture before
> adding a second unproven half.

> **Hackathon execution decision, 2026-07-18.** The immediate browser milestone is now one
> interactive generated-oracle vertical slice: a user supplies a spec and pinned Go signature;
> a test-writer produces and freezes the oracle before a separate code-writer starts; the page
> shows both artifacts and the repair trace. This deliberately brings together **F15**, the
> essential preflight portion of **F16**, **F17**, and the useful browser-input portion of
> **F12**. It landed as small, separately tested changes. Do not build a frontend-only
> form over the fixed task, accept client-supplied test source, or hardcode provider model IDs.
> Presets fill the same editable inputs; they never auto-run.

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
  The tracked `.env.example` and ignored local `.env` document the required provider variables
  plus optional model allowlist and role defaults. The configured-provider smoke test is opt-in:
  `LLM_LIVE_TEST=run go test -tags=integration
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
  then `go test -timeout <verifier timeout> ./...` only after a successful build, under one
  injected verifier timeout.
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

### Phase 1 — Blind test-writer and interactive path

- [x] **F15 — Generated oracle (the design change).** `domain.OracleMode` + `Task.Oracle`,
  pure `prompt.TestPrompt(spec, signature)`, and `repair.resolveOracle` now resolve and freeze
  the generated test source before attempt 1. `Repair` takes explicit coder + tester `llm.LLM`
  values and reports one accepted oracle to the run store before it asks the coder. Authored mode
  remains a supported control and permits a nil tester.
  Depends on: F4.
  Structural checks prove tester → frozen oracle → coder ordering, no test source in coder
  prompts, no candidate source in tester prompts, and no regeneration after acceptance.

- [x] **F16 — Oracle preflight + fault attribution.** Generated tests are parsed and compiled
  with `go test -c` against a parsed signature-derived stub before any coder call; the test
  binary is never run. A rejected candidate oracle retries only up to the injected
  `-oracle-attempts` cap. Exhaustion returns typed `OracleFailureError`, which becomes terminal
  `oraclefailed`, never a candidate attempt. Preflight also rejects source without a runnable
  `Test` function, a direct call to the pinned function, or a testing failure method, as well as
  build constraints, `TestMain`, `init`, test skips, and direct `os.Exit`; it type-checks the
  pinned stub and keeps caller cancellation/setup/preflight-timeout failures as ordinary
  infrastructure errors.
  Depends on: F15.

- [x] **F17 — Split coder and test-writer models.** `LLM_MODEL_CODER` /
  `LLM_MODEL_TESTER` fall back to `LLM_MODEL`; `LLM_MODELS` is an optional comma-separated
  browser allowlist. Role selection happens at the composition root. Role-agnostic
  `llm.ModelCatalog` creates one reusable client per allowed ID and rejects arbitrary IDs.
  Depends on: F15.
  Same-model role selection remains legal, but the browser records both selected IDs so a saved
  run is interpretable.
  Verification (2026-07-18): `go build ./...`, `go test ./...`, `go test -race ./...`,
  `go test -cover ./...`, `go vet ./...`, `gofmt`, inline-JavaScript syntax, the no-HTML-
  injection static check, and `git diff --check` passed. No live provider request was made.

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

- [x] **F7 — Run orchestration + store.** `internal/run.Store` starts per-run role clients in
  goroutines and returns stable IDs immediately. Its synchronous progress reporter records the
  accepted frozen oracle and appends `domain.Attempt` values under a mutex; `GetRun` returns a
  copied snapshot. State is an in-memory `map[string]*Run` for the lifetime of the process. The
  JSON record includes the submitted spec/signature, oracle, selected `coderModel`/
  `testerModel`, attempt budget, lifecycle fields, and result.
  Depends on: F4.
  A browser run owns a bounded context and private cancel function. Generated runs move through
  `writingoracle` and `preflightingoracle` with `currentAttempt: 0`, then candidate provider
  and verifier stages; cancellation is `canceling`, and all terminal runs are `complete`.
  The store rejects a second live run server-side, not only in the UI. A total deadline becomes
  `timedout`, while a per-call provider timeout remains an infrastructure outcome.
  Verification (2026-07-18): scripted runs cover generated fail → pass, authored control,
  frozen oracle snapshots, tester cancellation/timeout, `oraclefailed`, verifier staging,
  immutable snapshots, and one-live-run protection.

### Phase 3 — Web server

- [x] **F8 — HTTP layer.** `internal/server` + `cmd/repair -serve` use Go 1.22+ `net/http`
  routing. `GET /setup` returns safe model IDs, role defaults, and editable presets. `POST /run`
  accepts a strict JSON spec/signature/role-selection request plus a browser-generated
  idempotency token and starts a server-constructed generated-oracle task. Retrying the same
  token returns the original run ID rather than creating another run. `POST /run/{id}/cancel`
  accepts a live cancellation; `GET /run/{id}`
  returns the live `Run` snapshot. Unknown IDs return `404`; a second live start or terminal
  cancel returns `409`. JSON responses are `Cache-Control: no-store`.
  Depends on: F7.
  The server rejects unknown JSON fields (including `testCode`), invalid/type-invalid signatures,
  oversized fields, malformed request IDs, and model IDs outside the configured catalog with
  `400`.
  Verification (2026-07-18): handler tests serve the page/setup, start and poll a real
  verifier-backed generated-oracle run, reject unsafe input, recover the same ID after an
  idempotent retry, enforce one live run, and cancel a context-aware test-writer call.

### Phase 4 — UI (the flashy payoff)

- [x] **F9 — Embedded UI.** `internal/server/index.html`, embedded by
  `internal/server/assets.go`, is plain HTML/CSS/vanilla JS—no framework, CDN, npm, or build
  step. Its compact form accepts an optional name, required spec/signature, and separate allowed
  code-writer/test-writer models; preset buttons only populate those editable fields. It locks
  the form while a run is active, then shows role metadata, pending → frozen oracle source, and
  a comparison-first candidate/feedback/later-candidate layout. Attempt selectors and full JSON
  download preserve the observed record.
  Depends on: F8.
  It names oracle writing/preflight, coder wait, Go verification, cancellation, timeout, and
  oracle failure truthfully. A generated green state says a candidate satisfied a frozen blind
  oracle, never verified correctness. A lost start response retries the same idempotency token while the
  form remains locked; polls are bound to one ID/generation and preserve observation on ambiguous
  network failures. Verification (2026-07-18): the inline script parsed, static checks
  found no external assets/framework or HTML injection API, and the server test serves it.

### Phase 5 — Stretch (only if the above is solid)

- [ ] **F10 — Attempt diffs.** Highlight what changed between consecutive attempts.
- [ ] **F11 — SSE streaming.** Push attempts the instant they land instead of polling.
- [x] **F12 — Task input in UI.** The browser lets a user type a spec and required pinned signature rather
  than picking a file. The immediate generated-mode path accepts neither client test source nor
  a client-selected oracle mode: the server constructs `OracleGenerated`, resolves it before any
  candidate exists, then freezes it. A compact preset list fills those same editable fields and
  never auto-runs. The UI exposes separate allowed test-writer and code-writer model selections;
  provider-specific IDs come from configured allowlist data, never hardcoded browser values.
  Server-side length/type/model validation, idempotent start recovery, and one-live-run
  protection are implemented.
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
  (F17) and stronger oracles (F13). Never report a green `generated` run as "verified" — a
  candidate satisfied a frozen oracle generated blind to every candidate, and later candidates
  may have used Go feedback. Keep `authored` mode working as the control condition that makes
  this measurable.

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
  reconnect rather than orphaning a run in the UI; on an ambiguous start response, retry the same
  idempotency token to recover its run ID rather than accidentally creating a second run. Ignore
  callbacks tied to an earlier run.
  Verification (2026-07-18): `go build ./...`, `go test ./...`, server cancel-route tests, and
  run-store provider-wait/verifier/cancel/timeout tests passed. No live provider call was needed.

### Medium
*None yet.*

### Low
*None yet.*
