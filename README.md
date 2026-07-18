# ft

面向 Linux 服务器的运维命令封装 CLI：系统初始化、常用软件安装等，一条命令搞定。

**定位**：支持各主流 Linux 发行版，逐步扩展适配
**当前支持**：Ubuntu 24.04 / Ubuntu 26.04（暂不支持的系统会明确报错拒绝执行，不会误操作）

## 安装

从 [Releases](https://github.com/ABT-Tech-Limited/ft/releases) 下载对应架构的压缩包：

```bash
# amd64（x86_64）
curl -fsSL https://github.com/ABT-Tech-Limited/ft/releases/latest/download/ft_$(curl -fsSL https://api.github.com/repos/ABT-Tech-Limited/ft/releases/latest | grep -oP '"tag_name": "v\K[^"]+')_linux_amd64.tar.gz | tar -xz
sudo install ft /usr/local/bin/ft
```

或手动下载 `ft_<version>_linux_<amd64|arm64>.tar.gz` 解压后放入 `PATH`。

## 用法

```bash
ft init        # 初始化系统
ft version     # 查看版本
ft --help      # 查看全部命令
```

`ft init` 依次执行：

1. `apt-get update` — 刷新软件包索引
2. `apt-get upgrade -y` — 非交互升级已装软件（自动保留旧配置文件，不会交互挂起）
3. 安装常用工具：curl、wget、git、vim、unzip、zip、tar、net-tools、htop、ca-certificates、gnupg、lsb-release、software-properties-common

非 root 用户运行时自动加 `sudo`；任一步骤失败立即终止并透传退出码。

## 开发

```bash
go build ./...   # 构建
go test ./...    # 测试
go vet ./...     # 静态检查
```

目录结构：

```
cmd/                  # cobra 子命令（root、init、version）
internal/osrelease/   # /etc/os-release 解析与支持性判断
internal/aptx/        # apt-get 命令构建与执行
```

## 发布

推送 `v*` 标签即自动触发 GoReleaser 交叉编译（linux/amd64 + linux/arm64）并发布 GitHub Release：

```bash
git tag v0.1.0
git push origin v0.1.0
```

本地验证发布配置：

```bash
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
```
