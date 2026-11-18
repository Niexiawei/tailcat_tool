# Logcat（实时日志流）实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）跟踪进度。

**目标：** 侧栏新增「Logcat」页：选一台**已连接**的设备，实时流式看它 `adb logcat` 的输出，按级别 / 标签 / 内容关键词筛选，可暂停、可跟随最新、可清空缓冲。行只在内存里，不落盘。

**架构：** core 新建一条**流**（不是任务）：`Manager.StartLogcat` 用 `Runner.Start` 跑 `adb -s SERIAL logcat -v threadtime`，输出在管道协程里拆行 → 解析 → 追加进一个 5000 行的环形缓冲；页面每帧向流的 `Snapshot(filter)` 要「最新的 N 条匹配行」。界面是 `internal/app/logcat.go` 一个页面（筛选行 + 表格 + 状态行三步布局），路由 `/logcat`，入口在侧栏设备列表之下、紧跟「日志」。**只有一台设备的流在跑**：换设备、离开页面、设备断开都会关掉它。

**技术栈：** Go 1.27.3、`internal/core.Runner`（已有的进程抽象，`Start` + `*Proc.Stop`）、onevra `components`（`Select` / `Tabs` / `TextBox` / `Switch` / `Badge` / `Button*` / `Tooltip` / `Icon`）、myGo `ui.Table`（经 `internal/app/table.go` 的 `tableCard`，`ListState.FollowEnd`）、`ui.NewTester`。

**规格：** 用户 2026-11 的口头需求（「新增 logcat 功能，实时流式返回，需要支持筛选过滤」「同样使用左侧菜单加详情的形态，详情页可以选择已连接的 adb 设备」）；没有独立规格文档。取舍写进本文「决策与代价」，落地后同步进 `docs/architecture.md` §15 与 `docs/ui-and-console.md`。

---

## 全局约束

- 纯 Go、禁止 cgo；**不引入新依赖**（拆行、解析、过滤都是手写纯函数）。
- 依赖方向不变：`internal/app` 只调 `Manager` / `LogcatStream`，不自己 `exec`、不碰 `adb` 命令拼装。
- logcat **不是 Task**：不进任务面板、不进 `history.db`、不设超时、不受 `maxTaskOutput` 限制（它有自己的 5000 行上限）。
- 行只在内存里。**任何写入磁盘的路径都不要接**；也正因为不落盘，这里不做 `core.Redact`（不写文件就不存在凭据落盘问题）。
- 目标在启动瞬间快照：`StartLogcat` 拿到的 `adb` 路径与 serial 之后不再变；设备断开就停流，**不自动重连、不自动重启**（与 `needsAction` 红线一致）。
- 同一台设备同时只允许一条流：`StartLogcat` 先关旧的再开新的。
- 面向用户的中文文案，测试逐字断言。
- GUI 一律优先 onevra `components`；行式元素 `FillWidth()`，**禁止** `Fill()`；**禁止** `ButtonGhost`。
- 表格只经 `tableCard`（`TestTablesAreBuiltOnlyByTableCard` / `TestNoOnevraTableComponentsAnywhere` 会红）；行高不设、外层不套滚动容器。
- 每个提交：`gofmt -w`、`go vet ./...`、`go test ./...` 全绿；用户可见改动同提交写进 `docs/CHANGELOG.md`。
- 分支 `feat/logcat-page`。

---

## 审查重点（Review Focus）

1. **一行 20 KiB 的畸形日志不能撑爆内存或让页面卡住**：设备可能打出二进制/超长行，拆行器必须有单行上限并截断（`TestLogcatLongLineIsTruncated`）。
2. **没有已连接的设备时绝不能起 adb**：`logcat` 面对一个不在线的 serial 会挂住或刷错误，页面必须只给一句解释（`TestLogcatPageExplainsThereIsNothingToStream`）。
3. **离开页面必须停流**：不停就是后台一直从设备拉数据、吃 Tailnet 带宽，而用户看不到任何东西（`TestLeavingTheLogcatPageStopsTheStream`）。
4. **改筛选条件不能丢缓冲**：级别/标签/关键词只影响显示；把级别从「全部」切到「错误」再切回来，原来的行还在（`TestLogcatFilterChangeKeepsTheBuffer`）。
5. **设备断开后流必须结束**：页面不能一直显示「接收中」，要给「日志流已结束」与「重新开始」（`TestDisconnectStopsTheDeviceLogcatStream`、`TestLogcatPageShowsTheEndedStream`）。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `internal/core/logcat.go`（新建） | `LogcatLevel` 与严重度、`threadtime` 行解析、年份回退、拆行器、`LogcatEntry`、环形缓冲、`LogcatFilter`、`LogcatStream`（`Snapshot` / `Version` / `Clear` / `Close`） |
| `internal/core/logcat_test.go`（新建） | 上表的纯函数与流测试；**不启进程** |
| `internal/core/manager_logcat.go`（新建） | `StartLogcat` / `StopLogcat`、命令行拼装、按设备登记与替换 |
| `internal/core/manager_logcat_test.go`（新建） | 命令行、拒绝未在线设备、替换旧流、断开停流 |
| `internal/core/manager.go`（修改） | `logcats` 字段、`Disconnect` 停流 |
| `internal/core/manager_test.go`（修改） | `fakeRunner` 增加 `Start` 处理器与 `starts` 记录 |
| `internal/app/state.go`（修改） | `LogcatRoute`、`OpenLogcat`、`Logcat *logcatState`、`logcatMu`、帧标记（`beginLogcatFrame` / `settleLogcat`） |
| `internal/app/logcat.go`（新建） | 页面：设备选择、筛选行、表格、状态行、流的生命周期、清空、导出、已结束态 |
| `internal/app/manage.go`（修改） | 侧栏入口「Logcat」与面包屑 |
| `internal/app/detail.go`（修改） | 路由分支；每帧开始/结束时调流生命周期 |
| `internal/app/demo.go`（修改） | `demoRunner.Start` 按 250ms 产出演示 logcat 行，直到 ctx 取消 |
| `internal/app/logcat_test.go`（新建） | 页面、筛选、暂停、跟随、停流、演示 |
| `internal/app/screenshots_test.go`（修改） | 三个截图用例 |
| `internal/compat/compat_test.go`（修改） | `ListState.FollowEnd` / `ScrollToEnd` / `Element.RowClicked` 的编译期断言 |
| `docs/ui-and-console.md`、`docs/architecture.md`、`docs/CHANGELOG.md`（修改） | 文档与变更记录 |

