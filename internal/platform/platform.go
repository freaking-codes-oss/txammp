// Package platform maps the running Go target to the platform keys used in
// the component manifest ("linux-amd64", "darwin-arm64", ...).
package platform

import "runtime"

// Key returns the canonical manifest platform key for the current system,
// e.g. "linux-amd64", "darwin-arm64", "windows-amd64".
func Key() string {
	return runtime.GOOS + "-" + ArchKey()
}

// ArchKey normalizes runtime.GOARCH to the names used in the manifest.
func ArchKey() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "386":
		return "386"
	case "arm":
		return "arm"
	default:
		return runtime.GOARCH
	}
}

// IsWindows reports whether the current system is Windows.
func IsWindows() bool { return runtime.GOOS == "windows" }

// IsUnixLike reports whether the current system supports Unix domain sockets
// and POSIX signals (used for the PHP-FPM service).
func IsUnixLike() bool { return !IsWindows() }

// Supported reports whether the given set of manifest keys contains an entry
// for the current platform.
func Supported(keys map[string]bool) bool {
	if keys == nil {
		return false
	}
	// Exact match first.
	if keys[Key()] {
		return true
	}
	// A wildcard entry covers every platform.
	return keys["*"]
}
