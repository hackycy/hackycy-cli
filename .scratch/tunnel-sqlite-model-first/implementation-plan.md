# Tunnel SQLite 与 Ent 重构 Implementation Plan

## Source Decisions

- [map.md](map.md): 当前 v1 重构的目标、顺序、范围和所有决策已收敛。
- [baseline.md](baseline.md): Server/Node 现状、状态隔离和外部行为边界。
- [sqlite-maintenance-constraints.md](sqlite-maintenance-constraints.md): SQL 结构资产与 Ent Schema 的职责边界，以及未来 migration 维护规则。
- [issues/02-state-isolation.md](issues/02-state-isolation.md): 固定 `server-state-v1` / `node-state-v1` 状态目录和旧状态隔离。
- [issues/03-orm-prototype.md](issues/03-orm-prototype.md): Ent 与 ncruces 驱动、普通事务和专用连接事务的可行性。
- [issues/04-orm-choice.md](issues/04-orm-choice.md): Ent `v0.14.5`、驱动接入、生成代码和 SQL 结构资产同步规则。
- [issues/05-server-invariants.md](issues/05-server-invariants.md): Server 约束、不变量、并发写入和错误语义。
- [issues/06-node-state-model.md](issues/06-node-state-model.md): Node 身份、绑定、运行检查点、原始快照和恢复语义。
- [issues/07-data-access-boundary.md](issues/07-data-access-boundary.md): Ent 查询边界、原生 SQL 集中位置、事务所有权和错误映射。
- [issues/08-schema-lifecycle.md](issues/08-schema-lifecycle.md): 本次只做全新破坏性 v1，不实现 migration runner。
- [issues/09-rollout-acceptance.md](issues/09-rollout-acceptance.md): Server 后 Node 的阶段门禁和最终验收。
- [ENGINEERING.md](../../ENGINEERING.md) and [CLAUDE.md](../../CLAUDE.md): 仓库工程与维护约束。

## Outcome

Server 和 Node 都使用 Ent 生成的类型安全模型与查询 API，SQLite 真实结构由版本化 SQL 结构资产和同步的 Ent Schema 共同维护。两端在新的固定 v1 状态目录中初始化，旧数据库不被读取；Server 保留需要 `BEGIN IMMEDIATE` 的原子资源事务，Node 保留身份、管理快照和 FRPS 恢复语义，CLI、API 和协议行为保持不变。

## Non-Negotiable Rules

- 本次是全新、破坏性的 v1；不迁移、不升级、不接管旧 Server/Node 数据库或运行文件。
- SQL 结构资产是 SQLite 真实结构的唯一事实来源；Ent Schema 必须同步，只负责 ORM 模型、关系和生成代码。生产路径不得调用 Ent Auto Migration。
- 普通 CRUD、关系查询和计数优先走 Ent；复杂查询或 SQLite 特殊能力只能集中在数据库访问层，不能泄漏到 handler 或 service。
- Server 的选择性 `BEGIN IMMEDIATE` 由外层拥有专用连接、提交、回滚和关闭；短生命周期 Ent client 不拥有连接，也不嵌套 `client.Tx`。
- 事务只有成功提交后才能发布事件；任何检查、写入或提交失败都不得留下半完成状态。
- 不改变 CLI、Web API、Client/Node 协议、错误码、PATCH 三态、快照原始字节和 FRPS 所有权语义。
- 本次不实现 migration runner、`schema_migrations`、自动升级、备份恢复或兼容窗口；这些属于未来独立工作。

## Gate Overview

| Gate | Name | Unlock condition | Outcome |
| --- | --- | --- | --- |
| G0 | 建立 v1 SQL/Ent 基础 | 开始 | Server/Node 的 v1 结构资产、Ent Schema、生成代码和数据库访问骨架可独立验证。 |
| G1 | 完成 Server 持久化层 | G0 Exit 全部满足 | Server 在新状态目录中可初始化、重开并执行 Ent 查询、事务和不变量检查。 |
| G2 | 通过 Server 外部行为门禁 | G1 Exit 全部满足 | Server CLI/API/协议、并发资源规则和旧状态隔离保持既定行为。 |
| G3 | 完成 Node 持久化与管理状态 | G2 Exit 全部满足 | Node v1 身份、绑定、运行检查点和管理协议持久化完成。 |
| G4 | 通过 Node 恢复与最终交付门禁 | G3 Exit 全部满足 | Node 恢复、FRPS 所有权、全仓库回归和六平台构建全部通过，设计可交付实施。 |

