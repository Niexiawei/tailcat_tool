# 设备文件管理页 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 设备详情页新增「文件」标签页（顺序：概览 / 资源 / 终端 / 文件 / 日志）：左右两栏，左边是可展开的文件夹树，右边是所选文件夹里的条目表格（名称 / 大小 / 修改时间 / 权限）；点条目弹窗看属性，并能把文件下载到本地、把本地文件上传到当前文件夹。设备不提供创建时间，因此**不显示创建时间**。

**架构：** 读目录是 core 的一次「静默 adb 往返」（照 `SampleResources` 的形状：`operationTarget` + `validateTarget` + `m.runner.Run`，不建任务、不进历史），结果按 `(设备, 路径)` 缓存在 `Manager` 里，`Disconnect` 时随通道一起清掉。界面只读快照并逐帧渲染；展开目录才去读，读到之后 `notify()` 触发重绘。上传/下载**走 `BeginADBTask`**（可取消、日志页可见、进历史）。视图用 `components.Split` + `components.Tree`/`TreeItem` + `components.Table`/`TableRow` + `components.FileIcon`。

**技术栈：** Go、`adb shell`（经 `core.Runner`）、toybox `stat -c`、`components.Split`、`components.Tree`、`components.TreeItemWithLeading`、`components.Table`/`components.TableRow`、`components.ScrollView`、`components.FileIcon`、`components.Dialog`、`mygo.Dialog.Open/Save`、`ui.NewTester`。

**规格：** 本计划由对话需求（用户 2026-11 的两轮答复）直接得出，没有独立规格文档；裁决见本文「决策与代价」，落地后同步进 `docs/ui-and-console.md`（新增文件页一节）与 `docs/architecture.md` 第 15 节决策记录。

---

## 全局约束

- 纯 Go、禁止 cgo；`CGO_ENABLED=0 GOOS=linux|darwin|windows go build ./...` 必须通过（本轮不引入任何新依赖）。
- 依赖方向 `cmd → internal/app → internal/core → internal/platform`；`internal/app` 不得直接 `exec.Command`，一切设备命令走 `core.Runner`。
- **只发只读命令**：本页对设备只做 `cd` + `stat`（元数据读取）；唯二的写操作是用户点按钮发起的 `adb push` / `adb pull`，走任务机制。
- 设备命令必须绑定启动瞬间的 `operationTarget`（serial / adb / generation），执行前后各校验一次；跨代结果不得回写。
- 绝不能持有 `m.mu` 时调用 `validateTarget`、`recordTask`、`m.runner.Run` 等会再上锁或做 I/O 的方法。
- 截断/超时都要说清原因，绝不返回「看起来完整」的半份列表。
- 面向用户文案是中文，测试逐字断言；改文案必须同步改测试。
- GUI 一律优先用 onevra `components`；行式元素用 `FillWidth()`，**禁止** `Fill()`（同时占满高度）；`Split` 两个面板都要 `MinWidth(0)`。
- 每个提交都要带 `gofmt -w`、`go vet ./...`、`go test ./...`；用户可见改动同提交写进 `docs/CHANGELOG.md`。
- 分支 `feat/device-file-manager`，不要直接提 `main`。

---

## 审查重点（Review Focus）

每条都要有钉住它的测试，写在对应任务的步骤里。

1. **路径里的 `'`、空格、中文不得把脚本弄坏**：用户从树里点进来的路径可能带单引号（`/sdcard/John's/`）；脚本必须转义，或直接拒绝并给出中文原因——绝不能拼出一个注入脚本或语法错误的命令。
2. **巨型目录不得静默截断成「看起来完整」**：`/sdcard` 下一万条时 runner 的输出上限是 1 MiB，静默截断会让用户以为文件不见了；超过上限要么标注「列表已截断」，要么明确报错。
3. **列目录不得刷满日志页与历史库**：展开十个文件夹不能产生十条任务/十条历史记录。
4. **断开或重连后不得显示旧设备的目录内容**：目录缓存按设备键存，`Disconnect` 必须清掉。
5. **未连接时页面必须显示原因，不得显示上次的残影**：和资源页同样处理，不画空表格。
6. **读目录失败（目录不存在、无权限）要给出中文原因和重试入口**：不能是空白表格，也不能只弹一个转瞬即逝的提示条。
7. **详情弹窗里不得出现「创建时间」**：Android 多数文件系统没有 btime，用户已裁决不显示该字段；任何 `%W` / `birth` 字段都不进界面。
8. **上传/下载的路径非法时要有中文校验文案，且不得发命令**：换行、空字符、非绝对路径、空本地路径都必须在 `BeginADBTask` 之前挡住。
9. **树与表格的交互不得互相拖累到 0 宽**：`Split` 两个面板都 `MinWidth(0)`，窗口很窄时控件仍可见。
10. **同一目录并发多次读取不得打架**：双击/连续点击同一个文件夹只能有一次在飞，结果落回同一份快照。

---

## 决策与代价

