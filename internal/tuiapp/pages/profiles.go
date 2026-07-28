package pages

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/profiles"
)

type ProfileStore interface {
	List() ([]profiles.Entry, error)
	Index() (profiles.Index, error)
	Get(string) (profiles.Profile, error)
	Create(profiles.Profile) (profiles.Profile, error)
	Update(string, profiles.Profile) (profiles.Profile, error)
	Delete(string) error
	Activate(string) (profiles.Profile, error)
}
type ProfileDraft func() profiles.Profile
type ProfileEdit func(profiles.Profile) profiles.Profile

type Profiles struct {
	Store       ProfileStore
	Entries     []profiles.Entry
	Active      string
	Cursor      int
	NewProfile  ProfileDraft
	EditProfile ProfileEdit
	Message     string
}

func NewProfiles(store ProfileStore) Profiles { m := Profiles{Store: store}; m.reload(); return m }
func (m *Profiles) reload() {
	if m.Store == nil {
		return
	}
	entries, err := m.Store.List()
	if err != nil {
		m.Message = err.Error()
		return
	}
	m.Entries = entries
	if idx, err := m.Store.Index(); err == nil {
		m.Active = idx.Active
	}
	if m.Cursor >= len(m.Entries) {
		m.Cursor = max(0, len(m.Entries)-1)
	}
}
func (m Profiles) Init() tea.Cmd { return nil }
func (m Profiles) Update(msg tea.Msg) (Profiles, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch strings.ToLower(key.String()) {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "j":
		if m.Cursor < len(m.Entries)-1 {
			m.Cursor++
		}
	case "enter", "right":
		if len(m.Entries) > 0 {
			if _, err := m.Store.Activate(m.Entries[m.Cursor].ID); err != nil {
				m.Message = err.Error()
			} else {
				m.Message = "Activated " + m.Entries[m.Cursor].Name
				m.reload()
			}
		}
	case "n":
		if m.NewProfile == nil {
			m.Message = "New profile editor unavailable"
		} else if _, err := m.Store.Create(m.NewProfile()); err != nil {
			m.Message = err.Error()
		} else {
			m.Message = "Profile created"
			m.reload()
		}
	case "e":
		if len(m.Entries) == 0 || m.EditProfile == nil {
			m.Message = "Profile editor unavailable"
		} else if p, err := m.Store.Get(m.Entries[m.Cursor].ID); err != nil {
			m.Message = err.Error()
		} else if _, err = m.Store.Update(p.ID, m.EditProfile(p)); err != nil {
			m.Message = err.Error()
		} else {
			m.Message = "Profile updated"
			m.reload()
		}
	case "d", "delete":
		if len(m.Entries) > 0 {
			if err := m.Store.Delete(m.Entries[m.Cursor].ID); err != nil {
				m.Message = err.Error()
			} else {
				m.Message = "Profile deleted"
				m.reload()
			}
		}
	}
	return m, nil
}
func (m Profiles) View() string {
	rows := []string{Title("Profiles", "named provider configurations"), ""}
	if len(m.Entries) == 0 {
		rows = append(rows, "  No profiles. Press n to create one.")
	}
	for i, e := range m.Entries {
		mark := "  "
		if i == m.Cursor {
			mark = cursor.Render("› ")
		}
		active := ""
		if e.ID == m.Active {
			active = good.Render("  active")
		}
		rows = append(rows, mark+e.Name+"  "+muted.Render(e.ID)+active, "    API key env: "+e.APIKeyEnv)
	}
	rows = append(rows, "", muted.Render("[↑/↓] move  [→/enter] activate  [n] new  [e] edit  [d] delete"))
	if m.Message != "" {
		rows = append(rows, "", m.Message)
	}
	return strings.Join(rows, "\n")
}
