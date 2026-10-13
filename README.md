# ft

面向 Linux 服务器的运维命令封装 CLI：系统初始化、常用软件安装等，一条命令搞定。

**定位**：支持各主流 Linux 发行版，逐步扩展适配
**当前支持**：Ubuntu 24.04 / Ubuntu 26.04（暂不支持的系统会明确报错拒绝执行，不会误操作）

## 安装

```bash
curl --proto '=https' -fsSL https://raw.githubusercontent.com/ABT-Tech-Limited/ft/main/install.sh | sh
```

脚本会校验 SHA-256 后安装到 `/usr/local/bin/ft`；也可从 [Releases](https://github.com/ABT-Tech-Limited/ft/releases) 手动下载对应架构压缩包，提取其中的 `ft` 放入 `PATH`。

## 用法

```bash
ft init                # 初始化系统：apt 更新升级 + 安装常用工具
ft install docker      # 安装并启动 Docker Engine（官方 apt 源）
ft install nginx       # 安装并启动 Nginx
ft install fail2ban    # 安装并启动 Fail2Ban SSH 防护（自动同步 SSH 端口）
ft install codex       # 安装/更新 Codex CLI，配置并验证自定义 API
ft codex config        # 更新 Codex API HOST/Key，停止共享 app-server 让新配置生效
ft ssh port            # 查看当前生效的 SSH 端口
ft ssh port 10222      # 修改 SSH 端口（校验后重启，自动同步 fail2ban，检测 ufw）
ft ufw cloudflare      # 仅允许 Cloudflare IP 访问 80/443，自动注册每小时刷新
ft ufw status          # 查看 ufw 状态与全部规则（含 ft 托管规则统计）
ft ufw reload          # 重载规则并重启 ufw 服务，随后校验防火墙仍在运行
ft update              # 更新到最新稳定版（SHA-256 校验 + 原子替换）
ft version             # 查看版本
ft --help              # 查看全部命令
```

详细说明：

- [ft init — 系统初始化](docs/init.md)
- [ft install / ft update — 软件安装与更新](docs/install.md)（Docker、Nginx、Fail2Ban、Codex、自更新）
- [ft codex — Codex 配置管理](docs/codex.md)（更新 API HOST/Key 并刷新共享 app-server）
- [ft ssh — SSH 端口管理](docs/ssh.md)
- [ft ufw — 防火墙管理](docs/ufw.md)（Cloudflare 白名单、状态、重载）

## 开发

```bash
go build ./...   # 构建
go test ./...    # 测试
go vet ./...     # 静态检查
```

目录结构、发布流程见 [docs/development.md](docs/development.md)。
