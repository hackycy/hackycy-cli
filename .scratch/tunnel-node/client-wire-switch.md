# Client 新协议与跨 Node 切换契约

状态：已按[“Client 新协议与跨 Node 切换时序如何具体实现？”](./issues/14-client-wire-switch.md)决议。本文细化[Client 归属决议](./issues/05-client-node-assignment.md)和[破坏性发布边界](./issues/10-rollout-compatibility.md)；[交互式切换草模](./client-switch-flow-prototype.html)演示关键路径，不是生产实现。

## 当前代码差异

- 当前 [`TunnelProtocolVersion=4`](../../internal/tunnelruntime/protocol.go)；`welcome` 携带单一 Local FRPS 地址与 Token，后续 `desired_state` 仅带 Tunnel 快照和重启代数。[`client_run.go`](../../pkg/cmd/tunnel/connect/client_run.go)收到在线更新时只替换 `configuration.Snapshot`，所以单靠数据库改 `node_id` 不会切换 FRPC 目的地。
- [`ClientReconciler.ApplyWithResult`](../../pkg/cmd/tunnel/connect/client_reconciler.go)按 revision 去重，停止旧 FRPC、启动候选失败后统一恢复旧 FRPC；跨 Node 时这会违反已提交的新归属。[`ReadClientAppliedState`](../../pkg/cmd/tunnel/connect/client_state.go)只保存最后成功快照，没有“已收到新 Node 目标”高水位；当前不会在 Client 冷启动、尚未获得认证 `welcome` 时从缓存启动 FRPC。用户确认继续沿用这条规则。
- 当前 `apply_result.success` 说明本地配置写入与进程启动成功，`process_state.running` 只说明子进程仍活着，都不能证明已登录目标 FRPS 或代理注册。FRP 官方[Client 文档](https://gofrp.org/en/docs/features/common/client/)支持启用本机 `webServer` 后查询代理状态；实施时需以仓库固定的 FRP 构建验证具体状态接口与字段。

## 单一新版本的完整消息

新 Client 控制协议定为 **v5**，只接受同版本；v4 即使只使用 Local Node 也以现有不兼容关闭码 `4406` 和明确升级提示拒绝。`hello` 仍走主 Server 的 Client Token 认证，不使用 Node 管理身份。以下 `runtime` 是 `welcome` 和在线 `desired_state` 的**同一种完整对象**；Server 必须在同一个数据库读快照中生成它，避免地址/Token 来自一个 Node 而 Tunnel 归属来自另一个事务。

```jsonc
// Client → 主 Server
{"type":"hello","tunnelProtocolVersion":5,"ycyVersion":"...","platform":"...","architecture":"...",
 "lastAccepted":{"revision":8,"nodeId":"node-hk","digest":"sha256:..."},
 "lastApplied":{"revision":7,"nodeId":"local","digest":"sha256:..."}}

// 主 Server → Client；welcome 与 desired_state 都包含完整 runtime
{"type":"welcome | desired_state","tunnelProtocolVersion":5,
 "requiredFrpVersion":"...","artifact":{"version":"...","archive":"...","url":"...","sha256":"...","frpcSha256":"..."},
 "runtime":{"revision":9,"digest":"sha256:...","nodeId":"node-la",
   "advertisedFrpHost":"la.example.com","advertisedFrpPort":7000,"frpToken":"<secret>",
   "clientKey":"<Client ID>","tunnels":["<完整 TunnelDefinition>"]},
 "desiredRestartGeneration":2}

// Client → 主 Server；只确认本地配置应用，不把它冒充网络连通
{"type":"apply_result","tunnelProtocolVersion":5,"revision":9,"nodeId":"node-la",
 "digest":"sha256:...","success":true,"localState":"started | stopped_no_enabled_tunnels"}

// Client → 主 Server；独立的带版本运行观察
{"type":"frpc_status","tunnelProtocolVersion":5,"revision":9,"nodeId":"node-la",
 "digest":"sha256:...","processGeneration":"<本次 FRPC 启动随机 ID>",
 "process":"running | stopped | recovering | failed",
 "connection":"connected | disconnected | unknown | not_required",
 "proxies":[{"tunnelId":"...","state":"registered | failed | unknown","errorCode":"..."}]}
```

`runtime.digest` 是完整运行对象的规范编码 SHA-256：覆盖 `revision` 之外的 Node ID、对外 FRP host/port、Token、ClientKey、按 ID 排序的 Tunnel 定义及其选项；不覆盖外层消息类型和重启代数。同一 revision 不同 digest 为协议错误。按 Go 与 JSON 的实现应冻结规范字段顺序/数字表示或使用明确的 canonical 编码，不能拿任意对象序列化顺序当作跨进程幂等凭据。敏感 Token 仅在已认证 Client 的完整快照里出现，不写错误、日志或状态回报。

Client 只接收对外 FRP 地址，不接收 Node 管理地址。Node 地址、Token 或 Tunnel 内容变化都递增 Client `runtime.revision`；相同内容的重发保持版本。单独的人工重启继续使用 `desiredRestartGeneration`，不伪造一次归属切换。`apply_result` 失败时带结构化错误及实际保留/停止的本地运行状态。`hello`、`apply_result`、`frpc_status` 均带 Node 身份和版本；Server 只将与当前期望三元组 `(revision,nodeId,digest)` 匹配、且来自当前认证 WebSocket 会话的回报当作“当前状态”，迟到的旧 Node 回报不覆盖新归属。

## Client 持久状态和应用顺序

每个 Client 实例的私有状态目录保存一份原子更新的状态文件：`highestAccepted=(revision,nodeId,digest,完整目标)`、`lastGood=(revision,nodeId,digest,完整成功本地配置)` 和最近应用失败。FRPC 配置文件由它渲染，是工作文件；状态目录与凭据文件权限沿用当前私有文件规则。缓存**不是授权来源**：每次 Client 进程冷启动，先与主 Server 完成 Token 认证并收到 v5 `welcome`，才决定启动哪份 FRPC。主 Server 暂时不可达时保持 FRPC 停止并重试控制连接；运行中的控制连接暂时断开时，当前 FRPC 可以继续运行和重试其已接受目标。这两个场景应在面板分别显示，避免把冷启动等待误报为 Node 故障。

1. 收到完整有效的目标后，比较 `revision` 与 digest：低版本拒绝，同版本同内容幂等，同版本异内容为协议错误。先将新 `highestAccepted` 原子落盘，确保进程重启后不会把旧 `lastGood` 当成新目标。缓存写入失败时不得报告应用成功。
2. **同一 Node**：候选渲染和 `frpc verify` 在旧进程运行时完成；验证失败不动旧进程。若启动或成功状态落盘失败，按现有能力恢复同 Node 的 `lastGood`，报告新版本失败及真实 FRPC 状态。高水位仍为新版本，不接受旧版本命令覆盖；修复配置由 Server 发更高版本，暂时性进程故障可按当前目标退避重试。
3. **跨 Node**：一旦收到并验证新目标，先停旧 FRPC，再验证、启动新 Node 的候选配置；任何本地验证/启动/登录失败都**不重启旧 Node 的 FRPC**。若持久高水位写入失败，也停止旧 FRPC 并报告本地存储故障，等待同一新目标重新下发；冷启动仍须认证主 Server，不利用旧缓存回退。新目标的本地启动成功后写入 `lastGood` 并回 `apply_result.success`，实际登录/代理注册继续单独观察和重试。
4. 若没有启用 Tunnel，仍写入新 Node 的完整 `lastGood`，停止 FRPC 并回报 `stopped_no_enabled_tunnels`；`connection=not_required`，不能显示成登录失败。Client 后续启用 Tunnel 再按新完整版本启动目标 FRPC。

`lastAppliedRevision` 只在本地状态持久提交成功后推进；跨 Node 启动失败时它可仍指向旧 Node，但 `highestAccepted` 已指向新 Node，故旧 `lastGood` 只能用于历史展示，不能触发恢复。Client 重连时上报两个元组；主 Server 始终返回当前完整目标。若缓存高水位高于 Server 期望，报状态冲突并停旧 FRPC 等待管理员检查，不能用低版本暗中覆盖。Server 丢失通知后重连仍可送最新目标；当前 [`serverAgentOutbound`](../../pkg/cmd/tunnel/server/server_agent_outbound.go)的“只保留较新目标”策略可延续，但比较键须包含完整配置版本/摘要，不能只比较 Tunnel 快照和重启代数。

## 五条必须成立的时序

| 场景 | 主 Server 与 Client 的先后结果 |
| --- | --- |
| 在线切换 A → B | Server 对 B 做可用性与资源预检；一笔事务提交 `client.node_id=B`、Tunnel 资源迁移和 Client rev+1；`desired_state(runtime B)` 发出。Client 持久记录 B 目标、停 A、验证/启动 B，先报本地应用，再报 B 的连接/代理状态。Server 面板分别显示归属 B、Client 是否已接收、B 数据连接。 |
| 离线待切换 | Server 只写 `pending_node_id=B`，`node_id` 仍 A。Client 重连后，Server 对 B 重新做健康和资源检查；通过才提交切换并在 `welcome` 中给完整 B 快照。不通过保持待切换、继续给 A 快照并显示原因。 |
| B 首次登录失败 | B 归属已经提交，Client 已停 A 且本地 B 配置可能已启动；`apply_result` 可成功，但 `frpc_status.connection=disconnected` 或代理失败。Client 只重试 B，Server 不回滚归属，也不声称已连通。 |
| 提交后通知丢失 | Server 已持久提交 B，但 Client 尚运行 A；面板用目标 B 与 Client 最后应用 A 的差异显示“待送达/旧连接可能仍在”。下次 WebSocket 重连的 `welcome` 总是最新完整 B，Client 停 A 并切 B；不能仅凭 Server 的事务提交判定旧连接停止。 |
| 同 Node 配置失败 | Server 在 A 上修改 Tunnel 并递增 rev；Client 验证失败时 A 的旧 FRPC 仍运行，或启动失败后恢复 A 的 `lastGood`。`apply_result` 报新版本失败，已运行的旧版本单独显示；Server 修复后下发更高 rev，不自动改 Node。 |

## FRPC 真实状态观察

Client 为自己管理的 FRPC 在**仅本机回环地址**启用其 [webServer 状态接口](https://gofrp.org/en/docs/features/common/client/)，使用每实例独立的本地监听端口与私有认证信息；只由本机 Client Agent 查询，不暴露给主 Server 或公网。状态适配器绑定本项目固定的 FRP 构建并做端到端验证，按期望 Tunnel ID 对应的 FRP proxy 名称（现有渲染器使用 `t_<Tunnel ID>`）读取注册/错误状态。查询不到、接口不可用或格式不符时报告 `unknown` 与诊断错误，不从进程 PID 推断 `connected`。

有启用代理时，只有可信状态明确显示目标代理已注册，才可报告该代理 `registered`；目标版本至少一个代理已确认注册，可据此推断 FRPC 已与目标 FRPS 建立连接。若没有代理已注册且接口未给出独立连接证据，登录状态为 `unknown` 或按明确错误报 `disconnected`，不能从进程 PID 推断 `connected`。进程存活、认证主 Server、FRPC 连接目标 Node、代理注册是四层不同事实。`processGeneration` 与目标三元组绑定观察，避免旧进程的延迟状态被记在新 Node 名下；主 Server 为最后观察标上接收时间，断线或过期后显示未知，不把历史状态当作实时在线。当前只用 `ProcessState` 的 [`clientProcessStateReporter`](../../pkg/cmd/tunnel/connect/client_run.go)和 Server Gateway 需扩展，不能直接重命名状态便宣称有登录监测。

## 实施验证

- v4/v5 双向错版拒绝，Local Node 也不例外；`welcome` 与在线 `desired_state` 对同一数据库 revision 得到字节等价的 runtime；修改 Node 地址或 Token 而 Tunnel 不变仍递增 Client revision。
- 旧 revision、同 revision 异 digest、重连后重复完整快照、Server 丢失在线通知、Client 在持久目标/停旧/启新/写成功状态各点崩溃。
- 在线和待切换、跨 Node 首次登录失败、同 Node 验证/启动失败的实际 FRPC 进程与 Server 归属不变量；没有 Tunnel 时归属仍应用。
- 用固定 FRP 构建集成验证本机状态适配器：登录失败、一个代理注册失败、全代理成功、状态接口失效和进程代际变化；`apply_result.success` 不单独触发“已连接”。
