# Test Verifier — Implementation Tracker

*Identifier counter: the next new items are **F31, C7, B4**.
Branch strategy: `main` (active development and production during the hackathon).*

> **Product-language decision, 2026-07-24.** The product is named **Test Verifier**. “Verifier
> loop” names the bounded candidate → verifier-feedback → later-candidate mechanism, while
> `cmd/repair`, package paths, module name, and legacy document filenames remain stable. The public
> README, current design/architecture headings, browser title, binary example, and user-facing run
> language must lead with Test Verifier rather than implying that repair is the product.

> **Task-template workflow decision, 2026-07-24.** A **task template** is a persisted, editable
> authoring input: a name, specification, and confirmed Go signature. A **run** is one immutable
> execution record: its submitted task snapshot, selected models, frozen verification bundle, and
> attempts. Templates are stored under the project-root `templates/` directory and never contain
> test source, expected values, Rulebook text, an oracle-mode choice, or provider configuration.
> Selecting a template in the browser always starts the existing generated-oracle path; it does
> not turn browser input into a trusted authored oracle. Template changes cannot alter a prior run,
> whose task and frozen-bundle snapshots remain authoritative. Template persistence is separate
> from run persistence: runs remain in memory and downloaded JSON remains the portable evidence
> until a later explicit persistence feature. F6 supplies the source-free repository; F27 supplies
> the multi-route authoring, launch, and analysis browser workflow.

> **Continuation handoff, 2026-07-25.** F25 and F26 are implemented and committed as
> `78f3985`; their full verification suite passed without a provider call: `go build ./...`,
> `go test -count=1 ./...`, `go test -race ./...`, `go vet ./...`, `go mod verify`, embedded
> JavaScript syntax, and `git diff --check`. F25 adds the bounded reviewer/revision path,
> `LLM_MODEL_REVIEWER`, `reviewingoracle`, and review provenance; F26 adds `internal/draft`,
> `POST /signature-draft`, and explicit browser application of a syntax-valid draft. F6 and F27
> are now implemented in the current worktree: `internal/template` stores source-free template
> inputs, while the explicit browser routes author templates, launch template-backed generated
> runs, and analyze immutable evidence. Keep the no-test-source/no-oracle-mode/no-run-persistence
> boundary intact. The next product-validation work is **F5** (real saved-task prompt/oracle
> evaluation); **F18** remains an optional failure diagnostic. Re-read those contracts before
> coding and retain the F25–F27 changes.

> **Run-URL behavior, 2026-07-25.** Runs intentionally remain in memory. After a server restart,
> an old `/runs/{id}` URL is not corrupt evidence; it is unavailable because that process no
> longer owns the snapshot. F27's detail page must replace its loading state with an explicit
> explanation and direct the user back to `/runs`, never imply that the verifier is still loading.

> **Corrective design decision, 2026-07-21.** The first MinCoins profile was an invalid product
> shortcut: an exact browser-text match silently chose task-specific semantic code. The active
> platform must not recognize a problem family, ship a hidden profile route, or carry speculative
> profile plumbing merely because one regression needs stronger semantics. It returns to two
> generic delivery paths only: authored source from a trusted caller and blind generated source
> from the test-writer. No task-profile registry is implied. A future Oracle Rulebook may guide
> every generated run, but it must be universal, non-executable policy—not a task selector,
> expected-value store, or reference implementation. Preserve superseded work under `_archive/`;
> it is not part of the active build or runtime.

> **Modular composition decision, 2026-07-21.** The platform evolves through explicit typed
> components, manually wired at `cmd/repair`, rather than a dynamic pipeline or hidden registry.
> `oracle` owns pre-freeze source generation/preflight and will own its future review pass;
> `verification` owns only immutable bundle sealing/digests; `repair` owns candidate generation and execution against an already
> frozen bundle; `run` will own lifecycle/snapshots; and `server` will adapt HTTP/UI to those
> components. A component may report typed events to its caller but may not reach into `run`,
> `server`, or another component's mutable state. See F24–F26.

> **Design change, 2026-07-18 (after F1/C3, before F2).** The oracle is no longer
> human-authored-only. It is now written *blind to the code* in one of two modes: `authored`
> (human) or `generated` (a separate test-writer LLM context reading only task-specific spec +
> signature plus the checked-in universal Rulebook).
> `generated` is now the interactive browser showcase; `authored` remains the terminal control
> condition. New work identifiers: **F15–F18, C4, C5**. Amended: **F3, F4, F6, C1**. Phase 0
> stayed `authored`-only on purpose — prove the coder loop against a known-good fixture before
> adding a second unproven half.

> **Hackathon execution decision, 2026-07-18.** The immediate browser milestone is now one
> interactive generated-oracle vertical slice: a user supplies a spec and pinned Go signature;
> a test-writer produces and freezes the oracle before a separate code-writer starts; the page
> shows both artifacts and the repair trace. This deliberately brings together **F15**, the
> essential preflight portion of **F16**, **F17**, and the useful browser-input portion of
> **F12**. It landed as small, separately tested changes. Do not build a frontend-only
> form over the fixed task, accept client-supplied test source, or hardcode provider model IDs.
> Presets fill the same editable inputs; they never auto-run.

Status markers: `[ ]` not started · `[~]` in progress · `[x]` done.
Work the phases in order. **Phase 0 must be green before anything in Phase 3+ begins.**

## Features (F)

### Phase 0 — Core loop, terminal (critical path)

