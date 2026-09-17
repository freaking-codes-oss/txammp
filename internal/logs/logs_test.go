package logs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBusPublishSubscribe(t *testing.T) {
	b := NewBus(100)
	ch := b.Subscribe(8)

	b.Publish(Line{Source: "caddy", Text: "hello"})
	select {
	case l := <-ch:
		if l.Text != "hello" || l.Source != "caddy" {
			t.Errorf("bad line: %+v", l)
		}
	case <-time.After(time.Second):
		t.Fatalf("no line received")
	}

	b.Unsubscribe(ch)
	// Publishing after unsubscribe must not block.
	b.Publish(Line{Source: "x", Text: "y"})
}

func TestBusRing(t *testing.T) {
	b := NewBus(10)
	for i := 0; i < 25; i++ {
		b.Publish(Line{Text: itoa(i)})
	}
	recent := b.Recent(0)
	if len(recent) != 10 {
		t.Fatalf("ring should cap at 10, has %d", len(recent))
	}
	if recent[0].Text != "15" || recent[9].Text != "24" {
		t.Errorf("ring content wrong: %s..%s", recent[0].Text, recent[9].Text)
	}
	if got := b.Recent(3); len(got) != 3 || got[0].Text != "22" {
		t.Errorf("Recent(3) wrong: %+v", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestBusSlowSubscriberDoesNotBlock(t *testing.T) {
	b := NewBus(10)
	_ = b.Subscribe(1) // tiny buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			b.Publish(Line{Text: "x"})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("publishing blocked on slow subscriber")
	}
}

func TestTailerFollowsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	os.WriteFile(path, []byte("existing line\n"), 0o644)

	bus := NewBus(100)
	ch := bus.Subscribe(64)
	tailer := &Tailer{Path: path, Source: "php", Bus: bus, Backlog: 10}
	ctx, cancel := context.WithCancel(context.Background())
	tailer.Start(ctx)
	defer cancel()

	// Backlog line appears.
	waitForLine(t, ch, "existing line")

	// New appends appear.
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString("new line 1\nnew line 2\n")
	f.Close()
	waitForLine(t, ch, "new line 1")
	waitForLine(t, ch, "new line 2")
}

func TestTailerLiveOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	os.WriteFile(path, []byte("old stuff\n"), 0o644)

	bus := NewBus(10)
	ch := bus.Subscribe(8)
	tailer := &Tailer{Path: path, Source: "caddy", Bus: bus, Backlog: 0}
	ctx, cancel := context.WithCancel(context.Background())
	tailer.Start(ctx)
	defer cancel()

	// Give the tailer a poll cycle to open the file before writing.
	time.Sleep(400 * time.Millisecond)

	// Without backlog the old line must NOT appear; only new writes.
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString("fresh\n")
	f.Close()
	waitForLine(t, ch, "fresh")

	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, l := range bus.ring {
		if l.Text == "old stuff" {
			t.Errorf("backlog leaked in live mode")
		}
	}
}

func TestTailerSurvivesReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	os.WriteFile(path, []byte("one\ntwo\n"), 0o644)

	bus := NewBus(10)
	ch := bus.Subscribe(8)
	tailer := &Tailer{Path: path, Source: "db", Bus: bus, Backlog: 0}
	ctx, cancel := context.WithCancel(context.Background())
	tailer.Start(ctx)
	defer cancel()
	time.Sleep(400 * time.Millisecond)

	// Rotation: write a new file and rename it over the path (new inode).
	tmp := path + ".new"
	os.WriteFile(tmp, []byte("rotated\n"), 0o644)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	waitForLine(t, ch, "rotated")
}

func TestTailerSurvivesTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644)

	bus := NewBus(10)
	ch := bus.Subscribe(8)
	tailer := &Tailer{Path: path, Source: "db", Bus: bus, Backlog: 0}
	ctx, cancel := context.WithCancel(context.Background())
	tailer.Start(ctx)
	defer cancel()
	time.Sleep(400 * time.Millisecond)

	// Truncate to zero, let a poll cycle observe it, then append.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString("after-truncate\n")
	f.Close()
	waitForLine(t, ch, "after-truncate")
}

func waitForLine(t *testing.T, ch chan Line, text string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case l := <-ch:
			if l.Text == text {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("line %q never arrived", text)
}

var _ = sync.Mutex{}
