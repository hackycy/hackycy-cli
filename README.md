# ycy

`ycy` 是正在从 Bun 迁移到 Go 的命令行工具。当前开发构建版本为 `0.0.0-dev`；活动代码是 CGO-free Go CLI，`legacy/bun/` 仅保留为行为兼容的只读参考，不能作为当前开发入口。仓库结构契约见[项目结构文档](docs/project-layout.md)。

## 开始运行

已具备项目要求的 Go、Node.js、pnpm 和 Make 后，在仓库根目录执行一次：

```sh
make bootstrap
make hooks-install
```

构建并试跑当前 CLI：

```sh
make build
./build/ycy --help
./build/ycy --version
```

`make build` 会先构建三个 Vite 前端入口，再将 `web/dist` 和运行所需资源嵌入 `build/ycy`。因此这是本项目的标准启动方式，而不是直接运行旧的 Bun 代码。

常用校验命令：

```sh
make check       # 完整检查：Web、Go、锁文件和活动树隔离
make check-web   # 只检查并构建 Web
go test ./...    # 只执行 Go 测试；需要已有 web/dist
make fmt         # 有意应用 Go 格式化与 ESLint 自动修复时使用
```

## 使用与调试

完整的本地使用、前后端联调、VS Code/Delve 断点调试和代码导航说明见[开发指南](DEVELOPMENT.md)。质量门、Git hook 和跨平台构建细节见[贡献指南](CONTRIBUTING.md)。
