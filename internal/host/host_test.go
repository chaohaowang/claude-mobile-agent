package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrGenerate_GeneratesOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.toml")

	cfg, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !strings.HasPrefix(cfg.HostID, "host-") {
		t.Fatalf("host_id missing prefix: %q", cfg.HostID)
	}
	if len(cfg.HostID) != len("host-")+12 {
		t.Fatalf("host_id wrong length: %q", cfg.HostID)
	}
	if cfg.RelayURL == "" || cfg.PublicURL == "" {
		t.Fatalf("urls not populated: %+v", cfg)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm: %v", info.Mode().Perm())
	}
}

func TestLoadOrGenerate_ReusesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.toml")

	first, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.HostID != second.HostID {
		t.Fatalf("host_id changed across calls: %q vs %q", first.HostID, second.HostID)
	}
}

func TestLoadOrGenerate_DefaultsForOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.toml")
	if err := os.WriteFile(path, []byte(`host_id = "host-deadbeefcafe"`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadOrGenerate(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostID != "host-deadbeefcafe" {
		t.Fatalf("host_id changed: %q", cfg.HostID)
	}
	if cfg.RelayURL == "" || cfg.PublicURL == "" {
		t.Fatalf("urls not defaulted: %+v", cfg)
	}
}
