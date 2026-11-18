package core

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LogcatLevel is one Android log severity, ordered from chatty to fatal.
type LogcatLevel string

// The levels logcat prints, one letter each.
const (
	LogcatVerbose LogcatLevel = "V"
	LogcatDebug   LogcatLevel = "D"
	LogcatInfo    LogcatLevel = "I"
	LogcatWarn    LogcatLevel = "W"
	LogcatError   LogcatLevel = "E"
	LogcatFatal   LogcatLevel = "F"
)

// LogcatEntry is one line logcat printed.
type LogcatEntry struct {
	// Seq numbers the line within its stream. Two log lines can be identical,
	// so nothing about the content tells them apart and the table needs this.
	Seq   uint64
	At    time.Time
	Level LogcatLevel
	PID   int
	TID   int
	Tag   string
	Text  string
}

// ParseLogcatLine parses one line of `logcat -v threadtime`:
//
//	11-18 09:30:01.123  1234  1250 I ActivityManager: Started proc
//
// now resolves the year: threadtime prints neither year nor zone, so a line is
// read as the most recent time with that month, day and clock that is not in
// the future. ok is false for the buffer separators ("--------- beginning of
// main"), for continuation lines and for anything else it does not recognise.
func ParseLogcatLine(line string, now time.Time) (LogcatEntry, bool) {
	line = strings.TrimSuffix(line, "\r")
	// The first five tokens are fixed; everything after them is the tag and
	// the message, which may both contain spaces.
	tokens := make([]string, 5)
	rest := line
	for i := range tokens {
		rest = strings.TrimLeft(rest, " ")
		cut := strings.IndexByte(rest, ' ')
		if cut < 0 {
			return LogcatEntry{}, false
		}
		tokens[i] = rest[:cut]
		rest = rest[cut+1:]
	}
	level, ok := parseLogcatLevel(tokens[4])
	if !ok {
		return LogcatEntry{}, false
	}
	at, ok := parseLogcatTime(tokens[0], tokens[1], now)
	if !ok {
		return LogcatEntry{}, false
	}
	pid, err := strconv.Atoi(tokens[2])
	if err != nil {
		return LogcatEntry{}, false
	}
	tid, err := strconv.Atoi(tokens[3])
	if err != nil {
		return LogcatEntry{}, false
	}
	// The tag ends at the first ": " logcat writes after it; a message may
	// contain colons of its own.
	tag, text, found := strings.Cut(rest, ": ")
	if !found {
		return LogcatEntry{}, false
	}
	return LogcatEntry{At: at, Level: level, PID: pid, TID: tid, Tag: tag, Text: text}, true
}

// parseLogcatLevel reads the one-letter severity.
func parseLogcatLevel(token string) (LogcatLevel, bool) {
	switch LogcatLevel(token) {
	case LogcatVerbose, LogcatDebug, LogcatInfo, LogcatWarn, LogcatError, LogcatFatal:
		return LogcatLevel(token), true
	}
	return "", false
}

// parseLogcatTime joins the month-day and the clock with a year taken from now.
// A date in the future can only be a line from the year that just ended, which
// is what a log dump read in January is full of.
func parseLogcatTime(date, clock string, now time.Time) (time.Time, bool) {
	day, err := time.Parse("01-02", date)
	if err != nil {
		return time.Time{}, false
	}
	at, err := time.Parse("15:04:05.000", clock)
	if err != nil {
		return time.Time{}, false
	}
	stamp := time.Date(now.Year(), day.Month(), day.Day(),
		at.Hour(), at.Minute(), at.Second(), at.Nanosecond(), now.Location())
	if stamp.After(now.Add(24 * time.Hour)) {
		stamp = stamp.AddDate(-1, 0, 0)
	}
	return stamp, true
}

// logcatMaxLineBytes bounds one line. A device can print a runaway line, and a
// filter that never matches it would still keep it forever.
const logcatMaxLineBytes = 8192

// logcatLineTruncated is what a longer line says at its end.
const logcatLineTruncated = "…（已截断）"

// logcatSplitter turns the chunks a pipe hands us into whole lines: one read
// can carry half a line or three.
type logcatSplitter struct {
	pending []byte
	// discarding marks the rest of an over-long line: it is swallowed up to
	// its newline instead of arriving as lines of its own.
	discarding bool
}

// feed takes one chunk and hands every whole line to emit.
func (s *logcatSplitter) feed(chunk string, emit func(string)) {
	if chunk == "" {
		return
	}
	s.pending = append(s.pending, chunk...)
	for {
		index := bytes.IndexByte(s.pending, '\n')
		if index < 0 {
			// A line that never ends: keep what fits, say it was cut, and
			// swallow the rest up to its newline.
			if !s.discarding && len(s.pending) > logcatMaxLineBytes {
				emit(logcatTruncate(s.pending))
				s.pending = s.pending[:0]
				s.discarding = true
			}
			return
		}
		line := s.pending[:index]
		// Reslice rather than copy: one burst can carry hundreds of lines, and
		// shifting the rest down once per line is quadratic. The consumed
		// prefix is simply left behind until the buffer is grown again.
		s.pending = s.pending[index+1:]
		if s.discarding {
			s.discarding = false
			continue
		}
		text := strings.TrimSuffix(string(line), "\r")
		if len(text) > logcatMaxLineBytes {
			text = string(logcatTruncate([]byte(text)))
		}
		emit(text)
	}
}

