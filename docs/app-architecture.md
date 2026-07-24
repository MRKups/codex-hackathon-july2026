# Test Verifier — Application Architecture

The settled architecture for **Test Verifier**. Read this before changing the verification
platform, candidate-repair loop, HTTP API, or browser flow.

## Purpose and current shape

Test Verifier is a small Go program that makes a blind-oracle verification platform and its
bounded candidate-repair loop visible:

```text
user spec + pinned signature
        ↓
blind test-writer → source/preflight → frozen VerificationBundle
                                    ↓
code-writer → compile/execute bundle → capped feedback → later code-writer attempt
```

The browser always uses the interactive generated-source path. The manifest records only two
source origins—`generated` and `authored`. The terminal retains an authored SplitCents task as a
known-good control. Both sources use the same bundle verifier and repair loop.

The non-negotiable invariants are structural:

1. `prompt.TestPrompt(spec, signature)` has no candidate-code parameter.
2. `prompt.FirstPrompt` and `prompt.RepairPrompt` have no test-source parameter.
3. `oracle.Resolver.Resolve` completes before the first coder call and freezes one accepted
   bundle.
4. The browser cannot submit test source or an oracle-mode override. It submits only task text,
   signature, and safe model selections. The server constructs `OracleGenerated` for every
   browser run.

## What a green run means

| Oracle mode | Green means |
|---|---|
| `authored` | Candidate code satisfied a fixed human-authored oracle. |
| `generated` | A candidate—possibly repaired using Go feedback—satisfied an oracle generated blind to every candidate. It is not verified correctness. |

Generated mode is useful for an end-to-end demonstration and for later comparison work, but a
shared model family can still make the same wrong interpretation twice. The UI and docs must not
overstate a generated green result.

## Constraints

- **Go stdlib first.** No web framework, npm, CDN, or frontend build chain.
- **Go is the verifier.** The app runs `go build ./...`, compiles with `go test -c`, removes the
  source-bearing directory, then executes the test binary from a separate directory with a
  completion sentinel. `go test -c` also preflights an oracle without running test bodies.
- **Trusted-local only.** Model-produced Go is executable local code. Execution has a source-free
  cwd, minimal environment, capped output, and a timeout, but this project is not a process
  sandbox.
- **In-memory state.** Runs disappear when the process exits.
- **One live browser run.** The store rejects a second start, even if a caller bypasses the UI.

## Package direction

```text
browser (HTTP only) → server → run
                              ├→ oracle → {prompt, llm, verification, domain}
                              └→ repair → {prompt, llm, verification, domain}

cmd/repair → {llm/openai, oracle, repair} → {llm, verification, domain}
```

`cmd/repair` is the composition root. It reads configuration, selects the one configured provider
factory, creates the allowed model catalog, constructs the checked-in Rulebook plus concrete
oracle resolver and candidate executor, sets role defaults, injects presets, and wires `server`
to `run`. No lower package imports an upper package.

## Modular composition rule (F24–F25 implemented; F26 planned)

“Plug-and-play” here means explicit, small replacement seams—not a dynamically registered
pipeline whose ordering is hard to audit. The current component direction is:

```text
browser → server → run
                    ├─ oracle → {prompt, llm, verification, domain}
                    └─ repair → {candidate prompts, verifier}
                                      ├─ {prompt, llm, domain}
                                      └─ {verification, domain}

cmd/repair → constructs concrete defaults and injects them downward
```

`internal/oracle` owns only pre-freeze work: Rulebook-guided source authoring, structural
preflight, one bounded reviewer/revision pass, sealing, and resolution evidence. It returns a
frozen bundle and typed evidence; it never sees candidate source,
imports `repair`/`run`/`server`, or mutates a run record. `internal/verification` remains a
deterministic sealer/digest validator, not a policy engine. `internal/repair` consumes the
resolved bundle and owns candidate attempts; it does not know why a particular test rule exists.
`internal/run` executes the fixed resolver → validated snapshot → executor sequence, maps typed
component events to phases, and enforces only the generic resolution handoff contract
(task/digest/origin/evidence consistency), not source-generation policy. `internal/server` remains
an HTTP adapter.

