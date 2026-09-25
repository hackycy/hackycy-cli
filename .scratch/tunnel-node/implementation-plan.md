# Tunnel Node Implementation Plan

## Source Decisions

- `.scratch/tunnel-node/map.md` 与 `.scratch/tunnel-node/spec.md`：设计权威入口、目标形态和交付边界；各票据只以 `## Answer` 为最终决定。
- `.scratch/tunnel-node/issues/01-claim-trust.md`、`.scratch/tunnel-node/issues/02-binding-lifecycle.md`、`.scratch/tunnel-node/issues/11-node-management-wire.md`：首次认领、固定身份、Noise/HTTP、重放和移除语义；细节见 `.scratch/tunnel-node/node-management-wire.md`。
- `.scratch/tunnel-node/issues/03-local-remote-runtime-boundary.md`、`.scratch/tunnel-node/issues/04-node-desired-state.md`、`.scratch/tunnel-node/issues/12-node-state-crash-consistency.md`：Local/Remote 运行接点、完整期望快照及崩溃恢复；细节见 `.scratch/tunnel-node/node-state-crash.md`。
- `.scratch/tunnel-node/issues/05-client-node-assignment.md`、`.scratch/tunnel-node/issues/14-client-wire-switch.md`：当前与待切换归属、v5 完整 Client 运行配置和真实 FRPC 状态；细节见 `.scratch/tunnel-node/client-wire-switch.md`。
- `.scratch/tunnel-node/issues/06-node-resource-scope.md`、`.scratch/tunnel-node/issues/07-persistence-migration.md`、`.scratch/tunnel-node/issues/13-server-node-storage.md`：Node 资源范围、新库、事务与 Token 轮换；细节见 `.scratch/tunnel-node/server-storage-transactions.md`。
- `.scratch/tunnel-node/issues/08-management-api-permissions.md`、`.scratch/tunnel-node/issues/09-nodes-workflow-prototype.md`、`.scratch/tunnel-node/issues/15-node-management-api-contract.md`：权限、页面和 API 状态投影；细节见 `.scratch/tunnel-node/node-api-contract.md`。
- `.scratch/tunnel-node/issues/10-rollout-compatibility.md`、`.scratch/tunnel-node/issues/16-deployment-acceptance.md`：线性接入顺序、破坏性发布及验收矩阵；细节见 `.scratch/tunnel-node/deployment-acceptance.md`。
- `.scratch/tunnel-node/CONTEXT.md` 与 `.scratch/tunnel-node/integration-outline.md`：术语和现有代码入口，不增加产品决定。

## Outcome

同一新发行版的主 Server、Remote Node 和官方 Client 可在空状态目录中协同运行：Server 保留可独立工作的 Local Node，主动认领并管理远端 Node；用户把自己的 Client 指向任一 Node，Client 控制面仍连 Server，FRPC 数据面连所属 Node。管理、FRPS、归属、应用和真实连接状态可分别观察；部署文档与发布验收证据覆盖故障及破坏性切换。

## Non-Negotiable Rules

- Server 是账号、Client、Tunnel、Node 归属、期望配置和资源分配的唯一业务权威；Remote Node 只持久保存自身身份、固定 Controller 绑定与运行所需状态。
- Local Node 永远存在且继续由 Server 内的 `ManagedFRPS` 运行；Remote Node 是独立进程。Client 控制连接始终通向主 Server，一个 Client 同时只有一个当前 Node。
- 只支持同一精确发行版的单一新协议及 FRP 构建。旧 v4 Client、旧/未知 SQLite 库和错版管理协议明确拒绝；新 Server 只从空状态目录建库，不迁移、备份或改写旧库。
- Node 的公网 HTTP 管理面只承载匿名最小健康信息与 Noise XX 保护的管理消息；Server 核对 Node 指纹，Node 固定首个有效认领者。没有 mTLS、配对码、产品内 reset/rebind 或远程恢复旧 Controller。
- 管理失联不能推断远端 FRPS 已停止；Remote Node 保留最后成功配置。新配置失败按 Node 契约回滚并报告真实结果；已提交跨 Node 归属后 Client 不自动回旧 Node。
- 保存的 Node 端口池立即约束 Server 分配；远端应用状态单独观察。同数值端口可在不同 Node 复用，同一 hostname 只能归一个 Node。
- 普通登录用户可查看并选择全部 Node，只能管理自己的 Client；所有 admin 可管理 Node。公共投影不泄露管理地址、指纹、Token 或详细诊断。

