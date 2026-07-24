# Resume Handoff — 2026-07-24

## Completion update — 2026-07-24

This handoff has been completed. The interrupted `CandidateRequest` / `repair.Executor`
migration was finished without adding product scope: the stale test tuple calls were corrected,
the resolver → validation → snapshot → candidate-request handoff is covered by tests, and the
current design documents now show the request-based executor contract.

The final checks passed: `gofmt -l`, `go build ./...`, `go test -count=1 ./...`,
`go test -race ./...`, `go vet ./...`, `go mod verify`, `git diff --check`, and the active
task-routing audit. No provider request was made. This file is retained as a historical recovery
record; do not repeat the mechanical migration described below. Consult `docs/tracker.md` for
the next planned work, which remains F25/F26 and is not implemented.

The remainder preserves the deliberately honest in-progress record from before completion. At
that point the worktree was **mid-refactor and not known to build**. Do not reset, checkout,
delete, or discard unrelated changes.

## User intent to preserve

Test Verifier is a generic Go verification platform, not a collection of problem-specific
solutions.

- The oracle must be written blind to candidate code, frozen before candidate generation, and
  reused unchanged for the whole candidate-repair run.
- Generated oracle source may use only the task-specific specification, pinned Go signature, and
  checked-in universal Rulebook. It must not see candidate source.
- Candidate prompts may use specification, signature, previous candidate code, and capped Go
  feedback. They must never receive oracle source, Rulebook text, review material, or a task
  profile.
- The Rulebook is generic, non-executable prompt guidance. It must never become a task matcher,
  reference solver, expected-value store, DSL, or browser-supplied policy.
- Do not add MinCoins-specific code, profiles, branches, fixtures, or semantic routing. The only
  MinCoins material belongs under `_archive/` as history.
- Keep the program modular through narrow, explicit typed handoffs and manual construction in
  `cmd/repair`. Do not introduce a plugin registry, reflection, service locator, DI framework, or
  generic workflow/pipeline engine.
- The compiled-binary verifier remains important and generic. Do not replace it with task-specific
  checking or weaken its source-free execution boundary.

The user has specifically asked for a grounded platform, not scope creep. Do not start F25
(reviewer/revision pipeline), F26 (signature drafting), a committee, or any new product feature
while finishing this handoff item.

## What was already completed before the interruption

The main F24 direction was implemented as a generic refactor:

```text
browser → server → run → oracle.Resolve → frozen VerificationBundle → repair
```

1. `internal/oracle/` was added.
   - `oracle.Resolver` owns authored/generated source resolution before candidate work.
   - `oracle.Admitter` is the narrow deterministic structural-preflight seam.
   - Signature stub generation, AST/type-reachability checks, structural source admission, and
     generated-source retry limits moved out of `internal/repair`.
   - `oracle.OracleFailureError` is the typed exhausted-source outcome.
2. `internal/oracle/rulebook.go` adds checked-in `oracle-rulebook/v1` guidance and a stable digest.
   - It is appended only to generated source-author prompts.
   - Its version/digest and source-author-attempt count are stored as `oracle.Evidence`, separate
     from the executable bundle manifest/digest.
3. `internal/verification/` remains deliberately policy-free.
   - It seals and validates exact source, legal origin, task digest, and bundle digest.
4. `internal/run` was changed to resolve and snapshot the bundle before candidate work.
5. `internal/repair` was changed to candidate-side behavior rather than oracle authoring.
6. Browser requests still always construct generated-oracle tasks and reject unknown JSON fields,
   so browser users cannot submit source, a Rulebook, a profile, or a verifier override.
7. The docs, README, and AGENTS were updated substantially toward the then-current Frozen Oracle
   terminology and the generic platform boundary. The current product name is Test Verifier.

Before the later interrupted edits, these checks passed:

```text
go build ./...
go test -count=1 ./...
go test -race ./...
go vet ./...
go mod verify
git diff --check
```

That result does **not** apply to the current worktree after the unfinished changes below.

## Findings that triggered the follow-up edits

Three independent code reviews found real generic issues, not task-specific issues:

1. An injected `oracle.Resolver` could return a malformed bundle or a bundle whose manifest
   origin disagreed with the submitted `Task.Oracle`. `run` used to snapshot it before repair
   rejected it.
2. Authored runs briefly advertised candidate attempt `1` before an oracle had been admitted or a
   candidate existed.
3. A non-cooperative resolver could return after the run deadline and still cause a frozen bundle
   to be published, even though no candidate would run.

The intended generic fixes are sound and should be retained:

- `oracle.ValidateResolution(task, resolution)` validates bundle integrity, task binding,
  authored/generated origin, and generated-oracle evidence.
- `run` calls that validation before publishing a resolution or starting candidate work.
- `run` starts every run at `CurrentAttempt == 0`.
- `run` checks the context before resolution publication, so cancellation/deadline wins over a
  late resolver result.

## Current incomplete state

An additional small modularity seam was started after the reviews:

```text
run ──injects──> oracle.Resolver
run ──injects──> repair.Executor
```

This is intentionally only one real replacement boundary for the candidate side; it is not a
pipeline framework. `repair.DefaultExecutor` delegates to the existing `RepairWithConfig` loop.
`repair.CandidateRequest` carries only:

```go
type CandidateRequest struct {
    Spec      string
    Signature string
    Bundle    domain.VerificationBundle
}
```

The original `domain.Task` is no longer passed into candidate repair. This avoids accidentally
passing task/oracle metadata or duplicate raw authored test source into that API. The sealed bundle
still necessarily contains the frozen test source because the generic verifier must execute it;
the default candidate prompts must continue not to include it.

This seam is a reasonable completion of the user's modularity request. Finish it narrowly; do
not add more interfaces unless there is a demonstrated replacement boundary.

### Known compile failure

`internal/run/run_test.go` is presently inconsistent after `recordingExecutor.snapshot` changed
from three results to two:

```go
func (executor *recordingExecutor) snapshot() (int, repair.CandidateRequest)
```

These remaining call sites still destructure three values and must be changed to two:

- `internal/run/run_test.go:206`
- `internal/run/run_test.go:320`
- `internal/run/run_test.go:354`

They should become the equivalent of:

```go
if calls, _ := executor.snapshot(); calls != 0 {
    // ...
}
```

Run `gofmt` and focused tests immediately after that narrow repair. Expect there may be additional
mechanical compile errors from the `CandidateRequest` signature change; resolve only those related
to F24.

### Documentation currently needs a final consistency pass

Some docs already mention `repair.Executor`, but the implementation/documentation edit was
interrupted. In particular, verify and correct:

- `docs/app-architecture.md` contract snippets: they must show `repair.CandidateRequest`, not a
  raw `domain.Task` plus separate bundle.
- `docs/go-repair-loop.md` component contracts and the run handoff must show resolver validation
  before snapshot and the injected executor.
- `README.md`, `docs/file-structure.md`, and `docs/tracker.md` must accurately describe the
  concrete default executor without claiming a dynamic plugin system.
- `docs/tracker.md` currently marks F24 complete and includes historical verification wording.
  Update its verification record only after the final checks below pass. Do not claim the current
  worktree has passed them yet.

Also retain the clarification that a generated source author sees the task-specific spec/signature
**plus the checked-in universal Rulebook**. Older wording that says simply “spec + signature only”
is inaccurate after F24.

## Safe resume plan

1. Read `AGENTS.md`, `docs/go-repair-loop.md`, `docs/app-architecture.md`,
   `docs/file-structure.md`, and this handoff before editing.
2. Inspect `git status --short --branch`. The worktree contains unrelated, valid in-progress
   provider/OpenAI-SDK work as well as this F24 work. Preserve all of it.
3. Finish only the mechanical `CandidateRequest` / `repair.Executor` migration:
   - fix the three known test tuple mismatches above;
   - search `rg -n 'RepairWithConfig\\(|\.Execute\\(|NewStore\\(' --glob '!_archive/**' .` and
     update only stale F24 call sites;
   - ensure the default executor is manually constructed in `cmd/repair` and injected into
     `run.NewStore`;
   - keep `run` as the sole owner of resolver → validate → snapshot → executor ordering.
4. Preserve and test the generic behaviors found by review:
   - no candidate call before resolver completion;
   - no Rulebook/oracle-source leakage into candidate prompts;
   - invalid/mismatched resolver results create an infrastructure failure with no published bundle
     and no candidate work;
   - late resolution after deadline publishes nothing;
   - both authored and generated runs remain at attempt `0` until candidate generation.
5. Finish the documentation consistency pass described above. Keep F25 and F26 explicitly
   planned, not implemented.
6. Run the full verification suite, in this order:

   ```bash
   gofmt -w $(rg --files -g '*.go' -g '!_archive/**')
   go build ./...
   go test -count=1 ./...
   go test -race ./...
   go vet ./...
   go mod verify
   git diff --check
   ```

   If a command needs the local Go cache outside the sandbox, request the normal scoped Go-command
   approval; do not work around it by altering project files.
7. Run one final active-source audit before claiming completion:

   ```bash
   rg -n -i 'mincoins|minimum.?coin|task.?matcher|profile' \
     --glob '!_archive/**' --glob '!challenges/**' .
   ```

   Expected active matches are only intentional historical documentation references, not runtime
   routing or task-specific behavior. Do not remove archive/history merely to make this search
   empty.

## Important existing worktree context

- The tree is intentionally dirty. No commit was created during this session.
- Provider abstraction/OpenAI SDK changes are separate in-progress work. They include
  `internal/llm/openai/`, `go.mod`, `go.sum`, `.env.example`, and archived handwritten client
  files. Do not revert, delete, or reimplement them while completing F24.
- Archived MinCoins/profile experiments under `_archive/` are preserved by project policy and are
  excluded from the build.
- `docs/design-change-2026-07-18.md` is historical. Do not rewrite its old `resolveOracle`
  references as though it were the current architecture; its opening note already points readers
  to the current design documents.

## Definition of done for this handoff item

F24 is genuinely complete only when the worktree builds and the full checks pass, the docs match
the final explicit component boundaries, and the result still has no active task-specific routing.
The correct final story is:

```text
Task
  → oracle.Resolver
  → ValidateResolution
  → frozen VerificationBundle + separate evidence snapshot
  → repair.Executor (default: bounded Go candidate repair)
  → generic compiled-binary verifier
```

This is evidence of agreement with a frozen blind oracle, not a proof that arbitrary code is
correct.
