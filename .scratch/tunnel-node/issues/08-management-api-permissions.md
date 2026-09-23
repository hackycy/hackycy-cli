# 主 Server 的 Node 管理 API 和权限边界是什么？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: grilling
Status: resolved
Blocked by: 01, 02, 04, 07

## Question

决定主 Server 对管理员提供哪些最小操作：列出 Node、发起认领、编辑名称/对外 FRP 地址、分配 Client、更新配置、查看状态、正常移除及强制遗忘 Node 等；哪些操作需要环境管理员权限，普通账号能看见什么。远端 Node 自身的管理 API 应仅暴露哪些资源、哪些状态可匿名读取、如何避免泄露密钥或原 Controller 信息？定义认领失败（已被其他 Server 认领、指纹不符、不可达、协议不兼容）、配置失败、Node 离线与停用失败时 API 返回给 UI 的稳定错误和下一步动作。不能提供 Node 远程解绑或改绑 API。

参照：[现有 HTTP 路由](../../../pkg/cmd/tunnel/server/server_http.go)及已有后台权限模型。

## Comments

### 2026-09-23：已认领，第一轮待用户决定

代码核对：[`server_accounts.go`](../../../pkg/cmd/tunnel/server/server_accounts.go)目前只有 `admin` / `user` 两种角色；环境配置的管理员与后来创建的管理员都持有 `admin` 角色。[`server_workspace.go`](../../../pkg/cmd/tunnel/server/server_workspace.go)在每次业务操作重新验证会话，普通用户只能访问自己拥有的 Client/Tunnel；管理员能操作所有 Client、控制本地 FRPS、读取其 FRP Token 和配置。HTTP 写操作在[`server_http.go`](../../../pkg/cmd/tunnel/server/server_http.go)校验同源，错误以稳定代码返回。现有 `/healthz` 只表明主 Server HTTP 处理器可响应，不是 FRPS 健康承诺。

本轮仅需确定面向用户的权限范围：所有 `admin` 是否都能管理 Node；普通用户能否把自己 Client 分配到不同 Node；普通用户能看到哪些 Node 信息。管理协议是否匿名暴露详细状态、错误代码和 HTTP 路由形状属于可据已定安全边界设计的实现契约，不要求用户决定底层接口命名。

### 2026-09-23：用户第一轮回答

- 所有 `admin` 均可管理 Node；不区分环境管理员与其他管理员。
- 普通用户需要能为自己的 Client 选择 Node，但必须先得到对该 Node 的授权。Node 授权是用户账号与 Node 之间的权限，不等于 Client 已分配归属，也不授予认领或配置 Node 的权力。
- 普通用户在自己的 Client 页面只看当前或待切换 Node 的名称、对外连接地址和运行状态；不暴露管理地址、指纹、详细错误或凭据。

下一轮需补足授权默认值、撤销授权对已有 Client 的影响，以及选择 Node 时能否看被授权 Node 的简要候选列表。若没有候选列表，普通用户无法自行选择 Node；此处需要与第三项答案核对。

### 2026-09-23：用户撤回 Node 授权设计

用户明确撤回“按用户授权 Node”：**所有 Node 对所有用户可见，不做 Node 使用授权**，以降低复杂度。因此上一轮提出的默认授权、撤销授权、已授权候选列表问题全部作废，不引入授权表、授权管理 API 或授权状态。结合此前“普通用户也可以选择”与此轮“所有 Node 可见”，当前决议是所有已登录用户均可把**自己拥有的 Client** 选择到任何可用 Node；管理员可处理全部 Client。普通用户看到所有 Node 的公共摘要，但管理地址、身份指纹、凭据和详细管理错误仍仅管理员可见。

## Answer

### 主 Server 的操作与权限

- 继续使用现有 `admin` / `user` 两级账号权限，环境配置管理员和其他 `admin` 权限一致。Node 管理操作都要求已登录的 `admin`：预览身份指纹并确认认领、显式重新添加仍绑定本 Controller 的 Node、编辑远端 Node 名称和地址/期望配置、查看完整管理状态与错误、触发重应用/重启、轮换该 Node FRP Token、发起正常 Remove 或强制遗忘。Local Node 可查看，但不可删除；其 FRPS 静态配置仍以 Server 启动设置为准，不能经远端 Node 编辑接口另造权威。
- 所有已登录用户都可以列出 Local 与 Remote Node 的**公共摘要**，并在自己 Client 的选择器中看到全部 Node。摘要包括稳定 Node ID、显示名、Local/Remote 类型、对外 FRP 地址、HTTP 公网入口、是否可作为当前切换目标，以及有观察时间的 Node 连通性与 FRPS 运行状态。属于 `pending removal` 或不可用的 Node 要如实展示原因类别，不能伪装为可选；公开摘要不包含管理地址、Node/Controller 公钥或指纹、FRP Token、内部配置、详细错误、其他用户的 Client 数量或日志。Client 页面仍展示当前/待切换 Node 的公共摘要；选择器可展示全部候选 Node。
- 普通用户可为**自己拥有的 Client** 选择、切换或取消待切换目标 Node；管理员可操作任意 Client。选择任何 Node 都不需要额外授权或授权记录，但所有切换仍受[目标可用性和待切换规则](./05-client-node-assignment.md)以及[端口、域名资源约束](./06-node-resource-scope.md)限制。普通用户对自己 Tunnel 的现有创建/修改权限不因 Node 变化扩大为管理别人的 Client、认领 Node、配置 FRPS 或查看管理凭据。Server 必须在每次请求中验证会话、角色及 Client owner，不能仅靠前端隐藏按钮；使用既有同源检查保护浏览器写操作。
- Node 列表提供面向用户的摘要投影和面向管理员的详细投影；Client 视图引用同一稳定 `node_id`，把当前归属、待切换、Client 已应用版本、Client↔Node FRPC 状态与 Node 自身 FRPS 状态分开。在线、配置已应用及 FRPS 可用不能由一个布尔值代替；上次观察值附时间，Server 重启后的旧结果不能直接当成实时在线。

