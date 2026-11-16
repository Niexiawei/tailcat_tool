package core

import (
	"strconv"
	"strings"
)

// procfs parsing. Everything here is a pure function over text the device
// printed, so the sampling code and its tests never need a real /proc.

// cpuTimes is one /proc/stat cpu line in clock ticks.
type cpuTimes struct{ Total, Idle uint64 }

// memInfo is what /proc/meminfo says, in bytes.
type memInfo struct{ Total, Available uint64 }

// procEntry is one process as /proc describes it.
type procEntry struct {
	PID     int
	Command string // cmdline, NULs turned into spaces and trimmed
	Jiffies uint64 // utime+stime
	// StartTicks is field 22 of /proc/<pid>/stat. Together with the PID it
	// identifies one process, so a reused PID is never mistaken for the same
	// process running twice as long.
	StartTicks uint64
	RSS        uint64 // bytes, from VmRSS
}

// parseCPUStat reads the aggregate "cpu" line of /proc/stat. Guest times are
// left out: they are already counted inside user and nice.
func parseCPUStat(text string) (cpuTimes, bool) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var total, idle uint64
		for i, field := range fields[1:] {
			if i == 8 {
				break
			}
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return cpuTimes{}, false
			}
			total += value
			if i == 3 || i == 4 { // idle, iowait
				idle += value
			}
		}
		if total == 0 {
			return cpuTimes{}, false
		}
		return cpuTimes{Total: total, Idle: idle}, true
	}
	return cpuTimes{}, false
}

// parseMemInfo reads /proc/meminfo. MemAvailable is what the kernel says a
// user may still allocate; older kernels only have MemFree, so the usual
// approximation stands in for it.
func parseMemInfo(text string) (memInfo, bool) {
	values := map[string]uint64{}
	for _, line := range strings.Split(text, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		values[strings.TrimSpace(key)] = value * 1024 // kB
	}
	total := values["MemTotal"]
	if total == 0 {
		return memInfo{}, false
	}
	available, ok := values["MemAvailable"]
	if !ok {
		available = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	return memInfo{Total: total, Available: available}, true
}

// parseProcEntry reads one process. An empty cmdline means a kernel thread,
// which has no application to belong to and no useful numbers, so it is
// rejected rather than listed.
func parseProcEntry(pid int, statLine, statusText, cmdline string) (procEntry, bool) {
	command := strings.TrimSpace(strings.ReplaceAll(cmdline, "\x00", " "))
	if command == "" {
		return procEntry{}, false
	}
	open := strings.Index(statLine, "(")
	end := strings.LastIndex(statLine, ")")
	if open < 0 || end < open {
		return procEntry{}, false
	}
	// The comm sits inside the parentheses and may contain spaces and
	// parentheses of its own, so the numbered fields start after the last one.
	fields := strings.Fields(statLine[end+1:])
	if len(fields) < 20 {
		return procEntry{}, false
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64) // field 14
	if err != nil {
		return procEntry{}, false
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64) // field 15
	if err != nil {
		return procEntry{}, false
	}
	start, err := strconv.ParseUint(fields[19], 10, 64) // field 22
	if err != nil {
		return procEntry{}, false
	}
	return procEntry{
		PID:        pid,
		Command:    command,
		Jiffies:    utime + stime,
		StartTicks: start,
		RSS:        parseVmRSS(statusText),
	}, true
}

// parseVmRSS reads VmRSS (resident bytes) out of /proc/<pid>/status.
func parseVmRSS(status string) uint64 {
	for _, line := range strings.Split(status, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "VmRSS" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0
		}
		value, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0
		}
		return value * 1024 // kB
	}
	return 0
}

// parseGLESRenderer reads the GPU renderer out of "dumpsys SurfaceFlinger",
// whose GLES line is "<vendor>, <renderer>, <version>".
func parseGLESRenderer(surfaceFlinger string) string {
	for _, line := range strings.Split(surfaceFlinger, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "GLES:") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(line, "GLES:"), ",")
		if len(parts) < 2 {
			return ""
		}
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// parseGPUBusy reads a vendor GPU counter. Vendors disagree on the shape:
// "busy total" (Qualcomm kgsl), a bare percentage, or one with a "%" suffix.
// Anything that is not a number is unavailable, never 0%.
func parseGPUBusy(text string) (percent float64, ok bool) {
	fields := strings.Fields(strings.TrimSuffix(strings.TrimSpace(text), "%"))
	if len(fields) == 0 || len(fields) > 2 {
		return 0, false
	}
	busy, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	if len(fields) == 2 {
		total, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || total <= 0 {
			return 0, false
		}
		busy = busy / total * 100
	}
	if busy < 0 || busy > 100 {
		return 0, false
	}
	return busy, true
}

// FormatBytes is a byte count as the resource page shows it.
func FormatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatUint(n, 10) + " B"
	}
	value := float64(n)
	for _, name := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return strconv.FormatFloat(value, 'f', 1, 64) + " " + name
		}
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " PB"
}
