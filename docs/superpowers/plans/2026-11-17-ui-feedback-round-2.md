# 界面反馈第二轮：导航、日志、按钮与中继 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 把面包屑搬进 shell 的系统标题栏槽位（修好"回不去设备列表"）、把设备任务并进日志页（只读可滚动文本缓冲）、去掉无意义的通道计数、把幽灵按钮换成可读的层级，并把 DERP 中继改成分段切换 + 独立的中继历史。

**架构：** 视图改动集中在 `internal/app`；导航新增一个 `ViewModel.ContentTitle`，交给 `shells/sidebar` 的 `ContentTitle` 槽位，路由路径解析用一个纯函数 `deviceIDFromPath`。中继历史是 `internal/core` 新增的一张独立配置表 `relayHistory`（与已有的 `relayPresets` 并列、互不混用），保存设备时在同一笔持久化里补录。日志页用 mygo 的缓冲式多行控件（`ui.TextAreaBuffer`，经 `components.TextBoxBuffer` 包装）承载合并后的时间线。

**技术栈：** Go 1.27、`github.com/egoist/mygo/ui`（含 `ui.TextBuffer`、`TextAreaBufferBase`、`ScrollState`）、`github.com/oneo-me/onevra/source/components`（含 `Breadcrumbs`、`Tabs/TabsSegmented`、`NativeSelect`、`TextBoxBuffer`）、`github.com/oneo-me/onevra/source/shells/sidebar`。

**规格：** 本计划直接对应用户这一轮的 6 条截图反馈（用户编号为 1、2、3、4、6、7，没有第 5 条），以及 2026-11-17 对三个设计问题的裁决。竞品行为基线见 `docs/ui-and-console.md`，硬约束见 `AGENTS.md`。

---

## 全局约束

- 纯 Go、无 cgo；`CGO_ENABLED=0 GOOS=linux|darwin|windows go build ./...` 必须过。
- 依赖方向单向：`cmd → internal/app → internal/core → internal/platform`。中继历史属于 core。
- `tailcat` 字样只允许出现在 `forwarder.go`、`pinger.go`、`identity.go`、`relay.go`。
- 凭据不落配置文件；DERP 来源不是凭据，可以进 `configuration.json`（`RelayPreset` 已经这么做了）。
- 面向用户文案是中文，测试逐字断言；改文案必须同步改测试。
- GUI 一律优先用 onevra `components`；需要 mygo 基元时必须在提交信息里写明"哪个组件缺什么能力"。
- 行式元素用 `FillWidth()`，**禁止** `Fill()`（同时占满高度）。
- 每个提交都要带 `gofmt -w`、`go vet ./...`、`go test ./...`，用户可见改动同提交写进 `docs/CHANGELOG.md`。
- 分支 `feat/ui-feedback-round-2`，不要直接提 `main`。

---

## 决策（已确认）

| # | 决定 | 理由 / 代价 |
|---|---|---|
| D1 | 面包屑搬进 `ShellOptions.ContentTitle`（shell 的系统标题栏槽位），由 `ViewModel.ContentTitle` 渲染；**同时**保留内容区的 `HeaderHeight` 上内边距 | 用户裁决。`ContentTitle` 是 shell 自己画的标题栏区域，点击不会被拖拽区吞掉；内容区内边距仍然必须有，否则页面里的标题行与「编辑/连接/断开」按钮一样压在标题栏下面点不到。代价：设备页的标题行比之前再多让 48 DIP；`ContentTitle` 在 `Shell.SetPage` 替换页面上会被隐藏（本应用没有替换页）。 |
| D1b | 设备列表页正文不再重复一个大标题「我的设备」（标题栏已经写了） | 决策的连带结果。代价：列表页正文少一个 h1，空态文案「连接你的第一台设备」直接成为首行。设备页保留正文大标题，因为它带副标题与操作按钮。 |
| D2 | 日志页用 `components.TextBoxBuffer(c, buf, TextBoxOptions{Multiline: true, ReadOnly: true})`（底层就是 `ui.TextAreaBufferBase`），滚动交给外层 `ui.Scroll` | 用户要求"用 `ui.TextAreaBuffer` 显示、允许滚动、禁用输入"。mygo 的 `TrackScroll` 只接管滚动容器（`Scroll`/`List`），挂在文本区上不生效，所以滚动挂外层。代价：多一层容器（任务 2 步骤 1 先跑探针确认）。 |
| D3 | 任务行并入日志正文，删掉固定的「设备任务」面板；取消入口做成日志页顶行的「取消全部」；**侧栏底栏不加运行中计数** | 用户裁决"不需要"。代价：列表页看不到别家设备的任务，只能逐台进日志页看；这是明确接受的取舍，不要在后续 commit 里偷偷加回来。 |
| D4 | 幽灵按钮的边界写成规则：`ButtonGhost` 只用于①嵌在文字里的链接（面包屑的祖先项）、②对话框里紧挨 Primary 的次要动作（「取消」）；页面级动作一律 `ButtonOutline`/`ButtonSecondary`/`ButtonPrimary` | 截图里「编辑」「复制 ADB 地址」是透明底无边框，和正文混在一起。代价：按钮视觉重量整体上升一档。 |
| D5 | 中继历史**单独开一张 `Configuration.RelayHistory`**，与 `RelayPresets` 并列、互不混用；保存设备时自动补录，名字由来源派生 | 用户裁决。代价：多一个配置字段、多一个可持久化类型、多一套 core 测试；好处是"用户手动命名的预设"与"自动记住的来源"各自干净，后续的中继管理页可以只删历史。 |
| D6 | 中继切换用 `components.Tabs(..., TabsSegmented, ...)`，不用 mygo 的 `ui.Segmented` | 二者都是分段控件；`components.Tabs + TabsSegmented` 是本仓库「概览/终端/日志」已在用的同一个组件，视觉一致，且符合"onevra 优先"。若要严格用 `ui.Segmented`，只需换这一行调用，其余步骤不变。 |

**执行方式：** 本环境没有子智能体调度工具，`subagent-driven-development` 不可用，因此按 `executing-plans` 在当前会话逐任务内联执行，最后一个任务结束后做一次全分支审查。

---

## 一、问题与根因（先确认要改的是不是这块）

| 用户编号 | 现象 | 根因 | 位置 |
|---|---|---|---|
| 1 | 面包屑「我的设备」点不动，回不到列表页 | ①面包屑画在**内容区**里，`ContentView` 的根列没有 `Padding(sidebar.HeaderHeight(c), …)`，页面从 y=20 开始画，而 shell 的浮动标题栏高 `max(48, TitleBar.Height, Rem(3))` 且盖在内容之上——点击落在标题栏的拖拽区；②导航本身也放错了槽位，shell 提供了 `ContentTitle` 专门给标题栏内的面包屑用。`ui.NewTester` 不建 shell，所以 `TestBreadcrumbReturnsToDeviceIndex` 一直是绿的 | `internal/app/detail.go` `ContentView`/`breadcrumbs`、`internal/app/shell.go`、`shells/sidebar.Shell` |
| 2 | 设备任务单独占一块 box，还画在对话框上面 | `vm.TaskPanel(c)` 是 `ContentView` 里与路由页并列的第二段（`Shrink(0)` + 内嵌 `ui.Scroll().Height(150)`），它先于各对话框绘制，z 序上压住了 `components.Dialog`（截 3 里「取消/保存」被任务行盖住） | `internal/app/tasks.go`、`internal/app/detail.go` `ContentView` |
| 3 | 日志页是一行行 `ui.Row`，不能滚动、不能选中复制 | `logs()` 直接 `for range session.Logs` 渲染 `ui.Row`，没有任何滚动容器 | `internal/app/detail.go` `logs` |
| 4 | 底栏「GUI 0 条通道」意义不明 | 显示的是 `Manager.OwnedActiveCount()`（本应用自管且已分配 serial 的通道数），和设备行上的状态点是同一件事 | `internal/app/state.go` `SidebarFooter` |
| 6 | 「编辑」「复制 ADB 地址」几乎看不见 | 用的是 `ButtonGhost`（`bg`、`border` 都是零值，只剩文字），`ui.ButtonBase` 本身也不带底色 | `internal/app/detail.go` `devicePage`、`overview`；`state.go` `toolRow`；`detail.go` `toolbar` |
| 7 | 「默认中继 / 自定义中继 / 当前：自定义 JSON」是三个文字按钮，之前填过的中继不保留 | 切换是 ghost 按钮拼的；配置里只有"用户手动命名"的 `RelayPresets`，保存设备时不会记住来源 | `internal/app/editor.go`；`internal/core/manager_devices.go` `SaveDevice`、`internal/core/config.go` |

