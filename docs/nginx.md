# ft nginx — 默认优化、配置示例与 Cloudflare 同步

面向 Go 服务反向代理和静态网站，兼容 Cloudflare Flexible（Cloudflare 到源站使用 HTTP）。ft 管理基础配置与可信网段，不接管域名、后端地址或站点文件，不修改 UFW、TLS 证书或强制 HTTPS 跳转。

## 安装与应用配置

```bash
ft install nginx
```

安装并启动 Nginx 后会自动应用默认优化，拉取 Cloudflare 网段，并在配置成功后注册独立的每 10 分钟同步任务。重复执行复用原有托管文件和 cron 条目，不创建重复任务。

已有 Nginx 时也可显式执行：

```bash
ft nginx configure
```

该命令要求 Nginx 包已经完整安装，不替用户安装 Nginx。它会检查同步任务所需的 `cron` 包，必要时通过 apt 安装/修复 cron，并启用 cron 服务；已安装的 Nginx 不会仅因缺少 cron 而被升级。需要系统写权限的操作会通过 sudo 提权。

由于同步任务以 root 执行，ft 可执行文件及其父目录必须由 root 持有，且不能允许组或其他用户写入，也不能经过符号链接。建议使用安装脚本安装到 `/usr/local/bin/ft`；不要从用户可写目录或 `go run` 的临时目录注册长期 root 任务。

### 优化范围

| 配置 | 行为 |
|---|---|
| Worker | 保留 `worker_processes auto`，连接容量设置为 `worker_connections 2048` |
| 版本展示 | `server_tokens off`，隐藏版本但不声称完全移除 `Server: nginx` |
| 请求体 | `client_max_body_size 10m`，包括上传文件和表单开销 |
| 代理超时 | `proxy_read_timeout 60s`、`proxy_send_timeout 60s` |
| Gzip | 保留已有 Gzip，设置 `gzip_vary on`、最小长度 `1024`、等级 `4`，扩展 CSS、JavaScript、SVG 类型 |
| 日志与站点 | 保留现有访问/错误日志、站点 include、TLS 设置，不自动启用代理缓存或全站限流 |

代理读写超时是两次读写之间的空闲时间，不是请求总执行时间。严格的一分钟执行上限应由 Go 服务控制；更长任务可转异步。连接容量包括客户端与上游连接，并受系统文件描述符限制，不等同于用户人数。配置命令会检查 nginx.service 的软文件描述符限制；低于 2048 或无法读取时给出警告，不自动修改系统限制。

### 保护现有配置

- 只接受已知 Ubuntu 24.04/26.04 默认主配置，或未被手动修改的 ft 托管主配置；遇到自定义结构会拒绝覆盖。
- 已有同名 HTTP 配置或代理片段不符合当前 ft 内容时拒绝覆盖，不自动接管自定义片段。
- 修改前保存备份，执行 `nginx -t` 后才平滑 reload；校验或应用失败会恢复原托管文件并报错。
- `server_tokens`、`worker_connections` 等在原有正确作用域修改，不依赖 `99-*.conf` 的“后加载覆盖”。Nginx 同层重复指令可能直接报错。
- 不创建、覆盖或启用示例站点。安装、配置和 cron 注册是可分别失败的阶段；失败时软件包可能已经安装，不应把配置回滚理解为卸载软件包。

## 输出配置示例

```bash
ft nginx example         # 同时输出 Go 反代与静态网站示例
ft nginx example go      # 只输出 Go 反代示例
ft nginx example static  # 只输出静态网站示例
```

示例命令只写标准输出，不检测或修改系统、不联网，不要求 root，也可以在非 Ubuntu 开发机器上查看。示例域名、后端地址、WebSocket 路径和静态目录必须按实际业务调整。

### Go 服务接入

先通过 `ft install nginx` 或 `ft nginx configure` 生成所需的托管配置，再在实际含 `proxy_pass` 的 `location` 引用：

```nginx
location / {
    include /etc/nginx/snippets/ft-go-proxy.conf;
    proxy_pass http://ft_example_go_backend;
}
```

完整 demo 展示 `upstream` 空闲连接复用（`keepalive 16`）与源站 HTTP 监听。公共片段使用 HTTP/1.1，规范化 Host、客户端 IP 和原始访问协议，并按请求动态转发 WebSocket 的 Upgrade / Connection。

接入已有站点时，先合并同层已有的 `proxy_http_version` 和 `proxy_set_header`，不能盲目追加重复配置。每个代理 `location` 都应检查是否引用片段；`proxy_set_header` 具有整组继承规则，只在上层引用并不保证所有下层继承。

### WebSocket

公共代理片段已经支持协议升级。普通请求继承全局 `60s` 空闲超时；若某条 WebSocket 路径需要更长空闲时间，可在它自己的 `location` 单独设置，例如 `proxy_read_timeout 300s`。这只是可调整的站点策略，不会自动改变所有接口。

