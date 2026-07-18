# Repair Loop

Repair Loop is a small, stdlib-first Go hackathon prototype for a visible
**generate → verify → repair** workflow. An LLM writes a candidate Go solution; the Go toolchain
checks it against a frozen oracle; raw compiler or test feedback becomes the next repair prompt.

The browser demo makes that evidence legible: it shows the task and unchanged oracle, the exact
extracted candidate source verified by Go, the exact verifier output, and any later candidate
that passed.
It uses plain HTML, CSS, and browser JavaScript—no frontend framework, npm, CDN, or build step.

The current executable demo uses one fixed, human-authored task: `SplitCents`, which divides
cents among recipients and gives any remainder to the earliest recipients. Task loading and the
later generated-oracle mode are not user-facing yet.

## What a result means

The current demo runs in **authored-oracle mode**. The fixed `solution_test.go` was written before
the LLM writes code, is shown in the browser, and never enters a coder prompt. A green result
therefore means the candidate passed that independent Go oracle.

The loop never edits the oracle. It only extracts an unambiguous code fence when present, writes
the candidate verbatim to a disposable module, runs `go build ./...` and then `go test ./...`,
and supplies a failed stage's output to the next attempt.

## Prerequisites

- Go 1.26 or newer, with `go` on your `PATH`.
- An OpenAI-compatible Chat Completions provider, network access, and credentials for a live run.
- A modern web browser for the visual demo.
- Bash or Zsh for the shown `source .env` commands. In POSIX `sh`, use `. ./.env` instead.

There are no third-party Go dependencies, and no Node/npm or frontend build tooling to install.

## Set up the provider

Copy the tracked template once, fill in your own provider values, then load them into the shell:

```bash
[ -f .env ] || cp .env.example .env
# Edit .env and set LLM_BASE_URL, LLM_API_KEY, LLM_MODEL, and LLM_TIMEOUT.
source .env
```

`LLM_BASE_URL` is an OpenAI-compatible API base URL. It may include a version path such as `/v1`;
the client appends `/chat/completions`. Keep `.env` private: it is ignored by Git and must never
be committed. Use HTTPS for every non-local provider.

The program validates this configuration before it starts either demo. Starting the browser
server itself does not call the provider—the provider call begins only when you click **Run live
repair**.

## Build and verify

Compile and run the project tests without contacting a provider:

```bash
go build ./...
go test ./...
go vet ./...
```

To build a named executable instead of using `go run`:

```bash
go build -o repair ./cmd/repair
./repair -h
```

Optional development checks:

```bash
go test -race ./...
go test -cover ./...
gofmt -l $(rg --files -g '*.go')
```

The last command uses `rg` (ripgrep); it is not required to run the project.

## Run the browser demo

This is the recommended hackathon presentation path:

```bash
source .env
go run ./cmd/repair -serve -attempts 3 -verifier-timeout 10s -run-timeout 90s
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080) and click **Run live repair**. To use a
different free local address, add `-addr 127.0.0.1:8090` (the default is `127.0.0.1:8080`).
Press `Ctrl-C` in the terminal to stop the server.

While a run is live, the page says whether it is waiting for the provider or Go is verifying a
candidate, shows elapsed time against the run limit, and offers **Cancel run**. The three limits
serve different purposes: `LLM_TIMEOUT` bounds one provider completion, `-verifier-timeout`
bounds one `go build`/`go test` verification, and `-run-timeout` bounds the whole browser run
(90 seconds by default). A canceled or timed-out run is shown as such; neither is presented as a
verdict on the candidate code.

### Notes for judges

The top panels establish the inputs: the task specification and the frozen authored
`solution_test.go` oracle. The oracle remains unchanged for the entire run and is never shown to
the coder.

Each rejected card contains the exact extracted candidate source written to `solution.go` and
verified by Go. The middle panel contains the raw Go verifier feedback that the loop supplied to
the next repair prompt. If a later card is green, its source passed both `go build` and `go test`
against that same oracle.

After a terminal result, **Download run JSON** saves the spec, frozen oracle, exact candidates,
verifier outputs, phase/timing data, and final state as a portable record of the run.

A live provider can legitimately solve this small task on its first attempt, or can exhaust the
attempt budget. The UI shows either result honestly; it does not manufacture a red → green story.

## Run the terminal demo

The terminal remains useful for a quick provider check or a compact walkthrough:

```bash
source .env
go run ./cmd/repair -attempts 3 -verifier-timeout 10s
```

It prints each verified attempt and ends as one of:

- `passed` — a candidate satisfied the frozen oracle.
- `gave up` — the attempt limit was reached with a real failed verification.
- `provider or verifier infrastructure failure` — configuration, provider, filesystem, process,
  or caller failure; this is not a verdict on the candidate code.

## Optional live provider smoke check

This explicit integration test sends one short, harmless completion request (with at most one
retry) and does not print the response or secrets:

```bash
LLM_LIVE_TEST=run go test -tags=integration ./internal/llm -run '^TestLiveCompletion$' -count=1 -v
```

## Documentation

- [Design](docs/go-repair-loop.md)
- [Architecture](docs/app-architecture.md)
- [Blind-oracle design change](docs/design-change-2026-07-18.md)
- [Project tracker](docs/tracker.md)
- [Repository layout](docs/file-structure.md)
