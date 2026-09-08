# B Ops Console Lifecycle Refactor Goal Runbook

## Source Baseline

| Path | SHA-256 |
| --- | --- |
| `.scratch/terminal-experience-modernization/implementation-plan.md` | `7622c8520c732988bf240d866ae91a599c5e7c42d7603eb3ac44890f9119d56a` |
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
| `CONTEXT.md` | `a3400217b0e5dfaf39dbb729c2a0fb6ae38d6fd3e1eab24d38e4a76bb55c6da0` |

## State Rules

- `implementation-plan.md` is the sole source of Gate contracts; this runbook records only state and evidence.
- Execute only the one `active` Gate in the Goal Ledger at a time.
- Append slice, changed files, verification, risks, and next action to that Gate's Progress Log each turn.
- `passed` requires explicit evidence for every plan Exit condition; ordinary implementation or failed verification remains `active`.
- Use `blocked` only for a plan Stop condition and record the blocker and recovery condition.
- Pending manual acceptance ends a Goal through a one-time handoff log entry; it is not `blocked`, and the Gate remains `active`.
- Passing a Gate activates only its direct successor and ends the current Goal. The successor is executed only by a new Goal.

## Goal Ledger

| Gate | Status | Depends on | Plan contract | Unlock evidence |
| --- | --- | --- | --- | --- |
| G0: Shared B lifecycle foundation | active | none | `implementation-plan.md` -> `G0: Shared B lifecycle foundation` | no predecessor; prior G0 evidence was superseded during the 2026-09-08 control-plane reset |
| G1: Read-only and selection migration | planned | G0 | `implementation-plan.md` -> `G1: Read-only and selection migration` | G0 pending |
| G2: Mutation and archive migration | planned | G1 | `implementation-plan.md` -> `G2: Mutation and archive migration` | G1 pending |
| G3: Handoff boundaries and release acceptance | planned | G2 | `implementation-plan.md` -> `G3: Handoff boundaries and release acceptance` | G2 pending |

## Progress Log

### G0: Shared B lifecycle foundation

- 2026-09-08: initialized as `active` after the lifecycle control-plane reset. No implementation or G0 evidence is imported; prior generic-renderer audit and `g0-*` artifacts are superseded. Next action: establish the semantic Catalog, `FinishRequest`, eager renderer, replacement, Outcome, and structured Transcript slices from the new plan.

### G1: Read-only and selection migration

- 2026-09-08: initialized as `planned`; no implementation evidence.

### G2: Mutation and archive migration

- 2026-09-08: initialized as `planned`; no implementation evidence.

### G3: Handoff boundaries and release acceptance

- 2026-09-08: initialized as `planned`; no implementation evidence.
