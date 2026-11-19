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
13. [设备文件页](#13-设备文件页「文件」页签)

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
	vm.routedPage(c)   // 页面占满，含设备页的「概览 / 资源 / 终端 / 文件 / 日志」
})
```

**没有固定的任务面板。** 设备任务并进「日志」：设备页的日志标签与侧栏的日志页共用一份实现（`logView`，见 §14），数据来自 SQLite 时间线；运行中的任务在设备日志标签顶部一键取消。历史上那个 `Shrink(0)` 贴底面板会压住内联对话框，已删除。

**行式元素一律 `FillWidth()`。** `Fill()` 让行去抢高度，是最常见的布局事故（见 §9.2）。

## 3. 页面路由与导航

### 3.1 路由表

| 路由 | 页面 | 说明 |
|---|---|---|
| `/devices` | 设备列表页 | 卡片式列表（状态点 / 名称 / 连接方式与地址 / 状态），空态给出"添加设备"；页面标题在窗口标题栏里，正文不再重复 |
| `/devices/{id}` | 设备页 | 面包屑 + 标题行（编辑 / 连接 / 断开）+ 页签（概览 / 资源 / 终端 / 文件 / 日志） |
| `/logs` | 日志页 | 应用错误 / 通道日志 / 任务的统一时间线，四个筛选项（见 §14）；面包屑「首页 › 日志」；入口在侧栏设备列表之下 |
| `/logcat` | Logcat 页 | 一台已连接设备的 `adb logcat` 实时流，可按级别 / 标签 / 内容筛选、暂停、跟随、清空与导出（见 §15）；面包屑「首页 › Logcat」；入口紧跟在「日志」之后 |
| `/manage/appmeta` | 缓存管理页 | 按设备列出已缓存的应用名称与图标（包名、大小、读取时间、是否过期），可删除单条或清空这台设备；面包屑「首页 › 缓存管理」 |
| `/manage/derp` | DERP 节点页 | 上半部固定（新建 / 默认节点），下半部把预设与最近用过合成一个滚动框，可新建、编辑、删除（在用不可删）；入口在侧栏设备列表之下 |
| `/manage/derp/{id}` | DERP 节点详情页 | 区域与节点表、原始 JSON、使用设备；面包屑「首页 › DERP 节点 › 名称」，点中间一级回到列表；URL 型可强制重新获取 |
| `/manage/settings` | 工具设置页 | ADB / scrcpy 的路径与当前解析结果，缺失的工具可直接安装；面包屑「首页 › 工具设置」，侧栏设备列表之下有入口。**不是弹窗** |
| 其他 | 列表页 | 兜底 |
| `/devices/{无效 id}` | "设备不存在"页 | 附带"返回我的设备" |

用 `ui.NewRouter(DeviceIndexRoute)` 建在 `ViewModel` 上，一次创建、随窗口存活。

### 3.2 导航的两条入口

- **侧栏点击**：`deviceRow` → `vm.OpenDevice(id)`；已在该页时不重复 `Push`。
- **标题栏面包屑**：`vm.ContentTitle` 交给 `ShellOptions.ContentTitle`，用 `components.Breadcrumbs(c, []string{"首页", 设备名}, &vm.CrumbChoice)`；`Changed()` 且 index 为 0 时 `vm.OpenDevices()`。它读路由而不是 `Selection`，所以按 ⌘[ 后退时标题栏自动跟着变。根一级叫「首页」而不是「我的设备」：它下面的页面不止设备（缓存 / DERP / 日志 / 工具设置），「我的设备」是侧栏那个设备分组。`deviceIDFromPath(path)` 是这一步唯一的路径解析（纯函数，单独可测）。

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
	r.Title("首页")
	vm.toolbar(c)                   // 窗口级操作只在列表页
	vm.deviceIndexPage(c)
})
```

这样"后退回上一台设备"时侧栏高亮、详情内容天然一致，不需要额外同步逻辑。**不要**在别处再写一遍 `Selection = ...`，否则两个真相会打架。

管理页（缓存 / DERP / 日志 / 工具设置）在 `routedPage` 里统一走 `vm.dropSelection(path)`：它们不显示任何设备，侧栏留着一条高亮的设备行会被读成"这些数据属于它"。进缓存管理时它顺手把当前设备交给 `CacheDevice`——第一次打开仍落在刚才那台设备上（`TestManagePageLeavesNoDeviceSelected`、`TestCachePageOpensOnTheDeviceTheUserCameFrom`）。

### 3.4 窗口级操作只属于列表页

添加设备 / 临时连接 / 测试全部设备只对"设备集合"有意义，因此 `vm.toolbar(c)` 只在列表页分支里调用，顺序固定：**主操作「添加设备」在最前**，其余按常用度排在后面。设备页只保留设备自身的动作。「工具设置」既不在这里也不在底栏：它是一个页面，入口在侧栏的页面列表里（见 §4）。

### 3.5 动作一律「图标 + 文字」

窗口级动作统一走 `actionButton(c, icon, label, variant)`：`components.Button` 加 `Children`，里面是 `components.Icon(c, lucide.X)` 和 `components.Text`，按钮的 `Label` 仍是文案本身——所以无障碍名与 `Tester.Click("添加设备")` 都不受影响。用的都是 `lucide` 图标：`Plus`（添加设备）、`Zap`（临时连接）、`Radar`（测试全部设备）。

**不要**给动作只放文字：一行同尺寸的文字按钮读起来像正文（见 §10 的幽灵按钮一行）。

### 3.6 对话框的固定写法

所有 `components.Dialog` 都按同一套骨架排，`internal/app/reachability.go` 是完整例子：

```go
components.Dialog(c, &open, components.DialogOptions{Label: title}, func(panel ui.Element) {
	panel.Padding(18).Gap(10).Width(760)
	panel.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
			components.Text(c, title).FontSize(18).Bold()
			if components.IconButton(c, lucide.X(), "关闭", components.IconButtonPlain).Clicked() {
				vm.closeReachability() // 关窗 + 中止后台批次
				open = false
			}
		})
		components.Text(c, "一句话说明它做什么、代价是什么。").FontSize(11).TextColor(theme.TextMuted)
		// 内容：表格或表单
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			if actionButton(c, lucide.Play, "开始测试", components.ButtonPrimary).Disabled(running).Clicked() {
				vm.Manager.TestAllDevices()
			}
		})
	})
})
if !open {
	vm.closeReachability() // Esc、点遮罩、右上角 ✕ 都走到这里
}
```

