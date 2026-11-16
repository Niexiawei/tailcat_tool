# 设备资源占用页 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 在设备详情页新增「资源」标签页：显示 CPU / GPU 型号与 CPU、GPU、内存占用环形图，并用表格按应用分组列出设备上所有进程的占用；分组行显示**应用图标 + 应用名称**（不是包名），GPU 读不到时显示「不可用」。包名→（应用名，图标）映射在本地缓存 **7 天**，ADB 在线时可从分组上方的操作栏**手动刷新**这份映射。

**架构：** 三段能力汇到一个页面。
1. **设备内省**：`internal/core/procfs.go` 把 `/proc`、`dumpsys`、vendor sysfs 的文本变成结构化快照；`Manager` 每 2 秒采样一次放内存并 `notify()`。
2. **应用元数据**：新包 `internal/appmeta` 用 `adb` 把进程所属的 base.apk 拉到临时目录，用纯 Go 的 `github.com/avast/apkparser` 解析 `AndroidManifest.xml` + `resources.arsc` 拿 label 与图标 PNG，落**带 7 天有效期的本地缓存**；过期条目自动在后台重抓，界面不退回包名。
3. `internal/app` 只做渲染（`components/chart.Donut`、`components.Table`、`components.Avatar`、`components.Button`/`IconButton`）。

**技术栈：** Go、`adb shell`/`adb exec-out`（经 `core.Runner`）、`github.com/avast/apkparser`（纯 Go，无 cgo）、`components/chart.Donut`、`components.Table` + `components.TableRow`、`components.Avatar`、`components.ScrollView`、`ui.NewTester`。

**规格：** 本计划由对话需求（截图 + 文字）直接得出，没有独立规格文档；设计权衡见本文「决策与代价」，落地后同步进 `docs/architecture.md` 第 15 节。

---

## 全局约束

- 纯 Go、禁止 cgo；`CGO_ENABLED=0 GOOS=linux|darwin|windows go build ./...` 必须通过（`apkparser` 只用了 `unsafe` 做 float 解码，无 cgo）。
- 依赖方向 `cmd → internal/app → internal/core → internal/platform`；`internal/appmeta` 是 core 之外的实现包，只实现 core 定义的接口，**不得** import `internal/app`。
- `internal/app` 不得 `exec.Command`；一切设备命令走 `core.Runner`。
- 面向用户文案是中文；数值与文案在测试里逐字断言。
- 采样与应用元数据都必须绑定启动瞬间的 serial / adb / generation 快照（`operationTarget` + `targetIsCurrent`），设备重连后旧结果不得回写。
- 绝不能持有 `m.mu` 时调用 `m.runner.Run`、`validateTarget`、`recordTask` 等会再上锁的方法。
- 设备离线或没有通道时，资源页显示原因，不显示上一次的残影数据；刷新映射的按钮在离线时必须禁用。
- 缓存有效期常量只有一个：`appmeta.TTL = 7 * 24 * time.Hour`；时间从 `Store.now` 取（默认 `time.Now`，测试注入固定时钟），**不要在业务代码里直接调 `time.Now` 判断过期**。
- 新增目录 `appmeta/` 权限 0700、文件 0600；写缓存必须原子（临时文件 + rename）；拒绝符号链接路径。
- 任何用户可见改动同一提交内更新 `docs/CHANGELOG.md`（新增依赖同时写「依赖」类别 + `docs/build-notes.md` 实测体积）。
- `gofmt -l .` 无输出、`go vet ./...`、`go test ./...` 必须先绿再提交。

## 审查重点（Review Focus）

每条都要有钉住它的测试，写在对应任务的步骤里。

1. **只有一次采样时 CPU 必须是「—」而不是 0%**：`/proc/stat` 是累计值，第一次读只能记基线。
2. **GPU 读不到时显示「不可用」，绝不能是 0%**：绝大多数非高通设备没有 `kgsl` 节点。
3. **内核线程与无 cmdline 进程不进表格**：`/proc/<pid>/cmdline` 为空的条目既无应用归属也读不准。
4. **PID 复用不得把 CPU 时间算到别的进程头上**：只有 PID 且 starttime（stat 第 22 字段）都一致才做差。
5. **设备断开 / 重连后页面不得显示旧设备的进程列表**：generation 变化后快照清空。
6. **APK 是不可信输入，解析不得 panic、不得撑爆内存**：损坏/超大 APK 返回「无元数据」，绝不能把进程带崩（`recover` + 400 MB 上限）。
7. **应用元数据缺失时页面仍有分组行**：图标缺失退回首字母头像、名称退回包名，表格不能因此少一行。
8. **采样超时不得卡住窗口**：8 秒超时后放弃本次、保留上一次快照并标注「采样超时」；`adb exec-out cat` 单个 APK 超时 60 秒。
9. **7 天到期不得让界面退回包名**：过期条目继续显示旧名字，同时后台重抓；只有「从未有过缓存」才显示包名。
10. **设备离线时刷新按钮必须禁用且真的不连 adb**：不能出现点了毫无反应又发了命令的情况。
11. **并发刷新与轮询不得互相踩**：同一包在队列里只能有一份任务，刷新与自动补抓撞车时以刷新为准，不重复拉包。

---

## 决策与代价

