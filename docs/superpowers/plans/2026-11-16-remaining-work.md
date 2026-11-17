# 收尾工作实施计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）跟踪进度；已完成的历史条目写成 `- [x]` 并附取证，不要删除——它们说明"这一条为什么不用再排期"。
>
> 本文件 2026-11-16 制定，**2026-11-17 按 `docs/CHANGELOG.md` 与代码现状整体重写**。上一版的"现状"已经不对（它写于设备页、资源页、文件页、应用信息缓存之前），重写理由见「修订说明」。

**目标：** 把项目从"功能可用、链路验证过一部分"推进到"能装到别人的机器上、出问题能自助、历史能翻、托盘能提示"。

**架构：** 不新增传输路径、不改依赖方向。打包走 `go tool mygo build`（`mygo.json` 驱动，终端插件随包）；启动诊断、历史查询、干预聚合都以**纯函数 + core 窄接口**的形式落在 `internal/core`，`internal/app` 只渲染快照；缓存管理与 DERP 地图两个管理页各有独立计划（`2026-11-19-appmeta-cache-page.md`、`2026-11-19-derp-map-page.md`），共用侧栏入口与路由骨架。

**技术栈：** Go 1.27.3、`github.com/egoist/mygo` v0.9.1（`mygo.json` / `Tray.SetTitle` / `mygo.App.Version()`）、onevra `components`（`Dialog` / `TableVirtualized` / `ButtonOutline`）、`modernc.org/sqlite`、`ui.NewTester` + `png.Encode(tt.Image())`。

**规格：** 没有独立规格文档。需求来源：`docs/CHANGELOG.md` 的 `[未发布]`、`docs/architecture.md` §16「已知缺口与后续工作」、本文件上一版的 P1–P6；用户已裁决的"缓存管理页"与"DERP 地图管理页"移到了上面两个独立计划。取舍写进本文「决策与代价」，落地后回填 `docs/architecture.md` §15。

---

## 基线

| 项 | 值 |
|---|---|
| 分支 / 提交 | `main` @ `ed5d648`（2026-11-17） |
| 测试用例 | 354 个 `Test*`（`grep -rho '^func Test' --include=*_test.go \| wc -l`） |
| 体积 | 默认 48.8 MB、`-s -w` 33.9 MB（Windows/amd64，见 `docs/build-notes.md`） |
| CI | `.github/workflows/ci.yml`：三平台 `gofmt` / `go vet` / `go build` / `go test -race`，外加 `cross` 交叉编译。**没有打包 job** |
| 打包配置 | **不存在**：没有 `mygo.json`、没有 `resources/icon.png`、`go.mod` 没有 `tool` 指令、`--version` 也不存在 |
| 设备页 | 五个页签：概览 / 资源 / 终端 / 文件 / 日志（`internal/app/state.go` 的 `detailOverview` … `detailLog`） |

基线里的页面结构决定了两件事：上一版 P3 的入口（"任务面板"）已被删除，任务并进了设备页「日志」；上一版 P6 的部分真机项已经在做资源页/文件页时顺手验过，因此 P6 是"补验 + 留记录"，不是"从头开始"。

---

## 修订说明（上一版之后发生了什么）

