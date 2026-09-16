# Contributing

## Prerequisites

- Go toolchain `go1.26.7`
- Node.js 24 or newer
- pnpm `>=11.13.0 <12` (`11.24.0` recommended)
- Git and GNU Make

On Windows, use Git for Windows and GNU Make. The hook and build commands are repository-local and do not require PowerShell, WSL, or a global JavaScript hook manager.

## Setup

Run the explicit setup commands from the repository root:

```sh
make bootstrap
make hooks-install
make hooks-doctor
```

`make bootstrap` fetches the pinned Go modules, builds the local Lefthook executable, and installs frontend dependencies. It never installs Git hooks. `make hooks-install` safely replaces only the approved legacy pre-commit hook; it refuses to modify a custom hook or any configured `core.hooksPath`. Resolve custom policy manually, then rerun the command.

## Checks

The pre-commit Fast Gate runs `git diff --cached --check`, checks staged Go formatting, and lints staged frontend files. A partially staged file selects the job, but the job checks the current worktree content. The hook never formats, fixes, stages, installs dependencies, or accesses the network.

Run the Complete Gate before review:

```sh
make check
make build
```

`make check` is offline and non-mutating after bootstrap. It runs the Vite checks and verified asset graph before Go code that embeds `web/dist`, then runs Go formatting, vet, tests, lock verification, and active-tree isolation checks. `make cross-build` produces the six CGO-free target binaries for migration evidence.

Use `make fmt` only when you intend to apply Go formatting and ESLint fixes yourself, then choose what to stage.

## Formal Releases

The release version is recorded in `cmd/ycy/VERSION` and published as an
annotated `vX.Y.Z` Git tag; do not edit `cmd/ycy/main.go`. The Go release tool
updates VERSION, creates the release commit, and pushes the tag while keeping
the existing six binary names and `SHA256SUMS` contract. Use `make release` to
choose the next version and run the local gate. Read
[docs/releasing.md](docs/releasing.md) for the pre-tag checklist, failure
recovery, attestations, and Docker publication details.

## Hook Recovery And Bypass

`make hooks-uninstall` removes only the Lefthook-managed pre-commit hook. It never restores a legacy hook. `make hooks-doctor` reports the resolved repository, Git-common, and hook paths along with readiness failures.

Git's `git commit --no-verify` and one-operation `LEFTHOOK=0 git commit` bypasses remain available. A bypass does not make a check pass; run the Complete Gate afterward.
