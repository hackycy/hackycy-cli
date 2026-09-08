# Terminal Experience Modernization Recovery Implementation Plan

## Source Decisions

- [`map.md`](map.md) defines the command inventory, behavioral invariants, and
  B / `OPS CONSOLE` as the sole finite Rich visual contract.
- [`b-ops-console-audit.md`](b-ops-console-audit.md) records why former Gate
  evidence cannot establish B visual conformance.
- [`issues/01-research-latest-charm-v2-stack.md`](issues/01-research-latest-charm-v2-stack.md),
  [`research/01-latest-charm-v2-stack.md`](research/01-latest-charm-v2-stack.md),
  [`issues/02-research-exemplary-cli-terminal-experiences.md`](issues/02-research-exemplary-cli-terminal-experiences.md),
  and [`research/02-exemplary-cli-terminal-experiences.md`](research/02-exemplary-cli-terminal-experiences.md)
  define the compatible Charm stack and applicable terminal patterns.
- [`issues/03-inventory-every-command-terminal-surface.md`](issues/03-inventory-every-command-terminal-surface.md),
  [`inventories/03-current-command-terminal-surfaces.md`](inventories/03-current-command-terminal-surfaces.md),
  and [`issues/04-choose-charm-v2-and-logging-boundaries.md`](issues/04-choose-charm-v2-and-logging-boundaries.md)
  define the complete command surface, import, renderer, and logging boundaries.
- [`issues/05-prototype-vivid-terminal-visual-system.md`](issues/05-prototype-vivid-terminal-visual-system.md),
  [`internal/terminal/prototype-vivid/README.md`](../../internal/terminal/prototype-vivid/README.md),
  [`internal/terminal/prototype-vivid/view.go`](../../internal/terminal/prototype-vivid/view.go),
  and [`internal/terminal/prototype-vivid/theme.go`](../../internal/terminal/prototype-vivid/theme.go)
  define the accepted B reference.
- [`issues/06-choose-shared-terminal-experience-contract.md`](issues/06-choose-shared-terminal-experience-contract.md)
  defines the Experience, phase, Transcript, stream, and terminal lifecycle.
- [`issues/07-choose-root-help-and-error-presentation.md`](issues/07-choose-root-help-and-error-presentation.md)
  through [`issues/29-choose-upgrade-presentation.md`](issues/29-choose-upgrade-presentation.md)
  define command-owned presentation and compatibility contracts.
- [`issues/30-approve-rollout-and-acceptance-plan.md`](issues/30-approve-rollout-and-acceptance-plan.md)
  defines rollout and acceptance policy.
- [`baselines/README.md`](baselines/README.md), [`baselines/manifest.json`](baselines/manifest.json),
  [`CONTEXT.md`](../../CONTEXT.md), [`CLAUDE.md`](../../CLAUDE.md),
  [`docs/project-layout.md`](../../docs/project-layout.md), and
  [`docs/platform-boundaries.md`](../../docs/platform-boundaries.md) constrain
  behavior regression, terminology, ownership, and platform evidence.

## Outcome

Every finite Rich ycy command presents the accepted B / `OPS CONSOLE` shell,
not a generic Huh form or phase list. The shell has a stable command/status
bar, one safe metadata row, an aligned `STATE / PHASE / DETAIL` table, and an
active form or result region. It uses the B palette, paired state symbols, B
Huh bottom focus, and B compact single-column degradation.

Root and help remain durable stdout documents without AltScreen, but use the
same B hierarchy where their decision requires it. `diff`, `fs`, `tunnel
connect`, and `tunnel server` remain line-oriented Lifecycle Logs; only a
bounded finite selection or parent flow may use the Console. Existing
arguments, stream bytes, results, exits, side effects, redaction, signal, and
child-process boundaries remain unchanged.

## Non-Negotiable Rules

- The default `make prototype-terminal` B / `console` journey is the visual
  reference. Variant A and C are comparison material only and cannot become a
  production fallback.
- A title, AltScreen, Huh form, spinner, or generic Rich PTY test alone is not
  B evidence. A finite Rich screen must contain all B structural elements.
- `internal/terminal` is the only production owner of Bubble Tea, Huh, Lip
  Gloss, color-profile handling, and renderer modes. Commands supply semantic
  context, phases, interactions, safe details, and Results, never Charm models
  or styles.
- The production renderer has one B implementation. Do not add a legacy UI
  flag, A/C selection, generic fallback, parallel renderer, or terminal-emulator
  compatibility branch.
- At wide size, retain the B bar, metadata, divider, aligned table, and active
  region. Below the prototype `70x20` threshold, retain the heading, paired
  symbols and labels, ordered rows, safe details, and active region in one
  column. Do not collapse to only the current phase.
