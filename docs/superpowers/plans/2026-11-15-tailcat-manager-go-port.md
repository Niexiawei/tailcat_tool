# Tailcat Manager Go 重写（跨平台）实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 把 `tailcat-tool-swift`（SwiftUI + macOS 13/arm64 专用）的功能完整等价重写为一个 Go 单二进制桌面应用，在 macOS / Windows / Linux 上功能统一。

**架构：** 三层。`internal/core` 只放平台无关业务（会话状态机、转发、身份、探测、配置、脱敏），通过接口依赖外部能力；`internal/platform` 是每 OS 唯一实现（工具发现、进程树终止、监听端口枚举、终端、包管理器）；`internal/app` 是 mygo + onevra 视图层，只读 core 快照、只调 core 方法。**tailcat 以 Go 库内嵌**（`github.com/tailscale/tailcat`），不再有外部 `tailcat` 可执行文件、不再解析 stderr、不再需要 socat。core 用互斥锁 + generation token 保护状态，跨 goroutine 变更后通过 `ui.Context.Invalidate()` 触发重绘。

**技术栈：** Go 1.27.3+；GUI `github.com/egoist/mygo` v0.9.1（纯 Go、无 cgo）；组件 `github.com/oneo-me/onevra/source` v0.2026.18；传输 `github.com/tailscale/tailcat`（**跟随 upstream 最新，当前 v0.7.0**）；测试 `go test` + 表驱动 + 假 Runner（替换 Swift 的自制 `TailcatChecks`）。

**规格：** 本文件。源行为基线是 `tailcat-tool-swift/`（v0.1.9，commit `29ff494`），逐条对应见第 1 节。

---

## 全局约束

- Go 1.27.3 及以上；模块路径改为 `github.com/Niexiawei/tailcat-tool`（现在 `github.com/Niexiawei/tailcat_tool.git` 带 `.git` 后缀，必须修正）。
- 只用纯 Go 依赖，**不引入 cgo**；三平台都要能从 Windows 交叉编译（已验证 `CGO_ENABLED=0 GOOS=linux/darwin/windows` 均可构建 tailcat 客户端）。
- `internal/core` 不得 import `internal/app`、`mygo`、`onevra`；单测不需要图形环境。
- 所有外部进程（仅 `adb` / `scrcpy` / 可选终端）必须带超时与可取消；输出必须经凭据脱敏后才可进 UI/日志。
- 凭据只进系统凭据库（macOS Keychain / Windows Credential Manager / Linux Secret Service），配置文件**永不**包含 `tc...` 地址。
- 配置文件 0600、目录 0700（Windows 尽力而为），原子写入，拒绝跟随符号链接。
- **tailcat 跟随 upstream 最新版本**（当前 `v0.7.0`）；升级动作是单独一次提交 + 全量回归，因为库 API 未到 1.0。
- 面向用户的文案沿用 Swift 版中文措辞（状态名、错误、提示），集中在 `core/messages.go` + `app/strings.go`，测试逐字断言。
- 每个任务独立可测、独立 commit；提交信息用 `feat:` / `fix:` / `test:` / `chore:` 前缀。

## 审查重点（Review Focus）

1. **凭据泄漏到日志**：`tc...` 地址被拆到两次读取、或出现在 tailcat 的 `Logf` 输出里时，界面绝不能出现原文；期望整体替换为 `[连接凭据已隐藏]`，且不做成半个地址。
2. **ADB 操作打错设备**：操作开始后连接重连/断开/被 CLI 替换，旧任务绝不能落到新 serial 上；期望操作前二次校验 generation + `adb get-state`，不满足即拒绝并标记取消。
3. **端口串台与漂移误判**：两台设备并连、或上一会话的 `adb disconnect` 还在飞时，同一本地端口不能被复用；反过来，设备短暂 offline、或另一台型号相同的设备占着别的端口时，也不能误报成“你的入口被外部改了”。期望：端口在旧 serial 清理完成前保持占用；端口漂移只在抓到设备身份（`model:`/`device:`）匹配时才报，拿不到身份就不报。
4. **非 UTF-8 / 超长输出把 UI 卡死或乱码**：`adb`/`scrcpy` 输出在任意字节边界被切断、单行超 32 KiB 时，界面应显示完整可读文本且可取消，内存不无界增长。
5. **身份文件被覆盖**：身份文件损坏、权限不对、或两个 goroutine 同时初始化同一设备时，绝不允许静默换钥；期望报错并保留原文件。

---

## 1. Swift 版功能基线（逐条对应）

| # | 能力 | Swift 源 | Go 落点 | 变化 |
|---|---|---|---|---|
| 1 | 设备档案 `DeviceProfile`（UUID/名称/legacyUsername/preferredPort/remotePort/relaySource） | `TailcatCore/Models.swift` | `core/model.go` | 无 |
| 2 | 连接状态机 + 中文标签 | `Models.swift` | `core/model.go` | 7 → **8 态**（新增 `needsAction`"等待处理"） |
| 3 | 会话快照（serial/port/型号/代号/Android/序列号/Shell 权限/链路/日志） | `Models.swift` | `core/session.go` | `pid` 换成 `port` |
| 4 | 任务（运行/成功/失败/取消，输出末尾 32 KiB，历史 100 条） | `Models.swift` | `core/task.go` | 无 |
| 5 | 地址合法性 `^tc[A-Za-z0-9_-]{20,}$` + 日志脱敏 | `Models.swift` | `core/redact.go` | 无 |
| 6 | `OutputRedactor`：按行缓冲、跨读切断的凭据不泄漏、32768 字节截断 | `Models.swift` | `core/redact.go` | 无 |
| 7 | 凭据存储接口 + 进程内缓存（不落盘） | `Persistence.swift` | `core/credential.go` | `mygo.Secrets` 三平台 |
| 8 | 工具设置 + 路径发现 | `Persistence.swift` | `core/tools.go` + `platform/toolfind_*.go` | **只剩 adb / scrcpy** |
| 9 | 配置读写（0700/0600、原子写、pretty+sorted、反符号链接、`relayPresets` 缺省兼容） | `Persistence.swift` | `core/config.go` | Windows 权限尽力而为 |
| 10 | `tailcat-adb` 地址簿只读导入（XDG_STATE_HOME、文件名正则、≤8 KiB、跳过计数） | `Persistence.swift` | `core/legacybook.go` | Windows 默认目录不同 |
| 11 | 命令执行：独立 stdout/stderr 并发排空、UTF-8 边界、超时、取消、SIGTERM→SIGKILL | `Processes.swift` | `core/runner.go` + `platform/kill_*.go` | 只服务 adb/scrcpy |
| 12 | 转发就绪解析 `# forwarding 127.0.0.1:PORT -> remote localhost:REMOTE` | `Processes.swift` | — | **删除**：库直接返回 `net.Listener`，端口即时可知 |
| 13 | `TailcatTransport`：端口候选 5556–5655/固定、占用重试、5 s 就绪窗口 | `Processes.swift` | `core/forwarder.go` | 改为库：`net.Listen` + `Client.DialTCPPort` + `ProxyConns`；无 5 s 竞态 |
| 14 | DERP 归一化（URL 或 JSON、去尾逗号、`derpMap` 包装、Regions 1–128、节点 1–32、端口校验、≤128 KiB） | `TailcatRelay.swift` | `core/relay.go` | 校验保留，产出 `*tailcfg.DERPMap` 而非 JSON 文本 |
| 15 | 内联 DERP JSON 回环 HTTP 服务 | `TailcatRelay.swift` | — | **删除**：`Addr.Resolve(ctx, dm)` 直接吃内存 map |
| 16 | 每设备固定身份（connection/probe 两角色）、互斥、绝不换钥 | `ClientIdentity.swift` | `core/identity.go` | 库内 `tailcat.PrivateKey`，文件格式与 CLI 的 `--key=` 完全一致 |
| 17 | 探测串行化（同一 probe 身份不并发） | `ClientIdentity.swift` | `core/pinger.go` | 无 |
| 18 | CLI 外部会话识别（`bridges/*.pid`、`ps`、`lsof`、socat 命令行校验、`adb devices -l`） | `CLISessions.swift` | — | **本期完全不做**：只做 GUI 单通道，不存在“外部桥”这个概念 |
| 19 | 批量连通测试（并发 3、5 s 超时、取消、延迟/直连/中继） | `Reachability.swift` | `core/reachability.go` | `DiscoPing` 结构化字段，不再解析 `pong in … via …` |
| 20 | 组件安装（brew、关闭自动更新、1800 s、已有则直接配置、可取消） | `ComponentInstaller.swift` | `core/installer.go` + `platform/pkg_*.go` | **只剩 adb / scrcpy**（tailcat 不再是外部依赖） |
| 21 | 会话管理总控（连接/重连/断开、3 s 监视、离线重试 6 次退避、转发退出、临时设备、提升保存、导入、工具更新、预设、任务/日志、诊断、取消） | `SessionManager.swift` | `core/manager*.go` | 无 |
| 22 | 主窗口、侧栏分组、底部工具健康、详情概览/日志、任务面板、5 个表单、托盘、单实例、关闭保活、唤醒重检、`--demo` | `TailcatManager/*.swift` | `internal/app/*.go` | 托盘/终端/唤醒各自适配 |
| 23 | 凭据助手可执行文件（签名固定、JSON 协议 v1、父进程证书校验） | `TailcatCredentialHelper/main.swift` | **不移植** | 见 §4.3 |
| 24 | **【本期新增】外部 ADB 干预检测与"等待处理"对话框**（本地 `adb disconnect`、`adb kill-server`、设备换本地端口、listener 丢失） | —（Swift 无） | `core/attention.go` + `app/attention.go` | 见 §3.7、任务 16 |

