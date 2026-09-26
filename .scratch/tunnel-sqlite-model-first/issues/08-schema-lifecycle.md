# 定义空库 v1 与后续 schema 生命周期

Type: grilling
Status: resolved
Parent: ../map.md
Blocked by: 02, 04

## Question

按[SQLite 结构与 Ent 维护约束](../sqlite-maintenance-constraints.md)，当前重构是否需要实现 migration runner，还是只记录未来维护约束？决定全新 v1 的空状态初始化、版本不匹配处理、Ent Schema 同步及旧状态隔离范围，避免把未来的 migration 生命周期提前引入本次破坏性重构。

## Answer

- 本次 Server/Node 重构是全新的、破坏性的 v1，不读取、不迁移、不升级旧 `go-v1`/`node.sqlite` 数据库。新 `<data-dir>/server-state-v1/` 和 `<data-dir>/node-state-v1/` 只负责创建当前 v1 空状态；旧状态仍按隔离规则原样留存。
- 本次不实现 migration runner、`schema_migrations`、启动时自动执行后续 migration、破坏性升级备份、兼容窗口或数据库恢复流程。此前 Q31–Q42 讨论的 runner 目录、版本记录、漂移检查、重建表升级和备份策略属于未来真正需要升级已部署数据库时再开票决策的维护约束，不是本次生产实施门槛。
- 本次仍遵守 SQL 结构来源约束：数据库结构由提交到仓库的 SQL schema/migration 资产定义，Ent Schema 必须同步，执行 `go generate ./ent` 并提交生成代码；生产路径不得调用 Ent Auto Migration。具体初始建库调用方式由 Server/Node 实施阶段落地，但不能另造一份与 SQL 结构脱节的 DDL 来源。
- 新状态不存在或为空时才允许初始化当前 v1。已有新状态缺少必需文件、结构不匹配、版本未知或无法通过完整性检查时直接拒绝启动，不尝试自动升级、修复、重置身份或接管旧数据库；明确重置由操作者移走整个新状态目录后重新初始化。
- 未来一旦需要在已部署的新状态上演进 schema，再依据[SQLite 结构与 Ent 维护约束](../sqlite-maintenance-constraints.md)新增 migration 生命周期票据，采用 forward-only、历史文件不可修改、`schema_migrations` 记录和备份/恢复方案；本票据不预先锁定那些实现细节。
