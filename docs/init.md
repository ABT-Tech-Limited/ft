# ft init — 系统初始化

```bash
ft init
```

`ft init` 依次执行：

1. `apt-get update` — 刷新软件包索引
2. `apt-get upgrade -y` — 非交互升级已装软件（自动保留旧配置文件，不会交互挂起）
3. 安装常用工具：curl、wget、git、vim、unzip、zip、tar、net-tools、htop、ca-certificates、gnupg、lsb-release、software-properties-common

非 root 用户运行时自动加 `sudo`；任一步骤失败立即终止并透传退出码。
