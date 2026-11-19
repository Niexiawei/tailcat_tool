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

## 打包（`go tool mygo build`，2026-11-19 实测）

发布构建走 `mygo.json`，工具本身钉在 `go.mod` 的 `tool` 指令里，所以本地与 CI 是同一条命令：

```sh
CGO_ENABLED=0 go tool mygo build -platform windows/amd64
CGO_ENABLED=0 go tool mygo build -platform linux/amd64,linux/arm64
CGO_ENABLED=0 go tool mygo build -platform darwin/universal -sign -   # 需要 macOS
```

| 目标 | 耗时（本机 Windows/amd64 上交叉构建） | 产物 | 体积 |
|---|---|---|---|
| windows/amd64 | 41 s（`-skip-installer`）、21 s（增量 + NSIS） | `dist/windows-amd64/Tailcat Manager.exe` | 32.4 MB |
| windows/amd64 安装包 | +15 s（首次需下载 NSIS 3.13，约 2 s） | `tailcat-manager-setup-0.2.0-amd64.exe` | 10.8 MB |
| linux/amd64 | 49 s | `tailcat-manager` / `.deb` / `.rpm` / `.tar.gz` / `PKGBUILD` | 31.7 MB；每个包 14.1 MB |
| darwin/universal | 编两遍（CI 实测见 artifact） | `.app` + `tailcat-manager-0.2.0.dmg` | 待 CI 记录 |

`mygo build` 与 `go build` 的差别，以及为什么发布不能只发 `go build` 的产物：

1. **终端插件进包**：`ghostty-vt.dll`（2.2 MB）/ `libghostty-vt.so` / `libghostty-vt.dylib` 与可执行文件并排、随包走，用户首次打开终端页不需要联网下载。
2. **图标与版本进可执行文件**：Windows 会生成 `.syso`（图标、清单、版本号），macOS 生成 `AppIcon.icns`，Linux 生成 `.desktop`、多尺寸 PNG 与安装脚本。
3. **版本号**：`mygo.json` 的 `version` 通过 `-X github.com/egoist/mygo.packageVersion` 链进去，`tailcat-manager --version` 打印的就是它；`go build` 的产物没有这个变量，打印 `dev`。
4. **资源目录**：`resources/` 会整份拷到可执行文件旁边（当前只有 `icon.png`），`resources/<os>[-<arch>]/` 只发给对应平台。

`dist/` 已被 `.gitignore` 忽略；不要把产物提交进仓库。

## 体积预估与结论

- 当前 app 还没 import tailcat（转发器在任务 6 接入），`mygo + onevra` 已占 12–14 MB（stripped）。
- 含 tailcat 的等价二进制（`go test -c ./internal/compat`，还多带 testing 框架）实测 **22.2 MB** stripped。
- 任务 17 实测成品（含 tailcat 客户端）：**28.6 MB**（`-ldflags "-s -w"`，windows/amd64），
  落在预估区间上沿。加上历史库（`modernc.org/sqlite`，纯 Go）后为 **32.5 MB**。
  仍高于 mygo 宣传的"几 MB"，README 已如实写明。
- `-ldflags "-s -w"` 稳定省掉 30% 左右，发布构建一律带上。

## 复核方法

```sh
go tool mygo build -platform windows/amd64            # 含安装包
go tool mygo build -platform linux/amd64 -o /tmp/tm  # 想要别的输出目录用 -o
"./dist/windows-amd64/Tailcat Manager.exe" --version  # 应当等于 mygo.json 的 version
```

```sh
CGO_ENABLED=0 go build -ldflags "-s -w" -o dist/tm ./cmd/tailcat-manager
ls -l dist/tm
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags "-s -w" -o dist/tm-linux ./cmd/tailcat-manager
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags "-s -w" -o dist/tm-darwin-arm ./cmd/tailcat-manager
go test ./internal/compat/   # 第三方 API 契约
```
