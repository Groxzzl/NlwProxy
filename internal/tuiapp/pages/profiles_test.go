package pages

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/profiles"
)

type profileStoreStub struct {
	entries []profiles.Entry
	active  string
	items   map[string]profiles.Profile
}

func (s *profileStoreStub) List() ([]profiles.Entry, error) {
	return append([]profiles.Entry(nil), s.entries...), nil
}
func (s *profileStoreStub) Index() (profiles.Index, error) {
	return profiles.Index{Version: 1, Active: s.active, Profiles: s.entries}, nil
}
func (s *profileStoreStub) Get(id string) (profiles.Profile, error) { return s.items[id], nil }
func (s *profileStoreStub) Create(p profiles.Profile) (profiles.Profile, error) {
	s.items[p.ID] = p
	s.entries = append(s.entries, profiles.Entry{ID: p.ID, Name: p.Name, APIKeyEnv: p.Config.Upstreams[0].APIKeyEnv})
	return p, nil
}
func (s *profileStoreStub) Update(id string, p profiles.Profile) (profiles.Profile, error) {
	s.items[id] = p
	for i := range s.entries {
		if s.entries[i].ID == id {
			s.entries[i].Name = p.Name
		}
	}
	return p, nil
}
func (s *profileStoreStub) Delete(id string) error {
	out := s.entries[:0]
	for _, e := range s.entries {
		if e.ID != id {
			out = append(out, e)
		}
	}
	s.entries = out
	delete(s.items, id)
	return nil
}
func (s *profileStoreStub) Activate(id string) (profiles.Profile, error) {
	s.active = id
	return s.items[id], nil
}
func key(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
func TestProfilesArrowCRUD(t *testing.T) {
	a := testProfile()
	b := testProfile()
	b.ID = "beta"
	b.Name = "Beta"
	b.Config.Upstreams[0].APIKeyEnv = "BETA_KEY"
	s := &profileStoreStub{entries: []profiles.Entry{{ID: a.ID, Name: a.Name, APIKeyEnv: "OPENAI_ALPHA_KEY"}}, active: a.ID, items: map[string]profiles.Profile{a.ID: a}}
	m := NewProfiles(s)
	m.NewProfile = func() profiles.Profile { return b }
	m.EditProfile = func(p profiles.Profile) profiles.Profile { p.Name = "Beta edited"; return p }
	m, _ = m.Update(key('n'))
	if len(m.Entries) != 2 {
		t.Fatalf("create entries=%d", len(m.Entries))
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.Active != "beta" {
		t.Fatalf("active=%q", m.Active)
	}
	m, _ = m.Update(key('e'))
	if s.items["beta"].Name != "Beta edited" {
		t.Fatal("edit failed")
	}
	view := m.View()
	for _, want := range []string{"Profiles", "BETA_KEY", "active", "[n] new", "[d] delete"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	m, _ = m.Update(key('d'))
	if len(m.Entries) != 1 {
		t.Fatalf("delete entries=%d", len(m.Entries))
	}
}