---

## 2. 目标 / 非目标 / 反目标

**目标**

- macOS（arm64 + amd64）、Windows（amd64，后续 arm64）、Linux（amd64，X11/Wayland）单一代码库、功能统一。
- 行为与 Swift 版逐条等价（状态名、错误文案、超时值、并发上限、端口范围、重试次数）。
- 可直接读 Swift 版同一路径的 `configuration.json`；macOS 上可继续读已有 Keychain 条目与已生成的 `.private.json` 身份文件（不要求重新录入/重新授权设备）。
- 终端用户**只需**安装 `adb`（必需）和 `scrcpy`（可选），不再需要装 `tailcat` 命令行。
- **外部干预可感知**：用户在终端里 `adb disconnect`/`adb kill-server`、或设备被映射到新的本地端口时，GUI 能检测到，并在用户看向窗口（或唤醒后）用对话框让用户选择"重连"或"换本地端口"；绝不代替用户偷偷重连。
- core 全部自动化测试，UI 提供 `ui.NewTester` 视图测试与 `--demo` 冒烟。

**非目标（第一版不做）**

- tailcat **server** 侧（监听端）功能；我们只做客户端/转发端。
- **识别/接管 `tailcat-adb` 命令行自己创建的 socat 桥**（Swift 的"CLI 外部连接"功能）：本期不做——我们只做 GUI 自己的通道，不再起 socat，也就不再有"外部桥"这回事。
- 接管/杀掉其他进程的通道：检测到外部干预时，只提示与重建**自己的**通道，不动别人的进程。
- 远端部署、客户端密钥管理 UI、内嵌视频/终端、云同步、应用内自动更新。
- 崩溃/强杀后的孤儿进程自动清理（保留端口避让即可）。
- DERP 地图磁盘缓存（库默认是进程内内存缓存，够用）。

**反目标（技术上能做，但会算失败）**

- 为了"少一个依赖"回到 shell 调用 `tailcat` 二进制并解析 stderr。
- 为了跨平台牺牲凭据安全（把 `tc...` 明文写进配置文件）。
- 在 Windows/Linux 上假装支持 `tailcat-adb` CLI 会话识别（会误报/误操作）。
- 引入 cgo、Electron 式 webview 前端，或把 UI 退化成网页壳。
- 多设备共享同一个 node key（Swift 版刻意隔离，回退会造成 DERP 争用与身份冲突）。

---

## 3. 架构

### 3.1 目录结构

```
cmd/tailcat-manager/main.go        # 启动、单实例、托盘、窗口、退出清理
internal/app/
  state.go        # ViewModel：快照聚合、脏标记、Invalidate 桥
  strings.go      # 界面文案与格式化（单一来源）
  shell.go        # onevra sidebar shell 组装、页面路由、头部动作
  sidebar.go      # 分组设备列表、状态点、底部工具健康/安装摘要
  detail.go       # 设备详情：概览 9 行、操作区、Shell 命令、日志页
  tasks.go        # 任务历史（Collapsible + 取消）
  editor.go       # 设备/临时设备编辑、DERP 预设、保存到我的设备
  transfer.go     # 上传/下载表单
  settings.go     # 工具设置 + 安装进度
  reachability.go # 连通测试表
  tray.go         # 托盘菜单
  demo.go         # --demo 假环境
internal/core/
  model.go session.go task.go err.go messages.go
  redact.go credential.go config.go legacybook.go tools.go
  runner.go command.go                       # 仅 adb/scrcpy
  relay.go                                  # DERP 归一化 → *tailcfg.DERPMap
  identity.go pinger.go forwarder.go        # tailcat 库封装
  cliscan.go                                # 已取消（任务 13），不建此文件
  reachability.go installer.go
  manager.go manager_connect.go manager_devices.go manager_tasks.go
internal/platform/
  paths_{darwin,linux,windows}.go
  toolfind_{darwin,linux,windows}.go
  kill_{unix,windows}.go
  lock_{unix,windows}.go
  procs_{darwin,linux,windows}.go     # 本期不需要（任务 13 已取消）
  terminal_{darwin,linux,windows}.go
  pkg_{darwin,linux,windows}.go
  wake_{darwin,linux,windows}.go
  secrets.go                          # 薄封装 mygo.Secrets
internal/testutil/fake_runner.go fake_tailcat.go tempdir.go
```

### 3.2 依赖方向

```
cmd → app → core → platform
              ↘ tailcat（仅在 core/forwarder.go、core/pinger.go、core/identity.go、core/relay.go）
              ↘ testutil（仅测试）
```

`app` 不得直接 `exec.Command`；`core` 不得直接 `os.Getenv` 决定路径（走 `platform`）。**tailcat 的 import 只允许出现在上面四个文件**，其余代码面对我们自己的窄接口，方便将来换实现或打补丁。

### 3.3 tailcat 库用法（替代外部进程的全部要点）

| 原 CLI 用法 | Go 库等价物 |
|---|---|
| `tailcat parse <addr>` | `tailcat.ParseAddr(addr) (ConnInfo, error)`；`ParseAddrRaw` 给调试用 |
| `tailcat genkey --client --key=<file>` | `tailcat.NewPrivateKey()`（`Private key.NodePrivate` + `Public ConnInfo` 含 PSK），`json.MarshalIndent(priv, "", "\t")` 写 0600 —— **与 CLI 文件格式完全一致** |
| `tailcat --key=<file> printpub` | 读文件 → `json.Unmarshal` 到 `tailcat.PrivateKey` → `priv.Private.Public().String()`（`nodekey:<64 hex>`） |
| `tailcat --key=… --derpmap-url=… forward --bind=127.0.0.1 <addr> <local>:<remote>` | `net.Listen("tcp","127.0.0.1:<local>")` + `cl := &tailcat.Client{Server: addr, Key: priv.Private}`；accept 后 `cl.DialTCPPort(ctx, remote)` + `tailcat.ProxyConns(conn, remote)`；端口来自 `ln.Addr()` |
| `tailcat --key=… --derpmap-url=… ping --timeout=5s <addr>` | `cl.DiscoPing(ctx)` → `*ipnstate.PingResult{LatencySeconds, Endpoint, DERPRegionCode, DERPRegionID}` |
| `--derpmap-url=<url>`（自定义中继） | `addr.Resolve(ctx, dm)` 传内存 `*tailcfg.DERPMap`；官方默认则留空走 `Client.DERPMapURL` 默认值 |
| 内联 JSON 需本地回环 HTTP 服务 | **不需要**，`ConnInfo.Expand`/`Addr.Resolve` 接受 `*tailcfg.DERPMap` |
| 进程退出 = 通道结束 | `cl.Close()` + 关监听器 + 关活跃连接 |

`Client` 关键字段（v0.7.0，全部必须在首次使用前设置）：`Server Addr`、`Key key.NodePrivate`、`Logf logger.Logf`、`DERPMapURL string`、`DERPMapCache DERPMapCache`。`Client.Ping(ctx)` 只给延迟；`DiscoPing(ctx)` 才给直连/中继路径。

### 3.4 并发模型

Swift 用 `@MainActor` + `ObservableObject`；Go 版：

```go
type Manager struct {
    mu      sync.Mutex
    changed chan struct{}   // 容量 1，合并通知
    ...
}
func (m *Manager) notify() { select { case m.changed <- struct{}{}: default: } }

// app 侧：单一消费循环
go func() {
    for range m.Changed() { app.Post(func() { c.Invalidate() }) }
}()
```

每设备一条 `deviceJob{gen uuid.UUID, cancel context.CancelFunc, wg sync.WaitGroup}`；`isCurrent(id, gen)` 等价 Swift 的 generation 校验，所有异步收尾先校验再写状态。`ui.Context.Invalidate()` 官方保证可从任意 goroutine 调用。

### 3.5 会话状态机