| 决定 | 代价 |
|---|---|
| 入口是设备页第 5 个页签「文件」（概览/资源/终端/文件/日志），**不是**在终端页里再套一层分段器 | 用户裁决。设备页页签变五项，窄窗口下分段标签的字更挤；换来的是"终端页里两行同款分段器"的层级混淆不会发生 |
| 一个目录一次 `adb shell` 往返，循环跑 toybox `stat -c '%F\|%s\|%a\|%A\|%Y\|%n'` | 依赖设备有 toybox `stat`（Android 6+ 标配）；没有则报「该设备不支持读取文件属性」，**不做 `ls -la` 兜底解析**（两个解析器早晚不一致） |
| 名称放在格式串**最后**，解析用 `SplitN(…, 6)` | 文件名里出现 `\|` 也不会错位；代价是格式串读起来反直觉，靠注释与测试钉住 |
| **不显示创建时间**（不解析 `%W`） | Android 多数文件系统没有 btime，显示「—」比没有更让人怀疑；用户裁决直接不显示。代价：弹窗只有四行属性 |
| 列目录**静默执行**（不进任务、不进历史、不写通道日志） | 和 `SampleResources` 同一形状。代价：日志页看不到"读了哪个目录"，排查问题时只能靠任务；换来的是浏览十个文件夹不会刷满历史库 |
| 上传/下载**走 `BeginADBTask`** | 可取消、日志页可见、进历史、超时与脱敏免费获得；代价是同一个动作会在日志页留一条任务（这是想要的） |
| 目录缓存放在 `Manager`（键 `设备+路径`），`Disconnect` 清空 | 界面是状态的函数，渲染线程不持有数据；代价是重连后所有文件夹都要重新读一次 |
| 树只显示目录，右侧表格显示全部条目（含文件、符号链接） | 树不会变成一个不可用的巨型列表；代价是文件只能在右侧看到 |
| 根节点固定五个：`/sdcard`、`/storage/emulated/0`、`/data/local/tmp`、`/system`、`/` | 三个平台与所有 Android 版本都给得出这五个入口；代价是设备特有的挂载点（`/mnt/media_rw`）要用户从 `/` 自己钻进去 |
| 打开页面默认落在 `/sdcard` | 这是用户真正会去的地方；代价：只想看 `/data/local/tmp` 的人每次要多点一下 |
| 大小列对目录显示「—」，权限显示 `%A` 原文（弹窗里补八进制 `%a`） | 目录的 4096 字节是实现细节，写出来只会被当成"占了 4 KB"；代价是弹窗与表格的权限写法不同（`drwxrwx---` vs `770`） |
| 一个文件夹最多列 2000 条，超出标注「列表已截断」 | 避免 1 MiB 输出上限造成静默丢条目；代价是超大目录要多进一层子目录才能看全 |
| 不做：搜索、排序切换、删除/重命名/新建目录、文件内容预览、多选批传 | 本轮只解决"看得见 + 传得动"；这些每一项都要新的确认弹窗或编辑器，留到后续 |

**已确认的取舍（用户答复）：** 页签形式与顺序「概览/资源/终端/文件/日志」；没有 btime 就不显示创建时间；其余（取数命令、静默执行、交互模型、状态位置）按推荐方案。

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `internal/core/remotefs.go` | 纯函数：`RemoteJoin` / `RemoteParent` / `RemoteBase` / `ParseStatLine` / `SortRemoteEntries` / `remoteListScript` / `parseListOutput`；`RemoteEntry`、`DirListing` 类型 | 创建 |
| `internal/core/remotefs_test.go` | 上表逐条表驱动测试，fixture 内联 | 创建 |
| `internal/core/manager_files.go` | `Manager.ListRemoteDir` / `DirListing` / `forgetDirListingsLocked` | 创建 |
| `internal/core/manager_files_test.go` | 静默执行、失败文案、跨代失效、断开清理 | 创建 |
| 修改 `internal/core/manager.go:129-172` | `Manager` 字段 `dirListings map[dirKey]DirListing` + 构造初始化 | 修改 |
| 修改 `internal/core/manager.go:583-604` | `Disconnect` 里 `delete` 之外调 `m.forgetDirListingsLocked(id)` | 修改 |
| `internal/app/files.go` | 文件页：两栏骨架、文件夹树、条目表格、目录导航、上传/下载动作、详情弹窗 | 创建 |
| `internal/app/files_test.go` | `scriptedRunner` + 视图断言（含离线态、空目录、截断、属性弹窗） | 创建 |
| 修改 `internal/app/state.go:17-23` | 页签常量插入 `detailFiles`；新增 `fileState`、`files map[uuid.UUID]*fileState`、`fileLoads`/`fileErrs` | 修改 |
| 修改 `internal/app/detail.go:186-196` | `components.Tabs` 增加「文件」，`switch` 增加 `case detailFiles` | 修改 |
| 修改 `internal/app/detail.go`（`ContentView` 末尾） | 调用 `vm.FileDetailView(c)` | 修改 |
| 修改 `internal/app/demo.go:19-40` | `demoRunner` 对列表脚本按目录返回假数据 | 修改 |
| 修改 `internal/app/dialog_test.go` | 「文件属性」弹窗加入关闭动画回归 | 修改 |
| 修改 `internal/compat/compat_test.go` | 断言 `components.Split`/`SplitOptions`/`Tree`/`TreeItemWithLeading`/`FileIcon`、`mygo.Dialog.Save` | 修改 |
| 修改 `docs/CHANGELOG.md`、`docs/ui-and-console.md`、`docs/architecture.md` | 面向用户的「新增」条目；文件页一节（布局、取数、陷阱）；决策记录 | 修改 |

---

### 任务 1：路径与 `stat` 输出的纯函数

**文件：**
- 创建：`internal/core/remotefs.go`
- 测试：`internal/core/remotefs_test.go`

**接口（后续任务按此调用，不得改名）：**

