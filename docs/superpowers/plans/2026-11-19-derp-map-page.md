# DERP 地图管理页 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）跟踪进度。
>
> **前置：** 本计划依赖 `2026-11-19-appmeta-cache-page.md` 任务 1 落地的「管理入口骨架」（路由常量、侧栏设备列表之下的入口、`ContentTitle` 的面包屑）。骨架没做之前不要开工。

**目标：** 侧栏设备列表之下新增第二个入口「DERP 地图」。列表页集中管理三类来源——官方默认、用户预设、最近用过——可新建/编辑（内联 JSON）、删除（正在被设备使用的不允许），URL 型可强制重新获取；详情页查看区域与节点、原始 JSON、哪些设备在用。

**架构：** core 增加四个方法：`RelayPresetUsage`、`DeleteRelayPreset`、`RelayMapPreview`（抓取/解析一次，结果只进内存）、`CachedRelayMapPreview`（不联网）。预设本身仍只存在 `configuration.json` 的 `relayPresets` 里，**只是 `Source` 字符串**；页面不改设备配置，也不替代 tailcat 连接时的 DERP 获取。UI 两个路由：`/manage/derp`（列表）与 `/manage/derp/{id}`（详情），新建/编辑用一个 `components.Dialog`。

**技术栈：** Go 1.27.3、`net/http`（标准库，无新依赖）、`tailscale.com/tailcfg`（`core.NormalizeRelay` 已在用）、onevra `components`（`Table` / `Dialog` / `TextBox`（`Multiline`）/ `Tabs` / `Badge` / `Collapsible` / `IconButton`）、`httptest`（抓取测试）。

**规格：** 用户 2026-11 的口头需求（列表 + 增删改 + 使用中不可删 + JSON 可编辑 + URL 可强制重新获取 + 详情页）；没有独立规格文档。裁决写进本文「决策与代价」，落地后同步进 `docs/architecture.md` §15（「为什么预览抓取不改设备行为」）与 `docs/ui-and-console.md`。

---

## 全局约束

- 纯 Go、禁止 cgo；不引入新依赖（抓取用 `net/http`）。
- 依赖方向不变；`internal/app` 不直接发网络请求，一切经 `Manager`。
- 抓取上限 **128 KiB**（复用 `core.maxRelayConfigSize`）、超时 10 秒、只接受 `http` / `https`、拒绝带 userinfo 的 URL（`NormalizeRelay` 已挡）——这三条是安全边界，不是建议。
- 抓到的内容只放内存（每进程一份），**不落配置、不落缓存文件**：DERP 地图会变，落盘就会变成"上次的内容冒充当前内容"。
- 不写任何凭据；日志里不打印地图内容（只打 URL 的主机名）。
- 面向用户的中文文案，测试逐字断言。
- GUI 一律优先 onevra `components`；行式元素 `FillWidth()`，**禁止** `Fill()`。
- 每个提交：`gofmt -w`、`go vet ./...`、`go test ./...` 全绿；用户可见改动同提交写进 `docs/CHANGELOG.md`。
- 分支 `feat/manage-pages`（与缓存页同一个分支，骨架共享）。

---

## 审查重点（Review Focus）

每条都要有钉住它的测试，写在对应任务的步骤里。

