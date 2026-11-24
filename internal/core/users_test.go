package core

import (
	"context"
	"strings"
	"testing"
)

// The two shapes Android has used for a user record: pm calls the current one
// "running", and the id is the number `adb install --user` takes.
func TestParseDeviceUsersReadsIdNameAndCurrent(t *testing.T) {
	output := "Users:\n\tUserInfo{0:Owner:c13} running\n\tUserInfo{10:工作资料:404}\n"
	users := ParseDeviceUsers(output, 10)
	if len(users) != 2 {
		t.Fatalf("ParseDeviceUsers() = %+v", users)
	}
	if users[0].ID != 0 || users[0].Name != "Owner" || users[0].Current || !users[0].Running {
		t.Fatalf("first user = %+v", users[0])
	}
	if users[1].ID != 10 || users[1].Name != "工作资料" || !users[1].Current || users[1].Running {
		t.Fatalf("second user = %+v", users[1])
	}
}

// Older devices and locked-down builds answer with an exception instead of a
// list. That must read as "no users", never as a panic or a bogus entry.
func TestParseDeviceUsersSkipsGarbage(t *testing.T) {
	output := "Users:\n\tUserInfo{0:Owner:c13} running\n\trubbish\n" +
		"Error: java.lang.SecurityException: Shell does not have permission to access user 0\n" +
		"UserInfo{abc:broken:0}\n" +
		"UserInfo{11:unterminated\n"
	users := ParseDeviceUsers(output, 0)
	if len(users) != 1 || users[0].ID != 0 {
		t.Fatalf("ParseDeviceUsers() = %+v", users)
	}
}

// A profile with no name still has to be offered: the number is what adb takes.
func TestParseDeviceUsersNamesABlankProfileByItsNumber(t *testing.T) {
	users := ParseDeviceUsers("\tUserInfo{10::404}\n", -1)
	if len(users) != 1 || users[0].Name != "10" {
		t.Fatalf("ParseDeviceUsers() = %+v", users)
	}
}

func TestParseCurrentUserReadsAnInteger(t *testing.T) {
	if id, ok := ParseCurrentUser("10\n"); !ok || id != 10 {
		t.Fatalf("ParseCurrentUser(\"10\") = %d, %v", id, ok)
	}
	for _, output := range []string{"", "unknown\n", "Error: no current user\n"} {
		if id, ok := ParseCurrentUser(output); ok {
			t.Fatalf("ParseCurrentUser(%q) = %d, true", output, id)
		}
	}
}

// Connecting is the moment the profile list becomes readable, and 刷新信息 must
// refresh it too — the overview reads it from the session, not from the dialog.
func TestRefreshDeviceInformationStoresDeviceUsers(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		switch {
		case contains(c.Args, "get-state"):
			return Result{Output: "device\n"}, nil
		case containsSubstring(c.Args, "pm list users"):
			return Result{Output: "Users:\n\tUserInfo{0:Owner:c13} running\n\tUserInfo{10:工作资料:404}\n@@CURRENT\n10\n"}, nil
		case containsSubstring(c.Args, "getprop ro.product.model"):
			return Result{Output: "E245\nantora\n11\nSN123\nuid=0(root)\n"}, nil
		}
		return Result{}, nil
	})
	if err := f.m.RefreshDeviceInformation(context.Background(), f.id); err != nil {
		t.Fatalf("RefreshDeviceInformation() error: %v", err)
	}
	users := f.m.Session(f.id).Users
	if len(users) != 2 || users[1].ID != 10 || !users[1].Current {
		t.Fatalf("session.Users = %+v", users)
	}
}

// A device that will not list its users must not turn 刷新信息 into an error:
// the model and version are still worth having.
func TestRefreshDeviceInformationSurvivesAnUnreadableUserList(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		switch {
		case contains(c.Args, "get-state"):
			return Result{Output: "device\n"}, nil
		case containsSubstring(c.Args, "pm list users"):
			return Result{Output: "Error: java.lang.SecurityException\n", ExitCode: 1}, nil
		case containsSubstring(c.Args, "getprop ro.product.model"):
			return Result{Output: "E245\nantora\n11\nSN123\nuid=0(root)\n"}, nil
		}
		return Result{}, nil
	})
	if err := f.m.RefreshDeviceInformation(context.Background(), f.id); err != nil {
		t.Fatalf("RefreshDeviceInformation() error: %v", err)
	}
	if model := f.m.Session(f.id).Model; model != "E245" {
		t.Fatalf("model = %q, want the fields read before the user list", model)
	}
}

func TestDeviceUsersRequiresAnOnlineDevice(t *testing.T) {
	f := newManagerFixture(t)
	if _, err := f.m.DeviceUsers(context.Background(), f.id); err == nil {
		t.Fatal("DeviceUsers() accepted a device with no channel")
	}
	for _, call := range f.runner.commandLog() {
		if strings.Contains(call, "pm list users") {
			t.Fatalf("an offline read touched adb: %q", call)
		}
	}
}

// One round trip: the current user comes back in the same shell as the list,
// because pm list users does not say which one is current.
func TestDeviceUsersUsesOneShellRoundTrip(t *testing.T) {
	f := newManagerFixture(t)
	f.connect(t)
	f.runner.setHandler(func(_ context.Context, c Command) (Result, error) {
		if contains(c.Args, "get-state") {
			return Result{Output: "device\n"}, nil
		}
		return Result{Output: "Users:\n\tUserInfo{0:Owner:c13} running\n@@CURRENT\n0\n"}, nil
	})
	users, err := f.m.DeviceUsers(context.Background(), f.id)
	if err != nil {
		t.Fatalf("DeviceUsers() error: %v", err)
	}
	if len(users) != 1 || !users[0].Current {
		t.Fatalf("DeviceUsers() = %+v", users)
	}
	reads := 0
	for _, call := range f.runner.commandLog() {
		if strings.Contains(call, "pm list users") {
			reads++
			if !strings.Contains(call, "@@CURRENT") {
				t.Fatalf("the user list and the current user are two round trips: %q", call)
			}
		}
	}
	if reads != 1 {
		t.Fatalf("the device was asked for its users %d times, want 1: %v", reads, f.runner.commandLog())
	}
}