- **关闭只放右上角一个图标按钮**（`components.IconButton` + `lucide.X()`，`Label` 写「关闭」）。底栏不放「取消 / 关闭 / 完成」，只放这个对话框真正要做的那**一件**事；主作用按钮带图标、用 `ButtonPrimary`。
  目前只有「全部设备连通测试」是这个形状；「添加设备 / 上传下载」底栏还留着文字的「关闭」，迁移时照抄上面这段即可（在那之前，底栏里紧挨主按钮的次要动作仍用幽灵样式，见 §10 幽灵按钮一行）。
- **`if !open` 是唯一的关闭出口**：Esc、遮罩、✕ 都会把它置为 false，收尾（中止后台批次、丢弃草稿）只写这一处。
- **慢的、批量的、耗网络的事情由用户点按钮发起**，打开对话框不自动跑；关闭时中止它——看不见的结果不值得继续探测。
- **结果要一眼看懂**：成功/失败同时给文字和图标，颜色取 `theme.Success` / `theme.Danger`（映射集中在一个纯函数 `reachStatus` 里，测试直接断言颜色）。
- **表格一律用 `internal/app/table.go` 的 `tableCard`**：它调用 `ui.Table` 并加 1 DIP 圆角边框、按需要定高或 `Grow(1)`、给表一个标签。页面**不要**直接写 `ui.Table`，也不要再用 onevra 的 `components.Table` / `TableVirtualized`（`TestNoOnevraTableComponentsAnywhere` 会红）。
- **每个表格都有边框**：`tableCard` 默认画 1 DIP 圆角框。例外只给"表格已经在别人画的框里"：文件页右栏（整页浏览区是一个框）与 DERP 页的预设列表（下半部那个滚动框），那里传 `tableOptions{Unframed: true}`（`TestOnlyAFramedPaneLeavesTheTableUnframed` 钉住）。
- **不要把表格塞进 `components.ScrollView` / `ui.Scroll`**：`ui.Table` 自己就是滚动区，表头与内容在同一个横向滚动容器里；外面再套一层竖滚容器，列一调宽，调整抓手就会被裁到可视区外（这正是换组件的原因）。唯一例外是 `tableOptions{Fit: true}`：卡片按 `Rows` 给的行数撑满、不封顶到 `maxTableRows`，表格自己**没有**滚动条，由外面那个框统一滚（DERP 页的预设列表就是这么做的，见下一条）。
- **`Fit` 只给"外面那个框负责滚"的地方**：`Fit` 的表高=内容高，所以它不会藏行，也不会出现第二条滚动条。前提是**行高统一**：`Fit` 会把每行钉成主题的最小行高（`ListState.RowHeight`），因为行里放按钮的行天然比表头高，按"最小行高"估算表高会每行差一两像素，最后几行就被表格自己的滚动条藏起来（`TestDerpPresetTableShowsEverySavedRow` 钉住）。反过来说，一张几百行的表会一次性建出所有行——用在预设列表（几十条）可以，不要用在文件列表那种没有上限的地方。
- **行级点击走 `ListState.Selected` + `Table(...).Changed()`**：`ui.Table` 没有 `TableRow.Clicked()`；`Selected` 初始给 -1，否则点第一行"没变化"。
- **排序由页面算，不由组件算**：把 `&state.Sort` 交给表，建表前用 `Sort` 里的列自己排好行序（资源页 `sortApps` / `sortProcessRows`）。
- **筛选框一律 `ui.SearchField`**：放大镜、非空时的清除按钮、Escape 清空都是它自带的。绑定的是**草稿**，`Submitted()`（Enter）或清空才落到真正筛选用的字段（`logFilter.searchDraft`、`logcatState.tagDraft/termDraft`、`resourceState.filterDraft`）。不要再用 `components.TextBox` 配 `LeadingIcon: lucide.Search()` 逐字生效——三个页面各写一遍已经不划算，规则写在这里；清空必须立刻生效，否则读者面对一个看不见又摘不掉的筛选。
- **可能被截断的单行文本**都要挂 `components.Tooltip` 给全文（见 §10）。

## 4. 侧栏

```go
ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), Space(2), 0, Space(2)).Children(func() {
	ui.Scroll(c).FillWidth().Grow(1).Children(func() {
		vm.deviceGroups(c)
		ui.Column(c).Padding(0, 4).Gap(2).Children(func() { vm.manageEntries(c) })
	})
})
```

- **管理入口**：设备两组之后是四行页面入口——缓存管理（`lucide.Database`）、DERP 节点（`lucide.Server`）、日志（`lucide.ScrollText`）、工具设置（`lucide.Settings`）。它们是页面不是窗口动作，因此放在滚动区里、设备列表之下，而不是底栏；每行与设备行同样的内边距、左对齐与选中底色，读起来是同一份「去哪里」的列表。工具设置排在最后：它是配置，不是数据页。
- **分组**：`components.Accordion` 两个 `group.Item`，标题带数量（`我的设备 (3)` / `临时设备 (空)`），展开状态在 `ViewModel.SavedOpen / TemporaryOpen`，默认都展开。空组给一句说明而不是空白。
- **设备行**：`ui.ButtonBase(c).FillWidth().Shrink(0).Justify(ui.Start).AlignItems(ui.Center).Radius(t.Radius)`，选中时 `Background(t.Selection)`。`ButtonBase` 内部是 `coreRow(c).Center()`，**必须显式 `Justify(Start)`**，否则内容居中。
- **提示**：设备行的名称与状态行都挂了 `components.Tooltip`，侧栏只有 220 宽，长名字必然被截断。
- **底栏**：只有两个工具的健康状态（缺失时给「安装」按钮）。工具设置的入口在侧栏页面列表里，同一个地方只留一个入口——底栏再放一个按钮，读起来会像两件不同的事。不放通道计数（它与设备行上的状态点重复，且无法操作），也不放提示文本。

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

**关闭不在渲染线程上做**：`dropConsole` 把 `closeConsole(host)` 丢给 goroutine，因为关一个终端等于杀它背后的 `adb shell`，Windows 上是一次 `taskkill /T /F`（实测约 0.8 秒）。第一版在 `OnUnmount` 里同步关，于是"离开终端页签"和"从设备页跳到别的页面"都会卡一下。`CloseConsoles` 反过来必须等：退出应用时同步关完再退，否则 adb shell 会变成孤儿进程。`TestLeavingTheTerminalTabDoesNotWaitForItsProcess` 与 `TestQuittingTheAppWaitsForTheConsoles` 各钉一半。

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

### 8.2 三条"绝不能"规则

