// Package browser opens URLs in the user's default browser.
package browser

import (
	"os/exec"
	"runtime"
)

// Open opens the given URL with the platform default browser. It returns
// an error when no opener could be found; callers usually treat this as
// non-fatal and print the URL instead.
func Open(url string) error {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"open", url}}
	case "windows":
		candidates = [][]string{
			{"rundll32", "url.dll,FileProtocolHandler", url},
			{"cmd", "/c", "start", "", url},
		}
	default: // Linux, BSDs
		candidates = [][]string{
			{"xdg-open", url},
			{"wslview", url}, // WSL
			{"x-www-browser", url},
		}
	}
	var lastErr error
	for _, c := range candidates {
		cmd := exec.Command(c[0], c[1:]...)
		if err := cmd.Start(); err != nil {
			lastErr = err
			continue
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	return lastErr
}