```
                  connect()
disconnected ────────────────► connecting ──listener ok + adb device──► online
     ▲                              │                                   │
     │                              ├─ adb unauthorized ──► unauthorized│
     │                              ├─ 连接/Dial 异常 ────► failed       │
     │                              ▼                                   ▼
     └──── disconnecting ◄──── disconnect() 任意态          get-state≠device ──► offline
                                                                             │ 6 次退避重试
                                                                             ▼
                                                                          failed

online/offline ──外部干预（§3.7）──► needsAction ──用户选择重连/改端口──► connecting
                                        └──────用户选择稍后──────► needsAction（保持，可重开对话框）
```

不变式（与 Swift 同名同义）：连接、断开、诊断、任务都绑定 `generation`；断开顺序固定为 **关监听器 → 关活跃连接 → `adb disconnect` → `Client.Close()`**（Swift 是 `adb disconnect` 再杀进程；库模式下必须先把本地 listener 关掉，否则 `adb disconnect` 之后可能又接进新连接）；离线重试只对 `offline` 生效，`unauthorized` 不重试；**`needsAction` 也不重试**（不能把用户刚断开的东西又偷偷连上）。

### 3.6 core 关键接口（签名即契约）

```go
// ---- 外部进程（只剩 adb / scrcpy）----
type Runner interface {
    Start(ctx context.Context, c Command, onOutput func(string)) (*Proc, error)
    Run(ctx context.Context, c Command, onOutput func(string)) (Result, error)
}
type Command struct { Path string; Args, Env []string; Timeout time.Duration }
type Result  struct { ExitCode int; Output string; TimedOut, Cancelled bool }
type Proc    struct { PID int; Done <-chan int; Stop func() }

// ---- 凭据 ----
type CredentialStore interface {
    Read(id uuid.UUID) (string, bool, error)
    ReadForObservation(id uuid.UUID) (string, bool, error) // macOS 禁弹窗路径
    Write(address string, id uuid.UUID) error
    Delete(id uuid.UUID) error
}

// ---- 身份（库内，文件与 CLI 兼容）----
type Identity struct { File string; PublicKey string }
type IdentityProvider interface {
    // name = "<uuid>-connection" | "<uuid>-probe"
    Identity(ctx context.Context, name string) (Identity, error)
    NodeKey(ctx context.Context, name string) (key.NodePrivate, error) // forwarder/pinger 用
}

// ---- DERP ----
type RelayConfig struct {
    Kind  RelayKind        // RelayOfficial | RelayURL | RelayInline
    URL   string
    Map   *tailcfg.DERPMap // RelayInline 时非 nil
}

// ---- 转发（一条设备通道）----
type Forwarder interface {
    Open(ctx context.Context, req ForwardRequest, onLog func(string)) (*ForwardConn, error)
}
type ForwardRequest struct {
    Address       string
    PreferredPort int        // 0 = 从 5556 顺延
    RemotePort    int
    Identity      Identity
    Relay         RelayConfig
}
type ForwardConn struct {
    Listener net.Listener
    Port     int
    Serial   string          // "127.0.0.1:<port>"
    Close    func(ctx context.Context) error // 关 listener + 活跃连接 + Client.Close()
}

// ---- 探测 ----
type PingOutcome struct { Latency time.Duration; Direct bool; Route string }
type Pinger interface {
    Ping(ctx context.Context, deviceID uuid.UUID, address string, seconds int, relay RelayConfig) (PingOutcome, error)
}

// ---- 平台 ----
type Platform interface {
    FindTool(name string) (string, bool)
    DefaultAddressBookDir() string
    OpenTerminal(adbPath, serial string) error
    PackageManager() (name, path string, ok bool)
    InstallArgs(comp ToolComponent, pm string) []string
    OnWake(func()) (stop func())
    CredentialBackend() (name string, ok bool) // keychain / wincred / secret-service / none
}
```

### 3.7 外部 ADB 干预检测与待处理提示（本期新增）

Swift 版对“用户在本机 adb 里断开/改端口”是无感的：它只会看到 `get-state` 失败，然后默默重试 6 次、失败，甚至在你刚 `adb disconnect` 后又替你 `adb connect` 回去。Go 版把它变成一个**显式待办**。

**监视循环每 3 s 采一份快照**（比 Swift 多一次 `adb devices -l`，低频采样，每 3 个 tick 一次或状态非 online 时每 tick）：

| 输入 | 来源 |
|---|---|
| 本会话 serial / 端口 / 状态 / 上次设备身份（`deviceSerial`+`model`） | `Manager` 内存 |
| listener 是否仍活着 | accept 循环的 `alive` 原子位 + 一次 `dial 127.0.0.1:P` 探测 |
| `adb devices -l` 全量（serial → state/model/product/device） | `Runner` |

**判定规则**（纯函数 `DetectInterference(snapshot, listenerAlive, devices) []Attention`，按优先级）：

| 规则 | 条件 | 含义 | 建议动作 |
|---|---|---|---|
| `adbServerRestarted` | ≥2 个运行中会话的 serial 同时从表里消失 | 用户 `adb kill-server` / ADB 服务重启 | 全部重连 |
| `serialDetached` | listener 活着 && 本会话 serial 不在表里 && 表里没有同身份的新端口 | 用户在本机 `adb disconnect 127.0.0.1:P` | 重连 / 换端口 |
| `portDrift` | 当前 listener 端口 ≠ 该设备上次向用户确认/配置的 `preferredPort`（如自动重连时旧端口还没释放，挑了 5557） | **我们自己的**本地入口端口变了 | 切回原端口 / 记住新端口 |
| `portChanged` | 本会话 serial 不在表里 && 表里存在 `127.0.0.1:P2`（P2≠P）且其 `device`+`model` 与会话记录的设备身份一致 | 设备被映射到了新端口（很可能由其他程序建立） | 重连 / 换本地端口（P2 仅作为线索展示，**不接管**） |
| `listenerLost` | listener 不活了但 ADB 表里 serial 仍在且为 `device` | 我们自己的通道坏了 | 重连 |
| —（不报） | state 为 `offline`/`unauthorized`，或拿不到设备身份 | 设备侧问题 / 信息不足 | 走原有 6 次退避（前两者）或只报 `serialDetached` |

`portDrift` 和 `portChanged` 是两回事，都要实现：前者是“我们自己的端口变了”（不依赖设备身份，只看自己的记录），后者是“别的程序把同一台设备映射到了另一个端口”（必须身份匹配）。两者最终都导向同一个对话框、同一组动作。

注：`portChanged` 要求身份匹配（`model:` + `device:`），否则**不报**——同型号两台设备并存时宁可少报，也不能把 A 的端口当成 B 的。拿不到身份（从未 online 过）时退化为 `serialDetached`。

**命中后的状态处置**：

1. 立即停该设备的自动重试与监视重连，状态置 `needsAction`（新增第 8 个状态，中文标签"等待处理"，橙色点，`Busy()==false`）。
2. 写一条 `Attention{DeviceID, Kind, Serial, DetectedPort, Detail, DetectedAt}`；**同一设备同一种类只保留一条**，`DetectedAt` 不刷新（用于显示“多久前发现的”）。
3. 事件消失（serial 回来且 `state=device`，或用户已完成动作）→ 自动清除，状态回归。

**对话框触发时机（用户要求的“唤醒后提示”）**：

- 窗口**可见且聚焦**（`Window.IsVisible() && focused`）→ 当场弹队首一条。
- 窗口隐藏/失焦/在托盘后面 → **不抢焦点**，只排队；同时在托赘菜单该设备项上加 `Badge: "待处理"`，并发一条系统通知（`mygo.Notification{Title:"设备需要处理", Body:"E245：本地 ADB 入口已被移除"}`，点击 `OnNotificationClick` → `Show()`+`Focus()`，随即弹框）。
- `Window.OnFocus` 与 `platform.OnWake`（系统唤醒）都触发一次“泵出队列”；多条设备排队时一次只弹一条，处理完自动下一条。

**对话框内容**（`components.Dialog`）：

```
设备需要处理
E245 · 示例
本地 ADB 入口 127.0.0.1:5556 已被外部移除                    ← 或：该设备已在 127.0.0.1:5557 出现（可能由其他程序映射）
                                                              ← 或：本机入口从 127.0.0.1:5556 变成了 127.0.0.1:5557

[ 重新连接 ]   [ 切换本地端口… ]   [ 稍后处理 ]
```

- **重新连接**：`Disconnect` → `Connect`，沿用原端口策略（固定端口继续用固定端口，自动端口从 5556 重新分配）。不会去碰用户刚断开的东西——`Disconnect` 阶段如果 serial 已不在 ADB 表里，就跳过 `adb disconnect`。
- **切换本地端口…**：展开选择器（`自动` / 当前端口 / 自定义），勾选「记住此端口」→ 写回 `profile.preferredPort` 并持久化（失败回滚）；然后 `Disconnect`→`Connect(newPort)`。候选端口用 `net.Listen` 实测空闲（与转发器同一套逻辑，不另写一套）。**不把 P2 当作可选项**：如果 P2 是别的程序建的，我们不能接管它（既不能保证它的生命周期，也不应该去动），只在文案里当线索提示；如果用户真想把我们固定到 P2，就先去关掉那个程序，再在这里选 P2——`net.Listen` 会诚实报占用/成功。
- **稍后处理**：关框，设备保持`等待处理`，侧栏行可点击重开对话框；托赘 `Badge` 保留。

