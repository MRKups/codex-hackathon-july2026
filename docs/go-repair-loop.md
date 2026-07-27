# Test Verifier — System Design

Test Verifier is a visible Go verification platform built for a hackathon demonstration. Its
bounded candidate-repair loop is **specify → freeze → generate → verify → repair**: a browser
user supplies a specification and pinned function signature; the platform freezes one
verification bundle before candidate code exists; and capped Go feedback may inform a later
candidate. For arbitrary tasks, a blind test-writer produces the frozen source. For a locally
supplied authored task, trusted local code supplies the frozen source. In both cases the
platform seals one **VerificationBundle** before candidate code exists.

The terminal retains one authored-oracle SplitCents task as a control. Every browser run uses
generated-oracle mode because it makes the full flow inspectable without requiring users to write
tests themselves.

## The blind-oracle rule

An oracle is legitimate only when it was written without seeing the solution it judges.

What matters is what the oracle author saw, not who it was. An earlier version of this rule
required a *human* author. That conflated two separate things and only the second is
load-bearing: requiring a human bought no extra integrity, did not scale past a handful of
hand-written test files, and left the specification itself untested by anything. `authored` mode
survives as the control condition that makes generated mode measurable — it is not legacy, and
must not be deleted or left to rot.

| Mode | Oracle source | Test writer sees | Green means |
|---|---|---|---|
| `authored` | Human-written `solution_test.go` | Task only | Candidate passed an independent fixed oracle. |
| `generated` | Separate LLM completion | Task-specific spec + signature plus the checked-in universal Rulebook | A candidate satisfied an oracle generated blind to every candidate. Structural admission does not establish semantic correctness. |

The generated result is not “verified correctness.” The same model family can interpret vague
prose the same way twice. Separate selectable role models mitigate that risk but do not eliminate
it.

In generated mode a failing run therefore has two possible meanings: the candidate is wrong, or
the specification was ambiguous and the coder and test-writer resolved it differently. The second
is the more interesting one, because nothing else in the system tests the specification. A
persistent disagreement — the same assertion failing while the candidate visibly changes approach
— is worth inspecting on those grounds, but it does not prove the specification is
underspecified: candidate ability, oracle quality, and repair feedback are all confounders. See
F18 for the recorded signal.

Three structural invariants keep the flow honest:

1. `FirstPrompt(spec, signature)` and
   `RepairPrompt(spec, signature, previousCode, verifierOutput)` have no test-source parameter.
2. `TestPrompt(spec, signature)` has no candidate-code parameter; `oracle` appends only its
   checked-in universal Rulebook, and resolution happens before the program calls a coder at all.
3. One accepted `VerificationBundle` is frozen for the run. It is never regenerated after
   candidate attempt 1 and never written back into the repository.

Do not widen those APIs to “make something easier.” That would invalidate the project’s premise.

## Browser flow

```text
saved task template (name + spec + confirmed Go signature)
                  │
                  ▼
 test-writer model (server-loaded spec + signature + checked-in Rulebook)
                  │
                  ▼
       parse/preflight generated solution_test.go
                  │
                  ├── source rejected → another test-writer attempt, before code exists
                  ▼
       freeze one VerificationBundle in the Run record
                  │
                  ▼
  code-writer model (spec + signature; later: code + Go feedback)
                  │
                  ▼
 compile disposable test binary → remove source → execute test binary
                  │
                  ├── capped failure feedback → next code-writer prompt
                  └── pass → terminal result
```

The authoring browser saves only a stable template ID, display name, specification, and confirmed
signature in project-root `templates/<id>/template.json`. The launch browser submits only a
server-loaded template ID, two configured model IDs, and a non-empty browser-generated idempotency
token. It cannot submit `solution_test.go`, a provider endpoint/key, bundle source, or an
oracle-mode override. Retrying the same token returns the original run ID rather than starting a
second run. Every browser run is generated-oracle mode. Template edits cannot change a run because
each snapshot carries the selected template ID/content digest plus its own task/bundle evidence.

## Task contract

Every task has one bodyless, top-level Go function signature. For example:

```go
func SplitCents(total, recipients int) ([]int, error)
```

The signature is not cosmetic. It pins the shared API so independently generated tests and code
can compile together. Browser validation rejects blank, oversized, malformed, type-invalid,
method, multi-declaration, or function-body signatures before any provider call.

Task material should be pure, standard-library-only, fast, and deterministic — and should carry
*natural* ambiguity: cases where a reasonable person could go either way and most specifications
never say which. That is what makes a disagreement between an independently written oracle and an
independently written candidate informative rather than noise. The committed starter templates are
chosen on that basis — remainder allocation (who gets the extra cent), semver prerelease ordering,
word wrapping when a single word exceeds the line. Date arithmetic (what is January 31 plus one
month?) is another good source.

