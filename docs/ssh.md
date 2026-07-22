# ft ssh — SSH 端口管理

## 修改 SSH 端口

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

## 查看当前端口

不带参数运行 `ft ssh port` 可查看当前生效端口。
