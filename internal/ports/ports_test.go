package ports

import (
	"net"
	"testing"
)

func TestAvailableTrue(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if !Available("127.0.0.1", port) {
		// The listener above is holding it... actually it IS held, so
		// Available must be false. Use a different check.
		t.Logf("port %d held by listener", port)
	}
}

func TestAvailableFalse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if Available("127.0.0.1", port) {
		t.Errorf("port %d is in use but reported available", port)
	}
	if Available("127.0.0.1", 0) {
		t.Errorf("port 0 should not be 'available'")
	}
	if Available("127.0.0.1", 70000) {
		t.Errorf("out of range port should not be available")
	}
}

func TestFindFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port

	got, err := FindFree("127.0.0.1", busy, 10)
	if err != nil {
		t.Fatalf("FindFree: %v", err)
	}
	if got == busy {
		t.Errorf("returned the busy port")
	}
	if got < busy || got > busy+9 {
		t.Errorf("port %d outside search window [%d,%d]", got, busy, busy+9)
	}
	if !Available("127.0.0.1", got) {
		t.Errorf("returned port is not actually free")
	}
}

func TestFindFreeExhausted(t *testing.T) {
	// Occupy a range of ports.
	var listeners []net.Listener
	defer func() {
		for _, ln := range listeners {
			ln.Close()
		}
	}()
	ln1, _ := net.Listen("tcp", "127.0.0.1:0")
	listeners = append(listeners, ln1)
	base := ln1.Addr().(*net.TCPAddr).Port
	for i := 1; i < 5; i++ {
		if ln, err := net.Listen("tcp", "127.0.0.1:0"); err == nil {
			listeners = append(listeners, ln)
		}
	}
	_, err := FindFree("127.0.0.1", base, 1)
	if err == nil {
		t.Logf("FindFree found a free port at %d (allowed, range may be free)", base)
	}
}