---

## 二、文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `internal/app/detail.go` | 路由页、设备页、概览；`ContentView` 的槽位与上内边距 | 修改：加 `HeaderHeight` 内边距、删正文面包屑、删任务面板段、删旧 `logs()`、列表页去 h1、`toolbar` 去掉重复内边距 |
| `internal/app/state.go` | 侧栏、底栏、toast 队列、标题栏 | 修改：加 `ContentTitle` + `deviceIDFromPath`、删通道数、按钮层级、`logStates` 字段 |
| `internal/app/shell.go` | 窗口装配 | 修改：`ShellOptions.ContentTitle` 接上 `vm.ContentTitle` |
| `internal/app/logs.go` | 日志页：时间线合并（纯函数）、每设备的文本缓冲与滚动状态、只读文本区、运行中任务与取消 | **新建** |
| `internal/app/tasks.go` | 旧的固定任务面板 | **删除** |
| `internal/app/logs_test.go` | 时间线纯函数、每设备独立缓冲、空态、取消 | **新建** |
| `internal/app/editor.go` | 设备表单、中继选择 | 修改：分段切换 + 历史下拉；`EditorInput` 去掉 `RelaySelection` |
| `internal/app/app_test.go` | 视图与路由测试 | 修改：加标题栏/几何测试、替换面包屑与任务面板测试 |
| `internal/app/forms_test.go` | 表单校验纯函数测试 | 修改：中继字段改名后的用例 |
| `internal/core/config.go` | 配置文件读写与归一化 | 修改：加 `RelayHistory` 字段与 `nil` 归一化 |
| `internal/core/session.go` | 数据模型 | 修改：加 `RelayHistoryEntry` |
| `internal/core/manager.go` | 管理器状态与快照 | 修改：加 `relayHistory` 字段、`RelayHistory()` 访问器 |
| `internal/core/manager_devices.go` | 保存设备 | 修改：`rememberRelayLocked` 补录、`persistLocked` 带上新字段 |
| `internal/core/manager_devices_test.go` / `config_test.go` | 补录规则与往返 | 修改 |
| `docs/CHANGELOG.md` | 用户可见变更 | 每个任务各加条目 |
| `docs/ui-and-console.md` | 技术文档 | 任务 7 统一改：§2.1/§2.2/§3.2/§4/§6.2/§10/§12 |

---

## 审查重点（Review Focus）

| 输入 / 条件 | 正常人期待的行为 | 钉住它的测试（放在哪个任务） |
|---|---|---|
| 系统标题栏更高（macOS 带交通灯，`TitleBar.Height = 64`）或主题字号放大 | 正文第一个可交互元素（设备名、编辑按钮）仍在标题栏下方；标题栏里的面包屑可点 | `TestContentStaysBelowAFloatingHeader`（T1，`Tester.SetTitleBar` 换高度跑两遍） |
| 停在设备页按 ⌘[ / Alt+←，或点标题栏面包屑，或在列表页按 ⌘] | 三种方式都落在正确页面，标题栏文字跟着变 | `TestContentTitleBreadcrumbReturnsToTheDeviceIndex`、`TestContentTitleFollowsTheRouter`（T1） |
| 日志 200 行 + 该设备 50 个任务，且日志正在滚动 | 页面正常渲染、文本内容完整、不出现"只显示新行、旧行消失" | `TestLogTextKeepsEveryLineOfAFullWindow`（T2） |
| 两台设备来回切换，A 设备日志很长、B 设备刚连上 | 两个日志页各自独立，A 的行不会出现在 B 里 | `TestLogPageKeepsBuffersSeparatePerDevice`（T2） |
| 设备从未连接、没有任何任务，直接点「日志」 | 给一句「暂无通道日志与任务」，不是一块空白巨框 | `TestLogPageHasAnEmptyState`（T2） |
| 日志正在滚动追加时用户往回滚 | 不再自动跟到末尾；滚回底部后恢复跟随 | `TestLogPageStopsFollowingAfterScrollUp`（T2） |
| 同一个中继来源保存两次、或两个来源的 host 相同 | 历史里只留一条；名字冲突自动加后缀；**绝不**让设备保存失败 | `TestSaveDeviceRecordsRelayHistoryOnce`、`TestRelayHistoryNameNeverCollides`（T6） |
| 配置文件里 `relayHistory` 缺字段 / 是 `null`，或从没有中继历史的旧版本升级 | 老配置能读，历史为空，不报错、不崩 | `TestLoadAcceptsAConfigurationWithoutRelayHistory`、`TestSaveWritesRelayHistoryAsAnEmptyArray`（T6） |

---

## 任务 1：内容区让出浮动标题栏 + 面包屑搬进系统标题栏（用户问题 1）

**文件：**
- 修改：`internal/app/detail.go`（`ContentView`、`devicePage`、`deviceIndexPage`、`toolbar`，删 `breadcrumbs`）、`internal/app/state.go`（新增 `ContentTitle`、`deviceIDFromPath`）、`internal/app/shell.go`
- 测试：`internal/app/app_test.go`

- [ ] **步骤 1：写失败的测试**

