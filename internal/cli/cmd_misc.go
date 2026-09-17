package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/freaking-codes-oss/txammp/internal/browser"
	"github.com/freaking-codes-oss/txammp/internal/platform"
	"github.com/freaking-codes-oss/txammp/internal/ports"
	"github.com/freaking-codes-oss/txammp/internal/sendmail"
	"github.com/freaking-codes-oss/txammp/internal/services"
)

// cmdOpen implements `txampp open [web|adminer|mailpit]`.
func cmdOpen(g Global, args []string) int {
	target := "web"
	if len(args) > 0 {
		target = args[0]
	}
	ctx := context.Background()
	sess, err := openSession(ctx, g)
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	sk := sess.Stack
	var url string
	switch target {
	case "web", "site", ".":
		url = sk.WebURL()
	case "adminer", "db":
		if !sk.Proj.State.Adminer || sk.Proj.State.PHPBranch == "" {
			errf("Adminer is not enabled for this project")
			return ExitError
		}
		url = sk.AdminerURL()
	case "mailpit", "mail":
		if !sk.Proj.State.Mailpit {
			errf("Mailpit is not enabled for this project")
			return ExitError
		}
		url = sk.MailpitURL()
	default:
		errf("unknown target %q (web, adminer, mailpit)", target)
		return ExitUsage
	}
	// Verify the relevant service is actually serving.
	if st := sk.Service("caddy").Check(ctx); st != services.StatusRunning && target != "mailpit" {
		errf("stack is not running — run `txampp up` first")
		return ExitError
	}
	if err := browser.Open(url); err != nil {
		warnf("could not launch a browser: %v", err)
		printf("  %s\n", styleURL.Render(url))
		return ExitError
	}
	okf("opened %s", styleURL.Render(url))
	return ExitOK
}

// cmdConfigs implements `txampp configs`.
func cmdConfigs(g Global, args []string) int {
	fs := flag.NewFlagSet("configs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	printName := fs.String("print", "", "dump a config file: Caddyfile, php.ini, php-fpm.conf")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	ctx := context.Background()
	sess, err := openSession(ctx, g)
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	p := sess.Stack.Proj
	configs := []struct{ name, path string }{
		{"Caddyfile", p.ConfigPath("Caddyfile")},
		{"php.ini", p.ConfigPath("php.ini")},
		{"php-fpm.conf", p.ConfigPath("php-fpm.conf")},
		{"state.json", p.StatePath()},
	}
	if *printName != "" {
		for _, c := range configs {
			if strings.EqualFold(c.name, *printName) {
				data, err := os.ReadFile(c.path)
				if err != nil {
					errf("%v", err)
					return ExitError
				}
				printf("%s", string(data))
				return ExitOK
			}
		}
		errf("unknown config %q", *printName)
		return ExitUsage
	}
	printf("%s\n", styleHead.Render("CONFIG FILES"))
	for _, c := range configs {
		exists := " "
		if _, err := os.Stat(c.path); err == nil {
			exists = styleOK.Render("✓")
		}
		printf("  %s %-12s %s\n", exists, c.name, styleDim.Render(c.path))
	}
	return ExitOK
}

// cmdSendmail implements the sendmail bridge used by PHP mail().
func cmdSendmail(g Global, args []string) int {
	fs := flag.NewFlagSet("sendmail", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printf("usage: txampp sendmail [-t] [-i] [-f from] [--smtp host:port] < message\n") }
	smtpAddr := fs.String("smtp", "", "SMTP server address (default: this project's Mailpit)")
	from := fs.String("f", "", "envelope sender")
	_ = fs.Bool("i", false, "ignored (sendmail compatibility)")
	_ = fs.Bool("t", true, "read recipients from headers (sendmail compatibility)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	// sendmail compatibility: bare -t/-i/-f addr also accepted positionally.
	for _, a := range fs.Args() {
		switch {
		case a == "-t" || a == "-i":
		case strings.HasPrefix(a, "-f"):
			if a == "-f" {
				continue
			}
			*from = strings.TrimPrefix(a, "-f")
		}
	}

	addr := *smtpAddr
	if addr == "" {
		// Prefer this project's Mailpit when available.
		if sess, err := openSession(context.Background(), g); err == nil {
			addr = sess.Stack.SMTPAddr()
		} else {
			addr = fmt.Sprintf("127.0.0.1:%d", ports.DefaultMailpitSMTPPort)
		}
	}
	err := sendmail.Run(os.Stdin, sendmail.Options{
		SMTPAddr: addr,
		ReadTo:   true,
		Sender:   *from,
	})
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	return ExitOK
}

// cmdRunTool passes through to bundled binaries (txampp php -v).
func cmdRunTool(g Global, tool string, args []string) int {
	ctx := context.Background()
	sess, err := openSession(ctx, g)
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	p := sess.Stack.Proj
	bin := p.BinPath(tool)
	if _, err := os.Stat(bin); err != nil {
		errf("%s is not installed (run `txampp up`)", tool)
		return ExitError
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		errf("%v", err)
		return ExitError
	}
	return ExitOK
}

// cmdManifest implements `txampp manifest`.
func cmdManifest(g Global, args []string) int {
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	discover := fs.Bool("discover", false, "also discover live PHP versions")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	ctx := context.Background()
	st, m, source, err := setup(ctx, g)
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	_ = st

	printf("%s  (%s)\n\n", styleHead.Render("MANIFEST"), styleDim.Render(source))

	listComponent := func(id string) {
		c, err := m.Component(id)
		if err != nil {
			return
		}
		printf("  %-10s %-8s %s\n", styleBold.Render(id), "v"+c.Version, c.Label)
		if c.Versions != nil {
			for br, v := range c.Versions {
				printf("             %-8s %s\n", br, v.Version)
			}
		}
	}
	listComponent("caddy")
	listComponent("php")
	listComponent("adminer")
	listComponent("mailpit")

	if *discover {
		printf("\n%s\n", styleHead.Render("DISCOVERED PHP"))
		d, err := m.DiscoverPHP(ctx, nil, platform.Key())
		if err != nil {
			warnf("discovery unavailable: %v", err)
			return ExitOK
		}
		for br, v := range d {
			printf("  php %s -> %s\n", br, v)
		}
	}
	return ExitOK
}