Use interfaces only at real substitution points: the existing `llm.LLM`, the injected
`oracle.Resolver` consumed by `run`, the deterministic `oracle.Admitter` used by the resolver,
the injected `repair.Executor` consumed by `run`, and narrowly scoped test doubles. Keep default
implementations concrete. There must be no service locator, reflection, dynamic plugin registry,
generic `[]Step` executor, browser-selected Rulebook, or task-name/spec/signature dispatch.

The data handoff is one-way and inspectable:

```text
Task → oracle.Resolution{VerificationBundle, Evidence} → ValidateResolution → atomic snapshot
     → repair.CandidateRequest{Spec, Signature, Bundle} → repair.Executor → candidate attempts → Run snapshot
```

`VerificationBundle` stays deliberately narrow: exact source, origin, task digest, and bundle
digest. Generic Rulebook/review provenance belongs in separate resolution evidence, so a future
change to review policy cannot silently change what a frozen bundle means. A future `internal/draft`
package may propose a signature before a run, but it has no route to oracle/candidate source and
does not create verification evidence.

| Package | Responsibility |
|---|---|
| `internal/domain` | Dependency-free `Task`, `OracleMode`, `VerificationBundle` manifest/value types, `Attempt`, and pinned-signature validation. |
| `internal/draft` | Stateless human-confirmed signature proposal; it depends only on `prompt`, `llm`, and `domain` and cannot create verification evidence. |
| `internal/llm` | SDK-free completion interface, provider-neutral runtime config, safe errors, and role-agnostic model allowlist/catalog. |
| `internal/llm/openai` | Official OpenAI Go SDK Chat Completions adapter, provider-local endpoint/key validation, timeout/retry policy, and opt-in live smoke test. |
| `internal/prompt` | Pure prompt construction and conservative code-fence extraction. |
| `internal/verification` | Generic immutable-bundle sealing, validation, and canonical task/bundle digests. It never sees candidate code or task-family semantics. |
| `internal/oracle` | Pre-freeze source resolution: checked-in Rulebook guidance, blind source authoring, structural admission, one bounded review/revision pass, sealing, and generic resolution evidence. |
| `internal/repair` | `Executor` boundary plus the default candidate generation, source-free Go verification, retry limits, and generic feedback against an already sealed bundle. |
| `internal/run` | Bounded asynchronous runs, generic resolution-contract validation, snapshots, lifecycle phase, cancellation, and one-live-run guard. |
| `internal/server` | Strict JSON API plus the embedded vanilla-JS page. |

## Provider and model configuration

| Variable | Meaning |
|---|---|
| `LLM_PROVIDER` | Required provider adapter; currently only `openai` is registered. |
| `LLM_BASE_URL` | Explicit OpenAI Chat Completions-compatible API base URL; a `/v1` path is retained by the SDK adapter. |
| `LLM_API_KEY` | Selected-provider credential. Never sent to the browser. |
| `LLM_MODEL` | Required fallback model. |
| `LLM_MODELS` | Optional comma-separated browser allowlist. Empty means `LLM_MODEL` plus any explicit role defaults. |
| `LLM_MODEL_CODER` | Optional default code-writer model; falls back to `LLM_MODEL`. |
| `LLM_MODEL_TESTER` | Optional default blind test-writer model; falls back to `LLM_MODEL`. |
| `LLM_MODEL_REVIEWER` | Optional bounded oracle-reviewer model; falls back to the effective test-writer model and is not browser-selectable. |
| `LLM_TIMEOUT` | Whole-call timeout for one completion. |

`internal/llm.ModelCatalog` is provider-agnostic. It asks an injected `ClientFactory` for one
reusable client per configured model ID and rejects an empty or unknown selection. It deliberately
does not query a provider’s model endpoint or hardcode vendor model names. Role separation and
provider selection happen above it in `cmd/repair`; the browser cannot select a provider.

The OpenAI adapter uses one stateless user-message Chat Completion per call. It uses the SDK's
single-retry setting under a whole-call context deadline, retains the 1 MiB response bound, and
normalizes provider errors before they can enter a browser-visible `Run.Error`. Choosing the same
model for both roles is legal; choosing different configured IDs can reduce correlated
interpretations.

## Data model

