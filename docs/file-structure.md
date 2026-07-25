# File Structure

Keep this document aligned with the real tree. Significant new packages or moved files belong
here and in `docs/tracker.md`. The product is Test Verifier; the stable `repair` package,
`cmd/repair` directory, and `go-repair-loop.md` path name the underlying candidate-repair
mechanism and are not a second product name.

```text
.
├── .env.example                  # Provider, role-default, and model-allowlist template
├── .gitignore
├── AGENTS.md                     # Guardrails and package direction
├── README.md                     # Setup and browser/terminal use
├── go.mod                        # Go 1.26 module; pinned official OpenAI Go SDK
├── go.sum
├── cmd/
│   └── repair/
│       ├── main.go               # Composition root, flags, model roles, template-root wiring
│       └── main_test.go
├── challenges/
│   └── pi-prime-chunks.md         # Standalone challenge specification; not runtime verifier code
├── internal/
│   ├── domain/
│   │   ├── domain.go             # Task, OracleMode, VerificationBundle values, Attempt
│   │   ├── signature.go          # Pinned bodyless-function validation
│   │   └── signature_test.go
│   ├── draft/
│   │   ├── draft.go              # Human-confirmed signature proposal boundary
│   │   └── draft_test.go
│   ├── oracle/
│   │   ├── oracle.go             # Blind source resolver, typed progress, generic evidence
│   │   ├── preflight.go          # Signature stub + structural source admission
│   │   ├── review.go             # Strict bounded reviewer-result parsing and generic findings
│   │   ├── rulebook.go           # Checked-in universal non-executable oracle guidance
│   │   └── oracle_test.go
│   ├── llm/
│   │   ├── llm.go                # LLM completion interface + provider factory boundary
│   │   ├── config.go             # Provider-neutral runtime configuration
│   │   ├── errors.go             # Safe normalized provider errors
│   │   ├── catalog.go            # Safe configured model allowlist / client resolver
│   │   ├── config_test.go
│   │   ├── catalog_test.go
│   │   └── openai/
│   │       ├── client.go         # Official SDK Chat Completions adapter
│   │       ├── client_test.go
│   │       └── live_test.go      # Explicit `integration`-tagged provider smoke test
│   ├── template/
│   │   ├── template.go           # Source-free task-template repository
│   │   └── template_test.go      # Storage, validation, and safety tests
│   ├── prompt/
│   │   ├── prompt.go             # FirstPrompt, RepairPrompt, TestPrompt, ExtractGoCode
│   │   └── prompt_test.go
│   ├── verification/
│   │   ├── verification.go       # Bundle sealing, validation, and canonical digests
│   │   └── verification_test.go
│   ├── repair/
│   │   ├── repair.go             # Executor seam + candidate generation/retry against a sealed bundle
│   │   ├── verifier.go           # Source-free compiled-binary verifier, sentinel, output cap
│   │   └── repair_test.go
│   ├── run/
│   │   ├── run.go                # In-memory per-run state, phases, cancel, active-run guard
│   │   └── run_test.go
│   └── server/
│       ├── assets.go             # Embeds and serves explicit static routes/assets
│       ├── templates.html        # Template library page
│       ├── template.html         # New/edit template authoring page
│       ├── runs.html             # Template launch and current-process runs page
│       ├── run.html              # One immutable run-analysis page
│       ├── styles.css            # Shared spartan browser styling
│       ├── index.html            # Superseded one-screen page retained as history
│       ├── server.go             # /api template/run handlers and page routes
│       └── server_test.go
├── docs/
│   ├── go-repair-loop.md         # Test Verifier system design and behavior contract
│   ├── app-architecture.md       # This system’s current architecture
│   ├── design-change-2026-07-18.md
│   ├── file-structure.md         # This file
│   └── tracker.md                # Completed and remaining work
└── _archive/
    ├── llm/
    │   ├── client.go             # Superseded handwritten provider transport
    │   ├── client_test.go
    │   └── live_test.go
    ├── mincoins-profile-v1/
    │   ├── minimum-coin-change.md
    │   ├── profile.go
    │   └── profile_test.go
    ├── rulebook-v1/
    │   ├── rulebook.go           # Superseded one-off F20 compiler; retained as history
    │   ├── mincoins.go
    │   └── rulebook_test.go
    └── web/embed.go              # Superseded embedding sketch; excluded from build
```

## Data boundaries

- **Project tests** are `cmd/**/*_test.go` and `internal/**/*_test.go`, run by `go test ./...`.
- **Task oracle tests** are written verbatim into disposable preflight/verifier modules. Candidate
  and oracle source are removed before the compiled test binary executes in a separate directory;
  the generic completion harness adds no task semantics. The
  authored SplitCents control oracle is a committed Go string in `cmd/repair/main.go`; generated
  source and compiled verification-bundle source exist in the in-memory run snapshot and may be
  included in downloaded JSON, but are never written back into `templates/` or another repository
  task file.
- **Browser authoring submissions** contain only a template ID, name, spec, and signature;
  browser launch submissions contain a server-loaded template ID plus model IDs. Neither contains
  test source, a bundle, or provider credentials. Every browser launch follows the blind generated
  source path.
- **Downloaded run JSON** is a final accepted-evidence snapshot, not a repository artifact,
  persistent event log, or record of rejected oracle candidates.

## Planned additions

`internal/oracle/` and the candidate-side `repair.Executor` are explicitly wired at `cmd/repair`,
not a plugin registry, task-profile directory, or generic workflow framework. F25's bounded
reviewer/revision pass lives inside `internal/oracle`. `internal/template/` owns a project-root
`templates/` directory for source-free task-template JSON only; it is not an authored-oracle
loader and does not store generated test source. The browser uses explicit embedded authoring,
launch, and run-analysis documents plus one shared stylesheet. SQLite persistence, SSE, attempt
diffs, and stronger-oracle research
are still stretch work. Do not create them as scaffolding before there is a concrete tracker item.
