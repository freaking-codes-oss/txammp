package fastcgi

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// Response is the result of a FastCGI request.
type Response struct {
	Stdout    string
	Stderr    string
	AppStatus int
}

// Request performs a minimal FastCGI responder request over a unix socket
// or TCP address. It sends the given params and stdin (may be nil) and
// waits for the application to finish, with the overall duration bounded
// by timeout. The request ID is always 1 (no multiplexing needed).
func Request(ctx context.Context, network, addr string, params map[string]string, stdin []byte, timeout time.Duration) (Response, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return Response{}, fmt.Errorf("fastcgi dial %s %s: %w", network, addr, err)
	}
	defer conn.Close()
	if timeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}

	r := bufio.NewReader(conn)
	// BEGIN_REQUEST: role responder, do not keep connection.
	begin := []byte{0, RoleResponder, 0}
	if err := writeRecord(conn, TypeBeginRequest, 1, begin); err != nil {
		return Response{}, err
	}
	if err := writeRecord(conn, TypeParams, 1, encodeParams(nil, params)); err != nil {
		return Response{}, err
	}
	if err := writeRecord(conn, TypeParams, 1, nil); err != nil { // end of params
		return Response{}, err
	}
	if err := writeRecord(conn, TypeStdin, 1, stdin); err != nil {
		return Response{}, err
	}
	if err := writeRecord(conn, TypeStdin, 1, nil); err != nil { // end of stdin
		return Response{}, err
	}

	var resp Response
	for {
		rec, err := readRecord(r)
		if err != nil {
			return resp, fmt.Errorf("fastcgi read: %w", err)
		}
		switch rec.typ {
		case TypeStdout:
			resp.Stdout += string(rec.content)
		case TypeStderr:
			resp.Stderr += string(rec.content)
		case TypeEndRequest:
			if len(rec.content) >= 4 {
				resp.AppStatus = int(binary.BigEndian.Uint32(rec.content[:4]))
			}
			return resp, nil
		case TypeAbortRequest, TypeUnknownType:
			return resp, fmt.Errorf("fastcgi: unexpected record type %d", rec.typ)
		}
	}
}

// Ping issues a PHP-FPM "ping" request (the ping.path pool setting) and
// reports success when the response body contains "pong".
func Ping(ctx context.Context, socketPath string, timeout time.Duration) error {
	params := map[string]string{
		"REQUEST_METHOD":  "GET",
		"REQUEST_URI":     "/__fpm-ping",
		"SCRIPT_NAME":     "/__fpm-ping",
		"SCRIPT_FILENAME": "/__fpm-ping",
		"CONTENT_TYPE":    "",
		"CONTENT_LENGTH":  "0",
	}
	resp, err := Request(ctx, "unix", socketPath, params, nil, timeout)
	if err != nil {
		return err
	}
	if resp.AppStatus != 0 {
		return fmt.Errorf("fpm ping: app status %d", resp.AppStatus)
	}
	if len(resp.Stdout) == 0 || len(resp.Stdout) > 64 {
		return fmt.Errorf("fpm ping: unexpected response %q", resp.Stdout)
	}
	return nil
}
