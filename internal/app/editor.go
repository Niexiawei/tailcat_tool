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
	Mode           string
	DirectTarget   string
	Address        string
	LocalPort      string
	RemotePort     string
	RelaySelection string
	RelaySource    string
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
	if in.RelaySelection != "default" && in.RelaySelection != "" && strings.TrimSpace(in.RelaySource) == "" {
		return out, core.Error("请填写 DERP JSON 链接或内容，或选择默认官方节点")
	}
	if in.RelaySelection != "default" && in.RelaySelection != "" {
		out.RelaySource = strings.TrimSpace(in.RelaySource)
	}
	return out, nil
}

// openEditor fills the form from a device, or with defaults for a new one.
func (vm *ViewModel) openEditor(request *EditorRequest) {
	vm.PendingEditor = request
	vm.EditorError = ""
	vm.EditorPresetName = ""
	draft := EditorInput{RemotePort: "5555", RelaySelection: "default", Mode: "tailcat"}
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
			draft.RelaySelection = "custom"
			for _, preset := range vm.Manager.RelayPresets() {
				if preset.Source == profile.RelaySource {
					draft.RelaySelection = preset.ID.String()
					vm.EditorPresetName = preset.Name
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
				ui.Row(c).Gap(6).Children(func() {
					if components.Button(c, "默认中继", components.ButtonGhost).Clicked() {
						vm.EditorDraft.RelaySelection = "default"
						vm.EditorDraft.RelaySource = ""
					}
					if components.Button(c, "自定义中继", components.ButtonGhost).Clicked() {
						vm.EditorDraft.RelaySelection = "custom"
					}
					components.Text(c, "当前："+relaySelectionLabel(vm)).FontSize(11)
				})
				if vm.EditorDraft.RelaySelection != "default" {
					field(c, "DERP JSON 链接或内容", &vm.EditorDraft.RelaySource, false)
				}
			}
			if vm.EditorError != "" {
				components.Text(c, vm.EditorError).FontSize(11)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if components.Button(c, "取消", components.ButtonGhost).Clicked() {
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

func relaySelectionLabel(vm *ViewModel) string {
	if vm.EditorDraft.RelaySelection == "default" {
		return "官方默认节点"
	}
	for _, preset := range vm.Manager.RelayPresets() {
		if preset.ID.String() == vm.EditorDraft.RelaySelection {
			return preset.Name
		}
	}
	return "自定义 JSON"
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
