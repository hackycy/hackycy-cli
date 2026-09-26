# 所有权与架构

## 执行链

`cmd/ycy` -> `internal/ycycmd` -> `pkg/cmd/factory` 与 `pkg/cmdutil` -> `pkg/cmd/root` -> `pkg/cmd/<domain>[/<leaf>]` -> 所属 `internal/` 模块或 `web/`。

- `cmd/ycy` 只拥有 `VERSION` 和薄入口 `main.go`。进程退出由 `os.Exit(ycycmd.Main(version))` 完成；命令业务、平台逻辑和启动编排属于其他所有者。
- `internal/ycycmd` 组装进程 IO、终端能力、signal context、日志、隐藏 worker 与升级器分发、启动升级处理、嵌入式 Web 校验、Factory 和根命令执行。普通命令业务留在对应 command package。
- `pkg/cmd/root` 拥有 Cobra 根命令、全局 flags、诊断、命令注册及 CLI 错误和退出码归一化，不承载 leaf 业务。
- 每个 `pkg/cmd/<domain>[/<leaf>]` 拥有自己的语法、flags、`Options`、runner、呈现、leaf adapter 和包内测试。父命令只注册直接子命令；leaf 不依赖父命令或兄弟 leaf。
- 真正共享的能力由具体命名的 `internal/<module>` 拥有，不建立 `utils`、`common`、`services`、`interfaces`、`adapters` 一类泛化包。

`pkg/cmdutil.Factory` 只承载多个命令共用的进程能力：版本、IO、终端、日志、环境、工作目录、HTTP client、时间、配置存储和 Git runner。`ConfigStore` 与 `GitRunner` 延迟构造并缓存。命令独有的依赖留在该命令的 `Options`。

## 强制边界

`internal/architecture` 测试这些依赖规则。修改包布局或依赖方向时运行 `go test ./internal/architecture`。

- `cmd/ycy` 仅依赖 `internal/ycycmd`；普通 `internal/` 代码不反向导入 `pkg/cmd*`。
- `pkg/cmdutil` 不导入命令包；`pkg/cmd/factory` 不导入具体命令。
- Cobra 和 pflag 只在命令包中使用。命令包不直接导入 Charm UI 或 Log；`charm.land/log/v2` 由 `internal/logging` 封装。
- `os.Exit` 仅属于 `cmd/ycy`；配置持久化细节属于 `internal/appconfig`；嵌入式 Web 资源只由明确拥有它们的包使用。

用户配置由 `internal/appconfig` 持久化，目前位于 `~/.ycy-cli/config.json`。命令通过 Factory 获取存储，不在其他包自行拼接配置或锁文件路径。Tunnel Server/Node 的 SQLite 状态有独立所有者，见 [SQLite 与 Ent](sqlite-ent.md)。

## 平台变更

正式发布目标是 `darwin`、`linux`、`windows` 各自的 `amd64` 与 `arm64`，并使用 `CGO_ENABLED=0`。平台差异留在拥有该能力的模块中。优先使用局部的 `*_windows.go`、`*_unix.go`、`*_darwin.go`、`*_linux.go`，或小而可测的 `runtime.GOOS`/`GOARCH` 分支。新增判断前先运行 `rg 'runtime\.GO(OS|ARCH)|//go:build' internal pkg tools` 搜索既有做法，补对应测试并确认受影响目标可构建；不建立万能平台层。