- Use B amber `#FFB454` for primary/active hierarchy and cyan `#4CC9F0` for
  focus/accent. Green, yellow, red, and muted gray retain issue-05 meanings.
  Every state retains a textual label and paired symbol with or without color.
- Rich Huh forms use the B theme and bottom-focus rule. The Console has no
  persistent left rail. Metadata, form values, details, and Transcript fields
  stay bounded and command-owned safe projections.
- Preserve the Experience lifecycle, renderer lease, restoration and replay
  ordering, exactly-once Finish, Automation isolation, and command Result
  boundary from issue 06.
- Former behavior and semantic tests remain regression inputs, but no former
  Gate pass or human handoff is imported as B visual acceptance.
- Service Commands remain neutral, line-oriented Lifecycle Logs. They do not
  become B full-screen Consoles.
- Preserve existing dirty worktree changes unless a narrowly scoped recovery
  slice changes them. Never weaken, skip, delete, or regenerate tests or frozen
  command-surface artifacts to obtain a pass.

## Gate Overview

| Gate | Name | Unlock condition | Outcome |
| --- | --- | --- | --- |
| G0 | Production B Ops Console foundation | Start | Shared Rich rendering implements B with executable wide/narrow evidence. |
| G1 | Root and low-risk finite recovery | G0 Exit conditions all satisfied | Root/help and old low-risk finite commands use B without behavior drift. |
| G2 | External-read and service-boundary recovery | G1 Exit conditions all satisfied | External finite commands use B while `diff` and `fs` remain Lifecycle Logs. |
| G3 | Mutation and archive recovery | G2 Exit conditions all satisfied | Existing configuration, destructive, and archive routes use B without safety drift. |
| G4 | Git mutation and process handoff | G3 Exit conditions all satisfied | Git Fork, Git CM, and Run use B for finite parent work and preserve handoff. |
| G5 | Service lifecycles and detached updater | G4 Exit conditions all satisfied | Tunnel/updater work completes without expanding B into service lifecycles. |
| G6 | Release verification and full visual review | G5 Exit conditions all satisfied | All behavior and B/Lifecycle Log evidence is release-grade. |

## G0: Production B Ops Console foundation

### Purpose

Replace the generic shared Rich composition with one production implementation
of B before any command is treated as visually migrated.

### Inputs

- `b-ops-console-audit.md`
- `issues/04-choose-charm-v2-and-logging-boundaries.md`
- `issues/05-prototype-vivid-terminal-visual-system.md`
- `issues/06-choose-shared-terminal-experience-contract.md`
- `internal/terminal/prototype-vivid/{README.md,model.go,theme.go,view.go}`
- `internal/terminal/{rich.go,rich_form.go,presentation.go,tracked.go,interaction.go,experience.go,transcript.go}`
- Existing `internal/terminal` and `internal/terminaltest` tests

### Objective

Implement a semantic production B Console renderer in `internal/terminal`. It
must accept a command-owned safe Console descriptor before the first Rich form,
Notice, or Track and render the B bar, metadata, status table, active region,
compact layout, and B Huh theme without changing Experience behavior.

### Scope boundary

Only `internal/terminal`, `internal/terminaltest`, their direct tests, and
necessary architecture/import checks may change. Do not adapt a command
package, alter wording, change a Result, or modify a Service Command. Existing
command calls remain routed through a B-compatible internal default descriptor
until their own recovery Gate; they are not visually accepted until then.

### Constraints

- The descriptor contains semantic command identity, safe target/status
  context, and bounded metadata, never a Charm model.
- Notice content is transient active-region context. It cannot accumulate as a
  competing header or displace the table.
- Reached form steps and Work Phases render in catalog order. Wide rows use
  fixed state/phase columns; compact rows retain the heading, symbols, labels,
  order, and safe details.
- The active Huh form or small outcome is below the table. Large Results remain
  stdout-only and are not copied into the Console or Transcript.
- Direct Rich document rendering used by root/help receives B roles without
  starting AltScreen or changing durable content.

### Slice policy

Implement one reversible renderer behavior at a time: descriptor/state model;
wide shell; compact shell; B palette and Huh theme; form-step projection;
phase-table projection; direct durable B hierarchy; then normalized model and
PTY evidence. Do not combine a renderer slice with a command migration.

### Verification

#### Directed

