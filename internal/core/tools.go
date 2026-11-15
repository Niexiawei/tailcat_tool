package core

import (
	"os"
	"path/filepath"
	"runtime"
)

// ToolComponent names an external executable the app drives.
type ToolComponent string

// The external tools this build needs. tailcat itself is a library now, so it
// is not a component.
const (
	ToolADB    ToolComponent = "adb"
	ToolScrcpy ToolComponent = "scrcpy"
)

// AllTools lists the components in the order the UI shows them.
func AllTools() []ToolComponent { return []ToolComponent{ToolADB, ToolScrcpy} }

// Label is the name shown in the UI.
func (c ToolComponent) Label() string {
	if c == ToolADB {
		return "ADB"
	}
	return string(c)
}

// ExecutableName is the file name to look for on this platform.
func (c ToolComponent) ExecutableName() string { return string(c) }

// Required reports whether the app can work without the tool.
func (c ToolComponent) Required() bool { return c == ToolADB }

// ToolSettings holds the resolved absolute paths.
type ToolSettings struct {
	ADB    string `json:"adb"`
	Scrcpy string `json:"scrcpy"`
}

// Get returns the configured path for a component.
func (t ToolSettings) Get(c ToolComponent) string {
	switch c {
	case ToolADB:
		return t.ADB
	case ToolScrcpy:
		return t.Scrcpy
	default:
		return ""
	}
}

// Set stores the path for a component.
func (t *ToolSettings) Set(c ToolComponent, path string) {
	switch c {
	case ToolADB:
		t.ADB = path
	case ToolScrcpy:
		t.Scrcpy = path
	}
}

// LocateTool looks an executable up on this machine. It is injected so the
// core can be tested without touching the real PATH.
type LocateTool func(name string) (string, bool)

// FindToolPath returns the configured path when it is an absolute executable,
// otherwise it asks locate. The empty string means "not available".
func (t ToolSettings) FindToolPath(c ToolComponent, locate LocateTool) string {
	if path := t.Get(c); path != "" && filepath.IsAbs(path) && isExecutable(path) {
		return path
	}
	if locate == nil {
		return ""
	}
	if path, ok := locate(c.ExecutableName()); ok {
		return path
	}
	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		// Windows decides by extension, not by mode bits.
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}