---

## 任务 1：`logcat -v threadtime` 的行解析

**文件：**
- 创建：`internal/core/logcat.go`
- 创建：`internal/core/logcat_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestParseLogcatThreadtimeLine(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	e, ok := ParseLogcatLine("11-18 09:30:01.123  1234  1250 I ActivityManager: Started proc", now)
	// ok 为 true；At = 09:30:01.123 当日；Level = LogcatInfo；PID 1234；TID 1250；
	// Tag = "ActivityManager"；Text = "Started proc"
}

func TestParseLogcatLineKeepsATagWithSpaces(t *testing.T)      // "I OpenGLRenderer: ..." 与含空格的 tag 都取第一个 ": " 之前
func TestParseLogcatRejectsBufferSeparator(t *testing.T)        // "--------- beginning of main" → ok = false
func TestParseLogcatRejectsAnUnrelatedLine(t *testing.T)        // "error: device offline" → ok = false
func TestLogcatTimeUsesTheCurrentYear(t *testing.T)
func TestLogcatTimeFromDecemberStaysInThePast(t *testing.T)     // now = 1 月 5 日，行是 "12-31 23:00" → 上一年
func TestLogcatTimeSkipsAnImpossibleDate(t *testing.T)          // "02-30 10:00" → ok = false
func TestLogcatSplitterKeepsPartialLines(t *testing.T)          // feed("a\nb") 后 feed("c\n") → ["a", "bc"]
func TestLogcatSplitterHandlesCRLF(t *testing.T)
func TestLogcatLongLineIsTruncated(t *testing.T)                // 20000 字节一行 → 一条，长度 ≤ logcatMaxLineBytes + 截断后缀
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestParseLogcat|TestLogcat' -v`
预期：FAIL，`undefined: ParseLogcatLine`。

- [ ] **步骤 3：在 `internal/core/logcat.go` 中实现**

```go
// LogcatLevel is one Android log severity, ordered from chatty to fatal.
type LogcatLevel string

const (
	LogcatVerbose LogcatLevel = "V"
	LogcatDebug   LogcatLevel = "D"
	LogcatInfo    LogcatLevel = "I"
	LogcatWarn    LogcatLevel = "W"
	LogcatError   LogcatLevel = "E"
	LogcatFatal   LogcatLevel = "F"
)

// Rank orders the levels so a filter can ask for "this and above". A level it
// cannot name ranks below Verbose, so an odd letter never hides a line.
func (l LogcatLevel) Rank() int

// Label is the Chinese name the page's level tabs and badges show.
func (l LogcatLevel) Label() string // 详细 / 调试 / 信息 / 警告 / 错误 / 致命

// LogcatEntry is one line logcat printed.
type LogcatEntry struct {
	// Seq numbers the line. Two log lines can be identical, so nothing about
	// the content tells them apart and the table needs this.
	Seq   uint64
	At    time.Time
	Level LogcatLevel
	PID   int
	TID   int
	Tag   string
	Text  string
}

// ParseLogcatLine parses one line of `logcat -v threadtime`:
//
//	11-18 09:30:01.123  1234  1250 I ActivityManager: Started proc
//
// now resolves the year: threadtime prints neither year nor zone. ok is false
// for the buffer separators ("--------- beginning of main"), for continuation
// lines and for anything else it does not recognise.
func ParseLogcatLine(line string, now time.Time) (LogcatEntry, bool)

// logcatSplitter turns the chunks a pipe hands us into whole lines: one read
// can carry half a line or three.
type logcatSplitter struct{ pending []byte }
func (s *logcatSplitter) feed(chunk string, emit func(string))
func (s *logcatSplitter) flush(emit func(string))

// logcatMaxLineBytes bounds one line. A device can print a runaway line, and
// a filter that never matches it would still keep it forever.
const logcatMaxLineBytes = 8192
// logcatLineTruncated is what a longer line says at its end.
const logcatLineTruncated = "…（已截断）"
```

