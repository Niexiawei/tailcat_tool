package app

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"tailscale.com/tailcfg"
)

// DerpEditorDraft is the DERP editor as the dialog submits it. Kind is "json"
// or "url"; Content is the one box whose meaning the tab decides.
type DerpEditorDraft struct {
	ID      *uuid.UUID
	Name    string
	Kind    string
	Content string
	Error   string
}

// derpListPage collects every DERP source in one place: the official map,
// the presets, and the sources devices have used. Navigation goes through
// OpenDerpDetail so Back returns to the list.
func (vm *ViewModel) derpListPage(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Fill().Padding(20).Gap(14).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			if actionButton(c, lucide.Plus, "新建 DERP 地图", components.ButtonPrimary).Clicked() {
				vm.openDerpEditor(&DerpEditorDraft{Kind: "json"})
			}
			components.Text(c, "预设只影响之后选择它的设备").FontSize(11).TextColor(theme.TextMuted)
		})
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(16).Children(func() {
				vm.derpOfficialSection(c)
				vm.derpPresetSection(c)
				vm.derpHistorySection(c)
			})
		})
	})
}

// derpOfficialSection is the map tailcat ships. It has no local source, so it
// offers no detail, edit or delete action: pretending otherwise would show a
// copy that is not the map devices get.
func (vm *ViewModel) derpOfficialSection(c *ui.Context) {
	theme := c.Theme()
	components.Text(c, "官方默认").FontSize(13).Bold()
	count := 0
	for _, profile := range vm.Manager.AllProfiles() {
		if profile.RelaySource == "" {
			count++
		}
	}
	ui.Column(c).FillWidth().Gap(2).Padding(theme.Space(1.5), theme.Space(2)).
		Border(1, theme.Border).Radius(theme.Radius).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			components.Text(c, "官方默认节点").Bold()
			components.Badge(c, "内置", components.BadgeOptions{Tone: components.BadgeNeutral})
			components.Text(c, "tailcat 内置公共地图").FontSize(11).TextColor(theme.TextMuted).Grow(1)
			components.Text(c, derpUsageLabel(count)).FontSize(11).TextColor(theme.TextMuted)
		})
	})
}

// derpPresetSection lists the presets the user saved.
func (vm *ViewModel) derpPresetSection(c *ui.Context) {
	theme := c.Theme()
	components.Text(c, "DERP 地图（预设）").FontSize(13).Bold()
	presets := vm.Manager.RelayPresets()
	if len(presets) == 0 {
		components.Text(c, "还没有保存的 DERP 地图").FontSize(11).TextColor(theme.TextMuted)
		return
	}
	components.Table(c, &vm.DerpTable, derpColumns(), func() {
		for _, preset := range presets {
			preset := preset
			components.TableRow(c, preset.ID.String(), preset.Name, false, func(col int) {
				vm.derpPresetCell(c, col, preset)
			})
		}
	})
}

// derpHistorySection lists sources devices have used, so one of them can be
// saved with a name without retyping the URL or the JSON.
func (vm *ViewModel) derpHistorySection(c *ui.Context) {
	theme := c.Theme()
	components.Text(c, "最近用过").FontSize(13).Bold()
	history := vm.Manager.RelayHistory()
	if len(history) == 0 {
		components.Text(c, "还没有用过的 DERP 来源").FontSize(11).TextColor(theme.TextMuted)
		return
	}
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		for _, entry := range history {
			entry := entry
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(1).Grow(1).Children(func() {
					components.Text(c, entry.Name).SingleLine()
					source := derpSourceSummary(entry.Source)
					components.Tooltip(c, components.Text(c, source).FontSize(11).SingleLine(), entry.Source)
				})
				if components.Button(c, "存为预设", components.ButtonOutline).Clicked() {
					vm.openDerpEditor(&DerpEditorDraft{Content: entry.Source})
				}
			})
		}
	})
}

func derpColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "名称", ID: "name", MinWidth: 150},
		{Title: "类型", ID: "kind", Width: 72},
		{Title: "来源", ID: "source", MinWidth: 200},
		{Title: "使用设备", ID: "usage", Width: 88},
		{Title: "操作", ID: "actions", Width: 168},
	}
}

