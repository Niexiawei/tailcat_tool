package core

import "testing"

// adb pull (and adb push) print one "[ 42%] /path" line per update, separated by \r when
// stdout is a TTY and by \n when it is a pipe — hence the two separators here.
func TestParseTransferPercentReadsTheLastProgressLine(t *testing.T) {
	output := "[  0%] /data/app/~~x==/com.foo-y==/base.apk\r[ 42%] /data/app/~~x==/com.foo-y==/base.apk\r"
	if percent, ok := ParseTransferPercent(output); !ok || percent != 42 {
		t.Fatalf("ParseTransferPercent() = %d, %v; want 42, true", percent, ok)
	}
}

func TestParseTransferPercentReadsTheLastProgressLineOfANewlineRun(t *testing.T) {
	output := "[  0%] /x.apk\n[ 10%] /x.apk\n[100%] /x.apk\n1 file pulled. 30.2 MB/s\n"
	if percent, ok := ParseTransferPercent(output); !ok || percent != 100 {
		t.Fatalf("ParseTransferPercent() = %d, %v; want 100, true", percent, ok)
	}
}

// The closing line looks like progress to a careless parser: it has a number
// and a slash-separated path in it.
func TestParseTransferPercentIgnoresASummaryLine(t *testing.T) {
	output := "1 file pulled. 30.2 MB/s (12345678 bytes in 0.391s)\n"
	if percent, ok := ParseTransferPercent(output); ok {
		t.Fatalf("a summary line was read as progress: %d", percent)
	}
}

func TestParseTransferPercentRejectsImpossibleValues(t *testing.T) {
	outputs := []string{"[ 120%] /x.apk", "[ -1%] /x.apk", "[abc%] /x.apk", "[ 42% /x.apk", "[] /x.apk", ""}
	for _, output := range outputs {
		if percent, ok := ParseTransferPercent(output); ok {
			t.Fatalf("ParseTransferPercent(%q) accepted garbage: %d", output, percent)
		}
	}
}

// adb push 打的是同一种进度行；名字里带 Pull 会让下一个人再写一份解析。
func TestParseTransferPercentReadsAPushLine(t *testing.T) {
	percent, ok := ParseTransferPercent("[ 42%] /sdcard/Download/update.zip\r[100%] /sdcard/Download/update.zip\n")
	if !ok || percent != 100 {
		t.Fatalf("ParseTransferPercent() = %d, %v; want the last record", percent, ok)
	}
	if _, ok := ParseTransferPercent("Performing Streamed Install\n"); ok {
		t.Fatal("an install line is not a percentage")
	}
}
