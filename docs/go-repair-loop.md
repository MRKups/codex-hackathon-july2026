# Go Repair Loop — Build Sketch

A minimal **specify → generate → verify → repair** loop in Go with a blind oracle. A human
writes a spec; Phase 0 uses an authored oracle, while later generated mode adds a separate LLM
context that writes tests from the spec and signature alone. The Go toolchain runs candidate and
oracle together; on failure the first verifier error is fed back and the coder tries again,
until the tests pass or attempts run out.

No sandbox, no additional infrastructure services, no Python/JS. The configured LLM is the
only external HTTP dependency; the Go toolchain *is* the verifier:

- `go build` (the compiler) rejects most bad output for free, before anything runs — **checker #1**.
- `go test` runs the oracle — **checker #2**.
- Fast compile keeps the retry loop tight.
- An infinite loop in generated code is handled by a **timeout**, not isolation. That's enough for running your own benign code locally.

---

## Core principle (don't skip this)

**The oracle is written blind to the code.** Whatever produces the tests must never see the
solution they judge. Two legal oracle modes:

| Mode | Oracle author | What a failure means |
|---|---|---|
| `authored` | a human, before the run | the code is wrong |
| `generated` | a second LLM context, spec + signature only | the code is wrong or the two readings may differ |

`generated` is the later showcase and research-inspired variation. Because coder and
test-writer derive from the same spec without seeing each other, persistent disagreement can
surface a different reading worth inspecting. It does not diagnose underspecification by itself.
`authored` is kept as the control condition and the first working path.

Three things make this hold, and all three are non-negotiable:

1. **The test-writer never sees candidate code.** If it does, it writes tests that describe
   the implementation rather than the spec, and the oracle collapses into a rubber stamp.
2. **The oracle is frozen before attempt 1 and never regenerated mid-run.** A moving oracle
   cannot converge, and "make the test pass by rewriting the test" is the exact failure this
   project exists to rule out.
3. **The coder never sees the oracle's source** — only the verifier output from running it.
   That output is the repair signal; the test file itself is not.

The residual risk is **correlated misreading**: one model family reading an ambiguous spec
the same way twice, so code and tests agree on the wrong behaviour and the run goes green
for the wrong reason. Mitigation is to run the two roles on different models (see
`LLM_MODEL_CODER` / `LLM_MODEL_TESTER`). This is a mitigation, not a proof — a green run in
`generated` mode is weaker evidence than a green run in `authored` mode, and the docs should
keep saying so.

---

## The loop, in plain English

1. Take a **task**: a natural-language spec + a pinned signature (+ an authored test file, in
   `authored` mode).
2. **Build the oracle, once.** In `authored` mode, read it from the task. In `generated` mode,
   ask the test-writer context for a `package solution` test file from spec + signature only.
   Check it compiles (see Oracle preflight). Freeze it.
3. Ask the coder for a complete `package solution` source file that satisfies the spec.
4. Write it to a throwaway temp dir, next to the frozen oracle.
5. Run `go build ./...`, then `go test ./...` only when the build succeeds, under one verifier timeout.
6. **Pass** → done. **Fail** → capture the first failed command's output, feed it back into the next coder prompt, return to step 3.
7. Give up after N attempts, and record *how* it failed (see Failure mode signal).

It only moves forward, but each attempt can see the previous attempt's code and its
error — that feedback is the whole point. The oracle does not move at all.

---

## Oracle preflight

A generated oracle can fail to compile — wrong helper name, an import it never used, a call
that doesn't match the signature. That is a **test-writer** fault, and blaming the coder for
it would poison every measurement in the project.

Primary rule, cheap and stdlib-only: attribute build errors by file. If `go build` fails and
every reported error position is in `solution_test.go`, it is an oracle fault; regenerate the
oracle (up to a small injected cap) and restart the run. If any error is in `solution.go`, it
is an ordinary failed attempt. A run that cannot produce a compiling oracle within the cap
ends as `oraclefailed` — a run-level outcome, never a coder failure and never a green.

Stretch (F16): synthesize a signature-derived stub with `go/parser` and compile the oracle
against it *before* the first coder call, so an unusable oracle is caught without spending a
generation. The stub is only ever built, never run.

