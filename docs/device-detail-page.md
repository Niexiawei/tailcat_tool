# 设备详情页：功能迭代与页面优化

本文记录设备详情页（尤指新增的「资源」页）这一轮迭代的**实现细节、实测数据与踩过的坑**。
面向后续维护者：想改资源页的数据来源、采样脚本、进程表格或应用信息缓存时，先读这里。

相关文档：

- 架构与状态机：`docs/architecture.md`
- 界面布局、路由与内嵌终端：`docs/ui-and-console.md`
- 依赖版本与体积实测：`docs/build-notes.md`
- 面向用户的变更记录：`docs/CHANGELOG.md`

---

## 1. 页面结构（当前形态）

```
┌ 窗口标题栏 ─ 我的设备 › 设备名（可点返回，⌘[ / ⌘] 后退） ─────────────┐
│ 设备名（20pt）         [编辑] [重新连接] [断开]                        │
│ 本地 ADB 直连 · 127.0.0.1:5555 或 通过 Tailcat 转发 · 独立连接会话     │
│ [概览] [资源] [终端] [日志]                                            │
│ ── 当前页签的内容 ──────────────────────────────────────────────────── │
└───────────────────────────────────────────────────────────────────────┘
```

页签用 `components.Tabs`（`TabsSegmented`），索引具名化为 `detailOverview` / `detailResources` /
`detailTerminal` / `detailLog`（`internal/app/state.go`）。不要再写裸数字：新增页签会让老数字全部错位。

四个页签的职责边界：

| 页签 | 内容 | 是否访问设备 |
|---|---|---|
| 概览 | 连接方式、ADB 入口、型号/代号/Android/序列号/Shell 权限、设备操作 | 只在点按钮时 |
| 资源 | 型号行 + 三个占用圆环 + 按应用分组的进程表 | 页签可见时每 2 秒采样 |
| 终端 | 内嵌终端（设备 Shell / ADB 命令两种模式） | 用户输入命令时 |
| 日志 | 只读文本视图，通道日志与设备任务按时间合并 | 否 |

---

## 2. 资源页：一次采样到底做了什么

### 2.1 采样脚本

`internal/core/sample.go` 的 `resourceSampleScript`，单个 `adb shell` 往返：

```sh
echo @@STAT; IFS= read -r line < /proc/stat; echo "$line"
echo @@MEM;  cat /proc/meminfo
echo @@GPU;  for f in /sys/class/kgsl/kgsl-3d0/gpubusy \
                       /sys/class/kgsl/kgsl-3d0/gpu_busy_percentage \
                       /sys/kernel/gpu/gpu_busy \
                       /sys/class/devfreq/gpufreq/load; do
               IFS= read -r line < $f 2>/dev/null && echo "$line"
             done
echo @@PROC
for d in /proc/[0-9]*; do
  pid=${d#/proc/}; cmd=""
  IFS= read -r cmd < $d/cmdline 2>/dev/null; [ -n "$cmd" ] || continue   # 内核线程直接跳过
  stat=""; IFS= read -r stat < $d/stat 2>/dev/null || continue; [ -n "$stat" ] || continue
  rss=0; while IFS= read -r line; do
    case $line in VmRSS:*) set -- $line; rss=$2; break;; esac
  done < $d/status 2>/dev/null
  echo "@@P $pid|$rss|$stat|$cmd"
done
```

三个必须遵守的约定：

1. **循环里不许 fork 外部命令。** 每个进程执行一次 `head`/`grep`/`tr`，在 509 个进程的真机上就是
   1500 多次 fork。第一版正是这样写的，实测 **21.0 秒/轮**（见 §2.2），超过 8 秒超时 → 每轮都超时 →
   快照永远存不下来 → 页面永远显示「正在读取设备资源…」。只用内建命令（`read` / `echo` / `case`）后
   降到 **0.78 秒**。
2. **先读 `cmdline` 再读别的。** 内核线程没有 cmdline，跳过它们就不必再读 `stat` 与 `status`（省掉 70% 的读取）。
3. **一行一条记录。** `@@P <pid>|<rss KB>|<stat 原文>|<cmdline>`，用 `|` 分隔；解析时 `SplitN(…, 4)`，
   cmdline 里再出现 `|` 也不影响。

