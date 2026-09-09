# B Ops Console Lifecycle Refactor Implementation Plan

## Source Decisions

- [`map.md`](map.md): resolved terminal-experience scope, capability boundaries, and the B Ops Console direction.
- [`issues/05-prototype-vivid-terminal-visual-system.md`](issues/05-prototype-vivid-terminal-visual-system.md): prototype B visual system and `make prototype-terminal` as the visual reference.
- [`issues/06-choose-shared-terminal-experience-contract.md`](issues/06-choose-shared-terminal-experience-contract.md): terminal ownership, capability, redaction, stream, renderer-recovery, and service boundaries.
- [`issues/07-choose-root-help-and-error-presentation.md`](issues/07-choose-root-help-and-error-presentation.md) through [`issues/29-choose-upgrade-presentation.md`](issues/29-choose-upgrade-presentation.md): command-owned prompt, phase, result, redaction, side-effect, handoff, and Lifecycle Log decisions.
- [`issues/30-approve-rollout-and-acceptance-plan.md`](issues/30-approve-rollout-and-acceptance-plan.md): release evidence, behavioral baselines, capability modes, and acceptance gates.

## Outcome

All finite Rich commands use one production B Ops Console lifecycle. `OpenConsole` establishes the alternate screen before the first interaction and shows a complete safe Form Catalog. Starting work replaces that catalog with one complete Work Phase Catalog. `Finish` shows a command-owned safe Outcome Summary in the alternate screen for 800 ms, restores the primary screen, replays a structured stderr Interaction Transcript, and only then writes the existing stdout Result.

Service Commands keep their line-oriented Lifecycle Logs. `run` returns its parent terminal before the child process starts.

## Non-Negotiable Rules

- There is one production B renderer. Do not add a legacy/new UI switch, second renderer, or long-lived compatibility path.
- `Finish` receives a command-owned bounded `FinishRequest` containing `Outcome`, safe `Location`, and safe `Summary`.
- The same `FinishRequest` drives the final Live View and Transcript. Never derive either from stdout Result bytes.
- Add `ConsoleFormStep` Catalog and `InteractionRequest.ConsoleStepID`; Rich form rows are not dynamically appended as the formal model.
- A finite command submits one complete Work Phase Catalog. Merge existing segmented `Track` definitions and update flows before presentation.
- Form Catalog and Work Catalog are mutually exclusive current phases. They must never appear mixed in one table.
- Every state has both a symbol and text. Wide and narrow layouts retain order, safe detail, and an active region.
- Transcript replay is bounded, redacted, control-free, and has one outcome. It excludes keystrokes, animation, invalid secret input, and large Result documents.
- Preserve command parameters, Result schema and bytes, stdout/stderr ownership, exits, signals, side effects, permissions, redaction, and child-process boundaries.
- `diff`, `fs`, `tunnel connect`, and `tunnel server` do not become full-screen Consoles.
- Production starts with real `ACTIVE`/`PENDING` states. Prototype success/failure presets are demonstration controls, not production state.
- Outcome dwell is exactly 800 ms. The prototype's 700/800/900 ms branches are not a production contract.
- The Rich Work active region renders the prototype's Bubbles v2 `spinner.Meter` beside the current phase as an indeterminate liveness indicator. It resets for each Track or Work Catalog entry, ignores stale ticks outside Work, and never represents a percentage, ETA, or durable result.

## Gate Overview

| Gate | Name | Unlock condition | Outcome |
| --- | --- | --- | --- |
| G0 | Shared B lifecycle foundation | Start | Shared semantic lifecycle matches the prototype's pre-work, work, and outcome states. |
| G1 | Read-only and selection migration | G0 Exit conditions all met | Low-risk finite commands use complete Catalogs and safe Finish summaries. |
| G2 | Mutation and archive migration | G1 Exit conditions all met | Configuration, destructive, archive, and Git mutation behavior remains unchanged under the new lifecycle. |
| G3 | Handoff boundaries and release acceptance | G2 Exit conditions all met | Process/service boundaries and release acceptance are complete. |

## G0: Shared B lifecycle foundation

### Purpose

Replace the shared renderer's generic accumulation lifecycle with the prototype-aligned B Console semantic seam.

### Inputs

- `internal/terminal/prototype-vivid/README.md`, `internal/terminal/prototype-vivid/model.go`, `internal/terminal/prototype-vivid/view.go`, and `internal/terminal/prototype-vivid/theme.go`
- `internal/terminal/semantic.go`, `internal/terminal/experience.go`, `internal/terminal/rich.go`, `internal/terminal/tracked.go`, and `internal/terminal/transcript.go`
- Source decisions 05 and 06, terminal PTY tests, and terminal model tests

### Objective

