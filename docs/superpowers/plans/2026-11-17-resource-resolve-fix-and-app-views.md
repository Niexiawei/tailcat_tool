# 资源页：修掉「解析应用信息」一直转圈，并加应用/进程分段器与筛选

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 三件事，按优先级：

1. **修掉「解析应用信息」一直转圈无响应**（用户报的 bug，截图里那一行 `android.hardware.audio.service`）。
2. 资源页的应用列表**新增「应用 / 进程」分段器**，两栏各自成表。
3. 应用列表**新增模糊筛选**输入框，同时作用于两个视图。

**根因已定位并实测**（见下节），不是猜的：两处真实缺陷叠加，再加三条放大因素。

**技术栈：** Go、`adb exec-out`（经 `core.FileRunner`）、`github.com/avast/apkparser`、`components.Tabs` + `TabsSegmented`、`components.TextBox` + `LeadingIcon`、`components.Table` + `TableRow`、`ui.NewTester`。

**规格：** 本计划由对话需求（截图 + 文字）与本文的根因实测直接得出；权衡见「决策与代价」。

---

## 0. 根因调查报告（先看这个再动手）

### 0.1 复现环境

与用户截图同一台设备：`127.0.0.1:5555`，Android 11 车载模拟器，`121 个应用 · 138 个进程`
（与截图工具栏文字逐字一致）。只读命令：`pm path`、`ps`、`unzip -p`、`stat`。

### 0.2 实测数据

| # | 观测 | 结果 |
|---|---|---|
| 1 | 进程组能被 `pm path` 解析（即**可能有**应用名） | **19 / 121** |
| 2 | 进程组 `pm path` 失败（**永远拿不到**应用名） | **102 / 121（84%）**，含截图高亮那行 `android.hardware.audio.service` |
| 3 | `adb shell "unzip -p <apk> AndroidManifest.xml"`（gms） | **739233 字节**，条目真实大小 **724620**，多出的 **14613 = 该文件里 LF 的个数** |
| 4 | 同一条目走 `adb exec-out` | **724620 字节**，逐字节正确 |
| 5 | 现行 `readApkEntries` 解析 gms | `parse arsc err="chunk ... claims 67143040 bytes"`、`parse xml err="string count ... only 322500 available"` → `ok=false` |
| 6 | 于是落到整包回退：`exec-out cat base.apk`（**158 MB**） | **14.65 s**（局域网 adb，**不是** tailcat）；图标仍然丢失（`icon=0`） |
| 7 | 同样两个条目改走 `exec-out` | **1.31 s**，`label="Google Play services"` |
| 8 | `com.android.car.carlauncher` 前后对比 | 2.63 s → **0.29 s**，图标 312 B 也拿到了 |

**后续更正（2026-11-17，同一设备）：** 第 2 行的 102 / 121 是用只查 user 0 的 `pm path` 量出来的，
把「装在第二个用户（`UserInfo{10:Driver}`）下的应用」也算成了没有应用包。改用
`pm list packages --user all` 后是 **96 / 120**。计数口径错了，结论就跟着错——所以第 2 行的
「永远拿不到名字」只对真正的系统进程成立，对跨用户安装的应用不成立（那是一个独立的缺陷，
已单独修复，见 `internal/appmeta/store.go` 的 `apkPathFromListing`）。

### 0.3 根因

**根因 A（让「能解析的应用」也解析不出来）：`adb shell` 有 PTY，把二进制条目里的 LF 全换成 CRLF。**
`appmeta.store.extractEntry` 用的是 `adb … shell "unzip -p …"`。`adb shell` 会给远端命令分配 PTY，
`onlcr` 把 `\n` 译成 `\r\n`——**每一份二进制条目都被污染**（第 3 行实测：多出的字节数恰好等于 LF 个数）。
`apkparser` 于是必然解析失败，`readApkEntries` 恒返回 `ok=false`，
每一次「解析应用信息」都退化成**整包回退**（第 6 行：158 MB / 14.65 s）。

`docs/device-detail-page.md` §3.2 写的「第 1 级用 `RunToFile`（二进制安全）」是错的：
`RunToFile` 只保证**不经文本捕获**，挡不住**设备侧 PTY 的 CRLF 翻译**。那个「0.13 s / 127 KB」只量了传输，
没量解析结果——所以这个「快路径」从写出来那天起就一直是死代码。
经 tailcat 转发时整包回退更糟：几 MB/s × 158 MB 远超 `apkPullTimeout(60s)`，于是**静默超时**。

