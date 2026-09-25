# Tunnel Node 部署与端到端验收契约

状态：已按[“部署参数与端到端验收如何冻结？”](./issues/16-deployment-acceptance.md)及用户“按草案定稿”的反馈确定。这是**实施后的部署契约和验收计划**，不是现有版本可执行的 Node 操作手册。现有代码尚无 `ycy tunnel node`、v5 Client 或新 schema。产品边界见[交接文档](./spec.md)，安全帧见[Node 管理线契约](./node-management-wire.md)，状态持久化见[Node 崩溃恢复契约](./node-state-crash.md)。

## 运行拓扑和端口

主 Server 同时运行控制面和不可删除的 Local FRPS。Remote Node 初次启动只开放管理 HTTP；管理员认领后，才由主 Server 下发 FRPS bind、HTTP vhost、端口池、Token 等完整期望配置。Node 不配置主 Server URL、不持有管理员账号，也不主动注册。Server 访问 Node 管理地址；官方 Client 控制连接仍访问 Server，FRPC 数据连接访问所属 Node。

| 端口/地址 | 示例 | 访问方与部署要求 |
| --- | --- | --- |
| 主 Server 控制面 | `server.example.com:7500/tcp` | 浏览器和官方 Client 的 HTTP/WebSocket；现有默认端口 7500。部署可以给此 Web UI 单独使用 HTTPS 反向代理；这与 Node 管理链路的 Noise 加密无关。 |
| Local FRPS / HTTP / 端口池 | `7000/tcp`、`8080/tcp`、`20000-20100/tcp+udp` | 延续当前 Server 默认值，仍由 Server 启动设置管理。 |
| Remote Node 管理入口 | `http://203.0.113.10:7600` | 主 Server 主动直连的公网 HTTP 地址；仅承载匿名健康/协议提示和 Noise 握手/密文，不公开 Node 业务状态。7600 为 Node 默认管理端口。公网 IP:端口即可，不要求域名或 TLS 证书。 |
| Remote FRPS / HTTP / 端口池 | `7000/tcp`、`8080/tcp`、`20000-20100/tcp+udp` | 管理员认领后在 Server 的 Node 设置里保存；Node 应用前不保证监听。FRP 对外 host:port、HTTP 对外入口另填，允许 FRP/HTTP 固定端口做 NAT 映射。端口池必须映射为相同公网端口号。 |

三个对外地址分别录入：**Management Address** 是 Server→Node 管理 HTTP；**Advertised FRP Address** 是 Client→Node FRPS；**HTTP Ingress Address** 是 HTTP 域名应指向的 Node 入口。端口和 DNS 未就绪时可保存 HTTP 路由并提示实际访问可能失败；一个域名只归一个 Node。每个 Node 的管理监听、FRPS bind、HTTP vhost 与端口池不得在该主机上冲突，端口池不能包含本 Node 的保留监听端口。不同公网主机可复用同一端口号/池；同一主机的两个 Node 进程仍受真实 socket 绑定约束。[资源决议](./issues/06-node-resource-scope.md)是 Server 事务校验依据。

## Node 启动参数（拟实施）

唯一 Node 命令为 `ycy tunnel node`；不增加 `reset`、`rebind`、`claim`、`stop` 等 Node 子命令。参数只控制**本机管理监听和私有状态目录**，不在 Node 命令行配置 FRPS 端口、Token、主 Server 地址或业务路由。

| CLI 标志 | 环境变量 | 默认值 / 含义 |
| --- | --- | --- |
| `--management-bind-address` | `YCY_TUNNEL_NODE_MANAGEMENT_BIND_ADDRESS` | `0.0.0.0`，Node 管理 HTTP 的本机 bind IP；这是监听地址，不是面板里输入的公网 Management Address。 |
| `--management-port` | `YCY_TUNNEL_NODE_MANAGEMENT_PORT` | `7600`，1–65535 的 TCP 端口。 |
| `--data-dir` | `YCY_TUNNEL_NODE_DATA_DIR` | 状态根目录下的 `ycy/tunnel/node`；Linux 遵循 `XDG_STATE_HOME` 或 `~/.local/state`，macOS 使用 `~/Library/Application Support`，Windows 使用 `LOCALAPPDATA`。容器部署必须显式设成挂载卷路径。 |

