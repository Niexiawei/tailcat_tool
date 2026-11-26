package core

import (
	"strconv"
	"strings"
)

// ParseTransferPercent reads how far an `adb pull` or `adb push` has got from
// what it printed.
//
// The progress line is "[ 42%] /data/app/~~x==/com.foo-y==/base.apk", terminated
// by \r on a TTY and by \n through a pipe — and some adb builds print it not at
// all. Callers therefore treat "no answer" as indeterminate progress rather
// than as zero, and only ever look at the last record: the earlier ones are
// history, and reading them would make the bar jump backwards.
func ParseTransferPercent(output string) (int, bool) {
	percent, found := 0, false
	for _, raw := range strings.FieldsFunc(output, func(r rune) bool { return r == '\r' || r == '\n' }) {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		end := strings.Index(line, "]")
		if end < 0 {
			continue
		}
		inner := strings.TrimSpace(line[1:end])
		if !strings.HasSuffix(inner, "%") {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(inner, "%")))
		if err != nil || value < 0 || value > 100 {
			continue
		}
		percent, found = value, true
	}
	return percent, found
}