**防打扰**：用户 Ack（选任一动作或稍后）之前不重复弹；只有事件消失后又重新出现才再次提醒；同一设备同一时刻只在队列里占一个位置。

### 3.8 与 Swift 版的行为差异（有意为之，写进 README）

1. **不再依赖外部 tailcat**：工具设置去掉 tailcat 路径；组件安装只剩 adb/scrcpy；`--key=` 文件、DERP 链接、`tc...` 地址语义全部不变（服务端仍是真 tailcat），因此对端 EVCC/车机侧无需任何改动。
2. **端口分配**：先用 `net.Listen` 抢住候选端口，成功即就绪；不再有"5 秒内等 stderr 打印"的竞态，也不再需要解析 `address already in use`（bind 失败就是失败，直接下一个候选）。
3. **身份私钥进入本进程内存**：Swift 刻意只保存路径、把私钥交给 CLI；库模式下私钥必须加载到内存才能建链。文件权限仍 0600、格式仍与 CLI 一致、仍然绝不覆盖。
4. **凭据长度上限**：`mygo.Secrets` 上限 2560 字节（Swift 8192）。改为 ≤2560 并给明确错误（真实地址约 100 字节）。
5. **进程树终止**：Windows 必须整树终止，否则 `adb`/`scrcpy` 孙进程残留。
6. **取消语义**：Go `context`，Swift `Task.cancel`；对外表现一致（取消任务标 `cancelled` 而非 `failed`）。
7. **`pid` 字段**：不再展示 tailcat 进程 PID，改为展示本地端口（CLI 会话识别仍展示对方 PID）。

---

## 4. 平台适配矩阵

### 4.1 能力矩阵

| 能力 | macOS | Windows | Linux |
|---|---|---|---|
| 凭据库 | Keychain（`mygo.Secrets.Service("cc.coauto.tailcat-manager.connections")`，key = 大写 UUID，**可直读 Swift 版旧条目**） | Credential Manager（同约定） | Secret Service（无 libsecret 时降级：内存 + 启动提示） |
| 配置目录 | `~/Library/Application Support/TailcatManager` | `%AppData%\TailcatManager` | `${XDG_CONFIG_HOME:-~/.config}/TailcatManager` |
| 地址簿默认 | `${XDG_STATE_HOME:-~/.local/state}/tailcat-adb` | `%LOCALAPPDATA%\tailcat-adb` | `${XDG_STATE_HOME:-~/.local/state}/tailcat-adb` |
| adb 发现 | `/opt/homebrew/bin`、`/usr/local/bin`、`~/.local/bin`、Android SDK platform-tools、PATH | PATH+PATHEXT、`%LOCALAPPDATA%\Android\Sdk\platform-tools`、scoop shims、chocolatey bin | unix 列表 + `/snap/bin`、`/usr/lib/android-sdk/platform-tools` |
| 组件安装 | brew（adb → `install --cask android-platform-tools`） | winget（`Google.PlatformTools`）；scrcpy 无稳定包 → 提示手动 + 选路径 | apt/dnf/pacman：`android-tools-adb`、`scrcpy` |
| 传输层 | tailcat 库（纯 Go，无外部依赖） | 同左 | 同左 |
| 终端 | `Terminal.app` + 临时 `.command` | `cmd.exe /c start` 新窗口 + `adb -s … shell` | `x-terminal-emulator` → `gnome-terminal` → `konsole` → `xfce4-terminal` → `alacritty` → `kitty` → `foot` |
| 唤醒重检 | 平台事件可用则用，否则 30 s 轮询兜底 | 轮询兜底 | logind `PrepareForSleep` 或轮询兜底 |
| 进程终止 | TERM → 2 s → KILL | Job Object / `taskkill /T /F` | TERM → 2 s → KILL |
| 托盘 | 菜单栏（模板图标，深浅色） | 通知区 | 通知区/AppIndicator |
| 单实例 | `mygo.App.RequestSingleInstanceLock()` + `OnSecondInstance` | 同左 | 同左 |
| CLI 桥识别 | — | — | — |

### 4.2 平台前提

- 只需 `adb`（必需）、`scrcpy`（可选）。**不再需要安装 `tailcat`**。
- 对端（车机/EVCC）侧必须运行真正的 `tailcat` server 并给出 `tc...` 地址，这一点与 Swift 版一致。
- 库依赖 `tailscale.com`（BSD-3-Clause）与 `gvisor`、`wireguard-go`；**二进制体积基线：仅 tailcat 客户端面已 28 MB**，成品预计 35–45 MB、上限 60 MB（**已确认接受**，详见 §9）。

### 4.3 凭据助手为什么不移植

Swift 版引入独立助手，唯一理由是 **macOS Keychain 的 ACL 绑在调用方代码签名/哈希上**：ad-hoc 或本机签名的 GUI 每次重建哈希都变，用户每次更新都要重新授权（输登录密码点"始终允许"）。助手把授权对象从"经常变的 GUI"换成"装一次就不变的助手"，并对父进程做 `identifier + leaf 证书 SHA-1` 双重校验，用 stdin/stdout 传 JSON（地址不进 argv/日志），安装到 `~/Library/Application Support/TailcatManager/CredentialHelper/v1/`。

Go 版用 `mygo.Secrets` 进程内直连 Keychain，签名问题依旧存在，但代价只有 **开发期 `go run` 每次重建弹一次授权**；正式打包签名固定，装一次不弹。若将来要求"更新后零重新授权"，再用 Go 重写该助手（协议可保持 v1，service/account 不变）。

---

## 5. UI 映射（SwiftUI → mygo/onevra）

| SwiftUI | Go |
|---|---|
| `NavigationSplitView` + 侧栏 | `shells/sidebar.NewShell`（`Left`/`SetLeftWidth`/`SetLeftExpanded`/`SetPage`） |
| 侧栏 `List` 分组 | `components.List` + 分节标题 + 自定义行（`ui.Column` + `StatusDot`） |
| 状态点 `Circle` | `ui.Canvas`/圆角节点或 `lucide.Circle` + 颜色 |
| 详情 `Picker(.segmented)` | `components.Tabs(c, &tab, components.TabsSegmented, "概览", "日志")` |
| 详情键值行 | 自写 `detailRow(c, label, value)`（固定 95 px 标签 + 可选中文本） |
| `Table`（连通测试） | `components.Table` + `components.TableState` + `[]ui.TableColumn` + `TableRow` |
| `TextField` / `SecureField` | `components.TextBox(c, &v, opt)`；密码框加 `.Password()` |
| `Toggle` / `Picker` | `components.Switch` / `components.Select[T]` |
| `ProgressView` | `components.Spinner` / `components.Progress` |
| `DisclosureGroup` | `components.Collapsible(c, label, &open, opts, fn)` |
| `sheet` / `alert` / `confirmationDialog` | `components.Dialog` / `components.AlertDialog` |
| `contextMenu` | mygo `Page.OnContextMenu` / 元素上下文菜单 |
| `MenuBarExtra` | `mygo.NewTray(mygo.TrayOptions{...})` + `SetMenu`（动态重建） |
| 应用菜单 / 快捷键 | `mygo.NewMenu` + `Accelerator: "CmdOrCtrl+Shift+N"` |
| 工具栏 5 个按钮 | sidebar shell 头部动作位 + 应用菜单镜像 |
| `NSOpenPanel`（APK/上传/目录） | `mygo.Dialog.Open(mygo.OpenDialogOptions{...})` |
| 复制 ADB 入口 | `mygo.Clipboard.Write(transfer.TextData(serial), mygo.ClipboardOptions{})` |
| `NSWorkspace.didWake` | `platform.OnWake`（兜底 30 s 轮询） |
| 关闭窗口保活 | `OnClosed` → `Hide()`；托盘"打开管理窗口" → `Show()` |
| 单实例激活 | `RequestSingleInstanceLock` + `OnSecondInstance` → `Show()`/`Focus()` |
| 视图测试 | `ui.NewTester(view, w, h)`（`Click`/`HasText`/`Texts`） |

图标复用 `tailcat-tool-swift/resources/AppIcon.png`（BSD 3-Clause，注明出处），托盘另做 16/32 px 模板图；界面图标统一 `lucide`。

界面结构（信息密度与 Swift 版一致）：

