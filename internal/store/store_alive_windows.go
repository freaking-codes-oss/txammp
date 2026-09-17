//go:build windows

package store

import "os"

// processAlive reports whether a PID is alive (Windows implementation:
// OpenProcess via os.FindProcess; a closed handle returns an error).
func processAlive(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