// derpPresetCell draws one cell of one preset row.
func (vm *ViewModel) derpPresetCell(c *ui.Context, col int, preset core.RelayPreset) {
	theme := c.Theme()
	switch col {
	case 0:
		name := components.Text(c, preset.Name).FontSize(11).SingleLine()
		components.Tooltip(c, name, preset.Name)
	case 1:
		kind := "JSON"
		if kindOfRelay(preset.Source) == core.RelayURL {
			kind = "链接"
		}
		components.Badge(c, kind, components.BadgeOptions{Tone: components.BadgeNeutral})
	case 2:
		source := derpSourceSummary(preset.Source)
		components.Tooltip(c, components.Text(c, source).FontSize(11).SingleLine(), preset.Source)
	case 3:
		usage := derpUsageLabel(len(vm.Manager.RelayPresetUsage(preset.ID)))
		components.Text(c, usage).FontSize(11).TextColor(theme.TextMuted)
	default:
		ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			if components.Button(c, "详情", components.ButtonOutline).Height(22).FontSize(11).Clicked() {
				vm.OpenDerpDetail(preset.ID)
			}
			if components.Button(c, "编辑", components.ButtonOutline).Height(22).FontSize(11).Clicked() {
				vm.editDerpPreset(preset)
			}
			if components.Button(c, "删除", components.ButtonOutline).Height(22).FontSize(11).Clicked() {
				wanted := preset
				vm.DerpConfirmDelete = &wanted
			}
		})
	}
}

// derpUsageLabel says how many devices use one source; a dash keeps an unused
// row from reading as zero devices.
func derpUsageLabel(count int) string {
	if count == 0 {
		return "—"
	}
	return itoa(count) + " 台设备"
}

// kindOfRelay reports the source type without trusting the stored text: any
// unparsable source is reported as inline so the row still renders.
func kindOfRelay(source string) core.RelayKind {
	relay, err := core.NormalizeRelay(source)
	if err != nil {
		return core.RelayInline
	}
	return relay.Kind
}

// derpSourceSummary names a URL by its host and an inline map by its size, so
// two presets pointing at different maps read differently at a glance.
func derpSourceSummary(source string) string {
	if parsed, err := url.Parse(source); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return parsed.Host
	}
	relay, err := core.NormalizeRelay(source)
	if err != nil || relay.Map == nil {
		return "内联 JSON"
	}
	nodes := 0
	for _, region := range relay.Map.Regions {
		nodes += len(region.Nodes)
	}
	return itoa(len(relay.Map.Regions)) + " 个区域 · " + itoa(nodes) + " 个节点"
}

// openDerpEditor opens the dialog with a draft; an empty content and kind means
// a new preset, otherwise the dialog infers the tab from the source.
func (vm *ViewModel) openDerpEditor(draft *DerpEditorDraft) {
	if draft.Kind == "" {
		if kindOfRelay(draft.Content) == core.RelayURL {
			draft.Kind = "url"
		} else {
			draft.Kind = "json"
		}
	}
	vm.DerpEditor = draft
}

// editDerpPreset opens the editor on an existing preset.
func (vm *ViewModel) editDerpPreset(preset core.RelayPreset) {
	id := preset.ID
	vm.openDerpEditor(&DerpEditorDraft{ID: &id, Name: preset.Name, Content: preset.Source})
}

// derpDetailPage shows one preset's map: where it came from, what it holds,
// and which devices use it. The map is downloaded again on every open that has
// no cached copy, because a URL's content changes and a stale copy would look
// current.
func (vm *ViewModel) derpDetailPage(c *ui.Context, rawID string) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		vm.missingDerpPage(c)
		return
	}
	preset, found := vm.derpPreset(id)
	if !found {
		vm.missingDerpPage(c)
		return
	}
	vm.ensureDerpPreview(preset)

	theme := c.Theme()
	preview, previewErr, loading := vm.derpPreviewState(preset.Source)
	usage := derivedRelayUsers(vm.Manager.AllProfiles(), preset.Source)

	ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			components.Text(c, preset.Name).FontSize(18).Bold()
			components.Badge(c, derpKindLabel(preset.Source), components.BadgeOptions{Tone: components.BadgeNeutral})
			if len(usage) > 0 {
				if components.Button(c, derpUsageLabel(len(usage))+"在用", components.ButtonLink).Clicked() {
					if profile := firstRelayUser(vm.Manager.AllProfiles(), preset.Source); profile != nil {
						vm.OpenDevice(profile.ID)
					}
				}
			}
		})
		if len(usage) > 0 {
			components.Text(c, "正在使用："+strings.Join(usage, "、")).FontSize(11).TextColor(theme.TextMuted)
		}

		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			if kindOfRelay(preset.Source) == core.RelayURL {
				source := preset.Source
				components.TextBox(c, &source, components.TextBoxOptions{ReadOnly: true}).FillWidth().Grow(1).Lines(1, 1)
				if components.Button(c, "复制", components.ButtonOutline).Clicked() {
					vm.copyText(preset.Source)
				}
			} else {
				components.Text(c, "内联 JSON").FontSize(11).TextColor(theme.TextMuted)
			}
		})

		components.Text(c, "以下内容是刚刚下载/解析的一份；设备连接时由 tailcat 自行获取，可能已变化。").FontSize(11).TextColor(theme.TextMuted)

		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			if actionButton(c, lucide.RefreshCw, "重新获取", components.ButtonSecondary).Clicked() {
				vm.startDerpPreview(preset)
			}
			if loading {
				components.Spinner(c, components.SpinnerRing)
			}
			if !preview.FetchedAt.IsZero() {
				components.Text(c, "上次获取："+preview.FetchedAt.Format("2006-01-02 15:04:05")).FontSize(11).TextColor(theme.TextMuted)
			}
		})

		if previewErr != "" {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				components.Text(c, previewErr).FontSize(11).TextColor(theme.Warning)
				if components.Button(c, "重试", components.ButtonOutline).Clicked() {
					vm.startDerpPreview(preset)
				}
			})
		}

		if preview.Map != nil {
			vm.derpRegionTable(c, preview)
			components.Collapsible(c, "原始 JSON", &vm.DerpJSONOpen, components.CollapsibleOptions{}, func() {
				vm.derpRawJSON(c, preview.JSON)
			})
		}
	})
}

