# SQLite 结构与 Ent 维护约束

状态：已确认的未来维护约束；本次 v1 破坏性重构暂不实现 migration runner。真正需要升级已部署数据库时，再依据本文件建立 migration 生命周期设计。

## 必须遵守的边界

- `migrations/*.sql` 是真实 SQLite Schema 及其演进的唯一事实来源。Ent Schema 负责代码中的 ORM 模型、关系、类型安全查询和代码生成。生产启动或升级不得调用 Ent Auto Migration 建表、补列或修改约束。
- 每次表、字段、索引或约束变化都新增一个按版本排序的 `migrations/*.sql` 文件，同时修改 `ent/schema`，执行 `go generate ./ent`，提交生成代码。两边对实际数据库结构必须保持一致；不能仅改 SQL 或仅改 Ent Schema。
- 已执行的 migration 不得修改。`schema_migrations` 记录每个数据库已经应用的版本；修复已上线 migration 时，只能追加后续 migration。应用启动时按版本顺序检查并执行尚未应用的文件，并在成功后记录版本。重复部署应跳过已应用版本；失败时不能将未完成的 migration 标记为成功。
- 数据库演进采用 forward-only。仅回滚程序版本不能恢复已改变的数据库结构。破坏性修改、数据清理和字段删除前，必须设计备份、兼容窗口及恢复方案，并在实施验收中验证。
- SQLite 无法直接完成的复杂 `ALTER TABLE`、字段或约束修改，使用建新表、迁移数据、删旧表、重命名新表的重建流程，并保证整个过程的数据完整性和外键关系正确。
- 普通 CRUD 和关系查询优先走 Ent。复杂查询、SQLite 特殊能力或事务内原生查询可使用 `sql/execquery` 或底层 `*sql.DB`；访问逻辑集中在所属 Server/Node 的 repository 或数据库访问层，不泄漏到 handler 或 service。原生 SQL 不能绕过 migration 对结构变更的所有权。
- 若实体采用软删除，默认查询须过滤 `deleted_at`。Ent 的 `HasXxxWith` 等关系子查询不一定继承软删除拦截逻辑，需要在对应子查询显式加入 `DeletedAtIsNil()`，并用查询测试覆盖。

## 当前 v1 与未来演进

- 本次新 `<data-dir>/server-state-v1/` 和 `<data-dir>/node-state-v1/` 只建立当前 v1 空状态，不实现启动时 migration runner；旧 `go-v1`/`node.sqlite` 不在自动升级范围，仍按既定状态隔离规则处理。
- 未来在已部署的新状态上演进 schema 时，才启用本文件中的 forward-only、`schema_migrations`、历史文件漂移检查、备份、兼容窗口和恢复规则。
- Server 的选择性 `BEGIN IMMEDIATE`、提交后事件和领域错误映射，以及 Node 原始快照字节语义继续适用。

## 未来需要定下的实施细节

- Server 与 Node 各自的 migration 目录、版本命名、`schema_migrations` 结构、已执行文件的漂移检测，以及 `go generate ./ent` 对应的正式包布局。
- 启动时如何持锁、执行迁移和记录版本；迁移中途失败、SQLite 重建表及外键检查如何原子处理；如何区分可升级数据库与损坏或未知版本状态。
- 如何自动核对 migration 的最终数据库结构与 Ent Schema/生成代码，及如何验收备份、兼容窗口、恢复和软删除查询规则。
