package core

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// maxTaskOutput is how much of a task's output is kept for the UI.
const maxTaskOutput = 32768

// maxTaskHistory is how many finished tasks stay listed.
const maxTaskHistory = 100

// operationTarget is the device endpoint a task was bound to when it started.
type operationTarget struct {
	Serial     string
	ADB        string
	Generation uuid.UUID
}

// BeginADBTask starts one adb command against the device and returns its id.
func (m *Manager) BeginADBTask(ctx context.Context, id uuid.UUID, title string, args []string, timeout time.Duration) (uuid.UUID, error) {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	// Check the target now, so the caller learns about an offline device
	// before a task appears in the list; the task re-checks before running.
	if err := m.validateTarget(ctx, id, target); err != nil {
		return uuid.Nil, err
	}
	return m.startTask(id, title, target, Command{
		Path:    target.ADB,
		Args:    append([]string{"-s", target.Serial}, args...),
		Timeout: timeout,
	}), nil
}

// BeginScreen starts scrcpy for the device.
func (m *Manager) BeginScreen(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	if err := m.validateTarget(ctx, id, target); err != nil {
		return uuid.Nil, err
	}
	m.mu.Lock()
	scrcpy := m.tools.FindToolPath(ToolScrcpy, m.locate)
	m.mu.Unlock()
	if scrcpy == "" {
		return uuid.Nil, Error("未找到 scrcpy，请在设置中指定可执行文件的绝对路径")
	}
	return m.startTask(id, "屏幕控制", target, Command{
		Path: scrcpy,
		Args: []string{"-s", target.Serial, "--max-size=1280", "--video-bit-rate=4M", "--max-fps=30"},
		Env:  []string{"ADB=" + target.ADB},
		// The screen session is long-lived but not immortal.
		Timeout: 24 * time.Hour,
	}), nil
}

// RunADB runs one adb command for a device and returns its output. It goes
// through the task machinery, so it is redacted, bounded and recorded like
// every other command, and the target is validated twice as usual.
func (m *Manager) RunADB(ctx context.Context, id uuid.UUID, args []string, timeout time.Duration) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	taskID, err := m.BeginADBTask(ctx, id, "adb "+strings.Join(args, " "), args, timeout)
	if err != nil {
		return "", err
	}
	m.WaitTask(taskID)
	for _, task := range m.Tasks() {
		if task.ID == taskID {
			if task.State == TaskFailed {
				return "", Error(strings.TrimSpace(task.Output))
			}
			return task.Output, nil
		}
	}
	return "", nil
}

// TerminalCommand validates the target and returns what a terminal needs.
func (m *Manager) TerminalCommand(ctx context.Context, id uuid.UUID) (string, string, error) {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return "", "", err
	}
	if err := m.validateTarget(ctx, id, target); err != nil {
		return "", "", err
	}
	return target.ADB, target.Serial, nil
}

func (m *Manager) operationTarget(ctx context.Context, id uuid.UUID) (operationTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.profileLocked(id); !ok {
		return operationTarget{}, Error("设备不存在")
	}
	session := m.sessionLocked(id)
	if session.State.Busy() {
		return operationTarget{}, Error("该设备的 ADB 尚未进入 device 状态")
	}
	runtime, ok := m.runtimes[id]
	if !ok || runtime.serial == "" {
		return operationTarget{}, Error("设备通道已结束，请重新连接")
	}
	if session.State != StateOnline {
		return operationTarget{}, Error("该设备的 ADB 尚未进入 device 状态")
	}
	return operationTarget{Serial: runtime.serial, ADB: runtime.adb, Generation: runtime.generation}, nil
}

func (m *Manager) targetIsCurrent(id uuid.UUID, target operationTarget) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.profileLocked(id); !ok {
		return false
	}
	runtime, ok := m.runtimes[id]
	if !ok || runtime.serial == "" {
		return false
	}
	return m.isCurrentLocked(id, target.Generation) &&
		runtime.serial == target.Serial &&
		m.sessions[id].Serial == target.Serial
}

