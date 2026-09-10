# Platform Boundaries

This owner-level inventory records why the active six-target Go build keeps
each platform-suffixed implementation. Shared command and protocol behavior
remains in ordinary Go files; these files contain operating-system capabilities
or target-specific artifacts rather than a general platform abstraction.

Categories are **security**, **process**, **resource**, and **target artifact**.
Targets use `D` for Darwin, `L` for Linux, and `W` for Windows; architecture
suffixes further select `amd64` or `arm64`.

| Owner and files | Responsibility | Category | Why the standard library contract is insufficient | Targets | Evidence |
| --- | --- | --- | --- | --- | --- |
| `appconfig/machineid_{darwin,linux,windows}.go` | Read the native stable machine identifier. | resource | macOS I/O Registry, Linux machine-id files, and the Windows registry expose different identity sources. | D/L/W | `go test ./internal/appconfig` |
| `appconfig/replace_{unix,windows}.go` | Atomically replace the persisted configuration. | resource | Windows replacement needs `MoveFileExW`; Unix rename has different open-file semantics. | D/L/W | `go test ./internal/appconfig` |
| `filesession/permissions_{unix,windows}.go` | Protect session state as owner-only. | security | Unix mode bits and Windows DACLs are different access-control models. | D/L/W | `go test ./internal/filesession` |
| `filesession/replace_{unix,windows}.go` | Replace session state with the established retry policy. | resource | Windows file sharing can reject replacement and requires `MoveFileExW` plus classified retries. | D/L/W | `go test ./internal/filesession` |
| `gitprocess/process_{unix,windows}.go` | Configure, signal, kill, and reap Git child processes. | process | Unix process groups and signals have no equivalent Windows process contract. | D/L/W | `go test ./internal/gitprocess` |
| `processprobe/process_{unix,windows}.go` | Determine whether a PID currently exists. | process | Go has no cross-platform liveness API matching Unix `kill(pid, 0)` and Windows process inspection. | D/L/W | `go test ./internal/processprobe` |
| `sevenzipruntime/payload_{darwin,linux,windows}_{amd64,arm64}.go` | Embed the executable, license, and Windows DLL for each release target. | target artifact | Binary formats, executable names, and the Windows DLL layout are target-specific. | D/L/W, amd64/arm64 | `go test ./internal/sevenzipruntime`; `make cross-build` |
| `terminaltest/pty_{unix,windows}.go` | Provide the controlled PTY fixture or the explicit unsupported result. | resource | Unix PTYs are not exposed by the common library and are not equivalent to Windows consoles or ConPTY. | D/L/W | `go test ./internal/terminaltest` |
| `tunnelruntime/file_permissions_{other,windows}.go` | Protect FRP configuration and state files. | security | `chmod` cannot express the existing owner-only Windows DACL contract. | D/L/W | `go test ./internal/tunnelruntime` |
| `tunnelruntime/frp_supervisor_{unix,windows}.go` | Own, stop, kill, wait for, and release the FRP child tree. | process | Unix process groups and Windows Job Objects/console control have different lifecycle semantics. | D/L/W | `go test ./internal/tunnelruntime` |
| `updater/paths_{other,windows}.go` | Derive transaction and helper executable paths. | target artifact | Windows executable naming and replacement layout require `.exe`-aware paths. | D/L/W | `go test ./internal/updater` |
| `updater/permissions_{other,windows}.go` | Protect staged updater files. | security | Unix mode bits and Windows DACLs do not share a portable owner-only operation. | D/L/W | `go test ./internal/updater` |
| `updater/process_{unix,windows}.go` | Configure the detached updater child. | process | Unix session creation has no matching Windows `SysProcAttr` contract. | D/L/W | `go test ./internal/updater` |
| `updater/replace_state_{other,windows}.go` | Atomically replace updater transaction state. | resource | Windows uses `MoveFileExW` to preserve the current replacement behavior. | D/L/W | `go test ./internal/updater` |
| `updater/retry_{other,windows}.go` | Classify retryable file-operation errors. | resource | Windows sharing/access errors and Unix transient errors use different native codes. | D/L/W | `go test ./internal/updater` |
| `windowsacl/private_{other,windows}.go` | Apply and verify a private Windows DACL, with no-op implementations elsewhere. | security | The common library does not create or inspect Windows security descriptors. | D/L/W | Windows compile checks; owner permission tests |
| `ycycmd/signals_{unix,windows}.go` | Select CLI shutdown signals supported by the host. | process | Windows and Unix expose different meaningful signal sets. | D/L/W | `go test ./internal/ycycmd` |
| `pkg/cmd/diff/open_{unix,windows,other}.go` | Open comparison files without following unsafe link types. | security | Unix `O_NOFOLLOW` and Windows reparse-point checks require different APIs. | D/L/W | `go test ./pkg/cmd/diff` |
| `pkg/cmd/fs/archive_capacity_{unix,windows}.go` | Query free bytes and filesystem capacity. | resource | Unix `statfs` and Windows `GetDiskFreeSpaceEx` expose different capacity data. | D/L/W | `go test ./pkg/cmd/fs` |
| `pkg/cmd/fs/workspace_root_{other,windows}.go` | Keep workspace operations bound to a verified root. | security | Windows needs handle identity and reparse-point checks to preserve no-escape guarantees. | D/L/W | `go test ./pkg/cmd/fs` |
| `pkg/cmd/run/process_{unix,windows}.go` | Configure and terminate command child processes and map signal exits. | process | Unix process groups/signals and Windows termination/exit status differ. | D/L/W | `go test ./pkg/cmd/run` |
| `pkg/cmd/tunnel/server/database_uri_{other,windows}.go` | Encode the SQLite file URI. | resource | Windows drive and path syntax needs target-specific URI normalization. | D/L/W | `go test ./pkg/cmd/tunnel/server` |

