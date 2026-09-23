# 如何让主 Server 通过首次连接安全地认领零预配 Node？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: grilling
Status: resolved

## Question

`ycy tunnel node` 首次启动不预配主 Server 地址或长期 Token。Node 管理地址需支持公网 IP:端口直连和普通 HTTP；主 Server 主动连接，Node 自动信任第一个完成有效认领的连接者，此后不再开放新认领。不要使用配对码、mTLS 或依赖 HTTPS 证书。在此约束下，如何固定双方长期身份，以及如何在 HTTP 上传输管理配置与凭据时保护机密性、完整性和重放安全？需把首连抢占风险明确列为限制，并规定并发认领、超时重试、原子落盘与错误恢复的语义。

本票据只确定认领和后续管理认证的安全契约，不规定具体代码包或 HTTP 路由命名。

## Comments

### 2026-09-23：首轮待用户决定

已核对现有代码：Tunnel Server 使用普通 HTTP 监听；现有 Bearer Token 用于 Client 控制连接；当前没有 Node 的长期身份、一次性配对码或双向认证实现。可参考 [`server_listener.go`](../../../pkg/cmd/tunnel/server/server_listener.go)、[`server_http.go`](../../../pkg/cmd/tunnel/server/server_http.go)、[`server_clients.go`](../../../pkg/cmd/tunnel/server/server_clients.go)。

