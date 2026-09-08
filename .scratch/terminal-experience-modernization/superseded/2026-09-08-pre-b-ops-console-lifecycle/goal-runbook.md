# Terminal Experience Modernization Recovery Goal Runbook

## Source Baseline

| Path | SHA-256 |
| --- | --- |
| `.scratch/terminal-experience-modernization/b-ops-console-audit.md` | `156dd963f9b6c0e5c52bae26abfd8cf14df4ec4c814a304294bd1a96fedf039d` |
| `.scratch/terminal-experience-modernization/baselines/README.md` | `73df9a2bcc6014801d9269cbc24c742a7c83721f1a8dddcdacbf47b101654d9f` |
| `.scratch/terminal-experience-modernization/baselines/manifest.json` | `f38bbc6cb9c87be8166b123ee53fc9c3cf018efab076c22927b4036e776b48f5` |
| `.scratch/terminal-experience-modernization/implementation-plan.md` | `e51ef38ffb89ec2ff041f939fe43b061298e40b9a0e4f019e1b8f865c63a3aad` |
| `.scratch/terminal-experience-modernization/inventories/03-current-command-terminal-surfaces.md` | `30c1758c8b72077737aa85c5682a865cde78654fceadf1939b8e1f6d51098ae7` |
| `.scratch/terminal-experience-modernization/issues/01-research-latest-charm-v2-stack.md` | `3466e6493a6c77c1fb05b2563d330747239625a29571ada628299b0d6b1ada17` |
| `.scratch/terminal-experience-modernization/issues/02-research-exemplary-cli-terminal-experiences.md` | `27f768379595480928c5ab09c96188cfe6c4cdc73f0366fa3b83477a03981319` |
| `.scratch/terminal-experience-modernization/issues/03-inventory-every-command-terminal-surface.md` | `f9172a9c8485522e0d7f29586f183c76f664032e878f31c3b038932b140bba57` |
| `.scratch/terminal-experience-modernization/issues/04-choose-charm-v2-and-logging-boundaries.md` | `c8a1d5ee1eeea729230e1b3b4dc6cf505dbe5c3076d6226d619599e83de27136` |
| `.scratch/terminal-experience-modernization/issues/05-prototype-vivid-terminal-visual-system.md` | `adfdf81af89332ba66f04bb71f6c7555152afb182fcdf9529e623032908a92d2` |
| `.scratch/terminal-experience-modernization/issues/06-choose-shared-terminal-experience-contract.md` | `34166d3fff0e6015fa97028fe3d175fbc80c858fb71498c1d0c472416189fc71` |
| `.scratch/terminal-experience-modernization/issues/07-choose-root-help-and-error-presentation.md` | `e28081d4337a6986e078762dc94b1f3faeb45d2687e03e7ee74fc62cd91e0356` |
| `.scratch/terminal-experience-modernization/issues/08-choose-export-env-presentation.md` | `20596a71b236eaabdeecda48321b87ad7900628b8456bb6a9b43e54d248d9f4e` |
| `.scratch/terminal-experience-modernization/issues/09-choose-config-fork-list-presentation.md` | `516d45d5a80fb0a1a733ccbaee3fd95fa6a0b21916441a181ed20294b79ac088` |
| `.scratch/terminal-experience-modernization/issues/10-choose-config-fork-add-presentation.md` | `7f5039d12cc2377f366ab2ee551a023eafa1126db1876cdd690ac6813bf06620` |
| `.scratch/terminal-experience-modernization/issues/11-choose-config-fork-remove-presentation.md` | `d02eb7411d8562f9702d9de6446966f0846652bc03679cced381608635298903` |
| `.scratch/terminal-experience-modernization/issues/12-choose-config-cm-list-presentation.md` | `5b17fc8d17043ddf9908af4c38322a2d3d742d31f7fe528559461731bc10811a` |
| `.scratch/terminal-experience-modernization/issues/13-choose-config-cm-add-presentation.md` | `b85343d999c75325a58f1d0e481bea9eac5688caf855559c0308a84dbb2c4395` |
| `.scratch/terminal-experience-modernization/issues/14-choose-config-cm-use-presentation.md` | `9c92c7d7c612b1d15fce2170f88ebfc1f4be3e4954f6e4db31523c51fd088fdb` |
| `.scratch/terminal-experience-modernization/issues/15-choose-config-cm-set-presentation.md` | `fadd58c91e2da00a734cf0a6ca7a23689f2aac544e5d6ff2fcfdad0bba6ac346` |
| `.scratch/terminal-experience-modernization/issues/16-choose-config-cm-remove-presentation.md` | `8c6f25fbdc961e5b79e8cf667144edb48dd78fee07f81bcedf5e014faf5053d6` |
| `.scratch/terminal-experience-modernization/issues/17-choose-config-cm-test-presentation.md` | `1ea070f25683553f651f317d87c62a1cec36b51461e0df6c9456f933ecf2cf5f` |
| `.scratch/terminal-experience-modernization/issues/18-choose-diff-lifecycle-log-presentation.md` | `f931611f45ae8b88592a3c12473c7ba6bac5093cf4d24a95356b6041c9a84e07` |
| `.scratch/terminal-experience-modernization/issues/19-choose-fs-lifecycle-log-presentation.md` | `6860f2f9c22909a533297af0a46e25c00e74d9c776c1c9fbd9609f1332ff6a8e` |
| `.scratch/terminal-experience-modernization/issues/20-choose-git-heat-presentation.md` | `b66cac29c9724362ca5e089fcc4b3fd8d795044d148d9ea1ca5a99e8fe36597b` |
| `.scratch/terminal-experience-modernization/issues/21-choose-git-pulse-presentation.md` | `74ad403f2e1d55d35849b7b5db9e697cac7d59cab99da8759a689b18de611db0` |
| `.scratch/terminal-experience-modernization/issues/22-choose-git-fork-presentation.md` | `b10ceb31876d8b9bdd4b4207a4d2e155b8f1e91a54892b0434265ec3043faa5d` |
| `.scratch/terminal-experience-modernization/issues/23-choose-git-cm-presentation.md` | `a2e9f8a81a2020dfea451f39c75a65f2fab1a63d8602d5687bd7166cc4333ee6` |
| `.scratch/terminal-experience-modernization/issues/24-choose-rm-presentation.md` | `1b656baf42bfc31de8c99878d923d90a5a21618732dd5f0e0821e5107c86caab` |
| `.scratch/terminal-experience-modernization/issues/25-choose-run-handoff-presentation.md` | `59f38d0c098ae525e3c8260edff7aa2414f91625536f8749ee02942530dc1e1a` |
| `.scratch/terminal-experience-modernization/issues/26-choose-tunnel-connect-lifecycle-log-presentation.md` | `e69927f9fc012de0e4ec96e7a606821d69ee2cf188d6c50449a2ce70a095ab8a` |
| `.scratch/terminal-experience-modernization/issues/27-choose-tunnel-server-lifecycle-log-presentation.md` | `89279bc77d21c8ceb78a234fbb438b0576358ac70c253b87b846b4ba90c63cf5` |
| `.scratch/terminal-experience-modernization/issues/28-choose-zip-presentation.md` | `e5ec30e94d8abc9050d43ab968f77cf4f01567f631c4d6664d25a58c0e45b560` |
| `.scratch/terminal-experience-modernization/issues/29-choose-upgrade-presentation.md` | `867722b759822bad286e9ab988e13160ae15cc5131beb78480a001da1ec57e4c` |
| `.scratch/terminal-experience-modernization/issues/30-approve-rollout-and-acceptance-plan.md` | `73b9ee80a04f158f8eaeecb8b23e8d7839f2f68b145568ec8e96fb95cd76203d` |
| `.scratch/terminal-experience-modernization/map.md` | `65b036d823e06f51b097769a46548d9a5f83938ce13e6f089ae0b09503cccd9d` |
| `.scratch/terminal-experience-modernization/research/01-latest-charm-v2-stack.md` | `51fc0ad8d29e43cbc1529988a4f297c64010053e942fa22bac36633fba73cc92` |
| `.scratch/terminal-experience-modernization/research/02-exemplary-cli-terminal-experiences.md` | `d6d921dfdec393ccb20a62134adb52d286955cc7bb21506562960ba65474fd3b` |
| `CLAUDE.md` | `35a0f66a6531e61b35dc7288884da35eeef79ad1039f5ffdfd29295cee8d566e` |
| `CONTEXT.md` | `a3400217b0e5dfaf39dbb729c2a0fb6ae38d6fd3e1eab24d38e4a76bb55c6da0` |
| `Makefile` | `a0e779709707bc6fb03438e7ab03cf1f44ff3dd978a875f61b415da9861b368c` |
| `docs/platform-boundaries.md` | `be531d09b0d6bc619d54a66d6c3fb6105208f241806dfd26b70631c18f87e473` |
| `docs/project-layout.md` | `26252b0db008b5964ae2877f7cbbf1472873f14d3a6165bc123a7f925a73c5f9` |
| `internal/terminal/prototype-vivid/README.md` | `d9089026734c466ab8c1c8cf341cc621fb8348a5344571082c685c3c020a7d5e` |
| `internal/terminal/prototype-vivid/go.mod` | `24a685071f55866af716995af1d3ef9454a5dd98593a87b4ce848414f017183d` |
| `internal/terminal/prototype-vivid/go.sum` | `e59854f7db40ac0d364fe94ed6a622fbf453fe82f94d6da8977c5ad5b70d8398` |
| `internal/terminal/prototype-vivid/main.go` | `a2455b2620d39e2bd71fe29dda08d69017159279e03e9786e7086c425162e65d` |
| `internal/terminal/prototype-vivid/model.go` | `9d7c1de8bba09cc7aaa4488e68c09fedf22272cfd80e9c09ac17563fa055650a` |
| `internal/terminal/prototype-vivid/theme.go` | `3294b46f4f2448c928bd4d9590df4c53cab3f76e3c1ad33850c269abab13ec84` |
| `internal/terminal/prototype-vivid/view.go` | `9347ec260a6586dccfecda7d45dacd5c2b45a489aeed5316a9650b24e1384863` |

## State Rules

- `implementation-plan.md` 是 Gate 合同的唯一来源；本账本只记录状态和证据。
- 一次只执行 Goal Ledger 中唯一 `active` 的 Gate。
- 每轮向对应 Gate 的 Progress Log 追加 slice、修改、验证结果、风险和下一动作。
- `passed` 需要计划中每条 Exit condition 的明确证据；普通实现或验证失败保持 `active`。
- `blocked` 只用于计划声明的 Stop condition，并记录阻塞与恢复条件。
- 人工验收待确认是一次 Goal 终止交接，必须记录在 Progress Log；它不是 `blocked`，当前 Gate 必须保持 `active`，不得通过重复日志表示等待。
- 当前 Gate 通过后只激活直接后继并结束本次 Goal；直接后继虽为 `active`，但必须由新的 Goal 执行。最后一个 Gate 通过后记录 effort 完成并结束本次 Goal。

## Goal Ledger