```go
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
    TestCode  string
}
```

`TestCode` is required for authored mode. It is cleared at generated-mode start; the accepted
blind test-writer result is held only in the frozen `VerificationBundle`.

## Prompt and extraction rules

`internal/prompt` is pure: it has no I/O, provider calls, or `domain` import.

```go
FirstPrompt(spec, signature string) string
RepairPrompt(spec, signature, previousCode, verifierOutput string) string
TestPrompt(spec, signature string) string
ExtractGoCode(raw string) string
```

Coder prompts demand one complete `package solution` implementation, the exact signature, and
stdlib-only code. Test prompts demand one complete `package solution` test file using stdlib
`testing`, table-driven tests where useful, and no implementation.

Extraction removes only one unambiguous whole-response Markdown backtick fence. It never adds a
package declaration, imports, formatting, or source repair. A nil-error provider response is
therefore candidate text even when it is invalid Go; the verifier decides whether it works.

## Oracle preflight

Generated test source is executable model output and must not be mistaken for a code-writer
failure. `internal/oracle` applies the same structural source gate to generated and raw authored
source; generated source alone may retry after rejection. Before candidate generation, it:

1. Builds a panic-only stub from the parsed signature and type-checks it.
2. Parses the source and requires `package solution`, a runnable `Test...` function that
   receives `*testing.T`, plus a type-resolved pinned-function call and standard testing failure
   method reachable from the same test (including a called helper or `t.Run` subtest). It ignores
   statically false branches and uncalled helpers/closures.
3. Rejects build constraints, `//go:embed`, `TestMain`, `init`, skipped tests, and direct
   `os.Exit` calls in the source.
4. Writes the stub and test source into a disposable Go module.
5. Runs `go test -c ./...` under the injected verifier timeout. This compiles but does not run
   test bodies.

An invalid or non-compiling generated test candidate may be regenerated up to
the resolver's configured source-attempt cap, all before any coder call. Invalid authored source is a local
configuration/infrastructure error. The accepted test source is frozen. Exhausting the generated
cap returns
`*oracle.OracleFailureError`, and the run becomes `oraclefailed`; it is neither a candidate
attempt nor a pass. A preflight process timeout, provider error, caller cancellation,
temp-directory/file/process failure, or invalid user signature remains an ordinary infrastructure
error rather than an oracle verdict. These structural checks are a guardrail, not proof that a
generated oracle is semantically complete.

## Oracle Rulebook and bounded review pipeline

The Rulebook is universal guidance, not a task-specific verifier. `internal/oracle` owns a
checked-in, versioned Oracle Rulebook and includes it in every generated oracle-author prompt. It requires
source to derive only from the submitted spec/signature; prefer validity, boundary, error,
mutation, determinism, round-trip, and metamorphic checks; avoid untrusted non-trivial answer
keys; and avoid unsupported exactness or optimality claims rather than inventing a solution. It is
non-executable prompt policy, never browser input, task matching code, reference logic, or a
third oracle origin. Its version/digest, actual author/reviewer model IDs, call counts, final
review verdict, and bounded finding summaries are separate generic run evidence; they do not
change the VerificationBundle manifest or digest.

The current bounded resolution flow is:

```text
spec + confirmed signature
        ↓
oracle author + Rulebook → structural preflight → reviewer
        ↓                         ↓ accept          ↓ revise (once)
                     freeze VerificationBundle ← structural preflight
        ↓
existing candidate-repair loop
```

There is exactly one review pass after structural admission. The reviewer may see the proposed
oracle source, spec, signature, and Rulebook, but never candidate code. Its untrusted output is
strict, size-bounded JSON: `accept`, `revise`, or `reject`, plus at most six generic findings.
`revise` permits one author replacement and one final structural preflight; it never starts a
committee or regenerates an oracle after candidate work begins. Invalid review output, rejected
source, or failed revision becomes `oraclefailed` before any candidate code exists. The coder sees
neither oracle source nor review material. This improves reviewability, not the fundamental
assurance limit: rules can establish legal output but do not generally prove an arbitrary exact or
optimal result without a trusted reference, certificate, or bounded exhaustive check.

## VerificationBundle platform v1

Natural-language rules are not executable evidence by themselves. The platform therefore freezes
a `VerificationBundle`, rather than treating an accepted test source as a universal proof. A
bundle is deliberately small: the exact executable `solution_test.go` and a manifest containing
only its schema version, one legal origin (`authored` or `generated`), task digest, and bundle
digest.

