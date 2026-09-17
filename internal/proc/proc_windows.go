//go:build windows

package proc

import (
	"os/exec"
	"syscall"
)

// prepareCommand creates a new process group / breakaway so the service
// survives txampp exiting.
func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// terminate sends a graceful CTRL_BREAK to the process group; Windows
// services handle console events or exit on their own.
func terminate(pid int) {
	handle, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer syscall.CloseHandle(handle)
	// 0x40010004 = CTRL_BREAK_EVENT for process groups.
	_ = syscall.GenerateConsoleCtrlEvent(0x40010004, uint32(pid))
}

// kill force-terminates the process tree.
func kill(pid int) {
	handle, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer syscall.CloseHandle(handle)
	_ = syscall.TerminateProcess(handle, 1)
}

// alive checks whether the PID can be opened and is still active.
func alive(pid int) bool {
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return true // opened fine, query failed: assume alive
	}
	return code == 259 // STILL_ACTIVE
}

// matchesBinary is a no-op on Windows (no /proc).
func matchesBinary(pid int, bin string) bool { return true }
