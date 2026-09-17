// Package project manages the per-project .stack directory: layout,
// persisted state, gitignore hygiene and binary symlinks.
//
// The layout (everything txampp owns lives under .stack/):
//
//	.stack/
//	  bin/      symlinks (or copies on Windows) to cached binaries
//	  run/      PID files, unix sockets, state.json, sessions
//	  logs/     caddy.log, caddy-access.log, php-fpm.log, php-error.log, ...
//	  data/     project-local databases (e.g. sqlite files)
//	  config/   Caddyfile, php.ini, php-fpm.conf
//	  www/      bundled web apps (Adminer)
package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// DirName is the managed directory created inside a project.
const DirName = ".stack"

// Schema of state.json.
const stateSchema = 1

// State is the persisted project configuration.
type State struct {
	Schema     int            `json:"schema"`
	PHPBranch  string         `json:"php_branch,omitempty"` // "8.4" or "" for no PHP
	PHPVersion string         `json:"php_version,omitempty"`
	Database   string         `json:"database"` // "sqlite" | "none"
	Adminer    bool           `json:"adminer"`
	Mailpit    bool           `json:"mailpit"`
	Ports      map[string]int `json:"ports"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// Port names.
const (
	PortWeb         = "web"
	PortMailpitWeb  = "mailpit_web"
	PortMailpitSMTP = "mailpit_smtp"
)

// Project is an initialized project directory.
type Project struct {
	Root  string // absolute project root (document root)
	State State
}

// Detect returns whether dir contains an initialized txampp environment.
func Detect(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, DirName, "run", "state.json"))
	return err == nil && !info.IsDir()
}

// Open loads an existing project. It errors when the directory has not
// been initialized.
func Open(dir string) (*Project, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if !Detect(root) {
		return nil, fmt.Errorf("no txampp environment in %s: run `txampp init` first", root)
	}
	p := &Project{Root: root}
	data, err := os.ReadFile(p.StatePath())
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &p.State); err != nil {
		return nil, fmt.Errorf("corrupt %s: %w", p.StatePath(), err)
	}
	if p.State.Ports == nil {
		p.State.Ports = map[string]int{}
	}
	return p, nil
}

// Init creates the .stack layout in dir with the given state. It refuses
// to touch an existing environment (use Open).
func Init(dir string, st State) (*Project, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	if Detect(root) {
		return nil, errors.New("environment already initialized")
	}
	st.Schema = stateSchema
	if st.Database == "" {
		st.Database = "sqlite"
	}
	if st.Ports == nil {
		st.Ports = map[string]int{}
	}
	now := time.Now().UTC()
	st.CreatedAt = now
	st.UpdatedAt = now
	p := &Project{Root: root, State: st}
	for _, d := range p.dirs() {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if err := p.SaveState(); err != nil {
		return nil, err
	}
	if err := p.ensureGitignore(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Project) dirs() []string {
	return []string{p.BinDir(), p.RunDir(), p.PidDir(), p.LogDir(), p.DataDir(), p.ConfigDir(), p.WWWDir(), p.SessionsDir()}
}

// StackDir is <root>/.stack.
func (p *Project) StackDir() string { return filepath.Join(p.Root, DirName) }

// BinDir holds symlinks to cached binaries.
func (p *Project) BinDir() string { return filepath.Join(p.StackDir(), "bin") }

// RunDir holds runtime files (sockets, state).
func (p *Project) RunDir() string { return filepath.Join(p.StackDir(), "run") }

// PidDir holds PID files.
func (p *Project) PidDir() string { return filepath.Join(p.RunDir(), "pid") }

// LogDir holds service logs.
func (p *Project) LogDir() string { return filepath.Join(p.StackDir(), "logs") }

// DataDir holds project-local databases.
func (p *Project) DataDir() string { return filepath.Join(p.StackDir(), "data") }

// ConfigDir holds generated configuration files.
func (p *Project) ConfigDir() string { return filepath.Join(p.StackDir(), "config") }

// WWWDir holds bundled web apps (Adminer).
func (p *Project) WWWDir() string { return filepath.Join(p.StackDir(), "www") }

// SessionsDir holds PHP session files.
func (p *Project) SessionsDir() string { return filepath.Join(p.RunDir(), "sessions") }

// StatePath is the state.json location.
func (p *Project) StatePath() string { return filepath.Join(p.RunDir(), "state.json") }

// SocketPath is the PHP-FPM unix socket.
func (p *Project) SocketPath() string { return filepath.Join(p.RunDir(), "php-fpm.sock") }

// ConfigPath returns the path of a named config file.
func (p *Project) ConfigPath(name string) string { return filepath.Join(p.ConfigDir(), name) }

// LogPath returns the path of a named log file.
func (p *Project) LogPath(name string) string { return filepath.Join(p.LogDir(), name+".log") }

// PidPath returns the pid file for a service.
func (p *Project) PidPath(service string) string { return filepath.Join(p.PidDir(), service+".pid") }

// AdminerPath returns the installed Adminer file.
func (p *Project) AdminerPath() string { return filepath.Join(p.WWWDir(), "adminer.php") }

// BinPath returns the .stack/bin path for a binary name.
func (p *Project) BinPath(name string) string { return filepath.Join(p.BinDir(), name) }

// SaveState writes state.json atomically.
func (p *Project) SaveState() error {
	p.State.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(&p.State, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.StatePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.StatePath())
}

// LinkBin links (or copies on Windows) a cached binary into .stack/bin.
func (p *Project) LinkBin(name, target string) error {
	dst := p.BinPath(name)
	_ = os.Remove(dst)
	if err := os.MkdirAll(p.BinDir(), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(target, dst); err == nil {
		return nil
	} else if copyBinary(target, dst) == nil {
		return nil
	} else {
		// Symlink unsupported and copy failed.
		return fmt.Errorf("link %s -> %s: %w", dst, target, err)
	}
}

func copyBinary(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

// ensureGitignore makes sure the .stack directory never gets committed:
// a .stack/.gitignore containing "*" covers it from any parent git repo
// without touching the user's own .gitignore.
func (p *Project) ensureGitignore() error {
	return os.WriteFile(filepath.Join(p.StackDir(), ".gitignore"), []byte("*\n"), 0o644)
}

// IsGitRepo reports whether the project root is inside a git repository.
func (p *Project) IsGitRepo() bool {
	dir := p.Root
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// GitCommand builds an exec.Cmd for git inside the project.
func (p *Project) GitCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = p.Root
	return cmd
}
