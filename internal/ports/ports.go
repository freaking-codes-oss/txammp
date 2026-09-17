// Package ports picks free TCP ports for the local stack.
package ports

import (
	"fmt"
	"net"
)

// Defaults used by the stack.
const (
	DefaultWebPort         = 8080
	DefaultMailpitWebPort  = 8025
	DefaultMailpitSMTPPort = 1025
)

// Available reports whether host:port can be bound right now.
func Available(host string, port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// FindFree returns the first free port starting at preferred (inclusive),
// trying at most tries candidates. It returns an error when none are free.
// Note the usual TOCTOU caveat: the port is probed, not reserved.
func FindFree(host string, preferred, tries int) (int, error) {
	if tries <= 0 {
		tries = 1
	}
	for i := 0; i < tries; i++ {
		p := preferred + i
		if p > 65535 {
			break
		}
		if Available(host, p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port found starting at %d (tried %d)", preferred, tries)
}
