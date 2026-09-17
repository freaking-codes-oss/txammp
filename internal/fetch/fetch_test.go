package fetch

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadWithProgressAndChecksum(t *testing.T) {
	payload := bytes.Repeat([]byte("txampp"), 10000) // 60KB
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "txampp" {
			t.Errorf("missing user agent")
		}
		w.Write(payload)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	var updates int
	var finished bool
	err := Download(context.Background(), srv.Client(), srv.URL, dest, sha, func(p Progress) {
		if p.Finished {
			finished = true
		}
		updates++
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !finished {
		t.Errorf("no final progress event")
	}
	if updates < 1 {
		t.Errorf("expected at least the final progress event, got %d", updates)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Errorf("content mismatch")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Errorf(".part file left behind")
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("data")) }))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "out.bin")
	err := Download(context.Background(), srv.Client(), srv.URL, dest, "deadbeef", nil)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum error, got %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("destination should not exist on failure")
	}
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := Download(context.Background(), srv.Client(), srv.URL, dest, "", nil); err == nil {
		t.Fatal("expected error")
	}
}

// buildTarGz builds an in-memory tar.gz with the given files.
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}
		if strings.HasPrefix(name, "bin/") {
			hdr.Mode = 0o755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractTarGzNestedBin(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	os.WriteFile(archive, buildTarGz(t, map[string]string{
		"bin/php-fpm":          "binary",
		"bin/php":              "binary2",
		"LICENSE":              "license",
		"README.md":            "readme",
		"lib/pkg/whatever.txt": "nope",
	}), 0o644)

	dest := t.TempDir()
	found, err := Extract(archive, dest, "tar.gz", []string{"php-fpm"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	path, ok := found["php-fpm"]
	if !ok {
		t.Fatalf("php-fpm not found: %v", found)
	}
	if filepath.Base(path) != "php-fpm" {
		t.Errorf("unexpected path %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("binary not executable: %v", info.Mode())
	}
	// unwanted files not extracted (LICENSE allowed)
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README should be filtered out")
	}
	if _, err := os.Stat(filepath.Join(dest, "lib")); !os.IsNotExist(err) {
		t.Errorf("lib should be filtered out")
	}
}

func TestExtractTarGzAllWhenNoBins(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	os.WriteFile(archive, buildTarGz(t, map[string]string{"a.txt": "1", "b/c.txt": "2"}), 0o644)
	dest := t.TempDir()
	if _, err := Extract(archive, dest, "tar.gz", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "b", "c.txt")); err != nil {
		t.Errorf("expected all files extracted: %v", err)
	}
}

func TestExtractZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "a.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("caddy")
	w.Write([]byte("caddy-binary"))
	w2, _ := zw.Create("extra.txt")
	w2.Write([]byte("extra"))
	zw.Close()
	os.WriteFile(zipPath, buf.Bytes(), 0o644)

	dest := t.TempDir()
	found, err := Extract(zipPath, dest, "zip", []string{"caddy"})
	if err != nil {
		t.Fatalf("Extract zip: %v", err)
	}
	if found["caddy"] == "" {
		t.Errorf("caddy not extracted")
	}
	if _, err := os.Stat(filepath.Join(dest, "extra.txt")); !os.IsNotExist(err) {
		t.Errorf("extra.txt should be filtered")
	}
}

func TestExtractFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "adminer.php")
	os.WriteFile(src, []byte("<?php echo 1;"), 0o644)
	dest := t.TempDir()
	found, err := Extract(src, dest, "file", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("no bins expected for file format, got %v", found)
	}
	data, _ := os.ReadFile(filepath.Join(dest, "adminer.php"))
	if string(data) != "<?php echo 1;" {
		t.Errorf("copy mismatch")
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if _, err := safeJoin(dir, "../evil"); err == nil {
		t.Errorf("relative traversal must be rejected")
	}
	if _, err := safeJoin(dir, "/etc/passwd"); err == nil {
		t.Errorf("absolute path must be rejected")
	}
	if _, err := safeJoin(dir, "ok/file.txt"); err != nil {
		t.Errorf("valid path rejected: %v", err)
	}
}

func TestSHA256File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	os.WriteFile(p, []byte("hello"), 0o644)
	got, err := SHA256File(p)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("hello"))
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("hash mismatch")
	}
}
