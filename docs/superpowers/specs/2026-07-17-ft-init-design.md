# ft — Linux 运维命令封装 CLI：`ft init` 设计文档

日期：2026-07-17
状态：已定稿（自主模式：决策采用惯例默认值，均附理由，可事后调整）

## 目标

`ft` 是一个封装常用 Linux 运维命令的 CLI 工具（后续会扩展 nginx、docker 安装等子命令）。
本期交付：

1. `ft init`：系统初始化 —— `apt-get update` + `apt-get upgrade -y` + 安装常用工具包
2. 仅支持 Ubuntu 24.04 / 26.04，其他系统明确报错拒绝执行
3. GoReleaser + GitHub Actions：打 tag 推送后自动交叉编译并发布 GitHub Release

## 方案权衡

### CLI 框架

| 方案 | 优点 | 缺点 |
|------|------|------|
| A. 纯标准库 flag + switch | 零依赖、二进制小 | 子命令增多后 help/flag/嵌套全要手写 |
| B. spf13/cobra（选定） | Go CLI 事实标准（kubectl/gh/docker 同款），子命令、help、补全开箱即用 | 引入一个依赖（可接受） |
| C. urfave/cli | 与 B 类似 | 生态与惯例弱于 cobra |

选 **B**：项目明确会持续加子命令，扩展性优先。

### 其余决策

- **OS 检测**：解析 `/etc/os-release`（systemd 规范文件）的 `ID` + `VERSION_ID`，白名单 `ubuntu` × {`24.04`, `26.04`}。文件不存在（如 macOS）或不在白名单 → 报错退出，列出支持的系统。
- **apt-get 而非 apt**：`apt` 的 CLI 官方声明不保证稳定，脚本调用会输出 warning；`apt-get` 行为等价且面向脚本。
- **权限**：`euid != 0` 时自动为命令加 `sudo` 前缀（用户体验：`ft init` 直接可用）；root 下直接执行。
- **无人值守安全**：`DEBIAN_FRONTEND=noninteractive` + `-o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold`，防止 upgrade 遇到 conffile 冲突时交互挂起。
- **默认包列表**：curl、wget、git、vim、unzip、zip、tar、net-tools、htop、ca-certificates、gnupg、lsb-release、software-properties-common（用户指定 curl/net-tools「等」，此为常用运维集合，集中定义便于增删）。

## 架构

```
ft/
├── main.go                      # 入口；version 变量由 GoReleaser ldflags 注入
├── cmd/
│   ├── root.go                  # cobra root：--version、全局说明
│   └── init.go                  # ft init：编排检测 → update → upgrade → install
├── internal/
│   ├── osrelease/               # /etc/os-release 解析 + 支持性判断（纯函数，单测覆盖）
│   └── aptx/                    # apt 命令构建（纯函数，单测覆盖）+ 流式执行
├── .goreleaser.yaml             # linux amd64/arm64，CGO_ENABLED=0，tar.gz + checksums
└── .github/workflows/
    ├── ci.yml                   # push/PR → build + vet + test
    └── release.yml              # tag v* → GoReleaser 发布
```

数据流：`ft init` → 读 `/etc/os-release` → 不支持则报错退出 → 依次执行三个 apt 阶段，实时透传 stdout/stderr，任一阶段失败立即终止并返回非零退出码。

可测性：`osrelease` 解析/判断与 `aptx` 命令构建为纯函数（表驱动测试，macOS 上可跑）；真实执行层为薄封装，不做单测，由 Ubuntu 上人工/CI 验证。

## 发布流程

1. 本地开发 → push `main` → `ci.yml` 跑 build/vet/test
2. `git tag v0.1.0 && git push origin v0.1.0` → `release.yml` 用 GoReleaser 编译 linux/amd64 + linux/arm64，产出 tar.gz + checksums，自动创建 GitHub Release
3. 版本号经 `-X main.version={{.Version}}` 注入，`ft --version` / `ft version` 可查

## 错误处理

- 非 Ubuntu 24.04/26.04：打印检测到的系统与支持列表，exit 1
- apt 阶段失败：停止后续阶段，透传底层退出码
- 非交互环境缺 sudo 权限：由 sudo 自身报错透传

## 范围外（YAGNI）

- 包列表配置文件 / 命令行自定义包
- 其他发行版（Debian/CentOS）适配层
- `ft install nginx/docker` 等后续子命令（架构已预留 cmd/ 扩展点）
