# 决定 ORM 查询与事务边界

Type: grilling
Status: resolved
Parent: ../map.md
Blocked by: 04, 05, 06

## Question

Server 和 Node 的哪些日常读写优先由 Ent API 表达，哪些复杂查询和 SQLite 特殊操作允许集中使用原生 SQL？决定专用连接事务、提交后事件发送、错误映射和 JSON/可空字段转换的统一边界。

## Answer

- Server 和 Node 的普通 CRUD、关系查询、计数及业务检查优先使用生成的 Ent API 或查询构造器。复杂查询、SQLite 特殊能力、只读 schema/完整性检查、PRAGMA 和事务内原生查询可在所属数据库访问层使用 `sql/execquery` 或底层连接；事务内查询必须绑定该事务的连接。handler 和 service 不直接持有原生查询逻辑。建表、改字段、补索引及约束变更只通过版本化 `migrations/*.sql`，不由 Ent Auto Migration 或零散运行时 SQL 完成。
- 持久化状态各自持有一个 `*sql.DB` 连接池和基于它创建的主 Ent client，统一关闭连接池一次。普通多步写入使用 Ent `Tx`，单步写入使用 Ent mutation。Server 中需要串行化资源检查及写入的操作，按“重设计 Server 的持久化约束”使用专用 `*sql.Conn`：外层执行 `BEGIN IMMEDIATE`，以 `entgo.io/ent/dialect/sql.NewDriver` 包装该连接创建仅在回调期间使用的 Ent client，在同一连接内完成检查、写入和提交前校验。此 client 不调用 `Close`，也不在已经开始的 immediate 事务里再调用 `client.Tx`；外层负责提交、失败回滚和连接关闭。Node 使用普通 Ent 事务，无需专用 immediate 路径。
- 任何检查、mutation 或提交失败都使该笔操作失败；事务失败时回滚，只有成功提交后才发送事件或暴露成功结果。需跨事务步骤更新的修订号、Client/Tunnel/路由及 Node 检查点一同提交或回滚；事务中的 Ent 实体和短生命周期 client 不得在回调后继续使用。提交失败也不得发送事件。
- 在持久化边界集中把 Ent/SQLite 错误映射到既有领域错误码。优先用 `errors.Is` 识别 `github.com/ncruces/go-sqlite3` 的扩展约束码（包括 `CONSTRAINT_UNIQUE`、`CONSTRAINT_PRIMARYKEY`、`CONSTRAINT_FOREIGNKEY`、`CONSTRAINT_CHECK`），再结合当前操作、已经执行的业务检查及必要的定向冲突重查确定 `RESOURCE_RESERVED`、`NODE_RESOURCE_CONFLICT`、`NODE_ALREADY_CLAIMED` 等既有语义；不能只凭一个约束类别猜测业务原因。未识别或不能归因的约束作为内部错误返回，不匹配错误文本或索引名。Ent 的 `ConstraintError` 可 unwrap 底层驱动错误，因此映射应检查错误链中的类型化码。
- Ent 实体只在 Server/Node 持久化边界内流转，向上转换为现有领域类型。可空字段显式转换为领域指针，结构化 JSON 由模型字段表达并在边界转换；参与摘要或协议重放的 Node 快照保持原始字节，绝不经 ORM 的 JSON 编解码重排。HTTP PATCH 输入适配继续区分字段缺省、显式 `null` 和赋值，不能让 Ent 的可选字段写入语义吞掉这三态。
