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
ft init             # 初始化系统
ft install docker   # 安装并启动 Docker Engine
ft install nginx    # 安装并启动 Nginx
ft version          # 查看版本
ft --help           # 查看全部命令
```

`ft init` 依次执行：

1. `apt-get update` — 刷新软件包索引
2. `apt-get upgrade -y` — 非交互升级已装软件（自动保留旧配置文件，不会交互挂起）
3. 安装常用工具：curl、wget、git、vim、unzip、zip、tar、net-tools、htop、ca-certificates、gnupg、lsb-release、software-properties-common

非 root 用户运行时自动加 `sudo`；任一步骤失败立即终止并透传退出码。

### 安装 Docker

```bash
ft install docker
```

该命令使用 Docker 官方 apt 仓库，并依次：

1. 安装 `ca-certificates`、`curl` 等前置依赖
2. 配置 `/etc/apt/keyrings/docker.asc` 与 deb822 软件源
3. 安装 Docker Engine、CLI、containerd、Buildx 与 Compose 插件
4. 启动 `docker.service` 并设为开机启动
5. 将经校验的非 root 本地调用用户加入 `docker` 组

Ubuntu 24.04 使用 `noble` 源，Ubuntu 26.04 使用 `resolute` 源；当前支持 `amd64` 和 `arm64`。命令不会自动卸载已有的冲突容器包。

直接以普通本地用户运行时，命令按进程真实 UID 确认账户；通过 `sudo ft install docker` 运行时，会交叉校验 `SUDO_USER` 与 `SUDO_UID`，然后将该用户加入 `docker` 组。直接以 root 登录并运行时，因为无法安全推断应授权哪个普通用户，所以跳过用户组修改。

静态发布二进制目前只自动处理可从 `/etc/passwd` 解析的本地账户；SSSD、LDAP 或 NIS 目录账户解析失败时，会在包查询和系统修改前退出。

`docker` 组可以控制 Docker daemon，实际上授予了 **root 等价权限**，因此只应授予可信用户。组成员资格在新登录会话中生效；安装成功后请退出并重新登录，再直接执行 `docker ps`。

执行安装前会通过 `dpkg-query` 检查 Docker Engine、CLI、containerd、Buildx 与 Compose 插件：

- 组件已完整安装：跳过 apt 更新、软件源配置和包安装，不升级现有版本；仍会确保 `docker.service` 已启动并开机自启，并处理调用用户的 `docker` 组成员资格
- 组件缺失或处于半配置状态：执行完整安装流程以补齐/修复
- 检测到 `docker.io`、`podman-docker`、`containerd` 等冲突包：在修改系统前报错退出，不会自动卸载

### 安装 Nginx

```bash
ft install nginx
```

该命令从 Ubuntu 官方仓库安装 `nginx`，然后启动 `nginx.service` 并设为开机启动。它不会修改 UFW、防火墙、TLS 证书或站点配置。

如果 `nginx` 包已完整安装，命令会跳过 apt 更新和包安装，不升级现有版本，只确保服务已启动并开机自启；缺失或半配置时才执行完整安装流程。

两个安装命令都会先校验操作系统；每一步实时透传输出，任一步失败立即停止并保留底层退出码。

## 开发

```bash
go build ./...   # 构建
go test ./...    # 测试
go vet ./...     # 静态检查
```

目录结构：

```
cmd/                  # cobra 子命令（root、init、install、version）
internal/osrelease/   # /etc/os-release 解析与支持性判断
internal/aptx/        # apt-get 命令构建与兼容执行接口
internal/dpkgx/       # dpkg 包安装状态查询
internal/installer/   # Docker/Nginx 安装步骤构建
internal/sysx/        # context-aware 特权命令执行
internal/userx/       # 调用用户身份解析与校验
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
