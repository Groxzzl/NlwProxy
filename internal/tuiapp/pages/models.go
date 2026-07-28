package pages

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/gateway"
)

type Models struct {
	All      []gateway.Model
	Filter   textinput.Model
	Cursor   int
	Selected string
}

func NewModels(items []gateway.Model, selected string) Models {
	input := textinput.New()
	input.Placeholder = "Search models"
	input.Prompt = "/ "
	input.Focus()
	return Models{All: append([]gateway.Model(nil), items...), Filter: input, Selected: selected}
}
func (m Models) Init() tea.Cmd { return textinput.Blink }
func (m Models) Filtered() []gateway.Model {
	q := strings.ToLower(strings.TrimSpace(m.Filter.Value()))
	if q == "" {
		return append([]gateway.Model(nil), m.All...)
	}
	out := []gateway.Model{}
	for _, item := range m.All {
		if strings.Contains(strings.ToLower(item.ID+" "+item.Name+" "+item.OwnedBy), q) {
			out = append(out, item)
		}
	}
	return out
}
func (m Models) Update(msg tea.Msg) (Models, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "up", "ctrl+k":
			if m.Cursor > 0 {
				m.Cursor--
			}
			return m, nil
		case "down", "ctrl+j":
			if m.Cursor < len(m.Filtered())-1 {
				m.Cursor++
			}
			return m, nil
		case "enter":
			items := m.Filtered()
			if len(items) > 0 {
				m.Selected = items[m.Cursor].ID
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.Filter, cmd = m.Filter.Update(msg)
	if m.Cursor >= len(m.Filtered()) {
		m.Cursor = max(0, len(m.Filtered())-1)
	}
	return m, cmd
}
func (m Models) View() string {
	items := m.Filtered()
	rows := []string{Title("Models", "search and select a real upstream model"), "", m.Filter.View(), ""}
	if len(items) == 0 {
		rows = append(rows, "  No matching models.")
	}
	for i, item := range items {
		mark := "  "
		if i == m.Cursor {
			mark = cursor.Render("› ")
		}
		selected := ""
		if item.ID == m.Selected {
			selected = good.Render("  selected")
		}
		name := item.Name
		if name == "" {
			name = item.ID
		}
		rows = append(rows, mark+item.ID+"  "+muted.Render(name)+selected)
	}
	rows = append(rows, "", muted.Render("Type to search  [↑/↓] move  [enter] select"))
	return strings.Join(rows, "\n")
}
