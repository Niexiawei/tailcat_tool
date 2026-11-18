# 表格统一为 myGo `ui.Table`（含资源页排序与表格边框）实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）跟踪进度。
>
> **前置：** 无。当前 `main` 上所有表格仍是 onevra `components.Table` / `components.TableVirtualized`。

**目标：** 把仓库里全部 6 处表格（资源页两组、文件页、缓存管理页、DERP 节点页、DERP 详情页、日志页/设备日志标签、连通测试对话框）从 onevra 的表格组件换成 myGo 内置的 `ui.Table`；顺带修掉「列宽调宽后表头/调整手柄滚出可视区、再也抓不到」的问题（`ui.Table` 的表头跟随内容横向滚动）；给资源页「应用 / 进程」两个视图加上列排序，默认按**应用名称**升序；所有表格统一加 1 DIP 圆角边框。

**架构：** 新增 `internal/app/table.go` 作为**唯一**造表入口：`tableCard` 负责外框（`Border(1, theme.Border)` + `Radius` + `Clip`）、高度策略（`Grow` 或按行数定高）与无障碍标签，内部只调 `ui.Table`。排序不由组件代替：`ui.Table` 只把用户点的列写进 `ui.ListState.Sort`，行序由页面在**建表之前**用自己的纯函数算好（`sortApps` / `sortProcessRows`），因此排序规则可单测、可解释。行级交互（资源页展开、文件页进入目录、日志页打开任务输出）一律走 `ui.ListState.Selected` + `Table(...).Changed()`：`ui.Table` 没有 onevra 的 `TableRow.Clicked()`。资源页分组的展开改成行首的箭头按钮（`components.IconButton`），因为把 Selected 每次重置成 -1 会毁掉键盘上下键。

**技术栈：** Go 1.27.3、myGo `ui.Table` / `ui.ListState` / `ui.SortOrder` / `ui.NewTester`、onevra `components`（Text / IconButton / Dialog / Tooltip 等仍用，**表格除外**）、`lucide` 图标。

**规格：** 用户 2026-11-18 的口头需求（`components.Table` 列宽调大后调整抓手滚出可视区 → 统一换成 `ui.Table`；资源页应用/CPU/GPU/内存加排序、默认按应用名称、按应用与按进程同一规范；所有用到表格的页面加边框）。没有独立设计文档；本计划的取舍写进下面的「决策与代价」，落地后同步到 `docs/architecture.md` §15。

---

## 全局约束

- 纯 Go、禁止 cgo；**不新增任何依赖**（`ui.Table` 已随 `github.com/egoist/mygo v0.9.1` 在）。
- **表格只有一处来源**：任何 `ui.Table(` 只允许出现在 `internal/app/table.go`；页面一律调 `tableCard`。仓库里不再出现 `components.Table` / `components.TableRow` / `components.TableVirtualized` / `components.TableState` / `components.TableVirtualizedState`。
- **表格自己滚，不再套滚动容器**：`components.ScrollView`、`ui.Scroll` 不能包住表格（嵌套滚动就是「手柄滚出可视区」的成因）。
- 面向用户的中文文案，测试逐字断言；配色只从 `internal/app/theme.go` 取，不写字面量十六进制。
- 行式元素 `FillWidth()`；**禁止 `Fill()`**（会同时占满高度）。
- 每个提交：`gofmt -w`、`go vet ./...`、`go test ./...` 全绿；用户可见改动同一提交写进 `docs/CHANGELOG.md`。
- 分支 `feat/ui-table`；不要直接提交到 `main`。
- 不改 `components.TreeVirtualized`（文件页左侧文件夹树）——那是树，不是表格。

---

## 审查重点（Review Focus）

