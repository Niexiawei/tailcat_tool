# 断开后的残留状态 + 文件页连接归属 + 弹窗内进度与设备路径选择 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** ①断开设备后概览里不再留着上一次连接读到的「设备用户」；②文件页在重连/连接切换期间读目录失败时，不再把「该设备的 ADB 尚未进入 device 状态」当成永久失败；③文件页的目录与展开状态只属于**当前这次连接**，新连接从设备根目录 `/` 开始；④安装 APK、上传、下载的本地路径都在**各自的弹窗里选**（和「安装 APK」一致），设备侧路径用**文件页同一套浏览器**选，进度条**直接显示在原来的弹窗里**，不再另开一个进度弹窗。

**架构：** 三条独立的小改动 + 两块界面改造。

1. `core.clearDeviceInfo` 补上 `Users`（它已经是「只有活着的连接才知道的东西」的清理点）。
2. 新增哨兵错误 `core.ErrDeviceNotReady`（文案不变：`该设备的 ADB 尚未进入 device 状态`），由 `operationTarget` / `validateTargetLocked` 返回；文件页把它和 `ErrCancelled` 一样当作**临时状态**，不写进 `fileErrs`，连接恢复后自然重读。
3. `Session.ConnID`（本次连接的 generation）成为文件页状态的归属键：`fileState.conn != session.ConnID` 时整页重置到 `/`；`Connect` 同时丢掉上一次连接的目录缓存。
4. 把 `files.go` 右栏抽成 `fileBrowser(c, profile, state, opts)`，文件页与「选择设备路径」共用同一份树 + 面包屑 + 表格 + 行激活逻辑；每个设备可以有多个独立浏览实例（文件页一个、选择器一个）。
5. 新文件 `internal/app/taskprogress.go`：`taskBatch` + 纯函数 `batchSnapshot` / `elapsedLabel` + 共用的 `progressBlock`（进度条 + 文本 + 已用时间）。**没有独立弹窗**：传输弹窗与安装弹窗在自己的运行阶段调用它。

**技术栈：** Go（纯，无 cgo）、mygo `ui`（`ui.NewTester` 做视图测试）、onevra `components.Dialog` / `components.Progress` / `components.Split` / `components.TreeVirtualized` / `components.Breadcrumbs`、现有任务机制（`Manager.BeginADBTask` / `Tasks()` / `CancelTask` / `WaitTask`）、adb 的 `install` / `push` / `pull` 输出行。

**规格：** 本文档 §1（用户可见行为，逐字文案）、§2（根因）、§3（决定）。执行前把 §3 读一遍——D1/D2/D3 都可以被推翻，但必须先推翻再写代码。

## 全局约束

- 沿用 `AGENTS.md`：纯 Go 无 cgo；`internal/core` 不 import app；**不许 `exec.Command`**，一律 `runner.Run`；不持有 `m.mu` 时调用会上锁的方法；异步分支写状态前先 `isCurrentLocked`。
- 文案中文，逐字断言。核心原因句放 `internal/core/messages.go` 或 `err.go`，界面文案放视图里。
- TDD 顺序不可跳：写测试 → 跑一次看到 RED（原因必须是"功能缺失"）→ 最少实现 → 跑绿 → `gofmt -w` / `go vet ./...` / `go test ./...` → commit。
- GUI 只用现成组件：`components.Dialog`、`components.Progress`、`components.Spinner`、`components.Split`、`components.Button`（**不许 `ButtonGhost`**）；**不许**用 `ui.Row` 现拼进度条、树、面包屑或弹窗。
- **不要在一个弹窗里再开一个弹窗**：路径选择与运行进度都是同一个 `components.Dialog` 的不同阶段。
- 表格只经 `tableCard`；行式元素用 `FillWidth()`，不要用 `Fill()`。
- 每个改变用户行为的提交，同一次提交里带 `docs/CHANGELOG.md`（今天已有 `## 2026-11-26` 就追加，没有就在最上面新建）。
- 分支 `fix/device-state-and-transfer-dialog`；提交前缀 `fix:` / `feat:` / `refactor:` / `test:` / `docs:`。
- 测试基础设施用现成的：core 用 `newManagerFixture` / `fakeRunner.setHandler` / `f.connect(t)` / `listFixture`；app 用 `newRunnerViewModel` / `recordingRunner` / `waitForText` / `waitUntil` / `apkFixture`。

## 审查重点（Review Focus）

1. **连接切换期间的目录读取结果落到下一次连接上**——旧 listing 或旧失败写进新连接，用户看到上一台设备/上一次连接的文件。期望：新连接只显示新连接读到的内容（`TestFilePageReturnsToTheRootForANewConnection`、`TestReconnectForgetsCachedDirectoryListings`）。
2. **临时错误被当成永久失败缓存**——`fileErrs` 一旦写进去，页面就停在「读取失败」直到点重试。连接中的 `busy` 必须不写缓存（`TestFileReadDuringAReconnectIsNotAFailure`）。
3. **选择器与文件页互相踩状态**——在「选择设备路径」里翻目录，把文件页的当前目录/展开状态改掉了；或反过来。期望：两个实例各有独立的 `fileState`（`TestPickerKeepsItsOwnFolder`、`TestFilePageReturnsToTheRootForANewConnection`）。
4. **选择器能选到不能用的东西**——上传目标不是目录、下载源空着、路径没填就点「开始」、空选择就提交。期望：只允许选文件夹作为上传目标，两个路径齐了主按钮才可点，路径仍走 `ValidateRemotePath` / `remoteListScript` 的校验（`TestUploadDialogBrowsesTheDeviceForItsTargetFolder`、`TestTransferStartIsDisabledWithoutBothPaths`）。
5. **进度条显示假进度或关窗丢任务**——没有百分比时不许显示 0%；关窗（Esc/✕）任务继续、`取消` 才杀（`TestBatchProgressIsIndeterminateWithoutAPercentage`、`TestTransferCloseWhileRunningKeepsTheTask`、`TestTransferCancelStopsTheTask`）。
6. **失败原因说不清**——安装失败只显示"失败"而 adb 的 `Failure [INSTALL_FAILED_...]` 被丢掉。期望：失败时把任务输出（脱敏后）显示出来（`TestInstallDialogShowsWhyTheInstallFailed`）。

---

## 1. 用户可见行为（逐字文案）

### 1.1 断开之后，概览不再显示设备用户

断开（`断开` 按钮 / 退出时的 `DisconnectAll`）、连接失败、以及重新连接开始的那一刻，概览的「设备用户」都回到 `—`（与「设备型号」等行一致），不再留着上一次连接读到的用户表。

### 1.2 文件页在连接切换中不再报「该设备的 ADB 尚未进入 device 状态」

- 这个错误是**过渡态**（正在连接 / 正在断开），不写进页面的失败缓存：树里不出现「读取失败」徽标，右栏不停在失败提示上。
- 设备恢复到 `ADB 已连接` 后，页面自己重新读一次（不需要用户点「重试」）。
- 真正的失败仍然保留「读取失败 + 重试」：没有权限、目录不存在、超时。

### 1.3 文件页的状态只属于当前这次连接

- 新建的连接（第一次连接、重新连接、断开后再连接）总是从设备根目录 `/` 开始：右栏列 `/`，树只展开根节点，没有任何文件夹被自动打开。
- 同一次连接内照旧记住：右栏当前目录、面包屑、树的展开与游标、表格列宽与滚动位置。

### 1.4 上传 / 下载：本地路径都在弹窗里选

入口不变（概览的「上传」「下载」，文件页的「上传到此文件夹…」，文件属性的「下载到本地…」）。

**上传弹窗（表单阶段）**

| 元素 | 内容 |
|---|---|
| 标题 | `上传文件` |
| 行 | `本地文件` ｜ 一行一个已选路径（长路径换行）；没选时灰色 `未选择本地文件` |
| 按钮 | `选择文件…`（`ButtonOutline`，可多选）、`选择文件夹…`（`ButtonOutline`，选一个目录上传） |
| 行 | `上传到` ｜ 远端目录文本框（可手打）+ `浏览设备…`（`ButtonOutline`） |
| 错误 | 校验失败时在这一行说明（例如 `请填写绝对远端路径，例如 /sdcard/Download/example.txt`） |
| 按钮 | `取消`（`ButtonOutline`）、`开始`（`ButtonPrimary`，本地与远端都齐了才可点） |

**下载弹窗（表单阶段）**