| 决定 | 代价 |
|---|---|
| CPU/内存/GPU 用 `chart.Donut`，切片「已用 / 空闲」，`State.Index` 默认 0 让中心数字显示「已用」 | 用户按 Esc 清空选择后中心显示切片总和（100%）；不为这个角落自拼 Donut |
| GPU 不可用时该列统一显示「不可用」，并保留列 | 列宽被固定占用；不做动态隐藏 |
| GPU 使用率只读 vendor sysfs（`kgsl` / `devfreq` / MTK `gpu_busy`），逐进程 GPU 只在节点存在时读 | 多数设备整页 GPU 显示「不可用」；这是 Android 的能力边界，不做估算 |
| GPU 型号来自 `dumpsys SurfaceFlinger` 的 `GLES:` 行，首次采样后缓存 | 型号变化要重连才刷新 |
| **应用名与图标来自 base.apk 解析**（`pm path` → 拉整包 → `apkparser` 读 manifest + resources） | 首次见到的每个应用要拉一次 APK（几十 MB），磁盘缓存后 7 天内不再拉；纯矢量自适应图标拿不到位图时退回首字母头像 |
| 缓存**有效期 7 天**，过期后 `Lookup` 仍返回旧值（标记 `Stale`）并自动排队重抓 | 过期不等于清空：界面稳定，但用户会看到最多「一轮重抓」时间内的旧名字 |
| 缓存键 = 包名 + apk 路径 + size + mtime，与 7 天有效期**同时**生效：任一变化即重抓 | 应用更新会立刻重抓（这是想要的），代价是每次冷启动后首轮要多做一次 `pm path` + `stat` |
| ADB 在线时，分组表格上方提供「刷新应用信息」按钮：强制忽略 TTL 重抓当前可见包 | 多一个按钮与一次全量重拉（几十个 APK）；离线时禁用，避免误点后长时间无反应 |
| 缓存管理页（列表 / 单项删除 / 清空）**本轮不做**，但 core 现在就暴露 `AppMetaCache` 接口（`Entries`/`Invalidate`/`Purge`），下一轮直接接页面 | 接口暂无 UI 消费者，需要靠测试钉住，否则下一轮才发现形状不对 |
| 元数据后台预取 2 并发，只拉当前样本里出现的包 | 首屏几百毫秒内仍是包名 + 首字母头像，随后逐个替换成真图标与名称 |
| 采样间隔 2 秒，只在「资源」页可见时运行 | 页面隐藏时零 adb 开销；`ManagerOptions.ResourceInterval` 供测试注入 |
| 表格排序固定按 CPU 降序，不做点表头排序 | 少一屏交互；状态不持久化，回归风险小 |
| 进程 PID 只在展开的进程行里显示，分组行只显示进程数 | 少一列；需要展开才能看到细节 |

**已确认的取舍（用户答复）：** GPU 不可用 → 显示「不可用」；采样间隔 → 2 秒；PID → 只在展开行；映射缓存有效期 → 7 天；缓存列表管理 → 下一轮单独开页；刷新入口 → 分组上方操作栏。

**非目标（本轮不做）：** 缓存管理页面本身、应用元数据随设备同步/导出、矢量（自适应）图标的光栅化、GPU 使用率的估算或采样降级。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| 创建 `internal/core/procfs.go` | 纯函数：解析 `/proc/stat`、`/proc/meminfo`、`/proc/<pid>/{stat,status,cmdline}`、`GLES:` 行、GPU sysfs 文本；`FormatBytes` |
| 创建 `internal/core/procfs_test.go` | 上表的逐条表驱动测试，fixture 内联 |
| 创建 `internal/core/resources.go` | 公开类型 `ResourceSnapshot` 等；`GroupProcesses`；`SampleResources` / `StartResourceSampling` / `StopResourceSampling` / `Resources` / `RefreshAppMeta` |
| 创建 `internal/core/resources_test.go` | 分组、两次采样差值、GPU 缺失、generation 失效、采样循环、按需刷新 |
| 创建 `internal/core/appmeta.go` | `AppMeta`、`AppMetaEntry`、`AppMetaSource`、`AppMetaCache` 接口 |
| 修改 `internal/core/command.go`、`internal/core/runner.go` | 新窄接口 `FileRunner`（把 stdout 写进文件），`ExecRunner` 实现它；**不改** `Runner` |
| 修改 `internal/core/manager.go` | `ManagerOptions.ResourceInterval`、`AppMeta`；`Manager` 字段 `resources`/`prevSamples`/`hardware`/`samplers`/`appMeta`；`Disconnect`、`Shutdown` 清理 |
| 创建 `internal/appmeta/cache.go` | 磁盘缓存：条目读写、内存索引、TTL 判定、LRU 修剪、`Entries`/`Invalidate`/`Purge` |
| 创建 `internal/appmeta/store.go` | 拉 APK（`pm path` + `stat` + `exec-out cat` → 临时文件）、`apkparser` 解析 label/图标、2 并发队列、`Request`/`Refresh`/`Lookup` |
| 创建 `internal/appmeta/cache_test.go`、`store_test.go` | 固定时钟测 TTL；假 `FileRunner` + 手工构造的最小 APK fixture 测 label/图标/缓存命中/损坏包/去重 |
| 创建 `internal/app/resources.go` | 资源页：型号区、三个 Donut、分组表格 + 刷新操作栏、空/错误态、图标解码缓存 |
| 创建 `internal/app/resources_test.go` | `ui.NewTester` 断言环形图标签、表格分组、图标与名称、展开、GPU「不可用」、刷新按钮在线可用/离线禁用 |
| 修改 `internal/app/state.go:34` | `DetailTab` 改具名常量；`resources map[uuid.UUID]*resourceState`；`appIcons map[string]ui.ImageSource`；`metaRefreshing map[uuid.UUID]bool` |
| 修改 `internal/app/detail.go:186-195` | Tabs 增加「资源」，`switch` 用常量 |
| 修改 `internal/app/demo.go` | `demoRunner` 对采样脚本与 `pm path` 返回假数据；demo 环境注入内存版 `AppMetaSource`/`AppMetaCache` |
| 修改 `cmd/tailcat-manager/main.go` | 构造 `appmeta.New(...)` 并注入 `ManagerOptions.AppMeta` |
| 修改 `internal/compat/compat_test.go` | 断言 `chart.Donut`/`Slice`/`State`/`Options`、`components.Avatar`/`AvatarOptions`、`components.ScrollView`、`apkparser.ParseApk`/`ParseResourceTable` |
| 修改 `docs/CHANGELOG.md`、`docs/architecture.md`、`docs/build-notes.md` | 一条「新增」+ 一条「依赖」+ 一条「变更」；GPU 与图标/缓存能力的决策记录；体积实测 |

