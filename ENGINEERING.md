# Engineering Guide

本文件是仓库开发与维护约定的唯一工程文档。

**具体命令行为、参数、默认值和实现细节，以代码、测试、Makefile 和 CI 工作流为准。不要依赖历史文档描述。**

## 1. 真相来源

发生不一致时，按以下顺序判断：

1. 代码与测试。
2. `Makefile`、`.github/workflows/` 和 `tools/`。
3. 本文件。
4. `legacy/bun/` 仅用于必要的历史兼容分析，不属于当前实现，也禁止被生产代码依赖。

常用入口：

```text
cmd/ycy/                              二进制入口与 VERSION
internal/ycycmd/                     进程组合
pkg/cmd/root/                        Cobra 根命令和命令注册
pkg/cmd/<domain>[/<leaf>]/           各命令实现
pkg/cmd/factory/                     Factory 构造
pkg/cmdutil/                         共享命令能力类型
internal/                            有明确所有权的内部模块
internal/architecture/               架构约束测试
acceptance/                          黑盒/进程/PTY/Web 验收测试
web/                                 React/Vite 和嵌入资源
tools/                               构建、发布和辅助工具
.github/workflows/                   Release / Docker 流程
Makefile                             统一任务入口
```

不要在工程文档里复制完整 CLI 参数表。需要确认实际行为时：

```sh
make build
./build/ycy --help
./build/ycy <command> --help
```

然后阅读对应的 `command.go`、实现和测试。

---

## 2. 开发环境

要求：

```text
Go toolchain  go1.26.7
Node.js       >=24
pnpm          >=11.13.0 <12
推荐 pnpm     11.24.0
Git
GNU Make
```

首次准备：

```sh
make bootstrap
make hooks-install
make hooks-doctor
```

`make bootstrap` 会准备 Go、Web 和仓库本地 Lefthook 依赖，但不会自动安装 Git hook。

正常构建：

```sh
make build
./build/ycy --help
```

以下目录属于生成内容，不要放业务代码：

```text
build/
release/
web/dist/
web/node_modules/
.cache/
.tmp/
internal/sevenzipruntime/payload/
tools/lefthook/bin/
```

---

## 3. 架构

生产执行链：

```text
cmd/ycy
  -> internal/ycycmd
  -> pkg/cmd/factory + pkg/cmdutil
  -> pkg/cmd/root
  -> pkg/cmd/<domain>[/<leaf>]
  -> internal/* / web
```

### `cmd/ycy`

必须保持为薄入口。

当前职责只有：

```text
VERSION
main.go
```

`main.go` 负责：

```go
os.Exit(ycycmd.Main(version))
```

不要把命令业务、初始化逻辑或平台逻辑放回这里。

### `internal/ycycmd`

负责进程级组合，例如：

* stdin/stdout/stderr 和终端能力；
* signal context；
* logging 初始化；
* hidden worker / updater dispatch；
* startup update 处理；
* embedded Web 校验；
* Factory 和 root command 启动。

这里负责“组装进程”，不负责普通命令业务。

### `pkg/cmd/root`

负责：

* Cobra 根命令；
* 全局 flags；
* diagnostics；
* command registration；
* CLI 错误和 exit code 归一化。

具体业务必须继续下沉到所属 command package。

### `pkg/cmd/<domain>[/<leaf>]`

每个命令 owner 自己拥有：

* Cobra grammar；
* flags 和参数；
* `Options`；
* runner；
* presentation；
* leaf-specific adapter；
* package-local tests。

父命令只能注册自己的直接子命令。

命令 leaf 不允许依赖兄弟 leaf 或父命令。

需要共享行为时，放入真正拥有该能力的 `internal/<module>`，不要跨 command package 互相调用。

---

## 4. 共享能力

`pkg/cmdutil.Factory` 只保存多个命令真正共享的进程能力，例如：

```text
Version
IOStreams
Terminal
Logging
Environment
EnvironmentLookup
WorkingDirectory
HTTPClient
Now
ConfigStore
GitRunner
```

`ConfigStore` 和 `GitRunner` 是 lazy / memoized。

某个命令独有的依赖，应留在自己的 `Options` 中，不要因为一个调用点就塞进全局 Factory。

---

## 5. 禁止的架构模式

`internal/architecture` 中的测试是强制约束。

不要创建这类泛化逃生包：

```text
utils
common
services
interfaces
adapters
```

也不要通过换一个类似名字绕过这个原则。

同时保持以下边界：