1. **持有 `m.mu` 时不做 I/O。**
   历史库写入曾经在 `logLocked` 里直接发生，而 `logLocked` 几乎总在锁内调用，界面每帧读锁 → 磁盘一慢，界面就冻住（见 §9.1）。现在日志写入走独立协程 + 256 缓冲队列，队列满则丢弃。

2. **不要用 `mygo.RunOnMain` 从协程请求重绘。**
   它会**阻塞到主线程执行完**，把"请求重绘"变成一次跨线程同步点。`Window.Invalidate` 本身就会投递并合并，直接用即可。

3. **不在渲染线程上等一个子进程退出。**
   杀一个进程树在 Windows 上是同步的 `taskkill /T /F`，实测 **约 0.8 秒**（启一个只要 5–18 ms，差两个数量级）。这条规则踩过两次：离开 Logcat 页时 `LogcatStream.Close()` 等 `Proc.Stop`（§15），设备页「终端」页签卸载时 `term.Close()` 杀 adb shell（§5.4）。两处都改成"取消 + 丢给 goroutine"，只在**退出应用**时等（`Manager.Shutdown` → `CloseAndWait()`、`vm.CloseConsoles()`），否则应用先退会留下孤儿 adb。

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
| 用标签当键的树里出现同名项目 | 控制台刷 `two elements under one parent have the key "Download"`，同名目录共享一个状态 | 树键必须唯一：`components.TreeVirtualized` 用设备路径作键，不要用文件名 |
| 视图每一帧都给失败的 I/O 发起重试 | 失败原因被自己抹掉，页面永远停在「正在读取」，同时不停发命令（`/cache` 就是这么卡住的） | 失败要粘住：`loadDir` 见到已有失败记录就返回，只有用户点「重试」才清掉 |
| 用 `ButtonGhost` 做页面动作 | 按钮读成正文（无底色无边框），用户找不到入口；对话框里也会与正文混在一起 | **一律不用 ghost**：主操作 `Primary`、次要 `Secondary`、安静动作与对话框「取消 / 关闭」`Outline`，文字链接用 `ButtonLink`。`TestNoGhostButtonsAnywhere` 扫源码钉住 |
| 就地写十六进制颜色 | 同一个"警告橙"在三个文件里各写一遍，改一次漏两处 | 颜色只在 `internal/app/theme.go`（`accentHex` / `warningHex`）定义，视图取 `c.Theme()` 的字段 |
| 用内容（时间 + 来源 + 文本）拼表格行键 | 两行完全相同的日志在同一个父节点下撞 key，控制台被刷屏，跟随新日志还会把重复的一行当成"已存在"丢掉 | 行键用数据库自己的标识：`TimelineEntry.Key`（`log:<行号>` / `task:<uuid>`），没有内容能唯一区分两行；它经 `ui.ListState.Key` 交给表 |
| 用 `Changed()` 判断 `components.Switch` / `Tabs` 的切换 | 开关的 write-back 可能落在比 `Changed()` 早一帧的绘制里（`Click` 的按下与抬起分属两帧），于是界面上开关动了、状态却没保存——「跟随最新」关不掉就是这么来的 | 把控件写回的**值**和已保存的值比：`follow != f.Follow` 就落盘，不依赖 `Changed()` |
| 两个 `components.Tabs` 并排且首项同名 | 用户与测试都分不清点的是哪一组 | 每组前加灰字标签（「级别」「时间」），末档换名（「全部时间」）错开 |
| 单行文本被截断却不给全文 | `.SingleLine()` 只画省略号，用户拿不到完整名称/探测详情（如「没有有效连接凭据，请编辑设…」） | 每个可能被截断的 `SingleLine` 都要 `components.Tooltip(c, anchor, 全文)`：在 anchor 之后、同一帧内调用，忽略它的返回值（隐藏时无效）；悬停延迟由 MyGo 管（约 1 秒） |
| 对话框一打开就自动跑批量作业（如「全部设备连通测试」） | 用户没同意就花掉每台 8 秒的探测，设备多时打开即卡；而且结果没人等 | 打开只展示、由「开始测试」发起；关闭对话框时 `Checker.Cancel()` 中止在跑的批次——看不见的结果不值得继续探测 |
| 用内嵌横幅报一次性结果 | 同一句提示出现在两个槽位、不会消失，用户看不见也关不掉 | 事件走 `vm.Notify` / `vm.Fail`，页面只留状态 |
| 每帧 `ui.TextBuffer.Set` | 选区与 caret 被重置，正在复制的日志跳走 | 只在渲染文本变化时写（`internal/app/logs.go` 不再有缓冲，日志时间线用 `tableCard` 的 `ui.Table`） |
| 在渲染函数里直接查日志库 | 库大时每帧一次读，界面跟着磁盘抖 | 筛选变化时起 goroutine 读，落地后再 `Invalidate` 一帧（`ensureLogQuery`） |
| `ui.ListState.Selected` 从 0 开始 | 点第一行时"没变化"，`Changed()` 不触发，任务行打不开输出 | 初始值给 -1（`logFilter.selected`） |
| 倒序时间线上"跟随最新"当成滚到底 | 新行前插把内容往下顶，读者被推离最新一行 | 跟随＝`ScrollTo(0)`；"尾部"在倒序列表里是顶部 |
| 页面切走后跟随协程还在跑 | 看不见的视图每 2 秒查一次库 | `markLogVisible` 在切换视图时把上一个的 `following` 关掉 |
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
| 面包屑可点 | `Click("首页")` 断言回到 `/devices` |
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

### 11.1 截图核对（人工，不是断言）

改完界面「文字都在」不等于「看起来是对的」：边框有没有画、缩进对不对、控件有没有被压成 0 高、对话框是不是盖在页面上——这些只有看图能判断。`internal/app/screenshots_test.go` 把每个页面状态渲染成 PNG：

```sh
# Windows（正斜杠也可以）
TAILCAT_SHOT_DIR=D:/tmp/shots go test ./internal/app -run TestScreenshots -v
# macOS / Linux
TAILCAT_SHOT_DIR=/tmp/shots go test ./internal/app -run TestScreenshots -v
```

