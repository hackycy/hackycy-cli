# Tunnel Node 接入设计地图

Label: wayfinder:map

## Destination

形成一份可交给实现者的 Tunnel Node 接入设计：主 Server 主动认领远端 Node，主 Server 自带默认本地 Node，Client 的控制面继续连接主 Server、数据面连接被分配的 Node。设计须明确产品行为，以及管理线协议、持久化、Client 切换、管理 API 和部署验收的关键技术契约；本地图只推进决策，不实施功能。

## Notes

- **本地图是设计决议的权威入口。** 每张已关闭票据的 `## Answer` 是该领域的最终决议；`## Question` 和 `## Comments` 只记录提问、代码调查与被后续回答覆盖的讨论，不作为实现依据。跨票据有冲突时，以明确标注的较新决议为准，尤其是[破坏性发布边界](./issues/10-rollout-compatibility.md)对旧库迁移的覆盖。
- **全部 16 张决策票据已完成，本设计地图已到达交接点。** 实现按[交接文档](./spec.md)与[接入顺序决议](./issues/10-rollout-compatibility.md)另行推进；这里的示例与验收矩阵尚未成为生产代码。
- [实现交接入口](./spec.md)汇总产品形态并链接决议；[代码现状索引](./integration-outline.md)只记录现有接点；[领域术语](./CONTEXT.md)统一名称。原始讨论中仍有效的架构和流程已归并到这些文件及票据，未采用的示例方案不保留为设计来源。
- 所有 wayfinder 文件保存在本目录；票据在 `issues/` 下。开放、未认领、所有 `Blocked by` 均已 `resolved` 的票据构成 frontier，按文件编号选取。开始处理前先把该票据 `Status` 改为 `claimed`；决议写在票据 `## Answer`，再改为 `resolved`，并在下方登记一句摘要。
- 处理 `grilling` 票据时使用 grilling 和 domain-modeling；先陈列同一轮所有可回答的问题及建议，等待人的决定。处理 `prototype` 票据时先做可讨论的粗样，等待人反馈。
- 首版接入边界：一个主 Server 管理本地与远端 Node；Node 不预配主 Server 地址；Node 上没有 accounts、clients、tunnels 的业务数据库。新版本以空状态目录启动，本地 FRPS 仍可独立使用；旧数据库与旧 Client 不做跨版本兼容。细节及例外由票据决定。
- 用户补充的 CLI 约束：Node 只有 `ycy tunnel node` 启动服务，不增加 reset 等其他管理子命令。
- 前十张票据锁定产品行为和发布边界；技术设计票据 `issues/11`–`issues/16` 已完成协议、存储、API 与部署验收契约。实施顺序以[接入顺序决议](./issues/10-rollout-compatibility.md)为准。
- 技术票据先给出可审阅的消息、表结构、状态与部署草案，再讨论会改变安全性、故障行为或使用方式的取舍；单纯 Go 命名与目录拆分由实施者按仓库风格确定。

## Decisions so far

<!-- 只在票据关闭后写一行摘要和链接；不在地图复制完整决议。 -->