- [x] **F1 — LLM client.** `internal/llm`: the `LLM` interface + one concrete client for an
  OpenAI-compatible endpoint (`Complete(ctx, prompt) (string, error)`). Base URL, key, model,
  and timeout from env. Per-call timeout + one retry. Verify in isolation — a single completion
  returning text — before wiring anything else.
  Local verification (2026-07-18; rerun after adding the live test): `go build ./...`,
  `go test ./...`, `go test -race ./...`, `go test -cover ./...` (75.0%), `go vet ./...`,
  and `gofmt` passed. Tests include a standard-library `httptest` completion, retry, and
  timeout coverage. Non-success provider responses expose their HTTP status without retaining
  their body.
  The tracked `.env.example` and ignored local `.env` document the required provider variables
  plus optional model allowlist and role defaults. The configured-provider smoke test is opt-in:
  `LLM_LIVE_TEST=run go test -tags=integration
  ./internal/llm/openai -run '^TestLiveCompletion$' -count=1 -v`. It requires HTTPS, is excluded
  from ordinary test runs, and must pass before F1 is marked done.
  Live verification passed on 2026-07-18: the opt-in HTTPS probe returned non-empty text.
  An initial HTTP 401 was traced to and resolved by correcting the local API key; no secret is
  tracked. The completed C3 contract below unblocks F2.
  *Superseded transport note (2026-07-20): F19 archives this handwritten protocol client and
  replaces it with a pinned official SDK adapter while retaining the same repair-loop boundary.*

- [x] **F2 — Prompt + extraction.** `internal/prompt` (pure, no I/O):
  `FirstPrompt(spec, signature)`, `RepairPrompt(spec, signature, previousCode, verifierOutput)`,
  and `ExtractGoCode(raw)`. Prompts require a complete `package solution` source file with the
  specified signature, stdlib only, source only, and never tests. Repair prompts include the
  previous candidate and exact output from the first failed verifier stage. Extraction removes
  only one unambiguous complete backtick fence when it is the whole reply apart from outer
  whitespace, and never repairs the source. Verification (2026-07-18): `gofmt -l $(rg --files
  -g '*.go')`, `go build ./...`, `go test ./...`, `go test -race ./...`, `go test -cover ./...`
  (97.4% for `internal/prompt`), `go vet ./...`, and `git diff --check` passed. No live provider
  call was required.

- [x] **F3 — verifier.** `internal/repair`: write `go.mod`, model-produced `solution.go`, and
  the frozen `solution_test.go` from `domain.Task` into an `os.MkdirTemp` module. Write source
  verbatim; do not run `go mod tidy`, install dependencies, or repair it. Run `go build ./...`,
  then `go test -timeout <verifier timeout> ./...` only after a successful build, under one
  injected verifier timeout.
  A non-zero command exit is feedback with that command's raw combined output, not an infra
  error. Verifier timeout is failed-attempt feedback; caller cancellation, temp/write/cleanup,
  and command-launch failures bubble as errors. Verification (2026-07-18): passing code,
  build failure, test failure, malformed test source, verifier timeout, caller cancellation,
  missing `go`, and invalid-input tests passed. `gofmt -l $(rg --files -g '*.go')`, `go build
  ./...`, `go test ./...`, `go test -race ./...`, `go test -cover ./...` (79.5% for
  `internal/repair`), `go vet ./...`, and `git diff --check` passed. No live provider call was
  required.
  *Superseded execution note (2026-07-20): F21 retains the build stage but compiles a test binary,
  removes the source-bearing directory, and executes it from a separate minimal-environment
  directory with a completion sentinel and capped feedback.*

- [x] **F4 — Repair loop + one hardcoded task.** `internal/repair` `Repair(...)` wiring
  `llm.LLM` + `domain.Task` through generate → verify → feedback → retry, plus
  `cmd/repair/main.go` running ONE hardcoded, **authored-mode** tricky task and printing each
  attempt's number, pass/fail, and output through the attempt reporter.
  Depends on: F1, F2, F3.
  Scope note: `authored` mode only. F4 takes an explicit coder; F15 deliberately extends the
  loop with a test-writer after the generated-oracle contracts exist.
  **Milestone: `attempt 1 fail → … → attempt N pass` in the terminal. Protect this.**
  Verification (2026-07-18): a scripted LLM proves fail → exact verifier feedback → pass,
  extraction of fenced code, synchronous reporting, no oracle-source leak into coder prompts,
  exhaustion, provider failure, reporter failure, and pre-provider input validation. The fixed
  split-cents CLI oracle accepts a reference implementation. `gofmt -l $(rg --files -g '*.go')`,
  `go build ./...`, `go test ./...`, `go test -race ./...`, `go test -cover ./...` (86.8% for
  `internal/repair`), `go vet ./...`, and `git diff --check` passed. No live provider call was
  required.

### Phase 1 — Blind test-writer and interactive path

- [x] **F15 — Generated oracle (the design change).** `domain.OracleMode` + `Task.Oracle`,
  pure `prompt.TestPrompt(spec, signature)`, and the then-combined repair resolver first resolved
  and froze generated test source before attempt 1. F24 later moves that responsibility into
  `internal/oracle`; authored mode remains a supported control and permits a nil source author.
  Depends on: F4.
  Structural checks prove tester → frozen oracle → coder ordering, no test source in coder
  prompts, no candidate source in tester prompts, and no regeneration after acceptance.

- [x] **F16 — Oracle preflight + fault attribution.** Generated tests are parsed and compiled
  with `go test -c` against a parsed signature-derived stub before any coder call; the test
  binary is never run. A rejected candidate oracle retries only up to the injected
  `-oracle-attempts` cap. Exhaustion returns typed `oracle.OracleFailureError` (moved there by
  F24), which becomes terminal
  `oraclefailed`, never a candidate attempt. Preflight also rejects source without a runnable
  `Test` function, a direct call to the pinned function, or a testing failure method, as well as
  build constraints, `TestMain`, `init`, test skips, and direct `os.Exit`; it type-checks the
  pinned stub and keeps caller cancellation/setup/preflight-timeout failures as ordinary
  infrastructure errors.
  Depends on: F15.

- [x] **F17 — Split coder and test-writer models.** `LLM_MODEL_CODER` /
  `LLM_MODEL_TESTER` fall back to `LLM_MODEL`; `LLM_MODELS` is an optional comma-separated
  browser allowlist. Role selection happens at the composition root. Role-agnostic
  `llm.ModelCatalog` creates one reusable client per allowed ID and rejects arbitrary IDs.
  Depends on: F15.
  Same-model role selection remains legal, but the browser records both selected IDs so a saved
  run is interpretable.
  Verification (2026-07-18): `go build ./...`, `go test ./...`, `go test -race ./...`,
  `go test -cover ./...`, `go vet ./...`, `gofmt`, inline-JavaScript syntax, the no-HTML-
  injection static check, and `git diff --check` passed. No live provider request was made.

