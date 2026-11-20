# Tailcat 通道掉线检测与分层归因（A+B）实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** tailcat 通道断开时状态显示「Tailcat 断开」而不是永远停在「ADB 已连接」；设备 ADB 卡死而隧道正常时仍显示「ADB 离线」——两层原因分开呈现，并各自走正确的恢复动作。

**架构：** 每台设备两个循环。**快循环**（2 秒，`TunnelInterval`）只做一次隧道往返（`tailcat.Client.DiscoPing`），记录「连续无响应」次数，不改任何状态。**慢循环**（3 秒，`MonitorInterval`，与本地 ADB 的 `get-state` 同频）读 `get-state`，对 tailcat 通道再做一次端到端 `adb shell true`，然后**在一个写者里归因**：隧道与端到端都无应答 → 新状态 `tunnelLost`（重建通道）；只有端到端无应答 → `offline`（现有退避）；只有隧道无应答但设备仍应答 → 保持 online（DERP 路径抖动）。

**技术栈：** Go（纯、无 cgo）、`github.com/tailscale/tailcat`（`Client.DiscoPing` 已被 `internal/compat/compat_test.go` 钉住）、adb 命令行。

**规格：** 本文档 §1（证据与两条硬事实）与 §2（判定表、状态定义）。§1 的两条事实都是实测/读源码得到的，执行者若怀疑可按 §1 复现。

## 全局约束

- 沿用 `AGENTS.md`：纯 Go 无 cgo；`internal/core` 不 import app；**`tailcat` 只能出现在 `forwarder.go`、`pinger.go`、`identity.go`、`relay.go`**；不许 `exec.Command`（本计划只用 `runner.Run`）；不持有 `m.mu` 调会上锁的方法；异步分支写状态前先 `isCurrentLocked`。
- 用户可见文案集中在 `internal/core/messages.go` 或视图里，测试**逐字**断言；文案是中文。
- TDD 顺序不可跳：写测试 → 跑一次看到 RED → 最少实现 → 跑绿 → `gofmt -w` / `go vet ./...` / `go test ./...` → commit。
- 每个改变用户行为的提交，同一次提交里带一条 `docs/CHANGELOG.md`（当天已有 `## 2026-11-20` 就追加到其下）。
- 分支 `feat/tunnel-liveness`，提交信息 `fix:` / `feat:` / `docs:` 前缀。
- `needsAction` 语义不动：外部干预仍只由 `DetectInterference` 触发、且不许自动重试。

## 审查重点（Review Focus）

1. **隧道还活着但用户在看状态时被误判成「Tailcat 断开」**——DERP 路径抖动而数据通路正常时，A 会失败；必须靠 B 有应答把它挡回来（测试 `TestTunnelQuietButDeviceAnswersStaysOnline`）。
2. **设备 ADB 卡死被误判成隧道断开**——`get-state` 说 online、`shell true` 不答、但隧道探活正常，必须落到 `ADB 离线`，且只走 `adb connect` 退避（测试 `TestStuckAdbShowsOfflineNotTunnelLost`）。
3. **状态在两个写者之间抖**——每个 tick 两次写状态会每 3 秒产生两条日志；状态只能由 `refresh` 一处写（测试 `TestSignalLossLogsOneLine`）。
4. **用户点「断开」时恰好判定掉线**——重建前必须验 generation，不能把已断开的设备连回去（测试 `TestRebuildStopsWhenTheGenerationIsGone`）。
5. **`unauthorized` 设备通道死了却永远显示「ADB 待授权」**——授权前跑不了 B，A 连续无响应必须单独判掉线（测试 `TestTunnelLossWhileUnauthorized`）。
6. **本地直连设备被新逻辑牵连**——直连没有隧道也没有额外的 `shell` 往返，行为必须与今天完全一致（测试 `TestDirectDeviceKeepsItsOldCadence`）。

---

## 1. 证据与两条硬事实

### 1.1 `adb get-state` 回答的是本机缓存（现有检测失效的原因）

现有判活只有 `adb -s <serial> get-state`，而它回的是 **adb server 本地缓存的 transport 状态**，不做任何往返。实测（2026-11-20）：起一个「完成 adb 握手后彻底静默、socket 不关」的假设备（24 字节 `CNXN` 头 + device banner，之后不再读写）：

| 操作 | 结果 |
|---|---|
| `adb -s 127.0.0.1:5556 get-state` | `device`，exit=0 —— 仍然说在线 |
| `adb -s 127.0.0.1:5556 shell true` | 挂住，5 秒超时 exit=124 |
| 把假设备的 socket 真正关掉（FIN） | `get-state` → `error: device offline`，exit=1 |

所以本地 ADB 直连「显示 ADB 离线」是对的——设备真的关了 TCP，adb server 读到 EOF；而 tailcat 对端失联时 `tailcat.ProxyConns` 这侧没有心跳或超时，本地连接一直开着，adb server 读不到 EOF，`get-state` 永远说 `device`。底层 netstack 的 TCP 还会按 gVisor 的 RTO 退避重传（`MaxRTO = 120s`，多次才放弃），于是十几分钟内毫无反应。

### 1.2 `tailcat.Client.Ping` 不能当探活（必须用 `DiscoPing`）

`tailcat@v0.7.0/tailcat.go`：`Client.Ping` → `ping()`，等的是 `<-c.meowWait`；`c.meowWait` 由 `sync.OnceFunc`（`onMeowed`）**只关一次**。首次握手成功后，`Ping()` 每次都在几微秒内返回成功，对端早死了也照样绿。真正每次都发往返的是 `Client.DiscoPing(ctx)`：它向 server peer 发 disco ping 并等 pong（magicsock 从不回调时靠 ctx 兜底），顺带用一次 TSMP ping 触发直连路径发现。

