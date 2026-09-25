# Node 本地状态与 FRPS 应用怎样保证崩溃后一致？

Parent: [Tunnel Node 接入设计地图](../map.md)
Type: prototype
Status: resolved
Blocked by: 02, 04, 11

## Question

为独立的 `ycy tunnel node` 设计持久状态布局与应用事务：长期身份、固定 Controller、公钥指纹、已接受最高 `revision` 和摘要、最后成功配置、失败结果、FRP Token、持久停用标记分别何时落盘；如何限制文件权限并在容器卷中恢复。给出认领落盘、验证候选配置、停止旧 FRPS、启动新 FRPS、提交成功、回滚、停用这几个阶段的崩溃矩阵，说明每个重启点会运行哪一份配置，以及高水位如何避免旧指令复活。

对照现有 `FRPSupervisor` 与配置渲染能力，明确复用边界，不直接把 Server 专用 `ManagedFRPS` 用作远端运行时。需要把[离线运行和回滚决议](./04-node-desired-state.md)变成可实现的状态机，并指出一次性测试应覆盖的故障注入点。

## Comments

### 2026-09-23：已认领，初版恢复草案

已核对当前代码：Unix FRPS 子进程只设置独立进程组，强杀父进程后可能残留；Windows 通过 Job Object 在句柄关闭时终止子进程。现有客户端有文件临时写入、同步和 rename 路径，Server 已依赖 SQLite。SQLite 官方[同步级别文档](https://www.sqlite.org/pragma.html#pragma_synchronous)说明 WAL + `synchronous=FULL` 的提交耐久性。初版[Node 状态与崩溃恢复草案](../node-state-crash.md)用仅存运行状态的本机 SQLite 将绑定、版本和有效配置作为一份事务状态；Node 仍不保存业务库。草案列出了认领、验证、启动、回滚和停用的崩溃点。

本轮需用户确定两项会影响运维预期的边界：是否接受 Node 内部使用 SQLite 保存私有运行状态；强杀后遇到无法可靠确认归属的残留 FRPS 时，是否按草案报错而不误杀其他进程。

### 2026-09-23：用户确认及契约定稿

用户确认 Node 可以使用本地 SQLite 保存自身身份、Controller 绑定和 FRPS 配置缓存；强制结束后若残留 FRPS 无法可靠确认归属，保留该进程并报错。已将草案整理为[Node 本地状态与崩溃恢复契约](../node-state-crash.md)，并与[管理线协议契约](../node-management-wire.md)统一同版本重试语义。

## Answer

- Node 私有持久目录中的 SQLite 是自身运行状态的唯一权威，保存长期身份、固定 Controller、已接受版本与摘要、最后成功快照、待应用/失败结果和停用意图；不保存账号、Client、Tunnel 业务数据。使用本机持久卷、独占实例锁、私有权限、WAL 和 `synchronous=FULL`。已有库未知或损坏时停止启动，不自动生成新身份；用户手工清理身份与状态后才可能重新认领。
- `claim` 绑定在单一事务提交后才返回成功。完整快照通过检查后，先在事务中提交版本高水位和候选，再验证 FRPS 配置；验证成功才停旧进程、启动候选，成功确认后提交 `lastGood` 和 `appliedRevision`。失败时保留/恢复旧配置并记录原因；恢复失败如实报告。崩溃中断的未完成版本可用同版本同摘要继续，已有最终失败结果的重试须提高版本。
- `disabled` 先持久提交 `bootDisabled=true`，再停止 FRPS、删除有效运行凭据引用并确认停止；完成事务提交后才向 Server 确认 Remove。停用未完成或状态不确定时不重启旧配置，也不报告移除成功。绑定继续保留。
- Node 重启时先核对残留 FRPS 归属。只能清理可靠识别为自己留下的进程；归属不明时保留进程、暂停恢复与应用、报告 `FRPS_OWNERSHIP_UNKNOWN` 或端口冲突，等待人工处理。崩溃点矩阵、进程识别和故障注入验收以[Node 本地状态与崩溃恢复契约](../node-state-crash.md)为准。
