// Package fetch downloads stack components with progress reporting,
// optional checksum verification and archive extraction.
package fetch

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Progress reports download progress. Total is 0 when the server did not
// send a Content-Length.
type Progress struct {
	URL      string
	Written  int64
	Total    int64
	Finished bool
	Err      error
}

// ProgressFunc receives throttled progress updates (at most ~10/s) plus a
// final update when the download finishes or fails.
type ProgressFunc func(Progress)

// Download streams url to dest (via a .part temp file, atomically renamed).
// When sha256 is non-empty the downloaded bytes are verified before the
// rename. Progress updates are passed to the optional progress function.
func Download(ctx context.Context, httpClient *http.Client, url, dest, wantSHA256 string, prog ProgressFunc) error {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "txampp")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %s", url, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}

	var lastTick time.Time
	report := func(p Progress) {
		if prog == nil {
			return
		}
		now := time.Now()
		if !p.Finished && now.Sub(lastTick) < 100*time.Millisecond {
			return
		}
		lastTick = now
		prog(p)
	}

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(f, hasher), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(part)
		report(Progress{URL: url, Written: written, Total: resp.ContentLength, Finished: true, Err: copyErr})
		return fmt.Errorf("download %s: %w", url, copyErr)
	}
	if closeErr != nil {
		os.Remove(part)
		return closeErr
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if wantSHA256 != "" && !strings.EqualFold(got, wantSHA256) {
		os.Remove(part)
		err := fmt.Errorf("checksum mismatch for %s: got %s want %s", url, got, wantSHA256)
		report(Progress{URL: url, Written: written, Total: resp.ContentLength, Finished: true, Err: err})
		return err
	}
	if err := os.Rename(part, dest); err != nil {
		os.Remove(part)
		return err
	}
	report(Progress{URL: url, Written: written, Total: resp.ContentLength, Finished: true})
	return nil
}

// Extract unpacks an archive into destDir.
//
//   - "tar.gz" and "zip" archives: when names is non-empty only entries
//     whose base name appears in names are extracted (plus the LICENSE for
//     good measure); otherwise every regular file is extracted.
//   - "file": the source is copied to destDir/<name>.
//
// It returns a map of each requested name to the absolute path where it was
// placed (files are located anywhere inside destDir, so archives with a
// nested bin/ layout work transparently). Paths are sanitized: archives
// cannot write outside destDir.
func Extract(src, destDir, format string, names []string) (map[string]string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, n := range names {
		wanted[filepath.Base(n)] = true
	}

	switch format {
	case "tar.gz", "tgz":
		if err := extractTarGz(src, destDir, wanted); err != nil {
			return nil, err
		}
	case "zip":
		if err := extractZip(src, destDir, wanted); err != nil {
			return nil, err
		}
	case "file":
		if err := copyFile(src, filepath.Join(destDir, filepath.Base(src))); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported archive format %q", format)
	}

	// Locate requested binaries wherever they ended up.
	out := map[string]string{}
	for name := range wanted {
		found, err := findFile(destDir, name)
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", filepath.Base(src), err)
		}
		out[name] = found
	}
	return out, nil
}

func extractTarGz(src, destDir string, wanted map[string]bool) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(filepath.FromSlash(hdr.Name))
		if len(wanted) > 0 && !wanted[base] && base != "LICENSE" {
			continue
		}
		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			continue // skip suspicious entries
		}
		mode := os.FileMode(hdr.Mode) & 0o777
		if err := writeFile(target, tr, mode, wanted[base]); err != nil {
			return err
		}
	}
}

func extractZip(src, destDir string, wanted map[string]bool) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(zf.Name)
		if len(wanted) > 0 && !wanted[base] && base != "LICENSE" {
			continue
		}
		target, err := safeJoin(destDir, zf.Name)
		if err != nil {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		mode := zf.FileInfo().Mode().Perm()
		err = writeFile(target, rc, mode, wanted[base])
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(target string, r io.Reader, mode os.FileMode, executable bool) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	if executable {
		mode |= 0o111
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	return writeFile(dest, in, mode, mode&0o111 != 0)
}

// safeJoin joins destDir with a cleaned archive entry name and verifies the
// result stays inside destDir.
func safeJoin(destDir, name string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	target := filepath.Join(destDir, cleaned)
	root := filepath.Clean(destDir) + string(os.PathSeparator)
	if !strings.HasPrefix(target+string(os.PathSeparator), root) {
		return "", fmt.Errorf("unsafe path in archive: %q", name)
	}
	return target, nil
}

// findFile walks dir looking for a file with the given base name.
func findFile(dir, name string) (string, error) {
	var found string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == name {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("%s not found in extracted archive", name)
	}
	return found, nil
}

// SHA256File computes the hex sha256 of a file (used to record
// trust-on-first-use checksums).
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DownloadedName returns a cache-safe file name for a URL.
func DownloadedName(url string) string {
	u := strings.ReplaceAll(url, "://", "_")
	u = strings.ReplaceAll(u, "?", "_")
	u = strings.ReplaceAll(u, "&", "_")
	// Keep it bounded: use the last 100 chars which contain the distinct part.
	if len(u) > 100 {
		u = u[len(u)-100:]
	}
	return path.Base(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, u))
}
