# Node 管理线协议契约

状态：已按[“Node 管理链路的 Noise/HTTP 消息契约如何落地？”](./issues/11-node-management-wire.md)决议。本文是该票据的详细协议附件；安全边界以[认领决议](./issues/01-claim-trust.md)为前提，磁盘提交顺序由[Node 崩溃恢复票据](./issues/12-node-state-crash-consistency.md)继续细化。

## 协议与身份

- **一个管理操作建立一个短期 Noise XX 会话。** 状态读取、认领和提交配置各自重新握手；分块配置传输在该操作的同一会话内完成。会话只在内存中存在，不跨操作或进程重启恢复。Node 长期身份、固定 Controller 和配置版本仍在磁盘上持久化。这是用户选定的低维护复杂度方案。
- 固定 `Noise_XX_25519_ChaChaPoly_SHA256`，Node 管理协议初版为 `1`，独立于 Client 控制协议。Noise prologue 固定为 UTF-8 字节串 `ycy/tunnel-node-management/1`；版本不协商，不支持旧版降级。使用经协议测试向量验证的 Go Noise 实现，不自写握手或 AEAD。
- Node 与 Controller 各持久保存一对 32 字节 X25519 静态密钥。指纹格式为 `SHA256:` 加 `SHA-256(原始 32 字节公钥)` 的无填充 Base64URL 编码；面板显示完整值，确认时比较完整公钥字节，不凭地址或缩短指纹认定身份。Node 的业务 `nodeId` 可独立存储，但不能替代公钥固定。
- HTTP 只承载握手帧和密文帧。一次性握手 ID、会话 ID、配置 `revision` 和操作请求 ID 彼此独立，均不充当身份凭据。Controller 私钥和 Node 私钥绝不经过 HTTP；Node 管理地址变化不改变已固定身份。

## 管理员预览与正式认领

```text
管理员输入 Node HTTP 管理地址
Server ── XX 消息 1 ──> Node
Server <── XX 消息 2 ── Node；取得 Node 长期公钥
Server 显示 Node 指纹，管理员与 Node 本机输出核对
预览握手过期，不产生绑定

管理员确认
Server ── 新的 XX 消息 1 ──> Node
Server <── 新的 XX 消息 2 ── Node；再次核对同一公钥
Server ── XX 消息 3 ──> Node；双方取得会话密钥
Server ── 加密 claim(请求 ID) ──> Node
Node   ── 原子落盘首个 Controller 绑定 ──> 加密返回结果
```

正式认领时必须重新验证预览过的完整 Node 公钥。Node 从 Noise 第三条握手消息取得 Controller 静态公钥；`claim` 正文不允许提供另一个可替换的 Controller 公钥。Node 只在收到完整且有效的加密 `claim` 时争夺认领权；单纯握手不锁定 Node。多个有效认领并发时，持久化锁内只允许一个成功，其他请求只获不泄露原 Controller 的 `NODE_ALREADY_CLAIMED`。Node 发出成功回执前，绑定必须已持久化。若成功回执丢失，原 Controller 用新会话查询绑定结果；不同 Controller 不能用“重试”夺取绑定。

## HTTP 帧与会话顺序

外层使用 JSON，Noise 原始字节使用无填充 Base64URL。以下路径表达**语义契约**；实施时可按现有路由风格改名，但不能暴露明文业务操作。

| HTTP 操作 | 请求 / 响应外层字段 | 安全条件 |
| --- | --- | --- |
| `GET /health` | 固定的管理进程可达性和协议版本 | 匿名可用，不返回身份、绑定、FRPS、配置或原 Controller 信息。返回的版本提示未认证，只用于诊断。 |
| `POST /node/noise/start` | 请求 `version, m1`；响应 `handshakeId, m2` | Node 为每次握手创建独立暂存状态；Server 从 `m2` 取 Node 公钥并核对指纹。预览只走到这里，Node 不产生绑定。 |
| `POST /node/noise/finish` | 请求 `handshakeId, m3`；响应 `sessionId, seq=0, ciphertext` | `ciphertext` 是 Node 的加密确认，正文也包含 `sessionId` 和 Node 身份。身份不符时返回加密错误并关闭；完成后删除握手暂存。 |
| `POST /node/noise/message` | 请求 `sessionId, seq, ciphertext`；响应 `seq, ciphertext` | 每个方向严格按下一序号处理；解密正文含 `version, sessionId, nodeId, operation, requestId, body`，外层 ID 必须与正文匹配。操作与回执全程加密。 |

`handshakeId` 和 `sessionId` 均由 Node 的安全随机数生成，至少 128 位；仅作为内存查找句柄，不是身份认证凭据。Node 为每个方向保留 Noise 自身的 `CipherState` 和预期序号，序号从 0 开始：加密确认占 Node→Server 的 0，首个操作请求占 Server→Node 的 0，其响应占 Node→Server 的 1。只有成功验密且序号正确才推进状态。一个会话串行执行一个操作；`claim`/`status` 一次请求后关闭，配置提交的分块可连续使用，完成或超时即销毁。HTTP 重试不能并发复用同一会话。

会话的认证身份来自 Noise 握手中的静态公钥，而不是请求正文的 `nodeId`、`requestId` 或 IP。已认领 Node 只允许固定 Controller 查询状态或提交配置；陌生 Controller 可完成握手以获通用 `NODE_ALREADY_CLAIMED`，但不能看到绑定详情。未认领 Node 仅允许加密 `claim`，不能读取详细状态或下发 FRPS。Node 只接受 `claim`、`status`、`applySnapshot` 三类语义操作；停用属于 `applySnapshot(disabled)`，不提供远程解绑或命令式 start/stop/restart。普通 HTTP 请求不传 FRP Token、期望配置、私钥或详细状态。