## Failure mode signal

After the core dual-mode flow works, a run that exhausts its attempts can record a lightweight
failure diagnostic:

- **`varied`** — different tests failed across attempts, or failures moved around. The coder
  is searching and hasn't landed it. Ordinary difficulty.
- **`persistent`** — the *same* test assertion failed on every single attempt while the coder
  changed its approach. This can signal that the coder and test-writer read the task
  differently; inspect the spec, oracle, and model behaviour before drawing a conclusion.

Computed by intersecting the set of failing test names across attempts. It is a useful optional
aid for explaining a give-up, not a prerequisite for the hackathon demo.

---

## Data shapes

`Task` and `Attempt` live in the dependency-free `internal/domain` package:

```go
package domain

// OracleMode says who wrote the tests. It never changes during a run.
type OracleMode string

const (
	OracleAuthored  OracleMode = "authored"  // a human wrote TestCode ahead of time
	OracleGenerated OracleMode = "generated" // a separate LLM context writes it from Spec
)

// A single unit of work. The spec is the human's; the code is the coder's; the oracle is
// either the human's or the test-writer's — never the coder's.
type Task struct {
	Name      string     // identifier, used for logging
	Spec      string     // natural-language description of the desired behaviour
	Signature string     // e.g. "func Solve(in []int) (int, error)" — pin the API so tests compile
	Oracle    OracleMode // which mode this task runs in
	TestCode  string     // solution_test.go. Required when Oracle == OracleAuthored;
	                     // must be empty when OracleGenerated — the loop fills it once, up front.
}

// The result of one pass through the loop.
type Attempt struct {
	N      int    // attempt number, starting at 1
	Code   string // the generated solution.go
	Passed bool   // whether both verifier stages passed
	Output string // empty on success; otherwise failed-stage output or a stable timeout note
}
```

`Task.TestCode` is verifier input in both modes and **must never enter a coder prompt** in
either. In `generated` mode the loop resolves the oracle before attempt 1 and holds it
alongside the task for the rest of the run; the resolved oracle is what the UI displays.

`Run` remains a future `internal/run` type; F7, not C3, freezes its JSON shape:

```go
package run

import "codex-hackathon-july2026/internal/domain"

// One run of the loop over one task. This is what the web layer stores and
// serves. Attempts grows as the loop progresses, so a poller sees it fill up.
type Run struct {
	ID          string           `json:"id"`
	Task        string           `json:"task"`        // task name, for the UI header
	Spec        string           `json:"spec"`        // shown so the audience sees what's being solved
	Oracle      string           `json:"oracle"`      // "authored" | "generated"
	TestCode    string           `json:"testCode"`    // the frozen oracle, shown beside the spec
	Status      string           `json:"status"`      // "running" | "passed" | "gaveup" | "oraclefailed"
	FailureMode string           `json:"failureMode"` // "" | "varied" | "persistent" — set when gaveup
	Attempts    []domain.Attempt `json:"attempts"`    // appended to live, as each attempt finishes
}
```

`TestCode` is on the `Run` deliberately: in `generated` mode the oracle is a *result* of the
run, not an input to it, and showing it next to the spec is what makes the demo legible —
the audience can read the spec, read the tests something else derived from that spec, and
watch the coder try to satisfy them.

---

## Skeleton

The whole thing is ~four functions. TODOs mark where to fill in.

