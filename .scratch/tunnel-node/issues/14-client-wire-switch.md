# Client 新协议与跨 Node 切换时序如何具体实现？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: prototype
Status: resolved
Blocked by: 05, 07, 10

## Question

以现有 Client 协议 v4 为基础，草拟单一新版本的 `hello`、`welcome`、`desired_state`、应用回执和 FRPC 连接/代理注册状态消息。明确完整配置的字段、Node 身份与 `revision` 规则、重连后的去重、客户端持久化、旧 v4 拒绝方式；`welcome` 和在线更新必须投影同一份 FRP endpoint/Token/Tunnel 配置。

给出在线切换、离线待切换、目标 Node 提交后首次 FRPC 登录失败、Server 提交后通知丢失、同 Node 配置失败五条时序。特别核对现有 `ClientReconciler` 的回滚逻辑：跨 Node 失败不能自动启动旧 FRPC；同 Node 失败可按已决议规则恢复。说明实际 FRPC 登录和 Tunnel 注册状态从哪里观察，不能只凭进程 PID 声称成功。

## Comments

### 2026-09-23：已认领，消息与切换草案待反馈

已核对当前 [`protocol.go`](../../../internal/tunnelruntime/protocol.go)：协议 v4 的 `welcome` 有 FRP 地址/Token，在线 `desired_state` 只有 Tunnel 快照；[`client_run.go`](../../../pkg/cmd/tunnel/connect/client_run.go)收到在线消息时只替换 `Snapshot`，不会切换 FRPC 目标。[`ClientReconciler`](../../../pkg/cmd/tunnel/connect/client_reconciler.go)启动候选失败时总会尝试恢复旧 FRPC，同一逻辑不能用于已提交归属的跨 Node 切换。当前缓存只保存最后成功快照，Client 冷启动需要认证 `welcome` 才激活 FRPC。

已写出[Client 新协议与跨 Node 切换评审稿](../client-wire-switch.md)及[交互式切换草模](../client-switch-flow-prototype.html)，用 v5 完整 runtime 对象贯穿 `welcome`/`desired_state`，把本地应用与 FRPC/代理状态分开，并列出五条必经时序。FRP 官方[Client 文档](https://gofrp.org/en/docs/features/common/client/)说明启用本机 `webServer` 后可以查询代理状态；具体状态字段仍需用本项目固定 FRP 构建在实施时验证。此时草案尚未成为决议。

本轮需用户明确一个实际使用边界：官方 Client 进程在主 Server 不可达期间重新启动时，是否仅等待认证 `welcome` 再启动 FRPC（延续当前安全边界），还是直接运行本地缓存的旧 FRPC 以提高短时可用性。运行中控制连接断开时保留现有 FRPC，已由前置决议确定，不再重问。

### 2026-09-23：用户确认及契约定稿

用户选择“等待认证 `welcome` 后再启动”，即 Client 进程冷启动且主 Server 暂时不可达时不从旧缓存运行 FRPC；运行中的控制连接断开仍让当前 FRPC 继续运行。已整理为[Client 新协议与跨 Node 切换契约](../client-wire-switch.md)，并同步[交互式切换草模](../client-switch-flow-prototype.html)中的冷启动场景。以下 Answer 是本票据决议。

## Answer

- Client 控制协议使用单一 v5 结构；旧 v4 即使只连接 Local Node 也明确拒绝。`hello` 报已接受和最后成功应用的版本/Node/摘要；`welcome` 与在线 `desired_state` 均承载**同一完整 runtime 对象**，包括 Client revision/摘要、Node ID、对外 FRP host/port、该 Node Token、ClientKey 与全部 Tunnel，并带 FRP 构建要求。Server 在一个一致的数据库读快照中生成对象，Node、地址、Token 或 Tunnel 变化均递增 Client revision。
- Client 私有状态持久保存最高已接受目标与最后成功配置，比较 `(revision,nodeId,digest)`；低版本拒绝，同版本同内容幂等，同版本异内容为协议错误。进程**冷启动**须先通过主 Server 认证并收到 `welcome`，缓存不能单独启动 FRPC；运行中的控制连接暂时断开时，已运行的 FRPC 可以继续。主 Server 重连总下发最新完整目标，在线通知丢失由此补偿。
- 跨 Node 目标被接受后，官方 Client 停止旧 FRPC；候选验证、启动或目标首次登录失败均不恢复旧 Node，只保留新目标并报告/重试。相同 Node 的配置验证或启动失败可保留/恢复最后成功的该 Node 配置。没有启用 Tunnel 时，仍持久确认新 Node 归属，FRPC 保持停止。详细崩溃顺序及五条切换时序见[契约](../client-wire-switch.md)。
- `apply_result` 只确认本地配置应用，不代表已连接。另发携带 Node/版本/进程代际的 FRPC/代理状态；通过本机回环上的 FRPC 状态接口观察代理注册，接口不可用时显示未知，不能用 PID 冒充登录成功。Server 仅采纳匹配当前期望版本、Node 和摘要的回报，并对过期观察降级；状态来源与验证场景见[契约](../client-wire-switch.md)。
