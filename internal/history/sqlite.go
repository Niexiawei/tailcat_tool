// Package history keeps tasks, channel logs and reachability results in a
// local SQLite file so they survive a restart. It is the only place the app
// links a SQLite driver; configuration, credentials and identity keys stay in
// their own stores.
package history

import (
	"context"
	"database/sql"
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
`

// Store records history through one writer goroutine, so the UI never waits on
// a lock and SQLite never sees concurrent writers.
type Store struct {
	db      *sql.DB
	queue   chan func(*sql.DB) error
	done    chan struct{}
	closeOn sync.Once
	mu      sync.RWMutex
	closed  bool
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
	s := &Store{db: db, queue: make(chan func(*sql.DB) error, 256), done: make(chan struct{})}
	go s.run()
	return s, nil
}

func (s *Store) run() {
	defer close(s.done)
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
	s.queue <- job
	return nil
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

// RecordLog stores one channel log line.
func (s *Store) RecordLog(record core.LogRecord) error {
	return s.submit(func(db *sql.DB) error {
		_, err := db.Exec(`INSERT INTO channel_logs (device_id, at, text) VALUES (?,?,?)`,
			record.DeviceID.String(), record.At.UnixMilli(), record.Text)
		return err
	})
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

var _ core.Recorder = (*Store)(nil)
