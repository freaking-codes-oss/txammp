package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/freaking-codes-oss/txammp/internal/app"
	"github.com/freaking-codes-oss/txammp/internal/logs"
	"github.com/freaking-codes-oss/txammp/internal/manifest"
	"github.com/freaking-codes-oss/txammp/internal/platform"
	"github.com/freaking-codes-oss/txammp/internal/project"
	"github.com/freaking-codes-oss/txammp/internal/stack"
	"github.com/freaking-codes-oss/txammp/internal/store"
	"github.com/freaking-codes-oss/txammp/internal/version"
)

// wizState is the wizard step machine.
type wizState int

const (
	wizWelcome wizState = iota
	wizPHP
	wizDB
	wizExtras
	wizDownload
	wizError
)

type phpOption struct {
	Branch  string // "" for "no PHP"
	Version string
}

type progressBar struct {
	Label  string
	Pct    float64
	Cached bool
	Err    string
}

// wizard is the first-run experience.
type wizard struct {
	cfg        Config
	dir        string
	man        *manifest.Manifest
	manSource  string
	discovered map[string]string

	state wizState
	err   error

	confirmNo bool // welcome: user chose "no"

	phpOptions []phpOption
	phpSel     int
	dbOptions  []string
	dbSel      int
	adminerOn  bool
	mailpitOn  bool

	// provisioning
	bars      []*progressBar
	barsByID  map[string]*progressBar
	progCh    chan stack.ProgressEvent
	progModel progress.Model

	// completion
	sess *app.Session

	width, height int
}

func newWizard(cfg Config, dir string, m *manifest.Manifest, source string, discovered map[string]string) *wizard {
	w := &wizard{
		cfg: cfg, dir: dir, man: m, manSource: source, discovered: discovered,
		progModel: progress.New(progress.WithDefaultGradient()),
		barsByID:  map[string]*progressBar{},
	}
	if m != nil {
		w.buildOptions()
	}
	return w
}

// buildOptions prepares selection lists from the manifest.
func (w *wizard) buildOptions() {
	pkey := platform.Key()
	branches, err := w.man.PHPBranches(pkey, w.discovered)
	if err == nil && len(branches) > 0 {
		for _, br := range branches {
			ver, _ := w.man.PHPVersion(br, w.discovered)
			w.phpOptions = append(w.phpOptions, phpOption{Branch: br, Version: ver})
		}
	}
	// "No PHP" option last.
	w.phpOptions = append(w.phpOptions, phpOption{Branch: "", Version: ""})

	w.dbOptions = []string{"SQLite (zero-config)", "No database"}
	if len(w.phpOptions) > 0 {
		w.phpSel = 0
	}
	w.adminerOn = true
	if len(w.phpOptions) == 1 { // only "none"
		w.adminerOn = false
	}
}

func (w *wizard) init() tea.Cmd { return nil }

func (w *wizard) resize(width, height int) tea.Cmd {
	w.width, w.height = width, height
	return nil
}

// ---- provisioning command ------------------------------------------------------

type wizardProvisionMsg struct {
	sess *app.Session
	err  error
}

// provisionCmd initializes the project, downloads components and starts
// the stack, streaming progress through the channel.
func (w *wizard) provisionCmd() tea.Cmd {
	choices := w.currentChoices()
	man, discovered := w.man, w.discovered
	cfg := w.cfg
	dir := w.dir
	ch := w.progCh
	return func() tea.Msg {
		ctx := context.Background()
		st, err := store.Open()
		if err != nil {
			return wizardProvisionMsg{err: err}
		}
		proj, err := project.Init(dir, choices)
		if err != nil {
			return wizardProvisionMsg{err: err}
		}
		bus := logs.NewBus(4000)
		sk := stack.Open(proj, st, man, bus)
		sk.SetDiscovered(discovered)
		if err := sk.Ensure(ctx, func(ev stack.ProgressEvent) {
			select {
			case ch <- ev:
			default:
			}
		}); err != nil {
			return wizardProvisionMsg{err: err}
		}
		if err := sk.Start(ctx); err != nil {
			return wizardProvisionMsg{err: fmt.Errorf("services failed to start: %w", err)}
		}
		sk.AttachLogs(ctx, 80)
		_ = cfg
		return wizardProvisionMsg{sess: &app.Session{Stack: sk, Bus: bus}}
	}
}

// waitForProgress re-arms the progress channel reader.
func (w *wizard) waitForProgress() tea.Cmd {
	ch := w.progCh
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return wizardProgressMsg(ev)
	}
}

type wizardProgressMsg stack.ProgressEvent

func (w *wizard) currentChoices() project.State {
	php := w.phpOptions[w.phpSel]
	db := "sqlite"
	if w.dbSel == 1 {
		db = "none"
	}
	if php.Branch == "" {
		db = "none"
	}
	return project.State{
		PHPBranch: php.Branch,
		Database:  db,
		Adminer:   w.adminerOn && php.Branch != "",
		Mailpit:   w.mailpitOn,
	}
}

