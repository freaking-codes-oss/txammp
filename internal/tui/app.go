package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/freaking-codes-oss/txammp/internal/app"
	"github.com/freaking-codes-oss/txammp/internal/logs"
	"github.com/freaking-codes-oss/txammp/internal/manifest"
	"github.com/freaking-codes-oss/txammp/internal/platform"
	"github.com/freaking-codes-oss/txammp/internal/store"
)

// Config configures the TUI (mirrors global CLI flags).
type Config struct {
	Dir          string
	ManifestPath string
	MirrorURL    string
}

// Run starts the interactive frontend.
func Run(cfg Config) error {
	m := newApp(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// ---- messages ------------------------------------------------------------------

type msgManifestLoaded struct {
	manifest   *manifest.Manifest
	source     string
	discovered map[string]string
	err        error
}

type msgSessionReady struct {
	sess *app.Session
	err  error
}

// appScreen is the active top-level screen.
type appScreen int

const (
	screenLoading appScreen = iota
	screenWizard
	screenDashboard
)

// rootModel is the root model: it routes between the wizard and the dashboard.
type rootModel struct {
	cfg    Config
	width  int
	height int
	screen appScreen

	// loading state
	loadErr  error
	loadNote string

	// active session (dashboard)
	sess *app.Session

	wizard  *wizard
	dash    *dashboard
	quitted bool
}

func newApp(cfg Config) *rootModel {
	return &rootModel{cfg: cfg, screen: screenLoading}
}

func (a *rootModel) Init() tea.Cmd {
	return loadManifestCmd(a.cfg)
}

func (a *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		var cmds []tea.Cmd
		if a.wizard != nil {
			cmds = append(cmds, a.wizard.resize(m.Width, m.Height))
		}
		if a.dash != nil {
			cmds = append(cmds, a.dash.resize(m.Width, m.Height))
		}
		return a, tea.Batch(cmds...)

	case msgManifestLoaded:
		if m.err != nil {
			a.loadErr = m.err
			return a, nil
		}
		initialized := app.Detect(a.projectDir())
		if initialized {
			// Straight to the dashboard.
			a.screen = screenDashboard
			cmd := openSessionCmd(a.cfg)
			return a, tea.Batch(cmd, tea.Tick(spinnerFPS, func(t time.Time) tea.Msg { return tickMsg{} }))
		}
		a.screen = screenWizard
		a.wizard = newWizard(a.cfg, a.projectDir(), m.manifest, m.source, m.discovered)
		return a, a.wizard.init()

	case msgSessionReady:
		if m.err != nil {
			a.loadErr = m.err
			a.screen = screenWizard // show error with retry
			if a.wizard == nil {
				a.wizard = newWizard(a.cfg, a.projectDir(), nil, "", nil)
				a.wizard.state = wizError
				a.wizard.err = m.err
				return a, nil
			}
			a.wizard.state = wizError
			a.wizard.err = m.err
			return a, nil
		}
		a.sess = m.sess
		if a.dash == nil {
			a.dash = newDashboard(m.sess, a.width, a.height)
		} else {
			a.dash.setSession(m.sess)
		}
		a.screen = screenDashboard
		return a, a.dash.init()

	case tickMsg:
		// global refresh tick: statuses when the dashboard is active
		if a.screen == screenDashboard && a.dash != nil {
			return a, a.dash.tick()
		}
		return a, tea.Tick(spinnerFPS, func(t time.Time) tea.Msg { return tickMsg{} })

	case msgQuit:
		a.quitted = true
		return a, tea.Quit
	}

	// Delegate to the active screen.
	switch a.screen {
	case screenWizard:
		if a.wizard != nil {
			cmd := a.wizard.update(msg)
			if a.wizard.sess != nil {
				// Provisioning succeeded: hand off to the dashboard.
				a.sess = a.wizard.sess
				a.dash = newDashboard(a.sess, a.width, a.height)
				a.screen = screenDashboard
				return a, tea.Batch(cmd, a.dash.init())
			}
			return a, cmd
		}
	case screenDashboard:
		if a.dash != nil {
			cmd := a.dash.update(msg)
			if a.dash.wantQuit {
				return a, tea.Quit
			}
			return a, cmd
		}
	}
	return a, nil
}

func (a *rootModel) View() string {
	switch a.screen {
	case screenLoading:
		return a.viewLoading()
	case screenWizard:
		if a.wizard != nil {
			return a.wizard.view()
		}
		return a.viewLoading()
	case screenDashboard:
		if a.dash != nil {
			return a.dash.view()
		}
	}
	return a.viewLoading()
}

func (a *rootModel) viewLoading() string {
	if a.loadErr != nil {
		return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center,
			errorStyle.Render(fmt.Sprintf("cannot load manifest: %v", a.loadErr))+
				"\n\n"+helpStyle.Render("check --mirror / network, then restart txampp"))
	}
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center,
		titleStyle.Render("TXAMPP")+" "+dimStyle.Render("loading manifest…"))
}

func (a *rootModel) projectDir() string {
	dir := a.cfg.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return dir
}

// ---- shared commands and messages -----------------------------------------------

// tickMsg drives periodic refreshes.
type tickMsg struct{}

// spinnerFPS is the global refresh cadence.
const spinnerFPS = 500 * time.Millisecond

// manifestDiscoverTimeout bounds live PHP version discovery.
const manifestDiscoverTimeout = 8 * time.Second

type msgQuit struct{}

func loadManifestCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		o := app.Options{Dir: cfg.Dir, ManifestPath: cfg.ManifestPath, MirrorURL: cfg.MirrorURL}
		o.FromEnv()
		st, err := store.Open()
		if err != nil {
			return msgManifestLoaded{err: err}
		}
		m, source, err := app.LoadManifest(context.Background(), o, st)
		if err != nil {
			return msgManifestLoaded{err: err}
		}
		var discovered map[string]string
		dctx, cancel := context.WithTimeout(context.Background(), manifestDiscoverTimeout)
		defer cancel()
		discovered, _ = m.DiscoverPHP(dctx, nil, platform.Key())
		return msgManifestLoaded{manifest: m, source: source, discovered: discovered}
	}
}

func openSessionCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		sess, err := app.Open(context.Background(), app.Options{
			Dir: cfg.Dir, ManifestPath: cfg.ManifestPath, MirrorURL: cfg.MirrorURL,
		})
		return msgSessionReady{sess: sess, err: err}
	}
}

// logLineMsg wraps a bus line for the dashboard.
type logLineMsg struct{ line logs.Line }

// waitForLogs re-arms bus subscription.
func waitForLogs(ch chan logs.Line) tea.Cmd {
	return func() tea.Msg {
		l, ok := <-ch
		if !ok {
			return nil
		}
		return logLineMsg{line: l}
	}
}