## G0: 建立 v1 SQL/Ent 基础

### Purpose

建立两端共享约束下各自独立的 v1 数据访问基础，消除后续 Server/Node 实现对手写 schema 和散落 SQL 的依赖。

### Inputs

- `sqlite-maintenance-constraints.md`
- `issues/03-orm-prototype.md`
- `issues/04-orm-choice.md`
- `issues/07-data-access-boundary.md`
- 当前 `go.mod`、`Makefile`、`pkg/cmd/tunnel/server/database.go`、`pkg/cmd/tunnel/node/state.go`

### Objective

为 Server 和 Node 建立当前 v1 SQL 结构资产、同步的 `ent/schema`、固定 Ent 生成入口和数据库访问层骨架；空库初始化只创建当前 v1，不引入 migration runner。

### Scope boundary

允许新增 Ent 依赖、Schema、生成代码、v1 SQL 结构资产、数据库初始化辅助代码和测试夹具。不得实现 Server/Node 业务迁移、旧数据库兼容、后续 migration runner 或业务行为重构；具体 Server/Node 业务实体在 G1/G3 完成。

### Constraints

- Server 和 Node 使用 `entgo.io/ent v0.14.5` 与现有 ncruces SQLite 驱动。
- 结构 SQL 与 Ent Schema 必须表达同一组表、字段、索引、外键和基础 `CHECK`；生产不调用 `client.Schema.Create`。
- 初始化只能作用于新的 `server-state-v1` / `node-state-v1` 空目录。
- 生成代码提交到仓库；不得把 `.scratch/prototype` 当作生产包。

### Slice policy

按职责分片：先固定依赖和生成命令，再建立 Server 结构资产与模型，再建立 Node 结构资产与模型，最后接入各自的空库初始化和结构验证。每个 slice 只跨一个数据库边界或一个生成入口。

### Verification

#### Directed

- 每完成一端的结构资产和 Ent Schema，使用临时空库验证关键表、索引、外键和 `CHECK` 存在，验证只由 v1 SQL 结构资产初始化。
- 每次修改 Schema 后执行 `go generate ./ent`，确认生成代码可编译且与 SQL 字段/关系一致。

#### Repository

1. `go generate ./ent`，在每个生成 slice 后执行，并确认工作树没有未预期生成差异。
2. `GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 go test ./internal/architecture`，G0 收尾执行。
3. `GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 go vet ./...`，G0 收尾执行。

#### Manual acceptance

- 无。

### Evidence rule

G0 Exit 由两端结构验证记录、生成命令无差异证据、Ent/ncruces 编译测试证据和架构检查证据共同证明；任何一端缺少结构一致性证据都不能通过。

### Stop conditions

- SQL 结构资产与 Ent Schema 无法表达同一结构；需要重新决定约束归属；发现旧数据库兼容需求；或生成器、驱动、纯 Go 构建出现未决冲突。

### Rollback

按数据库边界回退当前未提交 slice，删除对应新增生成产物和临时空库；不修改旧状态目录，不回退其他数据库边界。

### Exit conditions

1. Server 与 Node 的当前 v1 SQL 结构资产和 Ent Schema 已提交且结构验证通过。
2. 固定的 `go generate ./ent` 入口可再生生成代码，且再生后无非预期差异。
3. 两端均通过现有 ncruces 驱动的 `CGO_ENABLED=0` 编译/测试及 `internal/architecture` 检查。
4. 生产初始化路径没有 Ent Auto Migration、旧数据库读取或 migration runner。

## G1: 完成 Server 持久化层

### Purpose

将 Server 的状态、查询、资源事务和启动不变量迁移到 Ent 与新的 v1 状态目录。

### Inputs

- G0 完成的 SQL 结构资产、Ent Schema 和生成代码
- `issues/02-state-isolation.md`
- `issues/05-server-invariants.md`
- `issues/07-data-access-boundary.md`
- `pkg/cmd/tunnel/server/state.go`、`database.go`、Server registry/runtime 文件及其测试

### Objective

Server 只打开 `server-state-v1`，通过主 Ent client 和专用 immediate 事务完成当前持久化读写、跨记录不变量检查和错误映射。

### Scope boundary

