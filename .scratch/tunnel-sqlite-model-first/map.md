# Tunnel SQLite 与 Ent 重构决策地图

Label: wayfinder:map

## Destination

形成可交付实施的 Tunnel Server 与 Node SQLite 重构设计：选定 Ent ORM 与 SQL 结构资产边界，明确全新 v1 schema、事务与数据约束、旧状态隔离、生成流程和验收方式。地图完成时这些设计决策均已解决；代码实施另行开展。

## Notes

- [已确认边界与代码事实](baseline.md)记录当前范围与旧代码事实；继续决策时先阅读它、仓库根目录的 `ENGINEERING.md` 和 `CLAUDE.md`。
- [SQLite 结构与 Ent 维护约束](sqlite-maintenance-constraints.md)是本地图的实施边界：SQL 资产管真实结构，Ent 管代码模型，两边同步；未来演进才启用 migration runner。
- 本地图只处理设计决策，不在票据中执行生产代码重构。按 `wayfinder`、`grilling` 和 `domain-modeling` 流程工作。
- 票据位于 `issues/`。`Status: open` 且所有 `Blocked by` 票据均已 `resolved` 的未认领票据构成当前 frontier；按文件编号选择。
- 开始处理票据前先将其设为 `Status: claimed`。每次决策把答案写入该票据，再更新此处的索引；一次会话最多解决一个非研究票据。

## Decisions so far

<!-- 地图建立后关闭的决策票据在此按名称链接；此前约定见 baseline.md。 -->

- [决定新旧 Tunnel 状态如何隔离](issues/02-state-isolation.md)：`--data-dir` 下分别使用固定的新 Server/Node 子目录，完整隔离数据库及关联状态；新状态不完整时拒绝启动。
- [验证 Ent ORM 的模型与事务](issues/03-orm-prototype.md)：原型验证 Ent 与现有驱动、专用 `*sql.Conn` 事务及六平台纯 Go 编译；其 Ent 建库仅是测试夹具。
- [选择 Ent ORM 与生成方式](issues/04-orm-choice.md)：Server/Node 使用 Ent `v0.14.5` 与现有纯 Go SQLite 驱动；SQL 结构资产管真实结构，Ent Schema 管模型与生成代码。
- [重设计 Server 的持久化约束](issues/05-server-invariants.md)：SQL 结构资产定义基础约束；本地 Node、Hostname 归属及 Client 换 Node 的跨记录规则由串行化服务事务与启动检查承担。
- [决定 Node 身份与运行状态的模型](issues/06-node-state-model.md)：三张 Ent 单行模型保存身份、可选绑定与运行检查点；新状态严格校验，原始快照与已验证进程所有权支撑崩溃恢复。
- [决定 ORM 查询与事务边界](issues/07-data-access-boundary.md)：普通 CRUD 优先 Ent，复杂原生查询集中在数据库访问层；Server 资源写入使用专用 immediate 事务。
- [定义空库 v1 与后续 schema 生命周期](issues/08-schema-lifecycle.md)：本次只初始化全新的破坏性 v1，不实现 migration runner；`migrations/*.sql`、forward-only 和 `schema_migrations` 作为未来数据库演进约束保留。
- [确定实施顺序与验收门槛](issues/09-rollout-acceptance.md)：Server 先于 Node；各阶段必须通过 v1 结构、Ent 生成、持久化不变量、协议回归、架构检查和六平台纯 Go 构建门禁。

## Not yet specified

- 无（当前 v1 设计没有未决的实施前置决策）。

## Out of scope

- 本地图内实施生产重构、提交发布或部署新版本；路线确定后再进入实施。
- 迁移旧 Server/Node SQLite 数据、兼容旧 schema、为旧数据库提供自动升级，或在本轮实现新状态目录内的后续 schema migration。
- 未来 migration runner、`schema_migrations`、备份恢复、兼容窗口和已部署数据库升级设计；需要时另起维护演进工作。
- 旧 Node 运行进程的接管、清理或部署切换；Node 尚未正式上线。
- 改变 CLI、Web API、Client/Node 协议的外部行为，或重构与 Tunnel 状态无关的持久化模块。