**因此A 只能用 `DiscoPing`。** 这一条写进 `forwarder.go` 的接口注释里，避免以后有人"优化"回 `Ping`。

### 1.3 复现方法

按 1.1 起假设备即可复现「`get-state` 说在线、`shell true` 挂住」；这也就是 tailcat 通道静默失效时 adb 看到的景象。

## 2. 目标行为

### 2.1 两个循环

| 循环 | 间隔 | 做什么 | 写状态？ |
|---|---|---|---|
| 快循环 `tunnelLoop` | `TunnelInterval`，默认 2 秒 | 一次 `DiscoPing`（`tunnelPingTimeout = 2s` 上限），维护 `runtime.tunnelMisses`；**达到阈值那一刻**写一行日志「Tailcat 链路探活连续 2 次无响应」 | 不写 |
| 慢循环（现有 `startMonitor` 的 poll 循环） | `MonitorInterval`，默认 3 秒（与本地 ADB 的 `get-state` 同频） | `get-state` → 对 tailcat 通道再做一次 `adb shell true`（**每 tick 都做**）→ 归因 → 写状态 → 干预检测 → 退避/重建 | 唯一写者 |

两个循环共享 `startMonitor` 建的那个 ctx 与一个 cancel（`m.monitors[id]`），`Disconnect` 一次取消两个。

### 2.2 归因表

记 `tunnelDown = runtime.tunnelMisses >= 2`；`answered` = 本次 `adb shell true` 有应答（clean exit 或输出含 `unauthorized`）。

| `tunnelDown` | `get-state` | `answered` | 结论 |
|---|---|---|---|
| 否 | online | 真 | `online`（不变，正常态） |
| 否 | online | **假** | **`offline`** +「设备在 4 秒内没有应答，ADB 可能已卡住」→ 现有 `adb connect` 退避 |
| 否 | offline / unauthorized | 不跑 B | 与今天完全一致 |
| **是** | online | 假 | **`tunnelLost`**（两层都断，低层是隧道） |
| **是** | offline / unauthorized | 不跑 B | **`tunnelLost`**（设备侧本来就不通，隧道也断了） |
| **是** | online | 真 | 保持 `online`：只有 DERP 路径抖动，数据通路正常（不写状态、不写日志） |
| 直连（`runtime.conn == nil`） | — | 不跑 A、不跑 B | 与今天完全一致 |

判掉线后（`tunnelLost`）：

1. `state = tunnelLost`，`Err = MsgTunnelLost`，写一行日志；`retryCounts[id] = 0`、`delete(nextRetries, id)`，让下一轮 tick 立刻开始重建。
2. **不停监视器**（与 `detectInterference` 不同——那里停是因为 `needsAction` 不许自动重试；这里要靠监视器重建）。
3. 下一轮 `retryOffline` 走重建分支：先停掉读死通道的东西（取消该设备的任务并等待、`StopLogcat`、`stopResourceSamplingLocked`，顺序同 `Disconnect`），再

```
关旧 conn → adb disconnect <旧 serial> → openTailcatChannel（新 listener + 新 Client）→ adb connect <新 serial> → 轮询 get-state
```

沿用 2/4/8/16/30/30 秒退避与 6 次预算；耗尽后留在 `tunnelLost`，`Err = MsgTunnelRebuildFailed`。

**恢复**：隧道自己活回来（`tunnelMisses` 归零）时，`refresh` 在写状态前清掉 `stalled`、写一行日志「Tailcat 通道已恢复」，然后本 tick 的 `get-state` 决定是 `online`（通道自愈，不重建）还是 `offline`（走 `adb connect` 退避）。所以短抖动不会拆通道。`stalled` 期间不跑 B（通道已死，`shell` 只会再挂 4 秒）；恢复那一个 tick 若 `get-state` 仍说 `device` 就按在线处理，ADB 是否真的通由下一 tick 的 B 裁决，最多慢一个周期。

### 2.3 新状态

第 9 个状态，加在 `internal/core/model.go`：

| 值 | 中文标签 | `Busy()` |
|---|---|---|
| `tunnelLost` | `Tailcat 断开` | false |

- 状态点（`internal/app/state.go` `statusDot`）用 warning 色，与 `offline`/`needsAction` 同族。
- 侧栏行显示「Tailcat 断开 · 5557」，详情页 `session.Err` 给出整句：「Tailcat 通道已断开，正在重建通道」。
- `operationTarget` 已经用 `!= StateOnline` 拒绝任务，无需改动。

## 3. 文件结构

| 文件 | 职责 |
|---|---|
| 修改 `internal/core/forwarder.go` | 隧道探活原语：`TailcatSession.DiscoPing`、`ForwardConn.TunnelPing`、`libSession.DiscoPing`（tailcat 调用仍只在这里），注释写明为什么不能用 `Client.Ping` |
| 新建 `internal/core/manager_channel.go` | 本特性业务逻辑：`roundTripAnswered`、`adbRoundTrip`、`probeTunnel`、`channelVerdict`、`openTailcatChannel`、`rebuildChannel` |
| 修改 `internal/core/manager.go` | 常量、`TunnelInterval` 选项、`runtimeState` 新字段、`startMonitor` 起两个循环、`refresh` 增加归因与守卫、`retryOffline` 的重建分支、`Connect` 改调 `openTailcatChannel` |
| 修改 `internal/core/model.go` / `model_test.go` | `StateTunnelLost` + 标签表 |
| 修改 `internal/core/messages.go` | `MsgTunnelLost`、`MsgTunnelRebuildFailed`、`MsgAdbStuck` |
| 修改 `internal/app/state.go` | `statusDot` 加 `tunnelLost`（warning 色） |
| 修改 `internal/core/manager_test.go` | `fakeOpener` 增加 `TunnelPing`/`attempts`/`pingErr` 与 setter |
| 新建 `internal/core/manager_channel_test.go` | 本特性的用例 |
| 修改 `internal/core/forwarder_test.go` | `fakeSession.DiscoPing` + 探活原语用例 |
| 修改 `docs/architecture.md` | §6 状态表 8→9 + 归因说明、§7.1 流程、§15 决策一行、§16 缺口 3 |
| 修改 `docs/CHANGELOG.md` | 每个行为提交一条 |

