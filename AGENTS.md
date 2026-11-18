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
9. **GUI 优先用 onevra 的 `components`，不要自己拼。** 规则见下文「GUI 组件优先级」。

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
- **页面截图（人工核对，不是断言）**：`TAILCAT_SHOT_DIR=/tmp/shots go test ./internal/app -run TestScreenshots` 把每个页面状态渲染成 PNG（`internal/app/screenshots_test.go`，用例表 `shotCases()`）。不设环境变量就 SKIP，CI 不写文件。改完界面自己看一眼：文字都在不等于布局是对的。加了新页面/对话框状态就加一条用例。终端页不在其中（要 libghostty-vt）。
- **无网络**：探测测试注入 `ManagerOptions.Prober`；转发测试注入 `ChannelOpener`；任何测试都不许真连 DERP。

## GUI 组件优先级

**任何界面元素，先查 onevra 有没有现成组件；有就必须用，不许用 `ui.Row` / `ui.Column` / `ui.ButtonBase` 自己拼一个等价物。**

升级路径固定：`onevra components` → `onevra shells/theme/icons` → mygo `ui` 基元。只有第三层才是合法退路，且必须在提交信息里写清"为什么没有现成组件、缺什么能力"。

| 需要 | 用 |
|---|---|
| 单选（互斥选项） | `components.Radio` |
| 分组折叠 / 手风琴 | `components.Accordion`（`group.Item`） |
| 单块折叠 | `components.Collapsible` |
| 开关 | `components.Switch` |
| 复选 | `components.Checkbox` |
| 分段切换 | `components.Tabs` + `TabsSegmented` |
| 按钮 | `components.Button`（`ButtonPrimary`/`ButtonSecondary`/`ButtonOutline`） |
| 图标按钮 | `components.IconButton` |
| 表格 | `ui.Table`，经 `internal/app/table.go` 的 `tableCard` |
| 下拉选择 | `components.Select` / `NativeSelect` / `Picker` |
| 数值 | `components.NumberInput` / `Stepper` / `Slider` |
| 文本输入 | `components.TextBox` |
| 提示 | `components.Badge` / `Toast` / `Tooltip` |
| 加载 | `components.Spinner` / `Skeleton` |
| 进度 | `components.Progress` |
| 弹窗 | `components.Dialog`（内联渲染，不开独立窗口） |
| 图标 | `onevra/icons/lucide` |
| 滚动区 | `components.ScrollView`；整块布局滚动用 `ui.Scroll` |

写新视图前的三步：

1. `ls $(go env GOMODCACHE)/github.com/oneo-me/onevra/source@*/components` 看清单，`go doc` 看签名与选项；
2. 在 `examples/gallery` 里搜同名 `control_*.go`，照官方用法写；
3. 用到的新组件同时加进 `internal/compat/compat_test.go` 的编译期断言，否则上游改签名 CI 不会红。

**反例（已发生过，不要重复）**：用两个 ghost 按钮加 `●`/`○` 冒充 radio；用手写 `sectionHeader` 文字冒充分组；把 footer 塞进列表末尾而不是用 `shell.LeftToolbar`；再用 onevra 的 `components.Table` / `TableVirtualized`——它自己不滚动、要套 `ScrollView`，而我们套的是竖滚容器，列一调宽，调整抓手就裁在可视区外抓不到（`TestNoOnevraTableComponentsAnywhere`）。

**不要用 `ButtonGhost`。** 无底色按钮在页面里读成正文，用户找不到入口。层级只有三档：`ButtonPrimary`（这一屏的主操作）、`ButtonSecondary`（次要动作）、`ButtonOutline`（安静的动作与对话框的「取消 / 关闭」）。文字里的链接用 `components.Button(..., ButtonLink)`。`TestNoGhostButtonsAnywhere`（`internal/app/style_test.go`）会扫源码，出现 `components.ButtonGhost` 直接红。

**配色只从 `internal/app/theme.go` 取。** 强调色 `accentHex`、警告色 `warningHex`（`#e37318`，同时是 `theme.Warning`，警告徽标与删除类按钮共用）都在那里定义，视图里不要再写字面量十六进制。

**布局约定**：`shells/sidebar` 的 `Left` / `Content` 必须用 `sidebar.HeaderHeight(c)` 作上内边距（否则被浮动标题栏压住）；固定底栏用 `LeftToolbar`，不要让列表和 footer 抢高度；行式元素用 `FillWidth()`，**`Fill()` 会同时占满高度**，行高失控通常就是它。