```go
package repair

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"codex-hackathon-july2026/internal/domain"
	"codex-hackathon-july2026/internal/llm"
	"codex-hackathon-july2026/internal/prompt"
)

// AttemptReporter receives each verified attempt synchronously. Nil means no progress
// reporting; a reporter error stops the loop and is returned to the caller.
type AttemptReporter func(domain.Attempt) error

// Repair is the outer loop: try up to maxAttempts times to satisfy the task.
// Its attempt cap and test timeout are injected by the caller. It returns the final
// attempt (passed or not) and only errors on caller or infrastructure failures.
//
// coder and tester are separate llm.LLM values (in practice, different models). tester is
// called at most once — during oracle resolution, before the loop — and never afterwards.
// In OracleAuthored mode tester is unused and may be nil.
func Repair(ctx context.Context, coder, tester llm.LLM, task domain.Task, maxAttempts int, testTimeout time.Duration, report AttemptReporter) (domain.Attempt, error) {
	if maxAttempts <= 0 {
		return domain.Attempt{}, errors.New("max attempts must be greater than zero")
	}
	if testTimeout <= 0 {
		return domain.Attempt{}, errors.New("verifier timeout must be greater than zero")
	}

	// Resolve the oracle ONCE, up front, and freeze it. In OracleAuthored mode this just
	// validates task.TestCode is present. In OracleGenerated mode it calls tester with spec
	// + signature only, and never again — nothing below this line may regenerate it.
	task, err := resolveOracle(ctx, tester, task)
	if err != nil {
		return domain.Attempt{}, err // includes the oraclefailed outcome
	}

	var last domain.Attempt // zero value: N==0, signals "first attempt" to generate()

	for i := 1; i <= maxAttempts; i++ {
		code, err := generate(ctx, coder, task.Spec, task.Signature, last)
		if err != nil {
			return last, err // real infra error (network, etc.) — bubble up
		}

		passed, output, err := runTests(ctx, task, code, testTimeout)
		if err != nil {
			return last, err
		}

		last = domain.Attempt{N: i, Code: code, Passed: passed, Output: output}
		if report != nil {
			if err := report(last); err != nil {
				return last, err
			}
		}
		if passed {
			return last, nil // success
		}
		// else: loop. Next generate() sees last.Code + last.Output and fixes it.
	}

	return last, nil // ran out of attempts; last holds the closest we got
}

// generate asks the coder for code. On the first attempt it just sends the spec;
// on later attempts it sends the previous broken code plus the error output.
// Note what it does NOT receive: a domain.Task, and therefore no route to TestCode.
func generate(ctx context.Context, model llm.LLM, spec, signature string, prev domain.Attempt) (string, error) {
	var promptText string
	if prev.N == 0 {
		promptText = prompt.FirstPrompt(spec, signature)
	} else {
		promptText = prompt.RepairPrompt(spec, signature, prev.Code, prev.Output)
	}

	raw, err := model.Complete(ctx, promptText)
	if err != nil {
		return "", err
	}
	return prompt.ExtractGoCode(raw), nil
}

// runTests writes a tiny throwaway Go module, then runs `go build` and `go test` against it.
// A non-zero build/test exit is feedback. Filesystem, context-cancellation, and command
// launch failures return an infrastructure error.
func runTests(ctx context.Context, task domain.Task, code string, timeout time.Duration) (passed bool, output string, infraErr error) {
	dir, err := os.MkdirTemp("", "repair-*")
	if err != nil {
		return false, "", err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			infraErr = errors.Join(infraErr, cleanupErr)
		}
	}()

	// A self-contained module. stdlib only in v1 → no `go mod tidy` needed.
	if err := write(dir, "go.mod", "module solution\n\ngo 1.26\n"); err != nil {
		return false, "", err
	}
	if err := write(dir, "solution.go", code); err != nil {
		return false, "", err
	}
	if err := write(dir, "solution_test.go", task.TestCode); err != nil {
		return false, "", err
	}

	// Timeout instead of a sandbox — kills runaway/infinite-loop code.
	testContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for _, args := range [][]string{{"build", "./..."}, {"test", "./..."}} {
		cmd := exec.CommandContext(testContext, "go", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()

		if callerErr := ctx.Err(); callerErr != nil {
			return false, "", callerErr
		}
		if testContext.Err() != nil {
			return false, "verifier timeout — generated code may have hung", nil
		}
		if err == nil {
			continue
		}

		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return false, "", err
		}
		return false, string(out), nil
	}

	return true, "", nil
}

func write(dir, name, content string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

// resolveOracle fixes the tests for this run and returns the task with TestCode populated.
// It is the ONLY place a test file is produced, it runs before attempt 1, and its result is
// immutable for the rest of the run.
//
// OracleAuthored: validate task.TestCode is non-empty; tester is not called.
// OracleGenerated: call tester with prompt.TestPrompt(spec, signature) — spec and signature
// and nothing else — extract, and preflight it. On a non-compiling oracle, retry up to the
// injected oracle cap; on exhaustion return the oraclefailed error. The candidate solution
// does not exist yet at this point in the program, which is what makes the guarantee
// structural rather than a matter of discipline.
func resolveOracle(ctx context.Context, tester llm.LLM, task domain.Task) (domain.Task, error) {
	// TODO(F15)
	return task, nil
}
```

