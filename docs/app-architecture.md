# Application Architecture

The settled architecture, stack, and patterns for **Repair Loop**. Read this before
changing anything.

## Overview

A small Go program that runs a **generate → verify → repair** loop: an LLM writes a complete
Go source file to a spec, the Go toolchain checks it, and the first failed verifier output is
fed back so the model tries again — until the tests pass or attempts run out. It runs as a CLI
(Phase 0) and as a web server with a live UI (Phase 3+).

Big constraints, stated up front:

- **The Go toolchain is the verifier.** `go build` (the compiler) is checker #1; `go test`
  is checker #2. No separate assertion framework.
- **No sandbox.** Generated code runs locally in a throwaway temp module; a context
  **timeout** handles runaway/infinite-loop code. This is acceptable because we run our own
  benign code, not untrusted third-party code at scale.
- **Tests are human-authored and separate from code generation** (see AGENTS.md).
- **State is in-memory** in v1. Nothing persists across restarts (SQLite is a stretch).

## Implementation Status

As of 2026-07-18, F1 and C3 are implemented. The repository contains the Go module,
`internal/llm`, and the dependency-free shared types in `internal/domain`. The client has an
OpenAI-compatible implementation, environment-backed configuration, and local tests; its
configured-provider smoke test returned non-empty text on 2026-07-18.

`prompt`, `repair`, `task`, `run`, `server`, `web`, and `cmd/repair` do not exist yet. F2 is
the next implementation item.

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
| Isolation | one injected verifier timeout across `go build` and `go test` — no sandbox |
| Build / deploy | `go build` → single static binary |

## Package Architecture

Each package has one job. `internal/domain` is the bottom leaf: it imports nothing and owns
shared `Task` and `Attempt` values. Dependency direction is `web → server → run → repair →
{prompt, llm, domain}`, with `task → domain` and loaded task data feeding `run`. `run` may also
import `domain` to pass tasks into repair and expose attempts. Nothing imports upward; no cycles.

| Package | Responsibility |
|---|---|
| `internal/domain` | Dependency-free `Task` and `Attempt` data. `Task.TestCode` is human-authored verifier input and must never enter a prompt. |
| `internal/llm` | The `LLM` interface + one concrete client for an OpenAI-compatible endpoint. Owns the HTTP call, and reads base URL + API key + model + timeout from env. Per-call timeout + one retry. The interface keeps the rest of the code independent of any specific provider — swapping providers is a base-URL/key/model change here and nowhere else. |
| `internal/prompt` | `FirstPrompt(spec, signature)`, `RepairPrompt(spec, signature, previousCode, verifierOutput)`, and `ExtractGoCode(raw)`. Pure string work, no I/O and no `domain` import. |
| `internal/repair` | The loop: `Repair`, `runTests`, and an optional synchronous attempt reporter. Owns temp-module verification (`go build`, then `go test`) and retry logic; imports `domain`, `llm`, and `prompt`. |
| `internal/task` | F6 loader for task files. It returns `domain.Task`; it does not own the shared type. |
| `internal/run` | Orchestration + state. Launches `Repair` in a goroutine, appends attempts to a `Run` as they land, answers "status of run X." In-memory. |
| `internal/server` | HTTP handlers (start a run, poll a run) + serving the embedded UI. Thin. |
| `web` | One HTML file + vanilla JS, baked into the binary. Polls the run endpoint and redraws. |

### Current F1 client behavior

`LLM_BASE_URL` is an API base URL (it may include `/v1`); the client appends
`/chat/completions`. It applies one timeout to the whole completion operation and makes one
immediate retry after a request/read error, HTTP 429, or HTTP 5xx. It currently permits
`http://` for local test/development providers, so non-local providers must use HTTPS.
Non-success responses are represented by HTTP status only; their bodies are not retained in
the client error. The retry and transport hardening work is tracked in C2.

## Data Shapes

`Task` and `Attempt` live in `internal/domain`:

```go
package domain

type Task struct {
	Name      string // identifier
	Spec      string // natural-language description of desired behaviour
	Signature string // pins the API so the human tests compile
	TestCode  string // full solution_test.go, human-written
}

type Attempt struct {
	N      int    // 1-based
	Code   string // generated solution.go
	Passed bool   // whether both verifier stages passed; false covers build/test failure or timeout
	Output string // exact failed-command output, or the stable verifier-timeout note
}
```

`Run` remains owned by `internal/run` in F7, when its JSON contract is frozen:

```go
package run

import "codex-hackathon-july2026/internal/domain"

type Run struct {
	ID       string           `json:"id"`
	Task     string           `json:"task"`
	Spec     string           `json:"spec"`
	Status   string           `json:"status"` // "running" | "passed" | "gaveup"
	Attempts []domain.Attempt `json:"attempts"`
}
```

## The Loop

1. `generate` builds a prompt from primitive fields only: spec + signature on attempt 1;
   previous candidate + exact failed verifier output later. It asks for a complete
   `package solution` source file, never a test file.
2. `runTests` writes `go.mod` + `solution.go` + `solution_test.go` into an `os.MkdirTemp`
   module, runs `go build ./...`, then runs `go test ./...` under one injected verifier timeout.
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

`Repair` validates a positive attempt cap and verifier timeout before calling the provider.
Then it follows this contract:

- Any text returned by `LLM.Complete` with a nil error is candidate source, even when it is
  empty, unfenced, or invalid Go. Extraction is conservative and never repairs it; the verifier
  decides whether it works.
- Provider transport, status, and protocol errors—including the concrete client's rejected empty
  or malformed response—are infrastructure errors and stop the loop.
- A non-zero `go build` or `go test` exit creates a failed attempt with the verbatim combined
  output from the command that failed. A build failure skips `go test`. A successful attempt has
  empty feedback.
- If the derived verifier timeout expires while the caller context remains live, return a failed
  attempt with one stable timeout note and no infrastructure error. If the caller context is
  cancelled or reaches its deadline, return `ctx.Err()` instead.
- Temp-directory, file-write, cleanup, and command-launch failures are infrastructure errors;
  cleanup errors must not be silently discarded. A synchronous attempt-reporter error is a caller
  error and stops the loop after the attempt it received. An exhausted positive attempt budget
  returns the final failed attempt with a nil error.

## Deployment

- **Target**: run the binary anywhere with the Go toolchain available (the loop shells out to
  `go build` and `go test`, so `go` must be on PATH on the host).
- **Build output**: a single static binary via `go build -o repair ./cmd/repair`. The UI is
  embedded, so there is no separate frontend to deploy.
