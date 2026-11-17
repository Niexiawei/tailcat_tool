# 应用信息缓存管理页 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）跟踪进度。

**目标：** 侧栏设备列表之下新增管理入口中的第一个——「缓存管理」（另两个是「DERP 地图」与「日志」，各自计划）。打开一个独立页面：按设备列出已缓存的应用信息条目（应用名、包名、大小、读取时间、是否过期），可删除单条、可清空这台设备的缓存。页面入口、路由与标题栏面包屑是本页与 DERP 地图页**共用**的骨架，在这里一次定下来。

**架构：** core 只多三个方法（`AppMetaCacheEntries` / `InvalidateAppMetaCache` / `PurgeAppMetaCache`），转发给已有的 `core.AppMetaCache` 与 `AppMetaSource.Forget`，**不新开存储、不新增依赖**；页面每帧读一次快照，删除/清空后调 `notify()` 重绘。入口在 `SidebarView` 的滚动区内、两组设备之后；页面是 `ui.Router` 的一个顶层路由。

**技术栈：** Go 1.27.3、onevra `components`（`Table` / `Dialog` / `Button` / `IconButton` / `Select` / `Badge`）、`onevra/icons/lucide`、`ui.NewTester` + `png.Encode(tt.Image())`、`internal/appmeta` 的 7 天磁盘缓存。

**规格：** 用户 2026-11 的口头需求 + `CHANGELOG.md` 里已写下的「缓存管理页将在下一版提供」；没有独立规格文档。裁决写进本文「决策与代价」，入口骨架落地后同步进 `docs/ui-and-console.md`（侧栏结构一节）。

---

## 全局约束

- 纯 Go、禁止 cgo；本轮不引入任何新依赖：大小格式化写成本地纯函数 `formatBytes`（单独单测），不去把间接依赖 `go-humanize` 提成直接依赖。
- 依赖方向 `cmd → internal/app → internal/core → internal/platform`；`internal/app` 不得直接 `exec.Command`，也不得自己读缓存目录。
- 缓存目录与权限沿用现状（`<配置目录>/appmeta`，配置目录 0700）；本页只**删除**文件，不新建路径。
- 不能持有 `m.mu` 时做 I/O（读目录、删文件都在 store 里，manager 只在锁内取字段）。
- 截图用例沿用 `TAILCAT_SHOT_DIR`；不设环境变量就 SKIP。
- 面向用户的中文文案，测试逐字断言。
- GUI 一律优先 onevra `components`；行式元素 `FillWidth()`，**禁止** `Fill()`（会同时占满高度）。
- 每个提交：`gofmt -w`、`go vet ./...`、`go test ./...` 全绿；用户可见改动同提交写进 `docs/CHANGELOG.md`。
- 分支 `feat/manage-pages`，不要直接提 `main`。

---

## 审查重点（Review Focus）

每条都要有钉住它的测试，写在对应任务的步骤里。

