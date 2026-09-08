# B Ops Console Recovery Audit

Date: 2026-09-04

## Purpose

This audit resets the terminal-modernization execution control plane after
comparing the current production Rich renderer with the accepted B / `OPS
CONSOLE` prototype. It records the disposition of the old Gates without
changing command behavior or treating prior visual acceptance as transferable.

The authoritative visual decision remains
[`issues/05-prototype-vivid-terminal-visual-system.md`](issues/05-prototype-vivid-terminal-visual-system.md).
The runnable reference remains `make prototype-terminal` with its default
`console` variant. This audit does not change that decision.

## Comparison Basis

The accepted prototype defines a finite Rich screen with all of these elements:

1. a stable top command/status bar;
2. one safe, bounded metadata row;
3. an aligned `STATE / PHASE / DETAIL` table on wide screens;
4. all reached form steps or Work Phases in catalog order, with the active or
   final detail attached to its row;
5. one active Huh form or result region below the table;
6. amber `#FFB454` primary and cyan `#4CC9F0` accent colors, paired with
   `◆ ACTIVE`, `✓ DONE`, `○ PENDING`, `! WARNING` or `⊘ CANCELLED`, and
   `✕ FAILED` state text;
7. a B Huh bottom-focus treatment, no persistent left rail, and a compact
   single-column view that retains the status heading, rows, and active region
   below the prototype's wide threshold; and
8. AltScreen restoration followed by the bounded Transcript, diagnostics, and
   final stdout result.

The relevant reference implementation is
[`internal/terminal/prototype-vivid/view.go`](../../internal/terminal/prototype-vivid/view.go),
especially `renderConsole`, `renderConsoleStatus`, and
`renderCompactConsoleStatus`; its palette and Huh treatment are in
[`internal/terminal/prototype-vivid/theme.go`](../../internal/terminal/prototype-vivid/theme.go).

## Current Production Finding

The production Rich root does not implement that shell. Its `View` composes a
history of generic Notice documents with either a raw Huh form or a generic
tracked-phase list. It has no command/status-bar model, metadata model, status
table renderer, or console-specific compact renderer:

- [`internal/terminal/rich.go`](../../internal/terminal/rich.go) selects only
  Notice, form, and Track modes and concatenates their rendered text.
- [`internal/terminal/tracked.go`](../../internal/terminal/tracked.go) renders
  a label plus generic per-phase lines such as `[done]`, rather than the three
  B columns and symbol-paired labels.
- [`internal/terminal/presentation.go`](../../internal/terminal/presentation.go)
  assigns generic ANSI roles, not the B amber/cyan palette.
- [`internal/terminal/rich_form.go`](../../internal/terminal/rich_form.go)
  configures Huh size and help only; it does not apply the B Huh theme or its
  bottom-focus treatment.
- The production compact threshold is `width < 32 || height < 8`, whereas the
  prototype switches to its B compact renderer below `70x20` and retains the
  status heading and rows there.

This is a structural mismatch, not an issue of command wording or a missing
single style token. A command adapter can supply a title, a Notice, or phase
text, but it cannot create the missing shell through the existing semantic API.

## Gate-By-Gate Disposition

| Superseded Gate | Reviewed current route | B disposition | Recovery handling |
| --- | --- | --- | --- |
| G0: baseline and dependency lock | Behavior baselines, v2 dependency/import boundary, command surface | Retain as behavior and architecture evidence only; it does not prove a B screen. | Reuse the frozen behavior baseline; do not recapture it to absorb presentation changes. |
| G1: shared semantic terminal foundation | `internal/terminal` Experience, Rich root, forms, tracking, Transcript, lease/recovery tests | Invalid as B visual evidence because the shared renderer omits the B shell, table, palette, and Huh theme. Semantic lifecycle evidence remains useful. | Rebuild the production B renderer first and rerun all shared terminal evidence. |
| G2: logging and root boundary | Root/help discovery projection and Logger boundary | Root discovery still needs the B hierarchy in durable Rich output. Service-log constraints remain valid because Service Commands are explicitly not Console views. | Requalify root/help after the shared renderer; regression-test service log separation. |
| G3: low-risk finite commands | `config fork list`, `config cm list`, `config cm use`, `git heat` | Adapters have safe projections and phases, but their visible Rich screens inherit the generic shared renderer. Old command visual records are not B acceptance. | Reintegrate every command with a B descriptor and rerun its visual, stream, and behavior evidence. |
| G4: external-read and service-startup commands | `export env`, `config cm test`, `git pulse`, parent `upgrade`, `diff`, `fs` | Finite commands inherit the same generic screen. `diff` and `fs` must remain line-oriented logs, not be forced into B. | Restore B only for finite Rich flows and explicitly regression-test the Service Command exclusion. |
| G5: configuration mutation, `rm`, and `zip` | Current uncommitted adapters, Huh interactions, phases, Transcript, PTY tests, and the former manual handoff | The semantic work may be retained, but it still inherits the generic renderer. The former G5 manual handoff is superseded and cannot close any recovery Gate. | Reintegrate each command after the shared B renderer is proven; create a new acceptance entry only after its automated B evidence is green. |
| G6-G8: Git handoff, tunnel/updater, final release | No completed B visual evidence was imported. | Not executed under this recovery plan. | Implement only after recovered finite and service cohorts pass their predecessor Gates. |

## Consequences

- No former `passed` state is imported into the replacement ledger. The old
  evidence may demonstrate behavior preservation, but it cannot satisfy a
  recovery Gate's B visual Exit conditions.
- Existing implementation files, including the current dirty G5 work, remain
  in place. Recovery Gates must adapt or repair them in small reversible
  slices; this audit authorizes no rollback or deletion of that work.
- A new Gate may not declare a command B-complete merely because it emits
  `YCY / ...`, uses Huh, opens AltScreen, or has a Rich PTY test. Its tests and
  review must prove the complete B hierarchy above.
- Service Commands retain the separate Lifecycle Log contract from the source
  decisions. The B recovery applies to their bounded finite selection or
  parent flows only, never to a long-running dashboard.

## Archived Control Plane

The superseded plan, runbook, and fixed prompt were copied before replacement
to [`superseded/2026-09-04-pre-b-ops-console-reset/`](superseded/2026-09-04-pre-b-ops-console-reset/).
They are historical material only and must not be used to advance the new
ledger.
