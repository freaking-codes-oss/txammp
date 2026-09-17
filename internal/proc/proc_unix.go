//go:build !windows

package proc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// prepareCommand puts the child into its own process group so signals can
// be delivered to the whole tree, and keeps it running when txampp exits.
func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate sends SIGTERM to the process group.
func terminate(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = syscall.Kill(pid, syscall.SIGTERM)
}

// kill sends SIGKILL to the process group.
func kill(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// alive probes a PID with signal 0.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// matchesBinary guards against PID recycling by comparing /proc/<pid>/exe
// or cmdline with the expected binary. When the check cannot be performed
// (non-Linux) it optimistically passes.
func matchesBinary(pid int, bin string) bool {
	if bin == "" {
		return true
	}
	// Best effort: compare the resolved executable link.
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return true
	}
	if exe == bin || strings.HasPrefix(exe, bin+" (deleted)") {
		return true
	}
	// Some systems report the interpreter; fall back to cmdline scan.
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return true
	}
	first := string(bytes.SplitN(data, []byte{0}, 2)[0])
	return first == bin || strings.HasSuffix(first, "/"+strings.TrimSuffix(bin, "/"))
}