1. **表格不能嵌在另一个滚动容器里**：`components.Table` 自己不滚，全靠外层容器；我们套的是竖滚容器，列一调宽，最右列的调整抓手就被裁在可视区外且没有横向滚动能过去——这正是要修的病。`TestResourceTableEndsAboveThePageEdge`、`TestLogPageEndsAboveThePageEdge` 仍须绿。
2. **排序不得动「用户打开的分组」和筛选项**：采样每秒重排，用户展开的 app 必须还开着。`TestSortKeepsTheExpandedGroup`。
3. **行激活必须仍然一次点击生效**：文件页点目录进目录、日志页点任务行看输出，都是单点击；换成 `Selected`+`Changed()` 后不能变成需要双击。`TestFolderRowOpensTheFolder`、`TestLogsPageShowsTaskOutput`。
4. **日志行键必须继续用 `logRowKey`**：时间/来源/文本相同的两行在同一父节点下会撞 key，myGo 会 panic 或丢行。`TestLogsPageKeepsTwoIdenticalLinesApart`、`TestFollowKeepsAnIdenticalNewLine`。
5. **表格虚拟化后，屏幕外的行不再被构建**：任何「断言第 40 行存在」的测试必须在滚动之后断言。`TestCachePageScrollsLongLists`、`TestDerpDetailScrollsLongMaps` 要按这个重写，而不是删掉。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `internal/app/table.go`（新建） | `tableCard` / `tableRowsHeight` / `tableOptions`：唯一的建表入口（边框、高度、标签） |
| `internal/app/table_test.go`（新建） | 上表的测试（定高、边框规则、只有它能造表） |
| `internal/app/resources.go`（修改） | 两组表换 `tableCard`；箭头展开；`sortApps` / `sortProcessRows`；列 `Sortable` |
| `internal/app/resources_test.go`（修改） | 展开、排序、默认顺序、长列表布局 |
| `internal/app/files.go`（修改） | 右栏表换 `tableCard`（树不动）；行激活改 `Selected`+`Changed()` |
| `internal/app/appcache.go`（修改） | 表格占页面剩余高度（去掉页面级 `ui.Scroll`），加边框 |
| `internal/app/derp.go`（修改） | 预设表、区域表换 `tableCard`（表格内滚、定高上限） |
| `internal/app/logs.go`（修改） | 时间线换 `ui.Table`（`Key` / `Selected` / `Changed` / `jumpToTop` 保留） |
| `internal/app/reachability.go`（修改） | 对话框里的表换 `tableCard`（定高上限，表内滚） |
| `internal/app/state.go`（修改） | `CacheTable` / `DerpTable` / `DerpRegionTable` 类型改为 `ui.ListState` |
| `internal/app/compat/compat_test.go`（修改） | 去掉 onevra 表格断言，加 `ui.Table` / `ui.ListState.Sort` / `Changed` / `Submitted` |
| `internal/app/style_test.go`（修改） | 仓库级守卫：表格组件只剩 myGo 的 |
| `docs/CHANGELOG.md`、`docs/ui-and-console.md`、`docs/architecture.md`、`AGENTS.md`（修改） | 用户可见变更与规则 |
| `internal/app/screenshots_test.go`（修改） | 需要时补用例；改完界面要人眼看一遍 |

---

## 决策与代价

以下是我（实现者）先定下的取舍，评审时可以推翻，但推翻要付上面写的代价：

1. **展开分组改成行首箭头按钮**，不再「点整行展开」。代价：多一个可见控件、`TestProcessTableExpandsOnClick` 要重写；收益：键盘上下键仍能在表格里正常移动，选中态不被展开动作霸占。用「Selected 每次重置成 -1」实现整行点击会毁掉键盘导航，不采用。
2. **排序方向沿用 myGo 原生语义**：点一个没排过的列表头 = 升序，再点 = 降序。代价：CPU / 内存点一次是先看最小的；收益：与 myGo 的其它表格一致，不自造组件行为。
3. **「进程」列也排序**（按应用视图里按进程数、按进程视图里按进程名）。用户只点名了「应用 / CPU / GPU / 内存」四列，但一个表头若整片都不可点会读成坏了一半。
4. **数值列的并列项一律用应用名 / 进程名升序兜底**，降序只翻转主键。代价：写排序函数多几行；收益：同一应用的行永远聚在一起，「按应用排序」的语义成立。
5. **页面级滚动保留，表格在页面里定高内滚**（DERP 列表、DERP 详情、连通测试对话框）。代价：表格最多显示 8 行，多看要滚表格本身；收益：不重排这些页面的信息层级。资源页、缓存页、日志页、文件页的表格本来就是这一屏的主体，直接 `Grow(1)` 吃掉剩余高度。
6. **文件页的表格不再单独加边框**：整页浏览区已经是一个 1 DIP 的框（`files.go` 的 `ui.Column...Border`），再套一层会出现双线。这条例外在 `table.go` 用 `tableOptions.Unframed` 明写，并由 `TestOnlyFilesLeavesTheTableUnframed` 钉住。
7. **`Sortable` 只在资源页启用**：其余页面的表格没有排序需求，不顺手加，避免把「点了表头没反应」变成新的困惑。

---

## 任务 1：表格卡片 `tableCard`

**文件：**
- 创建：`internal/app/table.go`
- 创建：`internal/app/table_test.go`
- 修改：`internal/compat/compat_test.go`（加 myGo 表格断言）

**接口（签名与语义固定，函数体由实现者写）：**

```go
// tableOptions says how tableCard frames a table.
type tableOptions struct {
	// Label names the table for tests and assistive technology.
	Label string
	// Grow lets the card take the room its parent gives it: use it where the
	// table is the page's scroller.
	Grow bool
	// Rows is how many rows the card makes room for when Grow is false: the
	// table keeps its own scroll inside a page that scrolls around it.
	Rows int
	// Unframed omits the card's border, for a table already inside a frame.
	Unframed bool
}

// tableCard builds the one table of a page and returns it, so the page can read
// Changed and Submitted; the card around it draws the border.
func tableCard(c *ui.Context, state *ui.ListState, columns []ui.TableColumn, rows int, opts tableOptions, cell func(row, col int)) ui.Element

// tableRowsHeight is how tall a table with rows rows is when it does not grow:
// the header plus the rows, at most maxTableRows of them.
func tableRowsHeight(c *ui.Context, rows int) float32

// maxTableRows caps how many rows a fixed-height card makes room for.
const maxTableRows = 8
```

