// Package stack orchestrates a project's services: it plans downloads,
// provisions binaries, generates configuration, and starts/stops services
// in dependency order.
package stack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freaking-codes-oss/txammp/internal/configgen"
	"github.com/freaking-codes-oss/txammp/internal/fetch"
	"github.com/freaking-codes-oss/txammp/internal/logs"
	"github.com/freaking-codes-oss/txammp/internal/manifest"
	"github.com/freaking-codes-oss/txammp/internal/platform"
	"github.com/freaking-codes-oss/txammp/internal/ports"
	"github.com/freaking-codes-oss/txammp/internal/proc"
	"github.com/freaking-codes-oss/txammp/internal/project"
	"github.com/freaking-codes-oss/txammp/internal/services"
	"github.com/freaking-codes-oss/txammp/internal/store"
)

// ProgressPhase describes a provisioning event.
type ProgressPhase string

const (
	PhaseStart    ProgressPhase = "start"
	PhaseProgress ProgressPhase = "progress"
	PhaseDone     ProgressPhase = "done"
	PhaseSkip     ProgressPhase = "skip" // already in cache
	PhaseError    ProgressPhase = "error"
)

// ProgressEvent reports provisioning progress to the UI.
type ProgressEvent struct {
	Component string
	Version   string
	FileID    string
	URL       string
	Phase     ProgressPhase
	Written   int64
	Total     int64
	Err       error
}

// Stack ties a project, the manifest and the global cache together.
type Stack struct {
	Proj   *project.Project
	Store  *store.Store
	Man    *manifest.Manifest
	Client *http.Client
	Bus    *logs.Bus

	// discovered holds runtime-discovered PHP versions (branch -> version).
	discovered map[string]string

	tailCancel context.CancelFunc
}

// Open wraps an initialized project.
func Open(p *project.Project, st *store.Store, m *manifest.Manifest, bus *logs.Bus) *Stack {
	if bus == nil {
		bus = logs.NewBus(4000)
	}
	return &Stack{
		Proj:       p,
		Store:      st,
		Man:        m,
		Client:     &http.Client{},
		Bus:        bus,
		discovered: map[string]string{},
	}
}

// SetDiscovered installs runtime-discovered PHP versions.
func (s *Stack) SetDiscovered(d map[string]string) {
	if d != nil {
		s.discovered = d
	}
}

// PHPVersion resolves the effective PHP version for the configured branch.
func (s *Stack) PHPVersion() (string, error) {
	if s.Proj.State.PHPBranch == "" {
		return "", errors.New("no php configured")
	}
	return s.Man.PHPVersion(s.Proj.State.PHPBranch, s.discovered)
}

// ---- provisioning -----------------------------------------------------------

// Plan returns the artifacts needed by the current project state.
func (s *Stack) Plan() ([]manifest.Artifact, error) {
	var out []manifest.Artifact

	caddyArts, err := s.Man.Resolve(manifest.CompCaddy, platformKey())
	if err != nil {
		return nil, fmt.Errorf("caddy: %w", err)
	}
	out = append(out, caddyArts...)

	if s.Proj.State.PHPBranch != "" {
		phpArts, err := s.Man.ResolvePHP(s.Proj.State.PHPBranch, platformKey(), s.discovered)
		if err != nil {
			return nil, fmt.Errorf("php: %w", err)
		}
		out = append(out, phpArts...)
	}

	if s.Proj.State.Adminer && s.Proj.State.PHPBranch != "" {
		arts, err := s.Man.Resolve(manifest.CompAdminer, platformKey())
		if err == nil {
			out = append(out, arts...)
		}
		// A missing Adminer entry is not fatal.
	}

	if s.Proj.State.Mailpit {
		arts, err := s.Man.Resolve(manifest.CompMailpit, platformKey())
		if err != nil {
			return nil, fmt.Errorf("mailpit: %w", err)
		}
		out = append(out, arts...)
	}
	return out, nil
}

// Ensure provisions every needed artifact: cache hit or download+extract.
// It is idempotent and safe to run repeatedly.
func (s *Stack) Ensure(ctx context.Context, prog func(ProgressEvent)) error {
	if prog == nil {
		prog = func(ProgressEvent) {}
	}
	arts, err := s.Plan()
	if err != nil {
		return err
	}
	release, err := s.Store.Acquire("downloads")
	if err != nil {
		return err
	}
	defer release()

	phpVersionResolved := ""
	for _, art := range arts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		path, err := s.ensureArtifact(ctx, art, prog)
		if err != nil {
			return fmt.Errorf("provision %s/%s: %w", art.Component, art.File.ID, err)
		}
		_ = path
		if art.Component == manifest.CompPHP {
			phpVersionResolved = art.Version
		}
	}

	// Record the resolved PHP version in state for display purposes.
	if phpVersionResolved != "" && s.Proj.State.PHPVersion != phpVersionResolved {
		s.Proj.State.PHPVersion = phpVersionResolved
		_ = s.Proj.SaveState()
	}
	return nil
}

