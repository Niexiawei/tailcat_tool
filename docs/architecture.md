# Tailcat Manager（Go 版）技术文档

> 本文档描述当前 `feat/go-rewrite` 分支的实现。行为基线是 `tailcat-tool-swift/`（Swift v0.1.9，commit `29ff494`），那份代码作为独立 git 仓库保留在本目录下、对主仓库 git-ignore。
>
> 规模：生产代码 53 个文件 / 6657 行，测试 22 个文件 / 3823 行，127 个用例。

## 目录

1. [背景与目标](#1-背景与目标)
2. [总体架构](#2-总体架构)
3. [目录结构](#3-目录结构)
4. [核心接口](#4-核心接口)
5. [数据与持久化](#5-数据与持久化)
6. [会话状态机](#6-会话状态机)
7. [两种连接方式](#7-两种连接方式)
8. [外部干预检测](#8-外部干预检测)
9. [设备操作与任务](#9-设备操作与任务)
10. [平台适配](#10-平台适配)
11. [界面层](#11-界面层)
12. [安全设计](#12-安全设计)
13. [测试策略](#13-测试策略)
14. [构建、体积与依赖](#14-构建体积与依赖)
15. [设计决策记录](#15-设计决策记录)
16. [已知缺口与后续工作](#16-已知缺口与后续工作)
17. [关键文件索引](#17-关键文件索引)

---

## 1. 背景与目标

原项目是 SwiftUI 应用，只覆盖 macOS 13+ / arm64，并且把 `tailcat` 当外部命令行使用：每台设备起一条 `tailcat forward` 子进程，从 stderr 里正则匹配 `# forwarding 127.0.0.1:PORT -> remote localhost:5555` 来判断就绪，靠 `socat` 桥配合一个独立的 `tailcat-adb` CLI 工具工作。

Go 版的两条主线：

1. **跨平台统一**：macOS（arm64/amd64）、Windows（amd64）、Linux（amd64）一套代码、一套功能，纯 Go、无 cgo，任意机器可交叉编译。
2. **把传输层变成库**：`github.com/tailscale/tailcat` 本身就是库（CLI 只是它的一层壳）。内嵌之后，外部二进制、stderr 解析、端口就绪竞态、内联 DERP 回环服务、socat 桥、CLI 会话识别这一整条链路全部消失。

用户只需安装 **`adb`（必需）** 和 **`scrcpy`（可选）**。

### 非目标

- tailcat **server** 侧（监听端）功能；本应用只做客户端/转发端。
- 外部部署、内嵌视频/终端、云同步、应用内自动更新。
- 识别或接管 `tailcat-adb` CLI 自己创建的 socat 桥（该功能已随 CLI 模式一起移除）。
- 从 Swift 版迁移数据（用户明确不需要）。

---

## 2. 总体架构

三层，依赖方向严格单向：

```
cmd/tailcat-manager   →   internal/app   →   internal/core   →   internal/platform
                                              ↘ tailcat（仅 4 个文件）
                                              ↘ internal/credstore（凭据库适配）
```

- **`internal/core`**：平台无关业务。会话状态机、转发、探测、身份、DERP 归一化、配置、脱敏、任务、干预检测、历史接口。**不 import mygo、onevra，也不 import app**，因此全部可无图形环境单测。
- **`internal/platform`**：每个 OS 唯一实现。工具发现、默认目录、进程树终止、文件锁、包管理器、终端、唤醒。**不 import core**（core 定义接口，platform 提供能力），避免循环依赖。
- **`internal/app`**：mygo + onevra 视图层。只读 core 快照、只调 core 方法。
- **`internal/credstore` / `internal/history`**：两个"适配器"包，分别封装系统凭据库与 SQLite，core 只认接口。

### 并发模型

Swift 用 `@MainActor` + `ObservableObject`。Go 版：

```go
// core：所有公开方法可并发调用，状态由一把互斥锁保护
type Manager struct {
    mu      sync.Mutex
    changed chan struct{} // 容量 1，自动合并通知
    ...
}
func (m *Manager) notify() { select { case m.changed <- struct{}{}: default: } }

// app：单一消费循环，把通知变成一次重绘
go func() {
    for range manager.Changed() {
        mygo.RunOnMain(func() { window.Invalidate() })
    }
}()
```

`ui.Context.Invalidate()` / `Window.Invalidate` 官方保证可从任意 goroutine 调用，因此不需要观察者框架。

**generation token**：每次连接/断开都生成新的 `uuid`，写进 `m.generations[id]`；所有异步回调（监视循环、任务收尾、日志回调、探测结果）落状态前都先 `isCurrentLocked(id, gen)`，否则丢弃。这取代了 Swift 的 `Task.cancel` + generation 双保险。

---

## 3. 目录结构

```
cmd/tailcat-manager/
  main.go              启动、单实例、配置装配、--demo
  resources/tray.png   托盘模板图标（32×32）
internal/app/          视图层
  app.go        App 生命周期、安装、终端、托盘刷新
  state.go      ViewModel：侧栏、状态点、工具健康、编辑器请求
  shell.go      窗口（onevra sidebar shell）+ 重绘桥
  sidebar.go    （并入 state.go）
  detail.go     工具栏、概览 9 行、日志页
  actions.go    复制、提升临时设备、Shell、APK、上传、下载、终端
  editor.go     设备表单 + 校验 + 连接方式开关
  transfer.go   上传/下载表单 + 远端路径校验
  settings.go   工具设置
  reachability.go 批量连通测试表
  attention.go  外部干预对话框
  tasks.go      任务面板
  tray.go       托盘菜单（随状态重建）
  demo.go       --demo 假环境（假 Runner / 假 Forwarder）
  strings/文案   集中在各视图 + core/messages.go
internal/core/
  model.go      8 态连接状态、4 态任务状态、中文标签
  session.go    Session / SessionLog / Profile / RelayPreset
  task.go       Task
  err.go        Error（面向用户的错误）、ErrCancelled、ErrUnsupported
  messages.go   文案常量（脱敏占位、超长行截断）
  redact.go     Plausible / Redact / Redactor
  config.go     Configuration + ConfigStore（原子写、权限、反符号链接）
  credential.go CredentialStore + 内存实现 + 缓存装饰器 + 长度上限
  tools.go      ToolComponent / ToolSettings / 工具路径解析
  command.go    Command / Result / Runner 接口
  runner.go     ExecRunner（双管道、UTF-8 边界、尾部 1 MiB、超时/取消）
  relay.go      DERP 归一化 → *tailcfg.DERPMap
  identity.go   设备身份（与 tailcat `--key=` 同格式）
  forwarder.go  转发器（tailcat.Client + net.Listener + ProxyConns）
  pinger.go     链路探测（DiscoPing）
  reachability.go 批量探测（3 并发）
  installer.go  adb/scrcpy 安装（brew/winget/apt/dnf/pacman）
  history.go    Recorder 接口 + Task/Log/Reachability 记录
  attention.go  外部干预规则 + needsAction
  manager.go    状态机总控（连接/断开/监视/重试/Shutdown）
  manager_devices.go 设备与预设的增删改（含直连设备）
  manager_tasks.go   设备操作、任务、诊断、设备信息
internal/platform/
  paths_{unix,windows}.go      配置目录、默认路径
  toolfind_{unix,windows}.go   adb/scrcpy/brew/winget 查找
  kill_{unix,windows}.go       进程组/进程树终止、存活检测
  lock_{unix,windows}.go       身份文件独占锁
  pkg_{darwin,linux,windows}.go 包管理器探测
  terminal_{darwin,linux,windows}.go 终端
internal/credstore/store.go    mygo.Secrets → CredentialStore
internal/history/sqlite.go     modernc.org/sqlite → Recorder
internal/compat/compat_test.go 第三方 API 契约（编译期断言）
```

---

## 4. 核心接口

全部定义在 `internal/core`，实现分散在各层，测试注入假实现。

```go
// 外部进程（只剩 adb / scrcpy）
type Runner interface {
    Start(ctx context.Context, c Command, onOutput func(string)) (*Proc, error)
    Run(ctx context.Context, c Command, onOutput func(string)) (Result, error)
}

// 设备通道（生产实现是 Forwarder，测试注入假实现）
type ChannelOpener interface {
    Open(ctx context.Context, req ForwardRequest, onLog func(string)) (*ForwardConn, error)
}

// 链路探测（Pinger 实现；Reachability 的 Prober 与之一致）
type Prober interface {
    Ping(ctx context.Context, deviceID uuid.UUID, address string, seconds int, relay RelayConfig) (PingOutcome, error)
}

// 凭据
type CredentialStore interface {
    Read(id uuid.UUID) (string, bool, error)
    Write(address string, id uuid.UUID) error
    Delete(id uuid.UUID) error
}

// 身份（IdentityStore 实现）
type IdentityProvider interface {
    Identity(ctx context.Context, name string) (Identity, error)
    NodeKey(ctx context.Context, name string) (key.NodePrivate, error)
}

// 历史（nil 表示只留内存）
type Recorder interface {
    RecordTask(TaskRecord) error
    RecordLog(LogRecord) error
    RecordReachability([]ReachabilityRecord) error
    RecentTasks(limit int) ([]Task, error)
    Close() error
}
```

`tailcat` 的 import 只允许出现在 `forwarder.go`、`pinger.go`、`identity.go`、`relay.go` 四个文件里，其余代码面对窄接口，方便替换或打补丁。

### tailcat 库用法对照

| 原 CLI 调用 | Go 库等价物 |
|---|---|
| `tailcat parse <addr>` | `tailcat.ParseAddr(addr)` |
| `genkey --client --key=<file>` | `tailcat.NewPrivateKey()` + `json.MarshalIndent(priv,"","\t")`（0600，格式逐字节一致） |
| `--key=<file> printpub` | `json.Unmarshal` → `priv.Private.Public().String()`（`nodekey:<64 hex>`） |
| `forward --bind=127.0.0.1 <addr> <local>:<remote>` | `net.Listen` + `&tailcat.Client{Server,Key,Logf}` + `DialTCPPort` + `tailcat.ProxyConns` |
| `ping --timeout=5s <addr>` | `cl.DiscoPing(ctx)` → `ipnstate.PingResult{LatencySeconds, Endpoint, DERPRegionCode}` |
| `--derpmap-url=<url>` / 内联 JSON 回环服务 | `addr.Resolve(ctx, *tailcfg.DERPMap)`，直接吃内存 map |

---

## 5. 数据与持久化

四块数据，四套语义，**永不混在一个文件里**：

| 数据 | 位置 | 权限 | 说明 |
|---|---|---|---|
| 设备配置、DERP 预设 | `<配置目录>/configuration.json` | 0700 / 0600 | 原子写（临时文件 + rename），pretty 2 空格，`profiles → tools → relayPresets` 字段顺序稳定；拒绝符号链接 |
| `tc...` 连接地址 | 系统凭据库 | 由系统管理 | macOS Keychain / Windows Credential Manager / Linux Secret Service；上限 2560 字节（Windows 的最小上限，全平台统一） |
| 设备身份私钥 | `<配置目录>/Identities/<名字>.private.json` | 0700 / 0600 | 名字形如 `<uuid>-connection` / `<uuid>-probe`；格式与 `tailcat --key=` 完全一致 |
| 历史（任务/日志/连通测试） | `<配置目录>/history.db` | 0600 | SQLite，纯 Go 驱动；删掉即清空历史，不影响使用。**写入绝不在管理器锁内进行**：通道日志先进内存队列（容量 256，满则丢弃），由独立协程写库；历史库自己的提交队列满时也直接跳过。界面每帧都要读管理器锁，任何写库都可能在磁盘繁忙时冻结窗口 |

`直连（本地 ADB）` 设备的目标地址（IP:端口 或 USB 序列号）存在配置里——**它不是凭据**；凭据库只放 `tc...`。

配置目录：

| 平台 | 路径 |
|---|---|
| macOS | `~/Library/Application Support/TailcatManager` |
| Linux | `${XDG_CONFIG_HOME:-~/.config}/TailcatManager` |
| Windows | `%AppData%\TailcatManager` |

未知字段会被忽略而不是拒绝（`TestConfigIgnoresUnknownKeys`），所以以后加/删字段不会让旧文件读不动。

`history.db` 三张表：

```sql
tasks(id, device_id, device_name, serial, title, started_at, ended_at, state, output)
channel_logs(device_id, at, text)
reachability(device_id, name, state, latency, route, detail, checked_at)
```

---

## 6. 会话状态机

8 个状态（Swift 是 7 个，新增 `needsAction`）：

| 值 | 中文标签 | Busy |
|---|---|---|
| `disconnected` | 未连接 | |
| `connecting` | 正在连接 | ✓ |
| `online` | ADB 已连接 | |
| `offline` | ADB 离线 | |
| `unauthorized` | ADB 待授权 | |
| `disconnecting` | 正在断开 | ✓ |
| `failed` | 连接失败 | |
| `needsAction` | 等待处理 | |

```
                  connect()
disconnected ────────────────► connecting ──listener ok + adb device──► online
     ▲                              │                                   │
     │                              ├─ adb unauthorized ──► unauthorized│
     │                              ├─ 连接异常 ──────────► failed      │
     │                              ▼                                   ▼
     └──── disconnecting ◄──── disconnect() 任意态          get-state≠device ──► offline
                                                                             │ 6 次退避重试
                                                                             ▼
                                                                          failed
online/offline ──外部干预──► needsAction ──用户选择重连/改端口──► connecting
                                 └──── 稍后 ────► needsAction（保持）
```

不变式：

1. 连接、断开、诊断、任务都绑定 generation；过期回调一律丢弃。
2. 断开顺序：**关监听器 → 关活跃连接 → `Client.Close()` → `adb disconnect`**。先把本地入口关掉，才不会有新连接在 `adb disconnect` 之后又接进来。
3. **只有 `offline` 会重试**，`unauthorized` 和 `needsAction` 都不重试——后者尤其重要，否则用户刚 `adb disconnect` 又会被偷偷连回去。
4. 重试退避 2/4/8/16/30/30 秒，第 6 次后提示"请检查设备后重新连接"。
5. `Connect`/`Disconnect` 是**同步**方法，界面在 goroutine 里调；渲染线程绝不直接调。

---

## 7. 两种连接方式

### 7.1 通过 Tailcat 转发（默认）

```
表单：tc... 地址 + 本地端口(可空) + 远端 ADB 端口 + DERP(默认/预设/自定义)
```

流程：校验地址 → 取/建 `connection` 身份 → 绑定本地端口（固定端口或 5556–5655）→ 建 `tailcat.Client` → `adb connect 127.0.0.1:<port>` → 轮询 `get-state` → 启动监视。

端口绑定成功即就绪，**没有 5 秒等待，也不解析 stderr**。

### 7.2 本地 ADB 直连

```go
DirectADB    bool   `json:"directADB,omitempty"`
DirectTarget string `json:"directTarget,omitempty"` // "192.168.1.20:5555" 或 "emulator-5554"
```

- 不建 Tailcat 通道、不加载身份、不碰 DERP（`forwarder.Open` 一次都不调用），**也不读凭据库**：`Connect` 在直连时不解析 Tailcat 地址，否则会以"没有有效连接凭据"提前失败。
- `RemotePort` 对直连无意义，保存为 0；加载时若发现"有 `directTarget` 但没有 `directADB`"（旧数据或半途保存），自动按直连处理并把残留字段归零。
- 目标含 `:` → 视为网络设备，先 `adb connect <target>`；否则视为 USB/模拟器序列号，直接当 serial 用。
- 之后的监视、退避、任务、干预检测、断开清理全部复用同一套代码——`runtimeState` 里 `conn` 可为 nil，serial 独立存放。
- 断开时，网络目标会 `adb disconnect`，USB 序列号不会（它从没被 "connect"）。

表单里是两态 radio（`● 通过 Tailcat 转发` / `○ 本地 ADB 直连`），默认 Tailcat。

校验两侧一致：空值、"含空格或换行"、以及**把 `tc...` 填进直连框**都会被拒并给出对应提示。

---

## 8. 外部干预检测

Swift 版对"用户在本机 adb 里动了我们的连接"是无感的：它只会看到 `get-state` 失败，然后默默重试、甚至替你连回去。Go 版把它变成显式待办。

### 规则（纯函数 `DetectInterference`）

| 规则 | 条件 | 含义 |
|---|---|---|
| `serialDetached` | listener 活着、本会话 serial 不在 `adb devices -l` 里 | 用户执行了 `adb disconnect 127.0.0.1:P` |
| `portChanged` | serial 不在表里，但表里出现另一个 `127.0.0.1:P2`，其 `model` + `device` 与会话记录一致 | 同一台设备被别的程序映射到了新端口（P2 只作线索展示，**不接管**） |
| `portDrift` | 当前监听端口 ≠ 配置的 `preferredPort` | 我们自己的入口端口变了 |
| 不报 | `offline` / `unauthorized`，或拿不到设备身份 | 设备侧问题，走原有退避 |

`portChanged` 必须设备身份匹配才报——两台同型号车机同时在线时，宁可少报也不能把 A 的端口当成 B 的。

### 命中后

1. 停止该设备的自动重试与监视重连，状态置 `needsAction`。
2. 记一条 `Attention{DeviceID, Kind, Serial, DetectedPort, Detail, DetectedAt}`；同类事件只留一条，Ack 之前不重复弹。
3. 事件消失（serial 回来且 `state=device`）自动清除。

### 对话框

- 窗口**可见且聚焦** → 当场弹队首一条。
- 窗口隐藏/失焦 → 不抢焦点，只排队 + 托盘该项加 `Badge`。
- `Window.OnFocus` 与系统唤醒都会泵一次队列；多台设备一次只弹一条。
- 三个动作：**重新连接**（沿用原端口策略）/ **改用其他本地端口**（`SwitchPort`，可选记住为新偏好）/ **稍后处理**（保持 `needsAction`，侧栏可点开重弹）。

---

## 9. 设备操作与任务

任务（`Task`）＝一条后台 adb/scrcpy 命令。启动时**快照** serial + adb 路径 + generation，所以切换侧栏、甚至重连都不会让老任务打到新设备上：

- 启动前校验一次目标（不满足就拒绝，不产生任务）；真正执行前再校验一次（`get-state` 必须 `device`）。
- 输出经 `Redactor` 去凭据，尾部保留 32 KiB；历史保留最近 100 条（只裁已完成的，运行中的永不裁）。
- 重连/断开/删除设备会取消并**等待**该设备正在跑的任务（每个任务有 done channel）。
- 屏幕控制（scrcpy）：`--max-size=1280 --video-bit-rate=4M --max-fps=30`，`ADB=<adb 路径>`，超时 24 小时。
- 支持的入口：安装 APK、上传、下载（先选本地目录）、Shell 单条命令、终端、屏幕控制、链路探测、刷新设备信息（5 个 getprop 字段）。

---

## 10. 平台适配

| 能力 | macOS | Windows | Linux |
|---|---|---|---|
| 凭据库 | Keychain | Credential Manager | Secret Service（不可用则内存 + 启动提示） |
| 配置目录 | `~/Library/Application Support/TailcatManager` | `%AppData%\TailcatManager` | `${XDG_CONFIG_HOME:-~/.config}/TailcatManager` |
| adb 发现 | `/opt/homebrew/bin`、`/usr/local/bin`、`~/.local/bin`、Android SDK、PATH | PATH+PATHEXT、`%LOCALAPPDATA%\Android\Sdk\platform-tools`、scoop/`chocolatey`/winget links | unix 列表 + `/snap/bin`、`/usr/lib/android-sdk/platform-tools` |
| 组件安装 | brew（adb → `install --cask android-platform-tools`） | winget（`Google.PlatformTools`）；scrcpy 无包 → 提示手动 | apt / dnf / pacman |
| 进程终止 | 进程组 SIGTERM → 2s → SIGKILL | `taskkill /T /F` | 同 macOS |
| 终端 | Terminal.app + 临时 `.command` | `cmd.exe /c start` 新窗口 | x-terminal-emulator → gnome-terminal → konsole → … |
| 单实例 | `mygo.App.RequestSingleInstanceLock` + `OnSecondInstance` | 同 | 同 |
| 唤醒 | 轮询兜底 | 轮询兜底 | 轮询兜底 |

---

## 11. 界面层

- 窗口：`onevra/source/shells/sidebar`（可折叠侧栏 + 内容区），主题 `theme.Onevra`。
- 侧栏用满 shell 的三个槽位：`Left` = `HeaderHeight` 上内边距 + `ui.Scroll` + `components.Accordion`（我的设备 / 临时设备，标题带数量）；`LeftToolbar` = 固定底栏（通道数、工具健康、新建临时连接）；`Content` 同样以 `HeaderHeight` 起头。行式元素一律 `FillWidth()`——`Fill()` 会同时占满高度。
- 内容区分页由 `ui.Router` 驱动：`/devices` 是设备列表页，`/devices/{id}` 是设备页（顶部 `components.Breadcrumbs`），侧栏点击与面包屑都是 `Router.Push`，路由是选中状态的来源——进入某页即选中该设备。详情页的编辑入口与终端页都在设备页上。
- 侧栏不参与路由，只负责导航；设备列表页显示无选中设备。
- 顶部窗口操作只在列表页出现；设备页只保留设备自身的动作。
- 终端页用 `github.com/egoist/mygo/plugins/terminal`（libghostty-vt，运行期加载，无 cgo）。两种模式：**设备 Shell** 已连接时命令是 `adb -s <serial> shell`；**ADB 命令**按行执行任意 adb 子命令，走 `Manager.RunADB`（复用任务机制：超时、脱敏、限流、历史）。未连接时两种模式都挂一个 `lineConsole`（内存 `io.ReadWriteCloser`），它在终端里打印「设备未连接」、本地回显输入、回车时不执行而是打印原因并弹错误提示。
控制台按「设备 + 连接状态 + 模式」缓存；页面卸载（`Element.OnUnmount`）或程序结束（`Terminal.Done`）时关闭并重建，避免出现 `[Process exited]` 的死终端。
- 布局槽位、页面路由、内嵌终端、提示机制、线程与锁的细节见 [`ui-and-console.md`](ui-and-console.md)。
- 视图是**状态的函数**：`ViewModel` 持有选择、表单草稿、对话框开关，管理器是唯一事实来源；`Changed()` → `Invalidate()`。
- 组件映射：`components.Text/Button/TextBox/Switch/Tabs/Table/Dialog/Icon`、`lucide` 图标、`ui.Column/Row/ButtonBase`。
- 弹窗（编辑设备、上传下载、工具设置、连通测试、外部干预）用 `components.Dialog` 内联渲染，不是独立系统窗口。
- 侧栏底部常驻工具健康：`adb` 不可用会给「安装」按钮；`scrcpy` 标为可选。
- 托盘菜单随状态重建：每台设备一个子菜单（连接/重连、断开、关闭临时设备）+ 断开全部 + 退出；关闭主窗口只隐藏，连接继续。
- `--demo`：假 Runner（不执行命令）+ 假 Forwarder（直接给出端口），不读配置、不碰凭据库，可完整走通界面与任务面板。

---

## 12. 安全设计

| 关注点 | 做法 |
|---|---|
| 凭据不外泄 | `tc...` 只存系统凭据库；配置文件、日志、任务输出、tailcat `Logf` 全部经 `Redact`/`Redactor`；脱敏按行缓冲，**跨读切断的凭据也不会漏半截**；单行超 32 KiB 整行丢弃（避免把凭据尾巴当普通文本显示） |
| 文件权限 | 配置目录 0700、文件 0600；身份目录 0700、私钥 0600；每次读取后重新 chmod 修正 |
| 符号链接 | 配置目录/文件、身份目录/文件、地址簿根目录全部拒绝跟随符号链接 |
| 身份不可静默更换 | 文件存在即复用；损坏 → 报错并保留原文件；生成走 `O_CREATE|O_EXCL`；同名额文件锁（flock / LockFileEx）保证并发只生成一次 |
| 私钥不进日志 | 只记录身份文件路径与 `nodekey:` 公钥；私钥仅在进程内存中给 tailcat 客户端使用 |
| 命令参数 | 不拼 shell 字符串；adb/scrcpy 一律 argv 传参；终端用单引号转义 |
| 历史库 | 只存任务/日志/连通结果，绝不含 `tc...`、私钥；库文件 0600；删库无副作用 |

---

## 13. 测试策略

```
gofmt -l .                    # 必须无输出
go vet ./...
go test -race ./...
go test ./internal/compat/    # 第三方 API 契约
```

- **core**：全部依赖注入假实现（假 `Runner`、假 `ChannelOpener`、假 `Prober`、假 dialer），不启真进程、不连网、不碰凭据库。`-race` 干净。
- **进程层**：用**重新执行测试二进制**的方式（`TestMain` + `CORE_RUNNER_HELPER` 环境变量）测真实管道、真实退出码、真实信号——覆盖双管道合并、UTF-8 跨读切断、1 MiB 尾部截断、超时、取消、进程树终止。
- **视图层**：mygo `ui.NewTester` 无窗口渲染，按文本查找并点击，断言"关键文本存在 + 关键按钮可点"。
- **平台层**：纯函数优先（安装参数、终端转义、`adb devices -l` 解析），需要真 OS 的用 `t.Skip` + 环境变量开关。
- **契约**：`internal/compat/compat_test.go` 用编译期断言锁住 mygo / onevra / tailcat 共 40 余个 API，上游改签名 CI 立刻红。

用例分布（127 个）：脱敏与模型、配置与凭据、身份与锁、进程执行、DERP、转发、探测、状态机、设备管理、任务、连通测试、安装器、干预检测、表单校验、视图。

---

## 14. 构建、体积与依赖

| 依赖 | 版本 | 说明 |
|---|---|---|
| Go | 1.27.3+ | mygo 与 tailcat 都要求 |
| `github.com/egoist/mygo` | v0.9.1 | 纯 Go GUI（webview 或原生 UI） |
| `github.com/oneo-me/onevra/source` | v0.2026.18 | 其 go.mod 钉 mygo v0.7.4，实测 v0.9.1 可用 |
| `github.com/tailscale/tailcat` | v0.7.0 | 传输层；`@latest` 即此版本 |
| `tailscale.com` | `v1.103.0-pre.…` | tailcat 的传递依赖（pre-release，已锁进 go.sum） |
| `modernc.org/sqlite` | v1.62.0 | 纯 Go SQLite，无 cgo |

体积（Windows/amd64）：

| 构建 | 体积 |
|---|---|
| 默认 | 46.9 MB |
| `-ldflags "-s -w"` | **32.5 MB** |
| 其中 tailcat 客户端面 | ≈ 28 MB（交付前） |
| 其中 SQLite 驱动 | ≈ 4 MB |

交叉编译耗时：首次 ~13 s/平台，增量 1–2 s。`CGO_ENABLED=0` 下 linux/amd64、darwin/arm64、darwin/amd64、windows/amd64 全部可构建。详见 [`build-notes.md`](build-notes.md)。

---

## 15. 设计决策记录

执行过程中做出的、偏离原计划或计划未覆盖的决定（每条都附带"如果错了"的代价）：

| 主题 | 决定 | 代价 |
|---|---|---|
| 传输层 | 用 tailcat 库而非外部进程 | 私钥进进程内存；体积 +28 MB |
| 兼容性 | 不做 Swift 数据迁移、不做 tailcat-adb 地址簿导入 | 老 CLI 用户需手输一次地址 |
| 状态数 | 7 → 8（新增 `needsAction`） | 侧栏多一种颜色/文案 |
| 连接调用 | `Connect`/`Disconnect` 同步，界面在 goroutine 调 | 渲染线程不可直接调 |
| 断开顺序 | 先关通道再 `adb disconnect` | 与 Swift 相反，未观测到问题 |
| 端口就绪 | 绑定成功即就绪，无 5 秒等待 | 无 |
| 进程终止 | Windows 用 `taskkill /T /F`，非 Job Object | 极少数抗 taskkill 的进程会活到重启 |
| 身份 | 进程内生成/读取，格式与 CLI 一致 | CLI 若改格式需转换器 |
| 表单呈现 | `components.Dialog` 内联，而非独立 sheet | 对话框不能拖出窗口 |
| 连接方式开关 | 两态按钮当 radio（未用 `components.Radio`） | 纯样式，可替换 |
| 直连目标存放 | 配置文件中（非凭据库） | 若把设备 IP 当秘密则写在 0600 文件里 |
| 历史库 | 独立 SQLite，配置/凭据/私钥不动 | 体积 +4 MB，多一个排障时要删的文件 |
| 历史写入 | 单写协程 + 队列，失败不影响主流程 | 极端情况下最后几条历史可能丢 |
| 干预检测 | 跑在监视循环里，而非独立 3 秒扫描 | 检测延迟 = 监视间隔 |
| 干预类型 | 只实现 3 种（`serialDetached`/`portChanged`/`portDrift`） | `adb kill-server` 会表现为多条提示 |
| 契约测试 | 断言放在 `_test.go` 里（零二进制体积） | 只由 `go test` 触发 |
| GPU 使用率 | 只读厂商 sysfs（kgsl/devfreq/MTK），读不到就标「不可用」，不做估算 | 多数设备 GPU 列显示「不可用」；逐进程 GPU 未实现 |
| 应用名与图标 | 用 `pm path` + `unzip -p` 只取 `AndroidManifest.xml`/`resources.arsc`/图标，`apkparser` 解析；没有 `unzip` 才回退整包；默认不解析，由用户按行或点刷新触发 | 自适应图标（XML 矢量）拿不到位图时退回首字母头像；依赖设备的 `unzip` |
| 应用信息缓存 | 7 天有效期 + 包名/apk 路径/size/mtime 键，过期仍显示旧值并后台重抓 | 应用原地更新后最多 7 天内仍显示旧名字（或用户手动刷新）；`appmeta/` 目录 100 条 ~64 MB 上限 |
| 采样范围 | 单次 `adb shell`（只用 shell 内建命令）输出全部进程 + `/proc/stat`，2 秒一轮；CPU/GPU 型号连上后只读一次；读数保留到断开连接 | 一次采样约 1 秒、几十 KB；页面隐藏即停止采样，但不清空读数 |

---

## 16. 已知缺口与后续工作

1. **真机联调未做**：ADB 实链路、scrcpy 窗口、设备授权流程、`adb disconnect` 干预弹窗，都只有自动化测试与 `--demo` 验证，**没有接过真设备**。这是当前最大的未知。
2. **发布打包未做**：没有 dmg / msi / deb 产物与签名，只有 `go build` + CI 矩阵。
3. **`adbServerRestarted` / `listenerLost`** 两种干预尚未实现（需要多设备关联与监听器健康探测）。
4. **配置读取失败**只打印一条提示，没有独立的启动错误页。
5. **历史只读展示**：任务面板会载入上次运行的任务，但还没有"按设备/时间筛选历史"的界面。
6. **托盘 Badge**：排队中的干预提示目前只会出现在托盘菜单文本里，没有数字角标。
7. **应用信息缓存管理页未做**：`core.AppMetaCache`（`Entries`/`Invalidate`/`Purge`）与磁盘缓存已就位，但还没有列出、删除、清空的界面。
8. **逐进程 GPU 未实现**：`GPUSample.PerProcessAvailable` 恒为 false，只有整机 GPU 使用率。

---

## 17. 关键文件索引

想快速理解某一块，按这个顺序读：

| 想了解 | 读这个 |
|---|---|
| 整体装配 | `cmd/tailcat-manager/main.go` |
| 状态机与监视循环 | `internal/core/manager.go` |
| 转发与端口 | `internal/core/forwarder.go` |
| 身份与锁 | `internal/core/identity.go`、`internal/platform/lock_*.go` |
| 探测 | `internal/core/pinger.go`、`internal/core/reachability.go` |
| 设备增删改（含直连） | `internal/core/manager_devices.go` |
| 任务与操作 | `internal/core/manager_tasks.go` |
| 外部干预 | `internal/core/attention.go`、`internal/app/attention.go` |
| 历史 | `internal/history/sqlite.go`、`internal/core/history.go` |
| 界面结构 | `internal/app/state.go`、`internal/app/detail.go` |
| 表单与校验 | `internal/app/editor.go`、`internal/app/transfer.go` |
| 第三方契约 | `internal/compat/compat_test.go` |
| 计划与执行账本 | `docs/superpowers/plans/2026-11-15-tailcat-manager-go-port.md`、`.superpowers/sdd/<plan>/progress.md`（git-ignore，本地保留） |
