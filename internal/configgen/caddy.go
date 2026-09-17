// Package configgen renders service configuration files (Caddyfile,
// php-fpm.conf, php.ini) for a project.
package configgen

import (
	"fmt"
	"strings"
)

// CaddyOptions describes everything the generated Caddyfile needs.
type CaddyOptions struct {
	Port        int    // HTTP port caddy listens on
	DocRoot     string // absolute path to the project root
	PHPEnabled  bool   // serve PHP through php-fpm
	PHPSocket   string // unix socket path (ignored when PHP disabled)
	Adminer     bool   // expose Adminer at /__adminer
	WWWDir      string // directory holding adminer.php
	Mailpit     bool   // reverse-proxy Mailpit UI at /__mailpit
	MailpitPort int    // mailpit web port
	AccessLog   string // access log file (site level)
	RuntimeLog  string // runtime log file (global level)
}

// Caddyfile renders the Caddyfile.
//
// Route order is explicit (caddy `route` preserves order):
//  1. health endpoint for txampp itself
//  2. block dotfiles (.stack, .git, ...) from being served
//  3. optional Mailpit reverse proxy at /__mailpit
//  4. optional Adminer app at /__adminer
//  5. project document root with php_fastcgi + file_server
func Caddyfile(o CaddyOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "{\n")
	fmt.Fprintf(&b, "\tadmin off\n")
	fmt.Fprintf(&b, "\tauto_https off\n")
	fmt.Fprintf(&b, "\tlog {\n")
	fmt.Fprintf(&b, "\t\toutput file %s\n", o.RuntimeLog)
	fmt.Fprintf(&b, "\t\tformat console\n")
	fmt.Fprintf(&b, "\t\tlevel INFO\n")
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "}\n\n")

	fmt.Fprintf(&b, ":%d {\n", o.Port)
	fmt.Fprintf(&b, "\troot * %s\n", caddyPath(o.DocRoot))
	fmt.Fprintf(&b, "\tencode zstd gzip\n\n")
	fmt.Fprintf(&b, "\tlog {\n")
	fmt.Fprintf(&b, "\t\toutput file %s\n", caddyPath(o.AccessLog))
	fmt.Fprintf(&b, "\t\tformat console\n")
	fmt.Fprintf(&b, "\t}\n\n")
	fmt.Fprintf(&b, "\troute {\n")
	fmt.Fprintf(&b, "\t\t# txampp health endpoint\n")
	fmt.Fprintf(&b, "\t\trespond /__txampp/health 200\n\n")
	fmt.Fprintf(&b, "\t\t# never serve dotfiles (.stack, .git, ...)\n")
	fmt.Fprintf(&b, "\t\t@dotfiles path /.* /*/.*/**\n")
	fmt.Fprintf(&b, "\t\trespond @dotfiles 404\n")

	if o.Mailpit {
		fmt.Fprintf(&b, "\n\t\t# Mailpit inbox\n")
		fmt.Fprintf(&b, "\t\thandle /__mailpit/* {\n")
		fmt.Fprintf(&b, "\t\t\turi strip_prefix /__mailpit\n")
		fmt.Fprintf(&b, "\t\t\treverse_proxy 127.0.0.1:%d\n", o.MailpitPort)
		fmt.Fprintf(&b, "\t\t}\n")
		fmt.Fprintf(&b, "\t\tredir /__mailpit /__mailpit/ 308\n")
	}

	if o.Adminer && o.PHPEnabled {
		fmt.Fprintf(&b, "\n\t\t# Adminer database GUI\n")
		fmt.Fprintf(&b, "\t\tredir /__adminer /__adminer/adminer.php 308\n")
		fmt.Fprintf(&b, "\t\thandle /__adminer/* {\n")
		fmt.Fprintf(&b, "\t\t\turi strip_prefix /__adminer\n")
		fmt.Fprintf(&b, "\t\t\troot * %s\n", caddyPath(o.WWWDir))
		fmt.Fprintf(&b, "\t\t\tphp_fastcgi %s\n", caddyFastCGI(o.PHPSocket))
		fmt.Fprintf(&b, "\t\t\tfile_server\n")
		fmt.Fprintf(&b, "\t\t}\n")
	}

	fmt.Fprintf(&b, "\n\t\t# project document root\n")
	if o.PHPEnabled {
		fmt.Fprintf(&b, "\t\tphp_fastcgi %s\n", caddyFastCGI(o.PHPSocket))
	}
	fmt.Fprintf(&b, "\t\tfile_server\n")
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "}\n")
	return b.String()
}

// caddyFastCGI renders a php_fastcgi upstream. Caddy uses the "unix/"
// scheme; with an absolute socket path this becomes "unix//path/to.sock".
func caddyFastCGI(socket string) string {
	if strings.HasPrefix(socket, "/") {
		return "unix/" + socket
	}
	return "unix/" + socket
}

// caddyPath quotes a path when it contains spaces.
func caddyPath(p string) string {
	if strings.ContainsAny(p, " \t\"") {
		return "\"" + strings.ReplaceAll(p, "\"", "\\\"") + "\""
	}
	return p
}
