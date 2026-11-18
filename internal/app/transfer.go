package app

import (
	"context"
	"strings"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// ValidateRemotePath rejects anything that cannot be an absolute device path.
func ValidateRemotePath(path string) error {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "/") {
		return core.Error("请填写绝对远端路径，例如 /sdcard/Download/example.txt")
	}
	if strings.ContainsAny(path, "\x00\n") {
		return core.Error("远端路径不能包含换行或空字符")
	}
	return nil
}

// TransferView is the upload/download form.
func (vm *ViewModel) TransferView(c *ui.Context) {
	open := vm.TransferOpen
	components.Dialog(c, &open, components.DialogOptions{Label: "文件传输"}, func(panel ui.Element) {
		panel.Padding(18).Gap(10).Width(460)
		panel.Children(func() {
			title := "下载文件"
			if vm.TransferUpload {
				title = "上传文件"
			}
			components.Text(c, title).FontSize(18).Bold()
			if vm.TransferLocalPath != "" {
				components.Text(c, "本地："+vm.TransferLocalPath).FontSize(11)
			}
			if !vm.TransferUpload {
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					components.Text(c, orPlaceholder(vm.TransferLocalPath, "请选择本地保存目录")).FontSize(11)
					if components.Button(c, "选择目录", components.ButtonOutline).Clicked() {
						vm.chooseDownloadFolder()
					}
				})
			}
			label := "远端文件或目录路径"
			if vm.TransferUpload {
				label = "远端保存目录或路径"
			}
			field(c, label, &vm.TransferRemotePath, false)
			if vm.TransferError != "" {
				components.Text(c, vm.TransferError).FontSize(11)
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				if components.Button(c, "取消", components.ButtonOutline).Clicked() {
					vm.TransferOpen = false
					open = false
				}
				if components.Button(c, "开始", components.ButtonPrimary).Clicked() {
					vm.submitTransfer()
				}
			})
		})
	})
	if !open {
		vm.TransferOpen = false
	}
}

func (vm *ViewModel) submitTransfer() {
	profile, ok := vm.SelectedProfile()
	if !ok {
		vm.TransferError = "请先选择设备"
		return
	}
	remote := strings.TrimSpace(vm.TransferRemotePath)
	if err := ValidateRemotePath(remote); err != nil {
		vm.TransferError = err.Error()
		return
	}
	if vm.TransferUpload {
		if _, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "上传 "+fileName(vm.TransferLocalPath),
			[]string{"push", vm.TransferLocalPath, remote}, 30*60*1e9); err != nil {
			vm.TransferError = err.Error()
			return
		}
	} else {
		if vm.TransferLocalPath == "" {
			vm.TransferError = "请选择本地保存目录"
			return
		}
		if _, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "下载 "+fileName(remote),
			[]string{"pull", remote, vm.TransferLocalPath}, 30*60*1e9); err != nil {
			vm.TransferError = err.Error()
			return
		}
	}
	vm.TransferOpen = false
}
