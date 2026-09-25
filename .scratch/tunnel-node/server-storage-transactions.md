# 主 Server 新库与 Node 资源事务契约

状态：已按[“主 Server 新库与 Node 资源事务如何具体实现？”](./issues/13-server-node-storage.md)决议。本文只设计新空库；已有产品边界以[资源归属](./issues/06-node-resource-scope.md)、[落库与破坏性升级](./issues/07-persistence-migration.md)、[Node 崩溃恢复](./issues/12-node-state-crash-consistency.md)为准。[交互式事务草模](./server-storage-flow-prototype.html)用于观察几条并发与故障路径，不是生产实现。

## 新库的权威与启动检查

- 新 schema 版本为 `3`，只在**数据库文件不存在且目录无旧库痕迹**时建立；独立 Controller 身份文件可以保留，用于[原 Controller 显式重新添加旧 Node](./issues/02-binding-lifecycle.md)。若数据库文件存在，先用只读方式检查版本，**在 `PRAGMA journal_mode=WAL`、建表、自动生成身份或其他写操作之前**拒绝 v1/v2、未知/无版本、表不完整或损坏的库。不能因缺少 `meta` 就对现有文件执行建表。对已确认的新库继续使用 `PRAGMA foreign_keys=ON`、WAL、`busy_timeout` 和现有 `BEGIN IMMEDIATE` 写事务模式。
- 旧库“原样拒绝”包括不改数据库及其 WAL/SHM 辅助文件。SQLite [WAL 文档](https://www.sqlite.org/wal.html)指出只读打开在缺少辅助文件时仍可能需要创建它们；版本检查必须避免这种写入。若无法无写入地读取现有文件，就报告无法判定/不兼容并停止；不能用忽略未 checkpoint 的 WAL 的检查结果判断它是空库。
- Controller X25519 私钥保存在 Server 私有状态目录的固定身份文件，数据库只保存文件引用、预期公钥/指纹以供启动交叉检查。文件创建与落盘先于使用它认领 Node；已有库但身份文件缺失或不匹配时停止启动，不能自动换钥。若**仅业务库丢失**而身份文件仍在，允许建立空的新库并由管理员按旧 Node 指纹显式重新添加；不能自动找回业务数据或自动加入旧 Node。启动配置提供的 Local Token 仍不入库。
- Local Node 使用保留 `node_id`，建库事务创建且永不删除。它的 FRPS bind、端口池、Token 等实际值继续来自 Server 启动配置；数据库中的 Local 对外地址/资源索引只是当前启动配置的物化投影，启动时核对并刷新，不能反过来覆盖启动配置。若配置变化与已有 Local Tunnel 的端口或其他 Node 的对外地址冲突，拒绝启动并说明冲突，不能静默挪动业务资源。

## 表与索引

这是关系与约束草图，不冻结 Go 字段名；所有主键/外键、唯一索引和事务边界是实现要求。HTTP hostname 先按现有 IDNA 小写规范化，`location` 保留现有规范化语义后再入库。

| 表 / 字段要点 | 数据库约束与用途 |
| --- | --- |
| `meta(schema_version, controller_key_ref, controller_pubkey)` | 精确版本检查；私钥不入库。另有自动生成的 Local FRP Token（仅启动配置未提供时）。 |
| `nodes(node_id, kind, name, lifecycle, advertised_frp_host, advertised_frp_port, http_ingress_host, http_ingress_port, created_at, updated_at)` | `kind=local/remote`；唯一 Local 保留 ID；`lifecycle=active/removing`，不把在线/FRPS 运行当持久状态。归一化的对外 FRP host:port 在所有活动 Node 间唯一。Local 的地址字段只是启动设置投影。 |
| `remote_nodes(node_id, node_public_key, management_address, desired_revision, desired_hash, desired_snapshot, active_token, staged_token, staged_token_revision)` | 一对一外键 `nodes`；公钥与规范化管理地址分别唯一；`desired_revision` 单调递增且完整快照/摘要同事务提交。保存的期望端口池即 Server 资源分配依据，不另存“已应用端口池”参与分配。Node 实际已应用版本是带观察时间的运行状态。`staged_token` 只用于正在轮换的 Node 快照，Client 投影始终读 `active_token`。 |
| `clients(..., node_id, pending_node_id, pending_since, desired_revision, last_applied_revision, last_applied_node_id, ...)` | 当前归属非空外键；待切换可空外键，指向 `active` Node；`UNIQUE(internal_id,node_id)` 供下方复合外键使用。新 Client 默认 Local。离线待切换不预占目标资源。回执须同时匹配当前 Node 和版本。 |
| `tunnels(..., client_internal_id, node_id, protocol, server_port, enabled, ...)` | `node_id` 是当前 Client 归属的物化值；`FOREIGN KEY(client_internal_id,node_id) REFERENCES clients(internal_id,node_id) DEFERRABLE INITIALLY DEFERRED`。`UNIQUE(node_id,protocol,server_port)` 的部分索引仅约束 TCP/UDP；禁用行也占用端口。 |
| `hostname_owners(hostname_key PRIMARY KEY, node_id)` | 一个域名在主 Server 范围内仅属于一个 Node；最后一条路由删除时，在同一事务中清除此行，避免空归属长期阻塞。 |
| `tunnel_http_routes(tunnel_id, node_id, hostname_key, location_key)` | `(hostname_key,location_key)` 唯一；复合外键 `(tunnel_id,node_id)→tunnels(id,node_id)`、`(hostname_key,node_id)→hostname_owners(hostname_key,node_id)` 可延迟检查，保证路由和整域归属一致。为复合外键增加相应唯一键。禁用 Tunnel 的路由行继续存在。 |

上述[可延迟复合外键](https://www.sqlite.org/foreignkeys.html)允许一次事务内把 Client、它的全部 Tunnel 与资源行一起迁到目标 Node，并在 `COMMIT` 时验证一致性。[唯一部分索引](https://www.sqlite.org/partialindex.html)在写入时约束冲突；冲突由事务整体回滚，不依靠前端预检。实现须在同一写事务里维护 `hostname_owners` 与路由的增删，并对“无路由的 owner”加清理触发器或等效的事务内清理与启动一致性审计。Remote Node 的 FRP Token、快照是密文传输所需的敏感数据，数据库文件及身份目录应仅 Server 用户可读；普通列表响应只投影脱敏字段。

## 写事务与外部调用边界

| 操作 | 单笔 `BEGIN IMMEDIATE` 内必须完成 | 提交后动作 / 冲突 |
| --- | --- | --- |
| 新建、修改、启用或禁用 Tunnel | 读 Client 当前 Node 和该 Node **已保存的期望端口池**；规范化资源；插入/更新 Tunnel、端口索引及 hostname owner/route；递增该 Client `desired_revision`。禁用不删除预留。 | 提交后通知 Client 重取完整快照；端口、路径或跨 Node hostname 冲突则整笔回滚，指出资源。删除 Tunnel 在同笔删除预留并递增版本。 |
| 端口池/Node 运行设置变更 | 检查包含 disabled 在内的已有资源和本机保留端口；递增 Node `desired_revision`，与新完整快照/摘要一起落库。**事务保存成功即以新端口池作为资源分配依据**，不与 Node 旧已应用范围取交集。 | 提交后调和器经 Noise 下发；Node 是否已经应用单独观察并显示。若 Node 尚未应用或应用失败，按新范围保存的 Tunnel 可能暂时无法注册或访问；保留已保存配置，显示不同步/失败原因并等待修复。需要恢复旧配置时用更高版本提交旧内容。 |
| 在线 Client 切换 | 网络健康预检在事务外完成；事务内复核目标仍为活动 Node，重新检查其当前资源及域名整体归属；更新 Client 当前 Node、全部 Tunnel 的物化 Node、路由/hostname owner，清待切换并递增 Client 版本。 | 提交后给 Client 下发目标完整配置，官方 Client 停旧 FRPC；首次目标连接失败也不自动回旧 Node。健康状态可能在预检后变化，属可观察故障，不反向撤销已提交归属。 |
| 离线 Client 选择目标 | 事务内验证有权操作、目标记录存在且为 `active`，写 `pending_node_id`/时间；当前 Node、端口与域名预留不变。 | Client 重连后做新鲜目标健康预检，再执行同一在线切换事务；目标失效或资源冲突则保留待切换并显示原因。用户可取消/替换。 |
| Remote Node Token 轮换 | 阶段 A：生成新 Token，只写 `staged_token`，递增 Node `desired_revision` 并存含新 Token 的完整快照。阶段 B：Node 报告该版本已持久应用后，同笔将新 Token 晋升 `active_token`、清 staged，并递增**此时**当前归该 Node 的所有 Client 版本。 | A 后通过 Noise 下发；B 后通知 Client 重取新 Token。Node 应用失败不执行 B。Server 在 A/B 之间崩溃，重启查询 Node 版本并完成 B；轮换期间只串行化该 Node 的期望快照更新，Client 分配和 Tunnel 保存照常进行，投影使用当时 `active_token`。既有或新分配 Client 可能短暂断开，其他 Node 不受影响。 |
| 正常 Remove | 检查无当前 Client、无 `pending_node_id` 指向它；设置 `lifecycle=removing`，递增 Node 版本并保存 `disabled` 快照/摘要。 | 提交后调和器下发停用；只有 Node 证实持久停用且 FRPS 已停止，才在第二笔事务删 Node。失联/失败保留 removing 并重试。期间拒绝新增归属或资源。 |
| Force Forget | 同样检查无当前/待切换 Client，再在一笔事务删除 Server Node 记录和期望状态。 | 不向 Node 发送停用；界面明确远端可能仍在运行，旧绑定不会消失。重新添加必须用原 Controller 身份、再次核对 Node 指纹并读高水位。 |

所有网络调用、FRPS 验证和健康查询都在数据库事务**外**。事务提交与通知之间的崩溃由启动扫描和周期性调和补偿：以数据库目标版本为权威，查询 Node 已接受/已应用版本后重试；Client 重连时读取当前完整投影。不能把内存事件队列当成唯一更新来源。`SQLITE_BUSY` 在忙超时后映射成可重试冲突；唯一索引/外键失败映射为具体端口、域名或依赖错误，不吞成成功。

Node 的期望配置与实际应用结果是两条独立事实。Server 保存扩大的端口池后可以立即在新范围创建 Tunnel；若远端尚未应用，面板显示 Node `desiredRevision` 与 `appliedRevision` 不一致及相关 Client/代理故障，不把资源保存成功误报为远端已可用。缩小端口池仍须先检查所有已保存 Tunnel（含禁用）是否留在新范围内。

### Token 轮换的关键窗口

Node 已提交新 Token，但 Server 尚未执行阶段 B 时，Node 可能拒绝仍持有旧 Token 的 Client。Server 应持续查询 Node 并完成 B；**不自动让 Node 回到旧 Token**，因为已经下发的新版本是持久事实。数据库保存 staged Token 与对应版本，因此重启后仍能完成晋升并通知所有此刻归属该 Node 的 Client。此时界面显示“Node 已更新，Client 凭据下发中”；轮换的可用性窗口是已接受的短暂重连代价，不能宣称零中断。

若阶段 A 的 Node 应用最终失败，`active_token` 仍为旧值，旧 FRPS 按 Node 回滚规则运行；管理员修复后可用更高 Node 版本重新尝试，或用更高版本明确恢复旧 Token 并结束轮换。不能只清除 staged 记录就假定 Node 已回旧状态。

## 调和层与生命周期接点

建议在 `ServerRuntime` 的 `State` 与 HTTP/Agent Gateway 之间加入 `NodeCoordinator`，持有 Node 存储操作、Local 的 `ManagedFRPS` 接点和 Remote 的 Noise 管理客户端。最小能力为：`ClientProjection(clientID) → 当前 Node 的 FRP endpoint/active Token/完整配置`，`Observe(nodeID) → 带时间的 Node/FRPS 状态`，`Reconcile(nodeID) → 查询远端版本并提交最新期望快照`，`SubscribeChanges()` 通知界面和 Client。`Reconcile` 是幂等触发，不持有数据库写事务跨网络；观察值不得直接替代业务归属。

构造顺序：取得 Server 状态目录锁 → 只读检查已有数据库版本并拒绝旧/未知库 → 加载/创建 Controller 身份并交叉核对 → 打开或建立新库、建立/校验 Local Node 及其设置投影 → 建账户/会话/控制平面 → 构造 Local `ManagedFRPS`、NodeCoordinator、Agent Gateway 与 HTTP Handler → 绑定控制监听 → 后台启动 Local FRPS 与 Remote 调和。Remote 不可达不阻止 Local 与后台面板启动。关闭顺序：先停止接收新请求、取消并等待 Remote 调和任务，再停止 Local FRPS，最后关闭数据库并释放状态目录锁；关闭主 Server 不发送 Remote `disabled`，远端继续运行其最后成功配置。`ServerRuntime` 当前仅持有一个 `ManagedFRPS` 且 Gateway 使用它作为全局准入条件，需要改为按 Client 当前 Node 投影；Local 仍使用原 `ManagedFRPS`，Remote 不调用它。

## 应验证的失败与并发路径

- 两个用户同时在同一 Node 建同协议同端口，以及跨 Node 建同数字端口；同一域名不同路径并发分配给不同 Node；删最后一条路由后转移域名。
- 在线切换与目标端口占用、同域名其他路径仍留旧 Node、目标在健康预检后离线；离线待切换期间新增冲突资源。
- Node 轮换新 Token 后 Server 在阶段 B 前崩溃；Node 应用失败；同时有人请求分配 Client。
- Remove 写入停用意图后 Server/Node 失联；确认回执丢失；Force Forget 不意外发送停用。
- 现有 v1/v2/未知/缺 `meta` 文件字节不变地拒绝；Local 启动配置与数据库物化投影冲突时拒绝；Controller 文件丢失不换钥。
