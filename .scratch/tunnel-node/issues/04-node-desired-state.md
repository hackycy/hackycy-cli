# 主 Server 应下发什么 Node 期望状态，Node 如何收敛？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: grilling
Status: resolved
Blocked by: 02, 03

## Question

决定远端 Node 的最小管理契约：期望状态里哪些 FRPS 设置、凭据和配置版本由主 Server 掌管，哪些只能由 Node 本机启动参数决定；用完整快照还是增量指令；Node 如何验证、原子应用、回滚并报告 `desired/applied/failed`。需说明主 Server 或 Node 离线后是继续运行上次成功配置、停止服务还是采用其他策略，以及重连后如何避免旧 revision 覆盖新状态。

对话提出 `PUT desired-state`，当前 `ManagedFRPS` 管理静态配置；要决定二者可复用的边界，而非把 Client 的 `DesiredState` 结构直接移给 Node。

## Comments

### 2026-09-23：运行行为确认与代码核对

用户确认：主 Server 暂时不可达、包括 Node 重启后仍不可达时，Node 继续运行上一次成功配置；新配置验证或启动失败时恢复上一次能工作的配置，并向 Server 报错。

当前 [`ManagedFRPS.start`](../../../pkg/cmd/tunnel/server/server_frps.go)会先停旧 FRPS，再写入并验证新配置，失败时没有回滚；其 `Stop` 也不保存停用标记，不能直接承担远端期望状态收敛。[`FRPSupervisor`](../../../internal/tunnelruntime/frp_supervisor.go)只监督本机子进程，重启后不会自行加载历史配置。可复用其进程监督和[`RenderFRPSConfig`](../../../internal/tunnelruntime/frp_toml.go)，但远端需另建持久期望状态调和器。现有[`ClientReconciler`](../../../pkg/cmd/tunnel/connect/client_reconciler.go)的候选配置验证与回滚流程可作参考；其“未收到 welcome 不从缓存启动”的策略不能照搬到 Node。

## Answer

### 配置权威和快照边界

- 主 Server 是每个 Remote Node 的 FRPS 运行配置和凭据权威。管理通道发送**完整的 Node 期望状态快照**，而不是 `start/stop/restart` 等互相独立的命令。快照至少含协议/配置格式版本、Node 身份、该 Node 单调递增的 `revision`、`running` 或 `disabled` 目标，以及运行时需要的 FRPS bind 地址与端口、HTTP vhost 端口、允许的 TCP/UDP 端口范围和该 Node 的 FRP Token。远端 Node 不保存 Client、Tunnel、账号等业务权威数据，也不自行生成 FRP Token。
- 主 Server 为每个 Remote Node 生成并持久化独立的 FRP Token；现有 `YCY_TUNNEL_FRP_TOKEN` 和本地 FRPS Token 行为保留给 Local Node。Token 仅经已认证的 Noise 管理会话交给对应 Node、经已认证的 Client 控制连接交给分配至该 Node 的 Client，不放进公开状态响应或日志。Token 轮换也是新 `revision`；切换 Client 的生效次序由[“Client 切换所属 Node 时控制协议如何保证 FRPC 切换？”](./05-client-node-assignment.md)确定。
- 主 Server 记录并向 Client 发布的 **Advertised FRP Address** 与 Node 的 FRPS bind 地址是两项独立配置；前者不写入 Node 的 `frps.toml`，可与监听端口不同，以容纳公网映射。现有自定义 404 页面若配置，由主 Server 将**内容**作为快照的一部分发给 Remote Node，Node 写入自己的本地路径；不能把主 Server 的文件路径原样发过去。是否在后台提供逐 Node 编辑入口由 API/UI 票据决定。
- Node 本机启动设置仅负责管理 HTTP 的监听地址与端口、持久状态目录、FRP 二进制/日志等机器环境。它不预配 Controller 地址，不在本地启动参数中另设一套与主 Server 冲突的 FRPS 端口或 Token。仍只有 `ycy tunnel node` 一个 Node 子命令；这些设置可通过启动参数或环境变量提供，具体名称留到实施规格。

### 应用、回滚与离线运行

