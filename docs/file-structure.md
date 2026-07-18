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
├── _archive/
│   └── web/
│       └── embed.go             # Superseded standalone-asset embedding sketch
├── go.mod                        # Module codex-hackathon-july2026 + Go 1.26
├── cmd/
│   └── repair/                    # Fixed authored demo: terminal or -serve [F4/F8]
│       ├── main.go
│       └── main_test.go
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
│   ├── repair/                   # Verifier [F3] + coder-only repair loop [F4]
│       ├── repair.go
│       └── repair_test.go
│   ├── run/                      # Asynchronous in-memory run snapshots [F7]
│   │   ├── run.go
│   │   └── run_test.go
│   └── server/                   # HTTP API + embedded plain browser UI [F8/F9]
│       ├── assets.go
│       ├── index.html
│       ├── server.go
│       └── server_test.go
└── docs/
    ├── go-repair-loop.md
    ├── app-architecture.md
    ├── design-change-2026-07-18.md # Adopted blind-oracle direction [F15+]
    ├── file-structure.md
    └── tracker.md
```

F1 is complete: a configured provider returned one real, non-empty completion on 2026-07-18.
`.env` is a local, Git-ignored copy of `.env.example`; source it to provide provider variables
to the live smoke test. C3, F2, F3, and F4 provide the authored-oracle loop. F7 stores live
snapshots, F8 serves the small local API, and F9 embeds the comparison-first browser page. The
fixed split-cents oracle currently lives in `cmd/repair/main.go`; task folders and generated
oracles remain future work.

## Target Structure

The following layout is the intended end state. Paths marked F15 onward remain planned.

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
│   └── server/                   # HTTP + embedded plain browser UI                         [F8/F9]
│       ├── assets.go             #   //go:embed index.html
│       ├── index.html            #   single-file UI (vanilla JS, polls /run/{id})
│       └── server.go
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
    code. They are **not** part of `go test ./...`. Today the fixed authored oracle is embedded
    in `cmd/repair/main.go`. Once F6 lands, authored task files will live at
    `tasks/*/solution_test.go`; generated ones will exist only in memory for the life of a run.
    Neither is ever written by the coder.
- **Task auto-discovery (F6 planned):** each subdirectory of `tasks/` will be one task. A
  `spec.md` will be required and pin the signature. A `solution_test.go` will be optional, with
  its presence selecting `OracleAuthored`; its absence selecting `OracleGenerated`.
- **The oracle is frozen per run.** Generated test files are never written back into `tasks/`.
  Promoting a generated oracle to an authored one is a deliberate human act — read it, decide it
  is right, commit it — never something the loop does on its own.
- **Temp modules:** the loop writes generated code into `os.MkdirTemp` modules and removes
  them after each run. They never live in the repo.
- **No path aliases:** Go uses the module path (`codex-hackathon-july2026/internal/...`). `internal/` means
  those packages can't be imported outside this module — intentional.
- **Generated artifacts:** the built binary and any scratch dirs are git-ignored.
- **Archiving:** superseded code moves to `_archive/`, not the trash.
