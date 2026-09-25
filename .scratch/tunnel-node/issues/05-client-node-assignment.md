# Client 切换所属 Node 时控制协议如何保证 FRPC 切换？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: grilling
Status: resolved
Blocked by: 03, 04

## Question

一个 Client 分配给一个 Node，控制 WebSocket 始终连接主 Server；分配更改后，它何时收到新 FRP 地址、端口和凭据，并如何与 Tunnel 快照 revision 一起应用？当前 `welcome` 有 FRP endpoint/Token，后续 `desired-state` 无该信息，而且 Agent Gateway 以本地 FRPS running 为准入条件。需确定协议演进、版本兼容、Client FRPC 切换失败时的行为、目标 Node 不可用时是否允许控制连接，以及切换期间旧 Node 上的会话何时失效。这里讨论的是 Client 所属 Node 的变更，不是 Node 与 Server 的改绑。

参照：[`AgentWelcome` / `DesiredState`](../../../internal/tunnelruntime/protocol.go)、[`BuildWelcome`](../../../pkg/cmd/tunnel/server/server_agent_protocol.go)和[ClientReconciler](../../../pkg/cmd/tunnel/connect/client_reconciler.go)。

## Comments

### 2026-09-23：已认领，第一轮待用户决定

代码核对：现有 `welcome` 一次性下发 FRP 地址和 Token；后续 `desired_state` 只带 Tunnel 快照与重启代数。在线切换 Node 不能仅改 Client 的 `node_id` 或 Tunnel revision，否则 FRPC 仍用旧地址与旧 Token。[`ClientReconciler`](../../../pkg/cmd/tunnel/connect/client_reconciler.go)有候选验证和失败回滚，但按快照 revision 去重，且当前“应用成功”只证明 FRPC 本地进程启动，不证明已经登录目标 FRPS。[`ServerAgentGateway`](../../../pkg/cmd/tunnel/server/server_agent_protocol.go)目前还以唯一 Local FRPS 的运行状态决定全部 Client 是否可接入。

