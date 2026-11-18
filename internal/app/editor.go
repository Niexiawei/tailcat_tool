package app

import (
	"context"
	"strconv"
	"strings"
	"unicode"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/oneo-me/onevra/source/components"
)

// EditorInput is the raw text of the device form.
type EditorInput struct {
	Name string
	// Mode is "tailcat" (default) or "adb" for a plain local ADB target.
	Mode         string
	DirectTarget string
	Address      string
	LocalPort    string
	RemotePort   string
	RelayTab     int
	// RelaySource is the DERP map URL or inline JSON, used when RelayTab is 1.
	RelaySource string
	// RelayPick is the history entry the picker shows, by Source.
	RelayPick string
}

// EditorValues are the validated values the manager receives.
type EditorValues struct {
	Name string
	// DirectADB means the device is reached over ADB without Tailcat.
	DirectADB     bool
	DirectTarget  string
	Address       string
	PreferredPort *int
	RemotePort    int
	RelaySource   string
}

// Validate turns form text into manager input, with the same wording the app
// shows inline.
func (in EditorInput) Validate() (EditorValues, error) {
	var out EditorValues
	out.Name = strings.TrimSpace(in.Name)
	if out.Name == "" || len([]rune(out.Name)) > 80 {
		return out, core.Error("设备名称需要 1–80 个字符")
	}
	if in.Mode == "adb" {
		out.DirectADB = true
		out.DirectTarget = strings.TrimSpace(in.DirectTarget)
		if out.DirectTarget == "" {
			return out, core.Error("请填写 ADB 地址（IP:端口 或 USB 序列号）")
		}
		if strings.IndexFunc(out.DirectTarget, unicode.IsSpace) >= 0 {
			return out, core.Error("ADB 地址不能包含空格或换行")
		}
		if core.Plausible(out.DirectTarget) {
			return out, core.Error("这是一个 Tailcat 地址，请改用“通过 Tailcat 转发”")
		}
		return out, nil
	}
	out.Address = strings.TrimSpace(in.Address)
	if !core.Plausible(out.Address) {
		return out, core.Error("请填写以 tc 开头的完整 Tailcat 连接地址")
	}
	if port := strings.TrimSpace(in.LocalPort); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil {
			return out, core.Error("端口需要填写整数")
		}
		if value < 1 || value > 65535 {
			return out, core.Error("端口必须在 1–65535 之间；本地端口留空表示自动分配")
		}
		out.PreferredPort = &value
	}
	remote, err := strconv.Atoi(strings.TrimSpace(in.RemotePort))
	if err != nil {
		return out, core.Error("端口需要填写整数")
	}
	if remote < 1 || remote > 65535 {
		return out, core.Error("端口必须在 1–65535 之间；本地端口留空表示自动分配")
	}
	out.RemotePort = remote
	// RelayTab 0 is the official map, so its source stays empty.
	if in.RelayTab != 0 {
		out.RelaySource = strings.TrimSpace(in.RelaySource)
		if out.RelaySource == "" {
			return out, core.Error("请填写 DERP JSON 链接或内容，或选择默认节点")
		}
	}
	return out, nil
}

// openEditor fills the form from a device, or with defaults for a new one.
func (vm *ViewModel) openEditor(request *EditorRequest) {
	vm.PendingEditor = request
	vm.EditorError = ""
	draft := EditorInput{RemotePort: "5555", Mode: "tailcat"}
	if request.Profile != nil {
		profile := *request.Profile
		if profile.DirectADB {
			draft.Mode = "adb"
			draft.DirectTarget = profile.DirectTarget
		}
		draft.Name = profile.Name
		draft.RemotePort = strconv.Itoa(profile.RemotePort)
		if profile.PreferredPort != nil {
			draft.LocalPort = strconv.Itoa(*profile.PreferredPort)
		}
		if profile.RelaySource != "" {
			draft.RelaySource = profile.RelaySource
			draft.RelayTab = 1
			// The picker shows the choice when it knows the source, whether it
			// was saved as a preset or only used before.
			for _, preset := range vm.Manager.RelayPresets() {
				if preset.Source == profile.RelaySource {
					draft.RelayPick = profile.RelaySource
					break
				}
			}
			if draft.RelayPick == "" {
				for _, entry := range vm.Manager.RelayHistory() {
					if entry.Source == profile.RelaySource {
						draft.RelayPick = profile.RelaySource
						break
					}
				}
			}
		}
		if address, err := vm.Manager.AddressForEditing(profile.ID); err == nil {
			draft.Address = address
		} else {
			vm.EditorError = err.Error()
		}
	}
	vm.EditorDraft = draft
}