与现有 Server 配置一致，显式 CLI 值优先于环境变量，环境变量优先于默认值；显式空值/非法端口报错，不静默改回默认。Node 把目录解析为绝对路径并独占加锁；目录权限 0700，身份/SQLite/快照文件仅进程用户可读。首次空目录启动生成长期 X25519 身份并打印**完整 Node 指纹**及管理监听提示；`0.0.0.0` 不是公网地址，面板中的可达地址由操作员按公网 IP/端口输入。未认领不启动 FRPS。旧卷重启保持身份、绑定、高水位及最后成功配置；现有未知/损坏状态不生成新身份，报错停机。Node 本地 SQLite 的 WAL、SHM 与主文件必须保存在**本机文件系统**，不能用跨主机共享网络文件系统的卷。[Node 状态契约](./node-state-crash.md)覆盖停用与残留 FRPS 处理。

裸机命令形状（**新命令实现后**）：

```sh
ycy tunnel node \
  --management-bind-address 0.0.0.0 \
  --management-port 7600 \
  --data-dir /var/lib/ycy-tunnel-node
```

同一台机器上启动多个 Node 时，每个实例必须有不同的 `--data-dir` 和管理端口，并为后续 FRPS 配置选择不冲突的本机监听。Node 本机手工清理整个身份/运行目录后再启动，会变成**新指纹、新身份**；这是操作员重新初始化机器，不是产品内的改绑或 reset 功能。

## Server 与容器示例（拟实施）

现有 `ycy tunnel server` 的 `--address`、`--control-port`、`--frp-port`、`--http-port`、`--port-range`、`--advertise-frp-addr`、`--data-dir` 和对应 `YCY_TUNNEL_*` 环境变量可继续用于 **Local Node**。当前默认值来自[`ResolveServerConfig`](../../pkg/cmd/tunnel/server/run.go)。新版首次部署选择**新的空 Server 状态目录**；旧 v1/v2 或未知 SQLite 目录不自动迁移、覆盖或备份，启动前拒绝打开。主 Server X25519 Controller 私钥单独保存在该私有状态目录的固定身份文件，不放进 SQLite；启动时与新库记录交叉核对。身份文件或库缺失/不匹配时按[Server 存储契约](./server-storage-transactions.md)处理，不能偷偷换钥。容器的 Server 卷和每个 Node 卷必须分开且持久，不能把 Node 身份卷挂给其他 Node。

现有 [`Dockerfile`](../../Dockerfile) 以 `ycy` 为入口，并打包固定 FRP 二进制；未来 Server 和 Node 示例都使用**同一精确发行版 tag**，不能像现有 [`docker-compose.tunnel.yml`](../../deploy/docker-compose.tunnel.yml) 那样默认 `latest`。以下是两个**分别运行在 Server 主机和 Node 主机**的 Compose 草模，旧镜像现在无法运行 Node 示例：

```yaml
# Server 主机；完整新部署使用新卷名，不能复用旧 tunnel-data
services:
  tunnel-server:
    image: ghcr.io/hackycy/hackycy-cli:${YCY_VERSION:?pin-one-release}
    command: [tunnel, server]
    restart: unless-stopped
    environment:
      YCY_TUNNEL_DATA_DIR: /data
      YCY_TUNNEL_ADMIN_PASSWORD: ${YCY_TUNNEL_ADMIN_PASSWORD:?required}
      YCY_TUNNEL_ADVERTISE_FRP_ADDR: server.example.com:7000
    volumes: [tunnel-server-v3:/data]
    ports:
      - "7500:7500/tcp"
      - "7000:7000/tcp"
      - "8080:8080/tcp"
      - "20000-20100:20000-20100/tcp"
      - "20000-20100:20000-20100/udp"
volumes:
  tunnel-server-v3:
```

```yaml
# Node 主机；与上方使用同一 YCY_VERSION，但不是同一状态卷
services:
  tunnel-node:
    image: ghcr.io/hackycy/hackycy-cli:${YCY_VERSION:?pin-one-release}
    command: [tunnel, node]
    restart: unless-stopped
    environment:
      YCY_TUNNEL_NODE_DATA_DIR: /data
      YCY_TUNNEL_NODE_MANAGEMENT_BIND_ADDRESS: 0.0.0.0
      YCY_TUNNEL_NODE_MANAGEMENT_PORT: "7600"
    volumes: [tunnel-node-data:/data]
    ports:
      - "7600:7600/tcp"             # Server → Node 的 Noise/HTTP 管理
      - "7000:7000/tcp"             # Client FRPC → Node FRPS
      - "8080:8080/tcp"             # HTTP Tunnel 入口
      - "20000-20100:20000-20100/tcp"
      - "20000-20100:20000-20100/udp"
volumes:
  tunnel-node-data:
```

