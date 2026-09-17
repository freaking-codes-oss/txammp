// Package version holds build metadata for txampp.
package version

import "fmt"

// These values are overridden at build time via -ldflags.
var (
	Version = "0.1.0"
	Commit  = "none"
	Date    = "unknown"
)

// Full returns a human readable version string.
func Full() string {
	return fmt.Sprintf("txampp v%s (commit %s, built %s)", Version, Commit, Date)
}

// Short returns just the semantic version.
func Short() string { return Version }