- 框：`ui.Column(c).FillWidth().Padding(1).Radius(t.Radius).Border(1, t.Border).Clip()`；`Unframed` 时省掉 `Padding/Radius/Border/Clip`。
- 表：`ui.Table(c, state, columns, rows, cell).FillWidth().Label(opts.Label)`。
- 高度**设在表上，不设在框上**（框会自己长到表 + 上下各 1 DIP padding + 1 DIP 边框）：`Grow` → 表 `.Grow(1).MinHeight(0)`、框 `.Grow(1).MinHeight(0)`；否则表 `.Height(tableRowsHeight(c, rows))`。
- `tableRowsHeight = t.Space(8) * (min(max(rows, 1), maxTableRows) + 1)`（`t.Space(8)` 正是 `ui.Table` 的表头高与行高下限）。

- [ ] **步骤 1：写失败测试**

`internal/app/table_test.go`：

```go
func TestTableCardSizesAShortTableForItsRows(t *testing.T) {
	var state ui.ListState
	var want float32
	tt := ui.NewTester(func(c *ui.Context) {
		want = tableRowsHeight(c, 3)
		tableCard(c, &state, []ui.TableColumn{{Title: "名称", ID: "name"}}, 3,
			tableOptions{Label: "探针表", Rows: 3},
			func(row, col int) { ui.Text(c, "行"+itoa(row)) })
	}, 600, 400)
	if rect, ok := tt.Find("探针表"); !ok || rect.H != want {
		t.Fatalf("table height = %v, want %v (found=%v)", rect.H, want, ok)
	}
}

func TestOnlyFilesLeavesTheTableUnframed(t *testing.T) {
	// scans internal/app/*.go: tableOptions{... Unframed: true} only in files.go
}
```

`internal/compat/compat_test.go` 追加（同文件已有 mygo/ui 断言块）：

```go
_ = ui.Table
_ func(*ui.Context, *ui.ListState, []ui.TableColumn, int, func(int, int)) ui.Element = ui.Table
_ = ui.SortOrder{}.Descending
_ = (*ui.ListState)(nil).Sort
_ = (*ui.ListState)(nil).Columns
_ = ui.Element.Changed
_ = ui.Element.Submitted
_ = ui.TableColumn{}.Sortable
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run 'TestTableCardSizesAShortTableForItsRows|TestOnlyFilesLeavesTheTableUnframed' -v`
预期：编译失败，`undefined: tableCard` / `undefined: tableRowsHeight`。

- [ ] **步骤 3：实现 `internal/app/table.go`**

按上面的签名与规则写；文件头注释说明「这是全仓唯一的建表入口，页面不要再直接写 `ui.Table`」。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/app -run 'TestTableCard|TestOnlyFiles' -v` → PASS
再跑：`go test ./internal/app ./internal/compat/ ./...` → 全绿（此时其它页面还没迁，仓库级守卫还没加）。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/table.go internal/app/table_test.go internal/compat/compat_test.go
git commit -m "feat: add the one table card every page frames its table with"
```

---

## 任务 2：资源页「按应用」表迁移 + 箭头展开

**文件：**
- 修改：`internal/app/resources.go`（`resourceState.tableApps` → `ui.ListState`；`processTable`）
- 修改：`internal/app/resources_test.go`（`TestProcessTableExpandsOnClick` 换成箭头版本）

- [ ] **步骤 1：写失败测试**

```go
func TestProcessGroupExpandsFromItsChevron(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("PID 100") {
		t.Fatal("the process rows are visible before expanding the group")
	}
	if err := tt.Click("展开 com.foo"); err != nil {
		t.Fatalf("no chevron on the group row: %v", err)
	}
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("PID 100") || !tt.HasText("com.foo:remote") {
		t.Fatalf("the expanded group does not show its processes; texts = %v", tt.Texts())
	}
	if err := tt.Click("收起 com.foo"); err != nil {
		t.Fatalf("the expanded group offers no way back: %v", err)
	}
	tt = ui.NewTester(vm.ContentView, 900, 900)
	if tt.HasText("PID 100") {
		t.Fatal("the group did not collapse")
	}
}

// Onevra's table is gone from this page: the sheet must stay a myGo one.
func TestResourcesPageUsesNoOnevraTable(t *testing.T) {
	assertNoOnevraTable(t, "resources.go")
}
```