在 Node 主机上开放公网可达的管理 TCP 7600 给主 Server，FRP TCP 7000 给被分配 Client，HTTP TCP 8080 和实际使用的 TCP/UDP 池给访问者。Management Address 在 Server 管理页填 `http://<Node 公网 IP>:7600`；FRP 对外地址填 `<Node 公网 IP>:7000`；HTTP 对外入口填 `<Node 公网 IP>:8080`。若 FRP/HTTP 固定端口在宿主机映射为其他端口，分别填**宿主机公网端口**；端口池映射保持端口号一致，以免 Tunnel 的 `serverPort` 与实际公网端口不符。管理端口即使通过公网 HTTP 开放，配置、Token 和状态仍只在 Noise 会话中传输；首次有效认领者抢占风险按[认领决议](./issues/01-claim-trust.md)接受，管理员应核对 Node 本机显示的完整指纹。`GET /health` 仅证明管理进程可响应，不代表 FRPS 正在运行或已收敛。

## 破坏性部署步骤与可观察结果

1. 在停机窗口停止旧 Server 与旧 Client，**原样保留旧 Server 状态目录/卷**。新版不自动备份、迁移旧 SQLite 或导入旧账号、Client Token 和 Tunnel；有原目录才可用旧版与原目录手工回退。不要让新 Server 指向旧目录试图“升级”。
2. 选定同一个新发行版 tag，准备新的空 Server 状态目录和每台 Node 各自持久的空目录。Server 的 Controller 身份文件与新库同卷保存；Node 的身份/SQLite/FRPS 缓存卷不随容器重建删除。两个实例不能共享同一 Node 卷或 Server 卷。
3. 启动新版 Server，确认空库建库、Local Node 与 Local FRPS 可用；重新建立管理员、Client、Token 和 Tunnel。旧 Client 控制协议 v4 不兼容，新 Client 必须按 v5 重新取得新版 Client Token。Server 控制面与 Node 管理面使用不同端口和身份体系。
4. 用同一版本启动 Node，记录本机打印的完整指纹；从 Server 面板预览公网 HTTP 管理地址、核对指纹并认领。认领后另填该 Node 的 FRPS bind、HTTP vhost、端口池以及两个对外地址；等配置已应用且 FRPS 新鲜观察为运行，再分配 Client。DNS 可后设，但 HTTP 域名应指向 Node 的 HTTP 公网入口。
5. 用新版官方 Client 连接主 Server；控制 `welcome` 给出当前 Node 的完整运行配置，FRPC 连接所属 Node。验证控制连接、Node FRPS、FRPC 登录和代理注册分别正常；切换 Node 或轮换 Token 时允许短时重连，不承诺零中断。

新 Server 遇旧/未知库须在任何 schema 写入前拒绝并保持数据库及 WAL/SHM 字节不变。已认领但错版的 Node 不作为新切换目标；它可能仍运行缓存 FRPS，面板显示“管理不兼容、远端运行未知”，不能显示“已停机”。Controller 身份文件丢失时不生成另一身份冒充原 Controller；Node 身份卷丢失时生成新指纹，不能因为公网 IP:端口相同就替换旧身份。没有自动改绑或恢复旧 Controller 的流程。

## 验收层级与运行入口

以下入口是**实现完成后**应运行的门槛，而非声称当前代码已通过新场景。已有 `make check-web`、`make acceptance`、`make command-surface` 与 Go 测试框架；新 Node 命令需要更新命令面快照，不能通过关闭检查跳过。[现有 Local 转发集成测试](../../pkg/cmd/tunnel/server/client_forwarding_integration_test.go)是 Local 回归种子。

| 层级 | 入口与夹具 | 重点 |
| --- | --- | --- |
| 单元与状态机 | `go test ./pkg/cmd/tunnel/... ./internal/tunnelruntime/...`；临时私有目录、假时钟、假 Noise/FRPS 接点 | 参数优先级、状态事务、版本高水位、端口/域名约束、权限投影、结构化错误。 |
| 进程集成 | 同包 Go 集成测试，固定本仓库 FRP 构建、临时 SQLite、真实环回 HTTP/WebSocket、真实 FRPC/FRPS | 认领、配置、Local/Remote 转发、Client 重连、Token 轮换、停止/回滚。进程退出后重启相同目录观察实际 FRPS。 |
| 独立二进制 | 扩展 `acceptance/tunnel_test.go`，由 `make acceptance` 运行；`make command-surface` 检查唯一 Node 子命令和帮助 | 真实 CLI 旗标/环境变量、退出码、错版、旧库原样拒绝、日志脱敏。 |
| Web | `make check-web`、现有浏览器验收入口扩展 Nodes/Client 页面 | 普通用户与 admin 投影、指纹确认、待移除、错误下一步、SSE 断线后重取。 |
| 容器及公网烟测 | 新 Server/Node Compose 示例分别在两台主机或等价隔离网络运行，固定同一镜像 tag；真实公网 IP:端口与防火墙 | 持久卷重建、Host 端口映射、TCP/UDP/HTTP 路由、HTTP DNS 提示。该环境是发布前部署验收，不由环回单测冒充。 |

