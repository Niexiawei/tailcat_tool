package core

import (
	"strings"
	"testing"
	"time"
)

// logcatNow is the "now" the parser resolves a line's year against.
func logcatNow() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }

func TestParseLogcatThreadtimeLine(t *testing.T) {
	line := "11-18 09:30:01.123  1234  1250 I ActivityManager: Started proc"
	entry, ok := ParseLogcatLine(line, logcatNow())
	if !ok {
		t.Fatalf("ParseLogcatLine(%q) rejected a line logcat prints", line)
	}
	want := time.Date(2026, 11, 18, 9, 30, 1, 123e6, time.UTC)
	if !entry.At.Equal(want) {
		t.Fatalf("At = %v, want %v", entry.At, want)
	}
	if entry.Level != LogcatInfo {
		t.Fatalf("Level = %q, want %q", entry.Level, LogcatInfo)
	}
	if entry.PID != 1234 || entry.TID != 1250 {
		t.Fatalf("PID/TID = %d/%d, want 1234/1250", entry.PID, entry.TID)
	}
	if entry.Tag != "ActivityManager" {
		t.Fatalf("Tag = %q, want ActivityManager", entry.Tag)
	}
	if entry.Text != "Started proc" {
		t.Fatalf("Text = %q, want %q", entry.Text, "Started proc")
	}
}

func TestParseLogcatLineKeepsATagWithSpaces(t *testing.T) {
	entry, ok := ParseLogcatLine("11-18 09:30:01.123  1234  1250 I Some Tag: text", logcatNow())
	if !ok {
		t.Fatal("a tag with a space was rejected")
	}
	if entry.Tag != "Some Tag" || entry.Text != "text" {
		t.Fatalf("Tag/Text = %q/%q, want %q/%q", entry.Tag, entry.Text, "Some Tag", "text")
	}
}

func TestParseLogcatLineKeepsAMessageWithAColon(t *testing.T) {
	entry, ok := ParseLogcatLine("11-18 09:30:01.123  1234  1250 E AndroidRuntime: FATAL: main", logcatNow())
	if !ok {
		t.Fatal("a message containing a colon was rejected")
	}
	if entry.Tag != "AndroidRuntime" || entry.Text != "FATAL: main" {
		t.Fatalf("Tag/Text = %q/%q, want %q/%q", entry.Tag, entry.Text, "AndroidRuntime", "FATAL: main")
	}
}

func TestParseLogcatRejectsBufferSeparator(t *testing.T) {
	if _, ok := ParseLogcatLine("--------- beginning of main", logcatNow()); ok {
		t.Fatal("a buffer separator was parsed as a log line")
	}
}

func TestParseLogcatRejectsAnUnrelatedLine(t *testing.T) {
	for _, line := range []string{"error: device offline", "", "11-18 09:30:01.123  1234  1250 I ActivityManager"} {
		if _, ok := ParseLogcatLine(line, logcatNow()); ok {
			t.Fatalf("ParseLogcatLine(%q) accepted a line that is not one", line)
		}
	}
}

func TestLogcatTimeUsesTheCurrentYear(t *testing.T) {
	entry, ok := ParseLogcatLine("01-05 08:00:00.000  1  1 I Tag: text", logcatNow())
	if !ok {
		t.Fatal("line rejected")
	}
	if got := entry.At.Year(); got != 2026 {
		t.Fatalf("year = %d, want 2026", got)
	}
}

func TestLogcatTimeFromDecemberStaysInThePast(t *testing.T) {
	// A dump read in January still holds December lines: they belong to the
	// year that just ended, not to a December eleven months away.
	now := time.Date(2027, 1, 5, 12, 0, 0, 0, time.UTC)
	entry, ok := ParseLogcatLine("12-31 23:00:00.000  1  1 I Tag: text", now)
	if !ok {
		t.Fatal("line rejected")
	}
	if got := entry.At.Year(); got != 2026 {
		t.Fatalf("year = %d, want 2026", got)
	}
}

func TestLogcatTimeSkipsAnImpossibleDate(t *testing.T) {
	if _, ok := ParseLogcatLine("02-30 10:00:00.000  1  1 I Tag: text", logcatNow()); ok {
		t.Fatal("February 30th was accepted")
	}
}

func TestLogcatSplitterKeepsPartialLines(t *testing.T) {
	var splitter logcatSplitter
	var lines []string
	emit := func(line string) { lines = append(lines, line) }
	splitter.feed("a\nb", emit)
	splitter.feed("c\n", emit)
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "bc" {
		t.Fatalf("lines = %q, want [a bc]", lines)
	}
}

func TestLogcatSplitterHandlesCRLF(t *testing.T) {
	var splitter logcatSplitter
	var lines []string
	splitter.feed("a\r\nb\r\n", func(line string) { lines = append(lines, line) })
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Fatalf("lines = %q, want [a b] without the carriage returns", lines)
	}
}

func TestLogcatLongLineIsTruncated(t *testing.T) {
	var splitter logcatSplitter
	var lines []string
	// One runaway line with no newline for 20 KiB: the splitter has to bound it
	// rather than keep growing, and the rest of the line must be swallowed
	// instead of surfacing as lines of its own.
	splitter.feed(strings.Repeat("x", 20000), func(line string) { lines = append(lines, line) })
	splitter.feed("\n11-18 09:30:01.123  1  1 I Tag: after\n", func(line string) { lines = append(lines, line) })
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2 (the runaway line and the one after it)", len(lines))
	}
	if !strings.HasSuffix(lines[0], logcatLineTruncated) {
		t.Fatalf("the long line does not say it was cut: %q…", lines[0][:40])
	}
	if len(lines[0]) > logcatMaxLineBytes+len(logcatLineTruncated) {
		t.Fatalf("kept %d bytes, want at most %d", len(lines[0]), logcatMaxLineBytes+len(logcatLineTruncated))
	}
	if lines[1] != "11-18 09:30:01.123  1  1 I Tag: after" {
		t.Fatalf("the line after the runaway one = %q", lines[1])
	}
}

func TestLogcatSplitterFlushesWhatIsLeft(t *testing.T) {
	var splitter logcatSplitter
	var lines []string
	splitter.feed("tail without a newline", func(line string) { lines = append(lines, line) })
	if len(lines) != 0 {
		t.Fatalf("lines = %q, want none before the flush", lines)
	}
	splitter.flush(func(line string) { lines = append(lines, line) })
	if len(lines) != 1 || lines[0] != "tail without a newline" {
		t.Fatalf("lines = %q, want the leftover line", lines)
	}
}