1. **清空之后名字必须真的消失**：只删磁盘文件、不清 `AppMetaSource` 的内存状态时，资源页仍显示旧名字，用户会以为按钮没生效。
2. **清空之后正在飞的读取不得把它写回来**：`appmeta` 的写协程可能在清空的同时完成一个包的读取。必须用"每设备一个 epoch"作废在飞的写入，否则用户清完缓存、刷新资源页又看到同一个名字（`TestPurgeDiscardsAnInFlightFetch`）。
3. **删除单条不得误删别的设备的同名应用**：缓存键是 `设备 uuid | 包名`，`com.tencent.mm` 在两台手机上各有条目。
4. **缓存不可用时页面不能崩**：`AppMetaCache` 为 nil（`main.go` 打开缓存失败）时，页面显示「应用信息缓存不可用」而不是空白表格或 panic。
5. **入口顺序固定：设备列表在上、管理入口在下**：用户明确要求放在设备列表之下；顺序错了等于需求没做（用 `tt.Find(...).Y` 断言）。
6. **清空是破坏性操作，必须二次确认**，且确认文案要写清"这台设备、N 条、约 X MB"。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `internal/core/manager_appcache.go`（新建） | 三个 manager 方法，把 `uuid` 换成缓存键 `deviceKey(id)`，并协调 `AppMetaSource.Forget` |
| `internal/core/manager.go`（修改） | `ManagerOptions.AppMetaCache` 字段与 `m.appMetaCache` |
| `internal/appmeta/store.go`（修改） | 每设备 epoch：`Purge` 递增，落盘前校验 |
| `internal/app/state.go`（修改） | 路由常量、`ManageRoute` 标题、`CachePage` 状态（选中设备、确认删除）、`SidebarView` 里的 `manageEntries` |
| `internal/app/manage.go`（新建） | 共享骨架：`manageEntries`（侧栏两行入口）、`managePageTitle`（面包屑文案）；DERP 页后续直接复用 |
| `internal/app/appcache.go`（新建） | 缓存管理页：设备选择、表格、删除、清空确认 |
| `internal/app/detail.go`（修改） | `routedPage` 增加 `CacheRoute` 分支；`ContentTitle` 增加管理页面包屑 |
| `cmd/tailcat-manager/main.go`（修改） | 同一个 `appmeta` store 同时注入 `AppMeta` 与 `AppMetaCache` |
| `internal/app/manage_test.go`（新建） | 入口/路由/面包屑的共享骨架测试 |
| `internal/app/appcache_test.go`（新建） | 页面、删除、清空的视图测试 |
| `internal/core/manager_appcache_test.go`（新建） | core 三个方法与竞态测试 |
| `internal/app/screenshots_test.go`（修改） | 加缓存页与清空确认对话框两个用例 |

---

## 任务 1：管理入口骨架（三个页面共用）

**文件：**
- 修改：`internal/app/state.go`、`internal/app/detail.go`
- 创建：`internal/app/manage.go`、`internal/app/manage_test.go`

- [ ] **步骤 1：编写失败的测试**

`internal/app/manage_test.go`：

```go
func TestSidebarShowsManageEntriesBelowDevices(t *testing.T) {
	vm := newTestViewModel(t) // 已有一台 E245
	tt := sidebarTester(vm, 280, 720)
	device, ok := tt.Find("E245")
	if !ok {
		t.Fatalf("device row missing; texts = %v", tt.Texts())
	}
	entry, ok := tt.Find("缓存管理")
	if !ok {
		t.Fatalf("cache entry missing; texts = %v", tt.Texts())
	}
	if entry.Y <= device.Y {
		t.Fatalf("缓存管理 is above the device list (%0.0f <= %0.0f)", entry.Y, device.Y)
	}
	if !tt.HasText("DERP 地图") {
		t.Fatalf("DERP entry missing; texts = %v", tt.Texts())
	}
}

func TestManageEntryOpensCachePage(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 720)
	if err := tt.Click("缓存管理"); err != nil {
		t.Fatalf("Click() error: %v", err)
	}
	if got := vm.Router.Path(); got != CacheRoute {
		t.Fatalf("path = %q, want %q", got, CacheRoute)
	}
	page := ui.NewTester(vm.ContentView, 900, 700)
	if !page.HasText("应用信息缓存") {
		t.Fatalf("cache page missing; texts = %v", page.Texts())
	}
	title := ui.NewTester(vm.ContentTitle, 400, 40)
	if !title.HasText("缓存管理") {
		t.Fatalf("title = %v, want 我的设备 › 缓存管理", title.Texts())
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run 'TestSidebarShowsManageEntriesBelowDevices|TestManageEntryOpensCachePage' -v`
预期：FAIL，`undefined: CacheRoute`。

- [ ] **步骤 3：实现入口与路由**

`internal/app/state.go`：