- [x] **F19 — Provider boundary + official OpenAI Go SDK.** Preserve `llm.LLM` as the sole
  repair-loop contract: `Complete(context.Context, string) (string, error)`. Make
  `ModelCatalog` depend only on a small SDK-free `ClientFactory` that creates one reusable
  `LLM` for a configured model ID. Put the first concrete adapter and all OpenAI SDK types in
  `internal/llm/openai`; only `cmd/repair` selects the injected `LLM_PROVIDER` factory. This
  first step supports one active provider per process, while the browser continues to receive
  only opaque local model IDs. Do not add provider selection to the browser, generic
  chat/message types, role-aware provider APIs, or provider imports to `prompt`, `repair`,
  `run`, or `server`.

  Replace the handwritten OpenAI-compatible client with the pinned official dependency
  `github.com/openai/openai-go/v3 v3.44.0`. This is the explicit AGENTS.md third-party
  dependency exception: the official typed SDK replaces handwritten provider protocol code and
  is sealed behind the existing narrow boundary; it is not a framework, ORM, or general-purpose
  abstraction library. Its direct/transitive dependencies must be recorded by `go.mod`/
  `go.sum`, and no additional provider dependency is added in this item. Archive the superseded
  raw client and its obsolete protocol tests under `_archive/`; do not delete them.

  The initial OpenAI adapter uses the SDK's **Chat Completions** surface to preserve the current
  `/chat/completions` behavior and configured compatible base URLs. A move to Responses is a
  separate behavior change: it must prove endpoint support, make response storage an explicit
  `false` policy, reject incomplete/non-text results, and never use conversation or previous-
  response state. Each completion remains a stateless, independent request even when the SDK
  transport/client is shared by the two roles.

  Preserve the current safety contract: one explicit retry owner (SDK max retries `1`, never an
  outer retry as well), a context-derived whole-call timeout, cancellation identity, non-empty
  output validation, and normalized errors that expose a safe status/category but never a
  provider response body or credential through `Run.Error`. Retain the current 1 MiB response
  bound or deliberately replace it with a documented, tested output-size policy before removing
  it. Pass the configured key/base URL explicitly rather than relying on ambient SDK defaults.
  Update `.env.example`, README, architecture, file structure, live smoke test, and C2 as part
  of the migration. Adapter tests use an injected HTTP client/factory and cover request shape,
  retry count, timeout/cancellation, output extraction, redaction, and catalog reuse; the
  paid HTTPS smoke test remains opt-in.
  Depends on: F17.
  Verification (2026-07-20): the SDK-free catalog and server use fake factories; the OpenAI
  adapter tests cover configured request shape, ambient SDK-env isolation, one retry, timeout,
  cancellation, invalid output, 1 MiB response bounds, base-URL credential rejection, and
  secret-bearing error redaction.
  `go mod verify`, `go build ./...`, `go test ./...`, `go test -race ./...`,
  `go test -cover ./...`, `go vet ./...`, `gofmt`, and `git diff --check` passed. The explicit
  HTTPS live smoke test was not run, so no provider request was made.

- [x] **F20 — Superseded rulebook experiment (archived).** A temporary third delivery path
  explored server-selected semantic code. It was not a safe general platform boundary and was
  removed from the active system by F23. Its source is retained under `_archive/` only; it is not
  built, registered, selectable, or described as a product capability.

- [x] **F21 — VerificationBundle baseline.** Retain the useful generic result of that work: a
  frozen bundle contains exact executable test source plus a schema version, one legal source
  origin (`authored` or `generated`), a task digest, and a bundle digest. The oracle resolver
  freezes it before candidate generation; every candidate executes the same source through the generic
  compiled-binary verifier. F23 deliberately removed the registry, task-family metadata, and
  any browser inference from this baseline.

  The verifier also structurally admits source against the pinned signature, compiles a test
  binary, removes its source-bearing directory, executes from a separate minimal-environment
  directory, requires a verifier-owned completion sentinel, and caps feedback before it reaches
  a coder prompt. This reduces ordinary source leakage and exit-zero bypasses; it is not an OS
  sandbox for hostile local code.

- [x] **F22 — Test Verifier documentation and product language.** Make the README the accurate
  entry point for the frozen-verification platform: explain the two authored/generated delivery
  paths, bundle freeze/digest/provenance, the bounded candidate-repair mechanism, confidence
  limits, trusted-local execution boundary, and current setup/run commands. Rename current
  public documentation headings and browser-facing labels to **Test Verifier**, while retaining
  `repair` only where it names the underlying package, command, or candidate-repair loop. The
  label was updated from Frozen Oracle on 2026-07-24; lower-case “frozen oracle” remains a
  technical description of a blind verifier fixed before candidate generation.
  Correct any claim that capped verifier feedback is “exact” or that runtime execution is direct
  `go test`. Do not rename the Go module, package paths, command directory, or historical design
  records in this scoped item.
  Verification: reviewed again as part of F23 against the current browser/runtime contract.

- [x] **F23 — Remove the MinCoins profile spike.** Removed every active MinCoins-specific path:
  the profile package, registration, exact browser-preset selection, profile tests, challenge
  fixture, profile metadata/plan/check fields, and profile-specific UI/docs. Removed the generic
  profile registry/plumbing too: without a trusted task-manifest path it was speculative
  scaffolding, not a reusable platform feature. Retain the task-agnostic
  `VerificationBundle`—exact source, one of the two legal origins, task/bundle digests—and the
  generic preflight, freeze, source-free compiled-binary verifier, run snapshot, and
  authored/generated repair paths. Archive superseded MinCoins work under `_archive/` rather
  than deleting it. The browser must construct only a generated oracle from submitted text; it
  may not infer semantic verification from a task name, signature, or matching prose.

  Depends on: F21.
  Verification (2026-07-21): an active-source audit found no task-family routing, profile
  registry, profile metadata, or MinCoins code outside `_archive/`; `taskForRunRequest` always
  constructs `OracleGenerated`, and lifecycle/API tests prove a valid browser run calls the test
  writer before candidate generation. `gofmt`, `go build ./...`, `go test -count=1 ./...`,
  `go test -race ./...`, `go vet ./...`, `go mod verify`, embedded-JavaScript syntax, and
  `git diff --check` passed. No provider request was made.