---

### 任务 1：`/proc` 与 GPU 文本的纯解析器

**文件：**
- 创建：`internal/core/procfs.go`
- 测试：`internal/core/procfs_test.go`

**接口（后续任务按此调用，不得改名）：**

```go
// cpuTimes is one /proc/stat cpu line in clock ticks.
type cpuTimes struct{ Total, Idle uint64 }

// memInfo is what /proc/meminfo says, in bytes.
type memInfo struct{ Total, Available uint64 }

// procEntry is one process as /proc describes it.
type procEntry struct {
	PID        int
	Command    string // cmdline，NUL 换成空格并 trim
	Jiffies    uint64 // utime+stime
	StartTicks uint64 // stat 第 22 字段，用于识别 PID 复用
	RSS        uint64 // 字节，来自 VmRSS
}

func parseCPUStat(text string) (cpuTimes, bool)
func parseMemInfo(text string) (memInfo, bool)
func parseProcEntry(pid int, statLine, statusText, cmdline string) (procEntry, bool)
func parseGLESRenderer(surfaceFlinger string) string
func parseGPUBusy(text string) (percent float64, ok bool) // "12 40" / "35" / "35%"
func FormatBytes(n uint64) string                        // "1.8 GB"
```

- [ ] **步骤 1：写失败的表驱动测试**

```go
func TestParseCPUStatSumsAllFields(t *testing.T)
func TestParseMemInfoUsesMemAvailable(t *testing.T)   // MemAvailable 缺失时退回 MemFree+Buffers+Cached
func TestParseProcEntryReadsJiffiesAndStartTicks(t *testing.T)      // 审查重点 4
func TestParseProcEntryRejectsEmptyCmdline(t *testing.T)            // 审查重点 3
func TestParseGLESRendererReadsSurfaceFlingerLine(t *testing.T)     // "GLES: Qualcomm, Adreno (TM) 640, OpenGL ES 3.2 ..."
func TestParseGPUBusyHandlesPercentAndBusyTotal(t *testing.T)       // "37%" / "37" / "37 100" → 37
func TestParseGPUBusyRejectsGarbage(t *testing.T)                   // "" → ok=false，不是 0（审查重点 2）
func TestFormatBytes(t *testing.T)                                  // 0→"0 B"、1<<20→"1.0 MB"
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core/ -run 'TestParse|TestFormatBytes' -v`
预期：FAIL / build failed，报 `undefined: parseCPUStat` 等。

- [ ] **步骤 3：在 `internal/core/procfs.go` 实现上述函数**

`parseCPUStat` 取首行 `cpu` 的 user..steal 之和为 Total、idle+iowait 为 Idle；`parseProcEntry` 从 stat 第 14/15 字段取 utime+stime、第 22 字段取 starttime，`status` 读 `VmRSS:`（kB→字节），cmdline 空则返回 false；`parseGPUBusy` 认 `N/M`、`N`、`N%` 三种写法，非数字一律 `ok=false`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core/ -run 'TestParse|TestFormatBytes' -v` → PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core/procfs.go internal/core/procfs_test.go
git add internal/core/procfs.go internal/core/procfs_test.go
git commit -m "feat: parse /proc and vendor GPU counters for the resource page"
```

---

### 任务 2：进程按应用分组

**文件：**
- 创建：`internal/core/resources.go`
- 测试：`internal/core/resources_test.go`

**接口：**

```go
// ProcessUsage is one process in the resource table.
type ProcessUsage struct {
	PID        int
	Name       string  // 进程名（cmdline 首个 token 的 basename）
	CPUPercent float64
	MemBytes   uint64
	GPUPercent float64
	GPUOk      bool
}

// AppUsage is one application group of processes.
type AppUsage struct {
	// Package is the group key: "com.foo" 与 "com.foo:remote" 合并。
	Package string
	// Label is the application's user-visible name; empty until appmeta knows it.
	Label string
	// HasIcon reports whether appmeta has an icon for this package.
	HasIcon bool
	// MetaStale reports that the cached name/icon is past its 7-day TTL.
	MetaStale  bool
	Processes  []ProcessUsage
	CPUPercent float64
	MemBytes   uint64
	GPUPercent float64
	GPUOk      bool
}

// GroupProcesses 把进程按应用分组：组内与组间都按 CPU 降序。
func GroupProcesses(entries []procEntry, cpuPercent map[int]float64) []AppUsage

// appKey 返回分组键：cmdline 首个 token 的 basename 去掉 ":suffix"。
func appKey(command string) string
```

- [ ] **步骤 1：写失败测试**

```go
func TestGroupProcessesByIdenticalPrefix(t *testing.T)       // "com.foo" 与 "com.foo:remote" 同组
func TestGroupProcessesKeepsSystemNamesSeparate(t *testing.T) // "system_server" 自成一组
func TestGroupProcessesSortsByCPUDescending(t *testing.T)
func TestGroupProcessesSkipsEmptyCommand(t *testing.T)        // 审查重点 3
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core/ -run TestGroupProcesses -v` → FAIL，`undefined: GroupProcesses`。

- [ ] **步骤 3：实现 `GroupProcesses` 与 `appKey`**

`appKey` 取 `strings.Fields(command)` 的第一个 token，`path.Base` 之后截掉 `:` 及其后缀；空命令返回 `""`，调用方跳过。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core/ -run TestGroupProcesses -v` → PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core/resources.go internal/core/resources_test.go
git add internal/core/resources.go internal/core/resources_test.go
git commit -m "feat: group device processes by application"
```

---

### 任务 3：`FileRunner` —— 把命令输出写进文件