```
┌───────────────┬──────────────────────────────────────────────┐
│ 我的设备       │  E245 · 示例            [连接][断开]          │
│  ● E245       │  ┌概览┬日志┐                                  │
│  ● EX5        │  本地入口 / 远端端口 / DERP 中继 / 链路 /      │
│ 临时设备       │  型号 / 代号 / Android / 序列号 / Shell 权限  │
│  ⚡ 新建临时连接│  [探测链路][刷新信息][编辑][复制 ADB 入口]     │
│               │  ── 设备操作 ──                              │
│ GUI 2 · CLI 1 │  [终端][安装 APK][上传][下载][屏幕控制]       │
│ ✓ adb         │  [执行一条 ADB Shell 命令…............][执行] │
│ ⚠ scrcpy[安装]│  ── 设备任务 ───────────────────────────────  │
└───────────────┴──────────────────────────────────────────────┘
```

---

## 6. 任务分解

> 每个任务 = 一次可独立审查的提交；先写失败测试，再实现，再 `go test ./...` + `go vet`，最后 commit。

### 任务 0：骨架与依赖冒烟（决定后面所有事）

**文件：** 修改 `go.mod`；创建 `cmd/tailcat-manager/main.go`、`internal/app/smoke.go`、`internal/app/compat.go`、`.github/workflows/ci.yml`

- [ ] **步骤 1：** 模块路径改为 `github.com/Niexiawei/tailcat-tool`；`go get github.com/egoist/mygo@v0.9.1 github.com/oneo-me/onevra/source@v0.2026.18 github.com/tailscale/tailcat@latest`（当前解析为 v0.7.0，把实际版本记进 `docs/build-notes.md`）。
- [ ] **步骤 2：** 最小窗口（`mygo.App.WhenReady` → `sidebar.NewShell` + 一个 `components.Button`），`go build` 通过。
- [ ] **步骤 3：** 解决 **onevra 钉 mygo v0.7.4、我们要 v0.9.1** 的冲突：`go build ./...` 是否报缺 API；若冲突，二选一并写进"决策记录"：(a) mygo 降到 v0.7.4；(b) 覆盖到 v0.9.1 并逐条修 onevra 报错。
- [ ] **步骤 4：** 编译期断言 `internal/app/compat.go`：`mygo.NewTray`、`mygo.Secrets`、`mygo.App.RequestSingleInstanceLock`、`components.TextBox(...).Password()`、`ui.NewTester`、`tailcat.Client`、`tailcat.ParseAddr`、`tailcat.NewPrivateKey`、`tailcat.ProxyConns`、`(*tailcat.Client).DiscoPing`、`Addr.Resolve` 全部存在。
- [ ] **步骤 5：** 三平台交叉编译基线：`CGO_ENABLED=0 GOOS=linux/darwin/windows go build ./...`，记录成品体积（预期 35–45 MB）与首次/增量构建耗时。
- [ ] **步骤 6：** CI 三平台矩阵 `go build` + `go test ./internal/core/...`，加 `-ldflags "-s -w"` 的体积对比记录。
- [ ] **步骤 7：** Commit `chore: bootstrap go rewrite with mygo, onevra and embedded tailcat`。

**验收：** 三平台 CI 绿；本机 `go run ./cmd/tailcat-manager` 打开空窗口；体积与构建耗时写进 `docs/build-notes.md`。

### 任务 1：模型、脱敏、错误、文案

**文件：** 创建 `internal/core/model.go`、`session.go`、`task.go`、`err.go`、`messages.go`、`redact.go`；对应 `_test.go`

- [ ] **步骤 1：** 写失败测试：`TestAddressPlausible`、`TestRedactAddressAndPattern`、`TestOutputRedactorSplitCredential`（分 3 块喂入，任一返回值不含 `tc` 片段）、`TestOutputRedactorLongLine`、`TestStateLabels`（7 个中文标签逐字比对）。
- [ ] **步骤 2：** `go test ./internal/core -run 'Redact|Plausible|Labels' -v` 确认 FAIL。
- [ ] **步骤 3：** 实现 `Plausible(string) bool`、`Redact(text, address string) string`、`Redactor{Append, Flush}`、`ConnectionState`（`String()` 中文 + `Busy()`）。
- [ ] **步骤 4：** 测试 PASS。
- [ ] **步骤 5：** Commit `feat(core): models, redaction and user-facing messages`。

### 任务 2：配置与凭据

**文件：** 创建 `internal/core/config.go`、`credential.go`、`tools.go`、`legacybook.go`；`internal/platform/paths_*.go`、`secrets.go`；测试 `config_test.go`、`legacybook_test.go`

- [ ] **步骤 1：** 写失败测试：`TestConfigRoundTrip`（与 Swift JSON 互读；缺 `relayPresets` 为空）、`TestConfigPermissions`（0700/0600，unix）、`TestConfigRejectsSymlink`、`TestConfigAtomicWrite`、`TestLegacyBookImport`（非法名/超 8 KiB/符号链接/非 `tc`/跳过计数）、`TestCachedCredentialStore`、`TestCredentialSizeLimit`（>2560 字节明确报错）。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `Configuration{Profiles,Tools,RelayPresets}`（JSON tag 与 Swift 完全一致）、`ConfigStore{Load,Save}`（`os.CreateTemp` + `Chmod` + `Rename`）、`CredentialStore` 三实现、`LegacyBook.Read(root)`、`ToolLocator.Find`（走 `Platform`，只剩 adb/scrcpy）。
- [ ] **步骤 4：** 测试 PASS + `go vet ./...`。
- [ ] **步骤 5：** Commit `feat(core): configuration, credentials and legacy address book`。

### 任务 3：进程执行层（只服务 adb / scrcpy）

**文件：** 创建 `internal/core/command.go`、`runner.go`；`internal/platform/kill_unix.go`、`kill_windows.go`；测试 `runner_test.go`

- [ ] **步骤 1：** 写失败测试：`TestRunCapturesBothPipes`、`TestRunTimeout`、`TestRunCancel`、`TestRunOutputTailCap`（保留末尾 1 MiB）、`TestUTF8SplitChunk`、`TestKillProcessTree`（`sh -c 'sleep 60 &'` 父退出后子也被终止）。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `Runner`（`exec.CommandContext` + 双管道排空 + 互斥 ring buffer + `Done`）、`Proc.Stop`（平台终止）、Windows Job Object。
- [ ] **步骤 4：** `go test -race ./internal/core` PASS。
- [ ] **步骤 5：** Commit `feat(core): process runner for adb and scrcpy with tree kill`。

### 任务 4：身份（库内，与 CLI 文件兼容）

**文件：** 创建 `internal/core/identity.go`；`internal/platform/lock_unix.go`、`lock_windows.go`；测试 `identity_test.go`

- [ ] **步骤 1：** 写失败测试：`TestIdentityFileMatchesCLIFormat`（`json.MarshalIndent(priv,"","\t")` 往返；含 `Private`/`Public` 两字段）、`TestIdentityCreatesOnce`（第二次复用，文件 mtime 不变）、`TestIdentityNeverReplacesCorruptFile`（损坏 → 报错且不重写）、`TestIdentityRejectsSymlinkOwnerAndMode`（0600/0700、非普通文件、符号链接）、`TestIdentityNameValidation`、`TestIdentityPublicKeyFormat`（`^nodekey:[0-9a-f]{64}$`）、`TestIdentityConcurrentInit`（两 goroutine 同时调用 → 只生成一次，文件锁生效）。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `IdentityStore{dir}`：`tailcat.NewPrivateKey()` + `json.MarshalIndent` + `OsOpenFile(...,0600)`、`flock` 50 ms × 100、读时 `json.Unmarshal` 到 `tailcat.PrivateKey`、`PublicKey = priv.Private.Public().String()`、`NodeKey()` 返回 `key.NodePrivate`。
- [ ] **步骤 4：** 测试 PASS。
- [ ] **步骤 5：** Commit `feat(core): tailcat identities compatible with the CLI key format`。

### 任务 5：DERP 归一化

**文件：** 创建 `internal/core/relay.go`；测试 `relay_test.go`

- [ ] **步骤 1：** 写失败测试（表驱动，逐条对应 Swift 校验分支）：URL 接受/拒绝（http/https 通过；ftp、user/password、fragment、无 host 拒绝）；去尾逗号（字符串内不动）；`"derpMap":{…}` 包装补全；Regions 1–128；每区 1–32 节点；`RegionID` 与键一致；`Regions` 为空拒绝；`HostName` 正则 `^[A-Za-z0-9_.:-]+$`；`DERPPort` 0–65535；`STUNPort` −1–65535；布尔值不当整数；>128 KiB 拒绝；`TestRelayProducesDERPMap`（内联 JSON → `*tailcfg.DERPMap` 且 Regions 数量正确）。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `NormalizeRelay(input string) (RelayConfig, error)`：URL → `RelayURL`；JSON → 清洗（去尾逗号、补 `derpMap` 包装）→ `json.Unmarshal` 到 `*tailcfg.DERPMap` → 逐项校验 → `RelayInline`；空 → `RelayOfficial`。
- [ ] **步骤 4：** 测试 PASS。
- [ ] **步骤 5：** Commit `feat(core): DERP map normalization to an in-memory map`。

