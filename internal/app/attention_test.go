package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

// waitFor polls cond until it holds, for work that runs off the render thread.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// attentionFleet is a view model with three devices whose local entries
// vanished at once, the way "adb kill-server" leaves them.
func attentionFleet(t *testing.T) (*ViewModel, []uuid.UUID) {
	t.Helper()
	vm := newTestViewModel(t)
	ids := []uuid.UUID{vm.Manager.Profiles()[0].ID}
	for _, name := range []string{"EX5", "客厅"} {
		id, err := vm.Manager.SaveDevice(core.SaveDeviceRequest{
			Name: name, Address: testAddress, RemotePort: 5555,
		})
		if err != nil {
			t.Fatalf("SaveDevice(%q) error: %v", name, err)
		}
		ids = append(ids, id)
	}
	// Connecting is what gives the aggregate a fleet to speak about; the fake
	// runner answers get-state with "device".
	for _, id := range ids {
		if err := vm.Manager.Connect(context.Background(), id); err != nil {
			t.Fatalf("Connect() error: %v", err)
		}
	}
	vm.Manager.ForceAttentionGroupForTest(ids, core.AttentionServerRestart, "3 台设备的本地 ADB 入口同时消失")
	return vm, ids
}

// One dismissable prompt for the whole fleet, with an action that brings all
// of it back: per-device "改用其他本地端口" makes no sense when the server
// itself restarted.
func TestAttentionAggregatedDialogOffersReconnectAll(t *testing.T) {
	vm, ids := attentionFleet(t)
	defer func() { vm.Manager.DisconnectAll(context.Background()) }()

	tt := ui.NewTester(vm.ContentView, 900, 700)
	joined := strings.Join(tt.Texts(), " | ")
	for _, want := range []string{"设备需要处理", "全部重新连接", "3 台设备", "E245", "EX5", "客厅"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the aggregated dialog lacks %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "改用其他本地端口") {
		t.Fatalf("the aggregated dialog still offers a single device's port switch: %s", joined)
	}

	if err := tt.Click("全部重新连接"); err != nil {
		t.Fatalf("Click(全部重新连接) error: %v", err)
	}
	// Every device in the group, not just the one that noticed first. The
	// reconnect runs off the render thread, so wait for it rather than
	// sampling the state the frame happened to leave behind.
	waitFor(t, "the fleet to reconnect", func() bool {
		for _, id := range ids {
			if vm.Manager.Session(id).State != core.StateOnline {
				return false
			}
		}
		return true
	})
	if attentions := vm.Manager.Attentions(); len(attentions) != 0 {
		t.Fatalf("the prompt survived its own action: %+v", attentions)
	}
}

// A single device keeps the original three actions: "改用其他本地端口" is the
// answer when one entry was taken, and "全部重新连接" would be a lie.
func TestAttentionSingleDeviceKeepsPortSwitch(t *testing.T) {
	vm := newTestViewModel(t)
	profile := vm.Manager.Profiles()[0]
	vm.Manager.ForceAttentionForTest(profile.ID, core.AttentionSerialDetached, "本地 ADB 入口已被外部移除")

	tt := ui.NewTester(vm.ContentView, 900, 700)
	joined := strings.Join(tt.Texts(), " | ")
	if !strings.Contains(joined, "改用其他本地端口") {
		t.Fatalf("the single-device dialog lost 改用其他本地端口: %s", joined)
	}
	if strings.Contains(joined, "全部重新连接") {
		t.Fatalf("the single-device dialog offers 全部重新连接: %s", joined)
	}
	vm.Manager.AckAttention(profile.ID)
}

// 稍后处理 on an aggregated prompt dismisses the group, and every device in it
// stays in "needs action" — the user said later, not reconnect.
func TestAttentionAggregatedLaterKeepsTheFleetWaiting(t *testing.T) {
	vm, ids := attentionFleet(t)
	defer func() { vm.Manager.DisconnectAll(context.Background()) }()

	tt := ui.NewTester(vm.ContentView, 900, 700)
	if err := tt.Click("稍后处理"); err != nil {
		t.Fatalf("Click(稍后处理) error: %v", err)
	}
	if attentions := vm.Manager.Attentions(); len(attentions) != 0 {
		t.Fatalf("the prompt is still there: %+v", attentions)
	}
	for _, id := range ids {
		if state := vm.Manager.Session(id).State; state != core.StateNeedsAction {
			t.Fatalf("device %v is %v, want needsAction", id, state)
		}
	}
}