## Gate Overview

| Gate | Name | Unlock condition | Outcome |
| --- | --- | --- | --- |
| G0 | 新库与 Local 基础模型 | 开始 | 空目录建新库并保留 Local 运行，旧库原样拒绝。 |
| G1 | 单一 v5 Client 控制协议 | G0 Exit 全部满足 | Local Client 使用完整配置并保持 HTTP/TCP/UDP 回归。 |
| G2 | Node 身份与管理链路 | G1 Exit 全部满足 | 独立 Node 可持久启动、被安全认领并固定 Controller。 |
| G3 | Node FRPS 收敛与崩溃恢复 | G2 Exit 全部满足 | Node 可靠应用、回滚、离线恢复与持久停用快照。 |
| G4 | Server 远端 Node 管理 | G3 Exit 全部满足 | Server 管理远端 Node，并通过 API 和页面准确展示状态。 |
| G5 | Node 资源与 Client 归属 | G4 Exit 全部满足 | 事务约束、用户选 Node、在线与离线归属决策可用。 |
| G6 | 官方 Client 跨 Node 连接 | G5 Exit 全部满足 | FRPC 切换、真实状态和 Token 轮换贯通。 |
| G7 | 移除、部署与发布验收 | G6 Exit 全部满足 | 移除语义、运维文档及端到端发布证据齐备。 |

## G0: 新库与 Local 基础模型

### Purpose

建立唯一的新数据与 Local 运行基础，避免后续远端接入建立在旧库隐式迁移之上。

### Inputs

- `.scratch/tunnel-node/issues/03-local-remote-runtime-boundary.md`、`07-persistence-migration.md`、`10-rollout-compatibility.md`、`13-server-node-storage.md` 和 `.scratch/tunnel-node/server-storage-transactions.md`。
- `pkg/cmd/tunnel/server/database.go`、`server_runtime.go`、`server_frps.go`、`server_tunnels.go`，`pkg/cmd/tunnel/server/client_forwarding_integration_test.go`。

### Objective

新空目录生成带不可删除 Local Node 的 schema v3、独立且可交叉核对的 Controller 身份，以及 Node/Client/资源的基础持久约束；Local FRPS 仍按启动设置工作。

### Scope boundary

只修改 Server 存储、Local Node 模型和相关读写接点。远端 Node 进程、管理网络、用户选 Node 和跨 Node 连接留给后续 Gate。

### Constraints

- 检查旧库、未知库和身份文件一致性必须先于任何 schema 写入；拒绝时数据库及 WAL/SHM 原字节不变。
- Local Node 不可删除，默认 Client 归 Local；Controller 私钥与 SQLite 分开保存于同一私有状态目录，不得静默补发丢失身份。
- 端口池保存即为 Server 分配依据；建模时预留每 Node TCP/UDP 和整域 hostname 约束，不以全局端口唯一代替。

### Slice policy

按启动前检查、身份与 schema 事务、Local 投影、资源约束四个独立调用簇切片；每片保持可在新空目录复现，完成一片再触及下一片。

### Verification

#### Directed

- 每片运行相应存储/启动单测；用临时空目录、旧 v1/v2/未知库及 WAL/SHM 夹具比较前后字节，验证拒绝和重复启动。
- 在本 Gate 收尾运行真实 Local FRPS 集成场景，观察 HTTP/TCP/UDP 请求往返及启动参数所给 Token/端口。

#### Repository

1. `go test ./pkg/cmd/tunnel/server/... ./internal/tunnelruntime/...`（每个存储或运行切片后）。
2. `make check-terminal`（Gate 收尾）。

#### Manual acceptance

- 无。

### Evidence rule

G0-E1 用空目录与旧库字节对比测试及启动日志证明；G0-E2 用表约束/并发事务测试证明；G0-E3 用 Local 转发集成输出和 `make check-terminal` 证明。记录实际测试名与结果。

### Stop conditions

- 无法在不写入旧库的情况下识别旧/未知 schema 或身份文件冲突。
- 新约束与已定的 Local 行为或 Node 资源决议冲突，且无法同时满足。

### Rollback