### 主 Server 管理 API 的最小契约

- 管理入口以“列出/读取 Node”“预览 Node 身份并确认认领”“编辑 Remote Node 的公开地址和期望配置”“查询运行/收敛状态”“正常移除/强制遗忘”为核心；Client 归属切换沿既有 Client 资源入口扩展。具体 HTTP 路径和方法在实施时按当前 `/api/...` 风格确定，不把路径字符串当成业务决议。
- 认领分为**预览**与**确认**：预览使用 Noise 握手得到 Node 公钥指纹，面板供管理员与 Node 本机输出核对；确认请求必须携带预览时固定的目标身份，Server 再次验证该身份后才提交加密 `claim`。地址换成另一台 Node、指纹变化或确认前已被别人认领，都不能静默绑定。认领成功但确认响应丢失时，凭原 Controller 身份与 Node 指纹查询并显式补全 Server 记录；不能再以 IP:端口推断同一 Node。
- 编辑管理地址可以先保存待验证的新地址，后续连接仍要求已固定 Node 公钥一致；连接失败或指纹不符时保留原身份并展示错误，不自动替换 Node。编辑对外 FRP 地址要递增受影响 Client 的完整期望配置版本；编辑 Node FRPS bind、HTTP vhost、端口池、404 内容或 Token 要生成新的 Node 期望 `revision`，并按既定资源规则预检。若目标 Node 离线，Server 可保存期望设置等待收敛，但不能把“已保存”显示成“远端已应用”。DNS 尚未就绪可保存 HTTP 路由并提示公网入口。
- 远端重启以更高 `revision` 重应用完整 `running` 快照表达，不另设无持久语义的“重启 FRPS”远程命令。正常 Remove 必须先无当前或待切换 Client，再提交持久 `disabled` 快照；离线或停用未确认时返回/展示“待移除”，确认远端停用后才移出活动清单。强制遗忘也须无 Client，单独操作并明确提示“仅删除 Server 记录，远端 FRPS 可能继续运行”；不提供远程解绑、改绑、清理 Node 身份或恢复其他 Controller 的 API。

### 远端 Node 的公网管理面

- 未认证请求最多获得通用 HTTP 服务可达性/协议协商信息；健康探测只表示管理进程可响应，不提供 Node 配置、FRPS 状态、Controller 身份、Token 或错误细节。Node 公钥在 Noise 握手中供指纹核对；被认领的 Node 对其他 Controller 只给不泄露原 Controller 身份的通用“已认领”结果。
- 所有有意义的管理请求与响应，包括认领、读取状态、提交完整期望快照和停用确认，都必须经过[Noise 安全会话](./01-claim-trust.md)认证加密。已认领 Node 只接受固定 Controller 身份，状态响应只含身份、版本、进程观察和经脱敏的失败原因；不能回显 FRP Token 或私钥。Node 不提供账号、Client、Tunnel 业务读写接口，也不提供匿名 `/api/node` 详细查询、原始配置下载、FRPS start/stop/restart 或远程解绑接口。管理 HTTP 的 session ID、序号、幂等和重放边界沿用认领决议。

### 稳定错误与界面动作

主 Server 对管理操作返回稳定机器可读错误码，HTTP 4xx 表示输入、权限或状态冲突，5xx/503 表示不可达或应用失败；前端不解析任意错误文案判断状态。面向普通用户只展示对选择 Client 有意义的安全摘要；身份、管理地址及详细诊断限管理员。

| 场景 / 错误码 | 面板下一步 |
| --- | --- |
| `NODE_UNREACHABLE` / `NODE_OFFLINE` | 检查管理地址、Node 进程与网络；已有绑定和上次应用状态保持，不能提示重新认领。 |
| `NODE_ALREADY_CLAIMED` | 提示目标已被认领，不透露原 Controller；若确属本 Server，用原身份与指纹显式重新添加，否则需操作员到 Node 本机处理。 |
| `NODE_IDENTITY_MISMATCH` | 停止认领或地址更新，重新核对 Node 本机指纹和管理地址；绝不自动替换旧身份。 |
| `NODE_PROTOCOL_INCOMPATIBLE` | 显示 Server/Node 管理协议不匹配，部署同一受支持发行版；不能当成配置失败重试，也不能报告 Node 已停机。 |
| `NODE_CONFIG_REJECTED` / `NODE_APPLY_FAILED` / `NODE_ROLLBACK_FAILED` | 显示失败版本与阶段；修改配置或修复 Node 运行环境后提交更高版本。回滚也失败时明确显示 FRPS 不可用。 |
| `NODE_TARGET_UNAVAILABLE` / `NODE_RESOURCE_CONFLICT` | Client 不改归属；选择可用 Node 或解决端口池、端口、HTTP 域名冲突。 |
| `NODE_HAS_CLIENTS` / `NODE_REMOVE_PENDING` | 先改派 Client 并清除待切换；待移除时等待远端持久停用确认，或由管理员明确选择强制遗忘。 |

权限不足继续使用现有 `AUTHENTICATION_REQUIRED` / `FORBIDDEN`；普通用户访问不存在或他人 Client 时沿现有 owner 边界处理。所有错误不得包含 FRP Token、Noise 密钥、原 Controller 公钥或任意 Node 返回的未过滤文本；诊断信息保留在管理员可见的结构化状态中。
