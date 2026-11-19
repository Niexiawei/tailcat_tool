# DERP 节点可用性探测 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 subagent-driven-development（推荐）或 executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）跟踪进度。
>
> **前置：** `2026-11-19-derp-map-page.md` 已落地（`/manage/derp` 列表页、`/manage/derp/{id}` 详情页、`RelayMapPreview` 预览缓存）。本计划在它之上加一层「节点到底通不通」。

**目标：** 给每个 DERP 节点加可用性判定——**HTTPS 的 `/derp/latency-check`** 与 **STUN binding request** 两条探测，**两者都做**：列表页每行显示 `可用节点/总节点数`，详情页区域表新增「可用」列、一个节点一个图标。探测**自动定期跑**（默认 5 分钟），列表页与详情页都能「立即检测」。

**架构：** core 新增 `internal/core/relay_probe.go`：`NodeProber` 接口（真实实现 `RelayProber`，测试注入假实现）、`Manager` 的一轮探测（拉地图 → 并发探测每个节点 → 结果只进内存 → `notify()`）、`RelayProbeInterval` 定期循环。app 只读结果画界面，不含任何网络调用。`--demo` 不启用循环，截图用假探测器。

**技术栈：** Go 1.27.3、`net/http` + `crypto/tls`（标准库）、`tailscale.com/net/stun`（已在依赖里，`tailcat` 自己就用它做 netcheck）与 `tailscale.com/net/tlsdial`（`derphttp` 用同一个包处理 `CertName` 与自签证书固定，已在依赖里编译）、`tailscale.com/net/stun/stuntest` 只在测试里用、onevra `components`（`Icon` / `Tooltip` / `Spinner` / `Button` / `Badge`）、`httptest` 与 `stuntest`（环回，不连公网）。

**规格：** 用户 2026-11-20 的口头需求；两处提问已定：**自动定期检测**（不是只在手点时检测）、其余按下列推荐（HTTP 通过即「可用」，STUN 单独作为第二信号；详情页多节点区域一格多图标 + tooltip）。没有独立规格文档。裁决写进本文「决策与代价」，落地后同步进 `docs/architecture.md` §15 与 `docs/ui-and-console.md`。

---

## 全局约束

- 纯 Go、禁止 cgo；**不引入新依赖**（`stun` / `stuntest` / `tlsdial` 都已在模块里，`derphttp` 已经把它们编进二进制）。
- 依赖方向不变；`internal/app` 不发网络请求、不算探测结果，一切经 `Manager`。
- **`tailcat` 仍只出现在 `forwarder.go` / `pinger.go` / `identity.go` / `relay.go`**：`relay_probe.go` 只用 `tailscale.com/net/stun`、`tailscale.com/net/tlsdial`、`crypto/tls` 与 `tailcfg`，不碰 `tailcat`。
- 探测的硬边界，不是建议：
  - 每个节点每个协议 **3 秒**超时（与 tailscale netcheck 的 `stunProbeTimeout` 同量级）；
  - 同时最多 **8 个节点**在探测（一轮总计几百个请求也打不满带宽）；
  - 一轮总时长 **2 分钟**封顶，超时后剩余节点标「未检测」，不把下一轮拖住；
  - HTTPS **必须校验证书**：不给 `RelayProber` 开 `InsecureSkipVerify` 的入口；测试用 `httptest` 自带的 `srv.Client()`。
- 探测目标按 `tailcfg` 的语义从**地图节点自己的字段**拼，不写死端口：
  - HTTPS：`https://<HostName>:<DERPPort>/derp/latency-check`，`DERPPort` 为 0 时用 **443**（`tailcfg.DERPNode.DERPPort` 的注释就是「可选的中继 HTTPS 备用端口，0 表示 443」）；
  - TLS 证书名：`CertName` 非空时用它当 `ServerName`，`sha256-raw:<hex>` 形式按证书指纹固定（与 `derphttp.Client.tlsClient`、`ipn/localapi` 的 `tlsConfigForNode` 一致），两处都用 `tlsdial.SetConfigExpectedCert` / `SetConfigExpectedCertHash`；
  - STUN：`<HostName>:<STUNPort>`，`STUNPort` 为 0 时用 **3478**，为 -1 表示该节点不提供 STUN；
  - `InsecureForTests` **不认**：那是 tailscale 自己的测试钩子，界面不给它开洞；测试靠注入 `Client`（`httptest` 自带的那份）绕过证书。
  - `DERPPort` 不是「明文 DERP 端口」而是 TLS 备用端口：不要另加 `http://` 探测（DERP 客户端只走 TLS）。
  - 探测目标只来自地图里的节点字段，不由界面随意输入（编辑器里改地图是另一回事，那走 `NormalizeRelay`）。
