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
			return out, core.Error("请填写 DERP JSON 链接或内容，或选择默认官方节点")
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
			for _, entry := range vm.Manager.RelayHistory() {
				if entry.Source == profile.RelaySource {
					draft.RelayPick = entry.Source
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
					segmentedChoice(c, &vm.EditorDraft.RelayTab, "官方默认", "自定义")
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
func relayHistoryOptions(entries []core.RelayHistoryEntry) []components.NativeSelectOption[string] {
	options := make([]components.NativeSelectOption[string], 0, len(entries))
	for _, entry := range entries {
		options = append(options, components.NativeSelectOption[string]{Value: entry.Source, Label: entry.Name})
	}
	return options
}

// relaySourceFields renders the history picker and the free-form source box.
func (vm *ViewModel) relaySourceFields(c *ui.Context) {
	if entries := vm.Manager.RelayHistory(); len(entries) > 0 {
		ui.Column(c).Gap(3).Children(func() {
			components.Text(c, "之前用过的中继").FontSize(11)
			pick := components.NativeSelect(c, &vm.EditorDraft.RelayPick, relayHistoryOptions(entries))
			if pick.Changed() {
				vm.EditorDraft.RelaySource = vm.EditorDraft.RelayPick
			}
			pick.Trigger.Label("之前用过的中继").FillWidth()
		})
	}
	field(c, "DERP JSON 链接或内容", &vm.EditorDraft.RelaySource, false)
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
