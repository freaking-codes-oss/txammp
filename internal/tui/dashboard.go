package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/freaking-codes-oss/txammp/internal/app"
	"github.com/freaking-codes-oss/txammp/internal/browser"
	"github.com/freaking-codes-oss/txammp/internal/logs"
	"github.com/freaking-codes-oss/txammp/internal/services"
	"github.com/freaking-codes-oss/txammp/internal/stack"
	"github.com/freaking-codes-oss/txammp/internal/version"
)

// focusPane identifies the focused dashboard panel.
type focusPane int

const (
	focusServices focusPane = iota
	focusLogs
)

// svcRow is a cached status row.
type svcRow struct {
	svc    services.Service
	status services.Status
}

// dashboard is the main screen.
type dashboard struct {
	sess       *app.Session
	width      int
	height     int
	focus      focusPane
	sel        int
	rows       []svcRow
	logs       viewport.Model
	logLines   []logs.Line
	logCh      chan logs.Line
	logChOpen  bool
	filterIdx  int // 0 = all; then Sources()
	help       bool
	configSel  int
	configOpen bool
	quitPrompt bool
	actionNote string
	wantQuit   bool
	busy       bool
}

func newDashboard(sess *app.Session, width, height int) *dashboard {
	logsHeight := 10
	if height > 30 {
		logsHeight = height - 20
	}
	if logsHeight < 6 {
		logsHeight = 6
	}
	d := &dashboard{
		sess:   sess,
		width:  width,
		height: height,
		logs:   viewport.New(width-2, logsHeight),
	}
	return d
}

func (d *dashboard) setSession(sess *app.Session) {
	if d.sess != nil {
		d.sess.Stack.DetachLogs()
	}
	d.sess = sess
}

func (d *dashboard) init() tea.Cmd {
	ctx := context.Background()
	d.sess.Stack.AttachLogs(ctx, 200)
	d.refreshRows(ctx)
	d.logCh = d.sess.Bus.Subscribe(1024)
	d.logChOpen = true
	// Preload recent history into the viewport.
	d.logLines = d.sess.Bus.Recent(500)
	d.renderLogs()
	d.logs.GotoBottom()
	return tea.Batch(waitForLogs(d.logCh), tea.Tick(spinnerFPS, func(t time.Time) tea.Msg { return tickMsg{} }))
}

func (d *dashboard) resize(width, height int) tea.Cmd {
	d.width, d.height = width, height
	logsHeight := d.height - 20
	if logsHeight < 6 {
		logsHeight = 6
	}
	d.logs.Width = width - 4
	d.logs.Height = logsHeight
	d.renderLogs()
	return nil
}

func (d *dashboard) tick() tea.Cmd {
	d.refreshRows(context.Background())
	return tea.Tick(spinnerFPS, func(t time.Time) tea.Msg { return tickMsg{} })
}

func (d *dashboard) refreshRows(ctx context.Context) {
	d.rows = nil
	for _, svc := range d.sess.Stack.Services() {
		d.rows = append(d.rows, svcRow{svc: svc, status: svc.Check(ctx)})
	}
	if d.sel >= len(d.rows) {
		d.sel = len(d.rows) - 1
	}
	if d.sel < 0 {
		d.sel = 0
	}
}

// ---- actions --------------------------------------------------------------------

// actionDoneMsg signals a background action finished.
type actionDoneMsg struct {
	note string
	err  error
}

func (d *dashboard) runAction(note string, fn func() error) tea.Cmd {
	d.busy = true
	d.actionNote = note
	return func() tea.Msg {
		err := fn()
		return actionDoneMsg{note: note, err: err}
	}
}

func (d *dashboard) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case logLineMsg:
		d.logLines = append(d.logLines, m.line)
		if len(d.logLines) > 2000 {
			d.logLines = d.logLines[len(d.logLines)-1000:]
		}
		d.renderLogs()
		d.logs.GotoBottom()
		return waitForLogs(d.logCh)

	case actionDoneMsg:
		d.busy = false
		if m.err != nil {
			d.actionNote = "✗ " + m.err.Error()
		} else {
			d.actionNote = m.note
		}
		d.refreshRows(context.Background())
		return nil

	case tea.KeyMsg:
		return d.handleKey(m)
	}
	return nil
}

