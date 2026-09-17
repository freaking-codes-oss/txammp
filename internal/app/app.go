// Package app contains setup shared by the CLI and TUI frontends:
// manifest resolution, project opening and stack construction.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/freaking-codes-oss/txammp/internal/logs"
	"github.com/freaking-codes-oss/txammp/internal/manifest"
	"github.com/freaking-codes-oss/txammp/internal/project"
	"github.com/freaking-codes-oss/txammp/internal/stack"
	"github.com/freaking-codes-oss/txammp/internal/store"
)

// Options mirrors the global flags both frontends accept.
type Options struct {
	Dir          string
	ManifestPath string
	MirrorURL    string
}

// FromEnv fills defaults from the environment.
func (o *Options) FromEnv() {
	if o.MirrorURL == "" {
		o.MirrorURL = os.Getenv("TXAMPP_MIRROR")
	}
	if o.Dir == "" {
		o.Dir, _ = os.Getwd()
	}
}

// ManifestTTL bounds reuse of a cached remote manifest.
const ManifestTTL = 24 * time.Hour

// LoadManifest resolves the effective manifest:
//
//  1. opts.ManifestPath  — explicit file
//  2. opts.MirrorURL     — $mirror/manifest.json over HTTP
//  3. $TXAMPP_MANIFEST_URL
//  4. cached remote manifest (TTL bounded)
//  5. the manifest embedded in the binary
func LoadManifest(ctx context.Context, o Options, st *store.Store) (*manifest.Manifest, string, error) {
	if o.ManifestPath != "" {
		m, err := manifest.LoadFile(o.ManifestPath)
		if err != nil {
			return nil, "", fmt.Errorf("--manifest: %w", err)
		}
		return m, o.ManifestPath, nil
	}
	if o.MirrorURL != "" {
		url := trimRightSlash(o.MirrorURL) + "/manifest.json"
		m, err := manifest.LoadURL(ctx, url)
		if err != nil {
			return nil, "", fmt.Errorf("mirror %s: %w", o.MirrorURL, err)
		}
		saveCache(st, m)
		return m, "mirror: " + o.MirrorURL, nil
	}
	if env := os.Getenv("TXAMPP_MANIFEST_URL"); env != "" {
		m, err := manifest.LoadURL(ctx, env)
		if err != nil {
			return nil, "", fmt.Errorf("TXAMPP_MANIFEST_URL: %w", err)
		}
		saveCache(st, m)
		return m, env, nil
	}
	if data, ok := st.LoadCachedManifest(ManifestTTL); ok {
		if m, err := manifest.Parse(data); err == nil {
			return m, "cached remote", nil
		}
	}
	m, err := manifest.Default()
	if err != nil {
		return nil, "", err
	}
	return m, "embedded default", nil
}

func saveCache(st *store.Store, m *manifest.Manifest) {
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = st.SaveManifest(data)
	}
}

func trimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// Session bundles everything a frontend needs.
type Session struct {
	Stack *stack.Stack
	Bus   *logs.Bus
}

// Open opens an initialized project and builds a stack from the given
// options. PHP version discovery is attempted (best effort, bounded by a
// short timeout).
func Open(ctx context.Context, o Options) (*Session, error) {
	o.FromEnv()
	proj, err := project.Open(o.Dir)
	if err != nil {
		return nil, err
	}
	st, err := store.Open()
	if err != nil {
		return nil, err
	}
	m, _, err := LoadManifest(ctx, o, st)
	if err != nil {
		return nil, err
	}
	bus := logs.NewBus(4000)
	sk := stack.Open(proj, st, m, bus)
	if proj.State.PHPBranch != "" {
		if d, err := discoverPHP(ctx, m); err == nil {
			sk.SetDiscovered(d)
		}
	}
	return &Session{Stack: sk, Bus: bus}, nil
}

// discoverPHP wraps manifest discovery with a short timeout.
func discoverPHP(ctx context.Context, m *manifest.Manifest) (map[string]string, error) {
	dctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return m.DiscoverPHP(dctx, nil, currentPlatform())
}

// Init initializes a project directory with the given state.
func Init(dir string, st project.State) (*project.Project, error) {
	return project.Init(dir, st)
}

// Detect reports whether dir has an initialized environment.
func Detect(dir string) bool { return project.Detect(dir) }
