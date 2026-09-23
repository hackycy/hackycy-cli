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
| G0: 新库与 Local 基础模型 | passed | none | `implementation-plan.md` -> `G0: 新库与 Local 基础模型` | E1/E2/E3 recorded below |
| G1: 单一 v5 Client 控制协议 | active | G0 | `implementation-plan.md` -> `G1: 单一 v5 Client 控制协议` | G0 passed |
| G2: Node 身份与管理链路 | planned | G1 | `implementation-plan.md` -> `G2: Node 身份与管理链路` | G1 pending |
| G3: Node FRPS 收敛与崩溃恢复 | planned | G2 | `implementation-plan.md` -> `G3: Node FRPS 收敛与崩溃恢复` | G2 pending |
| G4: Server 远端 Node 管理 | planned | G3 | `implementation-plan.md` -> `G4: Server 远端 Node 管理` | G3 pending |
| G5: Node 资源与 Client 归属 | planned | G4 | `implementation-plan.md` -> `G5: Node 资源与 Client 归属` | G4 pending |
| G6: 官方 Client 跨 Node 连接 | planned | G5 | `implementation-plan.md` -> `G6: 官方 Client 跨 Node 连接` | G5 pending |
| G7: 移除、部署与发布验收 | planned | G6 | `implementation-plan.md` -> `G7: 移除、部署与发布验收` | G6 pending |

## Progress Log

### G0: 新库与 Local 基础模型

- 2026-09-23: initialized as `active`; implementation has not started.
- 2026-09-23: slice 1 (startup preflight) complete. Changed `pkg/cmd/tunnel/server/database.go`, `state_test.go`, and the FRP fixture path in `client_forwarding_integration_test.go`. Existing database and WAL/SHM are inspected from a private copy before writable SQLite initialization; absent database with sidecars is rejected. Directed `TestOpenDatabaseRejectsUnknownWALWithoutChangingFiles|TestOpenState|TestInitializeDatabase` passed. Repository `go test ./pkg/cmd/tunnel/server/... ./internal/tunnelruntime/...` passed with `YCY_TUNNEL_TEST_FRP_RUNTIME_DIR` set to locally installed FRP 0.70.1; its frpc/frps SHA-256 matched the manifest. Initial unseeded run failed because the official archive download timed out. Risk: v2 remains supported until the schema/identity slice. Next: create v3 and cross-check the Controller identity before any new database write.
- 2026-09-23: slice 2 (Controller identity and v3 schema transaction) complete. Changed `database.go`, new `controller_identity.go`, `state.go`, `server_tunnels.go`, `state_test.go`, and Windows database test. Empty state creates one X25519 Controller key file and v3 transaction with the protected Local Node; repeat startup checks file/database public keys, rejects missing/mismatched identity, and can reuse the original identity after only the business database is lost. Old v1/v2/unknown and incomplete v3 fixtures are rejected before writes. The v3 route foreign key initially exposed a Local Tunnel write gap; the same transaction now maintains hostname owners and releases them on final-route deletion. Directed `TestOpenState|TestServerControlPlaneReservesHTTPRoutesTransactionally|TestGoClientToGoServerForwardsHTTPAndTCPAndUDPWithPinnedFRP` passed; repository `go test ./pkg/cmd/tunnel/server/... ./internal/tunnelruntime/...` passed using the verified local FRP fixture. Risk: Local runtime settings are not yet materialized in Node rows/port pool. Next: add Local startup projection and conflict checks.
- 2026-09-23: slice 3 (Local startup projection) complete. Added `server_local_node.go` and its focused tests; `server_runtime.go` now projects the configured Local port pool and explicit advertised endpoint before building account and control services. Shrinking past an occupied port or colliding with another active Node endpoint rejects the startup transaction without changing the saved projection. Directed `TestSyncLocalNodeProjection|TestServerRuntimeComposesOwnedResourcesAndReleasesThem` passed; repository `go test ./pkg/cmd/tunnel/server/... ./internal/tunnelruntime/...` passed with verified local FRP fixture. Risk: the allocation path still reads its in-memory range instead of the saved Node pool. Next: bind Local allocation to the saved pool and prove concurrent resource conflicts roll back.
- 2026-09-23: slice 4 (Node resource constraints) complete. Changed `server_clients.go`, `server_tunnels.go`, `server_local_node.go`, and added `server_node_resources_test.go`. Local Tunnel transactions now read the saved Node pool on each write; same-Node concurrent TCP allocation has one winner, another Node can reuse the number, and a hostname cannot be split across Nodes. Failed writes leave no Tunnel or revision residue. Directed `TestNodeResourceTransactionsEnforcePortAndHostnameOwnership|TestSyncLocalNodeProjection` passed; repository `go test ./pkg/cmd/tunnel/server/... ./internal/tunnelruntime/...` passed with the manifest-verified local FRP fixture. Risk: Gate-wide terminal verification and final E1/E2/E3 audit remain. Next: run the Local FRPS integration test and `make check-terminal`, then record exit evidence.
- 2026-09-23: G0 final acceptance and status transition. G0-E1: `TestOpenStateRejectsOldAndUnknownSchemasWithoutChangingFiles`, `TestOpenStateRejectsIncompleteV3WithoutChangingFiles`, `TestOpenStateControllerIdentityMismatchAndMissingDoNotChangeDatabase`, and `TestOpenStateReusesControllerIdentityWhenOnlyDatabaseIsLost` prove schema v3 empty initialization, idempotent restart, old/unknown/incomplete database rejection, Controller identity cross-check, and unchanged database bytes on rejection. G0-E2: `TestSyncLocalNodeProjectionRejectsDuplicateNodeEndpoint` and `TestNodeResourceTransactionsEnforcePortAndHostnameOwnership` prove immutable Local Node presence, default Local Client ownership, saved per-Node port-pool allocation, same-Node protocol/port conflict rollback, cross-Node numeric port reuse, and whole-transaction hostname ownership enforcement. G0-E3: `TestGoClientToGoServerForwardsHTTPAndTCPAndUDPWithPinnedFRP` passed with locally installed FRP 0.70.1 whose frpc/frps hashes match the manifest; second `make check-terminal` passed in full after one unrelated transient PTY failure on the first run. `git diff --check` passed. All G0 Exit conditions are satisfied; G0 marked `passed`, G1 marked `active` and activated without implementation. Effort for this Goal is complete; no G1 work was started.

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