解析要点（唯一算法，写死以免实现者自创）：按空格切出 `date time pid tid level`，剩下的整段按**第一个** `": "` 切成 tag 与 text；`date` 用 `time.Parse("01-02")` 精确校验（拒绝 `02-30`）；`pid`/`tid` 用 `strconv.Atoi`，失败即不认；`time` 用 `"15:04:05.000"`。年份回退：先用 `now.Year()` 组装，若结果晚于 `now.Add(24*time.Hour)` 则减一年。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestParseLogcat|TestLogcat' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/core/logcat.go internal/core/logcat_test.go
git commit -m "feat: parse logcat threadtime lines and split pipe chunks"
```

---

## 任务 2：环形缓冲、过滤与 `LogcatStream`

**文件：**
- 修改：`internal/core/logcat.go`
- 修改：`internal/core/logcat_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestLogcatBufferKeepsTheNewestLines(t *testing.T)          // limit 3，写入 5 行 → 3 条最新；Dropped = 2
func TestLogcatContinuationLineJoinsThePreviousEntry(t *testing.T) // 以空格开头的行拼到上一条 Text 后
func TestLogcatContinuationWithoutAPreviousLineIsDropped(t *testing.T)
func TestLogcatFilterKeepsMinimumLevelAndAbove(t *testing.T)     // MinLevel = 警告 → W/E/F
func TestLogcatFilterAnEmptyMinLevelKeepsEveryLevel(t *testing.T)
func TestLogcatFilterMatchesEveryTermAcrossTagAndText(t *testing.T) // Tags="activity" Terms="started proc"
func TestLogcatFilterIsCaseInsensitive(t *testing.T)
func TestLogcatSnapshotCopiesTheBuffer(t *testing.T)            // 改返回值不影响流
func TestLogcatSnapshotKeepsTheNewestMatchingRows(t *testing.T)  // 3000 行匹配、上限 2000 → 最新 2000，顺序旧→新
func TestLogcatClearKeepsTheCounters(t *testing.T)               // Clear 后 Entries 空、Received 不变、Seq 继续递增
func TestLogcatNotifyIsThrottled(t *testing.T)                   // 一次突发 100 块 → notify 调用 ≤ 2 次
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestLogcatBuffer|TestLogcatFilter|TestLogcatSnapshot|TestLogcatNotify' -v`
预期：FAIL，`undefined: LogcatFilter`。

- [ ] **步骤 3：实现**

```go
// LogcatMaxLines is how many lines one stream keeps: how far back the page can
// scroll, and the only bound on the stream's memory.
const LogcatMaxLines = 5000

// LogcatMaxRows is how many of them one frame hands the table. The table is
// virtualized, but the cell function still runs per row on screen and the
// filtered slice is rebuilt whenever lines arrive.
const LogcatMaxRows = 2000

// LogcatFilter is what the page shows of a stream. It never changes what the
// device is asked for, so switching it loses no lines.
type LogcatFilter struct {
	// MinLevel keeps MinLevel and above; "" keeps every level.
	MinLevel LogcatLevel
	// Tags and Terms are space-separated, case-insensitive substrings; every
	// term must hit, and Tags hits the tag while Terms hits the message.
	Tags  string
	Terms string
}

// Match reports whether one line passes the filter.
func (f LogcatFilter) Match(e LogcatEntry) bool

// LogcatSnapshot is what one frame draws.
type LogcatSnapshot struct {
	// Entries is oldest first, newest last, at most LogcatMaxRows.
	Entries []LogcatEntry
	// Received counts parsed lines since the stream started, Clear included.
	Received uint64
	// Dropped counts lines the ring had to evict.
	Dropped uint64
	// Version changes whenever Entries would: a page compares it with what it
	// drew last frame and skips the copy when nothing came.
	Version uint64
	// Exited reports the process ended; Err says why (empty for a clean end).
	Exited bool
	Err    string
}

// LogcatStream is one running `adb logcat`: a process, a ring of its parsed
// lines, and the filter the page applies to them.
type LogcatStream struct { /* mu, entries, limit, seq, received, dropped, exited, err, closed, notify, proc, cancel */ }

// Snapshot copies the newest lines that pass f. It runs on the render path, so
// it copies structs (strings are shared) and never parses.
func (s *LogcatStream) Snapshot(f LogcatFilter) LogcatSnapshot
// Version is cheap enough to ask every frame.
func (s *LogcatStream) Version() uint64
// Clear drops the kept lines; the counters keep counting.
func (s *LogcatStream) Clear()
// Closed reports whether Close has run.
func (s *LogcatStream) Closed() bool
// Close stops the process, cancels its context and marks the stream closed.
func (s *LogcatStream) Close()
// append takes one pipe chunk: split, parse, push.
func (s *LogcatStream) append(chunk string)
```

