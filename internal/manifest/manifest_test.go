package manifest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultManifestParses(t *testing.T) {
	m, err := Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
	for _, id := range []string{CompCaddy, CompPHP, CompAdminer, CompMailpit} {
		if _, err := m.Component(id); err != nil {
			t.Errorf("missing component %s: %v", id, err)
		}
	}
}

func TestResolveCaddy(t *testing.T) {
	m, _ := Default()
	arts, err := m.Resolve(CompCaddy, "linux-amd64")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("want 1 artifact, got %d", len(arts))
	}
	if arts[0].File.Format != "tar.gz" || len(arts[0].File.Bins) != 1 {
		t.Errorf("unexpected artifact: %+v", arts[0])
	}
	if arts[0].Version != m.Components[CompCaddy].Version {
		t.Errorf("version mismatch")
	}
}

func TestResolveWildcardPlatform(t *testing.T) {
	m, _ := Default()
	// adminer is served for any platform through the "*" key
	arts, err := m.Resolve(CompAdminer, "plan9-arm")
	if err != nil {
		t.Fatalf("Resolve adminer: %v", err)
	}
	if arts[0].File.Format != "file" {
		t.Errorf("adminer should be a single file, got %s", arts[0].File.Format)
	}
}

func TestResolvePHP(t *testing.T) {
	m, _ := Default()
	arts, err := m.ResolvePHP("8.4", "linux-amd64", nil)
	if err != nil {
		t.Fatalf("ResolvePHP: %v", err)
	}
	if len(arts) != 2 {
		t.Fatalf("want fpm+cli artifacts, got %d", len(arts))
	}
	ids := map[string]bool{}
	for _, a := range arts {
		ids[a.File.ID] = true
	}
	if !ids["fpm"] || !ids["cli"] {
		t.Errorf("want fpm and cli, got %v", ids)
	}
}

func TestResolvePHPMissingPlatform(t *testing.T) {
	m, _ := Default()
	if _, err := m.ResolvePHP("8.4", "windows-amd64", nil); err == nil {
		t.Errorf("expected error for unsupported platform")
	}
}

func TestPHPBranches(t *testing.T) {
	m, _ := Default()
	branches, err := m.PHPBranches("linux-amd64", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) < 1 {
		t.Fatalf("want at least one branch")
	}
	// newest first
	if versionLess(branches[0], branches[len(branches)-1]) {
		t.Errorf("branches not sorted newest-first: %v", branches)
	}
}

func TestPHPVersionDiscoveredOverrides(t *testing.T) {
	m, _ := Default()
	discovered := map[string]string{"8.4": "8.4.99"}
	arts, err := m.ResolvePHP("8.4", "linux-amd64", discovered)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Version != "8.4.99" {
			t.Errorf("want discovered version 8.4.99, got %s", a.Version)
		}
		if want := "php-8.4.99"; len(a.File.URL) > 0 && !contains(a.File.URL, want) {
			t.Errorf("url %s should contain %s", a.File.URL, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDiscoverPHP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/listing" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[
				{"name": "php-8.4.14-fpm-linux-x86_64.tar.gz", "is_dir": false},
				{"name": "php-8.4.14-cli-linux-x86_64.tar.gz", "is_dir": false},
				{"name": "php-8.5.2-fpm-linux-x86_64.tar.gz", "is_dir": false},
				{"name": "php-8.5.2-cli-linux-x86_64.tar.gz", "is_dir": false},
				{"name": "php-8.5.1-fpm-linux-x86_64.tar.gz", "is_dir": false},
				{"name": "php-8.5.1-cli-linux-x86_64.tar.gz", "is_dir": false},
				{"name": "php-8.3.77-fpm-linux-x86_64.tar.gz", "is_dir": false},
				{"name": "README.md", "is_dir": false},
				{"name": "somedir", "is_dir": true}
			]`))
			return
		}
		// serve the "archives" themselves
		w.Write([]byte("tar-bytes"))
	}))
	defer srv.Close()

	m, _ := Default()
	m.Components[CompPHP].Discovery.ListURL = srv.URL + "/listing?format=json"
	m.Components[CompPHP].Versions = nil // force discovery-only

	branches, err := m.DiscoverPHP(context.Background(), srv.Client(), "linux-amd64")
	if err != nil {
		t.Fatalf("DiscoverPHP: %v", err)
	}
	if branches["8.4"] != "8.4.14" {
		t.Errorf("8.4 -> %s, want 8.4.14", branches["8.4"])
	}
	if branches["8.5"] != "8.5.2" {
		t.Errorf("8.5 -> %s, want 8.5.2", branches["8.5"])
	}
	// 8.3.77 has no cli build: excluded
	if _, ok := branches["8.3"]; ok {
		t.Errorf("8.3 should be excluded (no cli build)")
	}

	// Resolving through discovery builds proper URLs.
	arts, err := m.ResolvePHP("8.4", "linux-amd64", branches)
	if err != nil {
		t.Fatalf("ResolvePHP(discovered): %v", err)
	}
	found := map[string]string{}
	for _, a := range arts {
		found[a.File.ID] = a.File.URL
	}
	if want := srv.URL + "/listing/php-8.4.14-fpm-linux-x86_64.tar.gz"; found["fpm"] != want {
		t.Errorf("fpm url: %s, want %s", found["fpm"], want)
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"8.4.9", "8.4.14", true},
		{"8.4.14", "8.4.9", false},
		{"8.4.14", "8.10.0", true},
		{"8.4.14", "8.4.14", false},
		{"8.4.14RC1", "8.4.14", true},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%s,%s) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}

func TestParseRejectsBadSchema(t *testing.T) {
	if _, err := Parse([]byte(`{"schema": 99, "components": {}}`)); err == nil {
		t.Errorf("expected schema error")
	}
	if _, err := Parse([]byte(`{`)); err == nil {
		t.Errorf("expected parse error")
	}
}
