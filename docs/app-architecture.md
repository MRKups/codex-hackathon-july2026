# Application Architecture

The settled architecture, stack, and patterns for **Repair Loop**. Read this before
changing anything.

## Overview

A small Go program that runs a **specify → generate → verify → repair** loop with a blind
oracle: a human writes a spec; `authored` mode uses a fixed human oracle, while later
`generated` mode uses a separate LLM context that sees only spec + signature. The Go toolchain
runs candidate and oracle together, and the first failed verifier output is fed back so the
coder tries again until the tests pass or attempts run out. The hackathon goal is a transparent
CLI loop first, then the web UI.

Big constraints, stated up front:

- **The Go toolchain is the verifier.** `go build` (the compiler) is checker #1; `go test`
  is checker #2. No separate assertion framework.
- **No sandbox.** Generated code runs locally in a throwaway temp module; a context
  **timeout** handles runaway/infinite-loop code. This is acceptable because we run our own
  benign code, not untrusted third-party code at scale.
- **The oracle is written blind to the code**, in one of two modes — `authored` (human) or
  `generated` (a separate test-writer context reading only spec + signature). Either way it is
  frozen before attempt 1 and never regenerated mid-run. See AGENTS.md.
- **When F17 lands, the coder and test-writer can run on separately configured models** so an
  ambiguous spec is less likely to be misread identically by both.
- **State is in-memory** in v1. Nothing persists across restarts (SQLite is a stretch).

### What a green run means

This distinction governs how results are reported and must not be blurred:

| Mode | Green means |
|---|---|
| `authored` | the code satisfies a human's fixed oracle — evidence of correctness |
| `generated` | two independent readings of the spec agreed — evidence of *spec convergence*, weaker |

A `generated` run that gives up with the same assertion failing on every attempt can be a useful
diagnostic: the coder and test-writer may have resolved the spec differently. It is not a proof
of ambiguity; oracle quality, coder capability, and repair feedback still need inspection.

## Implementation Status

As of 2026-07-18, F1 and C3 are implemented. The repository contains the Go module,
`internal/llm`, and the dependency-free shared types in `internal/domain`. The client has an
OpenAI-compatible implementation, environment-backed configuration, and local tests; its
configured-provider smoke test returned non-empty text on 2026-07-18.

`internal/prompt` now provides the pure F2 coder prompt builders and conservative source
extraction; `TestPrompt` remains F15 work. `internal/repair` now provides the F3 authored-oracle
verifier: it writes a disposable module and runs build before test under one timeout. The repair
loop itself, `task`, `run`, `server`, `web`, and `cmd/repair` do not exist yet. F4 is next.

The two-oracle-mode design (`authored` / `generated`) was adopted on 2026-07-18, after F1/C3
and before F2. `internal/domain` predates it and still needs the `OracleMode` field added —
tracked as F15. Phase 0 (F2–F4) is deliberately built `authored`-only; the test-writer arrives
in Phase 1 so the coder loop is proven against a known-good fixture first.

## Technology Stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| HTTP router (F8) | Go 1.22+ `net/http` routing; `chi` only if a concrete need justifies it |
| Frontend | single HTML file + vanilla JS, embedded via `//go:embed` |
| LLM provider | OpenAI-compatible chat completions endpoint (set by base URL + key + model + timeout) |
| Model roles (F17) | planned two `llm.LLM` values: coder (`LLM_MODEL_CODER`) and test-writer (`LLM_MODEL_TESTER`), both falling back to `LLM_MODEL` |
| Verifier | `go build` + `go test` in a temp module |
| Oracle (F15) | `authored` (human test file) or `generated` (test-writer context, spec + signature only), frozen before attempt 1 |
| Run state | in-memory `map[string]*Run` + mutex (SQLite via `modernc.org/sqlite` as a stretch) |
| Concurrency | one goroutine per run; status exposed for polling |
| Isolation | one injected verifier timeout across `go build` and `go test` — no sandbox |
| Build / deploy | `go build` → single static binary |

## Package Architecture

Each package has one job. `internal/domain` is the bottom leaf: it imports nothing and owns
shared `Task`, `Attempt`, and `OracleMode` values. Dependency direction is `web → server → run → repair →
{prompt, llm, domain}`, with `task → domain` and loaded task data feeding `run`. `run` may also
import `domain` to pass tasks into repair and expose attempts. Nothing imports upward; no cycles.

