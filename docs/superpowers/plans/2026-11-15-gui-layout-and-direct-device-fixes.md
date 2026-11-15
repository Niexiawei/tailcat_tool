# GUI 布局与直连设备显示修复方案

> 输入：三张截图与 6 条问题反馈。
> 范围：`internal/app`（视图层）+ 一处 `internal/core` 的持久化兜底 + 一条项目规则。
> 目标：侧栏成为「header + 固定列表 + footer」三段式，分组用 `components.Accordion`，连接方式用 `components.Radio`，详情页按 Tailcat / 本地 ADB 分支渲染。

---

## 一、问题与根因

| # | 现象 | 根因 | 位置 |
|---|---|---|---|
| 1 | 侧栏一台设备占满整个高度，行被拉长 | `row.Fill()` —— mygo 的 `Fill()` 同时把宽**和**高设成 100%，行在 Column 里于是各自撑满剩余高度 | `internal/app/state.go` `deviceRow` |
| 2 | 折叠按钮压在「我的设备」标题上 | 侧栏内容没有用 `sidebar.HeaderHeight(c)` 让出浮动标题栏的高度（shell 文档明确要求） | `internal/app/state.go` `SidebarView` |
| 3 | footer 与列表抢空间，和「新建临时连接」叠在一起 | footer 被塞进 `Left` 的 Column 末尾，而不是用 `shell.LeftToolbar` 提供的固定底栏槽位；列表也没有 `ui.Scroll` | `internal/app/state.go`、`shell.go` |
| 4 | 分组是手写的 `sectionHeader` 文字，不能折叠 | 未使用 `components.Accordion` | `internal/app/state.go` `sectionHeader` |
| 5 | 连接方式是两个 ghost 按钮加 `●`/`○` 冒充单选 | 上一轮为避免引入未验证 API 而临时实现；`components.Radio` 可用 | `internal/app/editor.go` `connectionModeRadio` |
| 6 | 直连设备（`127.0.0.1:5554`）详情页仍显示「远端端口 5555 / DERP 中继 / 本地入口」，连接报「没有有效连接凭据」 | 详情页无条件渲染 Tailcat 字段；`saveDirectDevice` 里 `RemotePort` 固定 5555，对直连毫无意义；连接走到 Tailcat 分支说明**保存时 `DirectADB` 没落到磁盘/内存**（凭据错误只会来自 `manager.go:374` 的 Tailcat 路径） | `internal/app/detail.go` `overview`、`internal/core/manager.go` `Connect` |

**关于第 6 条的判断**：报错文案「设备没有有效连接凭据，请编辑设备后重试」只可能由 `manager.go:374` 抛出，即 `Connect` 走了 Tailcat 分支、`profile.DirectADB == false`。因此这不是"渲染没区分"，而是**模式没有真正保存成功**，或保存成功但连接时读到的是旧值。必须先复现（T0），不要只改渲染。

---

## 二、目标结构

侧栏（`shells/sidebar` 的三个槽位各司其职）：

```
┌─ Left（Padding: HeaderHeight, 左侧 8, 右 8）─────────┐
│  [浮动标题栏：折叠按钮 —— 由 shell 绘制]              │
│  ┌─ ui.Scroll().Grow(1) ────────────────────────┐   │
│  │  components.Accordion                        │   │
│  │    ▸ 我的设备      (默认展开)                 │   │
│  │        设备行 × N（固定高度，FillWidth）       │   │
│  │    ▸ 临时设备      (默认展开)                 │   │
│  │        设备行 × N                             │   │
│  └──────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────┘
┌─ LeftToolbar（固定底栏，由 shell 绘制）──────────────┐
│  GUI 1 通道 · adb 可用 · scrcpy 可用                 │
│  [新建临时连接]                                      │
└──────────────────────────────────────────────────────┘
```

内容区同样以 `sidebar.HeaderHeight(c)` 作为上内边距，工具栏不再悬在窗口顶部空白里。

---

## 三、任务分解（TDD，每项一个提交）

### T0 复现并修掉"直连模式没保存下来"（core，先做）

先写会失败的测试，再决定改哪里：