```go
// RemoteEntry is one entry of a device directory. Creation time is absent on
// purpose: Android's filesystems rarely keep a birth time, and a column that
// is almost always empty is worse than no column.
type RemoteEntry struct {
	Name     string    // 条目名，不含路径
	Path     string    // 设备上的绝对路径
	Dir      bool      // %F == "directory"
	Link     bool      // %F == "symbolic link"
	Size     int64     // 字节；目录的这个值是实现细节，界面不显示
	Mode     string    // %A，如 "drwxrwx---"
	Octal    string    // %a，如 "770"
	Modified time.Time // %Y；零值表示设备没给
}

// DirListing is one directory as the device reported it.
type DirListing struct {
	Path      string
	Entries   []RemoteEntry
	Truncated bool // 超过 maxRemoteDirEntries，列表被截断
}

// maxRemoteDirEntries bounds one listing so a huge directory cannot turn into
// a silent truncation of the runner's 1 MiB output cap.
const maxRemoteDirEntries = 2000

func RemoteJoin(dir, name string) string     // "/sdcard/" + "a" = "/sdcard/a"；"/" + "sdcard" = "/sdcard"
func RemoteParent(path string) string        // "/sdcard/Download" → "/sdcard"；"/" 与 "/sdcard" 都 → "/"
func RemoteBase(path string) string          // "/sdcard/a.txt" → "a.txt"；"/" → "/"
func SortRemoteEntries(entries []RemoteEntry) // 目录在前，再按小写名，再按原名（稳定）
func ParseStatOutput(out, dir string) DirListing
func ParseStatLine(line, dir string) (RemoteEntry, bool)
func remoteListScript(dir string) (string, error)
```

`ParseStatLine` 的输入是 `%F|%s|%a|%A|%Y|%n` 一行，用 `strings.SplitN(line, "|", 6)`：
字段不足 6 个、名称为空或 `.`/`..`、或以 `stat:` 开头（`stat` 自己报错的输出）一律返回 `false`。
`%n` 带 `./` 前缀，去掉后只取最后一段作 `Name`，`Path = RemoteJoin(dir, Name)`。

`remoteListScript` 生成的脚本（`mksh` 内建命令为主，循环里不 fork 外部命令）：

```sh
cd '<dir>' || exit 9
n=0
for f in ./* ./.[!.]* ; do
  [ -e "$f" ] || [ -L "$f" ] || continue
  n=$((n+1))
  [ "$n" -gt 2000 ] && break
  stat -c '%F|%s|%a|%A|%Y|%n' "$f" 2>/dev/null
done
[ "$n" -gt 2000 ] && echo '@@TRUNCATED'
exit 0
```

`<dir>` 里的 `'` 替换成 `'\''`；路径含 `\x00`/`\n`/`\r` 或不是以 `/` 开头时返回 `core.Error("路径不能包含换行或空字符")` / `core.Error("请填写绝对远端路径")`，脚本不生成。

- [ ] **步骤 1：编写失败的测试**