| 上一版写的 | 现在的事实 | 对计划的影响 |
|---|---|---|
| 起点 `feat/go-rewrite` @ `58ead78`，168 个用例 | `main` @ `ed5d648`，354 个用例 | 基线重写 |
| 配置/历史/凭据失败是 `vm.Notice` 一行横幅 | 改成**错误级提示条**（`vm.FailText(env.Notice)`），但 `main.go` 用 `if notice == ""` 逐层兜底，**只有第一个问题会被说出**，其余静默丢弃 | P2 的理由更强了：用户现在连"降级模式"都看不见 |
| P3 从「重启后载入任务面板」进入 | 任务面板已删除，通道日志与任务合并在设备页「日志」标签里，但只读内存（每台 200 行、重启即丢）；`channel_logs` 表从来没有界面读过 | 用户追加要求（两个入口复用同一实现、应用错误也入库、可搜索）后，P3 拆成独立计划 `2026-11-19-logs-page.md` |
| P4 要新增 `listenerLost` 种类 | `AttentionKind` 实际只有三种：`serialDetached` / `portChanged` / `portDrift`，从来没有 `listenerLost` | `listenerLost` 从计划里删掉（`portDrift` 已覆盖"本机端口变了"），只做 `AttentionServerRestart`（上一版叫 `adbServerRestarted`） |
| P4 说"同一监视轮次内"聚合 | 监视器是**每设备一条 ticker**（`startMonitor` → `refresh` → `maybeDetectInterference`），不存在统一轮次 | 改成"时间窗 + 一次全局 `adb devices` 探测"，见 P4 |
| P5 要自己造角标 | mygo 已有 `TrayOptions.Title` / `Tray.SetTitle` / `SetToolTip` | P5 只做取值纯函数与调用，不碰平台层 |
| 打包只是"待做" | 仍未做（无 `mygo.json`），CI 只有 `build` 与 `cross` 两个 job，没有打包 job | P1 保持，步骤按 mygo v0.9.1 的真实字段与 flag 写死 |
| 缓存管理页不存在 | CHANGELOG 已对用户写"缓存管理页将在下一版提供"，core 侧 `AppMetaCache`（`Entries`/`Invalidate`/`Purge`）已就位但**没有 UI 消费者** | 已拆成独立计划 `2026-11-19-appmeta-cache-page.md`，与 DERP 地图页一起先做 |

---

## 现状与缺口

| 能力 | 状态 | 取证 |
|---|---|---|
| 会话管理、两种连接方式、干预检测（三种）、历史写入、内嵌终端、资源页、文件页 | 完成 | `internal/core/attention.go`、`manager*.go`、`internal/app/{resources,files}.go` |
| 真机 ADB 链路、采样与 APK 解析 | **部分验证** | CHANGELOG 里的真机实测：资源采样 21 s → 0.8 s；`Google Play services` 14.65 s → 1.78 s；`pm list packages --user all` 让误判分组 5 → 0；`/sdcard` 是 `symbolic link` |
| Tailcat 真链路（对端跑 server）、未授权、`adb disconnect` 干预弹窗、端口变化、scrcpy、多设备并发、重启持久化 | **未验证** | 只有 `--demo` 与自动化测试 |
| 真机验收记录 | **不存在** | 没有 `docs/verification.md` |
| 安装包（dmg / msi / deb）与签名 | **未做** | 无 `mygo.json`，CI 无打包 job |
| 启动诊断页 | **未做** | `main.go` 只保留第一条 `notice`，其余丢弃 |
| `adb kill-server` 聚合提示 | **未做** | `attention.go` 只有三种 kind，每台设备各自命中 `serialDetached` |
| 统一日志（应用 + 设备 + 任务，可搜索；设备标签复用同一实现） | **已拆出** | 独立计划 `2026-11-19-logs-page.md`；现状：日志只读内存（200 行、重启即丢），`channel_logs` 没有读路径，应用错误从不入库 |
| 托盘「待处理」提示 | **未做** | `keepTrayFresh` 只 `SetMenu`，没有 `SetTitle` / `SetToolTip` |
| 应用信息缓存管理页 | **已拆出** | 独立计划 `2026-11-19-appmeta-cache-page.md`；`core/appmeta.go` 的 `AppMetaCache` 已就位、无 UI 消费者 |
| DERP 地图管理页（列表 / 详情 / 增删改） | **已拆出** | 独立计划 `2026-11-19-derp-map-page.md`；现在只有编辑器里的预设下拉，`SaveRelayPreset` 有写无删，URL 内容无法预览 |

---

## 优先级与顺序

0. **先做两个管理页**：`2026-11-19-appmeta-cache-page.md`（缓存管理）与 `2026-11-19-derp-map-page.md`（DERP 地图）。它们不属本文件，但用户指定完成之后才回到这里。
1. **P1 打包与版本**——"别人能不能装"的唯一硬门槛。
2. **P6 真机验收与记录**——决定"装上去会不会踩空"；其中干预弹窗那一条要在 P4 之后**重验一次**。
3. **P2 启动诊断页**——用户遇到坏配置/坏历史库时能不能自助。
4. **P4 干预聚合**——`adb kill-server` 现在会一次弹 N 条，是真机上最容易让人以为"我做错了"的地方。
5. **P3 统一日志页**——已拆成 `2026-11-19-logs-page.md`；排在本文件所有条目之后，位置不变。
6. **P5 托盘「待处理」提示**——纯观感，随时可做。