以新空目录 schema/身份初始化为唯一切口撤销本 Gate 修改；不对旧目录运行回写式回退，也不删除用户既有目录。

### Exit conditions

- G0-E1：空目录建 v3 并重复启动幂等；旧 v1/v2/未知库及身份不匹配明确拒绝，拒绝前后原文件字节一致。
- G0-E2：数据库存在不可删除 Local Node、Client 默认归属及每 Node 资源约束；冲突写入完整回滚。
- G0-E3：仅 Local 的 Server 仍按启动设置运行 FRPS，既有 HTTP/TCP/UDP 集成往返成功。

## G1: 单一 v5 Client 控制协议

### Purpose

让新 Client 控制契约在 Local 场景完整可用，再引入远端归属。

### Inputs

- `.scratch/tunnel-node/issues/05-client-node-assignment.md`、`10-rollout-compatibility.md`、`14-client-wire-switch.md` 和 `.scratch/tunnel-node/client-wire-switch.md`。
- `internal/tunnelruntime/protocol.go`、`pkg/cmd/tunnel/server/server_agent_protocol.go`、`pkg/cmd/tunnel/connect/client_agent.go`、`client_run.go`、`client_reconciler.go`。

### Objective

Server 与官方 Client 统一使用 v5 完整 runtime 快照，Local 场景在首次 welcome、在线更新和重连后得出相同配置及独立应用/FRPC 状态。

### Scope boundary

只接入 Local Node 的 v5 消息和 Client 状态机；跨 Node 分配、远端管理、Token 轮换留给后续 Gate。删除 v4 双轨入口。

### Constraints

- `welcome` 与 `desired_state` 使用同一 runtime 对象与一致数据库读快照；Node/地址/Token/Tunnel 变化递增 Client revision。
- Client 冷启动必须等待认证 welcome；运行中的控制断线可保持现有 FRPC。低版本拒绝、同版本同内容幂等、同版本异内容报错。
- `apply_result` 不代表 FRPC 登录成功；真实登录/代理观察不可由 PID 推断。

### Slice policy

分别切协议结构与版本拒绝、Server 快照投影、Client 持久去重、Local FRPC 应用和观察；每片只改一条消息或运行调用簇。

### Verification

#### Directed

- 每个协议/状态片以 v4 hello、重复/乱序 v5 快照、通知丢失后重连和冷启动断线夹具验证。
- 收尾用现有真实 Server↔Client Local HTTP/TCP/UDP 集成测试验证完整往返，并分别观察应用结果与 FRPC/代理状态。

#### Repository

1. `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...`（每片后）。
2. `make check-terminal`（Gate 收尾）。
3. `make acceptance`（Gate 收尾，覆盖独立二进制协议拒绝）。

#### Manual acceptance

- 无。

### Evidence rule

G1-E1 由 v4 拒绝及消息字段/版本测试证明；G1-E2 由冷启动、重连、去重测试和 Local 集成往返证明；G1-E3 由状态接口观察夹具及独立二进制结果证明。

### Stop conditions

- 固定 FRP 构建不能提供足够的本机代理状态观察，导致无法区分应用与真实连接状态。
- v5 单一消息与既定 Local Client 行为发生不可化解的冲突。

### Rollback

以 v5 Client 控制协议调用簇为切口撤销未完成片；不得发布 v4/v5 并行协议作为回退方案。

### Exit conditions

- G1-E1：v5 的 welcome 与在线 desired_state 都含完整 Node/FRP/Token/Tunnel 快照；v4 Client 得明确协议错误。
- G1-E2：Local Client 冷启动、在线更新、控制重连和版本去重符合契约，HTTP/TCP/UDP 实际转发成功。
- G1-E3：本地应用结果与 FRPC 登录/代理状态分开上报，未知观察显示未知。

## G2: Node 身份与管理链路

### Purpose

建立零预配 Node 的持久身份、固定认领和独立于 TLS 的管理信道。

### Inputs

- `.scratch/tunnel-node/issues/01-claim-trust.md`、`02-binding-lifecycle.md`、`11-node-management-wire.md`、`12-node-state-crash-consistency.md`、`16-deployment-acceptance.md`。
- `.scratch/tunnel-node/node-management-wire.md`、`.scratch/tunnel-node/node-state-crash.md`、`.scratch/tunnel-node/deployment-acceptance.md`、`pkg/cmd/tunnel/tunnel.go`。

