# Agent House Rules & Non-Negotiables

Guardrails for the **Repair Loop** project — a Go generate → test → repair loop.
This file is read automatically by Codex. It is the guardrail; implementation detail
lives in code. **If a principle here is violated, the work must be rewritten.**

Keep this file short. A bloated rules file gets partially ignored.

## Core Principles

- **Go stdlib first.** The standard library is the foundation. Allowed third-party deps:
  `chi` (the HTTP router) and `modernc.org/sqlite` (only if persistence is added). **No**
  web framework, ORM, DI container, or other heavy dependency. Adding any other dependency
  requires a note in `docs/tracker.md` explaining why.

- **The code-writer never writes the tests. This is the point of the project.** For any
  task the loop solves, the tests are human-authored and fixed *before* generation, and
  they are fed to the loop as a given — never generated in the same context as the code.
  If code and its checking tests ever come from one model context, the guarantee is void
  and the work must be redone.

- **Respect the dependency direction.** `web → server → run → repair → { prompt, llm }`,
  with `task` feeding `run`. No package imports "upward" and no import cycles. Each
  package has one job (see `docs/app-architecture.md`). If you need something from an
  upper layer, the design is wrong — stop and flag it.

- **Errors are values.** Expected failures travel on the return (`(T, error)`) and must be
  handled. `panic` is only for genuinely impossible states ("this can't happen"), never for
  control flow and never to signal an expected failure. No silently-swallowed `nil`.

- **LLM output is untrusted text.** Extract the code from a model reply; validate it; let
  the compiler reject junk. Never `eval`, never trust formatting, never "repair" a reply by
  string-hacking it into shape — just extract and re-run the loop.

- **`prompt/` stays pure.** No I/O in the prompt package — it turns data into strings and
  back. Loop quality lives here, so it must stay trivially testable in isolation.

- **Config from env/flags, never hardcoded.** API key, base URL, model name, port, attempt
  cap, and timeout are all injected. Never commit a secret; never hardcode an endpoint or
  model string in a package.

- **Formatting is canonical.** `gofmt` and `go vet` must be clean before anything is
  "done." Canonical formatting keeps generated diffs small and reviewable.

- **Archive, don't delete.** Superseded code moves to `_archive/` (excluded from build), not
  the trash, so it stays available as reference.

- **The docs are the source of truth.** `docs/go-repair-loop.md` is the design;
  `docs/tracker.md` is the work list. Decisions are made there first, then implemented. If
  the plan and the code conflict, the plan wins — update the code.

## Agent Behavioral Rules

- **Read before coding.** Consult `docs/go-repair-loop.md` (design), `docs/app-architecture.md`
  (settled patterns), and `docs/file-structure.md` (where things go) before modifying
  packages, the loop, or the HTTP layer. Follow existing conventions instead of inventing new
  ones.

- **Build in phase order.** Work the phases in `docs/tracker.md` in order. **Do not build the
  web server or UI before the terminal loop is green (Phase 0).** A working loop with no UI
  beats a UI over a broken loop.

- **Verify before claiming done.** Run `go build ./...` and `go test ./...` (the project's own
  tests) and confirm they pass. Record how you verified on the tracker item.

- **Small diffs, one item at a time.** Take one tracker ID per change. Keep the diff scoped to
  it.
