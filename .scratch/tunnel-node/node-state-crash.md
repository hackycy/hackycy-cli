# Node 本地状态与崩溃恢复契约

状态：已按[“Node 本地状态与 FRPS 应用怎样保证崩溃后一致？”](./issues/12-node-state-crash-consistency.md)决议。已确定的离线运行、验证后切换和持久停用语义仍以[期望状态决议](./issues/04-node-desired-state.md)为准。

## 唯一持久权威

Node 在私有持久目录使用一个**仅保存自身运行状态的 SQLite 库**，不保存账号、Client、Tunnel 或其他业务数据。绑定、高水位、有效配置和停用意图由同一数据库事务提交。目录权限 0700，库及其 WAL/SHM 文件仅当前用户可访问；Windows 采用对应的私有 ACL。使用 WAL 与 `synchronous=FULL`，[SQLite 文档](https://www.sqlite.org/pragma.html#pragma_synchronous)说明该组合在提交时同步 WAL，以保留断电后的提交耐久性。[WAL 官方限制](https://www.sqlite.org/wal.html)要求数据库位于本机文件系统，Node 容器卷不能用跨主机共享的网络文件系统。Node 的 FRPS 配置文件、404 页面和下载的二进制是可重建的工作文件，不是配置权威。只接受明确支持的库格式版本；未知或损坏的现有库停止启动，不能自动清空或生成新身份。

| 状态 | 保存内容 | 生效原则 |
| --- | --- | --- |
| Node 身份 | X25519 私钥、公钥、稳定 Node ID | 首次空目录以事务建库；提交前不对外展示或用于认领。仅整个目录被操作员手工清理后才生成新身份。库丢失/损坏但目录仍有其他状态时拒绝自动生成新身份。 |
| Controller 绑定 | 固定 Controller 公钥 | 首个有效 `claim` 在事务中写入；事务提交后才回成功；重启与普通 Remove 均保留。 |
| 版本高水位 | `highestAcceptedRevision`、原始快照 SHA-256 | 完整快照通过身份、格式和摘要检查后，与待应用内容同事务保存。低版本和同版本异内容不可覆盖。 |
| 最后成功运行 | `appliedRevision`、完整有效快照（含该 Node Token 和 404 内容） | 仅候选 FRPS 启动确认后原子替换；重启优先从此快照渲染并运行。实时进程状态不从版本推断。 |
| 待应用/失败 | 候选快照、应用阶段、最近结构化失败 | 失败版本保留高水位与原因；重启看见中断的应用时先恢复最后成功运行，等原 Controller 重新提交同版本同摘要继续处理。 |
| 停用意图 | `bootDisabled`、停用进度/确认 | `disabled` 快照先持久写 `bootDisabled=true`，再停 FRPS。只要此标记存在，重启绝不从旧运行快照启动。 |

`frps.toml` 和 404 页面从快照渲染到权限为 0600 的私有候选文件。数据目录与 Node 的独占实例锁覆盖整个进程生命周期，防止同一卷并发启动两份 Node。配置 Token 不写普通日志或状态响应。正常停用完成后删除可运行的配置和 Token 的**有效引用**；SQLite 删除与 checkpoint 不能承诺物理介质上的彻底擦除，运维备份仍可能含旧凭据。

## 接受一份 running 快照

```text
完整分块 + 摘要正确
  → 校验 Controller/Node/格式/FRP 版本
  → 事务提交 highestAcceptedRevision、摘要、待应用快照
  → 私有候选文件渲染 + frps verify（旧 FRPS 继续运行）
  → 停旧 FRPS → 启动候选 → 等启动确认
  → 事务提交 lastGood=候选、appliedRevision、新结果
  → 回成功
```

验证失败时不触碰旧 FRPS，事务记录该版本失败。候选启动失败或最终状态事务提交失败时，停候选并尝试从 `lastGood` 重启旧 FRPS；回滚失败则报告 `rollback_failed` 和实际进程不可用。一个 Node 同时只应用一份快照。候选已接受却因 Node 崩溃而没有最终结果时，同版本同摘要可**恢复这次未完成的应用**；已有最终结果则只返回该结果。想在环境修复后重新尝试一个已记录失败的版本，Server 应发更高版本。若有归属不明的残留 FRPS，恢复和继续应用均暂停，直到人工排除冲突。

## 接受一份 disabled 快照

```text
完整快照校验
  → 事务提交更高版本、bootDisabled=true、停用待完成
  → 停止 FRPS 并确认其不在运行
  → 删除有效运行配置/Token 引用
  → 事务提交 disabledComplete、appliedRevision
  → 回成功；Server 此时才能完成正常 Remove
```

停用意图先落盘，是为了 Node 在任意后续崩溃点重启时不会复活旧 FRPS。若停进程失败、残留进程无法确认或最终事务失败，Node 保持待停用/失败，Server 不得把 Remove 显示为完成。绑定仍保留原 Controller。

## 崩溃点检查

| 崩溃时刻 | 重启后的动作与对 Server 的结果 |
| --- | --- |
| 首次身份事务提交前 | 只有确认此前身份从未持久提交或对外展示，且库处于可识别的初始状态，才可重试建库；其他残留文件或损坏/未知库一律停止并要求人工检查，不能默默生成另一身份。 |
| `claim` 提交前 / 后但回执前 | 前者仍未认领；后者仍绑定同一 Controller，原 Controller 新会话查询即可确认。 |
| 快照分块到一半或尚未接受 | 丢弃临时分块；高水位和旧 FRPS 不变。 |
| 已接受 running 新版本，尚未停旧进程 | 启动/继续最后成功配置，标记该候选为中断；等原 Controller 重试同摘要。 |
| 停旧后、候选启动中或成功但尚未提交 `lastGood` | 先处理残留进程，再从数据库中的旧 `lastGood` 启动；候选不冒充已应用。 |
| 新 `lastGood` 已提交，成功回执未送达 | 从新配置启动；Server 重新连接后读取 `appliedRevision`，不重复切换。 |
| 停用意图提交前 / 后但未确认 | 前者按旧 `lastGood` 启动；后者先检查并停止残留 FRPS，不启动旧配置，直到持久停用完成才回成功。 |
| `disabledComplete` 已提交，回执未送达 | 继续保持 FRPS 停止和绑定，Server 查询后可完成 Remove。 |

## 子进程所有权边界

当前 [Unix FRPS 子进程](../../internal/tunnelruntime/frp_supervisor_unix.go)只设置独立进程组；Node 进程被强制杀死时，FRPS 可能继续运行。[Windows 实现](../../internal/tunnelruntime/frp_supervisor_windows.go)使用关闭即终止的 Job Object。Node 重启必须先处理已识别的旧 FRPS 子进程，再按上述持久状态启动；记录 PID、启动时间、二进制/配置身份等可核对信息，**仅能确认是自己留下的进程才清理**。若不能可靠确认，就保留残留进程，不能仅凭占用端口杀进程；Node 报告 `FRPS_OWNERSHIP_UNKNOWN` 或端口冲突，不报配置已应用/停用成功，由操作员检查。容器重启通常会清理容器内进程，但不能以容器行为替代裸机的安全边界。

## 实现及验证接点

Remote Node 新建调和器和持久存储层；复用 [`FRPSupervisor`](../../internal/tunnelruntime/frp_supervisor.go)、[`RenderFRPSConfig`](../../internal/tunnelruntime/frp_toml.go)及 FRP 二进制准备能力。不要调用 Server 专用 [`ManagedFRPS.start`](../../pkg/cmd/tunnel/server/server_frps.go)，因为它当前先停旧进程再验证，且无远端回滚。测试需在上述每个事务和进程切换边界注入中断，再以相同持久目录重启，断言身份、高水位、有效配置、停用标记和实际 FRPS 状态；特别检查丢失回执和无法安全识别残留进程。