func (d *dashboard) handleKey(k tea.KeyMsg) tea.Cmd {
	// Overlays first.
	if d.quitPrompt {
		switch k.String() {
		case "q", "Q":
			return d.runAction("stopping stack…", func() error {
				err := d.sess.Stack.Stop(context.Background())
				d.wantQuit = true
				return err
			})
		case "x", "X":
			d.wantQuit = true
			return tea.Quit
		default:
			d.quitPrompt = false
			return nil
		}
	}
	if d.help {
		if k.String() == "?" || k.String() == "esc" {
			d.help = false
			return nil
		}
		return nil
	}
	if d.configOpen {
		return d.handleConfigKeys(k)
	}

	switch k.String() {
	case "ctrl+c":
		d.wantQuit = true
		return tea.Quit
	case "q":
		d.quitPrompt = true
		return nil
	case "?":
		d.help = true
		return nil
	case "tab":
		if d.focus == focusServices {
			d.focus = focusLogs
		} else {
			d.focus = focusServices
		}
		return nil
	case " ":
		// toggle focus
		if d.focus == focusServices {
			d.focus = focusLogs
		} else {
			d.focus = focusServices
		}
		return nil
	}

	if d.focus == focusLogs {
		switch k.String() {
		case "up", "k":
			d.logs.LineUp(1)
		case "down", "j":
			d.logs.LineDown(1)
		case "pgup", "b":
			d.logs.HalfPageUp()
		case "pgdown", "f":
			d.logs.HalfPageDown()
		case "home", "g":
			d.logs.GotoTop()
		case "end", "G":
			d.logs.GotoBottom()
		case "s":
			// 's' inside logs = filter cycle shortcut too
			d.cycleFilter()
		default:
			// forward to viewport keymap (arrows etc.)
			var cmd tea.Cmd
			d.logs, cmd = d.logs.Update(k)
			return cmd
		}
		return nil
	}

	// services focus
	switch k.String() {
	case "up", "k":
		if d.sel > 0 {
			d.sel--
		}
	case "down", "j":
		if d.sel < len(d.rows)-1 {
			d.sel++
		}
	case "r":
		if svc := d.selected(); svc != nil {
			name := svc.Label()
			return d.runAction(fmt.Sprintf("restarting %s…", name), func() error {
				return d.sess.Stack.Restart(context.Background(), svc.ID())
			})
		}
	case "s":
		if svc := d.selected(); svc != nil {
			name := svc.Label()
			return d.runAction(fmt.Sprintf("stopping %s…", name), func() error {
				return svc.Stop(context.Background())
			})
		}
	case "g", "enter":
		if svc := d.selected(); svc != nil {
			name := svc.Label()
			return d.runAction(fmt.Sprintf("starting %s…", name), func() error {
				return svc.Start(context.Background())
			})
		}
	case "o":
		d.openBrowser(d.sess.Stack.WebURL())
	case "a":
		d.openBrowser(d.sess.Stack.AdminerURL())
	case "m":
		d.openBrowser(d.sess.Stack.MailpitURL())
	case "c":
		d.configOpen = true
		d.configSel = 0
	case "l":
		d.focus = focusLogs
	case "f":
		d.cycleFilter()
	}
	return nil
}

func (d *dashboard) handleConfigKeys(k tea.KeyMsg) tea.Cmd {
	entries := d.configEntries()
	switch k.String() {
	case "esc":
		d.configOpen = false
	case "up", "k":
		if d.configSel > 0 {
			d.configSel--
		}
	case "down", "j":
		if d.configSel < len(entries)-1 {
			d.configSel++
		}
	case "enter":
		if d.configSel >= len(entries) {
			return nil
		}
		path := entries[d.configSel].path
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = os.Getenv("VISUAL")
		}
		if editor == "" {
			editor = "vi"
		}
		d.configOpen = false
		cmd := exec.Command(editor, path)
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				return actionDoneMsg{note: "editor: " + err.Error(), err: err}
			}
			return actionDoneMsg{note: "config saved: " + path}
		})
	}
	return nil
}

type configEntry struct {
	name, path string
}