P1 与 P6 必须相邻（打出来的包才拿去验收）；P2/P4 互不依赖，可并行；P3 与 P5 都是界面小改动，放在最后。

---

## 审查重点（Review Focus）

这五条是规格没写、但最可能伤到使用者的输入或失败模式。每一条的守护测试（打包/真机类则是验收步骤）写进对应任务的步骤里。

1. **`adb kill-server` 后任何一台设备都不得被自动重连**：聚合提示必须在 `retryOffline` 之前让设备进入 `needsAction`（`TestAttentionStopsRetry` 的推论）。守护见 P4 的 `TestAggregateMarksEveryAffectedDeviceNeedsAction`。
2. **打包产物首次打开终端页必须离线可用**：`go build` 与 `mygo build` 的差别就在这里，验收要在断网状态下做（P1 验收第 2 条）。
3. **启动诊断必须一次说全部问题**：现在 `if notice == ""` 只留第一条，用户修好第一个后重启才看见第二个（P2 的 `TestDiagnoseReportsEveryBrokenPart`）。
4. **真机发现必须先记录再修**：`docs/verification.md` 是交付证据，不是事后回忆；任何一条失败都要先落表（P6「产出」）。
5. **托盘计数按设备数而不是弹窗数**：P4 聚合之后一条 attention 可能挂多台，只数 `len(Attentions())` 会少数（P5 的 `TestTrayBadgeCountsAggregatedDevices`）。

---

## P1 发布打包与版本

### 文件

- 修改：`go.mod`（加 `tool` 指令）、`.github/workflows/ci.yml`、`cmd/tailcat-manager/main.go`、`docs/build-notes.md`、`README.md`、`docs/CHANGELOG.md`
- 创建：`mygo.json`、`resources/icon.png`、`cmd/tailcat-manager/version.go`

### 步骤

- [ ] **步骤 1：声明 CLI 工具**

```sh
go get -tool github.com/egoist/mygo/cmd/mygo@v0.9.1
go tool mygo -h
```

`go.mod` 出现 `tool github.com/egoist/mygo/cmd/mygo`，版本与 `require` 里的 v0.9.1 相同；之后一律 `go tool mygo build`，不在 CI 里 `go install` 到 `PATH`。

- [ ] **步骤 2：写 `mygo.json`**

```json
{
  "name": "Tailcat Manager",
  "slug": "tailcat-manager",
  "identifier": "cc.coauto.tailcat-manager",
  "version": "0.2.0",
  "copyright": "© 2026 Niexiawei",
  "license": "MIT",
  "macos": { "signingIdentity": "" }
}
```

`identifier` 必须与凭据库服务名 `cc.coauto.tailcat-manager.connections` 的前缀一致（`internal/credstore`），一次定死。`out` 用默认 `dist`（已被 `.gitignore` 忽略）。`version` 取 0.2.0：0.1.0 已发布，本轮包含文件页、资源页、缓存页。

- [ ] **步骤 3：图标**

`resources/icon.png`，1024×1024，方图、透明背景。现有 `cmd/tailcat-manager/resources/tray.png` 是托盘用的 32×32 模板图，**放大会糊**：需要从原图重导出（或重绘），另存 `resources/icon-dev.png` 可选（开发版图标）。

- [ ] **步骤 4：版本注入与 `--version`**

`cmd/tailcat-manager/version.go`：

```go
// versionText is the version mygo build links from mygo.json; "dev" for a
// plain go build, which links nothing.
func versionText() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "dev"
}
```

`main()` 的参数循环里加 `case "--version": fmt.Println(app.AppName, versionText()); return`（放在 `RequestSingleInstanceLock` 之前，免得为了打印版本去抢单实例锁）。

**先验证的事**：`mygo.App.Version()` 读的是链接期变量与包信息，理论上前 `Run` 也能取到；如果实测拿不到值，就改成在 `mygo.App.WhenReady` 里打印再 `mygo.App.Quit()`，并在 `version.go` 注释里写明原因。

- [ ] **步骤 5：CI 加打包 job**

`.github/workflows/ci.yml` 新增 `package` job（三平台 matrix），**删掉现有的 `cross` job**（打包 job 已经覆盖交叉编译，两份体积账没有意义）：

