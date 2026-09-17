// Package logs fans log lines from service log files in to subscribers
// (the TUI dashboard, `txampp logs -f`) through a ring buffer.
package logs

import (
	"strings"
	"sync"
	"time"
)

// Line is one log line.
type Line struct {
	Source string    // "caddy", "php", "mailpit", "txampp", ...
	Text   string    // line without trailing newline
	Time   time.Time // when it was observed
}

// Bus is a fan-out multiplexer with a bounded history.
type Bus struct {
	mu    sync.Mutex
	subs  map[chan Line]struct{}
	ring  []Line
	limit int
}

// NewBus creates a bus retaining the last `limit` lines.
func NewBus(limit int) *Bus {
	if limit <= 0 {
		limit = 4000
	}
	return &Bus{subs: map[chan Line]struct{}{}, limit: limit}
}

// Subscribe registers a buffered subscriber channel.
func (b *Bus) Subscribe(buffer int) chan Line {
	if buffer <= 0 {
		buffer = 256
	}
	ch := make(chan Line, buffer)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber and drains it.
func (b *Bus) Unsubscribe(ch chan Line) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// Publish appends a line to the ring and delivers it to every subscriber.
// Slow subscribers drop lines rather than block the pipeline.
func (b *Bus) Publish(l Line) {
	if l.Time.IsZero() {
		l.Time = time.Now()
	}
	b.mu.Lock()
	b.ring = append(b.ring, l)
	if len(b.ring) > b.limit {
		b.ring = b.ring[len(b.ring)-b.limit:]
	}
	subs := make([]chan Line, 0, len(b.subs))
	for ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- l:
		default: // subscriber is behind: drop
		}
	}
}

// Recent returns up to n most recent lines.
func (b *Bus) Recent(n int) []Line {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.ring) {
		n = len(b.ring)
	}
	out := make([]Line, n)
	copy(out, b.ring[len(b.ring)-n:])
	return out
}

// Sources returns the distinct sources seen so far, sorted.
func (b *Bus) Sources() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := map[string]bool{}
	for _, l := range b.ring {
		seen[l.Source] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	// simple sorted order
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Text renders lines with "[source] text" prefixes.
func Text(lines []Line) string {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString("[")
		sb.WriteString(l.Source)
		sb.WriteString("] ")
		sb.WriteString(l.Text)
		sb.WriteString("\n")
	}
	return sb.String()
}