func (d *dashboard) configEntries() []configEntry {
	p := d.sess.Stack.Proj
	return []configEntry{
		{"Caddyfile", p.ConfigPath("Caddyfile")},
		{"php.ini", p.ConfigPath("php.ini")},
		{"php-fpm.conf", p.ConfigPath("php-fpm.conf")},
		{"state.json", p.StatePath()},
	}
}

func (d *dashboard) cycleFilter() {
	sources := d.sess.Bus.Sources()
	// cycle: all -> each source -> all
	d.filterIdx++
	if d.filterIdx > len(sources) {
		d.filterIdx = 0
	}
	d.renderLogs()
	d.logs.GotoBottom()
}

func (d *dashboard) currentFilter() string {
	if d.filterIdx == 0 {
		return ""
	}
	sources := d.sess.Bus.Sources()
	if d.filterIdx-1 < len(sources) {
		return sources[d.filterIdx-1]
	}
	return ""
}

func (d *dashboard) openBrowser(url string) {
	if err := browser.Open(url); err != nil {
		d.actionNote = "browser: " + err.Error() + " — " + url
		return
	}
	d.actionNote = "opened " + url
}

func (d *dashboard) selected() services.Service {
	if d.sel < 0 || d.sel >= len(d.rows) {
		return nil
	}
	return d.rows[d.sel].svc
}

func (d *dashboard) renderLogs() {
	filter := d.currentFilter()
	var b strings.Builder
	for _, l := range d.logLines {
		if filter != "" && l.Source != filter {
			continue
		}
		tag := logSourceStyle(l.Source).Render(fmt.Sprintf("[%-7s]", l.Source))
		fmt.Fprintf(&b, "%s %s\n", tag, l.Text)
	}
	d.logs.SetContent(b.String())
}

// ---- view -------------------------------------------------------------------------

func (d *dashboard) view() string {
	if d.width == 0 {
		return "loading…"
	}
	sk := d.sess.Stack
	header := fmt.Sprintf("%s %s   %s   %s",
		titleStyle.Render("TXAMPP"),
		dimStyle.Render("v"+version.Short()),
		headerStyle.Render(sk.Proj.Root),
		dimStyle.Render(webBadge(sk)))

	servicesPane := d.viewServices()
	logsPane := d.viewLogs()

	footer := d.viewFooter()

	body := lipgloss.JoinVertical(lipgloss.Left,
		header,
		servicesPane,
		"",
		logsPane,
		footer,
	)

	if d.help {
		body = overlayCenter(d.width, d.height, body, d.viewHelp())
	}
	if d.configOpen {
		body = overlayCenter(d.width, d.height, body, d.viewConfig())
	}
	if d.quitPrompt {
		body = overlayCenter(d.width, d.height, body, d.viewQuitPrompt())
	}
	return body
}

func webBadge(sk *stack.Stack) string {
	return fmt.Sprintf("http://127.0.0.1:%d", sk.Proj.State.Ports["web"])
}

func (d *dashboard) viewServices() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("  SERVICES") + "\n")
	fmt.Fprintf(&b, "  %-24s %-10s %-12s %s\n",
		dimStyle.Render("service"), dimStyle.Render("status"), dimStyle.Render("version"), dimStyle.Render("endpoint"))
	for i, row := range d.rows {
		sel := i == d.sel && d.focus == focusServices
		name := row.svc.Label()
		if sel {
			name = selectedRowStyle.Render("› " + name)
		} else {
			name = "  " + name
		}
		fmt.Fprintf(&b, "  %-24s %-10s %-12s %s\n",
			name,
			statusStyle(row.status),
			dimStyle.Render(truncateStr(row.svc.Version(), 12)),
			dimStyle.Render(row.svc.Endpoint()))
	}
	return b.String()
}

func statusStyle(st services.Status) string {
	switch st {
	case services.StatusRunning:
		return runningStyle.Render("RUNNING")
	case services.StatusStarting:
		return startingStyle.Render("STARTING")
	case services.StatusFailed:
		return failedStyle.Render("FAILED")
	case services.StatusReady:
		return readyStyle.Render("READY")
	default:
		return stoppedStyle.Render("STOPPED")
	}
}

