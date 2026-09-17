package services

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/freaking-codes-oss/txammp/internal/proc"
	"github.com/freaking-codes-oss/txammp/internal/project"
)

// Mailpit is the fake SMTP catcher service.
type Mailpit struct {
	Proj *project.Project
	Bin  string

	proc *proc.Process
	ver  *versionProbe
}

// NewMailpit builds the service around project paths.
func NewMailpit(p *project.Project, bin string) *Mailpit {
	return &Mailpit{
		Proj: p,
		Bin:  bin,
		proc: &proc.Process{
			Name: "mailpit",
			Bin:  bin,
			Args: []string{
				"--smtp", fmt.Sprintf("127.0.0.1:%d", p.State.Ports[project.PortMailpitSMTP]),
				"--listen", fmt.Sprintf("127.0.0.1:%d", p.State.Ports[project.PortMailpitWeb]),
			},
			LogFile: p.LogPath("mailpit"),
			PidFile: p.PidPath("mailpit"),
		},
		ver: newVersionProbe(bin, "", "version"),
	}
}

func (m *Mailpit) ID() string    { return "mailpit" }
func (m *Mailpit) Label() string { return "Mailpit" }
func (m *Mailpit) Role() string  { return "Mail Trap" }
func (m *Mailpit) Endpoint() string {
	return fmt.Sprintf(":%d (web) :%d (smtp)",
		m.Proj.State.Ports[project.PortMailpitWeb],
		m.Proj.State.Ports[project.PortMailpitSMTP])
}
func (m *Mailpit) LogNames() []string { return []string{"mailpit"} }

func (m *Mailpit) Version() string { return m.ver.String() }

func (m *Mailpit) Check(ctx context.Context) Status {
	_, running := m.proc.Running()
	if running {
		if m.health(ctx, 1*time.Second) {
			return StatusRunning
		}
		if time.Since(m.proc.StartedAt()) < healthStartGrace {
			return StatusStarting
		}
		return StatusFailed
	}
	if _, err := os.Stat(m.Proj.PidPath("mailpit")); err == nil {
		return StatusFailed
	}
	return StatusStopped
}

func (m *Mailpit) health(ctx context.Context, timeout time.Duration) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/", m.Proj.State.Ports[project.PortMailpitWeb])
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}

func (m *Mailpit) Start(ctx context.Context) error {
	// Refresh ports from state in case they were renegotiated.
	m.proc.Args = []string{
		"--smtp", fmt.Sprintf("127.0.0.1:%d", m.Proj.State.Ports[project.PortMailpitSMTP]),
		"--listen", fmt.Sprintf("127.0.0.1:%d", m.Proj.State.Ports[project.PortMailpitWeb]),
	}
	return m.proc.Start()
}

func (m *Mailpit) Stop(ctx context.Context) error {
	return m.proc.Stop(5 * time.Second)
}

func (m *Mailpit) Restart(ctx context.Context) error {
	return m.proc.Restart(5 * time.Second)
}