| 元素 | 内容 |
|---|---|
| 标题 | `下载文件` |
| 行 | `设备上的文件或目录` ｜ 远端路径文本框 + `浏览设备…` |
| 行 | `保存到` ｜ 本地目录（或文件名）；没选时灰色 `请选择本地保存位置` + `选择位置…` |
| 按钮 | `取消` / `开始`（同上，两个路径齐了才可点） |

### 1.5 「浏览设备…」：复用文件页那一套浏览器

点「浏览设备…」把**同一个弹窗**切到浏览阶段（宽度从 460 变 760，高度固定约 420）：

| 元素 | 内容 |
|---|---|
| 顶部 | 面包屑（每一级可点回到该级）+ `刷新` |
| 左侧 | 与文件页相同的文件夹树（`components.TreeVirtualized`，路径为键，按需展开、按需读取） |
| 右侧 | 与文件页相同的文件表格（`tableCard`，名称 / 大小 / 修改时间 / 权限） |
| 底部 | `返回`（`ButtonOutline`，回到表单，已选的路径保留）、`选择此文件夹`（`ButtonPrimary`） |

行为：

- **上传的目标是目录**：点文件夹 = 进入；点文件没有反应；只有 `选择此文件夹` 能确定，选中的是当前目录。
- **下载的源可以是文件或目录**：点文件夹 = 进入；**点文件 = 直接选定并回到表单**（路径填进远端路径框）；`选择此文件夹` 选定当前目录。
- 读取失败照旧在原地说明原因并给 `重试`；过渡态（正在连接/正在断开）不显示失败，等设备回来后自动重读。
- 选择器自己有独立的浏览状态：在它里面翻目录**不会**改变文件页正在显示的目录，文件页翻目录也不会改变选择器。
- 打开选择器时从文件页当前目录开始（同一设备上"接着看"），文件页还没打开过则从 `/` 开始。

### 1.6 运行与结果：进度条就在原来的弹窗里

点「开始」（或从文件页/文件属性直接发起的上传下载）之后，**同一个弹窗**切到运行阶段：

| 元素 | 内容 |
|---|---|
| 标题 | 上传：`正在上传`；下载：`正在下载`；安装：`正在安装` |
| 说明行 | 操作名，如 `上传 update.zip`、`下载 照片.jpg`、`安装 app-release.apk`；多文件时 `上传 3 个文件`；安装另有 `安装到用户 0 · Owner（当前）` |
| 进度条 | `components.Progress`：有百分比时 `p/100`，否则 `-1`（不确定） |
| 进度文本 | 单文件：`42%`；下载且已知远端大小时退回 `已传输 1.2 MB / 4.0 MB`；多文件：`已完成 1 / 3 个文件`；安装：adb 最新一行输出（如 `Performing Streamed Install`） |
| 已用时间 | `已用 12 秒` / `已用 1 分 05 秒` |
| 按钮 | `取消`（`ButtonOutline`，中止任务并关窗）、`关闭`（`ButtonOutline`，只关窗） |

结束时同一弹窗换成结果：

- 成功：`已完成`（`ButtonPrimary` 的 `关闭`）。
- 失败：`失败：<adb 输出的尾部，脱敏后>`（安装用 `安装失败：…`）。
- 取消：`已取消`。
- Esc / ✕ / 遮罩都等于「关窗」：**运行中关窗不杀任务**，任务继续跑，可在「日志」页的任务列表里观察；只有 `取消` 会 `CancelTask`。

### 1.7 一个操作一个弹窗

| 入口 | 弹窗 |
|---|---|
| 概览 →「安装 APK」 | `安装 APK`（选文件 → 选用户 → 运行 → 结果） |
| 概览 →「上传」 | `上传文件`（表单 → 可浏览设备 → 运行 → 结果） |
| 概览 →「下载」 | `下载文件`（表单 → 可浏览设备 → 运行 → 结果） |
| 文件页 →「上传到此文件夹…」 | 先选本地文件（系统选择器），随后 `上传文件` 弹窗直接进运行阶段，远端目录已填成当前目录 |
| 文件属性 →「下载到本地…」 | 先选本地保存位置，随后 `下载文件` 弹窗直接进运行阶段 |

表格里再次点另一个入口不可能：弹窗是模态的。

---

## 2. 根因（写在计划里，省得实现者再猜）

| 现象 | 根因 | 位置 |
|---|---|---|
| 断开后设备用户还在 | `clearDeviceInfo` 清了 model/product/Android/serial/shell，**没清 `Session.Users`**；`Disconnect`、`failConnect`、`Connect` 都只调它 | `internal/core/manager.go` |
| 文件页偶发「ADB 尚未进入 device 状态」 | `loadDir` / `prefetchDir` 把**所有**错误都写进 `fileErrs`；重连期间（`StateConnecting`）的读取命中 `operationTarget` 的 `Busy()` 分支，过渡态被当成永久失败缓存，连接恢复也不重读 | `internal/core/manager_tasks.go`（`operationTarget`）、`internal/app/files.go` |
| 断开/重连后还打开着之前的目录；新设备连上就打开 `/sdcard` | ①`fileState` 从不失效；②新建 `fileState` 的默认路径写死 `/sdcard`；③`Connect` 不清 `dirListings` | `internal/app/files.go`、`internal/core/manager.go`（`Connect`） |
| 上传/下载的本地选择在弹窗之前、远端路径只能手打 | `chooseUpload` 先弹系统选择器再开弹窗；`TransferView` 只有一个远端文本框 | `internal/app/actions.go`、`transfer.go` |
| 安装/上传/下载没有进度、弹窗立刻消失 | `submitInstall` / `submitTransfer` 起完任务就关窗；`pushInto` / `startPull` 连弹窗都没有 | `internal/app/install.go`、`transfer.go`、`files.go` |

## 3. 决定（可推翻，但先推翻再写代码）

- **D1：新连接从 `/` 开始，而不是 `/sdcard`。** `/sdcard` 看起来就是"记忆下来的目录"——用户已经把它当成 bug 报了一次（"另一台设备打开的 sdcard 也在这里打开了"）。代价：进文件页多一层；`/` 的预读跳过 `/data`（shell 读不了）。想改回"连上直接看内部存储"只需改 `filePageRoot` 一处常量。
- **D2：关窗不杀任务，`取消` 才杀。** 与 `installDraft` 现有注释一致（"a started task keeps running in the task list"），也符合大文件被误关的现实。代价：关窗后要回「日志」页找任务，所以 `取消` 与 `关闭` 必须是两个按钮。
- **D3：远端路径选择器复用文件页的树 + 表格，但用独立的 `fileState`。** 共用渲染与读取逻辑，不共用浏览位置——否则选个目录就把用户正在看的文件页顶掉了。代价：每个设备多一份 `fileState`（内存几十 KB 的树/表状态）。
- **D4：不新增独立的「任务进度」弹窗。** 进度属于发起它的那个操作，放回安装/传输弹窗里；共用的是 `taskprogress.go` 里的 `batchSnapshot` 与 `progressBlock`，不是弹窗本身。
- **D5：`ParsePullPercent` 改名为 `ParseTransferPercent`。** 同一格式也被 `adb push` 打印；改名一次改完 `internal/app/apk.go` 与 `internal/core/pull_test.go`。
- **D6：文件页的「上传到此文件夹…」与文件属性的「下载到本地…」保留一键流程**：先弹系统选择器拿本地路径，再直接进传输弹窗的运行阶段（远端/本地已填好）。不是所有操作都要走完整表单。
- **D7：进度弹窗不持久化**，关掉不重开；任务本身进历史（`Manager.recordTask`），回看走日志页。

## 4. 文件结构

**新建**

| 文件 | 职责 |
|---|---|
| `internal/app/taskprogress.go` | `taskKind`、`taskBatch`、`batchView`、`batchSnapshot`、`elapsedLabel`、`progressBlock`（两个弹窗共享的进度块） |
| `internal/app/taskprogress_test.go` | 上面这些的纯函数测试 + 两个 `streamingRunner` 集成测试 |

**修改**

