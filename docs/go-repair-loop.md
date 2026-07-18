# Go Repair Loop — Build Sketch

A minimal **generate → test → repair** loop in Go. An LLM writes a Go function to a
spec; the Go toolchain checks it; on failure the error is fed back and the model
tries again, until the tests pass or attempts run out.

No sandbox, no external services, no Python/JS. The Go toolchain *is* the verifier:

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
2. Ask the model for a Go function that satisfies the spec.
3. Write it to a throwaway temp dir, next to the test file.
4. Run `go test` with a timeout.
5. **Pass** → done. **Fail** → capture the compiler/test output, feed it back into the next prompt, return to step 2.
6. Give up after N attempts.

It only moves forward, but each attempt can see the previous attempt's code and its
error — that feedback is the whole point.

---

## Data shapes

```go
// A single unit of work. Spec + tests are the human's; Code is the model's.
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
	Passed bool   // did `go test` exit 0?
	Output string // combined compiler/test output — the feedback signal
}

// One run of the loop over one task. This is what the web layer stores and
// serves. Attempts grows as the loop progresses, so a poller sees it fill up.
type Run struct {
	ID       string    `json:"id"`
	Task     string    `json:"task"`     // task name, for the UI header
	Spec     string    `json:"spec"`     // shown so the audience sees what's being solved
	Status   string    `json:"status"`   // "running" | "passed" | "gaveup"
	Attempts []Attempt `json:"attempts"` // appended to live, as each attempt finishes
}
```

---

## Skeleton

The whole thing is ~four functions. TODOs mark where to fill in.

```go
package repair

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// LLM is any completion provider. Implement it once for OpenAI, Gemini, etc.
type LLM interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// Repair is the outer loop: try up to maxAttempts times to satisfy the task.
// Returns the final attempt (passed or not) and only errors on infra failures.
func Repair(ctx context.Context, model LLM, task Task, maxAttempts int) (Attempt, error) {
	var last Attempt // zero value: N==0, signals "first attempt" to generate()

	for i := 1; i <= maxAttempts; i++ {
		code, err := generate(ctx, model, task, last)
		if err != nil {
			return last, err // real infra error (network, etc.) — bubble up
		}

		passed, output := runTests(ctx, task, code)

		last = Attempt{N: i, Code: code, Passed: passed, Output: output}
		if passed {
			return last, nil // success
		}
		// else: loop. Next generate() sees last.Code + last.Output and fixes it.
	}

	return last, nil // ran out of attempts; last holds the closest we got
}

// generate asks the model for code. On the first attempt it just sends the spec;
// on later attempts it sends the previous broken code plus the error output.
func generate(ctx context.Context, model LLM, task Task, prev Attempt) (string, error) {
	var prompt string
	if prev.N == 0 {
		prompt = firstPrompt(task) // TODO: spec + signature + "return only Go code"
	} else {
		prompt = repairPrompt(task, prev) // TODO: spec + prev.Code + prev.Output + "fix it"
	}

	raw, err := model.Complete(ctx, prompt)
	if err != nil {
		return "", err
	}
	return extractGoCode(raw), nil // TODO: strip ```go fences; treat output as untrusted text
}

// runTests writes a tiny throwaway Go module and runs `go test` against it.
// Returns pass/fail and the combined output. A non-zero exit (compile error OR
// test failure) is NOT an infra error here — it's exactly the signal we want.
func runTests(ctx context.Context, task Task, code string) (passed bool, output string) {
	dir, err := os.MkdirTemp("", "repair-*")
	if err != nil {
		return false, "could not create temp dir: " + err.Error()
	}
	defer os.RemoveAll(dir)

	// A self-contained module. stdlib only in v1 → no `go mod tidy` needed.
	write(dir, "go.mod", "module solution\n\ngo 1.26\n")
	write(dir, "solution.go", ensurePackage(code)) // TODO: guarantee `package solution` header
	write(dir, "solution_test.go", task.TestCode)   // human-written; already `package solution`

	// Timeout instead of a sandbox — kills runaway/infinite-loop code.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return false, "timeout — generated code likely hung (infinite loop)"
	}
	// go test exits 0 on pass, non-zero on compile error or failing test.
	return err == nil, string(out)
}

