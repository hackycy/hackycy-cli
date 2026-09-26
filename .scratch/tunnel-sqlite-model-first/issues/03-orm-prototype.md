# 验证 Ent ORM 的模型与事务

Type: prototype
Status: resolved
Parent: ../map.md

## Question

用隔离原型验证 Ent 生成客户端能否接入现有 SQLite 驱动、处理代表性约束错误、执行 Server 专用连接事务与 Node 状态写入，并检查六平台纯 Go 构建。原型中的 Ent 建库仅是测试夹具，不作为生产 Schema 来源。

## Prototype findings

[Ent + ncruces SQLite 原型](../prototype/ent-sqlite/README.md) 已运行通过：

- 原型用 Ent 建立了包含 `CHECK`、部分唯一索引及外键的测试数据库；Node 状态写入成功，无效端口、负修订号、活动端口重复、未知 owner 均被 SQLite 拒绝。它只验证运行能力，不验证生产 `migrations/*.sql` 与 Ent Schema 的一致性。
- 原有 `github.com/ncruces/go-sqlite3/driver` 可以通过 `entgo.io/ent/dialect/sql.OpenDB` 接入，未引入 CGO；生成代码重新生成前后校验和一致。
- 数据库使用 `_txlock=immediate` 时，普通 Ent `Client.Tx` 可完成事务写入。
- 现有选择性 `BEGIN IMMEDIATE` 也可保留：先从 `database/sql.DB` 取得专用 `*sql.Conn`，执行 `BEGIN IMMEDIATE`，再用 `entgo.io/ent/dialect/sql.NewDriver` 包装该连接创建短生命周期 Ent client，生成 mutation 在该连接内执行后由外层提交/回滚。该 client 不拥有连接，不能负责关闭它。
- `CGO_ENABLED=0` 的 darwin/linux/windows amd64/arm64 六个目标均可编译。

原型没有证明完整 Server/Node schema，也没有证明并发压力下的事务性能；这些留给后续票据。

## Answer

接受 Ent 作为模型和查询层，并采用分层事务边界：普通事务使用生成的 Ent client；需要保持现有选择性 `BEGIN IMMEDIATE` 和专用连接语义的操作，使用 `entgo.io/ent/dialect/sql.NewDriver` 包装已取得的 `*sql.Conn` 创建短生命周期 Ent client，由外层负责提交、回滚和关闭连接。该 client 不拥有连接，不负责关闭它。生产结构不得由 Ent Auto Migration 生成；SQL schema/migration 资产仍按维护约束管理，但本次 v1 不实现 migration runner。

原型已证明代表性约束执行、最小 Node 状态写入、现有纯 Go SQLite 驱动接入、代码再生一致性及六平台 `CGO_ENABLED=0` 编译均成立。完整生产 schema、SQL 结构资产、并发行为和生产适配器 API 均未由此原型验证。