// derpRegionTable lists the regions of one map. Regions come out of a Go map,
// so the table sorts by ID rather than trusting iteration order.
func (vm *ViewModel) derpRegionTable(c *ui.Context, preview core.RelayMapPreview) {
	ids := make([]tailcfg.DERPRegionID, 0, len(preview.Map.Regions))
	for id := range preview.Map.Regions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	components.Table(c, &vm.DerpRegionTable, []ui.TableColumn{
		{Title: "区域", ID: "region", Width: 68},
		{Title: "代码", ID: "code", Width: 110},
		{Title: "名称", ID: "name", MinWidth: 140},
		{Title: "节点", ID: "nodes", MinWidth: 200},
		{Title: "STUN 端口", ID: "stun", Width: 100},
		{Title: "仅 STUN", ID: "stunonly", Width: 80},
	}, func() {
		for _, id := range ids {
			region := preview.Map.Regions[id]
			components.TableRow(c, id, itoa(int(region.RegionID)), false, func(col int) {
				vm.derpRegionCell(c, col, region)
			})
		}
	})
}

func (vm *ViewModel) derpRegionCell(c *ui.Context, col int, region *tailcfg.DERPRegion) {
	switch col {
	case 0:
		components.Text(c, itoa(int(region.RegionID))).FontSize(11)
	case 1:
		components.Text(c, region.RegionCode).FontSize(11).SingleLine()
	case 2:
		components.Text(c, region.RegionName).FontSize(11).SingleLine()
	case 3:
		components.Text(c, derpNodeLabel(region)).FontSize(11).SingleLine()
	case 4:
		components.Text(c, derpStunPorts(region)).FontSize(11).SingleLine()
	default:
		components.Text(c, derpStunOnly(region)).FontSize(11)
	}
}