允许改 Server 状态打开、数据库访问、registry、tunnel/client/node/account 持久化和启动校验。保留 CLI/API/协议适配入口及既有外部错误语义；Node 持久化和最终跨端回归留给 G3/G4。

### Constraints

- `State` 拥有唯一 `*sql.DB` 和主 Ent client，并只关闭连接池一次。
- 普通写入使用 Ent mutation/transaction；资源竞争写入使用专用 `*sql.Conn`、`BEGIN IMMEDIATE` 和短生命周期 Ent client。
- 原生 SQL 仅在数据库访问层；提交后才发送事件。
- 保留本地 Node、Hostname、端口池、Client/Tunnel Node 归属等已决不变量和领域错误码。

### Slice policy

按持久化调用簇切片：状态打开与初始化、基础查询映射、单表 CRUD、Server 普通事务、资源 immediate 事务、启动不变量与错误映射。每个 slice 先迁移读路径，再迁移对应写路径和测试夹具。

### Verification

#### Directed

- 空目录、重复启动、缺失/损坏新状态和存在旧 `go-v1` 数据库的场景逐一验证路径和文件未被误读写。
- 对端口、Hostname、Client 换 Node、禁用 Tunnel 和本地 Node 保护执行冲突与回滚场景，确认事务失败无半完成状态、无提交前事件。
- 对每类 Ent/SQLite 约束错误执行既有领域错误映射，确认未知约束返回内部错误而不是文本猜测结果。

#### Repository

1. `GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 go test -count=1 ./pkg/cmd/tunnel/server/...`，每个 Server 持久化调用簇通过后执行。
2. `GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 go test -count=1 ./internal/architecture`，G1 收尾执行。
3. `GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 go vet ./...`，G1 收尾执行。

#### Manual acceptance

- 无。

### Evidence rule

G1 Exit 必须由 Server 包测试、定向状态隔离/回滚/错误映射证据和架构/ vet 证据共同覆盖；每个 Server 持久化调用簇都要有通过记录。

### Stop conditions

- 发现外部 API/协议语义必须改变；旧数据库被意外读取；事务边界无法保持；或约束错误无法稳定映射。

### Rollback

每个持久化调用簇以独立提交为 seam；失败时回退该调用簇及其测试夹具，不回退已通过的 G0 基础结构。

### Exit conditions

1. Server 状态只使用固定 `server-state-v1`，旧 `go-v1` 数据库不被读取或修改。
2. Server 的 Ent 查询、普通事务、immediate 资源事务、回滚、提交后事件和错误映射定向验证通过。
3. Server 启动不变量和资源并发规则测试覆盖既定成功、冲突和回滚场景。
4. Server 包测试、架构测试和 `go vet` 通过。

## G2: 通过 Server 外部行为门禁

### Purpose

证明 Server 持久化重构没有改变 CLI、Web API、Client/Node 协议和用户可观察的资源行为。

### Inputs

- G1 完成的 Server 实现
- `issues/05-server-invariants.md`
- `issues/09-rollout-acceptance.md`
- Server 黑盒、HTTP、协议和集成测试入口

### Objective

完成 Server 的行为回归、并发验收和跨平台纯 Go 构建，形成进入 Node 阶段的完整门禁证据。

### Scope boundary

只修复 Server 回归和验证缺口；不开始 Node 重构、不引入 migration runner、不改变外部合同。

### Constraints

- CLI 参数、退出码、Web API 状态码和 PATCH 缺省/null/赋值三态保持。
- Client/Node 协议消息、修订和 Server 错误码保持。
- 旧状态留存，新状态固定，失败不发布提交后事件。

### Slice policy

按用户可观察合同切片：CLI/启动、HTTP API、Tunnel 资源、Node 管理协议、重启与旧状态隔离、并发竞争。每个 slice 只修复对应回归，不顺带改变其他合同。

### Verification

#### Directed

- 运行 Server 的 CLI、HTTP、Client/Node 管理和资源并发场景，保存请求/响应、错误码、状态和数据库回滚证据。
- 对新旧目录、重启和初始化失败场景检查旧文件字节未改变、新状态路径准确。

#### Repository

1. `make check-terminal`，Server 行为 slice 收尾执行。
2. `make acceptance-terminal`，Server 黑盒/进程/PTY 回归收尾执行。
3. `make cross-build`，进入 Node 前执行一次，确认六个平台 `CGO_ENABLED=0` 构建。
4. `GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 go test ./internal/architecture`，最终收尾执行。