一个已知缺陷（已缓解）：Android 的 `sh`（mksh）在 `read` 时会**丢掉 NUL**，所以带参数的守护进程会
粘成一个词（`adbd--root_seclabel=u`）。`commandName` 里按 `:` / `--` / `=` 归一化成 `adbd`；
但 `com.foo:remote` 的后缀要留着（用户靠它区分主进程与子进程），所以 `appKey` 额外切 `:`。

### 2.2 实测数据（`CGO_ENABLED=0`，adb over TCP）

| 项目 | 设备 | 耗时 |
|---|---|---|
| 旧脚本（每进程 3 次 fork，509 进程） | 127.0.0.1:6666（真机） | **21.0 s** |
| 旧脚本的单进程循环（`printf` 版） | 同上 | 7.9 s |
| 新脚本：进程循环 | 同上 | 0.78 s |
| 新脚本：整轮（stat + meminfo + GPU + 进程） | 127.0.0.1:5555（模拟器） | 0.17 s |
| `cat /proc/stat` 单独 | 真机 | 0.20 s |
| `cat /proc/meminfo` 单独 | 真机 | 0.18 s |
| `dumpsys SurfaceFlinger`（整份） | 真机 | 0.82 s / 161 KB |
| `dumpsys SurfaceFlinger \| grep -m1 '^GLES:'` | 真机 | 0.2 s / 一行 |
| `unzip -p <apk> AndroidManifest.xml resources.arsc` | 模拟器 | 0.13 s / 127 KB |
| `adb exec-out cat <apk>`（整包） | — | 视应用 5–100 MB |
| `pm list packages --user all -f <包名>` | 模拟器 | 98 ms / 一行 |
| `pm path <包名>` | 模拟器 | 112 ms |

**这张表有一个陷阱，2026-11-17 才查清**：`unzip -p` 那行的 0.13 s 只量了传输，没量解析。
同一条命令走 `adb shell` 拿回来的字节是被 PTY 改过的（见 §3.2），解析必然失败，
所以真正的耗时从来不是 0.13 s，而是下面那条整包回退。**量带宽不等于量可用性。**

复测方法（只读，不会改设备）：

```sh
adb -s <serial> shell 'cat /proc/stat'
adb -s <serial> shell 'dumpsys SurfaceFlinger | grep -m1 "^GLES:"'
adb -s <serial> shell 'p=$(pm path <pkg> | head -1 | sed "s/package://"); unzip -p "$p" AndroidManifest.xml | wc -c'
```

### 2.3 CPU 百分比：为什么第一帧是「—」

`/proc/stat` 是累计值，只有两次读数才能算差值：

- 整机：`100 * (ΔTotal - ΔIdle) / ΔTotal`，`ΔTotal == 0` 时**不**当作 0%，而是 `Available=false`，
  页面显示「—」（`core.PlaceholderUnknown`）。
- 单进程：`100 * Δ(utime+stime) / ΔTotal`（单核基准，与 `top` 一致，8 核满载可到 800%）。
  PID 复用必须挡住：只有 PID 且 `stat` 第 22 字段 `starttime` 都一致才做差，否则跳过。

上一轮计数器存在 `Manager.sampleCounters`，随设备断开一起清掉。

### 2.4 GPU：有就显示，没有就说没有

Android 没有标准 GPU 使用率接口，只有厂商节点：

| 节点 | 设备族 |
|---|---|
| `/sys/class/kgsl/kgsl-3d0/gpubusy` | 高通（`忙 总`） |
| `/sys/class/kgsl/kgsl-3d0/gpu_busy_percentage` | 部分高通内核（`37%`） |
| `/sys/kernel/gpu/gpu_busy` | 联发科 |
| `/sys/class/devfreq/gpufreq/load` | 三星/Exynos 一类 |

`parseGPUBusy` 认 `N/M`、`N`、`N%` 三种写法，非数字一律 `ok=false`。读不到时 `GPUSample.Available=false`，
页面在 GPU 那格写「不可用」并附一句「该设备不提供 GPU 使用率」，**绝不显示 0%**（0% 会让人去查一个不存在的问题）。

