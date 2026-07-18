# Repair Loop — self-correcting Go code generation

A generate → test → repair loop in Go. An LLM writes a Go function to a spec; the Go
toolchain checks it; on failure the compiler/test output is fed back and the model tries
again, until the tests pass or attempts run out. A live web UI shows each attempt going
red → green.

Built on **Go 1.26** (stdlib-first), single static binary, UI embedded. No npm, no build
step, no sandbox.

> Working name — rename freely.

### Core modules

- **The loop** (`internal/repair`) — generate → `go test` → feed failure back → retry.
- **Tasks** (`tasks/`) — a spec plus **human-authored** tests that act as the oracle.
- **Web UI** (`web/`) — polls run status and renders attempts converging on green.

See `docs/go-repair-loop.md` for the full design and `docs/tracker.md` for the build plan.

## Tech Stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| HTTP | `chi` |
| Frontend | single embedded HTML + vanilla JS (`//go:embed`) |
| LLM provider | OpenAI-compatible chat completions endpoint |
| Verifier | `go build` + `go test` in a temp module |
| State | in-memory (SQLite optional, later) |

## Prerequisites

- **Go 1.26+** (must be on `PATH` — the loop shells out to `go test`)
- An **OpenAI-compatible chat completions endpoint** — base URL, API key, and model name,
  provided via environment variables (any provider that speaks the format works)

## How to Run

```bash
export LLM_BASE_URL=...              # any OpenAI-compatible endpoint
export LLM_API_KEY=...               # key for that endpoint
export LLM_MODEL=...                 # model name to request

go run ./cmd/repair                  # CLI: run the loop on a task, print each attempt
go run ./cmd/repair -serve           # start the web server (default :8080)

go build -o repair ./cmd/repair      # produce a single static binary
./repair -serve                      #   run it anywhere with `go` on PATH

go test ./...                        # the project's OWN tests (not the task oracles)
gofmt -l . && go vet ./...           # formatting + vet must be clean
```

## Deploy

It's a single binary with the UI embedded — copy it to a host that has the Go toolchain on
`PATH` and run `./repair -serve`. No separate frontend, no hosting service required.