- 结果**只进内存**（`m.relayProbes`），不落配置、不落缓存文件、不写日志内容：一个节点今天通明天不通，落盘就会变成"上次的结论冒充现在"。
- 界面文案是中文，测试逐字断言；不得出现「官方」「DERP 地图」（`TestNoOfficialOrMapWording`）。
- GUI 一律优先 onevra `components`；行式元素 `FillWidth()`，**禁止** `Fill()`。
- 每个提交：`gofmt -w`、`go vet ./...`、`go test ./...` 全绿；用户可见改动同提交写进 `docs/CHANGELOG.md`。
- 分支 `feat/derp-node-probe`。

---

## 审查重点（Review Focus）

每条都要有钉住它的测试，写在对应任务的步骤里。

1. **http 与 stun 是两条独立结论，不能互相顶替**：STUN 通而 HTTPS 不通的节点**不能**当中继用，反过来 STUN 端口被企业网封死的中继仍然可用。所以「可用」只由中继通道决定，STUN 结果单独显示（`TestSTUNOnlyNodeIsJudgedBySTUNAlone`、`TestNodeWithoutSTUNPortSkipsTheUDPCheck`、`TestRelayNodeProbeNeedsTheRelayChannel`）。
2. **探测失败是数据，不是错误**：一个节点超时不该让整轮失败、不该冒一个 toast、更不该把预设删掉（`TestProbeCycleKeepsGoingAfterOneNodeTimesOut`）。
3. **地图拉不到时整轮只有一个错误，且要说清是地图的问题**：URL 打不开与「节点全灭」在界面上必须能区分（`TestProbeCycleReportsAMapThatCannotBeLoaded`）。
4. **同一时刻只有一轮在跑**：定时器与「立即检测」会撞车，第二轮必须直接返回而不是排队堆积（`TestRefreshWhileRunningDoesNotStartASecondCycle`）。
5. **没有间隔就不跑**：`RelayProbeInterval == 0`（现有全部测试、`--demo`）时一条探测都不许发出去；否则 `go test ./...` 会在无网机器上变慢变红（`TestNoProbeLoopWithoutAnInterval`）。
6. **界面不许自己发请求**：列表页与详情页只读 `Manager.RelayProbe`，`internal/app` 里没有任何 `http.`/`net.Dial`（`TestAppSendsNoPacketsOfItsOwn`，源码扫描）。
7. **循环必须能停**：窗口关掉后 goroutine 要跟着 `m.ctx` 退出，不能留一个每 5 分钟醒一次的幽灵（`TestShutdownStopsTheProbeLoop`）。
8. **不能在持锁时打网络**：一轮探测的网络部分在锁外，写结果才上锁；与 `relayUsersLocked` 那类只比字段的写法一致（`go test -race ./internal/core` 兜底，另外 `TestProbeCycleDoesNotBlockOtherManagerCalls`）。
9. **徽标在单元格里真的居中**：`components.Badge` 自带 `AlignSelf(ui.Start)`，放进表格单元格会贴顶（用户截图里的「类型」列）——包一层居中行，并用 `Find` 比较两段文字的中心（`TestBadgesSitCenteredInATableCell`）。
10. **端口与证书名都按 `tailcfg` 语义读，不写死 443**：`DERPPort` 是 TLS 备用端口（0 → 443），`STUNPort` 0 → 3478，`CertName` 可能是一个和 `HostName` 不同的域名或证书指纹——写死 443 会把自建在非标端口的节点全判死，写死 `ServerName` 会让域名前置的节点永远握手失败（`TestProbeURLUsesTheNodesAlternateTLSPort`、`TestProbeTLSConfigPinsTheNodesCertName`、`TestSTUNPortDefaultsTo3478`）。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `internal/core/relay_probe.go`（新建） | `RelayProbeTarget` / `RelayCheck` / `RelayNodeProbe` / `RelayProbe`、`NodeProber` 接口与真实 `RelayProber`、`Available()` 之类纯函数 |
| `internal/core/relay_probe_test.go`（新建） | 两种探测（`httptest` + `stuntest`）、一轮探测、定时循环、并发与停止 |
| `internal/core/manager_relay.go`（修改） | `RelayProbe` / `RelayProbeRunning` / `RefreshRelayProbes`、`m.relayProbes` 读写、地图加载抽成 `loadRelayMap` 供预览与探测共用 |
| `internal/core/manager.go`（修改） | `ManagerOptions.RelayProber`、`RelayProbeInterval`；`NewManager` 在间隔 > 0 时启动循环 |
| `internal/core/manager_relay_test.go`（修改） | 锁外网络、停止、不重叠 |
| `internal/app/derp.go`（修改） | 列表的「可用节点」列 + 「立即检测」、详情页的「可用」图标列与统计行、`cellBadge` 用法 |
| `internal/app/derp_test.go`（修改） | 视图与交互；结果由假探测器喂 |
| `internal/app/table.go`（修改） | `cellBadge`（居中徽标：表格里唯一该出现的徽标写法） |
| `internal/app/derp_probe_test.go`（新建） | 探测相关视图测试与假探测器（`derp_test.go` 已经很长，另开一个） |
| `internal/app/style_test.go`（修改） | `TestAppSendsNoPacketsOfItsOwn` |
| `internal/app/screenshots_test.go`（修改） | 探测后的列表、探测后的详情两个用例 |
| `cmd/tailcat-manager/main.go`（修改） | `RelayProbeInterval: 5 * time.Minute` |
| `docs/*`、`docs/CHANGELOG.md` | 路由说明、决策记录、用户可见条目 |