GPU 型号来自 `dumpsys SurfaceFlinger` 的 `GLES: <厂商>, <renderer>, <版本>` 行，与 CPU 型号、核心数一起
**每个连接只读一次**（§2.6）。逐进程 GPU 仍未实现，`GPUSample.PerProcessAvailable` 恒为 `false`。

### 2.5 进程分组与命名

- 分组键 `appKey`：cmdline 第一个 token 的 basename，再切掉 `:suffix`、`--`、`=`。`com.foo` 与
  `com.foo:remote` 归到一组；`/system/bin/surfaceflinger` 归到 `surfaceflinger`。
- 行内显示名 `displayName`：同样归一化但**保留** `:`，所以展开后能看到 `com.foo:remote`。
- 内核线程（cmdline 为空）不进表；扫描阶段就已跳过。
- 组内与组间都按 CPU 降序，成本高的应用排在第一行。

### 2.6 采样的生命周期

```
进入「资源」页  → StartResourceSampling(id)   （幂等，每帧调用无副作用）
页签可见        → 每 resourceInterval(2s) 一轮 SampleResources
离开页签        → StopResourceSampling(id)：只停轮询，**保留上次读数**
断开连接        → Disconnect()：删掉读数、计数器、型号缓存、轮询
```

- 采样循环仿 `startMonitor`：`context.WithCancel`，每轮先验 `isCurrentLocked(id, generation)`，
  跨代（重连）立刻退出；`SampleResources` 内部还会再验一次目标，避免把旧通道的数字写到新通道上。
- 单轮超时 8 秒：`TimedOut` 时**保留上一次快照**并追加「采样超时」告警，而不是清空页面。
- 读数保留到断开的好处是切来切去不会重新拉一遍 `/proc`；代价是重进页面后最多 2 秒的旧数字。
- 型号（CPU 型号/核心数/GPU 型号）在**快照入库并 notify 之后**才去读：`dumpsys` 慢，不能挡在第一帧数字前面。

---

### 2.7 两种视图与筛选

同一份读数两种看法，用 `components.Tabs` 的 `TabsSegmented` 切换（`resourceState.view`）：

| 视图 | 行 | 列 |
|---|---|---|
| 应用（`resourceViewApps`） | 一组一行，点开展开组内进程 | 应用 / 进程 / CPU / GPU / 内存 |
| 进程（`resourceViewProcesses`） | 一进程一行，按 CPU 降序 | 进程（带 PID）/ 应用 / CPU / GPU / 内存 |

两个视图各存一份 `components.TableState`（`tableApps` / `tableProcesses`），列宽与
展开状态互不串味。进程视图不做展开：它本身就是展开后的形态。

筛选框（`resourceState.filter`，`components.TextBox` + `lucide.Search`）同时服务两个视图：

- 空格分词，**每个词都要命中**；大小写不敏感；逐字符**子序列**匹配（`mp` 命中
  `com.google.android.apps.maps`）。纯函数 `matchQuery` 在 `internal/app/filter.go`。
- 应用视图：命中应用名或包名 → 该组保留并列出它的全部进程；只命中某个子进程 →
  组保留、自动展开、只列出命中的进程（查询期间忽略用户的展开状态，清空后恢复）。
- 进程视图：命中进程名、应用名或包名即保留。
- 计数跟着筛选走；空结果显示「没有匹配的应用 / 进程」+「清空筛选即可看到全部」。

## 3. 应用名称与图标：默认不解析

### 3.1 为什么默认不解析

一个应用的名称与图标只能从它的 APK 里读（`pm`/`dumpsys` 都不给），读取成本 = 一次设备侧解压 + 传输。
一台设备常有 120+ 个分组行，全量解析几分钟起步，而且通过 tailcat 时带宽更紧。所以：