Establish `ConsoleFormStep`, `InteractionRequest.ConsoleStepID`, `FinishRequest`, eager Rich alternate-screen startup, Form-to-Work Catalog replacement, an Outcome view with 800 ms dwell, and a structured Transcript renderer.

### Scope boundary

Only change `internal/terminal`, `internal/terminaltest`, direct tests, and this effort's control-plane artifacts. Do not change command business logic, Result schemas, Service Commands, or stdout content in this Gate.

### Constraints

- `OpenConsole` starts the renderer before semantic interaction only for Rich capability with a usable terminal. If startup fails before AltScreen/semantic state, fall back to Plain.
- The initial Form Catalog is complete and normalizes to first `ACTIVE`, remaining `PENDING`.
- `FinishRequest.Location` and `FinishRequest.Summary` pass terminal-owned length and control-character validation.
- Outcome dwell may be interrupted by renderer termination or context cancellation, but accepts neither a new form nor a second Finish.
- Transcript sections are exactly `ANSWERS`, `WORK`, optional `AT`, and `OUTCOME`; omit empty sections.

### Slice policy

1. Replace active control-plane artifacts and create the fresh runbook.
2. Add semantic Catalog and `FinishRequest` validation.
3. Start AltScreen eagerly and render the initial Catalog.
4. Replace Form Catalog with Work Catalog rather than accumulating rows.
5. Add Outcome state, 800 ms dwell, and restore order.
6. Partition Transcript rendering, reuse the summary, and add boundary tests.
7. Capture color, `NO_COLOR`, wide, narrow, and PTY evidence.

### Verification

#### Directed

- During each semantic slice, run `go test ./internal/terminal ./internal/terminaltest`.
- During renderer slices, run `cd internal/terminal/prototype-vivid && go test ./...`.
- Add PTY coverage for first input, completed form, success, failure, cancellation, primary-screen restoration, and stdout ordering.
- Exercise `120x40`, `70x20`, and `40x15` in colored and `NO_COLOR=1` modes.
- Verify dwell through a controllable clock or explicit test message; do not use long wall-clock sleeps.

#### Repository

1. Run `make command-surface` after shared API changes.
2. Run `make check-terminal` after the Gate's directed suite passes.
3. Run `make check` after `make check-terminal`.
4. Run `git diff --check` last.

#### Manual acceptance

- None. G0 establishes automated shared contracts only.

### Evidence rule

Every lifecycle Exit condition needs semantic/model evidence and at least one PTY assertion. Generic-renderer records from superseded work do not count as G0 evidence.

### Stop conditions

- A safe Catalog cannot be produced without leaking secrets, paths, URLs, or Result data.
- One Finish cannot preserve restoration, Transcript, stdout ordering, and at-most-once semantics.
- The implementation requires a second renderer or runtime UI switch.

### Rollback

Revert only the current shared semantic/model slice and its tests. Do not restore the generic renderer as a production alternative.

### Exit conditions

1. Before first interaction, a B shell and complete safe Form Catalog exist.
2. Starting work replaces the full table; form and work rows never mix.
3. A final Outcome remains in AltScreen and exits under the 800 ms contract.
4. One `FinishRequest` summary enters both Live View and Transcript without reading stdout Result.
5. Transcript section order, redaction, bounds, and restore/output order have tests.
6. Rich, Plain, Automation, redirected, cancellation, renderer-failure, lease, and recovery tests pass.
7. The fresh control plane is complete and old recovery plan/runbook/prompt/audit/evidence are not execution inputs.

## G1: Read-only and selection migration

### Purpose

Migrate low-risk finite commands to the new Catalog lifecycle and prove that the command adapter preserves discovery and Result behavior.

### Inputs

- G0 evidence and shared contracts
- Decisions 07, 08, 09, 12, 14, 17, 20, and 21
- Existing package, stream, cancellation, and redaction tests for the scoped commands

### Objective

Migrate `config fork list`, `config cm list`, `config cm use`, `config cm test`, `git heat`, `git pulse`, and `export env` to complete safe Catalogs, one Work Catalog, and command-owned Finish summaries. Keep root/help durable stdout-only and regression-test it.

### Scope boundary

Change only the scoped command adapters, their direct tests, and command-specific terminal projections. Do not change mutation commands, Service Command lifecycle implementations, arguments, Result schemas, or command policy.

### Constraints

- Each command supplies a safe Form Catalog or first Work Catalog before `OpenConsole`.
- Lists, JSON, empty results, errors, and cancellations are not copied as large Live View or Transcript data.
- Rich, Plain, Automation, and redirected capability boundaries remain unchanged.
- Root/help does not start AltScreen.

### Slice policy