`runtimeState` 新增字段（`manager.go:111`）：

```go
	// tunnelMisses counts consecutive unanswered tunnel probes; the channel is
	// called quiet at tunnelMissesBeforeStalled and only a probe that answers
	// again resets it.
	tunnelMisses int
	// stalled marks a channel already reported as tunnelLost, so get-state
	// (which answers from a cache) cannot bring it back and retryOffline
	// rebuilds the channel instead of re-adding a dead ADB entry.
	stalled bool
```

`manager.go:15` 的 const 块加：

```go
	tunnelMissesBeforeStalled = 2
	tunnelPingTimeout         = 2 * time.Second
	tunnelInterval            = 2 * time.Second
	adbRoundTripTimeout       = 4 * time.Second
```

`ManagerOptions` 加 `TunnelInterval time.Duration`（`<= 0` 取 `tunnelInterval`；测试用小值）。

---

## 任务 1：隧道探活原语（`DiscoPing`）

**文件：**
- 修改：`internal/core/forwarder.go:19-27`（接口）、`:44-48`（`ForwardConn`）、`:200-206`（`libSession`）
- 测试：`internal/core/forwarder_test.go:30-60`（`fakeSession`）+ 文件末尾

- [ ] **步骤 1：编写失败的测试**

`fakeSession` 增加 `discoErr error`、`discos int` 与 `func (f *fakeSession) DiscoPing(ctx context.Context) error`（加锁自增后返回 `discoErr`）。追加：

```go
func TestForwardConnTunnelPingReachesTheSession(t *testing.T) {
	f, _, session := newForwarderForTest(t, "")
	conn, err := f.Open(context.Background(), ForwardRequest{Address: sampleAddress, RemotePort: 5555}, nil)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	defer conn.Close(context.Background())

	if conn.TunnelPing == nil {
		t.Fatal("a tailcat channel has no tunnel probe")
	}
	if err := conn.TunnelPing(context.Background()); err != nil {
		t.Fatalf("TunnelPing() error: %v", err)
	}
	if session.discos != 1 {
		t.Fatalf("session disco-pinged %d times, want 1", session.discos)
	}
	session.discoErr = Error("no answer")
	if err := conn.TunnelPing(context.Background()); err == nil {
		t.Fatal("TunnelPing() hid the tunnel's error")
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run TestForwardConnTunnelPingReachesTheSession -v`
预期：FAIL，编译错误 `conn.TunnelPing undefined`。

- [ ] **步骤 3：在 `internal/core/forwarder.go` 实现**

```go
// TailcatSession 增加一个方法（注释必须带上 1.2 的理由）：
	// DiscoPing asks the far end for one round trip. It is the only probe the
	// tailcat client offers after startup: Client.Ping waits on a channel that
	// is closed once, on the first meow ack, so it answers instantly forever
	// after and cannot detect a dead peer.
	DiscoPing(ctx context.Context) error

// ForwardConn 增加一个字段：
	// TunnelPing asks the tunnel for one round trip; nil when this channel has
	// no tunnel of its own (a direct ADB device, or a test opener that leaves
	// it out).
	TunnelPing func(ctx context.Context) error

// libSession 增加：
func (s *libSession) DiscoPing(ctx context.Context) error {
	_, err := s.client.DiscoPing(ctx)
	return err
}
```

