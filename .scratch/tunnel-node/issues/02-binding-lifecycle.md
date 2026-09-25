# Node 绑定、移除和恢复的状态语义是什么？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: grilling
Status: resolved
Blocked by: 01

## Question

在认领协议确定后，明确未认领、已绑定、运行中、离线/错误、被主 Server 移除等状态中，哪些是持久归属，哪些只是运行观察；已绑定 Node 对第二个 Server 应如何拒绝。Node CLI 只有 `ycy tunnel node` 启动服务，没有 reset 等其他子命令；因此需决定主 Server 的 Remove 如何让 Node 持久停用、是否清除绑定，以及是否支持失去 Controller 私钥后的恢复。还需决定主 Server 数据库丢失、Node 重装、旧 Node 重新出现时的处理，以及旧凭据何时失效。

应以具体场景给出允许的状态迁移和禁止的远程操作，而非只列状态名。

## Comments

### 2026-09-23：已认领，第一轮待用户决定

前置决议见[“如何让主 Server 通过首次连接安全地认领零预配 Node？”](./01-claim-trust.md)：Node 自动固定首个有效认领者；后续以 Noise 长期公钥验证同一 Controller，陌生身份不能再次认领；本机 reset 的含义留给本票据。

第一轮询问三个互不依赖的边界：持久绑定和运行观察是否分开；主 Server Remove 是先停止并确认、直接忘记，还是远程解绑；Node 本机 reset 是否保留 Node 身份。等待用户选择后再讨论 Controller 密钥/数据库丢失、Node 重装和旧 Node 重新出现的恢复流程。

现有代码只管理主 Server 自带的 FRPS，没有 Node 身份或 reset。当前 [`ManagedFRPS.Stop`](../../../pkg/cmd/tunnel/server/server_frps.go)只停止子进程、不删除配置；因此若 Remove 需要远端停止且重启后不恢复，最终设计必须表达**持久停用**，不能只发一次 Stop。现有 Client 删除会事务删除数据库记录，再异步吊销在线连接，见[`server_clients.go`](../../../pkg/cmd/tunnel/server/server_clients.go)和[`server_agent.go`](../../../pkg/cmd/tunnel/server/server_agent.go)。这些是接入约束，尚不代替本票据的产品决定。

### 2026-09-23：用户第一轮回答

- 将持久归属与在线、FRPS 运行、配置错误等运行观察分开。
- 主 Server Remove 前先要求 Node 无 Client；在线时持久停用远端 FRPS 并等待确认，离线时保持待移除，不把尚可能运行的旧配置当作已安全停止。
- 明确纠正：Node CLI 只有 `ycy tunnel node` 启动服务，不增加 reset 或其他管理子命令。原先关于本机 reset 是否保留身份的提问失效，尚需重新确定解绑与恢复路径。

### 2026-09-23：第二轮待用户决定

在“先持久停用并确认再 Remove”的前提下，询问 Remove 后 Node 是否保留 Controller 绑定，或由原 Server 远程解除绑定；同时询问 Controller 私钥彻底丢失时，是否允许停服务后手工清理 Node 本地状态目录作为灾难恢复。两项都不增加 Node CLI 子命令。

### 2026-09-23：用户第二轮回答

- Remove 完成后 Node 保留对原 Controller 的绑定。要改绑其他 Server，需在 Node 主机手工清理状态；不增加 CLI 管理命令。
- Controller 私钥彻底丢失时不提供旧绑定/原控制关系的恢复功能。不能以另一个 Server 身份远程接管已绑定 Node。是否可通过手工清理把同一台机器当作全新 Node 使用，与旧绑定恢复是两种不同语义；其身份结果仍待确认。

仍需确定手工清理的身份结果，以及 Node 已损坏或消失、无法按正常 Remove 流程收到停用确认时，主 Server 是否能遗忘残留记录。

### 2026-09-23：第三轮待用户决定

询问手工清理时是否生成全新 Node 身份；Node 永久失联时是否允许 Server 强制遗忘记录（不能保证远端 FRPS 停止）；仅 Server 数据库丢失而 Controller 私钥仍在时是否允许用同一身份重新添加 Node。三项选择将决定旧 Node 重新出现和凭据失效的具体规则。

### 2026-09-23：用户第三轮回答

- 对第一项强调：普通 Node 服务可以临时停止，再次运行时不应做任何清理，应恢复同一身份和绑定；容器重启是重要场景。此回答没有明确“主动改绑时手工清理”的文件范围，需要单独区分。
- 允许对永久失联 Node 在主 Server 侧强制遗忘，即使无法确认旧 FRPS 已停止。
- 如果只丢失 Server 数据库而长期 Controller 身份私钥仍在，允许以原身份重新添加仍绑定的 Node。