### 任务 6：转发器（核心，替代外部 tailcat 进程）

**文件：** 创建 `internal/core/forwarder.go`；`internal/testutil/fake_tailcat.go`；测试 `forwarder_test.go`

- [ ] **步骤 1：** 写失败测试（把 tailcat 客户端抽成窄接口 `tailcatDialer`，测试注入假实现）：
  - `TestForwardAutoPortFrom5556`（5556 被占 → 5557；连续 100 个都占 → "5556–5655 均被占用"）
  - `TestForwardPreferredPortBusy`（固定端口被占 → 立即报错，不静默改端口）
  - `TestForwardPortReadyImmediately`（`Open` 返回时端口已可 `net.Dial`，无 5 s 等待）
  - `TestForwardReturnsSerial`（`Serial == "127.0.0.1:<port>"`）
  - `TestForwardProxiesTraffic`（本地 listener 收到连接 → 假 dialer 返回的 `net.Pipe` 双向转发，字节一致）
  - `TestForwardCloseOrder`（`Close` 先关 listener、再关活跃连接、最后 `Client.Close()`；关闭后新连接被拒）
  - `TestForwardPortReleasedAfterClose`（关闭后同端口可再次监听）
  - `TestForwardInlineRelayUsesMap`（`RelayInline` → 假 dialer 收到 `*tailcfg.DERPMap`；`RelayOfficial` → 走默认 URL 分支）
  - `TestForwardRedactsLogs`（`Logf` 输出含 `tc...` → 回调前已脱敏）
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `LibForwarder.Open`：候选端口（`PreferredPort` 或 5556..5655）→ `net.Listen("tcp", "127.0.0.1:p")` → 构造 `&tailcat.Client{Server, Key, Logf}`（`RelayInline` 时先 `Server.Resolve(ctx, dm)`）→ `acceptLoop`（`cl.DialTCPPort` + `tailcat.ProxyConns`）→ 返回 `ForwardConn`；`Close` 用 `sync.Once` 保证幂等，`sync.Map` 跟踪活跃连接。
- [ ] **步骤 4：** `go test -race` PASS。
- [ ] **步骤 5：** Commit `feat(core): in-process forwarder on the tailcat library`。

### 任务 7：探测（库内）

**文件：** 创建 `internal/core/pinger.go`；测试 `pinger_test.go`

- [ ] **步骤 1：** 写失败测试：`TestPingSerializesSameDevice`（同设备并发两次 → 假 dialer 记录最大并发 = 1）、`TestPingDifferentDevicesConcurrent`、`TestPingTimeout`（ctx 5 s 截止）、`TestDiscoPingRoute`（`Endpoint == ""` → `中继 · DERP(<code>)`；`Endpoint` 非空 → `直连 · <endpoint>`）、`TestPingUsesProbeIdentity`（用 `<uuid>-probe` 而非 `-connection`）、`TestPingClosesClient`（无论成功失败都 `Close`，无 goroutine 泄漏 —— 用 `goleak`）。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `LibPinger.Ping(ctx, deviceID, address, seconds, relay)`：拿 probe 身份 → 建 `Client` → `DiscoPing` → 映射 `PingResult` → `defer cl.Close()`；同设备用 `sync.Mutex` + `map[uuid.UUID]*sync.Mutex` 串行化。
- [ ] **步骤 4：** 测试 PASS（`-race` + goleak）。
- [ ] **步骤 5：** Commit `feat(core): serialized probes via disco ping`。

### 任务 8：会话状态机（连接/监视/重试/断开）

**文件：** 创建 `internal/core/manager.go`、`manager_connect.go`；测试 `manager_connect_test.go`

- [ ] **步骤 1：** 写失败测试（假 Forwarder + 假 Runner）：
  - `TestConnectFlow`（`ParseAddr` 校验 → 身份 → `Open` → `adb connect 127.0.0.1:P` → 10×400 ms 轮询 → online）
  - `TestConnectRejectsBadAddress`（非法 `tc...` → 不建监听器，状态 failed + 文案）
  - `TestConnectUnauthorized`（`get-state` 含 `unauthorized` → `ADB 待授权`）
  - `TestDisconnectOrder`（关 listener → 关连接 → `adb disconnect` → `Client.Close`；端口在 disconnect 完成后才释放）
  - `TestReconnectCancelsOldTasks`、`TestStaleGenerationIgnored`（旧 generation 的回调不写状态）
  - `TestMonitorRefreshInterval`（3 s；`get-state` 成功才 online）
  - `TestOfflineRetryBackoff`（2/4/8/16/30/30 s，第 6 次后给"已尝试 6 次"）
  - `TestUnauthorizedNoRetry`、`TestForwarderExitMarksFailed`、`TestShutdownCancelsEverything`
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `Manager`（mu/changed/profiles/sessions/tasks/relayPresets/generations/runtimes/connections/monitors/taskJobs/retryCounts/nextRetries）+ `Connect`/`Disconnect`/`DisconnectAll`/`Shutdown`/`StartMonitor`/`Refresh`/`RetryOffline`/`RecheckConnections`。
- [ ] **步骤 4：** 测试 PASS（`-race`）。
- [ ] **步骤 5：** Commit `feat(core): per-device session state machine`。

### 任务 9：设备与预设管理

**文件：** 创建 `internal/core/manager_devices.go`；测试 `manager_devices_test.go`

- [ ] **步骤 1：** 写失败测试：`TestSaveDeviceRollbackOnPersistFailure`、`TestSaveDeviceRejectsWhileConnected`、`TestTemporaryDeviceMemoryOnly`、`TestPromoteTemporaryKeepsSession`（保存后连接与任务不中断；失败回滚删凭据）、`TestRemoveDeviceDisconnectsFirst`、`TestImportLegacyDedupByLegacyUsername`、`TestSaveRelayPresetDuplicateName`、`TestSaveRelayPresetNormalizesSource`、`TestValidatedDeviceInput`（名称 1–80、端口 1–65535、空本地端口 = 自动）。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现与 Swift 同名同语义的方法 + `notice` 文案。
- [ ] **步骤 4：** 测试 PASS。
- [ ] **步骤 5：** Commit `feat(core): device and relay preset management`。

### 任务 10：设备操作与任务

**文件：** 创建 `internal/core/manager_tasks.go`；测试 `manager_tasks_test.go`

- [ ] **步骤 1：** 写失败测试：`TestBeginADBTaskBindsSnapshot`、`TestTaskHistoryCap`（>100 只裁已完成）、`TestTaskOutputCapAndRedaction`（末尾 32 KiB）、`TestScreenUsesScrcpyArgs`（`--max-size=1280 --video-bit-rate=4M --max-fps=30`，86400 s，`ADB=<adb>`）、`TestValidateTargetRejectsOffline`（"任务目标已离线，未执行设备操作"）、`TestDiagnoseSetsRoute`（探测结果写进 route 字段）、`TestRefreshDeviceInformation`（5 行 getprop）。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `operationTarget`/`targetIsCurrent`/`validateTarget`/`startTask`/`cancelTask`/`Diagnose`/`RefreshDeviceInformation`/`BeginScreen`/`TerminalCommand`；日志上限 200 条 × 8192 字节。
- [ ] **步骤 4：** 测试 PASS。
- [ ] **步骤 5：** Commit `feat(core): device operations, tasks and diagnostics`。

### 任务 11：批量连通测试

**文件：** 创建 `internal/core/reachability.go`；测试 `reachability_test.go`

- [ ] **步骤 1：** 写失败测试：`TestReachabilityConcurrency3`（10 台 → 峰值并发 3）、`TestReachabilityCancel`（未开始标 `cancelled`）、`TestRouteMapping`（直连/中继两分支 + 延迟格式化保留 1 位小数）、`TestUnreachableVsFailed`、`TestDetailTruncated1000`、`TestAdbStateSnapshotAtCompletion`。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `Checker.Start(profiles, temporaryIDs, adbStateFn, addressFn)` + `Cancel()`/`Shutdown(ctx)`，worker 池 3，单次 5 s。
- [ ] **步骤 4：** 测试 PASS。
- [ ] **步骤 5：** Commit `feat(core): batch reachability checks`。

### 任务 12：组件安装（只剩 adb / scrcpy）

**文件：** 创建 `internal/core/installer.go`；`internal/platform/pkg_darwin.go`、`pkg_linux.go`、`pkg_windows.go`；测试 `installer_test.go`