- **不设环境变量就 SKIP**，`go test ./...` 不写文件、不耗时（CI 不受影响）。
- **没有断言、没有基准图**：像素级比对在不同字体/平台上必然不稳，本仓库也就不会因为「差一个像素」红。它的用途是人（或代理）看一眼。
- 用例表 `shotCases()` 覆盖：设备列表、侧栏、概览、资源、文件（列表 / 读失败 / 读取中 / 属性弹窗）、日志页（有数据 / 空态）、任务输出对话框、设备日志标签、设备编辑器、临时设备、工具设置页、连通测试、干预弹窗。**加了页面或对话框状态就加一条**。
- 每帧循环里都有一次真实 `time.Sleep`：`ui.Tester` 的时钟是虚拟的，目录读取、任务启动跑在 goroutine 上，不睡就永远等不到数据。
- `view` 字段可以只截某个 shell 槽位（侧栏就是这么截的）。
- **终端页不在此列**：真实终端要运行期加载 libghostty-vt，`go test` 不应下载它（见 §10 最后一行），终端外观用 `go run ./cmd/tailcat-manager --demo` 目视。

**注意**：`ui.Tester` 的时钟是虚拟的，`Tester.Advance` 才推进。用"帧是否还在排队"判断卡死会误判——提示条动画本身会持续请求帧。判断"界面是否被锁住"要用真实的时间断言（如上面的锁测试）。

## 12. 文件索引

| 关注点 | 文件 |
|---|---|
| 窗口与槽位 | `internal/app/shell.go` |
| 侧栏、分组、底栏、提示队列、标题栏面包屑 | `internal/app/state.go` |
| 路由、页面、概览、页签 | `internal/app/detail.go` |
| 日志页与设备日志标签（共享 `logView`、筛选、跟随、输出、清空） | `internal/app/logs.go` |
| Logcat 页（设备选择、筛选、表格、暂停/跟随、清空、导出、流的生命周期） | `internal/app/logcat.go` |
| logcat 拆行、解析、环形缓冲、过滤（纯逻辑，不启进程） | `internal/core/logcat.go` |
| logcat 流的启动与按设备登记（`StartLogcat` / `StopLogcat`） | `internal/core/manager_logcat.go` |
| 内嵌终端、`lineConsole` | `internal/app/terminal.go` |
| 表单（连接方式单选、中继分段与「预设 + 历史」合并选择；选中预设后来源框锁定，选「自定义」解锁为多行输入） | `internal/app/editor.go` |
| 上传/下载表单 | `internal/app/transfer.go` |
| 连通测试表（`tableCard`、状态图标） | `internal/app/reachability.go` |
| 错误回流（连接、探测、终端） | `internal/app/detail.go`、`internal/app/actions.go`、`internal/app/app.go` |
| 日志时间线查询、迁移、清理、清空 | `internal/history/sqlite.go` |
| 应用级日志入队（`RecordAppLog`） | `internal/core/manager.go` |
| 管理页入口骨架（侧栏 `manageEntries`、面包屑 `managePageTitle`） | `internal/app/manage.go` |
| 设备信息读取与清空 | `internal/core/manager_tasks.go`、`internal/core/manager.go` |
| 中继历史（`relayHistory`）与预设 | `internal/core/manager_devices.go`、`internal/core/config.go` |
| 设备文件的读取与缓存 | `internal/core/remotefs.go`（纯解析）、`internal/core/manager_files.go`（静默读 + 缓存） |
| 文件页视图与属性弹窗 | `internal/app/files.go` |
| 对话框关闭动画回归 | `internal/app/dialog_test.go` |
| 视图测试 | `internal/app/app_test.go`、`internal/app/forms_test.go`、`internal/app/terminal_test.go`、`internal/app/files_test.go` |
| 页面截图（人工核对） | `internal/app/screenshots_test.go`（`TAILCAT_SHOT_DIR` 打开） |
| Logcat 页的视图测试与假 runner（`logcatRunner`） | `internal/app/logcat_test.go` |
| 锁不阻塞测试 | `internal/core/manager_test.go`（`TestSlowRecorderNeverBlocksTheManager`） |

## 13. 设备文件页（「文件」页签）

设备页第四个页签（概览 / 资源 / 终端 / 文件 / 日志）是文件浏览与传输：左边是可展开的文件夹树，右边是所选文件夹的条目表格（名称 / 大小 / 修改时间 / 权限）。点文件行弹出属性弹窗，可以下载到本地；目录行可以进入，弹窗里也能把本地文件上传到那个文件夹。

### 13.1 布局

```go
// 整个浏览区套一层边框，和终端面板同一个写法。
ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(1).
	Radius(theme.Radius).Border(1, theme.Border).Clip().Children(func() {
	components.Split(c, &state.treeWidth,
		func() { vm.folderTree(c, profile, state) },   // 左：components.TreeVirtualized
		func() { vm.fileTable(c, profile, state) },    // 右：面包屑 + tableCard
		components.SplitOptions{FirstMin: 180, SecondMin: 260, Default: 260},
	).Fill()
})
```

**外观是一整块带边框的面板**，不是两列裸内容：`Padding(1)` 让里面的内容不吃掉边框（和终端面板一致），`Clip()` 让圆角真的裁掉内容。

三条必须遵守的约定：

| 约定 | 原因 |
|---|---|
| `Split` 的两个面板都要 `MinWidth(0)`（内部 `ui.Scroll` / `ScrollView` 都要带） | 不加的话长文件名会把左右两栏一起撑过窗口宽度，右侧被裁 |
| 树用 `components.TreeVirtualized`，**键是设备路径**（`fileState.tree`） | 树的所有行都是同一个 Column 的直接子元素。`components.Tree` 用**标签**当键，于是 `/sdcard/DCIM` 和 `/system/DCIM` 会拿到同一个 key，控制台报 `two elements under one parent have the key "DCIM"`，两行共享一个状态（展开一个另一个也变）。键换成路径后同名目录各走各的，顺便拿到虚拟滚动 |
| 树**只有一个根**：`/` | 曾经把 `/sdcard`、`/storage/emulated/0`、`/data/local/tmp`、`/system` 当捷径与 `/` 并列，结果 `/` 展开后同一个目录又出现一次（`... → storage → emulated → /storage/emulated/0`），两行同键、各子目录全部报 `have the key`。目录只能有一个真相：从 `/` 往下走 |
| 符号链接算文件夹（`RemoteEntry.Folder()`） | 真机上 `/sdcard` 是指向内部存储的软链，`stat` 报 `symbolic link`。只认 `directory` 的话，单根树里最重要的那个目录会直接消失。代价：指向普通文件的软链也会被当成文件夹，点进去会显示「无法进入」，这是可接受的一头错 |
| 行式元素 `FillWidth()`，滚动容器 `Grow(1).MinHeight(0)` | `Fill()` 会同时抢高度（见 §10） |

**展开箭头是「里面还有东西」的唯一提示**（`treeChildren`，纯函数，测试直接断言）：