// ---- update ---------------------------------------------------------------------

func (w *wizard) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		return w.handleKey(m)
	case wizardProgressMsg:
		w.applyProgress(stack.ProgressEvent(m))
		return w.waitForProgress()
	case wizardProvisionMsg:
		if m.err != nil {
			w.state = wizError
			w.err = m.err
			return nil
		}
		w.sess = m.sess
		return nil
	case progress.FrameMsg:
		pm, cmd := w.progModel.Update(msg)
		w.progModel = pm.(progress.Model)
		return cmd
	}
	return nil
}

func (w *wizard) handleKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc":
		if w.state != wizWelcome && w.state != wizDownload {
			w.state = wizWelcome
			return nil
		}
	}

	switch w.state {
	case wizWelcome:
		switch k.String() {
		case "y", "Y", "enter":
			w.next()
		case "n", "N":
			return tea.Quit
		}
	case wizPHP:
		switch k.String() {
		case "up", "k":
			if w.phpSel > 0 {
				w.phpSel--
			}
		case "down", "j":
			if w.phpSel < len(w.phpOptions)-1 {
				w.phpSel++
			}
		case "enter":
			w.next()
		}
	case wizDB:
		switch k.String() {
		case "up", "k":
			if w.dbSel > 0 {
				w.dbSel--
			}
		case "down", "j":
			if w.dbSel < len(w.dbOptions)-1 {
				w.dbSel++
			}
		case "enter":
			w.next()
		}
	case wizExtras:
		switch k.String() {
		case "up", "k":
			w.adminerOn = !w.adminerOn
		case "down", "j":
			w.mailpitOn = !w.mailpitOn
		case "a":
			w.adminerOn = !w.adminerOn
		case "m":
			w.mailpitOn = !w.mailpitOn
		case "tab":
			if w.adminerOn || w.mailpitOn {
				w.adminerOn, w.mailpitOn = w.mailpitOn, w.adminerOn
			}
		case "enter":
			w.startProvisioning()
			return tea.Batch(w.provisionCmd(), w.waitForProgress())
		}
	case wizError:
		switch k.String() {
		case "r", "R":
			// retry: re-run provisioning with the same choices.
			w.startProvisioning()
			return tea.Batch(w.provisionCmd(), w.waitForProgress())
		case "q", "Q", "esc":
			return tea.Quit
		}
	}
	return nil
}

func (w *wizard) next() {
	switch w.state {
	case wizWelcome:
		if len(w.phpOptions) <= 1 {
			w.phpSel = len(w.phpOptions) - 1 // "none"
			w.state = wizDB
			return
		}
		w.state = wizPHP
	case wizPHP:
		w.state = wizDB
	case wizDB:
		w.state = wizExtras
	}
}

func (w *wizard) startProvisioning() {
	w.state = wizDownload
	w.bars = nil
	w.barsByID = map[string]*progressBar{}
	w.progCh = make(chan stack.ProgressEvent, 512)
}

func (w *wizard) applyProgress(ev stack.ProgressEvent) {
	id := ev.Component + "/" + ev.FileID
	bar, ok := w.barsByID[id]
	if !ok {
		bar = &progressBar{Label: fmt.Sprintf("%s %s — %s", ev.Component, ev.Version, ev.FileID)}
		w.barsByID[id] = bar
		w.bars = append(w.bars, bar)
	}
	switch ev.Phase {
	case stack.PhaseSkip:
		bar.Cached = true
		bar.Pct = 1
	case stack.PhaseStart:
		bar.Pct = 0
	case stack.PhaseProgress:
		if ev.Total > 0 {
			bar.Pct = float64(ev.Written) / float64(ev.Total)
		} else {
			bar.Pct = 0.05 // indeterminate
		}
	case stack.PhaseDone:
		bar.Pct = 1
	case stack.PhaseError:
		bar.Err = fmt.Sprint(ev.Err)
	}
}

// ---- view ------------------------------------------------------------------------

func (w *wizard) view() string {
	var body string
	switch w.state {
	case wizWelcome:
		body = w.viewWelcome()
	case wizPHP:
		body = w.viewPHP()
	case wizDB:
		body = w.viewDB()
	case wizExtras:
		body = w.viewExtras()
	case wizDownload:
		body = w.viewDownload()
	case wizError:
		body = w.viewError()
	}
	header := titleStyle.Render("TXAMPP") + dimStyle.Render("  first-run setup — v"+version.Short())
	footer := dimStyle.Render("  ↑/↓ select · enter confirm · esc back · ctrl+c quit")
	return lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", footer)
}

