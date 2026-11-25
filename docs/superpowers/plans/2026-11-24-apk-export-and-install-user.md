# 资源页「保存 APK」+ 安装 APK 指定用户 + 概览设备用户 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** ①资源页每个应用行在「解析应用信息」之后多一个下载图标，点了**每次都要问**存到哪个目录，然后把该应用的 base APK 拉到本地，同一个弹窗里显示进度、失败原因或保存结果；②安装 APK 的安装方式不变（仍是 `adb install -r`），但先弹窗让用户选**装到哪个 Android 用户**；③概览页显示设备上的**全部用户**（含当前用户标记）。

**架构：** 核心新增三块：`ParsePullPercent`（把 `adb pull` 的进度行解析成百分比，纯函数）、`Manager.AppAPK`（包名 → base.apk 远端路径与大小）、`DeviceUser` 的解析与 `Manager.DeviceUsers`（`pm list users` + `am get-current-user`，一趟 shell）；用户列表同时写进 `Session.Users`，所以连接和「刷新信息」都会把它带回来，概览直接读。界面新增两个弹窗状态机：`apkSave`（查找路径 → 下载 → 完成/失败）与 `installDraft`（选用户 → 安装），下载与安装都复用现有任务机制（`Manager.BeginADBTask`），超时、取消、脱敏、历史不改一行。

**技术栈：** Go（纯，无 cgo）、mygo `ui`（`Tester` 做视图测试）、onevra `components.Dialog` / `components.Progress` / `components.Select` / `components.IconButton` / `lucide.Download`、adb 命令行（`pm list users`、`am get-current-user`、`pm list packages -f`、`stat`、`pull`、`install --user`）。

**规格：** 本文档 §1（用户可见行为，逐字文案）、§2（为什么这样做）、§3（现状与"为什么安装要保持 `-r`"）。执行前把 §4 的决定读一遍——它们是可以被推翻的，但要先推翻再写代码。

> **本文件的两种用途：** 任务 1–7 是已经执行完的实现记录（复选框全勾上、提交在 `feat/apk-export-install-user` 上）；验收后用户又提了 5 处改动，**§1 已按最终行为改写**，改动清单与钉住它们的测试在 §6。两者都已并入 `main`，不要按 §6 之前的旧描述再实现一遍。

## 全局约束

- 沿用 `AGENTS.md`：纯 Go 无 cgo；`internal/core` 不 import app；**不许 `exec.Command`**，一律 `runner.Run`；不持有 `m.mu` 时调用会上锁的方法；异步分支写状态前先 `isCurrentLocked`。
- 文案中文、集中在 `internal/core/messages.go`（核心原因）与视图里（界面），测试**逐字**断言。
- TDD 顺序不可跳：写测试 → 跑一次看到 RED（原因必须是"功能缺失"）→ 最少实现 → 跑绿 → `gofmt -w` / `go vet ./...` / `go test ./...` → commit。
- GUI 只用现成组件：`components.Dialog`、`components.Progress`、`components.Select`、`components.IconButton`、`lucide.Download`；**不许**用 `ui.Row`/`ui.ButtonBase` 现拼进度条、下拉或弹窗，**不许**用 `components.ButtonGhost`。
- 表格只经 `internal/app/table.go` 的 `tableCard`；行内按钮用 `components.IconButton`，行激活只认 `RowClicked()`。
- 行式元素用 `FillWidth()`，不要用 `Fill()`。
- 每个改变用户行为的提交，同一次提交里带 `docs/CHANGELOG.md`（当天没有 `## 2026-11-24` 就新建在最上面）。
- 分支 `feat/apk-export-install-user`（已合并进 `main`）；提交前缀 `feat:` / `fix:` / `test:` / `docs:`。

## 审查重点（Review Focus）

1. **包名/用户 id 被拼进 shell 字符串**——`pm list packages -f <pkg>` 与 `stat -c '%s' <path>` 都是 `adb shell <一整行>`，包名来自设备 `/proc` 的进程名（不可信）。含 `;`、`$(...)`、空格的包名必须被拒绝，**且拒绝时一条 adb 都不许发**（测试 `TestAppAPKRejectsAPackageNameThatCouldRunACommand`）。用户 id 也必须是解析出来的整数，不许把 `pm list users` 的原始文本拼进命令。
2. **同名文件被静默覆盖**——同一应用同一个包名，导出文件名必然相同；用户上一次导出的 APK 不能被覆盖（测试 `TestUniqueAPKPathNeverOverwritesWhatIsAlreadyThere`）。
3. **进度条卡在 0% 或直接跳 100%**——`adb pull` 的进度行随版本与 stdout 是否 TTY 而有无（§2.3）。没有百分比时退回"本地字节 / 远端字节"，两个都没有就用不确定进度（`-1`），不许显示假的 0%（测试 `TestAPKProgressFallsBackToTheBytesOnDisk`、`TestAPKProgressIsIndeterminateWithoutAnyTotal`）。
4. **把 APK 装错了用户**——用户列表为空、或 `pm list users` 读失败时不能默默装到 user 0 却不说明；装到哪个用户必须在弹窗里看得见（测试 `TestInstallDialogSaysWhichUserItWillUse`、`TestInstallFallsBackToTheDefaultUserWhenTheListFails`）。
5. **一条命令解析拖垮概览**——`pm list users` 在部分设备上直接报 `SecurityException` 或输出乱码，解析必须容忍并让概览显示 `—` 而不是空白或崩溃（测试 `TestParseDeviceUsersSkipsGarbage`、`TestOverviewShowsEveryDeviceUser`）。
6. **下载中关窗把任务丢了**——关窗必须是"取消任务"或"任务继续跑在任务列表里"，不能留下没人管也没取消的 `adb pull`，也不能重复点出两个任务（测试 `TestAPKSaveStartsOnePullOnly`、`TestAPKSaveCancelStopsTheTask`）。
7. **「打开目录」启动了却看不见**——GUI 子进程不能继承给控制台子进程准备的隐藏标志（`CREATE_NO_WINDOW`/`HideWindow`）：已有文件管理器窗口时它只转发请求、不显示窗口，用户看到的是「点了没反应」（`TestVisibleCommandShowsItsWindow`）。
8. **长值被窗口右边缘裁掉**——详情行的值必须有 `Grow(1).MinWidth(0)`，否则按自身宽度铺到窗外，且没有横向滚动能看到被裁的部分（截图 `03c-device-overview-users.png`、`03-device-overview.png`）。