- [x] **F24 — Oracle component boundary and universal Rulebook v1.** Moved generated/authored
  source resolution, signature-derived stub construction, structural admission, source-attempt
  cap, and typed `oracle.OracleFailureError` out of `repair` into `internal/oracle`. `run.Store`
  now receives an explicit `oracle.Resolver`, resolves `Task → Resolution{Bundle, Evidence}`
  before candidate work, validates that its sealed bundle and origin match the submitted task,
  atomically snapshots its frozen bundle/evidence, and then calls the injected candidate-side
  `repair.Executor` (whose default delegates to `repair.RepairWithConfig`) with a narrow
  candidate request: spec, signature, and sealed bundle only. `repair` has no source-author,
  separately supplied raw test source, Rulebook, or review input; its sealed bundle retains the
  executable test source required by the generic verifier.

  `oracle.DefaultResolver` is manually constructed at `cmd/repair` with a checked-in generic
  Rulebook and concrete structural `Admitter`. The Rulebook has version
  `oracle-rulebook/v1` and a canonical digest; it is attached to every generated source-author
  prompt and recorded separately as generic run evidence. It guides only spec/signature-derived
  validity, boundary/error, mutation, determinism, round-trip, and metamorphic checks, and tells
  the source author to avoid guessed non-trivial answer keys and unsupported optimality claims. It is not
  executable code, browser input, a task profile, matcher, reference implementation, or DSL.

  The new seams are `oracle.Resolver`, its deterministic `oracle.Admitter`, and the candidate
  `repair.Executor`; defaults are concrete and injected manually. There is no DI framework, service locator, generic `[]Step`
  pipeline, reflection, plugin registry, browser-selected policy, or task-name/spec/signature
  dispatch. `oracle` cannot see candidate code or mutate `run.Store`; `run` maps typed oracle
  progress to lifecycle phases; `VerificationBundle` remains source/origin/task-digest/bundle-
  digest only.

  Depends on: F15, F16, F21, F23.
  Verification (2026-07-24): structural-admission and resolver tests cover blind source prompts,
  stale-source rejection, retries, typed failure, authored resolution, Rulebook digest stability,
  separation from bundle digest, and resolution provenance. Candidate tests prove frozen-bundle
  validation/reuse and no oracle material in coder prompts. Run tests cover the injected
  resolver/executor handoff, no candidate work before resolution, malformed-resolution rejection
  before snapshot/candidate work, and a late resolver result after the deadline. `gofmt -l`,
  `go build ./...`, `go test -count=1 ./...`, `go test -race ./...`, `go vet ./...`,
  `go mod verify`, `git diff --check`, and the active task-routing audit passed. No provider
  request was made.

- [x] **F25 — Bounded generic oracle review pipeline v1.** Build on F24 with one explicit,
  bounded pre-freeze sequence: oracle author → structural preflight → oracle reviewer → at most
  one author revision → structural preflight → freeze. The reviewer sees only spec, pinned
  signature, Rulebook, and proposed test source; it never sees candidate code. The candidate
  author sees none of the oracle source or review material.

  Reviewer output is untrusted, strict, size-bounded data: `accept`, `revise`, or `reject`, plus
  generic finding categories (answer-key provenance, boundary/error coverage, validity/invariant
  coverage, unsupported semantic claim) and short findings. Invalid reviewer output is a bounded
  oracle-resolution failure, never silently interpreted. Record Rulebook version/digest, actual
  author/reviewer model IDs, counts, final verdict, and finding summaries as generic run evidence
  separate from the immutable bundle manifest. The UI may say “structurally admitted” or
  “reviewed generated oracle”; it must never say correct or proven.

  Configure one reviewer model ID at the composition root, with a visible recorded fallback to the
  author model when no separate ID is configured. Do not add a browser provider picker, committee
  voting, multiple-suite merging, task-specific reviewers, generated reference solvers, or a
  generic verification DSL in this item.

  Depends on: F24, F17.
  Verification (2026-07-24): `internal/oracle` now parses strict 16 KiB reviewer JSON with
  `accept`/`revise`/`reject`, one of four generic finding categories, at most six unique bounded
  summaries, and no unknown fields or trailing values. It performs at most one review and one
  revision after structural admission; reject, invalid data, and failed revision are typed
  `oraclefailed` outcomes before candidate work. Resolution evidence records the Rulebook,
  actual author/reviewer model IDs, call counts, final verdict, and findings outside the bundle
  manifest; failure evidence contains no rejected source. `cmd/repair` configures
  `LLM_MODEL_REVIEWER`, visibly records its fallback to the test-writer model, and never exposes a
  browser reviewer picker. Run snapshots report `reviewingoracle`; candidate prompts remain
  isolated from oracle source, Rulebook text, and reviewer findings.

  Fake author/reviewer tests cover accept, revise, reject, invalid review data, structural cap
  exhaustion, reviewer cancellation, prompt isolation, final evidence, and no candidate leakage;
  run/server tests cover the reviewer role and snapshot boundary. `gofmt`, `go build ./...`,
  `go test -count=1 ./...`, `go test -race ./...`, `go vet ./...`, `go mod verify`, and
  `git diff --check` passed. No provider request was made.

