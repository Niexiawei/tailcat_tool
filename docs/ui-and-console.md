# 界面与内嵌终端（技术文档）

> 记录本轮（提交 `9aaa288` … `88d1ddd`）界面布局、页面路由、内嵌终端与错误提示的设计与实现。
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

1. **一屏一职责**：侧栏只负责"有哪些设备"，内容区只负责"当前这一页"，任务面板只负责"正在跑什么"。三者互不争空间。
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
│  ├─ Scroll   │  └─ 任务面板（Shrink 0，可滚动）              │
│  │  Accordion│                                              │
├──────────────┤                                              │
│ LeftToolbar  │                                              │
│ 通道数/工具  │                                              │
└──────────────┴──────────────────────────────────────────────┘
```

对应代码（`internal/app/shell.go`）：

```go
sidebar.NewShell(sidebar.ShellOptions{
	Title:       AppName,
	Theme:       theme.Onevra,
	StateKey:    "tailcat-manager.main",
	Left:        vm.SidebarView,     // 设备列表（可滚动）
	LeftToolbar: vm.SidebarFooter,   // 固定底栏
	Content:     vm.ContentView,     // 路由 + 任务面板
})
```

三条硬性约定，违反即错位：

| 约定 | 原因 |
|---|---|
| `Left` 与 `Content` 必须用 `sidebar.HeaderHeight(c)` 作为上内边距 | 标题栏是浮在上面的，不留出来就会被折叠按钮压住 |
| 固定底栏用 `LeftToolbar`，不要塞进 `Left` 的列尾 | 塞进列尾会和列表抢高度，出现"底栏浮在列表中间" |
| 列内**只有一个**元素可以用 `Fill()` | `Fill()` 是宽 **和** 高都 100%，两个相邻的 `Fill()` 会叠在同一位置 |

### 2.2 内容区两段

`ContentView`（`internal/app/detail.go`）是显式的两段列：

```go
ui.Column(c).Fill().Children(func() {
	ui.Column(c).FillWidth().Grow(1).Children(func() { vm.routedPage(c) })  // 页面占满剩余
	vm.TaskPanel(c)                                                        // 面板固定贴底
})
```

任务面板（`internal/app/tasks.go`）自身是 `FillWidth().Shrink(0)`，列表包在 `ui.Scroll(c).Height(150)` 里，最多显示 20 条（只读最近 20 条，完整历史在库里）。

**行式元素一律 `FillWidth()`。** `Fill()` 让行去抢高度，是最常见的布局事故（见 §9.2）。

## 3. 页面路由与导航

### 3.1 路由表

| 路由 | 页面 | 说明 |
|---|---|---|
| `/devices` | 设备列表页 | 卡片式列表（状态点 / 名称 / 连接方式与地址 / 状态），空态给出"添加设备" |
| `/devices/{id}` | 设备页 | 面包屑 + 标题行（编辑 / 连接 / 断开）+ 页签（概览 / 终端 / 日志） |
| 其他 | 列表页 | 兜底 |
| `/devices/{无效 id}` | "设备不存在"页 | 附带"返回我的设备" |

用 `ui.NewRouter(DeviceIndexRoute)` 建在 `ViewModel` 上，一次创建、随窗口存活。

### 3.2 导航的两条入口

- **侧栏点击**：`deviceRow` → `vm.OpenDevice(id)`；已在该页时不重复 `Push`。
- **面包屑**：`components.Breadcrumbs(c, []string{"我的设备", 设备名}, &vm.CrumbChoice)`，`Changed()` 且 index 为 0 时 `vm.OpenDevices()`。

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

测试全部设备 / 临时连接 / 添加设备 / 工具设置只对"设备集合"有意义，因此 `vm.toolbar(c)` 只在列表页分支里调用。设备页只保留设备自身的动作。

## 4. 侧栏

```go
ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), Space(2), 0, Space(2)).Children(func() {
	ui.Scroll(c).FillWidth().Grow(1).Children(func() { vm.deviceGroups(c) })
})
```

- **分组**：`components.Accordion` 两个 `group.Item`，标题带数量（`我的设备 (3)` / `临时设备 (空)`），展开状态在 `ViewModel.SavedOpen / TemporaryOpen`，默认都展开。空组给一句说明而不是空白。
- **设备行**：`ui.ButtonBase(c).FillWidth().Shrink(0).Justify(ui.Start).AlignItems(ui.Center).Radius(t.Radius)`，选中时 `Background(t.Selection)`。`ButtonBase` 内部是 `coreRow(c).Center()`，**必须显式 `Justify(Start)`**，否则内容居中。
- **底栏**：通道数、两个工具的健康状态（缺失时给「安装」按钮）、一行提示文本。不放动作按钮。

## 5. 内嵌终端

终端来自 `github.com/egoist/mygo/plugins/terminal`（Ghostty 的 libghostty-vt，运行期加载，**无 cgo**）。发布需 `mygo build` 才会把库打进包，`go build` 的产物首次打开终端页时会下载一次。

### 5.1 两种模式

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

复用任务机制的好处是**免费**获得：目标双校验（启动前 + 执行前）、超时、输出脱敏、尾部 32 KiB 上限、任务面板可见、进历史库。

### 5.2 未连接时：`lineConsole`

未连接（或没有 adb、或演示模式）时挂一个内存 `io.ReadWriteCloser`：

- 启动即在终端里打印「设备未连接」+ 一句说明；
- **本地回显**你敲的字符（对端不是真 shell，没人替你回显）；
- 回车时**不执行**，只打印「设备未连接」并弹错误提示；
- 转义序列（方向键、功能键）用状态机吞掉，不会把 `[A` 当文字显示；
- 支持退格与 Ctrl-C。

### 5.3 控制台的生命周期

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
- 队列排空即止，不会重复弹。

### 6.2 什么该弹、什么不该弹

| 场景 | 呈现 |
|---|---|
| 连接/断开失败、探测失败、刷新信息失败、屏幕控制失败 | 错误提示 |
| 安装 APK、上传、下载、终端命令失败、组件安装失败 | 错误提示 |
| 复制成功、保存临时设备成功 | 成功提示 |
| 表单字段校验（地址为空、端口越界、名称过长） | **内联**文案：错误指向具体输入框，弹窗反而找不到落点 |

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
- **修复**：内容区改成显式两段（页面 `Grow(1)`、面板 `Shrink(0)`），面板内部用固定高度 `Scroll`。
- **防回归**：`TestTaskPanelSitsBelowThePage` 用 `ui.NewTester` 的 `Find` 比较几何位置，旧布局下报 `task panel (y=0) overlaps the page header (y=20 h=25)`。

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
| 几何不错位 | `Find("设备任务")` 与 `Find("我的设备")` 的 `Y/H` 比较 |
| 锁不被占用 | 注入阻塞 `Recorder`，断言 `Session()` 在 1 秒内返回 |
| 行不被拉伸 | 三个设备名 `Find` 出的 `Y` 间距 < 70（旧布局是 201） |
| 未连接终端 | `lineConsole` 单测：banner、回显、回车回调、转义序列吞掉 |

**注意**：`ui.Tester` 的时钟是虚拟的，`Tester.Advance` 才推进。用"帧是否还在排队"判断卡死会误判——提示条动画本身会持续请求帧。判断"界面是否被锁住"要用真实的时间断言（如上面的锁测试）。

## 12. 文件索引

| 关注点 | 文件 |
|---|---|
| 窗口与槽位 | `internal/app/shell.go` |
| 侧栏、分组、底栏、提示队列 | `internal/app/state.go` |
| 路由、页面、概览、页签 | `internal/app/detail.go` |
| 任务面板 | `internal/app/tasks.go` |
| 内嵌终端、`lineConsole` | `internal/app/terminal.go` |
| 表单（含连接方式单选） | `internal/app/editor.go` |
| 上传/下载表单 | `internal/app/transfer.go` |
| 错误回流（连接、探测、终端） | `internal/app/detail.go`、`internal/app/actions.go`、`internal/app/app.go` |
| 日志入队与写库 | `internal/core/manager.go`（`writeLogs`）、`internal/history/sqlite.go` |
| 设备信息读取与清空 | `internal/core/manager_tasks.go`、`internal/core/manager.go` |
| 视图测试 | `internal/app/app_test.go`、`internal/app/forms_test.go`、`internal/app/terminal_test.go` |
| 锁不阻塞测试 | `internal/core/manager_test.go`（`TestSlowRecorderNeverBlocksTheManager`） |
