# Test Verifier

Test Verifier is a small, stdlib-first Go verification platform for model-produced code. It makes
one bounded workflow visible and reproducible:

```text
specification + pinned Go signature
        ↓
resolve, preflight, and freeze one verification bundle
        ↓
candidate code → compiled Go verifier → capped feedback → later candidate
```

The product is the frozen verification evidence, not the repair loop by itself. A repair loop is
still useful: a later candidate can use Go feedback to converge on the same fixed verifier. It
must never change that verifier to make a failure disappear.

Test Verifier is a trusted-local prototype. It is suitable for inspecting model behaviour and
verification evidence during development; it is not a sandbox for hostile code or a proof system.

## How verification works

Every run begins by producing one immutable `VerificationBundle` before candidate code exists.
The bundle contains:

- the exact Go test source used for every candidate attempt;
- a manifest with schema version, source origin, task digest, and bundle digest.

The task digest binds the bundle to the submitted specification and signature. The bundle digest
binds the complete manifest and executable source. The browser shows both, and downloaded run JSON
keeps the accepted evidence snapshot.

For a generated run, the dedicated `oracle` component gives the blind source author a checked-in,
versioned Rulebook. It directs the author toward generic validity, boundary, error, mutation,
determinism, round-trip, and metamorphic checks, and away from guessed non-trivial answer keys.
The Rulebook is prompt guidance, not executable verifier logic or a task-specific profile. Its
version/digest is recorded separately from the bundle, so it cannot silently change what frozen
test source means.

There are two legal oracle origins:

| Delivery path | Source origin | How it is created | What a green result means |
|---|---|---|---|
| Free-form browser task | `generated` | A blind test-writer sees only the task-specific specification and signature plus the checked-in universal Rulebook, then its source passes structural preflight. | The candidate satisfied a blind generated oracle. This is agreement, not verified correctness. |
| Terminal control | `authored` | A human-owned test source is preflighted and frozen. | The candidate passed that fixed authored oracle; confidence depends on the quality of the authored tests. |

The browser cannot submit test source, expected values, reference logic, or an executable rule
language. Its interactive path is always generated-oracle mode. Authored source enters only
through trusted local code, such as the terminal control.

## Runtime boundary

Generated and authored test source is preflighted against a signature-derived stub before code is
requested. The structural gate requires a runnable test with a type-resolved call to the pinned
function and a reachable `testing.T` failure path. It rejects obvious bypasses such as
`TestMain`, `init`, skipped tests, direct `os.Exit`, build constraints, and `go:embed`.
This admission gate does not prove that generated tests express the right behaviour.

For each candidate, Test Verifier:

1. builds a disposable module;
2. compiles its tests to a standalone test binary;
3. removes the source-bearing directory;
4. runs that binary from a separate directory with only `PATH` and a private `TMPDIR`;
5. requires a verifier-owned completion sentinel after the tests finish; and
6. caps verifier feedback at 128 KiB before it can enter a later candidate prompt.

This blocks ordinary source-file leaks and early-success exits from crossing the repair boundary.
Coder prompts have no test-source parameter, and ordinary runtime source reads are blocked. This
is deliberately **not** a confidentiality boundary or OS sandbox: deliberately hostile code can
still interact with the host or emit arbitrary text into verifier output. Do not expose it to
untrusted public code.

## Architecture

```text
browser ──→ server ──→ run ──┬─→ oracle ──→ prompt + llm + verification + domain
                              └─→ repair ──→ prompt + llm + verification + domain

cmd/repair ──→ llm/openai + concrete oracle resolver + concrete candidate executor
```

`cmd/repair` is the composition root. It reads configuration, constructs the configured LLM
adapter and model allowlist, constructs the fixed Rulebook, oracle resolver, and candidate
executor, and wires the HTTP server to the in-memory run store. The run store performs the
explicit resolver → validated frozen snapshot → candidate-executor sequence. Lower packages never
import HTTP or browser code.

The browser authoring flow saves only a task name, specification, and pinned signature in a
source-free template. Launching a run sends only that server-loaded template ID, an idempotency
token, and two locally configured model IDs. The server always creates a generated-oracle task.
The test-writer receives no candidate code. The code-writer receives no test-source
parameter—only capped feedback from the frozen verifier after a failed attempt.

## Prerequisites

- Go 1.26 or newer, with `go` on `PATH`. Go must remain on `PATH` at runtime because the verifier
  compiles and executes disposable Go modules.
- An OpenAI Chat Completions-compatible endpoint, network access, and credentials for a live run.
- A modern browser for the visual demo.
- Bash or Zsh for the shown `source .env` commands. In POSIX `sh`, use `. ./.env`.