`assertNoOnevraTable(t, file)` 是 `internal/app/table_test.go` 里的测试助手：读 `internal/app/<file>`，出现 `components.Table`、`components.TableRow`、`components.TableVirtualized`、`components.TableState`、`components.TableVirtualizedState` 任一即 `t.Fatalf`。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run 'TestProcessGroupExpandsFromItsChevron|TestResourcesPageUsesNoOnevraTable' -v`
预期：前者报「no chevron on the group row」，后者报 `resources.go` 仍在用 `components.Table`。

- [ ] **步骤 3：实现**

- `resourceState.tableApps components.TableState` → `ui.ListState`。
- `processTable` 里删掉 `components.ScrollView`，改为：
  ```go
  tableCard(c, &state.tableApps, processColumns(), len(rows), tableOptions{
      Label: "按应用分组的进程表", Grow: true,
  }, func(row, col int) { ... })
  ```
  行是「已展平的可见行」：先把 `filteredApp`（按需带上 `processes`）展平成
  `[]appRow{app, process *core.ProcessUsage, key string}`，`cell` 用它按下标取行；行 `key` 用 `app.Package` 与 `app.Package+"#"+PID`，与现在的 `TableRow` 键一致。
- `appCell` 的第 0 列开头加箭头（用同一个 `label := appLabel(app)` 做文案，测试逐字断言）：
  ```go
  icon, action := lucide.ChevronRight(), "展开 "+label
  if expanded {
      icon, action = lucide.ChevronDown(), "收起 "+label
  }
  if components.IconButton(c, icon, action, components.IconButtonCompact).Clicked() {
      state.expanded[app.Package] = !expanded
  }
  ```
- 筛选时仍自动展开每一条命中项（`expanded := filtering || state.expanded[...]`），箭头在筛选时显示为「收起」，但点击只改 `state.expanded`，不影响筛选。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/app -run 'TestProcess|TestResource|TestHardware' -v` → PASS
必须仍绿的既有用例：`TestProcessTableGroupsByApp`、`TestProcessTableShowsAppNameNotPackage`、`TestResourceTableEndsAboveThePageEdge`、`TestResourceViewSwitchesBetweenAppsAndProcesses`、`TestResourceProcessViewHasItsOwnTable`、`TestProcessTableSaysGPUUnavailable`。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/resources.go internal/app/resources_test.go internal/app/table_test.go
git commit -m "refactor: draw the grouped resource table with ui.Table and a chevron"
```

---

## 任务 3：资源页「按进程」表迁移

**文件：**
- 修改：`internal/app/resources.go`（`resourceState.tableProcesses` → `ui.ListState`；`processList`）
- 修改：`internal/app/resources_test.go`

- [ ] **步骤 1：写失败测试**

```go
func TestProcessViewScrollsItsOwnTable(t *testing.T) {
	vm := manyAppsViewModel(t, 40) // 既有助手：40 个应用
	profile := vm.Manager.Profiles()[0]
	vm.resourceStateFor(profile.ID).view = resourceViewProcesses
	const window = 700
	tt := ui.NewTester(vm.ContentView, 900, window)
	tt.Frame()
	if rect, ok := tt.Find("按进程列出的进程表"); !ok || rect.Y+rect.H > window-20 {
		t.Fatalf("the process table ends at %v, past the page's bottom padding (found=%v)", rect.Y+rect.H, ok)
	}
}
```

同样追加 `assertNoOnevraTable(t, "resources.go")` 已被任务 2 覆盖，这里只需行为断言。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run TestProcessViewScrollsItsOwnTable -v`
预期：FAIL（`按进程列出的进程表` 仍是 `components.ScrollView` 的标签，位置随页面滚动而不是停在页底）。

- [ ] **步骤 3：实现**

`processList` 去掉 `components.ScrollView`，改调：
```go
tableCard(c, &state.tableProcesses, processViewColumns(), len(rows), tableOptions{
    Label: "按进程列出的进程表", Grow: true,
}, func(row, col int) { vm.processRowCell(c, col, rows[row]) })
```
`processRows` 的 CPU 排序在这一步**保留**（任务 4 才改成按排序状态）。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/app -run 'TestProcessView|TestResourceProcessView' -v` → PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/app/resources.go internal/app/resources_test.go
git commit -m "refactor: draw the process view with ui.Table"
```

---

## 任务 4：资源页排序（两个视图，默认按应用名称）

**文件：**
- 修改：`internal/app/resources.go`（默认排序、`Sortable` 列、`sortApps`、`sortProcessRows`、`processRows` 不再自排）
- 修改：`internal/app/resources_test.go`（纯函数 + 页面集成）

**接口：**

```go
// sortApps orders grouped rows by order: application name ascending when it
// names no column of the application view.
func sortApps(rows []filteredApp, order ui.SortOrder)

// sortProcessRows orders flat rows by order: application name ascending when it
// names no column of the process view.
func sortProcessRows(rows []processRow, order ui.SortOrder)
```

列与键（`ui.TableColumn.ID` 即排序键）：

| 视图 | 列 ID | 比较 | 并列兜底 |
|---|---|---|---|
| 应用 | `app` | `strings.ToLower(appLabel)` | 包名 |
| 应用 | `procs` | `len(app.Processes)` | 应用名 |
| 应用 | `cpu` | `app.CPUPercent` | 应用名 |
| 应用 | `gpu` | `gpuSortKey(app.GPUOk, app.GPUPercent)`（`!ok` → `-1`） | 应用名 |
| 应用 | `memory` | `app.MemBytes` | 应用名 |
| 进程 | `proc` | `strings.ToLower(process.Name)` | PID |
| 进程 | `app` | `appLabel` 小写 | 进程名、PID |
| 进程 | `cpu`/`gpu`/`memory` | 同名数值键 | 进程名、PID |

降序只翻转主键，兜底永远升序。`resourceState` 增加 `appSort` / `procSort ui.SortOrder`，创建时都给 `{Column: "app"}`，并把 `state.tableApps.Sort = &state.appSort`、`state.tableProcesses.Sort = &state.procSort` 设一次。

- [ ] **步骤 1：写失败测试**

纯函数（新助手 `twoAppFixture(t)`：`com.aaa` 的 utime 增量小于 `com.zzz`，两者内存 20 MB / 40 MB）：