---

## 任务 1：两种探测（一个节点）

**文件：**
- 创建：`internal/core/relay_probe.go`、`internal/core/relay_probe_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
// http 探测
func TestHTTPProbeAcceptsALatencyCheckReply(t *testing.T)          // 200 → OK，Note 为空（毫秒只做展示，测试不拿它断言）
func TestHTTPProbeRejectsAStatusAMiddleboxWouldSend(t *testing.T)  // 403 → 不通，Note 含 403
func TestHTTPProbeGivesUpAtTheTimeout(t *testing.T)                // 服务端 sleep，注入 50 ms 客户端 → Note 含 超时
func TestHTTPProbeReadsOnlyASmallBody(t *testing.T)                // 8 KiB 上限：返回 200 时不断流读完
// stun 探测
func TestSTUNProbeAcceptsTheTestServersReply(t *testing.T)         // stuntest.Serve → OK
func TestSTUNProbeReportsSilence(t *testing.T)                     // 关掉的 UDP 端 → 不通，Note 含 超时
func TestSTUNProbeIgnoresAForeignTransaction(t *testing.T)         // 回包 txID 不匹配 → 不通
// 可用性判定
func TestSTUNOnlyNodeIsJudgedBySTUNAlone(t *testing.T)             // HTTP 未跑，STUN 通 → Available
func TestNodeWithoutSTUNPortSkipsTheUDPCheck(t *testing.T)         // STUNPort < 0 → STUN.Ran == false
func TestRelayNodeProbeNeedsTheRelayChannel(t *testing.T)          // HTTP 不通 + STUN 通 → 不可用
// 目标按 tailcfg 语义拼（纯函数，不联网）
func TestProbeURLUsesTheNodesAlternateTLSPort(t *testing.T)         // DERPPort 8443 → https://host:8443/derp/latency-check
func TestProbeURLFallsBackToPort443(t *testing.T)                  // DERPPort 0 → https://host:443/derp/latency-check
func TestSTUNPortDefaultsTo3478(t *testing.T)                      // STUNPort 0 → host:3478
func TestProbeTLSConfigPinsTheNodesCertName(t *testing.T)          // CertName=relay.example.net → ServerName 是它，不是 HostName
func TestProbeTLSConfigPinsASelfSignedHash(t *testing.T)           // CertName=sha256-raw:… → ServerName 仍为 HostName，且装了 VerifyConnection
```

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/core -run 'TestHTTPProbe|TestSTUNProbe|TestRelayNodeProbe|TestSTUNOnlyNode|TestNodeWithoutSTUNPort' -v`
预期：FAIL，`undefined: RelayProber` / `undefined: RelayCheck`。

- [ ] **步骤 3：实现**

```go
// RelayProbeTarget is one node as a probe needs it. The fields are the
// tailcfg ones that change where a probe goes: DERPPort is the TLS port (443
// when zero), CertName is the TLS name to expect (HostName when empty, or a
// sha256-raw: pin), STUNPort is 3478 when zero and -1 when the node has none.
type RelayProbeTarget struct {
	RegionID tailcfg.DERPRegionID
	Name     string
	HostName string
	CertName string
	DERPPort int
	STUNPort int
	STUNOnly bool
}

// RelayCheck is one protocol's outcome. A check that could not run (an
// STUN-only node has no relay port; a node without STUNPort has no UDP
// endpoint) says so with Ran=false instead of pretending to fail.
type RelayCheck struct {
	Ran    bool
	OK     bool
	Note   string // 超时 / 服务器返回 403 / 不是 STUN 响应
	Millis int
}

// RelayNodeProbe is both checks on one node.
type RelayNodeProbe struct {
	RegionID  tailcfg.DERPRegionID
	Name      string
	Address   string // HostName:DERPPort，给 tooltip 用
	STUNOnly  bool
	HTTP      RelayCheck
	STUN      RelayCheck
	CheckedAt time.Time
}

// Available reports whether tailcat could use this node: the relay channel is
// what a device connects through, so a STUN-only node is judged by STUN and
// everything else by the HTTPS check. A blocked STUN port says nothing about
// the relay.
func (p RelayNodeProbe) Available() bool