| Gate | Status | Depends on | Plan contract | Unlock evidence |
| --- | --- | --- | --- | --- |
| G0: Production B Ops Console foundation | passed | none | `implementation-plan.md` -> `G0: Production B Ops Console foundation` | exit conditions 1-5 verified 2026-09-04 |
| G1: Root and low-risk finite recovery | passed | G0 | `implementation-plan.md` -> `G1: Root and low-risk finite recovery` | exit conditions 1-4 verified 2026-09-04 |
| G2: External-read and service-boundary recovery | passed | G1 | `implementation-plan.md` -> `G2: External-read and service-boundary recovery` | exit conditions 1-4 and manual acceptance verified 2026-09-04 |
| G3: Mutation and archive recovery | passed | G2 | `implementation-plan.md` -> `G3: Mutation and archive recovery` | exit conditions 1-4 and manual acceptance verified 2026-09-04 |
| G4: Git mutation and process handoff | passed | G3 | `implementation-plan.md` -> `G4: Git mutation and process handoff` | exit conditions 1-4 and manual acceptance verified 2026-09-05 |
| G5: Service lifecycles and detached updater | passed | G4 | `implementation-plan.md` -> `G5: Service lifecycles and detached updater` | exit conditions 1-4 and manual acceptance verified 2026-09-05 |
| G6: Release verification and full visual review | passed | G5 | `implementation-plan.md` -> `G6: Release verification and full visual review` | exit conditions 1-4 verified 2026-09-05 |

## Progress Log

### G0: Production B Ops Console foundation

- 2026-09-04: initialized as `active`; recovery implementation has not
  started. The prior G0-G8 control plane and its former manual handoff are
  archived under `superseded/2026-09-04-pre-b-ops-console-reset/` and are not
  imported because they do not establish the accepted B production shell.
  Control-plane compilation reviewed the current renderer and B prototype,
  replaced the plan/runbook, and preserved all existing command changes.
  Verification: every map/issue decision is resolved; the baseline above locks
  the source set. Risk: production Rich rendering remains structurally generic.
  Next action: execute the descriptor/state-model slice defined by G0.

- 2026-09-04: completed slice `descriptor/state model`. Added the semantic
  `ConsoleDescriptor`/`ConsoleMetadata` seam, bounded safe-field validation,
  and `Runtime.OpenConsole`; legacy `Open` now receives a B-compatible default
  descriptor so no command package changed. The descriptor is captured before
  Rich controller construction and exposed through the `Experience` interface
  for later command-owned adapters. Modified: `internal/terminal/{semantic.go,
  experience.go,rich.go,console.go,console_test.go}` and
  `internal/terminaltest/semantic_recording.go`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/terminal
  ./internal/terminaltest` passed. Risk: the production Rich root still uses
  the generic composition until the wide-shell slice replaces it. Next action:
  implement and test the B wide shell only.

- 2026-09-04: completed slice `wide shell`. At `70x20` and above, the Rich
  root now renders the B command/status bar, bounded metadata row, divider,
  aligned `STATE / PHASE / DETAIL` table, and a single active region below the
  table. The table owns complete phase history; the active region now contains
  only the current work summary while preserving Esc's second-press
  cancellation prompt and cancellation state. Modified:
  `internal/terminal/{rich.go,console_test.go}`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/terminal
  ./internal/terminaltest` passed after correcting a missing Lip Gloss import,
  removing duplicate generic phase rows, and restoring the existing Esc
  confirmation prompt. Risk: sub-`70x20` terminals still use the old compact
  projection and can omit reached phase rows. Next action: implement and test
  the B compact shell only.

- 2026-09-04: completed slice `compact shell`. Below `70x20`, the Rich root
  now uses a single-column B projection with the command/status bar, bounded
  metadata, `STATE / PHASE / DETAIL` heading, every catalog row in order, and
  the active region underneath. The active work summary retains the operation
  label, current phase detail, and Esc cancellation prompts. Modified:
  `internal/terminal/rich.go`, `internal/terminal/console_test.go`, and the
  narrow-model expectations in `internal/terminal/tracked_model_test.go`.
  Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/terminal
  ./internal/terminaltest` passed. Risk: the B palette and Huh bottom-focus
  theme are still pending; compact output currently uses the existing generic
  semantic role colors. Next action: replace the shared Rich palette and Huh
  theme with the B contract only.

- 2026-09-04: completed slice `B palette and Huh theme`. Replaced generic
  Rich roles with the accepted B amber `#FFB454`, cyan `#4CC9F0`, green,
  yellow, red, text, and muted colors. Added the sole production Huh theme:
  it has the B bottom focus rule, no left rail, paired select/multi-select
  symbols, and capability-gated emphasis/background styling so `NO_COLOR=1`
  emits no ANSI bytes. Modified: `internal/terminal/{presentation.go,
  rich_form.go,theme.go,console_test.go}`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/terminal
  ./internal/terminaltest` passed after correcting no-color bold ANSI output.
  Risk: reached form steps are still represented by one generic table row;
  they need their own semantic catalog projection. Next action: implement and
  test form-step projection only.

- 2026-09-04: completed slice `form-step projection`. Each successfully
  reached Rich interaction now adds one ordered Console table row from its safe
  `TranscriptLabel` (or safe message fallback); text, select, multi-select,
  confirm, and secret interaction kinds expose only their generic safe step
  detail. Completion and cancellation update the corresponding row without
  exposing answers or secret values, while the active Huh form remains below
  the table. Modified: `internal/terminal/{rich.go,console_test.go}`.
  Verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  ./internal/terminal ./internal/terminaltest` passed. Risk: Work Phase
  catalog rows still need an explicit lasting table projection after their
  active region is replaced. Next action: implement and test phase-table
  projection only.

- 2026-09-04: completed slice `phase-table projection`. A Track now projects
  its immutable catalog in order into the one Console status table; updates
  replace only that row's state/detail, and final rows persist after a later
  Notice or Form replaces the active region. The old generic final-phase
  Notice projection was removed, so completed phases cannot appear twice or
  displace the B hierarchy. Modified:
  `internal/terminal/{rich.go,tracked.go,console_test.go}`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  ./internal/terminal ./internal/terminaltest` passed. Risk: durable Rich
  documents still use generic role sequencing rather than the B-derived
  hierarchy required for root/help in its later Gate. Next action: implement
  and test direct durable B hierarchy only, without changing document content
  or terminal mode behavior.

- 2026-09-04: completed slice `direct durable B hierarchy`. Direct `WriteRich`
  documents now derive their roles from the production B palette, using cyan
  for document section/active hierarchy while retaining amber for the live
  Console bar and state marker. This is a styling-only terminal projection:
  document text, Plain output, stdout ownership, and non-AltScreen behavior
  remain unchanged. Modified:
  `internal/terminal/{presentation.go,presentation_test.go}`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  ./internal/terminal ./internal/terminaltest` passed. Risk: B structural
  model/PTY evidence still needs to demonstrate colored and `NO_COLOR` console
  frames, compact retention, and the existing restoration/replay lifecycle.
  Next action: add and run normalized model and PTY evidence only.

- 2026-09-04: completed slice `legacy introduction compatibility projection`.
  The shared Rich root now recognizes only the bounded, static three-block
  introduction shape used by commands that still use the default Console
  descriptor, and projects its safe command identity into the B bar and its
  title/description into the single metadata row. This keeps established
  Rich PTY introductions observable when a later B frame replaces the first
  frame, without changing command wording, Transcript contents, stdout
  Results, or the latest-Notice active-context rule. Modified:
  `internal/terminal/rich.go`. Verification: targeted `rm` Rich PTY success and
  cancellation journeys passed in both color and `NO_COLOR=1`; shared terminal
  tests and the prototype tests also passed. Risk: the full repository suite
  remains the final compatibility check. Next action: run the G0 repository
  verification sequence.

- 2026-09-04: completed slice `normalized model and PTY evidence`, including
  the final shared-renderer compatibility repair. Model evidence covers the
  descriptor/bar/metadata/divider/table/active-region hierarchy, B state
  symbols and palette, no-color control-free rendering, compact `70x20`
  degradation, ordered form and Track rows, no persistent rail, and bounded
  latest-Notice context. PTY evidence covers colored and `NO_COLOR=1` Rich
  journeys, resize retention, AltScreen restoration, cancellation, renderer
  lease/recovery, Transcript replay ordering, redirected stdout, and child
  handoff. The repository run initially exposed legacy `rm` Rich PTY
  expectations for the static introduction; the shared renderer now projects
  only that bounded safe three-block introduction into the B bar/metadata row,
  preserving command packages, wording, Results, Transcript semantics, and
  stdout ownership. Modified: `internal/terminal/rich.go` plus the existing
  G0 evidence files. Directed verification passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/terminal
  ./internal/terminaltest -count=1`,
  `cd internal/terminal/prototype-vivid && GOTOOLCHAIN=go1.26.7 GOWORK=off
  go test ./...`, and targeted `pkg/cmd/rm` Rich PTY success/cancellation
  tests in color and `NO_COLOR=1`. Repository verification passed in order:
  the shared terminal test command, `make command-surface`, `make check`, and
  `git diff --check`; `make check` completed all Go, architecture, Web, and
  command-package checks. Exit-condition evidence: (1) semantic descriptor,
  safe metadata, B bar/divider/wide table/active region: model tests and
  `internal/terminal` tests; (2) ordered form/Track rows, paired symbols,
  palette, bottom focus, and no rail: console/theme/model/PTY tests; (3)
  compact heading, rows, details, and active region below `70x20`: compact
  model and resize PTY tests; (4) Rich/Plain/Automation/redirected,
  Transcript, lease, cancellation, recovery, and exactly-once behavior:
  shared runtime and repository tests; (5) repository checks: all commands
  above passed. Risk: G1 is now active but intentionally has no
  implementation evidence in this Goal. Next action: end this Goal after the
  atomic Gate transition.

### G1: Root and low-risk finite recovery

- 2026-09-04: initialized as `planned`; no recovery implementation evidence.
- 2026-09-04: 已激活，尚未开始实施。

- 2026-09-04: completed slice `root/help durable B hierarchy`. The discovery
  adapter now assigns the B active hierarchy role to the durable `Usage`,
  `Commands`, `Flags`, and `Examples` headings while leaving every command,
  flag, example, order, and Plain/Automation byte intact. The root remains a
  direct stdout document with no Console start, AltScreen, or Transcript.
  Modified: `pkg/cmd/root/{terminal_discovery.go,terminal_discovery_test.go}`.
  Verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  -count=1 ./pkg/cmd/root` passed; the focused test verifies exact Plain
  content, colored B roles, and control-free no-color output alongside the
  existing version, completion, parser, and write-failure coverage. Risk:
  command-specific Rich PTY evidence for the remaining finite adapters is
  still absent. Next action: migrate only `config fork list` to an explicit
  safe Console descriptor and add its structural evidence.

