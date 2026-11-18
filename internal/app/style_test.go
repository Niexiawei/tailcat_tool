package app

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
)

// TestAppThemeWarningIsTheGlobalOrange pins the one place the warning colour is
// written: the clear-logs button and every warning badge read it from here.
func TestAppThemeWarningIsTheGlobalOrange(t *testing.T) {
	if warningHex != "#e37318" {
		t.Fatalf("warningHex = %q, want #e37318", warningHex)
	}
	var warning ui.Color
	tt := ui.NewTester(func(c *ui.Context) { warning = appTheme(c).Warning }, 100, 100)
	tt.Frame()
	if warning != ui.Hex(warningHex) {
		t.Fatalf("theme warning = %v, want %v", warning, ui.Hex(warningHex))
	}
}

// TestNoGhostButtonsAnywhere enforces the rule in AGENTS.md: a ghost button has
// no face at all, so a page-level action reads as body text.
func TestNoGhostButtonsAnywhere(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob() error: %v", err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if strings.Contains(string(source), "components.ButtonGhost") {
			t.Fatalf("%s uses components.ButtonGhost; use ButtonOutline for a dialog's 取消/关闭 or ButtonSecondary for a quiet action", path)
		}
	}
}

func TestSidebarLeavesSpaceBetweenTheGroupsAndTheLogEntry(t *testing.T) {
	vm := newTestViewModel(t)
	tt := sidebarTester(vm, 280, 720)
	// The empty temporary group's note is the last line of the accordion, so
	// it is the panel edge the entry must clear.
	panel, ok := tt.Find("临时设备只在本次运行有效。")
	if !ok {
		t.Fatalf("device groups missing; texts = %v", tt.Texts())
	}
	entry, ok := tt.Find("日志")
	if !ok {
		t.Fatalf("the log entry is missing; texts = %v", tt.Texts())
	}
	if gap := entry.Y - (panel.Y + panel.H); gap < 18 {
		t.Fatalf("the log entry sits %0.0f px under the device panels, want a space", gap)
	}
}

func TestLogFiltersShareOneLine(t *testing.T) {
	vm, reader, base := logsPageFixture(t)
	reader.add(core.TimelineEntry{
		At: base.Add(-time.Minute), Level: core.LogError, Source: core.LogApp, Text: "配置读取失败",
	})
	tt := ui.NewTester(vm.ContentView, 1100, 760)
	tt.Frame()
	waitForQuery(t, reader, 1)
	waitForText(t, tt, "级别")
	level, ok := tt.Find("错误")
	if !ok {
		t.Fatalf("level tab missing; texts = %v", tt.Texts())
	}
	rng, ok := tt.Find("7 天")
	if !ok {
		t.Fatalf("range tab missing; texts = %v", tt.Texts())
	}
	if math.Abs(float64(level.Y-rng.Y)) > 1 {
		t.Fatalf("级别 and 时间 are on different rows (%0.0f vs %0.0f); they must share one", level.Y, rng.Y)
	}
}
