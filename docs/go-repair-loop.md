# Go Repair Loop — Build Sketch

A minimal **generate → verify → repair** loop in Go. An LLM writes a complete Go source
file to a spec; the Go toolchain checks it; on failure the first verifier error is fed back
and the model tries again, until the tests pass or attempts run out.

No sandbox, no additional infrastructure services, no Python/JS. The configured LLM is the
only external HTTP dependency; the Go toolchain *is* the verifier:

- `go build` (the compiler) rejects most bad output for free, before anything runs — **checker #1**.
- `go test` runs human-written tests — **checker #2**.
- Fast compile keeps the retry loop tight.
- An infinite loop in generated code is handled by a **timeout**, not isolation. That's enough for running your own benign code locally.

---

## Core principle (don't skip this)

**The model that writes the code never writes the tests.** Tests are human-authored
and fixed for a given task. This is deliberate: if one context writes both, a
misunderstanding of the task corrupts *both* the code and the tests, and the tests
just rubber-stamp the bug. Keeping them separate is what makes a green result mean
something.

---

## The loop, in plain English

1. Take a **task**: a natural-language spec + a human-written test file.
2. Ask the model for a complete `package solution` source file that satisfies the spec.
3. Write it to a throwaway temp dir, next to the test file.
4. Run `go build ./...`, then `go test ./...` only when the build succeeds, under one verifier timeout.
5. **Pass** → done. **Fail** → capture the first failed command's output, feed it back into the next prompt, return to step 2.
6. Give up after N attempts.

It only moves forward, but each attempt can see the previous attempt's code and its
error — that feedback is the whole point.

---

## Data shapes

`Task` and `Attempt` live in the dependency-free `internal/domain` package:

```go
package domain

// A single unit of work. Spec + tests are the human's; generated code is the model's.
type Task struct {
	Name      string // identifier, used for logging
	Spec      string // natural-language description of the desired behaviour
	Signature string // e.g. "func Solve(in []int) (int, error)" — pin the API so tests compile
	TestCode  string // full contents of solution_test.go, human-written
}

// The result of one pass through the loop.
type Attempt struct {
	N      int    // attempt number, starting at 1
	Code   string // the generated solution.go
	Passed bool   // whether both verifier stages passed
	Output string // empty on success; otherwise failed-stage output or a stable timeout note
}
```

`Run` remains a future `internal/run` type; F7, not C3, freezes its JSON shape:

```go
package run

import "codex-hackathon-july2026/internal/domain"

// One run of the loop over one task. This is what the web layer stores and
// serves. Attempts grows as the loop progresses, so a poller sees it fill up.
type Run struct {
	ID       string           `json:"id"`
	Task     string           `json:"task"`     // task name, for the UI header
	Spec     string           `json:"spec"`     // shown so the audience sees what's being solved
	Status   string           `json:"status"`   // "running" | "passed" | "gaveup"
	Attempts []domain.Attempt `json:"attempts"` // appended to live, as each attempt finishes
}
```

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
func Repair(ctx context.Context, model llm.LLM, task domain.Task, maxAttempts int, testTimeout time.Duration, report AttemptReporter) (domain.Attempt, error) {
	if maxAttempts <= 0 {
		return domain.Attempt{}, errors.New("max attempts must be greater than zero")
	}
	if testTimeout <= 0 {
		return domain.Attempt{}, errors.New("verifier timeout must be greater than zero")
	}

	var last domain.Attempt // zero value: N==0, signals "first attempt" to generate()

	for i := 1; i <= maxAttempts; i++ {
		code, err := generate(ctx, model, task.Spec, task.Signature, last)
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

// generate asks the model for code. On the first attempt it just sends the spec;
// on later attempts it sends the previous broken code plus the error output.
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
```

Prompt API, to be implemented by F2:

- `FirstPrompt(spec, signature string) string` — require a complete `package solution` source
  file, the specified signature, stdlib only, and no explanation or tests.
- `RepairPrompt(spec, signature, previousCode, verifierOutput string) string` — same request,
  plus the previous candidate and exact output from the first verifier stage that failed.
- `ExtractGoCode(raw string) string` — remove only one unambiguous, complete markdown code
  fence. Never add a package header, imports, formatting, or another repair; if the reply is
  junk, the compiler will say so next pass.

The prompt API deliberately accepts only primitive strings. `generate` receives only spec and
signature, while `runTests` is the only helper that receives `Task.TestCode`.

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
| Attempt budget exhausted | Return the final failed attempt and nil error. |

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

- **`domain/`** — dependency-free owner of `Task` and `Attempt`. `Task.TestCode` is fixed,
  human-authored verifier input and never enters a model prompt.
- **`llm/`** — talks to the model. One interface (`LLM`), one concrete client (OpenAI-compatible chat completions). Owns the HTTP call and reads `LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL`, and `LLM_TIMEOUT` from env. It applies a per-call timeout + one retry. Nothing else in the system knows or cares which provider you use.
- **`prompt/`** — pure string work: `FirstPrompt`, `RepairPrompt`, and `ExtractGoCode`. It
  imports no project package; repair supplies only spec, signature, candidate code, and verifier
  output. This is where loop quality lives, so keep it isolated.
- **`repair/`** — the loop itself (`Repair`, `runTests`). Depends on `domain`, `llm`, and
  `prompt`. Owns the temp-dir verifier (`go build`, then `go test`) and retry logic. The heart;
  everything else is scaffolding around it.
- **`task/`** — F6 loader for task files. It returns `domain.Task`; it does not own the type.
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
Get a single LLM completion working in isolation first — an API call returning text is your first integration risk; nail it before wrapping it in anything. Then hardcode **one** task with a deliberately tricky spec (an off-by-one, an empty-input case — something a first attempt plausibly gets wrong), write its test file by hand, and wire the loop: generate → `go build` → `go test` → feed first-failure output back → retry. Print attempt number, pass/fail, output.
*Done when:* you watch `attempt 1 fail → … → attempt N pass` in the terminal. **This alone is a complete, honest demo.** Everything after is upside — guard this milestone.

**Phase 1 — Make the loop actually good.** *(still terminal)*
Tune the F2 `FirstPrompt` / `RepairPrompt` pair — the repair prompt must include the previous code **and** the exact first-failing verifier output; that feedback is what makes attempt N+1 smarter than N. Tune `ExtractGoCode` only within its conservative no-repair rule. Then throw 3–4 different tricky tasks at it and tune until it converges *repeatably*, not just once.
*Done when:* it fixes several different bugs on its own, reliably.

**Phase 2 — Wrap it in state.** *(the bridge)*
The `run` package: `StartRun(task)` launches `Repair` in a goroutine and returns a run ID immediately; attempts append to the `Run` as they finish. In-memory store, no database. **Freeze the JSON shape here** (the `Run` struct above) — that frozen contract is what lets the UI be built in parallel.
*Done when:* you can start a run and query its live status as JSON via curl.

**Phase 3 — Web server.** *(Go)*
`net/http` (Go 1.22+ routing is plenty; chi if you prefer). Two endpoints — see The web layer below. Handler starts the run and returns the ID; the goroutine does the work; the client polls.
*Done when:* curl can start a run and poll it to completion.

**Phase 4 — The UI.** *(the flashy payoff)*
One HTML file, vanilla JS, polls every ~500ms and redraws. The money shots — this is where the room's reaction is won:
- attempts appear one at a time as cards/columns;
- each shows attempt number, the code, a red/green status badge, the test output;
- the final card flips **green** — one clean, unmistakable state change;
- syntax highlighting via a CDN one-liner (e.g. highlight.js) — cheap polish, big visual lift.
Served via `embed`: single static binary, UI included.
*Done when:* you watch it fail red and go green, in the browser, live.

**Phase 5 — Stretch.** *(only if the above is solid)*
Diff between consecutive attempts (highlight what changed — "watch it reason toward correct"). SSE instead of polling so attempts stream the instant they land. Let the user type a task in the UI. Report *why* it gave up (still failing vs. same error looping). **Property tests as the check** instead of examples — a stronger oracle, and the more novel angle. SQLite so runs survive a restart (your OpenLoop already proved this pattern).

**Parallelism (maps to the hackathon's worktrees theme).** Once the JSON contract is frozen at the end of Phase 2, the loop and the UI stop depending on each other. That's the honest place to fan out: one worktree keeps improving the real loop; another builds the UI against a **mock run** that emits canned attempts on a timer. Merge when both are ready. Not parallelism for show — the fastest path through the back half.

---

## The web layer

The one contract everything agrees on — deliberately tiny:

```
POST /run       → { "id": "run_abc" }     // starts a run, returns immediately
GET  /run/{id}  → Run (the struct above)  // poll this ~2x/sec
```

The UI just polls `GET /run/{id}` and redraws `Attempts` each tick. When `Status`
leaves `"running"`, stop polling and do the final green/red flip. That's the entire
frontend contract.

**UI stack: one static HTML file, vanilla JS, served via Go `embed`.** No SvelteKit,
no npm, no build step — you get the live visual demo without any of the web-tooling
weight. (Your OpenLoop's SvelteKit setup is a fallback if you want components later,
but it's more moving parts than a one-day demo needs.)

---

## Gotchas

- **Package header.** Generated code and the test file must share a package (`solution`). Instruct the model; a missing header is compiler feedback for the next attempt, not something the host rewrites.
- **Stdlib only in v1.** If the model imports third-party packages you'd need `go mod tidy` and network. Forbid it in the prompt; keep tasks pure-function so it never needs to.
- **Verifier failure is not a program error.** `cmd.CombinedOutput()` returns a non-nil `err` on any non-zero build or test exit. That is expected feedback — do not bubble it up as an infra failure. Provider, filesystem, command-launch, cleanup, and caller-cancellation failures are real errors.
- **Untrusted output.** Treat the model's reply as text. Extract the code; never `eval`/trust formatting.
