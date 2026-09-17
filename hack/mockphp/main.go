// Command mockphp is a PHP-FPM/PHP stand-in for txampp's test suite.
//
// It speaks enough of the php/php-fpm CLI to satisfy txampp's service
// orchestration and enough FastCGI to serve requests through Caddy:
//
//	mockphp -v                          → prints a PHP-style version banner
//	mockphp script.php args...          → "executes" a file (prints a CGI response to stdout)
//	mockphp --nodaemonize --fpm-config F → serves FastCGI on the socket
//	                                       configured in F (listen = ..., ping.path = ...)
package main

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/freaking-codes-oss/txammp/internal/fastcgi"
)

const versionBanner = "PHP 8.4.14 (fpm-fcgi) (built: Sep 17 2026 00:00:00) (mock)\nCopyright (c) The PHP Group\n"

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		usage()
	}

	// php-fpm mode
	fpmConf := ""
	nodaemon := false
	plain := []string{}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--nodaemonize" || args[i] == "-F":
			nodaemon = true
		case args[i] == "--fpm-config" || args[i] == "-y":
			if i+1 < len(args) {
				fpmConf = args[i+1]
				i++
			}
		case args[i] == "-v" || args[i] == "--version":
			fmt.Print(versionBanner)
			return
		case strings.HasPrefix(args[i], "--test") || args[i] == "-t":
			fmt.Println("mockphp: configuration file test succeeded")
			return
		default:
			plain = append(plain, args[i])
		}
	}

	if fpmConf != "" {
		if !nodaemon {
			// mimic fpm daemonizing: just exit happily
			return
		}
		serveFPM(fpmConf)
		return
	}

	if len(plain) > 0 {
		// CLI mode: "run" a script.
		runScript(plain[0], plain[1:])
		return
	}
	usage()
}

func usage() {
	fmt.Fprintln(os.Stderr, "mockphp — test double for php/php-fpm")
	os.Exit(2)
}

// serveFPM runs a FastCGI responder as configured by a php-fpm.conf.
func serveFPM(confPath string) {
	conf, err := os.ReadFile(confPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mockphp: cannot read %s: %v\n", confPath, err)
		os.Exit(1)
	}
	listen := firstMatch(conf, `(?m)^listen\s*=\s*(.+)$`)
	pingPath := firstMatch(conf, `(?m)^ping\.path\s*=\s*(.+)$`)
	statusPath := firstMatch(conf, `(?m)^pm\.status_path\s*=\s*(.+)$`)
	if listen == "" {
		fmt.Fprintln(os.Stderr, "mockphp: no listen directive")
		os.Exit(1)
	}
	if pingPath == "" {
		pingPath = "/ping"
	}

	var ln net.Listener
	if strings.HasPrefix(listen, "/") {
		_ = os.Remove(listen)
		ln, err = net.Listen("unix", listen)
	} else {
		ln, err = net.Listen("tcp", listen)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "mockphp: listen %s: %v\n", listen, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[mockphp] ready on %s (ping=%s status=%s)\n", listen, pingPath, statusPath)

	err = fastcgi.Serve(ln, func(env map[string]string, stdin []byte) ([]byte, int) {
		script := env["SCRIPT_NAME"]
		switch script {
		case pingPath:
			return []byte("pong"), 0
		case statusPath:
			return []byte("pool: www\nprocesses: 1\n"), 0
		}
		return handleRequest(env, stdin)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mockphp: serve: %v\n", err)
		os.Exit(1)
	}
}

var reInfoName = regexp.MustCompile(`(^|/)info\.php$`)

// handleRequest renders a CGI response for a "PHP" request.
func handleRequest(env map[string]string, stdin []byte) ([]byte, int) {
	file := env["SCRIPT_FILENAME"]
	var body strings.Builder

	fmt.Fprintf(&body, "<!doctype html><html><head><title>mockphp</title></head><body>\n")
	fmt.Fprintf(&body, "<h1>mockphp served this page</h1>\n")
	fmt.Fprintf(&body, "<p>script: <code>%s</code></p>\n", file)
	if reInfoName.MatchString(file) {
		body.WriteString("<table class=\"phpinfo\">\n")
		keys := []string{"REQUEST_METHOD", "REQUEST_URI", "QUERY_STRING", "CONTENT_TYPE", "SERVER_SOFTWARE", "PHP_VERSION"}
		for _, k := range keys {
			fmt.Fprintf(&body, "<tr><td>%s</td><td>%s</td></tr>\n", k, env[k])
		}
		body.WriteString("</table>\n")
	}
	if len(stdin) > 0 {
		fmt.Fprintf(&body, "<pre class=\"stdin\">%s</pre>\n", string(stdin))
	}
	body.WriteString("</body></html>\n")

	var out strings.Builder
	out.WriteString("Status: 200 OK\r\n")
	out.WriteString("X-Powered-By: PHP/8.4.14 (mock)\r\n")
	out.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	fmt.Fprintf(&out, "Content-Length: %d\r\n", body.Len())
	out.WriteString("\r\n")
	out.WriteString(body.String())
	return []byte(out.String()), 0
}

// runScript "executes" a PHP file: prints headers + the file content.
func runScript(path string, args []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Status: 404 Not Found\r\nX-Powered-By: PHP/8.4.14 (mock)\r\n\r\nmockphp: %v\n", err)
		os.Exit(255)
	}
	fmt.Printf("X-Powered-By: PHP/8.4.14 (mock)\nmockphp executed %s (%d bytes)\nargv: %v\n", path, len(data), args)
}

func firstMatch(data []byte, pattern string) string {
	re := regexp.MustCompile(pattern)
	if m := re.FindSubmatch(data); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}
