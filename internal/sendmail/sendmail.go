// Package sendmail implements the sendmail-to-SMTP bridge that lets PHP's
// mail() function deliver into the local Mailpit instance.
//
// PHP invokes the sendmail_path binary as:
//
//	sendmail -t -i
//
// where -t means "read recipients from the message headers".
package sendmail

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"
)

// Options controls one sendmail invocation.
type Options struct {
	// SMTPAddr is the "host:port" of the local SMTP server (Mailpit).
	SMTPAddr string
	// ReadTo takes recipients from To/Cc/Bcc headers (the -t flag).
	ReadTo bool
	// Sender overrides the envelope sender (the -f flag or the From
	// header).
	Sender string
	// Timeout bounds the whole SMTP conversation.
	Timeout time.Duration
}

// Run reads an RFC 5322 message from r and delivers it via SMTP. It is
// intentionally forgiving: sendmail exit codes are simplified to error vs
// nil, and missing recipients result in a silent no-op (mail() should not
// fatal the PHP request).
func Run(r io.Reader, opts Options) error {
	if opts.SMTPAddr == "" {
		return fmt.Errorf("sendmail: no smtp address configured")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	msg, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("sendmail: read message: %w", err)
	}
	header, _ := splitMessage(msg)

	sender := opts.Sender
	if sender == "" {
		sender = firstAddress(header.Get("From"))
	}
	if sender == "" {
		sender = "txampp@localhost"
	}

	var recipients []string
	if opts.ReadTo {
		for _, h := range []string{"To", "Cc", "Bcc"} {
			recipients = append(recipients, addresses(header.Values(h))...)
		}
	} else {
		recipients = addresses(header.Values("To"))
	}
	recipients = compact(recipients)
	if len(recipients) == 0 {
		// Nothing to deliver: behave like a sendmail that swallowed the
		// message (postfix logs and exits 0 here).
		return nil
	}

	conn, err := net.DialTimeout("tcp", opts.SMTPAddr, timeout)
	if err != nil {
		return fmt.Errorf("sendmail: dial %s: %w", opts.SMTPAddr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	client := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if code, _, err := smtpRead(client); err != nil || code != 220 {
		return smtpErr("greeting", code, err)
	}
	if err := smtpWrite(client.Writer, "EHLO txampp"); err != nil {
		return err
	}
	// Read the (possibly multi-line) EHLO response.
	if code, _, err := smtpReadResponse(client); err != nil || code != 250 {
		return smtpErr("EHLO", code, err)
	}
	if err := smtpWrite(client.Writer, "MAIL FROM:<%s>", sender); err != nil {
		return err
	}
	if code, _, err := smtpRead(client); err != nil || code != 250 {
		return smtpErr("MAIL FROM", code, err)
	}
	for _, rcpt := range recipients {
		if err := smtpWrite(client.Writer, "RCPT TO:<%s>", rcpt); err != nil {
			return err
		}
		if code, _, err := smtpRead(client); err != nil || code == 0 || code >= 500 {
			return smtpErr("RCPT TO", code, err)
		}
	}
	if err := smtpWrite(client.Writer, "DATA"); err != nil {
		return err
	}
	if code, _, err := smtpRead(client); err != nil || code != 354 {
		return smtpErr("DATA", code, err)
	}
	if err := smtpWrite(client.Writer, "%s", dotStuff(normalizeCRLF(msg))); err != nil {
		return err
	}
	if err := smtpWrite(client.Writer, "."); err != nil {
		return err
	}
	if code, _, err := smtpRead(client); err != nil || code != 250 {
		return smtpErr("message body", code, err)
	}
	_ = smtpWrite(client.Writer, "QUIT")
	_, _, _ = smtpRead(client)
	return nil
}

// splitMessage splits an RFC 5322 message into header block and body.
func splitMessage(msg []byte) (textHeaderMap, []byte) {
	normalized := normalizeCRLF(msg)
	sep := []byte("\r\n\r\n")
	idx := bytes.Index(normalized, sep)
	if idx < 0 {
		// Tolerate header-only messages.
		return parseHeaders(normalized), nil
	}
	return parseHeaders(normalized[:idx]), normalized[idx+len(sep):]
}

// parseHeaders parses a header block into a case-insensitive multimap.
func parseHeaders(block []byte) textHeaderMap {
	m := textHeaderMap{vals: map[string][]string{}}
	for _, line := range strings.Split(string(block), "\r\n") {
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue // folded continuation: ignore
		}
		if i := strings.IndexByte(line, ':'); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			m.vals[strings.ToLower(k)] = append(m.vals[strings.ToLower(k)], v)
		}
	}
	return m
}

