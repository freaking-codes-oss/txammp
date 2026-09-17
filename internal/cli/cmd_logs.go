package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/lipgloss"
	"github.com/freaking-codes-oss/txammp/internal/logs"
	"github.com/freaking-codes-oss/txammp/internal/stack"
)

// cmdLogs implements `txampp logs`.
func cmdLogs(g Global, args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	follow := fs.Bool("f", false, "follow: stream new log lines")
	tailN := fs.Int("n", 40, "number of historical lines to show")
	source := fs.String("s", "", "filter by source (caddy, php, mailpit, txampp)")
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

	if !*follow {
		// Dump mode: read the log files directly (works while stopped).
		var lines []logs.Line
		for _, f := range logFilesOf(sk) {
			lines = append(lines, logs.Backlog(f.Path, f.Source, *tailN)...)
		}
		lines = append(lines, logs.Backlog(sk.Proj.LogPath("txampp"), "txampp", *tailN)...)
		if len(lines) == 0 {
			printf("%s\n", styleDim.Render("no logs yet — is the stack running? (txampp up)"))
			return ExitOK
		}
		if len(lines) > *tailN {
			lines = lines[len(lines)-*tailN:]
		}
		for _, l := range lines {
			if matchSource(l.Source, *source) {
				printLogLine(l)
			}
		}
		return ExitOK
	}

	// Follow mode: live tail through the bus.
	sk.AttachLogs(ctx, *tailN)
	defer sk.DetachLogs()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for _, l := range sk.Bus.Recent(*tailN) {
		if matchSource(l.Source, *source) {
			printLogLine(l)
		}
	}
	ch := sk.Bus.Subscribe(512)
	defer sk.Bus.Unsubscribe(ch)
	for {
		select {
		case <-sigCtx.Done():
			printf("\n")
			return ExitOK
		case l := <-ch:
			if matchSource(l.Source, *source) {
				printLogLine(l)
			}
		}
	}
}

// logSourceNames maps log file names (without .log) to display sources.
var logSourceNames = map[string]string{
	"caddy": "caddy", "caddy-access": "caddy",
	"php-fpm": "php", "php-error": "php", "php-fpm-slow": "php",
	"mailpit": "mailpit",
	"txampp":  "txampp",
}

// logFilesOf lists the stack's log files with their display sources.
func logFilesOf(sk *stack.Stack) []struct{ Path, Source string } {
	var out []struct{ Path, Source string }
	for _, svc := range sk.Services() {
		for _, name := range svc.LogNames() {
			src, ok := logSourceNames[name]
			if !ok {
				src = svc.ID()
			}
			out = append(out, struct{ Path, Source string }{sk.Proj.LogPath(name), src})
		}
	}
	return out
}

func matchSource(got, want string) bool { return want == "" || got == want }

var logSourceStyles = map[string]lipgloss.Style{}

func init() {
	for src, color := range map[string]string{
		"caddy": "51", "php": "213", "mailpit": "42", "txampp": "245",
	} {
		logSourceStyles[src] = lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	}
}

func printLogLine(l logs.Line) {
	ts := l.Time.Format("15:04:05")
	srcStyle, ok := logSourceStyles[l.Source]
	if !ok {
		srcStyle = styleDim
	}
	printf("%s %s %s\n", styleDim.Render(ts), srcStyle.Render(fmt.Sprintf("%-9s", "["+l.Source+"]")), l.Text)
}