```yaml
package:
  strategy:
    fail-fast: false
    matrix:
      include:
        - os: ubuntu-latest
          platform: linux/amd64,linux/arm64
        - os: macos-latest
          platform: darwin/universal
        - os: windows-latest
          platform: windows/amd64
  runs-on: ${{ matrix.os }}
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with: { go-version: '1.27.3', check-latest: true }
    - name: build packages
      env:
        CGO_ENABLED: '0'
        SIGN_IDENTITY: ${{ secrets.MACOS_SIGNING_IDENTITY }}
      shell: bash
      run: |
        if [ "$RUNNER_OS" = "macOS" ]; then
          sign=-
          if [ -n "$SIGN_IDENTITY" ]; then sign="$SIGN_IDENTITY"; else
            echo "::warning::no macOS signing identity; the dmg is ad-hoc signed and Gatekeeper will block it"
          fi
          go tool mygo build -platform "${{ matrix.platform }}" -sign "$sign"
        else
          go tool mygo build -platform "${{ matrix.platform }}"
        fi
    - uses: actions/upload-artifact@v4
      with:
        name: tailcat-manager-${{ matrix.os }}
        path: |
          dist/*.dmg
          dist/*.exe
          dist/*.deb
          dist/*.rpm
          dist/*.tar.gz
          dist/*.AppImage
```

Windows 上 `mygo build` 会为安装器下载 NSIS；runner 断网或下载失败时改用 `-skip-installer` 并只出 `.exe`，同时在 job 里 `::warning::` 说明，不许静默跳过。

- [ ] **步骤 6：文档与变更记录**

`docs/build-notes.md` 补「打包」一节：`mygo build` 与 `go build` 的差异（终端插件 `plugins/terminal/mygo-plugin.json` 被打进包 vs 首次运行联网下载）、三平台产物名、体积实测。`README.md` 补安装段落。`docs/CHANGELOG.md` 记「新增：安装包与版本号；`--version`」。

### 验收

1. 干净机器（无 Go、无 GOPATH 缓存）安装三平台产物后能启动，托盘图标正常。
2. **断网**打开设备页「终端」标签页，能输入并看到提示符——证明 libghostty-vt 在包里。
3. `tailcat-manager --version` 等于 `mygo.json` 的 `version`。
4. CI 三平台 artifact 都能下载，`ls -l` 体积写进 `build-notes.md`。

### 风险

- macOS ad-hoc 签名仍会被 Gatekeeper 拦：README 给出 `xattr -dr com.apple.quarantine` 的临时办法，正式发布需要 Developer ID。
- `darwin/universal` 等于编译两遍，CI 时长翻倍。
- `mygo build` 会改产物名字（`Tailcat Manager.app`、`tailcat-manager_0.2.0_amd64.deb`），脚本不要硬编码旧名字。
- Windows 的资源（图标、版本）由 `mygo build` 生成 `.syso`，与 `go build` 的产物不可比体积。

---

## P6 真机验收与记录

不写新功能，但要**留记录**，并且必须在 P1 的安装包上验（不是 `go run`）。P4 完成后第 4 条要重跑一次。

### 文件

- 创建：`docs/verification.md`

### 清单

| # | 场景 | 期望 | 状态 |
|---|---|---|---|
| 1 | USB 直连（真机序列号 / `emulator-5554`） | 型号 / Android / Shell 权限自动出现 | 已验证（资源页、文件页的"真机实测"走的就是 USB 直连） |
| 2 | 本地网络直连（`IP:5555`） | 探活走 `adb connect`，断开时 `adb disconnect` | **待验**（CHANGELOG 里没有网络直连的实测记录） |
| 3 | Tailcat 真链路（对端跑 `tailcat` server） | 本地端口从 5556 起分配，`adb devices` 出现 `127.0.0.1:5556` | **待验** |
| 4 | 设备未授权 | 状态「ADB 待授权」，不重试，提示在设备上确认 | **待验** |
| 5 | `adb disconnect 127.0.0.1:5556` | 侧栏变「等待处理」，**不自动重连**，弹对话框，三个选项各试一次 | **待验** |
| 6 | 同一台设备被映射到别的端口 | 「端口变化」只作线索，不接管 | **待验** |
| 7 | `adb kill-server`（多台在线） | **一条**聚合提示，含「全部重新连接」 | **P4 完成后验** |
| 8 | scrcpy 屏幕控制 | 窗口能开，参数 1280/4M/30fps，关闭不留进程 | **待验** |
| 9 | 内嵌终端两种模式 | Shell 有提示符；ADB 模式能 `pull` 文件到本地 | **待验** |
| 10 | 两台设备同时在线 | 任务、日志、终端、文件页互不串台 | **待验** |
| 11 | 重启应用 | 设备配置、历史任务都在；历史库损坏时能启动并提示 | **待验** |
| 12 | 断网 / 中继路径 | 批量连通测试给出中继路径与延迟，失败原因可读 | **待验** |