1. **删除正在被设备使用的地图，必须由 core 拒绝**：UI 的按钮禁用会过期（托盘、另一个页面、临时设备都可能改变使用关系），core 是唯一防线（`TestDeleteRelayPresetRefusesWhenADeviceUsesIt`）。
2. **保存来源必须走 `NormalizeRelay`**：自己 `json.Unmarshal` 会放过区域 ID 与键不一致、HostName 非法、节点数超限的地图，存进配置后设备连不上且错误在连接时才出现（`TestDerpEditorRejectsInvalidJSON`）。
3. **抓取必须有超时与大小上限**：没有上限时一个返回 1 GB 的 URL 会把内存吃光；没有超时会让详情页永远转圈（`TestRelayPreviewRejectsAnOversizedBody`、`TestRelayPreviewGivesUpAfterTheTimeout`）。
4. **抓取失败不等于来源无效**：离线时 URL 打不开，但保存一个合法 URL 必须仍然成功；失败只写在详情页并给「重试」（`TestRelayPreviewFailureKeepsThePresetSaved`）。
5. **详情页必须说清这是"现在下载的一份"，不是设备实际在用的**：URL 内容随时会变，设备连接时由 tailcat 自己再去取（`TestDerpDetailSaysThePreviewIsNotWhatDevicesUse`）。
6. **官方默认不能编辑也不能删除**：它是 tailcat 内置的公共地图，没有本地可改的 `Source`（`TestOfficialRowOffersNoEditOrDelete`）。
7. **编辑正在使用的预设不能悄悄改掉在用设备的来源**：设备存的是 `Source` 字符串，改预设只影响之后选它的人，界面必须写明（`TestDerpEditorWarnsWhenThePresetIsInUse`）。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `internal/core/manager_relay.go`（新建） | `RelayPresetUsage`、`DeleteRelayPreset`、`RelayMapPreview`、`CachedRelayMapPreview` |
| `internal/core/relay.go`（修改） | 抓取与解析（`fetchRelayMap`），复用 `NormalizeRelay` 与 `maxRelayConfigSize` |
| `internal/core/manager_relay_test.go`（新建） | 使用关系、删除、抓取（`httptest`） |
| `internal/app/derp.go`（新建） | 列表页、详情页、编辑器对话框、删除确认 |
| `internal/app/derp_test.go`（新建） | 视图与交互测试 |
| `internal/app/state.go`（修改） | `DerpDetailRoute`、`DerpPage` 状态（选中 id、编辑器草稿、确认删除、预览） |
| `internal/app/detail.go`（修改） | `routedPage` 增加两个分支 |
| `internal/app/manage.go`（修改） | `managePageTitle` 增加 DERP 两条路由的标题 |
| `internal/app/screenshots_test.go`（修改） | 列表、详情、编辑器、删除确认四个用例 |

---

## 任务 1：使用关系与删除

**文件：**
- 创建：`internal/core/manager_relay.go`、`internal/core/manager_relay_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestRelayPresetUsageNamesDevices(t *testing.T)      // 保存设备 + 临时设备都用它 → ["客厅", "临时调试"]
func TestDeleteRelayPresetRefusesWhenADeviceUsesIt(t *testing.T)
func TestDeleteRelayPresetRemovesAnUnusedOne(t *testing.T) // 重开配置库后读不到
```

断言文案逐字：`有 2 台设备正在使用「公司自建」，请先把它们改用其他 DERP 地图`。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestRelayPresetUsage|TestDeleteRelayPreset' -v`
预期：FAIL，`undefined: Manager.DeleteRelayPreset`。

- [ ] **步骤 3：实现**

```go
// RelayPresetUsage names the devices that use a preset. Temporary devices count:
// they hold a Source like any other device while the app runs.
func (m *Manager) RelayPresetUsage(id uuid.UUID) []string

// DeleteRelayPreset removes a preset no device uses. It refuses rather than
// rewriting device configuration behind the user's back.
func (m *Manager) DeleteRelayPreset(id uuid.UUID) error
```