// NodeProber runs the two checks on one node. Tests inject a fake so no packet
// leaves the machine. Failures come back as data, never as an error: one dead
// node is not a broken probe cycle.
type NodeProber interface {
	ProbeNode(ctx context.Context, target RelayProbeTarget) (RelayCheck, RelayCheck)
}

// RelayProber is the real prober.
type RelayProber struct {
	// Client runs the HTTPS check as-is. Tests set it to the httptest server's
	// own client, which trusts the test certificate; nil builds one client per
	// node with that node's CertName (see probeTLSConfig).
	Client *http.Client
	// Endpoint builds the HTTPS check's URL from one target; nil uses
	// https://<HostName>:<DERPPort or 443>/derp/latency-check.
	Endpoint func(target RelayProbeTarget) string
	// Timeout bounds each check; zero means 3 s.
	Timeout time.Duration
}

// probeURL is where the HTTPS check lands. DERPPort is the alternate TLS port
// (443 when zero), not a plaintext port: DERP clients always speak TLS.
func probeURL(target RelayProbeTarget) string

// probeTLSConfig mirrors derphttp.Client.tlsClient: CertName names the
// certificate to expect, and a sha256-raw: CertName pins the certificate
// itself. TLS verification stays on; InsecureForTests is not honored.
func probeTLSConfig(target RelayProbeTarget) *tls.Config

// stunAddress is where the UDP check lands; 0 means 3478.
func stunAddress(target RelayProbeTarget) (string, bool)
```

要点：

- HTTPS：`GET probeURL(target)`，**200–299 才算通**；`io.Copy(io.Discard, io.LimitReader(body, 8<<10))`；错误按 `context.DeadlineExceeded` / `net.Error.Timeout()` 分成「超时」与「连接失败」，正文与 URL 不进 `Note`。
- HTTPS 的传输：`Client` 为空时每个节点建一个 `*http.Transport{Proxy: http.ProxyFromEnvironment, DialTLSContext: ...}`：先 `net.Dialer.DialContext` 出 TCP，再用 `tls.Client(conn, probeTLSConfig(target))` 握手——TLS 配置是**按节点**变的，不能共用一个 `http.Client`。
- STUN：`stunAddress` 给出 `host:port`（0 → 3478，-1 → 不测）；`net.Dialer.DialContext(ctx, "udp", addr)` → `stun.Request(txID)` → 设读超时 → 读 64 KiB 缓冲区 → `stun.Is` 过滤 → `stun.ParseResponse`，**txID 必须相等**；`STUNOnly` 为真时 HTTP 返回 `RelayCheck{Ran: false}`。
- 所有字符串是面向用户的中文：`超时`、`连接失败`、`服务器返回 403`、`不是 STUN 响应`。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: probe a DERP node over HTTPS and STUN`

---

## 任务 2：一轮探测与结果缓存

**文件：**
- 修改：`internal/core/manager_relay.go`、`internal/core/manager.go`
- 修改：`internal/core/relay_probe_test.go`、`internal/core/manager_relay_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestProbeCycleCoversEveryNodeOfAMap(t *testing.T)              // 2 区域 3 节点 → 3 条结果
func TestProbeCycleParsesAnInlineMapWithoutNetwork(t *testing.T)    // 假 prober，无 http
func TestProbeCycleDownloadsAURLMapFirst(t *testing.T)              // 计数 server：一轮 1 次
func TestProbeCycleReportsAMapThatCannotBeLoaded(t *testing.T)       // 500 → Err 含 状态码，Nodes 空
func TestProbeCycleKeepsGoingAfterOneNodeTimesOut(t *testing.T)      // 一个假节点不通，其余照旧
func TestProbeCycleCopiesResultsOut(t *testing.T)                    // 改返回值不影响 manager
func TestProbeCycleDoesNotBlockOtherManagerCalls(t *testing.T)       // 探测挂住时 Profiles() 立即返回
func TestProbeCycleNotifiesTheWindow(t *testing.T)                   // Changed() 收到信号
func TestRefreshWhileRunningDoesNotStartASecondCycle(t *testing.T)   // 假 prober 计数 == 节点数
func TestProbeCycleProbesTheDefaultNodeList(t *testing.T)            // source "" → 用 DefaultNodeListURL
func TestNoProbeLoopWithoutAnInterval(t *testing.T)                  // 0 → 一轮都不跑
func TestProbeLoopRepeatsOnItsInterval(t *testing.T)                 // 20 ms 间隔 → ≥ 2 轮
func TestShutdownStopsTheProbeLoop(t *testing.T)                     // Shutdown 后计数不再增长
func TestProbeCycleGivesUpAfterItsBudget(t *testing.T)               // 注入 20 ms 预算 + 慢 prober → 剩余节点 未测量
```