```go
package domain

type OracleMode string

const (
    OracleAuthored  OracleMode = "authored"
    OracleGenerated OracleMode = "generated"
)

type Task struct {
    Name      string
    Spec      string
    Signature string
    Oracle    OracleMode
    TestCode  string // trusted authored input; generated source lives only in the bundle
}

type VerificationBundle struct {
    Manifest VerificationManifest
    TestCode string // exact frozen source; no direct coder-prompt route
}

type VerificationManifest struct {
    Version    string
    Origin     VerificationOrigin
    TaskDigest string
    Digest     string
}

type Attempt struct {
    N      int    `json:"n"`
    Code   string `json:"code"`
    Passed bool   `json:"passed"`
    Output string `json:"output"`
}
```

The browser snapshot persists the complete observed evidence for the life of the process:

```go
type Run struct {
    ID, Task, Spec, Signature string
    Oracle                    string
    Verification              domain.VerificationManifest
    OracleEvidence            oracle.Evidence
    TestCode                  string
    CoderModel, TesterModel   string
    MaxAttempts               int
    Status                    Status
    Stage                     Phase
    CurrentAttempt            int
    StartedAt, DeadlineAt     time.Time
    FailureMode, Error        string
    Attempts                  []domain.Attempt
}
```

All browser runs begin with pending source evidence and `CurrentAttempt == 0`. Once preflight
accepts the bundle, its manifest, source, and generic oracle evidence are written atomically once
to the snapshot, then candidate attempt 1 may begin. The snapshot copies those value fields so
later callers cannot alter frozen evidence.

## Oracle and repair contracts

`run.Store` receives one injected `oracle.Resolver` and one injected `repair.Executor`. For a
generated task it gives the resolver only the `Task` and selected blind source author; the resolver
supplies its static Rulebook to the pure test prompt, structurally admits zero or more source
candidates, and returns one sealed bundle or `*oracle.OracleFailureError`. For an authored task,
the resolver instead admits trusted `Task.TestCode` and needs no author. Before publishing any
result, `run` checks `oracle.ValidateResolution`: bundle digest/task binding, source origin that
matches task mode, and the appropriate generated-oracle evidence. Its typed writing/preflighting
callbacks are the only route by which `run` learns source-resolution progress.

```go
func RepairWithConfig(
    ctx context.Context,
    coder llm.LLM,
    request repair.CandidateRequest,
    config repair.Config, // limits
    report repair.ProgressReporter,
) (domain.Attempt, error)

type repair.CandidateRequest struct {
    Spec      string
    Signature string
    Bundle    domain.VerificationBundle
}

type repair.Executor interface {
    Execute(context.Context, llm.LLM, repair.CandidateRequest, repair.Config, repair.ProgressReporter) (domain.Attempt, error)
}
```

`RepairWithConfig` first validates the supplied frozen bundle, then reports only completed
candidate verifications. It has no test-writer, Rulebook, separately supplied test-source, or
oracle-review parameter. Its sealed bundle necessarily contains the executable source needed by
the verifier, but default candidate prompts use only `CandidateRequest.Spec`,
`CandidateRequest.Signature`, and later capped verifier feedback. `repair.DefaultExecutor` is the
current concrete implementation of `repair.Executor`.

For a generated task, `oracle.DefaultResolver`:

1. Parse the user’s one top-level function signature into a panic-only stub.
2. Ask the selected source author with only `TestPrompt(spec, signature)` plus the checked-in,
   versioned Rulebook.
3. Extract only an unambiguous whole-response code fence; otherwise keep the raw model text.
4. Reject empty/malformed/obviously bypassed test candidates and preflight valid-looking ones
   with `go test -c` against the stub. The structural gate type-resolves the pinned call and
   testing failure path from the same runnable test (including reachable helpers/subtests), but
   does not prove oracle quality.
5. Retry rejected oracle candidates only up to the resolver's configured source-attempt cap,
   before any coder call.
6. Return `*oracle.OracleFailureError` if none is accepted. The run maps this to `oraclefailed`.
   A preflight process timeout is infrastructure failure instead of an oracle verdict.

After an oracle is frozen, normal candidate attempts use only `FirstPrompt` or `RepairPrompt`.
Build/test non-zero exits are failed attempts. Provider errors, caller cancellation, temp-file
failures, process launch failures, and reporter errors are ordinary errors. An exhausted candidate
budget returns the final failed attempt with nil error.

## Run lifecycle