**根因 B（让所有失败都看起来像「还在转」）：解析结果是 fire-and-forget，界面只能瞎等。**
`core.AppMetaSource.Refresh` 没有返回值、没有完成信号、没有失败原因。行内 Spinner 完全靠
`resourceState.resolving[pkg]` 这个**本地时间戳**驱动，唯一的出口是 `resolveTimeout = 2 * time.Minute`。
于是：
- 第 2 行那 102 / 121 个「根本不是应用包」的进程（`surfaceflinger`、`system_server`、`android.hardware.*.service`…）
  点一下就**必然**转 2 分钟，然后按钮原样回来，没有任何解释；
- 整包回退超时（tailcat 上的大应用）同样是转满 2 分钟、什么也没有；
- 2 分钟后再点，`Store.failed` 的 10 分钟退避虽然会挡下，但 `Refresh` 会 `delete(s.failed, key)`，
  于是**又跑一遍同样注定失败的读取**，再转 2 分钟。这就是「依旧一直转圈无响应」。

### 0.4 放大因素

1. **整包回退无上限**：`maxAPKSize = 400 MB` 只挡「解析」，不挡「传输」；`apkPullTimeout = 60s` 到点就丢结果，不看进度。
2. **批量刷新抢队列**：`workers = 2`，FIFO 单队列。工具栏「刷新应用信息」一次塞 121 个包，
   行内单点请求排在全批之后——即使该应用只要 0.3 s，用户也要等整批。
3. **每行都提供「解析应用信息」**：包括 84% 注定失败的进程行；按钮没有「这次一定失败」的表达能力。

### 0.5 不修的东西（本轮明确不做）

- **不做应用名的批量预取提速**（如按 `pm list packages -f` 预取路径）：条目路径修好后单应用已降到 ~0.3 s，够用。
- **不做矢量（自适应）图标光栅化**：gms 这类 `@7f0802b6` / XML drawable 仍然只拿到名字、图标留空（首字母头像）。
- **不做缓存管理页**（仍是下一轮）。

---

## 1. 全局约束

- 纯 Go、禁止 cgo；`CGO_ENABLED=0 GOOS=linux|darwin|windows go build ./...` 必须通过。
- 依赖方向不变：`internal/appmeta` 实现 core 的接口，**不得** import `internal/app`；`internal/app` 不得 `exec.Command`。
- 设备命令一律走 `core.Runner` / `core.FileRunner`；**二进制条目必须走 `exec-out`**，不许再写 `adb shell <读二进制>`。
- 面向用户文案是中文，集中在 `internal/core/messages.go` 与视图，测试逐字断言。
- TDD 硬要求：先写会失败的测试（`go test` 报 undefined / 断言失败），跑一次确认失败原因，再写实现。
- 任何用户可见改动同一提交内更新 `docs/CHANGELOG.md`；本文 §0 的实测结论同步进 `docs/device-detail-page.md`。
- `gofmt -l .` 无输出、`go vet ./...`、`go test ./...`、`go test -race ./internal/core ./internal/app ./internal/appmeta` 先绿再提交。
- 分支：`feat/resource-resolve-and-views`（不要直接推 `main`）。

---

## 2. 审查重点（Review Focus）

每条都在对应任务里有钉住它的测试。

1. **二进制条目绝不能经过 `adb shell`**：断言命令行里是 `exec-out`，且**没有** `shell` 这个词。
2. **解析结果必须可查**：`Pending` / `Ready` / `Failed(reason, final)` 三种终态，界面只读状态、不自带计时器。
3. **「设备上没有应用包」是终态**：不再 10 分钟一次地重试注定失败的 `pm path`；只有「重试」按钮、重连、或重启才重新确认。
4. **Spinner 必须有确定终点**：读到、读失败、包不存在——三种都会让 Spinner 在两秒内消失。
5. **整包回退有上限**：超过 `maxWholeAPKPull`（64 MB）不拉，直接报「应用包过大，未读取」，绝不赌 60 秒超时。
6. **行内请求优先于批量**：批量 121 个包在跑时点一行，该行先出结果。
7. **批量进度按「终态」判定结束**，不再用「30 秒」或「命名数量变了」这种旁证。
8. **两个视图各自独立**：列、滚动位置、表格状态互不串味；进程视图里每个进程恰好出现一次。
9. **筛选是纯函数**：中文名、包名、`:remote` 子进程、大小写、多关键词都要有表驱动用例；无匹配时给文案而不是空白。
10. **任何异步分支仍要校验目标**：`ResolveAppMeta` / 批量刷新继续走 `operationTarget` + `validateTarget`。

