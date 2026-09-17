package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/freaking-codes-oss/txammp/internal/services"
	"github.com/freaking-codes-oss/txammp/internal/stack"
)

// cmdUp implements `txampp up`.
func cmdUp(g Global, args []string) int {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	ctx := context.Background()
	sess, err := openSession(ctx, g)
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	sk := sess.Stack
	sk.AttachLogs(ctx, 40)
	defer sk.DetachLogs()

	// Provision components with a CLI progress display.
	progress := newCLIProgress(g.NoColor)
	err = sk.Ensure(ctx, progress.event)
	progress.finish()
	if err != nil {
		errf("%v", err)
		return ExitError
	}

	if err := sk.Start(ctx); err != nil {
		errf("%v", err)
		printLogTailHint(sk)
		return ExitError
	}

	printSummary(sk)
	return ExitOK
}

func printLogTailHint(sk *stack.Stack) {
	printf("  %s run `txampp logs -n 40` to inspect service logs\n", styleDim.Render("hint:"))
}

func printSummary(sk *stack.Stack) {
	printf("\n%s\n", styleHead.Render("STACK ONLINE"))
	printf("  site      %s\n", styleURL.Render(sk.WebURL()))
	if sk.Proj.State.Adminer && sk.Proj.State.PHPBranch != "" {
		printf("  adminer   %s\n", styleURL.Render(sk.AdminerURL()))
	}
	if sk.Proj.State.Mailpit {
		printf("  mailpit   %s (smtp %s)\n", styleURL.Render(sk.MailpitURL()), sk.SMTPAddr())
	}
	printf("\n  %s\n", styleDim.Render("run `txampp` for the interactive dashboard, `txampp down` to stop"))
}

// cmdDown implements `txampp down`.
func cmdDown(g Global, args []string) int {
	ctx := context.Background()
	sess, err := openSession(ctx, g)
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	if err := sess.Stack.Stop(ctx); err != nil {
		errf("%v", err)
		return ExitError
	}
	okf("stack stopped")
	return ExitOK
}

// cmdRestart implements `txampp restart [service]`.
func cmdRestart(g Global, args []string) int {
	ctx := context.Background()
	sess, err := openSession(ctx, g)
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	sk := sess.Stack
	if len(args) == 0 {
		if err := sk.Stop(ctx); err != nil {
			errf("stop: %v", err)
			return ExitError
		}
		if err := sk.Start(ctx); err != nil {
			errf("start: %v", err)
			return ExitError
		}
		okf("stack restarted")
		printSummary(sk)
		return ExitOK
	}
	if err := sk.Restart(ctx, args[0]); err != nil {
		errf("%v", err)
		return ExitError
	}
	okf("%s restarted", args[0])
	return ExitOK
}

// cmdStatus implements `txampp status`.
func cmdStatus(g Global, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	watch := fs.Bool("w", false, "watch: refresh every second")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	ctx := context.Background()
	sess, err := openSession(ctx, g)
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	sk := sess.Stack

	for {
		if g.JSON {
			return printStatusJSON(ctx, sk)
		}
		printStatusTable(ctx, sk)
		if !*watch {
			return ExitOK
		}
		time.Sleep(time.Second)
		clearScreen()
	}
}

func printStatusTable(ctx context.Context, sk *stack.Stack) {
	printf("%s %s\n\n", styleHead.Render("TXAMPP STATUS"), styleDim.Render(sk.Proj.Root))
	printf("  %-22s %-10s %-16s %s\n", styleBold.Render("SERVICE"), "STATUS", "VERSION", "ENDPOINT")
	anyRunning := false
	for _, svc := range sk.Services() {
		st := svc.Check(ctx)
		if st == services.StatusRunning || st == services.StatusStarting {
			anyRunning = true
		}
		stStyle, ok := statusStyles[st]
		if !ok {
			stStyle = styleDim
		}
		printf("  %-22s %-10s %-16s %s\n",
			svc.Label(),
			stStyle.Render(strings.ToUpper(string(st))),
			truncate(svc.Version(), 16),
			svc.Endpoint())
	}
	printf("\n")
	if anyRunning {
		printf("  site      %s\n", styleURL.Render(sk.WebURL()))
		if sk.Proj.State.Adminer && sk.Proj.State.PHPBranch != "" {
			printf("  adminer   %s\n", styleURL.Render(sk.AdminerURL()))
		}
		if sk.Proj.State.Mailpit {
			printf("  mailpit   %s\n", styleURL.Render(sk.MailpitURL()))
		}
	} else {
		printf("  %s\n", styleDim.Render("stack is down — run `txampp up`"))
	}
}