### Objective

唯一 `ycy tunnel node` 启动 HTTP 管理监听，持久保存身份和首个 Controller 绑定，完整实现 Noise XX 会话帧及安全认领；未认领时不启动 FRPS。

### Scope boundary

实现 Node CLI、私有状态、管理协议和认领，不实现 FRPS 快照应用，也不接入 Server Web 页面。可用测试 Controller 驱动协议。

### Constraints

- 默认管理端口 7600；只允许既定三个 CLI 标志/环境变量及其优先级。无 Node reset、rebind、claim 或 stop 子命令。
- Node 身份/绑定用私有 SQLite 事务持久化；目录独占锁、权限与损坏卷拒绝须成立。重启保留身份，人工清空整个目录才产生新身份。
- 公网 HTTP 只公开最小健康/版本提示与 Noise 消息；每个管理操作独立短期 Noise XX 会话，固定双方长期身份、严控序号/帧长/重放，失败或回执丢失靠查询消歧。

### Slice policy

依次切 CLI/状态目录、长期身份/锁、Noise 握手与帧、预览/认领事务、错误与重放；每片用环回 HTTP 和真实临时目录验证。

### Verification

#### Directed

- 按契约使用两个 Controller 并发认领、预览不绑定、错指纹、断开/重复帧、乱序分块、回执丢失和重启同目录测试；检查明文 HTTP 不含密钥或业务状态。
- 独立二进制运行 Node，检查帮助面只有启动命令、非法参数退出、首次指纹与重启指纹一致。

#### Repository

1. `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...`（每个协议片后）。
2. `make command-surface`（CLI 形状固定后；必要时先按新命令更新快照）。
3. `make acceptance`（Gate 收尾）。

#### Manual acceptance

- 无。

### Evidence rule

G2-E1 由独立二进制、参数和持久目录测试证明；G2-E2 由双 Controller 认领/重启事务测试证明；G2-E3 由 Noise 帧、重放和明文暴露测试证明。

### Stop conditions

- 选用的 Noise XX 实现无法满足已定的身份固定、长度、序号或重放契约。
- 不能用本地事务保证并发 claim 只有一个胜者且在崩溃后不改绑。

### Rollback

以独立 `ycy tunnel node` 命令及其私有状态目录为切口撤销未完成接入；不提供运行时清理旧 Node 身份的回退命令。

### Exit conditions

- G2-E1：Node 仅以启动命令运行；首次输出完整指纹，重启同卷身份不变，损坏状态拒绝且不自造新身份。
- G2-E2：预览不绑定，首个有效加密 claim 固定 Controller；并发第二方、错误身份和重复认领均按协议拒绝。
- G2-E3：管理业务消息只在 Noise 密文内传输；旧序号、重放、超限和错版被拒且持久状态不变。

## G3: Node FRPS 收敛与崩溃恢复

### Purpose

使 Remote Node 在管理失联、配置失败和进程崩溃时保持可解释的 FRPS 状态。

### Inputs

- `.scratch/tunnel-node/issues/04-node-desired-state.md`、`11-node-management-wire.md`、`12-node-state-crash-consistency.md`。
- `.scratch/tunnel-node/node-management-wire.md`、`.scratch/tunnel-node/node-state-crash.md`、`pkg/cmd/tunnel/server/server_frps.go`。

### Objective

Node 接收单调 revision 的完整 running/disabled 快照，可靠验证和应用 FRPS，持久记录最后成功版本并在重启时正确恢复或停用。

### Scope boundary

只完成独立 Node 的管理操作和 FRPS supervisor；Server 业务数据库、UI、Client 归属由后续 Gate 处理。

### Constraints

- 低版本拒绝、同版本同摘要幂等、同版本不同摘要报错；大快照完整校验后提交。响应丢失需能查询持久结果。
- 配置验证失败不停止旧 FRPS；候选启动失败按契约恢复旧配置并报告；回滚失败报告真实不可用。
- disabled 意图先持久化再停 FRPS；重启后不得复活。归属不明残留 FRPS 保留并报错；Node 普通停止不主动停用已运行 FRPS。

### Slice policy

分别切快照验证、高水位事务、FRPS 候选应用/回滚、disabled、重启与残留进程识别；每片只处理一个状态转换簇。