func (m *Manager) validateTarget(ctx context.Context, id uuid.UUID, target operationTarget) error {
	if !m.targetIsCurrent(id, target) {
		return ErrCancelled
	}
	res, err := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "get-state"},
		Timeout: adbStateTimeout,
	}, nil)
	if err != nil {
		return sanitizeError(err)
	}
	if !m.targetIsCurrent(id, target) {
		return ErrCancelled
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Output) != "device" {
		return Error("任务目标已离线，未执行设备操作")
	}
	return nil
}

func (m *Manager) startTask(id uuid.UUID, title string, target operationTarget, command Command) uuid.UUID {
	taskID := uuid.New()
	name := "设备"
	m.mu.Lock()
	if profile, ok := m.profileLocked(id); ok {
		name = profile.Name
	}
	m.tasks = append(m.tasks, Task{
		ID:         taskID,
		DeviceID:   id,
		DeviceName: name,
		Serial:     target.Serial,
		Title:      title,
		Started:    time.Now(),
		State:      TaskRunning,
	})
	// Trim finished history only; a running task is always visible and
	// cancellable.
	for len(m.tasks) > maxTaskHistory {
		index := -1
		for i := range m.tasks {
			if m.tasks[i].State != TaskRunning {
				index = i
				break
			}
		}
		if index < 0 {
			break
		}
		m.tasks = append(m.tasks[:index], m.tasks[index+1:]...)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.taskCancels[taskID] = cancel
	done := make(chan struct{})
	m.taskDone[taskID] = done
	m.mu.Unlock()
	m.recordTask(taskID)

	go func() {
		defer close(done)
		defer func() {
			m.mu.Lock()
			delete(m.taskCancels, taskID)
			m.notify()
			m.mu.Unlock()
		}()
		redactor := NewRedactor("")
		err := m.validateTarget(ctx, id, target)
		if err == nil {
			res, runErr := m.runner.Run(ctx, command, func(chunk string) {
				safe := redactor.Append(chunk)
				if safe == "" {
					return
				}
				m.updateTask(taskID, func(task *Task) {
					task.Output = tailStringBytes(task.Output+safe, maxTaskOutput)
				})
			})
			if tail := redactor.Flush(); tail != "" {
				m.updateTask(taskID, func(task *Task) {
					task.Output = tailStringBytes(task.Output+tail, maxTaskOutput)
				})
			}
			err = runErr
			if err == nil && !res.Cancelled {
				if reqErr := res.RequireSuccess(); reqErr != nil {
					err = reqErr
				}
			}
			if err == nil && !res.Cancelled {
				output := tailStringBytes(Redact(res.Output, ""), maxTaskOutput)
				m.updateTask(taskID, func(task *Task) {
					task.State = TaskSucceeded
					if output != "" {
						task.Output = output
					}
				})
				m.log(id, title+"已完成 · "+target.Serial)
				return
			}
		}
		if err == ErrCancelled || m.ctx.Err() != nil || ctx.Err() != nil || isCancelled(err) {
			m.updateTask(taskID, func(task *Task) { task.State = TaskCancelled })
			return
		}
		m.updateTask(taskID, func(task *Task) {
			task.State = TaskFailed
			task.Output = tailStringBytes(Redact(err.Error(), ""), maxTaskOutput)
		})
		m.log(id, title+"失败："+sanitizeError(err).Error())
	}()

	return taskID
}

func isCancelled(err error) bool { return err == ErrCancelled }

func tailStringBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}

func (m *Manager) updateTask(id uuid.UUID, mutate func(*Task)) {
	m.mu.Lock()
	for i := range m.tasks {
		if m.tasks[i].ID == id {
			mutate(&m.tasks[i])
			break
		}
	}
	m.notify()
	m.mu.Unlock()
	m.recordTask(id)
}