```go
// app_test.go 顶部补 import "github.com/oneo-me/onevra/source/shells/sidebar"

func TestContentStaysBelowAFloatingHeader(t *testing.T) {
	for _, bar := range []ui.TitleBar{{Height: 48}, {Height: 64}} {
		vm := newTestViewModel(t)
		vm.OpenDevice(vm.Manager.Profiles()[0].ID)
		var header float32
		tt := ui.NewTester(func(c *ui.Context) {
			header = sidebar.HeaderHeight(c)
			vm.ContentView(c)
		}, 900, 700)
		tt.SetTitleBar(bar)
		tt.Frame()
		// The device name and the action row are the topmost body content;
		// the shell's floating header owns the strip above this height.
		for _, text := range []string{"E245", "编辑"} {
			rect, ok := tt.Find(text)
			if !ok {
				t.Fatalf("titlebar %v: %q is missing; texts = %v", bar, text, tt.Texts())
			}
			if rect.Y < header {
				t.Fatalf("titlebar %v: %q (y=%.0f) is under the floating header (h=%.0f)", bar, text, rect.Y, header)
			}
		}
	}
}

func TestIndexPageStaysBelowAFloatingHeader(t *testing.T) {
	vm := newTestViewModel(t)
	var header float32
	tt := ui.NewTester(func(c *ui.Context) {
		header = sidebar.HeaderHeight(c)
		vm.ContentView(c)
	}, 900, 700)
	rect, ok := tt.Find("测试全部设备")
	if !ok {
		t.Fatalf("the toolbar is missing; texts = %v", tt.Texts())
	}
	if rect.Y < header {
		t.Fatalf("the toolbar (y=%.0f) is under the floating header (h=%.0f)", rect.Y, header)
	}
}

func TestDeviceIDFromPath(t *testing.T) {
	id := uuid.New()
	if got, ok := deviceIDFromPath(DeviceRoute(id)); !ok || got != id {
		t.Fatalf("deviceIDFromPath(%q) = %v, %v", DeviceRoute(id), got, ok)
	}
	for _, path := range []string{DeviceIndexRoute, "", "/devices/nope", "/devices/" + id.String() + "/extra"} {
		if _, ok := deviceIDFromPath(path); ok {
			t.Fatalf("deviceIDFromPath(%q) accepted a non-device path", path)
		}
	}
}

func TestContentTitleShowsTheDevicePath(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	tt := ui.NewTester(vm.ContentTitle, 420, 48)
	if !tt.HasText("我的设备") || !tt.HasText("E245") {
		t.Fatalf("the title bar does not show the path; texts = %v", tt.Texts())
	}
}

func TestContentTitleBreadcrumbReturnsToTheDeviceIndex(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	// The title bar is a strip of its own, so the click never races the
	// window-drag area the way it did inside ContentView.
	tt := ui.NewTester(vm.ContentTitle, 420, 48)
	if err := tt.Click("我的设备"); err != nil {
		t.Fatalf("Click() on the breadcrumb error: %v", err)
	}
	if got := vm.Router.Path(); got != DeviceIndexRoute {
		t.Fatalf("route = %q, want %q", got, DeviceIndexRoute)
	}
}

func TestContentTitleFollowsTheRouter(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	tt := ui.NewTester(vm.ContentTitle, 420, 48)
	vm.Router.Back()
	tt.Frame()
	if tt.HasText("E245") {
		t.Fatalf("the title bar kept the device after going back; texts = %v", tt.Texts())
	}
	if !tt.HasText("我的设备") {
		t.Fatalf("the title bar lost the page name; texts = %v", tt.Texts())
	}
}

func TestDevicePageHasNoSecondBreadcrumb(t *testing.T) {
	vm := newTestViewModel(t)
	vm.OpenDevice(vm.Manager.Profiles()[0].ID)
	tt := ui.NewTester(vm.ContentView, 900, 700)
	// The body must not repeat navigation the title bar already shows; the
	// device name is body content, so only the crumb text counts.
	for _, text := range tt.Texts() {
		if text == "我的设备" {
			t.Fatalf("the device page repeats the breadcrumb in the body; texts = %v", tt.Texts())
		}
	}
}
```