---

## 1. 用户可见行为（逐字文案）

### 1.1 入口一：应用行的「保存应用安装包」

资源页（`DetailTab == detailResources`）应用表第一列，在现有 `appMetaAction`（「解析应用信息」/「重新读取应用信息」/重试）之后，加 `components.IconButton(c, lucide.Download(), "保存应用安装包", components.IconButtonCompact)`。

- 顺序固定：解析 → 保存 APK；与设备状态无关地显示（离线时由弹窗说明原因，藏掉会让人以为功能不存在）。
- 可访问名称就是测试查找键：`tt.Click("保存应用安装包")`。

### 1.2 「保存 APK」：每次都问目录，取消则什么也不做

**不设默认目录。** 点图标 → 直接弹系统目录选择（`mygo.Dialog.Open{Directory: true}`，`Title: "选择保存 APK 的目录"`）→ 选中后**立刻开始**，并把弹窗切到进度阶段；取消选择则什么也不发生（不弹提示、不建目录、不起任务）。

### 1.3 「保存 APK」弹窗（进度阶段，单一 `components.Dialog`）

| Stage | 内容 |
|---|---|
| `apkStageLocating` | `正在查找应用安装包…` + 不确定进度条 |
| `apkStageDownloading` | `正在保存 <文件全名>`、`components.Progress`（有百分比 `p/100`，否则 `-1`）、`42%` 或 `已传输 12.0 MB`、`取消`（中止任务并关窗） |
| `apkStageDone` | `已保存到 <绝对路径>`、`打开保存目录`（`vm.OpenDir` 非 nil 时显示）、`关闭`（`ButtonPrimary`） |
| `apkStageFailed` | `保存失败：<原因>`、`关闭`（`ButtonPrimary`） |

说明行固定为 `只保存设备上的基础安装包（base.apk），不含拆分包。`（`apkStageLocating` 与 `apkStageDownloading` 都显示）。失败不自动重试，留在弹窗里让用户看清楚再关。

### 1.4 入口二：「安装 APK」→ 先在弹窗里选文件，再选用户

概览「设备操作」里的「安装 APK」保留原位置。点它**立刻弹出**「安装 APK」弹窗，文件在弹窗里选（原先的做法是先弹系统文件选择、再弹窗，验收后改掉了，见 §6）。

| 元素 | 内容 |
|---|---|
| 标题 | `安装 APK` |
| 行 | `APK 文件` ｜ 未选时灰色 `未选择 APK 文件`，选中后显示**完整路径**（长路径在弹窗内换行） |
| 按钮 | `选择 APK 文件`（`ButtonOutline`，独立一行、保持自身宽度；直接放在弹窗列里会被拉成整条横幅，读成主操作） |
| 行 | 说明 `覆盖安装：同包名的应用会被替换，应用数据保留。` |
| 行 | `安装到用户` + `components.Select`（选项形如 `0 · Owner（当前）`、`10 · 工作资料`） |
| 加载中 | `components.Spinner` + `正在读取设备用户…` |
| 读不到用户 | `读不到设备上的用户列表，将安装到默认用户`（**仍然可以安装**，走今天的行为，见 §4） |
| 按钮 | `取消`（`ButtonOutline`）、`安装`（`ButtonPrimary`，**没选文件时禁用**；后台还有一道校验，键盘路径也起不来任务） |

安装命令：`install -r --user <id> <path>`；用户列表读不到或用户没选（列表里只有一个用户也照常选中它）时退回 `install -r <path>`。也就是说**安装方式仍然是覆盖安装，只是多了用户这一维**。

### 1.5 概览页：设备用户（表格）

概览在「Shell 权限」之后新增一节：

| 列 | 内容 |
|---|---|
| `用户`（70） | adb 认的数字，如 `0`、`10` |
| `名称`（最小 140） | 设备自己给的名字，如 `Owner`、`工作资料` |
| `状态`（90，**居中对齐**） | `当前用户`（绿色徽标）/ `运行中` / `未启动` |

一个用户都没有时显示 `—`（不是只有表头的空表）。数据来自 `Session.Users`，连接时与点「刷新信息」时都会重新读一次（§2.2）。

> 这一节最初是一行 `0 Owner（当前） · 10 工作资料` 的文字，验收后改成表格；状态列居中是因为表头、文字与徽标宽度不同，左对齐时读起来不在一条线上（见 §6）。

### 1.6 概览页：Shell 权限放进框里

`id` 在真机上是一行很长的 groups。它有自己的标签行和一个带内边距的圆角边框，长串在框内换行、左右都留白：

- 标签 `Shell 权限` 单独一行，值在下面的框里（等宽字体）；
- 没读到值时框里显示 `—`，不留一个空框（空框读起来像渲染坏了）。

同时修了所有详情行的根因：值文本以前没有 `Grow(1).MinWidth(0)`，长值按自身宽度铺到窗口右边被裁掉、又没有横向滚动；现在都在页内换行。

---

## 2. 为什么这样做

### 2.1 为什么不给默认目录

用户要求"每次询问"。默认目录还有一个隐藏代价：`~/Downloads` 在 Linux 上并不一定存在或不一定叫这个名字，猜错了就悄悄在用户主目录里建一个；而系统目录选择器本来就记住了上次的位置，第二次点只多一次确认。

### 2.2 用户列表读一次，两个地方用

`pm list users` + `am get-current-user` 合成**一条** shell（`pm list users; echo @@CURRENT; am get-current-user`），一趟往返、一个解析函数（`ParseDeviceUsers` + `ParseCurrentUser`）。这条查询同时被两条路径使用：

1. `RefreshDeviceInformation`（连接时 + 「刷新信息」按钮）→ 写 `Session.Users` → 概览显示；
2. `Manager.DeviceUsers`（安装弹窗打开时）→ 现读一次，因为用户可能刚在工作资料里装了个应用，而 session 里的列表是连接那一刻的。

### 2.3 进度有两个来源

`adb pull` 的进度行形如 `[ 42%] /data/app/~~x==/com.foo-y==/base.apk`，可能带 `\r`、带 `\n`，也可能**完全不打印**（stdout 不是 TTY 的某些版本）。所以：

1. 先在任务输出里找最后一条 `[nn%]`（`core.ParsePullPercent`）；
2. 找不到就用 `os.Stat(本地文件).Size / AppAPK.Size`（远端大小在解析阶段已拿到）；
3. 两个都不行就是不确定进度（`components.Progress(c, -1)`）。

