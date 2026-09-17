// Package manifest describes downloadable stack components and resolves
// them for the current platform.
//
// The manifest is pure data: it can be swapped out wholesale (embedded
// default, a local file, or an HTTP mirror) which makes txampp fully
// testable and offline-friendly.
package manifest

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// File describes a single downloadable artifact.
type File struct {
	// ID is a short identifier for the artifact within its component
	// (e.g. "bin", "fpm", "cli", "app").
	ID string `json:"id"`
	// URL is the absolute download URL.
	URL string `json:"url"`
	// SHA256 is an optional hex-encoded checksum. When empty the artifact
	// is trusted on first use and the observed hash is recorded locally.
	SHA256 string `json:"sha256,omitempty"`
	// Format is one of "tar.gz", "zip" or "file".
	Format string `json:"format"`
	// Bins lists file names inside the archive that should be extracted
	// and marked executable. Unused for "file" format.
	Bins []string `json:"bins,omitempty"`
	// Dest is the destination file name for "file" format artifacts.
	Dest string `json:"dest,omitempty"`
}

// Version is a set of platform-specific artifacts for one component version.
type Version struct {
	Version string            `json:"version"`
	Files   map[string][]File `json:"files"` // platform key -> files
}

// Discovery describes how to discover available versions at runtime.
type Discovery struct {
	// ListURL returns a JSON array of {"name": "...", "is_dir": bool}
	// entries (directory listing format).
	ListURL string `json:"list_url"`
	// Patterns maps platform key -> file ID -> file name template.
	// "{version}" is substituted with the discovered version.
	Patterns map[string]map[string]string `json:"patterns"`
}

// Component is a downloadable stack component.
type Component struct {
	Label string `json:"label"`
	// Kind is informational: "webserver", "runtime", "dbgui", "smtp".
	Kind string `json:"kind"`
	// Version + Files are used for single-version components.
	Version string            `json:"version,omitempty"`
	Files   map[string][]File `json:"files,omitempty"`
	// Versions maps a branch ("8.4") to a pinned version. Used by
	// multi-version components such as PHP.
	Versions map[string]*Version `json:"versions,omitempty"`
	// Discovery optionally allows discovering versions at runtime.
	Discovery *Discovery `json:"discovery,omitempty"`
}

// Manifest is the top-level document.
type Manifest struct {
	Schema     int                   `json:"schema"`
	Generated  time.Time             `json:"generated"`
	Components map[string]*Component `json:"components"`
}

// Well known component IDs.
const (
	CompCaddy   = "caddy"
	CompPHP     = "php"
	CompAdminer = "adminer"
	CompMailpit = "mailpit"
)

// Artifact is a resolved artifact: a file for a specific platform.
type Artifact struct {
	Component string
	Version   string
	File      File
}

//go:embed default.json
var defaultManifestJSON []byte

// Default returns the manifest compiled into the binary.
func Default() (*Manifest, error) {
	return Parse(defaultManifestJSON)
}

// Parse decodes manifest JSON.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("unsupported manifest schema %d (want 1)", m.Schema)
	}
	if len(m.Components) == 0 {
		return nil, errors.New("manifest contains no components")
	}
	return &m, nil
}

