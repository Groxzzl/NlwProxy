package proxymanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadDirMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "1.1.1.1:8080\n2.2.2.2:8080:user:pass\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "3.3.3.3:9090\nhttp://4.4.4.4:3128\n")
	// non-txt file must be ignored
	writeFile(t, filepath.Join(dir, "ignore.csv"), "5.5.5.5:1234\n")

	m := New(nil)
	added, errs := m.LoadDir(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if added != 4 {
		t.Fatalf("expected 4 added, got %d", added)
	}
	if total, _ := m.Count(); total != 4 {
		t.Fatalf("expected 4 entries, got %d", total)
	}
	// verify .csv was skipped
	for _, e := range m.List() {
		if e.Host == "5.5.5.5" {
			t.Fatal("non-txt file was imported")
		}
	}
}

func TestLoadDirDedup(t *testing.T) {
	dir := t.TempDir()
	// 1.1.1.1:8080 appears in both files; 2.2.2.2:8080 only once.
	writeFile(t, filepath.Join(dir, "a.txt"), "1.1.1.1:8080\n2.2.2.2:8080\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "1.1.1.1:8080:user:pass\n3.3.3.3:8080\n")

	m := New(nil)
	added, errs := m.LoadDir(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// 1.1.1.1:8080 deduped -> only 3 unique host:port
	if added != 3 {
		t.Fatalf("expected 3 added after dedup, got %d", added)
	}
	if total, _ := m.Count(); total != 3 {
		t.Fatalf("expected 3 entries after dedup, got %d", total)
	}
	// a.txt processed first (lexical), so first 1.1.1.1 wins: no username.
	seen := map[string]ProxyEntry{}
	for _, e := range m.List() {
		seen[proxyKey(e.Host, e.Port)] = e
	}
	if e := seen["1.1.1.1:8080"]; e.Username != "" {
		t.Fatalf("expected first-wins dedup (no username), got username=%q", e.Username)
	}
}

func TestLoadDirCreatesMissingDir(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "proxies")
	m := New(nil)
	added, errs := m.LoadDir(missing)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if added != 0 {
		t.Fatalf("expected 0 added from empty dir, got %d", added)
	}
	if _, err := os.Stat(missing); err != nil {
		t.Fatalf("LoadDir did not create dir: %v", err)
	}
}

func TestSaveJSONIncludesPasswords(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "1.1.1.1:8080:user:secretpass\n")
	m := New(nil)
	if _, errs := m.LoadDir(dir); len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}
	out := filepath.Join(dir, "proxies.json")
	if err := m.SaveJSON(out); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	var got []persistedProxy
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 persisted proxy, got %d", len(got))
	}
	if got[0].Password != "secretpass" {
		t.Fatalf("expected password persisted, got %q", got[0].Password)
	}
	if got[0].Source != "a.txt" {
		t.Fatalf("expected source a.txt, got %q", got[0].Source)
	}
}
