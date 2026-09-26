# 开发与验证

## 环境与生成内容

Go 工具链固定为 `go1.26.7`；Web 需要 Node.js 24 或更新版本、pnpm `>=11.13.0 <12`（包内指定 `11.24.0`）。还需 Git 和 GNU Make。处理环境配置时，核对 `Makefile`、`go.mod` 与 `web/package.json` 中的当前要求。

`make bootstrap` 准备 Go、Web 和仓库本地 Lefthook 依赖，但不安装 Git hook。需要配置 hook 时运行 `make hooks-install` 与 `make hooks-doctor`。正常构建执行 `make build`，随后用 `./build/ycy --help` 检查产物。

`build/`、`release/`、`web/dist/`、`web/node_modules/`、`.cache/`、`.tmp/`、`internal/sevenzipruntime/payload/` 和 `tools/lefthook/bin/` 都是生成内容，不放业务代码。所需嵌入资源由对应 Make target 准备；源码不能依赖这些目录在开发机上预先存在。

## 按变更行为选择检查

| 变更 | 针对性验证 |
| --- | --- |
| Go 模块 | 对实际所属包运行测试，例如 `go test ./pkg/cmd/diff/...`，并测试受影响的 `internal/` 包 |
| 架构或导入方向 | `go test ./internal/architecture` |
| Web 或嵌入资源 | `make check-web`，涵盖 ESLint、类型检查、Vitest、Vite 构建和资源校验 |
| 终端交互 | `make check-terminal` 与 `make acceptance-terminal` |
| 公开 CLI 语法 | `make command-surface` 及相关命令和验收测试 |
| 进程、信号、浏览器或跨包流程 | `make acceptance`、`make acceptance-web` 或更聚焦的验收目标 |

验收测试使用 `acceptance` build tag；普通 `go test ./...` 不会运行它们。提交或评审代码前，在环境允许时运行 `make check` 和 `make build`。`make check` 检查依赖锁、Web、Go 格式、vet 和 Go 测试；根据变更行为追加验收目标。检查失败或缺少前置条件时如实说明。

`make fmt` 会通过 `gofmt -w` 和 ESLint `--fix` 修改文件，只在准备接受这些修改时运行。pre-commit hook 是快速检查，覆盖暂存 diff 的空白、暂存 Go 文件的格式和暂存 Web 文件的 ESLint；它不负责格式化、暂存、安装依赖，也不代替 `make check`。同一文件混合暂存与未暂存修改时，实际工具可能检查当前工作树内容。

公开命令结构（command surface）是兼容性契约。`make command-surface-update` 会更新快照；只有明确要修改公开 CLI 时才运行，并人工审查生成的差异，不为消除测试失败直接更新。

## 调试

VS Code 的 `ycy: debug command` 配置从 `cmd/ycy` 启动；调试具体命令时调整其中的 `args`。先构建一次，确保嵌入式 Web 和 7-Zip 资源可用。`DEBUG=1` 控制未恢复的 Go 运行时错误的堆栈输出；普通日志诊断使用 `--log-level debug` 或 `YCY_LOG_LEVEL=debug`。
