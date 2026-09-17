// Command mailpitstub is a Mailpit stand-in for txampp's test suite.
//
// It serves the tiny HTTP surface txampp expects (/, /api/v1/info,
// /api/v1/messages) and runs a real SMTP sink, so the sendmail path can
// be exercised end to end: PHP mail() → txampp sendmail → SMTP → inbox.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type message struct {
	ID   string   `json:"ID"`
	From string   `json:"From"`
	To   []string `json:"To"`
	Data string   `json:"Data"`
	Size int      `json:"Size"`
}

type inbox struct {
	mu  sync.Mutex
	msg []message
	seq int
}

var box = &inbox{}

func main() {
	listen := flag.String("listen", "127.0.0.1:8025", "HTTP listen address")
	smtp := flag.String("smtp", "127.0.0.1:1025", "SMTP listen address")
	flag.Parse()

	go serveSMTP(*smtp)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>mailpitstub</title></head><body><h1>mailpitstub</h1></body></html>`)
	})
	mux.HandleFunc("/api/v1/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"Version":"v1.0-mock","Database":"mock","Messages":0}`)
	})
	mux.HandleFunc("/api/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		box.mu.Lock()
		defer box.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		_ = enc.Encode(map[string]any{"total": len(box.msg), "messages": box.msg})
	})
	fmt.Fprintf(os.Stderr, "mailpitstub http=%s smtp=%s\n", *listen, *smtp)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		fmt.Fprintln(os.Stderr, "mailpitstub:", err)
		os.Exit(1)
	}
}

// serveSMTP implements the minimum SMTP dialog to accept one message.
func serveSMTP(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mailpitstub smtp:", err)
		os.Exit(1)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go smtpSession(conn)
	}
}

func smtpSession(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	write := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
	write("220 mailpitstub ESMTP")
	sc := bufio.NewScanner(conn)

	var from string
	var rcpts []string
	var data strings.Builder
	inData := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if inData {
			if line == "." {
				inData = false
				box.mu.Lock()
				box.seq++
				box.msg = append(box.msg, message{
					ID:   fmt.Sprintf("mock-%d", box.seq),
					From: from,
					To:   rcpts,
					Data: data.String(),
					Size: data.Len(),
				})
				box.mu.Unlock()
				write("250 OK")
				continue
			}
			if strings.HasPrefix(line, ".") {
				line = line[1:]
			}
			data.WriteString(line + "\n")
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			write("250-mailpitstub")
			write("250 OK")
		case strings.HasPrefix(line, "MAIL FROM:"):
			from = strings.TrimPrefix(line, "MAIL FROM:")
			from = strings.Trim(from, "<> ")
			write("250 OK")
		case strings.HasPrefix(line, "RCPT TO:"):
			rcpt := strings.Trim(strings.TrimPrefix(line, "RCPT TO:"), "<> ")
			rcpts = append(rcpts, rcpt)
			write("250 OK")
		case line == "DATA":
			inData = true
			data.Reset()
			write("354 end with .")
		case line == "QUIT":
			write("221 bye")
			return
		case line == "RSET":
			from, rcpts = "", nil
			write("250 OK")
		default:
			write("250 OK")
		}
	}
}