func (d *dashboard) viewLogs() string {
	title := "  LIVE LOGS"
	if f := d.currentFilter(); f != "" {
		title += dimStyle.Render("  (filter: " + f + ")")
	}
	focusTag := ""
	if d.focus == focusLogs {
		focusTag = " " + shortcutStyle.Render("[focused]")
	}
	head := headerStyle.Render(title) + focusTag
	return lipgloss.JoinVertical(lipgloss.Left, head, "  "+d.logs.View())
}

func (d *dashboard) viewFooter() string {
	left := []string{
		shortcutStyle.Render("[O]") + " site",
		shortcutStyle.Render("[A]") + " adminer",
		shortcutStyle.Render("[M]") + " mailpit",
		shortcutStyle.Render("[C]") + " configs",
		shortcutStyle.Render("[F]") + " filter",
		shortcutStyle.Render("[?]") + " help",
		shortcutStyle.Render("[Q]") + " quit",
	}
	line1 := "  " + strings.Join(left, "  ")
	line2 := "  " + dimStyle.Render("[↑/↓] select · [R]estart · [S]top · [G] start · [Tab]/[Space] focus · [ctrl+c] leave running")
	if d.actionNote != "" {
		note := truncateStr(d.actionNote, d.width-6)
		if d.busy {
			note = startingStyle.Render(note)
		}
		line2 = "  " + note + "\n" + line2
	}
	return line1 + "\n" + line2
}

func (d *dashboard) viewHelp() string {
	lines := []string{
		titleStyle.Render("  TXAMPP — keys"),
		"",
		"  " + shortcutStyle.Render("↑/↓ / j,k") + "   move selection",
		"  " + shortcutStyle.Render("R") + "          restart focused service",
		"  " + shortcutStyle.Render("S") + "          stop focused service",
		"  " + shortcutStyle.Render("G / enter") + "  start focused service",
		"  " + shortcutStyle.Render("Tab / space") + " switch between services and logs",
		"  " + shortcutStyle.Render("O") + "          open the site in your browser",
		"  " + shortcutStyle.Render("A") + "          open Adminer",
		"  " + shortcutStyle.Render("M") + "          open Mailpit",
		"  " + shortcutStyle.Render("C") + "          edit configuration files",
		"  " + shortcutStyle.Render("F") + "          cycle the log filter",
		"  " + shortcutStyle.Render("Q") + "          stop the stack and quit",
		"  " + shortcutStyle.Render("ctrl+c") + "     quit, leaving services running",
		"",
		"  " + dimStyle.Render("binaries: ~/.cache/txampp · project state: .stack/"),
		"  " + dimStyle.Render("press any key to close"),
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("39")).
		Padding(1, 2)
	return box.Render(strings.Join(lines, "\n"))
}

func (d *dashboard) viewConfig() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("  CONFIG FILES") + dimStyle.Render("  — enter opens $EDITOR") + "\n\n")
	for i, e := range d.configEntries() {
		cursor, style := "  ", dimStyle
		if i == d.configSel {
			cursor, style = shortcutStyle.Render(" >"), selectedRowStyle
		}
		fmt.Fprintf(&b, "%s %s\n", cursor, style.Render(fmt.Sprintf("%-14s %s", e.name, e.path)))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("39")).
		Padding(1, 2)
	return box.Render(b.String())
}

func (d *dashboard) viewQuitPrompt() string {
	lines := []string{
		titleStyle.Render("  Quit txampp?"),
		"",
		"  " + shortcutStyle.Render("[Q]") + " stop all services and quit",
		"  " + shortcutStyle.Render("[X]") + " quit, keep services running",
		"  " + dimStyle.Render("any other key cancels"),
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("214")).
		Padding(1, 2)
	return box.Render(strings.Join(lines, "\n"))
}

// overlayCenter draws fg centered over bg.
func overlayCenter(w, h int, bg, fg string) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")
	fgW := 0
	for _, l := range fgLines {
		if len(l) > fgW {
			fgW = len(l)
		}
	}
	// naive overlay: place fg in the middle of bg's line count
	midY := (len(bgLines) - len(fgLines)) / 2
	if midY < 0 {
		midY = 0
	}
	var out []string
	for i, line := range bgLines {
		if i >= midY && i < midY+len(fgLines) {
			fgLine := fgLines[i-midY]
			pad := (w - fgW) / 2
			if pad < 0 {
				pad = 0
			}
			out = append(out, strings.Repeat(" ", pad)+fgLine)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