`notify` 字段是 `StartLogcat` 注入的回调（任务 3），节流到 `logcatNotifyInterval`（`var`，测试可改；默认 100ms）：一次写入只允许一次通知，**最后一块**在流结束时补一次通知，否则尾巴要等下一行才出现。续行判定：`ParseLogcatLine` 说不认识、且首字符是空格或 `\t`、且缓冲里已有行 → 拼进最后一行的 `Text`（以 `"\n"` 连接）。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestLogcat' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/core/logcat.go internal/core/logcat_test.go
git commit -m "feat: keep a bounded, filterable ring of logcat lines"
```

---

## 任务 3：`Manager.StartLogcat` 与断开清理

**文件：**
- 创建：`internal/core/manager_logcat.go`、`internal/core/manager_logcat_test.go`
- 修改：`internal/core/manager.go:Disconnect`、`internal/core/manager_test.go`（只为 `fakeRunner`）

- [ ] **步骤 1：编写失败的测试**

先在 `manager_test.go` 给 `fakeRunner` 加一个 `startHandler func(ctx, Command, func(string)) (*Proc, error)` 与 `starts []Command`（`Start` 记录并转交）。**假 Proc 必须用 `&Proc{PID: 0, Done: done}`**：`PID <= 0` 时 `Stop` 什么都不做，测试不会去动真实进程。

```go
func TestStartLogcatRunsThreadtimeOnTheDevice(t *testing.T)  // 恰为 -s SERIAL logcat -v threadtime，不带 shell、不带 -b
func TestStartLogcatHonoursTheBufferSelection(t *testing.T)   // Buffers = [main, crash] → -b main,crash
func TestStartLogcatRefusesADeviceThatIsNotOnline(t *testing.T) // 未连接 → error，且没有 Start 调用
func TestStartLogcatReplacesTheDeviceStream(t *testing.T)     // 第二次调用让第一条 Closed()
func TestLogcatStreamParsesWhatTheDevicePrints(t *testing.T)   // onOutput 两行 → Snapshot 两条
func TestLogcatStreamReportsAnExitedProcess(t *testing.T)      // 关闭 Done(1) → Exited、Err 非空
func TestDisconnectStopsTheDeviceLogcatStream(t *testing.T)
func TestStopLogcatIsIdempotent(t *testing.T)
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestStartLogcat|TestLogcatStream|TestDisconnectStopsTheDeviceLogcatStream' -v`
预期：FAIL，`undefined: (*Manager).StartLogcat`。

- [ ] **步骤 3：实现**

```go
// LogcatOptions is what a caller asks one device's log stream for.
type LogcatOptions struct {
	// Buffers selects logcat -b buffers; empty keeps logcat's default.
	Buffers []string
	// Limit caps the kept lines; 0 means LogcatMaxLines.
	Limit int
	// OnLines is called (throttled inside the stream) after lines arrive, so
	// the window repaints without one frame per pipe read. Nil is allowed.
	OnLines func()
}

// StartLogcat starts `adb -s SERIAL logcat -v threadtime` for a device and
// returns the stream it writes into. A device has at most one stream: an old
// one is closed first. It is not a Task on purpose — a log stream has no
// timeout, no output history and no place in the task panel.
func (m *Manager) StartLogcat(ctx context.Context, id uuid.UUID, opts LogcatOptions) (*LogcatStream, error)

// StopLogcat closes a device's stream, if one is running.
func (m *Manager) StopLogcat(id uuid.UUID)
```