- 2026-09-04: completed slice `config fork list descriptor/context`. The
  command now opens the frozen G0 Console seam with its own bounded context:
  `YCY / config fork list`, `provider inventory`, and the static scope `git
  fork configuration`. It does not place instance names, hosts, token previews,
  paths, or configuration data in the B bar/metadata row. Read ordering,
  loading phase, result documents, Plain/Automation behavior, and Transcript
  projection are unchanged. Modified: `pkg/cmd/config/fork/list/{command.go,
  terminal_test.go}`. Verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off
  CGO_ENABLED=0 go test -count=1 ./pkg/cmd/config/fork/list` passed. Risk:
  this command still needs fresh wide/compact color/no-color Rich PTY evidence.
  Next action: add only that PTY/stream evidence.

- 2026-09-04: completed slice `config fork list Rich PTY and stream evidence`.
  Added controlled PTY coverage at `120x40` and `40x15`, with and without
  color. The evidence asserts the command-owned B bar and safe metadata,
  ordered `STATE / PHASE / DETAIL` structure, active/loading and completed
  phase projection, compact width-bounded context, AltScreen restoration,
  Transcript-before-result ordering, Transcript redaction, complete durable
  Result fields, and control-free `NO_COLOR=1` output. Modified:
  `pkg/cmd/config/fork/list/terminal_pty_test.go`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/fork/list` passed. Risk: the remaining G1 adapters still
  need their own descriptor/context, phase or form, and structural PTY
  evidence. Next action: migrate only `config cm list`.

- 2026-09-04: completed slice `config cm list descriptor/context and default
  summary`. The command now opens its own bounded Console descriptor before
  store construction: `YCY / config cm list`, target `profile inventory`, and
  static scope `commit message configuration`. Rich successful runs retain a
  safe `Default profile: <name>` milestone only for a listed, display-safe
  default; no profile rows, Base URLs, models, or API keys enter the
  Interaction Transcript. Modified:
  `pkg/cmd/config/cm/list/{command.go,presentation.go,terminal_test.go}`.
  Verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  -count=1 ./pkg/cmd/config/cm/list` passed. Risk: command-specific Rich PTY
  and stream evidence is now complete for this adapter; remaining G1
  adapters still need migration. Next action: migrate only `config cm use`.

- 2026-09-04: completed slice `config cm list Rich PTY and stream evidence`.
  Added controlled `120x40`/`40x15` color and `NO_COLOR=1` PTY journeys that
  assert B context, phase table order, completed state, primary-screen
  restoration, Transcript phase/summary/default/outcome ordering and
  redaction, and complete post-AltScreen Result tables. Plain and Automation
  assertions preserve exact result bytes, loading-diagnostic ownership,
  control-free streams, and no partial output on read failure. Modified:
  `pkg/cmd/config/cm/list/terminal_pty_test.go`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/cm/list` passed. Risk: `config cm use` and `git heat`
  remain unqualified in G1. Next action: migrate only `config cm use`.

- 2026-09-04: completed slice `config cm use descriptor and safe phase
  context`. The atomic default-selection command now opens a bounded Console
  descriptor before store construction: `YCY / config cm use`, target `profile
  selection`, and static scope `commit message configuration`. Its single
  Work Phase includes the bounded safe requested profile in the active and
  successful detail while preserving the exact profile identity passed to the
  writer and the existing result/error contract. Modified:
  `pkg/cmd/config/cm/use/{command.go,terminal_test.go}`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/cm/use` passed. Risk: command-specific PTY and stream
  evidence remains to be recorded. Next action: add only that evidence.

- 2026-09-04: completed slice `config cm use Rich PTY and stream evidence`.
  Added controlled `120x40`/`40x15` color and `NO_COLOR=1` PTY journeys that
  assert B context, safe profile detail, phase/table order, completed state,
  primary-screen restoration, Transcript-before-result ordering, and durable
  success output. Plain and Automation assertions preserve loading diagnostic
  ownership, exact result bytes, control-free output, and no partial output
  on a missing-profile failure. Modified:
  `pkg/cmd/config/cm/use/terminal_pty_test.go`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/cm/use` passed. Risk: only `git heat` remains before G1
  repository verification. Next action: migrate only `git heat`.

- 2026-09-04: completed slice `git heat descriptor/context`. The finite Git
  inspection command now opens the frozen Console seam after input
  normalization and before repository discovery, with only the bounded
  command identity, target, normalized range, sort, and time mode in its
  descriptor. Query text, repository paths, Git arguments, timings, and path
  rows remain outside Console metadata; Plain/Automation result and error
  behavior remain unchanged. Modified: `pkg/cmd/git/heat/{terminal.go,
  terminal_test.go}`. Verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off
  CGO_ENABLED=0 go test -count=1 ./pkg/cmd/git/heat` passed. Risk: the
  command still needs the final fresh structural PTY and stream evidence
  review before G1 repository verification. Next action: add only the
  `git heat` Rich PTY/stream evidence.

- 2026-09-04: completed slice `git heat Rich PTY and stream evidence`.
  Added controlled `120x40` and `40x15` color/no-color journeys covering the
  B bar, safe normalized metadata, all three truthful phases, wide/compact
  rows, latest/earliest markers, primary-screen restoration, bounded
  Transcript redaction, and complete durable Result output. Plain and
  Automation fixtures assert loading ownership, exact result bytes, control
  stripping, and no partial output on repository failure. Modified:
  `pkg/cmd/git/heat/terminal_pty_test.go`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/git/heat` passed. Risk: final G1 repository checks and human
  review remain. Next action: run the declared G1 repository verification
  sequence in order.

- 2026-09-04: repository verification step 1 passed. Command and shared
  terminal package suite completed successfully:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./pkg/cmd/root
  ./pkg/cmd/config/fork/list ./pkg/cmd/config/cm/list ./pkg/cmd/config/cm/use
  ./pkg/cmd/git/heat ./internal/terminal`. No additional source changes were
  required. Risk: command-surface, repository check, acceptance journeys, and
  diff hygiene remain. Next action: run `make command-surface`.

- 2026-09-04: repository verification step 2a passed. `make command-surface`
  completed successfully, confirming the frozen command/help surface remains
  compatible. No additional source changes were required. Risk: full
  repository checks, tagged G1 acceptance journeys, and diff hygiene remain.
  Next action: run `make check`.

- 2026-09-04: diagnosed and repaired the first repository-check failure
  without changing G1 runtime behavior. New PTY assertions had imported the
  Charm ANSI helper directly from command packages, violating the repository
  dependency boundary; the helper now lives behind `internal/terminaltest` and
  command tests call that boundary. Modified:
  `internal/terminaltest/{streams.go,terminaltest_test.go}` and the five G1
  PTY/discovery test files. Directed re-verification passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/root ./pkg/cmd/config/fork/list ./pkg/cmd/config/cm/list
  ./pkg/cmd/config/cm/use ./pkg/cmd/git/heat ./internal/terminal
  ./internal/terminaltest`. Risk: full `make check` must be rerun; its prior
  unrelated tunnel-server integration timeout remains under observation. Next
  action: rerun `make check`.

- 2026-09-04: repository verification step 2b passed. The rerun of
  `make check` completed across the Go and web repository suites, including
  architecture/dependency checks, all command packages, and the tunnel-server
  integration suite. The earlier Charm import violations did not recur; no
  unrelated source changes were made. Risk: tagged G1 standalone acceptance
  journeys and final diff hygiene remain. Next action: run the declared
  acceptance command.

- 2026-09-04: repository verification step 3 passed. The tagged standalone
  acceptance journeys for `config fork list`, `config cm list`, `config cm
  use`, and `git heat` completed successfully with `GOTOOLCHAIN=go1.26.7
  GOWORK=off CGO_ENABLED=0 go test -count=1 -tags=acceptance ./acceptance
  -run '^(TestConfigForkListStandaloneBinary|TestConfigCMListStandaloneBinary|TestConfigCMUseStandaloneBinary|TestGitHeatStandaloneBinary)'`.
  Risk: only final diff hygiene and manual acceptance handoff remain. Next
  action: run `git diff --check`.

- 2026-09-04: repository verification step 4 passed: `git diff --check`
  completed with no whitespace errors. Final scope review found only G1
  presentation adapters, their evidence, and the necessary internal test
  boundary; no G2+ command was changed. Automated exit evidence is complete:
  the declared G1 package suite, `make command-surface`, rerun `make check`,
  the four tagged standalone journeys, and final diff hygiene all passed.

  Manual acceptance handoff: a local binary built from this worktree is ready
  at `/tmp/hackycy-g1-acceptance.l54jPH/ycy`; its isolated fake-config home is
  `/tmp/hackycy-g1-acceptance.l54jPH/home`, and its contained two-commit Git
  fixture is `/tmp/hackycy-g1-acceptance.l54jPH/repo`. The fixture smoke run
  passed for fork list, CM list, CM use, and Git heat, without touching the
  user configuration or the repository state. In a colored `120x40` TTY, set
  `HOME=/tmp/hackycy-g1-acceptance.l54jPH/home` and `USERPROFILE=` and inspect
  `ycy --help`, `ycy config fork list`, `ycy config cm list`, `ycy config cm
  use work`, and, from the contained Git fixture, `ycy git heat -n 2 -t files
  -s path -r -q main`. Repeat those scenarios in a `40x15` TTY with
  `NO_COLOR=1`.

  Minimum acceptance checklist: root/help stays a complete durable discovery
  document without AltScreen; each finite command shows the B hierarchy with
  safe bounded metadata and complete wide/compact phase/result rows; no-color
  symbols remain readable and have no persistent rail; finite commands restore
  the primary screen before a bounded Transcript and unchanged durable Result;
  no fixture token, API key, path, or full report row leaks into Transcript.
  Reply exactly once using `通过: root/help, config fork list, config cm list,
  config cm use, git heat` or `未通过: <command and reason>`. Next action: a
  new Goal with that explicit result may continue G1; this Goal ends now with
  G1 still `active` and no successor activated.

- 2026-09-04: final G1 acceptance completed. The user explicitly replied
  `全部通过`, accepting the handoff scenarios for root/help, `config fork
  list`, `config cm list`, `config cm use`, and `git heat`. Exit condition 1
  passed: root/help retained its durable discovery, parser, version,
  completion, redirected-output, and write-failure contracts while using the
  B hierarchy, evidenced by the focused root suite and `make
  command-surface`. Exit condition 2 passed: all four finite commands have
  fresh wide/compact color/no-color B structural PTY evidence plus behavior,
  redaction, Transcript, Plain, Automation, and standalone coverage. Exit
  condition 3 passed through the explicit manual acceptance. Exit condition 4
  passed: the declared package suite, `make command-surface`, rerun `make
  check`, tagged standalone acceptance journeys, and `git diff --check` all
  passed. G1 is now `passed`; its direct successor G2 is activated but has not
  been read, analyzed, implemented, or verified in this Goal.

### G2: External-read and service-boundary recovery

