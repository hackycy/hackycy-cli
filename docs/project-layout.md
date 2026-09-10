# Project Layout

This is the canonical repository-structure contract for ycy. It describes
where executable code, command behavior, shared Modules, evidence, and
generated output belong. The accepted structure is enforced by the
`internal/architecture` tests; a path search is supporting evidence, not a
replacement for those checks.

## Entry Chain

The production entry chain is deliberately one way:

```text
cmd/ycy/main.go
  -> internal/ycycmd
     -> pkg/cmd/factory and pkg/cmdutil
     -> pkg/cmd/root
        -> pkg/cmd/<domain>[/<leaf>]
           -> internal/* and approved Web owners
```

`cmd/ycy/main.go` contains the linker-injected version, `main`, the call to
`ycycmd.Main(version)`, and the sole `os.Exit` call. `internal/ycycmd` owns
process facts, terminal and logging setup, signals, hidden updater and
thumbnail-worker dispatch, startup-update consumption, Web validation, and
Factory/root execution. Command behavior does not live in the binary entry or
the process-composition package.

## Package Ownership

| Path | Responsibility |
| --- | --- |
| `pkg/cmd/root` | Global flags, diagnostics, discovery, command registration, and exit/error normalization. |
| `pkg/cmd/config` | The `config` parent and its `fork` and `cm` child groups. |
| `pkg/cmd/config/fork/*` | `config fork` leaves: `list`, `add`, and `remove`. |
| `pkg/cmd/config/cm/*` | `config cm` leaves: `list`, `add`, `use`, `set`, `remove`, and `test`. |
| `pkg/cmd/export` | The `export` parent. |
| `pkg/cmd/export/env` | The `export env` leaf. |
| `pkg/cmd/git` | The `git` parent. |
| `pkg/cmd/git/{heat,pulse,fork,cm}` | The four Git leaves and their command-specific results. |
| `pkg/cmd/{diff,fs,rm,run,upgrade,zip}` | Single-token command leaves, including their Cobra grammar, `Options`, runners, presentation, adapters, and package tests. |
| `pkg/cmd/tunnel` | The `tunnel` parent. |
| `pkg/cmd/tunnel/{server,connect}` | Independent Tunnel command leaves sharing only the approved runtime Module. |
| `pkg/cmd/factory` | Default construction of the bounded command Factory. |
| `pkg/cmdutil` | The exact process-level Factory type and IO stream utilities; it never imports command packages. |

The bounded Factory contains only process capabilities shared across command
leaves: version, IO streams, terminal, logging, environment lookup, working
directory, HTTP client, clock, and lazy/memoized ConfigStore and GitRunner.
Leaf-only dependencies stay in that leaf's `Options`.

The named private Modules under `internal/` retain narrow ownership:

- `appconfig` owns configuration persistence and locking.
- `processprobe` owns native process-liveness probes shared by persistence and
  updater owners; it is not a general platform abstraction.
- `filesession` owns file-session state and platform file operations.
- `fsthumbnail` owns the FS thumbnail worker protocol and pool.
- `gitprocess` owns shared Git process lifecycle and signal handling.
- `logging` owns diagnostics and redaction.
- `sevenzipmanifest` owns target/payload metadata; `sevenzipruntime` owns
  embedded 7-Zip materialization.
- `terminal` owns terminal Sessions and presentation runs; `terminaltest` is
  test-only terminal evidence.
- `tunnelruntime` owns the shared FRP/protocol runtime.
- `updater` owns release resolution, update transactions, replacement, and
  platform update operations.
- `windowsacl` owns Windows ACL behavior.

No generic `utils`, `common`, `services`, `interfaces`, `adapters`, or
`platform` package is a shared escape hatch.

## Dependency Direction

Production imports follow these rules:

```text
cmd/ycy -> internal/ycycmd
internal/ycycmd -> pkg/cmd/root, pkg/cmd/factory, pkg/cmdutil, internal/*, web,
                   and the exact pkg/cmd/upgrade startup-presentation boundary
pkg/cmd/factory -> pkg/cmdutil and internal/*
pkg/cmdutil -> internal/*
pkg/cmd/root -> direct command parents/leaves and pkg/cmdutil
command parents -> their direct children
command leaves -> pkg/cmdutil, approved internal Modules, web where owned,
                  and external libraries
```

`internal/ycycmd` calls `pkg/cmd/upgrade` only to present persisted startup
update state before Cobra runs; the Upgrade leaf remains the owner of that
terminal presentation. Other internal Modules never import `pkg/cmd*`,
`cmd/ycy`, or `legacy/bun`.
Command leaves never import sibling leaves. Only command packages import
Cobra/pflag, and no active production package imports the frozen Bun tree.
The architecture suite and the final `go list` inventory enforce these
boundaries.

## Tests And Fixtures

Package-local tests stay beside the implementation they exercise. Shared
Module and platform tests stay in the owning `internal` package. Cross-package,
standalone-binary, PTY, signal, worker, and native journeys live under the
tagged top-level `acceptance/` package; browser journeys that launch a real
service live under `acceptance/web/`. Every Go file in those directories uses
the `acceptance` build tag, so ordinary `go test ./...` and `make check` keep
their package-level scope. Run tagged evidence explicitly with `make acceptance`
or the finite `make acceptance-web` target.

Fixtures used by one package remain in that package's `testdata/`. A shared
acceptance scenario may use `acceptance/testdata/<scenario>/` only when at
least two acceptance cases genuinely share it; disposable state is created
with `t.TempDir()`.

## Support And Generated Areas

| Path | Contract |
| --- | --- |
| `acceptance/` | Tagged black-box, process, PTY, browser, and native evidence. |
| `docs/` | Maintainer and project-structure documentation. This file is canonical. |
| `web/` | Vite sources, embedded assets, static routes, and Web implementation tests. |
| `mock/` | Independent mock applications and their tests. |
| `legacy/bun/` | Frozen, read-only behavior reference with no active imports. |
| `tools/<name>/` | Independent build/check/harness tools; `tools/lefthook` remains a separate Go module. |
| `scripts/` | User-facing install scripts. The plural directory name is a contract. |
| `build/` | Ignored native/cross-build output only; no business code. |
| `public/` | Versioned static source assets. |

The following remain generated or ignored: `web/node_modules`, `web/dist`,
`build`, `release`, `.cache`, `.tmp`, `internal/sevenzipruntime/payload`, and
`tools/lefthook/bin`. The Makefile remains the single task entry point for
building, checking, acceptance, payload preparation, and release-artifact
verification. No parallel `output`, `artifacts`, or `vendor` root is added.

The frozen command-surface files under
`acceptance/testdata/command-surface/` are comparison-only after their initial
creation. They must never be regenerated to absorb a structural or behavioral
difference. No `.github/workflows` entry is part of this layout contract.

See the [README](../README.md) for a short orientation and the
[development guide](../DEVELOPMENT.md) for day-to-day commands and debugging.
