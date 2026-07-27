package cli

import (
	"os"

	"nlwproxy/internal/profiles"
)

type credentialSource interface {
	Lookup(string) (string, error)
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