`Open` 返回的 `ForwardConn` 里加 `TunnelPing: func(ctx context.Context) error { return session.DiscoPing(ctx) }`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestForward' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core/forwarder.go internal/core/forwarder_test.go
git add internal/core/forwarder.go internal/core/forwarder_test.go
git commit -m "feat: a channel can be asked whether its tunnel still answers"
```

（纯内部能力，无用户可见行为，不写 CHANGELOG。）

---

## 任务 2：端到端往返与判定纯函数

**文件：**
- 创建：`internal/core/manager_channel.go`
- 测试：`internal/core/manager_channel_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestRoundTripVerdicts(t *testing.T) {
	cases := []struct {
		name string
		res  Result
		err  error
		want bool
	}{
		{"clean exit", Result{ExitCode: 0}, nil, true},
		{"unauthorized is still an answer", Result{ExitCode: 1, Output: "error: device unauthorized\n"}, nil, true},
		{"timeout", Result{TimedOut: true, ExitCode: 1}, nil, false},
		{"cancelled", Result{Cancelled: true, ExitCode: 1}, nil, false},
		{"offline", Result{ExitCode: 1, Output: "error: device offline\n"}, nil, false},
		{"the spawn failed", Result{}, Error("无法运行 adb"), false},
	}
	for _, c := range cases {
		if got := roundTripAnswered(c.res, c.err); got != c.want {
			t.Fatalf("%s: roundTripAnswered = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRoundTripAsksOnlyTheChannelSerial(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.adbRoundTrip(context.Background(), os.Args[0], "127.0.0.1:45560"); err != nil {
		t.Fatalf("adbRoundTrip() error: %v", err)
	}
	log := f.runner.commandLog()
	if len(log) != 1 || log[0] != os.Args[0]+" -s 127.0.0.1:45560 shell true" {
		t.Fatalf("round trip ran %v, want one `-s 127.0.0.1:45560 shell true`", log)
	}
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestRoundTrip' -v`
预期：FAIL，编译错误 `undefined: roundTripAnswered` / `m.adbRoundTrip undefined`。

- [ ] **步骤 3：在 `internal/core/manager_channel.go` 实现**

```go
// roundTripAnswered reports whether one `adb shell true` proved the channel
// usable: a clean exit, or the device answering "unauthorized", which is still
// an answer. A timeout, a spawn error or any other failure is no answer.
func roundTripAnswered(res Result, err error) bool

// adbRoundTrip asks the device behind the channel to answer one shell command.
// The adb path and serial are the caller's snapshot, so the check can never
// drift onto another channel mid-flight. The error is user-facing.
func (m *Manager) adbRoundTrip(ctx context.Context, adbPath, serial string) error
```

`adbRoundTrip` 跑 `{"-s", serial, "shell", "true"}`，`Timeout: adbRoundTripTimeout`；`roundTripAnswered` 为假时返回 `Error("设备在 4 秒内没有应答")`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestRoundTrip' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core/manager_channel.go internal/core/manager_channel_test.go
git add internal/core/
git commit -m "feat: an end-to-end adb round trip that a cached get-state cannot fake"
```

---

## 任务 3：快循环（只数 misses，不改状态）

**文件：**
- 修改：`internal/core/manager.go`（`TunnelInterval` 选项、`runtimeState.tunnelMisses`、`startMonitor` 起两个循环）
- 修改：`internal/core/manager_test.go`（`fakeOpener` 装 `TunnelPing`、`pingErr`、`pings`、`setPingErr`/`pingCount`）
- 修改：`internal/core/manager_channel.go`（`probeTunnel`）
- 测试：`internal/core/manager_channel_test.go`

`fakeOpener.Open` 里给每个 `ForwardConn` 装：

```go
		TunnelPing: func(context.Context) error {
			o.mu.Lock()
			defer o.mu.Unlock()
			o.pings++
			return o.pingErr
		},
```

- [ ] **步骤 1：编写失败的测试**

```go
func TestTunnelLoopCountsConsecutiveMisses(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.MonitorInterval = time.Hour // 只测快循环
		o.TunnelInterval = 5 * time.Millisecond
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	f.opener.setPingErr(Error("no answer"))
	time.Sleep(40 * time.Millisecond)

	f.m.mu.Lock()
	misses := f.m.runtimes[f.id].tunnelMisses
	f.m.mu.Unlock()
	if misses < 2 {
		t.Fatalf("tunnelMisses = %d, want at least 2", misses)
	}
	// 快循环不许自己翻状态：状态只能由慢循环写。
	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("state = %v, want online (the fast loop must not write state)", state)
	}
	// 阈值那一刻只记一行日志。
	lines := strings.Join(sessionLogTexts(f.m.Session(f.id)), "\n")
	if strings.Count(lines, "Tailcat 链路探活连续") != 1 {
		t.Fatalf("the threshold was logged %d times, want 1:\n%s", strings.Count(lines, "Tailcat 链路探活连续"), lines)
	}
	f.m.Disconnect(context.Background(), f.id)
}

func TestTunnelProbeResetsOnAnswer(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.MonitorInterval = time.Hour
		o.TunnelInterval = 5 * time.Millisecond
	})
	generation := connectForTest(t, f)
	f.opener.setPingErr(Error("no answer"))
	f.m.probeTunnel(context.Background(), f.id, generation)
	f.m.probeTunnel(context.Background(), f.id, generation)
	f.m.mu.Lock()
	before := f.m.runtimes[f.id].tunnelMisses
	f.m.mu.Unlock()
	if before != 2 {
		t.Fatalf("misses = %d, want 2", before)
	}
	f.opener.setPingErr(nil)
	f.m.probeTunnel(context.Background(), f.id, generation)
	f.m.mu.Lock()
	after := f.m.runtimes[f.id].tunnelMisses
	f.m.mu.Unlock()
	if after != 0 {
		t.Fatalf("misses after an answer = %d, want 0", after)
	}
}

func TestTunnelLoopStopsWithTheMonitor(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) {
		o.MonitorInterval = time.Hour
		o.TunnelInterval = 5 * time.Millisecond
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	f.m.Disconnect(context.Background(), f.id)
	before := f.opener.pingCount()
	time.Sleep(40 * time.Millisecond)
	if after := f.opener.pingCount(); after != before {
		t.Fatalf("the fast loop pinged %d more times after Disconnect", after-before)
	}
}

func TestDirectDeviceIsNeverPinged(t *testing.T) {
	// 直连设备（SaveDevice{DirectADB:true, DirectTarget:"emulator-5554"} + Connect）：
	// 等 40ms 后断言 f.opener.pingCount() == 0。
}
```

测试辅助（放在 `manager_channel_test.go`）：

```go
// connectForTest 连接并返回当前 generation，供直接驱动的检查使用。
func connectForTest(t *testing.T, f *managerFixture) uuid.UUID

// sessionLogTexts 把会话日志拍平成文本，便于逐字断言。
func sessionLogTexts(session Session) []string
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestTunnelLoop|TestTunnelProbe|TestDirectDeviceIsNeverPinged' -v`
预期：FAIL，编译错误 `o.TunnelInterval undefined` / `m.probeTunnel undefined`。

- [ ] **步骤 3：实现**

`startMonitor` 改成两个循环共享一个 cancel：

```go
func (m *Manager) startMonitor(id uuid.UUID, generation uuid.UUID) {
	m.mu.Lock()
	m.stopMonitorLocked(id)
	ctx, cancel := context.WithCancel(m.ctx)
	m.monitors[id] = cancel
	interval := m.monitorInterval
	tunnelEvery := m.tunnelInterval
	m.mu.Unlock()

	go m.pollLoop(ctx, id, generation, interval)     // 现有循环体原样搬进来
	go m.tunnelLoop(ctx, id, generation, tunnelEvery)
}
```

`tunnelLoop` 每 tick 先 `isCurrentLocked` 再调 `m.probeTunnel(ctx, id, generation)`；`probeTunnel`：

