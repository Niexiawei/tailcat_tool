# 剩余工作实施计划

> 起点：`feat/go-rewrite` @ `58ead78`（界面、内嵌终端、锁与布局修复已完成并验证）。
> 目标：把项目从「功能可用」推进到「可以交付给别人安装使用」。
> 规则：遵循 `AGENTS.md`（纯 Go/无 cgo、依赖方向、onevra 优先、TDD、CHANGELOG 同提交）。

## 现状与缺口

| 能力 | 状态 |
|---|---|
| 会话管理、两种连接方式、干预检测、历史库、内嵌终端、路由与面包屑 | 完成 |
| 自动化测试 | 168 个用例，`-race` 干净 |
| 真机联调（Tailcat 真链路 / scrcpy / 授权 / 干预弹窗） | **未做** |
| 安装包（dmg / msi / deb）与签名 | **未做** |
| 历史数据只能"载入任务面板"，没有浏览/筛选界面 | 缺 |
| `adb kill-server` 会连出多条提示，没有聚合 | 缺 |
| 配置/历史库读取失败只有一条横幅 | 缺 |
| 托盘没有"待处理"数量提示 | 缺 |

优先级：**P1 打包** > **P6 真机验收** > **P2 启动错误页** > **P4 聚合提示** > **P3 历史界面** > **P5 托盘**。
P1 与 P6 是"能不能交付"的问题；P2/P4/P5 是稳定性与观感；P3 是新功能，最后做。

---

## P1 发布打包与版本

### 目标

`go tool mygo build` 一条命令产出 macOS `.app` + `.dmg`、Windows `.exe` + 安装器、Linux 可执行 + `.deb`/`.rpm`/`.tar.gz`，且**内嵌终端插件被打进包**（用户首次打开终端不需要联网下载 libghostty-vt）。

### 步骤

1. **声明 CLI 工具**（`go.mod`）：
   ```go
   tool github.com/egoist/mygo/cmd/mygo
   ```
   之后统一用 `go tool mygo build`，版本随 go.mod 锁定。
2. **写 `mygo.json`**（与 `mygo.config.ts` 二选一，本项目用 JSON，不引入 JS 运行时）：
   ```json
   {
     "name": "Tailcat Manager",
     "identifier": "cc.coauto.tailcat-manager",
     "version": "0.1.0",
     "copyright": "© 2026 …",
     "macos": { "signingIdentity": "" }
   }
   ```
   `identifier` 必须与凭据库服务名 `cc.coauto.tailcat-manager.connections` 的前缀一致，一次定死。
3. **图标**：`resources/icon.png`（1024×1024），由现有 `tray.png` 放大重绘或另备；`resources/icon-dev.png` 可选。
4. **版本注入**：`App.Version()` 或链接期变量 `main.version`，`--version` 输出与 `mygo.json` 同源（`mygo build` 会链接配置里的版本，运行时用 `mygo.App.Version()`）。
5. **CI 增加打包任务**（`.github/workflows/ci.yml`）：
   - `ubuntu-latest`：`go tool mygo build -platform linux/amd64,linux/arm64`
   - `macos-latest`：`darwin/universal`（`.dmg`）
   - `windows-latest`：`windows/amd64`（安装器）
   - 产物上传为 workflow artifact；签名凭据用 secrets，缺失时降级为未签名并打印说明。
6. **文档**：`docs/build-notes.md` 补一节「打包」，写明 `mygo build` 与 `go build` 的差异（尤其终端库）。CHANGELOG 记「新增：安装包与版本号」。

### 验收

- 干净机器（无 Go、无缓存）安装 `.dmg` / `.msi` / `.deb` 后能启动，托盘图标正常，终端页**无需联网**即可输入。
- `tailcat-manager --version` 与 `mygo.json` 一致。
- CI 三个平台产物都能下载，`ls -l` 体积记录进 `build-notes.md`。

### 风险

- macOS 未签名时 Gatekeeper 拦截：文档里给出 `xattr -dr com.apple.quarantine` 的临时办法，正式发布需要 Developer ID。
- Windows 安装器需要 NSIS：CI 的 `windows-latest` 自带，其他平台跳过并提示。
- `mygo build` 会在 build 产物里改名（`Tailcat Manager.app`），脚本不要硬编码旧名字。

---