```go
func TestSortAppsPutsTheApplicationNameFirst(t *testing.T)         // ui.SortOrder{} → com.aaa, com.zzz（与 CPU 顺序相反）
func TestSortAppsOrdersNumbersBothWays(t *testing.T)               // memory 升序 / 降序
func TestSortProcessesKeepsAnAppsProcessesTogether(t *testing.T)   // app 升序：com.aaa 的两行相邻
func TestSortProcessesPutsAnUnavailableGPULast(t *testing.T)       // gpu 升序：不可用的排最后
```

页面集成：

```go
func TestProcessTableStartsSortedByAppName(t *testing.T)   // 首行 com.aaa，第二行 com.zzz
func TestProcessViewStartsSortedByAppName(t *testing.T)    // 进程视图同样 com.aaa 的进程先出
func TestClickingAMemoryHeaderSortsTheTable(t *testing.T)  // clickColumnHeader(t, tt, "内存") → 20 MB 那行在前
func TestSortKeepsTheExpandedGroup(t *testing.T)           // 展开 com.zzz 后点「内存」表头，PID 200 仍在
```

测试助手（`resources_test.go` 顶部）：

```go
// clickColumnHeader clicks the header of the sortable column named title; a
// header is the only RoleColumnHeader with that name, which keeps it apart from
// a cell or a tab with the same words.
func clickColumnHeader(t *testing.T, tt *ui.Tester, title string) {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleColumnHeader, title)
	if !ok {
		t.Fatalf("no %q column header; access = %v", title, tt.AccessAll())
	}
	tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run 'TestSort|TestProcessTableStartsSorted|TestProcessViewStartsSorted|TestClickingAMemoryHeader' -v`
预期：编译失败 `undefined: sortApps`（纯函数），其余用例红。

- [ ] **步骤 3：实现**

- `processColumns()` / `processViewColumns()`：每个 `ui.TableColumn` 加 `Sortable: true`（第 0 列共享 `ID: "app"` 的是应用视图，进程视图首列 `ID: "proc"`）。
- `resourceStateFor`：初始化两个 `Sort` 指针与默认 `{Column: "app"}`。
- `processTable`：`order := state.appSort`；`kept := sortApps(filterApps(...), order)` 之后再展平成行。
- `processList`：`rows := sortProcessRows(filterProcessRows(processRows(snapshot), state.filter), state.procSort)`。
- `processRows`：去掉 `sort.SliceStable`（CPU 排序），只负责展平；顶部注释改为「行序由页面按表头选择排好」。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/app -run 'TestSort|TestProcess|TestResource|TestHardware' -v` → PASS
再跑 `go test ./internal/app` 全绿（`TestProcessViewListsEveryProcessOnce`、`TestResourceViewSwitchesBetweenAppsAndProcesses` 不能被排序搞坏）。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/resources.go internal/app/resources_test.go
git commit -m "feat: sort the resource tables by header, application name by default"
```

---

## 任务 5：文件页右栏表

**文件：**
- 修改：`internal/app/files.go`（`fileState.table` → `ui.ListState`；`fileTable`；`fileColumns` 不改）
- 修改：`internal/app/files_test.go`（`assertNoOnevraTable(t, "files.go")`；点击/选中相关用例）

- [ ] **步骤 1：写失败测试**

```go
func TestFilePageUsesNoOnevraTable(t *testing.T) { assertNoOnevraTable(t, "files.go") }

// The table's choice is the path the page selected, so a click on a folder both
// opens it and leaves the row highlighted.
func TestFileRowStaysChosenAfterOpeningIt(t *testing.T) {
	vm, _ := fileFixture(t) // 既有助手
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	if err := tt.Click("Download"); err != nil {
		t.Fatalf("Click(Download) error: %v", err)
	}
	tt.Frame()
	stack := vm.fileStateFor(vm.Manager.Profiles()[0].ID)
	if stack.selected != "/sdcard/Download" {
		t.Fatalf("selected = %q, want /sdcard/Download", stack.selected)
	}
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run 'TestFilePageUsesNoOnevraTable|TestFileRowStaysChosenAfterOpeningIt' -v`
预期：前者报 `files.go` 仍用 `components.Table`。

- [ ] **步骤 3：实现**

`fileTable` 的表格段落改为：
```go
chosen := -1
for i, entry := range listing.Entries {
    if entry.Path == state.selected {
        chosen = i
        break
    }
}
state.table.Selected = &chosen
table := tableCard(c, &state.table, fileColumns(), len(listing.Entries), tableOptions{
    Label: "设备文件列表", Grow: true, Unframed: true,
}, func(row, col int) { vm.fileCell(c, col, listing.Entries[row]) })
if table.Changed() && chosen >= 0 && chosen < len(listing.Entries) {
    vm.activateEntry(profile, state, listing.Entries[chosen])
}
```
- 去掉包住表格的 `components.ScrollView`；右栏外层已有边框，所以 `Unframed: true`（决策 6）。
- `OpenDialog`/属性弹窗等路径不变。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/app -run 'TestFile|TestFolder|TestTree|TestActivating' -v` → PASS
特别确认：`TestFolderRowOpensTheFolder`、`TestFileRowOpensTheAttributeDialog`、`TestFileTreeHighlightsTheFolderTheTableOpened`。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/files.go internal/app/files_test.go
git commit -m "refactor: draw the file listing with ui.Table"
```