**文件：**
- 修改：`internal/core/command.go`、`internal/core/runner.go`
- 测试：`internal/core/runner_test.go`（沿用 `TestMain` + `CORE_RUNNER_HELPER` 的 helper 模式）

**接口（新增窄接口，不改现有的 `Runner`，因此既有假实现不用动）：**

```go
// FileRunner 是能把命令的 stdout 直接写进文件的 Runner；二进制安全。
type FileRunner interface {
	Runner
	// RunToFile 把 stdout 写入 path（截断创建），stderr 仍按 Runner 的约定丢弃。
	// 返回值里的 Output 只保留 stderr 的最后 maxCapturedOutput 字节。
	RunToFile(ctx context.Context, c Command, path string) (Result, error)
}
```

`ExecRunner` 实现 `RunToFile`（`cmd.Stdout = file`）。

- [ ] **步骤 1：写失败的测试**

```go
func TestExecRunnerRunToFileKeepsBinaryStdout(t *testing.T) // helper 打印 0x00..0x1f 原始字节，断言文件字节一致且 Output 不含它
func TestExecRunnerRunToFileReportsExitCode(t *testing.T)  // helper 退出 3 → ExitCode 3
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core/ -run TestExecRunnerRunToFile -v` → FAIL，`ExecRunner has no field or method RunToFile`。

- [ ] **步骤 3：实现 `FileRunner` 与 `ExecRunner.RunToFile`**

`os.OpenFile(path, O_WRONLY|O_CREATE|O_TRUNC, 0o600)`；stderr 走现有 `outputSink`；`ctx` / `Timeout` 处理与 `Run` 一致。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core/ -run TestExecRunnerRunToFile -v && go test ./internal/core` → PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core && git add -u internal/core
git commit -m "feat: let the runner stream a command's stdout into a file"
```

---

### 任务 4：`internal/appmeta` —— 带 7 天有效期的应用名与图标缓存

**文件：**
- 创建：`internal/core/appmeta.go`、`internal/appmeta/cache.go`、`internal/appmeta/store.go`
- 测试：`internal/appmeta/cache_test.go`、`internal/appmeta/store_test.go`

**接口：**

```go
// internal/core/appmeta.go
// AppMeta is one application's identity as the device's APK describes it.
type AppMeta struct {
	Package string
	// Label is the application's name in its own language.
	Label string
	// Icon is a PNG/WebP/JPEG the UI decodes, empty when the app only ships a
	// vector icon (adaptive icon XML) we cannot rasterize.
	Icon []byte
	// FetchedAt is when the device's APK was read.
	FetchedAt time.Time
	// Stale reports that FetchedAt is older than appmeta.TTL.
	Stale bool
}

// AppMetaEntry is one cache row, for the cache page that comes later.
type AppMetaEntry struct {
	Package   string
	Label     string
	Size      int64
	FetchedAt time.Time
	Stale     bool
}

// AppMetaSource 提供设备上应用的名字与图标；没有信息时 ok=false。
type AppMetaSource interface {
	Lookup(device string, pkg string) (AppMeta, bool)
	// Request 安排后台解析这些包；已新鲜（未过期且未变）的包会被忽略。
	Request(device, adbPath, serial string, pkgs []string)
	// Refresh 忽略有效期强制重抓；与 Request 共用同一队列与去重。
	Refresh(device, adbPath, serial string, pkgs []string)
}

// AppMetaCache 是后续「应用信息缓存」管理页要用的口子。本轮没有 UI 消费者。
type AppMetaCache interface {
	Entries(device string) []AppMetaEntry
	Invalidate(device, pkg string) error
	Purge(device string) error
}

// internal/appmeta/store.go
// TTL 是缓存条目的有效期。
const TTL = 7 * 24 * time.Hour

// New 打开 store；dir 不存在时按 0700 创建，并扫描已有条目重建内存索引。
func New(dir string, runner core.FileRunner, workers int) (*Store, error)
```

`Store` 同时满足 `core.AppMetaSource` 与 `core.AppMetaCache`；`Store.now func() time.Time` 默认 `time.Now`。

**抓取流程（每个包一次）：**

1. `adb -s <serial> shell pm path <pkg>` → `package:/data/app/.../base.apk`
2. `adb -s <serial> shell stat -c '%s %Y' <apk>` → 缓存键 `<pkg>|<apk>|size|mtime`
3. 键一致且 `FetchedAt` 未过期 → 直接用（`Request` 不排队）；过期或键变化 → 排队
4. 队列里没有该键 → `adb -s <serial> exec-out cat <apk>` 写进 `os.MkdirTemp` 临时文件（≤400 MB，超时 60 s）
5. `apkparser.ParseApk` 收自定义 `ManifestEncoder`：在 `<application>` 上抓 `label`（已解析好的字符串）与 `icon`（`GetIconPng` 解析出的 `res/.../*.png|webp` 路径）
6. 从 zip 读该条目字节（≤1 MB）作为 `Icon`；`.xml` 或读失败 → `Icon` 留空
7. 原子写缓存 `<dir>/<sha256(key)>.json`（pkg / label / key / fetchedAt / iconMIME / base64 icon），权限 0600，rename 生效；删临时 APK
8. 全程 `defer recover()`，任何 panic 当作「无元数据」

**TTL 语义（审查重点 9）：** `Lookup` 对过期条目**仍然返回** `AppMeta{Stale: true}`，让界面继续显示旧名字；`Request` 遇到过期条目会排队重抓。只有从未缓存过才 `ok=false`。

- [ ] **步骤 1：写失败的测试**

用固定时钟（`store.now = func() time.Time { return fixed }`）与手工构造的最小 APK fixture（zip 内含二进制 `AndroidManifest.xml` + `resources.arsc` + `res/mipmap/ic.png`，base64 常量内联，由 `apkparser` 自带 testdata 裁剪而来）：