func printStatusJSON(ctx context.Context, sk *stack.Stack) int {
	type svcJSON struct {
		ID       string `json:"id"`
		Label    string `json:"label"`
		Status   string `json:"status"`
		Version  string `json:"version"`
		Endpoint string `json:"endpoint"`
	}
	out := struct {
		Project  string    `json:"project"`
		Running  bool      `json:"running"`
		Services []svcJSON `json:"services"`
		URLs     struct {
			Web     string `json:"web,omitempty"`
			Adminer string `json:"adminer,omitempty"`
			Mailpit string `json:"mailpit,omitempty"`
		} `json:"urls"`
	}{Project: sk.Proj.Root, Services: []svcJSON{}}
	for _, svc := range sk.Services() {
		st := svc.Check(ctx)
		if st == services.StatusRunning {
			out.Running = true
		}
		out.Services = append(out.Services, svcJSON{
			ID: svc.ID(), Label: svc.Label(), Status: string(st),
			Version: svc.Version(), Endpoint: svc.Endpoint(),
		})
	}
	out.URLs.Web = sk.WebURL()
	if sk.Proj.State.Adminer && sk.Proj.State.PHPBranch != "" {
		out.URLs.Adminer = sk.AdminerURL()
	}
	if sk.Proj.State.Mailpit {
		out.URLs.Mailpit = sk.MailpitURL()
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return ExitError
	}
	return ExitOK
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func clearScreen() { printf("\033[2J\033[H") }

// ---- download progress rendering ---------------------------------------------

// cliProgress renders artifact download progress on a single line per
// artifact (updates in place on TTYs).
type cliProgress struct {
	plain    bool
	current  string
	maxLabel int
	lastPct  int
}

func newCLIProgress(noColor bool) *cliProgress {
	return &cliProgress{plain: noColor || !isTTY(os.Stdout)}
}

func (p *cliProgress) event(ev stack.ProgressEvent) {
	label := ev.Component + "@" + ev.Version + "/" + ev.FileID
	switch ev.Phase {
	case "skip":
		p.flush()
		printf("  %s %s %s\n", styleOK.Render("✓"), padTo(label, 28), styleDim.Render("(cached)"))
	case "start":
		p.flush()
		p.current = label
		p.lastPct = -1
		printf("  %s %s …\r", styleDim.Render("↓"), padTo(label, 28))
	case "progress":
		if p.plain {
			return
		}
		pct := -1
		if ev.Total > 0 {
			pct = int(ev.Written * 100 / ev.Total)
		}
		if pct == p.lastPct {
			return
		}
		p.lastPct = pct
		if pct >= 0 {
			printf("  %s %s %s %3d%%\r", styleDim.Render("↓"), padTo(label, 28), bar20(pct), pct)
		} else {
			printf("  %s %s %s\r", styleDim.Render("↓"), padTo(label, 28), humanBytes(ev.Written))
		}
	case "done":
		p.flush()
		printf("  %s %s\n", styleOK.Render("✓"), padTo(label, 28))
	case "error":
		p.flush()
		printf("  %s %s %v\n", styleErr.Render("✗"), padTo(label, 28), ev.Err)
	}
}

func (p *cliProgress) flush() {
	if p.current != "" {
		printf("\r\033[K")
		p.current = ""
	}
}

func (p *cliProgress) finish() { p.flush() }

func padTo(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func bar20(pct int) string {
	filled := pct * 20 / 100
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", 20-filled) + "]"
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