// flush hands over whatever is left when the pipe closed mid-line.
func (s *logcatSplitter) flush(emit func(string)) {
	if s.discarding {
		s.discarding = false
		s.pending = s.pending[:0]
		return
	}
	if len(s.pending) == 0 {
		return
	}
	line := strings.TrimSuffix(string(s.pending), "\r")
	s.pending = s.pending[:0]
	emit(line)
}

// logcatTruncate cuts an over-long line on a rune boundary and says so.
func logcatTruncate(line []byte) string {
	cut := completeUTF8Prefix(line[:logcatMaxLineBytes])
	if cut <= 0 {
		cut = logcatMaxLineBytes
	}
	return string(line[:cut]) + logcatLineTruncated
}

// Rank orders the levels so a filter can ask for "this and above". A level it
// cannot name ranks below Verbose, so an odd letter never hides a line.
func (l LogcatLevel) Rank() int {
	switch l {
	case LogcatVerbose:
		return 0
	case LogcatDebug:
		return 1
	case LogcatInfo:
		return 2
	case LogcatWarn:
		return 3
	case LogcatError:
		return 4
	case LogcatFatal:
		return 5
	}
	return -1
}

// Label is the Chinese name the page's level badges show.
func (l LogcatLevel) Label() string {
	switch l {
	case LogcatVerbose:
		return "详细"
	case LogcatDebug:
		return "调试"
	case LogcatInfo:
		return "信息"
	case LogcatWarn:
		return "警告"
	case LogcatError:
		return "错误"
	case LogcatFatal:
		return "致命"
	}
	return string(l)
}

// LogcatMaxLines is how many lines one stream keeps: how far back the page can
// scroll, and the only bound on the stream's memory.
const LogcatMaxLines = 5000

// LogcatMaxRows is how many of them one frame hands the table. The table is
// virtualized, but the rows are rebuilt whenever the page reloads them, so they
// are capped well below what the ring holds.
const LogcatMaxRows = 2000

// logcatNotifyInterval is the least time between two frame requests. A device
// can print thousands of lines a second, and one repaint per line would spend
// the whole frame budget on a table nobody can read that fast.
var logcatNotifyInterval = 100 * time.Millisecond

// LogcatFilter is what the page shows of a stream. It never changes what the
// device is asked for, so switching it loses no lines.
type LogcatFilter struct {
	// MinLevel keeps MinLevel and above; "" keeps every level.
	MinLevel LogcatLevel
	// Tags hits the tag, Terms hits the message. Both are space-separated and
	// case-insensitive, and every term must hit.
	Tags  string
	Terms string
}

// Match reports whether one line passes the filter.
func (f LogcatFilter) Match(entry LogcatEntry) bool {
	if f.MinLevel != "" && entry.Level.Rank() < f.MinLevel.Rank() {
		return false
	}
	return logcatContainsAll(entry.Tag, f.Tags) && logcatContainsAll(entry.Text, f.Terms)
}

// logcatContainsAll reports whether every space-separated term appears in text.
func logcatContainsAll(text, query string) bool {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return true
	}
	lower := strings.ToLower(text)
	for _, term := range terms {
		if !strings.Contains(lower, term) {
			return false
		}
	}
	return true
}

// LogcatSnapshot is what one frame draws.
type LogcatSnapshot struct {
	// Entries is oldest first, newest last, at most LogcatMaxRows of them.
	Entries []LogcatEntry
	// Received counts parsed lines since the stream started, Clear included.
	Received uint64
	// Dropped counts lines the ring had to evict.
	Dropped uint64
	// Version changes whenever the buffer did: a page compares it with what it
	// drew last frame and skips the copy when nothing came.
	Version uint64
	// Exited reports the process ended; Err says why (empty for a clean end).
	Exited bool
	Err    string
}

// LogcatStream is one running `adb logcat`: the process, a ring of its parsed
// lines, and the filter the page applies to them. It is not a Task on purpose:
// a log stream has no timeout, no output history and no place in the task
// panel.
type LogcatStream struct {
	mu       sync.Mutex
	entries  []LogcatEntry
	limit    int
	splitter logcatSplitter
	seq      uint64
	received uint64
	dropped  uint64
	version  uint64
	exited   bool
	err      string
	closed   bool
	notify   func()
	lastNote time.Time
	clock    func() time.Time
	proc     *Proc
	cancel   context.CancelFunc
}