```go
// cache_test.go
func TestCacheEntryExpiresAfterSevenDays(t *testing.T)      // 审查重点 9：6d23h → Stale=false；7d1h → Stale=true
func TestCacheEntriesListsRowsForTheManagementPage(t *testing.T)
func TestCacheInvalidateRemovesOneEntry(t *testing.T)
func TestCachePurgeRemovesDeviceEntries(t *testing.T)
func TestCacheScansDirectoryOnOpen(t *testing.T)            // 重开 store 能读回条目
func TestCachePrunesBeyondLimits(t *testing.T)              // 500 条 / 64 MB 上限，删最旧
// store_test.go
func TestStoreReadsLabelAndIconFromApk(t *testing.T)
func TestStoreSkipsFreshEntryAndRefetchesStale(t *testing.T) // 审查重点 9
func TestStoreRefreshBypassesTTL(t *testing.T)               // 强制重抓，即使刚抓过
func TestStoreFallsBackWhenApkIsCorrupt(t *testing.T)        // 审查重点 6：不 panic，Lookup ok=false
func TestStoreRejectsOversizedApk(t *testing.T)              // >400MB 直接跳过
func TestStoreIgnoresDuplicateRequests(t *testing.T)         // 审查重点 11：Request 与 Refresh 混用只拉一次
func TestStoreKeepsLabelWhenIconIsVector(t *testing.T)       // 审查重点 7
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/appmeta/ -v` → FAIL，`undefined: New`。

- [ ] **步骤 3：实现 `cache.go` 与 `store.go`**

`go get github.com/avast/apkparser`（间接只带 `github.com/klauspost/compress`，已在依赖图里）。2 个 worker 从带缓冲 channel 取任务；去重键是缓存键（含 size/mtime），`pending map[string]bool` 与 `entries map[string]core.AppMeta` 都在 `mu` 下；`Refresh` 与 `Request` 共用队列，`Refresh` 只多一个「忽略 TTL」标志，同一键在队列里时以后到者的标志提升为强制。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/appmeta/ -v && CGO_ENABLED=0 GOOS=windows go build ./...` → PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core/appmeta.go internal/appmeta
git add go.mod go.sum internal/core/appmeta.go internal/appmeta
git commit -m "feat: cache app names and icons for seven days"
```

---

### 任务 5：单次采样 `Manager.SampleResources`

**文件：**
- 修改：`internal/core/resources.go`、`internal/core/manager.go`（`ManagerOptions.AppMeta`、`Manager` 新字段、`NewManager` 初始化）
- 测试：`internal/core/resources_test.go`（复用 `manager_test.go` 的 `fakeRunner`，加 `fakeAppMeta`）

**接口：**

```go
type ResourceSnapshot struct {
	At     time.Time
	CPU    CPUSample
	Memory MemorySample
	GPU    GPUSample
	Apps   []AppUsage
	// Warnings 是本次采样缺失项的中文说明（如「该设备不提供 GPU 使用率」）。
	Warnings []string
}

type CPUSample struct {
	Model     string
	Cores     int
	Percent   float64
	Available bool // 只有过一次采样时为 false，UI 显示「—」
}

type MemorySample struct {
	Total, Available, Used uint64
	Percent                float64
	Available              bool
}

type GPUSample struct {
	Renderer            string
	Percent             float64
	Available           bool // 读不到节点时为 false，UI 显示「不可用」
	PerProcessAvailable bool
}

func (m *Manager) SampleResources(ctx context.Context, id uuid.UUID) (ResourceSnapshot, error)
func (m *Manager) Resources(id uuid.UUID) (ResourceSnapshot, bool)
```

采样脚本（单个 `adb shell`，`Timeout: 8 * time.Second`）：

```sh
cat /proc/stat; echo @@MEM; cat /proc/meminfo; echo @@CPUINFO; grep -c ^processor /proc/cpuinfo; grep -m1 'model name' /proc/cpuinfo;
for d in /proc/[0-9]*; do echo @@P $d; cat $d/stat 2>/dev/null | head -1; grep VmRSS $d/status 2>/dev/null; tr '\0' ' ' < $d/cmdline 2>/dev/null; echo; done
```

GPU 单独一轮，避免每 2 秒跑 `dumpsys`：首次读 `dumpsys SurfaceFlinger | grep -m1 '^GLES:'` 取型号并缓存到 `m.hardware[id]`；每次采样读第一个存在的节点
`/sys/class/kgsl/kgsl-3d0/gpubusy`、`/sys/class/kgsl/kgsl-3d0/gpu_busy_percentage`、`/sys/kernel/gpu/gpu_busy`、`/sys/class/devfreq/*/load`；逐进程 GPU 仅在 `/sys/class/kgsl/kgsl-3d0/gpu_process_entries` 存在时读。

分组后用 `m.appMeta`（可能为 nil）填 `Label`/`HasIcon`/`MetaStale`，并对**过期或缺少**的包调 `Request`；`appMeta` 调用一律在 `m.mu` 之外。

- [ ] **步骤 1：写失败的测试**

```go
func TestSampleResourcesNeedsTwoReadingsForCPU(t *testing.T)         // 审查重点 1
func TestSampleResourcesReadsMemoryAndCPUModel(t *testing.T)
func TestSampleResourcesReportsGPUUnavailable(t *testing.T)          // 审查重点 2：「不可用」+ Warning
func TestSampleResourcesKeepsPreviousSnapshotOnTimeout(t *testing.T) // 审查重点 8
func TestSampleResourcesRejectsStaleGeneration(t *testing.T)         // 审查重点 5
func TestSampleResourcesUsesAppMetaLabels(t *testing.T)              // 有 label 用 label，没有用包名
func TestSampleResourcesKeepsStaleLabelAndRequestsRefresh(t *testing.T) // 审查重点 9：Stale 仍显示旧名 + 触发 Request
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core/ -run TestSampleResources -v` → FAIL，`undefined: SampleResources`。