* `cmd/ycy` 只能依赖 `internal/ycycmd`。
* 普通 `internal/*` 不得反向依赖 `pkg/cmd*`。
* `pkg/cmdutil` 不得依赖 command package。
* `pkg/cmd/factory` 不得依赖具体 command。
* command leaf 不得依赖兄弟或父 command。
* Cobra / pflag 只允许在 command packages 中使用。
* command packages 不直接依赖 Charm UI / Log。
* `charm.land/log/v2` 只由 `internal/logging` 封装。
* `os.Exit` 只属于 `cmd/ycy`。
* 配置持久化细节只属于 `internal/appconfig`。
* active production code 不得 import `legacy/bun`。
* Web embedded assets 只能由明确拥有它们的 package 使用。

修改目录结构或依赖方向时，先检查：

```sh
go test ./internal/architecture
```

---

## 6. 如何读代码

优先纵向读一个完整功能，不要一开始横扫整个仓库。

例如追踪 `diff`：

```text
pkg/cmd/root/app.go
  -> pkg/cmd/diff/command.go
  -> pkg/cmd/diff/run.go
  -> pkg/cmd/diff/*_test.go
  -> 相关 internal package
  -> web/diff/
```

通常按这个顺序判断：

```text
command.go
    ↓
参数、flags、Options 构造

run.go / 领域实现
    ↓
业务流程

*_test.go
    ↓
已经固定的行为和边界

internal/*
    ↓
真正共享的底层能力

acceptance/*
    ↓
真实进程、终端、浏览器、跨包行为
```

如果历史 Bun 实现和当前 Go 实现不同，优先当前测试和当前代码。

不要为了保持历史代码结构而破坏现在的架构。

---

## 7. 修改原则

只修改完成当前目标需要的代码。

不要：

* 顺便重构附近代码；
* 顺便统一无关命名；
* 为未来可能需求提前设计 abstraction；
* 为单一调用点建立共享层；
* 因为看到旧代码就顺手删除；
* 为了减少重复而制造错误的跨 owner 依赖。

新增功能优先沿现有 owner 扩展。

需要新 shared module 时，先明确它具体“拥有哪种能力”，而不是因为“多个地方可能能用”。

---

## 8. Web

Web 使用 React + Vite。

当前应用：

```text
diff
fs
tunnel-server
```

构建结果进入：

```text
web/dist/
```

然后通过 Go `embed` 编译进 `ycy`。

因此生产环境不是“Go 后端 + 独立前端服务器”，而是 Go 二进制同时提供 API 和嵌入页面。

`make build` 会先准备 Web 和其他嵌入资源，再编译 Go。

### 前端开发

Vite 模式：

```text
diff           http://127.0.0.1:5173
fs             http://127.0.0.1:5174
tunnel-server  http://127.0.0.1:5175
```

默认代理：

```text
diff           -> http://127.0.0.1:6173
fs             -> http://127.0.0.1:6174
tunnel-server  -> http://127.0.0.1:6175
```

也可以：

```sh
YCY_WEB_BACKEND=http://127.0.0.1:<port> pnpm --dir web ...
```

覆盖目标后端。

例如：

```sh
./build/ycy diff /path/before /path/after --port 6173
pnpm --dir web dev:diff
```

```sh
./build/ycy fs . --port 6174
pnpm --dir web dev:fs
```

Vite 只负责前端 HMR。

Go 代码修改后需要重新构建并重启后端。

---

## 9. Web 资源约束

Go 构建依赖有效的 `web/dist`。

`web/assets.go` 会嵌入并验证：

```text
diff/index.html
fs/index.html
tunnel-server/index.html
```

不要绕过 Vite asset graph 手工添加生产资源。

Web 修改至少运行：

```sh
make check-web
```

它包括：

```text
eslint
TypeScript typecheck
Vitest
Vite build
asset verification
```

---

## 10. Terminal

终端能力集中在：

```text
internal/terminal/
internal/terminaltest/
```

不要让各 command 自己重新实现 terminal detection、颜色、交互 session 或底层 UI runtime。

关键行为：

* `NO_COLOR` 必须生效；
* 状态不能只通过颜色表达；
* stdout 和 diagnostics 要保持边界；
* interactive UI 结束后要正确恢复终端；
* 鼠标模式退出时必须恢复；
* 长内容应允许滚动，而不是静默截断；
* 滚动位置与列表 selection 是不同状态；
* 非交互环境必须有合理 fallback；
* 最终可消费结果应在终端恢复后输出。

