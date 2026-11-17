package core

import (
	"strings"
	"testing"
	"time"
)

func TestRemoteJoinKeepsOneSlash(t *testing.T) {
	cases := []struct{ dir, name, want string }{
		{"/sdcard/", "Download", "/sdcard/Download"},
		{"/sdcard", "Download", "/sdcard/Download"},
		{"/", "sdcard", "/sdcard"},
		{"/sdcard", "", "/sdcard"},
	}
	for _, tc := range cases {
		if got := RemoteJoin(tc.dir, tc.name); got != tc.want {
			t.Fatalf("RemoteJoin(%q, %q) = %q, want %q", tc.dir, tc.name, got, tc.want)
		}
	}
}

func TestRemoteParentAndBaseStopAtRoot(t *testing.T) {
	parents := map[string]string{
		"/sdcard/Download": "/sdcard",
		"/sdcard":          "/",
		"/":                "/",
		"/sdcard/":         "/",
	}
	for path, want := range parents {
		if got := RemoteParent(path); got != want {
			t.Fatalf("RemoteParent(%q) = %q, want %q", path, got, want)
		}
	}
	bases := map[string]string{
		"/sdcard/a.txt":     "a.txt",
		"/sdcard/Download/": "Download",
		"/":                 "/",
		"/sdcard":           "sdcard",
	}
	for path, want := range bases {
		if got := RemoteBase(path); got != want {
			t.Fatalf("RemoteBase(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestParseStatLineKeepsAPipeInTheName(t *testing.T) {
	entry, ok := ParseStatLine("regular file|12|660|-rw-rw----|1700000000|./a|b.txt", "/sdcard")
	if !ok {
		t.Fatal("a stat line with a pipe in the name must parse")
	}
	if entry.Name != "a|b.txt" || entry.Path != "/sdcard/a|b.txt" {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Dir || entry.Link || entry.Size != 12 || entry.Octal != "660" || entry.Mode != "-rw-rw----" {
		t.Fatalf("entry = %+v", entry)
	}
	if !entry.Modified.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("Modified = %v", entry.Modified)
	}
}

func TestParseStatLineReadsDirectoriesAndLinks(t *testing.T) {
	dir, ok := ParseStatLine("directory|4096|770|drwxrwx---|0|./Download", "/sdcard")
	if !ok || !dir.Dir || dir.Name != "Download" || !dir.Modified.IsZero() {
		t.Fatalf("entry = %+v ok = %v", dir, ok)
	}
	link, ok := ParseStatLine("symbolic link|4|777|lrwxrwxrwx|0|./sdcard", "/")
	if !ok || !link.Link || link.Dir || link.Path != "/sdcard" {
		t.Fatalf("entry = %+v ok = %v", link, ok)
	}
}

func TestParseStatLineRejectsNoise(t *testing.T) {
	for _, line := range []string{
		"",
		"stat: './x': No such file or directory",
		"regular file|12",
		"directory|4096|770|drwxrwx---|1700000000|.",
		"directory|4096|770|drwxrwx---|1700000000|..",
		"directory|4096|770|drwxrwx---|1700000000|./",
	} {
		if entry, ok := ParseStatLine(line, "/sdcard"); ok {
			t.Fatalf("%q parsed into %+v", line, entry)
		}
	}
}

func TestParseStatOutputSortsAndMarksTruncation(t *testing.T) {
	out := strings.Join([]string{
		"regular file|12|660|-rw-rw----|1700000000|./b.txt",
		"regular file|12|660|-rw-rw----|1700000000|./A.txt",
		"directory|4096|770|drwxrwx---|1700000000|./download",
		"directory|4096|770|drwxrwx---|1700000000|./Download",
		"stat: './gone': No such file or directory",
		truncationMarker,
		"",
	}, "\n")

	listing := ParseStatOutput(out, "/sdcard")
	if !listing.Truncated {
		t.Fatal("the truncation marker must set Truncated")
	}
	got := make([]string, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		got = append(got, entry.Name)
	}
	want := []string{"Download", "download", "A.txt", "b.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if listing.Path != "/sdcard" {
		t.Fatalf("Path = %q", listing.Path)
	}
}

func TestRemoteListScriptQuotesAPathWithAQuote(t *testing.T) {
	script, err := remoteListScript("/sdcard/John's")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `cd '/sdcard/John'\''s' || exit 9`) {
		t.Fatalf("script = %q", script)
	}
	for _, want := range []string{"stat -c '%F|%s|%a|%A|%Y|%n'", "exit 0", truncationMarker} {
		if !strings.Contains(script, want) {
			t.Fatalf("script is missing %q:\n%s", want, script)
		}
	}
}

func TestRemoteListScriptRefusesUnsafePaths(t *testing.T) {
	for _, path := range []string{"/sdcard/a\nb", "/sdcard/a\x00b", "sdcard", ""} {
		if _, err := remoteListScript(path); err == nil {
			t.Fatalf("remoteListScript(%q) must fail", path)
		}
	}
}