- [ ] **步骤 3：实现 `SampleResources`**

`m.operationTarget` 取目标 → `m.validateTarget` 验一次 → `m.runner.Run` 在**锁外**执行 → 解析后 `m.mu.Lock()`，`if !m.targetIsCurrent(id, target) { return ErrCancelled }` → 写 `m.resources[id]`/`m.prevSamples[id]` → `m.notify()` → 解锁。CPU% = `100*(ΔTotal-ΔIdle)/ΔTotal`，`ΔTotal==0` 时 `Available=false`；per-pid 差值只在 PID 与 `StartTicks` 都一致时计算。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core/ -run 'TestSampleResources|TestGroupProcesses' -v` → PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core && git add -u internal/core
git commit -m "feat: sample device CPU, memory and GPU usage"
```

---

### 任务 6：采样循环与生命周期

**文件：**
- 修改：`internal/core/resources.go`、`internal/core/manager.go`（`ManagerOptions.ResourceInterval`、`Disconnect`、`Shutdown`）
- 测试：`internal/core/resources_test.go`

**接口：**

```go
const defaultResourceInterval = 2 * time.Second

func (m *Manager) StartResourceSampling(id uuid.UUID) // 幂等
func (m *Manager) StopResourceSampling(id uuid.UUID)  // 停止并清空快照
```

`ManagerOptions.ResourceInterval time.Duration`（`<=0` 用默认值，测试传 10ms）。

- [ ] **步骤 1：写失败测试**

```go
func TestResourceSamplingTicksUntilStopped(t *testing.T)  // 10ms 间隔，2 次后 Stop，计数不再增长
func TestResourceSamplingStopsOnDisconnect(t *testing.T)  // Disconnect 后快照清空
func TestStartResourceSamplingIsIdempotent(t *testing.T)  // 连调 3 次只有 1 个循环
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core/ -run 'TestResourceSampling|TestStartResourceSampling' -v` → FAIL，`undefined: StartResourceSampling`。

- [ ] **步骤 3：实现采样循环**

仿 `startMonitor`：`m.samplers map[uuid.UUID]context.CancelFunc`，`context.WithCancel(m.ctx)`，ticker 用 `m.resourceInterval`；每次 tick 先 `isCurrentLocked`，再在锁外 `SampleResources`。`Disconnect` 与 `Shutdown` 调 `stopResourceSamplingLocked` 并 `delete(m.resources, id)`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core/ -run 'TestResourceSampling|TestStartResourceSampling' -v && go test -race ./internal/core` → PASS，无 race。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core && git add -u internal/core
git commit -m "feat: sample device resources on an interval while the page is open"
```

---

### 任务 7：按需刷新应用信息 `Manager.RefreshAppMeta`

**文件：**
- 修改：`internal/core/resources.go`
- 测试：`internal/core/resources_test.go`

**接口：**

```go
// RefreshAppMeta 忽略缓存有效期，重抓当前快照里出现的所有应用的名称与图标。
// 设备不在线时返回错误，不发送任何 adb 命令。
func (m *Manager) RefreshAppMeta(ctx context.Context, id uuid.UUID) error
```

- [ ] **步骤 1：写失败的测试**

```go
func TestRefreshAppMetaRequiresOnlineDevice(t *testing.T)   // 审查重点 10：离线返回「设备未连接」且 fakeRunner 调用 0 次
func TestRefreshAppMetaForcesEveryVisiblePackage(t *testing.T) // fakeAppMeta.Refresh 收到快照里的全部包
func TestRefreshAppMetaWithoutSnapshotRefreshesNothing(t *testing.T) // 还没采样过 → 返回 nil，不排队
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core/ -run TestRefreshAppMeta -v` → FAIL，`undefined: RefreshAppMeta`。

- [ ] **步骤 3：实现 `RefreshAppMeta`**

`m.operationTarget(ctx, id)` 取快照（离线/忙时它已经会返回中文错误）；`m.mu` 下读 `m.resources[id]` 的包列表并 `m.mu.Unlock()`；`appMeta != nil` 时调 `Refresh(deviceKey, adder, target.Serial, pkgs)`。**不要**在这里等抓取完成——抓取是后台队列，视图靠 `notify()` 刷新。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core/ -run 'TestRefreshAppMeta|TestResourceSampling' -v` → PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core && git add -u internal/core
git commit -m "feat: refresh app names and icons on demand"
```

---

### 任务 8：详情页「资源」标签与三个环形图

**文件：**
- 创建：`internal/app/resources.go`、`internal/app/resources_test.go`
- 修改：`internal/app/state.go:34-35`、`internal/app/detail.go:186-195`
- 修改：`internal/app/forms_test.go:111`、`internal/app/logs_test.go:64,121`、`internal/app/terminal_test.go:157`（改用常量，不改断言）

**接口：**

```go
const (
	detailOverview = iota
	detailResources
	detailTerminal
	detailLog
)

// resourceState 是资源页的视图状态，按设备保留。
type resourceState struct {
	cpu, mem, gpu  chart.State
	table         components.TableState
	expanded      map[string]bool
	scroll        ui.ScrollState
	// refreshing 记录已请求刷新、等待新元数据到位的状态。
	refreshing bool
}

func (vm *ViewModel) resourcePage(c *ui.Context, profile core.Profile, session core.Session)
```

Tabs 文案顺序：`"概览", "资源", "终端", "日志"`。

- [ ] **步骤 1：写失败的视图测试**

```go
func TestResourcePageShowsHardwareAndDonutLabels(t *testing.T) // HasText「CPU」「GPU」「内存」与「42%」
func TestResourcePageSaysGPUUnavailable(t *testing.T)          // 「不可用」+「该设备不提供 GPU 使用率」
func TestResourcePageNeedsConnectionWhenOffline(t *testing.T)  // StateOffline → 「设备未连接」，不渲染表格
```