- [如何让主 Server 通过首次连接安全地认领零预配 Node？](./issues/01-claim-trust.md)：公网 HTTP 直连，主 Server 核对 Node 指纹；Node 自动固定首个有效认领者，管理消息使用 Noise XX 加密，并明确接受首认领抢占风险。
- [Node 绑定、移除和恢复的状态语义是什么？](./issues/02-binding-lifecycle.md)：普通重启保持绑定；正常移除须确认远端持久停用，强制遗忘只清 Server 记录；没有 Node 改绑操作，换 Controller 须本机清理后以新身份认领。
- [本地与远端 Node 在主 Server 中共享什么运行边界？](./issues/03-local-remote-runtime-boundary.md)：Local Node 与 Remote Node 共用业务视图；本地继续使用 `ManagedFRPS`，远端由独立进程管理 FRPS，主 Server 通过 Node 管理层选择运行接点。
- [主 Server 应下发什么 Node 期望状态，Node 如何收敛？](./issues/04-node-desired-state.md)：Server 下发带单调版本的完整配置；远端离线沿用上次成功配置，新配置失败回滚并报错，正常移除以持久停用快照确认。
- [Client 切换所属 Node 时控制协议如何保证 FRPC 切换？](./issues/05-client-node-assignment.md)：目标可用时提交新归属并下发完整 Client 配置，失败不自动回旧 Node；Client 离线时先待切换，连接状态与归属分开显示。
- [多 Node 下端口、HTTP 路由与 FRP 凭据按什么范围归属？](./issues/06-node-resource-scope.md)：TCP/UDP 端口池按 Node 隔离并可复用端口号；HTTP 域名整体只归一个 Node，DNS 未就绪可先保存路由并提示目标地址。
- [Node 与 Client 归属如何落库并升级现有部署？](./issues/07-persistence-migration.md)：按 Node 落库及事务性资源约束；后续破坏性升级决议取消旧库自动迁移，新版只接受空目录建库，旧库和旧版均拒绝跨版本打开。
- [主 Server 的 Node 管理 API 和权限边界是什么？](./issues/08-management-api-permissions.md)：所有登录用户可见、可选择全部 Node 并管理自己的 Client 归属；所有 admin 管理 Node，用户摘要与管理员详情分层，公网 Node 接口仅允许 Noise 保护的管理操作。
- [Nodes 页面怎样让认领、分配和故障状态可理解？](./issues/09-nodes-workflow-prototype.md)：用户接受可点击草模的首版流程；Nodes 对所有登录用户可见，管理员认领与移除，Client 页面分开展示归属、待切换和实际连接。
- [接入顺序和跨版本验收边界如何确定？](./issues/10-rollout-compatibility.md)：单一新协议和空库部署，不兼容旧 Client/SQLite/错版 Node；按基础模型、Node 运行时、Server 管理、Client 分配、发布验收推进。
- [Node 管理链路的 Noise/HTTP 消息契约如何落地？](./issues/11-node-management-wire.md)：每个管理操作独立短期 Noise XX 会话，认领固定握手身份，严格序号与查询式重试；较大快照分块校验后提交。
- [Node 本地状态与 FRPS 应用怎样保证崩溃后一致？](./issues/12-node-state-crash-consistency.md)：Node 用私有 SQLite 事务保存身份、绑定和运行快照；先持久停用意图再停 FRPS，归属不明的残留进程保留并报错。
- [主 Server 新库与 Node 资源事务如何具体实现？](./issues/13-server-node-storage.md)：新空库以 Node 约束端口和域名归属；端口池保存即用于分配，Node 应用另行观察；Token 轮换在 Node 确认后发布给 Client。
- [Client 新协议与跨 Node 切换时序如何具体实现？](./issues/14-client-wire-switch.md)：v5 统一下发完整 Node/FRP/Tunnel 配置；跨 Node 失败不回旧 FRPC，冷启动等认证 welcome，真实代理状态独立上报。
- [Node 管理 API 与页面状态契约如何具体定义？](./issues/15-node-management-api-contract.md)：沿现有 Web API/SSE 增加公共与管理员 Node 投影；保存、远端应用、Client 登录分别观察，认领与移除按持久状态跟踪。
- [部署参数与端到端验收如何冻结？](./issues/16-deployment-acceptance.md)：Node 单一启动命令默认公网 HTTP 管理 7600，持久卷与 FRPS 端口分离；同发行版空库部署，以故障注入矩阵作为发布门槛。

## Not yet specified

<!-- 当前可准确表述的落地问题已建为开放票据；技术设计过程中发现的新决策再补入地图。 -->

## Out of scope

- 在此轮设计中实现 CLI、数据库、HTTP API 或 Web UI；实施将在设计决议齐备后进行。
- Node 主动向主 Server 注册、配置固定的 Server 地址或长期共享注册 Token；这与已决议的主动认领方向相反。
- Node 承担业务数据库或管理账号体系；主 Server 仍是业务配置的唯一权威来源。
- 以 HTTPS 证书、mTLS 或配对码为认领必要条件，以及为管理链路专门适配反向代理；本轮按公网 HTTP IP:端口直连设计。
- 自动跨 Node 负载均衡和高可用控制器；本轮仅设计可显式分配的多 Node 接入。
- 恢复已丢失的 Controller 私钥、在保持原绑定的同时将 Node 转交其他 Server；用户选择把本机状态清理后的 Node 视作新 Node。