// newLogcatStream builds a stream that keeps limit lines and calls notify at
// most every logcatNotifyInterval when lines arrive. It starts no process:
// StartLogcat wires that.
func newLogcatStream(limit int, notify func()) *LogcatStream {
	if limit <= 0 {
		limit = LogcatMaxLines
	}
	return &LogcatStream{limit: limit, notify: notify, clock: time.Now}
}

// Version is cheap enough to ask every frame.
func (s *LogcatStream) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Clear drops the kept lines; the counters keep counting, because "this stream
// has received N lines" stays true after the reader emptied it.
func (s *LogcatStream) Clear() {
	s.mu.Lock()
	s.entries = nil
	s.version++
	s.mu.Unlock()
}

// Closed reports whether Close has run.
func (s *LogcatStream) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close stops the process and cancels its context. Both are needed: the real
// runner stops on the process, the demo one on the context.
func (s *LogcatStream) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	proc, cancel := s.proc, s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if proc != nil {
		proc.Stop()
	}
}

// Snapshot copies the newest lines that pass f. It runs on the render path, so
// it copies structs (the strings are shared) and never parses.
func (s *LogcatStream) Snapshot(f LogcatFilter) LogcatSnapshot {
	s.mu.Lock()
	entries := make([]LogcatEntry, 0, min(len(s.entries), LogcatMaxRows))
	for i := len(s.entries) - 1; i >= 0 && len(entries) < LogcatMaxRows; i-- {
		if f.Match(s.entries[i]) {
			entries = append(entries, s.entries[i])
		}
	}
	snap := LogcatSnapshot{
		Received: s.received,
		Dropped:  s.dropped,
		Version:  s.version,
		Exited:   s.exited,
		Err:      s.err,
	}
	s.mu.Unlock()
	// The page reads a stream top down, oldest line first.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	snap.Entries = entries
	return snap
}

// append takes one pipe chunk: split it into lines, parse them, keep them.
func (s *LogcatStream) append(chunk string) {
	s.mu.Lock()
	s.splitter.feed(chunk, s.appendLineLocked)
	fire := s.noteLocked()
	s.mu.Unlock()
	if fire {
		s.notify()
	}
}

// appendLineLocked keeps one line, or folds it into the line before it. It runs
// with mu held.
func (s *LogcatStream) appendLineLocked(line string) {
	if entry, ok := ParseLogcatLine(line, s.clock()); ok {
		s.pushLocked(entry)
		return
	}
	// A multi-line message prints its timestamp only on the first line; the
	// rest arrive indented and belong to the entry before them.
	if line != "" && (line[0] == ' ' || line[0] == '\t') && len(s.entries) > 0 {
		last := &s.entries[len(s.entries)-1]
		last.Text += "\n" + line
		s.version++
	}
}

// pushLocked appends one parsed line and evicts the oldest when the ring is
// full.
func (s *LogcatStream) pushLocked(entry LogcatEntry) {
	// One log call that carried newlines arrives as one record per line, all
	// stamped with the same time, pid, tid, level and tag. Folding them back
	// into one entry is what keeps a stack trace a stack trace instead of
	// dozens of rows.
	if n := len(s.entries); n > 0 {
		if last := &s.entries[n-1]; last.At.Equal(entry.At) && last.PID == entry.PID &&
			last.TID == entry.TID && last.Level == entry.Level && last.Tag == entry.Tag {
			last.Text += "\n" + entry.Text
			s.received++
			s.version++
			return
		}
	}
	s.seq++
	entry.Seq = s.seq
	s.entries = append(s.entries, entry)
	s.received++
	if excess := len(s.entries) - s.limit; excess > 0 {
		s.entries = append(s.entries[:0], s.entries[excess:]...)
		s.dropped += uint64(excess)
	}
	s.version++
}

// noteLocked reports whether this is a good moment to ask for a frame. It runs
// with mu held and never calls notify itself: the window's invalidate is safe
// off the render thread, but not while the buffer is locked.
func (s *LogcatStream) noteLocked() bool {
	if s.notify == nil {
		return false
	}
	now := s.clock()
	if !s.lastNote.IsZero() && now.Sub(s.lastNote) < logcatNotifyInterval {
		return false
	}
	s.lastNote = now
	return true
}

// attach hands the stream the process that feeds it.
func (s *LogcatStream) attach(proc *Proc, cancel context.CancelFunc) {
	s.mu.Lock()
	s.proc, s.cancel = proc, cancel
	s.mu.Unlock()
}

// finish records that the process ended, flushing whatever was left mid-line.
func (s *LogcatStream) finish(code int) {
	s.mu.Lock()
	s.splitter.flush(s.appendLineLocked)
	s.exited = true
	if code != 0 {
		s.err = Errorf("adb logcat 退出码 %d", code).Error()
	}
	s.version++
	s.mu.Unlock()
	if s.notify != nil {
		s.notify()
	}
}