### 2026-09-23：用户最终澄清

产品中**不存在 Node 改绑操作**。普通停机/容器重启保留身份与绑定；只有用户在 Node 主机手工清理身份、指纹及相关状态后再启动，才能把这台机器当作新的 Node 重新认领。主 Server 发起认领时必须清楚提示失败原因，特别是目标 Node 已绑定、身份不符或无法连接。

## Answer

### 归属和运行状态

- Node 持久归属只区分 `unclaimed` 与 `claimed(controller identity)`。`claimed` 后只认可原 Controller 的 Noise 公钥；其他 Server 的认领请求得到不泄露原 Controller 身份的“已被认领”结果。在线/离线、FRPS 运行/停止、配置错误是独立的观察状态，不改变归属。
- `ycy tunnel node` 是唯一 Node CLI 操作。Node 身份、Controller 绑定及运行所需状态保存在持久目录。普通停机、进程重启、容器重建只要复用该目录，就保留原身份和绑定；Node 重新监听后由主 Server 按已登记管理地址重连。FRPS 配置在离线和重启后的具体应用策略由[“主 Server 应下发什么 Node 期望状态，Node 如何收敛？”](./04-node-desired-state.md)决定。
- 没有远程解绑、改绑、Node reset 子命令。需要让同一机器重新接受其他 Server 时，操作员须在 Node 主机停服务、手工清理**整个 Node 身份和运行状态目录**后重新启动。此时生成新 Node 身份与指纹，按全新 Node 认领；旧绑定和旧配置不恢复。这是人为重新初始化机器，不是产品提供的改绑流程。容器部署必须为该目录挂载持久卷，否则容器重建会意外变成新 Node。

### 正常移除与强制遗忘

- 内建 Local Node 始终属于主 Server，不能 Remove；以下只适用于 Remote Node。普通 Remove 的前提是没有 Client 仍分配给该 Node。主 Server 先把记录置于待移除，向在线 Node 下发**持久停用**，等待 Node 落盘停用标记、停止 FRPS、清除可再次启动旧 FRPS 的有效配置/凭据并确认后，才从主 Server 的活动清单移除。若停用失败，保留待移除和错误信息。
- Node 离线时，主 Server 保留待移除记录并重试，不能把“请求已发出”当作远端已停止。旧 FRPS 在此期间仍可能运行。正常 Remove 完成后，Node 仍持久绑定原 Controller，处于停用状态，不自动开放新认领。若同一 Controller 后续重新添加它，仍须核对 Node 指纹并重新下发配置。
- 对损坏或永久失联、无法返回停用确认的 Node，管理员可以执行明确标记的**强制遗忘**，前提同样是无已分配 Client。此操作只删除主 Server 记录，不能保证远端 FRPS 已停止、旧配置或凭据已清除；界面必须明确提示这一点。旧 Node 再出现时不能自动加入清单，只能由持有原 Controller 身份的主 Server 按原 Node 指纹显式重新添加，或由用户手工清理 Node 状态使其成为全新 Node。

### 身份丢失与再次启动

- 仅主 Server 数据库丢失而 Controller 身份私钥仍在：主 Server 可以按原公钥与 Node 建立 Noise 会话，核对 Node 指纹，将仍绑定自己的 Node 显式重新添加；这不是重新认领其他 Controller 的 Node。
- Controller 身份私钥彻底丢失：不提供旧绑定、旧配置或原 Controller 身份的恢复/接管功能。旧 Node 继续拒绝新 Controller。用户若要复用该机器，只能按上述本机手工清理步骤把它初始化成新的 Node。备份私钥的运维政策另由部署文档规定，不在这里承诺恢复功能。
- Node 身份目录丢失、被清理或重装且没有持久卷：启动后是新 Node、新指纹，不能冒充旧 Node。主 Server 不应因管理 IP:端口相同而自动接受；旧记录若无法正常停用，可按强制遗忘处理。旧 Node 后来重新出现，也不能绕过指纹核对或自动替换新 Node。
- 认领失败、指纹不符、目标已被其他 Controller 认领、目标不可达、协议不兼容及停用失败，都须在主 Server 端呈现明确且不泄露他人身份的错误与下一步动作；具体错误码和页面文案由[“主 Server 的 Node 管理 API 和权限边界是什么？”](./08-management-api-permissions.md)及[“Nodes 页面怎样让认领、分配和故障状态可理解？”](./09-nodes-workflow-prototype.md)细化。