- 采样**从不**主动请求元数据（`attachAppMeta` 只查缓存，不排队）；
- 每行在「还没有名字」时提供一个 `PackageSearch` 图标按钮「解析应用信息」，只请求该包；
- 工具栏的「刷新应用信息」是批量动作，一次请求当前列表里的全部应用；
- 解析期间该行显示 `components.Spinner`（加载指示器），有名字后按钮自动消失。

### 3.2 三级取数（**二进制条目必须走 `exec-out`**）

| 级别 | 命令 | 何时使用 |
|---|---|---|
| 0. 找 APK | `pm list packages --user all -f <包名>`，按 `包名` 精确匹配 | 每次读取 |
| 1. 只取条目 | `adb exec-out unzip -p <apk> AndroidManifest.xml` / `resources.arsc` / `<icon 路径>` | 设备有 `unzip`（绝大多数 Android 都有） |
| 2. 整包回退 | `adb exec-out cat <apk>` → 临时文件 → `apkparser.ParseApk`，**只在该包 ≤ 64 MB 时** | 没有 `unzip`，或条目读不出来 |
| 3. 失败即说明原因 | `Pending` / `Ready` / `Failed{原因, Final}` 三种终态 | 包不存在、APK 过大、通道掉了、APK 损坏 |

**为什么找 APK 要看所有用户（这一条也是踩出来的）：** 采样脚本读的是整台设备的
`/proc`，因此能看到**所有用户**（多账户 / 工作资料 / 车载的「司机 / Driver」）的进程；
而 `pm path <包名>` 只按 **shell 自己的用户**（通常是 user 0）回答。应用装在第二个用户下时，
`pm path` 直接报「没有这个包」，于是界面对一个真实存在的应用写「无应用包」——而且因为它是
终态（`Final`），还会一直写下去。实测该模拟器的 `com.niexiawei.lynkcam` 只装在 `UserInfo{10:Driver}`，
user 0 只有它的 `androidTest` 包 `com.niexiawei.lynkcam.test`。
`pm list packages --user all -f <包名>` 一次往返列出所有用户，耗时与 `pm path` 相当（98 ms vs 112 ms）；
它的过滤是**子串**匹配，所以 Go 侧必须按 `包名` 精确比对（`com.foo` 也会返回 `com.foobar`）。
`pm path` 保留为回退：某些设备的 `pm list packages --user all` 不受支持。

**为什么条目必须走 `exec-out`（这一条是踩出来的，别再改回 `shell`）：**
`adb shell` 会给远端命令分配一个 PTY，tty 的 `onlcr` 把每个 `LF` 翻成 `CRLF`。
2026-11-17 在 Android 11 车载模拟器上实测 `com.google.android.gms` 的清单：

| 路径 | 字节数 |
|---|---|
| 条目真实大小（`unzip -l`） | 724620 |
| `adb shell "unzip -p …"` | **739233**（多出的 14613 = 该文件里 LF 的个数） |
| `adb exec-out unzip -p …` | **724620**（逐字节正确） |

`RunToFile` 只保证「不经过文本捕获」，挡不住**设备侧 PTY** 的改写；于是 `apkparser` 对每个
APK 都解析失败，第 1 级一直是一段死代码，每次「解析应用信息」都退化成第 2 级整包下载。
同一台设备实测：gms **14.65 s → 1.78 s**，`car.launcher` **2.63 s → 0.37 s**（图标也回来了）。
第 1 级拿到的条目写进临时文件后再用 `apkparser.ParseXml` + `ParseResourceTable` 解析，
不经过文本捕获，所以也不会被 UTF-8 截断污染。自适应图标（XML 矢量）拿不到位图，
此时只保留名称、图标留空，页面用首字母圆形头像兜底。

### 3.3 缓存（`internal/appmeta`）

- 目录 `configDir/appmeta/`，0700；每个条目一个 JSON 文件，0600；写入 = 临时文件 + rename；拒绝符号链接。
- **有效期 7 天**（`appmeta.TTL`），时间从 `Store.now` 取（测试注入固定时钟）。
- 过期**不清空**：`Lookup` 仍返回旧名字并标 `Stale`，页面在名字后写「7 天前更新」；但**不自动重抓**，
  与「用户主动触发」一致。只有从没缓存过才退回包名。