三者都在同一帧算完：没有额外 goroutine、没有定时器。

### 2.4 复用任务机制

`Manager.BeginADBTask` 已经提供：目标校验两次（启动时 + 执行前）、超时、输出脱敏、`CancelTask`、任务历史与输出对话框。自己再写一条 `pull` / `install` 执行路径会重复这五件事，并绕开「任务启动后不要改它的目标」的约束。

---

## 3. 现状：安装 APK 现在是什么行为

`internal/app/actions.go` 的 `chooseAPK` 目前执行：

```
adb -s <serial> install -r <本地.apk 绝对路径>
```

`-r` 是 replace/reinstall：**已安装的同包名应用会被覆盖，应用数据保留**（不动 `/data/data/<pkg>`）。具体边界：同签名且版本号 ≥ 现装 → 覆盖成功；签名不同 → `INSTALL_FAILED_UPDATE_INCOMPATIBLE`；降级 → `INSTALL_FAILED_VERSION_DOWNGRADE`（没有 `-d`）；`testOnly` 的 APK → `INSTALL_FAILED_TEST_ONLY`（没有 `-t`）；不指定用户 → 装到默认用户。

本次**不动** `-r`、不加 `-d`/`-t`，只加 `--user <id>`。`--user` 的行为已实测（emulator-5554 + platform-tools 34 / adb 1.0.41，两个用户）：对「只装在 user 0 的应用」执行 `install -r --user 10` 后 user 10 也能看到它，说明 adb 确实转发该参数；`--user 99` 报 Success 是 PackageManager 把未知 id 归到当前用户，不是 adb 丢参数。更老的 platform-tools 若**静默忽略**该参数，安装会落到默认用户而界面不会察觉——这条写进 CHANGELOG 的「已知限制」，也写进 `docs/architecture.md` 的决策表（没有改走「先 push 再 `pm install`」的理由）。

---

## 4. 我替你定下的细节（可在实现前推翻）

| 决定 | 取值 | 理由 / 代价 |
|---|---|---|
| 保存目录 | **没有默认值**，每次点图标就弹系统目录选择；取消 = 什么也不做 | 用户明确要求；代价是每次多一次确认 |
| 文件名 | `<应用名>_<包名>.apk`；应用名里 `<>:"/\|?*` 与控制字符换成 `_`，截到 60 字符 | 只用包名不好认、只用应用名会撞车；Windows 非法字符必须挡掉 |
| 重名 | ` (2)`、` (3)`… 插在 `.apk` 之前，**绝不覆盖** | 导出是备份动作，静默覆盖等于丢文件 |
| 关闭弹窗（下载中） | 「取消」= `Manager.CancelTask` + 关窗 | 不偷偷留后台任务，也不假装停了 |
| 安装用户默认值 | 设备报告的**当前用户**；报告不出来时选列表第一个 | 99% 的情况用户就想装到自己正在用的那个 |
| 用户列表读不到 | 弹窗写明「将安装到默认用户」，仍然允许安装（走今天的命令） | 老设备/权限受限设备上不能把安装功能整个废掉 |
| `pm list users` 一个用户都没有 | 视为"读不到"，同上 | 与「空列表"混淆会让人以为设备没用户 |
| 安装文件在哪选 | 弹窗内选（选中后显示完整路径），没有文件时「安装」禁用 | 用户要求；比「先选文件再弹窗」多一步确认，换来的是路径与目标用户同屏可见 |
| 打开保存目录 | 用可见窗口派生（`VisibleCommand`）+ 成功后提示 | 修复「点了没反应」；代价是多一个派生函数 |
| 概览的设备用户 | 表格而不是一行文字；状态列居中 | 用户多了要比对，一行文字读不清；代价是概览多占一块高度 |

## 5. 文件结构

**创建**

| 文件 | 职责 |
|---|---|
| `internal/core/pull.go` | `ParsePullPercent`：从任务输出读 `adb pull` 百分比（纯函数） |
| `internal/core/users.go` | `DeviceUser`、`ParseDeviceUsers`、`ParseCurrentUser`（纯函数）、`Manager.DeviceUsers`、`readDeviceUsers` |
| `internal/core/manager_apk.go` | `AppAPK`、`Manager.AppAPK`、`validPackageName` |
| `internal/app/apk.go` | `apkSave` 状态机、`beginAPKSave` / `runAPKSave` / `APKView`、`apkFileName` / `uniqueAPKPath` / `apkProgress` / `pickSaveFolder` |
| `internal/app/install.go` | `installDraft`、`openInstallDialog` / `submitInstall` / `InstallView`、`installUserLabel` / `installArgs` |
| `internal/app/apk_test.go` | 上面两块的纯函数与视图测试 |

**修改**

| 文件 | 改动 |
|---|---|
| `internal/core/messages.go` | `MsgAPKPathMissing`、`MsgAPKPathUnknown`、`MsgAPKPackageInvalid`、`MsgUsersUnreadable` |
| `internal/core/session.go` | `Session.Users []DeviceUser` |
| `internal/core/manager_tasks.go` | `RefreshDeviceInformation` 多读一次用户列表并写进 session（读失败不影响其他字段） |
| `internal/app/resources.go` | `appCell` 在 `appMetaAction` 之后调 `vm.apkDownloadAction(c, device, app)` |
| `internal/app/actions.go` | `chooseAPK` 改成"选文件 → 打开安装弹窗"，文件选择抽成 `pickAPKFile` |
| `internal/app/detail.go` | `overview` 加「设备用户」行；`ContentView` 末尾调 `vm.APKView(c)` 与 `vm.InstallView(c)` |
| `internal/app/state.go` | `ViewModel` 加 `APK *apkSave` + `apkMu`、`APKInstall *installDraft` + `installMu` |
| `internal/app/screenshots_test.go` | `shotCases()` 加三条：保存 APK 进度、安装 APK 选用户、概览设备用户 |
| `internal/app/dialog_test.go` | 关闭动画用例加「保存 APK」「安装 APK」 |
| `internal/compat/compat_test.go` | `lucide.Download` 的编译期断言（`components.Progress`、`components.Select` 已有） |
| `docs/CHANGELOG.md` | `## 2026-11-24` → `### 新增` 三条 + `### 已知限制` 一条 |

---

### 任务 1：`ParsePullPercent`

**文件：**
- 创建：`internal/core/pull.go`
- 创建：`internal/core/pull_test.go`

