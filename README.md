# hackycy-cli

[![License][license-src]][license-href]

hackycy的脚手架工具集

## 安装

### macOS & Linux

``` bash
curl -fsSL https://raw.githubusercontent.com/hackycy/hackycy-cli/main/scripts/install.sh | bash
```

### Windows

``` powershell
powershell -c "irm https://raw.githubusercontent.com/hackycy/hackycy-cli/main/scripts/install.ps1 | iex"
```

> Windows users upgrading from v0.0.46 or earlier must run this installer once before using `ycy upgrade`.

## 运行

``` bash
$ ycy --help
Usage: ycy [options] [command]

Options:
  -V, --version                output the version number
  -h, --help                   display help for command

Commands:
  export                       Export utilities
  config                        Manage ycy configuration
  git                          Git utilities
  npm                          npm utilities
  rm [options] [paths...]      Remove files/dirs, or smartly clean project artifacts when no path given
  fs [options] [directory]     Browse files in a directory (defaults to current directory)
  diff [options] <baseline-directory> <target-directory>
                               Compare two directories in a browser
  zip [options] [directory]    Zip a directory into a zip file
  tunnel                       Manage trusted tunnel clients and tunnel definitions
  upgrade                      Upgrade cli to the latest version
  help [command]               display help for command
```

在单包项目或 monorepo 中运行 `ycy npm open package-settings`，先从默认全选的清单中勾选要打开的包（`↑/↓` 移动、`Space` 切换，`Enter` 确认）。确认后按 `Enter` 逐个打开设置页，清单会显示打开、未打开、失败和未勾选的包；按 `Esc` 可提前结束。普通终端用编号选择（回车全选，`none` 清空），随后每次回车打开下一项，输入 `n` 停止。加 `--batch` 可无提示打开清单中的所有页面，也适用于非交互环境。

## License

[MIT](./LICENSE) License © [hackycy](https://github.com/hackycy)

<!-- Badges -->

[license-src]: https://img.shields.io/github/license/hackycy/hackycy-cli.svg?style=flat&colorA=080f12&colorB=1fa669
[license-href]: https://github.com/hackycy/hackycy-cli/blob/main/LICENSE
