# Repair Loop — Implementation Tracker

*Identifier counter: the next new items after this initial plan are **F15, C4, B1**.
Branch strategy: `main` (active development and production during the hackathon).*

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

- [ ] **F2 — Prompt + extraction.** `internal/prompt` (pure, no I/O):
  `FirstPrompt(spec, signature)`, `RepairPrompt(spec, signature, previousCode, verifierOutput)`,
  and `ExtractGoCode(raw)`. Prompts require a complete `package solution` source file with the
  specified signature, stdlib only, source only, and never tests. Repair prompts include the
  previous candidate and exact output from the first failed verifier stage. Extraction removes
  only one unambiguous complete fence and never repairs the source.

- [ ] **F3 — verifier.** `internal/repair`: write `go.mod`, model-produced `solution.go`, and
  fixed human `solution_test.go` from `domain.Task` into an `os.MkdirTemp` module. Write source
  verbatim; do not run `go mod tidy`, install dependencies, or repair it. Run `go build ./...`,
  then `go test ./...` only after a successful build, under one injected verifier timeout.
  A non-zero command exit is feedback with that command's raw combined output, not an infra
  error. Verifier timeout is failed-attempt feedback; caller cancellation, temp/write/cleanup,
  and command-launch failures bubble as errors.

- [ ] **F4 — Repair loop + one hardcoded task.** `internal/repair` `Repair(...)` wiring
  `llm.LLM` + `domain.Task` through generate → verify → feedback → retry, plus
  `cmd/repair/main.go` running ONE hardcoded, human-authored tricky task and printing each
  attempt's number, pass/fail, and output through the attempt reporter.
  Depends on: F1, F2, F3.
  **Milestone: `attempt 1 fail → … → attempt N pass` in the terminal. Protect this.**

### Phase 1 — Loop quality (still terminal)

- [ ] **F5 — Tune repair prompts on real tasks.** Feed previous code + test output cleanly;
  run 3–4 different tricky tasks; tune until it converges *repeatably*, not just once.
  Depends on: F4.

- [ ] **F6 — Task loading.** `internal/task`: load and return `domain.Task` values from `tasks/`
  (each subdir = one task: `spec.md` + human-authored `solution_test.go`). Auto-discovered, no
  registration.
  Depends on: F4.

### Phase 2 — State (the bridge)

- [ ] **F7 — Run orchestration + store.** `internal/run`: `StartRun(domain.Task)` launches
  `Repair` in a goroutine and returns a run ID immediately; `domain.Attempt` values append to a
  `Run` as they finish; in-memory `map[string]*Run` + mutex. **Freeze the `Run` JSON shape
  here** — this contract unblocks parallel UI work.
  Depends on: F4.
  Verify — start a run and poll its status as JSON via curl.

### Phase 3 — Web server

- [ ] **F8 — HTTP layer.** `internal/server` + `cmd/repair -serve`: `POST /run` (start, return
  ID) and `GET /run/{id}` (status + attempts). Use Go 1.22+ `net/http` routing; add `chi`
  only if a concrete need justifies it.
  Depends on: F7.
  Verify — curl starts a run and polls it to completion.

### Phase 4 — UI (the flashy payoff)

- [ ] **F9 — Embedded UI.** `web/index.html` + `web/embed.go`: vanilla JS polling
  `GET /run/{id}` ~2x/sec and redrawing. Attempts appear one at a time as cards; each shows
  number, code, a red/green badge, and test output; the final card flips green. Syntax
  highlighting via a CDN one-liner. Served via `//go:embed`.
  Depends on: F8.
  Verify — watch a run fail red and go green in the browser.

### Phase 5 — Stretch (only if the above is solid)

- [ ] **F10 — Attempt diffs.** Highlight what changed between consecutive attempts.
- [ ] **F11 — SSE streaming.** Push attempts the instant they land instead of polling.
- [ ] **F12 — Task input in UI.** Let the user type a spec + tests rather than picking a file.
- [ ] **F13 — Property-test check.** Offer property/metamorphic tests as the oracle instead of
  example tests — a stronger check and the more novel angle.
- [ ] **F14 — SQLite persistence.** `modernc.org/sqlite` (zero-CGO) so runs survive a restart.

## Concerns (C)

- [ ] **C1 — Enforce the code/test separation.** The whole premise requires task tests to be
  human-authored and never generated in the same context as the code. When adding tasks
  (F6) or task input (F12), make it structurally impossible to auto-generate the tests
  alongside the solution. Re-check whenever the task-authoring path changes.

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

## Bugs (B)

### Critical
*None yet.*

### High
*None yet.*

### Medium
*None yet.*

### Low
*None yet.*