| 该目录读过了吗 | 有子目录吗 | 结果 | 原因 |
|---|---|---|---|
| 没读过 | — | 显示箭头 | 还不知道里面有什么，先给箭头；点开才去读 |
| 读过 | 有 | 显示箭头 | 正常可展开 |
| 读过 | 没有 | **不显示箭头** | 点开也只会是空的，不要给出一个假承诺。**包括已经展开的**：早先为了"能收回去"保留箭头，用户看到的就是一个转向下、里面却什么都没有的箭头 |

组件按 `children(path)` 是否为 `nil` 决定画不画箭头，所以「键为空切片」和「键为 `nil`」是两种意思，别写反。

树只有一个根 `/`，默认展开到 `/sdcard`（`openTreeTo(state, "/sdcard")`），右侧默认列出 `/sdcard`。所以启动时会预取 `/` 下十几个目录——这是「点进去不用等」的价格，跳过 `/proc`、`/sys`、`/dev`、`/acct`。

**左侧必须跟着右侧走**（`showDir` + `revealInTree`），两件事缺一件用户就会觉得树没反应：

1. `openTreeTo` 把路径上的**每一级都展开，包括根 `/`**。漏掉根就会在用户手动收起 `/` 之后，右侧越点越深、左侧永远只剩一个折叠的 `/`。
2. 树建完当帧的 rows 之后，`revealInTree` 把 `state.tree.List.Selected` 移到 `state.path` 那一行并 `ScrollIntoView`；目标在屏幕外时也要滚过去（`TestFileTreeScrollsTheFolderIntoView`）。

`revealInTree` **直接改 `Selected`，不走 `ListState.choose`**：`choose` 会触发 `OnChange` → `followTreeCursor` → `showDir`，等于让树把右侧刚去过的地方再导航一遍（点一次面包屑会跳两次）。只有 `row != *Selected` 时才动，因此也不会每帧把用户手动滚走的树拽回来。

### 13.2 取数：一次 `stat` 往返，静默执行

```sh
cd '<目录>' || exit 9
for f in ./* ./.[!.]* ; do …; stat -c '%F|%s|%a|%A|%Y|%n' "$f"; done
```

- **名称放在格式串最后**，Go 侧 `SplitN(…, 6)`：文件名里有 `|` 也不会错位。
- 路径里的 `'` 转义成 `'\''`；含换行/NUL/非绝对路径时**不发命令**，直接返回中文错误。
- 脚本第一行是 `cd '<目录>' 2>&1 || exit 9`：`2>&1` 把 shell 自己对 `cd` 的抱怨带进输出，页面才能说「没有权限查看该文件夹：/cache」而不是笼统的「不可读」。退出码 `9` 是脚本自己的「`cd` 失败」。
- 单目录 **2000 条**上限，超出时脚本末尾打印 `@@TRUNCATED`，页面写「只显示前 2000 项」。这是为了防止 runner 的 1 MiB 输出上限把一个巨型目录**静默**截断成"看起来完整"的列表。
- **不显示创建时间**：`%W`（birth time）在 Android 的 ext4/f2fs 上几乎总是 0，用户已裁决不显示这一行。

`Manager.ListRemoteDir` 照 `SampleResources` 的形状写：`operationTarget` → `validateTarget` → `runner.Run` → `targetIsCurrent` → 解析 → 缓存 + `notify()`。**它不建任务、不写通道日志、不进历史**——否则展开十个文件夹就把日志页和历史库刷满（`TestListRemoteDirCreatesNoTask` 钉住）。上传/下载反过来必须走 `BeginADBTask`：可取消、日志页可见、超时与脱敏免费。

### 13.3 状态放哪

| 状态 | 位置 | 生命周期 |
|---|---|---|
| 目录内容（`DirListing`） | `Manager.dirListings`，键 `dirKey{device, path}` | `Disconnect` 时清空（`forgetDirListingsLocked`）。界面是状态的函数，渲染线程不持有数据 |
| 树展开、选中路径、右侧目录、表格列宽 | `ViewModel.files[device].fileState` | 随窗口存活；切设备不串味 |
| 正在读 / 读取失败 | `ViewModel.fileLoads` / `fileErrs`，键 `"device|path"`，由 `fileMu` 保护 | 由命令 goroutine 写，写完 `Invalidate()` |

`beginFileLoad` 保证**同一路径只有一次读取在飞**：连点两下文件夹不会发两条命令（`TestFileTreeReadsEachFolderOnce`）。

### 13.3.1 失败之后等用户，不要每帧重试

`loadDir` 的 `!force` 分支有三个提前返回：已缓存、**已有失败记录**、已有读取在飞。

第三条是踩出来的：早先只判断「已缓存」，于是一个读不了的目录（`/cache`）会在**每一帧**重新发起读取，而每次发起都先 `clearFileFailure` ——刚记下的失败原因被自己抹掉，页面永远停在「正在读取…」，看起来就是卡死。现在失败是粘住的，只有用户点「重试」才清掉（`TestFilePageWaitsForTheUserAfterAFailedRead`：20 帧后同一目录只读过 1 次）。

### 13.3.2 提前读下一级

一次目录读取成功之后，同一个 goroutine 会**顺序**把它的子目录各读一遍（`prefetchDirs`），于是点进任何一个子文件夹都是立刻就绪。三条边界：

- 顺序执行，不在同一台设备上并发发命令；
- 跳过 `/proc`、`/sys`、`/dev`、`/acct` 这类伪文件系统：它们的列表又长又没用，读一次纯属浪费；
- 失败同样记进 `fileErrs`，用户点进去直接看到原因（这反而是提前拿到的好消息）。

### 13.4 交互与弹窗

- 点树节点：右侧列该目录 + 展开该节点。
- 点表格里的**目录**行：进入（`showDir`：右侧切换 + `openTreeTo` 展开到该目录，含根；`revealInTree` 把树的高亮与滚动移到那一行）。
- 右侧上方的路径是 `components.Breadcrumbs` 面包屑：祖先可点，一路点回上层；`state.crumb` 是它的绑定，`showDir` 会把它重置为 -1。
- 文件列表区域的「正在读取」和「读取失败」都是**居中**的一整块（`AlignItems(Center).Justify(Center)`），不是贴在左上角的一行字——它们是这一栏的全部内容，不是它的一行。
- 点**文件**行：打开 `FileDetailView` —— 属性四行（文件名 / 文件大小 / 最后修改时间 / 文件权限 `%A (八进制)`）+ 主按钮「下载到本地…」+ 次按钮「上传到此文件夹…」。目录行改成「下载此文件夹…」。
- 弹窗按 §3.6 的固定骨架：右上角一个 `IconButton` 关闭，`if !open` 是唯一收口；**不要在**对话框里用 `components.Tabs`。
- 错误呈现分两类：读目录失败是**页面状态**（「读取失败：…」+「重试」），不弹提示条；发起下载/上传失败才是事件（`vm.Fail`）。
- 树里不渲染「正在读取…」「没有子文件夹」这类**额外行**：缩进要自己算，而且会让树的行数与目录数对不上。状态直接挂在节点行上——读取中给 `components.Spinner`，失败给红色「读取失败」小字并把原因挂 `Tooltip`。要动手重试就点该目录，右侧那块居中区域会给出完整原因和「重试」。