1. `internal/core/manager_devices_test.go`：
   - `TestSaveDirectDeviceSurvivesReload`：`SaveDevice{DirectADB:true, DirectTarget:"127.0.0.1:5554"}` → 用同一个 `ConfigStore` 重建一个 `Manager` → 断言 `Profiles()[0].DirectADB == true` 且 `DirectTarget == "127.0.0.1:5554"`。
   - `TestSaveDirectDeviceKeepsModeOnEdit`：先存直连，再用 `SaveDevice{ID:&id, DirectADB:true, DirectTarget:"127.0.0.1:5556"}` 编辑，断言模式仍是直连、字段被更新。
   - `TestDirectConnectDoesNotAskForCredential`：直连设备调用 `Connect`，断言错误信息**不含**「凭据」，且没有调用凭据库（假凭据库调用计数为 0）。
2. 若测试通过 → 问题在 UI（保存时 `EditorValues.DirectADB` 为 false），T4 换成 `components.Radio` 后一并修；但**仍要**加下面的兜底。
3. 在 `internal/core/config.go` 的加载归一化里加一条兜底（幂等，写回下一层不必单独存盘）：
   ```go
   // A target without the flag comes from an older build or a half-saved
   // form; a target is only meaningful for a direct device.
   for i := range cfg.Profiles {
       if cfg.Profiles[i].DirectTarget != "" && !cfg.Profiles[i].DirectADB {
           cfg.Profiles[i].DirectADB = true
       }
   }
   ```
   测试：`TestConfigInfersDirectModeFromTarget`。
4. 顺带让 `saveDirectDevice` 别再写死 `RemotePort: 5555`——直连没有远端端口这个概念，写 0，并在 `Configuration` 读回来时保持 0（`detail.go` 对直连不展示该字段，见 T5）。

**验收**：`go test ./internal/core` 全绿；磁盘上的 `configuration.json` 里直连设备是 `"directADB": true` 且 `"remotePort": 0`（或省略）。

### T1 侧栏骨架改用 shell 的 header / list / toolbar 三个槽位

`internal/app/shell.go`：

```go
shell := sidebar.NewShell(sidebar.ShellOptions{
    Title:       AppName,
    Theme:       theme.Onevra,
    Left:        vm.SidebarView,
    LeftToolbar: vm.SidebarFooter,   // 新增
    Content:     func(c *ui.Context) { vm.ContentView(c) },
    StateKey:    "tailcat-manager.main",
})
```

`internal/app/state.go`：

```go
func (vm *ViewModel) SidebarView(c *ui.Context) {
    vm.ensureSelection()
    t := c.Theme()
    ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), t.Space(2), 0, t.Space(2)).Children(func() {
        ui.Scroll(c).Grow(1).FillWidth().Children(func() {
            vm.deviceGroups(c)   // T3
        })
    })
}

func (vm *ViewModel) SidebarFooter(c *ui.Context) { … }  // 原 footer + 新建临时连接
```

`internal/app/detail.go` 的 `ContentView`：`ui.Column(c).Fill().Padding(sidebar.HeaderHeight(c), 0, 0, 0)` 再放工具栏与详情。

**验收**：`ui.NewTester` 断言两条——折叠按钮区域不与「我的设备」重叠（用 `Padding` 后的文本基线无法直接断言重叠，改为断言侧栏根节点首个元素是 `Scroll` 且 footer 文本出现在 `LeftToolbar` 的 tester 树里，见 T6）；手动 `--demo` 目视确认。

### T2 设备行改为固定高度

`deviceRow` 与详情里的行：

- `row.Fill()` → `row.FillWidth()`（关键修复，#1）。
- 行内文字保持 `SingleLine()`，副标题也加 `SingleLine()`，避免长设备名把行撑高。
- 全仓扫一遍 `ui.Row(c).Fill()`：只保留真正需要撑满高度的行，剩下改 `FillWidth()`（`detailRow`、`logs` 行、`toolRow` 等）。
- 行高统一：`Padding(t.Space(1), t.Space(1.5))` 保留，两行文字自然高度约 40 DIP，不再拉伸。

**验收**：`TestSidebarDeviceRowsHaveFixedHeight`——用 `ui.NewTester(vm.SidebarView, 240, 700)` 可断言两台设备行的 `Rect().H` 相等且 < 60（tester 提供测量接口时用它；若不便测量则断言 `Scroll` 存在 + 行文本数量，并在 PR 说明里附 `--demo` 截图）。

### T3 分组改用 `components.Accordion`

`ViewModel` 增加两个展开状态（默认都展开，随窗口生命周期存在即可）：

