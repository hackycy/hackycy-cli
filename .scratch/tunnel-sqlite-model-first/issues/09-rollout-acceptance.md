# 确定实施顺序与验收门槛

Type: grilling
Status: resolved
Parent: ../map.md
Blocked by: 02, 05, 06, 07, 08

## Question

把已决设计组织成 Server 后 Node 的可实施顺序：每阶段以哪些当前 v1 SQL 结构资产、Ent Schema 一致性、事务、重启恢复、CLI/API/协议验收和六平台纯 Go 构建检查证明外部行为未变？如何验收固定新状态目录的首次初始化、重复启动、当前 v1 结构不匹配拒绝、不完整状态拒绝，以及旧文件不被读取或改写？未来 migration runner 不属于本轮验收。

## Answer

- 实施顺序固定为 Server 后 Node。Server 阶段先完成 `server-state-v1`、当前 v1 SQL 结构资产、Ent Schema/生成代码、数据库访问层、事务与持久化不变量、启动检查及 CLI/API/协议回归；Server 全部门禁通过后再开始 Node 阶段。两端分别拥有自己的 Ent 包、数据库访问层和状态生命周期，不抽出泛化共享数据库包。
- Server 完成门槛包括：空目录初始化新状态；旧 `go-v1` 数据库不读取、不修改；新状态缺失、损坏或结构不匹配时拒绝启动；SQL 结构资产与 Ent Schema 同步；`go generate ./ent` 后无生成差异；普通 CRUD、关联查询、事务、错误映射、端口竞争、Hostname 归属、Client 换 Node、禁用 Tunnel 占用和并发不变量测试通过；CLI、Web API、Client/Node 协议保持现有语义；架构、Server 包、黑盒/集成测试及六平台 `CGO_ENABLED=0` 编译通过。
- Node 完成门槛包括：空目录初始化新状态；旧 `node.sqlite`、旧标记和旧 FRPS 文件不读取、不清理；Identity、ControllerBinding、RuntimeState 正确；首次 claim、重复 claim、修订冲突、幂等重放、运行切换、禁用、崩溃恢复、临时文件清理和 FRPS 所有权测试通过；损坏状态拒绝启动；Ent 生成无差异、SQL 结构与 Ent Schema 一致；管理协议、状态错误码和快照摘要字节保持；Node 测试、协议集成测试、架构测试和六平台纯 Go 编译通过。
- 外部行为回归覆盖 CLI 命令/参数/退出码、Web API 缺省/null/赋值三态、HTTP 状态码和领域错误码、Client/Node 协议消息与修订、Tunnel 资源分配、重启恢复、固定新状态目录以及失败不产生提交后事件。结构 SQL 只来自受控 SQL 资产；生产代码不调用 Ent Auto Migration；普通 CRUD/关系查询走 Ent，原生 SQL 只位于数据库访问层；静态检查和代码审查检查旧路径、旧表名、旧迁移分支及 handler/service 直接依赖 `*sql.DB`。
- 交付采用分阶段门禁：Server 未通过不得进入 Node；不提供旧数据库原地升级或程序降级恢复；新 v1 初始化失败时由操作者移走未完成的新状态目录后重试；地图完成后只交付设计，生产重构另起实施任务。未来 migration runner、`schema_migrations`、备份恢复和兼容窗口不属于本轮验收。