- 2026-09-04: initialized as `planned`; no recovery implementation evidence.
- 2026-09-04: 已激活，尚未开始实施。
- 2026-09-04: completed slice `export env descriptor/context`. The finite
  adapter now opens the frozen Console seam before discovery or interaction,
  with `YCY / export env`, target `environment JSON`, and bounded directory
  plus merge metadata. Dotenv selection, parsing, file ordering, output bytes,
  and Automation behavior remain unchanged. Modified:
  `pkg/cmd/export/env/{terminal.go,terminal_test.go}`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/export/env` passed. Risk: fresh Rich PTY evidence and the other
  finite adapters remain pending. Next action: add only the `config cm test`
  descriptor/context slice.

- 2026-09-04: completed slice `config cm test descriptor/context`. The
  connection check now opens the frozen Console seam before store resolution,
  with `YCY / config cm test`, target `provider connection`, static
  non-mutating scope, and an optional bounded requested-profile projection.
  Resolver ordering, provider request/timeout semantics, redaction, result
  documents, and exit behavior are unchanged. Modified:
  `pkg/cmd/config/cm/test/{terminal.go,terminal_test.go}`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/cm/test` passed. Risk: fresh Rich PTY evidence and the
  Git Pulse/Upgrade adapter contexts remain pending. Next action: add only the
  `git pulse` descriptor/context slice.

- 2026-09-04: completed slice `git pulse descriptor/context`. The finite Git
  activity adapter now opens the frozen Console seam before workspace
  discovery, with `YCY / git pulse`, a workspace-activity target, static
  history scope, bounded directory projection, and explicit/interactive date
  mode. Repository discovery, Git concurrency, date/author filtering, report
  bytes, and signal behavior remain unchanged. Modified:
  `pkg/cmd/git/pulse/{terminal.go,terminal_test.go}`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/git/pulse` passed. Risk: fresh wide/compact PTY evidence and the
  parent Upgrade descriptor remain pending. Next action: add only the parent
  `upgrade` descriptor/context slice.

- 2026-09-04: completed slice `upgrade descriptor/context`. The parent
  updater now opens the frozen Console seam before release resolution, with
  `YCY / upgrade`, a release-update target, detached-updater scope, and a
  bounded current-version field. Release, candidate, transaction, detached
  spawn, startup-state, and stdout semantics remain unchanged. Modified:
  `pkg/cmd/upgrade/{command.go,terminal_test.go}`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/upgrade` passed. Risk: command-specific PTY/stream evidence and
  service exclusion evidence remain pending. Next action: add only fresh
  `export env` Rich PTY and stream evidence.

- 2026-09-04: completed slice `export env` Rich PTY and stream evidence. Added
  a four-way colored/`NO_COLOR` PTY journey at `120x40` and `40x15`, covering
  the bounded B bar/metadata, phase table, compact degradation, primary-screen
  restoration, ordered redacted Transcript, and unchanged JSON Result. The
  journey exposed a Rich operation-lock re-entry when the command published
  its selection or variable-count milestone while a phase Track was active;
  the adapter now closes the discovery Track before the selection milestone
  and defers the variable-count milestone until the output Track closes.
  This is presentation synchronization only; dotenv selection, parsing,
  merge/order, output bytes, and Automation behavior remain unchanged.
  Modified: `pkg/cmd/export/env/{terminal.go,terminal_pty_test.go}`.
  Verification: the four PTY cases and full
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/export/env` package suite passed. Risk: the other three finite
  adapters still need fresh wide/compact PTY evidence, and diff/FS Lifecycle
  Log regressions remain pending. Next action: add only `config cm test`
  Rich PTY and stream evidence.

- 2026-09-04: completed slice `config cm test` Rich PTY and stream evidence.
  Expanded the provider-check journey to colored/`NO_COLOR` `120x40` and
  `40x15` PTYs with synchronized sizing. Evidence covers the frozen Console
  descriptor, complete wide provider scope, bounded compact projection, both
  real phases and B table headings, primary-screen restoration, redacted
  Transcript-before-result ordering, unchanged response/usage Result, and
  no-color control suppression. Existing Plain, Automation, redirected, error,
  cancellation, and redaction tests remain green. Modified:
  `pkg/cmd/config/cm/test/terminal_pty_test.go`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/cm/test` passed. Risk: `git pulse` and `upgrade` still
  need fresh wide/compact PTY evidence; diff/FS Lifecycle Log regressions
  remain pending. Next action: add only `git pulse` Rich PTY and stream
  evidence.

- 2026-09-04: completed slice `git pulse` Rich PTY and stream evidence.
  Expanded the existing journey to colored/`NO_COLOR` `120x40` and `40x15`
  PTYs with synchronized sizing and actual-width Rich report projection.
  Evidence covers the descriptor/bar, all four workspace phases, B table
  headings, compact target degradation, primary-screen restoration, ordered
  redacted Transcript, complete narrow/wide commit report, and color-control
  behavior. Existing date/author cancellation, Automation boundary, Plain
  stderr/stdout split, partial-fetch warnings, redaction, and one-result tests
  remain green. Modified: `pkg/cmd/git/pulse/terminal_pty_test.go`.
  Verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  -count=1 ./pkg/cmd/git/pulse` passed. Risk: `upgrade` still needs fresh
  wide/compact PTY evidence; diff/FS Lifecycle Log regressions remain pending.
  Next action: add only `upgrade` Rich PTY and stream evidence.

- 2026-09-04: completed slice `upgrade` Rich PTY and stream evidence. Expanded
  the parent updater journey to colored/`NO_COLOR` `120x40` and `40x15` PTYs,
  covering the bounded B descriptor/bar, phase table, compact height
  degradation, primary-screen restoration, redacted ordered Transcript, and
  unchanged scheduled-update Result. The live viewport assertions use stable
  phase prefixes where the renderer legitimately truncates a wide column, and
  require the final `Complete` phase only when it is visible in the wide
  viewport; the Transcript retains full phase names in every case. Modified:
  `pkg/cmd/upgrade/terminal_pty_test.go`. Verification: targeted four-way PTY
  test and full
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/upgrade` passed. Risk: only the independent diff/FS Lifecycle Log
  regression slices and final repository checks remain. Next action: add and
  verify only the `diff` no-Console service boundary slice.

- 2026-09-04: completed slice `diff` no-Console service boundary regression.
  Added a Rich-capability adapter test proving the existing line-oriented
  startup result remains outside AltScreen; the service still does not create
  a Console or Interaction Transcript. Existing lifecycle evidence covers
  ordered startup, initial and subsequent Refresh records, phase de-duplication,
  cancellation/failure, shutdown ownership, redaction/bounds, text symbols,
  configured levels, NDJSON envelope, request silence, and concurrent
  exactly-once terminal records. Modified:
  `pkg/cmd/diff/terminal_test.go`. Verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/diff` passed (including the focused lifecycle/no-Console run).
  Risk: the independent FS no-Console regression slice remains. Next action:
  add and verify only the `fs` no-Console service boundary slice.

- 2026-09-04: completed slice `fs` no-Console service boundary regression.
  Added a Rich-capability adapter test proving the existing startup and
  stopped Result Checkpoints remain line-oriented and outside AltScreen; no
  Console or Interaction Transcript is introduced. Existing FS lifecycle
  evidence covers ordered startup/capability/authentication records, public
  warning, Managed Task acceptance/start/terminal/retry/expiry behavior,
  bounded redaction, text symbols and levels, NDJSON schema, startup/task
  ordering, shutdown folding/counts, serve/release/checkpoint failures, and
  request/progress silence. Modified: `pkg/cmd/fs/terminal_test.go`.
  Verification: focused lifecycle/no-Console run and full
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 ./pkg/cmd/fs`
  passed. Risk: none within the service exclusion slice; the declared G2
  repository verification and manual acceptance remain. Next action: run
  repository verification in the plan's exact order.

- 2026-09-04: repository verification step 1 passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./pkg/cmd/export/env
  ./pkg/cmd/config/cm/test ./pkg/cmd/git/pulse ./pkg/cmd/upgrade
  ./pkg/cmd/diff ./pkg/cmd/fs ./internal/terminal`.
  Repository verification step 2 initially stopped at the Go format check for
  the new FS test; `gofmt` corrected the touched terminal tests, and the full
  `make check` rerun passed, including command-surface prerequisites, all Go
  packages, architecture checks, Web lint/type/test/build, and asset
  verification. Modified by the formatting repair:
  `pkg/cmd/{diff,fs}/terminal_test.go` and
  `pkg/cmd/upgrade/terminal_pty_test.go`. Risk: tagged standalone acceptance
  and final diff hygiene remain. Next action: run the declared acceptance
  subset.

- 2026-09-04: repository verification step 3 passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 -tags=acceptance
  ./acceptance -run '^(TestExportEnvStandaloneBinary|TestConfigCMTestStandaloneBinary|TestGitPulseStandaloneBinary|TestDiffStandaloneBinary|TestFSStandaloneBinary)'`.
  Repository verification step 4 passed: `git diff --check` reported no
  whitespace errors. Automated G2 evidence is complete across all four finite
  B journeys and both service Lifecycle Log exclusions. Risk: manual visual
  acceptance is the only remaining Gate condition. Next action: build and
  launch the isolated G2 acceptance entry, then hand off once.

- 2026-09-04: manual acceptance handoff for `G2` recorded. Automated Exit
  conditions 1, 2, and 4 are complete; no manual result has been asserted.
  Acceptance entry: local binary
  `.scratch/terminal-experience-modernization/g2-acceptance/ycy` (`--version`
  reports `1.0.0`) with isolated fixture
  `.scratch/terminal-experience-modernization/g2-acceptance/`. The loopback
  Diff service is ready at `http://127.0.0.1:61189` in detached screen
  session `21729.g2-diff-clean`; the loopback FS service is ready at
  `http://127.0.0.1:61190` in detached screen session `21726.g2-fs-clean`.
  Their durable stdout and Lifecycle Log captures are
  `.scratch/terminal-experience-modernization/g2-acceptance/{diff,fs}.{stdout,stderr}`.
  The fixture contains dotenv selection files, a CM failure-safe local
  profile, one committed Git Pulse repository, Diff baseline/target trees,
  and an FS browse root.

  Automated evidence supplied for review: the four finite adapters have
  colored and `NO_COLOR` `120x40`/`40x15` Rich PTY journeys with B
  descriptor/phase/Transcript/Result and stream/redaction assertions;
  Diff and FS have Lifecycle Log text/NDJSON, startup/task-or-refresh,
  failure/shutdown, ordering, bounds, request-silence, and no-AltScreen
  regressions; the declared package suite, `make command-surface`, rerun
  `make check`, tagged standalone acceptance subset, and `git diff --check`
  all passed. Minimal manual checklist: (1) inspect `export env`, `config cm
  test`, `git pulse`, and `upgrade` in a real PTY at colored `120x40` and
  `NO_COLOR=1` `40x15`, confirming the B bar/metadata/phase hierarchy,
  compact degradation, Transcript-before-Result ordering, and no secret
  leakage; cancel `upgrade` before any update is scheduled; (2) inspect one
  text Diff session and one text FS session from the running entry, confirming
  one-line bounded records and final stopping/stopped order; (3) run each
  service once with `--log-format=json` and confirm the stable NDJSON envelope,
  no ANSI/AltScreen controls, and unchanged HTTP/stdout behavior. Expected
  explicit reply format: `通过: <scenarios>` or `未通过: <scenario and reason>`.
  Next action: this Goal ends at the handoff; only a new Goal carrying an
  explicit acceptance result may continue G2 or change its state. G2 remains
  `active`; no successor Gate is activated.

