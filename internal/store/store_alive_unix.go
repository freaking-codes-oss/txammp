//go:build !windows

package store

import (
	"os"
	"syscall"
)

// processAlive reports whether a PID is alive (Unix implementation:
// signal 0 probe).
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