- [ ] **步骤 2：运行测试验证失败** → `go test ./internal/core -run 'TestProbe' -v`，FAIL（`undefined: Manager.RefreshRelayProbes`）。

- [ ] **步骤 3：实现**

```go
// RelayProbe is one source's whole map as it was last probed.
type RelayProbe struct {
	Source    string
	Nodes     []RelayNodeProbe
	CheckedAt time.Time
	// Err says the map itself could not be loaded, which the UI shows instead
	// of a count: "0/12" would read as twelve dead nodes.
	Err string
}

// Available counts the nodes a device could use. A map that failed to load has
// no count at all.
func (p RelayProbe) Available() (ok, total int)

// RelayProbe answers with the last cycle's result for a source; ok is false
// before the first cycle. An empty source is the default node list. The source
// goes through NormalizeRelay first, exactly like CachedRelayMapPreview, so a
// preset saved with stray whitespace still finds its own result.
func (m *Manager) RelayProbe(source string) (RelayProbe, bool)

// RelayProbeRunning reports whether a cycle is in flight.
func (m *Manager) RelayProbeRunning() bool

// RefreshRelayProbes starts a cycle now. A second call while one runs returns
// at once: a timer and a button must not queue up.
func (m *Manager) RefreshRelayProbes()
```

`ManagerOptions` 增加：

```go
	// RelayProber runs the two node checks; nil means the real prober. Tests
	// inject a fake so no packet leaves the machine.
	RelayProber NodeProber
	// RelayProbeInterval is how often the node list is probed again; 0 runs no
	// loop at all (tests, --demo).
	RelayProbeInterval time.Duration
```

要点：

- 结果存在 `Manager` 的新字段 `relayProbes map[string]RelayProbe`（`m.mu` 保护，在 `manager.go` 的结构体里声明、`NewManager` 里初始化）。
- 一轮 = `probeCycle`：**锁外**取快照（预设来源 + `""`）→ 逐个来源取地图 → 拆成 `RelayProbeTarget` → 8 个 worker 并发 `ProbeNode`（每个再套 3 秒 ctx）→ 全部结束后**一次**上锁写 `m.relayProbes[source]` → 循环结束 `m.notify()`。
- 开始与结束各 `notify()` 一次：开始那次是让列表的「检测中」与转圈能在点下去的那一帧出现，结束那次才是结果。
- 地图加载抽成 `loadRelayMap(ctx, source) (RelayMapPreview, error)`：内联走 `NormalizeRelay`，URL 走 `fetchRelayMap`，`""` 走 `DefaultNodeListURL`。**每轮重新下载**（节点会变），下载结果顺手写进 `m.relayPreviews`，详情页因此免费得到一次预览。
- 轮结束才 `notify()`，不在每个节点写一次：一轮 30 个节点不需要 30 次重绘。
- `RelayProbeInterval > 0` 时 `NewManager` 起 `go m.relayProbeLoop()`：先立刻跑一轮（打开应用就该看到结论），再跟着 ticker 走；`m.ctx.Done()` 退出。
- 周期的 ctx 用 `context.WithTimeout(m.ctx, relayProbeCycleBudget)`，到期后未开始的节点标 `未测量`（`RelayCheck{Ran: false}`），整轮照常写结果。预算与 HTTPS 客户端都用包级变量（`relayProbeBudget = 2 * time.Minute`、`relayProbeClient`，后者与已有的 `relayHTTPClient` 同一写法），测试就地改小、`t.Cleanup` 改回——本轮不做第六个 `ManagerOptions` 字段。
- 现有预览测试（`TestRelayPreviewFetchesAURL` 等）在抽出 `loadRelayMap` 之后必须原样变绿：预览与探测共用同一次下载，不改它的错误文案。

- [ ] **步骤 4：运行测试验证通过**（含 `go test -race ./internal/core`）。

- [ ] **步骤 5：Commit** `feat: probe every DERP node on a timer, in memory`

---

## 任务 3：列表页的「可用节点」列与「立即检测」

**文件：**
- 修改：`internal/app/derp.go`、`internal/app/state.go`（若要缓存行上的统计）、`internal/app/derp_test.go`、`internal/app/derp_probe_test.go`
- 修改：`cmd/tailcat-manager/main.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestDerpListShowsAvailableNodesOverTotal(t *testing.T)     // 3 通 1 死 → 「3/4」
func TestDerpListSaysNotDetectedBeforeTheFirstCycle(t *testing.T) // 「未检测」
func TestDerpListSaysProbeFailedWhenTheMapCannotBeLoaded(t *testing.T) // 「检测失败」+ tooltip 带原因
func TestDerpProbeNowStartsACycle(t *testing.T)                  // 点「立即检测」→ 假 prober 被调用
func TestDerpProbeColumnShowsWhenTheCycleIsRunning(t *testing.T)  // 「检测中」+ 上次检测时间
func TestDerpDefaultNodeRowShowsItsProbeSummary(t *testing.T)     // 默认节点行也有 「3/12」
func TestRelayProbeCellTextNamesTheThreeStates(t *testing.T)      // 纯函数：可用/未检测/检测失败
```

