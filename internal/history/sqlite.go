// Package history keeps tasks, channel logs and reachability results in a
// local SQLite file so they survive a restart. It is the only place the app
// links a SQLite driver; configuration, credentials and identity keys stay in
// their own stores.
package history

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Niexiawei/tailcat-tool/internal/core"
	"github.com/google/uuid"
	_ "modernc.org/sqlite" // pure Go driver, no cgo
)

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
  id          TEXT PRIMARY KEY,
  device_id   TEXT NOT NULL,
  device_name TEXT NOT NULL,
  serial      TEXT NOT NULL,
  title       TEXT NOT NULL,
  started_at  INTEGER NOT NULL,
  ended_at    INTEGER NOT NULL,
  state       TEXT NOT NULL,
  output      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_started ON tasks(started_at DESC);
CREATE TABLE IF NOT EXISTS channel_logs (
  device_id TEXT NOT NULL,
  at        INTEGER NOT NULL,
  text      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS channel_logs_at ON channel_logs(at DESC);
CREATE TABLE IF NOT EXISTS reachability (
  device_id  TEXT NOT NULL,
  name       TEXT NOT NULL,
  state      TEXT NOT NULL,
  latency    TEXT NOT NULL,
  route      TEXT NOT NULL,
  detail     TEXT NOT NULL,
  checked_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS reachability_checked ON reachability(checked_at DESC);
CREATE TABLE IF NOT EXISTS logs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  at          INTEGER NOT NULL,
  level       TEXT NOT NULL,
  source      TEXT NOT NULL,
  device_id   TEXT NOT NULL,
  device_name TEXT NOT NULL,
  text        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS logs_at ON logs(at DESC);
CREATE INDEX IF NOT EXISTS logs_device_at ON logs(device_id, at DESC);
`

// schemaVersion is what Open writes into PRAGMA user_version once the unified
// logs table has taken over channel_logs. A version below it is migrated.
const schemaVersion = 1

// Store records history through one writer goroutine, so the UI never waits on
// a lock and SQLite never sees concurrent writers.
type Store struct {
	db      *sql.DB
	queue   chan func(*sql.DB) error
	done    chan struct{}
	closeOn sync.Once
	mu      sync.RWMutex
	closed  bool
	// logWrites counts log inserts on the writer goroutine, which owns it.
	logWrites int
}

// Open creates or opens the history file at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// The migration runs before the writer goroutine exists: the first query
	// may follow Open immediately, and it must see the copied rows.
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, queue: make(chan func(*sql.DB) error, 256), done: make(chan struct{})}
	go s.run()
	return s, nil
}

// migrate copies the old channel_logs table into logs exactly once.
// channel_logs stays untouched: deleting user data to save space is not this
// function's call.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= schemaVersion {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
INSERT INTO logs (at, level, source, device_id, device_name, text)
SELECT at, 'info', 'channel', device_id,
       COALESCE((SELECT t.device_name FROM tasks t
                 WHERE t.device_id = channel_logs.device_id
                 ORDER BY t.started_at DESC LIMIT 1), ''),
       text
FROM channel_logs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`PRAGMA user_version = ` + itoa(schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// itoa renders a small non-negative integer, so the pragma above stays one
// constant instead of a format string.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// pruneLogs drops rows past the retention window and then the oldest rows
// above the cap. Retention is a hard limit with no setting, so the file stays
// around a few megabytes.
func pruneLogs(db *sql.DB, now time.Time) error {
	if _, err := db.Exec(`DELETE FROM logs WHERE at < ?`, now.Add(-core.LogRetention).UnixMilli()); err != nil {
		return err
	}
	_, err := db.Exec(`DELETE FROM logs WHERE id NOT IN (SELECT id FROM logs ORDER BY at DESC LIMIT ?)`, core.MaxLogRows)
	return err
}

// afterLogWrite prunes every thousandth insert, so a long session cannot grow
// the file far past the cap between opens.
func (s *Store) afterLogWrite(db *sql.DB) {
	s.logWrites++
	if s.logWrites%1000 == 0 {
		_ = pruneLogs(db, time.Now())
	}
}

// prune runs the retention rules now; Open runs the same rules on startup.
func (s *Store) prune(now time.Time) error {
	return s.submit(func(db *sql.DB) error { return pruneLogs(db, now) })
}

func (s *Store) run() {
	defer close(s.done)
	_ = pruneLogs(s.db, time.Now())
	for job := range s.queue {
		// A history write that fails must never take the app down.
		_ = job(s.db)
	}
}

func (s *Store) submit(job func(*sql.DB) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return core.Error("历史库已关闭")
	}
	// Never block the caller: history is best effort, and a caller may hold
	// a lock the window reads every frame.
	select {
	case s.queue <- job:
		return nil
	default:
		return core.Error("历史库繁忙，已跳过这条记录")
	}
}

// RecordTask stores one task.
func (s *Store) RecordTask(record core.TaskRecord) error {
	return s.submit(func(db *sql.DB) error {
		_, err := db.Exec(`INSERT INTO tasks
(id, device_id, device_name, serial, title, started_at, ended_at, state, output)
VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET ended_at=excluded.ended_at, state=excluded.state, output=excluded.output`,
			record.ID.String(), record.DeviceID.String(), record.DeviceName, record.Serial,
			record.Title, record.Started.UnixMilli(), record.Ended.UnixMilli(),
			string(record.State), record.Output)
		return err
	})
}

// RecordLog stores one channel log line. Device names are not written here:
// the timeline resolves them from the tasks table, so a line recorded before
// the device's first task is still labelled.
func (s *Store) RecordLog(record core.LogRecord) error {
	return s.submit(func(db *sql.DB) error {
		// Redact here as well as in the manager: this is the last wall before
		// the file, and one caller that forgets is a credential on disk.
		_, err := db.Exec(`INSERT INTO logs (at, level, source, device_id, device_name, text)
VALUES (?,?,?,?,?,?)`,
			record.At.UnixMilli(), string(core.LogInfo), string(core.LogChannel),
			record.DeviceID.String(), "", core.Redact(record.Text, ""))
		if err == nil {
			s.afterLogWrite(db)
		}
		return err
	})
}

// RecordAppLog stores one line the app itself produced, with no device.
func (s *Store) RecordAppLog(record core.AppLogRecord) error {
	level := record.Level
	if level == "" {
		level = core.LogInfo
	}
	return s.submit(func(db *sql.DB) error {
		_, err := db.Exec(`INSERT INTO logs (at, level, source, device_id, device_name, text)
VALUES (?,?,?,?,?,?)`,
			record.At.UnixMilli(), string(level), string(core.LogApp), "", "", core.Redact(record.Text, ""))
		if err == nil {
			s.afterLogWrite(db)
		}
		return err
	})
}

// escapeLike makes a search term literal: the user typed % and _, not a
// pattern.
func escapeLike(term string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
}

// logSelection is the app and channel half of the timeline query.
func logSelection(query core.LogQuery) (string, []any) {
	conditions := []string{"1 = 1"}
	var args []any
	switch {
	case query.DeviceID == nil:
		// Every device and every app line.
	case *query.DeviceID == uuid.Nil:
		conditions = append(conditions, "l.device_id = ''")
	default:
		conditions = append(conditions, "l.device_id = ?")
		args = append(args, query.DeviceID.String())
	}
	if query.Level != "" {
		conditions = append(conditions, "l.level = ?")
		args = append(args, string(query.Level))
	}
	if !query.Since.IsZero() {
		conditions = append(conditions, "l.at >= ?")
		args = append(args, query.Since.UnixMilli())
	}
	for _, term := range strings.Fields(query.Search) {
		conditions = append(conditions, `l.text LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(term)+"%")
	}
	return `SELECT l.at AS at, l.level AS level, l.source AS source, l.device_id AS device_id,
       COALESCE(NULLIF(l.device_name, ''),
                (SELECT t.device_name FROM tasks t WHERE t.device_id = l.device_id
                 ORDER BY t.started_at DESC LIMIT 1), '') AS device_name,
       l.text AS text, '' AS ref, 'log:' || l.id AS row_key
  FROM logs l WHERE ` + strings.Join(conditions, " AND "), args
}

