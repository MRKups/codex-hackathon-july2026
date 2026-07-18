# Application Architecture

The settled architecture, stack, and patterns for **Repair Loop**. Read this before
changing anything.

## Overview

A small Go program that runs a **specify → generate → verify → repair** loop with a blind
oracle: a human writes a spec; `authored` mode uses a fixed human oracle, while later
`generated` mode uses a separate LLM context that sees only spec + signature. The Go toolchain
runs candidate and oracle together, and the first failed verifier output is fed back so the
coder tries again until the tests pass or attempts run out. The hackathon goal is a transparent
end-to-end loop with a browser view that exposes the real evidence rather than a decorative
dashboard.

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

As of 2026-07-18, F1, C3, F2, F3, F4, F7, F8, and F9 are implemented. The repository contains
the Go module, an OpenAI-compatible `internal/llm` client, and the dependency-free shared types
in `internal/domain`. The configured-provider smoke test returned non-empty text on 2026-07-18.

`internal/prompt` provides the pure F2 coder prompt builders and conservative source extraction;
`TestPrompt` remains F15 work. `internal/repair` provides the F3 authored-oracle verifier and the
F4 coder-only `Repair` loop. `internal/run` owns asynchronous in-memory snapshots; `internal/server`
exposes the small polling API and embeds the single-file browser UI. `cmd/repair` runs one
hardcoded split-cents task in the terminal or serves it through `-serve`, distinguishing pass,
exhaustion, and provider/verifier failure.

The two-oracle-mode design (`authored` / `generated`) was adopted on 2026-07-18, after F1/C3
and before F2. `internal/domain` predates it and still needs the `OracleMode` field added —
tracked as F15. Phase 0 (F2–F4) is deliberately built `authored`-only; the test-writer arrives
in Phase 1 so the coder loop is proven against a known-good fixture first.

## Technology Stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| HTTP router | Go 1.22+ `net/http` routing; `chi` only if a concrete need justifies it |
| Frontend | one HTML file + vanilla JS, embedded by `internal/server` via `//go:embed` |
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
shared `Task` and `Attempt` values; F15 adds `OracleMode`. The Go dependency direction is
`server → run → repair → {prompt, llm, domain}`, with `task → domain` and loaded task data feeding
`run`. The browser page is an embedded server asset, not a Go package; its only relationship is
HTTP polling of `server`. `run` may also import `domain` to pass tasks into repair and expose
attempts. Nothing imports upward; no cycles.

| Package | Responsibility |
|---|---|
| `internal/domain` | Dependency-free `Task` and `Attempt` data. In F4, `Task.TestCode` is the fixed authored verifier input and must never enter a coder prompt. F15 adds `OracleMode`. |
| `internal/llm` | The `LLM` interface + one concrete client for an OpenAI-compatible endpoint. Owns the HTTP call, and reads base URL + API key + model + timeout from env. Per-call timeout + one retry. F4 composes one coder; F15/F17 later compose separate role values. This package stays role-agnostic. |
| `internal/prompt` | F4 provides `FirstPrompt(spec, signature)`, `RepairPrompt(spec, signature, previousCode, verifierOutput)`, and `ExtractGoCode(raw)`. Pure string work, no I/O and no `domain` import. F15 adds `TestPrompt(spec, signature)` with no candidate-code parameter. |
| `internal/repair` | F3 verifier + F4 coder-only loop: `Repair`, `generate`, `runTests`, and an optional synchronous attempt reporter. It owns temp-module verification (`go build`, then `go test`) and retry logic; imports `domain`, `llm`, and `prompt`. F15 adds oracle resolution and test-writer use. |
| `internal/task` | F6 loader for task files. It returns `domain.Task`; it does not own the shared type. A task directory without `solution_test.go` loads as `OracleGenerated`; one with it loads as `OracleAuthored`. |
| `internal/run` | Orchestration + state. Launches `Repair` in a goroutine, appends attempts to a `Run` as they land, answers "status of run X." In-memory. |
| `internal/server` | HTTP handlers (start a run, poll a run) plus the embedded `index.html` browser asset. Thin: it injects the fixed demo task but does not contain repair logic. |

### Provider configuration

| Variable | Meaning |
|---|---|
| `LLM_BASE_URL` | API base URL (may include `/v1`); the client appends `/chat/completions` |
| `LLM_API_KEY` | key for that endpoint |
| `LLM_MODEL` | default model, used for any role without its own override |
| `LLM_MODEL_CODER` | F17 planned override for the model that writes solutions; falls back to `LLM_MODEL` |
| `LLM_MODEL_TESTER` | F17 planned override for the model that writes oracles; falls back to `LLM_MODEL` |
| `LLM_TIMEOUT` | per-call timeout |

F17 will add the two role-model variables to `.env.example` and use them at the composition root.
Setting them to *different* values is the decorrelation mitigation described in the design doc;
using the same model remains a legal baseline.

### Current F1 client behavior

`LLM_BASE_URL` is an API base URL (it may include `/v1`); the client appends
`/chat/completions`. It applies one timeout to the whole completion operation and makes one
immediate retry after a request/read error, HTTP 429, or HTTP 5xx. It currently permits
`http://` for local test/development providers, so non-local providers must use HTTPS.
Non-success responses are represented by HTTP status only; their bodies are not retained in
the client error. The retry and transport hardening work is tracked in C2.

