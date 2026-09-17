// Package store manages txampp's global cache directory: downloaded
// binaries, install records and the cached remote manifest.
//
// The cache root is $TXAMPP_HOME when set, otherwise <user cache dir>/txampp
// (e.g. ~/.cache/txampp on Linux). Binaries are stored once per version and
// shared by every project; projects reference them through symlinks inside
// their .stack/bin directory.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EnvHome is the environment variable overriding the cache root.
const EnvHome = "TXAMPP_HOME"

// Store is a handle on the global cache.
type Store struct {
	Root string

	mu    sync.Mutex
	locks map[string]*lockEntry
}

type lockEntry struct {
	file *os.File
}

// FileRecord describes one installed artifact.
type FileRecord struct {
	Path        string    `json:"path"` // absolute path inside the cache
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installed_at"`
}

// installDB is the on-disk layout of installed.json.
type installDB struct {
	Schema int `json:"schema"`
	// component -> version -> fileID -> record
	Components map[string]map[string]map[string]FileRecord `json:"components"`
}

// Open returns a Store rooted at the configured location, creating it when
// missing.
func Open() (*Store, error) {
	root := os.Getenv(EnvHome)
	if root == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(cache, "txampp")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, d := range []string{abs, filepath.Join(abs, "bin"), filepath.Join(abs, "downloads")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{Root: abs, locks: map[string]*lockEntry{}}, nil
}

// DownloadsDir is where archives land before extraction.
func (s *Store) DownloadsDir() string { return filepath.Join(s.Root, "downloads") }

// ComponentDir returns the install directory for a component version.
func (s *Store) ComponentDir(component, version string) string {
	return filepath.Join(s.Root, "bin", component, sanitize(version))
}

// ManifestCachePath is where a fetched remote manifest is cached.
func (s *Store) ManifestCachePath() string { return filepath.Join(s.Root, "manifest.json") }

// Writable reports whether the cache root is writable.
func (s *Store) Writable() bool {
	probe := filepath.Join(s.Root, ".probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return false
	}
	os.Remove(probe)
	return true
}

// ---- install records -------------------------------------------------------

func (s *Store) loadDB() (installDB, error) {
	db := installDB{Schema: 1, Components: map[string]map[string]map[string]FileRecord{}}
	data, err := os.ReadFile(s.dbPath())
	if errors.Is(err, os.ErrNotExist) {
		return db, nil
	}
	if err != nil {
		return db, err
	}
	if err := json.Unmarshal(data, &db); err != nil {
		return db, fmt.Errorf("corrupt %s: %w", s.dbPath(), err)
	}
	if db.Components == nil {
		db.Components = map[string]map[string]map[string]FileRecord{}
	}
	return db, nil
}

func (s *Store) dbPath() string { return filepath.Join(s.Root, "installed.json") }

func (s *Store) saveDB(db installDB) error {
	data, err := json.MarshalIndent(&db, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.dbPath(), data)
}

// Lookup returns the record for one artifact of a component version.
func (s *Store) Lookup(component, version, fileID string) (FileRecord, bool) {
	db, err := s.loadDB()
	if err != nil {
		return FileRecord{}, false
	}
	rec, ok := db.Components[component][version][fileID]
	if !ok {
		return FileRecord{}, false
	}
	// Validate the recorded path still exists.
	if _, err := os.Stat(rec.Path); err != nil {
		return FileRecord{}, false
	}
	return rec, true
}

// Record stores the record for one artifact, creating parent entries.
func (s *Store) Record(component, version, fileID string, rec FileRecord) error {
	db, err := s.loadDB()
	if err != nil {
		return err
	}
	if db.Components[component] == nil {
		db.Components[component] = map[string]map[string]FileRecord{}
	}
	if db.Components[component][version] == nil {
		db.Components[component][version] = map[string]FileRecord{}
	}
	db.Components[component][version][fileID] = rec
	return s.saveDB(db)
}

// InstalledVersions lists installed versions of a component.
func (s *Store) InstalledVersions(component string) []string {
	db, err := s.loadDB()
	if err != nil {
		return nil
	}
	var out []string
	for v := range db.Components[component] {
		out = append(out, v)
	}
	return out
}

// SaveManifest caches a manifest document.
func (s *Store) SaveManifest(data []byte) error {
	return atomicWrite(s.ManifestCachePath(), data)
}

// LoadCachedManifest returns the cached manifest when fresher than ttl.
func (s *Store) LoadCachedManifest(ttl time.Duration) ([]byte, bool) {
	data, err := os.ReadFile(s.ManifestCachePath())
	if err != nil {
		return nil, false
	}
	info, err := os.Stat(s.ManifestCachePath())
	if err != nil || time.Since(info.ModTime()) > ttl {
		return nil, false
	}
	return data, true
}

// ---- locks -----------------------------------------------------------------

// Acquire takes a cross-process lock for a named scope (e.g. "downloads").
// It returns a release function. Locks are held via O_CREATE|O_EXCL lock
// files containing the holder PID; locks held by dead processes are stolen.
func (s *Store) Acquire(name string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.locks[name]; ok {
		// Already held in this process: share it.
		return func() {}, nil
	}
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(s.Root, ".lock-"+sanitize(name))
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(fmt.Sprintf("%d %s\n", os.Getpid(), time.Now().Format(time.RFC3339)))
			_ = f.Close()
			s.locks[name] = &lockEntry{}
			return func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				if _, ok := s.locks[name]; ok {
					os.Remove(lockPath)
					delete(s.locks, name)
				}
			}, nil
		}
		// Lock exists: steal it when stale or after a timeout.
		data, _ := os.ReadFile(lockPath)
		if staleLock(data) || time.Now().After(deadline) {
			os.Remove(lockPath)
			continue
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func staleLock(data []byte) bool {
	var pid int
	var ts string
	if _, err := fmt.Sscanf(string(data), "%d %s", &pid, &ts); err != nil {
		return true // unreadable: steal
	}
	if pid <= 0 || pid == os.Getpid() {
		return true
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil && time.Since(t) > 15*time.Minute {
		return true // too old: assume abandoned
	}
	return !processAlive(pid)
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