实现要点：
- 目标用 `m.operationTarget(ctx, id)` 快照（它已要求 `StateOnline` 且 serial 非空）；**不发额外 `get-state`**（`logcat` 自己会在目标消失时退出，页面会显示「已结束」，比多一次往返还准）。
- 命令行：`Command{Path: target.ADB, Args: append([]string{"-s", target.Serial, "logcat", "-v", "threadtime"}, bufferArgs...)}`，`Timeout` 留 0（永不过期）。
- `ctx, cancel := context.WithCancel(ctx)`；`ExecRunner.Start` 忽略 ctx，**演示 runner 靠它退出**，`Close` 两者都做（先 `cancel()` 再 `proc.Stop()`）。
- 登记进 `m.logcats map[uuid.UUID]*LogcatStream`；`Disconnect` 在**释放 `m.mu` 之后**、与 `cancelDeviceTasks` 同一段里调 `StopLogcat(id)` —— 那是外部进程操作，拿锁做会重演历史死锁。
- `runner.Start` 之后起一个协程读 `proc.Done`：`finish(code)` 把 `Exited` 置真、非 0 时 `Err = "adb logcat 退出码 N"`，再补一次 `OnLines`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestStartLogcat|TestLogcat|TestDisconnect' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/core/manager_logcat.go internal/core/manager_logcat_test.go internal/core/manager.go internal/core/manager_test.go
git commit -m "feat: run one streaming logcat per device"
```

---

## 任务 4：路由、侧栏入口与流的生命周期

**文件：**
- 修改：`internal/app/state.go`、`internal/app/manage.go`、`internal/app/detail.go`
- 创建：`internal/app/logcat.go`、`internal/app/logcat_test.go`

- [ ] **步骤 1：编写失败的测试**

测试夹具（`logcat_test.go`）：`newLogcatFixture(t)` 用 `newBlockingViewModel` **换成**支持 `Start` 的假 runner（`&logcatRunner{chunks: make(chan string)}`：`Start` 记下命令、返回 `&core.Proc{PID: 0, Done: done}`，把 `chunks` 里的文本喂给 `onOutput`），再 `Manager.Connect` 让 session 变 `StateOnline`，然后 `vm.OpenLogcat()`。

```go
func TestSidebarListsTheLogcatPage(t *testing.T)              // 侧栏有「Logcat」，点它路由到 LogcatRoute
func TestLogcatPageListsOnlyConnectedDevices(t *testing.T)     // 两台设备只连上一台 → 选择器里只有它
func TestLogcatPageExplainsThereIsNothingToStream(t *testing.T) // 一台都没连 → 「没有已连接的设备…」，且没有任何 Start 调用
func TestLogcatPageStartsAStreamForTheFirstConnectedDevice(t *testing.T)
func TestLeavingTheLogcatPageStopsTheStream(t *testing.T)      // 路由回设备页后流 Closed()
func TestLogcatPageShowsTheEndedStream(t *testing.T)           // Done 关闭 → 「日志流已结束」
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run 'TestSidebarListsTheLogcatPage|TestLogcatPage|TestLeavingTheLogcatPage' -v`
预期：FAIL，`undefined: LogcatRoute`。

- [ ] **步骤 3：实现**

- `state.go`：
  ```go
  // LogcatRoute is the page that streams a connected device's logcat output.
  const LogcatRoute = "/logcat"
  // OpenLogcat shows the streaming log page.
  func (vm *ViewModel) OpenLogcat()
  ```
  `ViewModel` 加 `Logcat *logcatState`（首次访问时建，筛选条件跨访问保留）与 `logcatMu sync.Mutex`、`logcatDrawn bool`。
  每帧生命周期（照 `clearActiveLog` 的写法，`defer` 保证提前 `return` 也算数）：
  ```go
  // beginLogcatFrame marks the streaming page as not drawn yet; the page sets
  // it back while it builds, so a frame that did not draw it closes the stream.
  func (vm *ViewModel) beginLogcatFrame()
  // settleLogcat stops the stream when its page was not drawn this frame.
  func (vm *ViewModel) settleLogcat()
  ```
- `manage.go`：`vm.manageEntry(c, lucide.Activity, "Logcat", LogcatRoute)` 排在「日志」之后、「工具设置」之前；`managePageTrail` 加 `case LogcatRoute: return []string{"Logcat"}, true`。
- `detail.go`：`routedPage` 顶部加 `vm.beginLogcatFrame()`、紧随其后 `defer vm.settleLogcat()`；`Router.View` 里加 `case r.Match(LogcatRoute): r.Title("Logcat"); vm.logcatPage(c)`。
- `internal/app/logcat.go`：
  ```go
  // logcatState is the one streaming log view: what the user chose, what the
  // stream kept, and the table state that holds the column layout and scroll.
  //
  // Only the render path writes these fields: OnLines requests a frame and
  // nothing else, so the only thing crossing a goroutine is the stream itself,
  // which has its own lock. logcatMu guards stream and the frame's drawn mark.
  type logcatState struct {
      Device  *uuid.UUID
      Filter  core.LogcatFilter
      Follow  bool // the 跟随最新 switch; ListState.FollowEnd mirrors it
      Paused  bool
      stream  *core.LogcatStream
      rows    []core.LogcatEntry
      version uint64
      received, dropped uint64
      exited  bool
      err     string
      // filterKey is the filter the rows were read with: a signature that
      // differs means re-read, so a filter change is noticed without a new
      // version from the stream.
      filterKey string
      table ui.ListState
  }

  // logcatPage is the Logcat page: the device, then the filters, then the
  // stream itself.
  func (vm *ViewModel) logcatPage(c *ui.Context)
  ```
  - 设备选择器 `components.Select`：只列 `Manager.Session(id).State == core.StateOnline && Serial != ""` 的设备，label 为 `名称 · serial`。**默认选第一台在线设备**；一台都没有时整页只画一句「没有已连接的设备，先到设备页连接一台。」并且**不起流**。
  - `ensureLogcatStream(f)`：设备为空/已下线/流已关 → 先 `Manager.StopLogcat`；设备在线且 `f.stream == nil` → `Manager.StartLogcat(context.Background(), *f.Device, core.LogcatOptions{OnLines: vm.requestFrame})`。失败则 `f.err = err.Error()`，页面显示 `日志流无法启动：<err>`。启动是 `exec.Start` 级别（毫秒内返回），允许在渲染路径上做（`terminal.New` 已有先例）。`settleLogcat` 关流时把 `f.stream` 置回 nil，下次进页面重新开。
  - 这一任务只画：标题行不必（标题在标题栏）、设备选择器、一个 `logPlaceholder`-式的空态或表格。表格与筛选在任务 5。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app -run 'TestSidebarListsTheLogcatPage|TestLogcatPage|TestLeavingTheLogcatPage' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/state.go internal/app/manage.go internal/app/detail.go internal/app/logcat.go internal/app/logcat_test.go
git commit -m "feat: add the streaming log page and its route"
```

---

## 任务 5：筛选行、表格与跟随／暂停