// taskSelection is the task half of the timeline: the row says when the task
// started and how it ended, never its output.
func taskSelection(query core.LogQuery) (string, []any) {
	conditions := []string{"1 = 1"}
	var args []any
	// A task row is an info row, so asking for errors or warnings excludes
	// tasks outright.
	if query.Level != "" && query.Level != core.LogInfo {
		conditions = append(conditions, "0 = 1")
	}
	switch {
	case query.DeviceID == nil:
	case *query.DeviceID == uuid.Nil:
		// App-only queries never show a task.
		conditions = append(conditions, "0 = 1")
	default:
		conditions = append(conditions, "t.device_id = ?")
		args = append(args, query.DeviceID.String())
	}
	if !query.Since.IsZero() {
		conditions = append(conditions, "t.started_at >= ?")
		args = append(args, query.Since.UnixMilli())
	}
	for _, term := range strings.Fields(query.Search) {
		conditions = append(conditions, `(t.title LIKE ? ESCAPE '\' OR t.output LIKE ? ESCAPE '\')`)
		pattern := "%" + escapeLike(term) + "%"
		args = append(args, pattern, pattern)
	}
	return `SELECT t.started_at AS at, 'info' AS level, 'task' AS source, t.device_id AS device_id,
       t.device_name AS device_name,
       t.title || ' · ' || CASE t.state
         WHEN 'running' THEN '执行中' WHEN 'succeeded' THEN '已完成'
         WHEN 'failed' THEN '失败' WHEN 'cancelled' THEN '已取消'
         ELSE t.state END AS text,
       t.id AS ref, 'task:' || t.id AS row_key
  FROM tasks t WHERE ` + strings.Join(conditions, " AND "), args
}