## Data Shapes

### Current Phase 0 types (C3/F4)

`Task` and `Attempt` live in `internal/domain`:

```go
package domain

type Task struct {
	Name      string // identifier
	Spec      string // natural-language description of desired behaviour
	Signature string // pins the API so the oracle compiles
	TestCode  string // fixed human-authored solution_test.go
}

type Attempt struct {
	N      int    `json:"n"`      // 1-based
	Code   string `json:"code"`   // generated solution.go
	Passed bool   `json:"passed"` // whether both verifier stages passed; false covers build/test failure or timeout
	Output string `json:"output"` // exact failed-command output, or the stable verifier-timeout note
}
```

F15 adds `OracleMode` and `Task.Oracle`, then resolves a generated `TestCode` before any coder
call. F7 has frozen the `Run` JSON contract below, including fields reserved for those later
features:

```go
package run

import "codex-hackathon-july2026/internal/domain"

type Run struct {
	ID          string           `json:"id"`
	Task        string           `json:"task"`
	Spec        string           `json:"spec"`
	Signature   string           `json:"signature"`
	Oracle      string           `json:"oracle"`      // "authored" | "generated"
	TestCode    string           `json:"testCode"`    // the frozen oracle, for display
	MaxAttempts int              `json:"maxAttempts"`
	Status      Status           `json:"status"`      // running | passed | gaveup | infrastructurefailed | oraclefailed
	FailureMode string           `json:"failureMode"` // "" | "varied" | "persistent"
	Error       string           `json:"error"`       // terminal infrastructure/oracle failure only
	Attempts    []domain.Attempt `json:"attempts"`
}
```

Current F7 runs set `Oracle` to `"authored"`. `oraclefailed` and `failureMode` are reserved for
the F15/F16 and F18 extensions respectively; `infrastructurefailed` is already used when the
provider or harness stops the run without a code verdict.

## The Loop

### Current Phase 0 loop (F4)

1. `Repair` validates the explicit coder, attempt cap, verifier timeout, and authored
   `Task.TestCode` before calling the provider.
2. `generate` builds a coder prompt from primitive fields only: spec + signature on attempt 1;
   previous candidate + exact failed verifier output later. It asks for a complete
   `package solution` source file, never a test file, and has no parameter through which the
   oracle could reach it.
3. `runTests` writes `go.mod` + `solution.go` + `solution_test.go` into an `os.MkdirTemp`
   module, runs `go build ./...`, then runs `go test ./...` under one injected verifier timeout.
4. Pass → return. Fail → report the completed attempt, feed its output into the next `generate`,
   or return the final failed attempt when the budget is exhausted.

### F15+ extension (planned)

F15 inserts `resolveOracle` before step 1. It validates the authored oracle or asks a separate
test-writer for a generated one from spec + signature only, then freezes the accepted result for
the rest of the run. F18 later adds optional `varied` / `persistent` classification.

### Oracle preflight and fault attribution

A generated oracle may not compile, and that must never be charged to the coder. `go build`
ignores `*_test.go`, so F16 preflights the generated `solution_test.go` with `go test -c` against
a signature-derived stub before the first coder call. That compiles the test without running it.
A preflight failure is an oracle fault; regenerate candidates up to an injected cap, then end the
run `oraclefailed` if no candidate is accepted. Use `go/parser` if needed to derive the stub
safely from the pinned signature; the stub is built, never executed.

### Failure mode classification

On `gaveup`, intersect the set of failing test names across all attempts. A non-empty
intersection where the coder's approach visibly changed is `persistent` — a diagnostic signal
that the two readers may have landed on different meanings. Otherwise `varied` — ordinary
difficulty. This is an optional comparison aid after the core dual-mode flow works, not a
prerequisite for the hackathon demo.

## HTTP Contract

```
GET  /task      → 200 fixed authored task context
POST /run       → 202 { "id": "run_abc" } // starts the injected fixed demo task immediately
GET  /run/{id}  → 200 Run                  // poll ~2x/sec; stop when Status != "running"
```

Unknown IDs return 404 JSON. API responses are `Cache-Control: no-store`. The shipped page is
comparison-first: it displays the frozen authored oracle above the exact candidate source and
raw verifier feedback, then compares a rejected candidate with a later candidate when available.
It renders untrusted source/output with DOM text nodes, never HTML.

## Persistence

**None in v1** — runs live in memory and are lost on restart. This is fine for a live demo.
SQLite (`modernc.org/sqlite`, zero-CGO) is a stretch item if runs need to survive restarts.

## Error Handling

In F4, `Repair` validates a non-nil coder, a positive attempt cap and verifier timeout, and a
non-empty authored oracle before calling the provider. Then it follows this contract:

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
- Oracle resolution failures are an F15+ class. A missing authored `TestCode` is a caller error.
  In generated mode, an empty extraction or a test file that fails preflight is retried up to the
  oracle cap; exhausting it returns an `oraclefailed` error before any coder call is made. An
  oracle fault is never reported as a failed attempt.

## Deployment

- **Target**: run the binary anywhere with the Go toolchain available (the loop shells out to
  `go build` and `go test`, so `go` must be on PATH on the host).
- **Build output**: a single static binary via `go build -o repair ./cmd/repair`. The UI is
  embedded, so there is no separate frontend to deploy.
