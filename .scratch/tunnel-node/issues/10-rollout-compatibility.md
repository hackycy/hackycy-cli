# 接入顺序和跨版本验收边界如何确定？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: grilling
Status: resolved
Blocked by: 04, 05, 06, 07, 08, 09

## Question

以前面票据的协议、模型和交互决议为前提，确定可独立交付的实现顺序、每阶段对新部署 Local Node 的行为保证、Server/Node/Client 版本不一致时的拒绝策略，以及达到“设计可实施”所需的验收场景。重点覆盖旧库拒绝、本地 Node 回归、首次认领/普通重启/重复认领、Client 切换所属 Node、离线收敛、配置失败回滚、凭据边界、正常移除/强制遗忘，以及 Controller 身份丢失后旧绑定不可恢复。最终结果应能指导实现，不在票据里编写生产功能。

## Comments

### 2026-09-23：已认领，版本与部署事实

当前 `TunnelProtocolVersion = 4`，Server 的 Client `hello` 和 Client 的 `welcome` 均严格比对版本，参照[`protocol.go`](../../../internal/tunnelruntime/protocol.go)、[`server_agent_protocol.go`](../../../pkg/cmd/tunnel/server/server_agent_protocol.go)和[`client_agent.go`](../../../pkg/cmd/tunnel/connect/client_agent.go)。现行 v4 的在线 `desired_state` 只带 Tunnel 快照与重启 generation，FRP 地址和 Token 只在 `welcome` 中发送；[Client 归属决议](./05-client-node-assignment.md)要求两类消息都承载完整 Node/地址/Token/Tunnel 快照。因此直接替换成新格式而不保留 v4 路径，会令旧 Client 与新 Server 不兼容。现有 Client 还固定校验 FRP 版本与 artifact，仓库当前绑定 FRP 0.70.1。

现有主 Server 仅注册 `tunnel server` 与 `tunnel connect`，Node 命令尚未实现。SQLite 当前 schema v2，只认 v1/v2；新版 schema 旧二进制拒绝打开。Docker Compose 当前仅部署主 Server，持久卷保存数据库、会话及本地 Token；Remote Node 的持久身份目录、端口映射和容器运行说明需要新增。现有集成测试覆盖 Go Client↔Go Server 的 HTTP/TCP/UDP 转发，但尚无 Remote Node 场景。

本轮先问两个独立的运维兼容边界：新版 Server 是否继续让旧版 Client 使用 Local Node；Server 与 Remote Node 管理协议不匹配时是否保留该 Node 上次成功的 FRPS 运行、拒绝新管理动作并要求升级。其余分阶段实现顺序和验收用既定票据及这两个回答形成，不把具体 Go 文件顺序交给用户决定。

### 2026-09-23：用户确定破坏性发布

用户明确两项跨版本兼容都不做；CLI 主要由个人使用，选择减少长期维护负担。进一步确认**连旧 SQLite 数据库迁移也取消**。因此旧 Client 无 Local Node 兼容通路，旧库不自动备份或转换，Server/Node 不维持混合协议功能。此前[落库票据](./07-persistence-migration.md)里“自动备份并迁移”的回答已被这一决议覆盖。

## Answer

### 唯一受支持的发布边界

- Server、Remote Node 和官方 Client 按**同一新发行版及其协议/FRP 构建**部署。客户端控制协议从当前 v4 升到新的单一版本；`hello`、`welcome` 和在线 `desired_state` 只使用新格式，不保留 v4 解析、Local Node 例外或双写。旧 Client 连接新版 Server 时明确报协议不兼容，旧 Token 和旧库数据也不导入。实现时冻结新协议号并统一检查，不能靠字段缺失静默降级。
- Remote Node 管理握手、完整快照格式及 FRP 二进制约束显式带版本。Server 与 Node 不匹配时拒绝认领、配置、切换目标和正常移除的成功判定，面板报告 `NODE_PROTOCOL_INCOMPATIBLE`，提示部署同一发行版；不提供旧版管理协议适配、自动降级或混合版本运行保证。Node 若已认领，失去可用管理连接期间仍可能按[离线运行决议](./04-node-desired-state.md)运行最后成功的 FRPS；版本不匹配**不等于已停用**，不能假称远端已停止。升级两侧并恢复同版本管理后，再按 Node 的高水位 `revision` 收敛。
- 新 Server 只在**空状态目录**建新 schema。旧 v1/v2 或未知 SQLite schema 原样拒绝，不自动迁移、备份、覆盖或默默新建替代库；旧 Server 不能打开新库。账号、Client、Token、Tunnel、路由及容器卷按新部署重新建立。完整拒绝与旧目录留存语义见[落库决议](./07-persistence-migration.md)。此发布不承诺原 Client 连接和 Tunnel 的无中断延续。

### 建议实现顺序及每段完成条件

