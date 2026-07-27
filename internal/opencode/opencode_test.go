package opencode

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverPrefersExplicitThenKnownLocations(t *testing.T) {
	home := t.TempDir()
	explicit := filepath.Join(home, "custom.json")
	if err := os.WriteFile(explicit, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(explicit, home, nil)
	if err != nil || got != explicit {
		t.Fatalf("got=%q err=%v", got, err)
	}

	known := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(known), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(known, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = Discover("", home, nil)
	if err != nil || got != known {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestDryRunPreservesFileAndShowsProviderDiff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	original := []byte(`{"theme":"dark","provider":{"other":{"name":"Other"}}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Setup(Options{Path: path, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, original) {
		t.Fatal("dry-run changed config")
	}
	if !strings.Contains(result.Diff, `"nlwproxy"`) || result.BackupPath != "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestSetupBackupChecksumRollbackAndUninstall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	original := []byte("{\n  \"theme\": \"dark\",\n  \"provider\": {\"other\": {\"name\": \"Other\"}}\n}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Setup(Options{Path: path, StateDir: filepath.Join(dir, "state")})
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupPath == "" || result.Checksum == "" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatal(err)
	}
	patched, _ := os.ReadFile(path)
	if bytes.Equal(patched, original) || !bytes.Contains(patched, []byte(`"nlwproxy"`)) || !bytes.Contains(patched, []byte(`"other"`)) {
		t.Fatalf("patched=%s", patched)
	}

	if err := Rollback(path, filepath.Join(dir, "state")); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(path)
	if !bytes.Equal(restored, original) {
		t.Fatalf("rollback mismatch\n%s", restored)
	}

	if _, err := Setup(Options{Path: path, StateDir: filepath.Join(dir, "state")}); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(path); err != nil {
		t.Fatal(err)
	}
	uninstalled, _ := os.ReadFile(path)
	if bytes.Contains(uninstalled, []byte(`"nlwproxy"`)) || !bytes.Contains(uninstalled, []byte(`"other"`)) || !bytes.Contains(uninstalled, []byte(`"theme"`)) {
		t.Fatalf("uninstalled=%s", uninstalled)
	}
}

func TestSetupRejectsInvalidJSONWithoutArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, []byte(`{"broken"`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(Options{Path: path, StateDir: filepath.Join(dir, "state")}); err == nil {
		t.Fatal("expected invalid JSON")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("artifacts created: %v", entries)
	}
}