- [x] **F26 — Human-confirmed Go signature drafting aid.** Add a separate task-authoring action,
  not a run or oracle mode: an allowlisted model receives only the current specification and
  proposes one bodyless top-level Go function signature. A pure prompt plus strict source/size
  handling and `domain.ValidateSignature` gate the suggestion. `POST /signature-draft` returns a
  draft only; the browser displays it as syntax-valid but semantically unverified, requires an
  explicit user “Use draft” action, and never starts a run or overwrites a changed specification.

  Reuse the selected oracle-author model initially; do not add a third browser role, provider
  behavior, task profile, or signature DSL. The final user-confirmed signature remains the normal
  shared input that is digested and frozen before oracle resolution.

  Depends on: F24.
  Verification (2026-07-24): added `internal/draft`, a stateless boundary that uses only a pure
  signature-draft prompt, the selected allowlisted test-writer model, a 2 KiB completion bound,
  conservative whole-fence extraction, and `domain.ValidateSignature`. It imports no oracle,
  repair, run, or server package and cannot create verification evidence. `POST /signature-draft`
  accepts only a bounded spec and configured test-writer model, returns only a syntax-valid draft,
  and never touches the run store. The browser tracks the source specification while a request is
  in flight, discards stale responses, and exposes an explicit **Use draft** action; it never
  overwrites the signature automatically.

  Prompt/service/handler tests cover isolation, malformed and oversized output, cancellation,
  provider failure, strict request/model validation, and no run side effect. `gofmt`,
  `go build ./...`, `go test -count=1 ./...`, `go test -race ./...`, `go vet ./...`,
  `go mod verify`, inline-JavaScript syntax, and `git diff --check` must pass without a provider
  call.

- [ ] **F18 — Failure mode signal.** On `gaveup`, intersect failing test names across attempts
  and record `persistent` (the same frozen assertion kept failing) or `varied` (ordinary
  difficulty). Do not interpret `persistent` as a specification mismatch: an internally
  inconsistent generated oracle is a counterexample.
  Surface it on the `Run` as an optional diagnostic after the dual-mode flow works; it is not a
  blocker for the hackathon demo.
  Depends on: F15, F7.

- [ ] **F5 — Tune repair prompts on real tasks.** Feed previous code + test output cleanly;
  run 3–4 different tricky tasks; tune until it converges *repeatably*, not just once.
  Depends on: F4.
  Caution: tune for a clear, honest demo first. For later comparisons, keep prompts fixed so
  prompt changes are not confused with the diagnostic signal (see C5).

- [x] **F6 — Source-free task-template repository.** Replace the superseded optional-test-file
  task-loader sketch with `internal/template`: a small data-only repository rooted at the
  composition-root-configured project `templates/` directory. Each template directory contains one
  versioned `template.json` with a stable validated ID, display name, specification, and confirmed
  bodyless Go signature. List, load, create, and update operations must validate all data with the
  same signature rules as a run, bound file sizes/counts, reject traversal and symlink escape, and
  use atomic replacement for updates. The repository never accepts or returns `solution_test.go`,
  a verification bundle, expected values, Rulebook material, oracle-mode choice, or provider
  configuration. It returns template values, not `domain.Task` values; the server constructs the
  generated task only when a user starts a run. Run snapshots later record an optional template ID
  and canonical template-content digest as provenance outside the bundle manifest. Do not write a
  generated oracle back into `templates/`.
  Depends on: F26.
  Verification (2026-07-25): added `internal/template`, a concrete repository configured from
  `cmd/repair -templates-dir` and injected into `server`. It creates, lists, loads, and atomically
  updates versioned `template.json` files containing only ID, display name, specification, and
  signature; canonical source-free content digests are calculated on load/save, never persisted
  as authoritative input. Strict decoding, bounded fields/files, stable lowercase IDs,
  `domain.ValidateSignature`, traversal rejection, and directory/file symlink rejection keep the
  repository data-only. Repository tests cover create/load/list/update, digest changes, malformed
  or oversized documents, unknown fields, mismatched IDs, traversal, symlinks, and atomic
  replacement. `gofmt`, `go build ./...`, `go test -count=1 ./...`, `go test -race ./...`,
  `go vet ./...`, `go mod verify`, and `git diff --check` passed without a provider call.
  Initial repository content (2026-07-25): committed five source-free editable starter templates:
  Split cents, Word wrap, Compare semantic versions, Reverse ASCII text, and Deduplicate strings.
  They contain only the v1 template fields and never pre-supply test source or oracle settings.

### Phase 2 — State (the bridge)

- [x] **F7 — Run orchestration + store.** `internal/run.Store` starts per-run role clients in
  goroutines and returns stable IDs immediately. Its synchronous progress reporter records the
  accepted frozen oracle and appends `domain.Attempt` values under a mutex; `GetRun` returns a
  copied snapshot. State is an in-memory `map[string]*Run` for the lifetime of the process. The
  JSON record includes the submitted spec/signature, oracle, frozen verification-bundle manifest
  (version, origin, task digest, bundle digest), separate generic oracle evidence, selected
  `coderModel`/`testerModel`, attempt budget, lifecycle fields, and result.
  Depends on: F4.
  A browser run owns a bounded context and private cancel function. Generated runs move through
  `writingoracle` and `preflightingoracle` with `currentAttempt: 0`, then candidate provider
  and verifier stages; cancellation is `canceling`, and all terminal runs are `complete`.
  The store rejects a second live run server-side, not only in the UI. A total deadline becomes
  `timedout`, while a per-call provider timeout remains an infrastructure outcome.
  Verification (2026-07-18): scripted runs cover generated fail → pass, authored control,
  frozen oracle snapshots, tester cancellation/timeout, `oraclefailed`, verifier staging,
  immutable snapshots, and one-live-run protection.

### Phase 3 — Web server

- [x] **F31 — Generic run-evidence interpretation.** Rework `/runs/{id}` so its first read is a
  concise, truthful explanation rather than raw Go source. Every task must render from generic,
  mechanically supported evidence only: the submitted task contract; frozen bundle provenance;
  oracle lifecycle and bounded reviewer findings; a top-level Go-test inventory; candidate
  attempt/execution outcomes; and the platform-wide claim boundary. Do not dispatch on task text,
  infer a problem-family coverage summary, manufacture a confidence/effectiveness score, or let
  an LLM author post-hoc semantic claims. Preserve the raw frozen source, candidate source,
  capped output, manifest/digests, and JSON as expandable audit material. A passing execution
  must clearly state that its silent Go output is normal: the candidate built, the frozen test
  binary completed, and its assertions did not fail. Failed attempts must retain only the actual
  generic execution stages reached. Add source inventory with the standard parser only after a
  bundle freezes; it must never affect its digest, preflight admission, candidate prompts, or
  the blind-oracle boundary. Depends on: F30, F25.
  Verification (2026-07-26): `verification.InspectTestSource` now records only frozen,
  top-level declarations named `Test…` after resolution is accepted; it is non-semantic,
  excluded from bundle digest/admission, cloned with snapshots, and tested against valid and
  malformed source. `/runs/{id}` now leads with a generic result claim, concrete successful
  execution explanation, task contract, bundle/review lifecycle, bounded reviewer findings,
  and the test-declaration inventory. The exact test/candidate source and manifest are retained
  as expandable audit material. The successful-output message explicitly explains that Go emits
  no failure output when the compiled test binary completes. Server tests assert the inventory
  and generic-evidence copy while retaining the no-HTML-insertion check. `gofmt`, embedded
  JavaScript syntax, `go build ./...`, `go test -count=1 ./...`, `go test -race ./...`,
  `go vet ./...`, `go mod verify`, and `git diff --check` passed without a provider call.

