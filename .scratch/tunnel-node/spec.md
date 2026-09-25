# Tunnel Node 接入设计交接

状态：全部设计决策与关键技术契约已完成，可进入实现。[设计地图](./map.md)及其已关闭票据的 `## Answer` 是权威来源；本页整合产品形态并给实施者导航，不替代票据细节。

## 目标形态

```text
官方 Client ──控制 WebSocket──> 主 Server（Controller + Local Node）
     │                              │
     │                              └── Noise/HTTP 管理──> Remote Node
     └──── FRPC 数据连接 ────────> 所属 Node 上的 FRPS
```

一个 Client 只归属一个 Node。Local Node 是默认归属；Remote Node 由主 Server 通过公网 HTTP 地址主动认领并管理。Node 只有 `ycy tunnel node` 启动命令，重启必须沿用持久身份和最后成功配置。Server 是业务配置权威，Node 不保存账号、Client 或 Tunnel 业务库。

## 启动与控制流程

1. 首次启动 `ycy tunnel node` 时，Node 生成并保存长期身份，开放公网可直达的 HTTP 管理监听，打印管理地址和公钥指纹，进入未认领状态；启动时无需主 Server 地址或注册 Token，未认领时不启动 FRPS。
2. 管理员在主 Server 的 Nodes 页面输入管理地址和显示名。Server 主动与 Node 建立 Noise XX 会话，展示 Node 指纹供与本机输出核对；确认后发送加密认领。Node 原子固定首个有效 Controller 身份，后续只接受该身份。认领成功与 FRPS 配置成功分开呈现。
3. 主 Server 持有账号、Client、Tunnel、Node 归属、期望配置和每 Node 的 FRP 凭据。Local Node 的 FRPS 继续由本机 `ManagedFRPS` 管理；Remote Node 保存自己的身份、绑定、最高版本、最后成功的 FRPS 配置和运行缓存，并报告观察状态。普通进程或容器重启复用 Node 持久目录，恢复原绑定和最后成功配置。
4. 官方 Client 的控制连接始终通向主 Server。Server 按 Client 当前归属下发完整 Node 身份、对外 FRP 地址、Token 和 Tunnel 快照；Client 的 FRPC 只连接所属 Node 的 FRPS，永远不使用 Node 管理地址。管理地址、对外 FRP 地址、HTTP 公网入口分别配置。

这个流程不包含配对码、mTLS、Node 主动注册、远程改绑或 Node reset 子命令。首次有效认领者可能抢先占有公网 Node；[认领决议](./issues/01-claim-trust.md)明确接受并限定了这一风险。

## 需要按票据实现的边界

| 领域 | 决议入口 |
| --- | --- |
| 首次认领、指纹核对、Noise XX、首认领风险 | [如何让主 Server 通过首次连接安全地认领零预配 Node？](./issues/01-claim-trust.md) |
| 固定绑定、正常移除、强制遗忘和本机清理边界 | [Node 绑定、移除和恢复的状态语义是什么？](./issues/02-binding-lifecycle.md) |
| Local/Remote 运行职责及代码接点 | [本地与远端 Node 在主 Server 中共享什么运行边界？](./issues/03-local-remote-runtime-boundary.md) |
| 完整期望状态、版本、离线运行、验证和回滚 | [主 Server 应下发什么 Node 期望状态，Node 如何收敛？](./issues/04-node-desired-state.md) |
| Client 当前/待切换归属、完整控制快照及 FRPC 状态 | [Client 切换所属 Node 时控制协议如何保证 FRPC 切换？](./issues/05-client-node-assignment.md) |
| Node 级端口池、HTTP 域名、三个外部地址和 FRP Token | [多 Node 下端口、HTTP 路由与 FRP 凭据按什么范围归属？](./issues/06-node-resource-scope.md) |
| 新库模型、事务约束和旧 SQLite 拒绝 | [Node 与 Client 归属如何落库并升级现有部署？](./issues/07-persistence-migration.md) |
| 管理权限、公开摘要、远端 API 和稳定错误 | [主 Server 的 Node 管理 API 和权限边界是什么？](./issues/08-management-api-permissions.md) |
| 页面流程与已接受的[可点击草模](./nodes-flow-prototype.html) | [Nodes 页面怎样让认领、分配和故障状态可理解？](./issues/09-nodes-workflow-prototype.md) |
| Web API、状态投影与错误返回 | [Node 管理 API 与页面状态契约如何具体定义？](./issues/15-node-management-api-contract.md) |
| 具体实施顺序、破坏性发布边界及验收矩阵 | [接入顺序和跨版本验收边界如何确定？](./issues/10-rollout-compatibility.md) |
| Node 启动参数、卷/端口映射与端到端故障验收 | [部署参数与端到端验收如何冻结？](./issues/16-deployment-acceptance.md) |

## 实施入口

[Noise/HTTP 消息契约](./issues/11-node-management-wire.md)、[Node 崩溃恢复](./issues/12-node-state-crash-consistency.md)、[Server 数据事务](./issues/13-server-node-storage.md)、[Client 新协议与切换时序](./issues/14-client-wire-switch.md)、[管理 API 与页面状态](./issues/15-node-management-api-contract.md)及[部署与验收](./issues/16-deployment-acceptance.md)均已定稿。按[接入顺序决议](./issues/10-rollout-compatibility.md)实施；现状接点见[代码索引](./integration-outline.md)，术语见[领域术语](./CONTEXT.md)。

这是**破坏性发布**：只支持同一新发行版的 Server、Node、Client；v4 Client 无兼容通道。新版 Server 只接受空状态目录创建新 schema，旧 SQLite 文件原样拒绝，不做自动迁移或备份。旧账号、凭据、Client、Tunnel 均需在新部署中重新建立。不同管理协议的 Node 不可作为新分配目标；已运行 Node 可能继续使用旧缓存 FRPS，版本错误不得被显示成已停机。

## 交付门槛

实现必须通过[发布验收矩阵](./issues/10-rollout-compatibility.md#发布验收矩阵)和[细化的故障注入验收](./deployment-acceptance.md#故障注入和验收矩阵)，尤其是 Local HTTP/TCP/UDP 回归、首次认领与重启持久性、离线和失败配置的收敛、Client 在线/离线切换、资源冲突、正常移除/强制遗忘、旧库与错版拒绝。部署文档须写明新空目录、持久 Node 卷、公开端口和预期中断。此页与其他 `.scratch` 文件是设计文档，尚无生产代码或测试变更。
