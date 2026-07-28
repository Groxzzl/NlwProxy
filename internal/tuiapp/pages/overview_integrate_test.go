package pages

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/config"
	"nlwproxy/internal/profiles"
	"nlwproxy/internal/tuiapp/clipboard"
)

func testProfile() profiles.Profile {
	c := config.Default()
	c.Upstreams = []config.Upstream{{Name: "OpenAI", BaseURL: "https://api.example.test/v1", APIKeyEnv: "OPENAI_ALPHA_KEY", Enabled: true}}
	return profiles.Profile{ID: "alpha", Name: "Alpha", Config: c}
}
func TestOverviewQuickConnectAndCopy(t *testing.T) {
	var copied string
	m := NewOverview(OverviewData{Profile: testProfile(), Status: "online", BaseURL: "http://127.0.0.1:8787/v1", LocalAPIKey: "local-key", Model: "gpt-live"}, clipboard.WriterFunc(func(s string) error { copied = s; return nil }))
	view := m.View()
	for _, want := range []string{"Quick Connect", "OPENAI_ALPHA_KEY", "OPENAI_BASE_URL", "gpt-live"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if copied != m.QuickConnect() || !strings.Contains(m.Message, "Copied") {
		t.Fatalf("copied=%q message=%q", copied, m.Message)
	}
}
func TestIntegrateTemplatesAreCompleteAndCopyable(t *testing.T) {
	data := IntegrationData{BaseURL: "http://localhost:8787/v1", APIKey: "local-key", Model: "vendor/model"}
	var copied string
	m := NewIntegrate(data, clipboard.WriterFunc(func(s string) error { copied = s; return nil }))
	if len(m.Formats) != 7 {
		t.Fatalf("formats=%d", len(m.Formats))
	}
	names := strings.Join(IntegrationFormatNames(), " ")
	for _, want := range []string{"Generic YAML", "OpenCode JSON", "Claude settings", "Continue", "Python", "Node", "Environment"} {
		if !strings.Contains(names, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for i := range m.Formats {
		m.Selected = i
		got := m.Template()
		for _, want := range []string{data.BaseURL, data.APIKey, data.Model} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s missing %q:\n%s", m.Formats[i].Name, want, got)
			}
		}
	}
	m.Selected = 1
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if copied != m.Template() {
		t.Fatal("copied template mismatch")
	}
}