- [x] **步骤 1：编写失败的测试**

```go
func TestParsePullPercentReadsTheLastProgressLine(t *testing.T) {
	output := "[  0%] /data/app/~~x==/com.foo-y==/base.apk\r[ 42%] /data/app/~~x==/com.foo-y==/base.apk\r"
	if percent, ok := ParsePullPercent(output); !ok || percent != 42 {
		t.Fatalf("ParsePullPercent() = %d, %v; want 42, true", percent, ok)
	}
}

func TestParsePullPercentIgnoresASummaryLine(t *testing.T) {
	output := "1 file pulled. 30.2 MB/s (12345678 bytes in 0.391s)\n"
	if _, ok := ParsePullPercent(output); ok {
		t.Fatal("a summary line was read as progress")
	}
}

func TestParsePullPercentRejectsImpossibleValues(t *testing.T) {
	for _, output := range []string{"[ 120%] /x.apk", "[ -1%] /x.apk", "[abc%] /x.apk", "[ 42% /x.apk"} {
		if _, ok := ParsePullPercent(output); ok {
			t.Fatalf("ParsePullPercent(%q) accepted garbage", output)
		}
	}
}
```

- [x] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run ParsePullPercent -v`
预期：FAIL，`undefined: ParsePullPercent`

- [x] **步骤 3：实现 `func ParsePullPercent(output string) (int, bool)`**

按 `\r` 和 `\n` 切行；每行 `TrimSpace` 后必须以 `[` 开头，取第一个 `]` 之前的内容；内容去掉空格后必须以 `%` 结尾、且 `strconv.Atoi` 能读成 `0..100`；返回**最后一条**合格记录。注释写清 §2.3 的两种来源与"为什么是最后一条"。

- [x] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run ParsePullPercent -v`
预期：PASS（3 个用例）

- [x] **步骤 5：Commit**

```sh
git add internal/core/pull.go internal/core/pull_test.go
git commit -m "feat: read adb pull progress from task output"
```

---

### 任务 2：设备用户（解析 + 读取 + 写进 session）

**文件：**
- 创建：`internal/core/users.go`
- 创建：`internal/core/users_test.go`
- 修改：`internal/core/session.go`（`Users` 字段）
- 修改：`internal/core/manager_tasks.go`（`RefreshDeviceInformation` 顺带读用户）
- 修改：`internal/core/messages.go`

- [x] **步骤 1：编写失败的测试**

```go
func TestParseDeviceUsersReadsIdNameAndCurrent(t *testing.T) {
	output := "Users:\n\tUserInfo{0:Owner:c13} running\n\tUserInfo{10:工作资料:404}\n"
	users := ParseDeviceUsers(output, 10)
	if len(users) != 2 {
		t.Fatalf("ParseDeviceUsers() = %v", users)
	}
	if users[0].ID != 0 || users[0].Name != "Owner" || users[0].Current {
		t.Fatalf("first user = %+v", users[0])
	}
	if users[1].ID != 10 || users[1].Name != "工作资料" || !users[1].Current {
		t.Fatalf("second user = %+v", users[1])
	}
}

func TestParseDeviceUsersSkipsGarbage(t *testing.T) {
	output := "Users:\n\tUserInfo{0:Owner:c13} running\n\trubbish\n" +
		"Error: java.lang.SecurityException: Shell does not have permission to access user 0\n"
	users := ParseDeviceUsers(output, 0)
	if len(users) != 1 || users[0].ID != 0 {
		t.Fatalf("ParseDeviceUsers() = %v", users)
	}
}

func TestParseCurrentUserReadsAnInteger(t *testing.T) // "10\n" → (10,true)；"unknown\n" → (false)

func TestRefreshDeviceInformationStoresDeviceUsers(t *testing.T)
// fakeRunner 答 get-state / getprop 脚本 / 用户脚本；断言 Session(id).Users 有 2 个、Current 正确。

func TestDeviceUsersRequiresAnOnlineDevice(t *testing.T) // 未 connect → err，且 commandLog 为空
func TestDeviceUsersUsesOneShellRoundTrip(t *testing.T)  // 只发一条含 "pm list users" 的 shell，另一条是 get-state
```

- [x] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'DeviceUser|ParseCurrentUser|ParseDeviceUsers' -v`
预期：FAIL，`undefined: ParseDeviceUsers`

- [x] **步骤 3：实现**

```go
// DeviceUser is one Android user profile on the device: the numbers adb install
// --user takes, and the label people recognise them by.
type DeviceUser struct {
	ID    int
	Name  string
	Current bool // the user the device says is running now
	Running bool // pm's own "running" marker
}

// ParseDeviceUsers reads `pm list users` output. Lines that are not a
// UserInfo record are skipped: some devices answer with a SecurityException.
func ParseDeviceUsers(output string, current int) []DeviceUser

// ParseCurrentUser reads what `am get-current-user` printed, if it is a number.
func ParseCurrentUser(output string) (int, bool)
```

`ParseDeviceUsers`：逐行找 `UserInfo{`/`UserInfo(`，取到同种右括号（`}`/`)`）为止；内部按 `:` 分成最多 3 段，第一段 `Atoi` 成功才收，第二段是名字（可能为空 → 用 `itoa(id)` 兜底）；`Current = id == current`；括号后还有 `running` 字样则 `Running = true`。

核心读取（只在这里拼 shell，且只有整数和解析出的路径进命令）：

```go
// deviceUsersScript asks for every profile and which one is running now, in one
// round trip: pm list users alone does not say which one is current.
const deviceUsersScript = "pm list users; echo @@CURRENT; am get-current-user"