- After each renderer slice, run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/terminal ./internal/terminaltest`.
- Extend model and PTY tests to assert B bar, metadata, heading, state rows,
  active region, paired symbols, B ANSI palette with color, no ANSI with
  `NO_COLOR=1`, no rail, compact preservation, and restoration/replay order.
- Run `cd internal/terminal/prototype-vivid && GOTOOLCHAIN=go1.26.7 GOWORK=off go test ./...` after an interpretation-affecting renderer change.
- Use `make prototype-terminal` as the visual reference only. Do not transplant
  its throwaway model or compare raw ANSI frame bytes.

#### Repository

1. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/terminal ./internal/terminaltest` after the final shared slice.
2. Run `make command-surface` and `make check` in that order.
3. Run `git diff --check`.

#### Manual acceptance

- 无。G0 仅自动化验证；首次生产 B 人工验收在 G1。

### Evidence rule

Focused model/PTY tests prove B structure and Experience lifecycle. Prototype
module tests prove the reference is buildable. Repository checks prove import,
behavior, and formatting stability. Former generic-renderer visual records do
not prove a G0 Exit condition.

### Stop conditions

- A B element cannot be represented through semantic command-owned data without
  exposing unsafe values or importing Charm into a command.
- B rendering cannot restore terminal modes, preserve Transcript ordering, or
  keep stdout independent from Live View.
- A proposal needs a legacy/new runtime switch or contradicts an accepted B
  rule.

### Rollback

Revert only the failing shared-renderer slice and its tests. Do not re-enable a
generic production Rich path as an alternative; repair the G0 renderer before
any command migration.

### Exit conditions

1. The Rich root has a semantic Console descriptor and renders B bar, safe
   metadata, divider, wide status table, and active region.
2. Form and Track states render in B order with paired labels/symbols, B
   palette, bottom Huh focus, and no rail.
3. Compact rendering follows the prototype `70x20` boundary and retains B
   heading, rows, details, and active region in one column.
4. Rich/Plain/Automation/redirected, Transcript, lease, cancellation,
   recovery, and exactly-once evidence passes.
5. G0 repository checks pass.

## G1: Root and low-risk finite recovery

### Purpose

Requalify root discovery and the former low-risk finite cohort against the
production B renderer before relying on any previous command visual record.

### Inputs

- `issues/07-choose-root-help-and-error-presentation.md`
- `issues/09-choose-config-fork-list-presentation.md`
- `issues/12-choose-config-cm-list-presentation.md`
- `issues/14-choose-config-cm-use-presentation.md`
- `issues/20-choose-git-heat-presentation.md`
- `pkg/cmd/root`, `pkg/cmd/config/fork/list`, `pkg/cmd/config/cm/list`,
  `pkg/cmd/config/cm/use`, and `pkg/cmd/git/heat`
- G0 Console descriptor, renderer, and test contracts

### Objective

Make root/help and `config fork list`, `config cm list`, `config cm use`, and
`git heat` supply command-owned B context and show their specified
loading/form/phase states in B without changing durable documents or behavior
baselines.

### Scope boundary

Change only the listed presentation adapters, their tests, and small
integration fixes required to use the frozen G0 Console seam. Do not change
configuration schemas, list ordering, Git inspection logic, Result fields,
Cobra grammar, root error semantics, or any G2+ command.

### Constraints

- Root/help remains a durable stdout document without AltScreen or Transcript.
  Its color-capable hierarchy is B-derived while usage, commands, options,
  examples, version, completion, parser, and write-error contracts stay exact.
- Each finite command configures B context before its first visible operation,
  preserves safe metadata ownership, and keeps large lists/reports out of the
  Transcript.
- Loading and Git inspection phases remain truthful. Rich is B; Plain and
  Automation remain control-free and behavior-compatible.

### Slice policy

Use one presentation adapter per slice: root/help, fork list, CM list, CM use,
then Git heat. Within a command, keep descriptor/context, phase/form
projection, result layout, and PTY/stream regression evidence independently
reviewable.

### Verification

#### Directed

- For each slice, exercise success, empty, failure, cancellation where
  applicable, redaction, exact Result bytes, and side-effect boundaries.
- Add structural Rich PTY assertions at `120x40` and `40x15` with color and
  `NO_COLOR=1`; assert B bar/metadata/table/active-region order, not merely
  command text or AltScreen escape sequences.
- Exercise root/help in color and redirected modes, including version,
  completion, parser error, and write-failure contracts.

#### Repository

1. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./pkg/cmd/root ./pkg/cmd/config/fork/list ./pkg/cmd/config/cm/list ./pkg/cmd/config/cm/use ./pkg/cmd/git/heat ./internal/terminal`.
2. Run `make command-surface` and `make check` in that order.
3. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 -tags=acceptance ./acceptance -run '^(TestConfigForkListStandaloneBinary|TestConfigCMListStandaloneBinary|TestConfigCMUseStandaloneBinary|TestGitHeatStandaloneBinary)'`.
4. Run `git diff --check`.