- [ ] **步骤 2：运行测试验证失败**。

- [ ] **步骤 3：实现**

- 顶部一行加 `components.Button(c, "立即检测", components.ButtonSecondary)`（图标 `lucide.RefreshCw`），点击 → `vm.Manager.RefreshRelayProbes()`；右侧 `检测中` + `components.Spinner`（`RelayProbeRunning()` 为真时），否则 `上次检测：2026-11-20 12:03:04`（取任一结果的最大 `CheckedAt`；一次都没有时不出这行）。
- 预设表加一列：`{Title: "可用节点", ID: "probe", Width: 76}`，放在「使用设备」之后。**列宽必须同时挪**：名称 `MinWidth` 150→130、来源 200→140、使用设备 `Width` 88→84、操作 `MinWidth` 200→184，六列合计约 686 DIP，仍在侧栏之后约 720 DIP 的可视区内——否则 操作 那一列被挤出右边缘，用户点不到 删除。单元格内容是纯函数的输出：

```go
// relayProbeCell is the list's 可用节点 column: a count once a cycle has
// produced one, and a word when it has not.
func relayProbeCell(probe core.RelayProbe, found bool) string // "3/4" / "未检测" / "检测失败"
```

  检测失败时再包一个 `components.Tooltip`，正文是 `probe.Err`。
- 默认节点那一行（`derpDefaultSection`）在 `使用设备` 之后追加同样的统计（`Manager.RelayProbe("")`）。
- 行数没变，`Fit` 与 `Unframed` 不变；新列宽度 88 足够放下 `128/128` 与 `未检测`。
- `cmd/tailcat-manager/main.go` 的 `ManagerOptions` 加 `RelayProbeInterval: 5 * time.Minute`；`internal/app/demo.go` **不加**（`--demo` 不联网）。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: show how many DERP nodes answer in the list`

---

## 任务 4：详情页的「可用」图标列

**文件：**
- 修改：`internal/app/derp.go`、`internal/app/derp_probe_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestDerpDetailDrawsOneProbeIconPerNode(t *testing.T)          // 2 节点区域 → 两个图标（按 AccessByRole(RoleImage) 名）
func TestDerpProbeIconSaysAvailableOrNot(t *testing.T)             // 「901a 可用」/「901b 不可用」
func TestDerpProbeIconIsDashedBeforeTheFirstCycle(t *testing.T)    // 「901a 未检测」
func TestDerpDetailProbeTooltipNamesBothChecks(t *testing.T)        // tooltip 文本含 HTTP 与 STUN 两条结论
func TestDerpDetailSummaryCountsRelayAndStun(t *testing.T)          // 「可用 2/3 节点 · STUN 通 1/2」
func TestRelayProbeSummarySkipsNodesWithoutASTUNPort(t *testing.T)  // 无 STUN 端口的节点不进 STUN 分母
func TestDerpDetailProbeNowStartsACycle(t *testing.T)
```

- [ ] **步骤 2：运行测试验证失败**。

- [ ] **步骤 3：实现**

- 区域表在「节点」之后插入一列 `{Title: "可用", ID: "probe", MinWidth: 72}`（在「STUN 端口」之前）：一个节点一个图标，顺序与「节点」列一致（`region.Nodes` 的原序）。
- 图标规则（纯函数，单测）：

```go
// nodeProbeIcon is the detail page's 可用 cell: one icon per node.
//   未检测 → lucide.CircleDashed, 灰
//   可用   → lucide.CircleCheck,  theme.Success
//   不可用 → lucide.CircleX,      theme.Warning
func nodeProbeIcon(theme *ui.Theme, probe RelayNodeProbe, known bool) (func() *ui.SVG, ui.Color, string)
```

  图标带 `.Label(...)`：`901a 可用` / `901a 不可用` / `901a 未检测`（无 label 的图标是装饰，测试与读屏都看不见）。
- 每个图标包一层 `components.Tooltip`，正文：`901a · relay.example.com:443 · HTTP 通 · STUN 超时`；未跑的检查写 `HTTP 未测`。
- 头部（名称 / Badge / 在用设备那一行下面）加统计行：`可用 2/3 节点 · STUN 通 1/2 · 上次检测：2026-11-20 12:03:04`；一次都没检测过时整行不出现。两个分数由纯函数生成（`relayProbeSummary`），单独单测：STUN 的分母只数**有 STUN 端口**的节点，没有 STUN 端口的节点不进分母。
- 「重新获取」旁边加 `components.Button(c, "立即检测", components.ButtonSecondary)` → `RefreshRelayProbes()`；`RelayProbeRunning()` 时显示 `components.Spinner`。
- 地图本身拉不到（`probe.Err != ""`）时，统计行换成 `检测失败：<原因>`，图标全为「未检测」。
- 详情页已有的 `DerpPreviewErr`（本页自己抓地图失败）与探测的 `probe.Err` 是两条独立信息，互不覆盖：前者管「这份地图现在下载不到」，后者管「最近一轮探测没拿到地图」。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `feat: mark each DERP node's health in the detail table`

