package proc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestProcess(t *testing.T, args ...string) *Process {
	t.Helper()
	dir := t.TempDir()
	sleepBin := "sleep"
	if runtime.GOOS == "windows" {
		sleepBin = "cmd"
		args = []string{"/C", "pause"}
	}
	return &Process{
		Name:    "test",
		Bin:     sleepBin,
		Args:    args,
		LogFile: filepath.Join(dir, "test.log"),
		PidFile: filepath.Join(dir, "test.pid"),
	}
}

func TestStartStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process test")
	}
	p := newTestProcess(t, "30")

	if _, running := p.Running(); running {
		t.Fatalf("should not be running before start")
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid, running := p.Running()
	if !running {
		t.Fatalf("should be running after start")
	}
	if pid <= 0 {
		t.Errorf("bad pid %d", pid)
	}

	// Log file receives output (stderr of `sleep 30` is empty; check it exists).
	if _, err := os.Stat(p.LogFile); err != nil {
		t.Errorf("log file missing: %v", err)
	}

	if err := p.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, running := p.Running(); running {
		t.Fatalf("should not be running after stop")
	}
	if _, err := os.Stat(p.PidFile); !os.IsNotExist(err) {
		t.Errorf("pidfile not cleaned up")
	}
}

func TestStopNotRunning(t *testing.T) {
	p := newTestProcess(t, "1")
	if err := p.Stop(time.Second); err == nil {
		t.Errorf("Stop should report not running")
	}
}

func TestDoubleStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process test")
	}
	p := newTestProcess(t, "30")
	defer p.Stop(2 * time.Second)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(); err == nil {
		t.Errorf("double start must fail")
	}
}

func TestRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process test")
	}
	p := newTestProcess(t, "30")
	defer p.Stop(2 * time.Second)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	pid1, _ := p.Running()
	if err := p.Restart(5 * time.Second); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	pid2, running := p.Running()
	if !running {
		t.Fatal("not running after restart")
	}
	if pid1 == pid2 {
		t.Errorf("expected a new pid after restart (%d == %d)", pid1, pid2)
	}
}

func TestProcessOutputCaptured(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process test")
	}
	dir := t.TempDir()
	p := &Process{
		Name:    "echoer",
		Bin:     "sh",
		Args:    []string{"-c", "echo hello-from-process; sleep 5"},
		LogFile: filepath.Join(dir, "out.log"),
		PidFile: filepath.Join(dir, "p.pid"),
	}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop(2 * time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(p.LogFile)
		if strings.Contains(string(data), "hello-from-process") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("log output not captured")
}
