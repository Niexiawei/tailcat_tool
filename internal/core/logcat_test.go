package core

import (
	"fmt"
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

// logcatText is the short form of a line logcat prints, for tests that do not
// care about the timestamp.
func logcatText(level, tag, text string) string {
	return "11-18 09:30:01.123  1234  1250 " + level + " " + tag + ": " + text + "\n"
}

// logcatTextMS is logcatText with a millisecond of its own. Tests that need many
// lines must vary it: logcat stamps one call's lines identically, and identical
// stamps fold into a single entry on purpose.
func logcatTextMS(ms int, level, tag, text string) string {
	return "11-18 09:30:01." + fmt.Sprintf("%03d", ms%1000) + "  1234  1250 " + level + " " + tag + ": " + text + "\n"
}

// newTestLogcatStream builds a stream with a small ring and a notify counter.
func newTestLogcatStream(limit int) (*LogcatStream, *int) {
	noted := 0
	return newLogcatStream(limit, func() { noted++ }), &noted
}

func TestLogcatBufferKeepsTheNewestLines(t *testing.T) {
	stream, _ := newTestLogcatStream(3)
	for i, text := range []string{"one", "two", "three", "four", "five"} {
		stream.append(logcatTextMS(i, "I", "Tag", text))
	}
	snap := stream.Snapshot(LogcatFilter{})
	if len(snap.Entries) != 3 {
		t.Fatalf("kept %d lines, want 3", len(snap.Entries))
	}
	if snap.Entries[0].Text != "three" || snap.Entries[2].Text != "five" {
		t.Fatalf("kept %q…%q, want the three newest", snap.Entries[0].Text, snap.Entries[2].Text)
	}
	if snap.Dropped != 2 {
		t.Fatalf("Dropped = %d, want 2", snap.Dropped)
	}
	if snap.Received != 5 {
		t.Fatalf("Received = %d, want 5", snap.Received)
	}
}

func TestLogcatContinuationLineJoinsThePreviousEntry(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	stream.append(logcatText("E", "AndroidRuntime", "FATAL EXCEPTION: main"))
	stream.append("  at com.demo.Main.main(Main.java:12)\n")
	stream.append("\tCaused by: boom\n")
	snap := stream.Snapshot(LogcatFilter{})
	if len(snap.Entries) != 1 {
		t.Fatalf("kept %d lines, want one entry with the continuation folded in", len(snap.Entries))
	}
	want := "FATAL EXCEPTION: main\n  at com.demo.Main.main(Main.java:12)\n\tCaused by: boom"
	if snap.Entries[0].Text != want {
		t.Fatalf("Text = %q, want %q", snap.Entries[0].Text, want)
	}
}

func TestLogcatContinuationWithoutAPreviousLineIsDropped(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	stream.append("  orphan continuation\n")
	stream.append("--------- beginning of main\n")
	if snap := stream.Snapshot(LogcatFilter{}); len(snap.Entries) != 0 {
		t.Fatalf("kept %d lines, want none", len(snap.Entries))
	}
}

func TestLogcatLevelRankOrdersEveryLevel(t *testing.T) {
	ordered := []LogcatLevel{LogcatVerbose, LogcatDebug, LogcatInfo, LogcatWarn, LogcatError, LogcatFatal}
	for i := 1; i < len(ordered); i++ {
		if ordered[i].Rank() <= ordered[i-1].Rank() {
			t.Fatalf("%q ranks %d, not above %q (%d)", ordered[i], ordered[i].Rank(), ordered[i-1], ordered[i-1].Rank())
		}
	}
	if LogcatLevel("?").Rank() > LogcatVerbose.Rank() {
		t.Fatalf("an unknown level ranks above verbose: a filter would hide it")
	}
}

func TestLogcatLevelLabelsAreChinese(t *testing.T) {
	want := map[LogcatLevel]string{
		LogcatVerbose: "详细", LogcatDebug: "调试", LogcatInfo: "信息",
		LogcatWarn: "警告", LogcatError: "错误", LogcatFatal: "致命",
	}
	for level, label := range want {
		if got := level.Label(); got != label {
			t.Fatalf("%q label = %q, want %q", level, got, label)
		}
	}
}

func TestLogcatFilterKeepsMinimumLevelAndAbove(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	stream.append(logcatText("V", "Tag", "verbose"))
	stream.append(logcatText("D", "Tag", "debug"))
	stream.append(logcatText("I", "Tag", "info"))
	stream.append(logcatText("W", "Tag", "warn"))
	stream.append(logcatText("E", "Tag", "error"))
	stream.append(logcatText("F", "Tag", "fatal"))
	snap := stream.Snapshot(LogcatFilter{MinLevel: LogcatWarn})
	if len(snap.Entries) != 3 {
		t.Fatalf("kept %d lines, want warn, error and fatal", len(snap.Entries))
	}
	for _, entry := range snap.Entries {
		if entry.Level.Rank() < LogcatWarn.Rank() {
			t.Fatalf("kept a %q line under a warn floor", entry.Level)
		}
	}
}

func TestLogcatFilterAnEmptyMinLevelKeepsEveryLevel(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	for _, level := range []string{"V", "D", "I", "W", "E", "F"} {
		stream.append(logcatText(level, "Tag", "line "+level))
	}
	if snap := stream.Snapshot(LogcatFilter{}); len(snap.Entries) != 6 {
		t.Fatalf("kept %d lines, want all six", len(snap.Entries))
	}
}

func TestLogcatFilterMatchesEveryTermAcrossTagAndText(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	stream.append(logcatText("I", "ActivityManager", "Started proc on display 0"))
	stream.append(logcatText("I", "OpenGLRenderer", "draw frame 12"))
	if snap := stream.Snapshot(LogcatFilter{Tags: "activity", Terms: "started display"}); len(snap.Entries) != 1 {
		t.Fatalf("kept %d lines, want only the ActivityManager one", len(snap.Entries))
	}
	if snap := stream.Snapshot(LogcatFilter{Tags: "activity draw"}); len(snap.Entries) != 0 {
		t.Fatalf("kept %d lines, want none: no line matches both tags", len(snap.Entries))
	}
}

func TestLogcatFilterIsCaseInsensitive(t *testing.T) {
	entry := LogcatEntry{Level: LogcatInfo, Tag: "AndroidRuntime", Text: "FATAL EXCEPTION"}
	if !(LogcatFilter{Tags: "androidruntime", Terms: "fatal"}).Match(entry) {
		t.Fatal("the filter is case sensitive")
	}
}

func TestLogcatSnapshotCopiesTheBuffer(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	stream.append(logcatText("I", "Tag", "original"))
	snap := stream.Snapshot(LogcatFilter{})
	snap.Entries[0].Text = "tampered"
	if again := stream.Snapshot(LogcatFilter{}); again.Entries[0].Text != "original" {
		t.Fatalf("the snapshot shared the stream's buffer: %q", again.Entries[0].Text)
	}
}

func TestLogcatSnapshotKeepsTheNewestMatchingRows(t *testing.T) {
	// Room for all 3001 lines, so nothing is evicted behind the test's back.
	stream, _ := newTestLogcatStream(4000)
	// Every tenth line matches: the snapshot returns the matches in reading
	// order, and a non-matching line at the very end does not hide them.
	for i := 0; i < 3000; i++ {
		text := "skip"
		if i%10 == 0 {
			text = "keep"
		}
		stream.append(logcatTextMS(i, "I", "Tag", text))
	}
	stream.append(logcatTextMS(999, "V", "Tag", "skip"))
	snap := stream.Snapshot(LogcatFilter{Terms: "keep"})
	if len(snap.Entries) != 300 {
		t.Fatalf("kept %d rows, want the 300 matches", len(snap.Entries))
	}
	if oldest, newest := snap.Entries[0].Seq, snap.Entries[len(snap.Entries)-1].Seq; oldest >= newest {
		t.Fatalf("Seq %d..%d, want oldest first", oldest, newest)
	}
}

func TestLogcatClearResetsTheCounters(t *testing.T) {
	stream, _ := newTestLogcatStream(3)
	for i := 0; i < 5; i++ {
		stream.append(logcatTextMS(i, "I", "Tag", "line"))
	}
	if snap := stream.Snapshot(LogcatFilter{}); snap.Dropped != 2 || snap.Received != 5 {
		t.Fatalf("Received/Dropped = %d/%d before Clear, want 5/2", snap.Received, snap.Dropped)
	}
	stream.Clear()
	snap := stream.Snapshot(LogcatFilter{})
	if len(snap.Entries) != 0 {
		t.Fatalf("kept %d entries after Clear", len(snap.Entries))
	}
	// "清空" empties the buffer and the two counters that describe it: a page
	// that says 「显示 0 条」 next to 「已接收 5 行 · 丢弃 2 条」 reads as a page
	// that lost five lines it still claims to hold.
	if snap.Received != 0 || snap.Dropped != 0 {
		t.Fatalf("Received/Dropped = %d/%d after Clear, want 0/0", snap.Received, snap.Dropped)
	}
	stream.append(logcatText("I", "Tag", "after"))
	if seq := stream.Snapshot(LogcatFilter{}).Entries[0].Seq; seq <= 5 {
		t.Fatalf("Seq = %d, want it to keep counting after Clear: it names rows, not lines", seq)
	}
}

func TestLogcatNotifyIsThrottled(t *testing.T) {
	stream, noted := newTestLogcatStream(10)
	for i := 0; i < 100; i++ {
		stream.append(logcatText("I", "Tag", "line"))
	}
	// The first chunk notifies; the rest are inside the throttle window.
	if *noted > 2 {
		t.Fatalf("notified %d times for one burst, want at most 2", *noted)
	}
}

func TestLogcatVersionMovesWhenLinesArrive(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	before := stream.Version()
	stream.append(logcatText("I", "Tag", "line"))
	if after := stream.Version(); after == before {
		t.Fatal("Version did not move although a line arrived")
	}
	stream.Clear()
	if cleared := stream.Version(); cleared == before {
		t.Fatal("Version did not move although the buffer was cleared")
	}
}

func TestLogcatCloseIsIdempotent(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	if stream.Closed() {
		t.Fatal("a fresh stream reports itself closed")
	}
	stream.Close()
	stream.Close()
	if !stream.Closed() {
		t.Fatal("Close did not close the stream")
	}
}

func TestLogcatStackFramesFromOneCallStayOneEntry(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	// One log call with newlines: logcat stores a record per line but stamps
	// them all with the same time, pid, tid, level and tag. They belong to one
	// entry, or a stack trace becomes dozens of rows.
	stream.append("11-18 09:42:35.293  1182  1182 E CCTAggProxyLogStore: java.util.concurrent.ExecutionException: boom\n" +
		"11-18 09:42:35.293  1182  1182 E CCTAggProxyLogStore: \tat dgwp.o(Unknown Source:12)\n" +
		"11-18 09:42:35.293  1182  1182 E CCTAggProxyLogStore: \tat asqd.l(Unknown Source:8)\n")
	snap := stream.Snapshot(LogcatFilter{})
	if len(snap.Entries) != 1 {
		t.Fatalf("kept %d entries, want one entry for one call", len(snap.Entries))
	}
	want := "java.util.concurrent.ExecutionException: boom\n\tat dgwp.o(Unknown Source:12)\n\tat asqd.l(Unknown Source:8)"
	if snap.Entries[0].Text != want {
		t.Fatalf("Text = %q, want the whole call", snap.Entries[0].Text)
	}
	if snap.Received != 3 {
		t.Fatalf("Received = %d, want 3: three lines were parsed", snap.Received)
	}
}

func TestLogcatLinesWithDifferentTimesStayApart(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	stream.append("11-18 09:42:35.293  1182  1182 E Tag: first\n" +
		"11-18 09:42:35.294  1182  1182 E Tag: second\n")
	if snap := stream.Snapshot(LogcatFilter{}); len(snap.Entries) != 2 {
		t.Fatalf("kept %d entries, want two: different times are different calls", len(snap.Entries))
	}
}

func TestLogcatLinesWithDifferentTagsStayApart(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	stream.append("11-18 09:42:35.293  1182  1182 E TagA: first\n" +
		"11-18 09:42:35.293  1182  1182 E TagB: second\n")
	if snap := stream.Snapshot(LogcatFilter{}); len(snap.Entries) != 2 {
		t.Fatalf("kept %d entries, want two: different tags are different calls", len(snap.Entries))
	}
}

func TestLogcatStreamCloseDoesNotWaitForTheProcess(t *testing.T) {
	stream, _ := newTestLogcatStream(10)
	release := make(chan struct{})
	stream.attach(func() { <-release }, func() {})
	done := make(chan struct{})
	go func() {
		stream.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() waited for the process: stopping one costs most of a second on Windows, and Close runs on the render thread")
	}
	close(release)
}

func TestLogcatBufferEvictsByBytes(t *testing.T) {
	stream, _ := newTestLogcatStream(1000)
	// Huge lines must not turn the ring into hundreds of megabytes: the budget
	// is bytes as well as rows, and it evicts the oldest either way.
	stream.maxBytes = 300
	for i := 0; i < 20; i++ {
		stream.append(logcatTextMS(i, "I", "Tag", strings.Repeat("x", 40)))
	}
	snap := stream.Snapshot(LogcatFilter{})
	if len(snap.Entries) == 0 || len(snap.Entries) > 3 {
		t.Fatalf("kept %d entries in a 300-byte ring, want a few", len(snap.Entries))
	}
	if snap.Dropped == 0 {
		t.Fatal("Dropped = 0 although the byte budget forced evictions")
	}
	if newest := snap.Entries[len(snap.Entries)-1].Text; !strings.HasPrefix(newest, "x") {
		t.Fatalf("the newest entry = %q", newest)
	}
}

func TestLogcatSnapshotKeepsEveryKeptEntry(t *testing.T) {
	stream, _ := newTestLogcatStream(3000)
	for i := 0; i < 2500; i++ {
		stream.append(logcatTextMS(i, "I", "Tag", "line"))
	}
	// The table is virtualized, so what the ring holds is what the page can
	// scroll through: a second, smaller display cap only confused the counts.
	if snap := stream.Snapshot(LogcatFilter{}); len(snap.Entries) != 2500 {
		t.Fatalf("snapshot holds %d entries, want all 2500 the ring kept", len(snap.Entries))
	}
}
