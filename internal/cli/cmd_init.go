package cli

import (
	"context"
	"flag"
	"os"
	"runtime"
	"strings"

	"github.com/freaking-codes-oss/txammp/internal/app"
	"github.com/freaking-codes-oss/txammp/internal/platform"
	"github.com/freaking-codes-oss/txammp/internal/ports"
	"github.com/freaking-codes-oss/txammp/internal/project"
	"github.com/freaking-codes-oss/txammp/internal/version"
)

// cmdInit implements `txampp init`.
func cmdInit(g Global, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	phpFlag := fs.String("php", "", "PHP branch to install (e.g. 8.4, 8.5) or \"none\" for static files only")
	dbFlag := fs.String("db", "sqlite", "database backend: sqlite or none")
	adminerFlag := fs.Bool("adminer", true, "install Adminer (single-file database GUI)")
	noAdminerFlag := fs.Bool("no-adminer", false, "skip Adminer")
	mailpitFlag := fs.Bool("mailpit", false, "install Mailpit (local mail catcher)")
	noMailpitFlag := fs.Bool("no-mailpit", false, "skip Mailpit even if configured")
	portFlag := fs.Int("port", ports.DefaultWebPort, "preferred web port")
	yesFlag := fs.Bool("yes", false, "non-interactive: accept defaults")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	dir := g.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if app.Detect(dir) {
		errf("environment already initialized in %s — run `txampp up`", dir)
		return ExitError
	}

	ctx := context.Background()
	st, m, source, err := setup(ctx, g)
	if err != nil {
		errf("%v", err)
		return ExitError
	}

	// Resolve the PHP choice.
	pkey := platform.Key()
	branches, _ := m.PHPBranches(pkey, nil)
	phpBranch := strings.TrimSpace(*phpFlag)
	phpSupported := len(branches) > 0
	if phpBranch == "" {
		if phpSupported {
			phpBranch = branches[0] // newest first
		} else {
			phpBranch = "none"
		}
	}
	if phpBranch == "latest" && phpSupported {
		if d, err := m.DiscoverPHP(ctx, nil, pkey); err == nil {
			if b, _ := m.PHPBranches(pkey, d); len(b) > 0 {
				phpBranch = b[0]
			}
		}
	}
	if phpBranch != "none" && !contains(branches, phpBranch) {
		if phpSupported {
			errf("PHP %s is not available for %s (available: %s)", phpBranch, pkey, strings.Join(branches, ", "))
		} else {
			errf("PHP is not available on %s in this manifest; use --php none", pkey)
		}
		return ExitError
	}
	if phpBranch == "none" && !phpSupported {
		warnf("static-php builds are not published for %s: continuing with a static-file server", pkey)
	}

	db := *dbFlag
	if db != "sqlite" && db != "none" {
		errf("unknown database %q (use sqlite or none)", db)
		return ExitUsage
	}
	if phpBranch == "none" && db != "none" {
		db = "none"
	}

	adminer := *adminerFlag && !*noAdminerFlag && phpBranch != "none"
	mailpit := *mailpitFlag && !*noMailpitFlag

	phpVersion := ""
	if phpBranch != "none" {
		if v, err := m.PHPVersion(phpBranch, nil); err == nil {
			phpVersion = v
		}
	}

	printf("%s %s\n", styleHead.Render("TXAMPP"), styleDim.Render("v"+version.Short()))
	printf("  project   %s\n", styleBold.Render(dir))
	printf("  manifest  %s\n", styleDim.Render(source))
	if phpBranch == "none" {
		printf("  php       %s\n", styleDim.Render("none (static files)"))
	} else {
		printf("  php       %s (%s)\n", styleBold.Render(phpBranch), styleDim.Render(phpVersion))
	}
	printf("  database  %s\n", db)
	printf("  adminer   %t\n", adminer)
	printf("  mailpit   %t\n\n", mailpit)

	if !*yesFlag && isInteractive() {
		if !confirm("Initialize the environment in this directory?", true) {
			warnf("aborted")
			return ExitError
		}
	} else if !*yesFlag && !isInteractive() {
		errf("refusing to initialize non-interactively without --yes")
		return ExitUsage
	}

	state := project.State{
		PHPBranch:  branchOrEmpty(phpBranch),
		PHPVersion: phpVersion,
		Database:   db,
		Adminer:    adminer,
		Mailpit:    mailpit,
		Ports: map[string]int{
			project.PortWeb:         *portFlag,
			project.PortMailpitWeb:  ports.DefaultMailpitWebPort,
			project.PortMailpitSMTP: ports.DefaultMailpitSMTPPort,
		},
	}
	proj, err := app.Init(dir, state)
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	_ = st

	okf("initialized .stack/ in %s", proj.Root)
	printf("  %s\n", styleDim.Render("add .stack to your editor's ignore list — it is already git-ignored"))
	printf("\nNext steps:\n")
	printf("  %s  start the stack\n", styleBold.Render("txampp up"))
	printf("  %s    interactive dashboard\n", styleBold.Render("txampp"))
	return ExitOK
}

func branchOrEmpty(b string) string {
	if b == "none" {
		return ""
	}
	return b
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// cmdHelp prints usage.
func cmdHelp() int {
	printf("%s — XAMPP-style local web dev stack, zero install, in any folder\n\n", styleBold.Render("txampp"))
	printf("%s\n", styleHead.Render("USAGE"))
	printf("  txampp                      interactive dashboard (or first-run wizard)\n")
	printf("  txampp <command> [flags]\n\n")
	printf("%s\n", styleHead.Render("COMMANDS"))
	printf("  init      initialize .stack/ in the current directory\n")
	printf("  up        download (if needed) and start all services\n")
	printf("  down      stop all services\n")
	printf("  restart   restart all services or one (txampp restart caddy)\n")
	printf("  status    show service status ( --json for machine output)\n")
	printf("  logs      print logs ( -f to follow, -s source to filter)\n")
	printf("  open      open web/adminer/mailpit in the browser\n")
	printf("  configs   show config file paths ( --print <name> to dump)\n")
	printf("  doctor    health-check the environment\n")
	printf("  manifest  show the active component manifest ( --discover)\n")
	printf("  sendmail  sendmail-to-SMTP bridge used by PHP mail()\n")
	printf("  php|caddy|mailpit  run a bundled tool (e.g. txampp php -v)\n")
	printf("  version   print the version\n\n")
	printf("%s\n", styleHead.Render("GLOBAL FLAGS"))
	printf("  --dir PATH        project directory (default: cwd)\n")
	printf("  --manifest PATH   explicit manifest file\n")
	printf("  --mirror URL      component mirror (also TXAMPP_MIRROR)\n")
	printf("  --json            machine readable output where supported\n")
	printf("  --no-color        disable colors\n")
	return ExitOK
}

// cmdVersion prints version information.
func cmdVersion() int {
	printf("%s\n", version.Full())
	printf("%s %s/%s\n", styleDim.Render("platform:"), runtime.GOOS, runtime.GOARCH)
	return ExitOK
}