用 `ui.NewTester`，对齐 `logs_test.go` 的写法（`vm.DetailTab = detailResources`）。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app/ -run TestResourcePage -v` → FAIL，`undefined: resourcePage`。

- [ ] **步骤 3：实现型号区与三个 Donut**

顶部一行：`CPU 型号 · {Cores} 核`、`GPU 型号`、`内存 total`（缺失显示 `core.PlaceholderUnknown`）。三列 `chart.Donut`，数据 `[]chart.Slice{{ID:"used",Label:"已用",Value:p},{ID:"idle",Label:"空闲",Value:100-p}}`，`chart.Options{Label: 标题, PlotHeight: 150, FormatValue: 整数百分比}`；GPU 不可用时该列不画图，改画 `"不可用"` + `Warnings` 里的说明。每帧 `vm.Manager.StartResourceSampling(profile.ID)`；离开标签页时由 `detailPage` 的 `switch` 调 `StopResourceSampling`。离线或 `State.Busy()` 时渲染「设备未连接」+ 原因。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app/ -run TestResourcePage -v && go test ./internal/app` → PASS（含被常量改动影响的旧测试）。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/app && git add -u internal/app
git commit -m "feat: add the device resource page with CPU, GPU and memory donuts"
```

---

### 任务 9：应用图标 + 应用名的分组表格，以及分组上方的刷新操作栏

**文件：**
- 修改：`internal/app/resources.go`、`internal/app/state.go`（`appIcons map[string]ui.ImageSource`）
- 测试：`internal/app/resources_test.go`

**接口：**

```go
// processTable 渲染分组上方的操作栏 + 按应用分组的进程表。
func (vm *ViewModel) processTable(c *ui.Context, st *resourceState, snap core.ResourceSnapshot, session core.Session)

// tableToolbar 是分组上方那一行：标题、刷新说明、刷新按钮。
func (vm *ViewModel) tableToolbar(c *ui.Context, st *resourceState, profile core.Profile, session core.Session)

// processColumns 是进程表的列定义（固定顺序，不排序）。
func processColumns() []ui.TableColumn

// appIcon 返回解码后的图标；没有图标时返回 nil，由 Avatar 显示首字母。
func (vm *ViewModel) appIcon(device, pkg string) ui.ImageSource
```

列：`应用`（MinWidth 200）、`进程`（Width 56，右对齐）、`CPU`（Width 72，右对齐）、`GPU`（Width 72，右对齐）、`内存`（Width 88，右对齐）。整表放进 `components.ScrollView` 并 `Grow(1)`。

分组行「应用」格 = `components.Avatar(c, label, components.AvatarOptions{Image: vm.appIcon(...), Size: 20})` + `components.Text(c, label)`；`label` 为空时用 `app.Package`（从没缓存过才这样）。`MetaStale` 为真时在名称后加一句灰色提示「应用信息 7 天前更新」。

操作栏（表格正上方，`FillWidth()` 的行）：左侧 `components.Text(c, "按应用分组")`；右侧 `components.IconButton(c, lucide.RefreshCw(), "刷新应用信息")`——`session.State != core.StateOnline` 时 `.Disabled(true)`，正在刷新时 `.Disabled(true)` + 文案「正在刷新应用信息…」，否则点击即 `go func(){ vm.Fail(vm.Manager.RefreshAppMeta(context.Background(), profile.ID)); st.refreshing = true; vm.Invalidate() }()`；新元数据到达（`HasIcon`/`Label` 变化）时清 `refreshing`。

- [ ] **步骤 1：写失败的视图测试**

```go
func TestProcessTableGroupsByApp(t *testing.T)                    // 两个同包进程 → 一行「微信」+「2 个进程」
func TestProcessTableShowsAppNameNotPackage(t *testing.T)         // label「微信」出现，纯包名不再作为行标签
func TestProcessTableFallsBackToPackageWithoutMeta(t *testing.T)  // 审查重点 7
func TestProcessTableMarksStaleMeta(t *testing.T)                 // 审查重点 9：「7 天前更新」且名字仍是旧名字
func TestProcessTableExpandsOnClick(t *testing.T)                 // 点击分组行后出现子行「PID 1234」
func TestProcessTableSaysGPUUnavailable(t *testing.T)             // GPU 列「不可用」，不是 0%（审查重点 2）
func TestResourceToolbarRefreshIsDisabledWhileOffline(t *testing.T) // 审查重点 10：离线时按钮 Disabled
func TestResourceToolbarRefreshAsksManagerToRefresh(t *testing.T)   // 在线点击 → fakeAppMeta 的 Refresh 被调用
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app/ -run 'TestProcessTable|TestResourceToolbar' -v` → FAIL，`undefined: processTable`。

- [ ] **步骤 3：实现操作栏、分组行与展开子行**

`components.Table(c, &st.table, processColumns(), func(){ ... })`，组行 `components.TableRow(c, app.Package, label, st.expanded[key], cell)`，`Clicked()` 切换 `st.expanded`；展开时追加进程子行（key 用 PID）。CPU `"%.1f%%"`、内存 `core.FormatBytes`、GPU 不可用写 `"不可用"`。`appIcon` 用 `ui.DecodeImage(meta.Icon)` 解码一次后缓存在 `vm.appIcons`（键 `<device>|<pkg>`），解码失败返回 nil。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app/ -run 'TestProcessTable|TestResourcePage|TestResourceToolbar' -v` → PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/app && git add -u internal/app
git commit -m "feat: group processes by app with icon, name and a refresh action"
```

---

### 任务 10：demo 数据、接线、compat 断言与文档

**文件：**
- 修改：`internal/app/demo.go`、`cmd/tailcat-manager/main.go`、`internal/compat/compat_test.go`、`docs/CHANGELOG.md`、`docs/architecture.md`、`docs/build-notes.md`

- [ ] **步骤 1：写失败的测试**

```go
// internal/app/app_test.go
func TestDemoRunnerAnswersResourceSampling(t *testing.T)  // 采样脚本 → 输出含 @@P，GroupProcesses 非空
func TestDemoEnvironmentHasAppNamesAndIcons(t *testing.T) // demo 的 AppMetaSource 给出「示例应用」与一张 1×1 PNG
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app/ -run 'TestDemoRunnerAnswersResourceSampling|TestDemoEnvironmentHasAppNamesAndIcons' -v` → FAIL。

- [ ] **步骤 3：让 demo 说完两套假数据并接线**

`demoRunner.Run` 脚本含 `@@MEM` 时返回内联 fixture（2 个应用、4 个进程、CPU 42%、内存 3.1/8.0 GB、GPU 节点缺失 → 不可用）；`pm path`/`stat`/`exec-out cat` 也返回假数据。demo 环境给 `ManagerOptions.AppMeta` 注入内存实现（「相机」「相册」+ 内联 PNG）。`main.go` 用 `appmeta.New(filepath.Join(configDir, "appmeta"), core.FileRunner(runner), 2)` 注入。

- [ ] **步骤 4：更新 compat 与文档**

- compat 追加：`chart.Donut`、`chart.Slice`、`chart.State`、`chart.Options`、`components.Avatar`、`components.AvatarOptions{}`、`components.AvatarNoStatus`、`components.ScrollView`、`components.ScrollViewOptions{}`、`components.TableState{}`、`components.IconButton`、`apkparser.ParseApk`、`apkparser.OpenZipReader`、`apkparser.ParseResourceTable`、`apkparser.ManifestEncoder`。
- `docs/CHANGELOG.md` → `## [未发布]`：
  - `### 新增`：`设备页新增「资源」标签页：显示 CPU / GPU 型号与 CPU、GPU、内存占用环形图，并按应用分组列出设备上所有进程的占用；分组行显示应用图标与应用名称（来自设备上的 APK）。`
  - `### 新增`：`应用信息在本地缓存 7 天；ADB 在线时可在分组上方的操作栏点「刷新应用信息」强制更新图标与名称（缓存管理页将在下一版提供）。`
  - `### 变更`：`GPU 使用率只在该设备提供计数节点时显示，其余设备显示「不可用」。`
  - `### 依赖`：`新增 github.com/avast/apkparser（纯 Go）用于读取 APK 里的应用名称与图标，安装包体积增加 X MB。`