func write(dir, name, content string) {
	_ = os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
```

Helpers left for you / Codex to fill in:

- `firstPrompt(task) string` — spec + signature, instruct "output only a Go function, package `solution`, stdlib only, no explanation."
- `repairPrompt(task, prev) string` — same, plus the previous code and the exact `go test` output, instruct "fix the code so the tests pass."
- `extractGoCode(raw) string` — pull the code out of the model's reply (strip markdown fences). Don't "repair" it — just extract; if it's junk, the compiler will say so next pass.
- `ensurePackage(code) string` — if the generated code lacks a `package solution` line, prepend one. Cheap guard against a common failure.

---

## Components

Break it into small Go packages, each with one job. Nothing points back up the list.

- **`llm/`** — talks to the model. One interface (`LLM`), one concrete client (OpenAI-compatible chat completions). Owns the HTTP call, the API key from env, and a per-call timeout + one retry. Nothing else in the system knows or cares which provider you use.
- **`prompt/`** — turns a task (+ optional previous attempt) into a prompt string, and pulls Go code back out of a model reply (`firstPrompt`, `repairPrompt`, `extractGoCode`). Pure string work, no I/O. Trivial to test and iterate — **this is where loop quality actually lives**, so keep it isolated.
- **`repair/`** — the loop itself (`Repair`, `runTests`). Depends on `llm` + `prompt`. Owns the temp-dir-and-`go test` machinery and the retry logic. The heart; everything else is scaffolding around it.
- **`task/`** — the `Task` type and where tasks come from. v1: a hardcoded slice. Later: load from a folder per task (`spec.md` + `solution_test.go`). Later still: built from an API request.
- **`run/`** — orchestration + state. Kicks off a `Repair` in a goroutine, appends attempts to a `Run` as they land, answers "status of run X." In-memory (a `map[string]*Run` + a mutex) for v1. This is the bridge between the pure loop and the web.
- **`server/`** — HTTP. Two handlers (start a run, poll a run) plus serving the UI. Thin.
- **`web/`** — the frontend. **One HTML file, vanilla JS, baked into the binary with `embed`.** No npm, no build step.

Dependency direction:

```
web → server → run → repair → { prompt, llm }
                       task ↗
```

That clean layering is what lets you build phase by phase — and parallelise the top against the bottom (see Build order).

---

## Build order

The rule: **each phase produces something that works and demos before the next begins.** Never have two broken layers at once.

**Phase 0 — One green run in the terminal.** *(critical path — do this first, protect it)*
Get a single LLM completion working in isolation first — an API call returning text is your first integration risk; nail it before wrapping it in anything. Then hardcode **one** task with a deliberately tricky spec (an off-by-one, an empty-input case — something a first attempt plausibly gets wrong), write its test file by hand, and wire the loop: generate → `go test` → feed failure back → retry. Print attempt number, pass/fail, output.
*Done when:* you watch `attempt 1 fail → … → attempt N pass` in the terminal. **This alone is a complete, honest demo.** Everything after is upside — guard this milestone.

**Phase 1 — Make the loop actually good.** *(still terminal)*
Real `firstPrompt` / `repairPrompt` — the repair prompt must include the previous code **and** the exact `go test` output; that feedback is what makes attempt N+1 smarter than N. Solid `extractGoCode`. Then throw 3–4 different tricky tasks at it and tune until it converges *repeatably*, not just once.
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

- **Package header.** Generated code and the test file must share a package (`solution`). Instruct the model, and keep `ensurePackage` as a backstop.
- **Stdlib only in v1.** If the model imports third-party packages you'd need `go mod tidy` and network. Forbid it in the prompt; keep tasks pure-function so it never needs to.
- **Test-failure is not a program error.** `cmd.CombinedOutput()` returns a non-nil `err` on any non-zero exit. That's expected on a failing test — don't bubble it up as an infra failure. Only a timeout or a filesystem/exec problem is "real."
- **Untrusted output.** Treat the model's reply as text. Extract the code; never `eval`/trust formatting.
