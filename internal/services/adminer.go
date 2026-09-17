package services

import (
	"context"
	"os"

	"github.com/freaking-codes-oss/txammp/internal/project"
)

// Adminer is a file-based service: the single adminer.php file served by
// Caddy at /__adminer.
type Adminer struct {
	Proj       *project.Project
	VersionStr string
}

// NewAdminer builds the service.
func NewAdminer(p *project.Project, version string) *Adminer {
	return &Adminer{Proj: p, VersionStr: version}
}

func (a *Adminer) ID() string    { return "adminer" }
func (a *Adminer) Label() string { return "Adminer" }
func (a *Adminer) Role() string  { return "Database GUI" }
func (a *Adminer) Endpoint() string {
	if a.Check(context.Background()) == StatusReady {
		return "/__adminer"
	}
	return "-"
}
func (a *Adminer) LogNames() []string { return nil }
func (a *Adminer) Version() string    { return a.VersionStr }

func (a *Adminer) Check(ctx context.Context) Status {
	if _, err := os.Stat(a.Proj.AdminerPath()); err == nil {
		return StatusReady
	}
	return StatusMissing
}

// Start is a no-op: provisioning happens during `ensure`.
func (a *Adminer) Start(ctx context.Context) error { return nil }

func (a *Adminer) Stop(ctx context.Context) error { return nil }

func (a *Adminer) Restart(ctx context.Context) error { return nil }