Migrate one command at a time: descriptor/Catalog, Ask IDs, one Track catalog, Finish summary, then PTY and stream regression. Keep root/help as a separate regression-only slice.

### Verification

#### Directed

- For each command, run its package tests, terminal tests, and cancellation/redaction tests after its adapter slice.
- Add at least one successful, empty-result, failed, or cancelled PTY scenario as applicable.
- For root/help, cover version, completion, parser error, and write failure.

#### Repository

1. Run `make check-terminal` after every completed command slice.
2. Run `make command-surface` after root/help or parser-adjacent coverage.
3. Run `git diff --check` before Gate completion.

#### Manual acceptance

- None.

### Evidence rule

Each command must prove Form/Work Catalog behavior, final summary projection, stdout/stderr/exit compatibility, and a safe projection in all applicable capability modes.

### Stop conditions

- A Result, exit, parser, redaction, or side-effect regression appears.
- A command exposes a needed shared lifecycle change not covered by G0.

### Rollback

Revert the current command adapter and tests while preserving G0 and completed command slices.

### Exit conditions

1. All scoped commands no longer use dynamically appended form rows.
2. Each has success plus failed/cancelled/empty-result evidence as applicable.
3. Plain, Automation, redirected, and Transcript behavior remains compatible for every scoped command.
4. Root/help remains durable stdout-only with its specified parser and output regressions passing.

## G2: Mutation and archive migration

### Purpose

Apply the lifecycle to high-risk finite workflows without changing confirmation, mutation ordering, rollback, or output behavior.

### Inputs

- G1 evidence and shared lifecycle contracts
- Decisions 10, 11, 13, 15, 16, 22, 23, 24, and 28
- Existing mutation, destructive, archive, Git, side-effect, and redaction tests

### Objective

Migrate `config fork add/remove`, `config cm add/set/remove`, `rm`, `zip`, `git fork`, and `git cm` to complete Work Catalogs and safe Finish summaries.

### Scope boundary

Change only scoped command adapters, phase streams, safe projections, and direct behavior tests. Do not modify general shared lifecycle semantics, Result schemas, arguments, mutation policy, or Service Commands.

### Constraints

- Complete interaction and confirmation before the first side effect.
- Destructive cancellation, partial failure, archive failure, commit failure, and push failure identify a safe phase.
- Transcript excludes credentials, absolute paths, URLs, patches, raw errors, and large Result data.
- Existing mutation ordering, defaults, and rollback semantics remain unchanged.

### Slice policy

Migrate in risk order: configuration mutations, destructive cleanup, archive, then Git mutation. For each command, isolate Catalog, phase stream, Finish projection, and side-effect test changes.

### Verification

#### Directed

- Run each command package's unit, terminal, redaction, and side-effect tests after its slice.
- Verify default confirmation, cancellation, failure phase, and narrow-table replacement in PTY.
- Assert success stdout appears once; failure/cancellation does not produce synthetic success output.

#### Repository

1. Run `make check-terminal` after each risk group.
2. Run `make command-surface` after each risk group.
3. Run relevant acceptance tests before Gate completion.
4. Run `git diff --check` last.

#### Manual acceptance

- None.

### Evidence rule

Each command requires semantic outcome, side-effect ordering, Catalog replacement, Transcript ordering, and four-capability-mode evidence.

### Stop conditions

- A side effect occurs early, a default confirmation changes, a partial result is lost, or a safe projection cannot be proven.

### Rollback

Revert only the active command adapter slice. Do not revert G0/G1 or other completed migrations.

### Exit conditions

1. Every scoped mutation/archive command uses one complete Work Catalog and command-owned safe Finish summary.
2. Existing confirmation, cancellation, partial-failure, rollback, and output behavior has regression evidence.
3. Prototype-style Transcript ordering and redaction are proven in every applicable capability mode.

## G3: Handoff boundaries and release acceptance

### Purpose

Complete process/service boundary work and obtain final automated and visual acceptance without expanding service commands into a Console.

### Inputs

- G2 evidence
- Decisions 18, 19, 25, 26, 27, and 29
- Existing `run`, `upgrade`, Lifecycle Log, NDJSON, shutdown, process, and acceptance tests
- `internal/terminal/prototype-vivid` launched by `make prototype-terminal`

### Objective

Finish `run`, `upgrade`, `tunnel connect`, `diff`, `fs`, and `tunnel server` boundary work; remove migration-only dynamic-Catalog compatibility paths and stale active recovery evidence; complete release acceptance.

### Scope boundary

Change only the scoped parent-process adapters, lifecycle-log paths, necessary cleanup of migration compatibility code, direct tests, and evidence. Do not intercept child I/O, alter NDJSON schemas, or make Service Commands full-screen.

### Constraints

