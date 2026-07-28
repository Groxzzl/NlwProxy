package pages

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/tuiapp/clipboard"
)

type IntegrationFormat struct {
	Name   string
	Render func(IntegrationData) string
}
type IntegrationData struct{ BaseURL, APIKey, Model string }

type Integrate struct {
	Data      IntegrationData
	Formats   []IntegrationFormat
	Selected  int
	Clipboard clipboard.Writer
	Message   string
}

func NewIntegrate(data IntegrationData, cb clipboard.Writer) Integrate {
	return Integrate{Data: data, Clipboard: cb, Formats: integrationFormats()}
}
func integrationFormats() []IntegrationFormat {
	return []IntegrationFormat{
		{"Generic YAML", func(d IntegrationData) string {
			return fmt.Sprintf("provider:\n  name: nlwproxy\n  base_url: %s\n  api_key: %s\n  model: %s\n", d.BaseURL, d.APIKey, d.Model)
		}},
		{"OpenCode JSON", func(d IntegrationData) string {
			v := map[string]any{"model": "nlwproxy/" + d.Model, "provider": map[string]any{"nlwproxy": map[string]any{"npm": "@ai-sdk/openai-compatible", "options": map[string]string{"baseURL": d.BaseURL, "apiKey": d.APIKey}, "models": map[string]any{d.Model: map[string]string{"name": d.Model}}}}}
			b, _ := json.MarshalIndent(v, "", "  ")
			return string(b) + "\n"
		}},
		{"Claude settings", func(d IntegrationData) string {
			v := map[string]any{"env": map[string]string{"ANTHROPIC_BASE_URL": d.BaseURL, "ANTHROPIC_AUTH_TOKEN": d.APIKey, "ANTHROPIC_MODEL": d.Model}}
			b, _ := json.MarshalIndent(v, "", "  ")
			return string(b) + "\n"
		}},
		{"Continue", func(d IntegrationData) string {
			return fmt.Sprintf("models:\n  - name: NLWProxy\n    provider: openai\n    model: %s\n    apiBase: %s\n    apiKey: %s\n", d.Model, d.BaseURL, d.APIKey)
		}},
		{"Python", func(d IntegrationData) string {
			return fmt.Sprintf("from openai import OpenAI\nclient = OpenAI(base_url=%q, api_key=%q)\nresponse = client.responses.create(model=%q, input=\"Hello\")\n", d.BaseURL, d.APIKey, d.Model)
		}},
		{"Node", func(d IntegrationData) string {
			return fmt.Sprintf("import OpenAI from \"openai\";\nconst client = new OpenAI({ baseURL: %q, apiKey: %q });\nconst response = await client.responses.create({ model: %q, input: \"Hello\" });\n", d.BaseURL, d.APIKey, d.Model)
		}},
		{"Environment", func(d IntegrationData) string {
			return fmt.Sprintf("OPENAI_BASE_URL=%s\nOPENAI_API_KEY=%s\nOPENAI_MODEL=%s\n", d.BaseURL, d.APIKey, d.Model)
		}},
	}
}
func (m Integrate) Init() tea.Cmd { return nil }
func (m Integrate) Update(msg tea.Msg) (Integrate, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.Selected > 0 {
			m.Selected--
		}
	case "down", "j":
		if m.Selected < len(m.Formats)-1 {
			m.Selected++
		}
	case "enter", "c":
		if m.Clipboard == nil {
			m.Message = "Clipboard unavailable"
		} else if err := m.Clipboard.WriteText(m.Template()); err != nil {
			m.Message = "Copy failed: " + err.Error()
		} else {
			m.Message = "Copied " + m.Formats[m.Selected].Name
		}
	}
	return m, nil
}
func (m Integrate) Template() string {
	if len(m.Formats) == 0 {
		return ""
	}
	i := m.Selected
	if i < 0 || i >= len(m.Formats) {
		i = 0
	}
	return m.Formats[i].Render(m.Data)
}
func (m Integrate) View() string {
	var menu []string
	for i, f := range m.Formats {
		mark := "  "
		if i == m.Selected {
			mark = cursor.Render("› ")
		}
		menu = append(menu, mark+f.Name)
	}
	parts := []string{Title("Integrate", "ready-to-copy client configuration"), "", strings.Join(menu, "\n"), "", accent.Render(m.Formats[m.Selected].Name), m.Template(), muted.Render("[↑/↓] select  [enter/c] copy")}
	if m.Message != "" {
		parts = append(parts, "", m.Message)
	}
	return strings.Join(parts, "\n")
}

func IntegrationFormatNames() []string {
	names := []string{}
	for _, f := range integrationFormats() {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}
