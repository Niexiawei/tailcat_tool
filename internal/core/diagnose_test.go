package core

import (
	"errors"
	"testing"
)

// A broken configuration must not hide a broken history file, a missing
// keychain and a missing adb: the user fixes what the page says, and a page
// that reports one of four problems after each restart is a page that lies.
func TestDiagnoseReportsEveryBrokenPart(t *testing.T) {
	problems := Diagnose(DiagnoseInput{
		ConfigDir:      "/config",
		ConfigErr:      errors.New("unexpected end of JSON input"),
		HistoryErr:     errors.New("disk full"),
		CredentialsErr: errors.New("no secret service"),
		AppMetaErr:     errors.New("permission denied"),
		ADBPath:        "",
		ScrcpyPath:     "/usr/bin/scrcpy",
	})
	if len(problems) != 5 {
		t.Fatalf("Diagnose() = %d problems, want 5: %+v", len(problems), problems)
	}
	wantTitles := []string{
		"配置读取失败，已使用默认设置",
		"历史库无法打开，本次运行不会保存历史",
		"未检测到系统凭据库，连接地址仅本次运行有效",
		"应用信息缓存不可用，资源页只显示包名",
		"未找到 adb，连接设备前需要先安装",
	}
	wantKinds := []ProblemKind{
		ProblemConfig, ProblemHistory, ProblemCredentials, ProblemAppMeta, ProblemADB,
	}
	for i, want := range wantTitles {
		if problems[i].Title != want {
			t.Errorf("problems[%d].Title = %q, want %q", i, problems[i].Title, want)
		}
		if problems[i].Kind != wantKinds[i] {
			t.Errorf("problems[%d].Kind = %v, want %v", i, problems[i].Kind, wantKinds[i])
		}
	}
	// The details are what the underlying failure said, so a user can search
	// for it or paste it into a bug report.
	if problems[0].Detail != "unexpected end of JSON input" {
		t.Errorf("config detail = %q", problems[0].Detail)
	}
	if problems[1].Detail != "disk full" {
		t.Errorf("history detail = %q", problems[1].Detail)
	}
	// Every one of these leaves the app usable, so all of them offer to carry
	// on; a problem that did not would have to say so.
	for _, p := range problems {
		if !p.Continue {
			t.Errorf("%q is not continuable; the app runs without it", p.Title)
		}
	}
	// The path is where to look, so it is the configuration directory the app
	// actually used.
	for _, i := range []int{0, 1, 3} {
		if problems[i].Path != "/config" {
			t.Errorf("problems[%d].Path = %q, want the configuration directory", i, problems[i].Path)
		}
	}
}

// A healthy start says nothing: an empty list, not a page with a "no problems"
// card.
func TestDiagnoseIsSilentWhenEverythingWorks(t *testing.T) {
	if problems := Diagnose(DiagnoseInput{
		ConfigDir:  "/config",
		ADBPath:    "/usr/bin/adb",
		ScrcpyPath: "/usr/bin/scrcpy",
	}); len(problems) != 0 {
		t.Fatalf("Diagnose() = %+v, want no problems", problems)
	}
}

// adb is the one tool the app cannot work without, so a missing one is a
// problem even when everything else loaded.
func TestDiagnoseReportsMissingADBAlone(t *testing.T) {
	problems := Diagnose(DiagnoseInput{ConfigDir: "/config"})
	if len(problems) != 1 || problems[0].Kind != ProblemADB {
		t.Fatalf("Diagnose() = %+v, want the adb problem alone", problems)
	}
}
