# Node 管理 API 与页面状态契约

状态：已按[“Node 管理 API 与页面状态契约如何具体定义？”](./issues/15-node-management-api-contract.md)及用户“按草稿定稿”的反馈确定。与[Nodes 页面草模](./nodes-flow-prototype.html)配套；本页是主 Server 的 **Web UI API**，公网 Node 的 Noise/HTTP 帧仍以[管理线契约](./node-management-wire.md)为准。下述路径、方法、返回时点、权限和状态投影作为实现契约。

## 与现有代码的接点

- 现有 [`ServerHTTPHandler`](../../pkg/cmd/tunnel/server/server_http.go) 以 `/api/...` 分发路由，浏览器写请求使用同源校验、会话 Cookie 和 `{version,error:{code,message}}`；新增 Node 路由维持这个外壳。`/api/agent` 仍仅供 Client 控制 WebSocket 使用，不能变成 Node 公网管理入口。
- [`ServerWorkspace`](../../pkg/cmd/tunnel/server/server_workspace.go) 已按 `admin` 和 Client owner 划分权限；Node 读写加在服务层，HTTP 仅投影和校验输入。所有登录用户读 Node 公共摘要；管理员读管理详情、写 Node；Client 归属写操作沿现有 owner 检查。
- 现有 [`/api/events`](../../pkg/cmd/tunnel/server/server_http_events.go) 发 `changed` 后，前端刷新 [`/api/state`](../../web/tunnel-server/app.tsx) 和当前资源；Node 保存、调和观察、Client 应用/FRPC 观察变化同样触发受权限投影的刷新。事件不带秘密，也不是唯一交付机制；重新打开页面或 SSE 断线后重新 GET。
- 前端 [`apiJson`](../../web/tunnel-server/api.ts) 目前只保留错误文案，实施时需要保留 `error.code`、`details` 供稳定动作提示；不要用字符串匹配错误文案。

## 资源与状态投影

`GET /api/nodes` 返回 `{version:1,nodes:NodeSummary[]}`，所有登录用户可读；`GET /api/nodes/{id}` 返回同一 `NodeSummary`，不存在为 404。`GET /api/nodes/{id}/management` 仅管理员，返回 `{version:1,node:NodeManagementView}`。Local Node 在公共列表里；其管理详情为只读的本地 FRPS 观察和启动配置投影，没有远端管理地址、指纹、Remove 或远端配置编辑动作。普通用户访问 `/management` 返回 403，不能靠前端隐藏链接实施权限。

```jsonc
// 公共摘要；Node 列表和 Client 的当前/待切换 Node 引用同一投影
{"version":1,"nodes":[{
  "id":"node-hk","name":"Hong Kong 01","kind":"remote","lifecycle":"active",
  "advertisedFrpAddress":{"host":"hk.example.com","port":7000},
  "httpIngressAddress":{"host":"hk.example.com","port":8080},
  "management":{"state":"reachable","observedAt":"2026-09-23T09:10:00Z"},
  "frps":{"state":"running","observedAt":"2026-09-23T09:10:00Z"},
  "selectability":{"selectable":true,"reason":null}
}]}

// 管理详情增加的字段；公开字段同上
{"id":"node-hk","managementAddress":"http://192.0.2.10:7600",
 "nodeFingerprint":"SHA256:...","controllerFingerprint":"SHA256:...",
 "desired":{"revision":12,"digest":"sha256:...","mode":"running","settings":{}},
 "observed":{"highestAcceptedRevision":12,"appliedRevision":11,
   "configuration":"apply_failed_old_running","frps":"running",
   "observedAt":"2026-09-23T09:10:00Z","error":{"code":"NODE_APPLY_FAILED","phase":"start","revision":12}},
 "tokenRotation":{"state":"idle"},"removal":{"state":"none"}}
```

`management.state = reachable | unreachable | unknown | incompatible | identity_mismatch`；`frps.state = running | stopped | failed | unknown`。服务端判定观察是否新鲜，返回 `observedAt` 和 `stale: boolean`（示例省略）；Server 重启时旧观察一律 `stale=true`，不可据此宣称 Node 在线。Node 离线时 `frps.state=unknown`，上次“running”仅放在管理员历史观察中或公共 `lastKnownFrps`（带时间、明确标为历史），不当作当前运行。Local 的 `management` 可为 `local`，不假装经过公网握手。`configuration` 独立表示 `unconfigured | pending | converged | rejected | apply_failed_old_running | rollback_failed | unknown`，由期望版本、Node 持久回报和新鲜观察共同决定；公开摘要不暴露详细错误、版本、Token、身份及管理地址。

`selectability` 由 Server 统一计算，列表与写入请求共用同一规则：仅 `active`、目标对外 FRP 地址完整、管理连接新鲜且协议/身份匹配、FRPS 新鲜地报告运行时为可选。配置 `pending` 或新版本失败但旧 FRPS 仍运行时，不额外要求期望版本和已应用版本相等；保存后的端口池已是 Server 资源依据，页面另行警告配置未收敛，实际代理可能暂不可用。Client 目标预检重新检查当前健康与资源，不能信任列表布尔值。`reason` 是安全枚举，例如 `unconfigured | node_offline | frps_stopped | protocol_incompatible | identity_mismatch | removing`，普通用户可见；管理诊断仅管理员可见。待移除 Node 继续列出，显示 `selectable=false`。

