package pages

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/profiles"
	"nlwproxy/internal/tuiapp/clipboard"
)

type OverviewData struct {
	Profile                  profiles.Profile
	Status                   string
	BaseURL                  string
	LocalAPIKey              string
	Model                    string
	Requests, Errors, Active int64
	ProxyTotal, ProxyAlive   int
}

type Overview struct {
	Data      OverviewData
	Clipboard clipboard.Writer
	Message   string
}

func NewOverview(data OverviewData, cb clipboard.Writer) Overview {
	return Overview{Data: data, Clipboard: cb}
}
func (m Overview) Init() tea.Cmd { return nil }
func (m Overview) Update(msg tea.Msg) (Overview, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	var value, label string
	switch strings.ToLower(key.String()) {
	case "u":
		value, label = m.Data.BaseURL, "URL"
	case "k":
		value, label = m.Data.LocalAPIKey, "API key"
	case "c":
		value, label = m.QuickConnect(), "connection details"
	default:
		return m, nil
	}
	if m.Clipboard == nil {
		m.Message = "Clipboard unavailable"
		return m, nil
	}
	if err := m.Clipboard.WriteText(value); err != nil {
		m.Message = "Copy failed: " + err.Error()
	} else {
		m.Message = "Copied " + label
	}
	return m, nil
}
func (m Overview) QuickConnect() string {
	env := "—"
	if len(m.Data.Profile.Config.Upstreams) > 0 {
		env = m.Data.Profile.Config.Upstreams[0].APIKeyEnv
	}
	return fmt.Sprintf("OPENAI_BASE_URL=%s\nOPENAI_API_KEY=%s\nOPENAI_MODEL=%s\n%s=<provider-key>\n", m.Data.BaseURL, m.Data.LocalAPIKey, m.Data.Model, env)
}
func (m Overview) View() string {
	envRows := []string{}
	for _, u := range m.Data.Profile.Config.Upstreams {
		envRows = append(envRows, fmt.Sprintf("  %-18s %s", u.Name, u.APIKeyEnv))
	}
	if len(envRows) == 0 {
		envRows = append(envRows, "  No provider routes configured.")
	}
	body := []string{
		Title("Overview", "gateway at a glance"), "",
		fmt.Sprintf("Status       %s", m.Data.Status), fmt.Sprintf("Profile      %s", m.Data.Profile.Name),
		fmt.Sprintf("Base URL     %s", m.Data.BaseURL), fmt.Sprintf("Model        %s", m.Data.Model),
		fmt.Sprintf("Requests     %d  Errors %d  Active %d", m.Data.Requests, m.Data.Errors, m.Data.Active), "",
		accent.Render("Quick Connect"), "  " + strings.ReplaceAll(strings.TrimSpace(m.QuickConnect()), "\n", "\n  "), "",
		accent.Render("Profile environment names"), strings.Join(envRows, "\n"), "",
		muted.Render("[u] copy URL  [k] copy local key  [c] copy Quick Connect"),
	}
	if m.Message != "" {
		body = append(body, "", m.Message)
	}
	return strings.Join(body, "\n")
}
