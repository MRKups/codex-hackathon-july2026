# Repair Loop

Repair Loop is a small, stdlib-first Go prototype for a visible
**specify → generate → verify → repair** workflow.

In the browser path, you write a specification and a pinned Go function signature. A blind
test-writer context creates `solution_test.go` from those two inputs only; the app compiles that
oracle against a stub and freezes it before a separate code-writer context creates any candidate.
Go then builds and tests each candidate. Failed compiler or test output becomes the next coder
prompt, while the oracle itself never moves.

The page is deliberately plain HTML, CSS, and browser JavaScript—no framework, npm, CDN, or
frontend build step. It makes the actual evidence visible: submitted spec, frozen test source,
selected role models, candidate code, raw Go feedback, and later attempts.

## What a result means

| Mode | Green means |
|---|---|
| Browser generated oracle | A candidate—possibly repaired using Go feedback—satisfied an oracle generated blind to every candidate. This is not a proof of correctness. |
| Terminal authored control | The candidate passed a fixed human-authored oracle. This is stronger evidence of correctness. |

The browser never accepts test source from a user submission. The test writer never receives
candidate code, and the code writer never receives test source—only the output of Go running it.

## Prerequisites

- Go 1.26 or newer, with `go` on your `PATH`.
- An OpenAI-compatible Chat Completions provider, network access, and credentials for a live run.
- A modern browser for the visual demo.
- Bash or Zsh for the shown `source .env` commands. In POSIX `sh`, use `. ./.env` instead.

There are no third-party Go dependencies and no Node/npm tooling to install.

Generated candidates and generated oracles are compiled locally. This is a trusted-local
hackathon tool, not a sandbox for untrusted public input.

## Set up the provider

Copy the tracked template once, fill in your provider values, then load them into the shell:

```bash
[ -f .env ] || cp .env.example .env
# Edit .env, then load it:
source .env
```

Required settings are `LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL`, and `LLM_TIMEOUT`.
`LLM_BASE_URL` may include a version path such as `/v1`; the client appends
`/chat/completions`. Keep `.env` private: it is ignored by Git and must never be committed.
Use HTTPS for every non-local provider.

The browser’s model dropdowns are local configuration, not a provider model-discovery API:

```bash
# One option for both roles:
export LLM_MODEL="your-provider-model"

# Or offer a safe list and choose defaults for each role:
export LLM_MODELS="fast-model,strong-model"
export LLM_MODEL_CODER="strong-model"
export LLM_MODEL_TESTER="fast-model"
```

If `LLM_MODELS` is blank, the app offers `LLM_MODEL` plus any explicitly configured role defaults.
If it is set, it must contain the effective coder and test-writer defaults. Every option uses the
same configured provider endpoint and credential. Selecting the same model for both roles is
legal; choosing different models can reduce correlated interpretations of a spec.

## Build and verify

These commands do not contact a provider:

```bash
go build ./...
go test ./...
go vet ./...
```

Optional development checks:

```bash
go test -race ./...
go test -cover ./...
gofmt -l $(rg --files -g '*.go')
```

To build a named executable:

```bash
go build -o repair ./cmd/repair
./repair -h
```

## Run the browser demo

Start the local server:

```bash
source .env
go run ./cmd/repair -serve -attempts 3 -oracle-attempts 2 -verifier-timeout 10s -run-timeout 2m30s
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080). Use `-addr 127.0.0.1:8090` for another
local address. Press `Ctrl-C` in the terminal to stop the server.

To use the app:

1. Click a preset to fill the form, or write your own specification.
2. Enter one bodyless Go function signature, such as
   `func Normalize(input string) (string, error)`.
3. Choose a code-writer and blind test-writer from the configured dropdowns.
4. Click **Start repair**.
5. Watch the page write and preflight the oracle before candidate attempt 1, then inspect the
   frozen tests, code attempts, and exact Go feedback.
6. After a terminal state, click **Download run JSON** to keep the final evidence snapshot.

The server permits one live run at a time. Browser starts use a local idempotency token, so a lost
start response is retried as the same request instead of creating a second run. The page shows whether it is writing/preflighting the
oracle, waiting for the coder, or verifying Go code, and it offers **Cancel run**. The limits are
independent: `LLM_TIMEOUT` bounds one completion, `-verifier-timeout` bounds one candidate
verification, `-oracle-attempts` bounds generated-oracle retries before any candidate exists,
and `-run-timeout` bounds the entire browser run. A cancellation, timeout, or `oraclefailed`
state is not a verdict on candidate code.

## Notes for judges

The top of a completed browser run establishes what happened in order: the submitted spec and
signature, the two selected model roles, and the test source independently generated and frozen
before candidate code existed. The code writer never receives that source.

Each candidate pane is the exact extracted source written to `solution.go`. The feedback pane is
the raw output from `go build` or `go test` that informed the next repair prompt. A later
candidate is checked against the same frozen oracle. A generated-oracle green state means a
candidate satisfied an oracle generated blind to every candidate; later candidates may have used
Go feedback to converge on it. The UI intentionally does not call this verification.

`Download run JSON` records the accepted evidence: input, selected models, frozen oracle,
completed attempts, verifier output, timestamps, and terminal state. It is a final snapshot, not
an event log of rejected oracle candidates or provider wrapper replies. A live model may pass on its first try; that is shown honestly.
For a repeatable repair trace, save a genuinely observed multi-attempt run rather than inventing
an initial failure.

## Run the authored terminal control

The terminal command keeps the original fixed SplitCents task and human-authored oracle. It is a
quick provider check and a control path separate from the browser’s generated oracle:

```bash
source .env
go run ./cmd/repair -attempts 3 -verifier-timeout 10s
```

It prints each verified attempt and ends as `passed`, `gave up`, or an infrastructure failure.

## Optional live provider smoke check

This explicit integration test sends one short completion request (with at most one retry) and
does not print a response or secrets:

```bash
source .env
LLM_LIVE_TEST=run go test -tags=integration ./internal/llm -run '^TestLiveCompletion$' -count=1 -v
```

## Documentation

- [Design](docs/go-repair-loop.md)
- [Architecture](docs/app-architecture.md)
- [Blind-oracle design change](docs/design-change-2026-07-18.md)
- [Project tracker](docs/tracker.md)
- [Repository layout](docs/file-structure.md)