### Verification

#### Directed

- 用真实固定 FRP 构建及临时 SQLite 逐点注入验证失败、启动失败、回滚失败、响应丢失、离线重启和各持久事务点 `SIGKILL`；核对进程、端口、持久版本及查询结果。
- 重启后验证最后成功 running 继续服务、disabled 永不复活、未知归属进程不被误杀。

#### Repository

1. `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...`（每片后）。
2. `make acceptance`（Gate 收尾，独立进程重启）。
3. `make check-terminal`（Gate 收尾）。

#### Manual acceptance

- 无。

### Evidence rule

G3-E1 由版本/摘要/分块测试证明；G3-E2 由真实 FRPS 失败注入与重启测试证明；G3-E3 由停用及残留进程故障注入证明。

### Stop conditions

- 固定 FRP 构建或进程所有权机制无法满足既定候选应用与残留进程边界。
- 无法在停用意图提交后证明重启不会启动旧 FRPS。

### Rollback

以 Node 的期望快照应用器为切口回退代码；运行时仅按已持久的 lastGood 或 disabled 状态恢复，不降低高水位或清除固定绑定。

### Exit conditions

- G3-E1：完整 running/disabled 快照按版本与摘要幂等收敛，乱序或不完整快照不改持久状态。
- G3-E2：管理断线/重启继续最后成功 FRPS；验证、启动及回滚失败分别产生契约规定的进程和可查询状态。
- G3-E3：持久 disabled 确认后 FRPS 停止且重启不复活；未知归属残留进程保留并明确报错。

## G4: Server 远端 Node 管理

### Purpose

让主 Server 管理 Remote Node，同时让运营者区分配置保存、远端应用和实际运行。

### Inputs

- `.scratch/tunnel-node/issues/03-local-remote-runtime-boundary.md`、`04-node-desired-state.md`、`08-management-api-permissions.md`、`09-nodes-workflow-prototype.md`、`13-server-node-storage.md`、`15-node-management-api-contract.md`。
- `.scratch/tunnel-node/server-storage-transactions.md`、`.scratch/tunnel-node/node-api-contract.md`、`pkg/cmd/tunnel/server/server_runtime.go`、`server_http.go`、`web/tunnel-server/app.tsx`。

### Objective

Server 持久管理远端 Node 的认领、配置、版本、地址与观察结果，Web API 和 Nodes 页面提供角色正确且可操作的投影。

### Scope boundary

接入 Node 注册表、协调器、认领/配置 API、公共摘要与管理员详情及页面；Client 归属选择、Token 轮换、Remove/Force Forget 留给后续 Gate。

### Constraints

- 认领前预览完整指纹，确认时重新握手核对；`201` 只证明绑定和记录，不证明 FRPS 已运行。管理地址变更不能替换固定 Node 身份。
- Local 继续由 `ManagedFRPS` 运行；Remote 用管理连接与观察状态，不因错版/断线宣称已停机。
- 保持现有同源保护、会话、API 错误结构和 `/api/events`；所有登录用户读公共摘要，admin 才读/写详情。

### Slice policy

依次切 Server 管理适配器、持久注册与协调、管理员 API、公共投影与事件、Nodes 页面；读、写、展示分开切。

### Verification

#### Directed

- 环回 Server↔Node 进程验证预览、认领、改地址固定指纹、同版配置、错版/失联/应用失败，核对数据库、Node 实态与 API 不同时间层状态。
- 以普通用户/admin 会话测试公共字段脱敏、写权限、结构化错误和 SSE 断线重取；浏览器检查 Nodes 认领/配置/故障动作。

#### Repository

1. `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...`（每个后端片后）。
2. `make check-web`（Web 片后）。
3. `make acceptance-web`（Gate 收尾）。

#### Manual acceptance

- 无。

### Evidence rule

G4-E1 由真实 Server↔Node 集成场景证明；G4-E2 由 API 权限/投影与 Web 验收输出证明；G4-E3 由断线、错版和失败应用时的持久/实际/页面状态对照证明。

### Stop conditions

- Server 无法区分已保存期望、远端已应用和新鲜 FRPS 运行观察。
- 所需公开投影会泄露已决议的管理员或 Node 私有字段。

### Rollback

