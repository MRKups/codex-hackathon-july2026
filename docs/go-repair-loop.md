# Go Repair Loop — Design

Repair Loop is a visible Go **specify → generate → verify → repair** workflow built for a
hackathon demonstration. A browser user supplies a specification and pinned function signature;
a blind test-writer produces a frozen oracle before a separate code-writer produces candidates.
The Go toolchain verifies each candidate, and exact failed output informs the next candidate.

The terminal retains one authored-oracle SplitCents task as a control. The browser’s main path is
generated-oracle mode because it makes the full flow inspectable without requiring users to write
tests themselves.

## The blind-oracle rule

An oracle is legitimate only when it was written without seeing the solution it judges.

| Mode | Oracle source | Test writer sees | Green means |
|---|---|---|---|
| `authored` | Human-written `solution_test.go` | Task only | Candidate passed an independent fixed oracle. |
| `generated` | Separate LLM completion | Spec + signature only | A candidate satisfied an oracle generated blind to every candidate. |

The generated result is not “verified correctness.” The same model family can interpret vague
prose the same way twice. Separate selectable role models mitigate that risk but do not eliminate
it.

Three structural invariants keep the flow honest:

1. `FirstPrompt(spec, signature)` and
   `RepairPrompt(spec, signature, previousCode, verifierOutput)` have no test-source parameter.
2. `TestPrompt(spec, signature)` has no candidate-code parameter, and oracle resolution happens
   before the program calls a coder at all.
3. One accepted oracle is frozen for the run. It is never regenerated after candidate attempt 1
   and never written back into the repository.

Do not widen those APIs to “make something easier.” That would invalidate the project’s premise.

## Browser flow

```text
editable preset or user-written spec + required Go signature
                  │
                  ▼
      test-writer model (spec + signature only)
                  │
                  ▼
  parse/check/preflight generated solution_test.go
                  │
                  ├── reject → another test-writer attempt, before code exists
                  │
                  ▼
          freeze accepted oracle in the Run record
                  │
                  ▼
  code-writer model (spec + signature; later: code + Go feedback)
                  │
                  ▼
      write disposable module → go build → go test
                  │
                  ├── failure output → next code-writer prompt
                  └── pass → terminal result
```

The browser submits only an optional name, spec, signature, two configured model IDs, and a
browser-generated idempotency token. It cannot submit `solution_test.go`, a provider endpoint/
key, or an oracle-mode override. Retrying the same token returns the original run ID rather than
starting a second run. Preset buttons simply fill the same editable inputs; they never start a
run automatically.

## Task contract

Every task has one bodyless, top-level Go function signature. For example:

```go
func SplitCents(total, recipients int) ([]int, error)
```

The signature is not cosmetic. It pins the shared API so independently generated tests and code
can compile together. Browser validation rejects blank, oversized, malformed, type-invalid,
method, multi-declaration, or function-body signatures before any provider call.

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

`TestCode` is required for authored mode. It is cleared at generated-mode start and populated
only by the accepted blind test-writer result.

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
failure. Before candidate generation, `repair`:

1. Builds a panic-only stub from the parsed signature and type-checks it.
2. Parses the generated test source and requires `package solution`, a runnable `Test...`
   function that receives `*testing.T`, a direct call to the pinned function, and a standard
   testing failure method.
3. Rejects build constraints, `TestMain`, `init`, skipped tests, and direct `os.Exit` calls in
   generated test source.
4. Writes the stub and test source into a disposable Go module.
5. Runs `go test -c ./...` under the injected verifier timeout. This compiles but does not run
   test bodies.

An invalid or non-compiling test candidate may be regenerated up to `maxOracleAttempts`, all
before any coder call. The accepted test source is frozen. Exhausting that cap returns
`*repair.OracleFailureError`, and the run becomes `oraclefailed`; it is neither a candidate
attempt nor a pass. A preflight process timeout, provider error, caller cancellation,
temp-directory/file/process failure, or invalid user signature remains an ordinary infrastructure
error rather than an oracle verdict. These structural checks are a guardrail, not proof that a
generated oracle is semantically complete.