```go
// probeTunnel runs one tunnel round trip (bounded by tunnelPingTimeout) and
// maintains the consecutive-miss counter. It never writes session state: the
// poll loop owns that, so a DERP hiccup cannot flip the UI on its own.
func (m *Manager) probeTunnel(ctx context.Context, id, generation uuid.UUID)
```

- 不持锁快照 `runtime`、`runtime.conn`、`runtime.conn.TunnelPing`；`conn == nil` 或 `TunnelPing == nil`（直连、老测试的假 opener）直接返回。
- 用 `context.WithTimeout(ctx, tunnelPingTimeout)` 调探活；返回后再回锁里重新取 `runtime`、验 generation。
- 成功 → `tunnelMisses = 0`；失败 → 自增，并在**恰好等于** `tunnelMissesBeforeStalled` 时 `m.logLocked(id, "Tailcat 链路探活连续 2 次无响应")` + `notify`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestTunnelLoop|TestTunnelProbe|TestDirectDeviceIsNeverPinged|TestMonitorPolls|TestStaleGenerationIgnored' -v`
预期：PASS（`TestMonitorPolls` 只数 `get-state`，不受快循环影响）。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core/
git add internal/core/
git commit -m "feat: probe a tailcat tunnel every two seconds, off the state writer"
```

---

## 任务 4：归因、新状态与恢复

**文件：**
- 修改：`internal/core/model.go`（`StateTunnelLost`）、`internal/core/model_test.go`
- 修改：`internal/core/messages.go`
- 修改：`internal/core/manager.go`（`refresh` 的归因、恢复与守卫）、`internal/core/manager_channel.go`（`channelVerdict`）
- 修改：`internal/app/state.go`（`statusDot`）
- 测试：`internal/core/manager_channel_test.go`、`internal/app/style_test.go` 附近无改动

- [ ] **步骤 1：编写失败的测试**

```go
func TestStateLabels(t *testing.T) { /* 现有用例表加一行：{StateTunnelLost, "Tailcat 断开"} */ }

// 隧道与端到端都不答 → 新状态，不是 ADB 离线。
func TestTunnelQuietAndDeviceSilentShowsTunnelLost(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.opener.setPingErr(Error("no answer"))
	f.runner.setHandler(silentDeviceHandler) // get-state 回 "device"；shell true 回 TimedOut
	quietTunnel(t, f, generation)            // probeTunnel ×2

	f.m.refresh(context.Background(), f.id, generation, true)

	session := f.m.Session(f.id)
	if session.State != StateTunnelLost {
		t.Fatalf("state = %v, want %v", session.State, StateTunnelLost)
	}
	if session.Err != MsgTunnelLost {
		t.Fatalf("Err = %q, want %q", session.Err, MsgTunnelLost)
	}
}

// 隧道不答但设备仍答（DERP 路径抖动）→ 保持在线，不写状态、不加日志。
func TestTunnelQuietButDeviceAnswersStaysOnline(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.opener.setPingErr(Error("no answer"))
	quietTunnel(t, f, generation)
	before := len(sessionLogTexts(f.m.Session(f.id)))

	f.m.refresh(context.Background(), f.id, generation, true)

	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("state = %v, want online", state)
	}
	if after := len(sessionLogTexts(f.m.Session(f.id))); after != before {
		t.Fatalf("the tick added %d log lines, want 0", after-before)
	}
}

// 隧道正常、设备不答 → 仍是 ADB 离线，且只产生一行状态日志。
func TestStuckAdbShowsOfflineNotTunnelLost(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.runner.setHandler(silentDeviceHandler)
	before := len(sessionLogTexts(f.m.Session(f.id)))

	f.m.refresh(context.Background(), f.id, generation, true)

	session := f.m.Session(f.id)
	if session.State != StateOffline {
		t.Fatalf("state = %v, want offline", session.State)
	}
	if session.Err != MsgAdbStuck {
		t.Fatalf("Err = %q, want %q", session.Err, MsgAdbStuck)
	}
	if delta := len(sessionLogTexts(f.m.Session(f.id))) - before; delta != 1 {
		t.Fatalf("the tick logged %d lines, want 1", delta)
	}
}

// 反复 tick 不许抖：状态与日志都不再增加。
func TestSignalLossLogsOneLine(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.runner.setHandler(silentDeviceHandler)
	f.m.refresh(context.Background(), f.id, generation, true)
	after := len(sessionLogTexts(f.m.Session(f.id)))
	for i := 0; i < 3; i++ {
		f.m.refresh(context.Background(), f.id, generation, true)
	}
	if now := len(sessionLogTexts(f.m.Session(f.id))); now != after {
		t.Fatalf("three more ticks added %d log lines, want 0", now-after)
	}
}

// 待授权设备：授权前跑不了端到端往返，隧道连续无响应也必须判掉线。
func TestTunnelLossWhileUnauthorized(t *testing.T) {
	// get-state 回 "error: device unauthorized"（Connect 后状态 unauthorized），
	// 隧道探活失败 ×2 → refresh(deep) 之后状态 = tunnelLost。
}

// 隧道活回来：清 stalled，本 tick 的 get-state 说话。
func TestTunnelRecoveryClearsTheStall(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	f.opener.setPingErr(Error("no answer"))
	f.runner.setHandler(silentDeviceHandler)
	quietTunnel(t, f, generation)
	f.m.refresh(context.Background(), f.id, generation, true) // → tunnelLost

	f.opener.setPingErr(nil)
	f.m.refresh(context.Background(), f.id, generation, true) // 隧道已答，get-state 仍说 device
	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("state = %v, want online after the tunnel answered again", state)
	}
}
```

共用辅助：

```go
// silentDeviceHandler: get-state 回 "device"（adb 缓存在说谎），shell true 超时。
func silentDeviceHandler(_ context.Context, c Command) (Result, error)

