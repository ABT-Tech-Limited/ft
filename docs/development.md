# 开发与发布

## 构建与测试

```bash
go build ./...   # 构建
go test ./...    # 测试
go vet ./...     # 静态检查
```

## 目录结构

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