---

## 任务 5：徽标在表格单元格里居中

用户截图里的「类型」列：`链接` / `JSON` 两个徽标贴在单元格顶部。根因是 `components.Badge` 内部对自己设了 `AlignSelf(ui.Start)`——在表格单元格的居中行里被钉到起始边，不是表格的问题。

**文件：**
- 修改：`internal/app/forms.go`（`cellBadge`）、`internal/app/derp.go`、`internal/app/appcache.go`、`internal/app/logs.go`、`internal/app/logcat.go`
- 修改：`internal/app/forms_test.go` 或 `internal/app/derp_test.go`

- [ ] **步骤 1：编写失败的测试**

```go
func TestBadgesSitCenteredInATableCell(t *testing.T)
// DERP 列表一帧，tt.Find("链接") 与 tt.Find("自建2") 的中心 Y 相差 ≤ 1 DIP。
// 修复前相差约 6 DIP（截图 18-derp-list 的「类型」列）。
```

- [ ] **步骤 2：运行测试验证失败**（记录实测差值，写进测试注释）。

- [ ] **步骤 3：实现**

```go
// cellBadge is components.Badge inside a table cell. Badge aligns itself to
// the start of its row on purpose (it sits at the top of a line of text), and
// a table cell centres its content from the middle: without this wrapper the
// pill rides above the text it describes.
func cellBadge(c *ui.Context, text string, tone components.BadgeTone) ui.Element {
	return ui.Row(c).AlignItems(ui.Center).Children(func() {
		components.Badge(c, text, components.BadgeOptions{Tone: tone})
	})
}
```

- 把 DERP 列表「类型」、默认节点行「内置」、缓存页「已过期」、日志/Logcat 的等级徽标换成 `cellBadge`。
- 不要改 onevra（第三方）：`Badge` 的 `AlignSelf(Start)` 在别处是对的（贴着一行文字的顶）。

- [ ] **步骤 4：运行测试验证通过**。

- [ ] **步骤 5：Commit** `fix: a badge sits level with the text in its table cell`

---

## 任务 6：截图、守卫、文档与变更记录

- [ ] **步骤 1：源码守卫**（`internal/app/style_test.go`）

```go
func TestAppSendsNoPacketsOfItsOwn(t *testing.T) // internal/app 非测试源码里出现 http.Get/Client、net.Dial、stun. 即红
```

- [ ] **步骤 2：截图用例**（`shotCases()`，用假 prober 喂结果，不联网）：
  - `18c-derp-list-probed`：三个预设，`3/4`、`未检测`、`检测失败`；
  - `19b-derp-detail-probed`：一个区域两节点（一个可用、一个不可用）+ 统计行 + 图标列。
  人工看一眼：「可用」列图标与「节点」列文字对齐、没有挤压「STUN 端口」、tooltip 能出来。
- [ ] **步骤 3：文档**
  - `docs/ui-and-console.md` 路由表两条各补一句（列表：`可用节点` 列与「立即检测」；详情：`可用` 图标列、统计行）；
  - `docs/architecture.md` §15 补决策：「节点可用性靠 HTTPS latency-check + STUN binding request，自动每 5 分钟跑一轮，结果只在内存」，代价写「应用开着就每 5 分钟向各节点与 tailcat 的节点列表各发一轮请求，用户可能把它当成本地缓存的结论」；
  - §16 已知缺口补一条：探测间隔写死在 `main.go`，界面里不能改。
- [ ] **步骤 4：CHANGELOG**（当天那一节的 `新增` / `修复`）：
  - `新增`：「DERP 节点列表显示每个地图的可用节点数（如 `7/10`），详情页每个节点一个图标标出是否可用；可用性用 HTTPS 与 STUN 两种探测，默认每 5 分钟自动跑一次，也可以点『立即检测』。」
  - `修复`：「DERP 节点的『类型』徽标不再贴在表格行顶部，和同一行的名称对齐。」

### 测试基础设施（用现成的，别再造）

