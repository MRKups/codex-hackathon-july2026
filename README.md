# Repair Loop — self-correcting Go code generation

A generate → test → repair loop in Go. An LLM writes a Go function to a spec; the Go
toolchain checks it; on failure the compiler/test output is fed back and the model tries
again, until the tests pass or attempts run out. The planned live web UI will show each
attempt going red → green.

The target is a **Go 1.26**, stdlib-first, single-binary application with an embedded UI.
There is no npm build step or sandbox.

> Working name — rename freely.

## Current status — Phase 0 / F1 complete

The repository currently contains only the module scaffold and the LLM boundary:

- `go.mod` declares `codex-hackathon-july2026`; there are no third-party dependencies.
- `internal/llm` provides `LLM`, an OpenAI-compatible chat-completions client, environment
  configuration, a whole-call timeout, and one immediate retry for its current retryable failures.
- Local verification is green: build, unit, race, coverage, and vet checks pass. Coverage is
  75.0% for the current LLM package.
- F1 is complete: the configured-provider smoke test returned non-empty text on 2026-07-18.
  C3 (the shared data/error contract) is the next prerequisite before F2. No prompt package,
  task, repair loop, CLI, HTTP server, or UI exists yet.

See `docs/tracker.md` for the authoritative current position and next gate.

## Target modules

- **The loop** (`internal/repair`) — generate → `go test` → feed failure back → retry.
- **Tasks** (`tasks/`) — a spec plus **human-authored** tests that act as the oracle.
- **Web UI** (`web/`) — polls run status and renders attempts converging on green.

See `docs/go-repair-loop.md` for the full design and `docs/tracker.md` for the build plan.

## Tech Stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| HTTP (F8) | Go 1.22+ `net/http` routing; add `chi` only if a concrete route need justifies it |
| Frontend | single embedded HTML + vanilla JS (`//go:embed`) |
| LLM provider | OpenAI-compatible chat completions endpoint |
| Verifier | `go build` + `go test` in a temp module |
| State | in-memory (SQLite optional, later) |

## Provider configuration (F1)

F1 expects an OpenAI-compatible API base URL, an API key, a model name, and a Go duration.
The base URL can include a version path such as `/v1`; the client adds `/chat/completions`.

```bash
export LLM_BASE_URL=...              # OpenAI-compatible API base URL, such as .../v1
export LLM_API_KEY=...               # key for that endpoint
export LLM_MODEL=...                 # model name to request
export LLM_TIMEOUT=30s               # timeout for one completion call
```

Use HTTPS for every non-local provider endpoint. The current client permits `http://` so
local test servers and local development providers work; do not send a real key to a
non-local plaintext endpoint.

For local setup, copy the tracked template and keep the populated file private:

```bash
[ -f .env ] || cp .env.example .env  # already created for the current workspace
# edit .env with your provider values
source .env
```

The live-provider smoke test is deliberately opt-in and sends one short harmless prompt. It
requires HTTPS, logs no provider response or secret, and may make up to two provider requests
because F1 retries one eligible failure. It is excluded from ordinary test runs:

```bash
LLM_LIVE_TEST=run go test -tags=integration ./internal/llm -run '^TestLiveCompletion$' -count=1 -v
```

## Current validation

```bash
go build ./...
go test ./...
go test -race ./...
go test -cover ./...
go vet ./...
gofmt -l internal/llm/*.go
```

A real completion is intentionally not run until the four `LLM_*` variables are configured
and the explicit `integration` build tag plus `LLM_LIVE_TEST=run` opt-in are supplied.

## Planned CLI and server commands

These commands become available only after their corresponding tracker milestones.

```bash
go run ./cmd/repair                  # F4: terminal repair loop
go run ./cmd/repair -serve           # F8: start the HTTP server

go build -o repair ./cmd/repair      # F8/F9: produce the future static binary
./repair -serve                      # F8/F9: run it where `go` is on PATH
```

## Planned deployment

The completed project will be a single binary with the UI embedded. Copy it to a host that
has the Go toolchain on `PATH` and run `./repair -serve`; no separate frontend or hosting
service will be required.