## P6 真机验收（不写代码，但要留记录）

### 清单

| # | 场景 | 期望 |
|---|---|---|
| 1 | USB 直连（`emulator-5554` 或真机序列号） | 连上后型号/Android/Shell 权限自动出现 |
| 2 | 本地网络直连（`IP:5555`） | 探活走 `adb connect`，断开时 `adb disconnect` |
| 3 | Tailcat 真链路（对端跑 `tailcat` server） | 本地端口从 5556 起分配，`adb devices` 出现 `127.0.0.1:5556` |
| 4 | 设备未授权 | 状态「ADB 待授权」，不重试，提示在设备上确认 |
| 5 | `adb disconnect 127.0.0.1:5556` | 侧栏变「等待处理」，**不自动重连**，回窗口弹对话框，三个选项各试一次 |
| 6 | 同一台设备被映射到别的端口 | 提示「端口变化」，只作线索，不接管 |
| 7 | scrcpy 屏幕控制 | 窗口能开，参数 1280/4M/30fps，关闭不留进程 |
| 8 | 内嵌终端两种模式 | Shell 有提示符；ADB 命令能 `pull` 文件到本地 |
| 9 | 两台设备同时在线 | 任务、日志、终端互不串台 |
| 10 | 重启应用 | 设备配置、历史任务都在；历史库损坏时能启动并提示 |

### 产出

- `docs/verification.md`：逐条记录日期、环境（OS / adb 版本 / 设备型号）、结果、异常截图与处理。
- 任何一条失败 → 开 issue 或在 CHANGELOG 的「已知限制」记一条。

---

## P2 启动错误页

### 现状问题

配置读取失败、历史库打不开、凭据库不可用，都只是 `vm.Notice` 一行横幅，用户不知道该怎么做，也不知道当前跑的是"降级模式"。

### 设计

在 `App.Start` 之前判断环境健康度，异常时**不建主窗口**，改建一个启动页（同 `Home` 路由风格）：

```
┌ Tailcat Manager ─────────────────────┐
│ ⚠ 配置读取失败                        │
│ 配置文件无法解析：unexpected end …    │
│ 路径：~/…/TailcatManager/configuration.json │
│ [ 用默认设置继续 ] [ 打开配置目录 ] [ 退出 ] │
└──────────────────────────────────────┘
```

- 健康度检查放进 `core`：`func Diagnose(env) []Problem`（配置解析、配置目录权限、历史库可打开、凭据库可用、adb/scrcpy 可执行），纯函数，可单测。
- 每个 `Problem` 带：标题、详情、路径、是否可继续、修复动作（打开目录 / 重试 / 继续降级）。

### 测试

- `TestDiagnoseReportsBrokenConfig`：写入半截 JSON → 断言 `Problem` 的标题与路径。
- 视图：`TestStartupPageListsProblems`，用 `ui.NewTester` 断言三条文案与按钮。

### 备注

历史库不可用属于"可继续"，配置不可解析也属于"可继续但要用默认设置"，凭据库不可用属于"可继续但地址只在本次运行有效"——三者文案要能区分。

---

## P4 `adb kill-server` 的聚合提示

### 现状问题

`adb kill-server` 之后，每台设备都会命中 `serialDetached`，弹出多条「入口被移除」，用户以为是自己操作错了。

### 设计

新增两种 `AttentionKind`：

| 规则 | 条件 | 提示 |
|---|---|---|
| `adbServerRestarted` | 多台设备**同时**（同一监视轮次内）从 `adb devices` 消失，且随后 `adb devices` 返回「daemon started」或空表 | 「ADB 服务被重启，N 台设备受影响」+ 一个「全部重新连接」按钮 |
| `listenerLost` | 本会话的本地监听端口仍在，但 `adb devices` 里该端口消失且**本轮之前存在**——单台时降级为 `serialDetached` | 合并进上一条 |

实现要点：

- 聚合判定放进 `core` 纯函数：`func AggregateDetachments(events []Detachment) *Attention`，单测覆盖 0/1/多台。
- 一轮监视里只产生**一条** `Attention`；`Attention.DeviceIDs []uuid.UUID` 扩展为多设备。
- 对话框对应新增动作「全部重新连接」，走 `DisconnectAll` 的兄弟方法 `ReconnectAll`（只重连受影响的）。
- 保留 `serialDetached` 用于单台且非 ADB 服务重启的情况。

