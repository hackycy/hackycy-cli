# 构建与发布

`cmd/ycy/VERSION` 是版本的唯一来源。`make build` 通过 `-X main.version=<version>` 注入版本并生成 `build/ycy`。`make cross-build` 使用 `CGO_ENABLED=0`，为 macOS、Linux 和 Windows 的 `amd64`、`arm64` 构建产物。正式产物名称必须与 `Makefile`、升级器、安装脚本和发布工作流保持一致。

稳定版本在 `VERSION` 中使用 `X.Y.Z`，从 `main` 发布 annotated `vX.Y.Z` tag；不支持 prerelease 标记、build metadata 或带前导零的数字段。`DRY_RUN=1 make release` 只运行预检，不写版本、不提交、不打 tag、不推送。明确执行正式发布时使用 `make release`：工具检查分支和工作区、同步 origin、选择新版本、运行 `make check`、更新 `VERSION`、创建提交并推送 `main` 与 annotated tag。不手工复制出第二条发布流程，也不为发布修改 `cmd/ycy/main.go`。

推送 tag 后触发 `.github/workflows/release.yml`。CI 校验 tag 类型、稳定版本格式及其与 `VERSION` 的一致性，并在干净环境中运行 `make release-candidate`。正式 Release 包含六个二进制文件及 `SHA256SUMS`；candidate 构建会重新准备全部嵌入资源。当前 macOS 和 Windows 产物没有 code signing，修改校验和或产物契约前需有明确的迁移设计。

Docker 是 GitHub Release 完成后，从对应 release tag 手动触发的 `.github/workflows/docker.yml`。它下载已发布的 Linux 二进制文件，用 `SHA256SUMS` 验证，准备固定版本的 FRP runtime，并向工作流指定的仓库发布 `linux/amd64` 与 `linux/arm64` 镜像；它不会自行重建另一套 CLI 二进制文件。执行发布任务前，核对 `Makefile`、`tools/release/`、`tools/release-artifacts/` 及两个工作流的当前行为。
