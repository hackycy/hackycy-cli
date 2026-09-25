# Node 管理 API 与页面状态契约如何具体定义？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: prototype
Status: resolved
Blocked by: 08, 09, 11, 13, 14

## Question

基于[权限决议](./08-management-api-permissions.md)和已接受的[可点击流程草模](../nodes-flow-prototype.html)，给出主 Server 对 Web UI 的请求/响应契约：Node 公共摘要与管理员详情、指纹预览和认领、编辑对外地址与 FRPS 目标配置、当前/待切换 Client 归属、正常 Remove/Force Forget、可操作性判定及结构化错误。明确何时返回状态、何时创建异步操作，如何区分管理连通性、FRPS 运行、配置收敛、Client FRPC 登录与最后观察时间。

所有登录用户都可查看和选择全部 Node，普通用户仅能改变自己的 Client；管理地址、身份、Token 和详细故障按既定权限投影。Node 自身的公网 HTTP 面只提供 Noise 保护的管理能力，不提供明文业务 API。草案需与既有 Server HTTP 路由与前端数据流可接合。

## Artifacts

- [主 Server Web UI API 契约](../node-api-contract.md)：请求/响应、权限、异步收敛、错误及与当前代码接点。
- [可点击状态草模](../node-api-state-prototype.html)：查看 API 返回后 Node、Client 各层状态怎样继续变化；纯内存演示，不是生产实现。

## Answer

- 用户确认按[主 Server Web UI API 契约](../node-api-contract.md)定稿。沿用现有 `/api/...`、会话与同源写入保护、`{version,error:{code,message,details?}}`、`/api/events` 刷新形状。所有登录用户读 Local/Remote Node 公共摘要，所有 admin 读管理详情并管理 Node；普通用户只可变更自己 Client 的归属。Node 公网管理面仍仅使用已决议的 Noise/HTTP 协议，不能暴露明文业务 API。
- 管理员先预览 Node 完整指纹，再确认首次认领或显式重新添加原 Controller 已绑定的 Node；确认时重新握手核对同一身份。认领返回 `201` 只代表 Node 绑定与 Server 记录完成，不代表 FRPS 可用。管理地址变更先持久保存待验证地址，验证固定 Node 公钥后才切为生效地址；不允许借地址变更替换身份。
- Node 配置/地址保存返回 Server 已提交的期望版本；远端应用、FRPS 运行与 Client FRPC 登录分别通过带观察时间的状态查询和事件刷新呈现。Node 端口池保存即为 Server 资源分配依据，不要求期望与已应用版本相等才可分配；切换目标仍需新鲜管理与 FRPS 运行预检。在线 Client 切换在 Server 事务提交后返回当前归属；离线只写待切换并返回 `202`。已提交新归属后的 FRPC 登录失败不自动回旧 Node。
- 正常 Remove 无当前/待切换 Client 后持久写入 `removing` 和停用快照，返回 `202`，仅在 Node 确认持久停用且 FRPS 停止后从列表消失。故障或待移除的 Node 可以由管理员再次确认风险并强制遗忘；这只删 Server 记录。配置、Token 轮换和移除的异步进度通过既有持久状态表示，不另建一套 operation 记录；结构化错误码驱动页面下一步，公开投影不泄露管理地址、指纹、Token 或详细诊断。