## 写接口与返回时点

所有写接口先验证会话、角色/Client owner 和同源 Origin，再严格解码 JSON。下面的 200/201/202 表示 **Server 已持久提交所述状态**；Node 是否收敛、Client 是否应用、FRPC 是否登录另读状态。网络预检不跨数据库写事务。管理动作没有第二套持久 `operation` 表：Node `lifecycle`、`desired.revision`、Client `pendingNodeId` 和轮换状态就是可重启恢复的操作状态。

| 动作 | 建议方法与输入 | 返回和后续 |
| --- | --- | --- |
| 预览身份 | `POST /api/nodes/claim-previews`，`{managementAddress}`，admin | `200 {previewId,managementAddress,nodeFingerprint,expiresAt}`。仅 Noise XX 前两帧，不占认领权。预览句柄短期保存在 Server 内存，绑定当前会话和规范化地址；换地址、过期或 Server 重启须重新预览。 |
| 首次认领 | `POST /api/nodes`，`{previewId,name,confirmedFingerprint,mode:"claim"}`，admin | 重新 Noise 握手并比对完整公钥；Node 持久绑定且 Server 记录提交后才 `201 {node:...}`。只建立未配置 Remote Node，不声称 FRPS 可用。认领回复丢失或 Server 记录提交失败时不重试“首认领”，需重新预览并显式重新添加。 |
| 原 Controller 重新添加 | 同 `POST /api/nodes`，`mode:"readd"`，admin | 再次核对指纹，并用 Server 原 Controller 身份读取 Node 持久绑定与高水位；核实一致才 `201`。不能按 IP 或相似指纹自动找回，Node 身份未清除且不改绑。 |
| 改名与公开/管理地址 | `PATCH /api/nodes/{id}`，仅出现的字段更新：`name`、`managementAddress`、`advertisedFrpAddress`、`httpIngressAddress`；admin | `200 {node:...}`，只表示 Server 保存。管理地址变化先保存为待验证地址，保留原已验证地址供当前调和；后台以固定 Node 公钥验证，成功才激活新地址，失败时详情显示原因，不能暗中换身份。对外 FRP 地址改变会在同笔事务递增受影响 Client 完整目标版本；HTTP 入口变更生成 DNS 提示。 |
| FRPS 期望配置 | `PUT /api/nodes/{id}/desired`，`{expectedRevision,settings}`；admin | 校验端口池/既有资源后保存完整 `running` 快照；`200 {desiredRevision,configuration:"pending"}`。端口池**此时**即成为 Server 分配依据；Node 离线也可保存。`expectedRevision` 不符为 409，避免管理员用过期表单覆盖新快照。 |
| 重应用/重启 | `POST /api/nodes/{id}/reapply`；admin | `200 {desiredRevision,configuration:"pending"}`，以更高版本重送完整 `running` 快照；不另发命令式重启。 |
| FRP Token 轮换 | `POST /api/nodes/{id}/frp-token/rotate`；admin | `202 {desiredRevision,tokenRotation:{state:"applying_to_node"}}`。Node 确认后 Server 晋升 Token、通知 Client；失败保留旧 active Token 并展示阶段。响应不回显 Token。 |
| 正常移除 | `DELETE /api/nodes/{id}`；admin | 无当前/待切换 Client 后写 `lifecycle=removing` 与更高版本 `disabled` 快照，`202 {nodeId,removal:{state:"pending",desiredRevision}}`；即使 Node 离线也能排队。读详情直到远端持久停用且 FRPS 停止；完成后从列表消失。重复 DELETE 保持 202。 |
| 强制遗忘 | `POST /api/nodes/{id}/force-forget`，`{confirmNodeId}`；admin | 仍须无当前/待切换 Client，且处于待移除或有管理不可达/身份错误/协议错误等无法正常完成停用的故障；仅删除 Server 记录，`204`。UI 先显示远端可能继续运行、旧凭据仍可能有效且 Node 本机仍绑定；Local 和正常可管理的活动 Node 拒绝。 |
| 选择或替换目标 | `PUT /api/clients/{id}/node-assignment`，`{nodeId}`；Client owner 或 admin | 在线时新鲜预检并在一笔事务迁移资源、提交归属，`200 {assignment}`；Client 控制离线时只持久保存待切换，`202 {assignment}`。即使后续 FRPC 登录失败，已提交 Node 不自动回退。 |
| 取消待切换 | `DELETE /api/clients/{id}/node-assignment/pending`；owner 或 admin | `200 {assignment}`；只清 pending，不改变已提交归属或已运行 FRPC。没有 pending 时幂等返回当前归属。 |

`pendingManagementAddress` 需持久保存以跨 Server 重启继续验证；它不是绑定身份或实际拨号地址。验证失败时保持原地址和待验证候选，管理员可覆盖候选再试。Node 长期公钥在整个过程中不变。

