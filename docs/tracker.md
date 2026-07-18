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
  tracked. C3 is now the prerequisite before F2.

- [ ] **F2 — Prompt + extraction.** `internal/prompt` (pure, no I/O): `firstPrompt(task)`,
  `repairPrompt(task, prev)`, `extractGoCode(raw)`. First prompt = spec + signature +
  "output only a Go function, package `solution`, stdlib only." Repair prompt additionally
  includes the previous code and the exact `go test` output.

- [ ] **F3 — runTests.** `internal/repair`: write `go.mod` + `solution.go` + `solution_test.go`
  into an `os.MkdirTemp` module, run `go build ./...` then `go test ./...` under an injected
  context timeout, return `(passed, combined output, error)`. A non-zero build/test exit is
  feedback, not an infra error; a timeout is a failed attempt with an "infinite loop" note.
  Filesystem, command-launch, and caller-cancellation failures must bubble up as errors.

- [ ] **F4 — Repair loop + one hardcoded task.** `internal/repair` `Repair(...)` wiring
  generate → runTests → feed-back → retry, plus `cmd/repair/main.go` running ONE hardcoded
  tricky task and printing each attempt's number, pass/fail, and output.
  Depends on: F1, F2, F3.
  **Milestone: `attempt 1 fail → … → attempt N pass` in the terminal. Protect this.**

### Phase 1 — Loop quality (still terminal)

- [ ] **F5 — Tune repair prompts on real tasks.** Feed previous code + test output cleanly;
  run 3–4 different tricky tasks; tune until it converges *repeatably*, not just once.
  Depends on: F4.

- [ ] **F6 — Task loading.** `internal/task`: load tasks from `tasks/` (each subdir = one
  task: `spec.md` + human-authored `solution_test.go`). Auto-discovered, no registration.
  Depends on: F4.

### Phase 2 — State (the bridge)

- [ ] **F7 — Run orchestration + store.** `internal/run`: `StartRun(task)` launches `Repair`
  in a goroutine and returns a run ID immediately; attempts append to a `Run` as they finish;
  in-memory `map[string]*Run` + mutex. **Freeze the `Run` JSON shape here** — this contract
  unblocks parallel UI work.
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

- [ ] **C3 — Resolve Phase 0 data and error contracts before F2/F3.** Define ownership of
  `Task` and `Attempt` so the pure `prompt` package never imports upward into `repair`; also
  decide how `repair` distinguishes valid-but-bad generated text (attempt feedback) from
  malformed provider protocol responses (infrastructure error).

## Bugs (B)

### Critical
*None yet.*

### High
*None yet.*

### Medium
*None yet.*

### Low
*None yet.*