#### Manual acceptance

- 无。

### Evidence rule

G2 Exit 必须包含 CLI/API/协议/资源并发的定向结果、`make check-terminal`、`make acceptance-terminal`、`make cross-build` 和架构检查结果；任何外部合同缺少证据都不能进入 G3。

### Stop conditions

- 外部行为差异无法归因于已批准设计；验收环境、FRPS/协议依赖不可用；或跨平台构建失败且原因未定位。

### Rollback

以 Server 行为合同为提交 seam；只回退导致回归的行为 slice，保留已证明的 G1 持久化层。

### Exit conditions

1. Server CLI、Web API、Client/Node 协议和资源错误语义回归通过。
2. Server 并发不变量、重启、旧状态隔离和失败事件行为有定向证据。
3. `make check-terminal`、`make acceptance-terminal`、`make cross-build` 和架构检查通过。
4. Server 阶段证据已记录，可解锁 Node 阶段。

## G3: 完成 Node 持久化与管理状态

### Purpose

将 Node 身份、Controller 绑定、运行检查点和管理快照迁移到 Ent 与新的 v1 状态目录。

### Inputs

- G2 完成的 Server 和跨平台基础
- G0 的 Node SQL 结构资产、Ent Schema 和生成代码
- `issues/02-state-isolation.md`
- `issues/06-node-state-model.md`
- `issues/07-data-access-boundary.md`
- `pkg/cmd/tunnel/node/state.go`、`runtime_state.go`、`management*.go` 及其测试

### Objective

Node 只打开 `node-state-v1`，使用 Ent 模型持久化 Identity、ControllerBinding、RuntimeState，并保持 claim、修订和快照语义。

### Scope boundary

允许改 Node 状态打开、初始化、持久化映射、管理事务和启动校验。保留管理协议入口和 FRPS 行为；最终崩溃恢复与全仓库门禁留给 G4。

### Constraints

- 新 v1 不读取旧 `node.sqlite`、旧标记、旧快照或旧 FRPS 文件，也不保留旧 v3/v4 自动迁移分支。
- 协议快照使用原始字节和精确摘要；Ent JSON 编解码不得重排摘要载荷。
- claim、接受候选和运行检查点使用 Ent 事务；实体不越过持久化边界。
- 缺失、损坏或结构不匹配的新状态拒绝启动，不替换身份。

### Slice policy

按 Node 状态职责切片：身份初始化/重开、ControllerBinding claim、RuntimeState 读写、候选接受与修订冲突、领域映射。每个 slice 先迁移持久化读写，再迁移相应管理调用和测试。

### Verification

#### Directed

- 空目录、重复启动、缺失/损坏数据库、初始化标记和运行行场景验证拒绝或初始化结果。
- 首次 claim、重复 claim、旧修订、同修订不同摘要、同修订同摘要和原始快照摘要场景验证。
- 验证旧 `node.sqlite`、旧标记和旧 FRPS 文件未被读取、清理或改写。

#### Repository

1. `GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 go test -count=1 ./pkg/cmd/tunnel/node/...`，每个 Node 状态调用簇通过后执行。
2. `GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 go vet ./...`，G3 收尾执行。

#### Manual acceptance

- 无。

### Evidence rule

G3 Exit 必须由 Node 状态、claim、修订/摘要、旧状态隔离的定向证据，Node 包测试和 `go vet` 共同证明；每个状态调用簇都要有通过记录。

### Stop conditions

- 需要接管旧 Node、改变管理协议、改变原始快照字节或无法安全区分不完整状态与新目录初始化。

### Rollback

每个 Node 状态调用簇以独立提交为 seam；失败只回退该调用簇和对应测试，不修改 Server 已通过实现。

### Exit conditions

1. Node 状态只使用固定 `node-state-v1`，旧数据库和运行文件不被读取或修改。
2. Identity、ControllerBinding、RuntimeState 的初始化、claim、修订、摘要和事务语义通过定向验证。
3. Node 包测试和 `go vet` 通过。
4. Node 管理协议入口仍可由现有测试调用，准备进入恢复和最终门禁。

## G4: 通过 Node 恢复与最终交付门禁

### Purpose

证明 Node 的运行切换、禁用、崩溃恢复和 FRPS 进程所有权安全，同时完成全仓库交付验证。