| 文件 | 改动 |
|---|---|
| `internal/core/session.go` | `Session.ConnID uuid.UUID`：本次连接的 generation，断开时清零 |
| `internal/core/manager.go` | `clearDeviceInfo` 清 `Users`；`Connect` 写 `ConnID` + `forgetDirListingsLocked`；`Disconnect` 清 `ConnID` |
| `internal/core/err.go` | 新增 `ErrDeviceNotReady` |
| `internal/core/manager_tasks.go` | `operationTarget`（两处）与 `validateTargetLocked` 返回 `ErrDeviceNotReady` |
| `internal/core/pull.go` | `ParsePullPercent` → `ParseTransferPercent`（D5） |
| `internal/app/apk.go` | 用新函数名 |
| `internal/app/files.go` | `filePane` 键、`filePageRoot`、`fileState.conn`、`syncFileState`/`resetFileState`、`transientFileError`、抽出 `fileBrowser` + `fileBrowserOptions`、`pickerStateFor`/`openPicker`/`chooseFromPicker`、`prefetchSkip` 加 `/data`、`pushInto`/`startPull` 接传输弹窗 |
| `internal/app/transfer.go` | `transferDraft`（阶段、本地/远端路径、选择器、任务、错误）+ 三阶段渲染 + `submitTransfer` 就地开跑 |
| `internal/app/install.go` | `installDraft` 加阶段/TaskIDs/Started，运行与结果都在本弹窗里 |
| `internal/app/actions.go` | `chooseUpload` / `startDownload` 改成只打开弹窗（不预先弹系统选择器） |
| `internal/app/state.go` | `Transfer` 弹窗状态、`transferMu`、`files` 键改成 `filePaneKey`（注释同步） |
| `internal/app/screenshots_test.go` | 新用例（见任务 8） |
| `internal/app/dialog_test.go` | 传输弹窗、安装弹窗、选择器阶段的关窗不 panic 用例 |
| `internal/core/users_test.go`、`manager_test.go`、`manager_files_test.go`、`manager_tasks_test.go`、`pull_test.go`；`internal/app/users_test.go`、`files_test.go`、`transfer_test.go`（新建）、`install_test.go`、`forms_test.go` | 测试 |
| `docs/CHANGELOG.md`、`docs/architecture.md` | 变更记录 + 决策记录 |

---

### 任务 1：断开之后忘掉设备用户

**文件：**
- 修改：`internal/core/manager.go`（`clearDeviceInfo`）
- 测试：`internal/core/users_test.go`、`internal/app/users_test.go`
- 修改：`docs/CHANGELOG.md`

- [ ] **步骤 1：写失败的测试（core）**

```go
// 断开之后，只有活着的连接才知道的东西（含用户列表）必须一起消失。
func TestDisconnectForgetsTheDeviceUsers(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		switch {
		case contains(c.Args, "get-state"):
			return Result{Output: "device\n"}, nil
		case containsSubstring(c.Args, "pm list users"):
			return Result{Output: "Users:\n\tUserInfo{0:Owner:c13} running\n@@CURRENT\n0\n"}, nil
		case containsSubstring(c.Args, "getprop ro.product.model"):
			return Result{Output: "E245\nantora\n11\nSN123\nuid=0(root)\n"}, nil
		}
		return Result{}, nil
	})
	if err := f.m.RefreshDeviceInformation(context.Background(), f.id); err != nil {
		t.Fatalf("RefreshDeviceInformation() error: %v", err)
	}
	if len(f.m.Session(f.id).Users) == 0 {
		t.Fatal("the fixture never read a user list")
	}

	f.m.Disconnect(context.Background(), f.id)

	if users := f.m.Session(f.id).Users; len(users) != 0 {
		t.Fatalf("a disconnected device still lists %+v", users)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run TestDisconnectForgetsTheDeviceUsers -v`
预期：FAIL，`a disconnected device still lists [{0 Owner ...}]`

- [ ] **步骤 3：实现（core）**

`clearDeviceInfo` 里 `s.ShellID = ""` 之后加：

```go
	// Users 也是连接期的事实：断开后留着一张用户表，读起来像设备还在。
	s.Users = nil
```

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run TestDisconnectForgetsTheDeviceUsers -v`
预期：PASS

- [ ] **步骤 5：写视图层的测试**

`internal/app/users_test.go`：

```go
// 概览读的是 Session.Users，断开后那一节必须回到 —。
func TestOverviewStopsShowingUsersAfterDisconnect(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	vm.Manager.ForceSessionUsersForTest(profile.ID, []core.DeviceUser{{ID: 0, Name: "Owner", Current: true}})
	vm.OpenDevice(profile.ID)

	tt := ui.NewTester(vm.ContentView, 1000, 760)
	tt.Frame()
	if !tt.HasText("Owner") {
		t.Fatalf("the fixture never showed a user; texts = %v", tt.Texts())
	}

	vm.Manager.Disconnect(context.Background(), profile.ID)
	tt.Frame()
	if tt.HasText("Owner") {
		t.Fatalf("the user list survived the disconnect; texts = %v", tt.Texts())
	}
}
```

- [ ] **步骤 6：加测试辅助（core）**

`internal/core/attention.go` 里 `ForceSessionStateForTest` 旁边：

```go
// ForceSessionUsersForTest sets one device's user list without asking a device.
func (m *Manager) ForceSessionUsersForTest(id uuid.UUID, users []DeviceUser) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setStateLocked(id, func(s *Session) { s.Users = users })
	m.notify()
}
```

- [ ] **步骤 7：运行测试验证通过**

运行：`go test ./internal/core -run TestDisconnectForgetsTheDeviceUsers -v && go test ./internal/app -run TestOverviewStopsShowingUsersAfterDisconnect -v`
预期：都 PASS

- [ ] **步骤 8：写变更记录并提交**

```markdown
### 修复