## Candidate verification and repair

The verifier writes exactly these files to a fresh temp module:

```text
go.mod
solution.go       # model text, verbatim after conservative fence extraction
solution_test.go  # frozen authored or generated oracle
```

It runs:

```text
go build ./...
go test -timeout <verifier timeout> ./...     # only if build passed
```

No `go mod tidy`, dependency installation, source patching, or test rewriting occurs. A non-zero
build/test exit is a failed `domain.Attempt` with raw combined output. A verifier timeout is also
failed-attempt feedback. Filesystem, cleanup, process-launch, reporter, provider, or caller-
context failures are returned as errors. An exhausted candidate cap returns the final failed
attempt and nil error.

The implemented repair API is:

```go
type ProgressReporter struct {
    OracleResolved  func(testCode string) error
    AttemptFinished func(domain.Attempt) error
}

func Repair(
    ctx context.Context,
    coder, tester llm.LLM,
    task domain.Task,
    maxAttempts int,
    testTimeout time.Duration,
    maxOracleAttempts int,
    report ProgressReporter,
) (domain.Attempt, error)
```

The oracle callback fires once after acceptance and before the first coder call. The attempt
callback fires synchronously after every completed Go verification.

## Run state and browser truthfulness

`run.Store` owns a deadline context and private cancel function for each browser run. It accepts
one live run at a time to avoid accidental parallel provider cost. Its snapshot records:

- submitted task name/spec/signature;
- `authored` or `generated` oracle mode and the frozen source;
- selected code-writer and test-writer model IDs;
- candidate attempts and raw verifier output;
- lifecycle stage, timestamps, deadline, status, and error text.

Live stages are `starting`, `writingoracle`, `preflightingoracle`, `waitingforprovider`,
`verifying`, and `canceling`; terminal snapshots use `complete`. Oracle stages use candidate
attempt `0`. Terminal statuses are `passed`, `gaveup`, `canceled`, `timedout`,
`oraclefailed`, and `infrastructurefailed`.

The browser polls snapshots about twice per second, binds every poll to a run ID/generation, and
uses DOM text nodes for untrusted source/output. It also retries an unconfirmed start with the
same idempotency token rather than risking a hidden second run. It shows a pending oracle before
code exists, then the frozen source beside the submitted spec. A generated green state means a
candidate satisfied that frozen blind oracle; a later candidate may have used Go feedback to get
there, so it is not verification. Downloaded JSON is a final accepted-evidence snapshot, not a
server-persistent event log: it omits rejected oracle candidates and provider wrapper replies.

## Configuration and limits

| Setting | Purpose |
|---|---|
| `LLM_TIMEOUT` | One provider completion, including the client’s retry policy. |
| `-verifier-timeout` | One candidate verification or generated-oracle compile preflight. |
| `-oracle-attempts` | Generated test candidates allowed before `oraclefailed`. |
| `-attempts` | Candidate-code attempts after an oracle is frozen. |
| `-run-timeout` | Entire browser run, across tester, coder, and Go work. |

`LLM_MODEL` remains required. `LLM_MODELS` is a local comma-separated browser allowlist.
`LLM_MODEL_CODER` and `LLM_MODEL_TESTER` select defaults and fall back to `LLM_MODEL`. The
browser sees only those IDs, never provider credentials or an arbitrary model field.

## Deferred work

- F5: prompt tuning on a handful of real tasks.
- F6: task folders and automatic task loading.
- F18: optional persistent/varied failure classification.
- F10/F11/F13/F14: diffs, SSE, property oracles, and persistence.
- C2: transport/retry hardening before repeated external or production use.

Keep the current flow transparent rather than “clever.” A real model can pass on attempt one;
the page must show that honestly. Save a genuinely observed multi-attempt JSON trace if a
repeatable repair walkthrough is needed.
