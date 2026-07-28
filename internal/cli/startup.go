package cli

import (
	"os"

	"nlwproxy/internal/config"
	"nlwproxy/internal/console"
	"nlwproxy/internal/profiles"
)

type credentialSource interface {
	Lookup(string) (string, error)
}

type credentialStore interface {
	credentialSource
	Set(string, string) error
}

func createOnboardingProfile(store *profiles.Store, result console.OnboardingResult, secrets credentialStore) (profiles.Profile, error) {
	s := result.Settings
	values := map[string]string{s.APIKeyEnv: s.ProviderAPIKey, s.GatewayAPIKeyEnv: s.GatewayAPIKey, s.BaseURLEnv: s.BaseURL, s.DefaultModelEnv: s.DefaultModel}
	for _, name := range []string{s.APIKeyEnv, s.GatewayAPIKeyEnv, s.BaseURLEnv, s.DefaultModelEnv} {
		if err := secrets.Set(name, values[name]); err != nil {
			return profiles.Profile{}, err
		}
		if err := os.Setenv(name, values[name]); err != nil {
			return profiles.Profile{}, err
		}
	}
	cfg := config.Default()
	cfg.DefaultModel = s.DefaultModel
	cfg.Server.LocalTokenEnv = s.GatewayAPIKeyEnv
	cfg.Upstreams = []config.Upstream{{Name: s.Provider, BaseURL: s.BaseURL, APIKeyEnv: s.APIKeyEnv, Priority: 10, Weight: 1, Enabled: true}}
	return store.Create(profiles.Profile{ID: result.ProfileID, Name: s.Provider, Config: cfg})
}

func loadCredential(name string, source credentialSource) (string, error) {
	if name == "" {
		return "", nil
	}
	if value := os.Getenv(name); value != "" {
		return value, nil
	}
	value, err := source.Lookup(name)
	if err != nil || value == "" {
		return value, err
	}
	if err := os.Setenv(name, value); err != nil {
		return "", err
	}
	return value, nil
}

func prepareConsoleProfile(store *profiles.Store, legacyPath string) (profiles.Profile, error) {
	if _, _, err := store.Migrate(legacyPath); err != nil {
		return profiles.Profile{}, err
	}
	entries, err := store.List()
	if err != nil {
		return profiles.Profile{}, err
	}
	if len(entries) > 1 {
		// Console startup always opens the selector for multiple profiles;
		// persisted activation remains useful to preselect a row, not skip it.
		return profiles.Profile{}, profiles.ErrSelectionRequired
	}
	return store.Select()
}
