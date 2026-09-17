// Package cli implements txampp's command line interface.
package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/freaking-codes-oss/txammp/internal/app"
	"github.com/freaking-codes-oss/txammp/internal/manifest"
	"github.com/freaking-codes-oss/txammp/internal/store"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Global holds cross-cutting flags.
type Global struct {
	Dir          string
	ManifestPath string
	MirrorURL    string
	JSON         bool
	NoColor      bool
}

// Run dispatches a subcommand and returns the process exit code.
func Run(args []string) int {
	var g Global

	// Extract global flags anywhere in the args.
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dir" && i+1 < len(args):
			i++
			g.Dir = args[i]
		case hasPrefix(a, "--dir="):
			g.Dir = trimPrefix(a, "--dir=")
		case a == "--manifest" && i+1 < len(args):
			i++
			g.ManifestPath = args[i]
		case hasPrefix(a, "--manifest="):
			g.ManifestPath = trimPrefix(a, "--manifest=")
		case a == "--mirror" && i+1 < len(args):
			i++
			g.MirrorURL = args[i]
		case hasPrefix(a, "--mirror="):
			g.MirrorURL = trimPrefix(a, "--mirror=")
		case a == "--json":
			g.JSON = true
		case a == "--no-color" || os.Getenv("NO_COLOR") != "":
			g.NoColor = true
		case a == "--help" || a == "-h":
			return cmdHelp()
		case a == "--version":
			return cmdVersion()
		case a == "--":
			rest = append(rest, args[i+1:]...)
			i = len(args)
		default:
			rest = append(rest, a)
		}
	}
	g.NoColor = g.NoColor || os.Getenv("NO_COLOR") != "" || !isTTY(os.Stdout)
	enableColor(!g.NoColor)

	if g.MirrorURL == "" {
		g.MirrorURL = os.Getenv("TXAMPP_MIRROR")
	}

	if len(rest) == 0 {
		// No subcommand: launch the TUI when interactive.
		return cmdTUI(g)
	}

	cmd, cmdArgs := rest[0], rest[1:]
	switch cmd {
	case "help":
		return cmdHelp()
	case "version":
		return cmdVersion()
	case "init":
		return cmdInit(g, cmdArgs)
	case "up", "start":
		return cmdUp(g, cmdArgs)
	case "down", "stop":
		return cmdDown(g, cmdArgs)
	case "restart":
		return cmdRestart(g, cmdArgs)
	case "status":
		return cmdStatus(g, cmdArgs)
	case "logs":
		return cmdLogs(g, cmdArgs)
	case "open":
		return cmdOpen(g, cmdArgs)
	case "configs", "config":
		return cmdConfigs(g, cmdArgs)
	case "sendmail":
		return cmdSendmail(g, cmdArgs)
	case "doctor":
		return cmdDoctor(g, cmdArgs)
	case "manifest":
		return cmdManifest(g, cmdArgs)
	case "php", "caddy", "mailpit", "adminer":
		// Convenience: `txampp php -v` style passthrough.
		return cmdRunTool(g, cmd, cmdArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (see `txampp help`)\n", cmd)
		return ExitUsage
	}
}

// ---- shared setup -----------------------------------------------------------

// appOptions converts global flags into app options.
func appOptions(g Global) app.Options {
	return app.Options{Dir: g.Dir, ManifestPath: g.ManifestPath, MirrorURL: g.MirrorURL}
}

// setup opens store + manifest for commands that need the global side.
func setup(ctx context.Context, g Global) (*store.Store, *manifest.Manifest, string, error) {
	st, err := store.Open()
	if err != nil {
		return nil, nil, "", err
	}
	o := appOptions(g)
	o.FromEnv()
	m, source, err := app.LoadManifest(ctx, o, st)
	if err != nil {
		return nil, nil, "", err
	}
	return st, m, source, nil
}

// openSession opens the project + stack for commands operating on an
// initialized environment.
func openSession(ctx context.Context, g Global) (*app.Session, error) {
	return app.Open(ctx, appOptions(g))
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func trimPrefix(s, p string) string {
	if hasPrefix(s, p) {
		return s[len(p):]
	}
	return s
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