- [x] **F30 — Truthful live run-progress display.** The immutable run detail page must make a
  long provider/oracle/verifier operation visibly active without inventing a completion percent.
  Render the server-owned current phase as a human-readable operation, show the current candidate
  attempt/budget where one exists, and run an animated **indeterminate** bar plus an elapsed timer
  while status is `running`. Terminal states stop the animation and display the actual outcome.
  The bar is activity feedback, not evidence of progress through a fixed amount of work: generated
  oracle review may request its one allowed revision and candidate attempts can vary. Keep polling
  the existing snapshot endpoint; do not add SSE or browser-side lifecycle state. Depends on: F27,
  F28.
  Verification (2026-07-25): `/runs/{id}` now renders the exact server phase as a readable
  operation, an animated indeterminate activity bar, and a client-side elapsed timer while polling
  the existing run snapshot. The bar remains visibly active during long oracle/provider/verifier
  work but makes no percentage claim; passed and non-passing terminal states stop it with the
  actual outcome. Expired run URLs also render a non-animated unavailable state. Server route tests
  assert the progress/expired-run copy and no unsafe HTML API; embedded JavaScript syntax checks,
  `gofmt`, `go build ./...`, `go test -count=1 ./...`, `go test -race ./...`, `go vet ./...`,
  `go mod verify`, and `git diff --check` passed without a provider call.

- [x] **F29 — Human-readable colored structured logs.** Replace the composition root's plain
  stdlib text handler with `github.com/lmittmann/tint`, a deliberately narrow third-party
  exception to the stdlib-first rule. The standard `slog` package produces structured records but
  has no colored human handler; `tint` preserves those records and improves local terminal
  diagnosis without entering `server`, `run`, the verification boundary, or the browser bundle.
  Add a validated `-log-color auto|always|never` flag: `auto` colors only interactive stderr and
  honors the standard `NO_COLOR` environment convention, while redirected logs remain plain.
  Keep every F28 no-source/no-secret logging rule unchanged. Depends on: F28.
  Verification (2026-07-25): added direct `github.com/lmittmann/tint v1.2.0` and used it only in
  `cmd/repair` to construct the injected logger. `auto` uses stderr's character-device mode and
  respects `NO_COLOR`; `always` and `never` are validated overrides. Unit tests prove debug-level
  handling, invalid configuration rejection, and actual ANSI escape presence/absence for the
  forced color/plain modes. `gofmt`, `go build ./...`, `go test -count=1 ./...`, `go test -race
  ./...`, `go vet ./...`, `go mod verify`, and `git diff --check` passed without a provider call.

- [x] **F28 — Safe structured operational observability.** Inject one explicitly constructed
  stdlib `slog.Logger` from `cmd/repair` into `server` and `run`, with a validated `-log-level`
  configuration flag. Emit metadata-only stderr events for HTTP responses, signature-draft
  start/success/failure, and run start/phase/frozen-bundle/attempt/cancel/terminal events. A
  signature-draft failure must retain a safe browser boundary while distinguishing a
  provider HTTP status, deadline/cancellation, response-size limit, invalid model output, or
  transport failure in both browser-safe wording and the log. Never log task/spec/signature text,
  prompt, candidate/test source, verifier/provider body, request body, endpoint, header, or
  credential. Tests must capture logs and prove useful failure classification plus the absence of
  submitted source text. Depends on: F7, F26, F27.
  Verification (2026-07-25): `cmd/repair -log-level` now constructs and injects a stdlib text
  logger to stderr, rejecting invalid levels. Request completion is logged without query/body
  data; successful mutations and all 4xx/5xx responses are visible at the default `info` level,
  while successful GET/poll responses remain `debug` to avoid log noise. Signature-draft events
  identify model ID, sizes, duration, and a safe failure class; an HTTP 500 also records
  `provider_status=500` and gives the browser a safe actionable explanation. `run.Store` logs
  start, every phase, frozen manifest digests/sizes, attempt sizes/result, cancellation, and
  terminal status/failure class without source or verifier output. Captured-log tests prove an
  HTTP-500 draft and run are classified while submitted-spec and oracle-source sentinels never
  appear. `gofmt`, `go build ./...`, `go test -count=1 ./...`, `go test -race ./...`, `go vet
  ./...`, `go mod verify`, and `git diff --check` passed without a provider call.

- [x] **F8 — HTTP layer.** `internal/server` + `cmd/repair -serve` use Go 1.22+ `net/http`
  routing. `GET /setup` returns safe model IDs, role defaults, and editable presets. `POST /run`
  accepts a strict JSON spec/signature/role-selection request plus a browser-generated
  idempotency token and always starts a server-constructed generated-oracle task. Retrying the same token returns the original
  run ID rather than creating another run. `POST /run/{id}/cancel`
  accepts a live cancellation; `GET /run/{id}`
  returns the live `Run` snapshot. Unknown IDs return `404`; a second live start or terminal
  cancel returns `409`. JSON responses are `Cache-Control: no-store`.
  Depends on: F7.
  The server rejects unknown JSON fields (including `testCode`), invalid/type-invalid signatures,
  oversized fields, malformed request IDs, and model IDs outside the configured catalog with
  `400`.
  Verification (2026-07-18): handler tests serve the page/setup, start and poll a real
  verifier-backed generated-oracle run, reject unsafe input, recover the same ID after an
  idempotent retry, enforce one live run, and cancel a context-aware test-writer call.

