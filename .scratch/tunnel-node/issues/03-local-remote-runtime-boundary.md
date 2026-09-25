# 本地与远端 Node 在主 Server 中共享什么运行边界？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: grilling
Status: resolved

## Question

现有 `ServerRuntime` 直接持有 `ManagedFRPS`，其状态、配置、Token 和进程操作被 HTTP 与 Agent Gateway 使用。如何让默认本地 Node 继续复用现有行为，同时让远端 Node 由管理传输执行同类操作？需要决定 Node 管理对象与 FRPS 运行对象的职责、最小接口、生命周期归属，以及本地 Node 是否出现在与远端 Node 相同的持久化模型和管理视图。不要预先把对话示意的 `NodeRuntime` 接口视为已批准。

参照：[现状与接入草图](../integration-outline.md)。

## Comments

### 2026-09-23：已认领，第一轮待用户决定

代码核对：[`ServerRuntime`](../../../pkg/cmd/tunnel/server/server_runtime.go)目前组合一个本地 `FRPSupervisor`、一个[`ManagedFRPS`](../../../pkg/cmd/tunnel/server/server_frps.go)和一个 `ServerAgentGateway`，并把同一个 `ManagedFRPS` 注入 HTTP 控制、Token、404 页面、状态和 Client welcome。[`ManagedFRPS`](../../../pkg/cmd/tunnel/server/server_frps.go)持有 Server 设置、单一配置文件和本地子进程，不能直接等同于远端 Node；[`FRPSupervisor`](../../../internal/tunnelruntime/frp_supervisor.go)则已是可在 Node 进程复用的本地进程监督能力。Gateway 的 Client 准入和 welcome 目前只检查这一个本地 FRPS，Node 归属后的修正由后续[Client 切换所属 Node 票据](./05-client-node-assignment.md)决定。

第一轮询问：默认本地 Node 是否作为正式持久记录；是否先用 NodeManager 与窄运行接口包住现有 `ManagedFRPS`，而不先将其整体改造成通用引擎。两题互不依赖，等待用户选择。

### 2026-09-23：问题改写

用户表示上轮的接口问题不易理解。上轮提问撤回；依据已明确的产品形态（Server 自带 Local Node、Nodes 页同列展示、保留现有 `ManagedFRPS`）和当前代码，直接确定实现边界。用户无需判断 Go 接口名称。

## Answer

### 一个 Node 产品模型，两种执行位置

- 主 Server 自带的 FRPS 是**默认 Local Node**。它与 Remote Node 同属 Nodes 清单中的正式 Node，具有稳定的逻辑身份，Client 默认归属 Local Node。新空目录部署后，即使未认领任何远端 Node，本地 FRPS 与新建 Client/Tunnel 仍应可用；旧账号、Client 和 Tunnel 不自动继承。Node 记录的表结构与旧库拒绝策略由[“Node 与 Client 归属如何落库并升级现有部署？”](./07-persistence-migration.md)确定。
- Local Node 与 Remote Node 对上层共用 Node 身份、归属、对外 FRP 地址、期望配置与运行状态的业务视图。Local Node 不需要认领，不能按远端 Node 的 Remove 流程删除；Remote Node 必须遵守已决定的首次认领和持久绑定规则。管理地址只属于 Remote Node，不能与对外 FRP 地址混用。

### 运行职责和最小接点

- `ServerRuntime` 继续拥有账号、会话、业务控制面和 HTTP 入口，并新增 Node 管理组合层。业务配置及 Client→Node 归属由主 Server 控制面持久化；Node 管理层按 Node 身份选择运行接点、发送配置、读取观察状态，不另建一份业务数据库。
- Local Node 的运行接点直接调用现有 `ManagedFRPS`；现有本地 FRPS 的启动、配置文件、Token、404 页面及进程监督先保留。不要在接入远端 Node 之前把 `ManagedFRPS` 整体改写成通用引擎。Remote Node 的运行接点通过已决议的 Noise-over-HTTP 管理通道访问独立的 `ycy tunnel node` 进程；远端进程在自己的机器上复用底层 `FRPSupervisor`、FRPS 配置渲染和二进制获取能力，而不依赖主 Server 的 `ManagedFRPS` 对象。
- 只在上层确实共同需要的地方定义窄能力：按 Node 读取状态、提交期望配置、请求持久停用。是否需要单独的 Restart 能力、期望状态字段和结果语义，由[“主 Server 应下发什么 Node 期望状态，Node 如何收敛？”](./04-node-desired-state.md)决定。不要把现有 Server 专用的 Token 读取、404 页面读写和整个 HTTP 状态对象塞进一个巨型 `NodeRuntime` 接口。
- 现有 `/api/server/frp/*` 等单本地 FRPS 路由在新设计中指向 Local Node；新的 Node 视图与操作在后续 API/UI 票据中设计。`ServerAgentGateway` 目前以唯一 Local FRPS 作为全部 Client 的准入门槛和 welcome 来源；引入 Node 归属后应按 Client 所属 Node 选择，具体协议变更由[“Client 切换所属 Node 时控制协议如何保证 FRPC 切换？”](./05-client-node-assignment.md)解决。

### 生命周期所有权

- 主 Server 进程启动/关闭时，仍负责其 Local Node 的 FRPS 子进程；Node 管理层只管理到远端的连接与观察。主 Server 进程关闭本身**不等于**对 Remote Node 下发停用，不能把远端进程生命周期绑在 `ServerRuntime.Close()` 上。
- `ycy tunnel node` 进程自己持有管理监听、持久身份和远端 FRPS 子进程。普通进程或容器重启从持久目录恢复绑定，随后由主 Server 重新访问其管理地址；远端离线时 FRPS 是否从缓存启动、如何收敛，仍归期望状态票据决定。
- 管理 HTTP 应先可用再异步启动 Local FRPS，延续现有启动顺序；单个 Remote Node 不可达不应阻止主 Server 管理面或其他 Node 工作。Local FRPS 失败也应保留管理面，让管理员可见错误并修复。