// DeviceUsers reads the device's user profiles and returns them in the order
// pm listed them.
func (m *Manager) DeviceUsers(ctx context.Context, id uuid.UUID) ([]DeviceUser, error)
```

实现要点：`m.operationTarget` → `m.validateTarget` → `runner.Run(shell deviceUsersScript, 6s)`；退出码非 0 或输出里没有任何 `UserInfo` → `Error(MsgUsersUnreadable)`；解析后 `if !m.targetIsCurrent(id, target) { return nil, ErrCancelled }`。

`RefreshDeviceInformation` 在写完 5 行 getprop 字段之后（**不要**持有 `m.mu`）调一次 `m.readDeviceUsers(ctx, target)`，成功就 `m.mu.Lock(); setStateLocked(id, func(s *Session) { s.Users = users }); m.notify(); m.mu.Unlock()`；失败只记 `m.log(id, …)`，绝不让整次刷新报错。

`internal/core/messages.go` 新增：

```go
// MsgAPKPathMissing means the device has no APK for the package: a process that
// is not an installed application, or one installed only for another user.
MsgAPKPathMissing = "设备上没有这个应用的安装包"
// MsgAPKPathUnknown means the device would not answer where the APK is.
MsgAPKPathUnknown = "读不到应用的安装包路径"
// MsgAPKPackageInvalid is a package name that cannot be asked about safely.
MsgAPKPackageInvalid = "包名无法用于查询安装包"
// MsgUsersUnreadable means the device would not list its users, so the install
// dialog cannot offer a choice.
MsgUsersUnreadable = "读不到设备上的用户列表"
```

- [x] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'DeviceUser|ParseCurrentUser|ParseDeviceUsers|RefreshDeviceInformation' -v && go test ./internal/core`
预期：全部 PASS

- [x] **步骤 5：Commit**

```sh
git add internal/core/users.go internal/core/users_test.go internal/core/session.go internal/core/manager_tasks.go internal/core/messages.go
git commit -m "feat: read the device's user profiles and keep them on the session"
```

---

### 任务 3：`Manager.AppAPK`

**文件：**
- 创建：`internal/core/manager_apk.go`
- 创建：`internal/core/manager_apk_test.go`

- [x] **步骤 1：编写失败的测试**

```go
func TestAppAPKReadsTheBasePathFromThePackageListing(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		switch {
		case contains(c.Args, "get-state"):
			return Result{Output: "device\n"}, nil
		case containsSubstring(c.Args, "pm list packages"):
			return Result{Output: "package:/data/app/~~x==/com.foo-y==/base.apk\n"}, nil
		case containsSubstring(c.Args, "stat -c"):
			return Result{Output: "1234\n"}, nil
		}
		return Result{}, nil
	})
	apk, err := f.m.AppAPK(context.Background(), f.id, "com.foo")
	if err != nil {
		t.Fatalf("AppAPK() error: %v", err)
	}
	if apk.Path != "/data/app/~~x==/com.foo-y==/base.apk" || apk.Size != 1234 {
		t.Fatalf("AppAPK() = %+v", apk)
	}
}

func TestAppAPKFallsBackToPMPathWhenTheListingFails(t *testing.T)         // listing exit 1，pm path 正常
func TestAppAPKReportsAMissingPackage(t *testing.T)                      // 列表里没有该包 → err == MsgAPKPathMissing
func TestAppAPKRejectsAPackageNameThatCouldRunACommand(t *testing.T)     // "com.foo;reboot" → err，且 commandLog 无 pm/stat
func TestAppAPKRequiresAnOnlineDevice(t *testing.T)                      // 未 connect → err，且没发命令
```

- [x] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run AppAPK -v`
预期：FAIL，`f.m.AppAPK undefined`

- [x] **步骤 3：实现**

```go
// AppAPK is one application's installable base package as the device holds it.
type AppAPK struct {
	Path string
	// Size is the remote file's byte count; 0 when the device would not say,
	// which makes the progress unknown rather than zero.
	Size int64
}

// AppAPK asks the device where a package's base APK lives and how big it is.
func (m *Manager) AppAPK(ctx context.Context, id uuid.UUID, pkg string) (AppAPK, error)
```

顺序：`validPackageName(pkg)` 不过 → `Error(MsgAPKPackageInvalid)`；`m.operationTarget` → `m.validateTarget`；`shell pm list packages --user all -f <pkg>`（6 秒），按 `package:<path>=<pkg>` 精确匹配；命令失败/非 0 → 退回 `shell pm path <pkg>`；都没有 → `Error(MsgAPKPathMissing)`；再 `shell stat -c '%s' <path>`，失败或无数字 → `Size = 0`；最后 `targetIsCurrent` 校验。

`validPackageName(pkg string) bool`：非空、≤ 255 字节、只含 `[A-Za-z0-9_.]`——理由见审查重点 1。

- [x] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run AppAPK -v && go test ./internal/core`
预期：PASS

- [x] **步骤 5：Commit**

```sh
git add internal/core/manager_apk.go internal/core/manager_apk_test.go
git commit -m "feat: resolve a package's base APK path and size from the device"
```

---

### 任务 4：概览显示设备上的全部用户

**文件：**
- 修改：`internal/app/detail.go`（`overview` 加一行）
- 创建/修改：`internal/app/apk_test.go`（或新建 `internal/app/users_test.go`，本计划用后者）
- 创建：`internal/app/users_test.go`

- [x] **步骤 1：编写失败的测试**

```go
func TestDeviceUsersTextListsEveryProfile(t *testing.T) {
	users := []core.DeviceUser{{ID: 0, Name: "Owner", Current: true}, {ID: 10, Name: "工作资料"}}
	if got := deviceUsersText(users); got != "0 Owner（当前） · 10 工作资料" {
		t.Fatalf("deviceUsersText() = %q", got)
	}
}

func TestDeviceUsersTextIsAPlaceholderWhenNothingWasRead(t *testing.T) // nil → core.PlaceholderUnknown

func TestOverviewShowsEveryDeviceUser(t *testing.T) {
	// resourceFixture 风格的 fixture：连接后 session.Users 有两个用户
	vm := usersFixture(t)
	vm.DetailTab = detailOverview
	tt := ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("设备用户") || !tt.HasText("0 Owner（当前） · 10 工作资料") {
		t.Fatalf("the overview lost the device users; texts = %v", tt.Texts())
	}
}
```

- [x] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run DeviceUser -v`
预期：FAIL，`undefined: deviceUsersText`

- [x] **步骤 3：实现**

`internal/app/detail.go` 的 `overview`，在 `detailRow(c, "Shell 权限", session.ShellID)` 之后加：

```go
detailRow(c, "设备用户", deviceUsersText(session.Users))
```

`deviceUsersText(users []core.DeviceUser) string`：空 → `core.PlaceholderUnknown`；否则每项 `itoa(ID) + " " + Name`，`Current` 的追加 `（当前）`，用 ` · ` 连接。

- [x] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app -run 'DeviceUser|OverviewShowsEveryDeviceUser' -v`
预期：PASS

- [x] **步骤 5：Commit**

```sh
git add internal/app/detail.go internal/app/users_test.go
git commit -m "feat: show every device user on the overview page"
```

