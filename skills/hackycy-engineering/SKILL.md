---
name: hackycy-engineering
description: 维护 hackycy-cli 仓库的 Go CLI 与嵌入式 Web 应用。处理代码修改、调试、测试、架构边界、SQLite/Ent 结构和发布准备时使用。
---

# hackycy-cli 工程开发

在本仓库开展工程工作时使用此 skill。引用文件独立保存项目约束；易变化的细节仍需核对当前仓库。信息冲突时，依次以代码和测试、`Makefile` 与 `tools/` 及 `.github/workflows/`、本 skill 为准。当前 Go/Web 实现是唯一受支持的实现。

## 沿功能所有者开展工作

1. 从需求涉及的行为确定代码所有者。CLI 功能先看 `pkg/cmd/root/` 的注册，再沿 `pkg/cmd/<domain>[/<leaf>]/command.go`、runner、包内测试和相关 `internal/` 模块纵向阅读。跨进程、终端、浏览器或包边界的行为还要阅读 `acceptance/`。
2. 参数或默认值有影响时，用 `make build` 和 `./build/ycy <command> --help` 核对实际 CLI 语法。具体行为由代码和测试定义，不在 skill 中复制完整命令表。
3. 修改公开契约前说明实质性歧义和取舍。优先在现有所有者内作满足需求的最小修改。只有真正拥有共享能力时才新增共享模块，避免顺带清理无关代码。
4. 先运行受影响所有者的针对性测试，再按[验证指南](references/verification.md)选择相关检查。交付代码修改前，在环境允许时运行 `make check` 和 `make build`；未运行或失败的检查要说明原因。

## SQLite 维护必读

修改 tunnel Server/Node 的持久化、SQL、Ent 模型或数据结构前，先完整阅读 [SQLite 与 Ent 维护约束](references/sqlite-ent.md)。`migrations/*.sql` 是真实数据库结构的唯一事实来源，Ent Schema 与生成代码必须同步；生产启动和升级不得用 Ent Auto Migration 修改结构。当前 v1 只有空状态初始化，没有启动时 migration runner。升级已部署数据库时，需先设计只能前进的迁移生命周期，不能把未来约束当成现有能力。

## 按任务读取引用

- 命令边界、Factory、配置或平台分支：阅读[架构](references/architecture.md)。
- 开发环境、测试、生成文件、命令界面、Git hook 或调试：阅读[验证指南](references/verification.md)。
- React、Vite、嵌入页面或浏览器流程：阅读[Web](references/web.md)。
- 交互式 CLI 或 PTY 行为：阅读[终端](references/terminal.md)。
- tunnel Server/Node 持久化、SQLite 结构、Ent 或数据升级：遵守上面的必读要求。
- 版本、跨平台构建、发布产物或 Docker 发布：阅读[发布](references/release.md)。

引用文件说明所有权和约束，不扩大当前需求范围。除非需求明确要求且经过验证，否则保持既有公开行为。