#### Manual acceptance

- Build a local binary and record its temporary fixture in the Progress Log.
  In a colored `120x40` TTY and then a `40x15` TTY with `NO_COLOR=1`, inspect
  root/help and the four commands named above.
- Confirm B hierarchy, safe metadata, complete wide/compact rows, readable
  symbols without color, no rail, AltScreen restoration for finite commands,
  bounded Transcript, and unchanged durable Result.
- Reply once with `通过: <commands>` or `未通过: <command and reason>`.

### Evidence rule

Command tests and tagged journeys prove behavior preservation. Structural PTY
tests prove production B rendering. Manual review proves the colored/no-color
command experience, not the throwaway prototype alone.

### Stop conditions

- A root/help style removes or reorders a required discovery field.
- A command cannot provide safe metadata without leaking a list, path, token,
  URL, or internal value.
- A shared-renderer defect requires reopening the G0 contract.

### Rollback

Revert the individual command adapter and its B tests. Preserve G0 and other
command slices; do not restore a command-specific generic renderer.

### Exit conditions

1. Root/help preserves its complete durable and parser/version/completion
   contract while using the required B hierarchy where styling is allowed.
2. Fork list, CM list, CM use, and Git heat each pass fresh B structural,
   capability, behavior, redaction, and Transcript evidence.
3. The G1 manual review explicitly accepts the listed command scenarios.
4. G1 repository checks pass.

## G2: External-read and service-boundary recovery

### Purpose

Recover the former external-read finite commands into B while proving the
shared repair does not turn Service Commands into full-screen dashboards.

### Inputs

- `issues/08-choose-export-env-presentation.md`
- `issues/17-choose-config-cm-test-presentation.md`
- `issues/18-choose-diff-lifecycle-log-presentation.md`
- `issues/19-choose-fs-lifecycle-log-presentation.md`
- `issues/21-choose-git-pulse-presentation.md`
- `issues/29-choose-upgrade-presentation.md`
- `pkg/cmd/export/env`, `pkg/cmd/config/cm/test`, `pkg/cmd/git/pulse`,
  `pkg/cmd/upgrade`, `pkg/cmd/diff`, and `pkg/cmd/fs`

### Objective

Make `export env`, `config cm test`, `git pulse`, and the parent `upgrade`
flow B-complete. Verify that `diff` and `fs` retain bounded text and NDJSON
Lifecycle Logs with no B AltScreen conversion.

### Scope boundary

Change only the listed adapters, presentation tests, service lifecycle tests,
and narrow integration fixes needed by the frozen Console seam. Do not change
dotenv semantics, provider requests, Git discovery, update transactions,
service HTTP/API behavior, NDJSON schema, or G3+ commands.

### Constraints

- Finite commands provide safe B descriptors and phase catalogs. Results, JSON,
  provider content, URLs, paths, and token projections retain their existing
  decision boundaries.
- `diff` and `fs` remain line-oriented, bounded, neutral, and schema-stable.
  They do not enter AltScreen, replay a Transcript, or gain a Console table.
- Parent `upgrade` B output ends before detached handoff; child/updater process
  ownership and startup output remain independent.

### Slice policy

Recover one finite adapter at a time: export env, CM test, Git pulse, parent
Upgrade. Treat diff and FS as independent no-Console regression slices. Keep
phase/context projection separate from provider and service logic.

### Verification

#### Directed

- Exercise finite success, malformed/timeout/provider failure, cancellation,
  Automation, redaction, Result, and B PTY states specified by each decision.
- Exercise diff and FS text/NDJSON startup, task/refresh, failure, shutdown,
  and no-AltScreen behavior.
- Run colored and `NO_COLOR=1` structural PTY checks for every finite form or
  phase family at wide and compact dimensions.

#### Repository

1. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./pkg/cmd/export/env ./pkg/cmd/config/cm/test ./pkg/cmd/git/pulse ./pkg/cmd/upgrade ./pkg/cmd/diff ./pkg/cmd/fs ./internal/terminal`.
2. Run `make command-surface` and `make check` in that order.
3. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 -tags=acceptance ./acceptance -run '^(TestExportEnvStandaloneBinary|TestConfigCMTestStandaloneBinary|TestGitPulseStandaloneBinary|TestDiffStandaloneBinary|TestFSStandaloneBinary)'`.
4. Run `git diff --check`.

#### Manual acceptance

- Record a local binary and fixture entry. Review each finite command in
  colored `120x40` and `NO_COLOR=1` `40x15` PTYs, then one text and one NDJSON
  session for both diff and FS.