同时改两处既有断言（它们依赖旧布局）：
- `TestDeviceIndexPageListsDevices`：正文不再有「我的设备」大标题，改成只断言设备名与点击跳转。
- `TestBreadcrumbReturnsToDeviceIndex`：**删除**，由 `TestContentTitleBreadcrumbReturnsToTheDeviceIndex` 取代（否则同一行为有两份测试，且旧的那份测的是错的槽位）。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app/ -run "StaysBelowAFloatingHeader|ContentTitle|DeviceIDFromPath|SecondBreadcrumb" -v`
预期：FAIL —— `undefined: deviceIDFromPath`、`vm.ContentTitle undefined`；几何用例报 `"E245" (y=20) is under the floating header (h=48)`。

- [ ] **步骤 3：内容区加内边距**

`internal/app/detail.go`，`ContentView` 的根列：

```go
ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), 0, 0, 0).Children(func() {
	ui.Column(c).FillWidth().Grow(1).Children(func() { vm.routedPage(c) })
	vm.TaskPanel(c) // 任务 3 会删掉这一行
})
```

`toolbar` 不再自己让位置（否则内边距叠加成两倍）：`ui.Row(c).FillWidth().Padding(0, 16, 8, 16).Gap(6).AlignItems(ui.Center)`。

- [ ] **步骤 4：`internal/app/state.go` 加标题栏渲染与路径解析**

```go
// deviceIDFromPath returns the device a route path names. It is the routing
// the title bar needs without entering Router.View.
func deviceIDFromPath(path string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(path, DeviceIndexRoute+"/")
	if !ok || strings.Contains(rest, "/") {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rest)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// ContentTitle fills the shell header's leading slot: the path back to the
// device index on a device page, and the page name everywhere else. It lives
// in the title bar so the header's drag strip can never swallow the click.
func (vm *ViewModel) ContentTitle(c *ui.Context) {
	title := func(text string) { components.Text(c, text).Bold().SingleLine() }
	if vm.Router == nil {
		title("我的设备")
		return
	}
	id, ok := deviceIDFromPath(vm.Router.Path())
	if !ok {
		title("我的设备")
		return
	}
	profile, ok := vm.profileByID(id.String())
	if !ok {
		title("设备不存在")
		return
	}
	vm.CrumbChoice = -1
	crumbs := components.Breadcrumbs(c, []string{"我的设备", profile.Name}, &vm.CrumbChoice)
	if crumbs.Changed() && vm.CrumbChoice == 0 {
		vm.OpenDevices()
	}
}
```

- [ ] **步骤 5：把槽位接上并删掉正文里的面包屑**

`internal/app/shell.go`：

```go
shell := sidebar.NewShell(sidebar.ShellOptions{
	Title:        AppName,
	Theme:        theme.Onevra,
	StateKey:     "tailcat-manager.main",
	Left:         vm.SidebarView,
	LeftToolbar:  vm.SidebarFooter,
	ContentTitle: vm.ContentTitle, // 标题栏内的面包屑
	Content:      func(c *ui.Context) { vm.ContentView(c) },
})
```

`internal/app/detail.go`：删掉 `devicePage` 里的 `vm.breadcrumbs(c, profile)` 与其函数；`deviceIndexPage` 去掉 `components.Text(c, "我的设备").FontSize(20).Bold()` 那一行（D1b）。

- [ ] **步骤 6：运行测试验证通过**

运行：`go test ./internal/app/ -v`
预期：PASS。特别确认 `TestToolbarOnlyOnTheIndexPage`、`TestDeviceIndexPageListsDevices`、`TestBackReturnsToPreviousDevice`、`TestMissingDevicePage` 仍绿。

- [ ] **步骤 7：Commit**

```bash
git add internal/app/detail.go internal/app/state.go internal/app/shell.go internal/app/app_test.go docs/CHANGELOG.md
git commit -m "fix(app): put the breadcrumb in the shell title bar

The breadcrumb was drawn by ContentView, which did not inset itself below
the shell's floating header, so every click on 「我的设备」landed on the
window-drag strip and the device page could not go back. The path now
lives in ShellOptions.ContentTitle, where the shell can never swallow the
click, and the body gives up the strip it was painting under."
```
`docs/CHANGELOG.md` 的 `### 修复` 加：`设备页的「我的设备」面包屑移到窗口标题栏内，可以点回设备列表`；`### 变更` 加：`设备列表页不再重复显示「我的设备」标题（改由窗口标题栏显示）`。

---

## 任务 2：日志页改成只读文本缓冲，并承载任务行（用户问题 2、3）

**文件：**
- 新建：`internal/app/logs.go`、`internal/app/logs_test.go`
- 修改：`internal/app/detail.go`（删旧的 `logs`，`DetailTab == 2` 改调 `vm.logPage`）、`internal/app/state.go`（加 `logStates` 字段）
- 测试：`internal/app/logs_test.go`

- [ ] **步骤 1：先探明控件事实（探针测试，跑一次即可，跑完删掉）**

mygo 的 `Element.TrackScroll` 文档写明它只接管**滚动容器**（`Scroll`、`ScrollHorizontal`、`ScrollBoth`、`List`），而 `ui.TextAreaBuffer` 是带 `flagScrollY` 的编辑器，**大概率不响应** `TrackScroll`。这决定日志区怎么写，先花两分钟确认，别猜：

```go
func TestProbeTextAreaBufferScroll(t *testing.T) {
	buf := ui.NewTextBuffer(strings.Repeat("line\n", 200))
	var scroll ui.ScrollState
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(10).Children(func() {
			components.TextBoxBuffer(c, buf, components.TextBoxOptions{Multiline: true, ReadOnly: true}).
				FillWidth().Grow(1).TrackScroll(&scroll)
		})
	}, 400, 200)
	tt.Frame()
	t.Logf("after frame 1: y=%.1f max=%.1f", scroll.Y, scroll.MaxY)
	scroll.Y = math.MaxFloat32
	tt.Frame()
	t.Logf("after follow:  y=%.1f max=%.1f", scroll.Y, scroll.MaxY)
}
```

运行：`go test ./internal/app/ -run TestProbeTextAreaBufferScroll -v`，把 `t.Logf` 的实际值记进本任务的提交信息。
- `MaxY > 0` 且设 `Y = math.MaxFloat32` 后 `Y` 落到 `MaxY`：文本区自己就是滚动容器，`TrackScroll` 直接挂它。
- `MaxY == 0`（预期情况）：改成 **外层 `ui.Scroll` + 让文本区铺满内容**——`components.TextBoxBuffer(...).Lines(n, n)`（`n = state.buffer.LineCount()`，最少 1），外层 `ui.Scroll(c).FillWidth().Grow(1).TrackScroll(&state.scroll)`。`logState.scroll` 与下面的测试不受影响（两种实现都靠 `scroll.Y/MaxY` 判断跟随），只是滚动条属于外层容器。

- [ ] **步骤 2：写失败的测试（时间线与日志页）**

```go
// internal/app/logs_test.go
package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

func TestDeviceTimelineMergesLogsAndTasksInOrder(t *testing.T) {
	deviceID := uuid.New()
	base := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	session := core.Session{Logs: []core.SessionLog{
		{At: base.Add(2 * time.Second), Text: "通道已就绪"},
		{At: base.Add(4 * time.Second), Text: "adb 已连接"},
	}}
	tasks := []core.Task{{
		ID: uuid.New(), DeviceID: deviceID, Title: "adb shell cat /proc/meminfo",
		Started: base.Add(3 * time.Second), State: core.TaskSucceeded,
	}}
	got := logText(deviceTimeline(session, tasks))
	want := strings.Join([]string{
		"12:00:02 通道已就绪",
		"12:00:03 adb shell cat /proc/meminfo · 已完成",
		"12:00:04 adb 已连接",
	}, "\n")
	if got != want {
		t.Fatalf("log text = %q, want %q", got, want)
	}
}

func TestDeviceTimelineIgnoresOtherDevices(t *testing.T) {
	mine, other := uuid.New(), uuid.New()
	tasks := []core.Task{
		{ID: uuid.New(), DeviceID: mine, Title: "上传"},
		{ID: uuid.New(), DeviceID: other, Title: "别的设备的任务"},
	}
	text := logText(deviceTimeline(core.Session{}, tasks))
	if !strings.Contains(text, "上传") || strings.Contains(text, "别的设备") {
		t.Fatalf("the timeline mixed devices up: %q", text)
	}
}

func TestLogTextKeepsEveryLineOfAFullWindow(t *testing.T) {
	session := core.Session{}
	for i := 0; i < 200; i++ {
		session.Logs = append(session.Logs, core.SessionLog{Text: "第" + itoa(i) + "行"})
	}
	tasks := make([]core.Task, 50)
	for i := range tasks {
		tasks[i] = core.Task{DeviceID: uuid.New(), Title: "任务" + itoa(i)}
	}
	if lines := strings.Split(logText(deviceTimeline(session, tasks)), "\n"); len(lines) != 250 {
		t.Fatalf("log text has %d lines, want 250", len(lines))
	}
}

func TestLogPageHasAnEmptyState(t *testing.T) {
	vm := newTestViewModel(t)
	vm.DetailTab = 2
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("暂无通道日志与任务") {
		t.Fatalf("no empty state on the log page; texts = %v", tt.Texts())
	}
}

func TestLogPageKeepsBuffersSeparatePerDevice(t *testing.T) {
	vm := newTestViewModel(t)
	first := vm.Manager.Profiles()[0]
	secondID, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{Name: "第二台", Address: testAddress, RemotePort: 5555})
	if err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	vm.logPageText(first.ID, core.Session{Logs: []core.SessionLog{{Text: "第一台的日志"}}})
	vm.logPageText(secondID, core.Session{Logs: []core.SessionLog{{Text: "第二台的日志"}}})
	if got := vm.logState(first.ID).text; strings.Contains(got, "第二台") {
		t.Fatalf("device A's buffer holds device B's lines: %q", got)
	}
	if got := vm.logState(secondID).text; strings.Contains(got, "第一台") {
		t.Fatalf("device B's buffer holds device A's lines: %q", got)
	}
}

func TestLogPageStopsFollowingAfterScrollUp(t *testing.T) {
	vm := newTestViewModel(t)
	id := vm.Manager.Profiles()[0].ID
	state := vm.logState(id)
	state.scroll.Y, state.scroll.MaxY = 0, 400
	vm.logPageText(id, core.Session{Logs: []core.SessionLog{{Text: "新的一行"}}})
	if state.follow {
		t.Fatal("scrolling up must stop the log page from following the tail")
	}
	state.scroll.Y = 396
	vm.logPageText(id, core.Session{Logs: []core.SessionLog{{Text: "又一行"}}})
	if !state.follow {
		t.Fatal("scrolling back to the bottom must resume following")
	}
}
```

- [ ] **步骤 3：运行测试验证失败**

运行：`go test ./internal/app/ -run "Timeline|LogPage|LogText" -v`
预期：FAIL，`undefined: deviceTimeline`（以及 `logText`、`vm.logState`、`vm.logPageText`）。

- [ ] **步骤 4：实现 `internal/app/logs.go`**

```go
// logLine is one row of the device log page.
type logLine struct {
	At   time.Time
	Text string
}

// deviceTimeline merges the channel log with this device's tasks, oldest
// first, so one page answers "what happened to this device".
func deviceTimeline(session core.Session, tasks []core.Task) []logLine

// logText renders a timeline one entry per line, each prefixed with its
// wall-clock time: "15:04:05 通道已就绪".
func logText(lines []logLine) string

// logState is one device's read-only log buffer plus its scroll position.
type logState struct {
	buffer *ui.TextBuffer
	scroll ui.ScrollState
	text   string // what buffer currently holds
	follow bool   // was at the tail last frame
}

// logState returns the (lazily created) state of one device's log page.
func (vm *ViewModel) logState(id uuid.UUID) *logState

// logPageText refreshes one device's buffer from a session snapshot and keeps
// the follow-the-tail decision. It is the only place that writes the buffer.
func (vm *ViewModel) logPageText(id uuid.UUID, session core.Session)

// logPage renders the log tab: a header line, the optional cancel action and
// the read-only text area.
func (vm *ViewModel) logPage(c *ui.Context, profile core.Profile, session core.Session)
```

约束：
- 时间线文本行：通道日志 `"15:04:05 " + entry.Text`；任务 `"15:04:05 " + task.Title + " · " + task.State.Label()`；同样时间按"日志在前、任务在后"稳定排序。
- `logPageText` 的 `follow` 判定用上一帧的 `scroll.Y/MaxY`：`follow = 首帧 || scroll.Y >= scroll.MaxY-4`；随后 `if follow { scroll.Y = math.MaxFloat32 }`。
- 文本变化才 `buffer.Set(text)`（`state.text != text` 时），并更新 `state.text`。
- 空时间线：`logPage` 渲染 `components.Text(c, "暂无通道日志与任务").FontSize(11)`，不渲染文本区。
- 非空：`components.TextBoxBuffer(c, state.buffer, components.TextBoxOptions{Multiline: true, ReadOnly: true}).FillWidth().Label("通道日志与任务")`，滚动按探针结论挂上（`TrackScroll(&state.scroll)` 或外层 `ui.Scroll`）。

- [ ] **步骤 5：接到设备页上**

`internal/app/detail.go`：`switch vm.DetailTab` 的 `case 2:` 从 `vm.logs(c, session)` 改成 `vm.logPage(c, profile, session)`；删除 `func (vm *ViewModel) logs(...)`。
`internal/app/state.go`：`ViewModel` 加字段 `logStates map[uuid.UUID]*logState`，在 `NewViewModel` 里 `logStates: map[uuid.UUID]*logState{}`。

- [ ] **步骤 6：运行测试验证通过**

运行：`go test ./internal/app/ -run "Timeline|LogPage|LogText|DevicePage" -v`
预期：PASS。

- [ ] **步骤 7：Commit**

```bash
git add internal/app/logs.go internal/app/logs_test.go internal/app/detail.go internal/app/state.go docs/CHANGELOG.md
git commit -m "feat(app): give the log tab a read-only scrolling timeline

Channel logs and this device's tasks are one story; the log tab now shows
them merged in one selectable, read-only text area that follows the tail
until the reader scrolls up. Probe result for TrackScroll on the text
area: <填探针输出>."
```
`docs/CHANGELOG.md` 的 `### 新增` 加：`设备页「日志」改为只读文本视图，可滚动、可选中复制，并按时间合并通道日志与设备任务`。

---

## 任务 3：删掉固定的任务面板，取消入口下沉（用户问题 2 的另一半）

**文件：**
- 删除：`internal/app/tasks.go`
- 修改：`internal/app/detail.go`（`ContentView` 去掉 `vm.TaskPanel(c)`）、`internal/app/logs.go`（顶行「取消全部」）
- 测试：`internal/app/app_test.go`、`internal/app/logs_test.go`

- [ ] **步骤 1：写失败的测试**

```go
// app_test.go：替换掉 TestTaskPanelSitsBelowThePage
func TestTaskPanelIsGone(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)
	taskID, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "adb cat", []string{"cat", "/proc/meminfo"}, time.Minute)
	if err != nil {
		t.Fatalf("BeginADBTask() error: %v", err)
	}
	vm.Manager.WaitTask(taskID)

	vm.OpenDevice(profile.ID)
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("设备任务") {
		t.Fatalf("the fixed task panel is still there; texts = %v", tt.Texts())
	}
}

// logs_test.go
func TestLogPageCancelsTheRunningTask(t *testing.T) {
	vm := newBlockingViewModel(t, &blockingRunner{release: make(chan struct{})})
	profile := vm.Manager.Profiles()[0]
	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer vm.Manager.Disconnect(context.Background(), profile.ID)
	taskID, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "adb install", []string{"install", "-r", "app.apk"}, time.Minute)
	if err != nil {
		t.Fatalf("BeginADBTask() error: %v", err)
	}
	vm.DetailTab = 2
	tt := ui.NewTester(vm.ContentView, 900, 700)
	if !tt.HasText("运行中 1 个任务") {
		t.Fatalf("the log page does not show the running task; texts = %v", tt.Texts())
	}
	if err := tt.Click("取消全部"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	vm.Manager.WaitTask(taskID)
	if got := vm.Manager.Tasks()[0].State; got != core.TaskCancelled {
		t.Fatalf("task state = %v, want %v", got, core.TaskCancelled)
	}
}

func TestRunningTasksFiltersByDeviceAndState(t *testing.T) {
	mine, other := uuid.New(), uuid.New()
	tasks := []core.Task{
		{ID: uuid.New(), DeviceID: mine, State: core.TaskRunning},
		{ID: uuid.New(), DeviceID: mine, State: core.TaskSucceeded},
		{ID: uuid.New(), DeviceID: other, State: core.TaskRunning},
	}
	if got := runningTasks(tasks, mine); len(got) != 1 {
		t.Fatalf("runningTasks() = %+v, want only this device's running task", got)
	}
}
```

配套的两个测试假实现（都放在 `_test.go` 里，`package app`）：

```go
// blockingRunner keeps every command running until release is closed or the
// task is cancelled.
type blockingRunner struct{ release chan struct{} }

func (r *blockingRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("Start is not used in view tests")
}

func (r *blockingRunner) Run(ctx context.Context, _ core.Command, _ func(string)) (core.Result, error) {
	select {
	case <-r.release:
		return core.Result{}, nil
	case <-ctx.Done():
		return core.Result{}, core.Error("已取消")
	}
}
```

`newBlockingViewModel(t, runner)` 与既有的 `newRunnerViewModel` 同构，只是 `Runner` 换成传进来的 `core.Runner`；顺手把 `newRunnerViewModel` 重构成接收 `func(core.Command) (core.Result, error)` 与 `core.Runner` 的小工具，避免第三份样板。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app/ -run "TaskPanelIsGone|CancelsTheRunningTask|RunningTasksFilters" -v`
预期：FAIL —— `the fixed task panel is still there`；`the log page does not show the running task`；`undefined: blockingRunner`。

- [ ] **步骤 3：删面板，把取消入口搬到日志页顶行**

1. `git rm internal/app/tasks.go`。
2. `detail.go` 的 `ContentView`：`ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), 0, 0, 0).Children(func() { ui.Column(c).FillWidth().Grow(1).Children(func() { vm.routedPage(c) }) })`。
3. `logs.go` 的 `logPage` 顶行：

```go
ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
	running := runningTasks(vm.Manager.Tasks(), profile.ID)
	components.Text(c, "通道日志与任务").FontSize(13).Bold()
	if len(running) > 0 {
		components.Text(c, "运行中 "+itoa(len(running))+" 个任务").FontSize(11)
		if components.Button(c, "取消全部", components.ButtonOutline).Clicked() {
			for _, task := range running {
				vm.Manager.CancelTask(task.ID)
			}
		}
	}
})
```

`runningTasks(tasks []core.Task, id uuid.UUID) []core.Task` 是纯过滤函数，一起单测（见上面的 `TestRunningTasksFiltersByDeviceAndState`）。

4. **不要**在 `SidebarFooter` 加运行中任务计数（D3）。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app/ -v`
预期：PASS（含 `TestLogPageCancelsTheRunningTask`）。

