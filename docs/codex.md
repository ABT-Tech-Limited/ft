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

第 4 步的结果会用一句话说明：守护进程正在运行且版本落后时，Codex 会下载对应版本的守护进程包并重启它；守护进程已停止时只更新包，下次启动 `codex` 生效；版本已是最新时不重启；在这个 `CODEX_HOME` 里从未启动过守护进程时无需处理。该步骤需要联网，有新版本时会额外下载一份与 CLI 同体积的守护进程包。

命令不修改配置和 Key，也不发起模型请求。必须以实际使用 Codex 的用户直接运行，不要加 `sudo`。安装器或守护进程更新任一步失败都会报错并保留底层错误；守护进程更新失败时 CLI 已更新完成，按提示手动执行 `codex app-server daemon update` 即可。

## 更新 API HOST 和 API Key

```bash
ft codex config
```

交互输入 Responses API 的 HOST 和 API Key，显示将写入的完整脱敏配置，确认后依次：

1. 原子写入 `~/.codex/config.toml`：重置 ft 托管字段（模型、provider 等），保留无关配置和注释
2. 通过标准输入调用官方 `codex login --with-api-key` 保存 Key，Key 不出现在参数、日志或 `config.toml` 中
3. 执行 `codex app-server daemon stop` 停止共享 app-server 守护进程；下次启动 `codex` 时自动拉起新进程并加载新配置
4. 使用严格配置、临时会话和只读沙箱发起一次最小模型请求验证，成功才提示“Codex 配置正确，可以使用”

必须以实际使用 Codex 的用户直接运行，不要加 `sudo`。Codex 未安装时会提示先执行 `ft install codex`。HOST 规则与 `ft install codex` 相同：完整 URL，远程必须 HTTPS，不含用户名、密码、query 或 fragment。ft 托管的默认配置见 [ft install](install.md#安装和配置-codex)。

任一步失败命令立即报错并保留底层错误，不会误报成功。停止守护进程失败时，配置和 Key 已保存，按提示手动执行 `codex app-server daemon stop` 后再启动 `codex`。

## 为什么不能只手改文件或只跑官方安装器

Codex 0.162 起交互式 `codex` 默认连接共享的 app-server 守护进程（`codex --no-daemon` 可绕过）。该进程只在启动时读取 `config.toml` 和 `auth.json`，手动改完文件后它仍持有旧 HOST/Key，典型表现是：`codex exec` 和 `codex --no-daemon` 正常，交互式 `codex` 报 `401 Unauthorized`。

`ft codex config` 和 `ft install codex` 的修复流程在保存 Key 后都会停止守护进程，避免这种状态不一致。已经手改过文件的，执行 `codex app-server daemon stop` 或 `codex app-server daemon restart` 再启动 `codex` 即可。

版本也有同样的问题。守护进程使用 `~/.codex/packages/app-server-daemon` 下自己的一份 Codex，官方安装器只更新 CLI 对应的 `packages/standalone`，所以只跑安装器之后，正在运行的守护进程仍是旧版本，连 `daemon stop` 再 `start` 也不会换成新包。Codex 自带的后台更新最终会把守护进程更新到最新，但时间不确定。`ft codex update` 和 `ft install codex` 在更新 CLI 后都会执行 `codex app-server daemon update`，让守护进程立即跟上。
