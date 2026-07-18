# File Structure

The layout of the **Repair Loop** project. Keep this in sync with the real tree — when you
add, move, or delete a significant file, update it here and note the tracker ID (e.g. F3)
that owns it.

## Current Structure

```text
.
├── .gitattributes
├── .env.example                  # Tracked provider-variable template [F1]
├── .gitignore
├── AGENTS.md
├── README.md
├── go.mod                        # Module codex-hackathon-july2026 + Go 1.26
├── internal/
│   ├── domain/                   # Shared Task and Attempt values; imports nothing [C3]
│   │   └── domain.go
│   ├── llm/                      # Provider boundary + OpenAI-compatible client [F1]
│   │   ├── llm.go
│   │   ├── client.go
│   │   ├── client_test.go
│   │   └── live_test.go             # `integration`-tagged configured-provider smoke test
│   ├── prompt/                   # Pure coder prompts + conservative extraction [F2]
│   │   ├── prompt.go
│   │   └── prompt_test.go
│   └── repair/                   # Authored-oracle temp-module verifier [F3]
│       ├── repair.go
│       └── repair_test.go
└── docs/
    ├── go-repair-loop.md
    ├── app-architecture.md
    ├── design-change-2026-07-18.md # Adopted blind-oracle direction [F15+]
    ├── file-structure.md
    └── tracker.md
```

F1 is complete: a configured provider returned one real, non-empty completion on 2026-07-18.
`.env` is a local, Git-ignored copy of `.env.example`; source it to provide provider variables
to the live smoke test. C3 is complete: `internal/domain` owns the shared data without creating
an upward import path. F2 is complete: `internal/prompt` is dependency-free and has no path for
oracle source to enter a coder prompt. F3 is complete: `internal/repair` writes candidate and
authored test source verbatim to a disposable module, then builds and tests it. F4 is next;
every other target path remains planned.

## Target Structure

The following layout is the intended end state. Paths marked F2 onward are not created
until their tracker item starts.

```
.
├── README.md                     # Overview, stack, run/build commands
├── AGENTS.md                     # Agent guardrails & non-negotiables (Codex reads this)
├── .env.example                  # Provider-variable template; copy to ignored .env [F1]
├── go.mod                        # Module (codex-hackathon-july2026) + Go 1.26
├── go.sum                        # Dependency checksums, once a dependency is added
├── Taskfile.yml                  # Optional: go run/build/test shortcuts
├── .gitignore                    # Standard Go ignores (binary, /tmp, etc.)
│
├── cmd/
│   └── repair/
│       └── main.go               # Entry point: parse flags, wire deps, run CLI or -serve  [F4/F8]
│
├── internal/
│   ├── domain/                   # Shared Task + Attempt types; dependency-free             [C3]
│   │   └── domain.go
│   ├── llm/                      # Provider interface + OpenAI-compatible client            [F1]
│   │   ├── llm.go                #   LLM interface
│   │   ├── client.go             #   concrete client (base URL/key/model/timeout from env)
│   │   ├── client_test.go        #   local OpenAI-compatible client tests
│   │   └── live_test.go          #   `integration`-tagged provider smoke test
│   ├── prompt/                   # FirstPrompt / RepairPrompt / ExtractGoCode — PURE        [F2]
│   │   └── prompt.go             #   + TestPrompt (spec + signature only)                  [F15]
│   ├── repair/                   # Verifier [F3] + Repair loop [F4] (build, then test)
│   │   ├── repair.go
│   │   └── oracle.go             #   resolveOracle + preflight/fault attribution           [F15]
│   ├── task/                     # Task loading only; returns domain.Task                  [F6]
│   │   └── task.go
│   ├── run/                      # Orchestration + in-memory Run store (goroutine per run)  [F7]
│   │   └── run.go
│   └── server/                   # HTTP handlers: POST /run, GET /run/{id}                  [F8]
│       └── server.go
│
├── web/                          # Frontend — embedded into the binary                     [F9]
│   ├── embed.go                  #   //go:embed index.html
│   └── index.html                #   single-file UI (vanilla JS, polls /run/{id})
│
├── tasks/                        # Task definitions: spec + optional authored oracle        [F6]
│   ├── example-generated/
│   │   └── spec.md               #   spec + signature only → OracleGenerated
│   └── example-authored/
│       ├── spec.md               #   spec + signature
│       └── solution_test.go      #   authored oracle → OracleAuthored (control condition)
│
├── docs/                         # Project documentation
│   ├── go-repair-loop.md         # The design doc (source of truth)
│   ├── app-architecture.md       # Settled architecture & stack
│   ├── file-structure.md         # This document
│   ├── design-change-2026-07-18.md  # Why the oracle rule changed; read before F2+
│   └── tracker.md                # Feature / concern / bug tracker
│
└── _archive/                     # Superseded code, excluded from build (see AGENTS.md)
```

## Conventions

- **Two kinds of tests, kept apart.**
  - *Project tests* (`internal/**/*_test.go`) test our own code — run with `go test ./...`.
  - *Task tests* are the oracles the loop copies into a temp module and runs against candidate
    code. They are **not** part of `go test ./...`. In `authored` mode they live at
    `tasks/*/solution_test.go`; in `generated` mode they exist only in memory for the life of
    the run and are returned on the `Run` for display. Neither is ever written by the coder.
- **Task auto-discovery:** each subdirectory of `tasks/` is one task. A `spec.md` is required
  and pins the signature. A `solution_test.go` is **optional**, and its presence is what selects
  the mode: present → `OracleAuthored`, absent → `OracleGenerated`. Dropping in a new folder is
  enough — no registration step.
- **The oracle is frozen per run.** Generated test files are never written back into `tasks/`.
  Promoting a generated oracle to an authored one is a deliberate human act — read it, decide it
  is right, commit it — never something the loop does on its own.
- **Temp modules:** the loop writes generated code into `os.MkdirTemp` modules and removes
  them after each run. They never live in the repo.
- **No path aliases:** Go uses the module path (`codex-hackathon-july2026/internal/...`). `internal/` means
  those packages can't be imported outside this module — intentional.
- **Generated artifacts:** the built binary and any scratch dirs are git-ignored.
- **Archiving:** superseded code moves to `_archive/`, not the trash.