### 表格（全部统一为 `ui.Table` 之后）

表格只有一条路：`tableCard`。以下每条都是踩过的坑，别重犯。

1. **只调 `internal/app/table.go` 的 `tableCard`**：页面里不许直接写 `ui.Table`，更不许再用 onevra 的 `components.Table` / `TableVirtualized`。两条守卫分别在 `internal/app/table_test.go`（`TestTablesAreBuiltOnlyByTableCard`：`ui.Table(` 只允许出现在 `table.go`）和 `internal/app/style_test.go`（`TestNoOnevraTableComponentsAnywhere`：onevra 表格组件全仓清零）。
2. **表格自己滚，外面不要再套滚动容器**。`components.Table` 自己不滚、要求调用方套 `ScrollView`，而我们套的是竖滚容器——列一调宽，最右列的调整抓手就被裁在可视区外且没有横向滚动能过去。`ui.Table` 的表头与行在同一个横向滚动区里，所以：
   - 表格是本屏主体（资源、缓存、日志、文件）→ `tableOptions{Grow: true}`，占满剩余高度；
   - 页面本身要滚（DERP 详情、连通测试对话框）→ 给 `tableOptions{Rows: n}` 定高，最多 `maxTableRows` 行，超出在表内滚；
   - 外面那个框负责滚（DERP 页的预设列表：上半部固定、下半部一个框）→ `tableOptions{Rows: n, Fit: true}`，表格按行数撑满、自己不滚，由框统一滚（`TestDerpPresetTableShowsEverySavedRow`、`TestDerpListPinsTheCreateActionAndDefaultNode`）。`Fit` 会把每行钉成主题的最小行高（`ListState.RowHeight`）——只有行高统一，表高才能等于内容高；行里有按钮的行本来就更高，按估算给高度就会每行差一两像素，最后几行被藏在表格自己的滚动条后面。**表高还要算上表头下那 1 DIP 分隔线**，少算就是 1 像素的内部滚动条，正好压在最后一列上。`Fit` 只给这类"行数有上限"的列表：它会一次建出所有行，别给文件列表那种没上限的表。
3. **行激活用 `RowClicked()`，不要用 `Changed()`**。`Changed()` 对上下键移动选中也报，用它会在用户只是按键浏览时进目录、弹任务输出（两次都是真 bug）。
4. **选中行（`ListState.Selected`）必须指向状态字段**，不能指向构建函数里的局部变量：列表在帧与帧之间写它，局部变量活不到下一帧，箭头键的写入会丢。文件页为此存 `fileState.chosen` + `chosenPath`（`TestFileTableKeyboardMovesTheChoice` 钉住）。
5. **`ListState.Key` 必须是稳定身份**（日志用 `logRowKey`：两行日志可以时间、来源、文本全同）。
6. **表格是虚拟化的**：屏幕外的行根本不存在，测试必须滚动之后再断言（`TestCacheTableKeepsTheClearActionInView`、`TestDerpRegionTableScrollsItsOwnRows`、`TestLogTimelineIsItsOwnScrollArea`）。任何「第 40 行在不在」的旧断言都要改写，不是删掉。
7. **排序由页面算**：把 `&state.Sort` 交给表，建表前用 `Sort` 里的列自己排好行序（资源页 `sortApps` / `sortProcessRows`）。列上要 `Sortable: true`；默认值写成一个 `ui.SortOrder{Column: "app"}`；降序只翻主键，并列键永远升序（同一应用的行不许散开）；`不可用` 的 GPU 双向都排在数字后面。
8. **每个表格都有边框**（`tableCard` 默认 1 DIP 圆角框）。例外只给「表格已经在别人画的框里」：文件页右栏（整页浏览区是框）与 DERP 页的预设列表（下半部那个滚动框），那里传 `Unframed: true`，由 `TestOnlyAFramedPaneLeavesTheTableUnframed` 钉住。

## 常见改动 cookbook

**给设备加一个字段**：`internal/core/session.go` 的 `Profile`（JSON tag 用 camelCase，可空加 `omitempty`）→ `SaveDeviceRequest`/`SaveTemporaryDeviceRequest` → `SaveDevice`/`saveDirectDevice` 赋值 → 编辑器 `EditorInput`/`EditorValues` 与 `Validate` → `internal/app/editor.go` 的字段与 `openEditor`。迁移兼容：未知字段被忽略，不需要写升级代码。