- [ ] **步骤 5：Commit**

```bash
git add -A internal/app docs/CHANGELOG.md
git commit -m "refactor(app): move device tasks into the log page

The fixed panel was a second full-height band under the page and painted
over inline dialogs. Tasks now live in the device log timeline, with one
cancel action above it; the sidebar deliberately gains no counter."
```
`docs/CHANGELOG.md` 的 `### 变更` 加：`设备任务不再单独显示在页面底部的面板里，改为并入该设备的「日志」页；运行中任务改为在日志页顶部取消`。

---

## 任务 4：底栏去掉「GUI N 条通道」（用户问题 4）

**文件：**
- 修改：`internal/app/state.go`（`SidebarFooter`）
- 测试：`internal/app/app_test.go`

- [ ] **步骤 1：写失败的测试**

```go
func TestSidebarFooterDropsTheChannelCount(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 420)
	if tt.HasText("条通道") {
		t.Fatalf("the footer still counts channels; texts = %v", tt.Texts())
	}
	if !tt.HasText("scrcpy") {
		t.Fatalf("the footer lost the tool health rows; texts = %v", tt.Texts())
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app/ -run TestSidebarFooterDropsTheChannelCount -v`
预期：FAIL，`the footer still counts channels`。

- [ ] **步骤 3：删掉那一行**

