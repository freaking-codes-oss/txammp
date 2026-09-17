// Command txampp turns any folder into a local web development
// environment: Caddy + static PHP-FPM (+ SQLite via PDO, Adminer, Mailpit)
// with an interactive dashboard — no root, no system packages.
package main

import (
	"os"

	"github.com/freaking-codes-oss/txammp/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