```go
func TestRemoteJoinKeepsOneSlash(t *testing.T) {
	cases := []struct{ dir, name, want string }{
		{"/sdcard/", "Download", "/sdcard/Download"},
		{"/sdcard", "Download", "/sdcard/Download"},
		{"/", "sdcard", "/sdcard"},
	}
	// …
}

func TestRemoteParentAndBaseStopAtRoot(t *testing.T) {
	// RemoteParent("/sdcard/Download") == "/sdcard"
	// RemoteParent("/sdcard") == "/"; RemoteParent("/") == "/"
	// RemoteBase("/sdcard/a.txt") == "a.txt"; RemoteBase("/") == "/"
}

func TestParseStatLineKeepsAPipeInTheName(t *testing.T) {
	entry, ok := ParseStatLine("regular file|12|660|-rw-rw----|1700000000|./a|b.txt", "/sdcard")
	if !ok || entry.Name != "a|b.txt" || entry.Path != "/sdcard/a|b.txt" {
		t.Fatalf("entry = %+v ok = %v", entry, ok)
	}
	if entry.Dir || entry.Size != 12 || entry.Octal != "660" || entry.Mode != "-rw-rw----" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestParseStatLineRejectsNoise(t *testing.T) {
	for _, line := range []string{
		"stat: './x': No such file or directory",
		"",
		"regular file|12",
		"directory|4096|770|drwxrwx---|1700000000|.",
	} {
		if entry, ok := ParseStatLine(line, "/sdcard"); ok {
			t.Fatalf("%q parsed into %+v", line, entry)
		}
	}
}

func TestParseStatLineReportsAnUnknownTime(t *testing.T) {
	entry, ok := ParseStatLine("directory|4096|770|drwxrwx---|0|./Download", "/sdcard")
	if !ok || !entry.Dir || !entry.Modified.IsZero() {
		t.Fatalf("entry = %+v ok = %v", entry, ok)
	}
}

func TestParseStatOutputMarksTruncation(t *testing.T) {
	out := "directory|4096|770|drwxrwx---|1700000000|./Download\n@@TRUNCATED\n"
	listing, _ := ParseStatOutput(out, "/sdcard")
	if !listing.Truncated || len(listing.Entries) != 1 {
		t.Fatalf("listing = %+v", listing)
	}
}

func TestSortRemoteEntriesPutsDirectoriesFirst(t *testing.T) {
	// 输入：b.txt, A.txt, Download(目录), download(目录)
	// 期望：Download, download, A.txt, b.txt
}

func TestRemoteListScriptQuotesAPathWithAQuote(t *testing.T) {
	script, err := remoteListScript("/sdcard/John's")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(script, `cd '/sdcard/John'\''s' || exit 9`) {
		t.Fatalf("script = %q", script)
	}
	if _, err := remoteListScript("/sdcard/a\nb"); err == nil {
		t.Fatal("a newline in a path must be refused")
	}
	if _, err := remoteListScript("sdcard"); err == nil {
		t.Fatal("a relative path must be refused")
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core/ -run 'TestRemote|TestParseStat|TestSortRemote' -v`
预期：FAIL，编译错误 `undefined: RemoteJoin` 等。

- [ ] **步骤 3：在 `internal/core/remotefs.go` 实现上述函数**

`ParseStatOutput` 逐行调 `ParseStatLine`，忽略解析失败的行（`stat` 报错会混在 stdout 之外，但设备偶尔会把它写进来）；`@@TRUNCATED` 行置 `Truncated` 后跳过；返回前不做排序（排序交给调用方显式 `SortRemoteEntries`，便于测试分开验）。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core/ -run 'TestRemote|TestParseStat|TestSortRemote' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/core/remotefs.go internal/core/remotefs_test.go
git commit -m "feat: parse device directory listings from stat output"
```

---

### 任务 2：`Manager.ListRemoteDir`（静默读、按设备缓存、断开清理）

**文件：**
- 创建：`internal/core/manager_files.go`、`internal/core/manager_files_test.go`
- 修改：`internal/core/manager.go`（字段 + 构造初始化 + `Disconnect` 清理）

**接口（后续任务按此调用，不得改名）：**

```go
// dirKey identifies one cached listing: a device and a path on it.
type dirKey struct {
	device uuid.UUID
	path   string
}

// remoteListTimeout bounds one directory read. A large /sdcard takes well over
// a second through a relay, so this is generous on purpose.
const remoteListTimeout = 15 * time.Second

// ListRemoteDir reads one directory off the device and caches it. It creates
// no task: browsing twenty folders must not fill the log page or the history.
func (m *Manager) ListRemoteDir(ctx context.Context, id uuid.UUID, dir string) (DirListing, error)

// DirListing returns the cached listing and whether it was read.
func (m *Manager) DirListing(id uuid.UUID, dir string) (DirListing, bool)

// forgetDirListingsLocked drops every listing of one device; m.mu must be held.
func (m *Manager) forgetDirListingsLocked(id uuid.UUID)
```

`Manager` 新字段：`dirListings map[dirKey]DirListing`（构造函数里 `map[dirKey]DirListing{}`）。
`Disconnect` 里与 `delete(m.resources, id)` 同一处调 `m.forgetDirListingsLocked(id)`。

`ListRemoteDir` 的形状照 `SampleResources`（`internal/core/sample.go:104`）：`operationTarget` → `validateTarget` → `remoteListScript` → `m.runner.Run(ctx, Command{Path: target.ADB, Args: []string{"-s", target.Serial, "shell", script}, Timeout: remoteListTimeout}, nil)` → `sanitizeError` / `Redact` → `targetIsCurrent` → `ParseStatOutput` + `SortRemoteEntries` → 存 cache + `notify()` → 返回。
退出码 9 专用：`cd` 失败 → `core.Error("目录不存在或不可读：" + dir)`。超时 → `core.Error("读取目录超时，请重试")`。

- [ ] **步骤 1：编写失败的测试**

```go
const listingFixture = "directory|4096|770|drwxrwx---|1700000000|./Download\n" +
	"regular file|12|660|-rw-rw----|1700000000|./照片.jpg\n"

func TestListRemoteDirCachesTheListing(t *testing.T) {
	m, _ := newTestManager(t, func(_ context.Context, c Command) (Result, error) {
		if !strings.Contains(c.Args[len(c.Args)-1], "stat -c") {
			t.Fatalf("unexpected command: %v", c.Args)
		}
		return Result{Output: listingFixture}, nil
	})
	profile := onlineProfile(t, m) // Connect + 断言 StateOnline

	listing, err := m.ListRemoteDir(context.Background(), profile.ID, "/sdcard")
	if err != nil { t.Fatal(err) }
	if len(listing.Entries) != 2 || listing.Entries[0].Name != "Download" || !listing.Entries[0].Dir {
		t.Fatalf("listing = %+v", listing)
	}
	cached, ok := m.DirListing(profile.ID, "/sdcard")
	if !ok || len(cached.Entries) != 2 { t.Fatalf("cached = %+v ok = %v", cached, ok) }
}

func TestListRemoteDirCreatesNoTask(t *testing.T) {
	// 读一次目录后断言 m.Tasks() 为空、注入的 Recorder 没收到任何 TaskRecord。
}

func TestListRemoteDirSaysWhenTheDirectoryIsMissing(t *testing.T) {
	// handler 返回 Result{ExitCode: 9}；断言 err 文案含 "目录不存在或不可读"。
}

func TestListRemoteDirRefusesAPathWithANewline(t *testing.T) {
	// err 非 nil，且 fakeRunner 一次都没被调用（列出 commandLog() 为证）。
}

func TestListRemoteDirRefusesAnOfflineDevice(t *testing.T) {
	// 未 Connect 的设备 → err 文案 "该设备的 ADB 尚未进入 device 状态"。
}

func TestListRemoteDirSurvivesADisconnect(t *testing.T) {
	// 读一次 → Disconnect → DirListing 返回 ok=false。
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core/ -run 'TestListRemoteDir' -v`
预期：FAIL，`undefined: ListRemoteDir`。

- [ ] **步骤 3：实现 `internal/core/manager_files.go` 与 `Manager` 字段**

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core/ -run 'TestListRemoteDir' -v && go test ./internal/core/`
预期：PASS（含既有测试，确认 `Disconnect` 改动没伤到别处）。

- [ ] **步骤 5：Commit**

```bash
git add internal/core/manager_files.go internal/core/manager_files_test.go internal/core/manager.go
git commit -m "feat: read a device directory without creating a task"
```

---

### 任务 3：页签与两栏骨架（树 + 表格 + 离线/空/失败态）

**文件：**
- 创建：`internal/app/files.go`、`internal/app/files_test.go`
- 修改：`internal/app/state.go`、`internal/app/detail.go`、`internal/app/demo.go`、`internal/compat/compat_test.go`

**接口（后续任务按此调用，不得改名）：**

```go
// fileState is one device's file page state. Everything that is not device
// data lives here, so views stay functions of state.
type fileState struct {
	treeWidth float32              // components.Split 的第一栏宽度
	path      string               // 右侧显示的目录
	selected  string               // 树与表格里高亮的路径
	open      map[string]*bool     // 树节点展开；map 的元素不可取址，所以存指针
	table     components.TableState
}

// fileRoots are the entry points the tree always offers.
var fileRoots = []string{"/sdcard", "/storage/emulated/0", "/data/local/tmp", "/system", "/"}

func (vm *ViewModel) fileStateFor(device uuid.UUID) *fileState
func (vm *ViewModel) filesPage(c *ui.Context, profile core.Profile, session core.Session)
func fileColumns() []ui.TableColumn            // 名称 / 大小 / 修改时间 / 权限
func formatRemoteTime(t time.Time) string      // "2006-01-02 15:04"；零值 → "—"
func (s *fileState) openAt(path string) *bool
```

`ViewModel` 新增字段（`state.go`）：

```go
	files     map[uuid.UUID]*fileState // 每台设备的页面状态；照 resources 的做法
	fileLoads map[string]bool          // "设备|路径" → 正在读，避免重复发命令
	fileErrs  map[string]string        // "设备|路径" → 读取失败的中文原因
```

`fileLoads` / `fileErrs` 会被 goroutine 读写，统一用 `ViewModel.mu`（提示条队列已用同一把锁）。
`fileStateFor` 的默认值：`path` 与 `selected` 都是 `"/sdcard"`。

页面骨架（`filesPage`）：

```go
if session.State != core.StateOnline { 说明为什么 + 不画表格 ; return }
ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
	components.Split(c, &state.treeWidth, left, right,
		components.SplitOptions{FirstMin: 180, SecondMin: 260, Default: 260}).Fill()
})
```

左栏：`ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(...)` → `ui.Scroll(c).FillWidth().Grow(1)` → `components.Tree(c, func(){ for _, root := range fileRoots { vm.treeNode(...) } })`。
节点用 `components.TreeItemWithLeading(c, core.RemoteBase(path), state.openAt(path), state.selected == path, leading, children)`，`leading` 给 `components.Icon(c, lucide.Folder())`；`children` 只在展开时传入（`open := *state.openAt(path)`）。
`nil` 用在这里要小心：`TreeItemWithLeading` 只有在 `open != nil && children != nil` 时才是分支，所以展开与未展开必须传同一个非 nil 指针，只切换 `children` 是否为 nil。

右栏：顶部一行是路径文本（`SingleLine` + `components.Tooltip`）与「刷新」「上传到此文件夹…」按钮（后两个在任务 4 接上，本任务先只放路径行）；下面是
`components.ScrollView(c, components.ScrollViewOptions{}).FillWidth().Grow(1).MinHeight(0).Label("设备文件列表")` → `components.Table(c, &state.table, fileColumns(), rows)`。
行用 `components.TableRow(c, entry.Path, entry.Name, state.selected == entry.Path, func(col int){ vm.fileCell(c, col, entry) })`：
- col 0：`ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center)` 里放 `components.FileIcon(c, entry.Name)`（目录用 `components.Icon(c, lucide.FolderOpen())`）+ `components.Text(c, entry.Name).FontSize(12).SingleLine()`
- col 1：目录写「—」，否则 `core.FormatBytes(entry.Size)`，`Align(ui.End)`
- col 2：`formatRemoteTime(entry.Modified)`
- col 3：`entry.Mode`

三种状态：读失败 → `读取失败：<原因>` + 「重试」按钮；空目录 → 「这个文件夹是空的」；`listing.Truncated` → 「只显示前 2000 项，请进入子文件夹查看其余内容」。
未连接 → 「连接后才能浏览设备文件。」（与 `resourceOffline` 同一位置与字号）。

演示模式（`demo.go`）：`demoRunner.Run` 里按脚本里的 `cd '<dir>'` 分支返回两段假数据——`/sdcard`（含 `Download`、`DCIM` 两个目录与 `照片.jpg`、`log.txt`）与 `/sdcard/Download`（一个目录 `旧文件`、一个文件 `update.zip`）；其余目录返回空输出（页面显示「这个文件夹是空的」）。

`compat_test.go` 新增编译期断言：

```go
	_ = components.Split
	_ = components.SplitPercent
	_ = components.Tree
	_ = components.TreeItemWithLeading
	_ = components.FileIcon
	_ = mygo.Dialog.Save
