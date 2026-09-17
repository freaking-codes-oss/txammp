package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/freaking-codes-oss/txammp/internal/services"
)

var (
	styleOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleDim  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleBold = lipgloss.NewStyle().Bold(true)
	styleHead = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleURL  = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Underline(true)
)

// statusStyles map service statuses to colors.
var statusStyles = map[services.Status]lipgloss.Style{
	services.StatusRunning:  styleOK,
	services.StatusStarting: styleWarn,
	services.StatusStopped:  styleDim,
	services.StatusFailed:   styleErr,
	services.StatusReady:    styleOK,
	services.StatusMissing:  styleWarn,
}

// enableColor activates or disables colored output globally.
func enableColor(on bool) {
	if on {
		return
	}
	styleOK = lipgloss.NewStyle()
	styleWarn = lipgloss.NewStyle()
	styleErr = lipgloss.NewStyle()
	styleDim = lipgloss.NewStyle()
	styleBold = lipgloss.NewStyle()
	styleHead = lipgloss.NewStyle()
	styleURL = lipgloss.NewStyle()
	statusStyles = map[services.Status]lipgloss.Style{}
	for k := range logSourceStyles {
		delete(logSourceStyles, k)
	}
}

func printf(format string, args ...any)  { fmt.Fprintf(os.Stdout, format, args...) }
func eprintf(format string, args ...any) { fmt.Fprintf(os.Stderr, format, args...) }

func okf(format string, args ...any) {
	printf("%s %s\n", styleOK.Render(" ✓ "), fmt.Sprintf(format, args...))
}
func warnf(format string, args ...any) {
	printf("%s %s\n", styleWarn.Render(" ! "), fmt.Sprintf(format, args...))
}
func errf(format string, args ...any) {
	eprintf("%s %s\n", styleErr.Render(" ✗ "), fmt.Sprintf(format, args...))
}

// isTTY reports whether f is a terminal.
func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func isInteractive() bool { return isTTY(os.Stdin) && isTTY(os.Stdout) }

// confirm asks a yes/no question. def is the answer for an empty line.
func confirm(prompt string, def bool) bool {
	suffix := "Y/n"
	if !def {
		suffix = "y/N"
	}
	fmt.Printf("%s [%s] ", prompt, suffix)
	var answer string
	if _, err := fmt.Scanln(&answer); err != nil || answer == "" {
		return def
	}
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}