### 产出

`docs/verification.md` 表头固定：日期 / OS / adb 版本 / scrcpy 版本 / 设备型号 / 场景 # / 结果 / 证据（截图文件名或命令输出）。规矩：**任何一条失败先写进这张表，再开修改**（对应 AGENTS.md 的 TDD 顺序）。

### 验收

12 条全部有记录；失败项要么当轮修掉并在 CHANGELOG 记「修复」，要么在 CHANGELOG 的「已知限制」里留一条并注明复现条件。

---

## P2 启动诊断页

### 现状问题

`cmd/tailcat-manager/main.go` 里四处失败各自想留一条 `notice`，但每处都是 `if notice == ""`，所以**只有第一个问题会被说出来**：配置解析失败时，历史库、凭据库、应用缓存三个问题全部消失。用户看到一条 8 秒后自动消失的错误提示条，既不知道当前跑的是降级模式，也不知道该修哪个文件。

### 文件

- 创建：`internal/core/diagnose.go`、`internal/core/diagnose_test.go`、`internal/app/startup.go`、`internal/app/startup_test.go`、`internal/platform/reveal_darwin.go`、`internal/platform/reveal_linux.go`、`internal/platform/reveal_windows.go`
- 修改：`cmd/tailcat-manager/main.go`、`internal/app/app.go`（`Environment`）、`internal/app/state.go`（`Startup` 状态）、`internal/app/screenshots_test.go`（加一条启动页用例）

### 步骤

- [ ] **步骤 1：写失败的测试 `TestDiagnoseReportsEveryBrokenPart`**

`internal/core/diagnose_test.go`：同时给四个错误（配置、历史库、凭据库、应用缓存），断言返回 4 个 `Problem`，每条 `Title` / `Path` 与顺序固定，且 `Config` 的 `Continue=true`。

- [ ] **步骤 2：实现 `core.Diagnose`**

```go
// Problem is one startup problem the user has to decide about.
type Problem struct {
	Title    string
	Detail   string
	Path     string
	Continue bool // false only when the app cannot run at all
}

// DiagnoseInput is what main.go learned before the window opened.
type DiagnoseInput struct {
	ConfigDir     string
	ConfigErr     error
	HistoryErr    error
	CredentialsErr error
	AppMetaErr    error
	ADBPath       string
	ScrcpyPath    string
}

// Diagnose turns the startup failures into user-facing problems, in the
// order the user should fix them.
func Diagnose(in DiagnoseInput) []Problem
```

四条文案必须互相区分（CHANGELOG 已有一条"应用信息缓存不可用…"可复用）：

| 失败 | Title | Continue | 后续 |
|---|---|---|---|
| 配置不可解析 | 配置读取失败，已使用默认设置 | true | 用默认设置继续 / 打开配置目录 |
| 历史库打不开 | 历史库无法打开，本次运行不会保存历史 | true | 继续 / 打开配置目录 |
| 凭据库不可用 | 未检测到系统凭据库，连接地址仅本次运行有效 | true | 继续 |
| 应用缓存不可用 | 应用信息缓存不可用，资源页只显示包名 | true | 继续 |
| `adb` 找不到 | 未找到 adb，连接设备前需要先安装 | true | 打开工具设置 |

- [ ] **步骤 3：`main.go` 收集全部问题**

`buildEnvironment` 不再用 `if notice == ""` 兜底：把四个 `error` 原样放进 `DiagnoseInput`，`env.Problems = core.Diagnose(...)`。`Environment.Notice` 换成 `Problems []core.Problem`；stderr 仍逐条打印（终端用户与 CI 需要）。

- [ ] **步骤 4：视图 `internal/app/startup.go`**

