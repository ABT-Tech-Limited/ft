# ft install / ft update — 软件安装与更新

Docker、Nginx 和 Fail2Ban 安装命令会先校验操作系统；每一步实时透传输出，任一步失败立即停止并保留底层退出码。Codex 是独立的用户级安装流程，不受 Ubuntu apt 检查限制。

## 安装和配置 Codex

```bash
ft install codex
```

该命令必须以实际使用 Codex 的普通用户直接运行，不要加 `sudo`。它支持 OpenAI 官方安装脚本支持的 macOS 和 Linux，流程如下：

1. 检测当前 `codex --version`
2. 下载并执行 OpenAI 官方 standalone installer；未安装时安装 latest，已有版本时检查并更新到 latest
3. 检测安装后的实际版本（优先 `CODEX_INSTALL_DIR` 或 `~/.local/bin/codex`，避免 PATH 中遗留的 npm/brew 旧版本）
4. 使用严格配置、临时会话和只读沙箱发起一次最小模型请求
5. 如果请求成功，提示“Codex 配置正确，可以使用”
6. 如果失败，交互输入 API 请求地址 HOST 和 API Key，显示完整脱敏配置，确认后保存并重新验证

`ft` 管理以下 Codex 默认配置：

```toml
model_provider = "custom"
model = "gpt-5.6-sol"
model_reasoning_effort = "ultra"
model_context_window = 1000000
model_auto_compact_token_limit = 800000

[model_providers.custom]
name = "Custom"
base_url = "https://your-api-host.example.com/v1"
wire_api = "responses"
requires_openai_auth = true
```

HOST 必须是完整 URL，不能包含用户名、密码、query 或 fragment；末尾 `/` 会自动规范化。远程服务必须使用 HTTPS；HTTP 只允许 `localhost`、`127.0.0.0/8` 或 `::1` 回环地址，避免 API Key 被明文传输。

确认页会显示最终将写入的完整 `config.toml` 和脱敏后的 Key。API Key 不会写入 `config.toml`、命令参数或日志；`ft` 通过标准输入调用官方 `codex login --with-api-key`，由 Codex 自己写入操作系统凭据存储或 `~/.codex/auth.json`。`~/.codex/config.toml` 中无关的用户配置和注释会保留；新目录和配置文件分别使用 `0700`、`0600` 权限并原子替换。若原 TOML 语法已损坏，确认页会明确说明，写入前先创建 `0600` 权限的 `.bak` 备份；若确认期间配置被其他进程修改，命令会拒绝覆盖并要求重试。

如果写入后的真实请求仍失败，命令不会误报成功。检查 HOST 是否指向兼容 Responses API 的 base URL、Key 是否有效，以及该服务是否提供 `gpt-5.6-sol`，然后重新运行 `ft install codex`。官方安装器默认把命令放在 `~/.local/bin`；当前终端若还找不到 `codex`，执行：

```bash
export PATH="$HOME/.local/bin:$PATH"
```

## 安装 Docker

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

## 安装 Nginx

```bash
ft install nginx
```

该命令从 Ubuntu 官方仓库安装 `nginx`，然后启动 `nginx.service` 并设为开机启动。它不会修改 UFW、防火墙、TLS 证书或站点配置。

如果 `nginx` 包已完整安装，命令会跳过 apt 更新和包安装，不升级现有版本，只确保服务已启动并开机自启；缺失或半配置时才执行完整安装流程。

## 安装 Fail2Ban

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

## 更新 ft

```bash
ft update
```

该命令查询 `ABT-Tech-Limited/ft` 的 GitHub Latest Release。只有远端稳定版语义版本高于当前版本时才更新；版本相同或本地版本更高时不会下载或替换文件，draft 和 prerelease 不参与自动更新。

更新时，ft 会根据当前架构选择 `linux_amd64` 或 `linux_arm64` 的 GoReleaser 压缩包，同时下载 `checksums.txt`。压缩包必须通过 SHA-256 校验，并且只会从安全的 tar.gz 结构中提取唯一的顶层普通文件 `ft`。校验、解压或下载任一步失败都不会修改当前二进制。

验证完成后，ft 在当前可执行文件所在目录创建临时文件，再通过原子重命名完成替换。如果安装位置（例如 `/usr/local/bin/ft`）需要更高权限，命令会自动调用 `sudo` 执行同样的暂存和原子替换流程。`dev` 构建会拒绝自更新；请先安装正式 Release。当前自更新仅支持 Linux `amd64` 和 `arm64`。