- [ ] **步骤 1：** 写失败测试：`TestInstallArgs`（brew adb → `install --cask android-platform-tools`；winget → `Google.PlatformTools`；apt → `android-tools-adb`）、`TestInstallerEnvSuppression`（7 个 `HOMEBREW_NO_*`）、`TestInstallerSkipsExisting`、`TestInstallerMissingPackageManager`（"未检测到 Homebrew…"）、`TestInstallerCannotAutoInstall`（Windows scrcpy → "无稳定包，请手动指定路径"）、`TestInstallerCancelAndFailure`、`TestInstallMissingComponents`。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `Installer.Install(ctx, comps, onConfigure)`（1800 s 超时、输出 32 KiB、可取消），`Platform.PackageManager`/`InstallArgs` 驱动。
- [ ] **步骤 4：** 测试 PASS。
- [ ] **步骤 5：** Commit `feat(core): adb and scrcpy installer per platform`。

### 任务 13（已取消，不做）：CLI 外部会话识别

**它原本做什么**：Swift 版支持用户在终端里用老 `tailcat-adb` 命令行建桥（每台设备一条 `socat TCP-LISTEN:<port>,bind=127.0.0.1 EXEC:<tailcat> <tc地址> <远端端口>`）。该功能每 ~3 秒扫地址簿里的 `bridges/<port>.pid`，核对 PID 确实是活的 socat、确实在该端口监听、命令行 5 段结构与 `tc...` 地址对得上，并用 `adb devices -l` 映射状态；然后把这些"不是 GUI 建的"通道挂到设备名下显示为"CLI 外部连接 · 5556"，允许在 GUI 里对它们执行 APK 安装/上传下载/Shell/scrcpy/探测，但**绝不连接、重连、断开或杀掉 CLI 进程**。

**为什么取消**：Go 版只做 GUI，自己就是唯一通道持有者，不起 socat、没有 `bridges/` 目录，也就没有"外部桥"这个概念。保留它要付出 `ps`/`lsof`（Linux `/proc`）解析、3 秒扫描循环、Keychain 免弹窗读取路径、地址与设备归属的启发式匹配，以及一整套误报/漏报 bug 面；而且**让 CLI 和 GUI 同时管同一台设备本身就有风险**。

**如果将来确实要支持老 CLI 并行**：重新开一份规格（不要复活本任务的步骤），先回答“两个工具同时操作同一台设备时谁说了算”。本期范围已用任务 16（外部 ADB 干预检测）覆盖“用户在本机 adb 动了我们”的场景。

### 任务 14：UI 骨架与状态桥

**文件：** 重写 `cmd/tailcat-manager/main.go`；创建 `internal/app/state.go`、`shell.go`、`sidebar.go`、`tray.go`、`strings.go`

- [ ] **步骤 1：** 写失败测试（`ui.NewTester`）：`TestViewModelNotifiesCoalesced`、`TestSidebarGroups`（我的设备/临时设备顺序、临时标记、状态文案）、`TestToolHealthRows`（缺失 → "安装"/"自动配置"）。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现 `ViewModel`、`Changed()` 消费循环（`app.Post` → `c.Invalidate()`）、sidebar shell（左栏 200/225/290）、底部工具健康、头部 5 个动作 + 应用菜单镜像、托盘动态菜单。
- [ ] **步骤 4：** 测试 PASS；`go run` 打开真实窗口无 panic。
- [ ] **步骤 5：** Commit `feat(app): window shell, sidebar and tray`。

### 任务 15：详情页、表单与任务面板

**文件：** 创建 `internal/app/detail.go`、`tasks.go`、`editor.go`、`transfer.go`、`settings.go`、`reachability.go`

- [ ] **步骤 1：** 写失败测试：`TestDeviceEditorValidation`、`TestTransferRemotePathValidation`（必须以 `/` 开头、不含 `\0`/`\n`）、`TestDetailRows`（9 行 + 回退文案）、`TestTaskCancelOnlyRunning`、`TestReachabilityTableRows`、`TestDemoModeNoRealCommands`。
- [ ] **步骤 2：** 确认 FAIL。
- [ ] **步骤 3：** 实现概览/日志 Tab、9 行详情、4+1 操作按钮、Shell 命令框、复制 ADB 入口、5 种 Dialog、文件选择、任务面板、连通测试表、`--demo`。
- [ ] **步骤 4：** 测试 PASS；`--demo` 手工冒烟：两台假设备连上、任务跑完、连通测试出结果。
- [ ] **步骤 5：** Commit `feat(app): detail, dialogs, tasks and reachability views`。

### 任务 16：外部 ADB 干预检测与"等待处理"对话框（本期新增，不可砍）

**文件：** 创建 `internal/core/attention.go`、`internal/app/attention.go`；修改 `internal/core/manager.go`、`manager_connect.go`、`model.go`（加 `needsAction`）、`internal/app/shell.go`、`tray.go`、`sidebar.go`；测试 `attention_test.go`、`attention_view_test.go`

- [ ] **步骤 1：** 写失败测试（纯函数检测）：`TestDetectSerialDetached`、`TestDetectPortDriftAgainstPreferred`（配了 5556 但监听在 5557）、`TestDetectNoDriftForAutoFirstFree`（自动端口且确实抢到 5556 → 不报）、`TestDetectPortChangedByIdentity`（P2 存在且 `device`+`model` 相同）、`TestDetectPortChangedNeedsIdentity`（身份缺失 → 降为 `serialDetached`，不报 `portChanged`）、`TestDetectListenerLost`、`TestDetectAdbServerRestarted`（≥2 设备同时消失）、`TestDetectIgnoresOfflineAndUnauthorized`（不误报）、`TestDetectSameModelDevicesNoFalsePositive`。
- [ ] **步骤 2：** 写失败测试（状态机交互）：`TestInterferenceStopsRetry`（命中后不再发 `adb connect`，状态 `needsAction`）、`TestAttentionDedupAndAck`（同类事件只一条；Ack 后重现才再报）、`TestAttentionClearedWhenHealthy`、`TestReconnectSkipsDisconnectWhenSerialGone`、`TestSwitchPortRemembersPreference`（写回 `preferredPort`，持久化失败回滚）、`TestPortCandidatesProbe`（`net.Listen` 实测，已占端口不出现）。
- [ ] **步骤 3：** 写失败测试（UI 时机，`ui.NewTester` + 假窗口状态）：`TestDialogShowsWhenFocused`、`TestDialogQueuedWhenUnfocused`、`TestDialogShowsOnFocusEvent`、`TestTrayBadgeWhilePending`、`TestQueueDrainsOneByOne`。
- [ ] **步骤 4：** 实现 `core/attention.go`：`Attention{DeviceID,Kind,Serial,DetectedPort,Detail,DetectedAt}`、`DetectInterference(...)`、`Manager.Attentions()`/`AckAttention`/`SwitchPort(id,port,remember)`；把监视循环改成“先采样 `adb devices -l` → 检测 → 命中则停重试并置 `needsAction`”。
- [ ] **步骤 5：** 实现 `app/attention.go`：队列 + `Window.OnFocus`/`IsVisible` 泵出 + `components.Dialog`（三个动作 + 端口选择器 + “记住此端口”）+ 托赘 `Badge` + 系统通知与点击回窗。
- [ ] **步骤 6：** 测试 PASS（`-race`）；手工验收：终端里 `adb disconnect 127.0.0.1:5556` → 侧栏变橙色“等待处理” → 切到后台再回来 → 弹框 → 选“切换本地端口 → 5557 → 记住” → 重连成功且配置里写入 `preferredPort: 5557`。
- [ ] **步骤 7：** Commit `feat: detect external adb interference and ask the user`。

### 任务 17：平台收尾与发布

**文件：** `internal/platform/terminal_*.go`、`wake_*.go`；`cmd/tailcat-manager/main.go`；`mygo.json`（或 `mygo.config`）、`README.md`、`THIRD_PARTY_NOTICES.md`、`.github/workflows/ci.yml`

- [ ] **步骤 1：** 写失败测试：`TestTerminalQuoting`（含空格/单引号路径）、`TestWakeFallbackPolling`（30 s）、`TestShutdownOrder`（取消任务 → 停监视 → `disconnectAll` → 清临时设备）。
- [ ] **步骤 2：** 实现三平台终端、唤醒、单实例、关闭窗口隐藏、退出 `Manager.Shutdown(ctx)`（10 s 超时）。
- [ ] **步骤 3：** `mygo build` 三平台产物（macOS .app/.dmg、Windows 安装包、Linux deb/rpm 择一）；图标复用现有资源，托盘另做模板图。
- [ ] **步骤 4：** README 重写：只需 adb/scrcpy、三平台安装命令、配置/身份/凭据位置、Clash Verge 进程分流片段（进程名改为本应用 + adb/scrcpy）、与 Swift 版差异（§3.8）、体积说明、“外部断开/换端口会被提示”一节。
- [ ] **步骤 5：** `THIRD_PARTY_NOTICES.md` 加 tailcat 与 tailscale.com（BSD-3-Clause）与 gvisor/wireguard-go；保留图标 BSD 声明与 MIT LICENSE。
- [ ] **步骤 6：** CI 加 `gofmt -l .`（无输出）、`go vet`、`go test -race`、三平台 `mygo build`。
- [ ] **步骤 7：** Commit `chore: platform integration, packaging and release pipeline`。

