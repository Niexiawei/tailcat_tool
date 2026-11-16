package core

import "testing"

func TestParseCPUStatSumsAllFields(t *testing.T) {
	// The second line (cpu0) must be ignored: only the aggregate line counts.
	text := "cpu  100 20 300 4000 50 0 10 0 0 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n"
	got, ok := parseCPUStat(text)
	if !ok {
		t.Fatal("parseCPUStat() did not find the aggregate line")
	}
	if got.Total != 4480 {
		t.Fatalf("Total = %d, want 4480", got.Total)
	}
	if got.Idle != 4050 {
		t.Fatalf("Idle = %d, want 4050", got.Idle)
	}
}

func TestParseCPUStatRejectsEmptyText(t *testing.T) {
	if _, ok := parseCPUStat(""); ok {
		t.Fatal("parseCPUStat(\"\") reported success")
	}
}

func TestParseMemInfoUsesMemAvailable(t *testing.T) {
	text := "MemTotal:        8000000 kB\nMemFree:          500000 kB\nMemAvailable:    2000000 kB\nBuffers:          100000 kB\nCached:          1000000 kB\n"
	got, ok := parseMemInfo(text)
	if !ok {
		t.Fatal("parseMemInfo() reported failure")
	}
	if got.Total != 8000000*1024 || got.Available != 2000000*1024 {
		t.Fatalf("memInfo = %+v", got)
	}
}

func TestParseMemInfoFallsBackWithoutMemAvailable(t *testing.T) {
	text := "MemTotal:        8000000 kB\nMemFree:          500000 kB\nBuffers:          100000 kB\nCached:          1000000 kB\n"
	got, ok := parseMemInfo(text)
	if !ok {
		t.Fatal("parseMemInfo() reported failure")
	}
	if got.Available != (500000+100000+1000000)*1024 {
		t.Fatalf("Available = %d, want MemFree+Buffers+Cached", got.Available)
	}
}

func TestParseProcEntryReadsJiffiesAndStartTicks(t *testing.T) {
	// Field 14 is utime, 15 stime, 22 starttime; the comm contains spaces so a
	// naive Fields() split would shift every index.
	stat := "1234 (Web Content) S 1 1234 1234 0 -1 4194560 1000 0 0 0 150 25 0 0 20 0 12 0 5000 1000000 500 18446744073709551615 0 0"
	status := "Name:\tWeb Content\nVmRSS:\t   123456 kB\nThreads:\t12\n"
	entry, ok := parseProcEntry(1234, stat, status, "com.foo\x00:remote\x00")
	if !ok {
		t.Fatal("parseProcEntry() reported failure")
	}
	if entry.PID != 1234 {
		t.Fatalf("PID = %d", entry.PID)
	}
	if entry.Jiffies != 175 {
		t.Fatalf("Jiffies = %d, want 175", entry.Jiffies)
	}
	if entry.StartTicks != 5000 {
		t.Fatalf("StartTicks = %d, want 5000", entry.StartTicks)
	}
	if entry.RSS != 123456*1024 {
		t.Fatalf("RSS = %d", entry.RSS)
	}
	if entry.Command != "com.foo :remote" {
		t.Fatalf("Command = %q", entry.Command)
	}
}

func TestParseProcEntryRejectsEmptyCmdline(t *testing.T) {
	stat := "7 (kworker/0:1) S 2 7 7 0 -1 69238880 0 0 0 0 0 0 0 0 20 0 1 0 12 0 0"
	if _, ok := parseProcEntry(7, stat, "Name:\tkworker\n", "\x00\x00"); ok {
		t.Fatal("parseProcEntry() accepted a kernel thread with an empty cmdline")
	}
}

func TestParseGLESRendererReadsSurfaceFlingerLine(t *testing.T) {
	text := "SurfaceFlinger global state:\nGLES: Qualcomm, Adreno (TM) 640, OpenGL ES 3.2 V@0530.0 (GIT@bd8b3f6a1d)\nEGL: 1.4\n"
	if got := parseGLESRenderer(text); got != "Adreno (TM) 640" {
		t.Fatalf("renderer = %q, want %q", got, "Adreno (TM) 640")
	}
}

func TestParseGPUBusyHandlesPercentAndBusyTotal(t *testing.T) {
	cases := []struct {
		text string
		want float64
	}{
		{"37%", 37},
		{"37", 37},
		{"37 100", 37},
		{"0 100", 0},
	}
	for _, tc := range cases {
		got, ok := parseGPUBusy(tc.text)
		if !ok || got != tc.want {
			t.Fatalf("parseGPUBusy(%q) = %v, %v; want %v, true", tc.text, got, ok, tc.want)
		}
	}
}

func TestParseGPUBusyRejectsGarbage(t *testing.T) {
	for _, text := range []string{"", "  \n", "n/a", "garbage", "12 0"} {
		if _, ok := parseGPUBusy(text); ok {
			t.Fatalf("parseGPUBusy(%q) reported success", text)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
		{3 * (1 << 30) / 2, "1.5 GB"},
	}
	for _, tc := range cases {
		if got := FormatBytes(tc.in); got != tc.want {
			t.Fatalf("FormatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