```go
// CacheRoute is the management page for cached application names and icons.
const CacheRoute = "/manage/appmeta"

// DerpRoute is the management page for DERP maps; the DERP plan adds its detail
// route next to it.
const DerpRoute = "/manage/derp"
```

`internal/app/manage.go`：

```go
// manageEntries is the browsing group under the device list. It sits inside the
// scrolling sidebar (not the footer) because these are pages, not window
// actions; the user asked for them below the devices. The logs page plan adds
// 日志 as the first row of this same group.
func (vm *ViewModel) manageEntries(c *ui.Context) { ... }

// managePageTitle returns the breadcrumb pair for a management page, empty for
// every other route. ContentTitle falls back to 我的设备 when it is empty.
func managePageTitle(path string) (string, bool)
```

两个入口用 `components.Button(c, "", components.ButtonGhost)` + `components.Icon` + `components.Text`，各 `FillWidth().Justify(ui.Start)`；点击分别 `vm.Router.Push(CacheRoute)` / `vm.Router.Push(DerpRoute)`。

`SidebarView` 里 `ui.Scroll` 的 Children 改成：

```go
vm.deviceGroups(c)
ui.Column(c).Padding(0, 4).Gap(2).Children(func() { vm.manageEntries(c) })
```

`detail.go` 的 `routedPage` 在 `/devices/{id}` 匹配之前加：

```go
if r.Match(CacheRoute) {
	r.Title("缓存管理")
	vm.cachePage(c)
	return
}
```

`ContentTitle`：`deviceIDFromPath` 失败时先问 `managePageTitle`，有值就画 `components.Breadcrumbs(c, []string{"我的设备", name}, &vm.CrumbChoice)`，`CrumbChoice == 0` 时 `vm.OpenDevices()`；没有才退回原来的「我的设备」纯文本。

**第一步的 `cachePage` 只画标题**（`components.Text(c, "应用信息缓存").FontSize(18).Bold()`），页面内容由任务 3 填。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app -run 'TestSidebarShowsManageEntriesBelowDevices|TestManageEntryOpensCachePage' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```sh
git add internal/app/{state.go,detail.go,manage.go,manage_test.go}
git commit -m "feat: add management entries under the device list"
```

---

## 任务 2：core 的缓存读写

**文件：**
- 创建：`internal/core/manager_appcache.go`、`internal/core/manager_appcache_test.go`
- 修改：`internal/core/manager.go`、`cmd/tailcat-manager/main.go`、`internal/appmeta/store.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestPurgeForgetsDeviceStatus(t *testing.T)          // 清空后 Status 查不到，Entries 为空
func TestInvalidateRemovesOneEntryOnly(t *testing.T)     // 另一台设备的同包名条目仍在
func TestCacheAccessorsReportUnavailableWhenNil(t *testing.T)
func TestPurgeDiscardsAnInFlightFetch(t *testing.T)       // 见「审查重点 2」
```

`TestPurgeDiscardsAnInFlightFetch` 用 `internal/appmeta` 的假 `core.Runner`（照 `store_test.go` 里已有的做法）让 `apkPath` 卡在一个 channel 上：先 `Request`，等它进入读取，`Purge`，再放行，然后断言 `Entries` 仍为空。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestPurgeForgetsDeviceStatus|TestPurgeDiscardsAnInFlightFetch' -v`
预期：FAIL，`undefined: Manager.AppMetaCacheEntries`。

- [ ] **步骤 3：实现**

```go
// AppMetaCacheEntries lists what the cache holds for one device, newest first.
func (m *Manager) AppMetaCacheEntries(id uuid.UUID) []AppMetaEntry

// InvalidateAppMetaCache drops one package's entry; the next read fetches it.
func (m *Manager) InvalidateAppMetaCache(id uuid.UUID, pkg string) error

// PurgeAppMetaCache empties one device's cache and forgets its in-memory
// statuses, so the next sampling pass reads every application again.
func (m *Manager) PurgeAppMetaCache(id uuid.UUID) error
```