`vm.Startup` 为真时，`ContentView` 渲染启动页而不是路由内容：每条 `Problem` 一张卡片（标题、详情、路径用等宽字体、按钮），底部「继续」按钮关闭启动页并跳到设备列表。打开配置目录走注入的 `Environment.OpenDir func(string) error`（`main.go` 里接 `platform.RevealDir`），**不要在 `internal/app` 里 `exec.Command`**。

- [ ] **步骤 5：`platform.RevealDir`**

三平台各一份：darwin `open <dir>`、windows `explorer <dir>`、linux `xdg-open <dir>`；失败返回 `core.Errorf("无法打开目录：…")` 级别的错误，界面用错误提示条显示。

- [ ] **步骤 6：视图测试 `TestStartupPageListsProblems`**

`ui.NewTester` 断言：三条问题的标题都出现、路径出现、按钮文案是「用默认设置继续」「打开配置目录」「退出」。再加一条截图用例进 `shotCases()`。

- [ ] **步骤 7：`gofmt -w`、`go vet ./...`、`go test ./...`，提交 `feat: show every startup problem on its own page`**

### 验收

- 手写半截 `configuration.json` + 删掉 `history.db` 的权限，启动后**同时**看到两条问题。
- 点「打开配置目录」能打开真实目录（三平台各一次）。
- CHANGELOG 记「新增：启动问题页，一次列出全部问题」并删掉「配置读取失败只有一条提示」的已知限制。

---

## P4 `adb kill-server` 的聚合提示

### 现状问题

`detectInterference` 是**每设备**跑的：`adb kill-server` 之后，每台设备各自的 ticker 都发现"自己的 serial 从 `adb devices` 里消失了"，于是各记一条 `serialDetached`，弹窗一次一条（`AttentionDialog` 只取 `Attentions()[0]`），用户以为是自己操作错了。

### 设计（按现有代码可实现）

1. **纯函数聚合**（`internal/core/attention.go`）：

```go
// Detachment is one device losing its ADB entry.
type Detachment struct {
	DeviceID uuid.UUID
	Serial   string
	At       time.Time
}

// AggregateDetachments merges detachments that happened close together: an
// "adb kill-server" drops every device at once, a user's own disconnect
// drops one.
func AggregateDetachments(events []Detachment, window time.Duration) []DetachmentGroup
```

2. **管理器里的升级，不是延迟裁决**：`detectInterference` 判定 `serialDetached` 后**立刻**建一条 `Attention` 并把该设备置为 `StateNeedsAction`（这是阻止 `retryOffline` 替用户连回去的唯一时机，红线）。窗口（3 秒）内第二台命中时，**不新建条目**，而是把已有那条升级：`DeviceIDs` 追加、`Kind` 换成 `AttentionServerRestart`、`Detail` 重写；同一次升级里用 `detachProbe` 标记 + 锁做**一次** `adb devices -l` 探测确认（确认后再定文案；**不引入 `singleflight`**，那是新依赖）。
3. **一条 attention 挂多台设备**：`m.attention` 仍以"首要设备 id"为键，另加 `m.attentionGroup map[uuid.UUID]uuid.UUID`（每台受影响设备 → 首要 id）。`maybeDetectInterference` 的 `_, pending := m.attention[id]` 改成查 `attentionGroup`；`AckAttention(任一受影响 id)` 解析到首要条目后整条删除，并清掉整组。`Attentions()` 仍是一条，不会因拷贝而重复。
4. **`Attention` 扩展**：加 `DeviceIDs []uuid.UUID`（`DeviceID` 保留为首台，避免一次性改掉所有视图）。`Label()` 加 `case AttentionServerRestart: return "ADB 服务被重启，N 台设备受影响"`（N 由视图按 `len(DeviceIDs)` 填）。
5. **动作**：新增 `Manager.ReconnectAll(ctx, ids []uuid.UUID)`，逐个 `Connect`（复用现有 `Connect`，不新建连接路径），单台失败只进日志不中断其余。弹窗在多台时把「改用其他本地端口」换成「全部重新连接」，三个动作对 `DeviceIDs` 里每台都执行一次（不是只对首要设备）。
6. **`listenerLost` 删除**：上一版设想的这个种类不存在也不需要，`AttentionPortDrift` 已经覆盖"本机端口变了"。