**加一个 Manager 方法**：所有公开方法都要 `m.mu.Lock()`；改完状态调 `m.notify()`；异步分支里写状态前先 `if !m.isCurrentLocked(id, generation) { return }`。

**加一种平台能力**：core 里定义接口或 `func` 类型 → 在 `internal/platform/` 为三个 OS 各写一份 → 在 `cmd/tailcat-manager/main.go` 或 `internal/app/app.go` 注入 → 用假实现写测试。

**加一条历史记录**：`internal/core/history.go` 加 record 类型与 `Recorder` 方法 → `internal/history/sqlite.go` 加表/插入/查询 → core 里在状态变化处调用（注意此时**不要**持有 `m.mu`）→ 加一个"重开库能读回"的测试。

**加一个对话框**：在 `ViewModel` 加 `XxxOpen bool` 与草稿字段，`internal/app/xxx.go` 里用 `components.Dialog(c, &open, ...)`，在 `ContentView` 末尾调用。校验逻辑抽成**纯函数**（如 `EditorInput.Validate`）并单测，不要只测视图。

**加一个表格 / 一列排序**：`ui.ListState` 放进页面状态（`internal/app/state.go` 或该页自己的 `xxxState`）→ 页面用 `tableCard(c, &state.table, columns, len(rows), tableOptions{Label: "……表", Grow: true}, cell)` → 行级动作用单元格上的 `.RowClicked()` → 需要排序时列加 `Sortable: true`、状态里放 `sort ui.SortOrder` 并把 `&sort` 交给 `table.Sort`，建表前用纯函数按 `sort` 排好行 → 补一个「点表头换顺序」和一个「默认顺序」的测试 → 表格页在 `screenshots_test.go` 里要有用例，改完自己看一眼。

## 绝不能碰的红线

- **`needsAction` 状态下不要自动重试或自动 `adb connect`。** 用户刚在终端断开，我们就不能偷偷连回去（`TestAttentionStopsRetry` 钉住）。
- **任务启动后不要改它的目标**：serial / adb 路径 / generation 必须在启动瞬间快照，执行前再校验一次。
- **不要用别人的本地端口**：`portChanged` 里的 P2 只作线索展示，不接管。
- **不要删/覆盖已存在的身份文件**（`TestIdentityNeverReplacesCorruptFile`）。
- **不要在持有 `m.mu` 时调用会再上锁的方法**（`detectInterference`、`recordTask`、`validateTarget` 之类），历史上这里死锁过两次。
- **不要自己实现 onevra 已有的组件**（radio、accordion、switch、tabs、dialog…）。见「GUI 组件优先级」。表格是例外：一律 `ui.Table`，经 `tableCard`（见「表格」一节）。
- **不要再用 onevra 的表格组件，也不要把表格塞进 `ScrollView` / `ui.Scroll`**：那正是列宽抓手滚出可视区的原因（`TestNoOnevraTableComponentsAnywhere`）。
- **表格的行激活不要用 `Changed()`**：上下键移动也会报它，用户按键浏览就会进目录 / 弹输出。用 `RowClicked()`。
- **行式元素不要用 `Fill()`**：它同时占满宽高，会让列表行被拉伸。用 `FillWidth()`。
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
- `docs/ui-and-console.md`：界面布局、路由、内嵌终端、提示与线程模型；**动界面前先读第 2、8、10 节**（槽位约定、锁与线程、陷阱清单）。
- `docs/CHANGELOG.md`：面向用户的变更记录，**每次改行为都要加一条**（见「变更记录」）。
- `docs/superpowers/plans/2026-11-15-tailcat-manager-go-port.md`：原始实施计划（历史文档，任务编号已不代表当前结构）。
- `docs/superpowers/plans/2026-11-15-gui-layout-and-direct-device-fixes.md`：本轮 GUI 布局与直连设备显示的修复方案（T0–T6）。
- `.superpowers/sdd/<plan>/progress.md`：执行账本（git-ignore），记录每一处偏离计划的裁决。

## 当前已知缺口

动手前先确认你要改的是不是这块：

1. **真机联调未做**——ADB 实链路、scrcpy、授权流程、干预弹窗都只有自动化测试验过。
2. 发布打包（dmg/msi/deb）与签名未做。
3. `adbServerRestarted` / `listenerLost` 两种干预未实现。
4. 配置读取失败只有一条提示，没有独立错误页。
5. 历史只有"重启后载入任务面板"，没有筛选界面。
