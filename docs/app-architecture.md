# Application Architecture

The settled architecture, stack, and patterns for **Repair Loop**. Read this before
changing anything.

## Overview

A small Go program that runs a **generate → test → repair** loop: an LLM writes a Go
function to a spec, the Go toolchain checks it, and on failure the compiler/test output is
fed back so the model tries again — until the tests pass or attempts run out. It runs as a
CLI (Phase 0) and as a web server with a live UI (Phase 3+).

Big constraints, stated up front:

- **The Go toolchain is the verifier.** `go build` (the compiler) is checker #1; `go test`
  is checker #2. No separate assertion framework.
- **No sandbox.** Generated code runs locally in a throwaway temp module; a context
  **timeout** handles runaway/infinite-loop code. This is acceptable because we run our own
  benign code, not untrusted third-party code at scale.
- **Tests are human-authored and separate from code generation** (see AGENTS.md).
- **State is in-memory** in v1. Nothing persists across restarts (SQLite is a stretch).

## Implementation Status

As of 2026-07-18, only F1 is implemented. The repository contains the Go module and
`internal/llm`: the `LLM` interface, an OpenAI-compatible client, environment-backed
configuration, and local tests. The client is locally verified with build, unit, race,
coverage, and vet checks; live-provider verification awaits configured `LLM_*` values.

`prompt`, `repair`, `task`, `run`, `server`, `web`, and `cmd/repair` do not exist yet.
F1 must make one real completion before F2 begins.

## Technology Stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| HTTP router (F8) | Go 1.22+ `net/http` routing; `chi` only if a concrete need justifies it |
| Frontend | single HTML file + vanilla JS, embedded via `//go:embed` |
| LLM provider | OpenAI-compatible chat completions endpoint (set by base URL + key + model + timeout) |
| Verifier (the "oracle") | `go build` + `go test` in a temp module |
| Run state | in-memory `map[string]*Run` + mutex (SQLite via `modernc.org/sqlite` as a stretch) |
| Concurrency | one goroutine per run; status exposed for polling |
| Isolation | `context` timeout on `go test` — no sandbox |
| Build / deploy | `go build` → single static binary |

## Package Architecture

Each package has one job. Dependency direction: `web → server → run → repair → {prompt, llm}`,
with `task` feeding `run`. Nothing imports upward; no cycles.

| Package | Responsibility |
|---|---|
| `internal/llm` | The `LLM` interface + one concrete client for an OpenAI-compatible endpoint. Owns the HTTP call, and reads base URL + API key + model + timeout from env. Per-call timeout + one retry. The interface keeps the rest of the code independent of any specific provider — swapping providers is a base-URL/key/model change here and nowhere else. |
| `internal/prompt` | `firstPrompt`, `repairPrompt`, `extractGoCode`. Pure string work, no I/O. Loop quality lives here. |
| `internal/repair` | The loop: `Repair`, `runTests`. Temp-module + `go test` machinery + retry logic. The heart. |
| `internal/task` | The `Task` type and task loading (hardcoded → files under `tasks/`). |
| `internal/run` | Orchestration + state. Launches `Repair` in a goroutine, appends attempts to a `Run` as they land, answers "status of run X." In-memory. |
| `internal/server` | HTTP handlers (start a run, poll a run) + serving the embedded UI. Thin. |
| `web` | One HTML file + vanilla JS, baked into the binary. Polls the run endpoint and redraws. |

### Current F1 client behavior

`LLM_BASE_URL` is an API base URL (it may include `/v1`); the client appends
`/chat/completions`. It applies one timeout to the whole completion operation and makes one
immediate retry after a request/read error, HTTP 429, or HTTP 5xx. It currently permits
`http://` for local test/development providers, so non-local providers must use HTTPS.
The retry and transport hardening work is tracked in C2.

## Data Shapes

```go
type Task struct {
	Name      string // identifier
	Spec      string // natural-language description of desired behaviour
	Signature string // pins the API so the human tests compile
	TestCode  string // full solution_test.go, human-written
}

type Attempt struct {
	N      int    // 1-based
	Code   string // generated solution.go
	Passed bool   // did `go test` exit 0?
	Output string // combined compiler/test output — the feedback signal
}

type Run struct {
	ID       string    `json:"id"`
	Task     string    `json:"task"`
	Spec     string    `json:"spec"`
	Status   string    `json:"status"`   // "running" | "passed" | "gaveup"
	Attempts []Attempt `json:"attempts"` // grows as the loop runs
}
```

## The Loop

1. `generate` builds a prompt (spec on attempt 1; previous code + `go test` output on
   later attempts) and asks the model for a Go function.
2. `runTests` writes `go.mod` + `solution.go` + `solution_test.go` into an `os.MkdirTemp`
   module, runs `go build ./...`, then runs `go test ./...` under the injected timeout.
3. Pass → return. Fail → record the attempt, feed its output into the next `generate`.
4. Stop at `maxAttempts`.

## HTTP Contract

```
POST /run       → { "id": "run_abc" }   // starts a run in a goroutine, returns immediately
GET  /run/{id}  → Run                    // poll ~2x/sec; stop when Status != "running"
```

Freeze this contract early — it lets the loop and the UI be built in parallel (one worktree
each), the UI polling a mock run until the real one is ready.

## Persistence

**None in v1** — runs live in memory and are lost on restart. This is fine for a live demo.
SQLite (`modernc.org/sqlite`, zero-CGO) is a stretch item if runs need to survive restarts.

## Error Handling

- A valid text completion — even one that is invalid Go — is recorded as a failed `Attempt`
  and drives the next iteration; it is **not** a program error.
- Provider transport/protocol failures (network errors, non-success API responses, malformed
  response JSON) are infrastructure errors until a later design explicitly classifies them.
- A generated-code test-execution timeout is recorded as a failed attempt with a "likely
  infinite loop" note. An LLM completion timeout is a provider/infrastructure error.
- Only genuine infra faults (network down, can't create temp dir, `go` not on PATH) bubble
  up as Go errors.

## Deployment

- **Target**: run the binary anywhere with the Go toolchain available (the loop shells out
  to `go test`, so `go` must be on PATH on the host).
- **Build output**: a single static binary via `go build -o repair ./cmd/repair`. The UI is
  embedded, so there is no separate frontend to deploy.