---

## 任务 6：缓存管理页

**文件：**
- 修改：`internal/app/appcache.go`（去掉页面级 `ui.Scroll`，表格 `Grow` + 边框）
- 修改：`internal/app/state.go`（`CacheTable ui.ListState`）
- 修改：`internal/app/appcache_test.go`（`TestCachePageScrollsLongLists` 按虚拟化重写）

- [ ] **步骤 1：写失败测试**

```go
func TestCachePageUsesNoOnevraTable(t *testing.T) { assertNoOnevraTable(t, "appcache.go") }

// The rows past the fold are built only once the table itself scrolls: the page
// no longer scrolls around the table.
func TestCacheTableKeepsTheClearActionInView(t *testing.T) {
	// 40 条缓存
	tt := ui.NewTester(vm.ContentView, 900, 700)
	tt.Frame()
	if tt.HasText("应用 39") {
		t.Fatal("the table built a row it does not show")
	}
	footer, ok := tt.Find("清空这台设备的缓存")
	if !ok || footer.Y+footer.H > 700 {
		t.Fatalf("the clear action is off screen: %+v (found=%v)", footer, ok)
	}
	tt.Scroll(450, 350, 0, 3000)
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	waitForText(t, tt, "应用 39")
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run 'TestCachePageUsesNoOnevraTable|TestCacheTableKeepsTheClearActionInView' -v`
预期：前者报 `appcache.go` 仍用 `components.Table`；后者报「built a row it does not show」（现在是整页滚动，40 行全建出来）。

- [ ] **步骤 3：实现**

- `cachePage`：把「条数 · 体积」那行留在列里，删除外层 `ui.Scroll` + 内层 `ui.Column`，改成
  ```go
  components.Text(c, itoa(len(entries))+" 条 · 约 "+formatBytes(total))...
  tableCard(c, &vm.CacheTable, cacheColumns(), len(entries), tableOptions{
      Label: "应用信息缓存列表", Grow: true,
  }, func(row, col int) { vm.cacheCell(c, col, entries[row]) })
  ```
- 空状态与「清空」按钮不变（按钮是 `Shrink(0)`，留在页底）。
- `state.go`：`CacheTable components.TableState` → `ui.ListState`（注释改成「列宽与滚动」）。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/app -run 'TestCache|TestFormatBytes|TestEntryLabel' -v` → PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/app/appcache.go internal/app/appcache_test.go internal/app/state.go
git commit -m "refactor: give the app-info cache list a framed ui.Table that scrolls itself"
```

---

## 任务 7：DERP 列表页（预设表）与详情页（区域表）

**文件：**
- 修改：`internal/app/derp.go`（`derpPresetSection`、`derpRegionTable`）
- 修改：`internal/app/state.go`（`DerpTable` / `DerpRegionTable` → `ui.ListState`）
- 修改：`internal/app/derp_test.go`（`TestDerpDetailScrollsLongMaps` 重写；`assertNoOnevraTable(t, "derp.go")`）

- [ ] **步骤 1：写失败测试**

```go
func TestDerpPageUsesNoOnevraTable(t *testing.T) { assertNoOnevraTable(t, "derp.go") }

// A 60-region map is reachable: the region table scrolls its own rows and the
// page keeps its scroll area for the rest.
func TestDerpRegionTableScrollsItsOwnRows(t *testing.T) {
	// manyRegionMap(60)，打开详情
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if tt.HasText("relay60.example.com") {
		t.Fatal("the region table built a row it does not show")
	}
	region, ok := tt.Find("区域表")
	if !ok {
		t.Fatal("the detail page has no region table")
	}
	tt.Scroll(region.X+region.W/2, region.Y+region.H/2, 0, 4000)
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	waitForText(t, tt, "relay60.example.com")
}
```

保留 `TestDerpDetailScrollsLongMaps` 的意图（页面仍有滚动区），但不再要求 60 行同时建出来。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run 'TestDerpPageUsesNoOnevraTable|TestDerpRegionTableScrollsItsOwnRows' -v`
预期：前者报 `derp.go`；后者报「the detail page has no region table」（`区域表` 这个标签还不存在）。

- [ ] **步骤 3：实现**

- `derpPresetSection`：
  ```go
  tableCard(c, &vm.DerpTable, derpColumns(), len(presets), tableOptions{
      Label: "DERP 节点预设列表", Rows: len(presets),
  }, func(row, col int) { vm.derpPresetCell(c, col, presets[row]) })
  ```
  （页面本身是 `ui.Scroll`，所以用 `Rows` 定高，最多 8 行；`derpColumns()` 不加 `Sortable`——决策 7）
- `derpRegionTable`：同样改成 `tableOptions{Label: "区域表", Rows: len(ids)}`。
- 两个表都不再依赖 `components.TableRow` 的 `Clicked`；页面上没有行级点击，只有行内按钮。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/app -run 'TestDerp|TestDefaultRow|TestManyRegion' -v` → PASS
必须仍绿：`TestDerpDetailShowsRegionsAndNodes`、各 `click(t, tt, "详情"/"编辑"/"删除")` 用例、`TestDerpEditorClosesWithoutPanic`。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/derp.go internal/app/derp_test.go internal/app/state.go
git commit -m "refactor: frame the DERP tables with ui.Table and let them scroll"
```

---

## 任务 8：日志（日志页 + 设备日志标签）

**文件：**
- 修改：`internal/app/logs.go`（`logFilter.table` → `ui.ListState`；`logTable`；`logTimeline` 的框）
- 修改：`internal/app/logs_test.go`（`assertNoOnevraTable(t, "logs.go")`）

- [ ] **步骤 1：写失败测试**

```go
func TestLogsPageUsesNoOnevraTable(t *testing.T) { assertNoOnevraTable(t, "logs.go") }