```

- [ ] **步骤 1：编写失败的测试**

```go
// scriptedRunner answers framework commands and hands the listing script to f.
type scriptedRunner struct {
	listing func(dir string) string
}

func (r scriptedRunner) Start(context.Context, core.Command, func(string)) (*core.Proc, error) {
	return nil, core.Error("Start is not used in view tests")
}
func (r scriptedRunner) Run(_ context.Context, c core.Command, _ func(string)) (core.Result, error) {
	for _, arg := range c.Args {
		if arg == "get-state" {
			return core.Result{Output: "device\n"}, nil
		}
	}
	if script := c.Args[len(c.Args)-1]; strings.Contains(script, "stat -c") {
		return core.Result{Output: r.listing(dirOfScript(script))}, nil
	}
	return core.Result{}, nil
}

// newFilesViewModel builds a connected device whose /sdcard holds the fixture.
func newFilesViewModel(t *testing.T, listing func(string) string) *ViewModel

func TestFilePageListsFoldersAndFiles(t *testing.T) {
	vm := newFilesViewModel(t, sdcardFixture)
	vm.DetailTab = detailFiles
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	tt.Frame()
	vm.ensureDirs(/* 首次进入读 /sdcard */)
	tt.Frame()
	for _, want := range []string{"Download", "照片.jpg", "权限"} {
		if !tt.HasText(want) { t.Fatalf("missing %q; texts = %v", want, tt.Texts()) }
	}
}

