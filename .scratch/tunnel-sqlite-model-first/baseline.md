# 已确认边界与代码事实

## 已确认

- 目标同时覆盖查询代码与 schema 的维护成本；Server 和 Node 都纳入设计，实施顺序为 Server 后 Node。
- 只放弃 SQLite 文件及旧数据的兼容性；CLI、Web API、Client/Node 协议等外部行为保持。
- 新持久化设计从 schema v1 开始，不读取旧 Server/Node 数据库；本次只初始化当前 v1，未来 schema 演进遵守独立的 forward-only migration 维护约束。
- `--data-dir` 仍由用户指定；新的 Server/Node 固定状态子目录见[决定新旧 Tunnel 状态如何隔离](issues/02-state-isolation.md)。
- SQLite 结构及演进以 `migrations/*.sql` 为唯一事实来源；Ent Schema 负责 ORM 模型和生成代码，两边保持一致，具体维护规则见[SQLite 结构与 Ent 维护约束](sqlite-maintenance-constraints.md)。允许重设计当前触发器和延迟复合外键承担的约束及事务流程。
- Server 与 Node 均选 Ent；早先的 `sqlc` 建议不再适用。

## 代码事实

- Server 数据库当前位于 `go-v1/tunnel.sqlite`，并与 Controller 密钥、会话状态共用目录；见 `pkg/cmd/tunnel/server/state.go` 与 `internal/filesession/store.go`。
- Node 数据库当前为 `node.sqlite`，同一目录还含初始化标记和 FRPS 运行文件；见 `pkg/cmd/tunnel/node/state.go`、`pkg/cmd/tunnel/node/node_runtime.go`。
- 当前驱动为 `github.com/ncruces/go-sqlite3`；发布要求 `CGO_ENABLED=0`，覆盖 darwin、linux、windows 各 amd64/arm64；见 `go.mod`、`Makefile`、`ENGINEERING.md`。
- Server 使用 `BEGIN IMMEDIATE` 的专用连接事务；现有 schema 有触发器、部分唯一索引及延迟复合外键；见 `pkg/cmd/tunnel/server/server_clients.go`、`pkg/cmd/tunnel/server/database.go`。
- Node 目前有手写 v3 到 v4 迁移，并将身份、绑定、运行快照保存在 SQLite；见 `pkg/cmd/tunnel/node/state.go`。
- 当前 Server/Node 包测试在 `CGO_ENABLED=0` 下通过；这只是旧实现基线，不证明候选 ORM 适配。