## Client 归属与四层状态

`GET /api/clients` 和 `GET /api/clients/{id}` 在既有 Client 投影中增加：

```jsonc
{"assignment":{
  "nodeId":"node-la","pendingNodeId":null,"pendingSince":null,
  "node":{"id":"node-la","name":"Los Angeles","advertisedFrpAddress":{"host":"la.example.com","port":7000},"frps":{"state":"running","observedAt":"..."}},
  "pendingNode":null,
  "desired":{"revision":9,"nodeId":"node-la"},
  "clientAccepted":{"revision":9,"nodeId":"node-la","observedAt":"..."},
  "clientApplied":{"revision":9,"nodeId":"node-la","state":"started","observedAt":"..."},
  "frpc":{"nodeId":"node-la","connection":"disconnected","observedAt":"...","proxies":[]}
}}
```

普通用户仅从自己的 Client 详情看到当前或待切换 Node 的**名称、对外连接地址、运行状态**；额外版本和 FRPC 故障状态仅归该 Client 本人或管理员可见，不把管理地址/指纹/Token 带到 Client 投影。所有登录用户的 Node 选择器单独请求 `/api/nodes`，但选择行为仍需 owner 权限。Client 与 Server 失联时 `clientApplied`/`frpc` 的上次值带 `stale=true`，不能把历史连接当成当前连接；“控制连接”“已接收目标”“本地已应用”“FRPC 登录”“代理注册”分别呈现。`pendingNodeId` 存在时 `nodeId` 仍是旧归属；重连若目标失效，pending 保留并附稳定原因。Server 事务提交后但通知尚未送达，`desired.nodeId` 是新 Node，`clientApplied.nodeId` 可能仍是旧 Node，页面标为“等待 Client 停旧连接”。

## 结构化错误与页面动作

保持 `{version:1,error:{code,message,details?}}`。`message` 是安全、可显示的人类说明；`details` 只放不敏感的字段（如 `nodeId`、`desiredRevision`、`phase`、`resourceKind`），不直接透出 Node 返回的任意文本。HTTP `401` 未登录；`403` 无权限/跨站写入；`404` 不存在或他人的 Client；`400` 输入非法；`409` 生命周期、身份、版本或资源冲突；`503` 本次必须的远端预览、认领、在线切换预检不可达。**已保存配置的远端应用失败不改变先前 200 为 5xx**，而是写入 Node 观察与错误状态并触发事件。

| 错误码 | 页面行为 |
| --- | --- |
| `NODE_UNREACHABLE`, `NODE_OFFLINE`, `NODE_PROTOCOL_INCOMPATIBLE` | 预览/认领/在线切换不可继续；详情区分远端运行未知与确定停机。 |
| `NODE_ALREADY_CLAIMED`, `NODE_IDENTITY_MISMATCH` | 停止确认；提示核对本机指纹。`NODE_ALREADY_CLAIMED` 可进入“原 Controller 重新添加”流程，但须重新验证身份。 |
| `NODE_CLAIM_OUTCOME_UNKNOWN` | 认领回执丢失或 Server 保存失败；提示重新预览并显式重新添加，不能让管理员盲目再认领。 |
| `NODE_TARGET_UNAVAILABLE`, `NODE_RESOURCE_CONFLICT` | 切换不提交；原归属保持，资源冲突以 `details.resourceKind` 指向端口或域名。 |
| `NODE_HAS_CLIENTS`, `NODE_REMOVE_PENDING` | 不允许完成删除/新分配；待移除详情继续显示停用进度及强制遗忘入口。 |
| `NODE_CONFIG_REJECTED`, `NODE_APPLY_FAILED`, `NODE_ROLLBACK_FAILED` | 异步状态显示具体版本和阶段，保留历史 FRPS 运行事实；不能用错误码推断旧进程已停止。 |
| `REVISION_CONFLICT` | 刷新详情后重新编辑；不覆盖较新设置。 |

## API 行为核对场景

1. 预览 → 管理员核对 Node 本机完整指纹 → 确认认领：预览不会绑定；`201` 后列表显示“已绑定、未配置”。确认前 Node 换身份，返回 `NODE_IDENTITY_MISMATCH`，数据库不加入 Node。
2. 编辑扩大端口池且 Node 离线：`PUT desired` 返回 `200` 和新 revision，新范围立即可保存 Tunnel；管理详情显示旧 applied revision、远端运行未知，不能显示“已部署”。
3. Client 在线切到 B：`PUT assignment` `200` 后归属 B，若 Client 的 `frpc_status=disconnected`，页面显示 B 故障且不回 A。Client 离线时 `202` 只保留 pending，资源仍在 A。
4. Remove 离线 Node：`DELETE` `202`，列表显示待移除，恢复后调和器送 `disabled`，收到持久停用/停止确认才消失；Force Forget `204` 不向 Node 发停用。
5. Node Token 轮换：`202` 后先显示“Node 应用中”，Node 持久应用后显示“Client 凭据下发中”，最终以 Client 各自应用和 FRPC 登录状态为准。