1. **新基础模型与 Local Node。** 建立只支持新 schema 的空目录初始化、独立 Controller 身份、不可删除的 Local Node、Node 级资源约束及 Client 当前/待切换归属；让现有 `ManagedFRPS` 继续负责 Local 运行。同步升级 Client 控制协议，使 `welcome` 与 `desired_state` 都能发送完整 Node endpoint/Token/Tunnel 快照。完成条件：新数据目录仅用 Local Node 时，账号与 Client 新建、HTTP/TCP/UDP 转发、重连及状态展示均可用；旧库和 v4 Client 给出明确拒绝。
2. **独立 Remote Node 运行时。** 加入唯一 Node 子命令 `ycy tunnel node`；实现持久身份、Noise XX 首次认领与绑定、管理会话及重放边界、FRPS 配置验证/应用/回滚、离线缓存与持久停用。Node 容器示例须挂载身份和运行状态卷，并分别暴露管理、FRP、HTTP 与端口池入口。完成条件：首次认领、重复/抢占认领、进程和容器重启、配置失败回滚、停用后不复活均可独立验证。
3. **Server 的 Remote Node 管理。** 接入 Node 注册表、按 Node 的完整期望快照与单调 `revision`、观察状态、管理 API、认领错误和管理员 Nodes 页面；Local 与 Remote 使用共同业务视图但各自运行接点。完成条件：同版本 Server 能管理一个远端 Node；管理失联、版本不匹配及应用失败均可定位，不影响 Local 或其他 Node。
4. **Client 分配和资源投影。** 接入按 Node 的端口池/域名约束、对外 FRP 与 HTTP 入口、Client 在线提交与离线待切换、官方 FRPC 停旧连新、目标状态和实际登录结果分开展示；普通用户可选所有 Node，所有 admin 可管理 Node。完成条件：Local↔Remote 和 Remote↔Remote 切换遵守预检与不自动回旧 Node 规则，冲突回滚事务，旧 Node 的共享 Token 不被误称为逐 Client 吊销。
5. **移除、运维文档和整体验收。** 补齐正常 Remove 的持久停用确认、Force Forget 的风险提示、Controller 身份丢失提示、Docker/裸机部署与新空目录切换说明，以及下列端到端验收。所有受支持部署场景完成后再发布功能；阶段内尚未接入的入口不作为兼容承诺。

### 旧部署切换说明

停掉旧 Server/Client，**保留旧状态目录原样**供手工查阅或回退；以新的空 Server 状态目录启动新发行版，重新建立管理员、Client、Token 和 Tunnel。若继续使用 Local FRPS，按新部署设置其监听与本地 Token；再以同一发行版启动持久化的 Remote Node、认领并分配 Client，最后用新版 Client 连接。旧 Client 凭据不能直接登录新 Server。旧目录没有由程序自动制作的备份；要回到旧版，只能用旧程序及自己保留的旧目录。已经认领的远端 Node 不会因为 Server 回退自动停机，须先按[移除规则](./02-binding-lifecycle.md)处理。实施文档应把这些操作和预期中断明确写出。

### 发布验收矩阵

| 范围 | 必须验证的可观察结果 |
| --- | --- |
| 新库与旧库 | 空目录建库/重启幂等；旧 v1/v2/未知库拒绝且不改字节；新 Client 默认 Local；旧程序拒绝新库。 |
| Local 回归 | 无 Remote Node 时，新增 Client 的 HTTP/TCP/UDP 转发和控制重连可用；Local Token 遵循启动设置。 |
| 认领与身份 | 指纹核对、首个有效认领者固定、第二 Controller 被拒、错误指纹/不可达/已认领/协议不符提示；重启保持身份，清目录产生新身份。 |
| Node 收敛 | 离线与容器重启沿用最后成功配置；较低版本拒绝、同版本幂等、配置验证失败保留旧运行、启动失败回滚并准确报状态；持久停用后重启不复活。 |
| Client 切换 | 目标离线预检拒绝；在线提交后即使首次 FRPC 登录失败仍固定新 Node；离线 Client 保留待切换到重连且目标就绪；旧 FRPC 在收到切换后停止，归属/应用/登录状态分别显示。 |
| 资源与凭据 | 同数值端口可在不同 Node 复用，同 Node 冲突拒绝；hostname 不能分属两 Node，DNS 未就绪可保存并警示；Token 按 Node 隔离，轮换先 Node 后 Client；旧共享 Token 不冒充逐 Client 吊销。 |
| 移除与恢复边界 | 有 Client 或待切换目标时不能删；正常 Remove 等远端停用确认；Force Forget 只删 Server 记录并警示旧 FRPS；Controller 私钥丢失后不能远程改绑。 |
| 版本与权限 | v4 Client、旧库、错版 Node 均有明确拒绝；错版 Node 不被误报已停机；所有登录用户可见并选择全部 Node，管理操作只开放给 admin。 |

该矩阵是实现与发布门槛，不代表已运行测试；现有 Local 转发集成测试可扩展，新 Node/错版场景需新增针对性验证。
