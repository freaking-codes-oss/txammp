package fastcgi

import (
	"bufio"
	"io"
	"log"
	"net"
	"sync"
)

// Handler handles one FastCGI responder request. It returns the raw
// STDOUT payload (for PHP this is the CGI response: "Status: 200\r\n..."
// headers followed by the body) plus an application status code. Handlers
// that need STDERR may write to the returned buffer via the helper struct.
type Handler func(env map[string]string, stdin []byte) (stdout []byte, appStatus int)

// Serve accepts FastCGI responder connections on ln until it is closed.
// It is intentionally minimal: one request per connection, no
// multiplexing — which is all PHP-FPM-style health checks and the mock
// responder need.
func Serve(ln net.Listener, h Handler) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer conn.Close()
			serveConn(conn, h)
		}()
	}
}

func serveConn(conn net.Conn, h Handler) {
	r := bufio.NewReader(conn)
	var (
		env        map[string]string
		stdin      []byte
		begun      bool
		paramsDone bool
		stdinDone  bool
		mu         sync.Mutex
	)
	for {
		rec, err := readRecord(r)
		if err != nil {
			if err != io.EOF && !begun {
				log.Printf("fastcgi: read: %v", err)
			}
			return
		}
		switch rec.typ {
		case TypeBeginRequest:
			begun = true
			// role is in content[0:2]; only responder supported.
			if len(rec.content) >= 2 && (rec.content[0] != 0 || rec.content[1] != RoleResponder) {
				writeEnd(conn, 1, 0, StatusUnknownRole)
				return
			}
		case TypeParams:
			if len(rec.content) == 0 {
				paramsDone = true
			} else {
				p, err := decodeParams(rec.content)
				if err != nil {
					log.Printf("fastcgi: params: %v", err)
					return
				}
				mu.Lock()
				if env == nil {
					env = map[string]string{}
				}
				for k, v := range p {
					env[k] = v
				}
				mu.Unlock()
			}
		case TypeStdin:
			if len(rec.content) == 0 {
				stdinDone = true
			} else {
				stdin = append(stdin, rec.content...)
			}
		case TypeAbortRequest:
			return
		case TypeGetValues:
			// Reply with empty values; not used by txampp.
			_ = writeRecord(conn, TypeGetValuesRes, 0, encodeParams(nil, map[string]string{}))
		}
		if begun && paramsDone && stdinDone {
			break
		}
	}
	out, status := h(env, stdin)
	if len(out) > 0 {
		if err := writeRecord(conn, TypeStdout, 1, out); err != nil {
			return
		}
	}
	// Empty stdout record + end request.
	_ = writeRecord(conn, TypeStdout, 1, nil)
	writeEnd(conn, 1, status, StatusRequestComplete)
}

// writeEnd writes an END_REQUEST record.
func writeEnd(w io.Writer, reqID uint16, appStatus int, protoStatus byte) error {
	content := make([]byte, 8)
	content[0] = byte(appStatus >> 24)
	content[1] = byte(appStatus >> 16)
	content[2] = byte(appStatus >> 8)
	content[3] = byte(appStatus)
	content[4] = protoStatus
	return writeRecord(w, TypeEndRequest, reqID, content)
}
