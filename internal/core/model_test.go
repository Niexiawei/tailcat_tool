package core

import "testing"

func TestStateLabels(t *testing.T) {
	cases := []struct {
		state ConnectionState
		label string
	}{
		{StateDisconnected, "未连接"},
		{StateConnecting, "正在连接"},
		{StateOnline, "ADB 已连接"},
		{StateOffline, "ADB 离线"},
		{StateUnauthorized, "ADB 待授权"},
		{StateDisconnecting, "正在断开"},
		{StateFailed, "连接失败"},
		{StateNeedsAction, "等待处理"},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.label {
			t.Errorf("%q.String() = %q, want %q", string(tc.state), got, tc.label)
		}
		if got := tc.state.Label(); got != tc.label {
			t.Errorf("%q.Label() = %q, want %q", string(tc.state), got, tc.label)
		}
	}
}

func TestStateBusy(t *testing.T) {
	busy := []ConnectionState{StateConnecting, StateDisconnecting}
	for _, s := range []ConnectionState{
		StateDisconnected, StateOnline, StateOffline, StateUnauthorized, StateFailed, StateNeedsAction,
	} {
		if s.Busy() {
			t.Errorf("%q.Busy() = true, want false", string(s))
		}
	}
	for _, s := range busy {
		if !s.Busy() {
			t.Errorf("%q.Busy() = false, want true", string(s))
		}
	}
}

func TestTaskStateLabels(t *testing.T) {
	cases := []struct {
		state TaskState
		label string
	}{
		{TaskRunning, "执行中"},
		{TaskSucceeded, "已完成"},
		{TaskFailed, "失败"},
		{TaskCancelled, "已取消"},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.label {
			t.Errorf("%q.String() = %q, want %q", string(tc.state), got, tc.label)
		}
	}
}
