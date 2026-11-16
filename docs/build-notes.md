# 构建基线（Build notes）

本文件记录每次依赖升级后的实测数据。任务 0 首次建立，后续任务在需要时追加。

## 依赖版本（任务 0 实测，2026-11-15）

| 依赖 | 版本 | 说明 |
|---|---|---|
| Go | 1.27.3（工具链自动下载 1.27.4） | mygo 与 tailcat 都要求 ≥1.27.3 |
| `github.com/egoist/mygo` | v0.9.1 | 纯 Go、无 cgo |
| `github.com/oneo-me/onevra/source` | v0.2026.18 | 其 `go.mod` 钉 mygo v0.7.4，实际用 v0.9.1 编译通过 |
| `github.com/tailscale/tailcat` | v0.7.0 | 当前 latest；`@latest` 解析结果 |
| `tailscale.com` | v1.103.0-pre.0.20260916030321-a2263542f260 | tailcat 的传递依赖（pre-release，已锁进 go.sum） |

**版本冲突结论**：onevra v0.2026.18 声明 `mygo v0.7.4`，我们用 v0.9.1，`go build ./...` 与
`go test ./internal/compat` 均通过，无需降级或 `replace`。`internal/compat/compat_test.go`
用编译期断言锁住两边共 40 余个 API（`mygo.NewTray`、`mygo.Secrets`、`ui.NewTester`、
`components.Table/Dialog/Tabs`、`tailcat.ParseAddr/ProxyConns/DialTCPPort/DiscoPing` 等），
上游一改签名 CI 立刻红。

## 构建耗时（Windows/amd64，本机，CGO_ENABLED=0）

| 目标 | 耗时 | 产物体积 `-s -w` | 产物体积默认 |
|---|---|---|---|
| windows/amd64（增量） | 1.3 s | 13.6 MB | 19.2 MB |
| linux/amd64（首次跨平台） | 13.3 s | 12.7 MB | — |
| darwin/arm64（首次） | 14.2 s | 12.2 MB | — |
| darwin/amd64（首次） | 13.3 s | 13.3 MB | — |

首轮依赖下载（`go mod download`）约 2–4 分钟，之后都在秒级。

## 资源页与 APK 解析（2026-11-18 实测，Windows/amd64，CGO_ENABLED=0）

| 项目 | 数值 |
|---|---|
| `github.com/avast/apkparser` | v0.0.0-20260423123151-7fcaee440f68（纯 Go；只额外带 `klauspost/compress`，图里已有） |
| 单独链接体积增量 | hello world 1.85 MB → 引入 apkparser 2.83 MB，约 **+1.0 MB** |
| 当前整包产物 | 默认 **48.8 MB**、`-s -w` **33.9 MB**（含终端插件与 tailscale，比上表的 19.2 MB 老数据新，上表数字来自更早的提交） |
| 首次打开资源页 | 每个应用拉一次 base.apk（数十 MB × 应用数），之后 7 天只读缓存；页面本身每 2 秒一次 `adb shell` 采样 |

结论：新增依赖对体积的影响约 1 MB，可接受；真正的成本在**首次**的资源页打开（网络与时间），已用 7 天磁盘缓存与手动刷新兜住。

## 终端插件（libghostty-vt）

详情页的内嵌终端来自 `github.com/egoist/mygo/plugins/terminal`，它**不使用 cgo**，但运行期要加载 libghostty-vt：

- `go run` / `go build` 产物首次打开终端页时会下载一次到用户缓存目录；
- 发布包应改用 `mygo build`，它会把该库打进应用（`plugins/terminal/mygo-plugin.json` 声明文件名与 SHA-256），否则用户首次使用终端需要联网；
- 下载失败时界面显示「终端不可用：…」，其他功能不受影响。

## 体积预估与结论

- 当前 app 还没 import tailcat（转发器在任务 6 接入），`mygo + onevra` 已占 12–14 MB（stripped）。
- 含 tailcat 的等价二进制（`go test -c ./internal/compat`，还多带 testing 框架）实测 **22.2 MB** stripped。
- 任务 17 实测成品（含 tailcat 客户端）：**28.6 MB**（`-ldflags "-s -w"`，windows/amd64），
  落在预估区间上沿。加上历史库（`modernc.org/sqlite`，纯 Go）后为 **32.5 MB**。
  仍高于 mygo 宣传的"几 MB"，README 已如实写明。
- `-ldflags "-s -w"` 稳定省掉 30% 左右，发布构建一律带上。

## 复核方法

```sh
CGO_ENABLED=0 go build -ldflags "-s -w" -o dist/tm ./cmd/tailcat-manager
ls -l dist/tm
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags "-s -w" -o dist/tm-linux ./cmd/tailcat-manager
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags "-s -w" -o dist/tm-darwin-arm ./cmd/tailcat-manager
go test ./internal/compat/   # 第三方 API 契约
```