`PurgeAppMetaCache` 在锁外调用 `cache.Purge(key)`，成功后 `source.Forget(key)`，然后 `m.notify()`。缓存为 nil 时返回 `Error("应用信息缓存不可用")`，`Entries` 返回 `nil`。

`internal/appmeta/store.go` 的 epoch：`purgeEpoch map[string]uint64`（带锁），`enqueue` 时记下 epoch 进 job，`handle` 落盘前比对，不一致就丢弃结果。`Purge` 递增 epoch 后再删文件。

`main.go`：`appmeta.New(...)` 的 store 同时赋给 `ManagerOptions.AppMeta` 与 `ManagerOptions.AppMetaCache`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core ./internal/appmeta -run 'AppMeta|Purge|Invalidate' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```sh
git add internal/core internal/appmeta cmd/tailcat-manager/main.go
git commit -m "feat: expose the app-metadata cache to the manager"
```

---

## 任务 3：页面内容（设备选择 + 表格 + 空态）

**文件：**
- 修改：`internal/app/state.go`（`CacheDevice uuid.UUID`、`CacheEntries []core.AppMetaEntry`）
- 修改：`internal/app/appcache.go`
- 创建/修改：`internal/app/appcache_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestCachePageListsEntriesOfChosenDevice(t *testing.T)
func TestCachePageExplainsAnEmptyCache(t *testing.T) // 「这台设备还没有缓存任何应用信息」
func TestCachePageSaysWhenTheCacheIsUnavailable(t *testing.T) // nil cache
```

表格列固定：**应用名 / 包名 / 大小 / 读取时间 / 状态**，行尾一个 `components.IconButton(c, lucide.Trash, "删除", components.IconButtonPlain)`。过期条目在状态列显示 `components.Badge`「已过期」（`AppMetaEntry.Stale`）。顶部一行 `components.Select`（设备，值用 `uuid`）+ 合计「N 条 · 约 X MB」。

- [ ] **步骤 2：运行测试验证失败** → `go test ./internal/app -run TestCachePage -v`，FAIL。

- [ ] **步骤 3：实现页面**

- 设备下拉默认选中 `vm.Selection`；`cacheDeviceChoice()` 返回 `Profiles + TemporaryProfiles`，每项标签 `名称 · 状态`。
- 每帧 `vm.Manager.AppMetaCacheEntries(vm.CacheDevice)` 取一次快照，**不要在渲染里删任何东西**。
- 大小用本地纯函数 `formatBytes(int64) string`（1024 进制，`1.2 MB`）；读取时间用 `entry.FetchedAt.Format("2006-01-02 15:04")`，与日志页的 `15:04:05` 风格一致。
- 空态与不可用状态各一句中文，居中显示，不画空表头。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: list cached app metadata per device`

---

## 任务 4：删除单条与清空

**文件：**
- 修改：`internal/app/appcache.go`、`internal/app/state.go`（`CacheConfirmOpen bool`、`CacheConfirmPkg string`）
- 修改：`internal/app/appcache_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestDeleteAsksBeforeDroppingOneEntry(t *testing.T)
func TestPurgeAsksBeforeClearingTheDevice(t *testing.T)
func TestPurgeClearsTheDecodedIconCache(t *testing.T) // vm.appIcons 里该 device|* 的键被删
```

断言点：删除要点到确认按钮才真正调用（假 cache 记录调用次数）；清空文案含设备名、条数与大小；`PurgeAppMetaCache` 返回错误时用 `vm.Fail(err)` 显示，并且对话框不关。

- [ ] **步骤 2：运行测试验证失败**。

- [ ] **步骤 3：实现**

- 删除单条：`components.IconButton(c, lucide.Trash)` → 打开 `CacheConfirmOpen`，确认后 `vm.Manager.InvalidateAppMetaCache(vm.CacheDevice, pkg)`，成功 `vm.Notify("已删除该应用信息的缓存")`。
- 底部「清空这台设备的缓存」用 `components.Button(c, ..., components.ButtonSecondary)`（组件库没有 danger 变体；危险语义靠确认对话框与文案表达），确认后 `PurgeAppMetaCache` + `vm.forgetAppIcons(vm.CacheDevice)` + `vm.Notify("已清空该设备的应用信息缓存")`。
- `forgetAppIcons` 放进 `internal/app/resources.go`，遍历 `vm.appIcons` 删掉 `device|` 前缀的键（DERP 页不需要，但这是缓存页的职责）。
- 清空后**不自动重新读取**：对话框里写明「资源页下次打开时会重新读取」。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: let the cache page delete and clear entries`