// submitEditor validates and saves the form.
func (vm *ViewModel) submitEditor() {
	request := vm.PendingEditor
	if request == nil {
		return
	}
	values, err := vm.EditorDraft.Validate()
	if err != nil {
		vm.EditorError = err.Error()
		return
	}
	var id *uuid.UUID
	if request.Profile != nil {
		existing := request.Profile.ID
		id = &existing
	}
	if request.Temporary {
		deviceID, saveErr := vm.Manager.SaveTemporaryDevice(core.SaveTemporaryDeviceRequest{
			ID: id, Name: values.Name, Address: values.Address,
			PreferredPort: values.PreferredPort, RemotePort: values.RemotePort, RelaySource: values.RelaySource,
			DirectADB: values.DirectADB, DirectTarget: values.DirectTarget,
		})
		if saveErr != nil {
			vm.EditorError = saveErr.Error()
			return
		}
		if request.Profile == nil {
			go func() { _ = vm.Manager.Connect(context.Background(), deviceID) }()
		}
		vm.OpenDevice(deviceID)
	} else {
		deviceID, saveErr := vm.Manager.SaveDevice(core.SaveDeviceRequest{
			ID: id, Name: values.Name, Address: values.Address,
			PreferredPort: values.PreferredPort, RemotePort: values.RemotePort, RelaySource: values.RelaySource,
			DirectADB: values.DirectADB, DirectTarget: values.DirectTarget,
		})
		if saveErr != nil {
			vm.EditorError = saveErr.Error()
			return
		}
		vm.OpenDevice(deviceID)
	}
	vm.PendingEditor = nil
}

// EditorView renders the device form as a modal panel.
func (vm *ViewModel) EditorView(c *ui.Context) {
	open := vm.PendingEditor != nil
	components.Dialog(c, &open, components.DialogOptions{Label: "设备设置"}, func(panel ui.Element) {
		panel.Padding(20).Gap(10).Width(470)
		panel.Children(func() {
			components.Text(c, vm.editorTitle()).FontSize(18).Bold()
			field(c, "设备名称", &vm.EditorDraft.Name, false)
			vm.connectionModeRadio(c)
			if vm.EditorDraft.Mode == "adb" {
				field(c, "ADB 地址（IP:端口 或 USB 序列号）", &vm.EditorDraft.DirectTarget, false)
				components.Text(c, "直连不经过 Tailcat：走本机 adb server，适合 USB 或同一局域网内的设备。").FontSize(11)
			} else {
				field(c, "Tailcat 地址", &vm.EditorDraft.Address, true)
				field(c, "本地端口（留空自动）", &vm.EditorDraft.LocalPort, false)
				field(c, "远端 ADB 端口", &vm.EditorDraft.RemotePort, false)
				ui.Column(c).Gap(3).Children(func() {
					components.Text(c, "DERP 中继").FontSize(11)
					segmentedChoice(c, &vm.EditorDraft.RelayTab, "默认节点", "自定义")
					if vm.EditorDraft.RelayTab == 1 {
						vm.relaySourceFields(c)
					}
				})
			}
			if vm.EditorError != "" {
				components.Text(c, vm.EditorError).FontSize(11)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if components.Button(c, "取消", components.ButtonOutline).Clicked() {
					vm.PendingEditor = nil
					open = false
				}
				if components.Button(c, "保存", components.ButtonPrimary).Clicked() {
					vm.submitEditor()
				}
			})
		})
	})
}

