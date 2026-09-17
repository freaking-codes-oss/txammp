// Package proc supervises child service processes: start, stop, liveness
// and PID bookkeeping that survives across txampp invocations.
package proc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Process describes a supervised service process.
type Process struct {
	Name    string // service id, e.g. "caddy"
	Bin     string // absolute binary path
	Args    []string
	Dir     string   // working directory (optional)
	Env     []string // extra env vars (optional)
	LogFile string   // stdout+stderr are appended here
	PidFile string   // pid bookkeeping location

	cmd     *exec.Cmd
	started time.Time
}

// pidInfo is the pidfile document.
type pidInfo struct {
	PID       int       `json:"pid"`
	Bin       string    `json:"bin"`
	StartedAt time.Time `json:"started_at"`
}

// ErrNotRunning is returned when a process is not running.
var ErrNotRunning = errors.New("not running")

// Start launches the process. It fails when it is already running.
func (p *Process) Start() error {
	if pid, ok := p.Running(); ok {
		return fmt.Errorf("%s already running (pid %d)", p.Name, pid)
	}
	if err := os.MkdirAll(filepath.Dir(p.LogFile), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.PidFile), 0o755); err != nil {
		return err
	}
	log, err := os.OpenFile(p.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log %s: %w", p.LogFile, err)
	}
	defer log.Close()

	cmd := exec.Command(p.Bin, p.Args...)
	if p.Dir != "" {
		cmd.Dir = p.Dir
	}
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, p.Env...)
	cmd.Stdout = log
	cmd.Stderr = log
	prepareCommand(cmd) // platform specifics (process group on Unix)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", p.Name, err)
	}
	p.cmd = cmd
	p.started = time.Now()
	// Record the resolved binary path for PID-reuse validation.
	bin := p.Bin
	if cmd.Path != "" {
		bin = cmd.Path
	}
	info := pidInfo{PID: cmd.Process.Pid, Bin: bin, StartedAt: p.started}
	if err := writeJSON(p.PidFile, info); err != nil {
		return err
	}
	// Reap quietly so long-running parents (the TUI) never accumulate
	// zombies.
	go func() { _ = cmd.Wait() }()
	return nil
}

// Running reports the PID when the process is alive according to the
// pidfile.
func (p *Process) Running() (int, bool) {
	info, err := readPid(p.PidFile)
	if err != nil {
		return 0, false
	}
	if !alive(info.PID) {
		return 0, false
	}
	// Guard against PID recycling: on Linux verify the binary too.
	if !matchesBinary(info.PID, info.Bin) {
		return 0, false
	}
	return info.PID, true
}

// StartedAt returns when the process was started (zero when unknown).
func (p *Process) StartedAt() time.Time {
	info, err := readPid(p.PidFile)
	if err != nil {
		return time.Time{}
	}
	return info.StartedAt
}

// Stop terminates the process group: SIGTERM, wait up to timeout, then
// SIGKILL. The pidfile is removed.
func (p *Process) Stop(timeout time.Duration) error {
	info, err := readPid(p.PidFile)
	if err != nil {
		return ErrNotRunning
	}
	if !alive(info.PID) {
		os.Remove(p.PidFile)
		return ErrNotRunning
	}
	terminate(info.PID)
	deadline := time.Now().Add(timeout)
	for alive(info.PID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(info.PID) {
		kill(info.PID)
		time.Sleep(100 * time.Millisecond)
	}
	os.Remove(p.PidFile)
	return nil
}

// Restart stops (if needed) and starts again.
func (p *Process) Restart(timeout time.Duration) error {
	if _, ok := p.Running(); ok {
		if err := p.Stop(timeout); err != nil && !errors.Is(err, ErrNotRunning) {
			return err
		}
	}
	// Give the OS a beat to release sockets/ports.
	time.Sleep(150 * time.Millisecond)
	return p.Start()
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readPid(path string) (pidInfo, error) {
	var info pidInfo
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("corrupt pidfile %s: %w", path, err)
	}
	if info.PID <= 0 {
		return info, fmt.Errorf("invalid pidfile %s", path)
	}
	return info, nil
}