func derpNodeLabel(region *tailcfg.DERPRegion) string {
	parts := make([]string, 0, len(region.Nodes))
	for _, node := range region.Nodes {
		label := node.HostName + ":" + itoa(node.DERPPort)
		if node.Name != "" && node.Name != node.HostName {
			label = node.Name + " · " + label
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

func derpStunPorts(region *tailcfg.DERPRegion) string {
	parts := make([]string, 0, len(region.Nodes))
	for _, node := range region.Nodes {
		if node.STUNPort < 0 {
			parts = append(parts, "—")
			continue
		}
		parts = append(parts, itoa(node.STUNPort))
	}
	return strings.Join(parts, ", ")
}

func derpStunOnly(region *tailcfg.DERPRegion) string {
	for _, node := range region.Nodes {
		if !node.STUNOnly {
			return "否"
		}
	}
	return "是"
}

// derpRawJSON shows the canonical map, capped: a 128 KiB text area would drag
// every frame it is drawn in.
const derpRawJSONLimit = 32 * 1024

func (vm *ViewModel) derpRawJSON(c *ui.Context, raw string) {
	shown := raw
	if len(shown) > derpRawJSONLimit {
		shown = truncateUTF8(shown, derpRawJSONLimit)
	}
	components.TextBox(c, &shown, components.TextBoxOptions{ReadOnly: true, Multiline: true}).
		FillWidth().Lines(18, 18)
	if len(raw) > derpRawJSONLimit {
		components.Text(c, "内容过长，只显示前 32 KiB，完整内容可复制来源自行查看").FontSize(11)
	}
}

// truncateUTF8 cuts at a rune boundary so a multi-byte character is not split
// into an invalid sequence.
func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func (vm *ViewModel) missingDerpPage(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(8).Children(func() {
		components.Text(c, "DERP 地图不存在").FontSize(18).Bold()
		components.Text(c, "它可能已被删除。").FontSize(11)
		if components.Button(c, "返回 DERP 地图", components.ButtonPrimary).Clicked() {
			vm.OpenDerp()
		}
	})
}

// derpPreset finds one preset by ID.
func (vm *ViewModel) derpPreset(id uuid.UUID) (core.RelayPreset, bool) {
	for _, preset := range vm.Manager.RelayPresets() {
		if preset.ID == id {
			return preset, true
		}
	}
	return core.RelayPreset{}, false
}

// ensureDerpPreview shows the cached copy when there is one and starts a fetch
// otherwise. It is safe to call every frame.
func (vm *ViewModel) ensureDerpPreview(preset core.RelayPreset) {
	if cached, ok := vm.Manager.CachedRelayMapPreview(preset.Source); ok {
		vm.derpPreviewMu.Lock()
		if vm.DerpPreview == nil || vm.DerpPreview.Source != cached.Source {
			shown := cached
			vm.DerpPreview = &shown
		}
		vm.derpPreviewMu.Unlock()
		return
	}
	vm.derpPreviewMu.Lock()
	loading := vm.DerpPreviewLoading
	haveCurrent := vm.DerpPreview != nil && vm.DerpPreview.Source == preset.Source
	tried := vm.DerpPreviewTried == preset.Source
	vm.derpPreviewMu.Unlock()
	if !loading && !haveCurrent && !tried {
		vm.startDerpPreview(preset)
	}
}

// startDerpPreview always asks the manager again: that is what 重新获取 means.
func (vm *ViewModel) startDerpPreview(preset core.RelayPreset) {
	vm.derpPreviewMu.Lock()
	if vm.DerpPreviewLoading {
		vm.derpPreviewMu.Unlock()
		return
	}
	vm.DerpPreviewLoading = true
	vm.DerpPreviewErr = ""
	vm.DerpPreviewTried = preset.Source
	vm.derpPreviewMu.Unlock()
	go func() {
		preview, err := vm.Manager.RelayMapPreview(context.Background(), preset.Source)
		vm.derpPreviewMu.Lock()
		vm.DerpPreviewLoading = false
		if err != nil {
			vm.DerpPreviewErr = err.Error()
		} else {
			shown := preview
			vm.DerpPreview = &shown
			vm.DerpPreviewErr = ""
		}
		vm.derpPreviewMu.Unlock()
		if vm.Invalidate != nil {
			vm.Invalidate()
		}
	}()
}

// derpPreviewState takes one consistent snapshot, and hides a preview that
// belongs to another preset the user just left.
func (vm *ViewModel) derpPreviewState(source string) (core.RelayMapPreview, string, bool) {
	vm.derpPreviewMu.Lock()
	defer vm.derpPreviewMu.Unlock()
	var preview core.RelayMapPreview
	if vm.DerpPreview != nil && vm.DerpPreview.Source == source {
		preview = *vm.DerpPreview
	}
	return preview, vm.DerpPreviewErr, vm.DerpPreviewLoading
}

// derivedRelayUsers names the devices using one source without asking core for
// a preset ID.
func derivedRelayUsers(profiles []core.Profile, source string) []string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile.RelaySource == source {
			names = append(names, profile.Name)
		}
	}
	return names
}

func firstRelayUser(profiles []core.Profile, source string) *core.Profile {
	for i := range profiles {
		if profiles[i].RelaySource == source {
			return &profiles[i]
		}
	}
	return nil
}

func derpKindLabel(source string) string {
	if kindOfRelay(source) == core.RelayURL {
		return "链接"
	}
	return "JSON"
}

// DerpEditorView is the create/edit dialog. Validation is core's: the dialog
// does not parse JSON itself, so a map the editor accepts is exactly one a
// device can connect with.
func (vm *ViewModel) DerpEditorView(c *ui.Context) {
	if vm.DerpEditor == nil {
		return
	}
	theme := c.Theme()
	open := true
	components.Dialog(c, &open, components.DialogOptions{Label: "DERP 地图"}, func(panel ui.Element) {
		panel.Padding(20).Gap(10).Width(470)
		panel.Children(func() {
			draft := vm.DerpEditor
			if draft == nil {
				open = false
				return
			}
			title := "新建 DERP 地图"
			if draft.ID != nil {
				title = "编辑 DERP 地图"
			}
			components.Text(c, title).FontSize(18).Bold()
			if draft.ID != nil {
				if preset, found := vm.derpPreset(*draft.ID); found {
					if users := len(vm.Manager.RelayPresetUsage(preset.ID)); users > 0 {
						components.Text(c, "这台地图正被 "+itoa(users)+" 台设备使用。修改只影响之后选择它的设备；已保存的设备仍用原来的来源。").
							FontSize(11).TextColor(theme.Warning)
					}
				}
			}
			field(c, "名称", &draft.Name, false)

			tab := 0
			if draft.Kind == "url" {
				tab = 1
			}
			components.Tabs(c, &tab, components.TabsSegmented, "JSON", "链接")
			if tab == 1 {
				draft.Kind = "url"
			} else {
				draft.Kind = "json"
			}
			ui.Column(c).Gap(3).Children(func() {
				components.Text(c, "内容").FontSize(11)
				if draft.Kind == "url" {
					components.TextBox(c, &draft.Content, components.TextBoxOptions{}).
						Label("内容").Placeholder("https://example.com/derpmap.json")
				} else {
					components.TextBox(c, &draft.Content, components.TextBoxOptions{Multiline: true}).
						Label("内容").Lines(12, 12)
				}
			})
			if draft.Error != "" {
				components.Text(c, draft.Error).FontSize(11).TextColor(theme.Warning)
			}
			ui.Row(c).FillWidth().Gap(6).Justify(ui.End).Children(func() {
				if components.Button(c, "取消", components.ButtonOutline).Clicked() {
					vm.DerpEditor = nil
					open = false
				}
				if components.Button(c, "保存", components.ButtonPrimary).Clicked() {
					vm.saveDerpDraft()
					open = vm.DerpEditor != nil
				}
			})
		})
	})
	if !open {
		vm.DerpEditor = nil
	}
}

// saveDerpDraft hands the draft to core and keeps the dialog open on a refusal,
// so the reason is next to the field that caused it.
func (vm *ViewModel) saveDerpDraft() {
	draft := vm.DerpEditor
	if draft == nil {
		return
	}
	_, err := vm.Manager.SaveRelayPreset(core.SaveRelayPresetRequest{
		ID:     draft.ID,
		Name:   draft.Name,
		Source: draft.Content,
	})
	if err != nil {
		draft.Error = err.Error()
		return
	}
	vm.DerpEditor = nil
	vm.Notify("已保存 DERP 地图")
}

// DerpDeleteView asks before removing a preset. The refusal comes from core and
// is shown here rather than disabling the button: whether a device uses the map
// changes outside this page.
func (vm *ViewModel) DerpDeleteView(c *ui.Context) {
	if vm.DerpConfirmDelete == nil {
		return
	}
	theme := c.Theme()
	open := true
	components.Dialog(c, &open, components.DialogOptions{Label: "删除 DERP 地图"}, func(panel ui.Element) {
		panel.Padding(20).Gap(10).Width(440)
		panel.Children(func() {
			preset := vm.DerpConfirmDelete
			if preset == nil {
				open = false
				return
			}
			components.Text(c, "删除「"+preset.Name+"」？").FontSize(18).Bold()
			components.Text(c, "删除后不能恢复；正在被设备使用的地图不会被删除。").FontSize(11).TextColor(theme.TextMuted)
			if vm.DerpDeleteError != "" {
				components.Text(c, vm.DerpDeleteError).FontSize(11).TextColor(theme.Warning)
			}
			ui.Row(c).FillWidth().Gap(6).Justify(ui.End).Children(func() {
				if components.Button(c, "取消", components.ButtonOutline).Clicked() {
					vm.DerpConfirmDelete = nil
					vm.DerpDeleteError = ""
					open = false
				}
				if components.Button(c, "确认删除", components.ButtonSecondary).Clicked() {
					if err := vm.Manager.DeleteRelayPreset(preset.ID); err != nil {
						vm.DerpDeleteError = err.Error()
						return
					}
					vm.Notify("已删除 DERP 地图")
					vm.DerpConfirmDelete = nil
					vm.DerpDeleteError = ""
					open = false
				}
			})
		})
	})
	if !open {
		vm.DerpConfirmDelete = nil
		vm.DerpDeleteError = ""
	}
}
