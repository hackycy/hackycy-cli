# 多 Node 下端口、HTTP 路由与 FRP 凭据按什么范围归属？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: grilling
Status: resolved
Blocked by: 03, 05

## Question

当前 TCP/UDP 端口分配与 HTTP hostname/location 唯一性是全局的，现有本地 FRPS 使用一个内部 Token。引入多个 FRPS 后，端口池、端口冲突和 HTTP 路由应按 Node 隔离还是保持全局约束？同一域名跨 Node 是否可复用，UI 应如何提示 DNS 必须指向目标 Node？前置票据已决定远端 FRP Token 按 Node 独立、Client 正常切换不轮换旧 Node Token；本票据需明确凭据轮换时相关 Client 的资源与可见影响，而不再重问独立/共享。回答还需覆盖管理地址与对外 FRP 地址不同的部署情形，并给出资源唯一性的不变量。

参照：[数据库约束](../../../pkg/cmd/tunnel/server/database.go)与[端口分配](../../../pkg/cmd/tunnel/server/server_tunnels.go)。

## Comments

### 2026-09-23：已认领，第一轮待用户决定

代码核对：当前 SQLite schema v2 无 Node 字段。[`tunnels_unique_transport_port`](../../../pkg/cmd/tunnel/server/database.go)按 `(protocol, server_port)` 全局唯一，因此 TCP/UDP 可以使用相同数字端口；[`tunnel_http_routes`](../../../pkg/cmd/tunnel/server/database.go)按不区分大小写的 `(hostname, location)` 全局唯一。即使 Tunnel 暂时 disabled，资源仍保留。现有[`availablePort`](../../../pkg/cmd/tunnel/server/server_tunnels.go)从唯一的 Server 端口池按同协议顺序找首个空闲端口。域名会规范化为小写 IDNA，现有流程不检查 DNS。[`ManagedFRPS`](../../../pkg/cmd/tunnel/server/server_frps.go)把唯一的本地 Token 写入 FRPS 并交给 Client，公开 FRP 地址只用于 Client 连接，不是管理地址。

前置决议已把远端 FRP Token 定为每 Node 独立，且 Client 正常切换不轮换旧 Node Token。第一轮只询问三项产品边界：不同 Node 是否可复用 TCP/UDP 数字端口并各有端口池；同一 HTTP hostname 是否只能归一个 Node；DNS 未配置时是否可先保存 HTTP 路由。等待用户选择。

### 2026-09-23：用户回答

- HK 与 LA 等不同 Node 各自设置端口池，可复用相同的 TCP/UDP 数字端口；同一 Node 内仍禁止冲突。
- 同一 HTTP 域名整体只归一个 Node；同 Node 内可保留按不同路径分配的现有能力。
- DNS 尚未指向目标 Node 时允许先保存 HTTP 路由，面板要提示正确的解析目标。

## Answer

### 资源命名空间

| 资源 | 唯一性与归属 |
| --- | --- |
| TCP/UDP 远端端口 | 按 `(nodeId, protocol, serverPort)` 唯一。HK Node 与 LA Node 可各用 TCP 20001；同一 Node 的 TCP 20001 与 UDP 20001 也可并存，延续现有按协议区分的行为。 |
| 端口池 | 每个 Node 有自己的允许范围，自动分配只扫描该 Node、该协议的占用端口；不同 Node 的范围可重叠。Local Node 初始范围继续来自现有 Server 设置，Remote Node 范围由主 Server 的该 Node 期望配置管理。 |
| HTTP hostname | 一个规范化 hostname 在整个主 Server 管理范围内**只能归一个 Node**。不能把 `app.example.com/` 放 HK、`app.example.com/api` 放 LA，因为 DNS 不能按路径选择 FRPS。同一 Node 内仍可有不同 `location`，但 `(hostname, location)` 不能重复。 |
| FRP Token | 每个 Node 一份，由主 Server 持有并发送给该 Node 与其已分配 Client；正常 Client 切换不轮换旧 Node Token。它是 FRPS 实例级凭据，不是 Client 独有的撤销凭据。 |

