package app

import (
	"context"
	"path/filepath"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

// writeClipboard is a variable so tests never touch the real clipboard.
var writeClipboard = func(text string) error {
	return mygo.Clipboard.Write(transfer.TextData(text), mygo.ClipboardOptions{})
}

// copyText puts a string on the clipboard.
func (vm *ViewModel) copyText(text string) {
	if err := writeClipboard(text); err != nil {
		vm.FailText("复制失败：" + err.Error())
		return
	}
	vm.Notify("已复制 " + text)
}

// promote saves a temporary device under a name the user picks.
func (vm *ViewModel) promote(profile core.Profile) {
	if err := vm.Manager.PromoteTemporaryDevice(profile.ID, profile.Name); err != nil {
		vm.Fail(err)
		return
	}
	vm.Notify("已保存到我的设备，当前会话保持")
}

// chooseAPK asks for an APK and installs it.
func (vm *ViewModel) chooseAPK(profile core.Profile) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:   "选择要安装到 " + profile.Name + " 的 APK",
		Filters: []mygo.FileFilter{{Name: "APK", Extensions: []string{"apk"}}},
	})
	if err != nil || len(paths) == 0 {
		return
	}
	if _, err := vm.Manager.BeginADBTask(context.Background(), profile.ID, "安装 "+fileName(paths[0]),
		[]string{"install", "-r", paths[0]}, 600*1e9); err != nil {
		vm.Fail(err)
	}
}

// chooseUpload asks for a local path and uploads it.
func (vm *ViewModel) chooseUpload(profile core.Profile) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     "选择上传到 " + profile.Name + " 的文件或文件夹",
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	vm.TransferOpen = true
	vm.TransferUpload = true
	vm.TransferLocalPath = paths[0]
	vm.TransferRemotePath = "/sdcard/Download/"
	vm.TransferError = ""
}

// startDownload asks for the remote path and a local folder, then pulls.
func (vm *ViewModel) startDownload(profile core.Profile) {
	vm.TransferOpen = true
	vm.TransferUpload = false
	vm.TransferLocalPath = ""
	vm.TransferRemotePath = "/sdcard/Download/"
	vm.TransferError = ""
}

// chooseDownloadFolder asks where to put a pulled file or directory.
func (vm *ViewModel) chooseDownloadFolder() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     "选择保存目录",
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	vm.TransferLocalPath = paths[0]
}

func fileName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == filepath.Separator {
			return path[i+1:]
		}
	}
	return path
}