- Confirm B table/form/phase behavior only for finite flows and ordered,
  bounded, control-free Lifecycle Log behavior for services.
- Reply once with `通过: <scenarios>` or `未通过: <scenario and reason>`.

### Evidence rule

Focused command/service tests prove semantics and protocol boundaries; B PTY
tests prove finite presentation; service text/NDJSON tests prove the exclusion;
manual review proves both actual user-facing modes.

### Stop conditions

- A finite B integration changes provider, JSON, update, or Result semantics.
- A service needs an AltScreen or Transcript to satisfy a visual assertion.
- A service log changes stable NDJSON schema, record ordering, or shutdown
  ownership.

### Rollback

Revert the affected finite adapter or Service Command regression slice only.
Keep G0/G1 and unrelated G2 slices intact.

### Exit conditions

1. Export env, CM test, Git pulse, and parent Upgrade pass fresh B structural,
   behavior, redaction, cancellation, and stream evidence.
2. Diff and FS pass Lifecycle Log text/NDJSON/no-Console regression evidence.
3. The G2 manual review explicitly accepts the recorded finite and service
   scenarios.
4. G2 repository checks pass.

## G3: Mutation and archive recovery

### Purpose

Recover the already worked-on configuration, destructive, and archive routes
so their real Huh interactions and phase states appear through B without
changing validation-first or mutation behavior.

### Inputs

- `issues/10-choose-config-fork-add-presentation.md`
- `issues/11-choose-config-fork-remove-presentation.md`
- `issues/13-choose-config-cm-add-presentation.md`
- `issues/15-choose-config-cm-set-presentation.md`
- `issues/16-choose-config-cm-remove-presentation.md`
- `issues/24-choose-rm-presentation.md`
- `issues/28-choose-zip-presentation.md`
- `pkg/cmd/config/{fork,cm}`, `pkg/cmd/rm`, and `pkg/cmd/zip`
- Current dirty command changes, retained as recovery input rather than
  assumed acceptance evidence

### Objective

Make `config fork add/remove`, `config cm add/set/remove`, `rm`, and `zip`
show command-owned form, confirmation, planning, mutation, and archive states
in B while preserving every established safety, persistence, archive, and
Result contract.

### Scope boundary

Only the listed adapters, direct orchestration needed for the semantic Console
descriptor, and focused tests may change. Do not change configuration schemas,
encryption, validation grammar, confirmation defaults, deletion/collection
algorithms, filename/glob semantics, archive bytes, revealer behavior, or
unrelated worktree changes.

### Constraints

- Each command supplies bounded safe context before its first form/phase and
  makes every reached form step or Work Phase visible in B catalog order.
- Default-No confirmation, Esc cancellation, context cancellation, and
  Automation rejection remain distinct and preserve existing side-effect
  boundaries.
- Credentials, tokens, unsafe Host/URL values, absolute paths, raw errors,
  hidden traversal, and file lists stay out of metadata, active region,
  Transcript, and inappropriate streams.
- The superseded G5 manual handoff is not evidence. A new handoff may be
  created only after this Gate's B automation is green.

### Slice policy

Use one command at a time. Within each command, separately review B descriptor
and form catalog, phase-table projection, Result/Transcript projection, then
PTY and behavior regression evidence. Keep reading, mutation, and presentation
changes distinct unless truthfully emitting a state needs them together.

### Verification

#### Directed

- Exercise every specified text, secret, select, multiselect, confirmation,
  invalid retry, default, overwrite, cancellation, Automation, failure,
  partial mutation, no-file, archive-result, and redaction path.
- Assert B wide/compact layouts in real PTYs for form and tracked work with
  color and `NO_COLOR=1`, including restoration and Transcript/Result ordering.
- Verify configuration ordering/encryption, deletion effects, archive bytes,
  entries, permissions, and host reveal behavior remain unchanged.

#### Repository

1. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./pkg/cmd/config/... ./pkg/cmd/rm ./pkg/cmd/zip ./internal/terminal`.
2. Run targeted `-race` evidence for every touched mutation package.
3. Run `make command-surface` and `make check` in that order.
4. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 -tags=acceptance ./acceptance -run '^(TestConfigForkAddStandaloneBinary|TestConfigForkRemoveStandaloneBinary|TestConfigCMAddStandaloneBinary|TestConfigCMSetStandaloneBinary|TestConfigCMRemoveStandaloneBinary|TestRMStandaloneBinary|TestZIPStandaloneBinary)'`.
5. Run `git diff --check`.

#### Manual acceptance