同轮提出三项互不依赖的问题：首版管理地址支持的网络范围；操作员认领时能否核对 Node 指纹；绑定后使用双向 TLS 还是 HTTPS 加请求签名。建议私网/VPN 直达、配对码加指纹、双向 TLS，等待用户确认或修正。选择双向 TLS 的理由是可使用现有 TLS 身份验证和传输保护能力；[TLS 1.3 规范](https://www.rfc-editor.org/info/rfc8446/)与 [Go `crypto/tls` 文档](https://pkg.go.dev/crypto/tls)是后续协议细化的依据。若使用自签名证书，必须明确定义证书固定或等价的验证方式，不能把跳过默认证书验证本身当作身份验证。

### 2026-09-23：用户首轮回答

- 管理地址需要公网可直达；用户举出公网 HTTP/HTTPS，理由是 Node 上的 FRPS 本身需要公网使用。此处尚需明确是否真的要求 **明文 HTTP 管理流量**，因为下述 mTLS 选择要求 TLS。
- 认领时同时核对配对码和 Node 身份指纹。
- 绑定后采用 mTLS 并固定双方证书身份，避免自定义请求签名与重放处理。

接下来澄清公网管理入口的传输方式与配对码有效期；票据尚未决议。

### 2026-09-23：第二轮待用户决定

- 公网管理入口是否仅接受 HTTPS？建议仅 HTTPS；明文 HTTP 与已选 mTLS 不兼容。
- 首版是否需要反向代理？建议主 Server 直接访问 Node HTTPS；如有代理要求，优先使用 TCP/TLS 透传以保留双方证书验证。
- 一次性配对码如何失效？建议 10 分钟过期、成功即失效、连续 5 次错误后要求在 Node 本机重新生成。

### 2026-09-23：用户第二轮纠正（覆盖上述候选）

- 不使用 mTLS；公网 Node 管理地址必须支持 IP:端口直连的普通 HTTP，不要求 HTTPS 证书。
- Server 与 Node 间的安全连接需考虑 HTTP 之上的其他方案，不能只依赖 HTTPS。
- 不使用配对码；主 Server 第一次主动连接后 Node 建立信任，并从此关闭新的认领入口。

上文关于 mTLS、HTTPS-only、配对码的建议已被否决，保留在 Comments 中作为讨论历史，不作为当前设计。先前“指纹核对”回答仍需重新确认其具体形式：是否只由主 Server 面板核对 Node 指纹，还是也要求 Node 本机批准 Controller 指纹。若首次到达者无任何 Node 本机或预共享授权，公网其他连接者可以先于真正的主 Server 建立信任；该信任边界必须由用户明确选择。

### 2026-09-23：首次信任边界与协议研究

用户选择 Node 自动信任第一个完成认领的连接者，不要求 Node 本机批准。此选择意味着第一次认领可能被公网其他连接者抢占；即便后续密钥固定，也不能追认首次连接者的真实身份。[SSH 协议架构 §4.1](https://datatracker.ietf.org/doc/html/rfc4251#section-4.1)对首次公钥未经独立核验的主动中间人风险有明确说明。

候选方案是在普通 HTTP 上承载 [Noise 协议](https://noiseprotocol.org/noise.html)的 XX 握手与 AEAD 加密消息；双方生成长期静态密钥，首次有效 `claim` 后 Node 原子持久化 Controller 公钥，后续只接受该身份。每个管理请求/响应均进入加密封装，并定义会话 ID、双向递增序号与重复请求拒绝规则；普通 HTTP 不直接暴露凭据或期望配置。这个方案只提供首连之后的机密性和身份连续性，不解决被人先认领的风险。

待最终确认：Server 面板是否仍需核对 Node 本机显示的身份指纹，以及是否接受上述应用层安全通道作为本票据的设计方向。

### 2026-09-23：用户最终确认

- 主 Server 面板保留 Node 指纹核对。
- 使用 Noise XX 作为普通 HTTP 上的应用层安全会话。

## Answer

### 首次连接与身份

1. `ycy tunnel node` 首次启动生成并持久保存 Node 的长期静态密钥，打印公网可直达的 HTTP 管理地址和 Node 公钥指纹；不显示配对码，也不预设主 Server 地址。
2. 主 Server 持久保存自己的长期静态密钥。管理员在 Add Node 输入 HTTP 地址；主 Server 主动与 Node 运行 `Noise_XX_25519_ChaChaPoly_SHA256` 握手，得到 Node 静态公钥，在面板展示指纹。管理员将其与 Node 本机输出核对，确认后才发送加密的 `claim`。若指纹不符，主 Server 终止，绝不发送管理凭据。
3. Node 不做本机人工批准，也不使用配对码。它自动接受**第一个完整、有效、经 Noise 会话提交的认领**，将该主 Server 的静态公钥作为 Controller 身份原子持久化。单纯 TCP/HTTP 连接或只完成握手不占用认领权。并发认领在持久化边界串行化，只有一方成功。
4. 认领成功后，Node 不再向其他公钥开放认领；陌生 Controller 仅得到不含原 Controller 细节的通用已认领错误。相同 Controller 公钥可以重新建立 Noise 会话并查询认领结果，以处理 Node 已落盘但主 Server 未收到确认的超时或崩溃。主 Server 也固定 Node 公钥，后续握手双方都必须验证已保存的对方公钥。绑定清除、密钥丢失与移除语义由[“Node 绑定、移除和恢复的状态语义是什么？”](./02-binding-lifecycle.md)继续决定。

### HTTP 上的安全通道

- 公网 `http://IP:port` 是正式支持的管理地址，不依赖 HTTPS 证书或 mTLS；如环境已有 HTTPS，也只是外层可选传输，身份仍以固定的 Noise 静态公钥为准。
- Noise XX 负责握手、双方密钥持有证明和会话密钥派生。所有具有管理含义的请求及响应，包括认领、期望配置、FRP 凭据和状态细节，必须放在 Noise transport 的认证加密封装内；普通 HTTP 只承载握手/密文，不能直接发送秘密或接受明文管理操作。实现须使用经审查的 Noise 实现，不能自写握手算法。
- 跨 HTTP 请求须定义会话 ID、双向递增序号和严格的重复消息拒绝；把协议版本、Node 身份、操作名及请求内容纳入加密封装，避免跨会话或跨操作重放。每次重连重新握手。对超时但可能已执行的操作，调用方查询已固定身份下的状态或使用有界的幂等语义，而非盲目重放密文。
- 固定公钥用于验证长期设备身份；网络地址变化不改变身份。HTTP 的 URL、网络元数据与流量时序不获得保密，管理消息内容获得会话级保护。实现细节以 [Noise 协议规范](https://noiseprotocol.org/noise.html)为依据。

### 明确接受的限制

Node 首次认领前没有任何预共享秘密、预置 Controller 公钥或本机批准，因此**不能证明第一个认领者确实是预期的主 Server**。公网攻击者可抢先完成有效认领并锁住 Node。主 Server 核对 Node 指纹能发现冒名 Node，但不能授权自己在 Node 上获得首认领权；未核对指纹时还会暴露于首次连接的冒名/中间人攻击。该抢先认领风险是用户选择自动首次信任的直接结果，不能描述成 Noise 已解决；首次公钥未经独立认证的限制见 [SSH 协议架构 §4.1](https://datatracker.ietf.org/doc/html/rfc4251#section-4.1)。