- 2026-09-04: explicit manual acceptance result received: `验收通过`.
  This accepts every scenario in the recorded checklist: colored `120x40` and
  `NO_COLOR=1` `40x15` PTYs for `export env`, `config cm test`, `git pulse`,
  and `upgrade` (including the pre-schedule cancellation); one bounded text
  session and one NDJSON session for both `diff` and `fs`; and unchanged
  stream, HTTP, redaction, Transcript/Result, and no-AltScreen behavior.
  Manual Exit condition 3 therefore passed. Together with the recorded
  automated evidence, Exit conditions 1-4 are all satisfied. Final evidence:
  the declared package suite, `make command-surface`, rerun `make check`, the
  tagged standalone acceptance subset, and `git diff --check` all passed.
  G2 is marked `passed`. Direct successor G3 is marked `active` and recorded
  as `已激活，尚未开始实施`; no G3 reading, analysis, implementation, or
  verification was performed in this Goal.

### G3: Mutation and archive recovery

- 2026-09-04: initialized as `planned`; no recovery implementation evidence.
- 2026-09-04: 已激活，尚未开始实施。
- 2026-09-04: completed slice `config fork add descriptor/context`. The
  command now opens the frozen Console seam before any store construction or
  form, with `YCY / config fork add`, bounded `provider connection setup`
  target, and static `git fork configuration` scope metadata. Existing five
  prompts, validation, silent alias overwrite, encryption, cancellation, and
  result/Automation boundaries remain unchanged. Modified:
  `pkg/cmd/config/fork/add/{command.go,terminal_test.go}`. Directed
  verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  -count=1 ./pkg/cmd/config/fork/add` passed. Risk: this command still needs
  fresh wide/compact B PTY and mutation/race evidence. Next action: implement
  only the `config fork remove` descriptor/context slice.
- 2026-09-04: completed slice `config fork remove descriptor/context`. The
  destructive route now opens the frozen Console seam before configuration
  access, with `YCY / config fork remove`, bounded `provider connection
  removal` target, and static git-fork scope metadata. Persisted ordering,
  first-item selection, default-No confirmation, idempotent missing-write
  result, cancellation, and Automation read-before-reject behavior remain
  unchanged. Modified: `pkg/cmd/config/fork/remove/{command.go,
  terminal_test.go}`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/fork/remove` passed. Risk: fresh wide/compact B PTY,
  failure/cancellation, and race evidence remain. Next action: implement only
  the `config cm add` descriptor/context slice.
- 2026-09-04: completed slice `config cm add descriptor/context`. The
  four-field profile setup now opens the frozen Console seam before store
  construction, with `YCY / config cm add`, bounded `commit message profile
  setup` target, and static commit-message configuration scope. Prompt order,
  validation retries, masked/encrypted API key, overwrite/default behavior,
  cancellation, and Automation pre-store rejection remain unchanged.
  Modified: `pkg/cmd/config/cm/add/{command.go,terminal_test.go}`. Directed
  verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  -count=1 ./pkg/cmd/config/cm/add` passed. Risk: fresh wide/compact B PTY,
  failure/cancellation, and race evidence remain. Next action: implement only
  the `config cm set` descriptor/context and atomic-phase slice.
- 2026-09-04: completed slice `config cm set descriptor/context`. The atomic
  noninteractive update now opens the frozen Console seam before store
  construction, with `YCY / config cm set`, bounded `commit message profile
  update` target, static configuration scope, and safe bounded profile/setting
  metadata; the requested value is never placed in the descriptor. Exact
  three-argument writer semantics, key parsing/normalization, encryption,
  failure precedence, Plain loading output, Automation silence, and the single
  `update-cm-profile` phase remain unchanged. Modified:
  `pkg/cmd/config/cm/set/{command.go,terminal_test.go}`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/cm/set` passed. Risk: fresh Rich PTY and race evidence for
  this command remain. Next action: implement only the `config cm remove`
  descriptor/context slice.
- 2026-09-04: completed slice `config cm remove descriptor/context`. The
  validated destructive route now opens the frozen Console seam before store
  access, with `YCY / config cm remove`, bounded `commit message profile
  removal` target, static configuration scope, and a safe bounded profile
  metadata projection. Exact profile lookup, validation-before-confirmation,
  default-No confirmation, default reassignment, cancellation, Automation
  read-before-reject behavior, and the two real Work Phases remain unchanged.
  Modified: `pkg/cmd/config/cm/remove/{command.go,terminal_test.go}`. Directed
  verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  -count=1 ./pkg/cmd/config/cm/remove` passed. Risk: fresh wide/compact B PTY
  and race evidence for this command remain. Next action: implement only the
  `rm` descriptor/context slice.
- 2026-09-04: completed slice `rm descriptor/context`. The destructive cleanup
  adapter now opens the frozen Console seam before working-directory access,
  with `YCY / rm`, a safe static cleanup target, and bounded route/mode
  metadata for explicit versus smart cleanup and force versus
  default-negative confirmation. The established introduction wording remains
  visible through the safe target projection; original paths are never put in
  the descriptor. Path resolution, risk warnings, confirmation defaults,
  concurrent deletion, partial-success results, cancellation, and Automation
  boundaries remain unchanged. Modified:
  `pkg/cmd/rm/{terminal.go,console_test.go}`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 ./pkg/cmd/rm`
  passed. Risk: fresh compact/wide B evidence is present for existing journeys,
  but full mutation/race/repository evidence remains. Next action: implement
  only the `zip` descriptor/context slice.
- 2026-09-04: completed slice `zip descriptor/context`. The finite archive
  adapter now opens the frozen Console seam before workspace discovery, with
  `YCY / zip`, a bounded archive-planning target, safe summary/directory
  metadata, and explicit `with-dir` and reveal mode labels. Absolute or
  control-bearing directory arguments are reduced to safe labels while the
  original directory, output-name, glob, archive, and revealer values remain
  unchanged for command execution. Automation rejection and all existing
  planning/phase/result boundaries remain unchanged. Modified:
  `pkg/cmd/zip/{terminal.go,console_test.go}`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 ./pkg/cmd/zip`
  passed. Risk: fresh wide/compact B PTY archive evidence remains. Next
  action: add the required fresh `config cm set` Rich PTY and stream evidence.
- 2026-09-04: completed slice `config cm set Rich PTY and stream evidence`.
  Added a real-PTY four-way journey (colored and `NO_COLOR=1`, `120x40` and
  `40x15`) around a blocked atomic writer. The live B bar/table and compact
  degradation are asserted before release; the completed phase, API-key
  redaction, primary-screen restoration, Transcript-before-Result ordering,
  and unchanged `Profile work updated` result are asserted afterward. The
  existing Plain, Automation, failure, parser, encryption, and writer-boundary
  tests remain green. Modified:
  `pkg/cmd/config/cm/set/terminal_pty_test.go`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/cm/set -run 'TestRunCMSetRichPTY|TestRunSet'` passed.
  Risk: the complete CM-set package and race run remain in the Gate-level
  verification. Next action: add fresh `config cm remove` Rich PTY and stream
  evidence, including cancellation and default reassignment safety.
- 2026-09-04: completed slice `config cm remove Rich PTY and stream evidence`.
  Added a real-PTY four-way journey (colored and `NO_COLOR=1`, `120x40` and
  `40x15`) around default-profile confirmation and a blocked removal writer.
  The live B bar/table, bounded profile context, compact confirmation context,
  default-removal guidance on wide screens, primary-screen restoration,
  Transcript-before-Result ordering, completed removal phase, and safe
  profile metadata projection are asserted; the writer marker proves the
  mutation boundary and no profile credential/model fixture escapes. The
  existing Plain, Automation, cancellation, failure, persistence, and
  default-reassignment tests remain green. Modified:
  `pkg/cmd/config/cm/remove/terminal_pty_extended_test.go`. Directed
  verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  -count=1 ./pkg/cmd/config/cm/remove -run
  '^TestRunCMRemoveRichPTYFourWayBJourney$'` passed. Risk: fresh `rm` and
  `zip` PTY/behavior evidence plus Gate-level race and repository checks
  remain. Next action: add only the `rm` fresh Rich PTY and cancellation slice.

- 2026-09-04: completed slice `rm fresh Rich PTY mutation and cancellation
  evidence`. Added real-PTY four-way explicit-path mutation and Esc-cancellation
  journeys (colored and `NO_COLOR=1`, `120x40` and `40x15`). The mutation
  fixture blocks the real remover until the parent observes the active
  `Delete selected paths` phase, then verifies the file is deleted, the safe
  newline path projection never leaks raw input, the B descriptor/table and
  confirmation context are present, the primary screen is restored, and the
  ordered Transcript precedes the single `Done!` Result. The cancellation
  fixture verifies Esc leaves the target intact and never invokes the remover,
  with bounded cancellation Transcript and restored terminal in every mode.
  Existing smart-route, partial-deletion, missing-path, Plain, Automation, and
  context-cancellation tests remain green. Modified:
  `pkg/cmd/rm/terminal_pty_extended_test.go`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/rm -run
  '^TestRunRMExplicitRichPTYFourWay(Mutation|Cancellation)Journey$'` and the
  complete `./pkg/cmd/rm` package both passed. Risk: fresh `zip` PTY/archive
  evidence plus Gate-level race and repository checks remain. Next action: add
  only the `zip` fresh Rich planning/archive slice.
- 2026-09-04: completed slice `config fork add fresh four-way Rich PTY
  evidence`. Extended the existing real-PTY journey to colored and
  `NO_COLOR=1` `120x40` and `40x15` surfaces, preserving all five prompts,
  provider/default selections, encrypted-token boundary, phase catalog,
  restoration, Transcript ordering, and the single durable success result.
  Compact assertions account for bounded wrapping while retaining the same
  semantic evidence. Modified:
  `pkg/cmd/config/fork/add/terminal_pty_test.go`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/fork/add -run
  '^TestRunForkAddRichPTYRestoresScreenAndRedactsTranscript$'` passed. Risk:
  fresh `config fork remove` and `config cm add` four-way evidence plus
  Gate-level race and repository checks remain. Next action: extend only the
  `config fork remove` Rich PTY slice.
- 2026-09-04: completed slice `config fork remove fresh four-way Rich PTY
  evidence`. Extended the real-PTY removal journey to colored and
  `NO_COLOR=1` `120x40` and `40x15` surfaces, preserving ordered selection,
  default-negative confirmation, bounded host projection, removal phase,
  primary-screen restoration, Transcript-before-Result ordering, and the
  writer marker. Compact input waits on stable truncated selection and
  confirmation markers while wide input retains the full labels. Modified:
  `pkg/cmd/config/fork/remove/terminal_pty_test.go`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/fork/remove -run
  '^TestRunForkRemoveRichPTYRestoresScreenAndProjectsTranscript$'` passed.
  Risk: fresh `config cm add` four-way evidence plus Gate-level race and
  repository checks remain. Next action: extend only the `config cm add` Rich
  PTY slice.
