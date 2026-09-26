# 嵌入式 Web

React/Vite 应用包括 `diff`、`fs` 和 `tunnel-server`。Vite 将三者构建到 `web/dist/`；`web/assets.go` 嵌入构建结果，并校验每个应用的 `index.html`。生产 Go 二进制同时提供 API 和页面。`make build` 在编译 Go 前准备 Web 及其他嵌入资源。生产资源应进入 Vite asset graph，不手工写入 `web/dist/`。

修改前端时，阅读 `web/<app>/` 下的应用、实际使用的共享组件、对应 Go HTTP 所有者及相关测试。至少运行 `make check-web`；浏览器流程或 Go/Web 集成变更还需运行 `make acceptance-web`。

Vite 只提供前端 HMR。三个模式的端口分别是 `5173`（`diff`）、`5174`（`fs`）、`5175`（`tunnel-server`），默认代理到后端 `6173`、`6174`、`6175`。先单独启动相应的已构建 Go 命令，再运行 `pnpm --dir web dev:<app>`；后端使用其他端口时设置 `YCY_WEB_BACKEND=http://127.0.0.1:<port>`。修改 Go 代码后，需要重新构建并重启后端。实际路由和代理配置以 `web/vite.config.ts` 及命令代码为准。