- Create and record a fresh local binary plus disposable configuration and
  project fixtures. Review all seven commands in colored `120x40` and
  `NO_COLOR=1` `40x15` PTYs, including one cancellation and one Automation
  invocation.
- Confirm B form focus, metadata, ordered rows, destructive wording,
  secret/path safety, restoration, exactly one durable Result, and no mutation
  after cancellation or Automation rejection.
- Reply once with `通过: <commands>` or `未通过: <command and reason>`.

### Evidence rule

Command tests and tagged standalone journeys prove safety and compatibility.
Structural PTY tests prove B. One manual review proves the real destructive and
archive experience across color and compact modes.

### Stop conditions

- A B metadata, phase, or form change can leak a credential, unsafe path,
  remote, raw error, or archive detail.
- Automation or cancellation can cause a later side effect.
- A change alters archive bytes, configuration storage, confirmation defaults,
  deletion ordering, or partial-success semantics.

### Rollback

Revert the affected command slice and tests only. Preserve prior recovery
slices and unrelated dirty worktree changes.

### Exit conditions

1. Every listed configuration, destructive, and archive command passes fresh
   B form/phase/Transcript/PTY evidence and preserves its behavior baseline.
2. Mutation, cancellation, Automation, redaction, archive/deletion, and race
   evidence passes for every applicable command.
3. The G3 manual review explicitly accepts the recorded command scenarios.
4. G3 repository checks pass.

## G4: Git mutation and process handoff

### Purpose

Deliver uncompleted Git mutation and `run` handoff work on the recovered B
foundation without letting the Console retain ownership of a child terminal.

### Inputs

- `issues/22-choose-git-fork-presentation.md`
- `issues/23-choose-git-cm-presentation.md`
- `issues/25-choose-run-handoff-presentation.md`
- `pkg/cmd/git/fork`, `pkg/cmd/git/cm`, `pkg/cmd/run`, and `internal/gitprocess`
- G0 B renderer and G1-G3 recovery evidence

### Objective

Implement or recover B Console presentation for Git Fork, Git CM, and finite
Run selection while preserving archive/fallback, Git mutation, partial failure,
child I/O, terminal release, cwd, signal, and exit-code contracts.

### Scope boundary

Only Git Fork, Git CM, Run, process adapters, direct tests, and narrowly
necessary terminal integration may change. Do not alter Git grammar, archive
provider behavior, staging/commit/push policy, child argv, or service work.

### Constraints

- Git Fork and Git CM use B only for finite selection, confirmation, review,
  and phases. Mutation values and partial accounting remain command-owned.
- Run exits B, freezes/replays its parent Transcript, releases renderer and
  terminal ownership, then starts the selected child with untouched inherited
  stdin/stdout/stderr/cwd and exact child exit behavior.
- No child output, Git credential, raw Git error, or large change list enters
  Console metadata or the parent Transcript.

### Slice policy

Implement Git Fork, Git CM, and Run as independent slices. For Run, separate
finite B selection/replay from release-before-exec and inherited-child tests.

### Verification

#### Directed

- Exercise archive-first/fallback, destination-replacement confirmation,
  staging/message/commit/push choices, partial Git failures, cancellation, and
  safe B projection.
- Exercise a real Run child through a PTY and assert all B output ends before
  child startup, with no parent decoration after handoff.
- Run wide/compact color/no-color B PTY evidence for each finite interaction.

#### Repository

1. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./pkg/cmd/git/fork ./pkg/cmd/git/cm ./pkg/cmd/run ./internal/gitprocess ./internal/terminal`.
2. Run `make command-surface` and `make check` in that order.
3. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 -tags=acceptance ./acceptance -run '^(TestGitForkStandaloneBinary|TestGitCMStandaloneBinary|TestRunStandaloneBinary)'`.
4. Run `git diff --check`.

#### Manual acceptance

- Record a local fixture for a Git Fork route, a Git CM success or partial
  route, and a real Run child. Review finite parent B screens at wide and
  compact dimensions, then verify the child owns the terminal after handoff.
- Reply once with `通过: <scenarios>` or `未通过: <scenario and reason>`.

### Evidence rule

Package/process/standalone tests prove Git and child compatibility. B PTY tests
prove finite presentation and release order. Manual review proves the actual
parent-to-child transition.

### Stop conditions

- A Console frame, renderer lease, or parent diagnostic appears after child
  startup.
- A B adaptation changes Git mutation order, creates retry/rollback, or
  exposes unsafe repository/provider data.

### Rollback

Revert the affected Git or Run slice only. Preserve the shared B foundation and
other recovered command cohorts.

### Exit conditions

1. Git Fork and Git CM preserve mutation, cancellation, Result, and redaction
   contracts with B structural/PTY evidence.
