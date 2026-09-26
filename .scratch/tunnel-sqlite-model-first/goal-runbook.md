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
| G0: 建立 v1 SQL/Ent 基础 | passed | none | `implementation-plan.md` -> `G0: 建立 v1 SQL/Ent 基础` | commit `b133d73`; all G0 Exit conditions verified below |
| G1: 完成 Server 持久化层 | active | G0 | `implementation-plan.md` -> `G1: 完成 Server 持久化层` | G0 passed; activated, implementation not started |
| G2: 通过 Server 外部行为门禁 | planned | G1 | `implementation-plan.md` -> `G2: 通过 Server 外部行为门禁` | G1 pending |
| G3: 完成 Node 持久化与管理状态 | planned | G2 | `implementation-plan.md` -> `G3: 完成 Node 持久化与管理状态` | G2 pending |
| G4: 通过 Node 恢复与最终交付门禁 | planned | G3 | `implementation-plan.md` -> `G4: 通过 Node 恢复与最终交付门禁` | G3 pending |

## Progress Log

### G0: 建立 v1 SQL/Ent 基础

- 2026-09-26: initialized as `active`; implementation has not started.
- 2026-09-26: slice G0-1 (依赖与生成入口): 修改 `go.mod`、`go.sum`、`ent/generate.go`，固定 Ent v0.14.5 并声明 Server/Node 两个生成入口；`GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./ent` 通过。风险：生成器引入的间接依赖待生成后整理；下一步只建立 Server v1 SQL 结构资产与 Ent Schema。
- 2026-09-26: slice G0-2 (Server 结构): 新增 `pkg/cmd/tunnel/server/migrations/001_v1.sql`、`ent/server/schema/schema.go`、生成的 `ent/server/**` 和 `ent/schema_test.go`；SQL 纳入 Node candidates/observations，去除旧触发器与延迟复合外键，Ent 生成器运行通过，临时空库结构/关键约束测试 `go test -count=1 ./ent` 及 `CGO_ENABLED=0 go test ./ent/server/...` 通过。风险：新路由 ID 和附属表内部 ID 需 G1 调用方适配；本 Gate 不改业务调用。下一步只建立 Node v1 SQL 结构与 Ent Schema。
- 2026-09-26: slice G0-3 (Node 结构): 新增 `pkg/cmd/tunnel/node/migrations/001_v1.sql`、`ent/node/schema/schema.go` 与生成的 `ent/node/**`，扩展 `ent/schema_test.go`；`go generate ./ent` 与 `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test -count=1 ./ent/...` 通过，临时空库验证三张表、列、CHECK 和非法身份/修订/所有权写入。风险：G3 需把旧 Node 状态调用改接新表，G0 不改业务路径；下一步接入 Server 空库初始化辅助代码。
- 2026-09-26: slice G0-4 (Server 空库入口): 新增 `pkg/cmd/tunnel/server/database_v1.go`、`database_v1_test.go`，使用嵌入 SQL 在空 `server-state-v1` 创建数据库并由 ncruces 连接池构造 Ent client；`go mod tidy` 补齐新依赖校验和，定向 `CGO_ENABLED=0 go test -count=1 ./pkg/cmd/tunnel/server -run '^TestOpenEmptyServerV1Database$'` 通过，覆盖 Ent 查询、非空目录拒绝及旧 `go-v1` 文件不变。风险：现有 `OpenState` 仍走旧路径，按 G1 范围再接入；下一步只实现 Node 空库入口。
- 2026-09-26: slice G0-5 (Node 空库入口): 新增 `pkg/cmd/tunnel/node/database_v1.go`、`database_v1_test.go`，在空 `node-state-v1` 先创建私有标记，再用嵌入 SQL 建库并接入 ncruces/Ent；定向 `CGO_ENABLED=0 go test -count=1 ./pkg/cmd/tunnel/node -run '^TestOpenEmptyNodeV1Database$'` 通过，覆盖 Ent 查询、非空目录拒绝及旧 `node.sqlite` 文件不变。风险：身份和运行行的原子业务初始化留给 G3；下一步完成结构复核与 G0 收尾门禁。
- 2026-09-26: slice G0-6 (Server 路由约束修正): 修改 `pkg/cmd/tunnel/server/migrations/001_v1.sql`、`ent/server/schema/schema.go`、`ent/schema_test.go`，将路由小写 CHECK 显式改为 `COLLATE BINARY` 并加入大写 hostname 拒绝测试；同时增强 SQL/Ent 列类型与非空核对、两端 Ent 实际写入测试。`go generate ./ent` 和定向 `CGO_ENABLED=0 go test -count=1 ./ent ./pkg/cmd/tunnel/server ./pkg/cmd/tunnel/node -run 'Test(ServerV1SQLMatchesEnt|NodeV1SQLMatchesEnt|OpenEmptyServerV1Database|OpenEmptyNodeV1Database)$'` 通过。风险：旧业务入口尚未切换新 v1，属后续 Gate；下一步按计划顺序运行 G0 Repository verification。
- 2026-09-26: slice G0-7 (架构清单同步): 修改 `internal/architecture/architecture_test.go`，显式登记 G0 新增的 Ent 源包与生成包；首次架构测试仅因固定 package inventory 缺项失败，更新后 `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/architecture` 通过，未跳过或弱化检查。风险：未来模型增加包时须继续维护显式清单；下一步完成最终结构复核和门禁。
- 2026-09-26: slice G0-8 (路由 collation 同步): 修改 `ent/server/schema/schema.go`、`ent/schema_test.go`，将 `hostname` 的 `NOCASE` collation 同步到 Ent 字段，并检查 SQLite 唯一索引实际 collation；`go generate ./ent`、两端结构/空库定向测试、`git diff --exit-code -- ent`、`GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go test ./internal/architecture` 与同环境 `go vet ./...` 均通过。`CGO_ENABLED=0 go test -count=1 ./pkg/cmd/tunnel/server/... ./pkg/cmd/tunnel/node/...` 通过（Server 99.556s，Node 64.631s）。风险：G1/G3 仍需分别迁移当前业务状态入口；下一步提交 G0 资产并核对 Exit conditions。
- 2026-09-26: G0 Exit 逐项验收：① Server/Node `migrations/001_v1.sql`、`ent/server/schema`、`ent/node/schema` 与生成代码已在 `b133d73` 提交；`TestServerV1SQLMatchesEnt` / `TestNodeV1SQLMatchesEnt` 在 ncruces 临时空库中核对表、列类型/非空、索引、外键、CHECK 与关键约束拒绝场景，通过。② `GOTOOLCHAIN=go1.26.7 GOWORK=off CGO_ENABLED=0 go generate ./ent` 通过，再生后 `git diff --exit-code -- ent` 无差异。③ 同环境定向 `go test -count=1 ./ent ./pkg/cmd/tunnel/server ./pkg/cmd/tunnel/node -run 'Test(ServerV1SQLMatchesEnt|NodeV1SQLMatchesEnt|OpenEmptyServerV1Database|OpenEmptyNodeV1Database)$'`、两端包完整 `go test -count=1 ./pkg/cmd/tunnel/server/... ./pkg/cmd/tunnel/node/...`、`go test ./internal/architecture`、`go vet ./...` 全通过，覆盖 CGO=0 的 Ent/ncruces 编译与写入。④ 新 v1 空库入口只访问固定 `server-state-v1` / `node-state-v1` 空子目录并执行嵌入 SQL；定向测试确认旧 `go-v1` / `node.sqlite` 文件不变，新增生产初始化辅助代码未调用 Ent Auto Migration、旧数据库读取或 migration runner。G0 判定 `passed`；G1 已激活，尚未开始实施，本次 Goal 到此结束。

### G1: 完成 Server 持久化层

- 2026-09-26: initialized as `planned`; no implementation evidence.

### G2: 通过 Server 外部行为门禁

- 2026-09-26: initialized as `planned`; no implementation evidence.

### G3: 完成 Node 持久化与管理状态

- 2026-09-26: initialized as `planned`; no implementation evidence.

### G4: 通过 Node 恢复与最终交付门禁

- 2026-09-26: initialized as `planned`; no implementation evidence.