// ensureArtifact provisions one artifact and returns its installed path.
func (s *Stack) ensureArtifact(ctx context.Context, art manifest.Artifact, prog func(ProgressEvent)) (string, error) {
	emit := func(ph ProgressPhase, written, total int64, err error) {
		prog(ProgressEvent{
			Component: art.Component, Version: art.Version, FileID: art.File.ID,
			URL: art.File.URL, Phase: ph, Written: written, Total: total, Err: err,
		})
	}

	if rec, ok := s.Store.Lookup(art.Component, art.Version, art.File.ID); ok {
		if err := s.linkArtifact(art, rec.Path); err != nil {
			return "", err
		}
		emit(PhaseSkip, 0, 0, nil)
		return rec.Path, nil
	}

	emit(PhaseStart, 0, 0, nil)
	archivePath := filepath.Join(s.Store.DownloadsDir(), fetchName(art))
	if err := download(ctx, s.Client, art, archivePath, prog); err != nil {
		emit(PhaseError, 0, 0, err)
		return "", err
	}

	var installed string
	if art.File.Format == "file" {
		// Single file (e.g. adminer.php) lands directly in .stack/www.
		destName := art.File.Dest
		if destName == "" {
			destName = filepath.Base(art.File.URL)
		}
		dest := filepath.Join(s.Proj.WWWDir(), destName)
		if err := copyArchiveTo(archivePath, dest); err != nil {
			return "", err
		}
		installed = dest
	} else {
		dir := filepath.Join(s.Store.ComponentDir(art.Component, art.Version), art.File.ID)
		found, err := extractArchive(archivePath, dir, art)
		if err != nil {
			return "", err
		}
		// The primary binary is the first entry of Bins.
		if len(art.File.Bins) > 0 {
			if p, ok := found[filepath.Base(art.File.Bins[0])]; ok {
				installed = p
			}
		}
		if installed == "" {
			// Non-binary archive: record the directory.
			installed = dir
		}
	}

	sha, _ := sha256File(installed)
	if err := s.Store.Record(art.Component, art.Version, art.File.ID, store.FileRecord{
		Path:        installed,
		SHA256:      sha,
		InstalledAt: time.Now().UTC(),
	}); err != nil {
		return "", err
	}
	if err := s.linkArtifact(art, installed); err != nil {
		return "", err
	}
	emit(PhaseDone, 0, 0, nil)
	return installed, nil
}