The task digest binds a bundle to the submitted specification and signature. The bundle digest
binds the complete manifest and exact test source. `internal/verification` owns source sealing,
validation, and canonical digesting. It has no task-family registry, expected-value store,
reference implementation, or executable rule language. `repair`, `run`, `server`, and the
browser never branch on a task name or problem family.

`AuthoredSource` seals trusted caller-owned source; `GeneratedSource` seals source accepted from
the blind test-writer after oracle preflight. Both use the same manifest schema and verifier. A valid
bundle proves internal consistency and provenance, not that its tests are semantically complete.

## Candidate verification and repair

The verifier writes the frozen bundle's exact executable source plus a generic, non-semantic
completion harness to a fresh compile directory:

```text
go.mod
solution.go       # model text, verbatim after conservative fence extraction
solution_test.go  # frozen authored or generated bundle
repair_harness_test.go # verifier-owned completion sentinel, never task semantics
```

It runs `go build ./...`, then `go test -c -o <separate execution directory> ./...`. It removes
the compile directory before executing that test binary from the source-free execution directory
with only a minimal environment (`PATH` and a private `TMPDIR`). A random completion sentinel
written by the harness after `m.Run()` must match before a zero exit is accepted. This prevents an
ordinary candidate or generated test from reading `solution.go`/`solution_test.go` at runtime and
turning source into later repair feedback; it also rejects an early `init`-time `os.Exit(0)`. Both
sources reject `//go:embed`, which would otherwise capture a neighboring source file at compile
time.

The binary runs with its test timeout on every candidate attempt—there is no successful test-result
cache to reuse. Combined verifier output is capped at 128 KiB with an explicit truncation marker
before it can enter a repair prompt. This is a blind-boundary mitigation and secret-reduction
measure, **not** an OS sandbox: deliberately hostile local Go code can still use the host outside
the protections described here.

No `go mod tidy`, dependency installation, task-source patching, or oracle rewriting occurs. A
non-zero build/test exit is a failed `domain.Attempt` with capped combined output. A verifier
timeout is also failed-attempt feedback. Filesystem, cleanup, process-launch, reporter, provider,
or caller-context failures are returned as errors. An exhausted candidate cap returns the final
failed attempt and nil error.

The implemented component contracts are:

```go
type oracle.Resolver interface {
    Resolve(context.Context, oracle.Request, oracle.ProgressReporter) (oracle.Resolution, error)
}

type repair.CandidateRequest struct {
    Spec      string
    Signature string
    Bundle    domain.VerificationBundle
}

type repair.Executor interface {
    Execute(context.Context, llm.LLM, repair.CandidateRequest, repair.Config, repair.ProgressReporter) (domain.Attempt, error)
}

func RepairWithConfig(
    ctx context.Context,
    coder llm.LLM,
    request repair.CandidateRequest,
    config repair.Config, // limits
    report repair.ProgressReporter,
) (domain.Attempt, error)
```

`run.Store` injects the resolver and candidate executor. It rejects a returned resolution when
its digest/task binding, mode/origin, or generated-oracle evidence is inconsistent; it also refuses
to publish one after cancellation or deadline. Only then does it atomically store the accepted
`Resolution`, construct `CandidateRequest{Spec, Signature, Bundle}`, and call candidate-only
repair. The sealed bundle necessarily carries source for the generic verifier, while default
candidate prompts receive only the request's specification/signature and later capped Go feedback.
The repair attempt callback fires synchronously after every completed Go verification.

## Run state and browser truthfulness

`run.Store` owns a deadline context and private cancel function for each browser run. It accepts
one live run at a time to avoid accidental parallel provider cost. Its snapshot records:

- submitted task name/spec/signature;
- `authored` or `generated` oracle mode, frozen source, and the complete verification-bundle
  manifest (version, origin, task digest, and bundle digest);
- separate generic oracle evidence (Rulebook version/digest, author/reviewer model IDs and call
  counts, final review verdict, and bounded finding summaries for generated runs);
- selected code-writer and test-writer model IDs, with the server-configured reviewer recorded in
  oracle evidence;
- optional source-free template ID and canonical content digest, outside the verification bundle;
- candidate attempts and capped combined verifier output;
- lifecycle stage, timestamps, deadline, status, and error text.

Live stages are `starting`, `writingoracle`, `preflightingoracle`, `reviewingoracle`,
`waitingforprovider`, `verifying`, and `canceling`; terminal snapshots use `complete`. Oracle stages use candidate
attempt `0`. Terminal statuses are `passed`, `gaveup`, `canceled`, `timedout`,
`oraclefailed`, and `infrastructurefailed`.

