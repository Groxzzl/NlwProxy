package cli

import (
	"encoding/json"
	"os"
	"path/filepath"

	"nlwproxy/internal/config"
)

// ensureHomeConfig creates the NLW Proxy home directory structure and writes a
// default config.json + profile if they don't exist yet. This is called on
// first bare `nlwproxy` invocation so the user gets a working dashboard without
// needing `nlwproxy init` first. Errors are silently ignored — worst case the
// TUI launches with the setup wizard.
func ensureHomeConfig() {
	home := homeDir()
	_ = os.MkdirAll(filepath.Join(home, "profiles"), 0o755)
	_ = os.MkdirAll(filepath.Join(home, "data", "proxies"), 0o755)

	configPath := filepath.Join(home, "config.json")
	if _, err := os.Stat(configPath); err == nil {
		return // already exists
	}

	// Also check if we're in a developer checkout with nlwproxy.json present —
	// if so, copy it to the home dir so the user's existing config is reused.
	if devConfig, err := os.ReadFile("nlwproxy.json"); err == nil {
		_ = os.WriteFile(configPath, devConfig, 0o644)
		// Copy profiles too if present
		if devProfiles, err := os.ReadFile(filepath.Join("profiles", "index.json")); err == nil {
			_ = os.WriteFile(filepath.Join(home, "profiles", "index.json"), devProfiles, 0o644)
		}
		// Copy profile detail files
		entries, _ := os.ReadDir("profiles")
		for _, e := range entries {
			if e.IsDir() || e.Name() == "index.json" || filepath.Ext(e.Name()) == ".example" {
				continue
			}
			data, err := os.ReadFile(filepath.Join("profiles", e.Name()))
			if err == nil {
				_ = os.WriteFile(filepath.Join(home, "profiles", e.Name()), data, 0o644)
			}
		}
		return
	}

	// No developer config — write a minimal default so the TUI can start.
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:8787"
	cfg.Server.LocalTokenEnv = "NLW_PROXY_LOCAL_TOKEN"
	cfg.ProxyOnly = true
	cfg.Routing.Strategy = "round_robin"
	cfg.Upstreams = []config.Upstream{{
		Name:      "MyProvider",
		BaseURL:   "https://opencode.ai/zen/v1",
		APIKeyEnv: "MYPROVIDER_API_KEY",
		Priority:  10,
		Weight:    1,
		Enabled:   true,
	}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err == nil {
		_ = os.WriteFile(configPath, data, 0o644)
	}
}