---

## 3. 决策与代价

| 决定 | 代价 |
|---|---|
| 条目读取从 `shell unzip -p` 改为 `exec-out unzip -p` | 没有 `unzip` 的设备仍然回退整包；`exec-out` 不经过 shell，路径里不能有 shell 展开（当前都是单引号包裹的绝对路径，安全） |
| 新增 `core.AppMetaStatus`（`State`/`Reason`/`Final`）与 `AppMetaSource.Status`/`Forget` | core 接口多两个方法，`demoAppMeta` 与 stub 都要实现；状态存在内存里，进程重启即清空 |
| 「没有应用包」记为 `Final`，批量刷新跳过 Final，行内「重试」仍可强制重查 | 用户新装应用后，旧进程行要等断开/重连（`Forget`）或重启才会重新有机会解析 |
| 删掉视图里的 `resolving` 时间戳与 `resolveTimeout = 2min` | 界面不再自带兜底计时；依赖 store 一定要写终态（worker 用 `defer` + `recover` 保证） |
| 失败行显示「无应用包」/「读取失败」+ Tooltip 原因 | 表格多一列宽度的灰色小字；不再对注定失败的进程提供按钮 |
| 整包回退上限 64 MB（`maxWholeAPKPull`） | 无 `unzip` 且 APK > 64 MB 的设备上，这类应用永久只能显示包名 |
| 行内请求走独立 urgent 队列，worker 优先取 urgent | 队列从一条变两条；urgent 被狂点时批量会饿着（可接受，用户在场） |
| 「应用 / 进程」用 `components.Tabs` + `TabsSegmented` | 顶部再多一行控件；两个视图各存一份 `TableState` |
| 筛选用子序列（fuzzy）匹配，空格分词 AND | 「zz」能匹配到名字里夹着 z 的行，属于 fuzzy 的固有噪声；用稳定排序（仍按 CPU）降低跳动 |
| 筛选时按查询自动展开分组，且只列出命中的进程 | 与「记住用户展开状态」有冲突：查询激活期间 `expanded` 被忽略，清空后恢复 |
| 计数文案在筛选时显示匹配数（`n 个应用 · m 个进程`） | 不再同时显示总数，用户清空输入即可看到全量 |

---

## 4. 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/core/appmeta.go` | 修改 | 新增 `AppMetaState` 常量、`AppMetaStatus`、`AppMetaSource.Status`/`Forget` |
| `internal/core/messages.go` | 修改 | 新增失败原因常量（`ReasonNoAppPackage` 等）与行内短标签 |
| `internal/core/sample.go` | 修改 | `Manager.AppMetaStatus`；`RefreshAppMeta` 跳过 `Final` 的包；`ResolveAppMeta` 保持强制 |
| `internal/core/manager.go` | 修改 | `Disconnect` / `Shutdown` 调 `appMeta.Forget(deviceKey(id))` |
| `internal/appmeta/store.go` | 修改 | 条目改 `exec-out`；`status` 表取代 `pending`/`failed`；`Status`/`Forget`；urgent 队列；整包上限；worker `defer`+`recover` |
| `internal/appmeta/store_test.go` | 修改 | 新用例见任务 2；保留并改造既有退避用例 |
| `internal/app/resources.go` | 修改 | 状态机替换盲等；分段器；筛选框；两个表格；批量进度 |
| `internal/app/resources_test.go` | 修改 | 新用例见任务 3–7 |
| `internal/app/state.go` | 修改 | `resourceState` 增 `view`/`filter`/`tableProcesses`/`scrollProcesses`/`batch*`，删 `resolving` |
| `internal/app/filter.go`（新） | 新增 | 纯函数 `matchQuery` 与 `filterApps`/`filterProcesses` |
| `internal/app/filter_test.go`（新） | 新增 | 表驱动 |
| `internal/app/demo.go` | 修改 | `demoAppMeta` 实现 `Status`/`Forget`；demo 采样里加一个系统进程，`--demo` 就能看到「无应用包」 |
| `internal/compat/compat_test.go` | 修改 | 仅当用到新组件（`TextBoxOptions.LeadingIcon` 已有使用则跳过）时补编译期断言 |
| `docs/device-detail-page.md` | 修改 | §3.2 更正 PTY/CRLF 结论、补 §0 实测表、补分段器与筛选的说明 |
| `docs/CHANGELOG.md` | 修改 | 「修复」「新增」「变更」各若干条（草案见 §9） |

