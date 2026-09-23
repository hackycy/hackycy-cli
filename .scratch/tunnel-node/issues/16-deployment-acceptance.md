# 部署参数与端到端验收如何冻结？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: prototype
Status: resolved
Blocked by: 11, 12, 13, 14, 15

## Question

把已决议的公网直连和破坏性发布落实为可执行部署契约：`ycy tunnel node` 的启动参数/环境变量、默认监听与状态目录、Node 持久卷、管理端口/FRP 端口/HTTP 入口/端口池映射、Controller 私钥保存、同发行版部署与错版错误、旧 Server 数据目录留存和新空目录启动步骤。Node 仍只有启动服务这一个子命令，不能新增 reset/改绑命令；不提供旧库迁移或旧 Client 兼容流程。

将[发布验收矩阵](./10-rollout-compatibility.md)细化到具体可运行的测试层级与故障注入：Local 回归、远端首次认领与抢占、重启/断线/版本不符、配置验证与启动失败、Client 切换、端口/域名冲突、Token 轮换、正常移除与强制遗忘。只冻结行为与验证路径，不在本票据实施生产功能。

## Artifacts

- [部署与端到端验收契约](../deployment-acceptance.md)：Node 单一启动命令与参数、端口/卷映射、同发行版 Compose 示例、破坏性部署步骤、测试层级和故障注入矩阵。Node 命令尚未实现，示例须在实施后才可执行。

## Answer

- 用户确认按[部署与端到端验收契约](../deployment-acceptance.md)定稿。Node 只有 `ycy tunnel node` 一个服务命令；拟新增 `--management-bind-address`、`--management-port`、`--data-dir` 及对应环境变量，默认管理监听 `0.0.0.0:7600`。它只配置本地管理监听与身份状态目录；FRPS bind、HTTP vhost、端口池、Token 和对外地址由主 Server 认领后配置/下发。首次空目录生成 Node 身份，持久卷重启保持身份、绑定和最后成功配置；状态损坏不得悄悄换身份。无 Node reset、远程改绑或预配 Server 地址。
- 主 Server 延续现有 Local Node 启动参数和默认端口；新版以空状态目录建新库，把 Controller 私钥单独存于同一私有状态卷。Remote Node 使用独立持久卷，管理 7600/tcp、FRPS 7000/tcp、HTTP 8080/tcp 与 TCP/UDP 端口池按示例分别暴露；端口池对外端口号与内部保持一致。Server 面板的管理 HTTP 地址、Client FRP 地址与 HTTP 公网入口分别填写。Server、Node、Client 使用同一精确发行版及固定 FRP 构建，不支持混合版本滚动运行；Docker 示例应固定镜像 tag，不默认 `latest`。
- 破坏性发布停掉旧 Server/Client，**原样保留**旧状态目录/卷，以新的空 Server 目录启动并重新建立账号、Client、Token 与 Tunnel；无旧 SQLite 自动迁移、备份或旧 Client 协议兼容。Node 认领后须核对完整本机指纹；旧目录、错版 Node 或失去 Controller 私钥都不能被程序自动接管/改绑。公网 HTTP Node 管理链路仍由 Noise XX 保护；健康 HTTP 不等于 FRPS 可用。
- 发布门槛按契约中的测试层级和故障注入矩阵执行：Local HTTP/TCP/UDP 回归、旧库原样拒绝、双 Controller 首认领竞争与重放、Node SQLite/FRPS 崩溃恢复、断线与错版、在线/离线 Client 切换、端口/域名冲突、Token 轮换窗口、正常移除和强制遗忘，以及独立二进制、Web 和真实公网 Docker 烟测。矩阵是未来实现的验收要求，本设计阶段未运行新增场景。