删除 `SidebarFooter` 里的 `components.Text(c, "GUI "+itoa(vm.Manager.OwnedActiveCount())+" 条通道")…`。
`OwnedActiveCount` 保留在 core（`tray.go` 还在用它判断"还有活动会话"），不要删。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app/ -run "SidebarFooter" -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/state.go internal/app/app_test.go docs/CHANGELOG.md
git commit -m "refactor(app): drop the unexplained channel counter from the footer

It restated the device rows' status dots with a number nobody could act on."
```
`docs/CHANGELOG.md` 的 `### 移除` 加：`侧栏底部的「GUI N 条通道」计数（与设备列表的状态重复且无法操作）`。

---

## 任务 5：按钮层级，幽灵按钮退出页面级动作（用户问题 6）

> **这一项没有自动断言，是本计划唯一偏离 TDD 的地方。** 变体的差别是像素（`ButtonGhost` 的 `bg`/`border` 都是零值），`ui.Tester` 只能按文案定位标签盒子，拿不到按钮盒子，采样像素又会随字体渲染与缩放抖动。所以本任务的门禁是 `gofmt` / `go vet` / `go build` / `go test ./...` 全绿 + 「界面核对清单」在 `--demo` 模式下逐条看过。评审时可以否决这个例外。

**文件：**
- 修改：`internal/app/detail.go`（`devicePage`、`overview`、`toolbar`）、`internal/app/state.go`（`toolRow`）
- 文档：`docs/ui-and-console.md`（任务 7 统一写按钮层级表）
- 测试：无新增；既有 `TestEditEntryOpensTheEditor` 等不得回归

- [ ] **步骤 1：按表换变体**

| 位置 | 现在 | 改成 |
|---|---|---|
| 设备页「编辑」 | `ButtonGhost` | `ButtonOutline` |
| 设备页「保存到我的设备」 | `ButtonGhost` | `ButtonOutline` |
| 概览「复制 ADB 地址」/「复制 ADB 入口」 | `ButtonGhost` | `ButtonOutline` |
| 列表页工具栏「测试全部设备」「临时连接」「工具设置」 | `ButtonGhost` | `ButtonSecondary` |
| 列表页工具栏「添加设备」 | `ButtonGhost` | `ButtonPrimary` |
| 侧栏底栏工具行的「安装」 | `ButtonGhost` | `ButtonOutline` |
| `ContentView` 横幅的「关闭」 | `ButtonGhost` | `ButtonLink` |
| 标题栏面包屑的祖先项（`components.Breadcrumbs` 内部） | `ButtonGhost` | **不动**（它本身就是文字链接） |
| 对话框「取消」（紧挨 Primary「保存」） | `ButtonGhost` | **不动**（D4 的例外） |

- [ ] **步骤 2：在 `--demo` 模式下逐条核对**

运行：`go run ./cmd/tailcat-manager --demo`
核对清单（每条都要能用一句话说清"这个按钮会不会被当成正文"）：
1. 标题栏：面包屑「我的设备 › 设备名」看得见、点得动，且不像正文；
2. 设备页标题行：编辑 / 连接 / 断开 三个按钮的边界都看得见；
3. 概览底部两行：探测链路 / 刷新信息 / 复制入口 / 安装 APK / 上传 / 下载 / 屏幕控制 边界一致；
4. 列表页工具栏四个动作都在底上有色块或边框，「添加设备」最重；
5. 侧栏底栏 scrcpy 的「安装」不再像一行普通文字；
6. 横幅的「关闭」仍然轻，但悬停会出现下划线。

- [ ] **步骤 3：门禁**

运行：`gofmt -l . && go vet ./... && go test ./...`
预期：`gofmt -l .` 无输出；其余全绿。

- [ ] **步骤 4：Commit**

```bash
git add internal/app/detail.go internal/app/state.go docs/CHANGELOG.md
git commit -m "fix(app): stop using ghost buttons for page-level actions

A ghost button paints no background and no border, so 「编辑」and「复制 ADB
地址」read as body text. Ghost now means a link inside text (breadcrumbs)
or a dialog's secondary action; everything else is outline, secondary or
primary. No automated assertion: the variants differ only in pixels, and
the tester can locate label boxes, not button boxes."
```
`docs/CHANGELOG.md` 的 `### 修复` 加：`「编辑」「复制 ADB 地址」「安装」等按钮不再是无底色无边框的幽灵样式，可读性恢复`。

---

## 任务 6：DERP 中继分段切换 + 独立的中继历史（用户问题 7）

**文件：**
- 修改：`internal/core/config.go`（`RelayHistory` 字段 + 归一化）、`internal/core/session.go`（`RelayHistoryEntry`）、`internal/core/manager.go`（字段与访问器）、`internal/core/manager_devices.go`（补录 + `persistLocked`）
- 修改：`internal/app/editor.go`（分段 + 历史下拉）
- 测试：`internal/core/manager_devices_test.go`、`internal/core/config_test.go`、`internal/app/forms_test.go`、`internal/app/app_test.go`

- [ ] **步骤 1：写失败的 core 测试**

```go
// internal/core/config_test.go
func TestSaveWritesRelayHistoryAsAnEmptyArray(t *testing.T) {
	store := NewConfigStore(t.TempDir())
	if err := store.Save(Configuration{}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	// 与 relayPresets 一致：永远写 []，不写 null。
	if !strings.Contains(string(raw), `"relayHistory": []`) {
		t.Fatalf("configuration.json = %s", raw)
	}
}

func TestLoadAcceptsAConfigurationWithoutRelayHistory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "configuration.json"), []byte(`{"profiles":[],"relayPresets":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewConfigStore(dir).Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.RelayHistory) != 0 {
		t.Fatalf("relayHistory = %+v, want empty", cfg.RelayHistory)
	}
}

