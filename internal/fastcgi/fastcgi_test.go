package fastcgi

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// echoHandler renders a tiny CGI response from the request env.
func echoHandler(env map[string]string, stdin []byte) ([]byte, int) {
	var b strings.Builder
	b.WriteString("Status: 200 OK\r\n")
	b.WriteString("X-Powered-By: mock\r\n\r\n")
	b.WriteString("script=" + env["SCRIPT_FILENAME"] + "\n")
	if len(stdin) > 0 {
		b.WriteString("stdin=" + string(stdin) + "\n")
	}
	return []byte(b.String()), 0
}

func startServer(t *testing.T) (net.Addr, func()) {
	t.Helper()
	ln, err := net.Listen("unix", t.TempDir()+"/fcgi.sock")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = Serve(ln, echoHandler) }()
	return ln.Addr(), func() { ln.Close() }
}

func TestRequestResponseRoundTrip(t *testing.T) {
	addr, stop := startServer(t)
	defer stop()

	resp, err := Request(context.Background(), "unix", addr.String(), map[string]string{
		"REQUEST_METHOD":  "POST",
		"SCRIPT_FILENAME": "/var/www/index.php",
	}, []byte("hello=world"), 5*time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if resp.AppStatus != 0 {
		t.Errorf("app status %d", resp.AppStatus)
	}
	if !strings.Contains(resp.Stdout, "Status: 200 OK") {
		t.Errorf("missing CGI status: %q", resp.Stdout)
	}
	if !strings.Contains(resp.Stdout, "script=/var/www/index.php") {
		t.Errorf("env not delivered: %q", resp.Stdout)
	}
	if !strings.Contains(resp.Stdout, "stdin=hello=world") {
		t.Errorf("stdin not delivered: %q", resp.Stdout)
	}
}

func TestRequestPingStyle(t *testing.T) {
	pingHandler := func(env map[string]string, stdin []byte) ([]byte, int) {
		if env["SCRIPT_NAME"] == "/__fpm-ping" {
			return []byte("pong"), 0
		}
		return echoHandler(env, stdin)
	}
	ln, err := net.Listen("unix", t.TempDir()+"/ping.sock")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = Serve(ln, pingHandler) }()
	defer ln.Close()

	resp, err := Request(context.Background(), "unix", ln.Addr().String(), map[string]string{
		"REQUEST_METHOD":  "GET",
		"REQUEST_URI":     "/__fpm-ping",
		"SCRIPT_NAME":     "/__fpm-ping",
		"SCRIPT_FILENAME": "/__fpm-ping",
	}, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if strings.TrimSpace(resp.Stdout) != "pong" {
		t.Errorf("want pong, got %q", resp.Stdout)
	}
}

func TestPingHelper(t *testing.T) {
	ln, err := net.Listen("unix", t.TempDir()+"/ping2.sock")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = Serve(ln, func(env map[string]string, stdin []byte) ([]byte, int) {
			return []byte("pong"), 0
		})
	}()
	defer ln.Close()

	if err := Ping(context.Background(), ln.Addr().String(), 3*time.Second); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestPingRefused(t *testing.T) {
	sock := t.TempDir() + "/missing.sock"
	if err := Ping(context.Background(), sock, 2*time.Second); err == nil {
		t.Errorf("Ping should fail on missing socket")
	}
}

func TestParamsEncodeDecode(t *testing.T) {
	params := map[string]string{
		"SCRIPT_FILENAME": "/some/path.php",
		"REQUEST_METHOD":  "GET",
		"LONG":            strings.Repeat("x", 300),
	}
	encoded := encodeParams(nil, params)
	decoded, err := decodeParams(encoded)
	if err != nil {
		t.Fatalf("decodeParams: %v", err)
	}
	if len(decoded) != len(params) {
		t.Fatalf("round trip lost entries: %d != %d", len(decoded), len(params))
	}
	for k, v := range params {
		if decoded[k] != v {
			t.Errorf("%s mismatch", k)
		}
	}
}

func TestLargeStdoutChunking(t *testing.T) {
	big := strings.Repeat("A", 200000) // > maxContent: requires chunked records
	ln, err := net.Listen("unix", t.TempDir()+"/big.sock")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = Serve(ln, func(env map[string]string, stdin []byte) ([]byte, int) {
			return []byte(big), 0
		})
	}()
	defer ln.Close()

	resp, err := Request(context.Background(), "unix", ln.Addr().String(),
		map[string]string{"REQUEST_METHOD": "GET"}, nil, 10*time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len(resp.Stdout) != len(big) {
		t.Errorf("stdout truncated: %d != %d", len(resp.Stdout), len(big))
	}
}
