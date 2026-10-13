# ft codex — Codex 配置管理

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

## 为什么不能只手改文件

Codex 0.162 起交互式 `codex` 默认连接共享的 app-server 守护进程（`codex --no-daemon` 可绕过）。该进程只在启动时读取 `config.toml` 和 `auth.json`，手动改完文件后它仍持有旧 HOST/Key，典型表现是：`codex exec` 和 `codex --no-daemon` 正常，交互式 `codex` 报 `401 Unauthorized`。

`ft codex config` 和 `ft install codex` 的修复流程在保存 Key 后都会停止守护进程，避免这种状态不一致。已经手改过文件的，执行 `codex app-server daemon stop` 或 `codex app-server daemon restart` 再启动 `codex` 即可。
