// Package services implements the individual stack services (Caddy,
// PHP-FPM, Mailpit, Adminer) behind a common interface.
package services

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Status is the runtime state of a service.
type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusFailed   Status = "failed"
	StatusReady    Status = "ready" // file-based services (Adminer)
	StatusMissing  Status = "missing"
)

// Service is one supervised stack component.
type Service interface {
	// ID is the stable service identifier ("caddy", "php", "mailpit",
	// "adminer").
	ID() string
	// Label is the display name.
	Label() string
	// Role describes what it does ("Web Server", "PHP Runtime", ...).
	Role() string
	// Version returns a version string (probed from the binary when
	// possible).
	Version() string
	// Endpoint returns the user-visible endpoint (":8080", "unix socket",
	// "/__adminer", "-").
	Endpoint() string
	// Check reports the current status.
	Check(ctx context.Context) Status
	// Start brings the service up.
	Start(ctx context.Context) error
	// Stop tears the service down.
	Stop(ctx context.Context) error
	// Restart stops and starts again.
	Restart(ctx context.Context) error
	// LogNames lists this service's log file names (relative to
	// .stack/logs).
	LogNames() []string
}

// healthStartGrace is how long a freshly started process may fail its
// health probe before being called "failed".
const healthStartGrace = 3 * time.Second

// probeVersion runs a version command once and caches the first line of
// output. Fallback is returned when the command fails.
type versionProbe struct {
	once     sync.Once
	bin      string
	args     []string
	fallback string
	result   string
}

func newVersionProbe(bin string, fallback string, args ...string) *versionProbe {
	return &versionProbe{bin: bin, args: args, fallback: fallback}
}

func (v *versionProbe) String() string {
	v.once.Do(func() {
		v.result = v.fallback
		if out, err := runCommand(v.bin, v.args...); err == nil && out != "" {
			v.result = out
		}
	})
	return v.result
}

// StatusWithGrace maps a process+health probe result to a Status.
func StatusWithGrace(running bool, startedAt time.Time, healthy func() bool) Status {
	if running {
		if healthy() {
			return StatusRunning
		}
		if time.Since(startedAt) < healthStartGrace {
			return StatusStarting
		}
		return StatusFailed
	}
	return StatusStopped
}

func endpointPort(port int) string { return fmt.Sprintf(":%d", port) }
