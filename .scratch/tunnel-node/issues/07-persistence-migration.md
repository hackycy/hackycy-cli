# Node 与 Client 归属如何落库并升级现有部署？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: grilling
Status: resolved
Blocked by: 02, 05, 06

## Question

在 Node 生命周期、Client 切换及资源归属明确后，决定主 Server 数据库需要哪些 Node 身份、管理/FRP 地址、配置 revision、状态与 Client `node_id` 字段；哪些运行观察不应持久化。现有 schema v2 无 Nodes，所有 Client 默认使用本地 FRPS；应如何迁移并保证升级后原连接、端口与路由行为可用？还需决定删除 Node 时有 Client/Tunnel 依赖的处理、回滚与备份边界，以及新旧二进制打开同一数据库的兼容行为。

参照：[数据库迁移入口](../../../pkg/cmd/tunnel/server/database.go)与[Client 模型](../../../pkg/cmd/tunnel/server/server_clients.go)。

## Comments

### 2026-09-23：已认领，第一轮待用户决定

代码核对：[`database.go`](../../../pkg/cmd/tunnel/server/database.go)当前为 schema v2，启用 SQLite WAL/外键，使用单事务完成建库或 v1→v2 迁移；未知版本直接拒绝，迁移失败回滚。[`state_test.go`](../../../pkg/cmd/tunnel/server/state_test.go)已有数据保留与失败回滚测试。数据库位于 Server 持久目录内，启动时先取得状态目录锁；当前没有内建数据库备份功能。Local FRP Token 若由环境或启动配置提供，不会落入数据库；只有自动生成的 Token 存入 `meta`，因此迁移不能假定旧库包含完整 Local Node 运行配置。

现有所有 Client 都由本地 FRPS 服务，旧 schema 无 `node_id`。端口与 HTTP 路由全局唯一；将所有旧 Client 归给新 Local Node 后不会产生新冲突。新规则下的按 Node 端口唯一、按 hostname 单 Node 归属需要新的资源约束，现有索引无法直接表达。

第一轮只问两个面向运维的边界：是否自动迁移并自动备份；旧二进制遇到新数据库是拒绝还是兼容读取。旧 Client 自动归 Local Node、Token 与 Tunnel 保持可用是本地图已有约束，不重复询问。

待决议的实现事实：v2 的 `tunnel_http_routes` 主键是全局 `(hostname, location)`，可让同一域名的不同路径分属不同 Client；升级时所有旧 Client 均归 Local Node，因此不会拆域名。v3 的按 Node 端口唯一不能只靠当前 `tunnels` 索引表达（Node 归属在 `clients` 上），须在同一事务中维护 Node 范围的资源预留；HTTP hostname 还需单独保证整体只归一个 Node。Server 的在线/离线与 FRPS 运行观察不能从上次进程内状态直接当成重启后的实时状态。Controller 身份私钥应独立于业务数据库持久保存，否则与[绑定生命周期决议](./02-binding-lifecycle.md)允许的“仅数据库丢失后以原 Controller 身份重新添加 Node”相矛盾。

候选持久模型供决议时核对：`nodes` 保留稳定身份、类型、显示名、远端公钥/指纹、管理地址、对外 FRP 地址、HTTP 公网入口、FRPS 期望配置/版本、Remote Node Token 与待移除意图；`clients` 增加非空当前 `node_id` 及可空待切换目标；每个 Tunnel 的端口/HTTP 路由预留能在事务内按 Node 检查。实时在线、FRPS PID 和当前健康度仅由运行时观察，历史最后上报可带时间戳缓存，但不是重启后的实时事实。Local Node 的运行设置与可选 FRP Token 仍以现有启动配置为准，数据库不应凭空生成第二份与其冲突的设置权威。

### 2026-09-23：用户确认

采用自动迁移并在修改旧库前自动保留一致性备份；升级后的数据库由旧版程序明确拒绝打开。需要回退旧版时，先停止服务并恢复升级前备份，不提供旧版直接读写新库的兼容层。

### 2026-09-23：后续破坏性升级决议覆盖迁移方案

用户在[“接入顺序和跨版本验收边界如何确定？”](./10-rollout-compatibility.md)明确取消旧数据库自动备份与迁移，并接受重新建立账号、Client、Token 和 Tunnel。此前“旧库自动备份迁移”的回答仅保留为讨论历史，下方 Answer 已按最终决议修订。新版检测到旧 schema 必须拒绝启动且不修改旧库；用户切换到空数据目录作为全新部署。此票据的 Node/Client 目标持久模型和事务性资源约束仍有效。

## Answer

### 持久模型与权威边界