Each browser run owns a `context.WithDeadline(context.Background(), deadline)` and a private
cancel function. It reports these live phases:

| Phase | Meaning |
|---|---|
| `starting` | Snapshot created. |
| `writingoracle` | Test-writer completion is in flight; candidate attempt is `0`. |
| `preflightingoracle` | A generated or authored source bundle is being checked before code exists. |
| `reviewingoracle` | The configured reviewer is assessing structurally admitted generated source before it freezes. |
| `waitingforprovider` | Code-writer completion is in flight. |
| `verifying` | Go is building/testing the current candidate. |
| `canceling` | User cancellation was accepted. |
| `complete` | Terminal state. |

Terminal statuses are `passed`, `gaveup`, `canceled`, `timedout`, `oraclefailed`, and
`infrastructurefailed`. Cancellation and the whole-run deadline take precedence over a pending
provider/verifier result and are never recast as candidate failures.

## HTTP contract

```text
GET  /setup                 → configured model IDs, role defaults, editable presets
POST /signature-draft       → syntax-valid signature draft only; never starts a run
POST /run                   → accepts task input, returns 202 {"id":"run_..."}
POST /run/{id}/cancel       → requests cancellation of a live run
GET  /run/{id}              → live Run snapshot
GET  /                      → embedded browser page
```

The request body for `POST /run` is exactly:

```json
{
  "requestId": "browser-generated-idempotency-token",
  "name": "optional label",
  "spec": "required behaviour",
  "signature": "func Solve(input string) (string, error)",
  "coderModel": "configured-id",
  "testerModel": "configured-id"
}
```

The browser creates a non-empty `requestId`; retrying it returns the same run ID and cannot start
a second run. The server uses `DisallowUnknownFields`, caps body/field size, validates one
type-valid bodyless function signature, and rejects model IDs outside the catalog. `testCode`, a
bundle, and an oracle-mode override are therefore impossible request fields. HTTP responses are
`Cache-Control: no-store`; terminal cancel or concurrent-start conflicts return `409`.

## Browser behavior

The page is a single embedded `index.html`. It uses `textContent`/DOM nodes for all provider and
verifier data, never HTML insertion. A compact form sits above the existing comparison layout:

- Preset buttons populate editable task inputs without starting a run.
- The signature-draft action uses the selected test-writer model, returns a syntax-valid proposal,
  and requires an explicit browser action before it changes the editable signature.
- The active run locks the form and exposes its role models.
- The verification panel moves from pending to frozen source before code appears and names its
  origin, manifest version, and digest.
- Candidate / capped feedback / later candidate remain side by side, with selectors for attempts.
- Polling is bound to a run ID plus generation, so late responses cannot overwrite another run.
- The page retains polling after ambiguous transport failure and offers cancellation.
- Downloaded JSON is the final accepted-evidence snapshot; rejected oracle candidates and raw
  provider wrapper replies are not retained, and there is no disk persistence yet.

## Planned task-template workflow (F6/F27)

The current page is a temporary single-screen workflow. F6 introduces a source-free,
project-root `templates/` repository, manually constructed at `cmd/repair` and injected into
`server`. A template is not a run and is not verification evidence: it contains only a stable ID,
display name, specification, and user-confirmed signature. It has no test source, expected value,
Rulebook material, model selection, provider configuration, oracle mode, or bundle. The repository
does not import `run`, `oracle`, or `repair`; `server` loads a selected template, constructs the
same `OracleGenerated` task used for free-form browser input, and starts the existing store.

F27 replaces the single document with explicit embedded routes for the template library and
authoring pages, the template-launch page, and a stable per-run analysis page. Model selection is
made only when launching a run; a saved template stays provider-agnostic. The run snapshot copies
the submitted task as it does today and will carry an optional template ID/content digest for
provenance. Editing a template therefore cannot alter an already-started or historical run.
Template files persist across restarts, while run snapshots remain in memory until a separate
persistence decision; final downloaded JSON remains the portable evidence record. Every browser
launch remains generated-oracle mode, and the test writer still resolves/freezes the bundle before
candidate generation.

## Deployment

Build a single binary with `go build -o test-verifier ./cmd/repair`. The target host needs Go
on `PATH` at runtime because verification shells out to `go build` and `go test -c`, then
executes the compiled test binary. The embedded UI has no separate deployment step.
