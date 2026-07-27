package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nlwproxy/internal/config"
	"nlwproxy/internal/profiles"
)

type fakeCredentialSource map[string]string

func (f fakeCredentialSource) Lookup(name string) (string, error) { return f[name], nil }

func TestLoadCredentialUsesRegistryFallbackAndInjectsProcessEnvironment(t *testing.T) {
	const name = "NLWPROXY_TEST_REGISTRY_CREDENTIAL"
	t.Setenv(name, "")
	got, err := loadCredential(name, fakeCredentialSource{name: "from-registry"})
	if err != nil || got != "from-registry" || os.Getenv(name) != "from-registry" {
		t.Fatalf("got=%q env=%q err=%v", got, os.Getenv(name), err)
	}
}

func TestLoadCredentialDoesNotConsultFallbackWhenProcessEnvironmentExists(t *testing.T) {
	const name = "NLWPROXY_TEST_PROCESS_CREDENTIAL"
	t.Setenv(name, "from-process")
	got, err := loadCredential(name, errorCredentialSource{})
	if err != nil || got != "from-process" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

type errorCredentialSource struct{}

func (errorCredentialSource) Lookup(string) (string, error) { return "", errors.New("must not run") }

func TestPrepareConsoleProfileMigratesOnceAndRepeatLaunchSelectsSameProfile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "nlwproxy.json")
	cfg := config.Default()
	cfg.Upstreams = []config.Upstream{{Name: "Legacy", BaseURL: "https://api.example.test/v1", APIKeyEnv: "LEGACY_KEY", Enabled: true}}
	if err := config.Write(legacy, cfg); err != nil {
		t.Fatal(err)
	}
	store, err := profiles.Open(filepath.Join(dir, "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := prepareConsoleProfile(store, legacy)
	if err != nil || first.ID != "legacy" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := prepareConsoleProfile(store, legacy)
	if err != nil || second.ID != first.ID {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	entries, _ := store.List()
	if len(entries) != 1 {
		t.Fatalf("migration duplicated profiles: %+v", entries)
	}
}

func TestPrepareConsoleProfileOneAutoSelectsAndMultipleRequiresSelector(t *testing.T) {
	store, _ := profiles.Open(t.TempDir())
	cfg := config.Default()
	cfg.Upstreams = []config.Upstream{{Name: "One", BaseURL: "https://one.example.test/v1", APIKeyEnv: "ONE_KEY", Enabled: true}}
	if _, err := store.Create(profiles.Profile{ID: "one", Name: "One", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if got, err := prepareConsoleProfile(store, filepath.Join(t.TempDir(), "missing.json")); err != nil || got.ID != "one" {
		t.Fatalf("one=%+v err=%v", got, err)
	}
	cfg.Upstreams[0].Name = "Two"
	if _, err := store.Create(profiles.Profile{ID: "two", Name: "Two", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareConsoleProfile(store, filepath.Join(t.TempDir(), "missing.json")); !errors.Is(err, profiles.ErrSelectionRequired) {
		t.Fatalf("multiple err=%v", err)
	}
}
