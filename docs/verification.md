# 真机验收记录（Verification）

本文件是**交付证据**，不是回忆：每条结果都要能对上一份命令输出、一张截图或一个测试名。
**任何一条失败先写进这张表，再开修改**（与 AGENTS.md 的 TDD 顺序一致）。

## 规矩

- 一次验收一段，段落内按场景编号排；不删旧段落，失败后修好的在「结果」里写「修好后见 <提交>」。
- 「证据」写可复现的东西：测试名 + 命令、命令输出片段、截图文件名（放在 `docs/shots/` 或工单里）。
- 只写了自动化测试的，注明 `自动化`；真的在一台设备/一台别人的机器上跑过的，注明 `真机`。
- 没有验的就写「待验」并在「阻塞原因」里说清缺什么，不要留空。

## 环境模板

| 项 | 值 |
|---|---|
| 日期 | |
| OS / 版本 | |
| adb 版本 | |
| scrcpy 版本 | |
| 设备型号 / Android | |
| 应用版本 | |
| 装的是哪个产物 | |

---

## 2026-11-19 · Windows 11 · 0.2.0（`feat/release-closing`）

| 项 | 值 |
|---|---|
| 日期 | 2026-11-19 |
| OS / 版本 | Windows（本机开发机，Go 1.27.3） |
| adb 版本 | Android Debug Bridge 1.0.41 / 34.0.5-10900879 |
| scrcpy 版本 | 未安装（`scrcpy` 不在 PATH） |
| 设备型号 / Android | `sdk_gcar_x86_64`（Automotive SDK built for x86_64），Android 11；adb 里两条入口 `emulator-5554` 与 `127.0.0.1:5555` **是同一台模拟器**（transport_id 44 / 45） |
| 应用版本 | 0.2.0（`mygo.json` 的 `version`） |
| 装的是哪个产物 | `go tool mygo build -platform windows/amd64` → `dist/windows-amd64/Tailcat Manager.exe`（32.4 MB）+ `ghostty-vt.dll`；另有 `tailcat-manager-setup-0.2.0-amd64.exe`（10.8 MB） |

### 场景

| # | 场景 | 期望 | 结果 | 证据 |
|---|---|---|---|---|
| 1 | USB 直连（真机序列号 / `emulator-5554`） | 型号 / Android / Shell 权限自动出现 | **已验证（真机）** | `TAILCAT_LIVE_ADB_USB=emulator-5554 go test ./internal/core -run TestLiveDirectUSBSerial -v` → `state=ADB 已连接 serial="emulator-5554" Android="11"`；资源页、文件页早前也在这台模拟器上实测过 |
| 2 | 本地网络直连（`IP:5555`） | 探活走 `adb connect`，断开时 `adb disconnect` | **已验证（真机）** | `TAILCAT_LIVE_ADB_HOST=127.0.0.1:5555 go test ./internal/core -run TestLiveDirectNetworkAddress -v` → `state=ADB 已连接 Android="11"`（连接前由 `connectDirect` 执行 `adb connect 127.0.0.1:5555`） |
| 3 | Tailcat 真链路（对端跑 `tailcat` server） | 本地端口从 5556 起分配，`adb devices` 出现 `127.0.0.1:5556` | **待验** | 阻塞原因：没有对端 `tailcat` server，也没有 `tc…` 地址 |
| 4 | 设备未授权 | 状态「ADB 待授权」，不重试，提示在设备上确认 | **待验** | 阻塞原因：已有的两台设备都已授权；模拟器上撤销 USB 调试授权要进系统界面 |
| 5 | `adb disconnect 127.0.0.1:5555` | 侧栏变「等待处理」，**不自动重连**，弹对话框 | **core 链路已验证（真机）**，对话框只做了自动化 | `TAILCAT_LIVE_ADB_HOST=127.0.0.1:5555 go test ./internal/core -run TestLiveExternalDisconnectStopsRetry -v` → `Kind:serialDetached`、`state=ADB 需要处理`、`retry count = 0`。对话框本身由 `TestAttentionAggregatedDialogOffersReconnectAll` 等视图测试覆盖，**没有人用鼠标点过** |
| 6 | 同一台设备被映射到别的端口 | 「端口变化」只作线索，不接管 | **待验** | 阻塞原因：需要 Tailcat 通道（场景 3）才能制造"本机端口变了" |
| 7 | `adb kill-server`（多台在线） | **一条**聚合提示，含「全部重新连接」 | **待验（真机）**，逻辑由自动化覆盖 | 阻塞原因：本机只有一台可连设备（`emulator-5554` 与 `127.0.0.1:5555` 是同一台），凑不出两台。聚合规则由 `TestAggregateMergesDetachmentsWithinWindow`、`TestAggregateMarksEveryAffectedDeviceNeedsAction`、`TestAttentionAggregatedDialogOffersReconnectAll` 覆盖 |
| 8 | scrcpy 屏幕控制 | 窗口能开，参数 1280/4M/30fps，关闭不留进程 | **待验** | 阻塞原因：本机没装 scrcpy |
| 9 | 内嵌终端两种模式 | Shell 有提示符；ADB 模式能 `pull` 文件到本地 | **部分** | 包内已确认带 `ghostty-vt.dll`（2.2 MB，与 exe 并排，见 `dist/windows-amd64/.mygo-files`），断网首次打开未人工验 |
| 10 | 两台设备同时在线 | 任务、日志、终端、文件页互不串台 | **待验** | 阻塞原因：同场景 7，只有一台设备 |
| 11 | 重启应用；历史库损坏时能启动并提示 | 设备配置、历史任务都在；坏历史库不影响启动 | **已验证（真机，exe）** | 见下「启动诊断」一节：半截 `configuration.json` + 打不开的 `history.db` 时，程序照常起来并把**两条**问题一起写到 stderr |
| 12 | 断网 / 中继路径 | 批量连通测试给出中继路径与延迟，失败原因可读 | **待验** | 阻塞原因：需要 Tailcat 通道（场景 3） |