The only direct third-party dependency is the pinned official
[`openai-go/v3`](https://github.com/openai/openai-go) SDK. There is no Node/npm tooling.

## Configure a provider

Copy the tracked template once, populate it with your own values, then load it:

```bash
[ -f .env ] || cp .env.example .env
# Edit .env, then:
source .env
```

The current composition root registers one provider adapter:

| Variable | Required | Meaning |
|---|---:|---|
| `LLM_PROVIDER` | Yes | Set to `openai`. |
| `LLM_BASE_URL` | Yes | OpenAI Chat Completions-compatible base URL; a path such as `/v1` is allowed. |
| `LLM_API_KEY` | Yes | Credential for that endpoint. |
| `LLM_MODEL` | Yes | Fallback model ID. |
| `LLM_TIMEOUT` | Yes | Go duration for one completion, including the adapter’s retry policy. |
| `LLM_MODELS` | No | Comma-separated browser model allowlist. |
| `LLM_MODEL_CODER` | No | Default candidate-code model; falls back to `LLM_MODEL`. |
| `LLM_MODEL_TESTER` | No | Default blind test-writer model; falls back to `LLM_MODEL`. |
| `LLM_MODEL_REVIEWER` | No | Default bounded oracle-reviewer model; falls back to the effective test-writer model. |

If `LLM_MODELS` is blank, the browser offers `LLM_MODEL` plus explicit role defaults. If it is
set, it must include all effective role defaults. The browser cannot discover provider models,
choose another provider, or select the reviewer; all options use the configured endpoint and
credential. Different role models can reduce correlated readings of an ambiguous specification,
but they do not establish correctness.

The verification platform adds no environment variables.

Keep `.env` private. It is ignored by Git and must never be committed. Use HTTPS for non-local
providers.

## Build and check the project

These commands do not contact a provider:

```bash
go build ./...
go test -count=1 ./...
go vet ./...
```

Useful development checks:

```bash
go test -race ./...
go test -cover ./...
go mod verify
gofmt -l $(find . -name '*.go' -not -path './_archive/*')
```

To make a product-named local executable while retaining the stable command directory:

```bash
go build -o test-verifier ./cmd/repair
./test-verifier -h
```

## Run the browser application

Load configuration and start the local server:

```bash
source .env
go run ./cmd/repair -serve \
  -attempts 3 \
  -oracle-attempts 2 \
  -verifier-timeout 10s \
  -run-timeout 2m30s \
  -log-level info \
  -log-color auto \
  -templates-dir templates
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080). Use `-addr 127.0.0.1:8090` for another
local address. Press `Ctrl-C` to stop the server.

To use it:

1. Open **Templates**. The repository includes five editable starter templates: Split cents, Word
   wrap, Compare semantic versions, Reverse ASCII text, and Deduplicate strings. Create another
   source-free task template—or edit a starter—and save its name, specification, and confirmed Go
   signature under `templates/<id>/template.json`. You may request a syntax-valid signature draft
   from the selected test-writer model, then explicitly apply or edit it; drafting never starts a
   run.
2. Open **Runs**, select the saved template, and choose locally configured code-writer and
   test-writer models. The server uses its configured reviewer model, falling back to the
   test-writer model when unset.
3. Start the generated-oracle run and follow its stable run-detail URL.
4. Read the run-evidence summary first: the precise result claim, task contract, generic oracle
   lifecycle/review evidence, and mechanical top-level `Test…` inventory. A passing run explains
   that silent Go output is expected—the candidate built and the frozen test binary completed.
   This is evidence of agreement with a fixed blind verifier, never proof of arbitrary
   correctness.
5. Expand the audit material when needed to inspect the exact frozen source, candidate attempts,
   capped feedback, manifest, and digests.
6. Download the final run JSON when the run reaches a terminal state.

Only one browser run may be live at a time. Browser runs are in memory and disappear when the
server restarts; templates remain in the project-root folder. A browser-generated idempotency token
makes a lost start response safe to retry.
The limits have separate jobs:

| Limit | Scope |
|---|---|
| `LLM_TIMEOUT` | One provider completion. |
| `-verifier-timeout` | One oracle compile preflight or the full candidate verification sequence: build, test-binary compile, and binary execution. |
| `-oracle-attempts` | Generated-oracle source attempts before `oraclefailed`; authored-source failures are configuration/infrastructure failures. |
| `-attempts` | Candidate attempts after the bundle is frozen. |
| `-run-timeout` | The whole browser run. |
| `-templates-dir` | Project-root directory used for source-free saved task templates. |

A cancellation, timeout, `oraclefailed`, or infrastructure failure is not a verdict that a
candidate implementation is wrong.

The server writes colored structured operational events to an interactive stderr. Set
`-log-level debug` while diagnosing a local issue; use `-log-color never` for a plain redirected
log or `-log-color always` when a terminal detector is unavailable. Logs identify HTTP status, selected model, durations, safe
signature-draft failure categories, and run phases without recording specifications, prompts,
source code, provider responses, endpoints, or credentials.

## Run the authored terminal control

The terminal command uses the fixed SplitCents task and human-authored oracle. It is a quick
provider/verification smoke path separate from the browser’s free-form workflow:

```bash
source .env
go run ./cmd/repair -attempts 3 -verifier-timeout 10s
```

It prints each completed candidate verification and ends as `passed`, `gave up`, or an
infrastructure failure.

## Optional live provider smoke test

This explicit integration test sends one short completion request, allows the adapter’s one retry,
and does not print provider responses or secrets:

```bash
source .env
LLM_LIVE_TEST=run go test -tags=integration ./internal/llm/openai \
  -run '^TestLiveCompletion$' -count=1 -v
```

## More detail

- [System design](docs/go-repair-loop.md)
- [Application architecture](docs/app-architecture.md)
- [Historical blind-oracle design change](docs/design-change-2026-07-18.md)
- [Implementation tracker](docs/tracker.md)
- [Repository layout](docs/file-structure.md)