---

## 5. 任务

### 任务 1：core 的解析状态与透出

- [x] 写测试 `internal/core/resources_refresh_test.go`：
  - `TestAppMetaStatusIsUnknownUntilAsked`：stub 源没被问过 → `Manager.AppMetaStatus` 返回 `ok=false`。
  - `TestAppMetaStatusIsReportedThroughTheManager`：stub 返回 `Pending`/`Failed{Reason,Final:true}` → 原样透出（`deviceKey` 断言：用设备 uuid 而不是序列号）。
  - `TestDisconnectForgetsAppMetaStatus`：`Disconnect` 后 stub 的 `forget` 被调用一次，且 deviceKey 与 `Lookup` 用的一致。
  - `TestRefreshAppMetaSkipsPackagesWithNoAppPackage`：快照里 3 个包，其中 1 个 `Final` → 只有 2 个进 `Refresh`。
- [x] 跑一次确认 RED。
- [x] `internal/core/appmeta.go`：

```go
type AppMetaState int

const (
    AppMetaUnknown AppMetaState = iota // 从没问过
    AppMetaPending                     // 正在读
    AppMetaReady                       // 有名字（可能 stale）
    AppMetaFailed                      // 读失败，Reason 说明原因
)

type AppMetaStatus struct {
    State  AppMetaState
    Reason string    // 面向用户的中文原因，Failed 时非空
    At     time.Time // 进入该状态的时刻
    Final  bool      // 重试也无用（设备上没有这个应用的应用包）
}

type AppMetaSource interface {
    Lookup(device, pkg string) (AppMeta, bool)
    Request(device, adbPath, serial string, pkgs []string)
    Refresh(device, adbPath, serial string, pkgs []string)
    // Status reports what is known about one package right now.
    Status(device, pkg string) (AppMetaStatus, bool)
    // Forget drops every status this device accumulated: after a reconnect a
    // process that had no APK may be a real application.
    Forget(device string)
}
```

- [x] `internal/core/sample.go` 加 `Manager.AppMetaStatus(id, pkg)`；`RefreshAppMeta` 过滤 `Final`：

```go
for _, app := range snapshot.Apps {
    if status, known := source.Status(deviceKey(id), app.Package); known && status.Final {
        continue
    }
    pkgs = append(pkgs, app.Package)
}
```

- [x] `internal/core/manager.go` 的 `Disconnect` 与 `Shutdown` 在放开 `m.mu` 之后调 `Forget`（注意：**不要在锁里调**）。
- [x] 验收：`go test ./internal/core/` 绿。

### 任务 2：`appmeta` 走 `exec-out` + 真实状态

- [x] 写测试 `internal/appmeta/store_test.go`：
  - `TestStoreReadsEntriesThroughExecOut`：假 runner 只对 `Args[0:2] == {"exec-out", "unzip"}` 返回条目内容 → `ok=true`；**同时断言命令里没有 `shell`**（这是钉住根因 A 的那条）。
  - `TestStoreReportsNoAppPackageAsFinalFailure`：`pm path` 输出为空 → `Status` = `Failed{Final:true, Reason: core.ReasonNoAppPackage}`。
  - `TestStoreReportsPendingWhileReading`：runner 阻塞在第一个命令上时 `Status == Pending`。
  - `TestStoreSkipsPackagesKnownToHaveNoAppPackage`：连续两次 `Refresh`，第二次不再产生任何 `pm path` 调用；`Refresh` 显式强制时可以再问（行内重试用）。
  - `TestStoreRetriesAFailedFetchAfterTheBackoff`（改造既有 `…BacksOff…`）。
  - `TestStoreGivesUpOnAPKsTooLargeToPull`：`stat` 报 80 MB、条目读不出 → `Failed{Reason: core.ReasonApkTooLarge}`，且**没有** `exec-out cat` 调用。
  - `TestStoreServesRowRequestsBeforeTheBatch`：批量塞 5 个包（runner 阻塞），再发 1 个 urgent，断言 urgent 先完成。
  - `TestStoreMarksAFailedStateWhenTheWorkerPanics`：让 `fetch` 里（测试钩子）panic，`Status` 仍落到 `Failed`。