The browser polls snapshots about twice per second, binds every poll to a run ID/generation, and
uses DOM text nodes for untrusted source/output. It also retries an unconfirmed start with the
same idempotency token rather than risking a hidden second run. It shows a pending bundle before
code exists, then its frozen source, manifest, digest, and origin beside the submitted spec. A
generated-source bundle green state means a candidate satisfied that frozen blind oracle; a later
candidate may have used Go feedback to get there, so it is not verified correctness. An authored
bundle is also evidence about its fixed source, not a universal correctness claim. Downloaded JSON
is a final accepted-evidence snapshot, not a server-persistent event log: it omits rejected oracle
candidates and provider wrapper replies.

The run-detail page leads with a generic evidence interpretation, not raw source. Its task
contract, bundle provenance, author/reviewer lifecycle, bounded review findings, parser-derived
top-level Go declarations named `Test…`, and candidate execution stages are mechanically supported facts. It
does not infer a task-family test strategy, rate semantic test quality, or assign a correctness
confidence score. Raw test/candidate source, capped output, and digests remain expandable audit
material. A silent successful Go test binary is displayed as a success explanation rather than
missing output: it means the candidate built, the frozen test binary completed, and its reached
assertions did not fail.

## Configuration and limits

| Setting | Purpose |
|---|---|
| `LLM_PROVIDER` | Required provider adapter; only `openai` is registered in this version. |
| `LLM_TIMEOUT` | One provider completion, including the client’s retry policy. |
| `-verifier-timeout` | One candidate verification or generated-oracle compile preflight. |
| `-oracle-attempts` | Generated test candidates allowed before `oraclefailed`. |
| `-attempts` | Candidate-code attempts after an oracle is frozen. |
| `-run-timeout` | Entire browser run, across tester, coder, and Go work. |
| `-templates-dir` | Project-root directory for source-free saved task templates. |
| `-log-level` | Minimum stderr structured-log level: `debug`, `info`, `warn`, or `error`. |
| `-log-color` | Stderr log color: `auto` (default), `always`, or `never`. |

`LLM_MODEL` remains required. `LLM_MODELS` is a local comma-separated browser allowlist.
`LLM_MODEL_CODER` and `LLM_MODEL_TESTER` select defaults and fall back to `LLM_MODEL`.
`LLM_MODEL_REVIEWER` falls back to the effective test-writer model and is not browser-selectable.
The browser sees only safe coder/test-writer IDs, never provider credentials, a provider selector,
or an arbitrary model field. The current OpenAI adapter uses the official SDK's stateless Chat Completions
surface with the explicitly configured base URL; a future Responses migration is a separate
behavior change, not an implicit transport detail.

## Operational observability

The browser does not receive provider diagnostics, prompts, or raw model output. Instead, the
composition root writes structured `slog` events to stderr through the narrow `tint` handler.
`-log-level` controls the minimum level and defaults to `info`; `debug` is useful while diagnosing
a local request. `-log-color auto` colors an interactive terminal and honors `NO_COLOR`; `always`
and `never` are explicit overrides. Logs cover every
HTTP response (method, path, status, duration), the signature-draft request/result lifecycle, and
the asynchronous run lifecycle (start, phase changes, frozen-bundle digest, capped attempt result,
cancel, and terminal status). Signature-draft failures distinguish safe categories such as provider
HTTP status, timeout, transport failure, overlarge reply, and invalid model output; the browser can
surface that same safe category without showing provider bodies.

The log contract is deliberately metadata-only. Never emit a task specification, Go signature,
prompt, candidate source, generated test source, verifier output, raw provider response, request
body, authorization header, API key, or provider endpoint. IDs, configured model IDs, byte counts,
status codes, elapsed durations, and already-public bundle/template digests are sufficient to
diagnose the ordinary operational path without weakening the source boundary.

The run-detail page renders this same server-owned lifecycle as a live, animated indeterminate
activity bar and elapsed timer. It names the actual stage—writing/preflighting/reviewing the blind
oracle, generating a candidate, or verifying Go—and shows candidate attempt/budget when relevant.
It deliberately does not estimate a percentage: oracle revision and bounded candidate repair mean
the remaining work is not known truthfully. A terminal snapshot stops the animation and shows its
actual result.

## Open work

Tracked in `docs/tracker.md`. Whatever lands there, keep the flow transparent rather than
“clever”: a real model can pass on attempt one, and the page must show that honestly. Save a
genuinely observed multi-attempt JSON trace if a repeatable repair walkthrough is needed.