---

## 7. 测试策略

- **单元/行为测试**：`internal/core` 全部用假 Runner / 假 Forwarder / 假 dialer（`tailcatDialer` 窄接口）/ 假 CredentialStore；不启真进程、不连网、不碰真凭据库；`go test -race ./...` 干净，长生命周期 goroutine 用 `goleak` 兜底（`Client`/`ProxyConns` 最容易泄漏）。
- **视图测试**：`ui.NewTester` 无窗口渲染；每个视图至少"不 panic + 关键文本存在 + 关键按钮可点"。
- **平台层**：纯函数优先（`InstallArgs`、终端引号、`ps`/`lsof`/`/proc` 解析），用固定文本 fixture；需要真 OS 的（Keychain、进程树终止、终端）用 `t.Skip` + 环境变量开关。
- **手工验收清单**（每个里程碑结束跑）：
  1. 两台真机并发连接：各自独立端口、独立身份、`adb devices` 两条记录。
  2. 待授权设备显示"ADB 待授权"、不自动重试、授权后转"ADB 已连接"。
  3. APK 安装 / 上传 / 下载 / Shell / scrcpy 五条路径各一次，任务面板状态与取消正确。
  4. 拔线/关设备：转"ADB 离线"→ 6 次退避 → 提示已尝试 6 次。
  5. 断开应用管理的连接：`adb devices` 无该 serial，端口可被其他程序立即复用，无残留监听。
  6. 批量连通测试：并发 3、可取消、路径显示直连/中继。
  7. 关闭主窗口连接仍在，托盘可重开；退出应用后无残留进程（三平台各验）。
  8. `--demo` 不触碰真实地址簿/凭据/设备。
  9. 导入旧 `configuration.json` + 旧 `.private.json`：设备名/端口/DERP 设置/身份保持，地址仍需一次 Keychain 授权（macOS）。
  10. 外部干预：终端 `adb disconnect 127.0.0.1:5556` / `adb kill-server` / 另一台设备抢用同型号身份 → 侧栏转“等待处理”、不自动重连、后台只排队不抢焦点、回到窗口弹框、三个动作各自生效（含“记住端口”持久化）。

---

## 8. 里程碑顺序与依赖

```
M0 任务0 骨架 + 依赖冒烟 + 体积基线（阻塞一切）
 └─ M1 任务1,2 模型/配置/凭据
     └─ M2 任务3,4,5 进程层/身份/DERP
         └─ M3 任务6,7,8 转发/探测/状态机  ← 功能内核，可先跑 CLI 冒烟
             ├─ M4 任务9,10,11 设备/任务/连通
             ├─ M5 任务12 安装器
             └─ M6 任务14,15 UI
                 └─ M7 任务16 外部干预检测 + 对话框（**本期必做**）
                     └─ M8 任务17 平台收尾 + 打包发布
```

M3 结束时应当能用 `go run` 的一个隐藏子命令（`--headless-connect <addr>`）完成真实连接验证，不必等 UI。

---

## 9. 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| **二进制体积**：仅 tailcat 客户端面 28 MB（含 tailscale.com / gvisor / wireguard-go） | 成品 35–45 MB，与 mygo "几 MB"的宣传不符 | **已确认接受**；M0 仍记录基线，`-ldflags "-s -w"`，README 明示体积 |
| tailcat 库 API 未到 1.0，`Client` 字段/行为可能变 | 升级即破 | 钉死 `v0.7.0`；tailcat 的 import 收敛在 4 个文件里；升级需单独评审任务 |
| `tailscale.com` 依赖含 pre-release 版本（`v1.103.0-pre.…`） | 构建可重现性 | 提交 `go.sum`；CI 用 `-mod=readonly`；不跑 `go get -u` |
| onevra v0.2026.18 钉 mygo v0.7.4，我们要 v0.9.1 | 编译失败或行为差异 | 任务 0 步骤 3 专门验证并记录决策 |
| 库模式下私钥进进程内存（Swift 版刻意不下放） | 内存 dump 可泄露 | 文件 0600 + 目录 0700；不做额外导出 UI；README 说明 |
| Linux 无 Secret Service | 无法保存凭据 | 降级内存 + 启动横幅；文档给 gnome-keyring/KWallet 安装方式 |
| Windows scrcpy 进程树终止不彻底 | 残留占用 | Job Object；退出打印未清理 PID |
| 无凭据助手，macOS 开发版反复弹授权 | 开发体验 | `go run` 用独立 service 后缀（`…connections.dev`）避免污染正式条目；打包签名固定 |
| 行为等价但文案漂移 | 无法对照验收 | 文案集中 + 逐字断言 |
| 外部干预**误判**（同型号设备、临时 offline）→ 频繁弹框 | 打扰用户，比不提示更糟 | `portChanged` 必须设备身份匹配；`offline`/`unauthorized` 不报；同类事件只一条 + Ack 前去重；单测覆盖“同型号两台”“身份缺失”“短暂 offline”三个反例 |
| 自动重试与“等用户决定”冲突（用户断开后又被偷偷连上） | 违背用户意图 | `needsAction` 状态下禁用一切自动 `adb connect`，单测 `TestInterferenceStopsRetry` 钩住 |

---

## 10. 待你确认的决策（我给的默认值）

1. **主模块名**：默认 `github.com/Niexiawei/tailcat-tool`（去掉 `.git` 后缀）。
2. **tailcat 版本**：跟随 upstream 最新（当前 `v0.7.0`，已是 latest tag）；用 `go get github.com/tailscale/tailcat@latest`，升级时跑全量回归并在决策记录里写版本。
3. **Swift 源码去留**：默认保留 `tailcat-tool-swift/` 作为行为基线与资源来源，Go 版稳定后另起提交归档。
4. **CLI 会话识别**：**不做**（用户确认：只做 GUI）。用户要切到 GUI 时，先把终端里 `tailcat-adb` 建的桥关掉。相应地，新增的第 8 个状态与任务 16 覆盖了“用户在本机 adb 动了我们的连接”这件事。
5. **凭据助手**：默认不移植，用 `mygo.Secrets`；若要求"开发/更新期零弹窗"，再用 Go 重写（协议 v1 不变）。
6. **配置与身份兼容**：默认 macOS 沿用 `~/Library/Application Support/TailcatManager/configuration.json`、Keychain service `cc.coauto.tailcat-manager.connections`（key 大写 UUID）、`Identities/*.private.json`，**可直接继承旧数据**；其他平台用各自规范目录。
7. **单实例**：默认全平台强制单实例。
8. **发布形态**：默认 `mygo build` 产出（macOS zip/dmg、Windows exe 安装包、Linux deb/rpm 择一），不做应用内自动更新。
9. **体积**：已确认可接受 35–45 MB；目标上限 60 MB（单文件，`-s -w` 后）。若未来要求 <20 MB，才需要评估把 tailcat 拆回独立子进程。
10. **外部干预提示的默认值**：检测到就停重试并转 `needsAction`（不偷偷重连）；前台立即弹、后台只排队，`OnFocus`/系统唤醒时弹出；新增第 8 个状态“等待处理”。

---

## 11. 决策记录（实施时追加）

| 日期 | 决策 | 理由 |
|---|---|---|
| 2026-11-15 | 计划建立，规格基于 Swift v0.1.9 | — |
| 2026-11-15 | 传输层由"外部 tailcat 进程"改为"内嵌 tailcat 库 v0.7.0" | 去掉外部二进制依赖与 stderr 解析、端口就绪竞态、内联 DERP 回环服务；三平台无需上游预编译产物 |
| 2026-11-15 | tailcat 跟随 upstream 最新（当前 v0.7.0），不精确钉版本 | 用户选择"直接用最新"；升级以单独提交 + 全量回归控制风险（库 API 未到 1.0） |
| 2026-11-15 | 确认接受 35–45 MB 单文件体积 | 换来零外部传输依赖；`-s -w` 与 README 说明收尾 |
| 2026-11-15 | "CLI 外部连接"识别第一版不做 | 我们自己就是唯一通道持有者；两边同时管同一设备有风险 |
| 2026-11-15 | 只做 GUI、彻底不做 CLI 链路（无 socat、无 bridges 目录） | 用户决定：去掉“外部桥”概念，从根上消除归属/接管问题 |
| 2026-11-15 | **本期新增**：外部 ADB 干预检测 + `needsAction` 状态 + 聚焦/唤醒时对话框（重连 / 换端口 / 稍后） | 用户要求：本机 `adb disconnect` 或设备换本地端口时必须感知，并由用户决定，不能偷偷重连 |