type textHeaderMap struct{ vals map[string][]string }

func (m textHeaderMap) Get(k string) string {
	vs := m.vals[strings.ToLower(k)]
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}

func (m textHeaderMap) Values(k string) []string { return m.vals[strings.ToLower(k)] }

// firstAddress extracts the first email address from a header value like
// `Display Name <user@example.com>` or `user@example.com`.
func firstAddress(v string) string {
	addrs := addresses([]string{v})
	if len(addrs) == 0 {
		return ""
	}
	return addrs[0]
}

// angleAddrRe matches <addr> forms; quoted display names may contain
// commas, so comma-splitting alone is not safe.
var angleAddrRe = regexp.MustCompile(`<([^<>]+)>`)

// addresses extracts angle-bracket or bare addresses from header values.
// When any <addr> form is present, only those are used; otherwise the
// value is split on commas (bare address lists).
func addresses(values []string) []string {
	var out []string
	for _, v := range values {
		if strings.Contains(v, "<") {
			for _, m := range angleAddrRe.FindAllStringSubmatch(v, -1) {
				out = append(out, strings.TrimSpace(m[1]))
			}
			continue
		}
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func compact(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// normalizeCRLF ensures \r\n line endings and a trailing newline.
func normalizeCRLF(msg []byte) []byte {
	s := strings.ReplaceAll(string(msg), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	if !strings.HasSuffix(s, "\r\n") {
		s += "\r\n"
	}
	return []byte(s)
}

// dotStuff prefixes any line starting with a dot (per RFC 5321 DATA).
func dotStuff(msg []byte) string {
	lines := strings.Split(strings.TrimSuffix(string(msg), "\r\n"), "\r\n")
	for i, l := range lines {
		if strings.HasPrefix(l, ".") {
			lines[i] = "." + l
		}
	}
	return strings.Join(lines, "\r\n")
}

// ---- minimal SMTP client helpers -------------------------------------------

func smtpWrite(w *bufio.Writer, format string, args ...any) error {
	if _, err := fmt.Fprintf(w, format+"\r\n", args...); err != nil {
		return fmt.Errorf("sendmail: write: %w", err)
	}
	return w.Flush()
}

// smtpRead reads one (single-line) SMTP reply.
func smtpRead(rw *bufio.ReadWriter) (int, string, error) {
	code, line, err := readReplyLine(rw.Reader)
	return code, line, err
}

// smtpReadResponse reads a possibly multi-line reply (used after EHLO).
func smtpReadResponse(rw *bufio.ReadWriter) (int, string, error) {
	var lastCode int
	var lastLine string
	for {
		code, line, err := readReplyLine(rw.Reader)
		if err != nil {
			return lastCode, lastLine, err
		}
		lastCode, lastLine = code, line
		if len(line) < 4 || line[3] != '-' {
			return code, line, nil
		}
	}
}

func readReplyLine(r *bufio.Reader) (int, string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, "", err
	}
	line = strings.TrimRight(line, "\r\n")
	code := 0
	if len(line) >= 3 {
		for _, c := range line[:3] {
			if c < '0' || c > '9' {
				return 0, line, nil
			}
			code = code*10 + int(c-'0')
		}
	}
	return code, line, nil
}

func smtpErr(stage string, code int, err error) error {
	if err != nil {
		return fmt.Errorf("sendmail: %s: %w", stage, err)
	}
	return fmt.Errorf("sendmail: %s: SMTP error %d", stage, code)
}
