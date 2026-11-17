package history

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/google/uuid"
)

// openLegacyFile writes the version-0 layout of the history file: channel logs
// but no unified logs table, and no user_version marker.
func openLegacyFile(t *testing.T, path string, fill func(db *sql.DB)) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening the legacy file: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE tasks (
  id TEXT PRIMARY KEY, device_id TEXT NOT NULL, device_name TEXT NOT NULL,
  serial TEXT NOT NULL, title TEXT NOT NULL, started_at INTEGER NOT NULL,
  ended_at INTEGER NOT NULL, state TEXT NOT NULL, output TEXT NOT NULL);
CREATE TABLE channel_logs (device_id TEXT NOT NULL, at INTEGER NOT NULL, text TEXT NOT NULL);`); err != nil {
		t.Fatalf("creating the legacy schema: %v", err)
	}
	fill(db)
}

func countRows(t *testing.T, store *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	return n
}

func TestMigrationCopiesChannelLogsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	deviceID := uuid.New()
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	openLegacyFile(t, path, func(db *sql.DB) {
		if _, err := db.Exec(`INSERT INTO tasks
(id, device_id, device_name, serial, title, started_at, ended_at, state, output)
VALUES (?,?,?,?,?,?,?,?,?)`,
			uuid.NewString(), deviceID.String(), "E245", "127.0.0.1:5556", "adb shell",
			base.UnixMilli(), base.UnixMilli(), "succeeded", "ok"); err != nil {
			t.Fatalf("seeding the legacy task: %v", err)
		}
		for i := 0; i < 2; i++ {
			if _, err := db.Exec(`INSERT INTO channel_logs (device_id, at, text) VALUES (?,?,?)`,
				deviceID.String(), base.Add(time.Duration(i)*time.Second).UnixMilli(),
				"旧日志"+strconv.Itoa(i)); err != nil {
				t.Fatalf("seeding a legacy channel log: %v", err)
			}
		}
	})

	for round := 0; round < 2; round++ {
		store, err := Open(path)
		if err != nil {
			t.Fatalf("open round %d: %v", round, err)
		}
		entries, err := store.Timeline(core.LogQuery{})
		if err != nil {
			t.Fatalf("Timeline() round %d: %v", round, err)
		}
		channels := 0
		for _, entry := range entries {
			if entry.Source != core.LogChannel {
				continue
			}
			channels++
			if entry.DeviceName != "E245" {
				t.Fatalf("migrated channel log lost its device name: %+v", entry)
			}
		}
		// Twice the same count: the second open must not migrate again.
		if channels != 2 {
			t.Fatalf("round %d copied %d channel logs, want 2", round, channels)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("closing round %d: %v", round, err)
		}
	}
}

func TestTimelineMergesAppChannelAndTaskRows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	deviceID, taskID := uuid.New(), uuid.New()
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	if err := store.RecordTask(core.TaskRecord{
		ID: taskID, DeviceID: deviceID, DeviceName: "E245", Serial: "127.0.0.1:5556",
		Title: "adb pull /sdcard/x", Started: base.Add(3 * time.Second),
		Ended: base.Add(4 * time.Second), State: core.TaskFailed, Output: "No such file or directory",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLog(core.LogRecord{DeviceID: deviceID, At: base.Add(2 * time.Second), Text: "通道已就绪"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAppLog(core.AppLogRecord{At: base.Add(time.Second), Level: core.LogError, Text: "连接失败：超时"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Timeline(core.LogQuery{})
	if err != nil {
		t.Fatalf("Timeline() error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	if entries[0].Source != core.LogTask || entries[0].TaskID != taskID {
		t.Fatalf("newest row = %+v, want the task", entries[0])
	}
	if entries[0].Text != "adb pull /sdcard/x · 失败" {
		t.Fatalf("task text = %q, want the title and its Chinese state", entries[0].Text)
	}
	if entries[0].DeviceName != "E245" || entries[0].DeviceID != deviceID {
		t.Fatalf("task row lost its device: %+v", entries[0])
	}
	if entries[1].Source != core.LogChannel || entries[1].Level != core.LogInfo {
		t.Fatalf("second row = %+v, want an info channel line", entries[1])
	}
	if entries[1].DeviceName != "E245" {
		t.Fatalf("channel row lost its device name: %+v", entries[1])
	}
	if entries[2].Source != core.LogApp || entries[2].Level != core.LogError {
		t.Fatalf("oldest row = %+v, want the app error", entries[2])
	}
	if entries[2].DeviceID != uuid.Nil || entries[2].DeviceName != "" {
		t.Fatalf("an app line must not claim a device: %+v", entries[2])
	}
}

func TestTimelineFiltersByDeviceLevelSinceAndText(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, second := uuid.New(), uuid.New()
	base := time.Now().Add(-2 * time.Hour).Truncate(time.Millisecond)
	for _, line := range []core.LogRecord{
		{DeviceID: first, At: base, Text: "第一台通道已就绪"},
		{DeviceID: first, At: base.Add(time.Minute), Text: "第一台连接失败：超时"},
		{DeviceID: second, At: base.Add(2 * time.Minute), Text: "第二台提醒"},
	} {
		if err := store.RecordLog(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordAppLog(core.AppLogRecord{At: base.Add(3 * time.Minute), Level: core.LogError, Text: "配置读取失败"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		query core.LogQuery
		want  []string
	}{
		{"every line", core.LogQuery{}, []string{"配置读取失败", "第二台提醒", "第一台连接失败：超时", "第一台通道已就绪"}},
		{"one device is not nil", core.LogQuery{DeviceID: &first}, []string{"第一台连接失败：超时", "第一台通道已就绪"}},
		{"app only is uuid.Nil", core.LogQuery{DeviceID: &uuid.Nil}, []string{"配置读取失败"}},
		{"errors only", core.LogQuery{Level: core.LogError}, []string{"配置读取失败"}},
		{"since drops older rows", core.LogQuery{Since: base.Add(time.Minute)}, []string{"配置读取失败", "第二台提醒", "第一台连接失败：超时"}},
		{"two terms both must match", core.LogQuery{Search: "第一台 超时"}, []string{"第一台连接失败：超时"}},
		{"one missing term matches nothing", core.LogQuery{Search: "第一台 不存在"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := store.Timeline(tc.query)
			if err != nil {
				t.Fatalf("Timeline() error: %v", err)
			}
			got := make([]string, len(entries))
			for i, entry := range entries {
				got[i] = entry.Text
			}
			if len(got) != len(tc.want) {
				t.Fatalf("texts = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("texts = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestTimelineSearchMatchesTaskOutputWithoutSelectingIt(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RecordTask(core.TaskRecord{
		ID: uuid.New(), DeviceID: uuid.New(), DeviceName: "E245", Serial: "127.0.0.1:5556",
		Title: "adb pull /sdcard/x", Started: time.Now().Add(-time.Minute),
		Ended: time.Now(), State: core.TaskFailed,
		Output: "adb: error: failed to stat remote object: No such file or directory",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Timeline(core.LogQuery{Search: "No such file"})
	if err != nil {
		t.Fatalf("Timeline() error: %v", err)
	}
	if len(entries) != 1 || entries[0].Source != core.LogTask {
		t.Fatalf("search over output = %+v, want the task", entries)
	}
	if strings.Contains(entries[0].Text, "No such file") {
		t.Fatalf("the row carries the whole output: %q", entries[0].Text)
	}
}

func TestTimelineLimitAppliesAfterFiltering(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mine, other := uuid.New(), uuid.New()
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	for i := 0; i < 60; i++ {
		if err := store.RecordLog(core.LogRecord{
			DeviceID: mine, At: base.Add(time.Duration(i) * time.Second),
			Text: "第" + strconv.Itoa(i) + "行",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		if err := store.RecordLog(core.LogRecord{
			DeviceID: other, At: base.Add(time.Duration(i) * time.Second), Text: "别的设备",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	entries, err := store.Timeline(core.LogQuery{DeviceID: &mine, Limit: 10})
	if err != nil {
		t.Fatalf("Timeline() error: %v", err)
	}
	if len(entries) != 10 {
		t.Fatalf("got %d rows, want the limit to cap them at 10", len(entries))
	}
	if entries[0].Text != "第59行" || entries[9].Text != "第50行" {
		t.Fatalf("limit kept %q..%q, want the newest ten of this device", entries[0].Text, entries[9].Text)
	}

	// The limit must come after the filter: a search matching one old row
	// still returns it, not the newest rows re-filtered in memory.
	narrow, err := store.Timeline(core.LogQuery{DeviceID: &mine, Search: "第1行", Limit: 10})
	if err != nil {
		t.Fatalf("Timeline() error: %v", err)
	}
	if len(narrow) != 1 || narrow[0].Text != "第1行" {
		t.Fatalf("search + limit = %+v, want the single match", narrow)
	}
}

func TestTimelineDefaultLimitIsBounded(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`
INSERT INTO logs (at, level, source, device_id, device_name, text)
WITH RECURSIVE seq(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM seq WHERE i < 700)
SELECT ?, 'info', 'channel', '', '', 'x' FROM seq`, time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("seeding rows: %v", err)
	}
	entries, err := store.Timeline(core.LogQuery{})
	if err != nil {
		t.Fatalf("Timeline() error: %v", err)
	}
	if len(entries) != core.DefaultLogLimit {
		t.Fatalf("got %d rows, want the %d row default", len(entries), core.DefaultLogLimit)
	}
}

func TestPruneKeepsThirtyDaysAndFiftyThousandRows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	if _, err := store.db.Exec(`INSERT INTO logs (at, level, source, device_id, device_name, text) VALUES (?,?,?,?,?,?)`,
		now.Add(-31*24*time.Hour).UnixMilli(), "info", "channel", "", "", "太老了"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO logs (at, level, source, device_id, device_name, text) VALUES (?,?,?,?,?,?)`,
		now.Add(-29*24*time.Hour).UnixMilli(), "info", "channel", "", "", "还在保留期内"); err != nil {
		t.Fatal(err)
	}
	if err := store.prune(now); err != nil {
		t.Fatalf("prune() error: %v", err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Timeline(core.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Text != "还在保留期内" {
		t.Fatalf("after pruning = %+v, want only the row inside 30 days", entries)
	}

	// Rows inside the window are capped, newest kept.
	if _, err := store.db.Exec(`
INSERT INTO logs (at, level, source, device_id, device_name, text)
WITH RECURSIVE seq(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM seq WHERE i < 50100)
SELECT ?, 'info', 'channel', '', '', 'x' FROM seq`, now.Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatalf("seeding rows: %v", err)
	}
	if err := store.prune(now); err != nil {
		t.Fatalf("prune() error: %v", err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, store, `SELECT COUNT(*) FROM logs`); got != core.MaxLogRows {
		t.Fatalf("kept %d rows, want the %d row cap", got, core.MaxLogRows)
	}
}

func TestTaskOutputReturnsTheWholeOutput(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	taskID := uuid.New()
	if err := store.RecordTask(core.TaskRecord{
		ID: taskID, DeviceID: uuid.New(), DeviceName: "E245", Title: "上传",
		Started: time.Now(), State: core.TaskFailed, Output: "第一行\n第二行",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	output, err := store.TaskOutput(taskID)
	if err != nil {
		t.Fatalf("TaskOutput() error: %v", err)
	}
	if output != "第一行\n第二行" {
		t.Fatalf("output = %q", output)
	}
}

func TestStoredLogTextIsRedacted(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	address := "tc" + strings.Repeat("A", 30)
	if err := store.RecordAppLog(core.AppLogRecord{
		At: time.Now(), Level: core.LogError, Text: "连接 " + address + " 失败",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLog(core.LogRecord{
		DeviceID: uuid.New(), At: time.Now(), Text: "通道 " + address + " 就绪",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Both halves: what the page would show, and what is on disk. The second
	// one is the one that matters after a restart.
	if got := countRows(t, store, `SELECT COUNT(*) FROM logs WHERE text LIKE ?`, "%"+address+"%"); got != 0 {
		t.Fatalf("%d rows kept the address on disk", got)
	}
	entries, err := store.Timeline(core.LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Text, address) {
			t.Fatalf("the timeline shows the address: %q", entry.Text)
		}
		if !strings.Contains(entry.Text, core.MsgRedacted) {
			t.Fatalf("text = %q, want the redaction marker", entry.Text)
		}
	}
}
