# GitHub CLI 的发版与版本管理调研

> 调研日期：2026-09-15。代码结论固定到 `cli/cli` 的 `trunk`
> 提交 `cac0dd795eeb5b9a448474fad7e295272fc5c486`；线上发布实例为
> [v2.100.0](https://github.com/cli/cli/releases/tag/v2.100.0)。本文只使用
> `cli/cli` 源码、其官方 Releases、以及 GitHub 官方文档等第一方来源。

## 一句话结论

GitHub CLI 把版本号视为一次受保护部署的**输入和产物契约**，而不是需要
提交的版本文件：维护者输入 `vX.Y.Z`，工作流校验格式，在三个原生 OS 上为
同一提交并行构建并把该值写入二进制、文件名和安装包元数据；所有资产、签名、
证明、校验和、Release Notes 以及官方包仓库随后围绕这个值生成。稳定版与预发布
共用一条流水线，是否包含连字符决定预发布及站点发布行为。

## 版本号从哪里来

1. 维护者执行 `script/release vX.Y.Z`。脚本默认以 `trunk` 为部署 ref，调用
   `gh workflow run deployment.yml`，将字符串作为 `tag_name` 输入；生产环境还
   提示到 Slack 人工批准。它不是改 `VERSION` 文件，也不是 push 一个预先存在的
   tag。[入口脚本](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/script/release#L22-L28)
   [调度实现](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/script/release#L81-L119)

2. CI 首先接受 `vMAJOR.MINOR.PATCH`，并可接受以 `-` 开头、由点分隔的预发布
   后缀，例如 `v2.100.0-rc.1`；`+build` 元数据被明确拒绝。正则并未限制数值的
   前导零，因此这是“接近 SemVer 的工程规则”，不能把它误称为完整的 SemVer
   校验器。[校验规则](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L38-L49)

3. 版本被注入而非编译期硬编码。常规本地构建优先读取 `GH_VERSION`，否则使用
   `git describe --tags`，再退化到短 commit SHA，并经 Go linker 写入
   `internal/build.Version`；GoReleaser 的三个发布构建也用 linker flag 写入
   `{{ .Version }}` 和构建日期。[本地构建逻辑](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/script/build.go#L48-L64)
   [取版本优先级](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/script/build.go#L125-L143)
   [发布 ldflags](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.goreleaser.yml#L19-L61)

4. 为使 GoReleaser 读取相同版本，Linux、macOS、Windows 构建 job 都只在 runner
   的 `HEAD` 上创建临时本地 tag，且注释明确说明它不会推送远端。最终 job 执行
   `gh release create TAG --target "$GITHUB_SHA"`。GitHub Release 创建时才创建
   远端 tag。这个判断也能由最新稳定版交叉验证：`v2.100.0` 的 Git ref object 是
   `commit`，不是 `tag`，即轻量标签。
   [临时标签](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L70-L79)
   [最终创建](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L423-L446)
   [v2.100.0 tag API](https://api.github.com/repos/cli/cli/git/ref/tags/v2.100.0)

5. 项目文档给维护者的产品级规则是：功能发布升 MINOR、破坏性发布升 MAJOR，且
   功能应在发布前至少一天完成 review/approval；文档未把 Conventional Commits
   或自动算版本作为 release 的真相来源。[官方发布指南](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/docs/releasing.md#L41-L47)

## 实际发布流水线

```text
script/release vX.Y.Z
  -> workflow_dispatch (production / staging / dry-run)
  -> 格式校验
  -> Linux + macOS + Windows 并行原生构建、打包、平台签名
  -> 汇总资产，生成 RPM/APT 仓库，attestation，SHA-256 清单
  -> gh release create --target <dispatch commit> --generate-notes
  -> 仅稳定正式版更新 cli.github.com 的手册、版本和包仓库
```

* `workflow_dispatch` 提供 `environment`、`platforms`、`release`、`dry_run`。
  表单默认 `dry_run: true`，而日常脚本传入 `false`；staging 可产出构建资产，dry
  run 可演练生产打包/签名但不发布。维护者批准是官方 runbook 的前置条件。
  [工作流输入](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L13-L35)
  [演练语义](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/docs/release-process-deep-dive.md#L26-L30)

* 三个 OS job 每个均为 20 分钟超时，产物通过 GitHub Actions artifacts（保留 7
  天）交给最终 `release` job，而不是让一个 Linux job cross-build 后直接发布。
  这样 macOS/Windows 的签名链可在原生环境完成。
  [Linux job](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L50-L92)
  [macOS job](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L94-L198)
  [Windows job](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L200-L317)

* GoReleaser 固定到 `v2.13.1`，避免工具更新改变资产文件名；配置同时定义 Linux
  `386/arm/amd64/arm64`、macOS `amd64/arm64`、Windows `386/amd64/arm64`，并生成
  tar.gz、zip、deb、rpm。Windows MSI 另由 MSBuild 封装，且把预发布后缀去掉以保持
  Windows `ProductVersion` 为数字版本。
  [工具版本与原因](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L63-L79)
  [打包矩阵](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.goreleaser.yml#L63-L113)
  [MSI 版本映射](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L269-L296)

* 稳定版与预发布的分界是 tag 中的连字符：最终 `gh release create` 加
  `--prerelease`，而站点及 APT/RPM 仓库发布要求 production、非 dry-run、且 tag
  不含连字符。这个设计意味着 RC 可以得到可下载 Release，但不会推动“latest”
  的官方站点/包仓库通道。
  [预发布判断与 Release 创建](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L423-L446)
  [稳定版站点发布闸门](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L447-L465)

## 产物、完整性与分发

* 发布 job 汇总所有平台资产，生产环境对 RPM 签名、生成并签名 RPM repository
  metadata、用 `reprepro` 更新 APT 仓库；它还对 `dist/gh_*` 建立 GitHub Artifact
  Attestations。macOS 使用 Developer ID `codesign` 与 `notarytool`，Windows 通过
  Azure Code Signing 对 zip 内二进制及 MSI 签名。
  [仓库签名与 attestation](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L372-L422)
  [macOS 签名脚本](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/script/sign#L8-L28)
  [Windows 签名脚本](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/script/sign.ps1#L1-L22)

* 最终 job 在上传前为每个 `gh_*` 资产生成 `sha256` 清单
  `gh_<version>_checksums.txt`。`v2.100.0` 的真实 Release 含 21 个平台包和 1 个
  checksum 文件，名称均带 `2.100.0` 而不是 `v2.100.0`；清单可直接用于离线
  核验。[清单生成](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L429-L446)
  [v2.100.0 真实资产](https://github.com/cli/cli/releases/tag/v2.100.0)
  [v2.100.0 checksum](https://github.com/cli/cli/releases/download/v2.100.0/gh_2.100.0_checksums.txt)

* Release 之外，Linux 的官方 APT/RPM 仓库由同一 deployment workflow 更新；Homebrew
  则由 Homebrew 的 autobump 约每 3 小时跟进，紧急时维护者按新 URL、SHA-256、PR
  的流程人工推进。Windows WinGet 和多种社区包管理器被明确标为各自外部项目的
  责任，不把它们伪装成同一条发布流水线能完全控制的下游。
  [Linux 官方仓库与 release assets](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/docs/install_linux.md#L33-L62)
  [Homebrew 机制](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/docs/releasing.md#L26-L31)
  [Windows 分发责任边界](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/docs/install_windows.md#L5-L37)

## 验证、说明和消费者视角

* 发布质量并不只依赖最后一天：PR/trunk 的 Go workflow 在 Linux/macOS/Windows
  跑 `go test -race -tags=integration ./...` 与 `go build`；lint workflow 还会验证
  所有目标平台的第三方许可证清单和 `govulncheck`。单独的 acceptance workflow 也
  能在三种 OS 对外部 GitHub 环境运行。它们与 deployment workflow 在 YAML 中没有
  显式 `needs` 关系，因此公开源码只能证明它们是日常质量门禁，不能断言它们被
  GitHub 分支保护强制为发布的前置条件。
  [三 OS 单元/集成门禁](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/go.yml#L11-L35)
  [许可证与漏洞门禁](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/lint.yml#L39-L81)
  [验收测试](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/acceptance.yml#L25-L60)

* Release Notes 由 `--generate-notes` 从两版之间已合并 PR 自动生成，不维护手写
  CHANGELOG；标题固定为 `GitHub CLI <无 v 的版本>`。实例 `v2.100.0` 可见 Features、
  Fixes、Docs & Chores、Dependencies、作者与 compare 链接。GitHub 的自动说明功能
  还可在仓库配置中按标签分类和识别首次贡献者。
  [生成调用](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml#L435-L446)
  [v2.100.0 发布说明](https://github.com/cli/cli/releases/tag/v2.100.0)
  [GitHub 官方自动 Release Notes 文档](https://docs.github.com/en/repositories/releasing-projects-on-github/automatically-generated-release-notes)

* `gh` 自身没有把“检测到新版本”混同为“自更新”：交互式非 CI 环境至多每 24 小时
  请求 `repos/<owner>/<repo>/releases/latest`，以 SemVer 比较 tag，发现更新后给出
  通知；用户仍通过其安装渠道升级，且可用 `GH_NO_UPDATE_NOTIFIER` 关闭提示。
  [通知条件与 API](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/internal/update/update.go#L81-L146)
  [比较逻辑](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/internal/update/update.go#L180-L190)

## 对 ycy 当前流程的建议

本项目已有的 [发布运行手册](releasing.md) 具备 GitHub CLI 的几个关键优点：标签是
唯一版本源、GitHub 自动生成 Release Notes、可核验的 `SHA256SUMS`、attestation、
发布后下载再验和 Docker 在 Release 公开后才执行。下面是可执行的对照建议，不会
改变当前流程：

| 优先级 | 建议 | 依据与边界 |
| --- | --- | --- |
| P0，保持 | 继续要求**带注释** `vX.Y.Z` tag、必须从 `main` 可达，并拒绝 build metadata / prerelease。 | `cli/cli` 以 workflow 创建轻量 tag；这适合其权限模型，但会削弱本项目当前 tag 审计与规则集的边界，不应照搬。 |
| P1 | 将“是否需要 RC”明确写成产品决策：若需要，采用 `vX.Y.Z-rc.N`，建立同稳定版的构建、验和、attestation 和可下载 GitHub prerelease，但绝不更新 `latest` Docker tag 或稳定更新源。 | 这是 `cli/cli` 的连字符通道模式；现行 runbook 正确地拒绝 prerelease，因此在需求出现前不应放宽正则。 |
| P1 | 保留当前 `make check` 与远端回下载校验，并在未来增加一个不发布的 `dry-run` / staging 演练入口，覆盖发布候选构建及目标平台自检。 | `cli/cli` 把 staging/dry-run 作为生产签名和打包的可重复演练；这是发布事故前发现权限、工具链和签名配置问题的机制，不是替代 PR CI。 |
| P1 | 把“下游分发所有权”列成显式矩阵：GitHub Release、GHCR/腾讯镜像是本项目管理；其他包管理器只有在有维护者和自动/手动 SHA 更新闭环后才宣称官方支持。 | `cli/cli` 明确区分自管 APT/RPM、Homebrew autobump、WinGet/社区渠道，避免把发布成功误认为用户已拿到更新。 |
| P2 | 仅在 macOS/Windows 成为正式支持的高信任安装渠道时，再评估平台代码签名与公证；继续维持 checksum + GitHub attestation 的当前保证，并记录这种取舍。 | `cli/cli` 的原生平台 job、密钥、Azure/Apple 基础设施服务于其 21 个包的受众；对 ycy 现有六个裸二进制，先把已定义的校验与公开后自检执行稳定，性价比更高。 |

## 可复核来源

* [部署工作流（固定提交）](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.github/workflows/deployment.yml)
* [发布入口脚本](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/script/release) 与 [GoReleaser 配置](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/.goreleaser.yml)
* [GitHub CLI 官方发布指南](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/docs/releasing.md) 与 [发布深潜文档](https://github.com/cli/cli/blob/cac0dd795eeb5b9a448474fad7e295272fc5c486/docs/release-process-deep-dive.md)
* [当前最新稳定 Release：v2.100.0](https://github.com/cli/cli/releases/tag/v2.100.0) 与 [对应 Git ref API](https://api.github.com/repos/cli/cli/git/ref/tags/v2.100.0)
* [GitHub 自动 Release Notes 官方文档](https://docs.github.com/en/repositories/releasing-projects-on-github/automatically-generated-release-notes) 与 [Artifact Attestations 官方文档](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations)