// quietTunnel 让隧道连续无响应达到阈值，不走循环，便于确定性断言。
func quietTunnel(t *testing.T, f *managerFixture, generation uuid.UUID)
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestStateLabels|TestTunnelQuiet|TestStuckAdb|TestSignalLoss|TestTunnelLossWhileUnauthorized|TestTunnelRecovery' -v`
预期：FAIL，编译错误 `undefined: StateTunnelLost` / `MsgTunnelLost` / `MsgAdbStuck`。

- [ ] **步骤 3：实现**

1. `model.go`：

```go
	// StateTunnelLost means our Tailcat tunnel stopped answering: the channel
	// is gone even though the local ADB entry may still look fine. It is not
	// needsAction — nothing outside changed our setup, and the app is already
	// rebuilding the channel.
	StateTunnelLost ConnectionState = "tunnelLost"
```
`Label()` 加 `case StateTunnelLost: return "Tailcat 断开"`；`Busy()` 不变。

2. `messages.go`：

```go
	// MsgTunnelLost is the session's explanation while the channel is being
	// rebuilt.
	MsgTunnelLost = "Tailcat 通道已断开，正在重建通道"
	// MsgTunnelRebuildFailed is what the device says after the rebuild budget
	// is spent.
	MsgTunnelRebuildFailed = "Tailcat 通道重建失败，请检查网络后重新连接"
	// MsgAdbStuck means the tunnel answers but the device's ADB does not: it is
	// a device-side outage, not a channel one.
	MsgAdbStuck = "设备在 4 秒内没有应答，ADB 可能已卡住"
```

3. `app/state.go` 的 `statusDot`：`StateUnauthorized, StateOffline, StateNeedsAction, StateTunnelLost` 共用一个 warning 分支。

4. `manager.go` 的 `refresh` 改成 `func (m *Manager) refresh(ctx context.Context, id uuid.UUID, generation uuid.UUID, roundTrip bool)`，并更新三个调用点：`Connect` 的轮询循环与 `retryOffline` 传 `false`（保持连接过程与今天一致，深检交给监视器），poll 循环传 `true`。`TestStaleGenerationIgnored` 里直接调用的那处同步改成 `false`。

`refresh` 的逻辑：

- 先 `get-state`（现有代码）得到 `adbState`。
- `roundTrip == true` 且 `runtime.conn != nil` 且 `adbState == StateOnline` 时，用 `adbRoundTrip` 得到 `answered`；`roundTrip` 为假、直连、或非 online 时 `answered = true`。
- 取 `runtime.tunnelMisses` 与 `runtime.stalled`，产出一个结论（纯函数，便于逐条测）：

```go
// channelVerdict is the one place that decides what one tick means. It is pure
// so every row of the attribution table is a table test.
func channelVerdict(adbState ConnectionState, answered bool, tunnelMisses int, stalled bool) (ConnectionState, string)
```

| 入参 | 返回 |
|---|---|
| `stalled` | `StateTunnelLost, MsgTunnelLost`（`refresh` 先在 `tunnelMisses == 0` 时清 `stalled`，所以走到这一行就是还没恢复） |
| `tunnelMisses >= 2` 且（`adbState != online` 或 `!answered`） | `StateTunnelLost, MsgTunnelLost` |
| `adbState == online && !answered` | `StateOffline, MsgAdbStuck` |
| 其它 | `adbState`，`Err` 按今天的规则（offline 用「本地通道运行中，ADB 尚未上线」，其余空） |

- 写状态前仍要 `isCurrentLocked`；`previous != state` 才写日志（现有行为）；`state == StateOnline` 时清零 `retryCounts`/`nextRetries`（**只在 `stalled == false` 时**，否则重建预算会被 get-state 的假在线清零）。
- 结论是 `StateTunnelLost` 且之前不是时：`stalled = true`、`retryCounts[id] = 0`、`delete(nextRetries, id)`、日志「Tailcat 通道已断开」。

5. `refresh` 的恢复与日志细节：持锁后先看 `runtime.stalled && runtime.tunnelMisses == 0` → 清 `stalled`、日志「Tailcat 通道已恢复」、`notify`，再走普通归因（于是同一 tick 就把状态改回 online/offline，**不重建通道**）。转折进入 `tunnelLost` 时日志行用 `MsgTunnelLost`，**不要**再让 `state.Label()` 记一次——否则一次掉线两条日志。`stalled` 为真时下面的 B 不跑（快照阶段就跳过），免得每次 tick 都白等 4 秒。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestState|TestTunnel|TestStuckAdb|TestSignalLoss|TestMonitor' -v && go test ./internal/app`
预期：PASS。界面的那一行颜色改动没有断言测试：在 `internal/app/screenshots_test.go` 的 `shotCases()` 里加一条 `StateTunnelLost` 的用例，用 `TAILCAT_SHOT_DIR=/tmp/shots go test ./internal/app -run TestScreenshots` 渲出来自己看一眼（AGENTS.md 要求改完界面自己看）。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core/ internal/app/
git add internal/core/ internal/app/ docs/CHANGELOG.md
git commit -m "fix: tell a dead tailcat tunnel apart from a dead adb device"
```

CHANGELOG（当天 `### 修复` 追加）：

```
- 能区分是通道断了还是设备的 ADB 断了：之前 tailcat 通道静默失效时状态会永远停在「ADB 已连接」（`adb get-state` 回答的是本机缓存的通道状态，设备不回应也照样说「在线」）。现在每 2 秒对通道做一次真实往返，每 3 秒（与本地 ADB 同频）让设备真正答一次 `shell`：两层都不答时状态显示新的「Tailcat 断开」，只有设备不答而通道正常时才显示「ADB 离线」。
```

---

## 任务 5：重建通道与预算