要点：使用关系按 `profile.RelaySource == preset.Source` 判断（不是按 ID，设备存的是 Source）；写法与 `SaveRelayPreset` 一致——先复制切片、`persistLocked()` 失败就回滚、成功才 `m.notify()`；锁内只比字段，不写盘。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: delete DERP presets that no device uses`

---

## 任务 2：抓取与解析（预览）

**文件：**
- 修改：`internal/core/relay.go`、`internal/core/manager_relay.go`
- 修改：`internal/core/manager_relay_test.go`

- [ ] **步骤 1：编写失败的测试**

用 `httptest.NewServer` 返回合法地图（最小可用：1 个区域、1 个节点，`RegionID` 与键一致）：

```go
func TestRelayPreviewParsesInlineMapWithoutNetwork(t *testing.T)
func TestRelayPreviewFetchesAURL(t *testing.T)                 // 断言区域数、节点 HostName、FetchedAt 非零
func TestRelayPreviewRejectsAnOversizedBody(t *testing.T)      // 128 KiB + 1
func TestRelayPreviewReportsHTTPFailure(t *testing.T)           // 500 → 文案含状态码
func TestRelayPreviewGivesUpAfterTheTimeout(t *testing.T)       // 服务端 sleep，超时 ≤ 10s（测试里把 client 换成 50ms）
func TestCachedRelayPreviewNeedsNoNetwork(t *testing.T)         // 关掉 server 后仍能读到
```

- [ ] **步骤 2：运行测试验证失败** → `go test ./internal/core -run TestRelayPreview -v`，FAIL。

- [ ] **步骤 3：实现**

```go
// RelayMapPreview is one DERP source as the detail page shows it.
type RelayMapPreview struct {
	Source    string
	Kind      RelayKind
	JSON      string          // canonical or downloaded JSON, indented
	Map       *tailcfg.DERPMap
	FetchedAt time.Time
}

// RelayMapPreview loads one source now: inline sources parse locally, URLs are
// downloaded. The result is kept in memory only, because a map changes and a
// stale copy would look current.
func (m *Manager) RelayMapPreview(ctx context.Context, source string) (RelayMapPreview, error)

// CachedRelayMapPreview answers without the network.
func (m *Manager) CachedRelayMapPreview(source string) (RelayMapPreview, bool)
```

`fetchRelayMap(ctx, rawURL)` 放在 `relay.go`：

```go
var relayHTTPClient = &http.Client{Timeout: 10 * time.Second}

// 200 才接受；LimitReader 到 maxRelayConfigSize+1；再交给 NormalizeRelay，
// 所以大小、区域数、节点数、HostName 的校验与编辑器完全相同。
```

预览缓存是 `m.relayPreviews map[string]RelayMapPreview`（`m.mu` 保护）；`RelayMapPreview` 失败时**不写缓存**，这样详情页的「重试」就是真的重试。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: preview a DERP map without touching device configuration`

---

## 任务 3：列表页

**文件：**
- 修改：`internal/app/derp.go`、`internal/app/state.go`、`internal/app/detail.go`、`internal/app/manage.go`
- 修改：`internal/app/derp_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestDerpListShowsOfficialPresetsAndHistory(t *testing.T)
func TestDerpListSaysHowManyDevicesUseEachMap(t *testing.T)
func TestOfficialRowOffersNoEditOrDelete(t *testing.T)
```

- [ ] **步骤 2：运行测试验证失败**。

- [ ] **步骤 3：实现**

页面三段（同一列，`components.Table`）：

1. **官方默认**：固定第一行，名称「官方默认节点」，来源「tailcat 内置公共地图」，使用设备数 = `RelaySource == ""` 的设备数，操作列空；
2. **DERP 地图（预设）**：`vm.Manager.RelayPresets()`，列＝名称 / 类型（`components.Badge`：`JSON` / `链接`）/ 来源（URL 显示主机名，JSON 显示「N 个区域 · M 个节点」）/ 使用设备（`RelayPresetUsage` 的台数，0 台时显示 `—`）/ 操作（`详情` `编辑` `删除`）；
3. **最近用过**：`vm.Manager.RelayHistory()`（最多 8 条，自动记录），每行「存为预设」按钮 → 打开编辑器预填该 `Source`。

顶部一行：`components.Button(c, "新建 DERP 地图", components.ButtonPrimary)`（图标 `lucide.Plus`）+ 一句说明「预设只影响之后选择它的设备」。空态（只有官方与历史）时给一句「还没有保存的 DERP 地图」。