// LoadFile reads a manifest from a path.
func LoadFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// LoadURL fetches a manifest over HTTP with a timeout.
func LoadURL(ctx context.Context, url string) (*Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Component returns a component by ID.
func (m *Manifest) Component(id string) (*Component, error) {
	c, ok := m.Components[id]
	if !ok {
		return nil, fmt.Errorf("manifest has no component %q", id)
	}
	return c, nil
}

// Resolve returns the artifacts for a single-version component on the
// current platform.
func (m *Manifest) Resolve(compID, platformKey string) ([]Artifact, error) {
	c, err := m.Component(compID)
	if err != nil {
		return nil, err
	}
	files, err := filesFor(c.Files, platformKey)
	if err != nil {
		return nil, fmt.Errorf("component %q: %w", compID, err)
	}
	out := make([]Artifact, 0, len(files))
	for _, f := range files {
		out = append(out, Artifact{Component: compID, Version: c.Version, File: f})
	}
	return out, nil
}

// PHPBranches returns the available PHP branches for the platform,
// sorted newest first. Pinned versions come first; discovered versions
// (if provided) are merged in.
func (m *Manifest) PHPBranches(platformKey string, discovered map[string]string) ([]string, error) {
	c, err := m.Component(CompPHP)
	if err != nil {
		return nil, err
	}
	branches := map[string]bool{}
	for br, v := range c.Versions {
		if _, ok := v.Files[platformKey]; ok {
			branches[br] = true
		}
	}
	for br := range discovered {
		branches[br] = true
	}
	out := make([]string, 0, len(branches))
	for br := range branches {
		out = append(out, br)
	}
	sort.Slice(out, func(i, j int) bool { return phpBranchLess(out[i], out[j]) })
	return out, nil
}

// ResolvePHP returns the artifacts (fpm + cli) for a PHP branch on the
// current platform. Discovered versions take precedence over the pinned
// manifest version for the same branch.
func (m *Manifest) ResolvePHP(branch, platformKey string, discovered map[string]string) ([]Artifact, error) {
	c, err := m.Component(CompPHP)
	if err != nil {
		return nil, err
	}
	v, ok := c.Versions[branch]
	if !ok {
		// Branch only exists via discovery.
		ver, ok2 := discovered[branch]
		if !ok2 {
			return nil, fmt.Errorf("no PHP branch %q for %s", branch, platformKey)
		}
		arts, err := c.DiscoveredArtifacts(branch, ver, platformKey)
		if err != nil {
			return nil, err
		}
		return arts, nil
	}
	files, err := filesFor(v.Files, platformKey)
	if err != nil {
		return nil, fmt.Errorf("php %s: %w", branch, err)
	}
	// Discovered version is fresher than the pinned one: prefer it.
	if dv, ok := discovered[branch]; ok && dv != v.Version {
		if arts, err := c.DiscoveredArtifacts(branch, dv, platformKey); err == nil {
			return arts, nil
		}
	}
	out := make([]Artifact, 0, len(files))
	for _, f := range files {
		out = append(out, Artifact{Component: CompPHP, Version: v.Version, File: f})
	}
	return out, nil
}

// PHPVersion returns the resolved PHP version string for a branch
// (discovered version if known, otherwise the pinned version).
func (m *Manifest) PHPVersion(branch string, discovered map[string]string) (string, error) {
	c, err := m.Component(CompPHP)
	if err != nil {
		return "", err
	}
	if v, ok := c.Versions[branch]; ok {
		if dv, ok := discovered[branch]; ok && dv != "" {
			return dv, nil
		}
		return v.Version, nil
	}
	if v, ok := discovered[branch]; ok {
		return v, nil
	}
	return "", fmt.Errorf("unknown PHP branch %q", branch)
}

// DiscoveredArtifacts builds artifacts for a version that was discovered
// via the discovery endpoint (no pinned URL/checksum).
func (c *Component) DiscoveredArtifacts(branch, version, platformKey string) ([]Artifact, error) {
	if c.Discovery == nil {
		return nil, fmt.Errorf("php %s: no discovery configured", branch)
	}
	pats, ok := c.Discovery.Patterns[platformKey]
	if !ok {
		return nil, fmt.Errorf("php %s: no discovery patterns for %s", branch, platformKey)
	}
	var out []Artifact
	for id, tmpl := range pats {
		if id != "fpm" && id != "cli" {
			continue
		}
		name := strings.ReplaceAll(tmpl, "{version}", version)
		// The list URL is a directory listing (possibly with a query
		// string); build the file URL from its directory part.
		base := strings.TrimRight(strings.SplitN(c.Discovery.ListURL, "?", 2)[0], "/")
		f := File{
			ID:     id,
			URL:    base + "/" + name,
			Format: "tar.gz",
			Bins:   []string{"php-fpm"},
		}
		if id == "cli" {
			f.Bins = []string{"php"}
		}
		out = append(out, Artifact{Component: CompPHP, Version: version, File: f})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("php %s: discovery produced no artifacts", branch)
	}
	return out, nil
}

// DiscoverPHP queries the discovery endpoint and returns branch -> version
// for the newest build of every major.minor branch that has all required
// artifacts for the platform.
func (m *Manifest) DiscoverPHP(ctx context.Context, httpClient *http.Client, platformKey string) (map[string]string, error) {
	c, err := m.Component(CompPHP)
	if err != nil || c.Discovery == nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Discovery.ListURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discover php versions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discover php versions: HTTP %s", resp.Status)
	}
	var entries []struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("discover php versions: bad listing: %w", err)
	}
	return versionsFromListing(entries, c.Discovery.Patterns, platformKey)
}