## 故障注入和验收矩阵

每条测试都记录 Server 的持久归属/期望版本、Node 的持久绑定/高水位/最后成功版本、实际 FRPS/FRPC 进程、Web/API 投影；不可只断言 HTTP 2xx 或进程存活。

| 场景 / 注入点 | 层级 | 必须观察到的结果 |
| --- | --- | --- |
| 仅 Local 的新空目录；HTTP、TCP、UDP 分别转发，Client 控制重连 | 集成 + 二进制 | 默认 Client 归 Local；实际请求往返成功；Local FRPS 沿启动设置运行。 |
| 旧 v1/v2、无版本、未知/损坏 SQLite；存在 WAL/SHM | 状态单测 + 二进制 | 新 Server 在写入前拒绝，文件字节不变；新空目录建 v3 幂等。 |
| Node 首次预览、完整指纹核对、双 Controller 并发 `claim` | Noise 集成 + 进程 | 预览不绑定；仅首个有效加密 claim 持久成功，第二方只得通用已认领错误。 |
| 错指纹、改 IP 后另一身份、Server 认领回执丢失 | Noise 集成 + 进程 | 不暗换身份；原 Controller 经新会话查询并显式重新添加，不能盲目再认领。 |
| Noise 旧序号/密文重放、丢响应、过期会话、分块过大或乱序 | 协议单测 + 进程 | 不重复执行；新会话查询结果；长度/序号拒绝，Node 旧状态不变。 |
| Node 进程与容器重建复用卷；卷丢失；未知/损坏库 | 进程 + 容器 | 复用卷保留指纹/绑定和最后成功配置；丢卷产生新身份，旧 Server 识别不匹配；损坏库不自建身份。 |
| Server→Node 管理断线、错版/FRP 版本不符 | 进程 + 容器 | Node 沿最后成功 FRPS 运行；管理和运行分别显示；错版不允许新分配，也不报告远端已停机。 |
| 配置验证失败、候选 FRPS 启动失败、回滚失败、响应丢失 | 故障注入 + 真实 FRPS | 验证失败旧 FRPS 不停；启动失败回滚旧配置并报失败；回滚失败真实运行状态为不可用；查询持久版本可消解响应丢失。 |
| 在 Node 身份、绑定、候选应用、lastGood、停用各事务点 `SIGKILL` 后重启 | 进程故障注入 | 符合[崩溃恢复表](./node-state-crash.md#崩溃点检查)；不能低版本覆盖，停用意图后绝不复活旧 FRPS。归属不明残留进程保留并报错。 |
| 在线 Local→Remote、Remote→Remote 切换；目标离线；Client 首次 FRPC 登录失败 | 集成 + Web | 离线预检拒绝且旧归属保留；已提交目标固定新 Node，官方旧 FRPC 停止，新目标失败不自动回旧 Node；归属/应用/登录分栏显示。 |
| Client 控制离线时选择目标，重连时目标仍不可用或发生资源冲突 | 集成 + Web | 只保存 pending，旧归属和资源不变；重连重检，失败保留 pending 并说明原因，可取消/替换。 |
| 跨 Node 同数值 TCP/UDP 端口、同 Node 冲突、整域 hostname 竞争、DNS 未就绪 | 事务并发 + 容器 | 不同 Node 端口可复用，同 Node/整域冲突事务回滚；DNS 不阻止保存但面板给目标地址提示。 |
| 新端口池已保存但 Node 未应用；缩小范围包含禁用 Tunnel | 事务 + Web | 新范围立即作为 Server 分配依据，应用状态单独显示；缩小若排除任何已占用端口则拒绝。 |
| Node Token 轮换：应用失败、Node 已确认但 Server 晋升前崩溃 | 进程 + 故障注入 | 失败时 Client 仍得旧 active Token；重启后查询 Node 并晋升/通知当前分配 Client；其他 Node 不受影响。 |
| Remove 有当前/待切换 Client、远端离线、停用确认丢失；Force Forget | 集成 + 故障注入 + Web | 有依赖拒绝；离线保持 `removing`，远端持久停用且 FRPS 停止后才删除；强制遗忘只清 Server 记录并显示旧 FRPS 风险。 |
| v4 Client、错版 Node、普通用户访问管理员 API、他人 Client | 二进制 + HTTP + Web | 稳定错版/权限错误，Node 不泄露原 Controller/Token，其他用户 Client 按现有 owner 边界返回 404。 |

发布前至少完成以上自动化层和一次真实公网地址的部署烟测；记录实际版本、FRP 构建、网络端口、测试报告和仍存在的预期中断。这里是验收计划，未实施也未执行这些新增测试。