- **假探测器**：`internal/core/relay_probe_test.go` 里放 `fakeNodeProber`（按 `RelayProbeTarget.Name` 决定通/不通，记调用次数，可阻塞到 `ctx` 结束——`TestProbeCycleGivesUpAfterItsBudget` 与 `TestDerpProbeColumnShowsWhenTheCycleIsRunning` 都要它）。
- **真实环回**：HTTPS 用 `httptest.NewTLSServer` + `srv.Client()` + `Endpoint` 指到 `srv.URL+"/derp/latency-check"`；STUN 用 `tailscale.com/net/stun/stuntest.Serve(t)`，把 `HostName` 设成 `127.0.0.1`、`STUNPort` 设成它的端口。**任何测试都不连公网。**
- **视图**：探测结果是异步写进来的，假探测器是同步的，`ui.NewTester` 走两帧即可（`tt.Frame()` 之间 `time.Sleep` 让 goroutine 落地，与截图用例同一写法）。

### 验收

- `--demo`：不联网（`RelayProbeInterval` 未设）、页面不报错；列表的可用列显示「未检测」。
- 真机：打开应用 5 秒内列表出现 `可用/总数`；把一台节点改成一个不存在的 HostName，下一轮它变红、计数 +1。
- 断网后：URL 预设显示「检测失败」并给出原因；内联预设仍然显示（不依赖网络）。
- 关窗口：`Shutdown` 后不再有探测请求（测试 `TestShutdownStopsTheProbeLoop` 钉住）。
- `gofmt -l .` 无输出；`go vet ./...`；`go test ./...`；`go test -race ./internal/core ./internal/app` 全绿。

### 风险

- `IPv4` / `IPv6` / `STUNTestIP` 这几个「强制地址」字段本轮不读：探测走 `HostName` 的 DNS。一个只填了 `IPv4` 而不给 `HostName` 解析的地图会显示不可用——写进 §16；自建这类节点时把 IP 填进 `HostName` 或配上 `CertName` 即可。
- 节点 `CertName` 的 `sha256-raw:` 指纹错了地图本身就连不上，探测也报不可用——这正是要显示的情形。
- 地图节点多（上限 128 区域 × 32 节点 = 4096）时一轮跑不完 2 分钟预算，剩余节点显示「未检测」；真实地图通常只有几十个节点。
- 探测走用户的网络与代理（`ProxyFromEnvironment`），企业网里 HTTPS 可能被中间设备拦住返回非 2xx：那正是要显示「不可用」的情形。
- 节点 HostName 来自地图，证书校验失败一律判不通；自签证书的自建节点会显示不可用（不复用 `InsecureSkipVerify`，不给界面开这个口子）。

---

## 决策与代价

| 决定 | 代价 |
|---|---|
| 可用 = 中继通道（HTTPS latency-check）通过；STUN 作为第二信号单独显示 | 用户想「两种都通才算好」时要自己看两个数；换来 10 个都能用的节点不会被报成 3/10 |
| TLS 端口取节点的 `DERPPort`（0 → 443）、证书名取 `CertName`（含 `sha256-raw:` 指纹） | 多一个 `tlsdial` 间接依赖与每节点一份 TLS 配置；换来自建在 8443 / 域名前置 / 自签证书节点不会被误判不可用 |
| 不认 `InsecureForTests` | 自签证书节点必须在 `CertName` 里写指纹，不能在探测里关校验；换来界面永远不可能变成一个「谁都能连」的探测工具 |
| STUN-only 节点用 STUN 判定，其余节点不看 STUN | 同一个「可用」在不同节点上含义略有差别；换来不把只能 STUN 的节点一律判死 |
| 自动每 5 分钟跑一轮，应用开着就一直跑 | 不打开 DERP 页也在发请求（每个 URL 预设 1 次地图 + 每节点 2 个包）；换来打开页面即有结论，不用等按钮 |
| 每轮重新下载 URL 地图，不复用上一轮 | 每 5 分钟多一次 HTTP；换来地图新增/下线节点能立刻反映，且详情页免费拿到新预览 |
| 默认节点列表也一起探测 | 每轮向 tailcat 的 `derpmap.json` 发一次请求（与 tailcat 自己连接时取图无关，只是探测用）；换来最常用的那一行也有结论。仍然不落盘 |
| 结果只在内存，重启后重新探测 | 刚启动的几十秒里是「未检测」；换来绝不拿昨天的结论冒充今天 |
| 界面只有一个「立即检测」，没有逐预设的探测按钮 | 想单独查一个预设时要等整轮（8 并发、通常几秒）；换来操作列不会从三个按钮涨到四个（那一列已经 184 DIP 宽） |
| 一轮总预算 2 分钟、8 并发 | 超大地图跑不完，剩余节点「未检测」；换来一次超时风暴不会把下一轮拖住，也不会把进程卡住 |
| 徽标包一层居中行，不改第三方 `Badge` | 多一层容器；换来 onevra 那套「徽标贴文字顶部」的语义在别处仍然成立（`components.Badge` 不该被我们的表格绑架） |
| 探测间隔写死在 `main.go`，不做设置项 | 用户不能调；换来本轮不加一个设置字段、一个设置页控件与一条迁移测试 |