- 缓存键 = `包名|apk 路径|size|mtime`，与 7 天同时生效：应用更新（size/mtime 变）即视为失效。
- 上限 500 条 / 64 MB，超出按 `FetchedAt` 从旧到新删。
- 下一版要做缓存管理页：`core.AppMetaCache`（`Entries` / `Invalidate` / `Purge`）已经实现并有测试，
  只是还没有 UI 消费者。

### 3.4 解析的状态：三种终态，没有计时器

`core.AppMetaSource` 除了 `Lookup` / `Request` / `Refresh`，还有 `Status` 与 `Forget`：

| 状态 | 含义 | 行内表现 |
|---|---|---|
| `AppMetaUnknown` | 从没问过 | 显示「解析应用信息」按钮 |
| `AppMetaPending` | 已入队或在读 | `components.Spinner`（无障碍名「正在读取应用信息」） |
| `AppMetaReady` | 缓存里有名字（可能过期） | 显示名字；过期时补「7 天前更新」 |
| `AppMetaFailed{Reason, Final}` | 读失败，`Reason` 是用户能读的中文原因 | 「无应用包」/「读取失败」+ Tooltip 全文；`Final` 时不给重试按钮 |

`Final` = **重试无用**：设备上没有这个应用的应用包（`system_server`、`surfaceflinger`、
`android.hardware.*.service` 之类）。这类行占多数，实测 **96 / 120 个分组**
（同一台模拟器）。早先数是「102 / 121」，多出来的那些是用只查 user 0 的 `pm path` 误判的
跨用户应用（见 §3.2），所以这个数字本身也是一个教训：**统计口径错了，结论就会错。**
这类行必须是终态而不是「10 分钟后再试一次」。
设备断开时 `Manager.Disconnect` 调 `Forget(deviceKey)`：重连后新装的应用应当重新有机会被解析。

worker 用 `defer` + `recover` 保证**一定**写终态：一次 panic 只丢掉这一个包的名字，
不会让某一行永远转圈。视图因此**不再持有任何兜底计时器**。

批量刷新（工具栏按钮）按下时记下这批包与起点时刻，每包进入 `Ready`/`Failed` 且时间在起点
之后才计入「正在读取应用信息 x/n」，全部落地就结束；90 秒只是防「某次读取彻底没回来」的兜底。
批量刷新会跳过已知 `Final` 的包，因为它们的问题是读不出而不是读得慢；要让这类行重新确认，
重新连接设备（`Forget`）即可，不必一个个点。

行内的单包请求走 `Store` 的 **urgent 队列**，批量的走普通队列，worker 优先取 urgent：
用户点的那一行不会排在 121 个包的批量刷新后面。

---

## 4. 页面优化清单（现象 → 根因 → 修法 → 守护测试）

