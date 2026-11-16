# 界面与内嵌终端（技术文档）

> 记录界面布局、页面路由、标题栏导航、内嵌终端与提示条的设计与实现（提交 `9aaa288` 起，最近一轮见 `docs/CHANGELOG.md`）。
> 架构总体见 [`architecture.md`](architecture.md)；构建与依赖见 [`build-notes.md`](build-notes.md)；行为变更见 [`CHANGELOG.md`](CHANGELOG.md)。

## 目录

1. [设计目标](#1-设计目标)
2. [布局骨架](#2-布局骨架)
3. [页面路由与导航](#3-页面路由与导航)
4. [侧栏](#4-侧栏)
5. [内嵌终端](#5-内嵌终端)
6. [提示与错误](#6-提示与错误)
7. [设备信息的生命周期](#7-设备信息的生命周期)
8. [线程与锁模型](#8-线程与锁模型)
9. [事故复盘](#9-事故复盘)
10. [陷阱清单](#10-陷阱清单)
11. [测试手法](#11-测试手法)
12. [文件索引](#12-文件索引)

---

## 1. 设计目标

1. **一屏一职责**：侧栏只负责"有哪些设备"，标题栏只负责"我在哪一层"，内容区只负责"当前这一页"。三者互不争空间。
2. **界面是状态的函数**：视图不持有临时布局状态；展开、页签、输入草稿都在 `ViewModel`，重绘由管理器通知驱动。
3. **绝不让界面等 I/O**：任何写库、跑命令、探测，都不允许出现在渲染线程上，也不允许在持有管理器锁时发生。
4. **组件优先**：能用的交互一律用 onevra 的 `components`，不自造（见 `AGENTS.md`「GUI 组件优先级」）。

## 2. 布局骨架

### 2.1 窗口三层槽位

`shells/sidebar` 提供三个独立槽位，不是一块自由画布：

```
┌─────────────────────────────────────────────────────────────┐
│  [浮动标题栏：折叠按钮]                        窗口按钮      │
├──────────────┬──────────────────────────────────────────────┤
│ Left         │ Content                                      │
│  Padding =   │  Padding(top) = HeaderHeight                 │
│  HeaderHeight│  ├─ Column(Grow 1)  ← 路由页                 │
│  ├─ Scroll   │  └─ （页面自己滚动，见 §2.2）                 │
│  │  Accordion│                                              │
├──────────────┤                                              │
│ LeftToolbar  │                                              │
│ 通道数/工具  │                                              │
└──────────────┴──────────────────────────────────────────────┘
```

对应代码（`internal/app/shell.go`）：

```go
sidebar.NewShell(sidebar.ShellOptions{
	Title:        AppName,
	Theme:        theme.Onevra,
	StateKey:     "tailcat-manager.main",
	Left:         vm.SidebarView,     // 设备列表（可滚动）
	LeftToolbar:  vm.SidebarFooter,   // 固定底栏
	ContentTitle: vm.ContentTitle,    // 标题栏内的面包屑
	Content:      vm.ContentView,     // 路由页
})
```

三条硬性约定，违反即错位：

| 约定 | 原因 |
|---|---|
| `Left` 与 `Content` 必须用 `sidebar.HeaderHeight(c)` 作为上内边距 | 标题栏是浮在上面的（高 `max(48, 系统标题栏高度, 主题行高)`），不留出来就会被它压住；标题栏还负责拖动窗口，画在它下面的按钮连点击都收不到 |
| 导航（面包屑）画在 `ContentTitle`，不要画在 `Content` 里 | `ContentTitle` 是 shell 的标题栏槽位，点击不会被拖拽区吞掉；`Content` 从标题栏下方开始 |
| 固定底栏用 `LeftToolbar`，不要塞进 `Left` 的列尾 | 塞进列尾会和列表抢高度，出现"底栏浮在列表中间" |
| 列内**只有一个**元素可以用 `Fill()` | `Fill()` 是宽 **和** 高都 100%，两个相邻的 `Fill()` 会叠在同一位置 |

### 2.2 内容区是一段

`ContentView`（`internal/app/detail.go`）只放一个路由页，并且自己让出标题栏高度：

```go
ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), 0, 0, 0).Children(func() {
	vm.routedPage(c)   // 页面占满，含设备页的「概览 / 终端 / 日志」
})
```

**没有固定的任务面板。** 设备任务并进该设备的「日志」标签页（按时间与通道日志合并），运行中的任务在日志页顶部一键取消。历史上那个 `Shrink(0)` 贴底面板会压住内联对话框，已删除。

**行式元素一律 `FillWidth()`。** `Fill()` 让行去抢高度，是最常见的布局事故（见 §9.2）。

## 3. 页面路由与导航

### 3.1 路由表

| 路由 | 页面 | 说明 |
|---|---|---|
| `/devices` | 设备列表页 | 卡片式列表（状态点 / 名称 / 连接方式与地址 / 状态），空态给出"添加设备"；页面标题在窗口标题栏里，正文不再重复 |
| `/devices/{id}` | 设备页 | 面包屑 + 标题行（编辑 / 连接 / 断开）+ 页签（概览 / 终端 / 日志） |
| 其他 | 列表页 | 兜底 |
| `/devices/{无效 id}` | "设备不存在"页 | 附带"返回我的设备" |

用 `ui.NewRouter(DeviceIndexRoute)` 建在 `ViewModel` 上，一次创建、随窗口存活。

### 3.2 导航的两条入口

- **侧栏点击**：`deviceRow` → `vm.OpenDevice(id)`；已在该页时不重复 `Push`。
- **标题栏面包屑**：`vm.ContentTitle` 交给 `ShellOptions.ContentTitle`，用 `components.Breadcrumbs(c, []string{"我的设备", 设备名}, &vm.CrumbChoice)`；`Changed()` 且 index 为 0 时 `vm.OpenDevices()`。它读路由而不是 `Selection`，所以按 ⌘[ 后退时标题栏自动跟着变。`deviceIDFromPath(path)` 是这一步唯一的路径解析（纯函数，单独可测）。

`Router` 自带 ⌘[ / ⌘]（Windows/Linux 为 Alt+← / →）与鼠标侧键，页面在历史中保留滚动位置与焦点。

### 3.3 路由是选中状态的来源

```go
vm.Router.View(c, func(r *ui.Route) {
	if r.Match("/devices/{id}") {
		profile, ok := vm.profileByID(r.Param("id"))
		...
		vm.Selection = profile.ID   // ← 页面决定选中，不是选中决定页面
		r.Title(profile.Name)
		vm.devicePage(c, profile)
		return
	}
	vm.Selection = uuid.Nil         // 列表页没有选中设备
	r.Title("我的设备")
	vm.toolbar(c)                   // 窗口级操作只在列表页
	vm.deviceIndexPage(c)
})
```

这样"后退回上一台设备"时侧栏高亮、详情内容天然一致，不需要额外同步逻辑。**不要**在别处再写一遍 `Selection = ...`，否则两个真相会打架。

### 3.4 窗口级操作只属于列表页

添加设备 / 临时连接 / 测试全部设备只对"设备集合"有意义，因此 `vm.toolbar(c)` 只在列表页分支里调用，顺序固定：**主操作「添加设备」在最前**，其余按常用度排在后面。设备页只保留设备自身的动作。「工具设置」不属于设备集合，它配置的是侧栏底栏显示的那两个工具，所以放在 `LeftToolbar`（见 §4）。

### 3.5 动作一律「图标 + 文字」

窗口级动作统一走 `actionButton(c, icon, label, variant)`：`components.Button` 加 `Children`，里面是 `components.Icon(c, lucide.X)` 和 `components.Text`，按钮的 `Label` 仍是文案本身——所以无障碍名与 `Tester.Click("添加设备")` 都不受影响。用的都是 `lucide` 图标：`Plus`（添加设备）、`Zap`（临时连接）、`Radar`（测试全部设备）、`Settings`（工具设置）。

**不要**给动作只放文字：一行同尺寸的文字按钮读起来像正文（见 §10 的幽灵按钮一行）。

## 4. 侧栏

```go
ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), Space(2), 0, Space(2)).Children(func() {
	ui.Scroll(c).FillWidth().Grow(1).Children(func() { vm.deviceGroups(c) })
})
```

- **分组**：`components.Accordion` 两个 `group.Item`，标题带数量（`我的设备 (3)` / `临时设备 (空)`），展开状态在 `ViewModel.SavedOpen / TemporaryOpen`，默认都展开。空组给一句说明而不是空白。
- **设备行**：`ui.ButtonBase(c).FillWidth().Shrink(0).Justify(ui.Start).AlignItems(ui.Center).Radius(t.Radius)`，选中时 `Background(t.Selection)`。`ButtonBase` 内部是 `coreRow(c).Center()`，**必须显式 `Justify(Start)`**，否则内容居中。
- **底栏**：两个工具的健康状态（缺失时给「安装」按钮），以及带图标的「工具设置」动作（它配置的就是这两行显示的工具）。不放通道计数（它与设备行上的状态点重复，且无法操作），也不放提示文本。

## 5. 内嵌终端

终端来自 `github.com/egoist/mygo/plugins/terminal`（Ghostty 的 libghostty-vt，运行期加载，**无 cgo**）。发布需 `mygo build` 才会把库打进包，`go build` 的产物首次打开终端页时会下载一次。

### 5.1 外观

终端是**自己的一块深色面板**，与窗口外观解耦：`consoleTheme()` 取 `terminal.DarkTheme()` 再把背景钉成 `#000000`、前景 `#f2f2f2`，三处 `terminal.Options`（设备 Shell / ADB 命令 / 未连接占位）都带上它。外面的框是 `Padding(1) + Radius + Border(1, theme.Border) + Clip()`：1 DIP 内缩是为了让终端自己画的背景不吃掉边框，圆角靠 `Clip()` 的圆角裁剪生效。

**不要**让终端跟随窗口的浅色主题：白底黑字的 shell 看起来像一个表单，用户会以为那是输入框。

### 5.2 两种模式

| 模式 | 实现 | 用途 |
|---|---|---|
| 设备 Shell（`ConsoleShell`） | `terminal.Options{Command: []string{adb, "-s", serial, "shell"}}` | 真正的交互式 shell：提示符、配色、全屏程序都正常 |
| ADB 命令（`ConsoleADB`） | `terminal.Options{Conn: lineConsole}` | 按行执行任意 adb 子命令（`pull` / `push` / `install` / `shell …`），UI 没做按钮的命令也能用 |

ADB 命令模式每一行都走 `Manager.RunADB(ctx, id, args, timeout)`：

```go
func (m *Manager) RunADB(ctx context.Context, id uuid.UUID, args []string, timeout time.Duration) (string, error) {
	taskID, err := m.BeginADBTask(ctx, id, "adb "+strings.Join(args, " "), args, timeout)
	if err != nil { return "", err }
	m.WaitTask(taskID)
	// 从任务里取输出 → 失败时返回 Error(output)
}
```

复用任务机制的好处是**免费**获得：目标双校验（启动前 + 执行前）、超时、输出脱敏、尾部 32 KiB 上限、日志页可见、进历史库。

### 5.3 未连接时：`lineConsole`

未连接（或没有 adb、或演示模式）时挂一个内存 `io.ReadWriteCloser`：

- 启动即在终端里打印「设备未连接」+ 一句说明；
- **本地回显**你敲的字符（对端不是真 shell，没人替你回显）；
- 回车时**不执行**，只打印「设备未连接」并弹错误提示；
- 转义序列（方向键、功能键）用状态机吞掉，不会把 `[A` 当文字显示；
- 支持退格与 Ctrl-C。

### 5.4 控制台的生命周期

```go
key := consoleKey(session, mode)   // "idle" | "adb:<serial>:shell" | "adb:<serial>:cmd"
if host != nil && host.key != key { drop }
if host != nil && host.exited()  { drop }   // Terminal.Done() 已关闭
if host == nil { create }
view := terminal.View(c, host.term).AutoFocus()
view.OnUnmount(func() { vm.dropConsole(device) })
```

两条重建规则解决两类死终端：

1. **换页/换模式/连接状态变化** → key 变了 → 关闭旧的、建新的；
2. **程序自己退出** → `Terminal.Done()` 已关闭 → 建新的。

没有这两条时，离开页面再回来会看到一个 `[Process exited]` 的终端，敲什么都没反应。

退出应用时 `App.Quit()` 调 `vm.CloseConsoles()` 统一关闭。

## 6. 提示与错误

### 6.1 两级提示

```go
vm.Fail(err)          // 错误级：Title「操作失败」+ err 文案，8 秒
vm.Notify("已复制…")   // 成功级：3 秒
```

- 走 `ViewModel` 内的队列 `toasts []ui.Toast`（互斥保护），因为调用方通常是 goroutine；
- `ContentView` 每帧 `drainToasts()` 后 `c.AddToast(t)`（帧内调用 `AddToast` 是安全的：`requestFrame` 在帧内是空操作，不会自激）；
- 队列排空即止，不会重复弹；
- 窗口还不存在时入队的提示条会留在队列里（`Invalidate` 为 nil），第一帧由 `ContentView` 排空，启动问题因此不会丢。

### 6.2 什么该弹、什么不该弹

| 场景 | 呈现 |
|---|---|
| 连接/断开失败、探测失败、刷新信息失败、屏幕控制失败 | 错误提示 |
| 安装 APK、上传、下载、终端命令失败、组件安装失败 | 错误提示 |
| 复制成功、保存临时设备成功、工具设置已保存、组件安装完成、设备「等待处理」 | 成功/信息提示条 |
| 启动问题（配置、历史库、凭据库打不开） | 错误提示条（10 秒）+ stderr 一行，**不再**是页面横幅 |
| 表单字段校验（地址为空、端口越界、名称过长） | **内联**文案：错误指向具体输入框，弹窗反而找不到落点 |
| 中继历史补录（`relayHistory`）失败 | **不提示**：它只是编辑器的便利数据，不影响设备是否保存成功 |

**横幅已经没有了。** 早期的 `vm.Notice` 是一条内嵌横幅，同一个字符串在内容区底部和侧栏底栏各画一遍，用户很难注意到、也不会自己消失。规则现在是：**一次性反馈（事件）走提示条，页面上的文字只描述状态**——连接失败原因（`session.Err`）、表单字段校验、终端不可用、连通测试的逐行结果都属于状态，留在原处。

### 6.3 不要静默吞掉错误

`go func() { _ = manager.Connect(...) }()` 这种写法在本仓库是**禁止**的：错误必须回流到界面。现在的写法是 `go func() { vm.Fail(manager.Connect(...)) }()`。

## 7. 设备信息的生命周期

型号 / 代号 / Android / 序列号 / Shell 权限只能从设备读，读取时机固定在 core：

| 时机 | 动作 |
|---|---|
| 连接成功（`startMonitor` 之后） | 同步调用一次 `RefreshDeviceInformation`（`getprop` 约百毫秒） |
| 开始连接 | 清空（不能显示上一台设备的信息） |
| 连接失败 / 断开 | 清空 |

因此概览里的这些字段是"连上就有、断开就空"，不依赖用户点「刷新信息」。手动刷新按钮仍保留，用于设备热重启后重新读取。

## 8. 线程与锁模型

### 8.1 谁在哪条线程

| 线程 | 做什么 | 禁止 |
|---|---|---|
| 主线程（渲染） | 建视图、读 `Manager` 快照、`c.AddToast`、`Router.Push` | 跑外部命令、等 I/O、等 goroutine |
| 管理器协程 | `Connect` / `Disconnect` / 任务执行 / 监视循环 | 触碰 `ui` 类型 |
| 命令 goroutine | `RunADB`、终端命令、安装、探测 | 直接改 `ViewModel` 字段（只允许入队 + 请求重绘） |

跨线程通知只有两个通道：`Manager.Changed()`（容量 1，自动合并）与 `Window.Invalidate()`（内部 `postMain` + CAS 合并，**任何 goroutine 可调**）。

### 8.2 两条"绝不能"规则

1. **持有 `m.mu` 时不做 I/O。**
   历史库写入曾经在 `logLocked` 里直接发生，而 `logLocked` 几乎总在锁内调用，界面每帧读锁 → 磁盘一慢，界面就冻住（见 §9.1）。现在日志写入走独立协程 + 256 缓冲队列，队列满则丢弃。

2. **不要用 `mygo.RunOnMain` 从协程请求重绘。**
   它会**阻塞到主线程执行完**，把"请求重绘"变成一次跨线程同步点。`Window.Invalidate` 本身就会投递并合并，直接用即可。

## 9. 事故复盘

### 9.1 命令出错后界面卡死

- **现象**：在 ADB 命令模式里敲一条会报错的命令（`cat /proc/meminfo` → `adb.exe: unknown command cat`），随后整个界面不再响应。
- **根因**：`Manager.logLocked` 在持有 `m.mu` 的情况下调用 `Recorder.RecordLog` → `Store.submit` → `s.queue <- job`。SQLite 写入一旦变慢（WAL 检查点、杀毒软件扫盘），通道塞满后 `submit` 阻塞，`m.mu` 一直被占住。界面每帧都要读这把锁（`Session` / `AllProfiles` / `Tasks`），于是渲染线程停摆。
- **为什么偏偏在命令出错后**：失败会产生日志 + 任务记录，正好压在这条路径上。
- **修复**：日志改投递到内存队列由独立协程写库；`Store.submit` 改为非阻塞（满则跳过）；重绘不再用 `RunOnMain`。
- **防回归**：`TestSlowRecorderNeverBlocksTheManager`——用一个永远阻塞的 `Recorder`，断言 `log()` 立即返回且 `Session()` 仍能拿锁。把直接调用改回去，该测试 2 秒即红。

### 9.2 任务面板压住页面标题

- **现象**：出现第一台任务后，"设备任务"面板画在内容区顶部，盖住面包屑与标题，标题被裁成 0 尺寸。
- **根因**：`ContentView` 里页面与面板是两个相邻的 `Fill()`（各 100% 高度），布局把它们叠在同一区域；路由器页面又是绝对定位的，于是谁在上谁在下取决于绘制顺序。
- **当轮修复**：内容区改成显式两段（页面 `Grow(1)`、面板 `Shrink(0)`），面板内部用固定高度 `Scroll`。
- **后续结论**：面板本身在下一轮被删除——它仍会压住内联对话框，而且任务本来就应该属于该设备的日志。现在任务行合并进「日志」页。
- **防回归**：`TestTaskPanelIsGone` 断言页面上不再出现「设备任务」；日志页的内容由 `logState(id).text` 断言。

### 9.3 终端显示 `[Process exited]` 后再也输入不了

- **根因**：控制台按设备缓存，页面卸载时 PTY 已结束，回来继续用同一个 `*terminal.Terminal`（程序已退出）。
- **修复**：`Element.OnUnmount` 关闭并丢弃；`Terminal.Done()` 已关闭时也重建。

## 10. 陷阱清单

| 陷阱 | 表现 | 正确做法 |
|---|---|---|
| `Fill()` 用在行上 | 行被拉成整列高 | 行用 `FillWidth()` |
| 两个相邻 `Fill()` | 元素互相覆盖、内容被裁 0 | 一个 `Grow(1)` + 一个 `Shrink(0)` |
| 忘了 `HeaderHeight` | 内容被浮动标题栏压住 | `Padding(sidebar.HeaderHeight(c), …)` |
| 底栏塞进列表列尾 | 底栏与列表抢高度 | 用 `shell.LeftToolbar` |
| `ButtonBase` 默认居中 | 行内容居中，不像列表项 | `.Justify(ui.Start)` |
| 行内 `Text` 不 `SingleLine()` | 长设备名把行撑高 | `.SingleLine()` |
| 持锁调用 `Recorder` | 磁盘一慢，界面冻住 | 入队，由独立协程写 |
| `ButtonGhost` 用在页面级动作上 | 按钮读成正文（无底色无边框），用户找不到入口 | 页面级动作用 `ButtonOutline`/`Secondary`/`Primary`；ghost 只留给文字里的链接与对话框里的「取消」 |
| 单行文本被截断却不给全文 | `.SingleLine()` 只画省略号，用户拿不到完整名称/探测详情（如「没有有效连接凭据，请编辑设…」） | 每个可能被截断的 `SingleLine` 都要 `components.Tooltip(c, anchor, 全文)`：在 anchor 之后、同一帧内调用，忽略它的返回值（隐藏时无效）；悬停延迟由 MyGo 管（约 1 秒） |
| 对话框一打开就自动跑批量作业（如「全部设备连通测试」） | 用户没同意就花掉每台 8 秒的探测，设备多时打开即卡；而且结果没人等 | 打开只展示、由「开始测试」发起；关闭对话框时 `Checker.Cancel()` 中止在跑的批次——看不见的结果不值得继续探测 |
| 用内嵌横幅报一次性结果 | 同一句提示出现在两个槽位、不会消失，用户看不见也关不掉 | 事件走 `vm.Notify` / `vm.Fail`，页面只留状态 |
| 每帧 `ui.TextBuffer.Set` | 选区与 caret 被重置，正在复制的日志跳走 | 只在渲染文本变化时写（`logState.text` 比对） |
| 在 `components.Dialog` 的内容里放 `components.Tabs(..., TabsSegmented, ...)` | 关闭对话框时崩 `ui: element from pass N used in pass M`：分段控件的 `Draw` 回调闭包住了"选中的那个 tab"元素，而 MyGo 的 overlay 关闭动画会在**不重建**的情况下继续绘制旧面板 | 对话框内用 `segmentedChoice`（`internal/app/editor.go`：`ui.SegmentedBase` + 组件库的凹槽/浮起取色）或 `components.Radio`；`components.Tabs` 只用在常驻页面上（如设备页的三个页签） |
| 直接用 `ui.Segmented` 代替上面的分段控件 | 它只画一层 `theme.Surface` 底，浅色窗口里轨道与选中块几乎看不见（用户会以为坏掉了） | 用 `segmentedChoice`，它复刻组件库分段页签的外观（轨道 `Text@6%`、选中块 `Surface` + 阴影、选中文案 600 字重） |
| `TrackScroll` 挂在不是滚动容器的元素上 | `MaxY` 一直是 0，跟随尾部失效 | `TrackScroll` 只接管 `Scroll`/`List` 这类容器；`TextAreaBuffer` 自带 `flagScrollY`，实测可用，但换控件时要重新确认 |
| `mygo.RunOnMain` 请求重绘 | 命令协程被主线程卡住 | `Window.Invalidate()` |
| 终端 `Write` 里再取同一把锁 | 自锁（敲字无反应） | 锁内只改状态，锁外 `push` |
| 用真终端写视图测试 | CI 需要下载 libghostty-vt | 视图测试里不打开终端页，或把 `newTerminal` 换掉 |

## 11. 测试手法

视图测试不需要窗口：`ui.NewTester(viewFunc, w, h)` 会真实构建并布局，可 `HasText` / `Click` / `Find`（拿矩形）/ `Texts`。

本轮新增的几类断言：

| 目的 | 手法 |
|---|---|
| 分页与返回 | `Click("E245")` 后断言 `Router.Path()`；`Router.Back()` 后断言 `Selection` 跟着变 |
| 面包屑可点 | `Click("我的设备")` 断言回到 `/devices` |
| 折叠面板 | `Click("我的设备 (1)")` 后断言行消失、`SavedOpen` 变为 false |
| 单选切换 | `Click("本地 ADB 直连")` 后断言表单字段变化 |
| 标题栏不压内容 | `Tester.SetTitleBar` 换成 48/64 DIP，断言正文首个可见元素 `Y >= sidebar.HeaderHeight(c)` |
| 日志时间线 | 纯函数 `deviceTimeline` / `logText` 断言行序；视图只断言 `logState(id).text` 与空态文案 |
| 锁不被占用 | 注入阻塞 `Recorder`，断言 `Session()` 在 1 秒内返回 |
| 行不被拉伸 | 三个设备名 `Find` 出的 `Y` 间距 < 70（旧布局是 201） |
| 未连接终端 | `lineConsole` 单测：banner、回显、回车回调、转义序列吞掉 |
| 动作顺序 | `Find("添加设备")`/`Find("临时连接")`/`Find("测试全部设备")` 的 `X` 递增 |
| 对话框关闭动画 | 打开 → 关闭 → `Advance(20ms) + Frame()` 十次，任何跨帧持有 Element 的绘制回调都会 panic（`TestDialogsCloseWithoutPanic`） |
| 截断文本有全文提示 | `Tester.Move` 到该元素中心 → `Advance(1500ms)` → `Frame()`，断言 `AccessByRole(ui.RoleTooltip, 全文)`；悬停前断言同一查询为空 |
| 关闭对话框会中止批次 | 注入永久阻塞的 `Prober`（`blockingProber`），断言 `Running()` 后才点右上角「关闭」，再断言 `Wait()` 后没有行停在「正在测试」 |
| 是提示条还是内嵌横幅 | 点完动作后断言文案出现、**没有**「关闭」按钮，且 `Advance(5s)` 后消失 |

**注意**：`ui.Tester` 的时钟是虚拟的，`Tester.Advance` 才推进。用"帧是否还在排队"判断卡死会误判——提示条动画本身会持续请求帧。判断"界面是否被锁住"要用真实的时间断言（如上面的锁测试）。

## 12. 文件索引

| 关注点 | 文件 |
|---|---|
| 窗口与槽位 | `internal/app/shell.go` |
| 侧栏、分组、底栏、提示队列、标题栏面包屑 | `internal/app/state.go` |
| 路由、页面、概览、页签 | `internal/app/detail.go` |
| 日志页（时间线、只读缓冲、取消） | `internal/app/logs.go` |
| 内嵌终端、`lineConsole` | `internal/app/terminal.go` |
| 表单（连接方式单选、中继分段与历史） | `internal/app/editor.go` |
| 上传/下载表单 | `internal/app/transfer.go` |
| 连通测试表（`components.Table`、状态图标） | `internal/app/reachability.go` |
| 错误回流（连接、探测、终端） | `internal/app/detail.go`、`internal/app/actions.go`、`internal/app/app.go` |
| 日志入队与写库 | `internal/core/manager.go`（`writeLogs`）、`internal/history/sqlite.go` |
| 设备信息读取与清空 | `internal/core/manager_tasks.go`、`internal/core/manager.go` |
| 中继历史（`relayHistory`）与预设 | `internal/core/manager_devices.go`、`internal/core/config.go` |
| 对话框关闭动画回归 | `internal/app/dialog_test.go` |
| 视图测试 | `internal/app/app_test.go`、`internal/app/forms_test.go`、`internal/app/terminal_test.go` |
| 锁不阻塞测试 | `internal/core/manager_test.go`（`TestSlowRecorderNeverBlocksTheManager`） |