// The timeline scrolls its own rows and stays framed, so a long text column
// scrolls sideways instead of pushing the header out of reach.
func TestLogTimelineIsItsOwnScrollArea(t *testing.T) {
	// 80 行
	tt := ui.NewTester(vm.ContentView, 900, 700)
	tt.Frame()
	waitForText(t, tt, "第0行日志")
	if _, ok := tt.Find("日志时间线"); !ok {
		t.Fatalf("the timeline is not a labelled table; texts = %v", tt.Texts())
	}
	if tt.HasText("第79行日志") {
		t.Fatal("the timeline built a row it does not show")
	}
	tt.Scroll(450, 300, 0, 4000)
	tt.Frame()
	waitForText(t, tt, "第79行日志")
}
```

保留 `TestLogsPageShowsTaskOutput`（单点击打开输出）、`TestLogsPageKeepsTheReaderAtTheTail`、`TestFollowKeepsAnIdenticalNewLine`、`TestLogsPageKeepsTwoIdenticalLinesApart` 不动。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run 'TestLogsPageUsesNoOnevraTable|TestLogTimelineIsItsOwnScrollArea' -v`
预期：前者报 `logs.go`；后者报「the timeline is not a labelled table」。

- [ ] **步骤 3：实现**

`logTable`：
```go
state := &f.table
if jump { state.ScrollTo(0, ui.Start) }
state.Selected = &f.selected
state.Key = func(row int) any { return logRowKey(entries[row], row) }
table := tableCard(c, state, logColumns(), len(entries), tableOptions{
    Label: "日志时间线", Grow: true,
}, func(row, col int) { /* 原来的 switch 不变 */ })
if table.Changed() {
    row := f.selected
    if row >= 0 && row < len(entries) && entries[row].Source == core.LogTask {
        vm.openLogOutput(entries[row])
    }
}
```
- `logFilter.table components.TableVirtualizedState` → `ui.ListState`；`state.List` 的用法改成直接 `state`。
- `logTimeline` 的 `logPlaceholder` 分支保持：空态仍是占位文本，不是 0 高度的表。
- `logColumns()` 不加 `Sortable`。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/app -run 'TestLog|TestDeviceLogTab|TestFollow|TestClearLogs' -v` → PASS

- [ ] **步骤 5：Commit**

```bash
git add internal/app/logs.go internal/app/logs_test.go
git commit -m "refactor: draw the log timeline with ui.Table"
```

---

## 任务 9：连通测试对话框 + 仓库级守卫

**文件：**
- 修改：`internal/app/reachability.go`
- 修改：`internal/app/style_test.go`（仓库级守卫）
- 删除：`internal/app/table_test.go` 里的 `assertNoOnevraTable` 助手与各页面的 `TestXxxPageUsesNoOnevraTable` 用例（被仓库级守卫取代）
- 修改：`internal/compat/compat_test.go`（删掉 onevra 表格断言）

- [ ] **步骤 1：写失败测试**

`internal/app/style_test.go`：

```go
// TestNoOnevraTableComponentsAnywhere enforces the rule of this migration: the
// onevra tables cannot scroll their own header, so a column resized wide puts
// its resize grip outside the window. Every table is ui.Table behind tableCard.
func TestNoOnevraTableComponentsAnywhere(t *testing.T) {
	assertNoComponentsTable(t, "internal/app/*.go")
}
```

其中 `assertNoComponentsTable` 扫描 `*.go`（跳过 `_test.go`），命中
`components.Table`、`components.TableRow`、`components.TableVirtualized`、`components.TableState`、`components.TableVirtualizedState` 任一即失败，并打印文件名。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/app -run TestNoOnevraTableComponentsAnywhere -v`
预期：FAIL，只报 `internal/app/reachability.go`（其它页面已在任务 2–8 迁完）。

- [ ] **步骤 3：实现**

- `reachabilityTable` 改为：
  ```go
  tableCard(c, nil, columns, len(results), tableOptions{Label: "连通测试结果", Rows: len(results)},
      func(row, col int) { /* 原来的 switch 不变 */ })
  ```
  （`state` 传 `nil`：这一屏不选行、不记忆列宽；对话框高度由 `Rows` 定，最多 8 行，多了表内滚）