## Retained Runtime Decisions

The following production and build-tool call sites still use `runtime.GOOS` or
`runtime.GOARCH`. They remain local because this phase does not centralize path,
permission, target-selection, or host-tool policy. New ordinary business logic
must not add another runtime branch without updating this inventory.

| Owner and sites | Runtime decision | Category | Targets and evidence |
| --- | --- | --- | --- |
| `sevenzipruntime/runtime.go` | Select the state root and skip Unix mode enforcement on Windows. | security / resource | D/L/W; `go test ./internal/sevenzipruntime` |
| `sevenzipmanifest/manifest.go` | Select the embedded artifact for the current OS and architecture. | target artifact | D/L/W, amd64/arm64; manifest tests and `make cross-build` |
| `tunnelruntime/{runtime_paths,protocol}.go` | Resolve the state root and publish the current wire target. | resource / target artifact | D/L/W, amd64/arm64; `go test ./internal/tunnelruntime` |
| `tunnelruntime/frp_runtime.go` | Enforce Unix executable modes while accepting Windows executable metadata. | security / target artifact | D/L/W; FRP runtime tests |
| `updater/candidate.go` | Clear the macOS quarantine attribute only on Darwin. | security | D; `go test ./internal/updater` |
| `updater/replace.go` | Avoid deleting the running updater copy on Windows. | resource | D/L/W; updater replacement tests |
| `updater/upgrade.go` | Default an injected release resolver to the current target. | target artifact | D/L/W, amd64/arm64; updater resolver tests |
| `pkg/cmd/diff/content.go` | Compare resolved Windows paths case-insensitively. | security | D/L/W; `go test ./pkg/cmd/diff` |
| `pkg/cmd/zip/adapter.go` | Choose the native reveal command (`open`, `xdg-open`, or `cmd`). | resource | D/L/W; `go test ./pkg/cmd/zip` |
| `pkg/cmd/tunnel/connect/client_state.go`, `pkg/cmd/tunnel/server/run.go` | Pass the current platform into the shared Tunnel state-root policy. | resource | D/L/W; client and server tests |
| `tools/hookctl/hookctl.go` | Add the Windows executable suffix for the pinned hook binary. | target artifact | D/L/W; `go test ./tools/hookctl` |
| `tools/prepare-sevenzip/main.go` | Validate host executable modes and select the Windows extraction bootstrap. | resource / target artifact | D/L/W; `go test ./tools/prepare-sevenzip` |

The supported release matrix is `darwin`, `linux`, and `windows` on `amd64`
and `arm64`. The current `!windows` build tags intentionally cover the two
supported Unix targets; adding another operating system requires an explicit
implementation and native evidence rather than inheriting a fallback.
