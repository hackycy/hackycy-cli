# Tunnel Node 领域术语

这份工作词汇表归纳已决议票据中的概念，供本地设计与实施使用。具体行为以[设计地图](./map.md)链接的票据 `## Answer` 为准。

## Language

**Tunnel Server（主 Server）**：拥有账号、Client、Tunnel 等业务配置，并管理 Node 的控制端。它同时包含一个默认的本地 Node。
_Avoid_: 把它与远端 FRPS 进程都称作 Server

**Tunnel Node（Node）**：承载一处 FRPS 数据面、接受主 Server 管理的运行单元。它不拥有业务配置的权威副本。
_Avoid_: 把 Node 叫作 Client

**Local Node（本地 Node）**：与主 Server 同进程、同部署的默认 Node，承载现有本地 FRPS。
_Avoid_: 将它视为额外安装的远端服务

**Remote Node（远端 Node）**：独立运行 `ycy tunnel node`、由主 Server 认领和管理的 Node。
_Avoid_: 将它视为自行向主 Server 注册的 Client

**Tunnel Client（Client）**：使用主 Server 的控制连接取得配置，并通过 FRPC 连接所分配 Node 的实体。
_Avoid_: Node、FRPS

**Management Address（管理地址）**：主 Server 用来访问远端 Node 管理接口的地址。
_Avoid_: FRP 地址

**Advertised FRP Address（对外 FRP 地址）**：Client 的 FRPC 用来访问所分配 Node 的 FRPS 的地址。
_Avoid_: 管理地址

**HTTP Ingress Address（HTTP 公网入口）**：访问某个 Node 的 HTTP Tunnel 时，浏览器实际到达的公网主机或 IP 与端口。
_Avoid_: 把它当作 Node 管理地址或 FRPC 连接地址

**Node Port Pool（Node 端口池）**：一个 Node 上可分配给 TCP/UDP Tunnel 的远端端口范围；不同 Node 的范围可以重叠。
_Avoid_: 把相同数字端口在所有 Node 上都视为冲突

**HTTP Hostname Ownership（HTTP 域名归属）**：一个 HTTP hostname 在当前主 Server 管理范围内只属于一个 Node；该 Node 内可以按路径细分路由。
_Avoid_: 将同一域名的不同路径拆给多个 Node

**Claim（认领）**：主 Server 主动发起、使未认领远端 Node 与该主 Server 建立管理归属的操作。
_Avoid_: Node 主动注册

**Node Identity（Node 身份）**：Node 持久持有、供主 Server 在管理连接中识别该 Node 的身份。
_Avoid_: 管理地址

**Controller Identity（控制器身份）**：主 Server 持久持有、供已认领 Node 识别其管理者的身份。
_Avoid_: Client Token

**Binding（绑定）**：Node 对一个 Controller 身份的持久归属，普通停机和重启不改变它。
_Avoid_: 在线状态、FRPS 运行状态

**Forced Forget（强制遗忘）**：主 Server 在未获 Node 停用确认时清除自己的 Node 记录的管理操作。它不表示远端 Node 已停用。
_Avoid_: 远程解绑、远程停用成功

**Desired State（期望状态）**：主 Server 持有并发送给运行单元的目标配置；运行单元报告实际应用结果。
_Avoid_: 把观察到的运行状态视为配置权威

**Applied Revision（已应用版本）**：Node 最后一次持久应用成功的配置版本；它不表示 FRPS 此刻一定在线。
_Avoid_: 用版本一致代替进程可用性检查

**Client Node Assignment（Client 归属）**：主 Server 已提交的 Client 目标 Node；与 Client 当前 FRPC 是否连上该 Node 分开记录。
_Avoid_: 把进程启动或一次连接失败当作自动改回旧 Node 的理由

**Pending Client Switch（待切换）**：Client 控制连接暂时离线时保存的目标 Node；执行前，当前归属仍是旧 Node。
_Avoid_: 将待切换显示成已经完成的归属变更