| Package | Responsibility |
|---|---|
| `internal/domain` | Dependency-free `Task`, `Attempt`, and `OracleMode` data. `Task.TestCode` is verifier input in both modes and must never enter a coder prompt. |
| `internal/llm` | The `LLM` interface + one concrete client for an OpenAI-compatible endpoint. Owns the HTTP call, and reads base URL + API key + model + timeout from env. Per-call timeout + one retry. The interface keeps the rest of the code independent of any specific provider — swapping providers is a base-URL/key/model change here and nowhere else. Role separation (coder vs test-writer) is the caller's job: two values of the same type, different model names. This package stays role-agnostic. |
| `internal/prompt` | `FirstPrompt(spec, signature)`, `RepairPrompt(spec, signature, previousCode, verifierOutput)`, `TestPrompt(spec, signature)`, and `ExtractGoCode(raw)`. Pure string work, no I/O and no `domain` import. The parameter lists enforce the core principle: no coder prompt can receive test source, and `TestPrompt` cannot receive candidate code. |
| `internal/repair` | The loop: `Repair`, `resolveOracle`, `runTests`, and an optional synchronous attempt reporter. Owns oracle resolution + preflight, temp-module verification (`go build`, then `go test`), and retry logic; imports `domain`, `llm`, and `prompt`. Takes two `llm.LLM` values; calls the test-writer at most once, before the attempt loop. |
| `internal/task` | F6 loader for task files. It returns `domain.Task`; it does not own the shared type. A task directory without `solution_test.go` loads as `OracleGenerated`; one with it loads as `OracleAuthored`. |
| `internal/run` | Orchestration + state. Launches `Repair` in a goroutine, appends attempts to a `Run` as they land, answers "status of run X." In-memory. |
| `internal/server` | HTTP handlers (start a run, poll a run) + serving the embedded UI. Thin. |
| `web` | One HTML file + vanilla JS, baked into the binary. Polls the run endpoint and redraws. |

### Provider configuration

| Variable | Meaning |
|---|---|
| `LLM_BASE_URL` | API base URL (may include `/v1`); the client appends `/chat/completions` |
| `LLM_API_KEY` | key for that endpoint |
| `LLM_MODEL` | default model, used for any role without its own override |
| `LLM_MODEL_CODER` | model that writes solutions; falls back to `LLM_MODEL` |
| `LLM_MODEL_TESTER` | model that writes oracles; falls back to `LLM_MODEL` |
| `LLM_TIMEOUT` | per-call timeout |

Setting the two role models to *different* values is the decorrelation mitigation described in
the design doc. Setting them the same is legal and useful as a baseline — it makes correlated
misreading more likely, which is itself worth measuring.

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

type OracleMode string

const (
	OracleAuthored  OracleMode = "authored"
	OracleGenerated OracleMode = "generated"
)

type Task struct {
	Name      string     // identifier
	Spec      string     // natural-language description of desired behaviour
	Signature string     // pins the API so the oracle compiles
	Oracle    OracleMode // who writes TestCode
	TestCode  string     // full solution_test.go. Required when OracleAuthored;
	                     // empty when OracleGenerated until resolveOracle fills it once.
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
	ID          string           `json:"id"`
	Task        string           `json:"task"`
	Spec        string           `json:"spec"`
	Oracle      string           `json:"oracle"`      // "authored" | "generated"
	TestCode    string           `json:"testCode"`    // the frozen oracle, for display
	Status      string           `json:"status"`      // "running" | "passed" | "gaveup" | "oraclefailed"
	FailureMode string           `json:"failureMode"` // "" | "varied" | "persistent"
	Attempts    []domain.Attempt `json:"attempts"`
}
```

## The Loop

0. `resolveOracle` fixes the tests **once, before attempt 1**. `authored` → validate
   `Task.TestCode` is present. `generated` → call the test-writer with `TestPrompt(spec,
   signature)`, extract, preflight (below), and store. Nothing after this step may change it.
1. `generate` builds a coder prompt from primitive fields only: spec + signature on attempt 1;
   previous candidate + exact failed verifier output later. It asks for a complete
   `package solution` source file, never a test file, and has no parameter through which the
   oracle could reach it.
2. `runTests` writes `go.mod` + `solution.go` + `solution_test.go` into an `os.MkdirTemp`
   module, runs `go build ./...`, then runs `go test ./...` under one injected verifier timeout.
3. Pass → return. Fail → record the attempt, feed its output into the next `generate`.
4. Stop at `maxAttempts` and classify the failure as `varied` or `persistent`.

### Oracle preflight and fault attribution

A generated oracle may not compile, and that must never be charged to the coder. Attribute
build failures by error position: if every error is inside `solution_test.go`, it is an oracle
fault — regenerate the oracle, up to an injected oracle cap, and restart the run. If any error
is in `solution.go`, it is an ordinary failed attempt. Exhausting the oracle cap ends the run
`oraclefailed`, which is neither a pass nor a coder failure.

F16 (stretch) moves this earlier: synthesize a signature-derived stub with `go/parser`, compile
the oracle against it before the first coder call, and catch an unusable oracle without
spending a generation. The stub is built, never executed.

### Failure mode classification

On `gaveup`, intersect the set of failing test names across all attempts. A non-empty
intersection where the coder's approach visibly changed is `persistent` — a diagnostic signal
that the two readers may have landed on different meanings. Otherwise `varied` — ordinary
difficulty. This is an optional comparison aid after the core dual-mode flow works, not a
prerequisite for the hackathon demo.

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
- Oracle resolution failures are their own class. A missing `TestCode` in `authored` mode is a
  caller error. In `generated` mode, an empty extraction or a test file that fails preflight is
  retried up to the oracle cap; exhausting it returns an `oraclefailed` error before any coder
  call is made. An oracle fault is never reported as a failed attempt.

## Deployment

- **Target**: run the binary anywhere with the Go toolchain available (the loop shells out to
  `go build` and `go test`, so `go` must be on PATH on the host).
- **Build output**: a single static binary via `go build -o repair ./cmd/repair`. The UI is
  embedded, so there is no separate frontend to deploy.
