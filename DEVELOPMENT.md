# ycy 开发指南

本文面向已经装好项目工具链、正在熟悉 Go 的开发者。所有命令都从仓库根目录执行。目录职责和依赖方向以[项目结构文档](docs/project-layout.md)为准。

## 先跑起来

首次进入仓库时，先准备项目依赖并启用本仓库的提交检查：

```sh
make bootstrap
make hooks-install
```

构建当前版本并查看实际可用的命令：

```sh
make build
./build/ycy --help
```

`build/ycy` 是本地构建产物。改动 Go 代码后，重新执行 `make build` 再用它试跑。改动 Web 代码后也要重新生成嵌入资源：

```sh
pnpm --dir web run build
make build
```

通常只需要 `make build`；上面的 Web 命令适合单独检查前端构建。不要从 `legacy/bun/` 启动当前程序，它只用于查阅原有实现和兼容行为。

## 探索命令

不要凭记忆拼参数，先使用各层级的帮助。以下命令只显示说明，不会修改配置或文件：

```sh
./build/ycy --help
./build/ycy config --help
./build/ycy git --help
./build/ycy fs --help
./build/ycy diff --help
```

当前 CLI 提供以下命令组：

| 命令 | 用途 |
| --- | --- |
| `config fork`、`config cm` | 管理 Fork 服务和提交消息（CM）配置 |
| `export env` | 将 `.env` 内容导出为 JSON |
| `git heat`、`git pulse`、`git fork`、`git cm` | Git 分析、下载与提交消息工具 |
| `run` | 交互式运行项目的 `package.json` 脚本 |
| `rm`、`zip` | 清理项目文件或压缩目录 |
| `diff`、`fs` | 启动本地 Web 服务，分别比较目录和浏览文件 |

配置写入用户目录的 `~/.ycy-cli/config.json`。在不确定某个命令是否会写入磁盘、调用网络或要求交互输入时，先运行该命令的 `--help`，再查看它在 `legacy/bun/` 中的兼容参考和对应测试。

## 启动 Web 命令

`diff` 和 `fs` 的生产行为由 Go 二进制提供 HTTP API 和嵌入式网页。启动后，终端会打印可打开的 URL；用 `Ctrl+C` 停止服务。

```sh
# 比较两个已有目录；终端会打印浏览器地址和 MCP 地址。
./build/ycy diff /absolute/path/baseline /absolute/path/target

# 在浏览器中查看当前目录。默认不允许文件管理操作。
./build/ycy fs . --safe-html
```

`fs` 默认监听 `0.0.0.0:1204`，`diff` 默认只在本机的 `127.0.0.1:1205` 提供服务。只有确实需要局域网访问时才为 `diff` 加 `--public`，并根据需要显式选择 `fs` 的 `--address`、`--manage` 和认证参数。

## 前后端联调

修改 React/Vite 前端时，在两个终端分别启动实际 Go 后端和 Vite 开发服务器。Vite 的代理地址由 `web/vite.config.ts` 约定，端口必须对应：

```sh
# 终端 A：FS 后端
./build/ycy fs . --port 6174

# 终端 B：FS Vite 开发服务器，打开 http://127.0.0.1:5174
pnpm --dir web dev:fs
```

```sh
# 终端 A：Diff 后端
./build/ycy diff /absolute/path/baseline /absolute/path/target --port 6173

# 终端 B：Diff Vite 开发服务器，打开 http://127.0.0.1:5173
pnpm --dir web dev:diff
```

Vite 提供前端热更新，但不会替你重启 Go 后端。修改 Go 代码后，停止终端 A、重新执行 `make build`，再重新启动对应命令。Tunnel 由 `./build/ycy tunnel server` 和 `./build/ycy tunnel connect` 提供；`web/tunnel-server` 是它的嵌入式管理界面资源，不是独立的本地服务入口。

## 调试 Go

仓库的 [`.vscode/launch.json`](../.vscode/launch.json) 已提供 `ycy: debug command`。使用它之前至少完成过一次 `make build`，以生成 Go 编译时必须嵌入的 `web/dist` 和 7-Zip 资源。

1. 在 VS Code 打开仓库，在 `cmd/ycy/main.go`、`internal/ycycmd/main.go`、`pkg/cmd/root/app.go` 或目标命令的 `run.go` 处设置断点。
2. 打开 “Run and Debug”，选择 `ycy: debug command`，按 F5。默认参数是 `--help`，可以验证命令树的创建过程。
3. 要跟踪具体命令时，将 `launch.json` 中该配置的 `args` 改为需要的参数，例如 `["git", "heat"]` 或 `["fs", ".", "--port", "1204"]`，再次按 F5。
4. 长时间运行的 `fs`、`diff` 服务可用调试器的 Stop 按钮或终端的 `Ctrl+C` 结束。

当程序触发未恢复的运行时错误时，配置中的 `DEBUG=1` 会输出 Go 堆栈。常规诊断日志使用 `--log-level debug` 或 `YCY_LOG_LEVEL=debug`；它们不是同一个开关。

调试测试通常更快：在 Go 测试函数上使用 VS Code 的 `Run Test`/`Debug Test`，或在终端缩小范围：

```sh
go test ./internal/ycycmd -run Test -v
go test ./pkg/cmd/diff -run Test -v
go test ./pkg/cmd/fs -run Test -v
```

测试名称和包路径以正在修改的命令为准。改动完成后执行 `make check`；提交前的 hook 只覆盖快速检查，不能替代它。

## 代码从哪里读起

一次命令调用的主路径是：

```text
命令行参数
  -> cmd/ycy                 注入版本并交给唯一的进程出口
  -> internal/ycycmd         进程事实、信号、启动路由和组合
  -> pkg/cmd/root             Cobra 根命令、参数与诊断配置
  -> pkg/cmd/<domain>/<leaf>  命令语法、业务逻辑与呈现
  -> web/                    diff/fs 等命令嵌入的 Vite/React 资源
```

建议以一个完整的垂直命令阅读，而不是从所有 Go 文件开始：

| 想了解的内容 | 先看 | 接着看 |
| --- | --- | --- |
| 命令名称、flags、参数 | `pkg/cmd/root/` 或对应叶子目录的 `command.go` | 同目录的 `command_test.go` |
| 命令业务与可测试边界 | `pkg/cmd/<domain>/<leaf>/` | 同目录的 `*_test.go` |
| 本地配置 | `internal/appconfig/` | `pkg/cmd/config/` 下的对应叶子 |
| Web 页面与代理端口 | `web/vite.config.ts` | `web/diff/`、`web/fs/` |

例如跟踪 `diff` 时依次阅读 `internal/ycycmd/main.go`、`pkg/cmd/root/app.go`、`pkg/cmd/diff/command.go`、`pkg/cmd/diff/run.go` 和同目录测试；跟踪 `fs` 时采用同样顺序。`cmd/ycy` 只有薄入口，进程组合放在 `internal/ycycmd`，命令业务不要回填到入口目录。

## 验证层级

按改动范围选择最小充分的检查，然后在交付前运行完整检查：

```sh
go test ./pkg/cmd/diff/...            # 只改 Diff 业务逻辑
go test ./internal/ycycmd ./pkg/cmd/root # 只改 CLI 组装或参数
pnpm --dir web run test               # 只改 React 逻辑
make check-web                        # 改 Web 的完整本地检查
make check                            # 准备提交或评审
make cross-build                      # 需要六个平台构建证据时
```

`make check` 在完成 `make bootstrap` 后应是离线、非修改性的完整门禁。`make fmt` 会修改 Go 和 Web 文件，只在希望接受自动格式化结果时运行。