Prompt API, to be implemented by F2 (`TestPrompt` by F15):

- `FirstPrompt(spec, signature string) string` — require a complete `package solution` source
  file, the specified signature, stdlib only, and no explanation or tests.
- `RepairPrompt(spec, signature, previousCode, verifierOutput string) string` — same request,
  plus the previous candidate and exact output from the first verifier stage that failed.
- `TestPrompt(spec, signature string) string` — require a complete `package solution` **test**
  file exercising the specified signature: stdlib `testing` only, table-driven, no
  implementation, no `main`. Takes spec and signature and *nothing else* — there is no
  parameter through which candidate code could reach it, and that absence is the guarantee.
- `ExtractGoCode(raw string) string` — remove only one unambiguous, complete markdown code
  fence. Never add a package header, imports, formatting, or another repair; if the reply is
  junk, the compiler will say so next pass. Shared by both roles.

The prompt API deliberately accepts only primitive strings, and the parameter lists are the
enforcement mechanism for the core principle: `FirstPrompt` and `RepairPrompt` have no way to
receive test source, `TestPrompt` has no way to receive candidate code. `runTests` is the only
helper that sees both. **If a future change adds a parameter that crosses that line, the
guarantee is void — stop and flag it rather than widening a signature.**

---

## Failure contract

`Repair` validates `maxAttempts > 0` and a positive verifier timeout before it contacts the
provider. After that, each event has one classification:

| Event | Result |
|---|---|
| `llm.LLM.Complete` returns `raw, nil` | `prompt.ExtractGoCode(raw)` is candidate source, even if it is empty or invalid Go. |
| Provider transport, status, or protocol error | Return the last completed attempt and a non-nil infrastructure error; do not fabricate an attempt. |
| `go build` exits non-zero | Failed attempt with that command's verbatim `CombinedOutput`; do not run tests. |
| `go test` exits non-zero after a build succeeds | Failed attempt with that command's verbatim `CombinedOutput`. |
| Derived verifier timeout while the caller context remains live | Failed attempt with `verifier timeout — generated code may have hung` and nil infrastructure error. |
| Caller cancellation or deadline | Return `ctx.Err()`; never relabel it as a verifier timeout. |
| Temp-dir, write, cleanup, process-launch, or attempt-reporter failure | Infrastructure/caller error. Cleanup errors must not be silently swallowed. |
| Oracle generation returns unusable or non-compiling test source | Regenerate the oracle up to the injected oracle cap. Exhausting that cap ends the run `oraclefailed` with an error; it is never a coder failure and never a pass. |
| `go build` fails with every error position inside `solution_test.go` | Oracle fault, not a failed attempt. Regenerate the oracle per the row above. |
| Attempt budget exhausted | Return the final failed attempt and nil error, with `FailureMode` set to `varied` or `persistent`. |

`Attempt.Output` is empty on a passing attempt. The optional synchronous `AttemptReporter` is
called after every completed verification so the terminal CLI can print real-time progress and
the later run store can append attempts without changing the repair loop. A reporter error stops
the loop and returns the attempt it received plus that error.

The concrete F1 client rejects an empty or malformed provider response as a protocol error. The
abstract rule above still treats any nil-error string from another `llm.LLM` implementation as
candidate source, which keeps fakes and future providers unambiguous.

---

## Components

Break it into small Go packages, each with one job. Nothing points back up the list.

- **`domain/`** — dependency-free owner of `Task`, `Attempt`, and `OracleMode`. `Task.TestCode`
  is verifier input in both modes and never enters a coder prompt.
- **`llm/`** — talks to the model. One interface (`LLM`), one concrete client (OpenAI-compatible chat completions). Owns the HTTP call and reads `LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL`, and `LLM_TIMEOUT` from env. It applies a per-call timeout + one retry. Nothing else in the system knows or cares which provider you use. **Two `LLM` values are constructed — a coder and a test-writer — differing only in model name.** The interface is unchanged; role separation lives in the caller, not in this package.
- **`prompt/`** — pure string work: `FirstPrompt`, `RepairPrompt`, `TestPrompt`, and
  `ExtractGoCode`. It imports no project package; repair supplies only spec, signature,
  candidate code, and verifier output. This is where loop quality lives, so keep it isolated.
