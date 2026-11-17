package app

import (
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

func TestMatchQuery(t *testing.T) {
	cases := []struct {
		name   string
		query  string
		fields []string
		want   bool
	}{
		{"chinese name", "微", []string{"微信"}, true},
		{"package prefix", "com.ex", []string{"com.example.app"}, true},
		{"subsequence of a package", "mp", []string{"com.google.android.apps.maps"}, true},
		{"case does not matter", "Car Launcher", []string{"car launcher"}, true},
		{"every term must hit", "car maps", []string{"Car Launcher"}, false},
		{"no match", "zz", []string{"微信"}, false},
		{"an empty query keeps everything", "", []string{"微信"}, true},
		{"terms may hit different fields", "maps google", []string{"Maps", "com.google.android.maps"}, true},
		{"a sub process name", "remote", []string{"com.foo:remote"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchQuery(c.query, c.fields...); got != c.want {
				t.Fatalf("matchQuery(%q, %v) = %v, want %v", c.query, c.fields, got, c.want)
			}
		})
	}
}

func TestFilterAppsKeepsGroupsMatchedByAProcess(t *testing.T) {
	apps := []core.AppUsage{
		{Package: "com.foo", Label: "微信", Processes: []core.ProcessUsage{
			{PID: 1, Name: "com.foo"},
			{PID: 2, Name: "com.foo:remote"},
		}},
		{Package: "system_server", Processes: []core.ProcessUsage{{PID: 3, Name: "system_server"}}},
	}

	got := filterApps(apps, "remote")
	if len(got) != 1 || got[0].app.Package != "com.foo" {
		t.Fatalf("filterApps() kept %d groups, want just com.foo", len(got))
	}
	if got[0].full {
		t.Fatal("a group that matched through one process reports as a full match")
	}
	if len(got[0].processes) != 1 || got[0].processes[0].PID != 2 {
		t.Fatalf("processes = %+v, want only the matching one", got[0].processes)
	}
}

func TestFilterKeepsAllProcessesOfAMatchedApplication(t *testing.T) {
	apps := []core.AppUsage{
		{Package: "com.foo", Label: "微信", Processes: []core.ProcessUsage{
			{PID: 1, Name: "com.foo"},
			{PID: 2, Name: "com.foo:remote"},
		}},
	}

	got := filterApps(apps, "微信")
	if len(got) != 1 || !got[0].full {
		t.Fatalf("filterApps() = %+v, want a full match on the application name", got)
	}
}

func TestFilterIgnoresExpandedStateWhileTheQueryIsActive(t *testing.T) {
	vm, _ := resourceFixture(t)
	profile := vm.Manager.Profiles()[0]
	vm.resourceStateFor(profile.ID).filter = "remote"

	tt := ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("PID 101") {
		t.Fatalf("the matching process of a closed group is hidden; texts = %v", tt.Texts())
	}
	if tt.HasText("PID 100") {
		t.Fatalf("a process that does not match is still listed; texts = %v", tt.Texts())
	}
	if tt.HasText("system_server") {
		t.Fatalf("a group with no match survived the filter; texts = %v", tt.Texts())
	}
}

func TestFilterWithNoMatchSaysSo(t *testing.T) {
	vm, _ := resourceFixture(t)
	profile := vm.Manager.Profiles()[0]
	vm.resourceStateFor(profile.ID).filter = "zzz"

	tt := ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("没有匹配的应用") {
		t.Fatalf("an empty filter result says nothing; texts = %v", tt.Texts())
	}
}

func TestFilterAppliesToBothViews(t *testing.T) {
	vm, _ := resourceFixture(t)
	profile := vm.Manager.Profiles()[0]
	state := vm.resourceStateFor(profile.ID)
	state.filter = "remote"
	state.view = resourceViewProcesses

	tt := ui.NewTester(vm.ContentView, 900, 900)
	if !tt.HasText("com.foo:remote") {
		t.Fatalf("the process view ignored the filter; texts = %v", tt.Texts())
	}
	if tt.HasText("system_server") {
		t.Fatalf("the process view kept a row the filter excluded; texts = %v", tt.Texts())
	}
}