- 2026-09-04: completed slice `config cm add fresh four-way Rich PTY
  evidence`. Extended the four-field real-PTY journey to colored and
  `NO_COLOR=1` `120x40` and `40x15` surfaces, preserving prompt order,
  secret-input redaction, collection/save phase catalog, primary-screen
  restoration, Transcript-before-Result ordering, and the writer marker.
  Compact synchronization uses the bounded `base` field fragment while wide
  synchronization retains the full URL label. Modified:
  `pkg/cmd/config/cm/add/terminal_pty_test.go`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  ./pkg/cmd/config/cm/add -run
  '^TestRunCMAddRichPTYRestoresScreenAndRedactsTranscript$'` passed. Risk:
  the fresh `zip` log entry and Gate-level race and repository checks remain.
  Next action: record the completed fresh `zip` PTY/archive evidence, then run
  the declared G3 repository verification.
- 2026-09-04: completed slice `zip fresh four-way Rich PTY planning and
  archive evidence`. The real-PTY journey passed colored and `NO_COLOR=1`
  `120x40` and `40x15` surfaces, covering all four planning interactions,
  ordered archive phases, bounded directory/with-dir/reveal metadata,
  primary-screen restoration, Transcript-before-Result ordering, and the
  reveal handoff. Disposable fixtures verified `with-dir` archive entries,
  hidden-file exclusion, exact archive contents, and no absolute path, URL,
  or hidden-content leakage. Modified:
  `pkg/cmd/zip/terminal_pty_extended_test.go`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 ./pkg/cmd/zip
  -run '^TestRunZIPRichPTYFourWayPlanningArchiveJourney$'` passed. Risk:
  Gate-level race and repository checks remain. Next action: run the declared
  G3 repository verification sequence.
- 2026-09-04: G3 Repository verification step 1 passed in the declared order:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./pkg/cmd/config/...
  ./pkg/cmd/rm ./pkg/cmd/zip ./internal/terminal`. All configuration,
  destructive, archive, and shared-terminal packages passed. Risk: targeted
  race evidence and the remaining repository checks remain. Next action: run
  G3 Repository verification step 2 for every touched mutation package.
- 2026-09-04: G3 Repository verification step 2 passed: targeted `-race`
  coverage for `config/fork/add`, `config/fork/remove`, `config/cm/add`,
  `config/cm/set`, `config/cm/remove`, `rm`, and `zip` all passed with
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0`. No race reports were
  emitted. Risk: command-surface, repository check, standalone acceptance,
  and diff checks remain. Next action: run `make command-surface`.
- 2026-09-04: G3 Repository verification step 3a passed: `make
  command-surface` completed successfully (`pkg/cmd/root` tests green).
  Risk: `make check`, standalone acceptance, and diff checks remain. Next
  action: run `make check`.
- 2026-09-04: G3 Repository verification step 3b passed: `make check`
  completed successfully. Frontend lint, typecheck, Vitest, Vite asset
  verification, and the full Go repository test suite passed; the existing
  Vite chunk-size warning was non-fatal. Risk: standalone acceptance and diff
  checks remain. Next action: run the declared G3 standalone acceptance
  command set.
- 2026-09-04: G3 Repository verification step 4 passed: the declared tagged
  standalone acceptance set for `config fork add/remove`, `config cm
  add/set/remove`, `rm`, and `zip` passed in `./acceptance`. Risk: only the
  final `git diff --check` repository check remains before preparing the
  manual acceptance fixtures. Next action: run `git diff --check`.
- 2026-09-04: G3 Repository verification step 5 passed: `git diff --check`
  reported no whitespace errors. All declared automated repository checks are
  green. Risk: manual acceptance handoff remains; no further implementation
  is authorized after the handoff record. Next action: build a fresh local
  binary and create disposable G3 acceptance fixtures.
- 2026-09-04: G3 manual acceptance handoff issued; explicit confirmation is
  pending and G3 remains `active`. Acceptance entry:
  `/Users/qigong-it-1/Workspace/hackycy-cli/.scratch/terminal-experience-modernization/g3-acceptance/README.md`.
  The fresh local binary is
  `/Users/qigong-it-1/Workspace/hackycy-cli/.scratch/terminal-experience-modernization/g3-acceptance/bin/ycy-g3`;
  its isolated configuration and project fixtures are under the same
  `g3-acceptance/{home,project}` directory and can be restored with
  `reset-fixtures.sh`. Run all seven commands through the README's colored
  `120x40` `wide` entry and `NO_COLOR=1` `40x15` `compact` entry: `config fork
  add/remove`, `config cm add/set/remove`, `rm`, and `zip`. Include the listed
  cancellation (`rm`: first `n`, then reset and `y`) and the listed Automation
  invocation (`printf 'ignored\\n' | ... config cm add`); the latter was
  freshly checked to reject before mutation, with the fixture config SHA-256
  unchanged. Minimum acceptance checklist: bounded B descriptor/focus/phase
  rows; understandable forms and destructive wording at both sizes; no
  credential, raw URL, absolute-path, hidden-file, or raw-error leakage;
  cancellation and Automation have no later side effect; primary-screen
  restoration and exactly one Result after Transcript; and the ZIP contains
  only the expected visible entries. Automated evidence: all G3 directed
  command/PTY tests; repository configuration/destructive/archive test suite;
  targeted `-race` for all seven mutation packages; `make command-surface`;
  `make check`; tagged standalone acceptance for all seven commands; and
  `git diff --check` all passed. Reply exactly once with `通过: <commands>` or
  `未通过: <command and reason>`. Next action: only a new Goal carrying that
  explicit result may resume G3; this Goal ends at this handoff.
- 2026-09-04: G3 manual acceptance result received: the user replied
  `验收通过`. Exit condition 3 is satisfied. Itemized final evidence: (1)
  every listed configuration, destructive, and archive command has fresh B
  form/phase/Transcript/PTY evidence with behavior baselines preserved; (2)
  mutation, cancellation, Automation, redaction, archive/deletion, and
  targeted race evidence passed for all applicable commands; (3) the recorded
  manual review scenarios were explicitly accepted by the user; and (4) the
  declared G3 repository sequence passed, including focused repository tests,
  targeted `-race`, `make command-surface`, `make check`, tagged standalone
  acceptance, and `git diff --check`. G3 Exit conditions 1-4 are therefore
  complete. G3 is marked `passed`; its direct successor G4 is changed from
  `planned` to `active` and recorded as `已激活，尚未开始实施`. No G4
  reading, analysis, implementation, or verification was performed in this
  Goal.

### G4: Git mutation and process handoff

- 2026-09-04: initialized as `planned`; no recovery implementation evidence.
- 2026-09-04: 已激活，尚未开始实施。本条仅记录 G3 完成后的直接后继
  激活；G4 不属于本次 Goal。
- 2026-09-04: completed slice `Git Fork archive-first four-way Rich PTY
  journey`. Added a disposable real-PTY fixture covering colored and
  `NO_COLOR=1` `120x40` and `40x15` surfaces, default-branch resolution,
  archive acquisition, bounded repository/destination/route metadata, the B
  `STATE / PHASE / DETAIL` projection, primary-screen restoration, safe
  provider projection, and Transcript-before-Result ordering. The fixture
  verified archive contents and destination creation without exposing the
  configured token or HTTP URL. Modified:
  `pkg/cmd/git/fork/terminal_pty_extended_test.go`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./pkg/cmd/git/fork
  -run '^TestGitForkRichPTYFourWayArchiveJourney$' -count=1` passed for all
  four surfaces. Risk: fallback, overwrite-decline/cancel, Git CM, Run, and
  Gate-level repository verification remain. Next action: add the Git Fork
  fallback and non-mutating cancellation PTY slice.

- 2026-09-04: completed slice `Git Fork fallback and overwrite safety
  four-way Rich PTY journeys`. Added disposable real-PTY fixtures covering
  archive failure to clone fallback with Git metadata removal, and both
  default-yes overwrite responses (decline and Esc cancellation) at colored
  and `NO_COLOR=1` `120x40` and `40x15` sizes. The fixtures asserted clone
  argv/ref, disk preservation on non-mutating exits, no network or credential
  leakage, B restoration, and safe cancellation/fallback Transcript facts.
  Modified: `pkg/cmd/git/fork/terminal_pty_extended_test.go`. Directed
  verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  ./pkg/cmd/git/fork -run '^TestGitForkRichPTYFourWay(FallbackJourney|OverwriteDeclineAndCancel)$'
  -count=1` passed for all eight scenarios. Risk: Git CM push/partial and
  Run handoff PTY evidence plus Gate-level repository verification remain.
  Next action: complete the Git CM Rich PTY slice.

- 2026-09-04: reverified slice `Git Fork fallback and overwrite safety` after
  the shared G4 fixture cleanup. The archive-first, clone-fallback,
  destination-preservation, default-yes decline, and Esc cancellation journeys
  still pass on colored and `NO_COLOR=1` wide/compact PTYs. Modified:
  `pkg/cmd/git/fork/terminal_pty_extended_test.go`. Directed verification:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  ./pkg/cmd/git/fork -run
  '^TestGitForkRichPTYFourWay(ArchiveJourney|FallbackJourney|OverwriteDeclineAndCancel)$'
  -count=1` passed. Risk: Git CM and Run slices still need their logged
  evidence and the Gate-level repository sequence. Next action: record Git CM
  stage/commit, push, and partial-result evidence.

- 2026-09-04: completed slice `Git CM stage/commit, push, and partial-result
  Rich PTY evidence`. Added four-way colored/no-color PTY journeys for staged
  selection plus commit and staged commit plus push, asserting phase order,
  confirmation, exactly-once durable results, provider/API-key redaction, and
  the committed partial boundary covered by the existing push-failure tests.
  The push fixture stages its change and creates a disposable bare remote before
  invoking the unchanged `Staged + Push` workflow. Modified:
  `pkg/cmd/git/cm/{terminal_pty_extended_test.go,terminal.go,run.go,commit.go,tracking.go}`.
  Directed verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go
  test ./pkg/cmd/git/cm -run
  '^TestGitCMRichPTYFourWay(StageAndCommitJourney|PushJourney)$' -count=1`
  passed; package partial-push coverage remains in `terminal_test.go` and
  `run_test.go`. Risk: Run selection and child handoff evidence plus the
  Gate-level repository sequence remain. Next action: record finite Run
  selection and Transcript replay evidence.