### 13.5 测试手法

`internal/app/files_test.go` 用 `newRunnerViewModel` 注入一个按目录返回 `stat` 输出的假 runner，然后：

| 目的 | 手法 |
|---|---|
| 树的形状 | `treeRows(vm)` 取整个树的路径序列，断言等于 `["/", "/sdcard", "/sdcard/DCIM", …]`：一个根、软链目录在里面、没有重复行 |
| 列表渲染 | `waitForText(t, tt, "照片.jpg")` —— 目录读取在 goroutine 里，必须「反复 `Frame()` 直到出现」，不能只 `Frame()` 一次 |
| 没有创建时间 | `tt.HasText("创建时间")` 为假（`HasText` 是子串匹配） |
| 树懒加载 | 点整条路径的根（页面别处不会出现 `/system` 这个文本）→ 再点 `bin` → 断言树里出现「没有子文件夹」 |
| 只读一次 | 点同一个节点三次，统计 runner 收到的 `cd '/system'` 命令数 = 1 |
| 提前读下一级 | 只等 `/sdcard` 一次读取，然后断言命令里已经出现 `cd '/sdcard/Download'` 与 `cd '/sdcard/DCIM'`（没点过任何节点） |
| 失败后不刷命令 | 让读取返回退出码 9，断言出现「读取失败：…」后再 `Frame()` 二十次，同一目录的命令数仍是 1 |
| 展开箭头也会读 | 直接把 `*state.openAt("/system")` 置真（等价于点箭头），断言发出了 `/system` 的命令 |
| 面包屑能回退 | 进入 `Download` 后点面屑里的 `sdcard`，断言回到了 `/sdcard` 的内容 |
| 树跟着右侧走 | 手动收起 `/` 再从表格进 `Download`，断言 `/sdcard/Download` 重新成为树里的一行（`TestFileTreeReopensTheRootTheTableNavigatedFrom`） |
| 高亮跟着走 | 从表格进 `Download` 后断言 `*state.tree.List.Selected` 指向 `/sdcard/Download`（不是 `/`）；`/` 下塞 40 个目录让 `/sdcard` 落在屏幕外，断言它落在 `List.Visible()` 里（`Visible` 两端都是闭区间） |
| 箭头规则 | 纯函数 `treeChildren` 的五个分支：没读过 / 读过有子目录 / 读过没子目录 / 空但已展开 |
| 同名目录不串状态 | 让 `/sdcard` 与 `/system` 都有 `Download`，展开两者：抓 `log.SetOutput` 断言没有 `have the key`，并断言树里没有重复行 |
| 树的实际内容 | `treeRows(vm)` 读 `TreeVirtualizedState` 的 `Rows()` / `Item(i)` 得到路径列表，比按标签找元素可靠（同名目录不会歧义） |
| 上传/下载 | 替换包级变量 `openLocalPaths` / `saveLocalPath`（`files.go` 里的 `mygo.Dialog.Open/Save`），断言 `Manager.Tasks()` 出现对应任务 |

**不要**在测试里直接改 `mygo.Dialog`：它是包级变量，改了会影响同进程其它测试；用自己的两个包装变量并在 `t.Cleanup` 还原。

## 14. 日志页与设备日志标签

日志界面**只有一份实现**：`internal/app/logs.go` 的 `logView(c, f, opts)`，两个入口只传不同的过滤状态与选项。

| 入口 | 路由 | 设备筛选 | 跟随最新 | 设备选择器 | 清空 |
|---|---|---|---|---|---|
| 侧栏「日志」 | `/logs` | 全部，可切成「只看应用」或某台设备 | 默认关 | 显示 | 有（警告橙按钮） |
| 设备页「日志」页签 | `/devices/{id}` | 钉死为本设备（`DeviceID = 本设备`，**不是 nil**） | 默认开 | 隐藏 | 有 |

设备标签之所以钉死设备而不是留空，是"这台设备的日志"要名副其实：应用级日志只进日志页的「只看应用」，不混进任何设备页。

**数据来源**：`core.HistoryReader`（`Timeline` / `TaskOutput` / `ClearLogs`），`cmd/tailcat-manager/main.go` 注入 `history.Store`。页面不读 `Session.Logs`，也不持有内存缓冲——重启后仍能查就是靠这一点。

**一条时间线**：SQL 用 `UNION ALL` 合并 `logs`（应用 / 通道）与 `tasks`，按时间倒序，`LIMIT` 在 `WHERE` 之后（默认 500 行，超出时页面底部提示"只显示最近 500 条"）。任务行的文本是「标题 · 中文状态」；任务的 `output` 只参与 `WHERE` 搜索，不进结果集。

**布局固定三段**：筛选行、时间线 box、底部动作行。

- 筛选行：设备选择器（只有日志页有）一行；级别（全部 / 错误 / 警告 / 信息）与时间段（今日 / 7 天 / 30 天 / 全部时间）同一行，各带一个灰字标签，避免两个「全部」挨着分不清；搜索框与「跟随最新」一行。

**搜索框是 `ui.SearchField`**（放大镜图标、非空时右侧出现清除按钮、Escape 也清空）：绑定的是 `searchDraft`，`Search` 才是查询真正用的值，**按 Enter 才生效**（`Submitted()`），清空时立刻生效——一个看不见的筛选条件不该继续筛。第一版是 `components.TextBox` 逐字生效，每敲一个字母就重读一次时间线。`Label("搜索日志")` 不只是无障碍：测试靠它定位输入框（`tt.Click("搜索日志")`）。
- 时间线 box **始终占满剩余高度**：没有日志时也不塌陷，空态文案「这段时间没有日志」在位居中、字号 16（正文是 11）；还没读完时显示「正在读取日志…」。
- 底部动作行固定在页面最下面：「刷新」（主要按钮 + 图标）在左、「清空日志…」（警告橙 + 垃圾桶图标）在右；被 `LIMIT` 截断时左侧多一句「只显示最近 500 条，请缩小时间范围」。

