package services

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/freaking-codes-oss/txammp/internal/configgen"
	"github.com/freaking-codes-oss/txammp/internal/proc"
	"github.com/freaking-codes-oss/txammp/internal/project"
)

// Caddy is the web server service.
type Caddy struct {
	Proj *project.Project
	// Bin is the caddy binary (resolved from .stack/bin).
	Bin string
	// Port is the HTTP port (from state).
	Port int
	// Options for rendering the Caddyfile.
	Options configgen.CaddyOptions

	proc *proc.Process
	ver  *versionProbe
}

// NewCaddy builds the service around project paths.
func NewCaddy(p *project.Project, bin string, opts configgen.CaddyOptions) *Caddy {
	opts.Port = p.State.Ports[project.PortWeb]
	return &Caddy{
		Proj:    p,
		Bin:     bin,
		Port:    opts.Port,
		Options: opts,
		proc: &proc.Process{
			Name:    "caddy",
			Bin:     bin,
			Args:    []string{"run", "--config", p.ConfigPath("Caddyfile"), "--adapter", "caddyfile"},
			LogFile: p.LogPath("caddy"),
			PidFile: p.PidPath("caddy"),
		},
		ver: newVersionProbe(bin, "", "version"),
	}
}

func (c *Caddy) ID() string    { return "caddy" }
func (c *Caddy) Label() string { return "Caddy Web Server" }
func (c *Caddy) Role() string  { return "Web Server" }
func (c *Caddy) Endpoint() string {
	return endpointPort(c.Proj.State.Ports[project.PortWeb])
}
func (c *Caddy) LogNames() []string { return []string{"caddy", "caddy-access"} }

func (c *Caddy) Version() string {
	if v := c.ver.String(); v != "" {
		return v
	}
	return ""
}

func (c *Caddy) Check(ctx context.Context) Status {
	pid, running := c.proc.Running()
	if running {
		if c.health(ctx, 1*time.Second) {
			return StatusRunning
		}
		if time.Since(c.proc.StartedAt()) < healthStartGrace {
			return StatusStarting
		}
		return StatusFailed
	}
	_ = pid
	if _, err := os.Stat(c.Proj.PidPath("caddy")); err == nil {
		// pidfile exists but process is gone: crashed.
		return StatusFailed
	}
	return StatusStopped
}

func (c *Caddy) health(ctx context.Context, timeout time.Duration) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/__txampp/health", c.Proj.State.Ports[project.PortWeb])
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
	return resp.StatusCode == http.StatusOK
}

func (c *Caddy) Start(ctx context.Context) error {
	// Refresh the port from state (it may have been renegotiated).
	c.Port = c.Proj.State.Ports[project.PortWeb]
	c.Options.Port = c.Port

	// Render + validate configuration before touching the process.
	if err := writeConfig(c.Proj.ConfigPath("Caddyfile"), configgen.Caddyfile(c.Options)); err != nil {
		return err
	}
	if out, err := runCommand(c.Bin, "validate", "--config", c.Proj.ConfigPath("Caddyfile"), "--adapter", "caddyfile"); err != nil {
		return fmt.Errorf("caddyfile invalid: %s", out)
	}
	return c.proc.Start()
}

func (c *Caddy) Stop(ctx context.Context) error {
	return c.proc.Stop(5 * time.Second)
}

func (c *Caddy) Restart(ctx context.Context) error {
	return c.proc.Restart(5 * time.Second)
}

func writeConfig(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
