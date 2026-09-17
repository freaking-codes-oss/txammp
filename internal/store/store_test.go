package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := openAt(filepath.Join(t.TempDir(), "txampp"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecordLookup(t *testing.T) {
	s := openTemp(t)
	bin := filepath.Join(t.TempDir(), "caddy")
	os.WriteFile(bin, []byte("bin"), 0o755)
	rec := FileRecord{Path: bin, SHA256: "abc", InstalledAt: time.Now()}
	if err := s.Record("caddy", "2.11.4", "bin", rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, ok := s.Lookup("caddy", "2.11.4", "bin")
	if !ok || got.Path != bin {
		t.Fatalf("Lookup: %+v ok=%v", got, ok)
	}
	if _, ok := s.Lookup("caddy", "2.11.4", "other"); ok {
		t.Errorf("unexpected hit for other file")
	}
	if _, ok := s.Lookup("caddy", "1.0", "bin"); ok {
		t.Errorf("unexpected hit for other version")
	}
}

func TestLookupValidatesFileExists(t *testing.T) {
	s := openTemp(t)
	path := filepath.Join(t.TempDir(), "bin")
	os.WriteFile(path, []byte("x"), 0o755)
	_ = s.Record("php", "8.4.14", "fpm", FileRecord{Path: path})
	if _, ok := s.Lookup("php", "8.4.14", "fpm"); !ok {
		t.Errorf("record with existing file should hit")
	}
	os.Remove(path)
	if _, ok := s.Lookup("php", "8.4.14", "fpm"); ok {
		t.Errorf("record with deleted file should miss")
	}
}

func TestInstalledVersions(t *testing.T) {
	s := openTemp(t)
	_ = s.Record("php", "8.4.14", "fpm", FileRecord{Path: "/a"})
	_ = s.Record("php", "8.5.8", "fpm", FileRecord{Path: "/b"})
	vs := s.InstalledVersions("php")
	if len(vs) != 2 {
		t.Fatalf("want 2 versions, got %v", vs)
	}
}

func TestManifestCache(t *testing.T) {
	s := openTemp(t)
	if _, ok := s.LoadCachedManifest(time.Minute); ok {
		t.Errorf("cache should be empty initially")
	}
	if err := s.SaveManifest([]byte(`{"schema":1,"components":{}}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LoadCachedManifest(time.Minute); !ok {
		t.Errorf("cache should be fresh")
	}
	// Negative TTL: expired.
	if _, ok := s.LoadCachedManifest(-time.Second); ok {
		t.Errorf("cache should be expired")
	}
}

func TestLockAcquireRelease(t *testing.T) {
	s := openTemp(t)
	release, err := s.Acquire("downloads")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Re-entrant in the same process succeeds immediately.
	release2, err := s.Acquire("downloads")
	if err != nil {
		t.Fatalf("re-entrant Acquire: %v", err)
	}
	release2()
	release()

	// After release, acquiring again works.
	release3, err := s.Acquire("downloads")
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	release3()
}

func TestLockStealsStale(t *testing.T) {
	s := openTemp(t)
	// Simulate a stale lock from a dead PID.
	lockPath := filepath.Join(s.Root, ".lock-downloads")
	os.WriteFile(lockPath, []byte("999999999 2020-01-01T00:00:00Z"), 0o644)

	release, err := s.Acquire("downloads")
	if err != nil {
		t.Fatalf("Acquire should steal the stale lock: %v", err)
	}
	release()
}

func TestWritable(t *testing.T) {
	s := openTemp(t)
	if !s.Writable() {
		t.Errorf("temp store should be writable")
	}
}