- [x] 跑一次确认 RED。
- [x] `extractEntry` 改：

```go
Args: []string{"-s", queued.serial, "exec-out", "unzip", "-p", apk, entry},
```

  注释里写清为什么：*`adb shell` 分配 PTY，会把条目里的每个 LF 翻成 CRLF（实测 724620 字节的清单变成 739233），
  `apkparser` 因此永远解析失败；`exec-out` 不经过 PTY。*
- [x] `Store` 字段：`status map[string]core.AppMetaStatus` 取代 `pending`/`failed`；`forced` 保留；新增 `urgent chan job`。
  - `enqueue(..., force bool)`：`Ready` 且新鲜 → 跳过；`Failed{Final:true}` 且非 force → 跳过；`Failed` 且在 `failedRetryAfter` 内且非 force → 跳过；`Pending` → 跳过（force 时置 `forced[key]` 让在跑的那个重读）。
  - `work()`：先取 urgent，再取批量；开始时写 `Pending`，结束时写 `Ready` 或 `Failed{Reason}`；`defer` 里 `recover()` 兜底写 `Failed`。
- [x] `fetch` 拆出失败原因：`apkPath` 失败 → `ReasonNoAppPackage, Final:true`；`pm path` 命令本身报错（adb 掉了）→ `ReasonAppPackageUnreadable, Final:false`；`size > maxWholeAPKPull(64<<20)` 且条目读不出 → `ReasonApkTooLarge`。
- [x] `Status`/`Forget` 实现；`Forget` 只清 `status`/`forced`，**不动磁盘缓存**。
- [x] 验收：`go test ./internal/appmeta/` 绿；真机复测（只读）：`LIVE_ADB=<serial>` 临时用例量一次 `com.google.android.gms`，应当 ≤ 2 s（基线 14.65 s），量完删掉临时文件。

### 任务 3：视图只读状态，Spinner 有确定终点

- [x] 写测试 `internal/app/resources_test.go`：
  - `TestRowShowsSpinnerWhileTheReadIsPending`：stub `Status` = `Pending` → 该行有 Spinner、没有按钮。
  - `TestResolvingSpinnerEndsWhenTheReadFails`：同一行先 `Pending` 再 `Failed{Final:true}`，重建 tester 后 Spinner 消失、出现「无应用包」，且**没有**按钮（钉住「不再盲等 2 分钟」）。
  - `TestRowOffersRetryAfterATransientFailure`：`Failed{Final:false}` → 有「重试」图标按钮，点击进 `Refresh`。
  - `TestRowStillOffersResolveWhenNothingIsKnown`：`Status` 未知 → 按钮照旧。
  - 删除/改写 `TestProcessTableShowsASpinnerWhileResolving`（原来靠本地时间戳，现在靠 stub 状态）。
- [x] 实现：`appCell` 改成 `switch`；删 `resolving`、`resolveTimeout`；失败文案用 `core.ReasonNoAppPackage` 的短标签（`无应用包`）与 Tooltip 全文。
- [x] 验收：`go test ./internal/app/ -run 'TestRow|TestResolving'` 绿。

### 任务 4：批量刷新按终态收敛 + 优先队列可见效果

- [x] 写测试：
  - `TestRefreshProgressEndsWhenEveryRowIsSettled`：stub 先全部 `Pending`，逐个转成 `Ready`，最后一个转完时「正在刷新应用信息…」消失（不再依赖 30 s 或命名数量）。
  - `TestRefreshSaysHowManyAreDone`：`正在读取应用信息 2/5`。
  - `TestRefreshDoesNotAskAboutProcessesWithNoAppPackage`：`manager.RefreshAppMeta` 只把非 Final 的包交给源。
