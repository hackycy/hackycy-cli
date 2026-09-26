# Tunnel SQLite 与 Ent

## 当前 v1 状态

Tunnel Server 与 Node 分别在 `pkg/cmd/tunnel/server/` 和 `pkg/cmd/tunnel/node/` 拥有 SQLite 状态。各自当前空状态的结构文件为 `migrations/001_v1.sql`；运行状态位于 `<data-dir>/server-state-v1/` 与 `<data-dir>/node-state-v1/`。代码嵌入 v1 SQL，初始化空状态并校验已有状态；启动时尚无升级用的 migration runner。旧 `go-v1` 和 `node.sqlite` 状态不在自动升级范围。核对当前事实时，阅读两个所有者的 `database_v1.go`、状态代码及测试，并查看 `ent/server/schema/`、`ent/node/schema/` 和 `ent/schema_test.go`。

两个所有者各自的 `migrations/*.sql` 是真实 SQLite Schema 及其演进的唯一事实来源；Ent Schema 提供 ORM 模型、关系、类型安全查询和生成代码。生产启动或升级不能用 Ent Auto Migration 建表、补列或修改约束。`ent/schema_test.go` 对照 v1 SQL 的结构和约束与生成的 Ent 元数据。`ent/generate.go` 提供 `go generate ./ent`；结构变化时提交受影响的生成代码，不能只修改 SQL 或只修改 Ent Schema。

普通 CRUD 和关系查询优先使用 Ent。复杂查询、SQLite 特有操作或事务内原生 SQL 可以在所属 Server/Node 的 repository 或数据库访问层使用 `sql/execquery` 或 `*sql.DB`。持久化逻辑不泄漏到 HTTP handler 或 service；原生 SQL 也不能绕过 migration 对结构变更的所有权。如果实体采用软删除，默认查询要过滤 `deleted_at`；Ent 的 `HasXxxWith` 等关系子查询不一定继承软删除拦截逻辑，需要在对应子查询显式加入 `DeletedAtIsNil()`，并用查询测试覆盖。

保持 Server 选择性使用 `BEGIN IMMEDIATE`、领域错误映射以及事务成功提交后才发送事件的行为。保持 Node 原始快照字节语义。修改事务或快照行为前，先核对当前实现与测试。

## 升级已部署状态

任务涉及已部署 v1 状态的结构变化时，需要同时设计 migration 生命周期。每次修改表、字段、索引或约束，都新增按版本排序的 `migrations/*.sql` 文件，修改对应 Ent Schema，运行 `go generate ./ent`，提交生成代码并测试 SQL/Ent 与实际数据库结构的一致性。已在部署数据库执行过的 migration 不得修改；通过后续 migration 修复。

目标生命周期是只能前进的迁移（forward-only）：每个数据库用 `schema_migrations` 记录已执行版本；启动时按序检查并执行待应用文件，重复部署跳过已完成版本，只有迁移成功才记录版本，失败不能将未完成的迁移标记为成功。仅回滚程序版本不能恢复已改变的数据库。破坏性修改、数据清理或字段删除前，要设计备份、兼容窗口和恢复方案，并在实施验收中验证。SQLite 无法直接完成的复杂 `ALTER TABLE`、字段或约束修改，使用建新表、迁移数据、删旧表、重命名新表的流程，并保证整个过程的数据完整性和外键关系正确。

目前尚未实现 runner 和 `schema_migrations`。实施首次升级前，需确定 Server/Node 各自的 migration 目录、版本命名、`schema_migrations` 结构、已执行文件漂移检测、Ent 生成代码的正式包布局；还需确定启动持锁、执行与记录版本的方式，迁移中途失败、重建表和外键检查的原子性，以及如何区分可升级、损坏和未知版本数据库。验收方案还须覆盖迁移后结构与 Ent 的自动核对、备份、兼容窗口、恢复和软删除查询规则。这些细节仍待设计，不应写成当前行为，也不应在无关 v1 修改中擅自定案。
