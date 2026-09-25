# 主 Server 新库与 Node 资源事务如何具体实现？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: prototype
Status: resolved
Blocked by: 06, 07, 11, 12

## Question

在只接受空数据目录的新 schema 中，给出可审阅的 SQLite 表/索引草案：Local/Remote Node、Controller 身份文件引用、Node 期望版本及配置、独立 FRP Token、Client 当前和待切换归属、Node 级 TCP/UDP 端口预留、HTTP hostname 整体归属与路径唯一性、待移除意图。需说明创建/修改/禁用 Tunnel、Client 切换、Token 轮换、正常 Remove 与 Force Forget 的事务边界和并发冲突结果。

结合现有 `database.go` schema v2、`ServerRuntime` 对单个 `ManagedFRPS` 的持有方式，提出新的 Node 管理/调和层最小接口与启动关闭顺序。旧 v1/v2/未知 schema 必须原样拒绝；不为旧数据设计迁移或兼容表。

## Comments

### 2026-09-23：已认领，事务草案待反馈

已核对现有 [`database.go`](../../../pkg/cmd/tunnel/server/database.go)：当前 schema v2 的 `tunnels_unique_transport_port` 全局唯一，`tunnel_http_routes` 只有 `(hostname,location)` 唯一；当前打开带 `meta` 的 v1 会迁移到 v2，若不存在 `meta` 则建表。新版本必须改为只对不存在的库建新 schema，并在修改前拒绝旧/未知库。现有 [`withImmediateTransaction`](../../../pkg/cmd/tunnel/server/server_clients.go)可作为事务模式参考，但需要把 Node 归属和资源约束一起维护。

[`ServerRuntime`](../../../pkg/cmd/tunnel/server/server_runtime.go)目前仅持有一个 `ManagedFRPS` 并把它传给 Gateway/HTTP Handler；Remote Node 需要独立的调和层，不能让其状态由 Local FRPS 代替。具体关系、索引和操作顺序已整理成[主 Server 新库与 Node 事务评审稿](../server-storage-transactions.md)；[交互式事务草模](../server-storage-flow-prototype.html)可点击观察端口复用、域名整体归属、离线待切换、Token 轮换和移除。此时为讨论材料，尚未成为决议。

待用户确认两处会影响运维行为的边界：Node 设置修改尚未确认应用时，新资源只允许使用旧已确认端口池与新期望端口池的交集；Token 轮换时 Node 已应用新 Token、Server 尚未完成 Client 发布的短暂窗口，重启后应自动完成发布，而非让 Node 回退旧 Token。

### 2026-09-23：用户修正端口池语义并确认 Token 恢复

用户明确：Node 端口池只要在主 Server 保存成功，就是资源分配依据；Node 应用状态应单独检查和展示，不要增加两份端口池取交集的关联复杂度。Token 轮换选择推荐方案：Node 已应用新 Token 而 Server 尚未通知 Client 时，Server 重启后自动完成晋升与下发。已按此修订[主 Server 新库与 Node 资源事务契约](../server-storage-transactions.md)和[交互式事务草模](../server-storage-flow-prototype.html)。下方 Answer 为最终决议，前面的交集提议仅是讨论历史。

## Answer

- 新版主 Server 只建立 schema v3 的空库，Local Node 使用不可删除的稳定身份；旧 v1/v2、未知或损坏的现有库在任何改写前拒绝。Controller 私钥独立保存在私有状态文件，数据库保存其引用与公钥核对值；Local FRPS 设置和显式 Token 仍以 Server 启动配置为准。新库表结构、索引与启动检查以[主 Server 新库与 Node 资源事务契约](../server-storage-transactions.md)为准。
- Remote Node 持久保存身份公钥、管理和对外地址、完整期望快照、单调 Node `revision`、独立 FRP Token 及待移除意图；Client 保存当前与待切换 Node。Tunnel 仍归 Client，物化当前 `node_id` 使 `(node_id, protocol, server_port)` 唯一；HTTP hostname 单独记录整域归属，`(hostname, location)` 唯一。禁用 Tunnel 继续占用资源，所有更新在写事务内维持外键和预留。
- **保存成功的 Node 端口池立即成为 Server 的资源分配依据。** 扩大范围后即使远端 Node 尚未应用，仍可在新范围保存 Tunnel；缩小范围需先拒绝任何包含禁用 Tunnel 的既有占用越界。Node 期望版本、实际已应用版本和 FRPS/代理可用性分别显示；远端暂未应用导致的访问失败不回滚已保存的资源。不使用旧已应用范围与新范围的交集限制。
- 新建/修改/禁用/删除 Tunnel、在线 Client 切换、离线待切换、Node 设置变更、正常 Remove 和 Force Forget 的事务边界按[详细契约](../server-storage-transactions.md)执行。在线切换在目标健康预检后提交归属和全部资源迁移，提交后首次 FRPC 失败不自动回旧 Node；离线 Client 只存待切换，重连时重新检查。正常 Remove 持久保存停用目标并等待 Node 确认，Force Forget 只删 Server 记录，两者都要求无当前或待切换 Client。
- Remote Node Token 轮换分两笔事务：先保存 staged Token 并下发新 Node 快照；Node 确认持久应用后，再晋升 active Token、递增此时归属该 Node 的所有 Client 配置版本并下发。若 Server 在两阶段之间重启，查询 Node 应用结果后自动完成晋升与下发，不让已切换的 Node 自动退回旧 Token。轮换期间仅串行化同一 Node 的期望快照更新，不冻结普通 Client 分配。
- `ServerRuntime` 在现有 Local `ManagedFRPS` 之外加入按 Node 选择的协调/调和层；数据库事务不跨网络，提交后通知可由重启扫描和周期调和补偿。Remote Node 不调用 Local 专用 `ManagedFRPS`；主 Server 关闭时不向 Remote 发送停用，Node 沿用最后成功配置。最小接口和启动关闭顺序以[详细契约](../server-storage-transactions.md)为准。