// internal/core/manager_devices_test.go
func TestSaveDeviceRecordsRelayHistoryOnce(t *testing.T) {
	// newManagerFixture 没有注入 ConfigStore 时只改内存；持久化另有用例。
	f := newManagerFixture(t)
	if _, err := f.m.SaveDevice(SaveDeviceRequest{Name: "a", Address: sampleAddress, RemotePort: 5555, RelaySource: "https://relay.example.com/map.json"}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	if _, err := f.m.SaveDevice(SaveDeviceRequest{Name: "b", Address: sampleAddress, RemotePort: 5555, RelaySource: "https://relay.example.com/map.json"}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	history := f.m.RelayHistory()
	if len(history) != 1 {
		t.Fatalf("history = %+v, want one entry for a repeated source", history)
	}
	if history[0].Name != "relay.example.com" {
		t.Fatalf("history name = %q, want the host", history[0].Name)
	}
	if history[0].LastUsed.IsZero() {
		t.Fatal("history entry has no LastUsed")
	}
}

func TestOfficialRelayIsNotRecordedInHistory(t *testing.T) {
	f := newManagerFixture(t)
	if _, err := f.m.SaveDevice(SaveDeviceRequest{Name: "a", Address: sampleAddress, RemotePort: 5555}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	if got := len(f.m.RelayHistory()); got != 0 {
		t.Fatalf("history = %d entries, want the official map skipped", got)
	}
}

func TestRelayHistoryKeepsOnlyTheNewestEntries(t *testing.T) {
	f := newManagerFixture(t)
	for i := 0; i < maxRelayHistory+3; i++ {
		source := "https://relay" + itoaSmall(i) + ".example.com/map.json"
		if _, err := f.m.SaveDevice(SaveDeviceRequest{Name: "d" + itoaSmall(i), Address: sampleAddress, RemotePort: 5555, RelaySource: source}); err != nil {
			t.Fatalf("SaveDevice() error: %v", err)
		}
	}
	history := f.m.RelayHistory()
	if len(history) != maxRelayHistory {
		t.Fatalf("history = %d entries, want %d", len(history), maxRelayHistory)
	}
	if history[0].Source != "https://relay" + itoaSmall(maxRelayHistory+2) + ".example.com/map.json" {
		t.Fatalf("history[0] = %+v, want the most recent source first", history[0])
	}
}

func TestRelayHistorySurvivesReload(t *testing.T) {
	// NewManager 不读 ConfigStore（它拿的是 ManagerOptions.Configuration），
	// 所以“重开能读回”直接验文件：保存后 Load 出同样的历史。
	store := NewConfigStore(t.TempDir())
	f := newManagerFixture(t, func(o *ManagerOptions) { o.Config = store })
	if _, err := f.m.SaveDevice(SaveDeviceRequest{Name: "a", Address: sampleAddress, RemotePort: 5555, RelaySource: "https://relay.example.com/map.json"}); err != nil {
		t.Fatalf("SaveDevice() error: %v", err)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(saved.RelayHistory) != 1 || saved.RelayHistory[0].Source != "https://relay.example.com/map.json" {
		t.Fatalf("relayHistory after reload = %+v", saved.RelayHistory)
	}
}

func TestRelayHistoryNameNeverCollides(t *testing.T) {
	f := newManagerFixture(t)
	for _, source := range []string{"https://relay.example.com/a.json", "https://relay.example.com/b.json"} {
		if _, err := f.m.SaveDevice(SaveDeviceRequest{Name: uuid.NewString()[:8], Address: sampleAddress, RemotePort: 5555, RelaySource: source}); err != nil {
			t.Fatalf("SaveDevice() error: %v", err)
		}
	}
	names := map[string]bool{}
	for _, entry := range f.m.RelayHistory() {
		if names[entry.Name] {
			t.Fatalf("duplicate history name %q in %+v", entry.Name, f.m.RelayHistory())
		}
		names[entry.Name] = true
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core/ -run "RelayHistory|OfficialRelay" -v`
预期：FAIL，`f.m.RelayHistory undefined`（以及 `maxRelayHistory undefined`）。

- [ ] **步骤 3：在 `internal/core` 里加这张表**

`session.go`（紧跟 `RelayPreset` 之后）：

```go
// RelayHistoryEntry is one custom DERP source the user has used before. It is
// what the editor offers as 「之前用过的中继」; a management page comes later.
type RelayHistoryEntry struct {
	Source   string    `json:"source"`
	// Name is derived from the source when it is first recorded, and is only
	// a label: Source is what goes back into a device.
	Name     string    `json:"name"`
	LastUsed time.Time `json:"lastUsed"`
}
```

`config.go`：`Configuration` 加 `RelayHistory []RelayHistoryEntry `json:"relayHistory"``；`ConfigStore.Save` 里跟 `RelayPresets` 一起把 nil 归一成空切片（`Load` 不需要改：老配置里没这个字段就是 nil，`len(nil) == 0`）。

`manager.go`：`Manager` 加 `relayHistory []RelayHistoryEntry`（`NewManager` 里 `relayHistory: opts.Configuration.RelayHistory`），访问器：

```go
// RelayHistory returns the custom DERP sources seen so far, newest first.
func (m *Manager) RelayHistory() []RelayHistoryEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RelayHistoryEntry(nil), m.relayHistory...)
}
```

`manager_devices.go`：`persistLocked` 的 `Configuration{...}` 里带上 `RelayHistory: m.relayHistory`；`SaveDevice` 在写盘前补录：

```go
// maxRelayHistory bounds the editor's history menu. Oldest last-used entries
// fall off first.
const maxRelayHistory = 8

// relayHistoryName derives a label from a DERP source: the host for a URL and
// a short digest for inline JSON. A taken name gets a " (2)" style suffix.
func relayHistoryName(source string, taken map[string]bool) string

// rememberRelayLocked records a custom DERP source for the editor's history.
// It dedupes by the canonical Source, keeps the newest first and never fails
// the device save.
func (m *Manager) rememberRelayLocked(source string)
```

要求：跳过 `RelayOfficial`（空来源）；按规范化后的 `relay.Source` 去重（`NormalizeRelay` 已把 JSON 规范化，字符串比较即可）；更新已有条目的 `LastUsed` 并移到最前；总数超过 `maxRelayHistory` 时丢最旧的；不返回错误、不阻断保存。

- [ ] **步骤 4：运行 core 测试验证通过**

运行：`go test ./internal/core/ -v`
预期：PASS（既有 `TestSaveDeviceStoresRelaySource`、`TestSaveRelayPresetRules`、配置往返用例不得回归）。

- [ ] **步骤 5：写失败的 app 测试**

```go
// forms_test.go：base 去掉 RelaySelection，改用 RelayTab 的零值（0 = 官方默认）
func TestDeviceEditorValidation(t *testing.T) {
	base := EditorInput{Name: "E245", Address: testAddress, RemotePort: "5555"}
	...
	{"custom relay without source", func(in *EditorInput) { in.RelayTab = 1 }, "DERP JSON"},
}

func TestEditorRelayTabSwitchesToCustom(t *testing.T) {
	vm := newTestViewModel(t)
	vm.openEditor(&EditorRequest{})
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("DERP JSON 链接或内容") {
		t.Fatalf("the custom relay field shows before it is chosen; texts = %v", tt.Texts())
	}
	if err := tt.Click("自定义"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if vm.EditorDraft.RelayTab != 1 || !tt.HasText("DERP JSON 链接或内容") {
		t.Fatalf("the segmented switch did not open the custom relay field; tab = %d", vm.EditorDraft.RelayTab)
	}
}

func TestRelayHistoryOptionsListSavedSources(t *testing.T) {
	entries := []core.RelayHistoryEntry{
		{Name: "内网中继", Source: "https://relay.example.com/map.json"},
		{Name: "内联 JSON 4f2a9c1b", Source: `{"Regions":{}}`},
	}
	options := relayHistoryOptions(entries)
	if len(options) != 2 || options[0].Label != "内网中继" || options[0].Value != "https://relay.example.com/map.json" {
		t.Fatalf("options = %+v", options)
	}
}
```

- [ ] **步骤 6：运行 app 测试验证失败**

运行：`go test ./internal/app/ -run "Editor|RelayHistory" -v`
预期：FAIL，`unknown field RelayTab in struct literal`、`undefined: relayHistoryOptions`。

- [ ] **步骤 7：改 `internal/app/editor.go`**

1. `EditorInput`：删 `RelaySelection`，加 `RelayTab int`（`0` 官方默认、`1` 自定义）与 `RelayPick string`（历史下拉当前选中项的 `Source`），保留 `RelaySource`；`ViewModel` 删 `EditorPresetName`。
2. `Validate()`：`RelayTab == 0` → `out.RelaySource = ""`；`RelayTab == 1` → 要求 `RelaySource` 非空，否则 `core.Error("请填写 DERP JSON 链接或内容，或选择默认官方节点")`。
3. `openEditor`：`draft.RelayTab` 由 `profile.RelaySource != ""` 决定；`draft.RelayPick` 取历史里 `Source` 相同那条的 `Source`（找不到就留空）；删掉 `RelaySelection`/`EditorPresetName` 的赋值与 `relaySelectionLabel`。
4. 视图（D6 的分段控件）：

```go
ui.Column(c).Gap(3).Children(func() {
	components.Text(c, "DERP 中继").FontSize(11)
	components.Tabs(c, &vm.EditorDraft.RelayTab, components.TabsSegmented, "官方默认", "自定义")
	if vm.EditorDraft.RelayTab == 1 {
		vm.relaySourceFields(c)
	}
})
```

5. `relaySourceFields`：历史下拉 + 输入框

```go
// relayHistoryOptions turns the recorded sources into the editor's history
// menu. Values are the canonical sources, so picking one fills the field.
func relayHistoryOptions(entries []core.RelayHistoryEntry) []components.NativeSelectOption[string]

// relaySourceFields renders the history picker and the free-form source box.
func (vm *ViewModel) relaySourceFields(c *ui.Context) {
	if entries := vm.Manager.RelayHistory(); len(entries) > 0 {
		ui.Column(c).Gap(3).Children(func() {
			components.Text(c, "之前用过的中继").FontSize(11)
			pick := components.NativeSelect(c, &vm.EditorDraft.RelayPick, relayHistoryOptions(entries))
			if pick.Changed() {
				vm.EditorDraft.RelaySource = vm.EditorDraft.RelayPick
			}
			pick.Trigger.Label("之前用过的中继").FillWidth()
		})
	}
	field(c, "DERP JSON 链接或内容", &vm.EditorDraft.RelaySource, false)
}
```

- [ ] **步骤 8：运行测试验证通过**

运行：`go test ./internal/app/ ./internal/core/ -v`
预期：PASS。

- [ ] **步骤 9：Commit**

```bash
git add internal/app/editor.go internal/app/forms_test.go internal/app/app_test.go internal/core docs/CHANGELOG.md
git commit -m "feat: segment the relay switch and remember custom DERP sources

The two ghost buttons read as a line of text and the source the user typed
was gone after saving. relayHistory is its own configuration table next to
relayPresets, so the editor can offer what was used before without touching
the user-named presets; a management page comes later."
```
`docs/CHANGELOG.md` 的 `### 新增` 加：`设备编辑器会记住用过的自定义 DERP 中继（保存在本地配置里），「自定义中继」可直接从历史里选`；`### 变更` 加：`中继选择改为「官方默认 / 自定义」分段切换`。

---

## 任务 7：文档与变更记录收口

**文件：**
- 修改：`docs/ui-and-console.md`（§2.1 槽位表、§2.2 内容区段数、§3 路由与导航、§4 侧栏与底栏、§6.2 提示、§10 陷阱、§12 文件索引）
- 修改：`docs/CHANGELOG.md`（补齐上面 6 个任务的条目，确认无重复、无空类别）

- [ ] **步骤 1：按实际实现改文档**

要点：
- §2.1 槽位表补两行：①`ContentTitle` 是面包屑的家，**不要**把导航画在 `Content` 里；②`Content` 必须自己用 `HeaderHeight` 上内边距，否则标题行与操作按钮落在标题栏拖拽区（本轮事故）。
- §2.2「内容区两段」改成一段：路由页占满；任务面板一节改成"设备页「日志」标签页承载通道日志与任务"。
- §3.2 导航入口改成两条：标题栏面包屑（`vm.ContentTitle`）与侧栏点击；删掉"面包屑在设备页顶部"的说法。
- §4 侧栏：删掉通道数；写明底栏只有工具健康状态与一行提示。列表页正文不再有 h1。
- §6.2 提示表加一行：中继历史补录失败**不弹提示**（它不影响保存）。
- §10 陷阱清单加三行：①`ButtonGhost` 用在页面级动作上 → 按钮读成正文，用 `ButtonOutline`；②`ui.TextAreaBuffer` 每帧 `Set` 会丢选区 → 只在文本变化时写；③`TrackScroll` 只接滚动容器（`Scroll`/`List`），挂在 `TextAreaBuffer` 上不会生效。
- §12 文件索引：`internal/app/tasks.go` 换成 `internal/app/logs.go`，补 `internal/app/state.go` 的 `ContentTitle` 与 `internal/core/config.go` 的 `relayHistory`。

- [ ] **步骤 2：门禁**

运行：`gofmt -l . && go vet ./... && go test ./... && go test -race ./internal/core ./internal/app`
预期：全绿，`gofmt -l .` 无输出。

- [ ] **步骤 3：Commit**

```bash
git add docs
git commit -m "docs: record the second round of interface feedback"
```

---

## 自检

**1. 规格覆盖度**：用户 6 条 → 任务映射：1→T1；2→T2+T3；3→T2；4→T4；6→T5；7→T6。三个已裁决的设计问题（面包屑进 `ContentTitle`、不要底栏计数、独立 `relayHistory`）分别落在 D1/D1b、D3、D5 与对应任务的代码步骤里。

**2. 步骤扫描**：每个测试步骤给出了测试名与断言；每个实现步骤给出了签名、文件与文案原文。唯一没有测试的步骤是 T5 的变体替换，已在任务开头写明理由与门禁。

**3. 类型一致性**：`deviceIDFromPath(path) (uuid.UUID, bool)`、`ViewModel.ContentTitle(c)`、`logState{buffer,scroll,text,follow}`、`logState(id)`、`logPageText(id,session)`、`runningTasks(tasks,id)`、`relayHistoryOptions(entries)`、`Manager.RelayHistory()`、`Manager.rememberRelayLocked(source)`、`relayHistoryName(source,taken)`、`maxRelayHistory`、`core.RelayHistoryEntry{Source,Name,LastUsed}`、`EditorInput.RelayTab/RelayPick` 在全部任务里同名同签名。`deviceTimeline` 只接收 `core.Session` 与 `[]core.Task`（纯函数）。

**4. 审查重点**：见上表，8 条都有对应测试，分别落在 T1、T1、T2、T2、T2、T2、T6、T6。

**5. 比例**：本计划比它描述的代码长，是因为要把"为什么"（根因、被否决的替代方案、用户裁决）留在文档里供评审否决；实现体量与 6 条界面反馈相当。

---

## 后续（本轮不做，避免范围失控）

- **中继管理页**（用户明确说"后续单独进行页面维护"）：管理 `RelayHistory`（删单条、清空、改名）与 `RelayPresets`，路由 `/settings/relay`。
- **日志历史**：`Recorder.RecentTasks` 与历史库里的日志还没有浏览界面，等 `2026-11-16-remaining-work.md` 的 P3。
- **日志选区不被新行重置**：现在文本变化就 `buffer.Set`，会丢选区；真要修需要增量 `Replace` + 保留 caret 位置，等有用户反馈再做。
- **全局任务总览**：面板去掉后，跨设备的任务只能逐台看；按 D3 的裁决不加底栏计数，若以后觉得找不到，再单独提一个入口（不要偷偷加回底栏）。
