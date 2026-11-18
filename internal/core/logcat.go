package core

import (
	"bytes"
	"strconv"
	"strings"
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
		if s.discarding {
			index := bytes.IndexByte(s.pending, '\n')
			if index < 0 {
				s.pending = s.pending[:0]
				return
			}
			s.pending = append(s.pending[:0], s.pending[index+1:]...)
			s.discarding = false
			continue
		}
		index := bytes.IndexByte(s.pending, '\n')
		if index < 0 {
			if len(s.pending) > logcatMaxLineBytes {
				emit(logcatTruncate(s.pending))
				s.pending = s.pending[:0]
				s.discarding = true
			}
			return
		}
		line := strings.TrimSuffix(string(s.pending[:index]), "\r")
		s.pending = append(s.pending[:0], s.pending[index+1:]...)
		if len(line) > logcatMaxLineBytes {
			line = string(logcatTruncate([]byte(line)))
		}
		emit(line)
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