以 Server 的 Remote Node 管理适配器和 Nodes UI 路由为切口撤销；Local `ManagedFRPS` 不受远端故障影响。

### Exit conditions

- G4-E1：同版 Server 可预览、认领、重新添加原绑定 Node、保存配置并观察远端版本和 FRPS 运行。
- G4-E2：普通用户可读全部 Node 摘要且看不到私有字段；admin 可管理详情，页面按 API 错误给出下一步。
- G4-E3：管理失联、错版、应用失败和旧观察分别显示，不把缓存运行误报停机。

## G5: Node 资源与 Client 归属

### Purpose

让用户显式选择 Client 的 Node，并在 Server 事务中维持资源和在线/离线归属一致性。

### Inputs

- `.scratch/tunnel-node/issues/05-client-node-assignment.md`、`06-node-resource-scope.md`、`08-management-api-permissions.md`、`13-server-node-storage.md`、`15-node-management-api-contract.md`。
- `.scratch/tunnel-node/server-storage-transactions.md`、`.scratch/tunnel-node/node-api-contract.md`、`pkg/cmd/tunnel/server/server_tunnels.go`、`server_agent_protocol.go`、`web/tunnel-server/app.tsx`。

### Objective

Server 支持每 Node 端口池/域名约束、Client 当前与待切换归属、目标预检及用户可理解的配置/连接分层状态。

### Scope boundary

实现 Server 事务、API、Client 页面和 v5 目标快照生成；真实官方 FRPC 跨 Node 切换在 G6 完成。不得把此 Gate 的数据库提交误报为数据面成功。

### Constraints

- 不同 Node 可复用 TCP/UDP 端口；同 Node 同协议冲突拒绝；整域 hostname 只能归一个 Node，DNS 未就绪可保存并提示目标入口。
- 端口池保存后立即作为分配依据，缩小范围若排除已占用端口则拒绝；期望与已应用版本分开观察。
- 在线目标需新鲜管理与 FRPS 运行预检；目标不合格则保留旧归属。离线 Client 只写 pending，重连再检查，可取消或替换；已提交新归属不自动回旧 Node。
- 普通用户可选全部 Node，但只能修改自己的 Client；跨用户访问沿现有 owner 边界处理。

### Slice policy

分别切端口事务、hostname 事务、Client 归属事务、完整快照投影、API 和 Client 页面；一个片只处理一个写规则或一个展示层。

### Verification

#### Directed

- 并发事务测试不同/相同 Node 的 TCP/UDP 端口、整域冲突、缩池占用、DNS 未就绪保存；失败后比对数据库完整回滚。
- 测试在线目标预检拒绝、在线提交、离线 pending、重连重检/冲突、取消/替换及 owner 权限；核对响应码与页面四层状态。

#### Repository

1. `go test ./pkg/cmd/tunnel/server/... ./internal/tunnelruntime/...`（每个事务片后）。
2. `make check-web`（展示片后）。
3. `make acceptance-web`（Gate 收尾）。

#### Manual acceptance

- 无。

### Evidence rule

G5-E1 由资源并发/回滚测试证明；G5-E2 由在线/离线事务与权限测试证明；G5-E3 由 API 响应、SSE 和浏览器页面的状态/提示证明。

### Stop conditions

- 既定资源规则无法在数据库事务边界内避免并发双分配。
- 目标预检缺少新鲜管理或 FRPS 观察而会把未知当成可用。

### Rollback

以 Client 归属事务及资源索引为切口撤销未完成片；已经提交的新归属不通过自动回旧 Node 作为回退。

### Exit conditions

- G5-E1：端口/hostname/缩池规则在并发下成立，失败写入不留下部分资源或归属。
- G5-E2：合格在线目标提交当前归属；不合格目标拒绝且旧归属不变；离线 pending 在重连时重新检查，可取消或替换。
- G5-E3：所有用户可见/可选 Node，Client 页面明确当前、待切换、应用和真实连接观察，DNS 提示指向目标入口。

## G6: 官方 Client 跨 Node 连接

### Purpose

把 Server 的归属决定落实为官方 FRPC 的实际连接和可核对的 Token 生命周期。

### Inputs

