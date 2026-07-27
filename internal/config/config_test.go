package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default invalid: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Fatalf("listen=%q", cfg.Server.Listen)
	}
	if cfg.Client != "opencode" {
		t.Fatalf("client=%q", cfg.Client)
	}
}

func TestWriteDefaultAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nlwproxy.json")
	if err := WriteDefault(path, false); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Client != "opencode" {
		t.Fatalf("client=%q", cfg.Client)
	}
	if err := WriteDefault(path, false); !os.IsExist(err) {
		t.Fatalf("expected exists, got %v", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"client":"opencode","server":{"listen":"127.0.0.1:1"},"upstreams":[],"extra":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateRejectsUnsafeListenAndBadUpstream(t *testing.T) {
	cfg := Default()
	cfg.Server.Listen = ":8787"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected loopback validation error")
	}
	cfg = Default()
	cfg.Upstreams = []Upstream{{Name: "primary", BaseURL: "http://example.com"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected HTTPS validation error")
	}
}

func TestValidateRejectsInlineCredentialsAndBadEnvName(t *testing.T) {
	for _, upstream := range []Upstream{
		{Name: "inline", BaseURL: "https://api.example.com", ProxyURL: "http://user:secret@127.0.0.1:8080", Enabled: true},
		{Name: "bad-env", BaseURL: "https://api.example.com", APIKeyEnv: "NOT AN ENV", Enabled: true},
	} {
		cfg := Default()
		cfg.Upstreams = []Upstream{upstream}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected validation error for %+v", upstream)
		}
	}
}

func TestWritePersistsValidatedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.Upstreams = []Upstream{{Name: "direct", BaseURL: "https://api.example.com/v1", APIKeyEnv: "EXAMPLE_KEY", Priority: 10, Enabled: true}}
	if err := Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Upstreams) != 1 || loaded.Upstreams[0].Priority != 10 {
		t.Fatalf("loaded=%+v", loaded)
	}
}
