# ft codex — Codex 更新与配置管理

## 更新 Codex 到最新版本

```bash
ft codex update
```

依次执行：

1. 检测当前 `codex --version`；未安装时报错并提示先运行 `ft install codex`
2. 下载并执行 OpenAI 官方 standalone installer，更新到 latest；已是最新时不重复下载
3. 检测安装后的实际版本，输出“Updated Codex CLI from X to Y”或“Codex CLI X is already the latest release”
4. 执行 `codex app-server daemon update`，让共享 app-server 守护进程的包跟上 CLI 版本

第 4 步的结果会用一句话说明：守护进程正在运行且版本落后时，Codex 会下载对应版本的守护进程包并重启它；守护进程已停止时只更新包，下次启动 `codex` 生效；版本已是最新时不重启；尚未选择受管理包时，Codex 可能返回 `unsupported`。若更新明确报错 `not managed by codex app-server daemon`，ft 会复用下文的安全停止流程，等待旧进程退出后重试更新；不会仅凭进程名或 `PPID=1` 猜测目标。该步骤需要联网，有新版本时会额外下载一份与 CLI 同体积的守护进程包。更新包不等于重新加载配置；需要立即加载现有配置时运行 `ft codex reload`。

命令不修改配置和 Key，也不发起模型请求。必须以实际使用 Codex 的用户直接运行，不要加 `sudo`。安装器或守护进程更新任一步失败都会报错并保留底层错误；守护进程更新失败时 CLI 已更新完成，按提示手动执行 `codex app-server daemon update` 即可。

## 更新 API HOST 和 API Key

```bash
ft codex config
```

交互输入 Responses API 的 HOST 和 API Key，显示将写入的完整脱敏配置，确认后依次：

1. 原子写入 `~/.codex/config.toml`：重置 ft 托管字段（模型、provider 等），保留无关配置和注释
2. 通过标准输入调用官方 `codex login --with-api-key` 保存 Key，Key 不出现在参数、日志或 `config.toml` 中
3. 执行与 `ft codex reload` 相同的停止、启动、状态验证流程，使新 daemon 立即加载配置
4. 使用严格配置、临时会话和只读沙箱发起一次最小模型请求验证；重载和请求均成功才提示“Codex 配置正确，可以使用”

必须以实际使用 Codex 的用户直接运行，不要加 `sudo`。Codex 未安装时会提示先执行 `ft install codex`。HOST 规则与 `ft install codex` 相同：完整 URL，远程必须 HTTPS，不含用户名、密码、query 或 fragment。ft 托管的默认配置见 [ft install](install.md#安装和配置-codex)。

确认写入前会提示：重载会断开客户端连接，并可能中断正在运行的任务。请先暂停任务，在独立 SSH 终端操作，而不是让正在重启的 Codex server 执行此命令。

任一步失败命令立即报错并保留底层错误，不会再把未停止的旧 server 当作成功。重载失败时，配置和 Key 已保存：处理进程问题后运行 `ft codex reload` 即可，不必重复输入 HOST/Key。模型请求仍失败时再检查 HOST、Key 和服务是否支持配置中的模型。

## 重载现有配置

```bash
ft codex reload
```

不修改 `config.toml` 或凭据，不运行 CLI 升级，也不发起模型请求。它重启当前 `CODEX_HOME` 的共享 app-server，而非发送配置热更新信号；首次启动受管理 daemon 时，Codex 自身可能需要下载 daemon 包。

`CODEX_HOME`（未设置时使用 `HOME`）必须是绝对路径，不能带首尾空白或 `..` 路径段。ft 会拒绝这些有歧义的值，避免路径清理后误操作另一个目录的 server；目录名中间的空格和普通软链接仍受支持。

流程如下：

1. Linux 上先检查目标 server 是否为当前命令的祖先进程；如果是，要求改在独立 SSH 终端操作，避免连同 ft 一起停止。然后调用 `codex app-server daemon stop`；已停止也可以继续。
2. 如果 Codex 明确报告未托管的 server，在 Linux 上连接当前 `CODEX_HOME` 的 control socket（支持指向 `/tmp/codex-daemon-*` 的软链接），通过内核 `SO_PEERCRED` 确定实际监听者 PID/UID，核对当前用户、真实可执行文件和直接启动的 app-server 参数。不会选择 proxy、其他用户、其他 socket 的 server，也不要求 `PPID=1`；该值不能证明任务空闲。
3. 使用 pidfd 绑定已确认的进程，重新检查 socket 所属进程未变后发送 `SIGTERM`，最多等待 30 秒退出。不会 `SIGKILL`，不会删除 socket 或 PID 文件；无法验证身份、发现目标是当前命令的祖先进程、系统不支持 pidfd 或等待超时都会报错。
4. 再次调用 `daemon stop` 确认旧 server 已停止，随后 `daemon start` 创建新受管理进程，并用 `daemon version` 确认 `running`、非空 `backend` 和运行版本。仅显示 `running` 或有 `managedCodexPath` 不足以证明接管成功。

Linux 自动恢复需要内核 5.3+ 的 pidfd 支持（Ubuntu 24.04/26.04 满足内核版本要求；安全策略也必须允许相关系统调用）。其他平台仍支持受管理 daemon 重载，但不会自动发送信号给未托管 server，需通过原启动程序关闭它后重试。

若停止后再次检测到未托管 server，或启动返回 `alreadyRunning` 而非新进程，ft 会报错要求先断开对应启动入口，不会循环杀进程。停止、启动和状态验证阶段上限 2 分钟。成功后重新连接客户端；成功只说明服务已重启并就绪，不代表 API Key 或模型请求已通过验证。

## 为什么不能只手改文件或只跑官方安装器

Codex 0.162 起交互式 `codex` 默认连接共享的 app-server 守护进程（`codex --no-daemon` 可绕过）。该进程只在启动时读取 `config.toml` 和 `auth.json`，手动改完文件后它仍持有旧 HOST/Key，典型表现是：`codex exec` 和 `codex --no-daemon` 正常，交互式 `codex` 报 `401 Unauthorized`。

`ft codex config` 和 `ft install codex` 的配置修复流程在保存 Key 后都会重载 server。已经手改过文件或已经保存配置但旧 server 仍运行时，执行 `ft codex reload`。独立的 `codex exec` 请求成功不能证明共享 server 已更新，重载步骤会单独验证服务状态。

版本也有同样的问题。守护进程使用 `~/.codex/packages/app-server-daemon` 下自己的一份 Codex，官方安装器只更新 CLI 对应的 `packages/standalone`，所以只跑安装器之后，正在运行的守护进程仍是旧版本，连 `daemon stop` 再 `start` 也不会换成新包。Codex 自带的后台更新最终会把守护进程更新到最新，但时间不确定。`ft codex update` 和 `ft install codex` 在更新 CLI 后都会执行 `codex app-server daemon update`，让守护进程立即跟上。