- [x] 实现：`resourceState` 删 `refreshAt`/`refreshSeen`/`settleRefresh`，改 `batchPkgs []string`；工具栏文案改成带进度的；结束时清空。
- [x] 验收：`go test ./internal/app/ -run TestRefresh` 绿。

### 任务 5：「应用 / 进程」分段器

- [x] 写测试：
  - `TestResourceViewSwitchesBetweenAppsAndProcesses`：点「进程」后表格里出现进程行、进程数等于样本里的进程总数（`138`），再点「应用」复原。
  - `TestProcessViewListsEveryProcessOnce`：不重不漏（用名字集合断言）。
  - `TestProcessViewKeepsItsOwnColumnsAndScroll`：进程视图列头是「进程 / 应用 / CPU / GPU / 内存」，切回应用视图列头复原。
- [x] 实现：
  - `state.go`：`const (resourceViewApps = iota; resourceViewProcesses)`；`resourceState` 增 `view int`、`tableApps`、`tableProcesses`、`scrollApps`、`scrollProcesses`。
  - `resources.go`：工具栏第一行用 `components.Tabs(c, &state.view, components.TabsSegmented, "应用", "进程")`；
    进程视图平铺 `snapshot.Apps` 展开后的 `ProcessUsage`，按 CPU 降序，行内「进程」格显示名字 + 灰色 `PID <n>`。
  - 进程视图不提供展开；应用视图保持现状。
- [x] 验收：`go test ./internal/app/ -run 'TestResourceView|TestProcessView'` 绿。

### 任务 6：模糊筛选

- [x] 写测试 `internal/app/filter_test.go`（表驱动，纯函数）：
  - `TestMatchQuery`：`{"微", "微信"}`→true；`{"wx", "通话"}`→false；`{"com.ex", "com.example.app"}`→true；
    `{"mp", "com.google.android.apps.maps"}`→true（子序列）；`{"Car Launcher", "car launcher"}`→true（大小写）；
    `{"car maps", "Car Launcher"}`→false（多词 AND）；`{"", 任意}`→true。
  - `TestFilterAppsKeepsGroupsMatchedByAProcess`：查询只命中子进程名 → 该组保留，且只列出命中的进程。
  - `TestFilterIgnoresExpandedStateWhileTheQueryIsActive`：没展开的组在查询下也列出命中的进程。
  - `TestFilterWithNoMatchSaysSo`：`没有匹配的应用`。
- [x] 实现：`filter.go` 的 `matchQuery(query string, fields ...string) bool`（空格分词、全部命中、逐字符子序列、`strings.ToLower`）；
  `filterApps(apps, query)` / `filterProcesses(apps, query)` 返回视图直接可渲染的结构。
- [x] 视图：工具栏 `components.TextBox(c, &state.filter, components.TextBoxOptions{LeadingIcon: lucide.Search()})`
  加 `Placeholder("筛选应用或进程")`、`Label("筛选应用或进程")`；计数文案随筛选变化；两个视图共用同一个输入。
- [x] 验收：`go test ./internal/app/ -run TestMatchQuery -v`、`go test ./internal/app/ -run TestFilter` 绿。

### 任务 7：demo、compat、文档、CHANGELOG

- [x] `demo.go`：`demoAppMeta` 实现 `Status`/`Forget`（`com.demo.camera` → `Ready`，`system_server` → `Failed{Final:true}`）；
  demo 采样输出里保留系统进程，`--demo` 下就能看到「无应用包」与两栏表格。
- [x] `internal/compat/compat_test.go`：`components.Tabs`/`TabsSegmented`/`TextBoxOptions.LeadingIcon` 已有使用则不重复加；新用到的组件必须加编译期断言。
- [x] `docs/device-detail-page.md`：
  - §3.2 更正：**二进制条目必须走 `exec-out`**；附 §0 的 739233 / 724620 / 14613 三组数字；
  - 新增一节「应用元数据的三种终态」与「分段器、筛选」；
  - §2.2 的「`unzip -p` 0.13 s」补一句：那只量了传输，没量解析。