```go
SavedOpen, TemporaryOpen bool   // NewViewModel 里置 true
```

```go
func (vm *ViewModel) deviceGroups(c *ui.Context) {
    components.Accordion(c, func(group *components.AccordionGroup) {
        group.Item("我的设备", &vm.SavedOpen, func() {
            profileList := vm.Manager.Profiles()
            if len(profileList) == 0 {
                components.Text(c, "还没有设备，点下方的“添加设备”。").FontSize(11)
                return
            }
            for _, p := range profileList { vm.deviceRow(c, p, false) }
        })
        group.Item("临时设备", &vm.TemporaryOpen, func() {
            list := vm.Manager.TemporaryProfiles()
            if len(list) == 0 {
                components.Text(c, "临时设备只在本次运行有效。").FontSize(11)
                return
            }
            for _, p := range list { vm.deviceRow(c, p, true) }
        })
    })
}
```

删除 `sectionHeader`。标题里带上数量（`"我的设备 (3)"`）—— 折叠后仍能看出有几台。

**验收**：`TestSidebarUsesAccordion` —— tester 文本包含「我的设备」「临时设备」；点击「我的设备」标题后设备行文本消失（`Accordion` 的 `Item` 自带 `Collapsible` 交互）。

### T4 连接方式改用 `components.Radio`

`internal/app/editor.go` 删掉 `connectionModeRadio` 的手写实现：

```go
ui.Row(c).Gap(12).Children(func() {
    components.Text(c, "连接方式").FontSize(11)
    components.Radio(c, &vm.EditorDraft.Mode, "tailcat", "通过 Tailcat 转发")
    components.Radio(c, &vm.EditorDraft.Mode, "adb", "本地 ADB 直连")
})
```

- `EditorInput.Mode` 保持 `""`/`"tailcat"`/`"adb"`；`Validate` 里把 `""` 视为 `"tailcat"`。
- 打开编辑器时**每次都重置**：`openEditor` 末尾显式 `if vm.EditorDraft.Mode == "" { vm.EditorDraft.Mode = "tailcat" }`，编辑直连设备时置 `"adb"`（已有逻辑保留）。
- `internal/compat/compat_test.go` 补编译期断言：
  ```go
  _ = components.Radio[string]
  _ = components.Accordion
  _ = (*components.AccordionGroup).Item
  _ = sidebar.HeaderHeight
  _ = ui.Scroll
  ```
  再加 `_ = ui.Element.FillWidth`（若类型允许则用 `var e ui.Element; _ = e.FillWidth`）。

**验收**：`TestEditorDefaultsToTailcatMode` 改为断言两个 radio 的可访问名存在；新增 `TestEditorRadioSelectsDirectMode`——点击「本地 ADB 直连」后 `vm.EditorDraft.Mode == "adb"`，且表单出现「ADB 地址（IP:端口 或 USB 序列号）」字段、不再出现「DERP JSON 链接或内容」。

### T5 详情页按连接方式分支，侧栏副标题同步

`internal/app/detail.go` `overview`：

```go
if profile.DirectADB {
    detailRow(c, "连接方式", "本地 ADB 直连")
    detailRow(c, "ADB 地址", orPlaceholder(profile.DirectTarget, "未填写"))
    detailRow(c, "本地入口", orPlaceholder(session.Serial, "未分配"))
} else {
    detailRow(c, "连接方式", "通过 Tailcat 转发")
    detailRow(c, "本地入口", orPlaceholder(session.Serial, "未分配"))
    detailRow(c, "远端端口", itoa(profile.RemotePort))
    detailRow(c, "DERP 中继", relayLabel(vm, profile))
}
detailRow(c, "链路", session.Route)
… 设备型号等不变
```

- 直连时不显示「远端端口」「DERP 中继」（它们对直连没有意义，正是第 6 条里被误读的 5555）。
- 设备标题下方副标题：直连显示 `本地 ADB 直连 · <target>`，Tailcat 显示原「独立连接会话」；临时设备仍显示「临时设备 · 仅本次运行」。
- 侧栏设备行副标题（`deviceRow`）：直连显示 target（如 `127.0.0.1:5554`），Tailcat 显示 `状态 · 端口`。
- 「复制 ADB 入口」在直连且未连接时禁用（没有入口可复制），改为可用文案「复制 ADB 地址」并复制 `DirectTarget`。

