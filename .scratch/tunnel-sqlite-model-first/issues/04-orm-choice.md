# 选择 Ent ORM 与生成方式

Type: grilling
Status: resolved
Parent: ../map.md
Blocked by: 03

## Question

结合原型，为 Server/Node 选定 Ent ORM、SQLite 驱动接入方式、SQL 结构资产与 Ent Schema 的职责，以及生成代码的版本固定与入库策略。

## Answer

- Server 和 Node 均采用 Ent。以 `entgo.io/ent v0.14.5` 为实施基线；现有 `github.com/ncruces/go-sqlite3` 继续作为 SQLite 驱动，通过 `entgo.io/ent/dialect/sql.OpenDB` 接入。不引入第二种 SQLite 驱动。升级 Ent 时重新检查生成代码、数据库约束和六平台 `CGO_ENABLED=0` 构建。
- `migrations/*.sql` 是真实数据库表、列、索引、`CHECK` 和外键及其演进的唯一事实来源。Ent Schema 同步表达 ORM 模型、关系与类型安全生成所需的结构；生产不调用 Ent Auto Migration。事务步骤、跨记录业务规则、错误处理和提交后事件属于 Go 服务逻辑。具体维护约束见[SQLite 结构与 Ent 维护约束](../sqlite-maintenance-constraints.md)。
- 常规连接池保持默认 deferred 事务；普通读写与事务使用生成的 Ent API。确需选择性 `BEGIN IMMEDIATE` 的操作使用已验证的专用 `*sql.Conn` 适配方式，由外层管理提交、回滚及连接生命周期。原型中的全局 `_txlock=immediate` 仅用于验证驱动能力，不作为生产默认设置。
- 根模块 `go.mod` 固定 Ent 依赖及 `entgo.io/ent/cmd/ent` 生成器工具版本，`go.sum` 随之入库。SQL 结构资产、Ent Schema 和生成的 Ent Go 客户端必须同步；修改结构时执行 `go generate ./ent`。将生成代码再生无差异及结构一致性检查接入 `make check`；本次 v1 不实现后续 migration runner。构建和发布仍检查六个平台的纯 Go 目标。
- Ent 已通过现有驱动接入、Node 状态写入、专用连接及六平台纯 Go 编译的原型验证。