- `.scratch/tunnel-node/issues/05-client-node-assignment.md`、`10-rollout-compatibility.md`、`13-server-node-storage.md`、`14-client-wire-switch.md`。
- `.scratch/tunnel-node/client-wire-switch.md`、`.scratch/tunnel-node/server-storage-transactions.md`、`pkg/cmd/tunnel/connect/client_reconciler.go`、`client_run.go`、`pkg/cmd/tunnel/server/server_agent_protocol.go`。

### Objective

官方 Client 按完整 v5 目标从旧 Node 停止并连接新 Node，Server 分别显示应用/登录/代理状态，Node Token 先远端确认再发布给 Client。

### Scope boundary

实现 Client 跨 Node 应用、观察回报、Server 状态接收和 Token 轮换。Node Remove 与部署发布留给 G7。

### Constraints

- 跨 Node 新目标一经接受，官方旧 FRPC 停止；候选或首次登录失败仍固定新 Node 并重试，不自动回旧 Node。同 Node 配置失败可恢复该 Node 最后成功配置。
- FRPC 状态从本机回环接口观察，接口不可用为未知；Server 只采纳与当前版本、Node、摘要一致的新鲜回报。
- 每 Node 独立 Token；先在 Node 确认新 Token，Server 才向关联 Client 发布。崩溃恢复须查询 Node 消歧，不能把旧共享 Token 说成逐 Client 吊销。

### Slice policy

分别切跨 Node 停旧、目标应用/重试、FRPC/代理观察、Server 回报过滤、Token 阶段事务；每片只改变一个状态转换调用簇。

### Verification

#### Directed

- 真实 Local→Remote、Remote→Remote 及 Remote→Local FRPC/FRPS 集成：观察旧官方 FRPC 停止、新目标首次登录失败仍不回旧、Tunnel 为空时归属已接受、控制通知丢失后 welcome 补偿。
- 注入状态接口不可用、过期回报、Node Token 应用失败及 Node 确认后 Server 崩溃；核对 Node 当前 Token、Server 持久阶段、Client 所收 Token 与实际代理。

#### Repository

1. `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...`（每片后）。
2. `make check-terminal`（Gate 收尾）。
3. `make acceptance`（Gate 收尾，真实二进制切换）。
4. `make acceptance-web`（Gate 收尾，状态投影）。

#### Manual acceptance

- 无。

### Evidence rule

G6-E1 由三类跨 Node 真实转发/失败场景证明；G6-E2 由登录/代理观察和过期过滤测试证明；G6-E3 由 Token 故障注入与重启消歧测试证明。

### Stop conditions

- 固定 FRP 构建不能提供可用的真实登录/代理观察，且无法按契约报告未知。
- 不能在 Node 确认与 Server 发布之间恢复 Token 阶段而会向 Client 发错 Token。

### Rollback

以 Client 目标应用器及 Token 阶段事务为切口撤销未完成代码；已提交的跨 Node 归属只通过新的显式用户决策改变，不由失败恢复暗中倒退。

### Exit conditions

- G6-E1：在线和离线待切换完成后官方 FRPC 连接目标 Node，旧官方连接停止；新目标失败不回旧 Node。
- G6-E2：Server 展示与目标版本匹配的应用、登录和代理观察；过期结果丢弃，观察不可用显示未知。
- G6-E3：Token 轮换仅在 Node 确认后向当前分配 Client 发布；失败或崩溃可查询恢复且其他 Node 不受影响。

## G7: 移除、部署与发布验收

### Purpose

关闭危险生命周期操作与破坏性部署的实际验收缺口。

### Inputs

- `.scratch/tunnel-node/issues/02-binding-lifecycle.md`、`10-rollout-compatibility.md`、`15-node-management-api-contract.md`、`16-deployment-acceptance.md`。
- `.scratch/tunnel-node/node-state-crash.md`、`.scratch/tunnel-node/node-api-contract.md`、`.scratch/tunnel-node/deployment-acceptance.md`、`deploy/docker-compose.tunnel.yml`、`acceptance/tunnel_test.go`、`acceptance/web/browser_test.go`。

### Objective

完成正常 Remove/Force Forget、固定版本的 Server/Node 部署资料及完整自动化和 Docker 等价隔离网络烟测。

### Scope boundary

仅补齐生命周期、部署和发布级验证；不加入兼容迁移、自动负载均衡或新管理子命令。

### Constraints