---

## 任务 5：截图、文档与变更记录

- [ ] **步骤 1：加截图用例**（`shotCases()`）：`缓存管理页（有条目）`、`缓存清空确认`。运行 `TAILCAT_SHOT_DIR=/tmp/shots go test ./internal/app -run TestScreenshots`，人工看一眼：表格列没挤成 0 宽、确认对话框没有出窗口。
- [ ] **步骤 2：文档** `docs/ui-and-console.md` 的侧栏一节补三行：设备列表 → 管理入口（缓存管理 / DERP 地图）→ 底栏工具状态；页面路由表补 `/manage/appmeta`。
- [ ] **步骤 3：CHANGELOG** 在 `[未发布]` → `新增` 里把「缓存管理页将在下一版提供」改成实际能力：
  「侧栏设备列表下新增『缓存管理』：按设备列出已缓存的应用名称与图标（包名、大小、读取时间、是否过期），可删除单条或清空这台设备的缓存。」
- [ ] **步骤 4：Commit** `docs: record the cache management page`

### 验收

- `--demo` 下能打开页面看到模拟设备，条目为空时给说明；删除与清空各走一遍，确认文案与提示都对。
- 真机：资源页解析过若干应用 → 缓存页能看到；清空 → 关掉资源页再打开，名字从包名开始重新读取。
- `go test ./...` 全绿；`gofmt -l .` 无输出。

### 风险

- `appmeta` 的 epoch 只能作废"清空时在飞"的读取；清空之后新发起的读取当然会写回，这是预期行为。
- 缓存键含设备 `uuid`：同一台手机重连后是另一台设备，旧条目会留在盘上直到 `prune`。页面文案只说「这台设备」。

---

## 决策与代价

| 决定 | 代价 |
|---|---|
| 入口放在**侧栏滚动区**、两组设备之后，而不是 `LeftToolbar` 底栏 | 设备多时入口被推到滚动区下方，要先滚一下；换来"在设备列表之下"的明确层级，底栏继续只放工具状态与设置 |
| 缓存页是独立路由，不是资源页里的对话框 | 多一个页面与一条面包屑；换来 DERP 地图页可以复刻同一骨架，且两页都能直接分享截图 |
| 设备下拉列出**全部**设备（含临时设备），而不只列出"有缓存"的 | 大部分设备第一次打开是空页；换来设备切换的位置固定，不会因为缓存被清空而下拉项消失 |
| 清空后不自动重新读取 | 用户可能期望"清完立刻重新读"；换来清空真的释放磁盘，重新读取由资源页的既有按钮决定 |
| 删除单条、清空都用同一个确认状态（`CacheConfirmOpen` + `CacheConfirmPkg`） | 状态里多一个空串含义（"整台设备"）；换来两个操作共用一套确认文案与测试 |
| 组件库没有 danger 按钮变体，清空用 `ButtonSecondary` | 按钮本身看不出破坏性，全靠确认对话框；换来不自己拼一个按钮外观（AGENTS.md 的组件优先） |
| 内存 epoch 作废在飞的写入，而不是清空时等待所有读取结束 | 多一个 epoch map 与两处比对；换来清空不会被一个慢速 APK 解析拖住几十秒 |
