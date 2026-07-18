# Application Architecture

The settled architecture for **Repair Loop**. Read this before changing the loop, HTTP API, or
browser flow.

## Purpose and current shape

Repair Loop is a small Go program that makes a blind-oracle repair loop visible:

```text
user spec + pinned signature
        ↓
blind test-writer → preflight → frozen oracle
        ↓
code-writer → Go build/test → feedback → later code-writer attempt
```

The browser is the interactive generated-oracle path. The terminal retains an authored
SplitCents task as a known-good control. Both use the same verifier and repair loop.

The non-negotiable invariants are structural:

1. `prompt.TestPrompt(spec, signature)` has no candidate-code parameter.
2. `prompt.FirstPrompt` and `prompt.RepairPrompt` have no test-source parameter.
3. `repair.resolveOracle` completes before the first coder call and freezes one accepted oracle.
4. The browser cannot submit test source or choose an oracle mode; it submits only task text,
   signature, and safe model selections. The server constructs `OracleGenerated`.

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
- **Go is the verifier.** The app runs `go build ./...`, then `go test -timeout <limit> ./...`
  in a disposable module. `go test -c` preflights a generated oracle without running test
  bodies.
- **Trusted-local only.** Model-produced Go is executable local code. A timeout bounds work;
  this project is not a process sandbox.
- **In-memory state.** Runs disappear when the process exits.
- **One live browser run.** The store rejects a second start, even if a caller bypasses the UI.

## Package direction

```text
browser (HTTP only) → server → run → repair
                                      ├→ prompt
                                      ├→ llm
                                      └→ domain
```

`cmd/repair` is the composition root. It reads configuration, creates the allowed model catalog,
sets role defaults, injects presets, and wires `server` to `run`. No lower package imports an
upper package.

| Package | Responsibility |
|---|---|
| `internal/domain` | Dependency-free `Task`, `OracleMode`, `Attempt`, and pinned-signature validation. |
| `internal/llm` | Stateless OpenAI-compatible completion client and role-agnostic model allowlist/catalog. |
| `internal/prompt` | Pure prompt construction and conservative code-fence extraction. |
| `internal/repair` | Oracle resolution/preflight, candidate generation, Go verification, and retry loop. |
| `internal/run` | Bounded asynchronous runs, snapshots, lifecycle phase, cancellation, and one-live-run guard. |
| `internal/server` | Strict JSON API plus the embedded vanilla-JS page. |

## Provider and model configuration

| Variable | Meaning |
|---|---|
| `LLM_BASE_URL` | OpenAI-compatible API base URL; the client appends `/chat/completions`. |
| `LLM_API_KEY` | Provider credential. Never sent to the browser. |
| `LLM_MODEL` | Required fallback model. |
| `LLM_MODELS` | Optional comma-separated browser allowlist. Empty means `LLM_MODEL` plus any explicit role defaults. |
| `LLM_MODEL_CODER` | Optional default code-writer model; falls back to `LLM_MODEL`. |
| `LLM_MODEL_TESTER` | Optional default blind test-writer model; falls back to `LLM_MODEL`. |
| `LLM_TIMEOUT` | Whole-call timeout for one completion. |

`internal/llm.ModelCatalog` is provider-agnostic. It builds one reusable client per configured
model ID and rejects an empty or unknown selection. It deliberately does not query a provider’s
model endpoint or hardcode vendor model names. Role separation happens above it in `cmd/repair`.

The model client uses one stateless user-message completion per call. Choosing the same model for
both roles is legal; choosing different configured IDs can reduce correlated interpretations.

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
    TestCode  string // authored input, or generated/frozen before attempt 1
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

Generated runs begin with empty `TestCode` and `CurrentAttempt == 0`. Once preflight accepts the
test source, `TestCode` is written once to the snapshot, then candidate attempt 1 may begin.

## Repair contract

```go
func Repair(
    ctx context.Context,
    coder, tester llm.LLM,
    task domain.Task,
    maxAttempts int,
    testTimeout time.Duration,
    maxOracleAttempts int,
    report repair.ProgressReporter,
) (domain.Attempt, error)
```

`ProgressReporter` can receive the one accepted oracle and each completed verification. It lets
the run store show frozen tests before any candidate lands.

For an authored task, `tester` may be nil and `Task.TestCode` is validated then frozen. For a
generated task:

1. Parse the user’s one top-level function signature into a panic-only stub.
2. Ask `tester` with only `TestPrompt(spec, signature)`.
3. Extract only an unambiguous whole-response code fence; otherwise keep the raw model text.
4. Reject empty/malformed/obviously bypassed test candidates and preflight valid-looking ones
   with `go test -c` against the stub. The structural gate requires a direct call to the pinned
   function and a standard testing failure method, but does not prove oracle quality.
5. Retry rejected oracle candidates only up to `maxOracleAttempts`, before any coder call.
6. Return `*repair.OracleFailureError` if none is accepted. The run maps this to `oraclefailed`.
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
| `preflightingoracle` | Generated test source is being checked before code exists. |
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

The browser creates `requestId`; retrying it returns the same run ID and cannot start a second
run. The server uses `DisallowUnknownFields`, caps body/field size, validates one type-valid
bodyless function signature, and rejects model IDs outside the catalog. `testCode` and an
oracle-mode override are therefore impossible request fields. HTTP responses are
`Cache-Control: no-store`; terminal cancel or concurrent-start conflicts return `409`.

## Browser behavior

The page is a single embedded `index.html`. It uses `textContent`/DOM nodes for all provider and
verifier data, never HTML insertion. A compact form sits above the existing comparison layout:

- Preset buttons populate editable task inputs without starting a run.
- The active run locks the form and exposes its role models.
- The oracle panel moves from pending to frozen source before code appears.
- Candidate / raw feedback / later candidate remain side by side, with selectors for attempts.
- Polling is bound to a run ID plus generation, so late responses cannot overwrite another run.
- The page retains polling after ambiguous transport failure and offers cancellation.
- Downloaded JSON is the final accepted-evidence snapshot; rejected oracle candidates and raw
  provider wrapper replies are not retained, and there is no disk persistence yet.

## Deployment

Build a single binary with `go build -o repair ./cmd/repair`. The target host needs Go on `PATH`
at runtime because verification shells out to `go build`, `go test`, and generated-oracle
preflight `go test -c`. The embedded UI has no separate deployment step.
