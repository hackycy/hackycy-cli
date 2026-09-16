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
  rm [options] [paths...]      Remove files/dirs, or smartly clean project artifacts when no path given
  fs [options] [directory]     Browse files in a directory (defaults to current directory)
  diff [options] <baseline-directory> <target-directory>
                               Compare two directories in a browser
  zip [options] [directory]    Zip a directory into a zip file
  run [path]                   Run package.json scripts
  tunnel                       Manage trusted tunnel clients and tunnel definitions
  upgrade                      Upgrade cli to the latest version
  help [command]               display help for command
```

## License

[MIT](./LICENSE) License © [hackycy](https://github.com/hackycy)

<!-- Badges -->

[license-src]: https://img.shields.io/github/license/hackycy/hackycy-cli.svg?style=flat&colorA=080f12&colorB=1fa669
[license-href]: https://github.com/hackycy/hackycy-cli/blob/main/LICENSE
