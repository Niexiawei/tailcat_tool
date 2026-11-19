package app

import (
	"testing"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/google/uuid"
)

// pendingAttention is an attention covering devices devices, the shape the
// manager records for a fleet-wide event.
func pendingAttention(devices int) core.Attention {
	ids := make([]uuid.UUID, devices)
	for i := range ids {
		ids[i] = uuid.New()
	}
	return core.Attention{
		DeviceID:  ids[0],
		DeviceIDs: ids,
		Kind:      core.AttentionServerRestart,
		Detail:    "本地 ADB 入口已被外部移除",
	}
}

func TestTrayBadgeCountsPendingDevices(t *testing.T) {
	title, tooltip, menuLabel := trayBadge([]core.Attention{pendingAttention(1)})
	if title != "1" {
		t.Fatalf("title = %q, want the count as the menu bar text", title)
	}
	if tooltip != "Tailcat Manager · 1 项待处理" {
		t.Fatalf("tooltip = %q", tooltip)
	}
	if menuLabel != "⚠ 1 项待处理" {
		t.Fatalf("menu label = %q", menuLabel)
	}
}

// One aggregated attention can cover a whole fleet; counting the attentions
// rather than the devices would say "1" while three devices wait.
func TestTrayBadgeCountsAggregatedDevices(t *testing.T) {
	title, tooltip, menuLabel := trayBadge([]core.Attention{pendingAttention(3), pendingAttention(1)})
	if title != "4" {
		t.Fatalf("title = %q, want 4 devices", title)
	}
	if tooltip != "Tailcat Manager · 4 项待处理" {
		t.Fatalf("tooltip = %q", tooltip)
	}
	if menuLabel != "⚠ 4 项待处理" {
		t.Fatalf("menu label = %q", menuLabel)
	}
}

// An attention recorded before DeviceIDs existed still counts as one device.
func TestTrayBadgeCountsSingleAttentionWithoutDeviceIDs(t *testing.T) {
	attention := pendingAttention(1)
	attention.DeviceIDs = nil
	if title, _, _ := trayBadge([]core.Attention{attention}); title != "1" {
		t.Fatalf("title = %q, want 1", title)
	}
}

func TestTrayBadgeClearsWhenNothingPending(t *testing.T) {
	title, tooltip, menuLabel := trayBadge(nil)
	if title != "" || tooltip != "" || menuLabel != "" {
		t.Fatalf("trayBadge(nil) = %q, %q, %q; want empty strings", title, tooltip, menuLabel)
	}
}

// The count has to be clickable and first: a badge with no way back to the
// window is a decoration, and Windows and Linux have no menu bar number to
// notice at all.
func TestTrayBadgeMenuLabelIsClickableFirst(t *testing.T) {
	vm := newTestViewModel(t)
	vm.Manager.ForceAttentionForTest(vm.Manager.Profiles()[0].ID, core.AttentionSerialDetached, "本地 ADB 入口已被外部移除")
	defer vm.Manager.AckAttention(vm.Manager.Profiles()[0].ID)

	shown := false
	menu := vm.TrayMenu(func() { shown = true }, func() {})
	items := menu.Items()
	if len(items) == 0 {
		t.Fatal("the tray menu is empty")
	}
	first := items[0]
	if first.Label != "⚠ 1 项待处理" {
		t.Fatalf("first tray item = %q, want the pending count", first.Label)
	}
	if first.Click == nil {
		t.Fatal("the pending count is not clickable, so it cannot bring the window back")
	}
	first.Click(first, nil)
	if !shown {
		t.Fatal("clicking the pending count did not show the window")
	}
}

// Without pending attentions the menu starts with the window action: no empty
// badge above it.
func TestTrayMenuHasNoBadgeWhenNothingIsPending(t *testing.T) {
	vm := newTestViewModel(t)
	items := vm.TrayMenu(func() {}, func() {}).Items()
	if len(items) == 0 {
		t.Fatal("the tray menu is empty")
	}
	if items[0].Label != "打开管理窗口" {
		t.Fatalf("first tray item = %q, want the window action", items[0].Label)
	}
	for _, item := range items {
		if item.Label == "⚠ 0 项待处理" {
			t.Fatal("the menu shows a badge with nothing pending")
		}
	}
}

// The title and tooltip are only written when their text changes: rewriting an
// unchanged menu bar title on every frame flickers, which is why trayText is a
// comparable value rather than a set of setters.
func TestTrayTextComparesByContent(t *testing.T) {
	first := trayText{title: "1", tooltip: "Tailcat Manager · 1 项待处理"}
	if first != (trayText{title: "1", tooltip: "Tailcat Manager · 1 项待处理"}) {
		t.Fatal("equal badges compare unequal, so every frame would rewrite them")
	}
	if first == (trayText{title: "2", tooltip: "Tailcat Manager · 2 项待处理"}) {
		t.Fatal("a different count compares equal, so the change would be lost")
	}
}
