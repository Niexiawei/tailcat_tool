# AGENTS.md

本文件面向在此仓库工作的 AI 编码代理与人类贡献者。**先读完再动手**，这里的约束多数是被测试钉住的，违反会红。

## 这是什么

Tailcat Manager 的 Go 版：一个跨平台桌面应用（macOS / Windows / Linux），管理多台 Android 设备的独立 ADB 会话。传输层是内嵌的 `github.com/tailscale/tailcat` 库——**没有外部 `tailcat` 可执行文件**，用户只需要 `adb`（必需）和 `scrcpy`（可选）。

Swift 原版在 `tailcat-tool-swift/`（独立 git 仓库，被本仓库 git-ignore），只作为**行为基线**参考，不要改它、不要往这里迁数据。

## 硬约束

1. **纯 Go，禁止 cgo。** 三平台都要能从任意机器交叉编译：
   ```sh
   CGO_ENABLED=0 GOOS=linux|darwin|windows GOARCH=amd64|arm64 go build ./...
   ```
   需要引入任何依赖前，先确认它在 `CGO_ENABLED=0` 下可构建（SQLite 用 `modernc.org/sqlite`，不是 `mattn/go-sqlite3`）。
2. **依赖方向单向**：`cmd → internal/app → internal/core → internal/platform`。
   - `internal/core` **不得** import `mygo`、`onevra`、`internal/app`。
   - `internal/platform` **不得** import `internal/core`（core 定义接口，platform 提供能力）。
   - `internal/app` **不得**直接 `exec.Command`。
3. **`tailcat` 只能出现在这四个文件里**：`internal/core/forwarder.go`、`pinger.go`、`identity.go`、`relay.go`。其余代码面对 core 里的窄接口。
4. **凭据绝不落配置文件。** `tc...` 地址只进系统凭据库；`configuration.json`、日志、任务输出、tailcat `Logf` 全部必须经过 `core.Redact` / `core.Redactor`。
5. **权限**：配置目录 0700、配置文件 0600、身份目录 0700、私钥 0600。写配置文件必须原子（临时文件 + rename）并拒绝符号链接。
6. **面向用户的文案是中文**，集中在 `internal/core/messages.go` 与各视图里，测试逐字断言。改文案要同步改测试。
7. **不要引入外部 `tailcat` 进程、不要解析它的 stderr、不要恢复 socat/`bridges/` 那套 CLI 模式**——那条链路已被移除，理由是它需要外部二进制且端口就绪存在 5 秒竞态。
8. **用户可见的改动必须写进 `docs/CHANGELOG.md`，且与代码在同一次提交里。** 规则见下文「变更记录」。

## 开发命令

```sh
go build ./cmd/tailcat-manager      # 当前平台
go run ./cmd/tailcat-manager --demo # 模拟设备，不读配置/凭据、不执行真实命令
gofmt -l .                          # 必须无输出
go vet ./...
go test ./...
go test -race ./internal/core ./internal/app
go test ./internal/compat/          # 第三方 API 契约
```

GUI 需要真实窗口；在无头环境中验证视图请用 `ui.NewTester`（见下），不要 `go run`。

## 目录与"新代码放哪"

| 要加的东西 | 放这里 |
|---|---|
| 业务规则、状态机、数据模型 | `internal/core/` |
| 新的外部能力（某 OS 特有的调用） | `internal/platform/<能力>_<os>.go` + core 里的接口 |
| 新的外部存储（库/文件） | 新包（如 `internal/history`、`internal/credstore`），实现 core 接口 |
| 界面、视图、表单 | `internal/app/` |
| 第三方 API 断言 | `internal/compat/compat_test.go` |

平台文件用构建标签：`//go:build darwin || linux`、`//go:build windows`。**不要**在 core 里用 `runtime.GOOS` 分支做能力开关（`isExecutable` 例外，它只是模式位差异）。

## TDD 是硬要求

没有先看到失败的测试，不要写生产代码。顺序固定：

1. 写测试（引用还不存在的 API，`go test` 报 undefined 即为 RED）。
2. **跑一次**，确认失败原因就是"功能缺失"。
3. 写最少实现。
4. 跑测试转绿。
5. `gofmt -w`、`go vet`、`go test ./...`，然后 commit。

被覆盖的测试名必须能说明行为（`TestDirectConnectUSBSerial`、`TestAttentionStopsRetry`），不要 `TestFoo`。

### 测试基础设施（用现成的，别再造）

- **假实现**：`internal/core/manager_test.go` 里的 `fakeRunner`、`fakeOpener`、`fakePingDialer`、`fakePingSession`；`internal/app/app_test.go` 里的 `stubRunner`。core 的假实现放在 `package core` 的 `_test.go` 里（否则会和 `internal/testutil` 循环导入）。
- **真实进程**：`internal/core/runner_test.go` 的 `TestMain` + `CORE_RUNNER_HELPER` 环境变量重新执行测试二进制。新增"真实管道/信号"用例就加一个 helper 模式，不要启外部程序。
- **视图**：`ui.NewTester(vm.SidebarView, 280, 720)`，用 `HasText` / `Click` / `Texts` 断言；不要依赖像素。
- **无网络**：探测测试注入 `ManagerOptions.Prober`；转发测试注入 `ChannelOpener`；任何测试都不许真连 DERP。

