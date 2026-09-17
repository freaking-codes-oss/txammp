module github.com/freaking-codes-oss/txammp

go 1.23.0

toolchain go1.23.4

require (
	github.com/charmbracelet/bubbles v0.21.0
	github.com/charmbracelet/bubbletea v1.3.6
	github.com/charmbracelet/lipgloss v1.1.0
)

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/charmbracelet/colorprofile v0.2.3-0.20250311203215-f60798e515dc // indirect
	github.com/charmbracelet/harmonica v0.2.0 // indirect
	github.com/charmbracelet/x/ansi v0.9.3 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.13-0.20250311204145-2c3ea96c31dd // indirect
	github.com/charmbracelet/x/term v0.2.1 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/sync v0.15.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.3.8 // indirect
)

// golang.org/x/* modules are fetched from their official GitHub mirrors
// (github.com/golang/<repo>, which contain the identical sources) instead
// of golang.org. This keeps builds working in environments where
// golang.org is unreachable, and — together with the committed vendor/
// directory — makes builds fully hermetic. The mirrors are byte-for-byte
// the same repositories, so this is equivalent to the canonical imports.
replace golang.org/x/sys => github.com/golang/sys v0.28.0

replace golang.org/x/term => github.com/golang/term v0.27.0

replace golang.org/x/sync => github.com/golang/sync v0.10.0

replace golang.org/x/text => github.com/golang/text v0.21.0

replace golang.org/x/net => github.com/golang/net v0.33.0

replace golang.org/x/exp => github.com/golang/exp v0.0.0-20250106191152-7588d65b2ba8
