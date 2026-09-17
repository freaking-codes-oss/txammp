package sendmail

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server capturing one conversation.
type fakeSMTP struct {
	addr     string
	greeting []string
	replies  chan string // captured commands
	data     chan string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{addr: ln.Addr().String(), replies: make(chan string, 32), data: make(chan string, 4)}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer ln.Close()
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		write := func(s string) {
			rw.WriteString(s + "\r\n")
			rw.Flush()
		}
		write("220 fake.local ESMTP")
		inData := false
		var data strings.Builder
		for {
			line, err := rw.Reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					f.data <- data.String()
					write("250 OK message accepted")
					continue
				}
				data.WriteString(line + "\n")
				continue
			}
			select {
			case f.replies <- line:
			default:
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				write("250-fake.local")
				write("250 OK")
			case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
				write("250 OK")
			case line == "DATA":
				inData = true
				write("354 go ahead")
			case line == "QUIT":
				write("221 bye")
				return
			default:
				write("250 OK")
			}
		}
	}()
	return f
}

func TestSendmailDelivers(t *testing.T) {
	srv := startFakeSMTP(t)
	msg := "From: Dev <dev@example.test>\r\n" +
		"To: alice@example.test, bob@example.test\r\n" +
		"Cc: carol@example.test\r\n" +
		"Subject: Hello\r\n" +
		"\r\n" +
		"This is the body.\r\n" +
		".starts with a dot\r\n"

	err := Run(strings.NewReader(msg), Options{SMTPAddr: srv.addr, ReadTo: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	expectCmd(t, srv, "EHLO txampp")
	expectCmd(t, srv, "MAIL FROM:<dev@example.test>")
	expectCmd(t, srv, "RCPT TO:<alice@example.test>")
	expectCmd(t, srv, "RCPT TO:<bob@example.test>")
	expectCmd(t, srv, "RCPT TO:<carol@example.test>")
	expectCmd(t, srv, "DATA")

	select {
	case body := <-srv.data:
		if !strings.Contains(body, "Subject: Hello") {
			t.Errorf("body lost headers: %q", body)
		}
		if !strings.Contains(body, "..starts with a dot") {
			t.Errorf("dot not stuffed: %q", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("message body never received")
	}
}

func TestSendmailNoRecipients(t *testing.T) {
	// No To headers and no -t: must silently succeed (sendmail semantics).
	err := Run(strings.NewReader("Subject: x\r\n\r\nbody\r\n"), Options{SMTPAddr: "127.0.0.1:1", ReadTo: false})
	if err != nil {
		t.Errorf("expected silent success, got %v", err)
	}
}

func TestSendmailServerDown(t *testing.T) {
	// Port 1 on loopback: connection refused.
	err := Run(strings.NewReader("To: a@b.c\r\n\r\nx\r\n"), Options{SMTPAddr: "127.0.0.1:1", ReadTo: true, Timeout: 2 * time.Second})
	if err == nil {
		t.Errorf("expected dial error")
	}
}

func TestSendmailLFOnlyInput(t *testing.T) {
	srv := startFakeSMTP(t)
	msg := "From: dev@example.test\nTo: alice@example.test\nSubject: LF\n\nbody line\n"
	if err := Run(strings.NewReader(msg), Options{SMTPAddr: srv.addr, ReadTo: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case body := <-srv.data:
		if !strings.Contains(body, "Subject: LF") {
			t.Errorf("body wrong: %q", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no message")
	}
}

func expectCmd(t *testing.T, srv *fakeSMTP, want string) {
	t.Helper()
	select {
	case got := <-srv.replies:
		if got != want {
			t.Errorf("command %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("command %q never sent", want)
	}
}

func TestAddressParsing(t *testing.T) {
	cases := map[string]string{
		"user@example.com":                  "user@example.com",
		"Display <user@example.com>":        "user@example.com",
		`"Weird, Name" <weird@example.com>`: "weird@example.com",
	}
	for in, want := range cases {
		got := firstAddress(in)
		if got != want {
			t.Errorf("firstAddress(%q) = %q, want %q", in, got, want)
		}
	}
}