当前 FRPS 使用实例级共享 Token，见[`RenderFRPSConfig`](../../../internal/tunnelruntime/frp_toml.go)与 FRP 的[Token 认证说明](https://gofrp.org/en/docs/features/common/authentication/)；单靠停止官方 Client 的旧 FRPC，不能吊销它已知的旧 Node Token。FRP 0.70.1 的[Server Plugin 文档](https://github.com/fatedier/frp/blob/v0.70.1/doc/server_plugin.md)列出 `Login`、`NewProxy` 等准入钩子，可供后续评估逐 Client 授权，但是否要求此安全边界先由用户决定。

第一轮询问三项可独立决定的行为：目标 Node 当前不可用时是否提交切换；Client 实际连接目标失败时是否回退；切换完成后旧 Node 是否必须拒绝该 Client 再次接入。等待用户选择。

### 2026-09-23：第一轮回答，第二轮待确认

- 目标 Node 当前离线或 FRPS 未运行时，拒绝切换，Client 仍归属旧 Node。
- 用户强调“能连接成功就不可以回退”，切换成功后的目标 Node 故障只展示状态、等待修复，不自动迁回旧 Node。首次尝试连接目标失败、尚未确认切换成功时是否回退，仍需单独确认。
- 切换成功后只要求官方 Client 停止旧 FRPC 连接；不要求旧 Node 在 FRP 层逐 Client 拒绝已知旧凭据的再次接入。这保留当前共享 Token 模型的限制，界面和设计不能声称旧访问权已被强制吊销。

第二轮只问两个具体行为：首次连接目标失败如何处理；Client 的控制连接暂时离线时，管理面是否接受待切换请求。

### 2026-09-23：第二轮部分回答

用户明确：目标 Node 在提交切换时可用，就将 Client 归属固定到新 Node；即使 Client 第一次尝试无法连上，也不自动回旧 Node，只显示目标 Node/FRPC 故障并等待修复。因此“归属变更成功”与“FRPC 已连通”必须是两个独立结果，不能用 FRPC 登录确认作为数据库归属提交的前提。Client 控制连接当时离线是否允许先保存归属，仍待回答。

实现推论待最终答复：`welcome` 和后续 `desired_state` 必须携带同一份完整 Client 运行快照（Node 身份、FRP 对外地址与端口、该 Node Token、Tunnel 定义和统一版本）；Node/凭据变更即使 Tunnel 未变也要递增 Client 版本。Client 收到跨 Node 版本后应停止旧 FRPC，并只尝试新目标；现有 `ClientReconciler` 的“候选启动失败即恢复旧 FRPC”行为只能用于**同一 Node 内**的普通配置失败，不能导致已固定的新归属悄悄回到旧 Node。Server 需把“归属已变更”“配置已应用”“FRPC 已登录/代理已注册”和 Node/FRPS 运行状态分开观察。现有 `FRPSupervisor` 的启动宽限只证明本地进程仍活着；FRP 官方[Client 状态文档](https://gofrp.org/en/docs/features/common/client/)指出可通过启用本机 `webServer` 查询代理状态，因此上线前必须用确切的 FRPC/FRPS 状态信号验证连接，而非仅凭进程 PID 报成功。

### 2026-09-23：第二轮最终回答

Client 控制连接暂时离线时，允许管理员先保存“待切换”；重连后才执行。待切换期间仍以旧 Node 为当前归属，旧 FRPC 可以继续运行；若 Client 重连时目标 Node 已不可用，保持待切换并等待目标恢复。

## Answer

### 切换何时提交

- 一个 Client 在任一时刻只有一个**已提交归属 Node**。切换前先检查目标 Node 已认领、主 Server 可访问、FRPS 运行且目标 FRP 对外地址配置完整；未来的端口/路由约束也须通过[“多 Node 下端口、HTTP 路由与 FRP 凭据按什么范围归属？”](./06-node-resource-scope.md)确定的规则。目标 Node 当前离线或 FRPS 未运行时，拒绝切换，旧归属与旧 FRPC 保持不变。这里的预检只说明主 Server 看到目标可用，不代表每台 Client 都能从自身网络到达它。
- Client 控制 WebSocket 在线时，预检通过就提交新归属并递增该 Client 的期望配置版本，向 Client 发送新配置。**不等待 FRPC 首次登录成功才提交**。此后即使目标网络不可达、FRPC 启动失败或目标 Node 后续离线，也不自动恢复旧归属；界面显示新归属及相应故障，等待修复或有权操作该 Client 的用户再次明确切换。
- Client 控制连接离线时，有权操作该 Client 的用户可以创建持久的**待切换目标**，但当前归属仍是旧 Node，不能把待切换显示成完成。Client 重连且协议兼容时重新检查目标 Node；若仍可用，再提交新归属并下发配置。若目标已不可用，保留待切换和旧归属，待恢复后重试；该用户可以取消或用另一个目标替换待切换。该待切换不要求旧 FRPC 在失联期间停止。权限边界见[“主 Server 的 Node 管理 API 和权限边界是什么？”](./08-management-api-permissions.md)：普通用户能操作自己的 Client，管理员能操作所有 Client，目标 Node 不另设按用户授权。

### Client 控制协议与运行行为

- Client 始终只与主 Server 建立控制 WebSocket；它接收被分配 Node 的**对外 FRP 地址**，绝不接收 Node 管理地址。`welcome` 与后续在线 `desired_state` 必须投影同一份完整 Client 期望配置：目标 `nodeId`、FRP host/port、该 Node 的 FRP Token、Tunnel 快照和统一的 Client `revision`。Node、地址、Token 或 Tunnel 任一运行配置变化都递增版本；不能只改 `client.node_id`，也不能只发当前缺少 endpoint/Token 的 `desired_state`。
- Client 按完整配置版本和内容校验应用；旧版本拒绝、同版本同内容幂等、同版本不同内容为协议错误。应用结果需回报 `revision` 和 `nodeId`，Server 只把与当前期望版本及目标 Node 匹配的回执当作最新结果。Server 重连下发最新完整快照，确保中途丢掉的在线通知不会把 Client 留在旧目标。控制协议升级为单一新版本，旧版 Client 连 Local Node 也明确拒绝；发布边界见[“接入顺序和跨版本验收边界如何确定？”](./10-rollout-compatibility.md)。
- 跨 Node 更新一旦送达 Client，官方 Client 应停止旧 FRPC，验证并尝试启动指向新 Node 的配置。即使新配置验证、启动或登录失败，也不能用[`ClientReconciler`](../../../pkg/cmd/tunnel/connect/client_reconciler.go)当前的通用回滚逻辑重新运行旧 Node 的 FRPC；保留新目标、报告失败并对可恢复故障退避重试。**同一 Node 内**的普通 Tunnel 配置失败仍可恢复上一份可用配置。若控制连接在 Server 提交后、Client 收到更新前中断，旧 FRPC 可能暂时继续运行；Server 必须通过“期望 Node”与“Client 已应用 Node/版本”不一致展示这一过渡状态，不得声称旧连接已停止。
- Gateway 应先认证 Client，再按其当前归属投影目标 Node 的 endpoint/Token 与状态，替换目前对唯一 Local FRPS 的检查。已分配 Node 后来离线时仍允许 Client 保持或重建控制连接，以接收目标配置并报告 FRPC 故障；Client 保持新归属、对新 FRPS 重试，不因数据面故障自动切回 Local Node。没有启用的 Tunnel 时，FRPC 可以保持停止，但 Client 仍应确认已应用新归属配置。

### 成功、故障和旧 Node 权限

- “归属已提交”“Client 已保存/启动目标配置”“FRPC 已登录目标 FRPS”“各启用 Tunnel 已注册”和“Node/FRPS 正在运行”是不同观察值。现有 `apply_result.success` 仅代表本地配置应用与进程启动，不能作为 FRPC 已连通的证明。实现时须从 FRPC/FRPS 的可靠状态信号获取目标连接和代理注册结果，再向 Server 报告；可评估 FRP 官方支持的本机 [Client 状态接口](https://gofrp.org/en/docs/features/common/client/)。面板应显示故障发生在 Server↔Node、Node 的 FRPS、Client↔Node 或 Tunnel 注册哪一段。
- 切换送达后，官方 Client 停止旧 FRPC；**不要求**旧 Node 按 Client 身份强制拒绝再次连接，也不因单个 Client 切换而轮换旧 Node 的共享 Token。因此已获知旧 Token 的自定义 FRPC 仍可能手工接入旧 Node，尤其在 Client 控制连接失联或旧进程尚未收到更新时。此为用户接受的首版权限边界；不能把 Client 归属变化表述成旧 FRP 凭据已吊销。以后若要逐 Client 强制撤销，需另设计 FRPS 准入机制，不能假设当前 per-Node Token 已具备该能力。
- 每个 Node 的 FRP Token 由主 Server 管理，Client 只获得其当前目标 Node 的 Token。正常切换不更换源或目标 Node 的共享 Token，避免无关 Client 重连。若管理员主动轮换某 Node 的 Token，应先确认 Node 已应用新 Token，再对分配在其上的 Client 递增版本并下发完整配置；过渡期可能短暂断开，离线 Client 在重连后取得新凭据，不能声称无损切换。正常 Remove/强制遗忘的旧凭据风险仍按[“Node 绑定、移除和恢复的状态语义是什么？”](./02-binding-lifecycle.md)处理。