| 现象 | 根因 | 修法 | 测试 |
|---|---|---|---|
| 资源页最后一行被窗口裁掉、滚动条贴到窗口底边 | 页签容器用 `Fill()`（占满父内容高度），从标题+页签下方起算后仍按整高排，把设备页 20 DIP 的底部内边距吃掉 | 页签容器改 `Grow(1).MinHeight(0)`；表格滚动区与进程区同样加 `MinHeight(0)` | `TestResourceTableEndsAboveThePageEdge` |
| 日志页同样超出窗口 | 同上（`Fill()` + 内嵌文本框 `Grow(1)`） | 同样改法 | `TestLogPageEndsAboveThePageEdge` |
| CPU 型号太长，把「内存」挤没了 | 型号行三列没有约定宽度，按内容撑开 | 三列 `Grow(1).MinWidth(0)` + `SingleLine()` 省略号 + `components.Tooltip` 显示全文 | `TestHardwareRowSharesTheWidthEqually` |
| 圆环中间显示 100% | `chart.Donut` 中间的数字取「切片总和」，指针扫过环再离开（`Index=-1`）就退回总和 | 放弃环形图的选中语义，改圆环进度 + 自绘中间数字（永远是占用率） | `TestUsageRingCenterShowsTheUsedShare` |
| 圆环太细 | `components.ProgressRing` 线宽固定 `theme.Space(0.75)`，无参数 | 按同样语义自绘，线宽 `直径/9`，保留 `Role(ui.RoleProgress)` + `Range(0,1,值)` | 视图中可见（无像素断言，靠 code review + 组件契约测试） |
| 日志文本框与上方标题同名，读屏念两遍 | 无 | 文本框无障碍标签改为「通道日志与任务内容」 | `TestLogPageEndsAboveThePageEdge`（依赖该标签） |
| **「解析应用信息」一直转圈、点了没反应** | ①二进制条目经 `adb shell` 的 PTY 被 CRLF 污染，快路径恒失败 → 每次整包下载（gms 14.65 s）；②解析结果 fire-and-forget，视图只能靠 `resolveTimeout=2min` 盲等；③该设备 102/121 个进程根本没有应用包 | 条目改 `exec-out`；`Store` 上报 `Pending`/`Ready`/`Failed(原因,Final)`；视图只读状态、失败行写明原因且不再给注定失败的重试 | `TestStoreReadsEntriesThroughExecOut`、`TestStoreReportsNoAppPackageAsFinalFailure`、`TestResolvingSpinnerEndsWhenTheReadFails` |
| 批量刷新按钮灰很久，看不出在读什么 | 结束条件是「30 秒」或「命名数量变了」，而多数行永远不会有名字 | 按终态收敛 + 「正在读取应用信息 x/n」+ 90 秒兜底 | `TestRefreshProgressEndsWhenEveryRowIsSettled`、`TestRefreshSaysHowManyAreDone` |
| 列表要一行行翻才能找到某个进程 | 只有按应用分组一种形态 | 「应用 / 进程」分段器 + 模糊筛选 | `TestResourceViewSwitchesBetweenAppsAndProcesses`、`TestFilterIgnoresExpandedStateWhileTheQueryIsActive` |

**布局经验（值得背下来）**：在 Column 里，`Fill()` 是「占满父内容」而不是「吃掉剩下的空间」。
一个页签/面板的内容要占满剩余高度时用 `Grow(1).MinHeight(0)`；只有列里唯一那个元素才适合 `Fill()`。

---

## 5. 数据流

```
adb shell <采样脚本>            (1 次往返，~0.8s)
      │  @@STAT / @@MEM / @@GPU / @@P 行
      ▼
parseResourceOutput            (纯函数，见 internal/core/sample.go)
      │  cpuTimes / memInfo / gpuPercent / []procEntry
      ▼
buildSnapshot                  (与上一轮 counters 求差 → CPU%、单进程 CPU%)
      │  + 型号（来自 m.hardware，连接期内只读一次）
      ▼
attachAppMeta                  (只查 appmeta 缓存，不排队)
      ▼
m.resources[id] = snapshot; notify()
      ▼
resourcePage → appMetaAction  (读 Manager.AppMetaStatus：Pending 转圈 / Failed 写原因)
      │
      └─ 点击 → Manager.ResolveAppMeta → Store.Refresh（urgent 队列）
                        ├─ exec-out unzip -p 条目 → apkparser
                        ├─ 成功：缓存 + Ready
                        └─ 失败：Failed{原因, Final}；Final 不再重试
      ▼
resourcePage                   (每帧读 Manager.Resources)
      ├─ 型号行（等宽 + Tooltip）
      ├─ 三个圆环（自绘，中间写占用率）
      └─ 表格（按应用分组，行内「解析应用信息」→ Manager.ResolveAppMeta）
                                  │
                                  ▼
                        appmeta.Store（unzip -p 条目 → 解析 → 7 天磁盘缓存）
```

---

## 6. 关键文件

