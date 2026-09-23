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
| G1: 单一 v5 Client 控制协议 | passed | G0 | `implementation-plan.md` -> `G1: 单一 v5 Client 控制协议` | E1/E2/E3 recorded below |
| G2: Node 身份与管理链路 | active | G1 | `implementation-plan.md` -> `G2: Node 身份与管理链路` | G1 passed; activated, implementation not started |
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
- 2026-09-24: slice 1 (v5 message shape and explicit version rejection) in progress. Changed `internal/tunnelruntime/protocol.go`, added `protocol_v5_test.go` and `server_agent_v5_test.go`. `TestRuntimeDigestIsStableAcrossTunnelOrdering`, `TestV5WelcomeAndDesiredStateCarryTheSameRuntimeObject`, and `TestServerAgentRejectsV4HelloWithExplicitUpgradeClose` passed. Initial protocol package suite still fails on old v4 field assertions; no tests were removed or skipped. Risk: source compatibility bridges in protocol structs still need removal before single-v5 exit. Next: finish v5 wire fixtures and strict decoding.
- 2026-09-24: slice 2 (Server complete Local runtime read projection) in progress. Added `server_client_runtime.go`; changed `server_agent_protocol.go`, `server_agent.go`, and `server_agent_outbound.go`. The Server now reads Client, Node assignment, and Tunnel rows in one database transaction and sends a digest-bearing full runtime on both welcome and desired_state. Compile-only `go test ./pkg/cmd/tunnel/server ./pkg/cmd/tunnel/connect ./internal/tunnelruntime -run '^$'` passed. Existing Server suite fails on v4 fixtures as expected during migration. Risk: Local endpoint and Token changes still need revision handling. Next: migrate fixtures, assert equal runtime for welcome/online updates, and add revision tests.
- 2026-09-24: slice 3 (Client durable runtime high-water and deduplication) in progress. Changed `client_state.go`, `client_reconciler.go`, `client_agent.go`, and `client_run.go`; added `client_reconciler_v5_test.go`. `TestClientReconcilerRejectsSameRevisionWithDifferentRuntimeDigest` and `TestClientReconcilerNeverAppliesBelowPersistedHighestAcceptedRuntime` passed; Client compile-only test passed. Highest accepted target is atomically saved before local apply. Risk: same-Node rollback and Local FRPC observations require further verification; old Client tests still contain v4 wire assumptions. Next: complete Local apply/observation and run package suites.
- 2026-09-24: slice 1 (single v5 wire shape and rejection) continued. Changed `protocol.go`, `server_agent_protocol.go`, `client_agent.go`, and v5 protocol/agent fixture tests. Removed production welcome/desired-state v4 fields and hello compatibility decoding; v4 version is identified before v5 shape validation so it receives explicit 4406. Directed protocol/agent tests passed. First repository `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...` failed only in two Server gateway tests still sending v4 hello or lacking a valid v5 session; no tests were skipped. Risk: remaining fixtures may still assume v4. Next: migrate those fixtures and rerun repository verification before advancing slices.
- 2026-09-24: slice 1 complete. Changed `server_agent_test.go` to use v5 hello and runtime-qualified process reports. Directed `TestServerAgentGatewayObservesConnectionAndProcessStateChanges|TestServerAgentGatewayPresentsDurableRestartGenerationToActiveConnection|TestServerAgentRejectsV4HelloWithExplicitUpgradeClose` passed; repository `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...` passed using the installed FRP 0.70.1 fixture. Risk: Server Local endpoint/Token changes and independent FRPC observations are not yet fully covered. Next: verify and complete the Server runtime projection/revision slice.
- 2026-09-24: slice 2 complete. Added `server_client_runtime_test.go`; changed `server_client_runtime.go` and `server_runtime.go`. Server startup stores a Local endpoint/port/Token signature and advances assigned Client revisions transactionally only when it changes; full Client/Node/Tunnel runtime comes from one database transaction. Directed `TestSyncLocalClientRuntimeRevisionAdvancesOnlyWhenEndpointOrTokenChanges|TestServerAgentWelcomeAndOnlineDesiredStateUseIdenticalRuntime|TestServerAgentConnectionPresentsDesiredStateOnlyAfterWelcome` passed; repository `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...` passed with the installed FRP 0.70.1 fixture. Risk: Client high-water collision and independent FRPC observation still need audit. Next: complete Client persistence/deduplication slice.
- 2026-09-24: slice 3 complete. Changed `client_reconciler.go`, `client_state.go`, `client_agent.go`, `client_run.go`, and `client_reconciler_v5_test.go`. Lower revisions now fail protocol validation; same-revision conflict is checked before writing accepted state; corrupt accepted state halts application; in-process reconnect hello can use the persisted accepted target even when local apply fails. Directed `TestClientReconciler|TestClientRunCold|TestClientRunReconnect|TestClientAgentConnect` passed; repository `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...` passed with FRP 0.70.1. Risk: local failure disposition and independent FRPC observation remain. Next: complete Local FRPC application slice.
- 2026-09-24: slice 4 complete. Changed `client_run.go` and `client_run_test.go`. Successful apply reports `started` or `stopped_no_enabled_tunnels`; failed apply reports observed previous-running/stopped state when available or `unknown`; protocol/high-water corruption becomes fatal so shutdown stops the managed FRPC. Existing same-Node verification/start rollback and cold-start/reconnect behavior remain covered by directed `TestRunClient|TestClientReconciler`, which passed. Repository `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...` passed with FRP 0.70.1. Risk: `frpc_status` still needs independent wire reporting and Server expiry logic. Next: implement the observation slice.
- 2026-09-24: slice 5 complete. Changed `protocol.go`, `client_run.go`, `server_agent.go`, `server_agent_protocol.go`, `server_http_test.go`, and Local forwarding integration test; added `server_agent_frpc_status_test.go`. Client now sends independent `frpc_status` from the loopback authenticated FRPC API; Server matches the current session and `(revision,nodeId,digest)`, rejects unproven connected status, and degrades stale/disconnected observations to unknown. Directed `TestServerAgentFRPCStatusRequiresCurrentRuntimeAndExpires` and real `TestGoClientToGoServerForwardsHTTPAndTCPAndUDPWithPinnedFRP` passed; the latter observed all three proxies registered from FRP 0.70.1. Repository `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...` passed with the pinned runtime. Risk: Gate-wide terminal and independent-binary acceptance still remain. Next: format/audit diff, run `make check-terminal`, then `make acceptance`.
- 2026-09-24: Gate audit follow-up on slice 3. Changed `client_state.go`, `client_reconciler.go`, Client cache/reconciler/run tests, and the Local forwarding integration assertion. Removed redundant v4-shaped cache fields, made persisted runtime digest validation explicit, and fixed same-revision conflict tests to mutate the actual v5 runtime. First repository attempt found one integration fixture still reading `state.Snapshot`; after updating it, directed Client tests and `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...` passed with pinned FRP 0.70.1. Earlier `make check-terminal` passed on its second run after one unrelated, isolated `pkg/cmd/run` timing failure; `make acceptance` and standalone binary v4 rejection test had passed before this cache-only follow-up. Risk: gate-wide checks must be rerun after the follow-up. Next: rerun terminal then acceptance in plan order.
- 2026-09-24: Gate verification retry. After cache follow-up, two `make check-terminal` runs failed in the real Local forwarding test because durable applied revision stayed 0 at its 20-second deadline; one run used `GOFLAGS=-p=1` and therefore package parallelism alone does not explain it. The same test isolated with the terminal build environment passed in 6.89s, and the complete Server package later passed in 51.6s. Added secret-safe timeout diagnostics to the integration test for accepted/applied revisions and Gateway state; this is diagnostic only and does not weaken assertions. Risk: intermittent full-suite startup/apply delay remains unresolved. Next: rerun terminal suite and use the new evidence if it fails, then run acceptance only after terminal passes.
- 2026-09-24: G1 final acceptance and status transition. G1-E1: `TestProtocolV5MessagesRetainRestartRecoveryFields`, `TestServerAgentWelcomeAndOnlineDesiredStateUseIdenticalRuntime`, `TestServerAgentRejectsV4HelloWithExplicitUpgradeClose`, and standalone-binary `TestTunnelStandaloneBinaryRejectsV4ClientHello` prove complete v5 runtime on both messages and actual 4406 upgrade rejection of v4. G1-E2: Client reconciler high-water, corrupt-cache, same-revision conflict and cold-start/reconnect tests passed; `TestGoClientToGoServerForwardsHTTPAndTCPAndUDPWithPinnedFRP` passed with FRP 0.70.1 and actual HTTP/TCP/UDP round trips. G1-E3: `TestFRPCStatusObservationSeparatesRegisteredFailedAndUnknownProxies` and `TestServerAgentFRPCStatusRequiresCurrentRuntimeAndExpires` prove independent Local FRPC observation and stale/disconnected unknown projection; real integration observed all three proxies registered while `apply_result` remained separate. Final repository `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...` passed with pinned FRP. Final `GOMAXPROCS=2 GOFLAGS=-p=1 make check-terminal` passed in full after earlier load-sensitive timing failures in the Local integration and unrelated `pkg/cmd/run` 2-second PID fixture; no test was skipped or weakened. Final `make acceptance` passed (`acceptance` and `acceptance/web`), including independent binary v4 rejection. `git diff --check` passed. All G1 Exit conditions are satisfied; G1 marked `passed`, G2 marked `active` and activated without implementation. This Goal's work is complete; no G2 work was started.

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