- 删除 `table_test.go` 的 `assertNoOnevraTable` 与 6 个 `TestXxxPageUsesNoOnevraTable`。
- `compat_test.go`：删掉 `components.Table`、`components.TableRow`、`components.TableState{}.Columns`、`components.TableVirtualized`、`components.TableVirtualizedState{}.List` 五行断言。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/app -run 'TestReach|TestNoOnevra' -v` → PASS
再跑 `go test ./...` → 全绿；`go vet ./...` 干净；`gofmt -l .` 无输出。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/reachability.go internal/app/style_test.go internal/app/table_test.go internal/compat/compat_test.go
git commit -m "refactor: finish the ui.Table migration and forbid onevra tables"
```

---

## 任务 10：文档、截图核对

**文件：**
- 修改：`docs/CHANGELOG.md`（`## [未发布]` → `变更`）
- 修改：`docs/ui-and-console.md`（表格规则与陷阱表）
- 修改：`docs/architecture.md`（§15 决策记录）
- 修改：`AGENTS.md`（GUI 组件优先级表里的表格一行）
- 可能修改：`internal/app/screenshots_test.go`（新状态才加用例）

- [ ] **步骤 1：改文档**

`docs/CHANGELOG.md` 的 `变更` 段新增（一条一行，用户视角）：

```markdown
- 所有表格换成 myGo 内置表格：列宽调宽后表头跟着内容横向滚动，拖动列宽的抓手不会再跑出窗口；表格统一加上圆角边框。
- 资源页的应用 / 进程两个列表现可按应用名、进程数（进程视图为进程名）、CPU、GPU、内存排序，默认按应用名称升序；点表头升序、再点降序。
- 资源页的分组行改为行首的「展开 / 收起」箭头按钮，不再点整行展开（键盘上下键因此在表格里照常可用）。
```

`docs/ui-and-console.md`：
- 表格约定（原「表格用 `components.Table` + `TableRow`」）改为「表格一律用 `internal/app/table.go` 的 `tableCard`：它加边框、定高度、给标签；页面不要直接写 `ui.Table`，也不要把表格塞进 `components.ScrollView` / `ui.Scroll`」。
- 陷阱表里 `TableVirtualized` 的两行（行键、`List.Selected` 从 0 开始）改写为 `ui.Table` 版本：行键用 `ListState.Key`，`Selected` 初始 -1，`Changed()` 取代 `OnChange`。
- 页面清单里 `components.Table` 字样同步改成 `tableCard`。

`docs/architecture.md` §15 新增一条决策：**表格统一 `ui.Table`**。写清「为什么」（onevra 的 `components.Table` 自己不滚动，文档要求调用方套 `ScrollView`；我们套的是**竖滚**的容器，列一调宽，最右那列的调整抓手就被裁在可视区外、没有横向滚动能过去。`ui.Table` 把表头与行放进同一个横向滚动区，抓手永远跟着列走）与「代价」（失去 onevra 的斑马纹与中性选中色，选中改强调色；行级点击要靠 `Selected`；排序由页面算，不是组件算）。

`AGENTS.md`：组件优先级表「表格」一行从 `components.Table / TableVirtualized` 改成 `ui.Table`（经 `internal/app/table.go` 的 `tableCard`），并在反例清单里加一句「不要再用 onevra 的表格组件：它的表头不横向滚动，列宽手柄会滚出可视区」。

- [ ] **步骤 2：渲染截图人工核对**

运行：`TAILCAT_SHOT_DIR=/tmp/shots go test ./internal/app -run TestScreenshots -v`
逐张看：`04-device-resources`（边框 + 表头 + 箭头）、`22-appmeta-cache` / `26-appmeta-cache-long`、`18-derp-list` / `19-derp-detail`、`05-device-files`、`09-device-logs` / `15-logs-page`、`13-reachability`。要确认的是布局而不是文字：边框是否双线、表头是否被浮动标题栏压住、页底是否被窗口切掉。发现问题回到对应任务改。

- [ ] **步骤 3：跑完整验证**

运行：
```sh
gofmt -l .
go vet ./...
go test ./...
go test -race ./internal/core ./internal/app
go test ./internal/compat/
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...
```
预期：全部无输出或 PASS。

- [ ] **步骤 4：Commit**

```bash
git add docs/CHANGELOG.md docs/ui-and-console.md docs/architecture.md AGENTS.md
git commit -m "docs: record the ui.Table migration, sorting and the framing rule"
```

---

## 自检记录

- **规格覆盖**：换组件（任务 2–9）、修「手柄滚出可视区」（`ui.Table` 自带横向滚动 + 任务 1 的「表格自己滚」约束 + 任务 6–7 的嵌套滚动拆除）、资源页排序与默认顺序（任务 4）、所有表格加边框（任务 1 的 `tableCard` + 各任务落地 + 任务 9 的守卫）。
- **未决/需确认**：`docs/CHANGELOG` 之外没有规格文档；用户给的第 2、3 张参考图（`pi-clipboard-8a46…png` 与 `pi-clipboard-3d13…png`）**字节完全相同**，所以「参考样式」只能理解为缓存页现在的样子（表格带圆角边框、无斑马纹）。若原本想给的是另一种边框样式，需要在动手前补图。
- **类型一致性**：`tableCard` 返回 `ui.Element`（页面读 `Changed`）；`tableOptions` 字段在任务 2–9 全部只用到 `Label/Grow/Rows/Unframed`；排序函数签名在任务 4 定义，任务 4 内部两处调用。
