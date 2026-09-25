# Node 管理链路的 Noise/HTTP 消息契约如何落地？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: prototype
Status: resolved
Blocked by: 01, 02, 04, 08, 10

## Question

在[认领安全决议](./01-claim-trust.md)确定 Noise XX、公网 HTTP、首个有效认领者固定后，给出一份可审阅的线协议草案：握手的 HTTP 往返与消息封装、Node/Controller 长期公钥和指纹编码、认领提交及并发胜者、会话 ID 与双向序号、超时重试和幂等查询、版本不匹配拒绝、响应错误与大小/时间限制。需明确哪些匿名请求可用，哪些请求必须经过 Noise 认证加密；认领前后在崩溃点上的可观察结果不能含糊。

草案应含至少一次完整认领、一次管理状态读取、一次期望快照提交及重复请求的时序示例。对具体 HTTP 路由名和 Go 包名可由实施者按仓库风格决定，不把这些命名交给用户拍板。

参考：[管理 API 权限决议](./08-management-api-permissions.md)、[期望状态决议](./04-node-desired-state.md)。

## Comments

### 2026-09-23：已认领，草案前的事实核对

[Noise 规范](https://noiseprotocol.org/noise.html)的 XX 模式包含三条交替的握手消息，握手完成后产生两个方向的 transport `CipherState`；协议还允许通过共同的 prologue 绑定应用上下文。规范限制每条 Noise 消息不超过 65535 字节，而现有 [`ManagedFRPS`](../../../pkg/cmd/tunnel/server/server_frps.go)允许最大 512 KiB 的自定义 404 页面，因此远端快照不能无条件塞进一条 transport 消息。实现草案必须明确分块/大小上限，不能仅写“加密 JSON 后 POST”。

当前仓库没有 Node 管理协议，现有 Client WebSocket 协议是 v4，不能直接拿它的 Bearer Token 当 Node 身份。下一步先给出 HTTP 握手和加密请求时序，再用认领并发、丢失响应及重放示例核对是否满足已决议的首认领和持久绑定规则。

初版评审稿提出“每个管理操作一条短期 Noise 会话”，把身份/配置持久性留给 Node 磁盘状态，避免跨操作会话恢复。

### 2026-09-23：用户确认及协议定稿

用户回答“按你的方案设计”，接受每个管理操作重新建立 Noise XX 短会话、较大配置在同一操作会话内分块传输的方向。结合 Noise 消息上限及现有 512 KiB 404 页面边界，已将初版草案细化为[Node 管理线协议契约](../node-management-wire.md)，包括握手帧、会话计数、指纹编码、认领并发、版本拒绝、分块快照、超时重试、错误与大小限制。以下 Answer 是本票据决议，附件给出详细时序和输入边界。

## Answer

- 每个 `claim`、`status`、`applySnapshot` 管理操作独立运行一次 `Noise_XX_25519_ChaChaPoly_SHA256` 握手；多块快照只在本次短会话内传输，操作结束或超时即销毁会话。Node 不需要跨重启恢复会话密钥或序号。管理协议版本与 Client 控制协议分开，版本不匹配直接拒绝，不做降级。
- Node/Controller 使用持久 X25519 静态身份。管理员先预览 Node 指纹，再确认；正式认领重新握手并比较完整 Node 公钥。Node 从 Noise 握手识别 Controller，仅在加密 `claim` 完整有效时按原子持久化顺序让首个 Controller 获胜。未知 Controller 不得到原绑定详情；同一 Controller 可在响应丢失后用新会话查询结果。
- 外层 HTTP 只承载握手和 Noise 密文。每会话有随机 ID、双向严格递增序号，解密正文绑定 Node、协议版本、操作与请求 ID；旧序号和重复密文拒绝。丢失响应后废弃会话、重新握手并查询已持久化结果，不跨会话重放密文。唯一匿名内容为无敏感信息的健康/协议提示。
- 快照采用有序 `begin`/`chunk`/`commit`，每块不超过 32 KiB、总量不超过 2 MiB；Node 在完整摘要与版本校验通过前不提升最高已接受版本或影响旧 FRPS。低版本、同版本不同内容、无效格式、运行失败分别返回稳定错误。具体磁盘提交与崩溃恢复顺序在[Node 状态票据](./12-node-state-crash-consistency.md)继续决定。
- 消息字段、握手往返、资源时限、状态读取、配置下发、认领竞态和异常恢复以[Node 管理线协议契约](../node-management-wire.md)为准；实施时用 Noise 测试向量及并发/超时/大消息场景验证，不自写加密算法。