`state.go`：`DerpDetailRoute(id uuid.UUID) string`；`detail.go` 的 `routedPage` 加 `r.Match(DerpRoute)` 与 `r.Match(DerpRoute + "/{id}")`；`managePageTitle` 认识这两个路径（详情页标题用预设名称，找不到时「DERP 地图」）。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: list DERP maps in one place`

---

## 任务 4：详情页

**文件：**
- 修改：`internal/app/derp.go`、`internal/app/state.go`（`DerpPreview *core.RelayMapPreview`、`DerpPreviewLoading bool`、`DerpPreviewErr string`）

- [ ] **步骤 1：编写失败的测试**

```go
func TestDerpDetailShowsRegionsAndNodes(t *testing.T)
func TestDerpDetailShowsWhichDevicesUseIt(t *testing.T)
func TestDerpDetailSaysThePreviewIsNotWhatDevicesUse(t *testing.T) // 文案逐字
func TestDerpForceRefetchAsksTheManagerAgain(t *testing.T)          // 第二次点击必须再抓一次
```

- [ ] **步骤 2：运行测试验证失败**。

- [ ] **步骤 3：实现**

打开页面：先 `CachedRelayMapPreview(source)`，没有就起一个 goroutine `RelayMapPreview`，置 `DerpPreviewLoading`，完成后 `vm.Invalidate()`。

页面内容（`ui.Scroll`，整页滚动）：

- 头部：名称（18pt bold）+ 类型 Badge + 「N 台设备在用」（点了跳到设备页的对应设备）；
- 来源一行：URL 用 `components.TextBox(ReadOnly)` 展示，旁边「复制」；内联 JSON 显示「内联 JSON」；
- 说明一句（逐字）：「以下内容是刚刚下载/解析的一份；设备连接时由 tailcat 自行获取，可能已变化。」
- 「重新获取」按钮（`lucide.RefreshCw`）→ 再次 `RelayMapPreview`；旁边的「上次获取：2006-01-02 15:04:05」；
- 失败时：错误行 + 「重试」+ 保留预设本身（不因为抓取失败就删）；
- 区域表（`components.Table`）：区域 ID / 代码 / 名称 / 节点（`HostName:DERPPort`）/ STUN 端口 / `STUNOnly`；
- 原始 JSON：`components.Collapsible` 里放 `components.TextBox(c, &json, components.TextBoxOptions{ReadOnly: true, Multiline: true})`，`Lines(18)`；超过 32 KiB 时只放前 32 KiB 并写「内容过长，只显示前 32 KiB，完整内容可复制来源自行查看」。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: show one DERP map's regions, nodes and JSON`

---

## 任务 5：新建 / 编辑 / 删除

**文件：**
- 修改：`internal/app/derp.go`、`internal/app/state.go`（`DerpEditor *DerpEditorDraft`、`DerpConfirmDelete *core.RelayPreset`）
- 修改：`internal/app/derp_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestDerpEditorSavesAnInlineMap(t *testing.T)          // 保存后预设出现在列表与配置库
func TestDerpEditorRejectsInvalidJSON(t *testing.T)        // 内联错误，弹窗不关
func TestDerpEditorWarnsWhenThePresetIsInUse(t *testing.T)  // 修改在用预设的提示文案
func TestDerpDeleteAsksBeforeRemovingAPreset(t *testing.T)
func TestDerpDeleteRefusalIsShownInTheDialog(t *testing.T)   // 交给 core 拒绝，错误显示在确认框里
```

- [ ] **步骤 2：运行测试验证失败**。

- [ ] **步骤 3：实现**

`DerpEditorDraft{ ID *uuid.UUID, Name, Kind, Content string, Error string }`；`Kind` 是 `"json"` / `"url"`，用 `components.Tabs` + `TabsSegmented` 切换（与设备编辑器的中继分段一致）：