- 主 Server 数据库新增正式的 `nodes` 记录，包含稳定 `node_id`、`local/remote` 类型、显示名、创建/更新时间。Local Node 使用唯一且跨启动稳定的保留身份，始终存在，不能删除；Remote Node 保存其**长期公钥**（指纹由公钥导出供展示）、管理地址、对外 FRP 地址、HTTP 公网入口、FRPS bind/HTTP vhost/端口池等期望设置、该 Node 独立的 FRP Token、Node 期望配置和单调 `revision`、正常移除的待处理/持久停用意图。公钥不能仅以 IP:端口替代；同一远端身份不能登记为两个活动 Node，管理地址冲突也应明确报错而非静默替换身份。
- Local Node 的 FRPS 子进程、监听端口、端口池和可选 `YCY_TUNNEL_FRP_TOKEN` **仍以 Server 启动配置为权威**。Local Node 记录保存稳定业务身份及必要元数据，不再造一份会与环境变量冲突的静态配置。新库若无启动配置 Token，按现有生成规则在新 `meta` 保存本地 Token；启动配置提供的 Token 不入库，不能假定旧库中的 Token 会自动转入新部署。
- Controller 长期私钥保存在 Server 私有持久目录的独立受保护文件中，**不只保存在业务 SQLite**。这满足[“Node 绑定、移除和恢复的状态语义是什么？”](./02-binding-lifecycle.md)中“只丢失数据库而保留 Controller 身份时可显式重新添加旧 Node”的规则；私钥真正丢失仍无恢复/改绑。Node 自己的私钥、绑定和 FRPS 已应用缓存只在 Node 的持久目录，不复制进主 Server 数据库。
- `clients` 增加非空当前 `node_id` 外键；离线时选择的目标保存在可空的 `pending_node_id`（及必要的创建时间），执行前不改当前归属。现有 `desired_revision` / `last_applied_revision` 继续是 Client 级版本，但最后上报的 Node 身份应与版本一起记录，避免旧 Node 的迟到回执被误当成新归属已应用。Client 的在线状态、FRPC 登录状态和进程 PID 不能由这些持久字段推断；重启后先标为待观察，收到新会话/状态报告才更新。
- Server 以数据库中的期望配置、归属、Token 和移除意图为权威；Remote Node 的已应用版本、FRPS 运行、可达性和最近错误来自带观察时间的管理回报。可持久保存最后一次报告供诊断，但必须标注为历史快照，不能在 Server 重启后直接显示为“在线”或“正在运行”。Local Node 状态直接从本机 `ManagedFRPS` 读取。

### 资源约束与删除

- 数据库必须可在**同一事务**内表达 `(node_id, protocol, server_port)` 唯一和每个 HTTP hostname 只属于一个 Node、同 Node 内 `(hostname, location)` 唯一。由于当前端口唯一索引位于 `tunnels`、Node 归属将位于 `clients`，单靠跨表查询不能提供并发安全的唯一约束；实施时应使用带 `node_id` 的资源预留字段/表和唯一索引（或等效的事务性约束），在创建、修改、禁用、删除 Tunnel 和 Client 切换时同步维护。禁用仍占用资源；HTTP hostname 的跨 Node 归属不能只靠旧 `(hostname, location)` 主键保证。
- 现有 Tunnel 仍从属于 Client；Client 切换 Node 时，应在事务中核对所有 Tunnel 的端口池与 hostname 约束，一次性迁移其资源预留、改变 `client.node_id` 并递增 Client 期望版本。任一冲突则整笔回滚、旧归属不变。待切换目标执行时重新校验。删除或强制遗忘 Remote Node 前，不得有当前分配 Client，亦不得留下指向它的待切换目标；先取消待切换或改派。外键使用限制删除语义，Local Node 永不可删。正常 Remove 的远端停用确认和强制遗忘风险仍以[生命周期决议](./02-binding-lifecycle.md)为准。

### 破坏性升级、旧状态保留和回退

1. 新版 Server 在取得状态目录锁后检查 schema。**空数据目录**直接建立新 schema 和唯一 Local Node；遇到旧 v1/v2 或任何未知 schema，明确拒绝启动，输出“需要空数据目录/当前数据库不兼容”的操作提示。不能自动迁移、自动覆盖、自动删除、自动备份或静默新建另一份库来掩盖旧状态。用户自行留存原目录后，指定新的空目录启动；账号、会话、Client、Token、Tunnel 与路由都按全新部署重新建立。
2. 新数据库从一开始使用 `clients.node_id NOT NULL` 与 Node 范围资源预留，无旧行回填路径。新建 Client 默认属于 Local Node，Local FRPS 设置仍来自启动配置；没有 Remote Node 时本地 FRPS 能按新版本规则独立运行。旧版 Client 凭据和 Tunnel 定义不自动导入。若需要参考旧数据，用户只读保存旧目录/旧程序输出并在新部署中手工重建，不能把旧 SQLite 文件复制进新目录当作兼容导入。
3. 旧版 Server 遇到新 schema 也必须拒绝启动，不能降级写入。回退旧版仅能停止新版，换回**未被新版改写**的旧数据目录和旧版启动设置；新版期间新增的账号、Client、Node 与 Tunnel 不会自动回到旧版。若用户已手工清理旧目录且未自行备份，就没有旧状态可恢复。后续新版本灾难恢复应备份完整的新 Server 状态目录，尤其独立保存的 Controller 私钥，并另行保管环境配置的 Local FRP Token；数据库单文件不构成完整备份。
4. 新版一旦认领 Remote Node，切回旧二进制或旧数据目录**不会停止这些 Node**；Node 可能继续运行上次成功的 FRPS。要退回没有 Node 功能的旧版，需先在新版中迁走 Client、正常移除并确认停用远端 Node；无法确认时按强制遗忘风险处理。只恢复较早的新版本状态、且 Controller 身份仍在时，也须先读取 Node 高水位 `revision` 再用更高版本收敛，不能盲发旧版本。

### 验收边界

- 验收覆盖空目录建新库并重启幂等、旧 v1/v2/未知 schema 拒绝且字节内容不被修改、新建 Client 默认归 Local Node、禁用 Tunnel 的 Node 范围资源预留、Local Token 来源仍符合新部署的启动设置、旧二进制拒绝新 schema。现有[`state_test.go`](../../../pkg/cmd/tunnel/server/state_test.go)里的旧 v1→v2 测试是当前版本历史事实；新设计不要求保留旧库迁移代码路径。此票据只确定设计，不实施测试或功能。
