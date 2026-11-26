package app

import (
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

// chooseAPK opens the install dialog; the APK itself is chosen there, so the
// path is visible next to the user it will be installed for.
func (vm *ViewModel) chooseAPK(profile core.Profile) {
	vm.openInstallDialog(profile.ID, "")
}

// pickAPKFile asks for the APK to install. It is a variable so tests never open
// a real file dialog; false means the user cancelled.
var pickAPKFile = func(title string) (string, bool) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:   title,
		Filters: []mygo.FileFilter{{Name: "APK", Extensions: []string{"apk"}}},
	})
	if err != nil || len(paths) == 0 {
		return "", false
	}
	return paths[0], true
}

// chooseUpload opens the upload dialog: the files are chosen inside it, so the
// local picks and the device folder are visible together before anything runs.
func (vm *ViewModel) chooseUpload(profile core.Profile) {
	vm.openTransfer(profile.ID, true)
}

// startDownload opens the download dialog: both paths are chosen inside it.
func (vm *ViewModel) startDownload(profile core.Profile) {
	vm.openTransfer(profile.ID, false)
}

func fileName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == filepath.Separator {
			return path[i+1:]
		}
	}
	return path
}