// Timeline merges the app lines, every channel line and the tasks into one
// newest-first slice. The limit applies after filtering, in SQL.
func (s *Store) Timeline(query core.LogQuery) ([]core.TimelineEntry, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = core.DefaultLogLimit
	}
	logs, logArgs := logSelection(query)
	tasks, taskArgs := taskSelection(query)
	args := append(append(logArgs, taskArgs...), limit)
	rows, err := s.db.Query(`SELECT at, level, source, device_id, device_name, text, ref, row_key FROM (
`+logs+`
  UNION ALL
`+tasks+`
) ORDER BY at DESC, ref DESC, row_key DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.TimelineEntry
	for rows.Next() {
		var (
			at                             int64
			level, source, name            string
			deviceID, text, taskID, rowKey string
		)
		if err := rows.Scan(&at, &level, &source, &deviceID, &name, &text, &taskID, &rowKey); err != nil {
			return nil, err
		}
		entry := core.TimelineEntry{
			Key:        rowKey,
			At:         time.UnixMilli(at),
			Level:      core.LogLevel(level),
			Source:     core.LogSource(source),
			DeviceName: name,
			Text:       text,
		}
		if parsed, err := uuid.Parse(deviceID); err == nil {
			entry.DeviceID = parsed
		}
		if parsed, err := uuid.Parse(taskID); err == nil {
			entry.TaskID = parsed
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// TaskOutput returns what one task printed, for its dialog.
func (s *Store) TaskOutput(id uuid.UUID) (string, error) {
	var output string
	err := s.db.QueryRow(`SELECT output FROM tasks WHERE id = ?`, id.String()).Scan(&output)
	if errors.Is(err, sql.ErrNoRows) {
		return "", core.Error(core.MsgTaskOutputMissing)
	}
	return output, err
}

// RecordReachability stores one batch of rows.
func (s *Store) RecordReachability(records []core.ReachabilityRecord) error {
	if len(records) == 0 {
		return nil
	}
	return s.submit(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO reachability
(device_id, name, state, latency, route, detail, checked_at) VALUES (?,?,?,?,?,?,?)`)
		if err != nil {
			tx.Rollback()
			return err
		}
		defer stmt.Close()
		for _, record := range records {
			if _, err := stmt.Exec(record.DeviceID.String(), record.Name, record.State,
				record.Latency, record.Route, record.Detail, record.CheckedAt.UnixMilli()); err != nil {
				tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	})
}

// RecentTasks returns the newest tasks, oldest first.
func (s *Store) RecentTasks(limit int) ([]core.Task, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, device_id, device_name, serial, title, started_at, state, output
FROM tasks ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Task
	for rows.Next() {
		var (
			id, deviceID, name, serial, title, state, output string
			started                                          int64
		)
		if err := rows.Scan(&id, &deviceID, &name, &serial, &title, &started, &state, &output); err != nil {
			return nil, err
		}
		task := core.Task{
			DeviceName: name,
			Serial:     serial,
			Title:      title,
			Started:    time.UnixMilli(started),
			State:      core.TaskState(state),
			Output:     output,
		}
		if parsed, err := uuid.Parse(id); err == nil {
			task.ID = parsed
		}
		if parsed, err := uuid.Parse(deviceID); err == nil {
			task.DeviceID = parsed
		}
		out = append(out, task)
	}
	// Oldest first so the panel appends like a log.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// Close flushes and closes the file.
func (s *Store) Close() error {
	s.closeOn.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.queue)
		s.mu.Unlock()
		<-s.done
	})
	return s.db.Close()
}

// Flush waits until every queued write has been attempted.
func (s *Store) Flush(ctx context.Context) error {
	done := make(chan struct{})
	// The job closes done when the writer reaches it, i.e. after every write
	// queued before this call has been attempted.
	if err := s.submit(func(*sql.DB) error { close(done); return nil }); err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var (
	_ core.Recorder      = (*Store)(nil)
	_ core.HistoryReader = (*Store)(nil)
)

// clearLogWhere is the deletion range: device and time. Level and keywords are
// left out on purpose — they narrow what is shown, not what is swept.
func clearLogWhere(query core.LogQuery) (string, []any) {
	conditions := []string{"1 = 1"}
	var args []any
	switch {
	case query.DeviceID == nil:
	case *query.DeviceID == uuid.Nil:
		conditions = append(conditions, "device_id = ''")
	default:
		conditions = append(conditions, "device_id = ?")
		args = append(args, query.DeviceID.String())
	}
	if !query.Since.IsZero() {
		conditions = append(conditions, "at >= ?")
		args = append(args, query.Since.UnixMilli())
	}
	return strings.Join(conditions, " AND "), args
}

// ClearLogs deletes the log rows in range. No VACUUM: the file will not shrink,
// but the space is reused, and a vacuum on a large file would freeze the app.
func (s *Store) ClearLogs(query core.LogQuery) (int64, error) {
	type outcome struct {
		removed int64
		err     error
	}
	done := make(chan outcome, 1)
	where, args := clearLogWhere(query)
	submitErr := s.submit(func(db *sql.DB) error {
		var result outcome
		res, err := db.Exec(`DELETE FROM logs WHERE `+where, args...)
		if err == nil {
			result.removed, result.err = res.RowsAffected()
		} else {
			result.err = err
		}
		done <- result
		return nil
	})
	if submitErr != nil {
		return 0, submitErr
	}
	result := <-done
	return result.removed, result.err
}