### Phase 4 — UI (the flashy payoff)

- [x] **F9 — Embedded UI.** `internal/server/index.html`, embedded by
  `internal/server/assets.go`, is plain HTML/CSS/vanilla JS—no framework, CDN, npm, or build
  step. Its compact form accepts an optional name, required spec/signature, and separate allowed
  code-writer/test-writer models; preset buttons only populate those editable fields. It locks
  the form while a run is active, then shows role metadata, pending → frozen oracle source, its
  verification-bundle origin/digests, and a comparison-first candidate/feedback/later-candidate layout.
  Attempt selectors and full JSON download preserve the observed record.
  Depends on: F8.
  It names oracle writing/preflight, coder wait, Go verification, cancellation, timeout, and
  oracle failure truthfully. A generated green state says a candidate satisfied a frozen blind
  oracle, never verified correctness. A lost start response retries the same idempotency token while the
  form remains locked; polls are bound to one ID/generation and preserve observation on ambiguous
  network failures. Verification (2026-07-18): the inline script parsed, static checks
  found no external assets/framework or HTML injection API, and the server test serves it.

- [x] **F27 — Multi-route template authoring and run analysis UI.** Replace the one-screen
  browser workflow with small embedded vanilla HTML/CSS/JS documents and explicit routes:
  `/templates` for the library, `/templates/new` and `/templates/{id}` for focused authoring,
  `/runs` for template selection and launch, and `/runs/{id}` for one evidence record. Add strict
  JSON APIs for F6 list/load/create/update and for starting a generated-oracle run from a
  server-loaded template; preserve the current strict run/cancel/snapshot protections and
  idempotency. The authoring page validates and saves only template inputs, gives concise guidance,
  and hosts the F26 signature-draft action with explicit user acceptance. The launch page shows a
  selected template read-only, offers only configured coder/tester model IDs, and never exposes an
  oracle-mode or test-source control. The analysis page moves the existing oracle/source/manifest,
  candidate, capped-output, cancellation, and JSON-download views behind a stable run URL.

  Use `textContent` for every untrusted value, no frontend framework/CDN/build chain, and an
  explicit route allowlist rather than a client-side catch-all. The initial run list is only the
  current process's snapshots; after restart it truthfully shows no history. Do not add SQLite,
  automatic template execution, browser-side source loading, destructive template deletion, or a
  generic workflow engine in this item.

  Depends on: F6, F26, F9.
  Verification: repository-backed handler tests cover list/load/save validation, traversal and
  symlink rejection, atomic update behavior, template-to-generated-task conversion, template
  provenance snapshotting, unknown-template rejection, idempotent launch, and one-live-run
  protection. Browser tests cover every route, navigation, explicit signature-draft application,
  untrusted text rendering, and a final run-detail evidence view. `go build ./...`,
  `go test ./...`, `go test -race ./...`, `go vet ./...`, `go mod verify`, formatting, and
  `git diff --check` must pass without a provider call.
  Verification (2026-07-25): added explicit embedded `/templates`, `/templates/new`,
  `/templates/{id}`, `/runs`, and `/runs/{id}` pages with one shared stylesheet and no frontend
  framework/CDN/build chain. `/api/templates` provides strict list/create/load/update operations;
  `/api/templates/{id}/runs` server-loads the source-free template, constructs the generated task,
  records its ID/canonical digest as run provenance outside the bundle, and preserves the store's
  idempotency and single-live-run guard. `/api/runs` supplies current-process history plus detail
  and cancellation endpoints. Authoring keeps signature drafting explicit, launch keeps templates
  read-only and exposes only configured coder/tester selections, and run detail renders untrusted
  values with `textContent`, including frozen oracle evidence, attempts, capped output, and final
  JSON download. Legacy generated-only API aliases remain for programmatic compatibility; the
  browser uses only the template-backed routes. Handler tests cover template persistence/errors,
  template-to-generated-task conversion, provenance snapshotting, unknown templates, idempotent
  launch, existing one-live-run protection, explicit routes, and unsafe DOM API absence. `gofmt`,
  all embedded-JavaScript syntax checks, `go build ./...`, `go test -count=1 ./...`,
  `go test -race ./...`, `go vet ./...`, `go mod verify`, and `git diff --check` passed without a
  provider call.

### Phase 5 — Stretch (only if the above is solid)

- [ ] **F10 — Attempt diffs.** Highlight what changed between consecutive attempts.
- [ ] **F11 — SSE streaming.** Push attempts the instant they land instead of polling.
- [x] **F12 — Task input in UI.** The browser lets a user type a spec and required pinned signature rather
  than picking a file. It accepts neither client test source nor a client-selected oracle mode:
  the server constructs `OracleGenerated` for every browser submission; it resolves and freezes
  before any candidate exists. A compact preset list fills those same editable fields and never auto-runs. The UI
  exposes separate allowed test-writer and code-writer model selections; provider-specific IDs
  come from configured allowlist data, never hardcoded browser values.
  Server-side length/type/model validation, idempotent start recovery, and one-live-run
  protection are implemented.
- [ ] **F13 — Broader verification guidance.** Do not add a generic verifier language or task
  family registry. If a future trusted authored task needs property or metamorphic checks, keep
  them in its human-owned Go source and first show a second concrete use case. Properties can
  narrow the gap between "passed the oracle" and "met the spec," but an optimality claim still
  needs a trusted reference, a bounded exhaustive check, a lower-bound proof, or a certificate.
- [ ] **F14 — SQLite persistence.** `modernc.org/sqlite` (zero-CGO) so runs survive a restart.

## Concerns (C)