- 2026-09-04: completed slice `Run finite selection and Transcript replay
  evidence`. Added real-PTY four-way script/package-manager selection
  journeys, preserving declaration and lockfile order while asserting the B
  phase table, bounded project context, primary-screen restoration, ordered
  replay, and control-free `NO_COLOR=1` output. Modified:
  `pkg/cmd/run/{adapter.go,run.go,tracking.go,terminal_pty_extended_test.go}`.
  Directed verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go
  test ./pkg/cmd/run -run '^TestRunRichPTYFourWaySelectionAndTranscript$'
  -count=1` passed. Risk: release-before-exec and inherited child stream,
  cwd, signal, and exit evidence remain. Next action: record the real child
  handoff matrix.

- 2026-09-04: completed slice `Run release-before-exec and inherited-child
  evidence`. Added a real child PTY matrix for normal, exit-7, and SIGTERM/143
  outcomes across colored/no-color wide/compact surfaces. It asserts exact
  argv, inherited stdin/stdout/stderr and cwd, terminal release before
  `CHILD_START`, no parent decoration after startup, and child-owned exit
  behavior. The Unix process adapter places an inherited terminal child in the
  foreground while retaining an isolated process group for cancellation.
  Modified:
  `pkg/cmd/run/{process.go,process_unix.go,process_windows.go,adapter.go,run.go,terminal_pty_extended_test.go}`.
  Directed verification: `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go
  test ./pkg/cmd/run -run
  '^TestRunRichPTYFourWayHandoffAndChildStreams$' -count=1` passed after
  removing temporary diagnostic scaffolding. Risk: only the declared G4
  repository verification and manual acceptance handoff remain. Next action:
  run repository verification in the plan's exact order.

- 2026-09-04: G4 repository verification step 1 passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  ./pkg/cmd/git/fork ./pkg/cmd/git/cm ./pkg/cmd/run ./internal/gitprocess
  ./internal/terminal`. Git Fork, Git CM, Run, process, and shared terminal
  tests all passed. Risk: command-surface, full repository, standalone
  acceptance, and diff checks remain. Next action: run `make
  command-surface`.

- 2026-09-04: G4 repository verification step 2 passed: `make
  command-surface` completed with the root command-surface suite green. Risk:
  the full `make check`, tagged G4 standalone acceptance, and diff check
  remain. Next action: run `make check`.

- 2026-09-04: G4 repository verification step 3 passed: `make check`
  completed the lock/toolchain isolation checks, Web lint/typecheck/Vitest,
  Vite asset verification, Go vet, and the full Go repository test suite.
  The existing Vite chunk-size advisory was non-fatal. Risk: tagged G4
  standalone acceptance and `git diff --check` remain. Next action: run the
  declared Git Fork, Git CM, and Run standalone acceptance tests.

- 2026-09-04: G4 repository verification step 4 passed: the declared tagged
  standalone acceptance command
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  -tags=acceptance ./acceptance -run
  '^(TestGitForkStandaloneBinary|TestGitCMStandaloneBinary|TestRunStandaloneBinary)'`
  passed for all three binaries. Risk: only the final whitespace/diff check
  remains before preparing manual G4 fixtures. Next action: run `git diff
  --check`.

- 2026-09-04: G4 repository verification step 5 passed: `git diff --check`
  reported no whitespace errors. The full declared automated set is green:
  directed Git Fork archive/fallback/overwrite PTYs, Git CM stage/commit and
  push PTYs plus partial-push package coverage, Run selection and child
  handoff PTYs; repository step 1; `make command-surface`; `make check`; and
  tagged standalone Git Fork/Git CM/Run acceptance. Risk: only the required
  human review remains. Next action: start the disposable local fixture and
  issue one G4 acceptance handoff.

- 2026-09-04: G4 manual acceptance handoff issued; explicit confirmation is
  pending and G4 remains `active`. Acceptance entry:
  `/Users/qigong-it-1/Workspace/hackycy-cli/.scratch/terminal-experience-modernization/g4-acceptance/README.md`.
  The fresh CGO-free binary is
  `/Users/qigong-it-1/Workspace/hackycy-cli/.scratch/terminal-experience-modernization/g4-acceptance/bin/ycy`.
  The disposable fixture server is running at
  `http://github.localhost:8765` (launchctl label `ycy-g4-fixture`, current
  PID 2733); its GitHub-style archive and CM provider endpoints were health
  checked, and the local CM repository/bare remote and Run child fixture were
  reset successfully. Review the three recorded scenarios from the README:
  Git Fork archive-first acquisition, Git CM staged commit and push (or the
  documented committed-partial push failure), and a real `run` child handoff.
  Execute each at `120x40` and `40x15`, using `NO_COLOR=1` for the compact
  pass. Minimum acceptance checklist: (1) B command/status bar, safe context,
  truthful phases, and readable forms at both sizes; (2) primary-screen
  restoration, ordered Transcript before exactly one durable result, and no
  credential/raw URL/absolute-path/hidden-entry leakage; (3) Git Fork archive
  contents and destination behavior; (4) Git CM commit/push or committed
  partial state and redacted provider data; (5) Run release-before-exec with
  child-owned argv, cwd, stdin/stdout/stderr, and no parent decoration after
  `RUN_CHILD_START`. Reply exactly once with `通过: <scenarios>` or
  `未通过: <scenario and reason>`. Next action: only a new Goal carrying
  that explicit result may resume G4; this Goal ends at this handoff.

- 2026-09-05: G4 manual acceptance result received: the user replied
  `验收通过`. The recorded Git Fork archive/fallback/overwrite scenarios,
  Git CM staged commit/push and partial-result scenarios, and Run
  release-before-exec child-handoff scenarios are accepted at the requested
  wide/compact and color/no-color surfaces. Exit condition 3 is satisfied.
  No additional implementation or verification was performed after the
  handoff.

- 2026-09-05: G4 final acceptance completed and Gate status changed to
  `passed`. Exit condition 1 evidence: Git Fork and Git CM mutation,
  cancellation, Result, and redaction contracts are covered by the recorded
  four-way PTY journeys and package tests. Exit condition 2 evidence: Run
  release-before-exec, inherited argv/stdin/stdout/stderr/cwd, normal,
  non-zero, and SIGTERM/143 child outcomes are covered by the recorded PTY
  matrix and process tests; no parent decoration appears after child startup.
  Exit condition 3 evidence: the explicit user acceptance above covers all
  recorded manual scenarios. Exit condition 4 evidence: the declared G4
  repository sequence passed, including focused package/process tests, `make
  command-surface`, `make check`, tagged standalone Git Fork/Git CM/Run
  acceptance, and `git diff --check`. G4 is complete; its direct successor
  G5 is activated below and has no implementation or verification in this
  Goal.

### G5: Service lifecycles and detached updater

- 2026-09-04: initialized as `planned`; no recovery implementation evidence.
- 2026-09-05: 已激活，尚未开始实施。本条仅记录 G4 通过后的直接后继
  激活；G5 不属于本次 Goal。
- 2026-09-05: completed slice `Tunnel Connect bounded selection projection`.
  Modified `pkg/cmd/tunnel/connect/terminal.go` and its focused tests. The
  ambiguous remembered-connection form now opens a bounded command-owned
  Console descriptor; labels contain only a normalized origin, validated
  last-authenticated time, and ordinals for equal origins. Opaque connection
  IDs remain values and token, ID, and state-path material is absent from the
  projection. Cancellation is a diagnostic-stream semantic milestone. Directed
  verification passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  ./pkg/cmd/tunnel/connect ./internal/terminal ./internal/terminaltest`.
  Risk: the continuing client session still emits legacy unstructured records
  and non-cancellable reconnect sleeps. Next action: implement the Tunnel
  Connect lifecycle event and shutdown slice.

- 2026-09-05: completed slice `Tunnel Server lifecycle and FRPS boundary`.
  Modified `pkg/cmd/tunnel/server/{run.go,lifecycle.go,server_runtime.go,
  server_listener.go,server_agent.go,server_agent_protocol.go,server_http.go,
  server_frps.go}` and `internal/logging/logging.go`. Control-plane startup,
  listener readiness, server start/shutdown/stop, FRPS preparation and
  restart/stop intent/result events now have stable lifecycle IDs; FRPS child
  output remains bounded, debug-only, redacted, and control-character-clean.
  Agent connect/disconnect/restore/revoke and state/process warnings are
  projected with client-safe references, per-client/category aggregation, and
  revision/state deduplication. Existing NDJSON outer schema and service
  ownership remain unchanged. Directed verification passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  ./pkg/cmd/tunnel/server`. Risk: combined repository and tagged standalone
  evidence remains; next action: implement and verify the detached updater
  replacement slice.

- 2026-09-05: completed slice `Upgrade parent cancellation and detached
  ownership boundary`. Modified `internal/updater/upgrade.go`,
  `pkg/cmd/upgrade/terminal.go`, and `pkg/cmd/upgrade/presentation.go`.
  Scheduling now checks cancellation before spawning and uses an unbound
  detached command after launch, so parent cancellation cannot terminate the
  updater child. Rich/Plain presentation prioritizes cancellation over the
  historical `ExitCodeError` abort wrapper and emits no `Update aborted.`
  result for a cancelled run. Directed verification passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/updater
  ./pkg/cmd/upgrade ./internal/ycycmd`. Risk: hidden replacement and state
  persistence still need the full rollback and exactly-once evidence. Next
  action: implement the hidden replacement/state slice.

- 2026-09-05: completed slice `Detached updater replacement, rollback, and
  startup state`. Modified `internal/updater/{replace.go,state.go,
  replace_test.go,state_test.go}`, `internal/updater/upgrade.go`,
  `pkg/cmd/upgrade/{terminal.go,presentation.go,presentation_test.go}`, and
  `internal/ycycmd/main.go`. Detached children no longer inherit the parent
  cancellation context; replacement re-verifies the installed hash/version,
  propagates rollback failure (including an initially absent target), keeps
  cleanup warnings distinct, removes staged/updater debris on failed waits,
  and treats completed transactions as idempotent. Completed startup state is
  consumed once under a process-local race guard, and persisted failure text is
  limited to safe categories. Directed verification passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/updater
  ./pkg/cmd/upgrade ./internal/ycycmd`; race verification passed for
  `./internal/updater ./pkg/cmd/upgrade`; tagged standalone detached
  replacement/rollback/self-check acceptance passed. Risk: the combined G5
  repository sequence and local service/updater handoff remain. Next action:
  run the declared G5 repository verification in order.

- 2026-09-05: completed slice `Tunnel standalone help compatibility assertion`.
  The G5 standalone acceptance retained its complete tunnel flag and
  validation coverage while aligning the two heading checks with the approved
  G1 frozen durable discovery surface (`Flags:` rather than the superseded
  Cobra-only `Global Flags:`). Modified `acceptance/tunnel_test.go` only.
  Directed verification passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  -tags=acceptance ./acceptance -run
  '^TestTunnelServerStandaloneBinaryPreservesCLIValidation$'`. Risk: none
  observed in the focused compatibility check; the full G5 repository
  sequence remains. Next action: run repository verification step 1.

- 2026-09-05: repository verification step 1 passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test
  ./pkg/cmd/tunnel/... ./pkg/cmd/upgrade ./internal/tunnelruntime
  ./internal/updater ./internal/ycycmd ./internal/terminal`. All targeted
  Tunnel, Upgrade, runtime, updater, process, and shared terminal packages
  passed. Risk: command-surface and the full repository check remain. Next
  action: run `make command-surface`.

