# txampp

**XAMPP-style local web development stack in a single binary — zero install, in any folder.**

`txampp` turns any directory into a PHP dev environment: it downloads
platform-appropriate *static* binaries (no system packages, no daemons),
generates all configuration, supervises the processes, and cleans up after
itself. Everything lives in a project-local `.stack/` directory that is
already git-ignored — delete it and nothing remains on your machine.

## What's in the stack

| Component   | Source                                       | Role                                    |
| ----------- | -------------------------------------------- | --------------------------------------- |
| PHP (FPM)   | [static-php.dev](https://static-php.dev) bulk builds | PHP runtime, served over FastCGI |
| Caddy       | GitHub Releases                              | Web server + automatic HTTPS-ish localhost serving |
| Adminer     | GitHub Releases                              | Single-file database GUI (SQLite)       |
| Mailpit     | GitHub Releases                              | Local mail catcher with a web UI        |

Caddy proxies PHP requests to php-fpm over a unix socket in `.stack/run/`.
PHP's `mail()` is routed through txampp's sendmail bridge into Mailpit, so
outgoing mail from your app shows up in the Mailpit UI instead of the void.

## Quick start

```console
$ cd my-project
$ txampp init --php 8.4 --yes     # creates .stack/, resolves versions
$ txampp up                       # downloads (first run) and starts services
$ txampp open                     # opens the site in your browser
$ txampp                          # interactive TUI dashboard
```

Drop a `index.php` in the project root (or `public/`) and you're serving PHP
on http://localhost:8080.

## Commands

| Command                          | Description                                          |
| -------------------------------- | ---------------------------------------------------- |
| `txampp`                         | interactive dashboard (or first-run wizard)          |
| `txampp init [--php 8.4] [--yes]` | initialize `.stack/` in the current directory      |
| `txampp up` / `down`             | start / stop all services                            |
| `txampp restart [service]`       | restart all services or one                          |
| `txampp status [--json]`         | service status                                       |
| `txampp logs [-f] [-s source]`   | view / follow unified logs                           |
| `txampp open [web\|adminer\|mailpit]` | open a URL in the browser                       |
| `txampp configs [--print name]`  | show generated config paths / contents               |
| `txampp doctor`                  | health-check the environment                         |
| `txampp manifest [--discover]`   | show the active component manifest                   |
| `txampp sendmail`                | sendmail-to-SMTP bridge used by PHP `mail()`         |
| `txampp php -v` (etc.)           | run a bundled tool directly                          |

Global flags: `--dir PATH`, `--manifest PATH`, `--mirror URL` (or
`TXAMPP_MIRROR`), `--json`, `--no-color`.

## Manifest & versions

Component downloads are driven by a JSON manifest. Resolution order:

1. `--manifest PATH` — explicit file
2. `--mirror URL` — `$mirror/manifest.json` over HTTP
3. `TXAMPP_MANIFEST_URL`
4. a cached remote manifest (24h TTL)
5. the manifest embedded in the binary

PHP versions are *discovered* live from the static-php.dev bulk listing, so
`txampp init` offers the newest patch release of each branch (8.4, 8.5, …)
rather than whatever was pinned when your binary was built. The embedded
manifest acts as an offline fallback.

## Development

```console
$ go build -o txampp .     # vendor/ is committed: hermetic, no network needed
$ go test ./...
```

- `go.mod` replaces `golang.org/x/*` with their identical GitHub mirrors so
  builds work where golang.org is unreachable; sources are vendored either way.
- `hack/mockphp` — a PHP/php-fpm impostor speaking real FastCGI, for tests.
- `hack/mailpitstub` — Mailpit impostor: HTTP info endpoints + a working SMTP sink.

### Package map

| Package            | Responsibility                                          |
| ------------------ | ------------------------------------------------------- |
| `internal/cli`     | command dispatch, flags, output styling                 |
| `internal/tui`     | bubbletea dashboard                                     |
| `internal/app`     | session glue (manifest loading, init, open)             |
| `internal/stack`   | plan/ensure artifacts, start/stop, ports, wiring        |
| `internal/services`| per-service definitions (caddy, php-fpm, mailpit)       |
| `internal/proc`    | process supervision with PID-file reuse detection       |
| `internal/configgen`| Caddyfile / php-fpm.conf / php.ini generation          |
| `internal/fastcgi` | minimal FastCGI client+server (health checks, mocks)    |
| `internal/fetch`   | downloads with progress, checksums, tar.gz/zip extract  |
| `internal/manifest`| manifest model + static-php.dev discovery              |
| `internal/store`   | global cache (~/.cache/txampp), lock files, records     |
| `internal/logs`    | unified log bus with rotation-aware tailers             |
| `internal/sendmail`| sendmail → SMTP bridge                                  |
| `internal/project` | `.stack/` layout + state.json                           |
| `internal/ports`   | free-port discovery                                     |
| `internal/platform`| OS/arch keys (linux-amd64, …)                           |
