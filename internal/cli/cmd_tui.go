package cli

import (
	"github.com/freaking-codes-oss/txammp/internal/tui"
)

// cmdTUI launches the interactive frontend when stdin+stdout are a
// terminal; otherwise it prints help.
func cmdTUI(g Global) int {
	if !isInteractive() {
		return cmdHelp()
	}
	err := tui.Run(tui.Config{
		Dir:          g.Dir,
		ManifestPath: g.ManifestPath,
		MirrorURL:    g.MirrorURL,
	})
	if err != nil {
		errf("%v", err)
		return ExitError
	}
	return ExitOK
}