- 名称 `components.TextBox`（1–80 字符，与 core 的规则一致）；
- 内容：`json` 分支用 `components.TextBox(c, &draft.Content, components.TextBoxOptions{Multiline: true})` 并 `.Lines(12)`；`url` 分支单行；
- 保存 → `vm.Manager.SaveRelayPreset(core.SaveRelayPresetRequest{ID: draft.ID, Name: ..., Source: draft.Content})`；**校验只靠 core**，界面不自己解析；
- 编辑在用预设时，弹窗顶部加一行提示（逐字）：「这台地图正被 N 台设备使用。修改只影响之后选择它的设备；已保存的设备仍用原来的来源。」
- 删除：`components.Dialog` 二次确认，确认后调 `DeleteRelayPreset`；返回错误就显示在对话框里（不关），因为"有几台在用"是随状态变化的信息。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: create, edit and delete DERP presets`

---

## 任务 6：截图、文档与变更记录

- [ ] **步骤 1：截图用例**（`shotCases()`）：`DERP 列表（官方 + 2 个预设 + 历史）`、`DERP 详情（区域表 + JSON）`、`DERP 编辑器（JSON）`、`DERP 删除确认（使用中）`。人工看一眼：区域表列没挤成 0 宽、JSON 折叠区能展开、长 URL 有省略号与 Tooltip。
- [ ] **步骤 2：文档** `docs/ui-and-console.md` 的路由表补 `/manage/derp` 与 `/manage/derp/{id}`；`docs/architecture.md` §15 补一行决策：「预览抓取只影响界面，不改设备配置」，代价写「用户可能误以为这里刷新就等于设备换地图」。
- [ ] **步骤 3：CHANGELOG**（`新增`）：
  「侧栏设备列表下新增『DERP 地图』：集中列出官方默认、保存的地图与最近用过的来源；可新建与编辑 JSON 地图，URL 型可立即重新获取并查看区域与节点；正在被设备使用的地图不允许删除。」
- [ ] **步骤 4：Commit** `docs: record the DERP map management page`

### 验收

- `--demo`：列表能打开，新建一个 1 区域地图 → 出现在列表 → 详情能看到区域/节点 → 编辑 JSON 改名字 → 保存后列表更新。
- 真机/本机 `httptest`：URL 型来源「重新获取」两次都真的发出请求（测试用计数 server 断言）。
- 一台设备选用某预设后：该预设的删除按钮显示不可用并说明原因；core 直接调用也拒绝。
- `gofmt -l .` 无输出；`go test ./...` 全绿。

### 风险

- 官方默认地图没有本地副本，详情页对它只能说明"由 tailcat 提供"，无法预览区域与节点；这是能力边界，不是漏做。
- 抓取走用户的网络与代理设置（`http.DefaultTransport` 的 `ProxyFromEnvironment`），企业网里可能失败；失败只影响预览。
- 预设之间可能出现"同一 Source、不同名称"（`SaveRelayPreset` 只保证名称唯一，不保证来源唯一）；列表按 Source 展示，重复来源会被用户看出来，但本轮不做合并（见决策表）。

---

## 决策与代价

| 决定 | 代价 |
|---|---|
| DERP 页与缓存页共用 `manage.go` 的入口与面包屑骨架 | 两个计划有先后依赖（缓存页任务 1 必须先落地）；换来侧栏只有一种"管理入口"外观，不会一页在底栏一页在列表下 |
| 列表把「官方默认」「预设」「最近用过」三段放在同一页 | 页面比单一列表长；换来用户不需要先想"我要找的是预设还是历史"——需求原话就是"管理整合" |
| 删除限制只看设备是否在用，不看最近用过 | 一个刚被弃用的来源可以立刻删掉；换来规则可解释（"有设备在用就不能删"） |
| 编辑正在使用的预设只警告、不阻止 | 用户可能在设备仍连着时改动它，下一次连接才生效；换来不必先切断设备才能修一个写错的地图 |
| 预览抓取放内存，不落盘 | 每次打开详情都要重新下载一次 URL 地图（若本进程没抓过）；换来"看到的就是现在的"，不会用旧内容冒充当前 |
| 原始 JSON 超过 32 KiB 只显示前 32 KiB | 超大地图看不全，要靠复制来源去看；换来一个 `TextArea` 不会被 128 KiB 文本拖慢整帧 |
| 官方默认行没有详情页 | 用户可能想核对官方节点；换来不伪造一份本地副本（tailcat 默认地图随时可能变） |
| 编辑器只做 1–80 字符的名称与一个内容框，不做"从 URL 导入成 JSON" | 想把 URL 固化成 JSON 的用户要自己复制；换来两个来源类型各自可解释，编辑不会把 URL 悄悄换成快照 |