1. 主 Server 先持久化最新期望快照及 `revision`，再通过 Noise 保护的 HTTP 管理请求下发。每个 Node 同时只应用一份快照。重连后主 Server 先读取 Node 状态，再发送本地最新快照；短暂失联不生成无意义的新版本，也不使其他 Node 的管理操作停顿。
2. Node 先验证已绑定 Controller 身份、目标 Node 身份、格式版本、字段和端口约束，再在私有临时文件中渲染配置与 404 页面，执行 `frps verify -c`。**验证失败不停止当前 FRPS**。验证通过后才停止旧进程、启动候选配置并等待启动确认；成功后把新快照、有效配置和 `appliedRevision` 持久提交，再返回成功。不能直接调用当前“先停再验证”的 `ManagedFRPS.start` 实现。
3. 候选 FRPS 启动或提交失败时，恢复上次成功快照和配置并重启旧 FRPS；保留旧 `appliedRevision`，记录这次 `failedRevision` 与结构化原因。端口被外部进程占用等情况也可能使回滚失败；此时必须如实报告 `rollback_failed` 和 FRPS 不可用，不能把“已尝试回滚”显示为“正在运行”。旧进程后续意外退出时，现有 supervisor 可按旧有效配置退避重启；`appliedRevision` 只代表配置曾成功应用，运行可用性仍以实时进程状态报告。
4. Node 保存已接受的最高 `revision` 与快照摘要、最后成功应用的快照/配置、应用结果和持久停用标记。低于最高已接受版本的请求拒绝；同版本同内容为幂等重试，同版本不同内容拒绝。失败版本不会把旧版变为可写；要回退配置，主 Server 须用**更高** `revision` 重新提交旧内容。若主 Server 仅丢失业务库而保留 Controller 身份，重新添加原 Node 时先读取其版本，再从更高版本继续，不能从 1 盲目覆盖。
5. 主 Server 暂时离线时，Node 不需要主动找 Server，继续运行最后成功的 FRPS 配置。Node 进程或容器重启后读取持久目录：已认领且最后应用目标为 `running` 时，先从上次成功配置启动 FRPS，再等待原 Controller 重新连接与收敛；未认领时不启动 FRPS。失败的新期望配置不在离线冷启动时取代旧配置。Server 面板把 Node 连通性、`desiredRevision`、`appliedRevision`、失败原因和 FRPS 进程状态分开展示，不能因版本相同就推断服务在线。
6. 正常 Remove 使用更高版本的 `disabled` 快照。Node 先持久保存停用意图，停止 FRPS，清除可供重启旧 FRPS 的有效配置和 Token，再持久确认停用完成；只有确认 FRPS 已停且重启后不会复活，主 Server 才完成 Remove。停用失败或 Node 离线维持待移除，强制遗忘的风险仍按[“Node 绑定、移除和恢复的状态语义是什么？”](./02-binding-lifecycle.md)处理。重新添加仍绑定原 Controller 的已停用 Node 时须下发新 `running` 快照和新 Token。

### 实施接点与状态语义

- Remote Node 应复用底层[`FRPSupervisor`](../../../internal/tunnelruntime/frp_supervisor.go)、[`RenderFRPSConfig`](../../../internal/tunnelruntime/frp_toml.go)与 FRP 二进制准备能力，新增负责候选配置、持久化、回滚的 Node 调和器；不要直接复用含 Server 静态设置、HTTP 状态投影和无回滚启动顺序的 `ManagedFRPS`。Local Node 继续走现有 `ManagedFRPS`；将来若统一本地回滚能力，可单独改造而不作为远端接入的前置条件。
- 远端管理层至少提供“提交完整期望快照”和“读取观察状态”两个能力。面板若需要“重启”，主 Server 可对相同运行配置创建新 `revision` 要求重应用，不需要另设没有持久语义的远端 Restart 命令。状态区分 `desiredRevision`（主 Server 目标）、Node 已接受的版本、`appliedRevision`、`failedRevision/error`、FRPS 实时进程状态以及 Node 连通性；`PUT` 的 HTTP 成功只说明一次交互完成，只有已持久应用且启动成功的结果才算收敛。
- 快照格式与 FRP 二进制版本须显式检查；不匹配返回稳定错误。已经运行的 Node 在管理连接不兼容时可能仍持有最后成功配置，但混合版本不受支持，不能据此宣称已停机或允许新的分配；发布边界与升级顺序见[“接入顺序和跨版本验收边界如何确定？”](./10-rollout-compatibility.md)。