// linkArtifact links installed binaries into .stack/bin.
func (s *Stack) linkArtifact(art manifest.Artifact, installed string) error {
	for _, bin := range art.File.Bins {
		base := filepath.Base(bin)
		if strings.HasSuffix(installed, base) {
			if err := s.Proj.LinkBin(base, installed); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- services ----------------------------------------------------------------

// Services builds the service list for the current state. Binaries are
// referenced through .stack/bin; missing binaries surface as start errors.
func (s *Stack) Services() []services.Service {
	var out []services.Service
	p := s.Proj

	mailpitEnabled := p.State.Mailpit
	phpEnabled := p.State.PHPBranch != ""

	var mailpitPort int
	if mailpitEnabled {
		mailpitPort = p.State.Ports[project.PortMailpitWeb]
	}

	adminerVersion, _ := s.manifestVersion(manifest.CompAdminer)

	caddySvc := services.NewCaddy(p, p.BinPath("caddy"), configgen.CaddyOptions{
		Port:        p.State.Ports[project.PortWeb],
		DocRoot:     p.Root,
		PHPEnabled:  phpEnabled,
		PHPSocket:   p.SocketPath(),
		Adminer:     p.State.Adminer && phpEnabled,
		WWWDir:      p.WWWDir(),
		Mailpit:     mailpitEnabled,
		MailpitPort: mailpitPort,
		AccessLog:   p.LogPath("caddy-access"),
		RuntimeLog:  p.LogPath("caddy"),
	})
	out = append(out, caddySvc)

	if phpEnabled {
		phpVer, _ := s.PHPVersion()
		sendmail := ""
		if mailpitEnabled {
			if exe, err := os.Executable(); err == nil {
				sendmail = fmt.Sprintf("%s sendmail --smtp 127.0.0.1:%d", exe, p.State.Ports[project.PortMailpitSMTP])
			}
		}
		out = append(out, services.NewPHPFPM(p, p.BinPath("php-fpm"), phpVer, sendmail))
	}

	if mailpitEnabled {
		out = append(out, services.NewMailpit(p, p.BinPath("mailpit")))
	}

	if p.State.Adminer && phpEnabled {
		out = append(out, services.NewAdminer(p, adminerVersion))
	}
	return out
}

// Service finds a service by ID.
func (s *Stack) Service(id string) services.Service {
	for _, svc := range s.Services() {
		if svc.ID() == id {
			return svc
		}
	}
	return nil
}

func (s *Stack) manifestVersion(compID string) (string, error) {
	c, err := s.Man.Component(compID)
	if err != nil {
		return "", err
	}
	return c.Version, nil
}

// ---- lifecycle -----------------------------------------------------------------

// Start ensures ports are usable, then starts services in dependency
// order: PHP-FPM (workers must be up before caddy proxies), Mailpit, and
// finally Caddy. Services already running are left alone.
func (s *Stack) Start(ctx context.Context) error {
	p := s.Proj
	s.ensurePorts()

	php := s.Service("php")
	if php != nil {
		if _, running := procRunningCheck(php); !running {
			if err := php.Start(ctx); err != nil {
				return fmt.Errorf("php: %w", err)
			}
		}
		if err := waitHealthy(ctx, php, 15*time.Second); err != nil {
			return fmt.Errorf("php: %w", err)
		}
		s.Event("php", "php-fpm ready on %s", p.SocketPath())
	}

	mailpit := s.Service("mailpit")
	if mailpit != nil {
		if _, running := procRunningCheck(mailpit); !running {
			if err := mailpit.Start(ctx); err != nil {
				return fmt.Errorf("mailpit: %w", err)
			}
		}
		if err := waitHealthy(ctx, mailpit, 15*time.Second); err != nil {
			return fmt.Errorf("mailpit: %w", err)
		}
		s.Event("mailpit", "mailpit listening on %s", mailpit.Endpoint())
	}

	caddy := s.Service("caddy")
	if _, running := procRunningCheck(caddy); !running {
		if err := caddy.Start(ctx); err != nil {
			return fmt.Errorf("caddy: %w", err)
		}
	}
	if err := waitHealthy(ctx, caddy, 15*time.Second); err != nil {
		return fmt.Errorf("caddy: %w", err)
	}
	s.Event("caddy", "caddy serving http://127.0.0.1:%d", p.State.Ports[project.PortWeb])
	return nil
}

// Stop stops services in reverse order, best effort.
func (s *Stack) Stop(ctx context.Context) error {
	var errs []error
	for _, id := range []string{"caddy", "mailpit", "php"} {
		if svc := s.Service(id); svc != nil {
			if err := svc.Stop(ctx); err != nil && !errors.Is(err, proc.ErrNotRunning) {
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
			} else if err == nil {
				s.Event(id, "%s stopped", svc.Label())
			}
		}
	}
	return errors.Join(errs...)
}

// Restart restarts one service by ID.
func (s *Stack) Restart(ctx context.Context, id string) error {
	svc := s.Service(id)
	if svc == nil {
		return fmt.Errorf("unknown service %q", id)
	}
	if err := svc.Restart(ctx); err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}
	s.Event(id, "%s restarted", svc.Label())
	return nil
}

// ensurePorts renegotiates ports that are busy. Ports already served by
// this stack (our pidfiles) are kept as-is.
func (s *Stack) ensurePorts() {
	p := s.Proj
	type portJob struct {
		key  string
		def  int
		ours func() bool
	}
	jobs := []portJob{
		{project.PortWeb, ports.DefaultWebPort, func() bool { return isOurs(p, "caddy") }},
	}
	if p.State.Mailpit {
		jobs = append(jobs,
			portJob{project.PortMailpitWeb, ports.DefaultMailpitWebPort, func() bool { return isOurs(p, "mailpit") }},
			portJob{project.PortMailpitSMTP, ports.DefaultMailpitSMTPPort, func() bool { return isOurs(p, "mailpit") }},
		)
	}
	changed := false
	for _, j := range jobs {
		current := p.State.Ports[j.key]
		if current == 0 {
			current = j.def
		}
		if j.ours() {
			p.State.Ports[j.key] = current
			continue
		}
		if !ports.Available("127.0.0.1", current) {
			next, err := ports.FindFree("127.0.0.1", current+1, 20)
			if err == nil {
				s.Event("txampp", "port %d busy, switching to %d", current, next)
				p.State.Ports[j.key] = next
				changed = true
			}
		} else {
			p.State.Ports[j.key] = current
		}
	}
	if changed {
		_ = p.SaveState()
	}
}

// isOurs reports whether a service process of this stack is running.
func isOurs(p *project.Project, name string) bool {
	_, ok := procRunning(pidProc(p, name))
	return ok
}

func pidProc(p *project.Project, name string) *proc.Process {
	return &proc.Process{Name: name, PidFile: p.PidPath(name)}
}

func procRunningCheck(svc services.Service) (int, bool) {
	// Delegate: a service is "running" when its status is running or
	// starting (recently started by a previous invocation).
	st := svc.Check(context.Background())
	return 0, st == services.StatusRunning || st == services.StatusStarting
}

func procRunning(p *proc.Process) (int, bool) { return p.Running() }

func platformKey() string { return platform.Key() }

func download(ctx context.Context, client *http.Client, art manifest.Artifact, dest string, prog func(ProgressEvent)) error {
	return fetch.Download(ctx, client, art.File.URL, dest, art.File.SHA256, func(p fetch.Progress) {
		prog(ProgressEvent{
			Component: art.Component, Version: art.Version, FileID: art.File.ID,
			URL: art.File.URL, Phase: PhaseProgress, Written: p.Written, Total: p.Total,
		})
	})
}

// waitHealthy polls a service until it reports running.
func waitHealthy(ctx context.Context, svc services.Service, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st := svc.Check(ctx)
		if st == services.StatusRunning || st == services.StatusReady {
			return nil
		}
		if st == services.StatusStopped || st == services.StatusFailed {
			// Give it a tiny grace: Check may run before the process
			// settles.
			if time.Now().After(deadline) {
				return fmt.Errorf("not healthy (status %s)", st)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for service (status %s)", st)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// ---- logs ---------------------------------------------------------------------

// AttachLogs starts tailers for every service log into the bus.
func (s *Stack) AttachLogs(ctx context.Context, backlog int) {
	if s.tailCancel != nil {
		return
	}
	tailCtx, cancel := context.WithCancel(ctx)
	s.tailCancel = cancel

	sourceOf := map[string]string{
		"caddy": "caddy", "caddy-access": "caddy",
		"php-fpm": "php", "php-error": "php", "php-fpm-slow": "php",
		"mailpit": "mailpit",
	}
	for _, svc := range s.Services() {
		for _, name := range svc.LogNames() {
			source, ok := sourceOf[name]
			if !ok {
				source = svc.ID()
			}
			t := &logs.Tailer{Path: s.Proj.LogPath(name), Source: source, Bus: s.Bus, Backlog: backlog}
			t.Start(tailCtx)
		}
	}
}

// DetachLogs stops tailers.
func (s *Stack) DetachLogs() {
	if s.tailCancel != nil {
		s.tailCancel()
		s.tailCancel = nil
	}
}

// Event records a txampp lifecycle event in the bus and txampp.log.
func (s *Stack) Event(source, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.Bus.Publish(logs.Line{Source: source, Text: msg})
	f, err := os.OpenFile(s.Proj.LogPath("txampp"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		fmt.Fprintf(f, "[%s] %s\n", time.Now().Format(time.RFC3339), msg)
		f.Close()
	}
}

// ---- URLs ---------------------------------------------------------------------

// WebURL returns the project URL.
func (s *Stack) WebURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.Proj.State.Ports[project.PortWeb])
}

// AdminerURL returns the Adminer URL.
func (s *Stack) AdminerURL() string {
	return s.WebURL() + "/__adminer/"
}

// MailpitURL returns the Mailpit web UI URL.
func (s *Stack) MailpitURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.Proj.State.Ports[project.PortMailpitWeb])
}

// SMTPAddr returns the local SMTP address (for the sendmail bridge).
func (s *Stack) SMTPAddr() string {
	return fmt.Sprintf("127.0.0.1:%d", s.Proj.State.Ports[project.PortMailpitSMTP])
}

// ---- provisioning helpers ------------------------------------------------------

// fetchName derives a unique archive file name for an artifact.
func fetchName(art manifest.Artifact) string {
	name := fetch.DownloadedName(art.File.URL)
	if filepath.Ext(name) != ".gz" && filepath.Ext(name) != ".zip" {
		switch art.File.Format {
		case "tar.gz":
			name += ".tar.gz"
		case "zip":
			name += ".zip"
		}
	}
	return name
}

// extractArchive unpacks an artifact archive into dir and returns the
// located binaries by name.
func extractArchive(archivePath, dir string, art manifest.Artifact) (map[string]string, error) {
	return fetch.Extract(archivePath, dir, art.File.Format, art.File.Bins)
}

// sha256File hashes a file for the trust-on-first-use record.
func sha256File(path string) (string, error) {
	return fetch.SHA256File(path)
}

// copyArchiveTo moves a downloaded single-file artifact into place.
func copyArchiveTo(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