// recordTask writes the task's current state to history when one is wired.
func (m *Manager) recordTask(id uuid.UUID) {
	m.mu.Lock()
	var record TaskRecord
	found := false
	for i := range m.tasks {
		if m.tasks[i].ID == id {
			task := m.tasks[i]
			record = TaskRecord{
				ID: task.ID, DeviceID: task.DeviceID, DeviceName: task.DeviceName,
				Serial: task.Serial, Title: task.Title, Started: task.Started,
				Ended: time.Now(), State: task.State, Output: task.Output,
			}
			found = true
			break
		}
	}
	recorder := m.recorder
	m.mu.Unlock()
	if found && recorder != nil {
		_ = recorder.RecordTask(record)
	}
}

// LoadHistory seeds the task panel with tasks from a previous run.
func (m *Manager) LoadHistory(tasks []Task) {
	if len(tasks) == 0 {
		return
	}
	m.mu.Lock()
	seen := map[uuid.UUID]bool{}
	for _, task := range m.tasks {
		seen[task.ID] = true
	}
	for _, task := range tasks {
		if !seen[task.ID] {
			m.tasks = append(m.tasks, task)
		}
	}
	for len(m.tasks) > maxTaskHistory {
		m.tasks = m.tasks[1:]
	}
	m.notify()
	m.mu.Unlock()
}

// CancelTask stops a running task.
func (m *Manager) CancelTask(id uuid.UUID) {
	m.mu.Lock()
	cancel := m.taskCancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// WaitTask blocks until the task finished.
func (m *Manager) WaitTask(id uuid.UUID) {
	m.mu.Lock()
	done := m.taskDone[id]
	m.mu.Unlock()
	if done == nil {
		return
	}
	<-done
}

// Diagnose probes the device link and stores the route it found.
func (m *Manager) Diagnose(ctx context.Context, id uuid.UUID) error {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	address, addrErr := m.addressLocked(id)
	relaySource := ""
	if profile, ok := m.profileLocked(id); ok {
		relaySource = profile.RelaySource
	}
	pinger := m.pinger
	m.mu.Unlock()
	if addrErr != nil {
		return addrErr
	}
	relay, err := NormalizeRelay(relaySource)
	if err != nil {
		return err
	}
	outcome, err := pinger.Ping(ctx, id, address, 3, relay)
	if err != nil {
		m.log(id, "链路探测："+sanitizeError(err).Error())
		return err
	}
	if !m.targetIsCurrent(id, target) {
		return ErrCancelled
	}
	m.mu.Lock()
	m.setStateLocked(id, func(s *Session) { s.Route = outcome.Route })
	m.logLocked(id, "链路："+outcome.Route)
	m.notify()
	m.mu.Unlock()
	return nil
}

// RefreshDeviceInformation reads the model, product, Android version, serial
// and shell identity from the device.
func (m *Manager) RefreshDeviceInformation(ctx context.Context, id uuid.UUID) error {
	target, err := m.operationTarget(ctx, id)
	if err != nil {
		return err
	}
	if err := m.validateTarget(ctx, id, target); err != nil {
		return err
	}
	const script = "getprop ro.product.model; getprop ro.product.device; getprop ro.build.version.release; getprop ro.serialno; id"
	res, err := m.runner.Run(ctx, Command{
		Path:    target.ADB,
		Args:    []string{"-s", target.Serial, "shell", script},
		Timeout: 6 * time.Second,
	}, nil)
	if err != nil {
		return sanitizeError(err)
	}
	if res.ExitCode != 0 {
		return sanitizeError(Error(strings.TrimSpace(Redact(res.Output, ""))))
	}
	if !m.targetIsCurrent(id, target) {
		return ErrCancelled
	}
	lines := strings.Split(strings.ReplaceAll(res.Output, "\r", ""), "\n")
	if len(lines) < 5 {
		return nil
	}
	m.mu.Lock()
	m.setStateLocked(id, func(s *Session) {
		s.Model = lines[0]
		s.Product = lines[1]
		s.Android = lines[2]
		s.DeviceSerial = lines[3]
		s.ShellID = lines[4]
	})
	m.notify()
	m.mu.Unlock()
	return nil
}