## 常见改动 cookbook

**给设备加一个字段**：`internal/core/session.go` 的 `Profile`（JSON tag 用 camelCase，可空加 `omitempty`）→ `SaveDeviceRequest`/`SaveTemporaryDeviceRequest` → `SaveDevice`/`saveDirectDevice` 赋值 → 编辑器 `EditorInput`/`EditorValues` 与 `Validate` → `internal/app/editor.go` 的字段与 `openEditor`。迁移兼容：未知字段被忽略，不需要写升级代码。

**加一个 Manager 方法**：所有公开方法都要 `m.mu.Lock()`；改完状态调 `m.notify()`；异步分支里写状态前先 `if !m.isCurrentLocked(id, generation) { return }`。

**加一种平台能力**：core 里定义接口或 `func` 类型 → 在 `internal/platform/` 为三个 OS 各写一份 → 在 `cmd/tailcat-manager/main.go` 或 `internal/app/app.go` 注入 → 用假实现写测试。

**加一条历史记录**：`internal/core/history.go` 加 record 类型与 `Recorder` 方法 → `internal/history/sqlite.go` 加表/插入/查询 → core 里在状态变化处调用（注意此时**不要**持有 `m.mu`）→ 加一个"重开库能读回"的测试。

**加一个对话框**：在 `ViewModel` 加 `XxxOpen bool` 与草稿字段，`internal/app/xxx.go` 里用 `components.Dialog(c, &open, ...)`，在 `ContentView` 末尾调用。校验逻辑抽成**纯函数**（如 `EditorInput.Validate`）并单测，不要只测视图。

## 绝不能碰的红线

- **`needsAction` 状态下不要自动重试或自动 `adb connect`。** 用户刚在终端断开，我们就不能偷偷连回去（`TestAttentionStopsRetry` 钉住）。
- **任务启动后不要改它的目标**：serial / adb 路径 / generation 必须在启动瞬间快照，执行前再校验一次。
- **不要用别人的本地端口**：`portChanged` 里的 P2 只作线索展示，不接管。
- **不要删/覆盖已存在的身份文件**（`TestIdentityNeverReplacesCorruptFile`）。
- **不要在持有 `m.mu` 时调用会再上锁的方法**（`detectInterference`、`recordTask`、`validateTarget` 之类），历史上这里死锁过两次。
- **不要 `panic` 来表达用户错误**：用 `core.Error` / `core.Errorf`，它们是面向用户的文案。

## 提交

- 分支：功能开发在 `feat/*`，不要直接往 `main` 提。
- 提交信息用 `feat:` / `fix:` / `refactor:` / `docs:` / `chore:` / `test:` 前缀，正文说清"为什么"。
- 一个提交一个可独立审查的变更；`gofmt` 与测试必须先绿。
- 改变用户行为的提交，同一提交里必须包含 `docs/CHANGELOG.md` 的条目。

## 变更记录（CHANGELOG）

`docs/CHANGELOG.md` 是**面向用户的变更记录**，不是提交日志的复制品。

- 判断标准：用户能感知到吗？界面、行为、文案、配置格式、文件位置、体积、依赖要求。纯重构/纯测试/纯内部整理不记。
- 写进最上面的 `## [未发布]`，归入 `新增` / `变更` / `修复` / `移除` / `安全` / `依赖`；空类别不要留标题。
- 一条一行，写**结果**而不是函数名：
  - 好：`设备编辑器新增“本地 ADB 直连”，可填 IP:端口 或 USB 序列号`
  - 差：`add DirectADB field to Profile`
- 破坏性变更（配置格式、文件位置、删除功能）必须出现在 `变更`/`移除` 里并标注 **（破坏性）**。
- "为什么这样取舍"写进 `docs/architecture.md` 的「设计决策记录」，不要塞进 CHANGELOG。
- 发布时才把 `未发布` 改成 `## [x.y.z] - YYYY-MM-DD`；已发布的段落不再修改。

## 文档

- `docs/architecture.md`：架构、状态机、两种连接方式、干预检测、安全设计、**决策记录（含每条的代价）**、已知缺口。改设计前先读第 15、16 节。
- `docs/build-notes.md`：依赖版本、体积与构建耗时实测，升级依赖后要更新。
- `docs/CHANGELOG.md`：面向用户的变更记录，**每次改行为都要加一条**（见「变更记录」）。
- `docs/superpowers/plans/2026-11-15-tailcat-manager-go-port.md`：原始实施计划（历史文档，任务编号已不代表当前结构）。
- `.superpowers/sdd/<plan>/progress.md`：执行账本（git-ignore），记录每一处偏离计划的裁决。

## 当前已知缺口

动手前先确认你要改的是不是这块：

1. **真机联调未做**——ADB 实链路、scrcpy、授权流程、干预弹窗都只有自动化测试验过。
2. 发布打包（dmg/msi/deb）与签名未做。
3. `adbServerRestarted` / `listenerLost` 两种干预未实现。
4. 配置读取失败只有一条提示，没有独立错误页。
5. 历史只有"重启后载入任务面板"，没有筛选界面。
