package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gronnbeck/hq/internal/client"
)

func TestLoadConfigCreatesTheTemplate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server != defaultServer || cfg.Agent != "" {
		t.Fatalf("got %+v, want the defaults", cfg)
	}
	b, err := os.ReadFile(filepath.Join(dir, "hq", "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml wasn't created: %v", err)
	}
	if !strings.Contains(string(b), "server: "+defaultServer) {
		t.Fatalf("unexpected template:\n%s", b)
	}

	// The template parses to the same defaults.
	again, err := loadConfig()
	if err != nil || again != cfg {
		t.Fatalf("reloaded %+v, %v; want %+v", again, err, cfg)
	}
}

func TestLoadConfigReadsSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "hq"), 0o700)
	os.WriteFile(filepath.Join(dir, "hq", "config.yaml"),
		[]byte("server: http://localhost:8080\nagent: reviewer\n"), 0o600)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server != "http://localhost:8080" || cfg.Agent != "reviewer" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestLoadConfigKeepsTheDefaultServerWhenUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "hq"), 0o700)
	os.WriteFile(filepath.Join(dir, "hq", "config.yaml"), []byte("agent: reviewer\n"), 0o600)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server != defaultServer {
		t.Fatalf("server = %q, want %q", cfg.Server, defaultServer)
	}
}

func TestSessionsOnlyGoToTheirOwnServer(t *testing.T) {
	sess := session{Server: "https://hq.np.lol", Token: "hqs_abc"}

	c := &client.Client{BaseURL: "https://hq.np.lol"}
	if err := useSession(c, sess); err != nil || c.Token != "hqs_abc" {
		t.Fatalf("same server: token %q, err %v", c.Token, err)
	}

	other := &client.Client{BaseURL: "http://localhost:8080"}
	if err := useSession(other, sess); err == nil || other.Token != "" {
		t.Fatalf("other server got token %q, err %v", other.Token, err)
	}
}
