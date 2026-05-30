package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigUsesInlinePassword(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	body := []byte(`
unifi:
  base_url: "https://unifi.example.test"
  username: "iplist"
  password: "dummy-password"
sources:
  - name: "test"
    url: "https://iplist.example.test/export"
    type: "ipv4_cidr"
    network_id: "wan-id"
`)
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UniFi.Password != "dummy-password" {
		t.Fatalf("password = %q", cfg.UniFi.Password)
	}
	if cfg.UniFi.Site != "default" {
		t.Fatalf("site default = %q", cfg.UniFi.Site)
	}
	if cfg.Safety.MaxEntries != 20000 {
		t.Fatalf("max entries default = %d", cfg.Safety.MaxEntries)
	}
}
