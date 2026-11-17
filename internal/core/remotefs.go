package core

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reading a directory off a device is one `adb shell` round trip and a pure
// parse, so everything here is a function over text the device printed.

// maxRemoteDirEntries bounds one listing. The runner keeps at most 1 MiB of a
// command's output, so an unbounded listing of a huge directory would be
// truncated silently and the page would claim files are missing. The script
// stops here and says so instead.
const maxRemoteDirEntries = 2000

// truncationMarker is what the list script prints when it stopped early.
const truncationMarker = "@@TRUNCATED"

// RemoteEntry is one entry of a device directory. There is no creation time on
// purpose: Android's filesystems rarely keep a birth time, and a column that is
// almost always empty reads as a bug.
type RemoteEntry struct {
	Name string // the entry's name, without its directory
	Path string // the absolute path on the device
	Dir  bool   // stat called it a directory
	Link bool   // stat called it a symbolic link
	// Size is the size in bytes. For a directory it is the filesystem's own
	// bookkeeping, which the page does not show.
	Size     int64
	Mode     string    // the permissions as stat prints them, such as "drwxrwx---"
	Octal    string    // the same permissions in octal, such as "770"
	Modified time.Time // zero when the device did not report a usable time
}

// Folder reports whether a tree should offer to descend into the entry. A
// symbolic link counts: on Android /sdcard is a link to the internal storage,
// and a tree that hid links would hide the folder people actually use. The
// cost is that a link to a regular file is offered as a folder; opening it says
// why it cannot be entered.
func (e RemoteEntry) Folder() bool { return e.Dir || e.Link }

// DirListing is one directory as the device reported it.
type DirListing struct {
	Path      string
	Entries   []RemoteEntry
	Truncated bool // the directory held more than maxRemoteDirEntries
}

// RemoteJoin appends a name to a device directory.
func RemoteJoin(dir, name string) string {
	if name == "" {
		return dir
	}
	return strings.TrimRight(dir, "/") + "/" + name
}

// RemoteParent is the directory above path; the root is its own parent.
func RemoteParent(path string) string {
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		return "/"
	}
	if i := strings.LastIndex(trimmed, "/"); i > 0 {
		return trimmed[:i]
	}
	return "/"
}

// RemoteBase is the last element of path; the root is its own name.
func RemoteBase(path string) string {
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		return "/"
	}
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// ParseStatLine reads one line of `stat -c '%F|%s|%a|%A|%Y|%n'`. The name comes
// last so a file whose name contains the separator still parses.
func ParseStatLine(line, dir string) (RemoteEntry, bool) {
	fields := strings.SplitN(strings.TrimRight(line, "\r"), "|", 6)
	if len(fields) != 6 {
		return RemoteEntry{}, false
	}
	if strings.HasPrefix(fields[0], "stat:") {
		return RemoteEntry{}, false
	}
	// stat echoes the path it was given, which the script passes as "./name".
	name := fields[5]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" || name == "." || name == ".." {
		return RemoteEntry{}, false
	}
	entry := RemoteEntry{
		Name:  name,
		Path:  RemoteJoin(dir, name),
		Dir:   fields[0] == "directory",
		Link:  fields[0] == "symbolic link",
		Octal: fields[2],
		Mode:  fields[3],
	}
	if size, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
		entry.Size = size
	}
	// A zero is what stat prints for "this filesystem keeps no such stamp".
	if seconds, err := strconv.ParseInt(fields[4], 10, 64); err == nil && seconds > 0 {
		entry.Modified = time.Unix(seconds, 0)
	}
	return entry, true
}

// ParseStatOutput turns the script's whole output into a sorted listing, and
// reports whether the device stopped early.
func ParseStatOutput(out, dir string) DirListing {
	listing := DirListing{Path: dir}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if line == truncationMarker {
			listing.Truncated = true
			continue
		}
		if entry, ok := ParseStatLine(line, dir); ok {
			listing.Entries = append(listing.Entries, entry)
		}
	}
	SortRemoteEntries(listing.Entries)
	return listing
}

// SortRemoteEntries puts folders (directories and links to them) first, then
// sorts case-insensitively by name, so a listing reads the way a file manager
// does: /sdcard belongs beside /storage, not after the last log file.
func SortRemoteEntries(entries []RemoteEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Folder() != b.Folder() {
			return a.Folder()
		}
		al, bl := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if al != bl {
			return al < bl
		}
		return a.Name < b.Name
	})
}

// remoteListScript builds the one shell command that lists a directory: one
// stat per entry, no process forks inside the loop, and an explicit exit code
// for "this directory is not there". Everything it runs is read-only.
func remoteListScript(dir string) (string, error) {
	if strings.ContainsAny(dir, "\x00\n\r") {
		return "", Error("路径不能包含换行或空字符")
	}
	if !strings.HasPrefix(dir, "/") {
		return "", Error("请填写绝对远端路径")
	}
	// A leading - is not a problem inside cd's argument, but a quote would end
	// the string early, so it is escaped the way POSIX shells expect.
	quoted := strings.ReplaceAll(dir, "'", `'\''`)
	limit := strconv.Itoa(maxRemoteDirEntries)
	// 2>&1 sends the shell's own complaint about cd into the output, so the
	// page can say why a folder is unreachable instead of guessing.
	return "cd '" + quoted + "' 2>&1 || exit 9\n" +
		"n=0\n" +
		"for f in ./* ./.[!.]* ; do\n" +
		"  [ -e \"$f\" ] || [ -L \"$f\" ] || continue\n" +
		"  n=$((n+1))\n" +
		"  [ \"$n\" -gt " + limit + " ] && break\n" +
		"  stat -c '%F|%s|%a|%A|%Y|%n' \"$f\" 2>/dev/null\n" +
		"done\n" +
		"[ \"$n\" -gt " + limit + " ] && echo '" + truncationMarker + "'\n" +
		"exit 0\n", nil
}
