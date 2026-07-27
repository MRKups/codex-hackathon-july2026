# Agent House Rules & Non-Negotiables

Guardrails for the **Test Verifier** project — a Go verification platform with a bounded
candidate-repair loop.
This file is read automatically by Codex. It is the guardrail; implementation detail
lives in code. **If a principle here is violated, the work must be rewritten.**

Keep this file short. A bloated rules file gets partially ignored.

## Core Principles

- **Go stdlib first.** The standard library is the foundation. Allowed third-party deps:
  `chi` (the HTTP router) and `modernc.org/sqlite` (only if persistence is added). **No**
  web framework, ORM, DI container, or other heavy dependency. Adding any other dependency
  requires a note in `docs/tracker.md` explaining why.

- **The oracle is written blind to the code. This is the point of the project.** The tests
  that judge a solution must be produced by something that has never seen that solution.
  Two legal sources: **authored** (a human-owned test source supplied by trusted local code) or
  **generated** (a separate test-writer context reads only the task-specific spec + signature,
  plus the checked-in universal Rulebook).
  Both are fixed *before* the repair loop starts and frozen for the whole run. Illegal, always:
  one context producing both code
  and tests; a test-writer that sees candidate code; regenerating the oracle mid-run to make
  a failing solution pass. If any of those happen the guarantee is void and the work must be
  redone.
  A browser request must never supply verifier source, expected values, reference logic, or an
  executable rule language. The generic platform records provenance and digests, but does not
  turn natural-language rules into executable evidence.
  The verifier executes a compiled test binary only after removing its source-bearing directory
  and uses a minimal runtime environment. This protects the ordinary source boundary; it is not
  an OS sandbox for hostile local code.

- **Build the end-to-end blind-oracle loop first.** The task spec is the shared input; in
  generated mode, disagreement can expose different readings of it. That is a useful
  diagnostic, not a verdict on the spec or either model. Never resolve a failing run by editing
  its frozen oracle; inspect the task after the run, without making research analysis a
  prerequisite for a working demo.

- **Respect the dependency direction.** `browser → server → run → { oracle, repair }`, with
  `oracle` and `repair` depending only downward on `prompt`, `llm`, `verification`, and `domain`,
  and with `task` feeding `run`. No package imports "upward" and no import cycles. Each package
  has one job (see `docs/app-architecture.md`). If you need something from an upper layer, the
  design is wrong — stop and flag it.

- **Use explicit seams, not magic orchestration.** Components exchange small typed inputs and
  outputs and are wired manually at `cmd/repair`; no service locator, reflection, dynamic plugin
  registry, task-text routing, or hidden global default. Define an interface only at a real
  replacement boundary, keep default implementations concrete, and never let a lower component
  mutate `run.Store` or reach into another component's internals.

- **Errors are values.** Expected failures travel on the return (`(T, error)`) and must be
  handled. `panic` is only for genuinely impossible states ("this can't happen"), never for
  control flow and never to signal an expected failure. No silently-swallowed `nil`.

- **LLM output is untrusted text.** Extract the code from a model reply; validate it; let
  the compiler reject junk. Never `eval`, never trust formatting, never "repair" a reply by
  string-hacking it into shape — just extract and re-run the loop.

- **`prompt/` stays pure.** No I/O in the prompt package — it turns data into strings and
  back. Loop quality lives here, so it must stay trivially testable in isolation.

- **Config from env/flags, never hardcoded.** API key, base URL, model names, port, attempt
  cap, and timeout are all injected. Never commit a secret; never hardcode an endpoint or
  model string in a package. Coder and test-writer defaults are configured separately, while
  the browser may select only from the local configured model allowlist — see
  `docs/app-architecture.md`.

- **Formatting is canonical.** `gofmt` and `go vet` must be clean before anything is
  "done." Canonical formatting keeps generated diffs small and reviewable.

- **Delete superseded code; Git is the archive.** Remove replaced code in the commit that
  replaces it, and name the superseding work in the message. Do not keep a parallel `_archive/`
  tree: a reader cannot tell live code from dead code at a glance, and Git already preserves
  every earlier version.

- **The docs are the source of truth.** `docs/go-repair-loop.md` is the design;
  `docs/tracker.md` is the work list. Decisions are made there first, then implemented. If
  the plan and the code conflict, the plan wins — update the code.

- **The tracker holds open work only.** Delete an item when it ships; do not leave a completed
  entry or a record of how it was verified. Git history says what changed. If finishing an item
  established a standing rule, write that rule into the relevant design doc and drop it from the
  tracker.

## Agent Behavioral Rules

- **Read before coding.** Consult `docs/go-repair-loop.md` (design), `docs/app-architecture.md`
  (settled patterns), and `docs/file-structure.md` (where things go) before modifying
  packages, the loop, or the HTTP layer. Follow existing conventions instead of inventing new
  ones.

- **Respect stated dependencies.** Tracker items name what they depend on; do not start one
  whose dependency is still open. A working loop with no UI beats a UI over a broken loop.

- **Verify before claiming done.** Run `go build ./...` and `go test ./...` (the project's own
  tests) and confirm they pass before saying an item is finished.

- **Small diffs, one item at a time.** Take one tracker ID per change. Keep the diff scoped to
  it.
