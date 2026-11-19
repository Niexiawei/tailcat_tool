package core

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// LogcatOptions is what a caller asks one device's log stream for.
type LogcatOptions struct {
	// Buffers selects logcat -b buffers; empty keeps logcat's default.
	Buffers []string
	// Limit caps the kept entries; 0 means LogcatMaxLines.
	Limit int
	// OnLines is called, throttled inside the stream, after lines arrive, so
	// the window repaints without one frame per pipe read. Nil is allowed.
	OnLines func()
}

// StartLogcat starts `adb -s SERIAL logcat -v threadtime` for a device and
// returns the stream it writes into. A device has at most one stream: an old
// one is closed first.
//
// It is not a Task on purpose: a log stream has no timeout, no output history
// and no place in the task panel. The target is snapshotted here and never
// re-resolved, so a stream cannot slide onto another device.
func (m *Manager) StartLogcat(ctx context.Context, id uuid.UUID, opts LogcatOptions) (*LogcatStream, error) {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	args := []string{"-s", target.Serial, "logcat", "-v", "threadtime"}
	if len(opts.Buffers) > 0 {
		args = append(args, "-b", strings.Join(opts.Buffers, ","))
	}
	stream := newLogcatStream(opts.Limit, opts.OnLines)
	streamCtx, cancel := context.WithCancel(ctx)
	proc, err := m.runner.Start(streamCtx, Command{Path: target.ADB, Args: args}, stream.append)
	if err != nil {
		cancel()
		return nil, sanitizeError(err)
	}
	stream.attach(proc.Stop, cancel)

	m.mu.Lock()
	previous := m.logcats[id]
	m.logcats[id] = stream
	m.mu.Unlock()
	if previous != nil {
		previous.Close()
	}

	go func() {
		select {
		case code := <-proc.Done:
			stream.finish(code)
		case <-streamCtx.Done():
			// Closed by the page or by a disconnect: nothing to report.
		}
	}()
	return stream, nil
}

// StopLogcat closes a device's stream, if one is running. It is safe to call
// more than once and for a device that has no stream.
func (m *Manager) StopLogcat(id uuid.UUID) {
	m.mu.Lock()
	stream := m.logcats[id]
	delete(m.logcats, id)
	m.mu.Unlock()
	if stream != nil {
		stream.Close()
	}
}