- `run` uses B Console only through selection/preparation, restores the parent terminal, replays Transcript, releases the renderer lease, then starts the child without capture, decoration, output rewriting, or exit-code changes.
- `upgrade` parent Finish/Transcript cannot block detached updater replacement, rollback, or startup transaction behavior.
- `tunnel connect` has bounded selection only; its continuing session remains a Lifecycle Log.
- `diff`, `fs`, and `tunnel server` remain Lifecycle Logs and preserve NDJSON/shutdown contracts.

### Slice policy

Treat each boundary separately: `run` handoff, `upgrade` transaction boundary, `tunnel connect` selection-to-log boundary, then `diff`/`fs`/`tunnel server` no-Console regression and cleanup.

### Verification

#### Directed

- Add real-child handoff coverage for `run` and verify parent restoration before exec.
- Test `upgrade` detached replacement, rollback, cleanup warning, and startup transaction independently of Outcome dwell.
- Test `tunnel connect` selection plus Lifecycle Log continuation, and verify `diff`, `fs`, and `tunnel server` never allocate a full-screen Console.
- Verify Lifecycle Log ordering, NDJSON, shutdown, redaction, signal, and stream behavior.

#### Repository

1. Run `make check-terminal`.
2. Run `make acceptance-terminal`.
3. Run `make acceptance`.
4. Run `make command-surface`.
5. Run `make check`.
6. Run `make cross-build`.
7. Run `make prototype-terminal` as the final visual reference environment.

#### Manual acceptance

- In colored and `NO_COLOR=1` modes, inspect one multi-step form at first render, after Form-to-Work replacement, and at successful, failed, and cancelled Outcomes with 800 ms dwell and primary-screen restoration.
- Inspect stderr order `ANSWERS`, `WORK`, optional `AT`, `OUTCOME`, followed by stdout Result; cover `120x40` and `40x15`.
- Inspect `run` child handoff and verify Service Commands do not show the full-screen Console.
- Reply exactly `通过: <场景列表>` or `未通过: <场景及证据>`.

### Evidence rule

G3 passes only with the automated matrix, cross-platform build, PTY/stream evidence, and one explicit manual-acceptance response covering the listed scenarios.

### Stop conditions

- Child handoff captures or rewrites output.
- A Service Command receives a full-screen UI.
- Any stdout, NDJSON, exit, signal, side-effect, or redaction regression occurs.
- Manual acceptance cannot distinguish pre-work, working, and outcome states.

### Rollback

Revert by command boundary. Revert G0 only for a demonstrated shared-contract regression, preserving unrelated command migrations.

### Exit conditions

1. All finite Rich commands have migrated Catalogs, Finish summaries, Outcome dwell, and Transcript behavior.
2. `run`, `upgrade`, and Service Command boundaries have their specialized evidence.
3. Dynamic-Catalog compatibility and stale active recovery evidence are removed from execution paths.
4. Repository, acceptance, cross-build, PTY/stream, and required manual acceptance all pass.

## Definition Of Done

- G0 through G3 pass in order, and the runbook records evidence for every Exit condition.
- The production lifecycle matches B Ops Console behavior from `make prototype-terminal`: before interaction, during work, Outcome dwell, restoration, and replay.
- Result, exit, signal, side-effect, redaction, and child-process contracts remain compatible.
- This `implementation-plan.md`, `goal-runbook.md`, and `goal-prompt.md` are the only active control plane.
- Superseded recovery plans, prompts, audits, and stale G0 evidence are not execution inputs.

## Meter Parity Acceptance

- `richTrackMode` uses `spinner.Meter` with the active B visual role beside the current phase; phase details and cancellation text retain their existing projection.
- Starting a normal Track or controlled Work Catalog starts a fresh Meter. Returning from a controlled Form starts a new Meter, so tick messages from the prior instance cannot animate the resumed Work view.
- Form, Outcome, and renderer-close paths ignore late Meter ticks. Meter frames never enter the Interaction Transcript, stdout Result, Plain Interactive, Automation, or Service Command Lifecycle Logs.
- Model coverage proves initial frame, frame advance, reset, stale-tick rejection, and controlled Work/Form resumption. Rich PTY coverage proves an observable Meter frame at `120x40`, `70x20`, and `40x15`, with and without color, while preserving primary-screen restoration and stream ordering.

## Explicitly Out Of Scope

- Changing command arguments, business rules, Result schemas, exit behavior, or secret policy.
- Converting Service Commands into full-screen dashboards.
- Generating Live View or Transcript content from stdout Result.
- Creating a new terminal-emulator compatibility matrix.
- Retaining a permanent legacy/new renderer split.
- Modifying `superseded/` history, resolved map, issues, research, inventories, or baselines after this control-plane reset.