| 想改什么 | 读/改这个 |
|---|---|
| 采样脚本、快照类型、CPU/GPU 计算 | `internal/core/sample.go` |
| /proc 与 GPU 文本的解析 | `internal/core/procfs.go` |
| 进程分组与命名 | `internal/core/resources.go` |
| 应用元数据接口与状态 | `internal/core/appmeta.go`（`AppMetaStatus`）、`internal/core/messages.go`（失败原因文案） |
| APK 取数、解析、缓存、退避 | `internal/appmeta/store.go`、`internal/appmeta/cache.go` |
| 资源页视图（型号行、圆环、表格、行内解析、应用/进程两视图） | `internal/app/resources.go` |
| 模糊筛选的纯函数 | `internal/app/filter.go` |
| 页签常量与视图状态 | `internal/app/state.go`、`internal/app/detail.go` |
| 二进制安全地把命令输出写进文件 | `internal/core/command.go`（`FileRunner`）、`internal/core/runner.go` |
| 演示数据（`--demo`） | `internal/app/demo.go` |

---

## 7. 决策与代价

| 决定 | 代价 |
|---|---|
| 采样只用 shell 内建命令，一行一条记录 | 设备 shell 丢 NUL，带参数的命令会粘连，只能按 `--`/`=` 归一化，仍有极少数名字不精确 |
| CPU/GPU 型号每个连接只读一次，且在首帧数字之后 | 重连后前 1–2 秒型号显示「—」；型号真变了要重连才刷新 |
| 读数保留到断开，离开页签只停轮询 | 重进页面最多看到 2 秒前的数字 |
| 应用名默认不解析，按行/按批触发 | 首次进入时全是包名，需要用户动手 |
| 只取 APK 的三个条目（`unzip -p`） | 依赖设备的 `unzip`；没有时回退整包，慢但可用 |
| 元数据 7 天有效期，过期仍显示旧值、不自动重抓 | 应用改名的设备在缓存过期后仍显示旧名，直到用户点刷新 |
| 占用圆环自绘（组件 `ProgressRing` 线宽不可调） | 上游改进 `ProgressRing` 不会自动惠及这里 |
| 逐进程 GPU 不做 | 只能看到整机 GPU，看不到哪个应用在用 |
| 二进制条目走 `exec-out`，不再经过 `adb shell` | `exec-out` 不经过 shell，路径不能有 shell 展开；APK 路径来自 `pm path`、不含空格，逐项传参即可 |
| 整包回退上限 64 MB | 没有 `unzip` 且 APK > 64 MB 的设备上，这类应用只能显示包名并说明原因 |
| 「没有应用包」记为终态，断开连接时才清 | 用户新装应用后，旧进程行要等重连或重启才会重新有机会被解析 |
| 行内单包请求优先于批量 | urgent 队列被连续点击时批量会短暂饿着；点击的人就在现场 |
| 筛选时忽略用户的展开状态 | 查询期间不能手动折叠/展开（点行无效），清空后恢复原样 |
| 一个筛选框同时服务两个视图 | 视图之间共享查询，切换视图不会丢问题，也无法为两个视图各存一个查询 |

---

## 8. 已知缺口

1. **逐进程 GPU 未实现**：`GPUSample.PerProcessAvailable` 恒为 false。
2. **缓存管理页未做**：`core.AppMetaCache` 的三个方法已就绪，缺界面。
3. **矢量（自适应）图标**：没有光栅化能力，退回首字母头像。
4. **窗口很矮时**（< ~450 DIP）型号行 + 圆环占满，表格被压到 0 高；页面本身不整体滚动。
5. **同组多进程的 GPU 归属**：整机 GPU 有值但无法分摊到进程，表格里是「不可用」。

---

## 9. 验证方式

自动化（不碰真设备）：

```sh
go test ./internal/core/ -run 'TestSampleResources|TestGroupProcesses|TestParse' -v
go test ./internal/appmeta/ -v          # 含现场构造的最小 APK 与 unzip 快路径
go test ./internal/app/  -run 'TestResource|TestProcessTable|TestHardware|TestUsageRing' -v
go test -race ./internal/core ./internal/app ./internal/appmeta
```

视图层用 `ui.NewTester`：断言文本、`Find` 取元素矩形（验证滚动区不越界、三列等宽）、
`Move` 制造指针进出（验证圆环中间数字不退回 100%）。

真机/模拟器上复测性能时，**只用只读命令**（`cat` / `ls` / `pm path` / `dumpsys` / `unzip -p`），
不要在别人的设备上做写入或删除。
