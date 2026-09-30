# 嵌入式 Web

React/Vite 应用包括 `diff`、`fs` 和 `tunnel-server`。Vite 将三者构建到 `web/dist/`；`web/assets.go` 嵌入构建结果，并校验每个应用的 `index.html`。生产 Go 二进制同时提供 API 和页面。`make build` 在编译 Go 前准备 Web 及其他嵌入资源。生产资源应进入 Vite asset graph，不手工写入 `web/dist/`。

修改前端时，阅读 `web/<app>/` 下的应用、实际使用的共享组件、对应 Go HTTP 所有者及相关测试。至少运行 `make check-web`；浏览器流程或 Go/Web 集成变更还需运行 `make acceptance-web`。

## 共享组件与滚动容器

实现或修改 UI 前，先检查 `web/shared/components/ui/` 和 `web/shared/admin/` 的现有组件及接口。已有共享能力优先复用，业务组件通过组合共享组件实现，使控件外观与交互保持一致。共享能力不足时，按实际需求做最小扩展；只有应用特有的布局或行为留在应用内。

应用自行管理的滚动区域统一使用 `web/shared/components/ui/scroll-area.tsx` 的 `ScrollArea`。局部封装仅负责尺寸、间距和内容布局，滚动条外观由共享组件维护。原生输入控件和第三方编辑器内部滚动保留其自身机制；`overflow: hidden` 等裁切布局不属于滚动容器。

滚动区域需有实际生效的尺寸约束，尤其检查 grid/flex 子项的 `min-height: 0`、viewport 高度上限和所需滚动方向。弹窗标题与操作区固定，正文滚动；带固定页签的编辑器只滚动页签正文。修改后验证长内容可到达、校验焦点可见，桌面和移动端无裁切或重叠，并检查浅色与深色主题下的共享样式。

## 本地开发

Vite 只提供前端 HMR。三个模式的端口分别是 `5173`（`diff`）、`5174`（`fs`）、`5175`（`tunnel-server`），默认代理到后端 `6173`、`6174`、`6175`。先单独启动相应的已构建 Go 命令，再运行 `pnpm --dir web dev:<app>`；后端使用其他端口时设置 `YCY_WEB_BACKEND=http://127.0.0.1:<port>`。修改 Go 代码后，需要重新构建并重启后端。实际路由和代理配置以 `web/vite.config.ts` 及命令代码为准。