---

### 任务 5：保存 APK（每次问目录 + 进度弹窗）

**文件：**
- 创建：`internal/app/apk.go`
- 修改：`internal/app/state.go`（`APK *apkSave` + `apkMu`）
- 修改：`internal/app/detail.go`（`ContentView` 末尾调 `vm.APKView(c)`）
- 创建：`internal/app/apk_test.go`

- [x] **步骤 1：编写失败的测试**

```go
func TestAPKFileNameDropsCharactersWindowsForbids(t *testing.T) {
	got := apkFileName(`微信: "内测/版"`, "com.tencent.mm")
	if strings.ContainsAny(got, `<>:"/\|?*`) || !strings.HasSuffix(got, "com.tencent.mm.apk") {
		t.Fatalf("apkFileName() = %q", got)
	}
}

func TestAPKFileNameFallsBackToThePackage(t *testing.T) // label 空 → "com.foo.apk"

func TestUniqueAPKPathNeverOverwritesWhatIsAlreadyThere(t *testing.T) {
	taken := map[string]bool{"/d/com.foo.apk": true, "/d/com.foo (2).apk": true}
	if got := uniqueAPKPath("/d", "com.foo.apk", func(p string) bool { return taken[p] }); got != "/d/com.foo (3).apk" {
		t.Fatalf("uniqueAPKPath() = %q", got)
	}
}

func TestAPKDownloadAlwaysAsksForAFolder(t *testing.T) {
	// 桩返回 "" 就是用户在系统选择器里按了取消
	var asked int
	defer stubPickSaveFolder(func(string) (string, bool) { asked++; return "", false })()
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if err := tt.Click("保存应用安装包"); err != nil {
		t.Fatalf("the row has no 保存应用安装包 button: %v", err)
	}
	if asked != 1 || vm.APK != nil {
		t.Fatalf("asked = %d, dialog = %v; cancelling must ask once and open nothing", asked, vm.APK)
	}
}

func TestAPKSaveStartsOnePullOnly(t *testing.T)          // 点两次「开始」/重复调用 beginAPKSave → commandLog 里 pull 只有一条
func TestAPKSaveShowsTheProgressItReads(t *testing.T)    // 任务输出 "[ 42%] …" → 文本有 "42%"
func TestAPKProgressFallsBackToTheBytesOnDisk(t *testing.T)      // 无百分比，本地 512B / Total 1024 → 0.5
func TestAPKProgressIsIndeterminateWithoutAnyTotal(t *testing.T)  // Total 0 且无百分比 → -1
func TestAPKSaveReportsWhyThePullFailed(t *testing.T)    // pull 退出码 1 → 「保存失败：」
func TestAPKSaveCancelStopsTheTask(t *testing.T)         // 点「取消」→ TaskCancelled
func TestAPKSaveDoneShowsWhereTheFileWent(t *testing.T)  // 「已保存到 <LocalPath>」+「打开保存目录」
```

- [x] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run 'APK' -v`
预期：FAIL，`undefined: apkFileName`

- [x] **步骤 3：实现**

```go
// apkSave is the 「保存 APK」 dialog: which application, where it is going, and
// how far the pull got. A nil *apkSave closes the dialog.
type apkSave struct {
	Device    uuid.UUID
	Package   string
	Label     string
	Dir       string
	Error     string
	Stage     int
	APKPath   string
	Total     int64
	LocalPath string
	TaskID    uuid.UUID
}

// pickSaveFolder asks where to put the exported APK. It is a variable so tests
// never open a real file dialog; "no folder" means the user cancelled.
var pickSaveFolder = func(title string) (string, bool)

// stubPickSaveFolder replaces the picker for one test and returns the restore.
func stubPickSaveFolder(fn func(string) (string, bool)) (restore func())

// apkDownloadAction is the end of every row's name cell: the icon that exports
// the application's base APK. It is drawn whatever the device's state is — a
// hidden button would make the feature look absent instead of unavailable.
func (vm *ViewModel) apkDownloadAction(c *ui.Context, device uuid.UUID, app core.AppUsage) {
	if !components.IconButton(c, lucide.Download(), "保存应用安装包", components.IconButtonCompact).Clicked() {
		return
	}
	dir, ok := pickSaveFolder("选择保存 APK 的目录")
	if !ok || dir == "" {
		return // 取消选择：什么也不做
	}
	vm.beginAPKSave(device, app, dir)
}
```

`beginAPKSave(device, app, dir)`：加锁建 `apkSave{Stage: apkStageLocating, Dir: dir}`（若已有 `Stage != done/failed` 的弹窗则直接返回，防重复）；`os.MkdirAll(dir, 0o755)` 失败 → `Stage = apkStageFailed` + `Error = "无法创建保存目录：" + err`；否则起 goroutine：`vm.Manager.AppAPK` → `uniqueAPKPath(dir, apkFileName(app.Label, app.Package), fileExists)` → `vm.Manager.BeginADBTask(ctx, device, "保存 APK "+app.Package, []string{"pull", apk.Path, local}, 30*time.Minute)`，每步加锁写状态 + `vm.requestFrame()`。

`APKView(c)`：`components.Dialog(c, &open, components.DialogOptions{Label: "保存 APK"}, …)`，宽 480，按 §1.3 渲染；进度阶段每帧用

```go
task, found := apkTask(vm.Manager.Tasks(), state.TaskID)
```

判断 `TaskSucceeded` / `TaskFailed` / `TaskCancelled` 推进 Stage，`value := apkProgress(task, state.Total, state.LocalPath)` 画 `components.Progress(c, value)`。`取消` → `vm.Manager.CancelTask(state.TaskID)` 并关窗；`打开保存目录` → `vm.OpenDir(state.Dir)`；`关闭` → `vm.APK = nil`。

- [x] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app -run 'APK' -v && go test ./internal/app`
预期：PASS

- [x] **步骤 5：Commit**

```sh
git add internal/app/apk.go internal/app/apk_test.go internal/app/state.go internal/app/detail.go
git commit -m "feat: export an app's APK with an always-asked folder and live progress"
```

---

### 任务 6：安装 APK 选择用户

**文件：**
- 创建：`internal/app/install.go`
- 修改：`internal/app/actions.go`（`chooseAPK` → 选文件后打开弹窗；`pickAPKFile` 抽成变量）
- 修改：`internal/app/state.go`（`APKInstall *installDraft` + `installMu`）
- 修改：`internal/app/detail.go`（`ContentView` 末尾调 `vm.InstallView(c)`）
- 创建：`internal/app/install_test.go`

- [x] **步骤 1：编写失败的测试**

```go
func TestInstallArgsTargetTheChosenUser(t *testing.T) {
	args := installArgs("/tmp/app.apk", 10, true)
	want := []string{"install", "-r", "--user", "10", "/tmp/app.apk"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("installArgs() = %v", args)
	}
}

