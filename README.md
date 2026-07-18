# Repair Loop

Repair Loop is a small, stdlib-first Go hackathon prototype for an honest
specify → generate → verify → repair workflow with a blind oracle. The immediate goal is a
transparent end-to-end loop: resolve an independent oracle, generate candidate code, verify it,
and show the repair attempts.

A human writes a spec and pins a Go signature. In `authored` mode, a human supplies the oracle;
in `generated` mode, a separate LLM context derives one from the spec and signature alone. The
Go toolchain checks candidate code against the frozen oracle in a disposable module; compiler or
test feedback becomes input to the next attempt until the solution passes or the attempt budget
is exhausted.

## The integrity rule

**The oracle is written blind to the code.** Whatever writes the tests must never have seen the
solution they judge. Two legal modes:

| Mode | Tests written by | A green run means |
|---|---|---|
| `authored` | a human, before the run | the code satisfies an independent oracle |
| `generated` | a second model, from spec + signature only | two readings of the spec agreed |

In both modes the oracle is fixed before the first attempt and frozen for the whole run. The
loop repairs the code; it never touches the test. Regenerating an oracle to rescue a failing
solution voids the guarantee.

`generated` is the research-inspired variation, not an additional prerequisite for the
hackathon demo. Persistent disagreement can surface different readings worth inspecting; it
does not diagnose the spec by itself. `authored` mode is the control and the deliberately
first-built path, so the core loop can be proved before a second LLM role is introduced.

The honest caveat: a generated oracle is derived from the same ambiguous prose the coder reads,
so both can misread it the same way and go green for the wrong reason. Running the two roles on
different models reduces that; it does not eliminate it. A green `generated` run is evidence of
agreement, not proof of correctness.

## How the loop works

1. Supply a task specification and required Go signature (plus a test file, in `authored` mode).
2. Resolve the oracle **once**: read the authored tests, or ask the test-writer for a test file
   from spec + signature alone. Check it compiles. Freeze it.
3. Ask the coder for a complete `package solution` source file.
4. Write the candidate and the frozen oracle into a temporary Go module.
5. Run `go build ./...`, then `go test ./...` if the build succeeds.
6. Send any compiler or test output back with the previous candidate for another attempt.

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

### Planned role overrides (F17)

When generated mode lands, the two roles will be configured separately, each falling back to
`LLM_MODEL` when unset:

| Variable | Role |
|---|---|
| `LLM_MODEL_CODER` | writes candidate solutions |
| `LLM_MODEL_TESTER` | writes oracles in `generated` mode |

Pointing them at different models is the decorrelation mitigation described above — it makes
it less likely that an ambiguous spec is misread identically by both. Setting them the same is
legal, and useful as a baseline.

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

- [Blind-oracle design change](docs/design-change-2026-07-18.md)
- [Design](docs/go-repair-loop.md)
- [Architecture](docs/app-architecture.md)
- [Project status and delivery plan](docs/tracker.md)
- [Repository layout](docs/file-structure.md)