**文件：**
- 修改：`internal/core/manager.go:471-610`（`Connect` 的 tailcat 分支改调助手）、`:882-916`（`retryOffline` 加重建分支）
- 修改：`internal/core/manager_channel.go`（`openTailcatChannel`、`rebuildChannel`）
- 测试：`internal/core/manager_channel_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestTunnelLostRebuildsTheChannel(t *testing.T) {
	f := newManagerFixture(t)
	generation := connectForTest(t, f)
	oldSerial := f.m.Session(f.id).Serial
	stallForTest(t, f, generation) // 隧道无响应 ×2 + 设备不答 + refresh(deep)
	f.opener.setPingErr(nil)
	f.runner.setHandler(healthyADB) // 回默认：get-state "device"、shell 干净退出

	f.m.retryOffline(context.Background(), f.id, generation)

	if n := f.opener.openCount(); n != 2 {
		t.Fatalf("channels opened = %d, want 2", n)
	}
	if n := f.opener.closeCount(); n != 1 {
		t.Fatalf("channels closed = %d, want 1", n)
	}
	session := f.m.Session(f.id)
	if session.State != StateOnline {
		t.Fatalf("state = %v, want online", session.State)
	}
	log := strings.Join(f.runner.commandLog(), "\n")
	if !strings.Contains(log, "disconnect "+oldSerial) || !strings.Contains(log, "connect "+session.Serial) {
		t.Fatalf("the rebuild did not replace the ADB entry:\n%s", log)
	}
	f.m.mu.Lock()
	keptGeneration := f.m.generations[f.id] == generation
	stalled := f.m.runtimes[f.id].stalled
	f.m.mu.Unlock()
	if !keptGeneration {
		t.Fatal("the rebuild changed the generation; the monitor that noticed the stall would stop")
	}
	if stalled {
		t.Fatal("the fresh channel is still marked stalled")
	}
}

func TestRebuildBudgetIsBounded(t *testing.T) {
	// 判掉线后置 opener.fail；循环调用 retryOffline 并把 nextRetries 清掉；
	// 断言 attemptCount == 1+6、状态仍是 tunnelLost、Err == MsgTunnelRebuildFailed。
}

func TestRebuildStopsWhenTheGenerationIsGone(t *testing.T) {
	f := newManagerFixture(t)
	connectForTest(t, f)
	if err := f.m.rebuildChannel(context.Background(), f.id, uuid.New()); err != nil {
		t.Fatalf("rebuildChannel() error: %v", err)
	}
	if n := f.opener.attemptCount(); n != 1 {
		t.Fatalf("Open attempts = %d, want 1 (only the original connect)", n)
	}
	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("state = %v, want the original online", state)
	}
}

func TestStalledChannelStopsItsReaders(t *testing.T) {
	// 连上后塞一个采样取消函数（m.samplers[id] = cancel），判掉线并重试一次；
	// 断言 <-ctx.Done() 已关闭。
}
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestTunnelLostRebuilds|TestRebuildBudget|TestRebuildStopsWhen|TestStalledChannelStops' -v`
预期：FAIL，编译错误 `m.rebuildChannel undefined`；补齐签名后 `openCount == 1`（重建没有发生）。

- [ ] **步骤 3：实现**

1. 把 `Connect` 里 `relay, err := NormalizeRelay(...)`（`:529`）到轮询结束（`:598`）的 tailcat 分支搬进：

```go
// openTailcatChannel builds one Tailcat channel and attaches ADB to it. The
// caller owns the generation and the monitor: this returns once ADB accepted
// the channel, or with an error the caller turns into a state. It never starts
// a monitor and never resets a generation.
func (m *Manager) openTailcatChannel(ctx context.Context, id, generation uuid.UUID, profile Profile, adbPath, address string) error
```

`Connect` 保留早先的 `addressLocked` + `!Plausible` 检查（文案与顺序不变，`TestConnectRejectsBadAddress` 依赖它）、直连分支，以及助手返回后的 `m.startMonitor(...)` 与 `_ = m.RefreshDeviceInformation(ctx, id)`；错误仍走 `m.failConnect`。

2. `retryOffline` 的选择逻辑变成三态：`attempt`（现有 `adb connect`）、`rebuild`（状态为 `StateTunnelLost`）、都不做。`rebuild` 分支在重建前先清理读死通道的东西（顺序同 `Disconnect`：`waitDones(m.cancelDeviceTasks(id))`、`m.StopLogcat(id)`、`m.mu.Lock(); m.stopResourceSamplingLocked(id); m.mu.Unlock()`），再 `m.log(id, "重建 Tailcat 通道（n/6）")` 并调 `m.rebuildChannel`。耗尽分支按状态分叉出 `MsgTunnelRebuildFailed` 与现有的「自动恢复已尝试 6 次……」。

3. `rebuildChannel`：

```go
// rebuildChannel replaces a stalled channel with a fresh one, keeping the
// device's generation so the monitor that noticed the stall survives. The old
// listener and its ADB entry go first, in Disconnect's order.
func (m *Manager) rebuildChannel(ctx context.Context, id, generation uuid.UUID) error
```

顺序：`isCurrentLocked` + `runtime != nil` + `profile` 快照（任一不满足直接返回 nil）→ 关旧 `conn` → `adb disconnect <旧 serial>`（`Timeout: 3 * time.Second`，失败只记日志）→ `openTailcatChannel`。助手成功会整体替换 `runtimes[id]`（`stalled`、`tunnelMisses` 随之清零）；助手失败时旧 `runtime.stalled` 必须保持为真，否则下一轮会退回无用的 `adb connect`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/core -run 'TestTunnelLostRebuilds|TestRebuild|TestConnect|TestDisconnect|TestStalledChannel' -v`
预期：PASS。

- [ ] **步骤 5：Commit**

```bash
gofmt -w internal/core/
git add internal/core/ docs/CHANGELOG.md
git commit -m "fix: rebuild a dead tailcat channel instead of re-adding a dead adb entry"
```

CHANGELOG（当天 `### 修复` 追加）：

