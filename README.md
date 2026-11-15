# Tailcat Manager（Go 版）

跨平台桌面应用，管理多个独立的 Tailcat → ADB 会话。macOS / Windows / Linux 同一套代码、同一套功能。

Go 版把 Swift 版外部调用的 `tailcat` 命令行换成了 **`github.com/tailscale/tailcat` 库内嵌**：不再需要单独安装 tailcat，工具设置里只剩 `adb`（必需）和 `scrcpy`（可选）。转发、探测、客户端身份、DERP 地图全部在进程内完成。

## 构建

需要 Go 1.27.3 及以上，纯 Go、无 cgo。

```sh
go build ./cmd/tailcat-manager        # 当前平台
CGO_ENABLED=0 GOOS=linux go build ./cmd/tailcat-manager
```

界面演示（模拟设备，不执行真实命令、不碰凭据库）：

```sh
go run ./cmd/tailcat-manager --demo
```

## 平台前提

| 平台 | ADB | scrcpy | 凭据库 | 自动安装 |
|---|---|---|---|---|
| macOS | Homebrew / Android SDK | 可选 | Keychain | `brew` |
| Windows | winget / Android SDK | 可选（需手动安装） | Credential Manager | `winget` |
| Linux | 发行版包 / Android SDK | 可选 | Secret Service（无则仅内存） | `apt` / `dnf` / `pacman` |

对端（车机 / EVCC）仍需运行真正的 `tailcat` server 并给出 `tc...` 连接地址。

添加设备时可选两种连接方式（默认前者）：

- **通过 Tailcat 转发**：填 `tc...` 地址，走 Tailcat 通道，适合跨网/远程设备。
- **本地 ADB 直连**：填 `IP:端口` 或 USB 序列号（如 `emulator-5554`），直接用本机 adb server，不经过 Tailcat、不使用设备身份；`IP:端口` 会先执行 `adb connect`。

## 数据位置

- 配置：macOS `~/Library/Application Support/TailcatManager/configuration.json`；Linux `${XDG_CONFIG_HOME:-~/.config}/TailcatManager/`；Windows `%AppData%\TailcatManager\`。目录 0700、文件 0600。
- 连接地址只进系统凭据库，**不写配置文件**。
- 直连设备的目标地址存在配置里（它不是凭据）；Tailcat 设备只有 `tc...` 地址进凭据库。
- 设备身份：`<配置目录>/Identities/<uuid>-connection.private.json` 与 `-probe.private.json`，格式与 `tailcat --key=` 一致。
- 历史：`<配置目录>/history.db`（SQLite，纯 Go 驱动 `modernc.org/sqlite`），只存任务、通道日志、连通测试结果；配置、凭据、私钥都不进这个库。删掉它就等于清空历史，不影响使用。

## 与 Swift 版的差异

- 不需要外部 `tailcat`；转发端口绑定成功即就绪，不再解析 stderr、没有 5 秒等待。
- 私钥由本进程加载（Swift 版交给 CLI 进程），文件权限与格式不变。
- 新增 **“等待处理”** 状态：用户在本机 `adb disconnect`、或设备被映射到别的本地端口时，应用会停下来让用户选 **重新连接 / 改用其他本地端口 / 稍后处理**，不再偷偷重连。
- 不再支持识别 `tailcat-adb` 命令行自己建的 socat 桥（只做 GUI 单通道）。

## 体积

单文件约 22–26 MB（`-ldflags "-s -w"`），主要来自 `tailscale.com` 与 WireGuard 数据面。实测数据见 [docs/build-notes.md](docs/build-notes.md)。

## 文档

- [docs/architecture.md](docs/architecture.md)：架构、状态机、两种连接方式、外部干预检测、安全设计与决策记录
- [docs/build-notes.md](docs/build-notes.md)：依赖版本、体积与构建耗时实测
- [docs/CHANGELOG.md](docs/CHANGELOG.md)：版本变更记录（后续改动都记在这里）
- [AGENTS.md](AGENTS.md)：仓库规则（含"改行为必须更新 CHANGELOG"）

## 测试

```sh
gofmt -l .      # 必须无输出
go vet ./...
go test -race ./...
go test ./internal/compat/   # 第三方 API 契约
```

## 许可

MIT。图标素材沿用上游 BSD 3-Clause，见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