2. Run passes release-before-exec, child stream, cwd, signal, and exit evidence,
   including the B parent boundary.
3. The G4 manual review explicitly accepts the recorded scenarios.
4. G4 repository checks pass.

## G5: Service lifecycles and detached updater

### Purpose

Complete long-running service and detached update paths while preserving the
G2 parent Upgrade presentation, without expanding B beyond explicitly bounded
finite entry points.

### Inputs

- `issues/26-choose-tunnel-connect-lifecycle-log-presentation.md`
- `issues/27-choose-tunnel-server-lifecycle-log-presentation.md`
- `issues/29-choose-upgrade-presentation.md`
- `pkg/cmd/tunnel`, `pkg/cmd/upgrade`, `internal/tunnelruntime`,
  `internal/updater`, `internal/ycycmd`, and `internal/logging`

### Objective

Deliver the specified Tunnel Lifecycle Logs and detached updater chain. Use B
only for an ambiguous finite Tunnel selection; treat the G2 parent Upgrade
Console as a regression boundary rather than a second migration. Keep
continuing service and hidden-updater paths out of AltScreen.

### Scope boundary

Only Tunnel logging/presentation, updater presentation/integration, related
runtime/process tests, and necessary B finite-entry integration may change. Do
not redesign browser applications, protocol schemas, FRP behavior, service
business logic, or unrelated command cohorts. The G2 parent Upgrade
presentation may receive a test-only regression adjustment, but no new parent
visual behavior belongs here.

### Constraints

- Tunnel Connect selection may use B only when ambiguous; its subsequent client
  session is a line-oriented Lifecycle Log.
- Tunnel Server is always a line-oriented Lifecycle Log. It has no Console,
  Transcript, or full-screen dashboard.
- The G2 parent Upgrade B output ends before detached replacement. The hidden
  updater never renders a parent Result or Console.
- Text/NDJSON event ordering, stable IDs/schema, redaction, aggregation,
  supervision, rollback, startup consumption, and exactly-once shutdown remain
  authoritative.

### Slice policy

Implement Tunnel Connect, Tunnel Server, and detached Upgrade as separate
high-risk slices. Within Upgrade, separate candidate/state scheduling, hidden
replacement, next-startup consumption, and the G2 parent-boundary regression
check.

### Verification

#### Directed

- Exercise remembered-selection ambiguity, reconnect/failure windows, FRP and
  reconciliation states, server control/FRPS independence, agent aggregation,
  log filtering, text/NDJSON schema, redaction, and exactly-once shutdown.
- Exercise strict release resolution, candidate verification, detached state,
  replacement, rollback, cleanup warning, and one-time startup result.
- Exercise B PTY behavior only for bounded Tunnel selection and the existing G2
  parent Upgrade regression; assert services and hidden updater have no
  AltScreen or Transcript.

#### Repository

1. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./pkg/cmd/tunnel/... ./pkg/cmd/upgrade ./internal/tunnelruntime ./internal/updater ./internal/ycycmd ./internal/terminal`.
2. Run `make command-surface` and `make check` in that order.
3. Run `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 -tags=acceptance ./acceptance -run '^(TestTunnelServerStandaloneBinary|TestDetachedGoToGoStandaloneReplacementRollbackAndSelfCheck)'`.
4. Run `git diff --check`.

#### Manual acceptance

- Record exact local service and updater fixtures. Inspect colored/no-color
  Tunnel text and NDJSON sessions for startup, recovery, warning, and shutdown
  density. Review bounded B selection, the existing G2 Upgrade parent flow,
  and detached-update startup result separately.
- Reply once with `通过: <scenarios>` or `未通过: <scenario and reason>`.

### Evidence rule

Lifecycle/event/process tests prove service and updater safety. B PTY tests
prove only allowed finite regions. Manual review proves terminal ownership and
operator readability in actual sessions.

### Stop conditions

- A continuing service enters AltScreen, replays a Transcript, floods a normal
  log level, or changes NDJSON schema.
- Detached replacement cannot prove verification, rollback, process ownership,
  or exactly-once startup consumption.
- A finite B entry leaks service credentials, paths, protocol details, or raw
  errors.

### Rollback

Revert the smallest affected Tunnel or Upgrade slice. If a shared B boundary
regresses, restore its last verified G0-compatible slice before continuing.

### Exit conditions

1. Tunnel Connect and Server pass lifecycle, redaction, filtering, aggregation,
   shutdown, text/NDJSON, and no-Console evidence.
2. The G2 parent Upgrade B behavior remains green, and detached replacement,
   rollback, cleanup-warning, and startup-consumption evidence passes.
3. The G5 manual review explicitly accepts the recorded scenarios.
4. G5 repository checks pass.

## G6: Release verification and full visual review

### Purpose

Prove the recovered terminal experience is coherent across every command,
capability mode, supported platform, and visual boundary before release.

### Inputs

- All prior Gate evidence and frozen behavior baselines
- `issues/30-approve-rollout-and-acceptance-plan.md`
- `Makefile`, `docs/project-layout.md`, and `docs/platform-boundaries.md`
- `internal/terminal/prototype-vivid` B reference

### Objective

Produce release-grade evidence that every finite Rich command uses B, every
Service Command retains its Lifecycle Log contract, and no behavior baseline or
platform boundary regressed.

### Scope boundary

Only integration fixes, evidence normalization, and the smallest necessary
command or renderer repair may change. Do not introduce a visual decision, new
behavior, release-only renderer, or test exception.

### Constraints

- The B checklist applies to every finite Rich command and root/help where its
  decision requires durable B hierarchy.
- Service Commands are reviewed separately for Lifecycle Log density/schema,
  never as B full-screen Consoles.
- No beta flag, dual renderer, skipped test, weakened assertion, regenerated
  frozen command surface, or unrecorded terminal limitation is acceptable.

### Slice policy

Treat each failing command, platform, or B structural assertion as the smallest
recovery slice. Keep shared-renderer fixes distinct from command-adapter and
behavior fixes.

### Verification

#### Directed

- Compare all command behavior against frozen baselines and command decisions:
  streams, exits, side effects, schemas, signals, ownership, redaction, and
  Result bytes.
- Run the full Rich/Plain/Automation/redirected matrix, all form families, B
  wide/compact/color/no-color PTY structural checks, Transcript ordering,
  service text/NDJSON checks, Run handoff, and detached update journeys.
- Run prototype module tests and use `make prototype-terminal` during human
  comparison. It remains reference evidence, not production output.

#### Repository

1. Run `make check`.
2. Run `make command-surface`.
3. Run `make acceptance`.
4. Run `make cross-build`.
5. Run `git diff --check`.

#### Manual acceptance

- Build the release-candidate binary and record exact fixtures. Review a
  representative colored `120x40` and `NO_COLOR=1` `40x15` scenario from each
  finite command family, root/help, every Service Command, Run, and Upgrade.
- Confirm B bar/metadata/table/active region, palette/symbol semantics, Huh
  focus, compact layout, restoration/replay order, safe projection, and the
  separate Lifecycle Log and child-process boundaries.
- Reply exactly `通过: release candidate accepted` or
  `未通过: <scenario and reason>`.

### Evidence rule

All earlier Gate evidence plus the final repository matrix prove behavior and
integration. The final human review proves the complete B and Lifecycle Log
contract on a release-candidate binary.

### Stop conditions

- Any command fails a baseline, B structural, redaction, stream, lifecycle,
  child-handoff, updater, or supported-platform condition.
- A final failure could be hidden by excluding a test, changing frozen baseline
  data, or accepting a generic screen as B.

### Rollback

Revert the smallest affected command or shared-renderer slice. Re-run
applicable predecessor-Gate evidence before returning to release verification.

### Exit conditions

1. Every finite Rich command and root/help has current B structural, PTY, and
   behavior evidence; every Service Command has Lifecycle Log evidence.
2. Frozen behavior, capability, stream, redaction, handoff, and updater
   contracts remain intact on supported targets.
3. All G6 repository checks pass.
4. The release-candidate manual review explicitly accepts the full checklist.

## Definition Of Done

1. G0-G6 pass in order with traceable Exit-condition evidence.
2. Production finite Rich rendering matches accepted B hierarchy, state
   semantics, palette, Huh focus, compact layout, and replay order, without an
   A/C or generic-renderer fallback.
3. Every command retains its frozen behavior and decision-specific safety,
   stream, redaction, side-effect, process, and compatibility contract.
4. Every Service Command retains a bounded, schema-stable Lifecycle Log rather
   than a full-screen Console.
5. The final repository matrix and release-candidate manual acceptance pass.

## Explicitly Out Of Scope

- Changing command names, Cobra grammar, defaults, Result schemas, exits,
  business rules, persistence schemas, algorithms, or external effects merely
  to make visual recovery easier.
- Replacing a Service Command with a full-screen dashboard.
- Shipping a configurable A/B/C visual selector, legacy renderer, or
  terminal-specific compatibility matrix.
- Redesigning browser applications served by `fs`, `diff`, or Tunnel.
- Regenerating frozen command-surface artifacts to hide a structural or
  behavior difference.
