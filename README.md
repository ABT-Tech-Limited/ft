# ft

面向 Linux 服务器的运维命令封装 CLI：系统初始化、常用软件安装等，一条命令搞定。

**定位**：支持各主流 Linux 发行版，逐步扩展适配
**当前支持**：Ubuntu 24.04 / Ubuntu 26.04（暂不支持的系统会明确报错拒绝执行，不会误操作）

## 安装

从 [Releases](https://github.com/ABT-Tech-Limited/ft/releases) 下载对应架构的压缩包：

```bash
(
	set -eu
	tmp_dir="$(mktemp -d)"
	trap 'rm -rf "$tmp_dir"' EXIT

	case "$(uname -m)" in
		x86_64) arch="amd64" ;;
		aarch64|arm64) arch="arm64" ;;
		*) echo "不支持的架构: $(uname -m)" >&2; exit 1 ;;
	esac

	curl --proto '=https' --proto-redir '=https' -fsSL \
		https://api.github.com/repos/ABT-Tech-Limited/ft/releases/latest \
		-o "$tmp_dir/release.json"
	version="$(sed -n 's/.*"tag_name":[[:space:]]*"v\([^"]*\)".*/\1/p' "$tmp_dir/release.json")"
	case "$version" in
		""|*[!0-9A-Za-z.+-]*) echo "无法获取有效的最新版本号" >&2; exit 1 ;;
	esac

	archive="ft_${version}_linux_${arch}.tar.gz"
	curl --proto '=https' --proto-redir '=https' -fsSL \
		"https://github.com/ABT-Tech-Limited/ft/releases/latest/download/$archive" \
		-o "$tmp_dir/$archive"
	curl --proto '=https' --proto-redir '=https' -fsSL \
		"https://github.com/ABT-Tech-Limited/ft/releases/latest/download/checksums.txt" \
		-o "$tmp_dir/checksums.txt"

	expected="$(
		awk -v name="$archive" '
			$2 == name { count++; hash = $1 }
			END { if (count != 1) exit 1; print hash }
		' "$tmp_dir/checksums.txt"
	)"
	actual="$(sha256sum "$tmp_dir/$archive")"
	actual="${actual%% *}"
	[ "$actual" = "$expected" ] || { echo "SHA-256 校验失败" >&2; exit 1; }

	tar -xzf "$tmp_dir/$archive" -C "$tmp_dir" ft
	sudo install -m 0755 -- "$tmp_dir/ft" /usr/local/bin/ft
)
```

或手动下载 `ft_<version>_linux_<amd64|arm64>.tar.gz`，仅提取其中的 `ft`（例如 `tar -xzf <压缩包> ft`），再放入 `PATH`。

## 用法

```bash
ft init                # 初始化系统
ft install docker      # 安装并启动 Docker Engine
ft install nginx       # 安装并启动 Nginx
ft install fail2ban    # 安装并启动 Fail2Ban SSH 防护
ft ssh port            # 查看当前生效的 SSH 端口
ft ssh port 10222      # 修改 SSH 端口（校验后重启，自动同步 fail2ban，检测 ufw）
ft ufw cloudflare      # 仅允许 Cloudflare IP 访问 80/443，自动注册每小时刷新
ft ufw status          # 查看 ufw 状态与全部规则（含 ft 托管规则统计）
ft ufw reload          # 重载规则并重启 ufw 服务，随后校验防火墙仍在运行
ft update              # 更新到最新稳定版
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

### 修改 SSH 端口

```bash
ft ssh port 10222
```

写入 ft 托管的 drop-in 配置 `/etc/ssh/sshd_config.d/00-ft-port.conf`（不改动主配置文件），
依次执行：`sshd -t` 语法校验 → `sshd -T` 复核生效端口 → `systemctl daemon-reload` →
重启 SSH（自动兼容 socket activation）。校验失败会恢复原配置且**不重启**，零锁死风险。

- 调用 `sshd -T` 前自动确保特权分离目录 `/run/sshd` 存在（0755）。该目录位于 tmpfs，仅在
  SSH 服务运行期间由 systemd 创建，容器内或服务停止后会缺失，届时 `sshd -T`/`sshd -t`
  会直接报 `Missing privilege separation directory` 退出。
- fail2ban 已安装时自动同步 ft 托管的 SSH jail 端口。
- ufw 启用且新端口未放行时拒绝执行（先 `sudo ufw allow <端口>/tcp` 再重试）；ft 不修改防火墙规则。
- 改完后请保持当前会话，先在新终端用 `ssh -p <端口> user@host` 验证连通再退出。

不带参数运行 `ft ssh port` 可查看当前生效端口。

### 仅允许 Cloudflare 访问 80/443

```bash
ft ufw cloudflare
```

拉取 Cloudflare 官方 IP 段（`https://www.cloudflare.com/ips-v4` 与 `ips-v6`），把 TCP 80/443 收紧为仅 Cloudflare 可访问：

1. 删除其他放行 80/443 的 ufw 规则（含 `Nginx Full` 等应用配置规则，逐条打印可审计）；
2. 为每个 Cloudflare 段添加 `ufw allow proto tcp from <段> to any port 80,443`，并用注释 `ft-cloudflare` 标记为 ft 托管；
3. `ufw reload` 生效。

**自动保持最新**：命令成功后会把自己注册到 `/etc/cron.d/ft-ufw-cloudflare`，每小时自动重跑一次，输出追加到 `/var/log/ft-ufw-cloudflare.log`。执行分钟在首次注册时从 1–59 随机选取（不同服务器大概率错开，避免整点集中请求 Cloudflare），之后保持稳定不再变动。托管规则与最新列表一致时不做任何改动（避免每小时无谓重建规则），列表变化时才整体重建。删除该 cron 文件即可停用自动刷新；`cron` 包未安装时跳过注册并给出提示。

**Docker 自动处理**：Docker 发布端口直接走 iptables、绕过 ufw 的 INPUT 链，普通 ufw 规则拦不住 Docker 端口。检测到已安装 Docker 而 [ufw-docker](https://github.com/chaifeng/ufw-docker) 尚未配置时，ft 会自动下载**固定 commit 且校验 SHA-256** 的 ufw-docker 脚本到 `/usr/local/bin/ufw-docker`，执行 `ufw-docker install` 并重启 ufw，然后同时维护 `ufw route allow` 规则，把 Docker 发布的 80/443 一并收紧。注意：ufw-docker 安装后，Docker 发布的**其他**端口也会默认拒绝外部访问，需要放行时用 `ufw-docker allow <容器> <端口>` 或 `ufw route allow`。

执行前置条件（任一不满足都会明确报错退出，防火墙零改动）：

- ufw 已安装且 active。ft 不会代跑 `sudo ufw enable`——请先自行放行 SSH 端口再启用防火墙。
- 默认入站策略为 deny/reject，否则其他 IP 依旧能进；可先执行 `sudo ufw default deny incoming`（确认 SSH 端口已放行）。
- Cloudflare 列表拉取或校验失败（数量异常、混入非法条目等）、ufw-docker 下载校验失败时立即中止。

放行全部端口的规则（如管理 IP 白名单）与可能挡住 Cloudflare 的 DENY/REJECT 规则不会被删除，但会在收尾时提示人工复查。SSH 及其他端口的规则一律不动。云安全组等 ufw 之外的防火墙不在管理范围，需自行确认放行 Cloudflare。

### 查看 ufw 状态与重载

```bash
ft ufw status
ft ufw reload
```

`ft ufw status` 只读不改，汇总打印：防火墙状态（active/inactive、默认策略、日志级别）、是否开机启动、ufw-docker 是否就绪、每小时 Cloudflare 刷新是否已注册（含执行分钟），以及 `ufw status numbered` 的全部编号规则和 ft 托管规则统计。ufw 未安装时仅提示，不报错。

`ft ufw reload` 依次执行 `ufw reload`（重读全部规则文件）与 `systemctl restart ufw`（完整重跑框架初始化，一并重应用 ufw-docker 注入的 before/after 链），随后复查防火墙仍为 active，否则报错。ufw 未安装或未启用时拒绝执行。

## 更新 ft

```bash
ft update
```

该命令查询 `ABT-Tech-Limited/ft` 的 GitHub Latest Release。只有远端稳定版语义版本高于当前版本时才更新；版本相同或本地版本更高时不会下载或替换文件，draft 和 prerelease 不参与自动更新。

更新时，ft 会根据当前架构选择 `linux_amd64` 或 `linux_arm64` 的 GoReleaser 压缩包，同时下载 `checksums.txt`。压缩包必须通过 SHA-256 校验，并且只会从安全的 tar.gz 结构中提取唯一的顶层普通文件 `ft`。校验、解压或下载任一步失败都不会修改当前二进制。

验证完成后，ft 在当前可执行文件所在目录创建临时文件，再通过原子重命名完成替换。如果安装位置（例如 `/usr/local/bin/ft`）需要更高权限，命令会自动调用 `sudo` 执行同样的暂存和原子替换流程。`dev` 构建会拒绝自更新；请先安装正式 Release。当前自更新仅支持 Linux `amd64` 和 `arm64`。

## 开发

```bash
go build ./...   # 构建
go test ./...    # 测试
go vet ./...     # 静态检查
```

目录结构：

```
cmd/                  # cobra 子命令（root、init、install、update、version）
internal/osrelease/   # /etc/os-release 解析与支持性判断
internal/aptx/        # apt-get 命令构建与兼容执行接口
internal/dpkgx/       # dpkg 包安装状态查询
internal/installer/   # Docker/Nginx/Fail2Ban 安装步骤构建
internal/sshx/        # OpenSSH 有效端口检测与解析
internal/sysx/        # context-aware 特权命令执行
internal/updater/     # GitHub Release 检查、校验与原子自更新
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