关键词分隔多个词时全部命中才算匹配（`LIKE` 转义 `% _ \`）。关键词与级别只影响**显示**，不参与清空的删除范围。

**跟随最新**：打开时每 2 秒用 `Since = 最新一行的时间` 只查增量，结果前插（时间线是倒序），并把列表滚回第 0 行——倒序列表里"尾部"就是顶部。关掉跟随，页面保持静止，新行不会把读者顶走。

**查询不在渲染路径上**：`ensureLogQuery` 只在"筛选与上次查询不一致"时起一个 goroutine，落地后 `Invalidate` 一帧；同一时间只允许一次在飞（`loading`）。`logMu` 保护查询协程写的那部分（`entries` / `query` / `loading` / `follow` / `lastAt`），`table`（`ui.ListState`）与 `selected` 只由渲染线程碰。

**任务行可点**：点任务行 → `TaskOutput(id)`（goroutine）→ 对话框里滚动显示输出。清空只删 `logs`：任务记录保留，对话框里写明"任务记录不会被删除"；删除范围＝当前时间段 + 设备，级别与关键词不算。

**空态**：`这段时间没有日志`；还没读完时显示 `正在读取日志…`。

**`--demo`**：`demoHistory`（`internal/app/demo.go`）是内存版 `Recorder` + `HistoryReader`，预置一条错误、一条警告、每台一条通道日志与一条失败任务，所以 `--demo` 的日志页与截图不会是空的。

---

## 15. Logcat 页（实时日志流）

侧栏「Logcat」（`lucide.Activity`）→ 路由 `/logcat`，面包屑「首页 › Logcat」。名字保留英文：它就是 logcat，翻译成「实时日志」反而要用户多猜一次这两个页面有什么区别——`/logs`（§14）是**应用与设备的历史时间线**，`/logcat` 是**一台设备此刻的日志流**，两者不共享任何数据。

**布局固定四段**：设备选择器、筛选行（级别与开关一行、两个搜索框一行）、表格 box、底部状态行。

- 设备选择器**只列已连接的设备**（`session.State == StateOnline` 且有 serial）。没连上的设备不在这里出现：一个离线 serial 会让 `adb logcat` 挂住或反复报错。一台都没有时整页只有一句「没有已连接的设备，先到设备页连接一台。」，不起任何进程。
- 筛选行：第一行是「级别」灰字标签 + 5 段 `components.Tabs`（全部 / 调试 / 信息 / 警告 / 错误，是**最低级别**语义：选「警告」留 W/E/F），右侧「跟随最新」开关与「暂停 / 继续」；第二行是「标签」「内容」两个 `ui.SearchField`，空格分隔多个词、全部命中才算、大小写不敏感——与 §14 的关键词规则相同。两个框各有一份草稿（`tagDraft` / `termDraft`），**按 Enter 才落到 `Filter.Tags` / `Filter.Terms`**，清空时立刻落（`logcatSearchField`）。重新进入页面时草稿会从已生效的值同步回来，所以输入框显示的永远是正在生效的筛选，而不是上次离开时打了一半的字。
- **暂停时筛选照样生效**：暂停冻结的是「新行自己进来」，不是「不让你找」。改筛选会立刻在**整个缓冲**里重算（包括暂停之后收到的行），因为筛选问的是缓冲，不是屏幕；新行仍然要等「继续」才出现。第一版把暂停实现成「干脆不刷新」，于是暂停后标签/内容框形同虚设。
- **标签列两行**：上面是 logcat 的 tag，下面灰字是这条日志的**进程名/包名**（`Manager.ProcessName`，来自页面打开时与之后每 10 秒一次 `adb shell ps -A` 的静默读取；断开连接时清空）。PID 会被回收，所以旧读数只是提示。设备不认 `ps -A` 时只少这一行，页面其余部分照常。
- **时间 / 级别 / PID / 标签 / 内容都能选中**：每个单元格包在 `ui.Selectable()` 里，拖选、双击选词、`Shift`+方向键、右键菜单的 Copy/Select All 都可用，⌘C / Ctrl+C 复制选中的部分。行尾那个复制图标仍然是把**整条**（含多行堆栈）一次拿走。拖选只在单个单元格内生效（表格的每个单元格是独立的 Selectable 容器），要整行用图标。
- 表格 `Grow` 占满剩余高度，空态也不塌陷：还没收到行时「正在接收日志…」，有筛选条件且没命中时「没有符合筛选条件的日志」。
- **内容列换行显示完整**：这一列不 `SingleLine`，一条日志需要几行就占几行，行高随内容增高（表格不设 `RowHeight`）。原来的单行省略 + 悬停提示把堆栈截成一行，读崩溃现场等于读不到。代价：一次可见的行变少。
- **行尾是复制图标**（`components.IconButton` + `lucide.Copy`，`IconButtonCompact`）：点它把这一条按导出的格式（`2006-01-02 15:04:05.000 E/Tag(pid): 正文`，多行保留换行）放进剪贴板，提示「已复制这一行」。`writeClipboard`（`internal/app/actions.go`）是变量，测试替换它，不碰真剪贴板。
- 状态行：`已接收 N 行 · 显示 M 条`（**行**是设备打印的行，**条**是页面上的条目：一次调用打出多行仍是一条），丢弃过再加 `· 缓冲已满，丢弃 K 条（上限 30000 条 / 16 MB）`——上限写在数字旁边，用户不用猜缓冲有多大，暂停时加 `· 已暂停`；右侧「导出…」「清空缓冲」，流结束后多一个「重新开始」；读者滚离末尾而「跟随最新」仍开着时，状态文字右边多一句「已暂停跟随」和一个「跳到最新」（见下一段）。「清空缓冲」把**行数、条数与两个计数器一起归零**（`LogcatStream.Clear`）：缓冲里的东西和描述它的数字是一件事，「显示 0 条」旁边挂着「已接收 39400 行」读起来像丢了 39400 行。`Seq` 不归零——它命名行身份，不能与旧行重号。

**同一次调用的多行是一条**：logcat 把一条带换行的消息（异常堆栈）存成一行一条记录，但时间戳、pid、tid、级别、tag 全同。解析后遇到「这五项与上一条完全相同」就拼到上一条的正文里（换行连接），所以一条堆栈在表格里是一条，不是几十行。代价：同一毫秒、同一 tag 的两条不同消息会被并成一条（正文都在，只是同一条目里换行）。

**「跟随最新」开关打开就是"贴到最新"**：mygo 的 `ListState.FollowEnd` 只是"允许跟随"，它真正的 `following` 是"上一帧是否停在末尾"，所以光把开关置 true 不会动——用户得先手动滚到底部，开关才生效。页面因此在三种时刻显式调一次 `ScrollToEnd()`（`logcatState.jumpToEnd`）：**打开开关**、**从暂停继续**、**进入页面**。关掉开关会顺手清掉还没执行的跳转，否则那一帧的表格会先跳到末尾再停下。

**但"开关开着却不在末尾"仍然会出现**，而且是故意的：读者往上滚看历史时，mygo 会把 `following` 置 false（否则日志流每 100 ms 就把他拽回末尾，没法读）。这时开关的状态是骗人的——它说"跟随最新"，实际不跟。补法不是偷偷把它关掉，而是在底栏露出 **「已暂停跟随」+ 「跳到最新」**：一次点击 `ScrollToEnd()`，把读者送回正在写的那一行。判据用 `ListState.AtEnd()`（读上一帧的布局，所以底栏必须建在表格**之后**）。`TestLogcatOffersAWayBackToTheNewestLine` 钉住三件事：在末尾时不露这个按钮、滚走后不强行跟随、点了就回到最新。

**复制列是固定列**（`TableColumn.Fixed` + `Align: ui.End`）：`ui.Table` 默认让用户拖动表头换列序、拉边框改列宽，而 `ListState.Columns` 会把这份布局存下来——复制按钮因此会被拖到中间，或者被拉宽到挤出右边缘。`Fixed` 让它在声明的位置（最后）不动、不可拉宽，行序怎么排都不影响。另外**固定列不会被压缩**：窗口变窄时它们不让位，让位的只有 `Width: 0` 的「内容」列，所以五列固定宽度之和就是按钮需要的宽度。现在声明的值（时间 132 / 级别 58 / PID 56 / 标签 190 / 复制 44）是算着 **600 DIP 窗口**给的——按原来的 150/64/64/190，窗口 600 时按钮只剩 14 px、560 时完全不可见；现在 600 完整，580 起也完整，再窄就只剩横向滚动，那是宽表格的通用取舍。`TestLogcatCopyColumnStaysLast` 用一次伪造的拖拽（`Columns.Order = ["copy","at"]`）钉住位置，`TestLogcatCopyButtonStaysOnScreenInANarrowWindow` 在 600 DIP 窗口里钉住可见性。

**行序旧→新（与 §14 的时间线正相反）**：流就是往下滚的读法，新行出现在底部。`ui.ListState.FollowEnd` 正好是这个语义——`FollowEnd = 跟随最新`，用户往上滚就自动停止跟随、滚回底部又继续。表格的行身份是 `LogcatEntry.Seq`：两行日志可以时间、级别、标签、正文全同，只有序号能区分。

**数据来源**：`core.LogcatStream`（`internal/core/logcat.go`），不是任务、不进 SQLite、没有超时、不占任务面板。`Manager.StartLogcat` 用 `Runner.Start` 跑 `adb -s SERIAL logcat -v threadtime`，输出在管道协程里拆行 → 解析 → 进一个环形缓冲（**30000 条 / 16 MB**，谁先满就从最早的一端丢）；页面每帧只调 `Snapshot(filter)` 取当前缓冲里全部命中筛选的条目——表格是虚拟化的，所以缓冲里有多少就能滚多少，没有第二道上限。一台设备同时只有一条流，换设备先关旧的。

**筛选全在本机**：级别 / 标签 / 关键词都不过设备（不重开进程、不丢行）。代价是 V/D 行照样从设备经 Tailnet 传到本机，一台刷屏设备可能占带宽；真要压带宽，升级路径是给 `StartLogcat` 加 `*:W` 这样的 spec，代价是切换级别会重开进程。

**流的生命周期**：`routedPage` 每帧先 `beginLogcatFrame()`、再 `defer settleLogcat()`；`logcatPage` 一进来就 `logcatDrawn = true`。一帧没画这个页面就说明用户走了，`settleLogcat` 立刻 `Manager.StopLogcat`。关窗口到托盘时没有帧可画，属于同一件事的另一半：`shell.go` 的 `window.OnClosed` 里调 `vm.CloseLogcat()`，否则藏起来的窗口会一直读设备通道。

**停流绝不能在渲染线程上等进程**：Windows 上杀一个进程树是同步的 `taskkill /T /F`，实测 **约 0.8 秒**（`Runner.Start` 只要 5–18 ms）。第一版在 `Close()` 里直接调 `Proc.Stop()`，而 `Close` 是在画页面时被调用的，于是每次离开 Logcat 页都卡 1–2 秒。现在 `Close()` 只做 `cancel()` 并把 `stop` 丢给一个 goroutine，立刻返回；只有退出应用走 `Manager.Shutdown` → `CloseAndWait()`，那里必须等——应用先退就会留下一个孤儿 adb。`TestLogcatStreamCloseDoesNotWaitForTheProcess` 与 `TestShutdownWaitsForTheLogcatStop` 钉着这两半。**不自动重启**：流结束（设备断开、adb 退出）后页面显示「日志流已结束」与「重新开始」，必须由用户点——断开后偷偷连回去是红线。

**`-v threadtime` 的年份**：threadtime 不打印年份与时区。解析时用「当前年」，若算出的时间晚于现在 24 小时则减一年（1 月读到的 12 月日志属于刚过去的那年）。续行（不以时间戳开头的缩进行）拼到上一条的正文里；`--------- beginning of main` 这类分隔行丢弃；单行超过 8 KiB 截断并加「（已截断）」，否则一条跑飞的二进制行会把缓冲撑爆。

**导出**：状态行的「导出…」调 `mygo.Dialog.Save` 弹系统保存对话框（默认文件名 `logcat-YYYYMMDD-HHMMSS.txt`，过滤 `*.txt`），写的是**当前筛选命中的行**——筛选是这个页面的意义所在。一行一条 `2006-01-02 15:04:05.000 I/Tag(1234): 正文`：文件里写完整日期，因为被翻看的文件需要年份，而表格里只到月日。写文件在 goroutine 里做，成功弹「已导出 N 行到 <路径>」；用户取消不是错误，静默返回（系统对话框在某些平台上把"取消"报成 error）。没有可导出的行时不弹对话框，只提示「没有可导出的行」。

**`--demo`**：`demoRunner.Start` 见到 `logcat` 就按 150ms 打印一条演示行，直到 ctx 取消（`--demo` 没有真实进程，`ExecRunner` 忽略 ctx，演示版遵守它）。所以演示环境里这一页有内容，筛选与导出也能试。