建议 Go 服务约每 30 秒发送心跳，避免 Nginx、Cloudflare 或其他网络设备回收长期无数据连接。WebSocket 不需要因此全局关闭代理缓冲；SSE/AI 流式 HTTP 是另一种场景，应单独配置对应路径。

### 静态资源缓存

静态网站 demo 不引用 Go 代理片段，缓存策略区分资源类型：

- 常见 CSS、JS、图片、字体等资源缓存 1 小时，适合未统一使用内容哈希文件名的普通站点。
- HTML 使用重新验证策略，不设长期强缓存，避免发布后一直加载旧入口页。
- 不存在的静态资源返回 404，不回退为 HTML；不会给 404 响应附加长期缓存策略。
- 示例限制点文件和点目录访问，但保留 `/.well-known/`，避免直接破坏证书验证等合法路径。
- SPA 的 `/index.html` 路由回退需要站点明确选择，不会默认为所有静态站点开启。
- 一年缓存和 `immutable` 仅适用于文件名随内容变化的资源，应在确认构建产物带内容哈希后自行启用，不能直接套到所有文件或 HTML。

浏览器缓存与 Cloudflare 边缘缓存是两层独立策略；这里的源站示例不修改 Cloudflare Cache Rules，也不保证立即刷新已有边缘缓存。

站点改动后校验并重载：

```bash
sudo nginx -t && sudo systemctl reload nginx
```

## Cloudflare 可信网段

ft 从 Cloudflare 官方 `https://www.cloudflare.com/ips-v4` 和 `https://www.cloudflare.com/ips-v6` 拉取网段，校验后用同一份数据生成 real-IP 信任配置和原始来源判断表。

配置布局：

```text
/etc/nginx/nginx.conf                    主配置的受控基础优化
/etc/nginx/conf.d/ft-http.conf            全局参数与可信协议信息处理
/etc/nginx/snippets/ft-go-proxy.conf      站点按需引用的代理片段
/etc/nginx/ft/cloudflare-active.conf     指向同一代网段快照的活动配置
/etc/nginx/ft/                          网段快照与配置备份
```

活动配置通过一次原子切换引用同一代的两份网段文件，避免一份新、一份旧。自动生成的文件不应手工编辑；手工改动会影响后续安全检查。

### IP 与协议的信任边界

- 只有原始连接对端属于官方 Cloudflare 网段时，才接受 `CF-Connecting-IP` 和合法的转发协议信息；不是根据已经还原的访客 IP 判断来源。
- 用户经 HTTPS 访问 Cloudflare，即使源站连接为 HTTP，Go 仍可获得正确的 `X-Forwarded-Proto: https`；直接访问源站并伪造该头不会被无条件信任。
- Go 统一读取 Nginx 规范化后的 `X-Real-IP` / `X-Forwarded-For` / `X-Forwarded-Proto`。片段会清除未规范化的标准 `Forwarded`、`X-Forwarded-Port` 和原始 `CF-Connecting-IP`；已有程序若依赖这些原始头，应先核对再接入。
- Go 应只信任 Nginx 这一层代理，后端端口不应允许外网绕过 Nginx 随意访问。若中间再增加负载均衡，需重新评估代理链。
- 来源属于 Cloudflare 网段不证明请求经过了你自己的 Cloudflare 域名配置，也不阻止源站直连；它不能替代防火墙或源站身份认证。

Flexible 模式的 Cloudflare 到源站链路仍为明文；这里不会擅自改为 HTTPS 或加入可能形成循环的源站强制跳转。需要保护这段链路时，应另行配置证书并迁移到 Full (strict)。

## 每 10 分钟同步

配置成功后注册独立任务 `/etc/cron.d/ft-nginx-cloudflare`，每 10 分钟执行一次，日志追加到 `/var/log/ft-nginx-cloudflare.log`。手动触发：

```bash
ft nginx cloudflare sync
```

同步只维护 Cloudflare 网段快照，不安装软件、不覆盖站点、不重新优化主配置，也不修改 UFW。列表没有变化时跳过 Nginx 重载；任一列表下载或校验失败保留旧快照；配置校验或重载失败恢复原托管配置并报错。首次配置失败不会注册无效的同步任务。同步过程使用互斥锁，并设有 5 分钟执行时限，避免异常阻塞导致定时任务不断堆积；失败后的恢复另有短时限。

删除 `/etc/cron.d/ft-nginx-cloudflare` 可停止定时同步，但仍可手动执行同步命令。再次运行 `ft nginx configure` 或 `ft install nginx` 会检查并重新注册任务。定期检查同步日志，避免长期失败后一直使用旧名单。

Nginx 同步与 `ft ufw cloudflare` 的任务互相独立。UFW 同样改为每 10 分钟刷新；升级 ft 后重新执行 `ft ufw cloudflare`，或等待原任务下一次成功执行，即可迁移旧的每小时计划，复用原文件且不增加重复条目。详情见 [ft ufw](ufw.md)。