### 启动诊断（场景 11 的证据）

半截配置 + 同名的目录占住 `history.db`（SQLite 打不开）：

```sh
mkdir -p /tmp/cfg-broken/history.db
printf '{"profiles": [{"id": "zz' > /tmp/cfg-broken/configuration.json
TAILCAT_CONFIG_DIR=/tmp/cfg-broken "dist/windows-amd64/Tailcat Manager.exe"
```

输出（stderr，程序继续运行，6 秒后由 timeout 结束）：

```
配置读取失败，已使用默认设置：配置文件无法解析：unexpected end of JSON input
历史库无法打开，本次运行不会保存历史：unable to open database file (14)
```

这同时是「P2：一次说清全部问题」的验收证据——旧行为只会留下第一条。

### 发布产物（场景 1–2 用的包）

```
$ "dist/windows-amd64/Tailcat Manager.exe" --version
Tailcat Manager 0.2.0
```

`dist/windows-amd64/` 里 exe 32.4 MB、`ghostty-vt.dll` 2.2 MB、`icon.png`；
`dist/linux-amd64/` 里 `tailcat-manager` 31.7 MB 与 `.deb` / `.rpm` / `.tar.gz` 各约 14.1 MB。

### 这一轮修掉的东西

- 干预弹窗只画一帧就消失、点不到按钮（`AttentionDialog` 的早退守卫）——发现于本轮聚合工作，改法与理由见 `docs/architecture.md` §15「干预弹窗每帧重建」。
- 多台设备一起掉线时弹 N 条提示——见 §15「多设备干预的聚合」。真机验收等有两台设备时补（场景 7）。

### 这一轮没能验的

场景 3、4、6、7、8、10、12，原因见上表「阻塞原因」列。它们的共同点是**要有第二台设备、Tailcat 对端或 scrcpy**；本机的自动化测试与 `--demo` 覆盖了界面与规则，但覆盖不了「真实的 adb 与真实的设备」。

## 复现命令

```sh
# 真机（core 层，不依赖界面）
TAILCAT_LIVE_ADB_USB=emulator-5554 TAILCAT_LIVE_ADB_HOST=127.0.0.1:5555 \
  go test ./internal/core -run TestLive -v

# 打包与版本
go tool mygo build -platform windows/amd64
"dist/windows-amd64/Tailcat Manager.exe" --version

# 启动诊断
TAILCAT_CONFIG_DIR=<半截配置目录> "dist/windows-amd64/Tailcat Manager.exe" 2>&1 | cat
```

`TestLive*` 默认跳过（不设环境变量就 SKIP），所以 CI 不需要设备。