- **`repair/`** — the loop itself (`Repair`, `resolveOracle`, `runTests`). Depends on `domain`,
  `llm`, and `prompt`. Owns oracle resolution + preflight, the temp-dir verifier (`go build`,
  then `go test`), and retry logic. The heart; everything else is scaffolding around it.
  `Repair` takes **two** `llm.LLM` values — coder and test-writer — and the test-writer is
  called exactly once, before the attempt loop begins.
- **`task/`** — F6 loader for task files. It returns `domain.Task`; it does not own the type.
  A task directory with no `solution_test.go` loads as `OracleGenerated`; one with a test file
  loads as `OracleAuthored`.
- **`run/`** — orchestration + state. Kicks off a `Repair` in a goroutine, appends attempts to a `Run` as they land, answers "status of run X." In-memory (a `map[string]*Run` + a mutex) for v1. This is the bridge between the pure loop and the web.
- **`server/`** — HTTP. Two handlers (start a run, poll a run) plus serving the UI. Thin.
- **`web/`** — the frontend. **One HTML file, vanilla JS, baked into the binary with `embed`.** No npm, no build step.

Import direction:

```
web → server → run → repair
                    ├→ prompt
                    ├→ llm
                    └→ domain
task → domain
run  → domain
```

Task data flows separately: `task` loader → `domain.Task` → `run` or `repair`. F4 may construct
a `domain.Task` directly because it predates the F6 loader.

That clean layering is what lets you build phase by phase — and parallelise the top against the bottom (see Build order).

---

## Build order

The rule: **each phase produces something that works and demos before the next begins.** Never have two broken layers at once.

**Phase 0 — One green run in the terminal.** *(critical path — do this first, protect it)*
Get a single LLM completion working in isolation first — an API call returning text is your first integration risk; nail it before wrapping it in anything. Then hardcode **one** task with a deliberately tricky spec (an off-by-one, an empty-input case — something a first attempt plausibly gets wrong), write its test file by hand (`authored` mode), and wire the loop: generate → `go build` → `go test` → feed first-failure output back → retry. Print attempt number, pass/fail, output.
*Done when:* you watch `attempt 1 fail → … → attempt N pass` in the terminal. **This alone is a complete, honest demo.** Everything after is upside — guard this milestone.
**Deliberately `authored`-only.** Bringing up the coder loop and the test-writer at the same time means two unproven halves and no way to tell which one broke. The authored oracle is a known-good fixture; earn it first.

**Phase 1 — The test-writer, then loop quality.** *(still terminal)*
First add `generated` mode (F15): `TestPrompt`, oracle resolution before the loop, and the build-error attribution rule that stops an uncompilable oracle from being blamed on the coder. Run the *same* task in both modes and compare the behaviour — a useful extension once the demo is solid.
Then tune the F2 `FirstPrompt` / `RepairPrompt` pair — the repair prompt must include the previous code **and** the exact first-failing verifier output; that feedback is what makes attempt N+1 smarter than N. Tune `ExtractGoCode` only within its conservative no-repair rule.
*Caution on tuning:* for later comparisons, keep the loop transparent rather than tuning away
every disagreement. For the hackathon, tune only enough to make the flow clear and honest.
*Done when:* the same task runs in both oracle modes and the comparison is legible.

**Phase 2 — Wrap it in state.** *(the bridge)*
The `run` package: `StartRun(task)` launches `Repair` in a goroutine and returns a run ID immediately; attempts append to the `Run` as they finish. In-memory store, no database. **Freeze the JSON shape here** (the `Run` struct above) — that frozen contract is what lets the UI be built in parallel.
*Done when:* you can start a run and query its live status as JSON via curl.

**Phase 3 — Web server.** *(Go)*
`net/http` (Go 1.22+ routing is plenty; chi if you prefer). Two endpoints — see The web layer below. Handler starts the run and returns the ID; the goroutine does the work; the client polls.
*Done when:* curl can start a run and poll it to completion.

