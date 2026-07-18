# Repair Loop

Repair Loop is a small, stdlib-first Go project exploring an honest
generate → verify → repair workflow.

A human defines a programming task and its fixed test oracle. An LLM produces only a
candidate Go solution. The Go toolchain verifies that candidate in a disposable module;
compiler or test feedback becomes input to the next attempt until the solution passes or
the attempt budget is exhausted.

## The integrity rule

The code-writing model never writes the tests. Task tests are human-authored and fixed before
generation, so a passing result is checked against an independent oracle rather than tests the
same model could have adapted to its own answer.

## How the loop works

1. Supply a task specification, required Go signature, and human-authored test file.
2. Ask the configured LLM for a complete `package solution` source file.
3. Write the candidate and fixed test into a temporary Go module.
4. Run `go build ./...`, then `go test ./...` if the build succeeds.
5. Send any compiler or test output back with the previous candidate for another attempt.

Generated code is untrusted text. The host may remove one unambiguous, complete code fence,
but never invents imports, package declarations, formatting, or a "fix" of its own; the Go
compiler remains the judge.

## Requirements

- Go 1.26 or newer.
- The `go` command on `PATH` for build and verification.
- An OpenAI-compatible provider only when running the optional live provider check.

## Local verification

Run the project's normal checks without contacting a provider:

```bash
go build ./...
go test ./...
go test -race ./...
go test -cover ./...
go vet ./...
gofmt -l $(rg --files -g '*.go')
```

## Provider configuration

Copy the tracked template once, add your own credentials locally, and load it into the shell:

```bash
[ -f .env ] || cp .env.example .env
# edit .env with your provider values
source .env
```

`LLM_BASE_URL` is an OpenAI-compatible API base URL; it may include a version path such as
`/v1`, and the client appends `/chat/completions`. Set `LLM_API_KEY`, `LLM_MODEL`, and
`LLM_TIMEOUT` alongside it. Keep `.env` private: it is Git-ignored and must never be
committed.

Use HTTPS for every non-local provider. The client permits `http://` only so local test and
development providers can work; never send a real key to a non-local plaintext endpoint.

### Optional live provider check

The integration check is deliberately opt-in, sends a short harmless prompt, and does not log
the provider response or secrets. It may make up to two provider requests under the current
retry policy.

```bash
LLM_LIVE_TEST=run go test -tags=integration ./internal/llm -run '^TestLiveCompletion$' -count=1 -v
```

## Documentation

- [Design](docs/go-repair-loop.md)
- [Architecture](docs/app-architecture.md)
- [Project status and delivery plan](docs/tracker.md)
- [Repository layout](docs/file-structure.md)