**文件：**
- 修改：`internal/app/logcat.go`、`internal/app/logcat_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestLogcatPageShowsParsedEntries(t *testing.T)          // 时间 09:30:01.123 / 级别 警告 / PID / 标签 / 正文 全部在内
func TestLogcatLevelTabsKeepThatLevelAndAbove(t *testing.T)    // 切到「错误」→ 只剩 E/F
func TestLogcatTagAndKeywordBoxesFilterTheBuffer(t *testing.T)
func TestLogcatFilterChangeKeepsTheBuffer(t *testing.T)        // 切到「错误」再切回「全部」，原来的行仍在（审查重点 4）
func TestLogcatPauseFreezesTheRows(t *testing.T)               // 暂停期间来的行不显示，继续后显示
func TestLogcatFollowKeepsTheNewestLineInView(t *testing.T)     // ListState.FollowEnd 跟随开关
func TestLogcatFootRowCountsReceivedAndDropped(t *testing.T)     // 「已接收 3 行 · 显示 3 行」
func TestLogcatPageExplainsAnEmptyFilter(t *testing.T)          // 「没有符合筛选条件的日志」
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run 'TestLogcatPageShows|TestLogcatLevel|TestLogcatPause|TestLogcatFollow' -v`
预期：FAIL（页面还没画表格，找不到任何日志行）。

- [ ] **步骤 3：实现**

固定三段布局（`ui.Column(c).Fill().Padding(20).Gap(10)` 内）：筛选行、表格 box（`Grow(1)`，空态也占满）、状态行。

```go
// logcatLevelTabs are the level floors the page offers: 全部 keeps every
// level, the rest keep that level and above.
var logcatLevelTabs = []struct {
	Label string
	Level core.LogcatLevel
}{
	{"全部", ""}, {"调试", core.LogcatDebug}, {"信息", core.LogcatInfo},
	{"警告", core.LogcatWarn}, {"错误", core.LogcatError},
}

// refreshLogcatRows re-reads the stream when something changed. It runs on the
// render path but only copies when the stream's version moved or a filter
// changed: a paused view keeps the rows it shows.
func (vm *ViewModel) refreshLogcatRows(f *logcatState)
```

- 筛选行：设备选择器一行；`级别` 灰字标签 + `components.Tabs(c, &i, components.TabsSegmented, ...)`（5 段）；`标签` / `内容` 两个 `components.TextBox`（`FillWidth().Grow(1)`，占位文案见下）；`跟随最新` `components.Switch`；`暂停 / 继续` `components.ButtonSecondary` 带 `lucide.Pause` / `lucide.Play`。
- 表格：`tableCard(c, &f.table, logcatColumns(), len(f.rows), tableOptions{Label: "Logcat 表", Grow: true}, cell)`；列＝时间（`01-02 15:04:05.000`，宽 150）/ 级别（`components.Badge`：错误与致命 `BadgeDanger`、警告 `BadgeWarning`、其余 `BadgeNeutral`，宽 64）/ PID（64）/ 标签（160）/ 内容（单行省略 + `components.Tooltip` 全文）。`state.Key = func(row int) any { return f.rows[row].Seq }`；`state.FollowEnd = f.Follow`。
- 行序**旧→新**（新行在底部），所以 `FollowEnd` 就是"贴底滚动"，与 `docs/ui-and-console.md` §14 那个倒序日志页相反——这是流的读法，写进文档。
- 状态行：`已接收 N 行 · 显示 M 行`，丢弃过再加 `· 缓冲已满，丢弃 K 行`，暂停时加 `· 已暂停`；右侧 `清空缓冲`（`ButtonOutline`）在任务 6 接上。
- 重绘节奏：`OnLines` 已经节流到 100ms，`refreshLogcatRows` 只在 `Version()` 变化或筛选签名变化时复制；暂停时只更新计数不换行。
- 文案（测试逐字断言）：占位 `标签筛选：如 ActivityManager，空格分隔多个`、`内容筛选：空格分隔的多个关键词都要匹配`；空态 `正在接收日志…`、`没有符合筛选条件的日志`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app -run 'TestLogcat' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/logcat.go internal/app/logcat_test.go
git commit -m "feat: filter the streaming log by level, tag and text"
```

---

## 任务 6：清空、结束态、演示数据、截图与文档

**文件：**
- 修改：`internal/app/logcat.go`、`internal/app/logcat_test.go`、`internal/app/demo.go`
- 修改：`internal/app/screenshots_test.go`、`internal/compat/compat_test.go`
- 修改：`docs/ui-and-console.md`、`docs/architecture.md`、`docs/CHANGELOG.md`
- [ ] **步骤 1：编写失败的测试**

```go
func TestLogcatClearEmptiesTheTable(t *testing.T)          // 点「清空缓冲」→ 空态；「已接收」不变
func TestLogcatPageRebuildsAnEndedStream(t *testing.T)      // 已结束后点「重新开始」→ 新流
func TestDemoLogcatStreamFeedsSyntheticLines(t *testing.T)  // --demo 环境下页面有行（否则截图与人工验收是空的）
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run 'TestLogcatClear|TestLogcatPageRebuilds|TestDemoLogcat' -v`
预期：FAIL（没有「清空缓冲」「重新开始」按钮，演示环境没有流）。

- [ ] **步骤 3：实现**

- 清空：`f.rows = nil`、`f.stream.Clear()`、`f.version` 归零，再刷一帧；计数保留（`Received` 是"这次流一共收到多少行"）。
- 结束态：`Snapshot.Exited` → 表格上方一行 `日志流已结束`（`Err` 非空时 `日志流已结束：<Err>`）+ `重新开始`（`ButtonPrimary`）。**不自动重启**（红线：`needsAction`/断开后不自动连回去），要用户点。
- `demo.go`：`demoRunner.Start` 若命令行含 `logcat`，起一个协程按 250ms 往 `onOutput` 写一条演示行（级别、标签、PID 轮换，正文带计数），`<-ctx.Done()` 时退出；返回 `&core.Proc{PID: 0, Done: done}`，`done` 在协程结束时关闭。真实 `ExecRunner` 忽略 ctx，所以 `LogcatStream.Close` 必须**同时** `cancel()` 与 `proc.Stop()`（任务 3 已写死）。
- `compat_test.go` 补：`_ = ui.ListState{}.FollowEnd`、`_ = (*ui.ListState).ScrollToEnd`、`_ = (*ui.Element).RowClicked`、`_ = components.BadgeOptions{}.Tone`。
- 截图：`Logcat 页（有行）`、`Logcat 页（空态 / 未连接设备）`、`Logcat 页（已暂停）`、`Logcat 页（已结束）`；`TAILCAT_SHOT_DIR=/tmp/shots go test ./internal/app -run TestScreenshots` 后人工看一眼列宽、Badge 颜色、状态行是否被挤出页面。导出用系统保存对话框，没有可截图的内联控件。
- 文档：
  - `docs/ui-and-console.md`：路由表加 `/logcat` 一行；新增「§15 Logcat 页」——三段布局、5 个级别档、行序旧→新与 §14 相反的原因、暂停/跟随/清空的语义、离开页面即停流、只列在线设备；
  - `docs/architecture.md`：§9 补一句「logcat 流不是任务」；§15 决策表加本文「决策与代价」的 5 条（含「导出的是当前筛选结果」）；
  - `docs/CHANGELOG.md` `未发布 / 新增`：两项，页面一条与导出一条（见任务 7 的文案）。

- [ ] **步骤 4：运行测试验证通过**

运行：`gofmt -l . && go vet ./... && go test ./... && go test -race ./internal/core ./internal/app`
预期：无输出 / 全绿。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/logcat.go internal/app/logcat_test.go internal/app/demo.go internal/app/screenshots_test.go internal/compat/compat_test.go docs
git commit -m "docs: record the streaming logcat page"
```

