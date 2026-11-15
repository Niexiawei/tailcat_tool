package core

import (
	"context"
	"testing"
	"time"
)

const deviceList = "List of devices attached\n127.0.0.1:5556\tdevice\n127.0.0.1:5557\toffline\n"

func TestParseADBDevices(t *testing.T) {
	devices := ParseADBDevices(deviceList)
	if len(devices) != 2 {
		t.Fatalf("parsed %d devices, want 2", len(devices))
	}
	if devices[0].Serial != "127.0.0.1:5556" || devices[0].State != StateOnline {
		t.Fatalf("first device = %+v", devices[0])
	}
	if devices[1].State != StateOffline {
		t.Fatalf("second device = %+v", devices[1])
	}
}

func TestDetectSerialDetached(t *testing.T) {
	session := SeriallessSession{Serial: "127.0.0.1:5556"}
	attention := DetectInterference(session, ParseADBDevices("List of devices attached\n"))
	if attention == nil || attention.Kind != AttentionSerialDetached {
		t.Fatalf("attention = %+v, want serialDetached", attention)
	}
}

func TestDetectIgnoresAttachedDevice(t *testing.T) {
	session := SeriallessSession{Serial: "127.0.0.1:5556"}
	if attention := DetectInterference(session, ParseADBDevices(deviceList)); attention != nil {
		t.Fatalf("attention = %+v, want none while the entry is still attached", attention)
	}
}

func TestDetectPortChangedByIdentity(t *testing.T) {
	session := SeriallessSession{Serial: "127.0.0.1:5556", Model: "E245", DeviceCode: "antora"}
	devices := ParseADBDevices("List of devices attached\n127.0.0.1:5557\tdevice model:E245 device:antora\n")
	attention := DetectInterference(session, devices)
	if attention == nil || attention.Kind != AttentionPortChanged {
		t.Fatalf("attention = %+v, want portChanged", attention)
	}
	if attention.DetectedPort != 5557 {
		t.Fatalf("DetectedPort = %d, want 5557", attention.DetectedPort)
	}
}

func TestDetectSameModelNoFalsePositive(t *testing.T) {
	session := SeriallessSession{Serial: "127.0.0.1:5556", Model: "E245", DeviceCode: "antora"}
	devices := ParseADBDevices("List of devices attached\n127.0.0.1:5557\tdevice model:E245 device:other\n")
	attention := DetectInterference(session, devices)
	if attention == nil || attention.Kind != AttentionSerialDetached {
		t.Fatalf("attention = %+v, want serialDetached (not a port change)", attention)
	}
}

func TestAttentionNoSerialMeansNothing(t *testing.T) {
	if attention := DetectInterference(SeriallessSession{}, ParseADBDevices(deviceList)); attention != nil {
		t.Fatalf("attention = %+v, want none without a channel", attention)
	}
}

func TestSwitchPortRemembersPreference(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	if err := f.m.SwitchPort(context.Background(), f.id, 5601, true); err != nil {
		t.Fatalf("SwitchPort() error: %v", err)
	}
	profile := f.m.Profiles()[0]
	if profile.PreferredPort == nil || *profile.PreferredPort != 5601 {
		t.Fatalf("preferred port = %v, want 5601", profile.PreferredPort)
	}
	if state := f.m.Session(f.id).State; state != StateOnline {
		t.Fatalf("state = %v, want online after switching", state)
	}
	f.m.Disconnect(context.Background(), f.id)
}

func TestAttentionStopsRetry(t *testing.T) {
	f := newManagerFixture(t, func(o *ManagerOptions) { o.MonitorInterval = 5 * 1e6 })
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{ExitCode: 1, Output: "error: device offline\n"}, nil
		}
		if contains(c.Args, "devices") {
			// The entry is gone: this is external interference.
			return Result{Output: "List of devices attached\n"}, nil
		}
		return Result{}, nil
	})
	if err := f.m.Connect(context.Background(), f.id); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(f.m.Attentions()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	attentions := f.m.Attentions()
	if len(attentions) != 1 || attentions[0].Kind != AttentionSerialDetached {
		t.Fatalf("attentions = %+v", attentions)
	}
	if state := f.m.Session(f.id).State; state != StateNeedsAction {
		t.Fatalf("state = %v, want needsAction", state)
	}
	f.m.mu.Lock()
	count := f.m.retryCounts[f.id]
	f.m.mu.Unlock()
	if count != 0 {
		t.Fatalf("retry count = %d, want 0 once the user has to decide", count)
	}
	f.m.Disconnect(context.Background(), f.id)
}
