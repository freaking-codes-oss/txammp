package project

import (
	"os"
	"path/filepath"
	"testing"
)

func testState() State {
	return State{
		PHPBranch: "8.4",
		Database:  "sqlite",
		Adminer:   true,
		Mailpit:   true,
		Ports:     map[string]int{PortWeb: 8080, PortMailpitWeb: 8025, PortMailpitSMTP: 1025},
	}
}

func TestInitCreatesLayout(t *testing.T) {
	root := t.TempDir()
	p, err := Init(root, testState())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, d := range []string{p.BinDir(), p.RunDir(), p.PidDir(), p.LogDir(), p.DataDir(), p.ConfigDir(), p.WWWDir(), p.SessionsDir()} {
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			t.Errorf("missing dir %s (%v)", d, err)
		}
	}
	// .stack/.gitignore hides everything from parent repos
	gi, err := os.ReadFile(filepath.Join(p.StackDir(), ".gitignore"))
	if err != nil || string(gi) != "*\n" {
		t.Errorf("inner gitignore wrong: %q %v", gi, err)
	}
}

func TestInitRefusesDouble(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, testState()); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, testState()); err == nil {
		t.Errorf("double init must fail")
	}
}

func TestOpenAndStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	p, _ := Init(root, testState())

	// State defaults filled in.
	if p.State.Schema != stateSchema {
		t.Errorf("schema not set")
	}

	p2, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if p2.State.PHPBranch != "8.4" || !p2.State.Adminer || p2.State.Ports[PortWeb] != 8080 {
		t.Errorf("state round trip mismatch: %+v", p2.State)
	}

	// SaveState / reload.
	p2.State.Ports[PortWeb] = 9999
	if err := p2.SaveState(); err != nil {
		t.Fatal(err)
	}
	p3, _ := Open(root)
	if p3.State.Ports[PortWeb] != 9999 {
		t.Errorf("SaveState did not persist")
	}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()
	if Detect(root) {
		t.Errorf("empty dir detected as project")
	}
	Init(root, testState())
	if !Detect(root) {
		t.Errorf("initialized dir not detected")
	}
}

func TestLinkBinSymlink(t *testing.T) {
	root := t.TempDir()
	p, _ := Init(root, testState())

	target := filepath.Join(root, "realbin")
	os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755)

	if err := p.LinkBin("caddy", target); err != nil {
		t.Fatalf("LinkBin: %v", err)
	}
	link := p.BinPath("caddy")
	resolved, err := os.Readlink(link)
	if err == nil && resolved != target {
		// Symlink created but pointing elsewhere?
		t.Errorf("symlink -> %s, want %s", resolved, target)
	}
	// Either way the path must be executable/usable.
	if _, err := os.Stat(link); err != nil {
		t.Errorf("bin path unusable: %v", err)
	}
}

func TestOpenUninitialized(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Errorf("Open should fail on uninitialized dir")
	}
}

func TestOpenMissingDir(t *testing.T) {
	if _, err := Open("/nonexistent-xyz"); err == nil {
		t.Errorf("Open should fail")
	}
}