- 有当前或待切换 Client 时拒绝正常 Remove；无依赖后持久 `removing`、下发 disabled 并等 Node 持久停用且 FRPS 停止才删除。离线或确认丢失保持待移除并查询消歧。
- Force Forget 只删除 Server 记录，清楚提示旧 Node 可能继续运行；不远程清身份、不暗改绑定。Controller 私钥丢失不得自动生成替代身份访问旧 Node。
- 文档用同一精确发行版、新空 Server 目录、每 Node 独立持久卷和公网 HTTP 管理端口 7600；旧状态目录原样留存，并写明停机/重建的预期中断。

### Slice policy

先切 Remove 事务、disabled 确认、Force Forget 风险呈现，再切裸机/容器文档、自动化故障矩阵和 Docker 等价隔离网络烟测；每片对应一个生命周期行为或验收层。

### Verification

#### Directed

- 按 `.scratch/tunnel-node/deployment-acceptance.md` 故障矩阵逐项记录数据库归属、Node 持久高水位、FRPS/FRPC 进程、API/Web 投影；覆盖旧库字节不变、双 Controller、崩溃点、离线、切换、资源、Token、Remove、权限与错版。
- 在独立 Docker 网络中使用同一精确发行版运行 Server、Node 和官方 Client，从 Server 容器经 Node 网络地址的 7600 端口认领；保留持久卷重建容器后验证 HTTP/TCP/UDP 与 DNS 提示。记录版本、FRP 构建、网络隔离、端口映射和预期中断；实际进程、持久状态及 API/Web 投影均须取证，不能以环回单测或单次 HTTP 成功替代。

#### Repository

1. `make command-surface`（最终 CLI 面）。
2. `make check`（全部源码与 Web 校验）。
3. `make acceptance`（独立二进制验收）。
4. `make acceptance-web`（浏览器验收）。

#### Manual acceptance

- 无。用户已明确授权以等价隔离网络测试通过作为 G7 验收结果，不需要公网或另一次人工验收。

### Evidence rule

G7-E1 由 Remove/Force Forget 故障注入、API 与 Web 验收证明；G7-E2 由四条 Repository 命令、故障矩阵逐项记录和 Docker 等价隔离网络烟测证明；G7-E3 由部署文件/文档核对及隔离部署的可执行性证明。

### Stop conditions

- 缺少可运行的 Docker 等价隔离网络，无法执行真实进程与持久卷烟测。
- 实际 Remove 不能可靠取得持久 disabled 与 FRPS 停止双重确认。
- 故障矩阵或人工验收发现与已决议边界不符，且无法在本 Gate 范围内修复。

### Rollback

以 Server 的 Remove 生命周期与部署示例为切口撤销未完成片；Force Forget 不触碰远端，已认领 Node 的身份不会因 Server 代码回退自动清理。

### Exit conditions

- G7-E1：正常 Remove 在依赖存在、离线和回执丢失时保持正确状态，确认停用后才删除；Force Forget 只清 Server 记录且风险可见。
- G7-E2：完整故障矩阵、仓库命令和 Docker 等价隔离网络 HTTP/TCP/UDP 烟测均有可复核结果，错版/旧库拒绝无隐式数据修改。
- G7-E3：固定版本的裸机/容器部署说明可执行，隔离部署中的页面与实际操作验收通过。

## Definition Of Done

- G0–G7 的 Exit conditions 各有对应 Directed、Repository 与适用的 Manual 证据，账本记录通过。
- 单一 v5 Client 与 Noise/HTTP Node 管理契约、Server schema v3、API 状态和 CLI 帮助一致；Local 与 Remote 端到端行为满足设计地图。
- 破坏性部署说明、故障矩阵和 G7 Docker 等价隔离网络验收齐备；旧目录保持原样且没有迁移或兼容双轨。

## Explicitly Out Of Scope

- 旧 SQLite、旧 Client 或错版 Node 的兼容迁移、自动备份、混合协议、无中断切换。
- Node 主动注册、固定 Server 地址、长期共享注册 Token、mTLS/配对码、面向管理链路的反向代理适配。
- Node 产品内 reset/rebind/stop/claim 子命令、远程转交已绑定 Node、恢复丢失的 Controller 私钥。
- Node 业务账号/Client/Tunnel 数据库、逐 Client 旧 FRP Token 吊销、自动跨 Node 负载均衡和高可用 Controller。
