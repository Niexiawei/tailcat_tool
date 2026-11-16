package core

import "testing"

// groupedKeys lists the group keys in the order the table shows them.
func groupedKeys(apps []AppUsage) []string {
	keys := make([]string, len(apps))
	for i, app := range apps {
		keys[i] = app.Package
	}
	return keys
}

func TestGroupProcessesByIdenticalPrefix(t *testing.T) {
	entries := []procEntry{
		{PID: 1, Command: "com.foo"},
		{PID: 2, Command: "com.foo:remote"},
		{PID: 3, Command: "com.bar"},
	}
	apps := GroupProcesses(entries, map[int]float64{1: 10, 2: 5, 3: 1})
	if len(apps) != 2 {
		t.Fatalf("groups = %v, want com.foo and com.bar", groupedKeys(apps))
	}
	if apps[0].Package != "com.foo" || len(apps[0].Processes) != 2 {
		t.Fatalf("com.foo = %+v", apps[0])
	}
	if apps[0].CPUPercent != 15 {
		t.Fatalf("com.foo CPU = %v, want 15", apps[0].CPUPercent)
	}
}

func TestGroupProcessesKeepsSystemNamesSeparate(t *testing.T) {
	entries := []procEntry{
		{PID: 1, Command: "system_server"},
		{PID: 2, Command: "/system/bin/surfaceflinger"},
	}
	apps := GroupProcesses(entries, map[int]float64{1: 1, 2: 2})
	keys := groupedKeys(apps)
	if len(keys) != 2 || keys[0] != "surfaceflinger" || keys[1] != "system_server" {
		t.Fatalf("groups = %v", keys)
	}
}

func TestGroupProcessesSortsByCPUDescending(t *testing.T) {
	entries := []procEntry{
		{PID: 1, Command: "com.small"},
		{PID: 2, Command: "com.big"},
		{PID: 3, Command: "com.big:worker"},
		{PID: 4, Command: "com.middle"},
	}
	apps := GroupProcesses(entries, map[int]float64{1: 1, 2: 30, 3: 20, 4: 10})
	if got := groupedKeys(apps); len(got) != 3 || got[0] != "com.big" || got[1] != "com.middle" || got[2] != "com.small" {
		t.Fatalf("groups = %v", got)
	}
	if apps[0].Processes[0].PID != 2 || apps[0].Processes[1].PID != 3 {
		t.Fatalf("com.big processes = %+v", apps[0].Processes)
	}
	if apps[0].CPUPercent != 50 {
		t.Fatalf("com.big CPU = %v, want 50", apps[0].CPUPercent)
	}
}

func TestGroupProcessesSkipsEmptyCommand(t *testing.T) {
	entries := []procEntry{
		{PID: 1, Command: ""},
		{PID: 2, Command: "   "},
		{PID: 3, Command: "com.foo"},
	}
	apps := GroupProcesses(entries, map[int]float64{1: 5, 2: 5, 3: 1})
	if got := groupedKeys(apps); len(got) != 1 || got[0] != "com.foo" {
		t.Fatalf("groups = %v", got)
	}
}
