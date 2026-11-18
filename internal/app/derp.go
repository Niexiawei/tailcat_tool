package app

import (
	"net/url"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
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