- [ ] **C1 — Enforce the blind-oracle separation.** *(amended 2026-07-18 — the rule is no
  longer "human-authored", it is "written without sight of the code".)* Three invariants, all
  structural rather than procedural:
  1. No coder prompt builder may accept test source. `FirstPrompt`/`RepairPrompt` take spec,
     signature, previous code, verifier output — nothing else.
  2. `TestPrompt` may not accept candidate code, and `oracle.Resolver.Resolve` runs before any
     coder call exists to produce some. The ordering is the guarantee.
  3. The oracle is frozen once per run. Nothing may regenerate it after attempt 1, and a
     generated oracle is never written back into `templates/`.
  Re-check whenever the task-authoring path or a prompt signature changes. **Widening one of
  those parameter lists voids the project's premise — flag it, don't do it.**

- [ ] **C4 — Correlated misreading in `generated` mode.** Coder and test-writer read the same
  ambiguous prose; the same model family can misread it identically, producing agreement on the
  wrong behaviour and a green run for the wrong reason. Mitigate with distinct role models
  (F17) and stronger oracles (F13). Never report a green `generated` run as "verified" — a
  candidate satisfied a frozen oracle generated blind to every candidate, and later candidates
  may have used Go feedback. Keep `authored` mode working as the control condition that makes
  this measurable.

- [ ] **C5 — Keep later comparisons interpretable.** During an optional spec-comparison run,
  a repair prompt tuned hard enough to rescue any spec can hide disagreement. Keep prompts fixed
  while measuring; treat prompt-quality work and spec-quality work as separate experiments and
  never vary both at once. This does not block ordinary hackathon prompt work or the core demo.

- [~] **C6 — Generated oracle semantic validity and answer-key provenance.** F16 proves only
  that generated Go test source is structurally admissible and compiles against a
  signature-derived stub. It cannot establish that a hard-coded expected value satisfies the
  specification. An observed generated oracle contained an impossible arithmetic answer key and
  falsely rejected a plausible candidate on every attempt. Distinguish structural admission,
  deterministic validity rules, and trusted-reference answer keys. An LLM committee may reject
  suspicious source before freezing, but is a filter rather than evidence of truth. Free-form
  generated tasks remain explicitly unaudited semantically.

- [ ] **C2 — Harden provider-adapter transport and retry policy before production use.** The
  current OpenAI adapter permits `http://` for local development and delegates one configured
  retry to the official SDK, which can use provider backoff/`Retry-After` guidance. Before
  production or repeated non-local use, restrict plaintext HTTP or require an explicit opt-in,
  decide exactly which transport/status failures may retry, and account for the possibility that
  a paid POST completed before a retryable connection failure. Preserve the whole-call context
  deadline, response-size bound, and sanitized error contract across every future adapter.

- [x] **C3 — Resolve Phase 0 data and error contracts before F2/F3.** `internal/domain` owns
  dependency-free `Task` and `Attempt`; `internal/task` is a future loader only. F2 will create
  a type-free `prompt` package with the three exported primitive APIs named above. The repair
  generation helper receives only spec and signature, so `Task.TestCode` has no route to the
  LLM. The future `repair` package will use the existing `llm.LLM` interface and import
  `domain`, `prompt`, and `llm` without an upward dependency.
  Contract: any nil-error completion string is candidate source; provider/transport/protocol
  errors are infrastructure errors. Non-zero build/test exits and verifier-owned timeout are
  failed attempts; caller cancellation/deadline, invalid cap/timeout, temp/write/cleanup,
  process-launch, and reporter failures are errors. Exhaustion returns the last failed attempt
  and nil.
  Verification (2026-07-18): `gofmt -l $(rg --files -g '*.go')`, `go build ./...`, `go test ./...`,
  `go test -race ./...`, `go test -cover ./...` (75.0% for `internal/llm`), `go vet ./...`,
  and `git diff --check` passed. No live provider call was required.
  *Amendment (2026-07-18, post-design-change):* "`Task.TestCode` has no route to the LLM" now
  reads "no route to the **coder**." In `generated` mode `TestCode` is produced by the
  test-writer before the loop begins and is then frozen. The contract that matters is unchanged
  and still holds: no coder prompt builder can receive test source. `Task` gains an `Oracle`
  field under F15.

## Bugs (B)

### Critical
*None yet.*

### High

- [~] **B3 — Generated oracle can falsely reject a candidate with an internally inconsistent
  answer key.** A saved historical trace froze an impossible expected result, so every candidate
  attempt failed the same invalid assertion. Preserve the trace only as an archived teaching
  artifact; do not route future browser tasks through task-specific semantic code. The active
  mitigation is honest labeling, structural preflight, and human/trusted-source review when a
  task needs stronger semantic verification.

- [x] **B1 — Make browser runs observable and bounded.** A browser run previously exposed only
  `running` plus completed attempts, so a delayed provider response or Go check looked frozen;
  the page could also lose its poller without stopping the server-side job. Add an explicit live
  stage, current attempt, start/deadline timestamps, a server-owned whole-run context, and a
  real `POST /run/{id}/cancel` path. Do not relabel a user cancellation or configured whole-run
  deadline as a candidate failure. On ambiguous poll failure, keep the start action disabled and
  reconnect rather than orphaning a run in the UI; on an ambiguous start response, retry the same
  idempotency token to recover its run ID rather than accidentally creating a second run. Ignore
  callbacks tied to an earlier run.
  Verification (2026-07-18): `go build ./...`, `go test ./...`, server cancel-route tests, and
  run-store provider-wait/verifier/cancel/timeout tests passed. No live provider call was needed.

- [x] **B2 — Require server-side start idempotency tokens.** `POST /run` is documented as
  accepting a browser-generated idempotency token, but an empty `requestId` currently passes
  validation and bypasses the replay registry. A direct caller can therefore start another paid
  run after a prior run becomes terminal. Reject missing or whitespace-only request IDs with
  `400`; retain the existing alphabet/length validation and same-token replay behavior across
  both live and terminal runs. Do not invent a server-only replacement token, because the browser
  must be able to retry an ambiguous start response with the same value. Add handler tests for
  missing/blank rejection and unchanged replay behavior.
  Verification (2026-07-20): `go build ./...`, `go test ./...`, focused
  `go test ./internal/server`, `go vet ./...`, `gofmt`, and `git diff --check` passed. No live
  provider call was made.

### Medium
*None yet.*

### Low
*None yet.*
