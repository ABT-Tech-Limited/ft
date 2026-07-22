# ft ufw — 防火墙管理

## 仅允许 Cloudflare 访问 80/443

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

## 查看 ufw 状态与重载

```bash
ft ufw status
ft ufw reload
```

`ft ufw status` 只读不改，汇总打印：防火墙状态（active/inactive、默认策略、日志级别）、是否开机启动、ufw-docker 是否就绪、每小时 Cloudflare 刷新是否已注册（含执行分钟），以及 `ufw status numbered` 的全部编号规则和 ft 托管规则统计。ufw 未安装时仅提示，不报错。

`ft ufw reload` 依次执行 `ufw reload`（重读全部规则文件）与 `systemctl restart ufw`（完整重跑框架初始化，一并重应用 ufw-docker 注入的 before/after 链），随后复查防火墙仍为 active，否则报错。ufw 未安装或未启用时拒绝执行。