// editorTitle names the form for the device being edited.
func (vm *ViewModel) editorTitle() string {
	if vm.PendingEditor == nil {
		return "添加设备"
	}
	switch {
	case vm.PendingEditor.Temporary && vm.PendingEditor.Profile == nil:
		return "临时连接"
	case vm.PendingEditor.Temporary:
		return "编辑临时设备"
	case vm.PendingEditor.Profile != nil:
		return "编辑设备"
	}
	return "添加设备"
}

// connectionModeRadio is the two-way choice between a Tailcat channel and a
// direct ADB connection, built from the component library's Radio.
func (vm *ViewModel) connectionModeRadio(c *ui.Context) {
	if vm.EditorDraft.Mode == "" {
		vm.EditorDraft.Mode = "tailcat"
	}
	theme := c.Theme()
	ui.Row(c).Gap(theme.Space(3)).AlignItems(ui.Center).Children(func() {
		components.Text(c, "连接方式").FontSize(11)
		components.Radio(c, &vm.EditorDraft.Mode, "tailcat", "通过 Tailcat 转发")
		components.Radio(c, &vm.EditorDraft.Mode, "adb", "本地 ADB 直连")
	})
}

// segmentedChoice is a two-way switch drawn the way the library draws its
// segmented tabs (recessed track, raised thumb), because components.Tabs cannot
// be used inside a dialog: it keeps the chosen tab as an Element in its paint
// callback, and MyGo paints a closing dialog without rebuilding it, which
// panics with "element from pass N used in pass M".
func segmentedChoice(c *ui.Context, selected *int, labels ...string) ui.Element {
	theme := c.Theme()
	parts := ui.SegmentedBase(c, selected, len(labels))
	track := parts.Track
	pill := theme.Space(20)
	track.AlignItems(ui.Stretch).Padding(theme.Space(.5)).Gap(theme.Space(.5)).
		Radius(pill).Background(theme.Text.Alpha(.06)).AlignSelf(ui.Start)
	face, shadow := raisedFace(theme)
	track.Children(func() {
		for i, label := range labels {
			segment := parts.Segment(i).Padding(theme.Space(1.5), theme.Space(3)).Radius(pill)
			chosen := i == *selected
			if chosen {
				segment.TextColor(theme.Text).Background(face).Shadow(0, 1, 2, 0, shadow)
			} else {
				segment.TextColor(theme.Text.Alpha(.6))
				if segment.Hovered() {
					segment.Background(theme.Text.Alpha(.04))
				}
			}
			segment.Children(func() {
				text := components.Text(c, label).SingleLine()
				if chosen {
					text.FontWeight(600)
				}
			})
		}
	})
	return track
}

// raisedFace returns the face and cast shadow of a control raised off the
// surface, as the chosen segment of a segmented track is drawn: a lighter
// surface in either appearance.
func raisedFace(theme *ui.Theme) (face, shadow ui.Color) {
	if theme.Dark {
		return theme.SurfaceHover, ui.RGBA(0, 0, 0, .4)
	}
	return theme.Surface, ui.RGBA(0, 0, 0, .14)
}

// relayHistoryOptions turns the recorded sources into the editor's history
// menu. Values are the canonical sources, so picking one fills the field.
// relaySourceOption is one row of the merged picker.
type relaySourceOption struct {
	Label string
	Value string
}

// relaySourceOptions merges the saved presets and the sources devices have used
// into one menu, presets first. A source that is both appears once, under the
// name the user gave it: two rows with one source would make the choice
// ambiguous.
func relaySourceOptions(presets []core.RelayPreset, history []core.RelayHistoryEntry) []relaySourceOption {
	options := make([]relaySourceOption, 0, len(presets)+len(history))
	seen := make(map[string]bool, len(presets)+len(history))
	for _, preset := range presets {
		if preset.Source == "" || seen[preset.Source] {
			continue
		}
		seen[preset.Source] = true
		options = append(options, relaySourceOption{Value: preset.Source, Label: "预设 · " + preset.Name})
	}
	for _, entry := range history {
		if entry.Source == "" || seen[entry.Source] {
			continue
		}
		seen[entry.Source] = true
		options = append(options, relaySourceOption{Value: entry.Source, Label: "历史 · " + entry.Name})
	}
	return options
}

