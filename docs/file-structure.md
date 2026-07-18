# File Structure

Keep this document aligned with the real tree. Significant new packages or moved files belong
here and in `docs/tracker.md`.

```text
.
├── .env.example                  # Provider, role-default, and model-allowlist template
├── .gitignore
├── AGENTS.md                     # Guardrails and package direction
├── README.md                     # Setup and browser/terminal use
├── go.mod                        # Go 1.26 module; no third-party dependencies
├── cmd/
│   └── repair/
│       ├── main.go               # Composition root, flags, model roles, browser presets
│       └── main_test.go
├── internal/
│   ├── domain/
│   │   ├── domain.go             # Task, OracleMode, Attempt
│   │   ├── signature.go          # Pinned bodyless-function validation
│   │   └── signature_test.go
│   ├── llm/
│   │   ├── llm.go                # LLM completion interface
│   │   ├── client.go             # OpenAI-compatible stateless client and env config
│   │   ├── catalog.go            # Safe configured model allowlist / client resolver
│   │   ├── client_test.go
│   │   ├── catalog_test.go
│   │   └── live_test.go          # Explicit `integration`-tagged provider smoke test
│   ├── prompt/
│   │   ├── prompt.go             # FirstPrompt, RepairPrompt, TestPrompt, ExtractGoCode
│   │   └── prompt_test.go
│   ├── repair/
│   │   ├── repair.go             # Oracle resolve/preflight + generate/verify/repair loop
│   │   └── repair_test.go
│   ├── run/
│   │   ├── run.go                # In-memory per-run state, phases, cancel, active-run guard
│   │   └── run_test.go
│   └── server/
│       ├── assets.go             # Embeds index.html
│       ├── index.html            # One plain HTML/CSS/JS interactive page
│       ├── server.go             # /setup, /run, /run/{id}, cancel handlers
│       └── server_test.go
├── docs/
│   ├── go-repair-loop.md         # Design and behavior contract
│   ├── app-architecture.md       # This system’s current architecture
│   ├── design-change-2026-07-18.md
│   ├── file-structure.md         # This file
│   └── tracker.md                # Completed and remaining work
└── _archive/
    └── web/embed.go              # Superseded embedding sketch; excluded from build
```

## Data boundaries

- **Project tests** are `cmd/**/*_test.go` and `internal/**/*_test.go`, run by `go test ./...`.
- **Task oracle tests** are written verbatim into disposable preflight/verifier modules. The
  authored SplitCents control oracle is a committed Go string in `cmd/repair/main.go`; generated
  oracle source exists in the in-memory run snapshot and may be included in downloaded JSON, but
  is never written back into `tasks/` or another repository task file.
- **Browser submissions** contain a name, spec, signature, and model IDs. They never contain test
  source or provider credentials.
- **Downloaded run JSON** is a final accepted-evidence snapshot, not a repository artifact,
  persistent event log, or record of rejected oracle candidates.

## Planned additions

`internal/task` plus `tasks/` remains F6 work for file-backed authored/generated task loading.
SQLite persistence, SSE, attempt diffs, and property-test oracles are still stretch work. Do not
create them as scaffolding before there is a concrete tracker item.