func TestFilePageHasNoCreationTimeColumn(t *testing.T) {
	// tt.HasText("创建时间") 必须为 false（页签、表头、弹窗都不出现）。
}

func TestFilePageExplainsAnOfflineDevice(t *testing.T) {
	// 未 Connect → HasText("连接后才能浏览设备文件。") 且 !HasText("权限")。
}

func TestFilePageNamesAnEmptyFolder(t *testing.T) {
	// /sdcard 返回空输出 → HasText("这个文件夹是空的")。
}

func TestFilePageWarnsWhenTheListingIsTruncated(t *testing.T) {
	// 输出带 @@TRUNCATED → HasText("只显示前 2000 项")。
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app/ -run 'TestFilePage' -v`
预期：FAIL，`undefined: detailFiles` / `undefined: newFilesViewModel`。

- [ ] **步骤 3：在 `internal/app/files.go` 实现骨架，并在 `state.go`、`detail.go`、`demo.go` 接上**

`detail.go` 的 `components.Tabs(...)` 改成 `"概览", "资源", "终端", "文件", "日志"`，`switch` 里 `case detailFiles:` 先调 `vm.Manager.StopResourceSampling(profile.ID)` 再 `vm.filesPage(c, profile, session)`（离开资源页要停轮询，和设备页其他分支一致）。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app/ -run 'TestFilePage' -v && go test ./internal/app/`
预期：PASS。若 `TestDialogsCloseWithoutPanic` 或页签相关旧测试变红，说明页签名/索引写错，按具名常量修正。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/files.go internal/app/files_test.go internal/app/state.go internal/app/detail.go internal/app/demo.go internal/compat/compat_test.go
git commit -m "feat: add a file tab with a folder tree and a file table"
```

---

### 任务 4：目录导航、懒加载与上传

**文件：**
- 修改：`internal/app/files.go`、`internal/app/files_test.go`

**接口（后续任务按此调用，不得改名）：**

```go
// ensureDirs loads the directories that are not cached yet, at most one read in
// flight per path, and records failures where the page can show them.
func (vm *ViewModel) ensureDirs(profile core.Profile, paths ...string)

// showDir points the right pane at dir; the tree follows.
func (vm *ViewModel) showDir(profile core.Profile, state *fileState, dir string)

// pushInto uploads local files into remoteDir through the task machinery.
func (vm *ViewModel) pushInto(profile core.Profile, remoteDir string)
```

行为细节：

- 进入页面或切换页签时 `vm.ensureDirs(profile, state.path)`；展开节点时 `vm.ensureDirs(profile, dir)`。
- `ensureDirs` 在 goroutine 里 `m.ListRemoteDir(ctx, id, p)`；错误写进 `vm.fileErrs[key]`（不弹提示条——展开文件夹失败是页面状态，不是一次性事件）；无论成败都清 `vm.fileLoads[key]` 并 `Invalidate`。
- 点击树节点：`state.selected = path` + `vm.showDir(...)` + `vm.ensureDirs(...)`。
- 点击表格里的**目录**行：`state.path = entry.Path`、`state.selected = entry.Path`、`*state.openAt(entry.Path) = true`、`vm.ensureDirs(profile, entry.Path)`。
- 右栏顶部「刷新」：不删缓存，直接重新 `ListRemoteDir`（覆盖同一个键）；「上传到此文件夹…」调 `vm.pushInto(profile, state.path)`。
- `pushInto`：`mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择要上传到 " + remoteDir + " 的文件", Multiple: true})` → 对每个路径 `m.BeginADBTask(ctx, profile.ID, "上传 "+fileName(p), []string{"push", p, remoteDir}, 30*time.Minute)`，错误 `vm.Fail(err)`。用户取消（`err != nil || len(paths)==0`）什么都不做。

- [ ] **步骤 1：编写失败的测试**

```go
func TestFileTreeExpandsIntoItsSubdirectories(t *testing.T) {
	// 点击树上的 "Download" → 发一次 /sdcard/Download 的读取；
	// 展开后 HasText("update.zip")。
}

func TestFileRowNavigationOpensTheFolder(t *testing.T) {
	// 表格里点 "Download" 行 → 右侧路径变成 /sdcard/Download，出现 update.zip。
}

func TestFileTreeReadsEachFolderOnlyOnce(t *testing.T) {
	// 连续 Frame 十次，计数 scriptedRunner 收到的列表命令：同一路径只读一次。
}

func TestFilePageShowsAReadFailureInline(t *testing.T) {
	// runner 返回 ExitCode 9 → HasText("读取失败：目录不存在或不可读：/sdcard") 且 HasText("重试")。
}

func TestUploadStartsATaskForEverySelectedFile(t *testing.T) {
	// 注入 mygo.Dialog.Open 的假实现（见注释）→ 断言 Tasks() 里出现「上传 a.txt」
	// 且 commandLog 里是 push a.txt /sdcard。
}
```

`mygo.Dialog` 是包级变量，测试里可替换：在 `files.go` 里包一层 `var openLocalPaths = mygo.Dialog.Open` / `var saveLocalPath = mygo.Dialog.Save`，测试替换这两个变量并在 `t.Cleanup` 里还原（不要直接在测试里改 `mygo.Dialog`）。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app/ -run 'TestFileTree|TestFileRow|TestFilePageShowsAReadFailure|TestUploadStarts' -v`
预期：FAIL。

- [ ] **步骤 3：在 `internal/app/files.go` 实现导航、懒加载与上传**

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app/ -run 'TestFile' -v && go test ./internal/app/`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/files.go internal/app/files_test.go
git commit -m "feat: navigate device folders lazily and upload into them"
```

---

### 任务 5：条目详情弹窗（属性 + 下载 / 上传）

**文件：**
- 修改：`internal/app/files.go`、`internal/app/state.go`、`internal/app/detail.go`（`ContentView` 末尾）、`internal/app/files_test.go`、`internal/app/dialog_test.go`

**接口（后续任务按此调用，不得改名）：**

```go
// FileDetail is the entry whose dialog is open; nil closes it.
type FileDetail struct {
	Device uuid.UUID
	Entry  core.RemoteEntry
}

// FileDetailView draws the entry dialog. ContentView calls it last, next to
// TransferView, so it paints above the page.
func (vm *ViewModel) FileDetailView(c *ui.Context)

// pullEntry downloads one entry: a file save dialog for files, a folder picker
// for directories.
func (vm *ViewModel) pullEntry(profile core.Profile, entry core.RemoteEntry)
```

`ViewModel` 新字段：`FileDetail *FileDetail`。

弹窗按 `docs/ui-and-console.md` §3.6 的固定骨架：`components.Dialog(c, &open, components.DialogOptions{Label: "文件属性"}, func(panel ui.Element){…})`，面板 `Padding(18).Gap(10).Width(460)`：

1. 标题行：`entry.Name`（`FontSize(18).Bold().SingleLine()`）+ 右上角 `components.IconButton(c, lucide.X(), "关闭", components.IconButtonPlain)`，被截断时挂 `components.Tooltip`；
2. 副标题：`entry.Path`（`FontSize(11).TextColor(theme.TextMuted).SingleLine()` + Tooltip）；
3. 四行属性，用已有的 `detailRow(c, label, value)` 写法（标签 90 宽）：
   `文件名` / `文件大小`（目录写「—」，文件写 `core.FormatBytes(size)`）/ `最后修改时间`（`formatRemoteTime`）/ `文件权限`（`entry.Mode + " (" + entry.Octal + ")"`）；
   **不出现「创建时间」**；
4. 底栏只有一个主作用按钮 + 右上角关闭：文件给 `actionButton(c, lucide.Download, "下载到本地…", components.ButtonPrimary)` → `vm.pullEntry(...)`；目录给 `actionButton(c, lucide.Download, "下载此文件夹…", components.ButtonPrimary)`（同样走 `pullEntry`）+ 次一级 `actionButton(c, lucide.Upload, "上传到此文件夹…", components.ButtonSecondary)` → `vm.pushInto(profile, entry.Path)`；文件行也给「上传到此文件夹…」，目标目录为 `core.RemoteParent(entry.Path)`；
5. `if !open { vm.FileDetail = nil }` 是唯一收口。

`pullEntry`：目录 → `saveLocalPath` 换成目录选择（`openLocalFiles(mygo.OpenDialogOptions{Title: "选择保存目录", Directory: true})`）+ `pull entry.Path destDir`；文件 → `saveLocalFile(mygo.SaveDialogOptions{Title: "保存到本地", DefaultPath: entry.Name})` + `pull entry.Path destFile`，两者都 `BeginADBTask(..., "下载 "+entry.Name, ..., 30*time.Minute)`，`DefaultPath` 为空时用 `entry.Name` 兜底。

- [ ] **步骤 1：编写失败的测试**

```go
func TestFileRowOpensTheAttributeDialog(t *testing.T) {
	// 点表格里的 "照片.jpg" → HasText("文件属性")、HasText("12 B")、
	// HasText("最后修改时间")、HasText("-rw-rw---- (660)")；
	// 且 !HasText("创建时间")。
}

func TestAttributeDialogOffersBothTransfers(t *testing.T) {
	// 弹窗里 HasText("下载到本地…") 与 HasText("上传到此文件夹…")。
}

func TestAttributeDialogPullsAFileToTheChosenPath(t *testing.T) {
	// 替换 saveLocalFile 返回 "/tmp/x.jpg" → 断言 Tasks() 出现「下载 照片.jpg」
	// 且 commandLog 里有 pull <path> /tmp/x.jpg。
}

func TestAttributeDialogDoesNotPullWhenThePickerIsCancelled(t *testing.T) {
	// saveLocalFile 返回 ("", nil) → 没有任何新任务。
}
```

`dialog_test.go` 的用例表加一行：
`{"file detail", func(vm *ViewModel) { vm.FileDetail = &FileDetail{Device: …, Entry: core.RemoteEntry{Name: "a.txt"}} }, func(vm *ViewModel) { vm.FileDetail = nil }}`。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app/ -run 'TestAttributeDialog|TestFileRowOpensTheAttributeDialog' -v`
预期：FAIL，`undefined: FileDetail`。

- [ ] **步骤 3：实现 `FileDetailView`、`pullEntry`，并在 `ContentView` 末尾调用**

`ContentView` 里顺序：先页面，再 `vm.TransferView(c)` 等既有对话框，最后 `vm.FileDetailView(c)`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app/ -run 'TestAttributeDialog|TestFileRow|TestDialogsCloseWithoutPanic' -v`
预期：PASS（含关闭动画回归）。

- [ ] **步骤 5：Commit**

```bash
git add internal/app/files.go internal/app/files_test.go internal/app/state.go internal/app/detail.go internal/app/dialog_test.go
git commit -m "feat: show file attributes and transfer a single entry"
```

---

### 任务 6：文档与全量验证

**文件：**
- 修改：`docs/CHANGELOG.md`（`## [未发布]` → `### 新增`）
- 修改：`docs/ui-and-console.md`（新增「6. 设备文件页」一节，其后章节号顺延）
- 修改：`docs/architecture.md`（第 15 节决策记录补两条）

- [ ] **步骤 1：写 CHANGELOG 条目**

```markdown
- 设备页新增「文件」标签页：左边是文件夹树（`/sdcard`、`/storage/emulated/0`、`/data/local/tmp`、`/system`、`/` 五个入口，展开时才读取），右边是当前文件夹的条目表格（名称 / 大小 / 修改时间 / 权限）；点条目弹出属性弹窗，可把文件下载到本地、把本地文件上传到当前文件夹。设备不提供创建时间，因此不显示该字段。
```

- [ ] **步骤 2：写 `docs/ui-and-console.md` 的文件页一节**

必须写进去的四条（都是本轮踩到的）：
1. 读目录走 `Manager.ListRemoteDir`，**不建任务**；上传/下载才走 `BeginADBTask`；
2. `components.Split` 两栏都要 `MinWidth(0)`，`treeWidth` 存在 `ViewModel` 里；
3. `components.TreeItemWithLeading` 的 `open` 指针来自 `fileState.openAt(path)`（**map 的元素不可取址**，不能写 `&state.open[path]`）；
4. 表格滚动用 `components.ScrollView` 包 `components.Table`，与资源页同一写法；行用 `FillWidth()` 不用 `Fill()`。

- [ ] **步骤 3：写 `docs/architecture.md` 决策记录两条**

「列目录为什么静默执行（代价：日志页看不到读了哪个目录）」「为什么不做 `ls -la` 兜底解析、为什么没有创建时间列（代价：没有 toybox `stat` 的老设备上该页不可用）」。

- [ ] **步骤 4：全量验证**

```sh
gofmt -l .                                  # 必须无输出
go vet ./...
go test ./...
go test -race ./internal/core ./internal/app
CGO_ENABLED=0 GOOS=linux go build ./... && CGO_ENABLED=0 GOOS=windows go build ./... && CGO_ENABLED=0 GOOS=darwin go build ./...
go run ./cmd/tailcat-manager --demo          # 目视：文件页有假数据，展开 /sdcard 能看到 Download 与 照片.jpg
```

预期：全部通过（`--demo` 需真实窗口，无头环境跳过这一步，改用 `go test ./internal/app/ -run TestFilePage` 覆盖）。

- [ ] **步骤 5：Commit**

```bash
git add docs/CHANGELOG.md docs/ui-and-console.md docs/architecture.md
git commit -m "docs: describe the device file page and its trade-offs"
```

---

## 自检记录

- **规格覆盖：** 页签入口与顺序（任务 3）、左右两栏（任务 3）、文件夹树（任务 3/4）、条目表格（任务 3）、点击弹窗（任务 5）、push/pull（任务 4/5）、属性五项中的四项（任务 5，创建时间按用户裁决去除）、多个「审查重点」各自有测试。
- **类型一致性：** `RemoteEntry` / `DirListing` 在任务 1 定义、任务 2 返回、任务 3/5 渲染，字段名一致（`Dir`/`Link`/`Size`/`Mode`/`Octal`/`Modified`）；`fileState` 字段在任务 3 定义、任务 4/5 使用。
- **比例：** 本计划没有内联函数体，只有签名、脚本与测试名。
- **执行方式：** 本环境没有子智能体调度工具，`subagent-driven-development` 不可用，按 `executing-plans` 在当前会话逐任务内联执行，最后一个任务结束后做一次全分支审查。

---

## 实现偏差（执行后补记）

实际落地与上文的差异，其余照计划执行：

| 计划 | 实际 | 原因 |
|---|---|---|
| `ParseStatOutput` 返回 `(DirListing, bool)` | 只返回 `DirListing` | 那个 bool 没有任何调用方会用：命令成功而没有条目就是空目录，页面文案一样 |
| 树的根用 `core.RemoteBase(path)` 作标签 | 根用整条路径（`/storage/emulated/0` 而不是 `0`） | `RemoteBase` 对多级路径给出的是最后一段，作根节点标签没有意义 |
| 取本地文件的包装变量叫 `openLocalFiles` / `saveLocalFile` | `openLocalPaths` / `saveLocalPath` | 它们也用于目录选择（`Directory: true`），名字不该带 File |
| 两张表各自渲染「读取失败 + 重试」 | 树节点与右侧表格各有一份 | 失败可能发生在树里展开的节点上，右侧此时还是好的 |
| — | `fileMu` 单独一把锁 | `fileLoads`/`fileErrs` 由命令 goroutine 写，不应该和提示条队列共用一把锁 |

新增文件：`internal/core/remotefs.go`（+测试）、`internal/core/manager_files.go`（+测试）、`internal/app/files.go`（+测试）。
