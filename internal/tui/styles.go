// Package tui implements txampp's interactive frontend: the first-run
// wizard and the services dashboard.
package tui

import "github.com/charmbracelet/lipgloss"

// Shared styles for the TUI.
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39")).
			Padding(0, 1)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Bold(true)

	normalBorder = lipgloss.RoundedBorder()

	paneBase = lipgloss.NewStyle().
			BorderStyle(normalBorder).
			BorderForeground(lipgloss.Color("240"))

	focusedPane = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("39")).
			BorderTop(true)

	selectedRowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Bold(true)

	runningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	startingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	failedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	stoppedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	readyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))

	urlStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("51")).Underline(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	shortcutStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)

	logSourceColors = map[string]string{
		"caddy":   "51",
		"php":     "213",
		"mailpit": "42",
		"txampp":  "245",
	}
)

// logSourceStyle returns a style for a log source tag.
func logSourceStyle(source string) lipgloss.Style {
	if c, ok := logSourceColors[source]; ok {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
	}
	return dimStyle
}