需要修改终端交互时，重点运行：

```sh
make check-terminal
make acceptance-terminal
```

---

## 11. 平台边界

正式支持六个目标：

```text
darwin/amd64
darwin/arm64
linux/amd64
linux/arm64
windows/amd64
windows/arm64
```

发布构建使用：

```text
CGO_ENABLED=0
```

平台差异应保留在真正拥有对应能力的模块中。

优先使用：

```text
*_windows.go
*_unix.go
*_darwin.go
*_linux.go
```

或局部、明确且可测试的：

```go
runtime.GOOS
runtime.GOARCH
```

当前平台差异主要涉及：

* machine identity；
* file replacement；
* filesystem permission / ACL；
* process groups / Job Objects / signals；
* PID liveness；
* filesystem capacity；
* symlink / reparse-point 安全；
* updater replacement；
* executable naming；
* 7-Zip embedded payload；
* FRP runtime；
* path / URI normalization。

新增平台判断前先搜索：

```sh
rg 'runtime\.GO(OS|ARCH)|//go:build' internal pkg tools
```

不要创建一个万能 `platform` abstraction。

如果新增普通 runtime platform branch，同时要补对应测试，并确认六个平台构建仍成立。

---

## 12. 配置

用户配置由：

```text
internal/appconfig/
```

统一持久化。

配置文件目前位于用户目录：

```text
~/.ycy-cli/config.json
```

不要在其他 package 自行拼接 `config.json` 或锁文件路径。

命令层通过 Factory 获取 ConfigStore。

配置格式、锁、replace 和机器身份等行为都应继续留在 `internal/appconfig`。

---

## 13. 测试层级

### Package tests

优先运行当前修改直接影响的 package：

```sh
go test ./pkg/cmd/diff/...
go test ./pkg/cmd/fs/...
go test ./internal/appconfig
go test ./internal/ycycmd ./pkg/cmd/root
```

### Web

```sh
pnpm --dir web run test
make check-web
```

### 完整本地 gate

提交或评审前：

```sh
make check
make build
```

`make check` 包含 Web、Go formatting verification、vet、Go tests 和 lock verification。

### Acceptance

`acceptance/` 使用：

```text
//go:build acceptance
```

因此普通：

```sh
go test ./...
```

不会运行这些测试。

需要显式运行：

```sh
make acceptance
make acceptance-web
make acceptance-terminal
```

真实进程、PTY、signal、浏览器和其他跨 package journey 应放这里，而不是硬塞进普通 unit tests。

---

## 14. 格式化

主动接受自动修改时才运行：

```sh
make fmt
```

它会：

* `gofmt -w`
* ESLint `--fix`

不要把 `make fmt` 当成无副作用检查命令。

---

## 15. Git Hook

安装：

```sh
make hooks-install
make hooks-doctor
```

pre-commit 是 Fast Gate，只负责：

```text
git diff --cached --check
staged Go gofmt check
staged Web ESLint
```

hook 不会：

* 自动格式化；
* 自动 fix；
* 自动 stage；
* 安装依赖；
* 访问网络。

部分暂存文件命中规则后，实际工具可能检查当前 worktree 内容，因此需要注意 staged / unstaged 混合状态。

可以使用：

```sh
git commit --no-verify
```

或：

```sh
LEFTHOOK=0 git commit
```

临时绕过。

绕过只代表没运行 hook，不代表代码通过验证。之后仍运行：

```sh
make check
```

---

## 16. Debug

VS Code 已提供：

```text
ycy: debug command
```

入口：

```text
cmd/ycy
```

默认参数：

```text
--help
```

调试具体命令时直接修改 `.vscode/launch.json` 的 `args`。

例如：

```json
["git", "heat"]
```

或：

```json
["fs", ".", "--port", "1204"]
```

调试前建议至少完成一次：

```sh
make build
```

因为 Go 编译依赖生成后的 Web 和 7-Zip embedded resources。

`DEBUG=1` 用于未恢复 runtime error 的 Go stack。

普通日志诊断使用：

```text
--log-level debug
YCY_LOG_LEVEL=debug
```

不要混淆这两类开关。

---

## 17. Command surface

CLI 的公开命令结构有冻结测试。

检查：

```sh
make command-surface
```

不要因为测试失败就直接更新 snapshot。

只有明确决定改变公开 CLI contract 时，才运行：

```sh
make command-surface-update
```

然后人工审查变化。