- 断开设备后概览里的「设备用户」不再留在屏幕上：断开、连接失败与重新连接都会清空上一次连接读到的用户列表。
```

```bash
gofmt -w internal/core internal/app
go vet ./...
git add internal/core/manager.go internal/core/attention.go internal/core/users_test.go internal/app/users_test.go docs/CHANGELOG.md
git commit -m "fix: forget the device user list when the channel ends"
```

---

### 任务 2：连接切换中的目录读取不再变成永久失败

**文件：**
- 修改：`internal/core/err.go`、`internal/core/manager_tasks.go`、`internal/core/manager.go`、`internal/app/files.go`
- 测试：`internal/core/manager_tasks_test.go`、`internal/app/files_test.go`

- [ ] **步骤 1：写失败的测试（core）**

```go
// 「还没进 device 状态」是过渡态，调用方要能认出来（errors.Is），文案不变。
func TestOperationTargetReportsNotReadyAsTransient(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.m.ForceSessionStateForTest(f.id, StateConnecting, "")

	_, err := f.m.BeginADBTask(context.Background(), f.id, "上传 a.txt",
		[]string{"push", "a.txt", "/sdcard/"}, time.Minute)

	if !errors.Is(err, ErrDeviceNotReady) {
		t.Fatalf("BeginADBTask() error = %v, want ErrDeviceNotReady", err)
	}
	if err.Error() != "该设备的 ADB 尚未进入 device 状态" {
		t.Fatalf("the wording changed: %q", err.Error())
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run TestOperationTargetReportsNotReadyAsTransient -v`
预期：FAIL，`undefined: ErrDeviceNotReady`

- [ ] **步骤 3：实现（core）**

`internal/core/err.go`：

```go
// ErrDeviceNotReady means the channel exists but ADB has not reached the
// `device` state yet: connecting, disconnecting, or still offline. It is
// transient, and callers must not cache it as a failure.
var ErrDeviceNotReady = Error("该设备的 ADB 尚未进入 device 状态")
```

`operationTarget` 的 `Busy()` 分支与 `!= StateOnline` 分支都 `return operationTarget{}, ErrDeviceNotReady`；`validateTargetLocked` 末尾的 `Error("该设备的 ADB 尚未进入 device 状态")` 改成 `ErrDeviceNotReady`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestOperationTargetReportsNotReadyAsTransient|TestDirectConnect' -v`
预期：PASS（直连用例断言的是文案，文案没变）

- [ ] **步骤 5：写失败的测试（app）**

`internal/app/files_test.go`：

```go
// 重连来临时正在进行的目录读取会撞上「还没进 device 状态」。那是过渡态：
// 页面不许留下「读取失败」，连接恢复后自己重读。
func TestFileReadDuringAReconnectIsNotAFailure(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "照片.jpg")

	vm.Manager.ForceSessionStateForTest(profile.ID, core.StateConnecting, "")
	vm.loadDir(profile, "/sdcard", true)
	waitUntil(t, "the blocked read to finish", func() bool {
		return !vm.fileLoading(fileKey(profile.ID, "/sdcard"))
	})
	if reason := vm.fileFailure(fileKey(profile.ID, "/sdcard")); reason != "" {
		t.Fatalf("a transition state was cached as a failure: %q", reason)
	}

	vm.Manager.ForceSessionStateForTest(profile.ID, core.StateOnline, "")
	tt.Frame()
	if joined := strings.Join(tt.Texts(), "\n"); strings.Contains(joined, "读取失败") {
		t.Fatalf("the page kept a failure from a reconnect; texts = %v", tt.Texts())
	}
}
```

- [ ] **步骤 6：运行测试验证失败**

运行：`go test ./internal/app -run TestFileReadDuringAReconnectIsNotAFailure -v`
预期：FAIL，`a transition state was cached as a failure: "该设备的 ADB 尚未进入 device 状态"`

- [ ] **步骤 7：实现（app）**

`internal/app/files.go`：

```go
// transientFileError reports a read that failed only because the channel was
// changing: a cancelled read, or one that arrived before ADB reached `device`.
// Neither is the folder's fault, so neither is cached as a failure.
func transientFileError(err error) bool {
	return errors.Is(err, core.ErrCancelled) || errors.Is(err, core.ErrDeviceNotReady)
}
```

`loadDir` 的 goroutine 里把 `if errors.Is(err, core.ErrCancelled) { err = nil }` 换成 `if transientFileError(err) { err = nil }`；`prefetchDir` 同样处理。

- [ ] **步骤 8：运行测试验证通过**

运行：`go test ./internal/app -run 'TestFileReadDuringAReconnectIsNotAFailure|TestFilePageWaitsForTheUserAfterAFailedRead|TestFilePageShowsAReadFailureInline' -v`
预期：PASS（最后两个保证真正的失败仍然留着重试）

- [ ] **步骤 9：写变更记录并提交**

```markdown
- 文件页在重连过程中读目录，不再把「该设备的 ADB 尚未进入 device 状态」当成永久失败停在页面上；连接恢复后会自动重新读取。
```

```bash
gofmt -w internal/core internal/app
go vet ./...
go test ./internal/core ./internal/app
git add internal/core/err.go internal/core/manager_tasks.go internal/core/manager.go internal/core/manager_tasks_test.go internal/app/files.go internal/app/files_test.go docs/CHANGELOG.md
git commit -m "fix: treat a directory read during a reconnect as transient"
```

---

### 任务 3：文件页状态按连接归属，新连接从 `/` 开始

**文件：**
- 修改：`internal/core/session.go`、`internal/core/manager.go`、`internal/app/files.go`、`internal/app/state.go`
- 测试：`internal/core/manager_test.go`、`internal/core/manager_files_test.go`、`internal/app/files_test.go`

- [ ] **步骤 1：写失败的测试（core：连接标识）**

```go
// 一次连接有一个 id：文件页靠它判断手里的目录状态是不是这次连接的。
func TestConnectStampsTheSessionWithItsConnection(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	first := f.m.Session(f.id).ConnID
	if first == uuid.Nil {
		t.Fatal("a connected session has no connection id")
	}

	f.connect(t) // 重新连接
	second := f.m.Session(f.id).ConnID
	if second == uuid.Nil || second == first {
		t.Fatalf("reconnect kept the old connection id: %v → %v", first, second)
	}

	f.m.Disconnect(context.Background(), f.id)
	if got := f.m.Session(f.id).ConnID; got != uuid.Nil {
		t.Fatalf("a disconnected session still claims connection %v", got)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run TestConnectStampsTheSessionWithItsConnection -v`
预期：FAIL，`f.m.Session(f.id).ConnID undefined`

- [ ] **步骤 3：实现（core）**

`internal/core/session.go` 的 `Session` 里紧跟 `Serial`：

```go
	// ConnID is the channel this session's runtime facts belong to. The file
	// page keys its per-device state on it, so a new connection starts clean
	// instead of reusing the previous one's folders. Zero means no channel.
	ConnID uuid.UUID
```

`internal/core/manager.go`：

- `Connect` 里设 `StateConnecting` 的那个 `setStateLocked` 加 `s.ConnID = generation`；
- 同一锁区间内加 `m.forgetDirListingsLocked(id)`（上一次连接读到的目录不许被下一次连接直接命中）；
- `Disconnect` 最后的 `StateDisconnected` 那个 `setStateLocked` 加 `s.ConnID = uuid.Nil`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestConnectStampsTheSessionWithItsConnection|TestConnectFlow|TestDisconnect' -v`
预期：PASS

- [ ] **步骤 5：写失败的测试（core：缓存丢弃）**

```go
// 重连之后，上一次连接的目录缓存必须失效：否则新连接一进来就显示旧内容。
func TestReconnectForgetsCachedDirectoryListings(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.runner.setHandler(listFixture(listingFixture))
	if _, err := f.m.ListRemoteDir(context.Background(), f.id, "/sdcard"); err != nil {
		t.Fatalf("ListRemoteDir() error: %v", err)
	}
	if _, ok := f.m.DirListing(f.id, "/sdcard"); !ok {
		t.Fatal("the read was not cached")
	}

	f.connect(t)

	if _, ok := f.m.DirListing(f.id, "/sdcard"); ok {
		t.Fatal("a new connection reused the previous connection's listing")
	}
}
```

- [ ] **步骤 6：运行测试验证失败**

运行：`go test ./internal/core -run TestReconnectForgetsCachedDirectoryListings -v`
预期：FAIL。步骤 3 已经加了 `forgetDirListingsLocked` 的话这里直接 PASS——先跑，别假设。

- [ ] **步骤 7：写失败的测试（app）**

先在 `internal/app/files_test.go` 拆 helper：现有 `newFilesViewModel` 的函数体改名为 `newConnectedFilesViewModel`（连接 + 定位到文件页，**不导航**），然后

```go
// newFilesViewModel 停在 /sdcard：多数用例测的是列表内容，不是起点。
func newFilesViewModel(t *testing.T, read func(string) (core.Result, error)) (*ViewModel, func() []string) {
	t.Helper()
	vm, commands := newConnectedFilesViewModel(t, read)
	profile := vm.Manager.Profiles()[0]
	vm.showDir(profile, vm.fileStateFor(profile.ID), "/sdcard")
	return vm, commands
}
```

新用例：

```go
// 一次新连接从设备根开始：没有文件夹被自动打开（这就是"另一台设备打开的
// sdcard 也在这里打开了"的来源）。
func TestNewFilePageStartsAtTheDeviceRoot(t *testing.T) {
	vm, _ := newConnectedFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	if got := vm.fileStateFor(profile.ID).path; got != "/" {
		t.Fatalf("the page starts on %q, want the device root", got)
	}
	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "system")
	if tt.HasText("照片.jpg") {
		t.Fatalf("a fresh connection opened /sdcard by itself; texts = %v", tt.Texts())
	}
}

// 同一次连接里目录与展开状态要记住；换了连接就回到根。
func TestFilePageReturnsToTheRootForANewConnection(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	vm.showDir(profile, vm.fileStateFor(profile.ID), "/sdcard/Download")

	tt := ui.NewTester(vm.ContentView, 1000, 760)
	waitForText(t, tt, "update.zip") // 同一连接内确实记着

	if err := vm.Manager.Connect(context.Background(), profile.ID); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	waitForText(t, tt, "system") // 新连接：回到 /

	if got := vm.fileStateFor(profile.ID).path; got != "/" {
		t.Fatalf("the new connection reused the old folder: %q", got)
	}
	if tt.HasText("update.zip") {
		t.Fatalf("the old folder's rows are still on screen; texts = %v", tt.Texts())
	}
}
```

- [ ] **步骤 8：运行测试验证失败**

运行：`go test ./internal/app -run 'TestNewFilePageStartsAtTheDeviceRoot|TestFilePageReturnsToTheRootForANewConnection' -v`
预期：两个都 FAIL（`the page starts on "/sdcard"` / `the new connection reused the old folder`）

- [ ] **步骤 9：实现（app）**

`internal/app/files.go`：

```go
// filePageRoot is where a fresh connection's file page starts. It is the device
// root on purpose: /sdcard is what a remembered folder looks like, and every
// device opening it read as "it copied the other device's state".
const filePageRoot = "/"
```

`fileState` 加字段（`path` 上方）：

```go
	// conn is the connection this state belongs to. A new connection starts
	// from filePageRoot instead of inheriting the previous one's folder.
	conn uuid.UUID
```

`fileStateFor` 的首帧默认值只改两处：`path: filePageRoot, selected: filePageRoot`（`treeWidth`/`crumb`/`chosen` 保持现状）。新增：

```go
// syncFileState returns the state of the connection now on screen, resetting it
// when the device reconnected. A state created before its first frame (conn is
// zero) is adopted rather than reset, so callers may prepare a folder up front.
func (vm *ViewModel) syncFileState(device uuid.UUID, conn uuid.UUID) *fileState { ... }

// resetFileState drops everything the previous connection left: the folder, the
// tree, the table and the cached failures. Its directory listings are dropped
// by the manager when the new connection starts.
func (vm *ViewModel) resetFileState(device uuid.UUID, state *fileState, conn uuid.UUID) { ... }

// forgetFileEntries drops the per-path bookkeeping of one device.
func (vm *ViewModel) forgetFileEntries(device uuid.UUID) { /* 删掉 fileErrs / fileLoads 里该设备前缀的键 */ }
```

`filesPage` 里 `state := vm.fileStateFor(profile.ID)` 改成 `state := vm.syncFileState(profile.ID, session.ConnID)`（本任务用 `device uuid.UUID` 单键；任务 4 再把它换成带 pane 的 `filePaneKey`，两步都能独立跑绿）。

`prefetchSkip` 加 `/data`（shell 读不了，预读只会给树加一个红徽标；手动展开仍会读）。

- [ ] **步骤 10：运行测试验证通过**

运行：`go test ./internal/app -run 'TestNewFilePageStartsAtTheDeviceRoot|TestFilePageReturnsToTheRootForANewConnection' -v && go test ./internal/core -run TestReconnectForgetsCachedDirectoryListings -v`
预期：PASS

- [ ] **步骤 11：跑整个文件页测试，修被默认路径影响的地方**

运行：`go test ./internal/app -run 'TestFile|TestFolder|TestUpload|TestAttribute|TestTree|TestActivating|TestFolding' -v`
预期：PASS。仍假设起点是 `/sdcard` 的用例改用 `newFilesViewModel`（起点 `/sdcard` 的 helper）——**不要**把断言改弱。

- [ ] **步骤 12：写变更记录并提交**

```markdown
### 变更

- 文件页每次连接都从设备根目录 `/` 开始，不再沿用上一次连接（或看起来像另一台设备）打开过的文件夹；同一次连接内仍然记住当前目录、树的展开位置与表格列宽。
```

```bash
gofmt -w internal/core internal/app
go vet ./...
go test ./internal/core ./internal/app
git add internal/core/session.go internal/core/manager.go internal/core/manager_test.go internal/core/manager_files_test.go internal/app/files.go internal/app/state.go internal/app/files_test.go docs/CHANGELOG.md
git commit -m "fix: give the file page a fresh folder for every connection"
```

---

### 任务 4：把文件页右栏抽成可复用的 `fileBrowser`

**文件：**
- 修改：`internal/app/files.go`、`internal/app/state.go`
- 测试：`internal/app/files_test.go`（回归 + 新用例）

- [ ] **步骤 1：先跑现有用例，得到回归基线**

运行：`go test ./internal/app -run 'TestFile|TestFolder|TestTree|TestActivating|TestFolding|TestAttributeDialog|TestUpload|TestBreadcrumbs' -v`
预期：全 PASS。重构之后必须还是全 PASS——这是这个任务的验收标准，不要改这些断言。

- [ ] **步骤 2：写失败的测试（复用点 + 独立实例）**

```go
// 同一个设备可以有两个独立浏览器：文件页与「选择设备路径」。
func TestPickerKeepsItsOwnFolder(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	picker := vm.pickerStateFor(profile.ID)
	vm.showDir(profile, vm.fileStateFor(profile.ID), "/sdcard/Download")
	vm.showDir(profile, picker, "/system")

	if got := vm.fileStateFor(profile.ID).path; got != "/sdcard/Download" {
		t.Fatalf("the file page moved with the picker: %q", got)
	}
	if got := picker.path; got != "/system" {
		t.Fatalf("the picker lost its own folder: %q", got)
	}
}

// 文件页与选择器共用一套渲染：选择器里的表格也用同一套列。
func TestPickerRendersTheSameColumnsAsTheFilePage(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	tt := ui.NewTester(func(c *ui.Context) {
		vm.pickerPane(c, profile, vm.pickerStateFor(profile.ID), pickFolder)
	}, 760, 420)
	waitForText(t, tt, "照片.jpg")
	for _, want := range []string{"名称", "大小", "修改时间", "权限", "sdcard"} {
		if !tt.HasText(want) {
			t.Fatalf("the picker has no %q; texts = %v", want, tt.Texts())
		}
	}
}
```

- [ ] **步骤 3：运行测试验证失败**

运行：`go test ./internal/app -run 'TestPickerKeepsItsOwnFolder|TestPickerRendersTheSameColumnsAsTheFilePage' -v`
预期：FAIL，`undefined: pickerStateFor` / `pickerPane` / `pickFolder`

- [ ] **步骤 4：实现（拆出 `fileBrowser`）**

`internal/app/files.go`：

```go
// filePaneKey names one independent browser of one device: the file tab has
// one, the transfer dialog's device picker has its own, so browsing in either
// one never moves the other.
type filePaneKey struct {
	device uuid.UUID
	pane   int
}

const (
	filePanePage = iota
	filePanePicker
)

func filePagePane(device uuid.UUID) filePaneKey   { return filePaneKey{device: device, pane: filePanePage} }
func filePickerPane(device uuid.UUID) filePaneKey { return filePaneKey{device: device, pane: filePanePicker} }
```

`ViewModel.files` 的键从 `uuid.UUID` 改成 `filePaneKey`；`fileStateFor(device)` 保留（= 文件页那个 pane，测试与截图都在用），再加 `pickerStateFor(device)`。`syncFileState` / `resetFileState` 都改成收 `filePaneKey`；重置时按 `key.device` 清 `fileErrs` / `fileLoads`。

抽出浏览器（行为与今天逐条一致）：

```go
// fileBrowserOptions says how one caller of the browser differs: the file page
// offers refresh and upload, the picker offers 选定.
type fileBrowserOptions struct {
	// Label is the table's accessible name.
	Label  string
	// Actions is drawn to the right of the breadcrumbs.
	Actions func()
	// Choose, when non-nil, replaces the page's activation rule: a row click
	// calls it instead of opening the entry.
	Choose func(core.RemoteEntry)
	// Grow fills the remaining height (the page) instead of a fixed one (the
	// picker inside a dialog).
	Grow bool
	// Height is used when Grow is false.
	Height float32
	// Unframed skips tableCard's border: the page draws its own frame.
	Unframed bool
}

// fileBrowser draws the right pane: breadcrumbs, the caller's actions, the
// listing of state.path, and what a row activation does.
func (vm *ViewModel) fileBrowser(c *ui.Context, profile core.Profile, state *fileState, opts fileBrowserOptions)
```

`fileTable` 变成 `fileBrowser` 的一层包装（`Label: "设备文件列表"`、`Actions` = 刷新 + 上传到此文件夹、`Grow: true`、`Unframed: true`，`Choose: nil`），保留 `activateEntry` 作为默认激活规则。

选择器侧（本任务只做渲染与状态，弹窗接线在任务 6）：

```go
// pickerMode decides what the picker lets the user choose.
type pickerMode int

const (
	// pickFolder is an upload destination: only the current folder counts.
	pickFolder pickerMode = iota
	// pickEntry is a download source: a file can be chosen directly, or the
	// current folder.
	pickEntry
)

// pickerPane draws the device browser inside the transfer dialog.
func (vm *ViewModel) pickerPane(c *ui.Context, profile core.Profile, state *fileState, mode pickerMode)
```

`pickerPane`：`components.Split`（左 `folderTree`，右 `fileBrowser{Label: "选择设备路径列表", Actions: 刷新, Choose: 点文件时回调, Height: 360, Unframed: false}`），外部高度 `Height(380)`。

- [ ] **步骤 5：运行测试验证通过**

运行：`go test ./internal/app -run 'TestPicker|TestFile|TestFolder|TestTree|TestActivating|TestFolding|TestAttributeDialog|TestUpload' -v`
预期：全 PASS（新用例 + 步骤 1 的基线一起）

- [ ] **步骤 6：Commit**

```bash
gofmt -w internal/app
go vet ./...
go test ./internal/app
git add internal/app/files.go internal/app/state.go internal/app/files_test.go
git commit -m "refactor: share the file browser between the page and a device picker"
```

---

### 任务 5：进度的共享件（`taskBatch` / `batchSnapshot` / `progressBlock`）

**文件：**
- 创建：`internal/app/taskprogress.go`、`internal/app/taskprogress_test.go`
- 修改：`internal/core/pull.go`、`internal/core/pull_test.go`、`internal/app/apk.go`

- [ ] **步骤 1：写失败的测试（core：push 也打同一种进度行）**

`internal/core/pull_test.go`：`TestParsePullPercent*` 改名 `TestParseTransferPercent*`，并加：

```go
// adb push 打的是同一种进度行；名字里带 Pull 会让下一个人再写一份解析。
func TestParseTransferPercentReadsAPushLine(t *testing.T) {
	percent, ok := ParseTransferPercent("[ 42%] /sdcard/Download/update.zip\r[100%] /sdcard/Download/update.zip\n")
	if !ok || percent != 100 {
		t.Fatalf("ParseTransferPercent() = %d, %v; want the last record", percent, ok)
	}
	if _, ok := ParseTransferPercent("Performing Streamed Install\n"); ok {
		t.Fatal("an install line is not a percentage")
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run TestParseTransferPercentReadsAPushLine -v`
预期：FAIL，`undefined: ParseTransferPercent`

- [ ] **步骤 3：实现（core + apk.go）**

`internal/core/pull.go` 里改名，注释改成 `adb pull` / `adb push`；`internal/app/apk.go` 两处调用改名。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run TestParseTransferPercent -v && go test ./internal/app -run TestAPK -v`
预期：PASS

- [ ] **步骤 5：写失败的测试（app：纯函数）**

`internal/app/taskprogress_test.go`：

```go
func TestBatchProgressAddsUpFinishedAndRunningFiles(t *testing.T) {
	batch := taskBatch{Kind: taskPush, Tasks: []uuid.UUID{uuid.New(), uuid.New()}}
	tasks := []core.Task{
		{ID: batch.Tasks[0], State: core.TaskSucceeded},
		{ID: batch.Tasks[1], State: core.TaskRunning, Output: "[ 50%] /sdcard/Download/b.bin"},
	}
	view := batchSnapshot(batch, tasks, nil)
	if view.Finished != 1 || view.Count != 2 {
		t.Fatalf("view = %+v", view)
	}
	if view.Fraction < 0.74 || view.Fraction > 0.76 {
		t.Fatalf("fraction = %v, want 0.75", view.Fraction)
	}
	if view.Text != "已完成 1 / 2 个文件" {
		t.Fatalf("text = %q", view.Text)
	}
}

func TestBatchProgressIsIndeterminateWithoutAPercentage(t *testing.T) {
	id := uuid.New()
	view := batchSnapshot(taskBatch{Kind: taskInstall, Tasks: []uuid.UUID{id}},
		[]core.Task{{ID: id, State: core.TaskRunning, Output: "Performing Streamed Install\n"}}, nil)
	if view.Fraction != -1 {
		t.Fatalf("fraction = %v, want -1 (不确定)", view.Fraction)
	}
	if view.Text != "Performing Streamed Install" {
		t.Fatalf("text = %q, want adb's last line", view.Text)
	}
}

func TestBatchProgressFallsBackToTheBytesOnDisk(t *testing.T) {
	id := uuid.New()
	size := func(string) int64 { return 512 }
	view := batchSnapshot(taskBatch{Kind: taskPull, Tasks: []uuid.UUID{id}, LocalPath: "/tmp/a.bin", Total: 1024},
		[]core.Task{{ID: id, State: core.TaskRunning}}, size)
	if view.Fraction != 0.5 || view.Text != "已传输 512 B / 1.0 KB" {
		t.Fatalf("view = %+v", view)
	}
}

func TestBatchProgressShowsWhyTheTaskFailed(t *testing.T) {
	id := uuid.New()
	view := batchSnapshot(taskBatch{Kind: taskInstall, Tasks: []uuid.UUID{id}},
		[]core.Task{{ID: id, State: core.TaskFailed, Output: "Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]\n"}}, nil)
	if view.State != core.TaskFailed || !strings.Contains(view.Reason, "INSTALL_FAILED_INSUFFICIENT_STORAGE") {
		t.Fatalf("view = %+v", view)
	}
}

func TestBatchProgressIsCompleteWhenEveryTaskFinished(t *testing.T) {
	id := uuid.New()
	view := batchSnapshot(taskBatch{Kind: taskPush, Tasks: []uuid.UUID{id}},
		[]core.Task{{ID: id, State: core.TaskSucceeded}}, nil)
	if view.State != core.TaskSucceeded || view.Fraction != 1 || view.Text != "已完成" {
		t.Fatalf("view = %+v", view)
	}
}

func TestElapsedLabelSpellsOutMinutes(t *testing.T) {
	cases := map[time.Duration]string{
		3 * time.Second:                "已用 3 秒",
		65 * time.Second:               "已用 1 分 05 秒",
		2*time.Minute + 7*time.Second:  "已用 2 分 07 秒",
	}
	for in, want := range cases {
		if got := elapsedLabel(in); got != want {
			t.Fatalf("elapsedLabel(%v) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **步骤 6：运行测试验证失败**

运行：`go test ./internal/app -run 'TestBatchProgress|TestElapsedLabel' -v`
预期：FAIL，`undefined: taskBatch` / `batchSnapshot` / `elapsedLabel`

- [ ] **步骤 7：实现（`internal/app/taskprogress.go`）**

```go
// taskKind says which adb command the batch runs; it decides what "progress"
// means. Only push and pull print percentages.
type taskKind int

const (
	taskInstall taskKind = iota
	taskPush
	taskPull
)

// taskBatch is the operation one dialog is running: one task, or the several
// uploads that one pick started.
type taskBatch struct {
	Device uuid.UUID
	// Title is the operation's name, e.g. 上传 update.zip / 上传 3 个文件 /
	// 安装 app-release.apk.
	Title string
	// Note is the secondary line, e.g. 安装到用户 0 · Owner（当前）.
	Note string
	Kind taskKind
	Tasks []uuid.UUID
	// LocalPath and Total are the pull fallback: adb does not always print a
	// percentage, so the bytes on disk answer instead.
	LocalPath string
	Total     int64
	Started   time.Time
}

// batchView is what the progress block shows for one frame.
type batchView struct {
	State    core.TaskState
	Fraction float64 // 0..1, or -1 when nothing is known yet
	Text     string
	Reason   string
	Finished int
	Count    int
}

// batchSnapshot turns the batch's tasks into what the dialog shows...
func batchSnapshot(batch taskBatch, tasks []core.Task, size func(string) int64) batchView

// progressBlock draws the bar, its text and the elapsed time, and returns what
// it drew. Both dialogs call it so the two can never disagree.
func progressBlock(c *ui.Context, batch taskBatch, tasks []core.Task, size func(string) int64) batchView

// elapsedLabel spells out how long a task has been running.
func elapsedLabel(d time.Duration) string
```

`batchSnapshot` 的规则（测试即规格）：

- `Count = len(batch.Tasks)`；`Finished` 是状态不是 `TaskRunning` 的数量（已被历史裁掉的任务算已完成，`Reason` 留空）。
- `State`：任一 `TaskFailed` → `TaskFailed`（`Reason` = 最后一个失败任务的 `strings.TrimSpace(Output)`）；否则任一 `TaskCancelled` → `TaskCancelled`；否则还有 `TaskRunning` → `TaskRunning`；否则 `TaskSucceeded`。
- 百分比分支（只有 `taskPush` / `taskPull` 才试）：运行中任务的 `core.ParseTransferPercent` 给出 `p`，则 `Fraction = (Finished + p/100) / Count`。
- 单任务 + `taskPull` + `Total > 0` + `LocalPath != ""` 且 `size != nil` 时退回字节：`Fraction = min(1, size(LocalPath)/Total)`，`Text = "已传输 " + core.FormatBytes(size) + " / " + core.FormatBytes(Total)`；`size` 为 nil 跳过这条。
- 其余运行中：`Fraction = -1`。全部完成：`Fraction = 1`、`Text = "已完成"`。
- `Text`：`taskInstall` 运行中 → 运行中任务输出的最后一个非空行，没有则 `正在安装，请稍候…`；`taskPush`/`taskPull` 单任务 → `42%`；多任务 → `已完成 n / m 个文件`。

`progressBlock`：`components.Progress(c, view.Fraction).Label("操作进度")` + `components.Text(c, view.Text)`（`Text` 非空时）+ 运行中 `components.Text(c, elapsedLabel(time.Since(batch.Started)) + "…")` 灰色。

- [ ] **步骤 8：运行测试验证通过**

运行：`go test ./internal/app -run 'TestBatchProgress|TestElapsedLabel' -v`
预期：PASS

- [ ] **步骤 9：Commit**

```bash
gofmt -w internal/core internal/app
go vet ./...
go test ./internal/core ./internal/app
git add internal/core/pull.go internal/core/pull_test.go internal/app/apk.go internal/app/taskprogress.go internal/app/taskprogress_test.go
git commit -m "feat: add the shared progress block the transfer and install dialogs draw"
```

---

### 任务 6：传输弹窗改成「表单 → 选择设备路径 → 运行 → 结果」

**文件：**
- 修改：`internal/app/transfer.go`（重写）、`internal/app/actions.go`、`internal/app/state.go`、`internal/app/files.go`、`internal/app/detail.go`（入口调用不变）
- 测试：`internal/app/transfer_test.go`（新建）、`internal/app/files_test.go`

- [ ] **步骤 1：写失败的测试**

```go
// 本地路径在弹窗里选：打开弹窗什么也没选，点「选择文件…」才填上。
func TestUploadDialogPicksItsLocalFilesInsideTheDialog(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	openLocalPaths = func(mygo.OpenDialogOptions) ([]string, error) {
		return []string{"/tmp/update.zip"}, nil
	}
	t.Cleanup(func() { openLocalPaths = mygo.Dialog.Open })

	vm.chooseUpload(profile)
	if draft, open := vm.transferSnapshot(); !open || draft.Upload != true || len(draft.LocalPaths) != 0 {
		t.Fatalf("the dialog should open empty: %+v, %v", draft, open)
	}

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("选择文件…"); err != nil {
		t.Fatalf("no 选择文件… button: %v", err)
	}
	draft, _ := vm.transferSnapshot()
	if len(draft.LocalPaths) != 1 || draft.LocalPaths[0] != "/tmp/update.zip" {
		t.Fatalf("local paths = %v", draft.LocalPaths)
	}
}

// 「浏览设备…」把同一个弹窗切到选择阶段，选中的是设备上的目录。
func TestUploadDialogBrowsesTheDeviceForItsTargetFolder(t *testing.T) {
	vm, _ := newFilesViewModel(t, readFixture)
	profile := vm.Manager.Profiles()[0]
	vm.chooseUpload(profile)

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	if err := tt.Click("浏览设备…"); err != nil {
		t.Fatalf("no 浏览设备… button: %v", err)
	}
	waitForText(t, tt, "Download")
	if err := tt.Click("Download"); err != nil { // 进入文件夹
		t.Fatalf("the picker has no Download row: %v", err)
	}
	waitForText(t, tt, "update.zip")
	if err := tt.Click("选择此文件夹"); err != nil {
		t.Fatalf("no 选择此文件夹 button: %v", err)
	}
	draft, _ := vm.transferSnapshot()
	if draft.RemotePath != "/sdcard/Download" {
		t.Fatalf("remote path = %q", draft.RemotePath)
	}
	if draft.Stage != transferStageForm {
		t.Fatalf("the picker did not return to the form: %d", draft.Stage)
	}
}

// 下载的来源可以直接点一个文件。
func TestDownloadDialogPicksAFileFromTheDevice(t *testing.T) { /* 同上，点「照片.jpg」后 RemotePath == "/sdcard/照片.jpg" */ }

// 开始之后进度就在这个弹窗里；关窗不杀任务、取消才杀。
func TestTransferShowsProgressInTheSameDialog(t *testing.T) { /* 用 streamingRunner{hold:true} 起任务，断言 "正在上传" + "取消" */ }
func TestTransferCloseWhileRunningKeepsTheTask(t *testing.T) { /* tt.Click("关闭") 或 Esc，任务仍 TaskRunning */ }
func TestTransferCancelStopsTheTask(t *testing.T) { /* 点「取消」后 TaskCancelled */ }

// 两个路径没齐时不能开始。
func TestTransferStartIsDisabledWithoutBothPaths(t *testing.T) { /* 只填远端 → 按钮 Disabled，点不动 */ }
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run 'TestUploadDialog|TestDownloadDialog|TestTransfer' -v`
预期：FAIL，`undefined: vm.transferSnapshot` / `transferStageForm`

- [ ] **步骤 3：实现（`transferDraft` + 阶段渲染）**

`internal/app/transfer.go` 重写成一份状态机，字段与 `installDraft` / `apkSave` 同构：

```go
// 传输弹窗的阶段。
const (
	transferStageForm = iota
	transferStageBrowsing
	transferStageRunning
	transferStageDone
	transferStageFailed
)

// transferDraft is the 「上传文件」/「下载文件」 dialog: what the user chose, where
// it is browsing, and the task it is following. A nil *transferDraft closes it.
type transferDraft struct {
	Device uuid.UUID
	Upload bool
	Stage  int
	// LocalPaths is one or more local files/folders for an upload, and exactly
	// one local destination for a download.
	LocalPaths []string
	// RemotePath is the device side: the destination folder of an upload, the
	// source of a download.
	RemotePath string
	// PickerMode says what the browser stage may choose.
	PickerMode pickerMode
	// Err explains the last refusal (a bad path, a task that would not start).
	Err     string
	TaskIDs []uuid.UUID
	Started time.Time
}
```

`ViewModel` 加 `Transfer *transferDraft` + `transferMu`（替代原来的 `TransferOpen/TransferUpload/TransferLocalPath/TransferRemotePath/TransferError` 五个字段，`state.go` 里那五个删掉）。

新增方法：`transferSnapshot() (transferDraft, bool)`、`openTransfer(profile, upload bool)`、`closeTransfer()`、`browseDevice()`（进 `transferStageBrowsing`，`pickerStateFor(device)` 从文件页当前目录开始）、`leaveBrowsing()`、`chooseEntry(entry)`、`chooseFolder()`、`submitTransfer()`、`closeWhileRunning()`、`cancelTransfer()`。

`TransferView` 按阶段画（同一个 `components.Dialog`，`open` 由 `vm.Transfer != nil` 决定）：

- 表单：见 §1.4 的两张表；`开始` 的禁用条件是「上传：`len(LocalPaths)==0` 或远端为空」「下载：远端为空或 `len(LocalPaths)==0`」。
- 浏览：`panel.Width(760)`，`pickerPane(c, profile, state, draft.PickerMode)` + 底部 `返回` / `选择此文件夹`。
- 运行：`progressBlock` + `取消` / `关闭`；`batch := taskBatch{Device, Title, Kind, Tasks: draft.TaskIDs, LocalPath: 下载时的落地路径, Started: draft.Started}`。
- 结果：`已完成` / `失败：…` / `已取消` + `关闭`。

`submitTransfer`（就地开跑，不再关窗）：校验 → `BeginADBTask`（上传对每个本地路径各一个任务；下载一个）→ 记 `TaskIDs`、`Started`、`Stage = transferStageRunning`；起不来就把原因写 `Err` 并留在表单阶段。

- [ ] **步骤 4：改两个入口**

`internal/app/actions.go`：`chooseUpload` / `startDownload` 只负责 `vm.openTransfer(profile, true/false)`（不再预先弹系统选择器、不再预填 `/sdcard/Download/`；远端默认 `/sdcard`，见 D1 的口径：新弹窗不猜一个深层目录）。`chooseDownloadFolder` 变成弹窗里的「选择位置…」回调（下载的本地目的地）。

- [ ] **步骤 5：文件页的两个一键入口接到同一个弹窗**

`internal/app/files.go`：

- `pushInto`：选完本地文件 → `openTransfer` → 填 `LocalPaths`、`RemotePath = remoteDir` → 立即 `submitTransfer()`（弹窗直接进运行阶段）。
- `startPull`：本地位置已选 → `openTransfer(upload=false)` → 填 `RemotePath = entry.Path`、`LocalPaths = [local]` → `submitTransfer()`。

- [ ] **步骤 6：运行测试验证通过**

运行：`go test ./internal/app -run 'TestUpload|TestDownload|TestTransfer|TestAttributeDialog|TestPicker' -v`
预期：PASS

- [ ] **步骤 7：更新受影响的用例**

`dialog_test.go` 的 `{"transfer", ...}` 改成 `vm.openTransfer(...)` / `vm.closeTransfer()`；`forms_test.go` 只测 `ValidateRemotePath`，不受影响。跑 `go test ./internal/app` 全量，把因字段改名而红的用例按新 API 改好（不许删断言）。

- [ ] **步骤 8：写变更记录并提交**

```markdown
### 变更

- 「上传」「下载」弹窗改版：本地文件/保存位置在弹窗里选（和「安装 APK」一致），设备侧路径用弹窗内的「浏览设备…」选择（复用文件页的文件夹树与文件列表），仍然可以手打路径。
- 上传、下载、安装的进度条直接显示在原来的弹窗里（百分比 / 已传输字节 / 文件计数 / 已用时间），结束时就地显示结果；「取消」中止任务，关窗则任务继续运行。

### 新增

- 文件页的「上传到此文件夹…」与文件属性的「下载到本地…」选完本地路径后，直接进入传输弹窗的运行阶段，不再没有反馈。
```

```bash
gofmt -w internal/app
go vet ./...
go test ./internal/app ./internal/core
git add internal/app/transfer.go internal/app/actions.go internal/app/state.go internal/app/files.go internal/app/detail.go internal/app/transfer_test.go internal/app/files_test.go internal/app/dialog_test.go docs/CHANGELOG.md
git commit -m "feat: choose both transfer paths in the dialog and show progress there"
```

---

### 任务 7：安装弹窗加运行阶段，进度也在本弹窗里

**文件：**
- 修改：`internal/app/install.go`
- 测试：`internal/app/install_test.go`

- [ ] **步骤 1：写失败的测试**

```go
// 点「安装」不再关窗：同一个弹窗切到运行阶段，结束显示结果。
func TestInstallShowsProgressInItsOwnDialog(t *testing.T) {
	vm, runner := apkFixture(t, nil) // runner 的 install 返回 Success
	profile := vm.Manager.Profiles()[0]
	vm.openInstallDialog(profile.ID, "/tmp/示例.apk")
	waitUntil(t, "the user list to load", func() bool {
		state, open := vm.installSnapshot()
		return open && !state.Loading
	})
	state, _ := vm.installSnapshot()
	vm.submitInstall(&state)
	vm.Manager.WaitTask(state.TaskIDs[0])

	tt := ui.NewTester(vm.ContentView, 1000, 800)
	tt.Frame()
	for _, want := range []string{"安装 APK", "安装 示例.apk", "已完成", "关闭"} {
		if !tt.HasText(want) {
			t.Fatalf("the install dialog has no %q; texts = %v", want, tt.Texts())
		}
	}
}

// 失败时说清 adb 的原因。
func TestInstallDialogShowsWhyTheInstallFailed(t *testing.T) { /* runner 返回 ExitCode 1 + "Failure [INSTALL_FAILED_INSUFFICIENT_STORAGE]" */ }

// 取消杀任务，关窗不杀。
func TestInstallCancelStopsTheTask(t *testing.T)           { /* 点「取消」→ TaskCancelled */ }
func TestInstallCloseWhileRunningKeepsTheTask(t *testing.T) { /* 关窗 → TaskRunning */ }

// 运行中不能再点出第二个安装。
func TestInstallStartsOneTaskOnly(t *testing.T) // 已存在，改成"运行阶段没有安装按钮"
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/app -run TestInstall -v`
预期：FAIL，`state.TaskIDs undefined`

- [ ] **步骤 3：实现**

`installDraft` 加：

```go
	// Stage is the dialog's phase: form → running → done/failed.
	Stage   int
	TaskIDs []uuid.UUID
	Started time.Time
```

（复用 `transferStage*` 的取值，或在本文件定义 `installStageForm/Running/Done/Failed`，二选一并写进注释；**建议复用 `transferStage*`**，两个弹窗的阶段含义相同。）

`submitInstall` 成功后：写 `TaskIDs` / `Started` / `Stage = transferStageRunning`，**不再** `vm.APKInstall = nil`；结束（读 `Manager.Tasks()` 得到终态）时把 `Stage` 切到 done/failed，并在 `Stage` 转入结果时清掉 `Loading` 的语义（表单阶段才看用户列表）。

`InstallView` 按阶段画：

- 表单：今天的布局（标题、APK 文件行、`选择 APK 文件`、覆盖安装说明、用户选择、错误、`取消`/`安装`）。
- 运行：`progressBlock(c, taskBatch{Device, Title: "安装 " + fileName(path), Note: installNote(state), Kind: taskInstall, Tasks: state.TaskIDs, Started: state.Started}, vm.Manager.Tasks(), fileSize)` + `取消` / `关闭`。
- 结果：`已完成`（`ButtonPrimary` 的 `关闭`）或 `安装失败：<原因>`。

额外补一个 `installNote` 测试：`TestInstallProgressNamesTheChosenUser`（`Note` 形如 `安装到用户 0 · Owner（当前）`）。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/app -run TestInstall -v`
预期：PASS

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/app
go vet ./...
go test ./internal/app
git add internal/app/install.go internal/app/install_test.go
git commit -m "feat: run the install inside its dialog with progress"
```

---

### 任务 8：截图核对、决策记录与全量验证

**文件：**
- 修改：`internal/app/screenshots_test.go`、`docs/architecture.md`、`docs/CHANGELOG.md`

- [ ] **步骤 1：加截图用例**

在 `shotCases()` 末尾按现有编号追加：

```go
		{
			name:  "05c-device-files-fresh-connection",
			build: func(t *testing.T) *ViewModel { return shotDevice(t, readFixture, detailFiles) },
			settle: 20,
		},
		{
			name: "41-upload-dialog-form",
			build: func(t *testing.T) *ViewModel {
				vm := shotDevice(t, readFixture, detailFiles)
				vm.openTransfer(vm.Manager.Profiles()[0], true)
				return vm
			},
			settle: 12,
		},
		{
			name: "41b-upload-dialog-picker",
			build: func(t *testing.T) *ViewModel {
				vm := shotDevice(t, readFixture, detailFiles)
				vm.openTransfer(vm.Manager.Profiles()[0], true)
				vm.browseDevice()
				return vm
			},
			settle: 24,
		},
		{
			name: "42-transfer-running",
			build: func(t *testing.T) *ViewModel { /* streamingRunner 建一个真任务，见任务 5 的 helper */ },
			settle: 12,
		},
		{
			name: "43-install-running",
			build: func(t *testing.T) *ViewModel { /* 打开安装弹窗并 submitInstall，runner 阻塞 */ },
			settle: 12,
		},
```

- [ ] **步骤 2：渲染并人眼核对**

运行：`TAILCAT_SHOT_DIR=/tmp/shots go test ./internal/app -run TestScreenshots -v`
要看的：`05-device-files.png`、`05c-device-files-fresh-connection.png`、`41-upload-dialog-form.png`、`41b-upload-dialog-picker.png`、`42-transfer-running.png`、`43-install-running.png`、`34-install-apk-dialog.png`。
确认：760 宽的浏览阶段里树与表格都不被裁；进度条与文本不重叠；失败原因在弹窗内换行；按钮层级（结束态 `关闭` 是 Primary，运行态只有 `取消`/`关闭` 两个 Outline）；`/` 的文件页只剩一行根。

- [ ] **步骤 3：写决策记录**

`docs/architecture.md` 的「设计决策记录」加四条（每条带代价）：

1. 文件页状态按 `Session.ConnID` 归属，新连接回到 `/`——代价：进入文件页多一层；换来"不继承上一次/别人的目录"。
2. 连接切换中的目录读取失败不缓存——代价：多一个 `ErrDeviceNotReady` 哨兵，要靠它区分临时与真实失败。
3. 设备路径选择器与文件页共用 `fileBrowser`，但各自一个 `fileState`——代价：每设备多一份浏览状态；换来"选路径不会把页面顶掉"。
4. 进度放回各自的弹窗、关窗不杀任务——代价：关窗后要回日志页找任务；换来"操作与它的进度在同一个地方"。

- [ ] **步骤 4：全量验证**

```bash
gofmt -l .          # 必须无输出
go vet ./...
go test ./...
go test -race ./internal/core ./internal/app
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
```

- [ ] **步骤 5：Commit**

```bash
git add internal/app/screenshots_test.go docs/architecture.md docs/CHANGELOG.md
git commit -m "docs: record the connection-scoped file page and the in-dialog progress"
```

---

## 5. 不做的事（YAGNI）

- 不做独立的「任务中心」弹窗或任务队列 UI（日志页的任务列表已经有了）。
- 不给选择器加搜索框、多选、新建文件夹。
- 不改 `apkSave`（「保存 APK」）；它的百分比与字节兜底已经是同一套 `ParseTransferPercent` / `fileSize`。
- 不改任务历史的保留策略（`maxTaskHistory`）。
- 不给安装加"重新安装""卸载"。
- 不动 Swift 原版（`tailcat-tool-swift/`）。

## 6. 已知限制

- 进度百分比取决于 adb 有没有打印 `[ nn%]`：新旧 platform-tools 行为不一致，安装则从不打印。没有百分比时显示不确定进度条 + 已用时间，不假装有数。
- 多文件上传的聚合百分比只在"当前文件有百分比"时准确；每换一个文件会有一次回落（按已完成文件数摊平，不会倒退到 0）。
- 目录下载（`adb pull` 一个文件夹）没有总字节数，只能靠 adb 的百分比行。
- 选择器不预读整棵树（只预读当前层），进一个大目录仍会有一次等待。
