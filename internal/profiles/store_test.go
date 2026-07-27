package profiles

import (
	"errors"
	"nlwproxy/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func validProfile(id, name, env string) Profile {
	c := config.Default()
	c.Upstreams = []config.Upstream{{Name: name, BaseURL: "https://api.example.test/v1", APIKeyEnv: env, Enabled: true}}
	return Profile{ID: id, Name: name, Config: c}
}
func TestSelectionRulesAndPersistence(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Select(); !errors.Is(e, ErrWizardRequired) {
		t.Fatalf("zero=%v", e)
	}
	if _, e = s.Create(validProfile("one", "One", "ONE_KEY")); e != nil {
		t.Fatal(e)
	}
	p, e := s.Select()
	if e != nil || p.ID != "one" {
		t.Fatalf("one=%+v %v", p, e)
	}
	if _, e = s.Create(validProfile("two", "Two", "TWO_KEY")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Activate("two"); e != nil {
		t.Fatal(e)
	}
	s2, e := Open(s.Dir())
	if e != nil {
		t.Fatal(e)
	}
	p, e = s2.Select()
	if e != nil || p.ID != "two" {
		t.Fatalf("persist=%+v %v", p, e)
	}
	idx, _ := s2.Index()
	idx.Active = ""
	idx.LastUsed = ""
	if e = s2.writeIndex(idx); e != nil {
		t.Fatal(e)
	}
	if _, e = s2.Select(); !errors.Is(e, ErrSelectionRequired) {
		t.Fatalf("multiple=%v", e)
	}
}
func TestCRUDAndSeparateEnvironmentNames(t *testing.T) {
	s, _ := Open(t.TempDir())
	a, e := s.Create(validProfile("alpha", "Alpha", "ALPHA_KEY"))
	if e != nil {
		t.Fatal(e)
	}
	if a.Config.Upstreams[0].APIKeyEnv != "ALPHA_KEY" {
		t.Fatal("env")
	}
	if _, e = s.Create(validProfile("alpha", "Again", "OTHER_KEY")); !errors.Is(e, ErrExists) {
		t.Fatalf("duplicate=%v", e)
	}
	b, e := s.Create(validProfile("beta", "Beta", "BETA_KEY"))
	if e != nil {
		t.Fatal(e)
	}
	b.Name = "Beta Updated"
	if _, e = s.Update("beta", b); e != nil {
		t.Fatal(e)
	}
	got, _ := s.Get("beta")
	if got.Name != "Beta Updated" || got.Config.Upstreams[0].APIKeyEnv != "BETA_KEY" {
		t.Fatalf("got=%+v", got)
	}
	if e = s.Delete("alpha"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get("alpha"); !errors.Is(e, ErrNotFound) {
		t.Fatalf("deleted=%v", e)
	}
}
func TestPathSafety(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, id := range []string{"../escape", "a/b", "A", "", ".hidden"} {
		if _, e := s.Create(validProfile(id, "Bad", "BAD_KEY")); e == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	if _, e := os.Stat(filepath.Join(filepath.Dir(s.Dir()), "escape.json")); !os.IsNotExist(e) {
		t.Fatalf("escape file exists: %v", e)
	}
}
func TestMigrationFromLegacyConfig(t *testing.T) {
	d := t.TempDir()
	legacy := filepath.Join(d, "nlwproxy.json")
	c := config.Default()
	c.Upstreams = []config.Upstream{{Name: "My Provider", BaseURL: "https://api.example.test/v1", APIKeyEnv: "LEGACY_PROVIDER_KEY", Enabled: true}}
	if e := config.Write(legacy, c); e != nil {
		t.Fatal(e)
	}
	s, _ := Open(filepath.Join(d, "profiles"))
	p, changed, e := s.Migrate(legacy)
	if e != nil || !changed {
		t.Fatalf("migrate=%+v %v %v", p, changed, e)
	}
	if p.ID != "my-provider" || p.Config.Upstreams[0].APIKeyEnv != "LEGACY_PROVIDER_KEY" {
		t.Fatalf("profile=%+v", p)
	}
	idx, _ := s.Index()
	if idx.Active != p.ID || idx.LastUsed != p.ID {
		t.Fatalf("index=%+v", idx)
	}
	if _, changed, e = s.Migrate(legacy); e != nil || changed {
		t.Fatalf("second migration changed=%v err=%v", changed, e)
	}
}