command surface 属于用户兼容性边界，不是普通测试快照。

---

## 18. Build

本机：

```sh
make build
```

输出：

```text
build/ycy
```

版本来自：

```text
cmd/ycy/VERSION
```

通过 linker 注入：

```text
-X main.version=<version>
```

跨平台：

```sh
make cross-build
```

生成：

```text
build/cross/ycy-macos-x64
build/cross/ycy-macos-arm64
build/cross/ycy-linux-x64
build/cross/ycy-linux-arm64
build/cross/ycy-windows-x64.exe
build/cross/ycy-windows-arm64.exe
```

不要手工改变正式产物名称，升级器、安装脚本和发布流程依赖这些名字。

---

## 19. Release

正式版本唯一记录在：

```text
cmd/ycy/VERSION
```

不要为发布修改：

```text
cmd/ycy/main.go
```

稳定版本格式：

```text
X.Y.Z
```

Git tag：

```text
vX.Y.Z
```

不接受：

```text
rc
beta
prerelease
+build metadata
leading-zero numeric components
```

正式 release tag 必须是：

* annotated tag；
* 与 `cmd/ycy/VERSION` 完全一致；
* 从 `main` 发布。

### Dry run

```sh
DRY_RUN=1 make release
```

用于执行 release preflight，但不写版本、不 commit、不 tag、不 push。

### 正式发布

```sh
make release
```

release tool 负责：

```text
检查 main
检查工作区
同步 origin
读取 VERSION
选择新版本
执行 make check
更新 VERSION
创建 release commit
push main
创建 annotated tag
push tag
```

不要手工复制这一套步骤形成第二条发布流程。

---

## 20. Release CI

推送：

```text
vX.Y.Z
```

后触发：

```text
.github/workflows/release.yml
```

CI 会重新验证：

* ref 是 tag；
* tag 格式正确；
* `VERSION` 与 tag 一致；
* tag 是 annotated；
* release candidate 可以从干净环境构建。

正式 Release 包含六个 binary 和：

```text
SHA256SUMS
```

GitHub Release notes 自动生成。

发布资产构建入口：

```sh
make release-candidate
```

这条命令要求干净的 candidate 环境，并重新准备依赖和所有 embedded artifacts。

---

## 21. Docker

Docker 发布和 GitHub Release 是两步。

先完成 GitHub Release，再从对应 release tag 手动运行：

```text
.github/workflows/docker.yml
```

Docker workflow 会：

1. 下载已经发布的 Linux binaries；
2. 使用 `SHA256SUMS` 验证；
3. 准备 pinned FRP runtime；
4. 构建：

   * `linux/amd64`
   * `linux/arm64`
5. 推送镜像。

当前镜像目标：

```text
ghcr.io/hackycy/hackycy-cli
sgccr.ccs.tencentyun.com/sooosin-sg/hackycy-cli
```

发布：

```text
:<version tag>
:latest
```

不要让 Docker 流程重新自行构建一套未经 GitHub Release 验证的 ycy binary。

---

## 22. 发布完整性

当前 macOS / Windows release binary 不使用平台 code signing。

主要完整性机制是：

* release workflow；
* 固定的构建输入；
* `SHA256SUMS`；
* candidate verification；
* Docker 发布前重新下载并验和。

不要在没有明确设计和迁移计划的情况下修改发布文件名、checksum contract 或版本来源。

---

## 23. Generated / Frozen 区域

### `legacy/bun/`

冻结。

用途：

```text
历史行为分析
兼容性参考
迁移对照
```

禁止：

```text
production import
从这里启动当前程序
继续在这里开发新功能
```

### Generated output

以下内容都不属于源代码：

```text
build/
release/
web/dist/
web/node_modules/
.cache/
.tmp/
internal/sevenzipruntime/payload/
tools/lefthook/bin/
```

不要提交，也不要让业务代码依赖它们在开发机上预先存在；由对应 Make target 负责生成。

---

## 24. 工作原则

处理需求时：

1. 先找到代码 owner。
2. 读取对应测试。
3. 明确最小修改范围。
4. 修改代码。
5. 跑最小充分测试。
6. 交付前跑完整 gate。

不要因为已有代码复杂就继续增加复杂度。

能在现有 owner 内解决，就不要创建新的 abstraction。

能通过一个小函数完成，就不要建立新 framework。

能通过测试固定行为，就不要靠长注释描述行为。

**代码定义实现，测试定义边界，Makefile 定义工程入口，CI 定义发布事实。**