func (w *wizard) viewWelcome() string {
	lines := []string{
		"  No environment detected in:",
		"",
		"  " + headerStyle.Render(w.dir),
		"",
	}
	if w.man != nil {
		lines = append(lines,
			"  txampp will create "+shortcutStyle.Render(".stack/")+" here and install:",
			"",
			"    • Caddy — web server with automatic PHP routing",
		)
		if len(w.phpOptions) > 1 {
			lines = append(lines, "    • Static PHP (CLI + FPM) from static-php.dev")
		}
		lines = append(lines,
			"    • project-local logs, sockets and data — nothing outside this folder",
			"",
		)
	} else {
		lines = append(lines, "  (manifest unavailable)", "")
	}
	lines = append(lines, "  Initialize the environment? "+shortcutStyle.Render("[Y]es")+" / "+dimStyle.Render("[N]o"))
	return strings.Join(lines, "\n")
}

func (w *wizard) viewPHP() string {
	var b strings.Builder
	b.WriteString("  Select a PHP version:\n\n")
	for i, opt := range w.phpOptions {
		cursor, style := "  ", dimStyle
		if i == w.phpSel {
			cursor, style = shortcutStyle.Render(" >"), selectedRowStyle
		}
		if opt.Branch == "" {
			b.WriteString(fmt.Sprintf("%s %s\n", cursor, style.Render("No PHP — static files only")))
		} else {
			b.WriteString(fmt.Sprintf("%s %s\n", cursor, style.Render(fmt.Sprintf("PHP %s   (%s)", opt.Branch, opt.Version))))
		}
	}
	if w.manSource != "" {
		b.WriteString("\n" + dimStyle.Render("  source: "+w.manSource))
	}
	return b.String()
}

func (w *wizard) viewDB() string {
	var b strings.Builder
	b.WriteString("  Select a database:\n\n")
	for i, opt := range w.dbOptions {
		cursor, style := "  ", dimStyle
		if i == w.dbSel {
			cursor, style = shortcutStyle.Render(" >"), selectedRowStyle
		}
		b.WriteString(fmt.Sprintf("%s %s\n", cursor, style.Render(opt)))
	}
	b.WriteString("\n" + dimStyle.Render("  SQLite is served through PHP's pdo_sqlite — no database process."))
	return b.String()
}

func (w *wizard) viewExtras() string {
	adminer := "[ ]"
	if w.adminerOn {
		adminer = runningStyle.Render("[x]")
	}
	mailpit := "[ ]"
	if w.mailpitOn {
		mailpit = runningStyle.Render("[x]")
	}
	lines := []string{
		"  Extras:",
		"",
		"  " + adminer + " Adminer — single-file database GUI at /__adminer",
		"  " + mailpit + " Mailpit — local SMTP catcher with a web inbox",
		"",
		"  " + dimStyle.Render("A toggles Adminer · M toggles Mailpit"),
		"  " + shortcutStyle.Render("[enter]") + " download & start",
	}
	return strings.Join(lines, "\n")
}

func (w *wizard) viewDownload() string {
	var b strings.Builder
	b.WriteString("  Provisioning the stack…\n\n")
	for _, bar := range w.bars {
		if bar.Cached {
			b.WriteString(fmt.Sprintf("  %s %s %s\n", runningStyle.Render("✓"), padTo(bar.Label, 40), dimStyle.Render("cached")))
			continue
		}
		if bar.Err != "" {
			b.WriteString(fmt.Sprintf("  %s %s %s\n", failedStyle.Render("✗"), padTo(bar.Label, 40), errorStyle.Render(bar.Err)))
			continue
		}
		if bar.Pct >= 1 {
			b.WriteString(fmt.Sprintf("  %s %s\n", runningStyle.Render("✓"), padTo(bar.Label, 40)))
			continue
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", "↓", w.progModel.ViewAs(bar.Pct)))
		_ = bar
	}
	if len(w.bars) == 0 {
		b.WriteString("  " + dimStyle.Render("resolving components…"))
	}
	b.WriteString("\n  " + dimStyle.Render("binaries are cached globally in ~/.cache/txampp and shared across projects"))
	return b.String()
}

func (w *wizard) viewError() string {
	return strings.Join([]string{
		errorStyle.Render("  Provisioning failed"),
		"",
		"  " + wrapText(w.err.Error(), 72),
		"",
		"  " + shortcutStyle.Render("[R]etry") + "  " + dimStyle.Render("[Q]uit"),
	}, "\n")
}

func wrapText(s string, width int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}
	var lines []string
	cur := words[0]
	for _, wd := range words[1:] {
		if len(cur)+1+len(wd) > width {
			lines = append(lines, cur)
			cur = wd
			continue
		}
		cur += " " + wd
	}
	lines = append(lines, cur)
	return strings.Join(lines, "\n  ")
}

func padTo(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

// sortedKeys is a tiny helper for deterministic map iteration.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
