# Tunnel Node Goal Runbook

## Source Baseline

| Path | SHA-256 |
| --- | --- |
| `.scratch/tunnel-node/CONTEXT.md` | `6de849157e23c996df73b0102611a1d1a877306ae402ef032b0033ba362935e6` |
| `.scratch/tunnel-node/client-wire-switch.md` | `21727d3b1cca8cf080b8d9980df878f5b69df726b5fdf5d5a587f9df4817b5d9` |
| `.scratch/tunnel-node/deployment-acceptance.md` | `1b14f2c85adcd8a5e94c84d1d58b06e6365bad2788586f237ba8c57fe164fe95` |
| `.scratch/tunnel-node/implementation-plan.md` | `98f4efb555b49a23bf092da9edf1fc8f48fb6257084a5c5a269c8ec0f6d421f7` |
| `.scratch/tunnel-node/integration-outline.md` | `2c84521a51431d6f95f188839e0de001039200f01246f38411eef32e06e4a4e9` |
| `.scratch/tunnel-node/issues/01-claim-trust.md` | `5b33b52e220ecca0daa507ffbbbb1c810b3b9f538606eaa1e3745071a7587d57` |
| `.scratch/tunnel-node/issues/02-binding-lifecycle.md` | `c0134e86ac8b77fa73ef4c153ea59958a93ba8d431f3cbb94f67d86338fd232c` |
| `.scratch/tunnel-node/issues/03-local-remote-runtime-boundary.md` | `59a944d1068ad643db42dc834f98eb1fa2b7766fedfe5e89524ba55fc4faa367` |
| `.scratch/tunnel-node/issues/04-node-desired-state.md` | `704e33757abc8f72bc7415ec4a8e4d26fdc58f8090679dc54609012c80fba2fc` |
| `.scratch/tunnel-node/issues/05-client-node-assignment.md` | `5c1f2272d87cec88c2a21695171af56fa364eb2a7cbcdf388ec33a734de946b6` |
| `.scratch/tunnel-node/issues/06-node-resource-scope.md` | `f3a69d0fd96f36efd3cf9e23e8140126967953382bbb8e7f51046667cdc2fb19` |
| `.scratch/tunnel-node/issues/07-persistence-migration.md` | `47a5e9fda040e902c838b1549662d42a592c7d06ad981066175c93492143a69c` |
| `.scratch/tunnel-node/issues/08-management-api-permissions.md` | `a726550a5283ac576de95a4e999436974088ecd04de552fbae35e33a67aacbd4` |
| `.scratch/tunnel-node/issues/09-nodes-workflow-prototype.md` | `41c54950fc6e68afe44ff2898a3cf3e5af4a9fdb568a952cac8f3d1cbb4ad48d` |
| `.scratch/tunnel-node/issues/10-rollout-compatibility.md` | `4f6998165a4e0fcd601741cfc9fe5ec04baae21f3d15e8853c264395b9b98afa` |
| `.scratch/tunnel-node/issues/11-node-management-wire.md` | `6ef160053bd16afc8d4bdff2217f0c648d2220872e31912bd3fa381c89b58d10` |
| `.scratch/tunnel-node/issues/12-node-state-crash-consistency.md` | `ee321450fb0c820236c813a3b99097b2fab4e11ad29a0c0d2d52e20f96c8fa1f` |
| `.scratch/tunnel-node/issues/13-server-node-storage.md` | `5b5c93943ac27b0a44e07572ad85b04a52128c699c459a6ad76956efc6fb2d74` |
| `.scratch/tunnel-node/issues/14-client-wire-switch.md` | `9523fb610c6961580f7ce3de7b2beef2cce217d11e90a80290b89cc1fa014923` |
| `.scratch/tunnel-node/issues/15-node-management-api-contract.md` | `5a1ffc953a84840126fbababeefdac417317e9d0f09a6a943ad3296fe35489d9` |
| `.scratch/tunnel-node/issues/16-deployment-acceptance.md` | `0735dd6ed830a88059c91ba2093045f095d6f0add73d86ce674a71d91eb11c1b` |
| `.scratch/tunnel-node/map.md` | `7187e4db832c3f79f89f01f5cfd3a61f26fb90872e7f738d6074ac8090048c7c` |
| `.scratch/tunnel-node/node-api-contract.md` | `35a022b9f19c2d54677abc511ea290222e346ba86a20862ea80897cfe3daf7c0` |
| `.scratch/tunnel-node/node-management-wire.md` | `d8b8f16757108ee85b60dac33fd25bcaec2293f3c018b01a6794ad2bf07c3372` |
| `.scratch/tunnel-node/node-state-crash.md` | `f86df5c463f1f7d953359c88ff07516597b5473b1b4394dabb725b890f77a3ba` |
| `.scratch/tunnel-node/server-storage-transactions.md` | `bf7793b987a7e9968a0294b38332cea76774848aab1c43e18a6e10f06acfd129` |
| `.scratch/tunnel-node/spec.md` | `4e5ee7f2cbdba5283e1d4bb857226a6b3041e8e4636fccacb98d5cb2e81d33a4` |
| `CLAUDE.md` | `35a0f66a6531e61b35dc7288884da35eeef79ad1039f5ffdfd29295cee8d566e` |

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
| G0: 新库与 Local 基础模型 | active | none | `implementation-plan.md` -> `G0: 新库与 Local 基础模型` | no predecessor |
| G1: 单一 v5 Client 控制协议 | planned | G0 | `implementation-plan.md` -> `G1: 单一 v5 Client 控制协议` | G0 pending |
| G2: Node 身份与管理链路 | planned | G1 | `implementation-plan.md` -> `G2: Node 身份与管理链路` | G1 pending |
| G3: Node FRPS 收敛与崩溃恢复 | planned | G2 | `implementation-plan.md` -> `G3: Node FRPS 收敛与崩溃恢复` | G2 pending |
| G4: Server 远端 Node 管理 | planned | G3 | `implementation-plan.md` -> `G4: Server 远端 Node 管理` | G3 pending |
| G5: Node 资源与 Client 归属 | planned | G4 | `implementation-plan.md` -> `G5: Node 资源与 Client 归属` | G4 pending |
| G6: 官方 Client 跨 Node 连接 | planned | G5 | `implementation-plan.md` -> `G6: 官方 Client 跨 Node 连接` | G5 pending |
| G7: 移除、部署与发布验收 | planned | G6 | `implementation-plan.md` -> `G7: 移除、部署与发布验收` | G6 pending |

## Progress Log

### G0: 新库与 Local 基础模型

- 2026-09-23: initialized as `active`; implementation has not started.

### G1: 单一 v5 Client 控制协议

- 2026-09-23: initialized as `planned`; no implementation evidence.

### G2: Node 身份与管理链路

- 2026-09-23: initialized as `planned`; no implementation evidence.

### G3: Node FRPS 收敛与崩溃恢复

- 2026-09-23: initialized as `planned`; no implementation evidence.

### G4: Server 远端 Node 管理

- 2026-09-23: initialized as `planned`; no implementation evidence.

### G5: Node 资源与 Client 归属

- 2026-09-23: initialized as `planned`; no implementation evidence.

### G6: 官方 Client 跨 Node 连接

- 2026-09-23: initialized as `planned`; no implementation evidence.

### G7: 移除、部署与发布验收

- 2026-09-23: initialized as `planned`; no implementation evidence.