**Phase 4 — The UI.** *(the flashy payoff)*
One HTML file, vanilla JS, polls every ~500ms and redraws. The money shots — this is where the room's reaction is won:
- the spec and the frozen oracle sit side by side at the top — the audience reads what was asked, then reads the tests a *different* model derived from it;
- attempts appear one at a time as cards/columns;
- each shows attempt number, the code, a red/green status badge, the test output;
- the final card flips **green** — one clean, unmistakable state change;
- syntax highlighting via a CDN one-liner (e.g. highlight.js) — cheap polish, big visual lift.
Served via `embed`: single static binary, UI included.
*Done when:* you watch it fail red and go green, in the browser, live.

**Phase 5 — Stretch.** *(only if the above is solid)*
Diff between consecutive attempts (highlight what changed — "watch it reason toward correct"). SSE instead of polling so attempts stream the instant they land. Let the user type a task in the UI. **Property tests as the check** instead of examples — a stronger oracle, and the more novel angle. SQLite so runs survive a restart (your OpenLoop already proved this pattern).

**An optional later experiment, once the harness works.** Take one task, write three specs for
it at increasing precision — vague, moderate, airtight — and run each several times, varying
only the spec. Compare attempts-to-convergence and failure modes as observations, not guaranteed
outcomes. That can make a useful plot, but it is not the reason to delay the working demo.

Good task material: things with *natural* ambiguity, where a reasonable person could go either
way and most specs never say which. Date arithmetic (what is January 31 plus one month?),
remainder allocation (split $100 three ways — who gets the extra cent?), semver prerelease
ordering, word-wrap edge cases (a single word longer than the line). Pure functions, stdlib
only, fast, deterministic.

**Parallelism (maps to the hackathon's worktrees theme).** Once the JSON contract is frozen at the end of Phase 2, the loop and the UI stop depending on each other. That's the honest place to fan out: one worktree keeps improving the real loop; another builds the UI against a **mock run** that emits canned attempts on a timer. Merge when both are ready. Not parallelism for show — the fastest path through the back half.

---

## The web layer

The one contract everything agrees on — deliberately tiny:

```
POST /run       → { "id": "run_abc" }     // starts a run, returns immediately
GET  /run/{id}  → Run (the struct above)  // poll this ~2x/sec
```

The UI just polls `GET /run/{id}` and redraws `Attempts` each tick. When `Status`
leaves `"running"`, stop polling and do the final flip — green on `passed`, red on
`gaveup` (showing `FailureMode`), and a distinct neutral state on `oraclefailed`, which
is a harness problem rather than a verdict on the code. That's the entire frontend contract.

**UI stack: one static HTML file, vanilla JS, served via Go `embed`.** No SvelteKit,
no npm, no build step — you get the live visual demo without any of the web-tooling
weight. (Your OpenLoop's SvelteKit setup is a fallback if you want components later,
but it's more moving parts than a one-day demo needs.)

---

## Gotchas

- **Package header.** Generated code and the test file must share a package (`solution`). Instruct both roles; a missing header is compiler feedback for the next attempt, not something the host rewrites.
- **Stdlib only in v1.** If either model imports third-party packages you'd need `go mod tidy` and network. Forbid it in both prompts; keep tasks pure-function so it never needs to.
- **A generated oracle can be wrong.** It is derived from the same ambiguous prose the coder reads, and it gets no more authority than that. `generated` mode measures agreement between two readings of a spec; it does not prove correctness. Never describe a green `generated` run as "verified" — say the coder and the test-writer agreed.
- **Don't let the test-writer specify the impossible.** It sees the signature but not the code, so it can invent behaviour the spec never mentions. That is not a bug to patch in the harness — it is the ambiguity signal firing. Fix the spec, not the oracle.
- **Verifier failure is not a program error.** `cmd.CombinedOutput()` returns a non-nil `err` on any non-zero build or test exit. That is expected feedback — do not bubble it up as an infra failure. Provider, filesystem, command-launch, cleanup, and caller-cancellation failures are real errors.
- **Untrusted output.** Treat the model's reply as text. Extract the code; never `eval`/trust formatting.