- [x] `docs/CHANGELOG.md`（同一次提交）：
  - **修复**：`解析应用信息不再一直转圈：读取失败（如系统进程没有应用包、应用包过大）会在该行直接说明原因`
  - **修复**：`应用名称与图标改为只读 APK 里的清单与资源表，不再整包下载，单个应用从十几秒降到一秒以内`
  - **新增**：`资源页新增「应用 / 进程」切换，进程视图一行一个进程，含 PID、所属应用与占用`
  - **新增**：`资源页新增筛选框，可按应用名、包名或进程名模糊筛选，空格分隔多个关键词`
  - **变更**：`「刷新应用信息」跳过设备上并不存在的应用包，并显示已读进度`
- [x] 验收：`gofmt -l .` 无输出、`go vet ./...`、`go test ./...`、`go test -race ./internal/core ./internal/app ./internal/appmeta`。

---

## 6. 真机/模拟器复测清单（只读命令）

```sh
# 1. 条目路径：exec-out 与 shell 的字节数差异
apk=$(adb -s <serial> shell 'pm path com.google.android.gms' | head -1 | sed 's/package://')
adb -s <serial> shell  "unzip -p '$apk' AndroidManifest.xml" | wc -c   # 会被 PTY 加 CRLF（偏大）
adb -s <serial> exec-out unzip -p "$apk" AndroidManifest.xml | wc -c   # 与 unzip -l 的条目大小一致

# 2. 有多少行注定解析不出名字
adb -s <serial> shell 'for d in /proc/[0-9]*; do c=$(tr "\0" "\n" < $d/cmdline 2>/dev/null | head -1); \
  [ -n "$c" ] || continue; n=${c##*/}; n=${n%%:*}; n=${n%%--*}; n=${n%%=*}; echo "$n"; done | sort -u \
  | while read -r p; do pm path "$p" >/dev/null 2>&1 && echo OK || echo FAIL; done | sort | uniq -c'
# 本机基线：19 OK / 102 FAIL

# 3. 界面
go run ./cmd/tailcat-manager        # 真实设备；点一行「解析应用信息」，应 ≤ 2 s 出名字或直接说原因
go run ./cmd/tailcat-manager --demo # 应能看到「无应用包」行、应用/进程切换、筛选
```

---

## 7. 自检

- [x] 每条「审查重点」都能指出一条测试名。
- [x] 没有测试依赖真设备或网络：所有新用例用 stub / 假 runner；真机只用只读命令手工复测。
- [x] `extractEntry` 的注释里写清 PTY/CRLF 结论，避免下一个人又改回 `shell`。
- [x] 视图里不再有 `time.Since(*time.Time)` 之类的兜底计时（批量进度的 90 s 兜底除外，且必须是**唯一**一处）。
- [x] `resourceState` 里没有跨设备的共享状态；切换设备后视图状态各自独立。
- [x] `grep -rn "unzip -p" internal/` 只出现在 `exec-out` 的调用点上。

---

## 8. 风险

| 风险 | 缓解 |
|---|---|
| 某些设备的 `unzip` 是别的实现，`exec-out unzip -p` 参数不兼容 | 保持整包回退；失败原因写进状态，不再静默。真机复测清单第 1 条可当场验证 |
| `exec-out` 不经 shell，含空格的 APK 路径可能被拆参 | APK 路径一律来自 `pm path`，不含空格；实现时仍用 `Args` 逐项传参，不做字符串拼接 |
| 状态表常驻内存（每设备 × 每包一条） | 单条 < 100 字节，121 行 × 数十设备仍在 KB 级；`Forget` 在断开时清 |
| 删掉 2 分钟兜底后，若某次 worker 卡死会永远 `Pending` | worker 的 `defer` + `recover` 保证写终态；批量进度的 90 s 兜底只作用于**工具栏文案**，不隐藏行内状态 |
| 筛选与自动采样冲突（滚动位置跳动） | 筛选期间排序仍按 CPU，表格状态由用户操作驱动，不随采样重置 |

---

## 9. 落地顺序

`任务 1 → 2`（先让设备侧与 core 说实话）→ `任务 3 → 4`（界面不再瞎等）→ `任务 5 → 6`（两个新功能）→ `任务 7`（demo/文档/CHANGELOG）。

前四个任务合起来修掉 bug，可以单独提交；后三个是功能，另起提交，便于回滚。
