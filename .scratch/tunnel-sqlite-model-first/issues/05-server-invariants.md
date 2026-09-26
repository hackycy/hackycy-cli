# 重设计 Server 的持久化约束

Type: grilling
Status: resolved
Parent: ../map.md
Blocked by: 04

## Question

如何用 SQL 结构资产、同步的 Ent 模型和明确的事务操作，保留当前 Server 对本地 Node 不可删除/变更、Hostname Owner 自动释放、Client 换 Node 时资源归属一致性，以及端口和路由唯一性的外部行为？决定哪些约束留在数据库结构中，哪些由服务层原子操作承担，并定义失败与并发场景。

## Answer

- Server 的所有持久化表均由 SQL 结构资产创建，包括目前在构造函数中另行创建的 `node_management_candidates` 和 `node_observations`；Ent Schema 同步其模型。SQL 定义主键、普通外键与删除规则、字段取值及跨列 `CHECK`、唯一索引；新结构不保留旧触发器或延迟复合外键。跨记录的业务约束由服务事务承担。
- SQL 结构资产对 `nodes` 定义约束：`node_id = 'local'` 当且仅当 `kind = 'local'`，本地 Node 的 `lifecycle = 'active'`；Ent Schema 同步该模型。本地 Node 行在空库初始化时创建；服务入口拒绝删除及身份变更，启动时验证它存在且状态正确。启动设置仍可更新本地 Node 的对外端点和端口池，调整端口池前须检查已占用端口。远端 Node 的注册和删除继续受现有生命周期与依赖检查约束。
- 删除 `hostname_owners` 表及其自动释放触发器。Hostname 的归属由 `tunnel_http_routes -> tunnels -> node_id` 推导：一个 hostname 可在同一 Node 上有多个不同 location，不能分布于多个 Node；最后一条路由删除后自然不再有归属。SQL 结构资产定义全局 `(hostname, location)` 唯一索引，并让路由通过普通外键依附 Tunnel、随 Tunnel 删除。写入时继续规范化为小写 IDNA hostname；数据库 `CHECK` 约束存储值为小写，服务在资源事务内检查同 hostname 的其他路由是否属于不同 Node。路由更新时检查最终路由集合，而非把被替换的旧路由视为竞争者。
- 保留 `tunnels.node_id`，由 SQL 结构资产定义 `(node_id, protocol, server_port)` 的传输端口唯一索引；TCP 与 UDP、不同 Node 可复用同一数字，`enabled = false` 仍占用端口。HTTP Tunnel 的 `server_port` 为空，SQL 定义 protocol/端口/域名组合的 `CHECK`。删除 `tunnel_http_routes.node_id`，其 Node 始终从 Tunnel 读取。Tunnel 对 Client 和 Node 使用普通外键；`tunnels.node_id = clients.node_id` 由服务维护，取代延迟复合外键。
- Tunnel 创建、更新、删除、批量导入，Client 在线换 Node，以及本地/远端端口池调整均在选择性 `BEGIN IMMEDIATE` 事务内完成资源检查和写入。换 Node 前读取目标端口池，检查每个传输端口、目标端口唯一性及所迁移 hostname 的其他路由；随后在同一事务内更新 Client 和所属 Tunnels，并在提交前检查该 Client 的全部 Tunnel `node_id` 与 Client 一致。离线待迁移状态只改 `pending_node_id`，不提前移动资源。端口池缩小时同样检查已有 Tunnel，包括禁用的 Tunnel。
- `BEGIN IMMEDIATE` 串行化竞争写入；SQL 结构资产定义的唯一索引和外键作为最后一道约束。任一检查、写入或提交失败均回滚 Client、Tunnel、路由、修订号等整笔变更，不发送提交后事件。保留外部错误语义：Tunnel 资源重复为 `RESOURCE_RESERVED`，端口池不足或越界为现有对应错误，Node 迁移及远端端口池冲突为 `NODE_RESOURCE_CONFLICT`，本地端口池配置冲突维持启动错误，本地 Node 删除为 `NODE_REMOVE_FORBIDDEN`。具体 Ent/SQLite 错误识别集中于“决定 ORM 查询与事务边界”票据。
- 重新打开数据库时，除 schema/身份/SQLite 完整性检查外，还验证服务承担的持久化不变量：本地 Node 存在且有效、Tunnel 与 Client 的 Node 一致、每个 hostname 只落在一个 Node、已有传输端口位于所属 Node 的端口池。发现不一致就拒绝启动，不静默修复。验收用并发创建不同 location 的同 hostname、同端口竞争、跨 Node 复用、禁用 Tunnel 占用、换 Node 冲突回滚和最后一条路由删除后重用覆盖这些规则；旧测试中直接写表构造状态的部分须改用新模型或受控测试夹具。