- `docs/architecture.md`：设计决策记录补三条——GPU 无标准接口（代价：多数设备「不可用」）、图标靠拉 APK 解析（代价：首次数十 MB 流量/带宽，只缓存小图不缓存 APK）、元数据缓存 7 天 + 过期仍显示旧值（代价：最多一轮重抓时间内的旧名字）。
- `docs/build-notes.md`：补依赖版本与 `CGO_ENABLED=0` 三平台构建体积/耗时实测。

- [ ] **步骤 5：全量验证并提交**

```bash
gofmt -l . && go vet ./... && go test ./... && go test -race ./internal/core ./internal/app
CGO_ENABLED=0 GOOS=windows go build ./... && CGO_ENABLED=0 GOOS=darwin go build ./...
git add -A && git commit -m "docs: record the resource page, app metadata cache and their limits"
```

---

## 自检

1. **规格覆盖度**：截图「CPU/GPU 信息」→ 任务 8 型号区；「CPU/GPU/内存占用 Donut」→ 任务 1/5/8；「按 app 分组显示所有进程占用」→ 任务 2/9；「分组名用图标 + 应用名而非包名」→ 任务 4/9；「GPU 不可用显示不可用」→ 任务 1（`ok=false`）/5（`Available=false`）/8/9（文案）；「本地缓存 7 天」→ 任务 4（TTL + Stale）；「后续单独开列表管理缓存」→ 任务 4 的 `AppMetaCache`（`Entries`/`Invalidate`/`Purge`）+ 决策表「非目标」；「ADB 连接成功时可主动刷新映射」→ 任务 7（core）+ 任务 9（操作栏 + 离线禁用）。
2. **步骤扫描**：每个任务先给测试名与断言，再给实现要点与验证命令，最后 commit；没有「处理边界情况」这类空步骤。
3. **类型一致性**：`ResourceSnapshot`/`AppUsage`/`ProcessUsage` 在任务 2、5 定义，任务 8、9 只使用；`AppMeta`/`AppMetaSource`/`AppMetaCache` 任务 4 定义，任务 5、7、9 使用；`FormatBytes` 任务 1 定义、任务 9 使用；`FileRunner` 任务 3 定义、任务 4 使用；`detailResources` 任务 8 定义、任务 9 引用；`RefreshAppMeta` 任务 7 定义、任务 9 调用。
4. **审查重点**：11 条各有测试——`TestSampleResourcesNeedsTwoReadingsForCPU`；`TestParseGPUBusyRejectsGarbage`/`TestProcessTableSaysGPUUnavailable`；`TestParseProcEntryRejectsEmptyCmdline`/`TestGroupProcessesSkipsEmptyCommand`；`TestParseProcEntryReadsJiffiesAndStartTicks`；`TestSampleResourcesRejectsStaleGeneration`；`TestStoreFallsBackWhenApkIsCorrupt`；`TestProcessTableFallsBackToPackageWithoutMeta`；`TestSampleResourcesKeepsPreviousSnapshotOnTimeout`；`TestCacheEntryExpiresAfterSevenDays`/`TestStoreSkipsFreshEntryAndRefetchesStale`/`TestProcessTableMarksStaleMeta`；`TestRefreshAppMetaRequiresOnlineDevice`/`TestResourceToolbarRefreshIsDisabledWhileOffline`；`TestStoreIgnoresDuplicateRequests`。
5. **比例**：计划只固定签名、文案与测试断言，不复写实现。