### Inputs

- G3 完成的 Node 实现
- `issues/06-node-state-model.md`
- `issues/09-rollout-acceptance.md`
- Node runtime/recovery、协议和 acceptance 测试入口

### Objective

完成 Node 恢复行为、全仓库回归、架构检查和六平台构建，形成可交付的实现计划完成证据。

### Scope boundary

只修复 Node 恢复、FRPS 所有权、跨端回归和交付门禁缺口；不实现未来 migration runner，不接管旧 Node，不开展与 Tunnel 状态无关的持久化重构。

### Constraints

- 只有数据库登记且所有权校验通过的 FRPS 才能被终止；未知所有权不得接管或清理。
- disabled/switching 检查点必须按已决顺序持久化，崩溃后不得误启动旧配置。
- 生成配置和临时文件只位于新 Node 状态目录；原始快照恢复后摘要不变。
- Server 和 Node 的 CLI/API/协议外部行为保持既定语义。

### Slice policy

按恢复边界切片：running 切换、disabled 流程、崩溃检查点恢复、所有权未知、临时文件清理、生成文件重建，最后做全仓库回归。每个 slice 独立注入故障并验证重启结果。

### Verification

#### Directed

- 在每个持久化检查点注入进程终止，重启后验证状态、FRPS 所有权、临时文件和最后成功快照。
- 验证未知/未登记进程不被终止，disabled 意图完成清理，running 状态从最后成功快照恢复。
- 验证协议、HTTP PATCH 三态、错误码、CLI 退出码和 Server 资源行为的跨端回归。

#### Repository

1. `make check`，包含 Web、锁、Go vet 和全仓库 Go 测试，最终收尾执行。
2. `make check-terminal`，最终收尾重复执行，覆盖 Tunnel 端到端包。
3. `make acceptance-terminal`，最终收尾执行黑盒/进程/PTY 验收。
4. `make cross-build`，最终收尾重复执行六平台纯 Go 构建。
5. `GOTOOLCHAIN=$(GO_TOOLCHAIN) GOWORK=off CGO_ENABLED=0 go test ./internal/architecture`，最终收尾执行。

#### Manual acceptance

- 无。

### Evidence rule

G4 Exit 必须有每个恢复边界的故障注入结果、Node recovery/protocol 测试结果、`make check`、`make check-terminal`、`make acceptance-terminal`、`make cross-build` 和架构检查结果；所有计划 Gate 的 Exit conditions 均需有证据才能宣布 effort 完成。

### Stop conditions

- 任何恢复场景可能终止未知进程、误启动旧配置、改变协议/错误语义，或最终门禁失败原因未定位。

### Rollback

以每个恢复边界的故障注入测试和对应实现为 seam；回退单个恢复 slice，不回退已证明的 Server 阶段或基础 Ent 生成资产。

### Exit conditions

1. Node running、disabled、崩溃恢复、所有权校验、临时文件清理和配置重建通过定向验证。
2. 全仓库 Go/Web/架构/黑盒/PTY 验收通过。
3. 六平台 `CGO_ENABLED=0` 构建通过。
4. 旧数据库、未来 migration runner 和无关持久化模块均未被纳入实现。
5. G0–G4 的证据完整，达到本 effort 的 Definition Of Done。

## Definition Of Done

- G0–G4 全部通过并有可复核的 Progress Log 证据。
- Server 后 Node 的 v1 重构顺序、结构来源、状态隔离、事务边界和外部行为合同均按决策文档实现。
- 生成代码无差异，架构、全仓库、Tunnel、acceptance 和六平台纯 Go 构建门禁均通过。
- 旧 SQLite 状态未被读取、修改、迁移或清理；本次未实现未来 migration runner、备份恢复和兼容窗口。
- CLI、Web API、Client/Node 协议、错误码、PATCH 三态、快照摘要和 FRPS 所有权行为保持既定语义。

## Explicitly Out Of Scope

- 旧 Server/Node 数据库迁移、兼容、接管、清理或原地升级。
- 本次实现 migration runner、`schema_migrations`、后续 schema 自动升级、备份恢复、兼容窗口和降级恢复。
- 旧 Node 进程接管或部署切换。
- 与 Tunnel Server/Node 状态无关的持久化模块重构。
- CLI、Web API、Client/Node 协议的产品行为变更。
