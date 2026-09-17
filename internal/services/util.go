package services

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// runCommand executes a command with a timeout and returns its first
// output line (stdout or stderr).
func runCommand(bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	return first, nil
}