```
- 「Tailcat 断开」的恢复动作是重建通道：先停掉读死通道的任务、Logcat 与资源采样，再关旧通道与它的本地 ADB 入口，新建 Tailcat 通道并重新 `adb connect`，沿用原来的 6 次退避（2/4/8/16/30/30 秒）；六次之后提示「Tailcat 通道重建失败，请检查网络后重新连接」。通道自己活回来（比如只是短暂断网）时不重建，状态直接回到正常。
```

---

## 任务 6：文档

**文件：**
- 修改：`docs/architecture.md` §6（状态表 8→9 + 归因）、§7.1（两个循环）、§15（一行决策）、§16 第 3 条
- 修改：`docs/CHANGELOG.md`：任务 4/5 已各写一条，这里只补「已知限制」

- [ ] **步骤 1：§6 状态表**

标题改成「9 个状态（Swift 是 7 个，新增 `needsAction` 与 `tunnelLost`）」，表格加一行 `| tunnelLost | Tailcat 断开 | |`；状态机图补一条 `online ──隧道与端到端都不答──► tunnelLost`，并注明不变式：**判活不只看 `get-state`**（它回答的是 adb server 的缓存，tailcat 通道静默死亡时仍回 `device`），归因见 §7.1。

- [ ] **步骤 2：§7.1 流程**

在「轮询 `get-state` → 启动监视」之后补：监视每 2 秒做一次 `tailcat.Client.DiscoPing`（不是 `Client.Ping`——后者只在首次 meow 握手时真实等待，之后永远立即返回成功），连续 2 次无响应算「隧道静默」；每 3 秒（与本地 ADB 同频）`get-state` + 一次 `adb shell true`，两层都不答 → `tunnelLost`（重建通道），只有端到端不答 → `offline`。

- [ ] **步骤 3：§15 决策表**

追加一行：

| 主题 | 决定 | 代价 |
|---|---|---|
| 通道判活 | 不信任 `adb get-state`：2 秒一次 `DiscoPing` 记「连续无响应」，3 秒一次端到端 `adb shell true`；两层都不答判 `tunnelLost`，只有端到端不答判 `offline`，只有探活不答（数据通路仍通）不动状态 | 每设备每 2 秒一次 disco ping（`DiscoPing` 还会顺带发一个 TSMP ping 触发直连发现）、每 3 秒一个 adb 进程与设备上一个 shell 会话；`get-state` 的缓存不可信这事在文档里也写明了，否则以后有人会把 B 去掉。代价：`DiscoPing` 走的是 magicsock 路径，只能证明「对端 magicsock 活着」，数据通路要靠 B |

- [ ] **步骤 4：§16 第 3 条**

补一句：**通道静默失效已覆盖**（见 §15「通道判活」），`listenerLost` 只剩「本地 listener 被外部关掉」这一种。

- [ ] **步骤 5：CHANGELOG「已知限制」**

```
- Tailcat 通道掉线的发现时间：隧道层约 4–7 秒（2 秒一次探活、连续两次 + 一个监视周期），设备 ADB 卡死但通道正常时最多约 7 秒（3 秒一个监视周期 + 4 秒往返上限）。
```

- [ ] **步骤 6：验证并提交**

```bash
gofmt -l .            # 必须无输出
go vet ./...
go test ./... && go test -race ./internal/core
git add docs/architecture.md docs/CHANGELOG.md
git commit -m "docs: record how a tailcat channel is judged alive"
```

---

## 验证清单（收尾前）

- [ ] `gofmt -l .` 无输出、`go vet ./...` 干净、`go test ./...` 全绿、`go test -race ./internal/core ./internal/app` 无数据竞争。
- [ ] `go build ./cmd/tailcat-manager`，`go run ./cmd/tailcat-manager --demo` 能起、无探活噪声。
- [ ] 交叉编译仍成立：`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...`（Windows/macOS 同理）。
- [ ] 真机（需 tailcat 对端）：连上后让对端下线 → 10 秒内显示「Tailcat 断开」→ 对端回来后自动恢复；把设备端 adb 停掉但通道不动 → 显示「ADB 离线」。验收记录写进 `docs/verification.md`。

## 自检

- 规格覆盖：§2.2 归因表 7 行各有测试（`TestTunnelQuietAndDeviceSilentShowsTunnelLost`、`TestStuckAdbShowsOfflineNotTunnelLost`、`TestTunnelQuietButDeviceAnswersStaysOnline`、`TestTunnelLossWhileUnauthorized`、`TestDirectDeviceIsNeverPinged`、`TestStateLabels` 覆盖 offline/unauthorized 不变的行）；§2.2「判掉线后」三条由 `TestTunnelLostRebuildsTheChannel`（顺序 + generation + stalled 清零）与 `TestStalledChannelStopsItsReaders` 钉住；§2.2 恢复由 `TestTunnelRecoveryClearsTheStall` 钉住。
- 类型一致：`TunnelPing`、`DiscoPing`、`probeTunnel`、`channelVerdict`、`openTailcatChannel`、`rebuildChannel`、`roundTripAnswered`、`adbRoundTrip` 在任务 1–5 中签名一致；`runtimeState` 的 `tunnelMisses`/`stalled` 只在任务 3、4、5 使用。
- 测试辅助一致：`connectForTest`、`sessionLogTexts`、`silentDeviceHandler`、`healthyADB`、`quietTunnel`、`stallForTest` 六个名字在任务 3–5 的用例里引用一致，全部定义在 `internal/core/manager_channel_test.go`。
- 硬事实落地：`forwarder.go` 的接口注释写明为什么不能用 `Client.Ping`；`architecture.md` §7.1 同样写明，且 §15 代价栏说明 B 不能省。
- 比例：本计划只写签名、判定表与断言，函数体留给实现者。
