package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/freaking-codes-oss/txammp/internal/ports"
	"github.com/freaking-codes-oss/txammp/internal/project"
	"github.com/freaking-codes-oss/txammp/internal/services"
	"github.com/freaking-codes-oss/txammp/internal/version"
)

// cmdDoctor implements `txampp doctor`: a series of environment checks.
func cmdDoctor(g Global, args []string) int {
	ctx := context.Background()
	dir := g.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	failures := 0

	check := func(name string, err error, warnOnly bool) {
		if err == nil {
			printf("  %s %s\n", styleOK.Render("PASS"), name)
			return
		}
		if warnOnly {
			printf("  %s %s — %v\n", styleWarn.Render("WARN"), name, err)
			return
		}
		printf("  %s %s — %v\n", styleErr.Render("FAIL"), name, err)
		failures++
	}

	printf("%s %s\n\n", styleHead.Render("TXAMPP DOCTOR"), styleDim.Render("v"+version.Short()))

	// Manifest resolution.
	_, m, source, err := setup(ctx, g)
	check("manifest loaded ("+source+")", err, false)

	// Project + binaries.
	sess, sessErr := openSession(ctx, g)
	if sessErr != nil {
		printf("  %s %s — %v\n", styleWarn.Render("WARN"), "project", sessErr)
	} else {
		sk := sess.Stack
		check("project .stack layout", sk.Proj.SaveState(), false)

		for _, svc := range sk.Services() {
			if svc.ID() == "adminer" {
				st := svc.Check(ctx)
				check("adminer file", statusIs(st, services.StatusReady), true)
				continue
			}
			st := svc.Check(ctx)
			switch st {
			case services.StatusRunning, services.StatusStarting, services.StatusStopped:
				printf("  %s %s — binary OK\n", styleOK.Render("PASS"), svc.Label())
			case services.StatusFailed:
				printf("  %s %s — failed earlier (see `txampp logs`)\n", styleWarn.Render("WARN"), svc.Label())
			default:
				printf("  %s %s — not installed yet (run `txampp up`)\n", styleWarn.Render("WARN"), svc.Label())
			}
		}

		webPort := sk.Proj.State.Ports[project.PortWeb]
		if webPort == 0 {
			webPort = ports.DefaultWebPort
		}
		if caddySvc := sk.Service("caddy"); caddySvc != nil && caddySvc.Check(ctx) == services.StatusRunning {
			printf("  %s web port %d — served by this stack\n", styleOK.Render("PASS"), webPort)
		} else if ports.Available("127.0.0.1", webPort) {
			printf("  %s web port %d free\n", styleOK.Render("PASS"), webPort)
		} else {
			printf("  %s web port %d busy — txampp picks the next free one automatically\n", styleWarn.Render("WARN"), webPort)
		}
	}
	_ = m

	// $EDITOR for the TUI config editor.
	if os.Getenv("EDITOR") == "" && os.Getenv("VISUAL") == "" {
		printf("  %s $EDITOR unset — config editing falls back to vi\n", styleWarn.Render("WARN"))
	}

	printf("\n")
	if failures > 0 {
		errf("%d check(s) failed", failures)
		return ExitError
	}
	okf("all checks passed")
	return ExitOK
}

func statusIs(got, want services.Status) error {
	if got != want {
		return fmt.Errorf("status %s", got)
	}
	return nil
}