### 测试

- `TestAggregateKeepsOneDeviceAsSerialDetached`（窗口内 1 台 → 原行为）
- `TestAggregateMergesDetachmentsWithinWindow`（3 台 → 1 组，且 `Attentions()` 只返回 1 条）
- `TestAggregateIgnoresDetachmentsBeyondWindow`（跨窗口不合并）
- `TestAggregateMarksEveryAffectedDeviceNeedsAction`（用 `fakeRunner` 让 `adb devices` 返回空表；断言三台都在同一次 `notify()` 之前已是 `needsAction`，且 `fakeRunner` 没有收到 `adb connect`）
- `TestReconnectAllSkipsHealthyDevices`
- `TestAckAttentionClearsEveryDeviceInTheGroup`
- `TestAttentionAggregatedDialogOffersReconnectAll`（视图，`ui.NewTester`）

### 验收

三台设备在线时 `adb kill-server`：只弹**一个**对话框，文案含"3 台设备"，点「全部重新连接」后三台各自进入连接流程，日志里三条各自的结果。

### 风险

- 聚合窗口是启发式：3 秒内用户手动断开两台也会被当成服务重启。文案用"ADB 服务被重启，N 台设备受影响"而不是断言原因，且单台仍是原来的 `serialDetached`。
- `Attention.DeviceIDs` 会让 `Attentions()` 的消费者（弹窗、托盘计数）都要按台数算，别只数 `len(Attentions())`（P5 依赖这一条）。

---

## P7 应用信息缓存管理页（已拆出）

本条单独成计划：`docs/superpowers/plans/2026-11-19-appmeta-cache-page.md`。

拆分原因：用户要求它的入口放在**侧栏设备列表之下**，与 DERP 地图管理页共用同一套路由与入口骨架，两个页面要一起设计才不会出现两种“管理页”；而且它的验收与 `2026-11-16-remaining-work.md` 的任何一条都不相交。

本计划只留顺序约束：**这两个管理页先做，做完再回到本文件**。

---

## P3 统一日志（已拆出）

本条单独成计划：`docs/superpowers/plans/2026-11-19-logs-page.md`。

拆分原因：用户要求设备页的「日志」标签与独立日志页**复用同一份实现**（标签只查自己那台设备的 `deviceId`），并把应用自己的错误/警告也放进去、支持时间段与模糊搜索、日志落 SQLite。这已经不是"给日志标签接个数据库"的小改，而是「新增 `logs` 表 + 一次 `user_version` 迁移 + 一份共享的日志界面 + 设备标签改调它」，验收点与本文件其他条目完全不同；它还依赖两个管理页先落地的入口骨架，因此排在它们之后。

本计划的处理方式与 P7 相同：**两个管理页 → 本文件 P1/P6/P2/P4/P5 → 日志页**（用户指定的大顺序不变，日志页在 P3 的位置）。P3 原本要解决的问题——日志只读内存、每台 200 行、重启即丢、`channel_logs` 没有读路径——全部写进了那份计划。

---

## P5 托盘「待处理」提示

### 设计

- 纯函数（`internal/app/tray.go`）：

```go
// trayBadge turns the pending attentions into what the tray shows.
func trayBadge(attentions []core.Attention) (title, tooltip, menuLabel string)
```

`title` 是 macOS 菜单栏文字（数量，用 `Tray.SetTitle`）；`tooltip` 是 `Tailcat Manager · N 项待处理`（全平台）；`menuLabel` 是菜单首项 `⚠ N 项待处理`（Windows/Linux 没有角标，macOS 也加，点击唤回窗口）。**N 按设备数算**：一条聚合 attention 可能挂多台（P4），`len(attentions)` 会少数。

- 无待处理时三项都为空串；`keepTrayFresh` 在重建菜单时顺便 `SetTitle` / `SetToolTip`，并且**只在文本变化时**调用（避免每帧闪一下）。macOS 的 `SetTitle` 数字等宽，两位以上不必补空格（mygo 的说明只要求变化时保持长度稳定，这里数量 0–99 不会抖动到需要填充）。

### 测试

- `TestTrayBadgeCountsPendingDevices`
- `TestTrayBadgeCountsAggregatedDevices`（一条 attention 带 `DeviceIDs` 三台 → 3）
- `TestTrayBadgeClearsWhenNothingPending`
- `TestTrayBadgeMenuLabelIsClickableFirst`（断言菜单首项的 `Click` 非 nil 且在 `mygo.Separator()` 之前）