---

## 任务 7：导出为 .txt

**文件：**
- 修改：`internal/app/logcat.go`（按钮、保存对话框、写文件）
- 修改：`internal/app/logcat_test.go`

前置：`saveLocalPath = mygo.Dialog.Save` 已经在 `internal/app/files.go:32` 定义，直接用它（测试里替换这个变量，不打开真对话框）。

- [ ] **步骤 1：编写失败的测试**

```go
func TestLogcatExportWritesTheVisibleRows(t *testing.T)   // 假 saveLocalPath 返回 t.TempDir()/x.txt；文件里是 3 行、含完整日期与 tag
func TestLogcatExportKeepsTheFilter(t *testing.T)          // 只导出当前筛选命中的行，不是整个缓冲
func TestLogcatExportCancelledWritesNothing(t *testing.T)  // 对话框返回 "" → 不建文件、不弹错误
func TestLogcatExportRefusesAnEmptyTable(t *testing.T)     // 没有行 → 「没有可导出的行」，不打开对话框
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run 'TestLogcatExport' -v`
预期：FAIL（页面上没有「导出…」按钮）。

- [ ] **步骤 3：实现**

```go
// exportLogcat asks where to save and writes the rows the page shows: the
// filter is the point of the page, so the file holds what the user is looking
// at, not the whole buffer.
func (vm *ViewModel) exportLogcat(f *logcatState)

// logcatExportText is the file's content: one line per entry, oldest first,
// in the shape a person reads in a text editor.
func logcatExportText(rows []core.LogcatEntry) string
```

- 按钮：状态行右侧「导出…」（`components.ButtonOutline` + `lucide.Download`），排在「清空缓冲」左边；没有行时不画（或者点了给 `vm.FailText("没有可导出的行")`——**选后者**，按钮位置稳定，用户不会看着按钮忽有忽无）。
- 对话框：
  ```go
  target, err := saveLocalPath(mygo.SaveDialogOptions{
      Title:       "导出 Logcat",
      DefaultPath: "logcat-" + time.Now().Format("20060102-150405") + ".txt",
      Filters:     []mygo.FileFilter{{Name: "文本文件", Extensions: []string{"txt"}}},
  })
  ```