- 禁用的 Tunnel 继续**占用**其端口或 HTTP hostname/location，延续现有行为；只有删除或显式修改资源才释放。HTTP hostname 不区分大小写，继续使用现有 IDNA 规范化；路径保留现有规范化与匹配语义。资源约束应在持久化事务内检查，不能只依赖前端预检，避免并发创建冲突。
- 每个 Node 的端口池不能包含其 FRPS bind、HTTP vhost 或 Node 管理监听等本机保留端口；缩小端口池时若任何现有 Tunnel（包括 disabled）占用范围外端口，应拒绝修改并列出冲突，不能自动改端口。不同 Node 恰好运行在同一公网主机时，本机监听端口冲突仍由配置校验与 FRPS 启动结果报告；“Node 级资源可复用”不保证两个进程能绑定同一个 IP:端口。
- Client 切换所属 Node 前，须在目标 Node 上验证其全部 TCP/UDP 端口落在目标端口池且没有同协议占用；其每个 HTTP hostname 要么尚未被占用，要么已经归目标 Node，不能因同 hostname 的其他路径仍留在源 Node 而把域名拆开。任一冲突即拒绝整次切换，旧归属不变并给出具体资源；离线 Client 的待切换在实际执行时重新检查。只有原 Node 上该 hostname 的**所有**路由一起迁走，域名所有权才可在同一事务中转给目标 Node。新建/修改 Tunnel 也遵守同一套规则。

### 三类地址与 DNS 提示

- **Management Address** 是主 Server 到 Remote Node 的管理 HTTP 地址，例如 `http://203.0.113.10:7600`；它只用于认领和管理，不发给 Client，也不是 HTTP Tunnel 的域名解析目标。
- **Advertised FRP Address** 是 Client FRPC 连接该 Node FRPS 的公网 host:port，例如 `hk.example.com:7000`。它可与 FRPS 本机 bind 地址/端口不同，以支持端口映射；主 Server 在 Client 归属和 `welcome/desired_state` 中使用它。两个活动 Node 不应配置相同的规范化 Advertised FRP host:port，否则同一连接目标无法明确指向某个 Node；DNS 别名解析到同一实际地址等无法可靠静态判断的情况，仅作提示并以实际连接状态为准。
- **HTTP Ingress Address** 是浏览器访问该 Node HTTP vhost 的公网目标及端口，独立于管理地址和 FRPC 的 FRP 地址。它可默认取该 Node 的公网主机与 HTTP vhost 端口，但要允许显式配置公网映射后的地址/端口。创建 HTTP Tunnel 前，Node 记录应有足够的公网 HTTP 入口信息，供面板展示 `http://<custom-domain>[:port]` 及 DNS 应指向的 IP/主机；DNS 记录本身不携带端口，公网 HTTP 端口不是 80 时必须把端口展示在访问 URL 中。此设计沿用当前 FRPS 的 HTTP vhost 能力，不凭空承诺 HTTPS vhost。
- DNS 尚未就绪**不阻止保存** HTTP 路由。面板展示该域名应指向的 Node 公网 HTTP 入口、当前检查结果（若能检查）及“解析/外部端口未就绪时不可访问”的提示。DNS 检查只能辅助诊断，不能把解析结果当作资源归属的权威，也不能在创建时强制要求公网 DNS 已生效。变更 Node 的公网 HTTP 入口后，应提示管理员更新分配在此 Node 上的域名 DNS，并保持原路由配置直至明确修改。

### 凭据变化与现有部署

- Local Node 延续单一 FRP Token 与 `YCY_TUNNEL_FRP_TOKEN` 的配置来源；Remote Node 的 Token 在主 Server 按 Node 独立生成、持久化。不能把 Local Node 的 Token 自动复制给新认领的远端 Node。新空库中的 Client 默认归 Local Node；旧数据库不迁移，具体约束和拒绝策略见[“Node 与 Client 归属如何落库并升级现有部署？”](./07-persistence-migration.md)。
- 单个 Node 的 Token 轮换需要先让该 Node 应用并确认新 FRPS 配置，再给它的**所有已分配 Client**递增完整配置版本并下发新 Token。该 Node 上的 Client 可能短暂断线并重连，离线 Client 要在重连主 Server 后获取新 Token；其他 Node 的 Token 和 Client 不受影响。Node 应用新配置失败时不能提前向 Client 发布新 Token。正常 Client 切换不触发 Token 轮换；已离开旧 Node 的 Client 可能仍知道旧 Token，这一已接受的限制见[“Client 切换所属 Node 时控制协议如何保证 FRPC 切换？”](./05-client-node-assignment.md)。
