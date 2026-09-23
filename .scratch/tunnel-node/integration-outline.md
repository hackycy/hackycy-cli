# Tunnel Node 代码现状索引

本文件记录当前仓库的代码接点，供实施时定位改动。产品形态见[实现交接入口](./spec.md)；最终决议以[设计地图](./map.md)链接的票据 `## Answer` 为准。

## 已决定的产品边界

```text
Tunnel Client --控制连接--> Tunnel Server --管理/认领--> Remote Node
      |                         |
      +-- FRPC 数据连接 ------> FRPS（被分配的 Node）
                                |
                             Local Node
                             （Server 自带 FRPS）
```

- 远端 Node 首次运行只需 `ycy tunnel node`，不输入主 Server 地址或长期注册令牌。主 Server 通过公网可直达的 HTTP 管理地址主动发起首次连接，在面板核对 Node 指纹；Node 自动固定首个有效认领者，管理内容由 Noise XX 会话保护。首次抢占风险和协议边界见[认领决议](./issues/01-claim-trust.md)。
- 一个 Client 归属一个 Node；Client 始终从主 Server 获得控制面配置。管理地址与对外 FRP 地址是两个不同概念。
- 主 Server 是业务配置的权威来源；远端 Node 只保留自身身份、绑定和运行所需的配置缓存。
- Node CLI 只有 `ycy tunnel node` 启动服务。普通停机或容器重启复用持久状态目录，继续原绑定；没有产品层面的 Node 改绑操作。Remove、强制遗忘及手工清理后作为新 Node 认领的边界见[生命周期决议](./issues/02-binding-lifecycle.md)。

## 已核对的代码接点

| 接点 | 当前情况 | 接入时需处理的差异 |
| --- | --- | --- |
| [`pkg/cmd/tunnel/tunnel.go`](../../pkg/cmd/tunnel/tunnel.go) | `tunnel` 下只注册 `server` 与 `connect` | 增加独立 `node` 命令及本地状态目录 |
| [`pkg/cmd/tunnel/server/server_runtime.go`](../../pkg/cmd/tunnel/server/server_runtime.go) | `ServerRuntime` 直接持有一个 `ManagedFRPS`，并把它注入 Agent Gateway 和 HTTP Handler | 保留 Local 的 `ManagedFRPS`，在上层增加按 Node 选择运行接点的管理层；具体接口在实现时确定 |
| [`pkg/cmd/tunnel/server/server_listener.go`](../../pkg/cmd/tunnel/server/server_listener.go) 与 [`pkg/cmd/tunnel/server/server_http.go`](../../pkg/cmd/tunnel/server/server_http.go) | 现有 Server 管理入口由 `http.Server.Serve` 提供 HTTP；浏览器会话和 Client Bearer Token 各有自己的认证路径 | 远端 Node 的 Noise 管理通道需单独接入，不能直接复用现有 Client Token |
| [`pkg/cmd/tunnel/server/server_frps.go`](../../pkg/cmd/tunnel/server/server_frps.go) | `ManagedFRPS` 使用 Server settings、单个内部 FRP Token，维护本地 FRPS 进程 | Local 继续使用它；Remote 复用底层 supervisor/renderer 并采用独立 Node Token 与验证后应用流程 |
| [`pkg/cmd/tunnel/server/server_agent_protocol.go`](../../pkg/cmd/tunnel/server/server_agent_protocol.go) 与 [`internal/tunnelruntime/protocol.go`](../../internal/tunnelruntime/protocol.go) | `welcome` 含 FRP 地址和 Token；后续 `desired-state` 只含 Tunnel 快照和重启代数；`welcome` 还以本地 FRPS running 作为准入条件 | 升级单一 Client 协议，让两类消息都携带所属 Node 的完整配置，并按 Node 报告应用与登录状态 |
| [`pkg/cmd/tunnel/server/database.go`](../../pkg/cmd/tunnel/server/database.go) 与 [`pkg/cmd/tunnel/server/server_tunnels.go`](../../pkg/cmd/tunnel/server/server_tunnels.go) | SQLite schema v2；Client 无 Node 外键；TCP/UDP 端口与 HTTP 路由按全局唯一处理，端口池来自 Server settings | 在新空库建立 Node 归属和资源约束，旧 schema 原样拒绝 |
| [`pkg/cmd/tunnel/connect/client_reconciler.go`](../../pkg/cmd/tunnel/connect/client_reconciler.go) | Client 已具备按 revision 应用 FRPC 配置、验证与回滚的流程 | 跨 Node 切换停旧 FRPC 后不自动回旧 Node；同 Node 配置失败仍可恢复旧配置 |
| [`pkg/cmd/tunnel/server/server_http.go`](../../pkg/cmd/tunnel/server/server_http.go) 与 [`web/tunnel-server/app.tsx`](../../web/tunnel-server/app.tsx) | 后台已有 Server、Client、Account 管理路径；没有 Nodes 页面 | 增加所有登录用户可见的 Nodes 摘要及 Client 选择，Node 管理操作限管理员 |

## 实施与验收入口

实施按[接入顺序决议](./issues/10-rollout-compatibility.md)分阶段推进。该票据的发布验收矩阵覆盖新空库与 Local 回归、认领与重启、Node 离线和回滚、Client 切换、资源约束、移除、版本拒绝及权限。现有 Local HTTP/TCP/UDP 集成测试可作为新版本回归基础，Remote Node 的端到端场景需在实施时补齐。