- 2026-09-05: repository verification step 2a passed: `make
  command-surface` completed in read-only comparison mode. The frozen command
  manifest, all help documents (including the approved `Flags:` discovery
  heading), and completion artifacts remain unchanged. Risk: `make check`,
  tagged G5 acceptance, and diff hygiene remain. Next action: run `make
  check`.

- 2026-09-05: repository verification step 2b passed: `make check` completed
  the repository Go, architecture/dependency, Web lint/type/test/build, and
  generated-asset checks. The existing Vite chunk-size advisory was
  informational only; no check failed. Risk: tagged G5 acceptance and diff
  hygiene remain. Next action: run the declared standalone acceptance regex.

- 2026-09-05: repository verification step 3 passed:
  `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1
  -tags=acceptance ./acceptance -run
  '^(TestTunnelServerStandaloneBinary|TestDetachedGoToGoStandaloneReplacementRollbackAndSelfCheck)'`.
  The standalone Tunnel server validation/help journey and detached Go-to-Go
  replacement, rollback, and self-check journey both passed. Risk: only final
  diff hygiene remains before the manual handoff. Next action: run `git diff
  --check`.

- 2026-09-05: repository verification step 4 passed: `git diff --check`
  reported no whitespace errors. The complete declared G5 repository sequence
  is now green: focused package tests, `make command-surface`, `make check`,
  tagged standalone Tunnel/updater acceptance, and diff hygiene. The existing
  Vite chunk-size advisory from `make check` remains informational only. Risk:
  automated Exit conditions are evidenced; the recorded local service and
  updater sessions require the one requested human review. Next action: hand
  off G5 manual acceptance and end this Goal without changing Gate state.

- 2026-09-05: G5 manual acceptance handoff is pending confirmation. Exact
  acceptance root is `/tmp/hackycy-g5-acceptance.rpDCgB`. Service entry points
  are the detached `screen` sessions `ycy-g5-text` (text Lifecycle Log;
  control `127.0.0.1:17500`, FRP `17000`, HTTP-vhost `18080`) and `ycy-g5-json`
  (NDJSON Lifecycle Log; control `127.0.0.1:17501`, FRP `17001`, HTTP-vhost
  `18081`); the text and NDJSON logs are respectively
  `server-text.log` and `server-json.log`, with `client.log` and
  `client-json.log` for the client sessions. The built CLI is
  `/tmp/hackycy-g5-acceptance.rpDCgB/ycy`; the updater fixture is
  `/tmp/hackycy-g5-acceptance.rpDCgB/updater-fixture`, whose target is currently
  `2.0.0` and whose completed state file is
  `ycy-target.go-update-state.json`. Automated evidence is the passed G5
  repository sequence recorded immediately above plus the directed package,
  race, PTY, and tagged standalone evidence in this log. Minimal review:
  (1) inspect colored and `NO_COLOR=1` text plus NDJSON service logs for
  startup, listener/control-plane availability before FRPS, agent/state or
  warning/recovery density, and one terminal shutdown record with no
  AltScreen/Transcript; (2) in a real TTY run
  `HOME=/tmp/hackycy-g5-acceptance.rpDCgB/home USERPROFILE=/tmp/hackycy-g5-acceptance.rpDCgB/home TERM=xterm-256color /tmp/hackycy-g5-acceptance.rpDCgB/ycy tunnel connect`
  and confirm bounded remembered selection contains only safe origin/time
  labels, cancellation exits once, and the continuing session is a plain
  Lifecycle Log; (3) separately run
  `/tmp/hackycy-g5-acceptance.rpDCgB/updater-fixture/ycy-target --help`
  once, confirm it reports `Updated ycy to v2.0.0.` and consumes the state,
  while `/tmp/hackycy-g5-acceptance.rpDCgB/updater-fixture/ycy-target --version`
  remains exactly `2.0.0` followed by a newline and does not consume it.
  Reply exactly once with `通过: <scenarios>` or
  `未通过: <scenario and reason>`. Next action: only a new Goal carrying
  that explicit result may resume G5; this Goal ends at this handoff and G5
  remains `active`.

- 2026-09-05: G5 final acceptance completed. The user replied `验收通过`,
  explicitly accepting the recorded manual scenarios. Exit condition 1
  evidence: the colored and no-color Tunnel text/NDJSON service sessions and
  client logs were reviewed for startup, control-plane availability before
  FRPS, agent/state and warning/recovery density, redaction, bounded output,
  and exactly-once shutdown; the bounded remembered-connection selector was
  reviewed for safe labels and cancellation, and continuing service output
  remained a Lifecycle Log without AltScreen or Transcript. Exit condition 2
  evidence: the existing G2 parent Upgrade flow and detached updater fixture
  were reviewed separately; replacement, rollback, cleanup-warning, and
  one-time startup-result behavior matched the recorded acceptance, while the
  target remained `2.0.0` and the state transaction was consumed once. Exit
  condition 3 evidence: the explicit user acceptance above covers every item
  in the G5 handoff checklist. Exit condition 4 evidence: repository
  verification steps 1-4 passed, comprising the targeted package tests,
  `make command-surface`, `make check`, tagged standalone Tunnel/updater
  acceptance, and `git diff --check`. G5 is marked `passed`; its direct
  successor G6 is activated below and has no implementation or verification
  in this Goal. This Goal is complete.

### G6: Release verification and full visual review

- 2026-09-04: initialized as `planned`; no recovery implementation evidence.
- 2026-09-05: 已激活，尚未开始实施。本条仅记录 G5 通过后的直接后继
  激活；G6 不属于本次 Goal。
- 2026-09-05: completed slice `terminal verification entrypoints`. Added the
  plan-required `make check-terminal` and `make acceptance-terminal` targets,
  including the help listing and the complete migrated command/package test
  matrix; no runtime behavior or frozen command surface changed. Modified:
  `Makefile`. Verification: `make check-terminal` passed across shared
  terminal/terminaltest, every finite command, both Lifecycle Log services,
  and the updater. Risk: prototype, tagged acceptance, release cross-build,
  and final repository evidence remain. Next action: run prototype and
  terminal acceptance verification only.
- 2026-09-05: completed slice `prototype and terminal acceptance verification`.
  The pinned `internal/terminal/prototype-vivid` module compiled and its
  package tests passed; `make acceptance-terminal` passed both tagged
  acceptance packages, including PTY, signal, child-process, service, and
  detached-updater journeys. Modified: no additional files. Risk: the exact
  G6 repository verification order and release-candidate build remain. Next
  action: run `make check` first.
- 2026-09-05: repository verification step 1 passed: `make check` completed
  lock, no-bun, Web, Go format/vet/unit, architecture, and asset checks. No
  source changes were required. Risk: command surface, tagged acceptance,
  cross-build, diff hygiene, and release-candidate visual handoff remain.
  Next action: run `make command-surface`.
- 2026-09-05: repository verification step 2 passed: `make command-surface`
  confirmed all frozen help and completion snapshots. No source changes were
  required. Risk: tagged acceptance, cross-build, diff hygiene, and release
  candidate review remain. Next action: run `make acceptance`.
- 2026-09-05: repository verification step 3 passed: `make acceptance`
  completed all tagged acceptance packages, including web, process, signal,
  PTY, service, and updater journeys. No source changes were required. Risk:
  only the six-target cross-build, diff hygiene, release-candidate fixture,
  and manual visual review remain. Next action: run `make cross-build`.
- 2026-09-05: repository verification step 4 passed: `make cross-build`
  produced all six supported darwin/linux/windows amd64/arm64 binaries after
  the exact-target rebuild; the first invocation's shell wrapper failure was
  unrelated to the build and the equivalent rerun exited 0. Repository
  verification step 5 passed: `git diff --check` reported no whitespace
  errors. `PNPM='pnpm dlx pnpm@11.13.0' make release-candidate` then built and
  verified the `0.1.0` release candidate and its `SHA256SUMS`; the candidate
  reports `0.1.0`. Modified: no runtime files. Risk: only the required
  release-candidate manual review remains. Next action: hand off the exact
  isolated fixture and review matrix once.
- 2026-09-05: G6 manual acceptance handoff recorded. Acceptance entry is the
  release candidate directory `release/0.1.0/`; on this host use
  `/tmp/hackycy-g6-acceptance.RQl0av/ycy` with isolated HOME
  `/tmp/hackycy-g6-acceptance.RQl0av/home`, workspace
  `/tmp/hackycy-g6-acceptance.RQl0av/workspace` (contains `.env`), and Git
  fixture `/tmp/hackycy-g6-acceptance.RQl0av/repo` (one committed baseline
  plus one working-tree change). Smoke evidence passed for `--version`,
  `config fork list`, `config cm list`, `export env`, and `git heat`.

  Automated evidence for review: the prototype module tests; `make
  check-terminal`; `make acceptance-terminal`; repository `make check`, `make
  command-surface`, `make acceptance`, `make cross-build`, and `git diff
  --check`; and release-artifact checksum verification. The minimum manual
  review is: in a colored `120x40` TTY and a `NO_COLOR=1` `40x15` TTY inspect
  root/help plus one representative from every finite family (`export env`,
  config fork/cm list/add/use/set/remove/test, git heat/pulse/fork/cm, `rm`,
  `zip`, `run`, and `upgrade`); inspect text sessions for `diff`, `fs`,
  `tunnel connect`, and `tunnel server`; and verify the Run child handoff and
  Upgrade detached replacement journey against their existing fixtures.
  Confirm the B bar/metadata/table/active region, paired state symbols,
  palette and Huh bottom focus, compact single-column behavior, restoration
  and Transcript replay order, safe projection/redaction, unchanged Result
  bytes/streams/exits, and separate bounded Lifecycle Log plus child-process
  boundaries. For each Service Command also run `--log-format=json` and
  confirm stable NDJSON with no ANSI or AltScreen controls.

  Reply exactly `通过: release candidate accepted` or `未通过: <scenario and
  reason>`. Next action: only a new Goal carrying that explicit result may
  continue G6. This Goal ends at the handoff; G6 remains `active`.
- 2026-09-05: G6 final acceptance completed. The user replied `通过`,
  explicitly accepting the release-candidate checklist. Exit condition 1
  evidence: the recorded terminal matrix and service reviews cover every
  finite Rich command/root-help B structure and every Service Command
  Lifecycle Log contract, including PTY dimensions, color modes, replay,
  redaction, and child-process boundaries. Exit condition 2 evidence: the
  frozen behavior, capability, stream, redaction, handoff, updater, and
  supported-target contracts are covered by the passed package, acceptance,
  cross-build, and release-artifact checks. Exit condition 3 evidence: `make
  check`, `make command-surface`, `make acceptance`, `make cross-build`,
  `make check-terminal`, `make acceptance-terminal`, prototype tests, and
  `git diff --check` passed; the `0.1.0` candidate and `SHA256SUMS` were
  verified. Exit condition 4 evidence: the explicit user acceptance above
  accepts the complete recorded release-candidate checklist. G6 is marked
  `passed`; no successor Gate exists. Terminal experience modernization
  effort is complete.