// relayPickCustom is the picker row that unlocks the source box. Choosing it
// clears both the pick and the box, so the user starts a custom source from
// empty rather than from a preset they just abandoned.
const relayPickCustom = "自定义（不用预设，可输入内容）"

// relaySourceFields renders the merged picker and the source box.
//
// Picking a preset locks the box: the source belongs to the preset, and editing
// it here would store a different map under the preset's label — the device
// would then quietly stop matching the preset the list names. Choosing 自定义
// clears the box and unlocks it, which is also how a preset is un-picked.
func (vm *ViewModel) relaySourceFields(c *ui.Context) {
	options := relaySourceOptions(vm.Manager.RelayPresets(), vm.Manager.RelayHistory())
	ui.Column(c).Gap(3).Children(func() {
		components.Text(c, "从预设或历史选择").FontSize(11)
		pick := components.Select(c, &vm.EditorDraft.RelayPick)
		pick.Trigger.FillWidth().Label("DERP 节点来源")
		pick.Trigger.Children(func() {
			components.Text(c, vm.relayPickLabel(options)).SingleLine()
		})
		pick.Popup(func(ui.Element) {
			// 自定义 comes first: clearing a preset is a normal choice, not a
			// hidden gesture.
			item := pick.Item("")
			item.FillWidth().Justify(ui.Start)
			item.Children(func() { components.Text(c, relayPickCustom).SingleLine() })
			for _, option := range options {
				value := option.Value
				item := pick.Item(value)
				item.FillWidth().Justify(ui.Start)
				item.Children(func() { components.Text(c, option.Label).SingleLine() })
			}
		})
		if pick.Trigger.Changed() {
			if vm.EditorDraft.RelayPick == "" {
				vm.EditorDraft.RelaySource = ""
			} else {
				vm.EditorDraft.RelaySource = vm.EditorDraft.RelayPick
			}
		}
	})
	locked := vm.EditorDraft.RelayPick != ""
	ui.Column(c).Gap(3).Children(func() {
		components.Text(c, "DERP JSON 链接或内容").FontSize(11)
		if locked {
			// A read-only dump of an inline map opens scrolled to its last line,
			// which reads like a broken field. The locked box shows what the
			// device will store in one line instead; the full text comes back
			// the moment the user chooses 自定义.
			display := vm.relaySourceDisplay(vm.EditorDraft.RelaySource)
			components.TextBox(c, &display, components.TextBoxOptions{ReadOnly: true}).
				Label("DERP JSON 链接或内容").FillWidth()
			components.Text(c, "来源由所选预设提供；要自己填，请在上方选「自定义」").FontSize(11).TextColor(c.Theme().TextMuted)
			return
		}
		components.TextBox(c, &vm.EditorDraft.RelaySource, components.TextBoxOptions{Multiline: true}).
			Label("DERP JSON 链接或内容").FillWidth().Lines(4, 8)
	})
}

// relaySourceDisplay is the locked box's one line: the URL itself, or what the
// inline map holds.
func (vm *ViewModel) relaySourceDisplay(source string) string {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return source
	}
	return "内联 JSON · " + vm.derpInfoFor(source).summary
}

// relayPickLabel names the chosen option; an empty pick is the custom row.
func (vm *ViewModel) relayPickLabel(options []relaySourceOption) string {
	if vm.EditorDraft.RelayPick == "" {
		return relayPickCustom
	}
	for _, option := range options {
		if option.Value == vm.EditorDraft.RelayPick {
			return option.Label
		}
	}
	return relayPickCustom
}

// field renders one labelled text input.
func field(c *ui.Context, label string, value *string, secret bool) {
	ui.Column(c).Gap(3).Children(func() {
		components.Text(c, label).FontSize(11)
		box := components.TextBox(c, value, components.TextBoxOptions{})
		if secret {
			box.Password()
		}
		box.Label(label)
	})
}