托盘本身无法在 `ui.Tester` 里测，所有判断都放进 `trayBadge`，`TrayMenu` 只负责把它填进去。

### 验收

两台设备进入「等待处理」后：macOS 菜单栏出现数字，三个平台的 ToolTip 与菜单首项都说"2 项待处理"；点首项唤回窗口并弹对话框；处理完后数字消失。

---

## 提交与文档约定

- 每个 P 一个或多个提交，**同提交更新 `docs/CHANGELOG.md`**；用户可见的文案改动必须同步改测试。
- P1 完成后更新 `docs/build-notes.md`（打包、三平台体积、终端插件）、`README.md`（安装段落）。
- P2 的界面细节补进 `docs/ui-and-console.md`；P3 与两个管理页的界面细节补进各自计划要求的文档；P4 的聚合窗口写进 `docs/architecture.md` §15 决策记录（含代价）。
- P6 的每条结果进 `docs/verification.md`；顺手把 `docs/architecture.md` §16 与 `docs/CHANGELOG.md`「已知限制」里已解决的行删掉——已知缺口过时等于骗人。
- 每个任务的收尾命令固定：`gofmt -w` → `go vet ./...` → `go test ./...`（视图任务再加 `TAILCAT_SHOT_DIR` 截图人工看一眼）。

## 不在本轮范围

- 自动更新（`mygo.json` 的 `updates` 通道）——需要发布托管与签名链。
- 多语言 i18n（当前固定 zh-Hans）。
- 文件页的写操作：删除、重命名、新建目录、文件内容预览、多选批传。
- 逐进程 GPU 采样（`GPUSample.PerProcessAvailable` 恒为 false）。
- CLI 模式、多窗口、Windows Job Object、云端同步。

## 决策与代价

| 决定 | 代价 |
|---|---|
| P1 用 `go tool mygo build`，CI 三平台各建各的 | Go 版本与 mygo 版本被 `go.mod` 一起钉住；`darwin/universal` 编两遍，CI 时长翻倍。换来的是本地与 CI 同一条命令、终端插件默认进包 |
| 保留 `go build ./...` 的测试 job，删掉 `cross` job | 少一份"纯交叉编译"的独立证据；但打包 job 本身就在交叉编译，两件事实在重复记账 |
| 启动诊断做成独立页面而不是提示条 | 多一个页面状态与一条截图用例；换来"四个问题一次说清"，而不是修一个重启一次 |
| `Diagnose` 的输入是 `main.go` 已经拿到的 `error`，不是让 core 重新读一遍配置 | `Diagnose` 不能独立探测磁盘（它只看调用方给了什么）；换来真正的纯函数，且不会二次产生副作用（例如把损坏的配置写回） |
| P4 用"3 秒窗口 + 一次全局 `adb devices` 探测"，不引入全局监视循环 | 启发式，手工同时断开两台会被当成服务重启；文案因此只说"N 台设备受影响"，不断言原因。换来不动每设备一条 ticker 的现有结构 |
| `Attention.DeviceIDs` 与 `DeviceID` 并存，并用 `attentionGroup` 记录组内成员 | 结构里两处表达同一件事，`AckAttention` / `pending` 判定都要先解析到首要 id，容易漏；换来 P4 可以增量改视图，且 `Attentions()` 不会因拷贝而重复 |
| `HistoryReader` 是独立可选接口，不进 `Recorder` | `main.go` 多一次类型断言（`recorder.(core.HistoryReader)`）；换来 nil `Recorder` 时应用照常运行，测试里的假 recorder 也不用实现查询 |
| 历史列表不返回 `output` | 详情要多一次查询，界面要处理"详情还在读"的状态；换来列表不会被 3 MB 输出拖慢 |
| P5 的计数按设备数而不是 attention 条数 | 需要 `len(DeviceIDs)` 的兜底（单台 attention 时 `DeviceIDs` 可能为空）；换来 P4 聚合后数量正确 |
| P3 拆成独立计划，但顺序留在本文件 | 三个页面的计划分在三份文件，改共用骨架时要一起看；换来每份计划只说自己那一页，审阅时不必在九条任务里翻 |