- `target == ""` 或 `err != nil` → 什么都不做（用户取消不是错误）。
- 写文件：`os.WriteFile(target, []byte(logcatExportText(f.rows)), 0o600)`，在 goroutine 里做，成功 `vm.Notify("已导出 " + itoa(len(f.rows)) + " 行到 " + target)`，失败 `vm.Fail(err)`。
- 每行格式（写死以免实现者自创）：`2006-01-02 15:04:05.000 I/ActivityManager(1234): text`——**文件里写完整日期**（表格里只有 `01-02`）：一个被翻看的文件需要年份。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app -run 'TestLogcat' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/logcat.go internal/app/logcat_test.go
git commit -m "feat: export the filtered logcat rows to a text file"
```

---

## 验收

- `--demo`：进「Logcat」→ 有演示行滚动 → 切级别只剩该档及更高 → 标签/关键词框立即收窄 → 暂停后行不再变、继续后补上 → 清空后空态 → 导出后打开那个 .txt，内容是屏幕上那一批行 → 侧栏切到别的页再回来，流重新开、筛选条件保留。
- 真机（**本轮唯一的硬未知**）：连一台设备 → 页面自动开始 → 设备上触发一条崩溃（`adb shell am crash` 之类）→ 页面出现 `E AndroidRuntime`；拔线或断开 → 显示「日志流已结束」。
- 带宽：连一台在刷 V 级日志的设备，观察是否把通道打满（这是「级别只在本机过滤」的直接后果，见决策表）。

## 风险

- **设备端不过滤**：级别筛选只在本机做，V/D 级别的日志照样从设备经 Tailnet 传到本机。一台刷屏设备可能占用可观带宽。升级路径：给 `StartLogcat` 加 `*:W` 这样的 spec（需要重开进程、会丢切换瞬间的行），等真机实测确认后再做。
- **同一台设备只能有一条流**：两台设备同时看不在本轮；页面切设备会丢上一台的行（设备缓冲区里的历史还在，重开会重新 dump 一遍）。
- **`-v threadtime` 没有年份与时区**：年份靠"不晚于现在"回退，跨年时刻的初始转储可能差一年（不会出现未来时间）。
- 演示流靠 ctx 取消，真实流靠 `proc.Stop()`：两者都写在 `Close` 里，谁漏了都会在拔线场景下留一个僵死协程。

---

## 决策与代价

| 决定 | 代价 |
|---|---|
| logcat **不是 Task**，走独立的 `LogcatStream` | 任务面板与日志页看不到"我在看日志"；换来没有超时、没有 32 KiB 输出上限、不会把历史库塞满 |
| 筛选**全在本机**（级别/标签/关键词都不过设备） | V/D 行照样占用通道带宽（真正刷屏的设备可能明显）；换来切筛选不重开进程、不丢行、只有一条代码路径 |
| 5000 行环形缓冲、每帧最多 2000 行 | 更早的行找不回来（设备自己的缓冲区里还有，重开可再 dump）；换来内存有硬上限，与设备日志速率无关 |
| 行**不落盘**（不进 `history.db`） | 关掉应用就没了；换来不用为一条每秒几十行的流设计保留期、清理与脱敏，`history.db` 不会被日志流撑大。要留证据走「导出…」: 那是一次显式动作，写去哪里由用户选 |
| 导出的是**当前筛选结果**，不是整个缓冲 | 想看全量得先把筛选清空；换来「我筛完把它存下来」这个最常用的动作不需要额外的“导出范围”选项 |
| 导出用 `mygo.Dialog.Save` 问用户存哪里 | 多一步系统对话框，`--demo` 与无头测试里必须替换 `saveLocalPath`；换来不往用户目录里塞文件、也不发明一个自己的目录约定 |
| 一个页面、一台设备、一条流 | 不能并排看两台设备；换来生命周期只有一处（离开页面/换设备/断开都只停一条流），不会漏关 |
| 只列**已在线**的设备，不列"能连但没连"的 | 想看的设备必须先连上（多一步）；换来不会因为一个离线 serial 让 `logcat` 挂住刷错 |
| 行序**旧→新**（与日志页倒序相反） | 两个日志页面读法不一致，文档要写清楚；换来流就是"往下滚"，`ListState.FollowEnd` 直接可用 |
| 离开页面就停流，回来重开 | 回来时看到的是设备缓冲区里的最近一段，而不是"我刚才看到的位置"；换来不会在后台持续吃带宽 |
| 设备选择器默认选第一台在线设备并**自动开始** | 用户可能并不想现在就看日志（一进页面就有 adb 进程）；换来"选设备→看日志"一步到位，日志页也默认加载 |
| 不自动重启已结束的流 | 用户要手点「重新开始」；换来严格遵守"断开/需要动作后不偷偷重连"的红线 |
| 不做缓冲区选择（main/system/crash）与设备端清空 | 看不到 crash 之外的其它 buffer、不能在设备端 `logcat -c`；换来更少的控件与更少的"我在看哪个 buffer"困惑，两个都是后续可加的小改动 |

---

## 已定的选择（用户 2026-11-18 当面裁定）

1. 入口与面包屑都叫 **「Logcat」**（不叫「实时日志」，界面里这一处保留英文产品名）。
2. **不加**设备页页签里的 Logcat，只做侧栏页面。
3. **要做**导出 .txt，存到哪里由用户在系统保存对话框里选（任务 7）。

---

## 不在本轮范围

- 按包名 / PID 过滤（需要先 `ps` 往返拿 PID，且 PID 会随进程重启变化）。
- 多条流并行（多设备并排）。
- 缓冲区选择（`-b all/system/crash`）与设备端清空（`logcat -c`）。
- 导出为 JSON / CSV，或导出整个缓冲（不做范围选项）。
- 复制单行。
- 把筛选条件持久化到配置（现在活到进程结束，跨访问保留）。
- 设备页页签里的 Logcat（用户已明确不做）。