资源界限：单个 HTTP 请求体最多 96 KiB；单个 Noise transport 密文仍须满足协议的 65535 字节限制。候选快照总大小最多 2 MiB，单块正文最多 32 KiB。握手暂存 15 秒过期，空闲会话 60 秒过期，整个会话最长 10 分钟；Node 同时最多保留 64 个握手和 32 个会话，超出返回不带身份细节的繁忙错误。超时或进程退出只丢内存会话，不改变已落盘绑定、版本或有效 FRPS 配置。这些值是首版的输入边界，后续若调整须同步双方协议和验收。

## 较大快照

[Noise 规范](https://noiseprotocol.org/noise.html)限制单条消息最多 65535 字节；当前 Server 的自定义 404 页面上限是 512 KiB。一个 `applySnapshot` 会话依次发送：

```text
begin  {revision, nodeId, totalBytes, sha256, formatVersion, frpVersion}
chunk  {offset, bytes}     // offset 必须恰好等于已收到字节数，单块 ≤ 32 KiB
...
commit {revision, sha256}   // 完整字节数和摘要吻合后才接受
```

Node 将候选写入权限为 0600 的私有暂存文件；分块到齐和摘要匹配前，不提升最高已接受版本，也不碰运行中的 FRPS。`commit` 在 Node 身份、Controller、格式、版本、端口范围与 FRP 构建检查通过后，按[期望状态决议](./issues/04-node-desired-state.md)记录最高已接受版本/摘要，并进入验证、应用或回滚。候选验证失败也保留该版本的失败结果。低版本或同版本不同摘要拒绝。相同版本和摘要若已有最终结果，仅回报该结果；若持久状态显示因崩溃中断且尚无最终结果，原 Controller 可重传以继续这次未完成的应用。最终失败后想再次尝试，Server 须使用更高版本。中途失联只丢弃未提交候选，不改变已应用配置；`commit` 响应丢失时先查询版本和结果。暂存文件清理及崩溃恢复的持久顺序以[Node 状态决议](./issues/12-node-state-crash-consistency.md)和[崩溃恢复契约](./node-state-crash.md)为准。

## 丢包、重放与错版

- 同一会话的旧序号或重复密文直接拒绝，不重新执行；请求与响应各自单调递增。会话一旦丢失响应，调用方丢弃该会话并重新握手，不把相同密文复制到新会话。
- `claim` 成功响应丢失时，用原 Controller 身份和已核对的 Node 公钥重新连接，查询绑定。配置 `commit` 响应丢失时，查询 `highestAcceptedRevision`、摘要、`appliedRevision` 与最后失败结果；已有最终结果就读取结果，持久状态显示应用中断则可重传相同版本/摘要继续。若该版本从未被接受，可重新传相同内容；已接受但最终失败时，修复后须以更高版本重新提交。
- 错误协议版本或 FRP 构建不能进入配置应用路径，Server 不得将其作为可切换目标。错版的已认领 Node 仍可能运行缓存 FRPS，错误提示必须说明“管理不可用，远端运行未知”，不能报告停机成功。

## 时序核对

| 场景 | 必须出现的结果 |
| --- | --- |
| 首次认领 | 指纹预览结束不改变 Node；正式新握手核对完整公钥；加密 `claim` 进入单一持久提交点，成功回执后 Node 永久只认该 Controller。 |
| 并发认领 | 两个 Controller 都可能完成握手，但只有首个成功持久提交的 `claim` 生效；败者只获 `NODE_ALREADY_CLAIMED`，不能读原 Controller 身份。 |
| 已绑定状态读取 | 原 Controller 重新握手，固定双方公钥后加密 `status`；回报 Node 身份、高水位/摘要、已应用版本、带时间的 FRPS 观察和脱敏错误，不回传 FRP Token。 |
| 配置下发 | 新短会话中按 `begin`、有序 `chunk`、`commit` 发送完整快照；只有持久应用且启动成功才回报 `appliedRevision` 更新，验证/启动失败如实回报旧运行或回滚失败。 |
| 重复或丢失响应 | 同会话重复序号直接拒绝；响应丢失则废弃会话，新握手后读持久结果。不能靠重发同一密文猜测上次是否执行。 |

## 错误与保密边界

Noise 建立前只允许通用的 `NODE_PROTOCOL_INCOMPATIBLE`、`BAD_FRAME`、`BUSY`、`UNAVAILABLE` 等无身份细节错误；它们来自未认证 HTTP，主 Server 不据此执行安全敏感状态迁移。完成身份校验后的加密错误至少区分 `NODE_ALREADY_CLAIMED`、`NODE_IDENTITY_MISMATCH`、`NODE_REVISION_STALE`、`NODE_REVISION_CONFLICT`、`NODE_SNAPSHOT_TOO_LARGE`、`NODE_SNAPSHOT_INVALID`、`NODE_CONFIG_REJECTED`、`NODE_APPLY_FAILED`、`NODE_ROLLBACK_FAILED`。Server 面板将它们映射为[已决议的用户可见错误](./issues/08-management-api-permissions.md)，不展示私钥、FRP Token 或任意未过滤的 Node 原文。Noise 会话断开、HTTP 2xx 和 `commit` 已收到都不单独代表 FRPS 已运行；以持久应用结果和新鲜运行观察分别判断。

短会话每次操作多一次 XX 握手，但避免长期 session 的重连、nonce 与内存状态管理；Node 管理操作频率低，这一取舍已获用户确认。具体 Go 包结构由实施者决定，必须以 Noise 官方测试向量、并发认领、故障注入及最大长度测试验证实现。
