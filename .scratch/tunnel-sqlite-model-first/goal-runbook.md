# Tunnel SQLite 与 Ent 重构 Goal Runbook

## Source Baseline

| Path | SHA-256 |
| --- | --- |
| `.scratch/tunnel-sqlite-model-first/baseline.md` | `ed3da64ae9b7e2e9f89b37c7ec8903a98ca0683b6560152c9de3cd5896dc4c17` |
| `.scratch/tunnel-sqlite-model-first/implementation-plan.md` | `246413ad39367cbf87a7d2b6c848729f161465477b7d10a95b965aafd6947c26` |
| `.scratch/tunnel-sqlite-model-first/issues/02-state-isolation.md` | `8bf747456de2d200553727ea53bad575c0185fb16ba74ebce5961e3b411f0cf2` |
| `.scratch/tunnel-sqlite-model-first/issues/03-orm-prototype.md` | `11f54f1379d9bf45e5d912b5cf04957a85397d5e0688d8eb8fa5215d1657578d` |
| `.scratch/tunnel-sqlite-model-first/issues/04-orm-choice.md` | `f6401a55cdee760e9848b387ce4847106ce2d0b980c6272287f06ac6b181a6a2` |
| `.scratch/tunnel-sqlite-model-first/issues/05-server-invariants.md` | `405b36c9c815df48a1bf86ba807f158e1c4ee8d6b3066956265011461fffb3ff` |
| `.scratch/tunnel-sqlite-model-first/issues/06-node-state-model.md` | `9b41cf85d98f538576da639f0db85317f4b8995b4d48bde7ad76021359ae25d4` |
| `.scratch/tunnel-sqlite-model-first/issues/07-data-access-boundary.md` | `b249554751b2b2b5a1359fef8f66faf7522067329d3e4dbee917646399149992` |
| `.scratch/tunnel-sqlite-model-first/issues/08-schema-lifecycle.md` | `dfe2fe89cd4faa93ee2810475097cadea00099b3275cd77384409ca2ebffe02f` |
| `.scratch/tunnel-sqlite-model-first/issues/09-rollout-acceptance.md` | `47f07b1cbef550634e15c2202be0b3a2afc84eeab21b3c4a7986f15aefab0266` |
| `.scratch/tunnel-sqlite-model-first/map.md` | `cea9340b091b9f62de206e6a7e6fa2657aab4acacd2dd577514b8ac897e8935b` |
| `.scratch/tunnel-sqlite-model-first/prototype/ent-sqlite/README.md` | `8d9658f8dd144b03c23200ed6d697b26df559d57574ad998f294f0e7d216a021` |
| `.scratch/tunnel-sqlite-model-first/sqlite-maintenance-constraints.md` | `dbbe0fe549b55e42f1b0e85e48e7a6aae46fa41a3b306c61adbfb394f4e30801` |
| `CLAUDE.md` | `35a0f66a6531e61b35dc7288884da35eeef79ad1039f5ffdfd29295cee8d566e` |
| `ENGINEERING.md` | `13fe46814b45138d227a301eccda03a5f5ae81dece71a8ec738e4ef1257f6172` |
| `Makefile` | `656fd137023b4bd8653af1cdb8efa880692027934ff0c833bc8440306278a9e4` |

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
| G0: 建立 v1 SQL/Ent 基础 | active | none | `implementation-plan.md` -> `G0: 建立 v1 SQL/Ent 基础` | no predecessor |
| G1: 完成 Server 持久化层 | planned | G0 | `implementation-plan.md` -> `G1: 完成 Server 持久化层` | G0 pending |
| G2: 通过 Server 外部行为门禁 | planned | G1 | `implementation-plan.md` -> `G2: 通过 Server 外部行为门禁` | G1 pending |
| G3: 完成 Node 持久化与管理状态 | planned | G2 | `implementation-plan.md` -> `G3: 完成 Node 持久化与管理状态` | G2 pending |
| G4: 通过 Node 恢复与最终交付门禁 | planned | G3 | `implementation-plan.md` -> `G4: 通过 Node 恢复与最终交付门禁` | G3 pending |

## Progress Log

### G0: 建立 v1 SQL/Ent 基础

- 2026-09-26: initialized as `active`; implementation has not started.

### G1: 完成 Server 持久化层

- 2026-09-26: initialized as `planned`; no implementation evidence.

### G2: 通过 Server 外部行为门禁

- 2026-09-26: initialized as `planned`; no implementation evidence.

### G3: 完成 Node 持久化与管理状态

- 2026-09-26: initialized as `planned`; no implementation evidence.

### G4: 通过 Node 恢复与最终交付门禁

- 2026-09-26: initialized as `planned`; no implementation evidence.