### 测试

- `TestAggregateOneDeviceStaysSerialDetached`
- `TestAggregateManyDevicesBecomesServerRestart`
- `TestAttentionAggregatedDialogOffersReconnectAll`（视图）

---

## P3 历史浏览界面

### 现状

历史库有三张表（`tasks` / `channel_logs` / `reachability`），但只在启动时读最近 50 条任务塞进任务面板，用户无法查"上周那次 pull 为什么失败"。

### 设计

- 新路由 `/history`（从列表页工具栏进入，带面包屑「我的设备 / 历史」）。
- 顶部筛选：设备下拉（`components.Select`）、状态分段（全部/成功/失败/取消）、时间范围（今日 / 7 天 / 30 天 / 全部）、关键字输入（匹配标题与输出）。
- 列表：设备、标题、开始时间、耗时、状态；点开单条 → 详情面板显示输出全文（`Scroll` + 等宽字体）。
- 操作：清空历史（二次确认）、导出当前筛选为 `.txt`（`mygo.Dialog.Save`）。
- `Recorder` 扩展（**保持接口小**）：
  ```go
  type TaskQuery struct {
      DeviceID *uuid.UUID
      State    TaskState   // 空表示全部
      Since    time.Time
      Search   string
      Limit    int
  }
  type HistoryReader interface {
      QueryTasks(TaskQuery) ([]Task, error)
      QueryLogs(deviceID uuid.UUID, since time.Time, limit int) ([]SessionLog, error)
  }
  ```
  `core` 只依赖 `HistoryReader`（可选接口，nil 表示不支持浏览），`history` 包实现；SQLite 侧补索引：
  ```sql
  CREATE INDEX IF NOT EXISTS idx_tasks_started ON tasks(started_at DESC);
  CREATE INDEX IF NOT EXISTS idx_tasks_device ON tasks(device_id, started_at DESC);
  ```
  已有库用 `CREATE INDEX IF NOT EXISTS` 兼容，不需要迁移版本号。

### 测试

- `history`：`TestQueryTasksFiltersByDeviceStateAndText`、`TestQueryTasksOrdersNewestFirst`、`TestLogsQueryLimit`。
- 视图：`TestHistoryPageShowsRowsAndEmptyState`、`TestHistoryFilterCallsQuery`（用假 reader 断言查询参数）。

### 风险

输出可能很大（每条上限 32 KiB，100 条就是 3 MB）——列表查询**不带 output 字段**，详情按 id 单独取。

---

## P5 托盘「待处理」提示

### 设计

- `Tray.SetTitle("2")`（macOS 菜单栏文字）+ `SetToolTip("Tailcat Manager · 2 项待处理")`（全平台）。
- Windows/Linux 没有角标能力：改为在菜单首项插入 `⚠ 2 项待处理`（点击唤起窗口并弹对话框），并在托盘图标无法换图时至少保证 ToolTip 正确。
- `keepTrayFresh` 已有：在重建菜单时顺便更新 Title/ToolTip，注意 `SetTitle` 只在数量变化时调用，避免闪烁。

### 测试

- `TestTrayTitleShowsPendingCount`：伪造 2 条 Attention → 断言菜单首项文本与 ToolTip。
- 视图之外的托盘无法在 `ui.Tester` 里测：隔离成纯函数 `trayLabel(attention []core.Attention) (title, tooltip string, menuLabel string)` 再测。

---

## 提交与文档约定

- 每个 P 一个或多个 `feat:`/`fix:`/`chore:` 提交，**同提交更新 `docs/CHANGELOG.md`**。
- P1 完成后更新 `docs/build-notes.md`（打包与体积）、`README.md`（安装段落）。
- P2/P3/P4 的界面细节补进 `docs/ui-and-console.md`（新章节或对应小节）。
- 决策（如"历史查询不带输出"、"聚合只在同一轮内判定"）在 `docs/architecture.md` 的决策记录表补一行，注明代价。

## 不在本轮范围

- 自动更新（`mygo` 的 updates 通道）——需要发布托管与签名链，等有稳定渠道再说。
- 多语言 i18n（当前固定 zh-Hans）。
- CLI 模式、多窗口、Windows Job Object。
- 云端同步或团队共享设备列表。