func TestInstallArgsWithoutAUserKeepTodaysCommand(t *testing.T) // installArgs(path, 0, false) == {"install","-r",path}

func TestInstallUserLabelMarksTheCurrentUser(t *testing.T) // {0,"Owner",Current:true} → "0 · Owner（当前）"

func TestInstallDialogOffersEveryUser(t *testing.T)   // 弹窗文本含 "安装到用户"、"0 · Owner（当前）"、"10 · 工作资料"、"覆盖安装：同包名的应用会被替换，应用数据保留。"
func TestInstallDialogSaysWhichUserItWillUse(t *testing.T) // 默认选中 Current 的那个
func TestInstallFallsBackToTheDefaultUserWhenTheListFails(t *testing.T) // 用户读取失败 → 文本 "读不到设备上的用户列表，将安装到默认用户"，点「安装」→ commandLog 里没有 --user
func TestInstallStartsOneTaskOnly(t *testing.T)
func TestInstallDialogCancelStartsNothing(t *testing.T)
```

- [x] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run Install -v`
预期：FAIL，`undefined: installArgs`

- [x] **步骤 3：实现**

```go
// installDraft is the 「安装 APK」 dialog: the file the user picked and which
// device user it goes to. A nil *installDraft closes the dialog.
type installDraft struct {
	Device uuid.UUID
	Path   string
	Users  []core.DeviceUser
	// UserID is the chosen install user; 0 is a legitimate id, so Chosen says
	// whether anything is selected at all.
	UserID int
	Chosen bool
	// Loading is true while the user list is being read.
	Loading bool
	Err     string
}

// installArgs is the adb command for a chosen user: the same -r as before, plus
// --user when the device named a user. keepUser is false when the list could
// not be read, and then the command is exactly today's.
func installArgs(path string, userID int, keepUser bool) []string

// installUserLabel is what the picker shows: the number adb takes, then the
// profile's name, then a marker on the one that is running now.
func installUserLabel(user core.DeviceUser) string
```

`chooseAPK(profile)` 改成：`pickAPKFile(...)` 拿到路径后 `vm.openInstallDialog(profile, path)`。`openInstallDialog` 建 `installDraft{Device, Path, Loading: true}`，选当前用户为默认（`Chosen = true`），然后 goroutine `vm.Manager.DeviceUsers(ctx, profile.ID)`：成功 → `Users`、`Chosen`、`Loading = false`；失败 → `Err = core.MsgUsersUnreadable`、`Chosen = false`、`Loading = false`、`requestFrame()`。

`InstallView(c)`：`components.Dialog`，宽 480；按 §1.4 渲染；`components.Select(c, &state.UserID)` 的 `Popup` 里每个用户一个 `sel.Item(user.ID)`；`安装` → `vm.Manager.BeginADBTask(ctx, device, "安装 "+fileName(path), installArgs(path, state.UserID, state.Chosen), 10*time.Minute)` → 成功关窗、失败写 `Err` 留在弹窗里。

- [x] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app -run Install -v && go test ./internal/app`
预期：PASS

- [x] **步骤 5：Commit**

```sh
git add internal/app/install.go internal/app/install_test.go internal/app/actions.go internal/app/state.go internal/app/detail.go
git commit -m "feat: choose which device user an APK is installed for"
```

---

### 任务 7：接线、截图、契约、CHANGELOG

**文件：**
- 修改：`internal/app/resources.go`（`appCell` 调 `vm.apkDownloadAction`）
- 修改：`internal/app/screenshots_test.go`、`internal/app/dialog_test.go`
- 修改：`internal/compat/compat_test.go`
- 修改：`docs/CHANGELOG.md`
- 测试：`internal/app/apk_test.go`

- [x] **步骤 1：编写失败的测试**

```go
func TestAppRowOffersAPKDownloadAfterTheMetaButton(t *testing.T) {
	vm, _ := resourceFixture(t)
	tt := ui.NewTester(vm.ContentView, 1000, 800)
	if err := tt.Click("保存应用安装包"); err != nil {
		t.Fatalf("the row has no 保存应用安装包 button: %v", err)
	}
}

func TestAPKDownloadActionUsesTheRowsOwnPackage(t *testing.T) // 点第二行的图标，弹窗里的包名是那一行的
```

- [x] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run 'AppRowOffersAPK|APKDownloadAction' -v`
预期：FAIL，`Click("保存应用安装包")` 找不到目标

- [x] **步骤 3：实现接线与文档**

`resources.go` 的 `appCell` case 0，`vm.appMetaAction(c, device, app)` 之后加：

```go
vm.apkDownloadAction(c, device, app)
```

`shotCases()` 加三条：`apk save progress`（`Stage = apkStageDownloading`、`Total = 1048576`）、`install dialog`（两个用户、选中当前用户）、`overview users`（session 里有两个用户）。`dialog_test.go` 用例表加 `{"apk save", …}` 与 `{"install apk", …}`。

`compat_test.go` 的 lucide 断言区加 `_ func() *ui.SVG = lucide.Download`。

`docs/CHANGELOG.md` 顶部新建或追加 `## 2026-11-24`：

```markdown
### 新增

- 资源页每个应用行新增「保存应用安装包」：点一下先选保存目录（每次都会问，不设默认），选好后弹窗里显示下载进度、百分比与结果；文件名是「应用名_包名.apk」，重名自动加序号、不覆盖已有文件。只导出基础安装包，不含拆分包。
- 安装 APK 时可以选装到哪个 Android 用户（工作资料等）：弹窗列出设备上全部用户、默认选中当前用户，并写明这是覆盖安装、应用数据保留。
- 概览页新增「设备用户」一行，列出设备上全部用户并标记当前用户；连接时与点「刷新信息」时重新读取。

### 已知限制

- 安装到指定用户需要 platform-tools 25 以上的 adb；更老的 adb 会报 `unknown option`，任务以失败结束、错误原样显示。
```

- [x] **步骤 4：运行测试验证通过**