// versionsFromListing maps directory entries to branch -> newest version,
// requiring the "fpm" pattern to match (the "cli" pattern is optional but
// included when present).
func versionsFromListing(entries []struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}, patterns map[string]map[string]string, platformKey string) (map[string]string, error) {
	pats, ok := patterns[platformKey]
	if !ok {
		return nil, fmt.Errorf("no discovery patterns for %s", platformKey)
	}
	fpmRe, err := patternToRegex(pats["fpm"])
	if err != nil {
		return nil, err
	}
	cliRe, _ := patternToRegex(pats["cli"])
	byVersion := map[string]bool{} // version has fpm
	cliOK := map[string]bool{}     // version has cli
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		if m := fpmRe.FindStringSubmatch(e.Name); m != nil {
			byVersion[m[1]] = true
		}
		if cliRe != nil {
			if m := cliRe.FindStringSubmatch(e.Name); m != nil {
				cliOK[m[1]] = true
			}
		}
	}
	branches := map[string]string{}
	for v := range byVersion {
		br := majorMinor(v)
		if br == "" {
			continue
		}
		if cur, ok := branches[br]; !ok || versionLess(cur, v) {
			branches[br] = v
		}
	}
	// Only keep versions that also have a CLI build (txampp installs both).
	for br, v := range branches {
		if !cliOK[v] {
			delete(branches, br)
		}
	}
	if len(branches) == 0 {
		return nil, errors.New("no php versions discovered for this platform")
	}
	return branches, nil
}

// patternToRegex turns "php-{version}-fpm-linux-x86_64.tar.gz" into a
// regexp capturing the version.
func patternToRegex(tmpl string) (*regexp.Regexp, error) {
	if tmpl == "" {
		return nil, nil
	}
	escaped := regexp.QuoteMeta(tmpl)
	// The "{version}" braces were quoted; unquote and replace with a group.
	escaped = strings.ReplaceAll(escaped, regexp.QuoteMeta("{version}"), `(\d+\.\d+\.\d+(?:-[A-Za-z0-9.]+)?)`)
	return regexp.Compile("^" + escaped + "$")
}

// majorMinor extracts "8.4" from "8.4.14".
func majorMinor(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

func phpBranchLess(a, b string) bool { return versionLess(b, a) } // newest first

// versionLess compares dotted numeric versions, tolerating pre-release
// suffixes (e.g. "8.4.14RC1" sorts before "8.4.14").
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, bi := numericPrefix(as[i]), numericPrefix(bs[i])
		if ai != bi {
			return ai < bi
		}
		if as[i] != bs[i] {
			// Same number, different suffix (e.g. "14RC1" vs "14"):
			// the plain numeric segment is the final release and sorts
			// after any pre-release suffix.
			aPlain, bPlain := allDigits(as[i]), allDigits(bs[i])
			if aPlain != bPlain {
				return bPlain
			}
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}

func numericPrefix(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// allDigits reports whether a segment is purely numeric.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func filesFor(files map[string][]File, platformKey string) ([]File, error) {
	if files == nil {
		return nil, errors.New("no artifacts defined")
	}
	if fs, ok := files[platformKey]; ok {
		return fs, nil
	}
	if fs, ok := files["*"]; ok {
		return fs, nil
	}
	return nil, fmt.Errorf("no artifacts for platform %s", platformKey)
}