**验收**：`TestDetailShowsDirectModeWithoutTailcatFields` —— 插入一个直连 profile 后渲染详情，断言文本含「本地 ADB 直连」「127.0.0.1:5554」，且**不含**「远端端口」「DERP 中继」。

### T6 视图回归与人工验收

1. `go test ./internal/app` 全部通过（新增 4 个视图测试：固定行高、Accordion 折叠、Radio 切换、详情分支）。
2. `gofmt -l .` 无输出、`go vet ./...`、`go test -race ./...`。
3. `go run ./cmd/tailcat-manager --demo` 逐项目视：侧栏不重叠、行高度固定、两组可折叠、footer 固定、编辑器是两个 radio、`--demo` 的直连设备详情不再出现 DERP 行。
4. 真实场景：删掉旧设备，重新添加 `127.0.0.1:5554`（本地 ADB 直连）→ 确认磁盘 `configuration.json` 里 `"directADB": true` → 点连接应提示「ADB 尚未上线」而不是「没有有效连接凭据」。

---

## 四、影响面

| 文件 | 改动 |
|---|---|
| `internal/app/shell.go` | 加 `LeftToolbar`、`StateKey` |
| `internal/app/state.go` | 侧栏三段式、`Scroll`、`Accordion`、`FillWidth`、`deviceGroups`、`SidebarFooter`、副标题分支 |
| `internal/app/detail.go` | `HeaderHeight` 内边距、`overview` 分支、直连副标题 |
| `internal/app/editor.go` | `components.Radio`，删除 `connectionModeRadio` |
| `internal/core/config.go` | 加载时从 `directTarget` 推断 `directADB` |
| `internal/core/manager_devices.go` | `saveDirectDevice` 不再写死 5555 |
| `internal/compat/compat_test.go` | Radio / Accordion / HeaderHeight / Scroll / FillWidth 断言 |
| `internal/app/*_test.go` | 4 个新视图测试 |
| `docs/CHANGELOG.md` | 「修复」4 条 + 「变更」1 条 |
| `AGENTS.md` | 新增 GUI 组件优先级规则（本轮已先写入） |

**风险与取舍**：

- `Accordion` 在侧栏里自带一圈边框，视觉上比裸标题重；若观感不可接受，退路是 `components.Collapsible` 逐组包裹（同样属于 onevra，不算自己实现）。
- `LeftToolbar` 高度由 shell 决定，footer 内容要足够短；超过两行会被裁剪，因此「GUI n 通道」与两个工具状态合并成一行，`新建临时连接` 单独一行。
- `FillWidth` 修好后，选中高亮由 `row.Background(theme.Selection)` 提供，不需要额外撑高。
- 直连设备不再保存 `RemotePort`，属于配置格式变化 → CHANGELOG 标「变更」，并在加载时把旧值的 5555 统一归零（幂等，无迁移脚本）。

---

## 五、规则补充（已写入 `AGENTS.md`）

**GUI 一律优先使用 onevra 提供的 `components`，不要自己拼。** 只有在 onevra 确实没有对应能力时，才允许用 mygo 的 `ui` 基元，并在提交信息里写清"为什么没有现成组件"。常用映射：

| 需要 | 用 |
|---|---|
| 单选（2 个以上互斥选项） | `components.Radio` |
| 分组折叠 / 手风琴 | `components.Accordion` |
| 单块折叠 | `components.Collapsible` |
| 开关 | `components.Switch` |
| 复选 | `components.Checkbox` |
| 分段切换 | `components.Tabs`（`TabsSegmented`） |
| 表格 | `components.Table` / `TableVirtualized` |
| 滚动区 | `components.ScrollView`（或 `ui.Scroll` 于布局层） |
| 提示条 | `components.Badge` / `Toast` |
| 悬停提示 | `components.Tooltip` |
| 下拉 / 选择 | `components.Select` / `NativeSelect` / `Picker` |
| 数值 | `components.NumberInput` / `Stepper` / `Slider` |
| 图标按钮 | `components.IconButton` |
| 加载中 | `components.Spinner` / `Skeleton` |
| 空态 | `components.Text` + 已有 `EmptyState` 风格，不要自绘插图 |

落地检查：写任何新视图前先 `ls $(go env GOMODCACHE)/github.com/oneo-me/onevra/source@*/components`，并用 `go doc` 确认签名；用到的新组件先加进 `internal/compat/compat_test.go`，否则 CI 无法在上游改签名时报警。