运行：`gofmt -l . && go vet ./... && go test ./...`
预期：`gofmt -l .` 无输出；`vet` 无报告；全部 PASS
另外人工核对一次：`TAILCAT_SHOT_DIR=/tmp/shots go test ./internal/app -run TestScreenshots -v`，看三张新 PNG（进度条、用户下拉、概览一行是否挤在一起）。

- [x] **步骤 5：Commit**

```sh
git add -A
git commit -m "feat: export APKs and install for a chosen device user from the app pages"
```

---

## 6. 第二轮：验收后的改动

第一轮合并（`5082314` 之后）用户验收时提了 5 处改动。它们不是新计划，而是对本文件 §1 的修正；改动、原因与钉住它的测试如下，`§1` 已按最终行为改写。

| # | 现象 | 改动 | 钉住它的测试 |
|---|---|---|---|
| 1 | 「打开保存目录」点了没反应 | `platform.RevealDir` 之前用 `Command()`，它带 `CREATE_NO_WINDOW`+`HideWindow`——那是给 adb 这类控制台子进程准备的。`explorer` 以隐藏方式启动时，已有资源管理器窗口的情况下只转发请求、不弹窗口。新增 `platform.VisibleCommand`（只保留进程组，不带隐藏标志），三平台 `RevealDir` 改用它；打开成功后提示「已打开保存目录」，即使文件管理器开在窗口后面也有反馈 | `TestVisibleCommandShowsItsWindow`（先把实现改回旧标志确认它红：`CreationFlags = 0x8000200, want no CREATE_NO_WINDOW`）、`TestAPKSaveRevealSaysItOpenedTheFolder` |
| 2 | 「设备用户」一句话读不清（用户多了没法比） | 概览里的一行文字换成表格：`用户` / `名称` / `状态`，走统一的 `tableCard`；空列表显示 `—` 而不是只有表头 | `TestDeviceUserStatusWords`、`TestOverviewShowsEveryDeviceUser`、`TestOverviewSaysNothingWhenTheUserListIsEmpty` |
| 3 | 状态列左对齐时，表头、文字、徽标宽度不同、读起来不在一条线上 | 该列 `Align: ui.Center`（表格同时给表头与单元格加 `Justify(Center)`） | `TestDeviceUserStatusColumnIsCentred`（改之前红：`"未启动" centres at 814.5, header at 812.0`，容差 2px） |
| 4 | Shell 权限的长 groups 顶到窗口右边被裁掉 | 值放进带内边距的圆角框（等宽字体，长串在框内换行），空值显示 `—`；同时给 `detailRow` 的值加 `Grow(1).MinWidth(0)`——这才是所有详情行长值被裁的根因（没有横向滚动能看被裁掉的部分） | 截图 `03c-device-overview-users.png`（长 `id` 在框内两行结束）、`03-device-overview.png`（空值时框里是 `—`） |
| 5 | 安装 APK 先弹系统文件选择再弹窗，用户看不到自己选了哪个文件 | 点「安装 APK」直接开弹窗，文件在弹窗里用「选择 APK 文件」选，选中后显示完整路径；没选文件时「安装」禁用，`submitInstall` 里还有一道空路径校验（键盘路径也起不来任务）。「选择 APK 文件」放在独立 Row 里：直接放进弹窗列会被拉成整条横幅，读成主操作 | `TestChooseAPKOpensTheDialogBeforeAskingForAFile`（选文件之前 `pickAPKFile` 一次都不许调用）、`TestInstallDialogPicksTheAPKAndShowsItsPath`、`TestInstallWaitsForAFileBeforeItStarts`；截图 `34-install-apk-dialog.png`、`34b-install-apk-dialog-empty.png` |

第二轮改动的文件：`internal/platform/exec.go`（+`VisibleCommand`）、`kill_unix.go` / `kill_windows.go`（+`configureVisibleProcess`）、`reveal_*.go`、`internal/app/install.go`、`internal/app/actions.go`、`internal/app/detail.go`、`internal/app/state.go`（+`UserTable`），以及各自的测试与截图用例。

写进 `docs/CHANGELOG.md` 的条目（`## 2026-11-24`）：保存 APK、安装选用户 + 弹窗内选文件、概览设备用户表（状态列居中）+ Shell 权限加框、打开保存目录修复、`--user` 需要较新 platform-tools 的已知限制。设计取舍另有一行在 `docs/architecture.md` 的「设计决策记录」（打开目录用 `VisibleCommand`）。

## 自检

- **规格覆盖**：§1.1 入口 → 任务 7；§1.2 每次问目录 → 任务 5（`pickSaveFolder`、`beginAPKSave`）；§1.3 四个阶段 → 任务 5；§1.4 安装选用户（第二轮改为弹窗内选文件）→ 任务 6 + §6；§1.5 概览用户表格与 §1.6 Shell 权限框 → 任务 4 + §6；§2.2 一条 shell → 任务 2（`TestDeviceUsersUsesOneShellRoundTrip`）；§2.3 两个进度来源 → 任务 1/5；§3 安装方式不变 → 任务 6（`TestInstallArgsWithoutAUserKeepTodaysCommand` + `-r` 保持）；§4 的决定 → 任务 3（命名、重名）、任务 5（不设默认、取消即取消）、任务 6（默认当前用户、列表失败仍可装）、§6（弹窗内选文件、可见窗口打开目录、设备用户用表格）。
- **类型一致性**：`core.AppAPK{Path,Size}`、`core.DeviceUser{ID,Name,Current,Running}`、`Session.Users []DeviceUser`、`ParseDeviceUsers(string,int) []DeviceUser`、`ParseCurrentUser(string) (int,bool)`、`ParsePullPercent(string) (int,bool)`、`apkSave.{Device,Package,Label,Dir,Error,Stage,APKPath,Total,LocalPath,TaskID}`、`apkStageLocating/Downloading/Done/Failed`、`apkFileName(label,pkg) string`、`uniqueAPKPath(dir,name string, exists func(string) bool) string`、`apkProgress(task core.Task, total int64, localPath string) float64`、`installDraft.{Device,Path,Users,UserID,Chosen,Loading,Err}`、`installArgs(path string, userID int, keepUser bool) []string`、`installUserLabel(core.DeviceUser) string`。
- **未决（本计划不做）**：安装失败的「重试」按钮、`-d`（允许降级）/`-t`（testOnly）/`-g`（授予全部运行时权限）、split APK 打包导出。要做就作为新需求重走设计。
