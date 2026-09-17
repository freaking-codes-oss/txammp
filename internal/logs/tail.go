package logs

import (
	"bytes"
	"context"
	"os"
	"sync"
	"time"
)

// Tailer follows a log file and publishes new lines to a bus. It polls the
// file (no inotify dependency) and handles truncation/rotation by
// reopening from the start.
type Tailer struct {
	Path   string
	Source string
	Bus    *Bus

	// Backlog is the number of pre-existing lines to publish on start.
	// Zero starts at the end of the file (live mode).
	Backlog int

	once sync.Once
}

// Start launches the tail goroutine. It returns immediately.
func (t *Tailer) Start(ctx context.Context) {
	go t.loop(ctx)
}

func (t *Tailer) loop(ctx context.Context) {
	var (
		f      *os.File
		offset int64
		buf    []byte
	)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	open := func(fromEnd bool) {
		if f != nil {
			f.Close()
			f = nil
		}
		file, err := os.Open(t.Path)
		if err != nil {
			return
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return
		}
		f = file
		if fromEnd {
			offset = info.Size()
		} else {
			offset = 0
		}
	}

	// Initial open: with backlog we read the tail of the file; without we
	// start at the end.
	if t.Backlog > 0 {
		if lines := readBacklog(t.Path, t.Backlog); len(lines) > 0 {
			for _, ln := range lines {
				t.Bus.Publish(Line{Source: t.Source, Text: ln})
			}
		}
	}
	open(t.Backlog == 0)

	for {
		select {
		case <-ctx.Done():
			if f != nil {
				f.Close()
			}
			return
		case <-ticker.C:
		}
		if f == nil {
			open(t.Backlog == 0)
			if f == nil {
				continue
			}
		}
		info, err := f.Stat()
		if err != nil {
			open(false)
			continue
		}
		// Detect replacement (same inode check): the path may now point
		// at a different file (rotation, atomic rewrite).
		if current, err := os.Stat(t.Path); err != nil || !os.SameFile(info, current) {
			open(false)
			continue
		}
		if info.Size() < offset {
			// truncated/rotated: reopen from start
			open(false)
			continue
		}
		if info.Size() == offset {
			continue
		}
		chunk := make([]byte, info.Size()-offset)
		n, err := f.ReadAt(chunk, offset)
		if n > 0 {
			offset += int64(n)
			buf = append(buf, chunk[:n]...)
			for {
				idx := bytes.IndexByte(buf, '\n')
				if idx < 0 {
					break
				}
				line := string(buf[:idx])
				buf = buf[idx+1:]
				t.publish(line)
			}
		}
		if err != nil && offset == 0 {
			_ = err // file may vanish; retry next tick
		}
	}
}

func (t *Tailer) publish(line string) {
	if line == "" {
		return
	}
	t.Bus.Publish(Line{Source: t.Source, Text: trimCR(line)})
}

// trimCR strips a single trailing carriage return (CRLF files).
func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

// readBacklog returns the last n lines of a file (or fewer).
func readBacklog(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	if len(data) == 0 {
		return nil
	}
	all := bytes.Split(data, []byte("\n"))
	if len(all) > n {
		all = all[len(all)-n:]
	}
	out := make([]string, 0, len(all))
	for _, l := range all {
		if len(bytes.TrimSpace(l)) > 0 {
			out = append(out, trimCR(string(l)))
		}
	}
	return out
}
