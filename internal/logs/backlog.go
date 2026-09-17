package logs

import (
	"os"
	"sort"
	"time"
)

// Backlog reads the last n lines of a log file, tagged with source.
func Backlog(path, source string, n int) []Line {
	lines := readBacklog(path, n)
	out := make([]Line, 0, len(lines))
	now := time.Now()
	for _, l := range lines {
		out = append(out, Line{Source: source, Text: l, Time: now})
	}
	return out
}

// MergeBacklog merges and sorts multiple backlogs by nothing more
// sophisticated than file order (per-file timestamps are unavailable
// without parsing); within a file order is preserved.
func MergeBacklog(files []struct{ Path, Source string }, n int) []Line {
	var all []Line
	for _, f := range files {
		if _, err := os.Stat(f.Path); err != nil {
			continue
		}
		all = append(all, Backlog(f.Path, f.Source, n)...)
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// SortLines is provided for callers collecting lines from several
// sources with real timestamps.
func SortLines(lines []Line) {
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Time.Before(lines[j].Time) })
}
