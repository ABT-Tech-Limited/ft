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
ft init                # 初始化系统
ft install docker      # 安装并启动 Docker Engine
ft install nginx       # 安装并启动 Nginx
ft install fail2ban    # 安装并启动 Fail2Ban SSH 防护
ft version             # 查看版本
ft --help              # 查看全部命令
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

### 安装 Fail2Ban

```bash
ft install fail2ban
```

该命令从 Ubuntu 官方仓库安装 `fail2ban` 和 `nftables`。执行系统修改前，ft 会调用 `/usr/sbin/sshd -T` 读取 OpenSSH 展开 `Include` 后的当前有效端口，并原子更新自己管理的 `/etc/fail2ban/jail.d/zz-ft-sshd.conf`。例如 SSH 当前使用 10222，生成的关键配置就是：

```ini
[sshd]
enabled = true
port = 10222
```

ft 不会专门查找或硬编码 10222：当前是 22 就同步 22；迁移期 SSH 同时监听 22 和 10222，就同步为 `port = 22,10222`。随后 ft 会校验配置、启用并重启 `fail2ban.service`，让已经运行的 nftables jail 应用这些端口。重复执行会根据 SSH 当前配置更新同一个文件。

如果 `sshd -T` 无法运行，命令会在 apt、配置写入和服务操作前停止。只有需要绕过检测（例如端口完全由自定义 systemd socket 配置控制）时才显式传入：

```bash
ft install fail2ban --ssh-port 10222
```

自动检测和 `--ssh-port` 都只配置 Fail2Ban，不会修改 sshd，也不会开放 UFW、云安全组或其他防火墙端口。切换 SSH 端口时应保留当前会话，先确认新端口已监听且能从新连接登录，再运行 ft 同步 Fail2Ban，确认状态后才关闭旧端口。

当前 Ubuntu 24.04（Fail2Ban 1.0.2）和 Ubuntu 26.04（Fail2Ban 1.1.0）的包默认规则一致：

- 默认只启用 `sshd` jail，Nginx 等其他 jail 不启用
- 同一来源 IP 在 10 分钟内匹配 5 次 SSH 登录失败后，封禁 10 分钟
- 从 systemd journal 读取 SSH 日志，使用 nftables 执行封禁
- 使用 `mode = normal`，只封该来源访问 SSH TCP 端口，不封它访问其他端口
- 默认不启用递增封禁，也不发送邮件通知

ft 会自动同步 sshd 的有效端口，但远程管理端的来源 IP 不会自动加入白名单；共享 NAT、VPN 或堡垒机出口在调低 `maxretry` 前需要评估误封风险。

调整时不要直接修改包管理的 `jail.conf` 或 `jail.d/defaults-debian.conf`，而应新建 `/etc/fail2ban/jail.d/sshd.local`，例如：

```ini
[sshd]
bantime = 1h
findtime = 10m
maxretry = 5
# port = 10222
# ignoreip = 127.0.0.1/8 ::1 203.0.113.10
```

不要直接编辑 `zz-ft-sshd.conf`，因为下次安装时 ft 会更新它。Fail2Ban 会在所有 `.conf` 之后加载 `.local`，因此需要覆盖自动端口或调整其他规则时，继续使用 `sshd.local`。

修改后先校验，再重启服务并查看实际状态：

```bash
sudo fail2ban-client -t
sudo systemctl restart fail2ban.service
sudo fail2ban-client status sshd
```

如果服务重启后状态命令暂时提示无法连接 socket，等待片刻后重试。

安装前会通过 `dpkg-query` 检查 `fail2ban` 和 `nftables`；两者都完整安装时跳过 apt 更新和包安装，但仍会校验配置并确保服务运行。任一包缺失或处于半配置状态时执行完整安装流程。

三个安装命令都会先校验操作系统；每一步实时透传输出，任一步失败立即停止并保留底层退出码。

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
internal/installer/   # Docker/Nginx/Fail2Ban 安装步骤构建
internal/sshx/        # OpenSSH 有效端口检测与解析
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
