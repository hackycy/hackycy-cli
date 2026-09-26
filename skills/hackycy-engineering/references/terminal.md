# 终端行为

`internal/terminal/` 统一拥有终端检测、颜色、交互 session 与底层 UI runtime；`internal/terminaltest/` 提供测试能力。命令应复用这些能力，不自行建立另一套终端 runtime。

修改交互式命令时，保持以下可观察行为：

- 遵守 `NO_COLOR`；状态不能只通过颜色表达。
- 保持可消费的 stdout 与 diagnostics 分离；终端 session 恢复屏幕和模式后，再输出最终可消费结果。
- 正常结束、取消和出错时都恢复终端及鼠标模式。
- 长内容允许滚动，不静默截断；滚动位置与列表 selection 是不同状态。
- 非交互环境提供可用的 fallback。

阅读所属命令的 terminal adapter、`internal/terminal/` 实现、包内测试，以及 PTY 或验收测试。修改交互行为后运行 `make check-terminal` 和 `make acceptance-terminal`。
