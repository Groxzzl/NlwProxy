package pages

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/metrics"
)

type RequestSource interface{ Snapshot() metrics.Snapshot }
type Requests struct {
	Source   RequestSource
	Snapshot metrics.Snapshot
}

func NewRequests(source RequestSource) Requests { m := Requests{Source: source}; m.refresh(); return m }
func (m *Requests) refresh() {
	if m.Source != nil {
		m.Snapshot = m.Source.Snapshot()
	}
}
func (m Requests) Init() tea.Cmd { return nil }
func (m Requests) Update(msg tea.Msg) (Requests, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && strings.ToLower(k.String()) == "r" {
		m.refresh()
	}
	return m, nil
}
func (m Requests) View() string {
	rows := []string{Title("Requests", "metadata only; prompt and response content are never stored"), "", fmt.Sprintf("Total %d  Errors %d  Active %d", m.Snapshot.Total, m.Snapshot.Errors, m.Snapshot.Active), "", "TIME       ID         ENDPOINT                 MODEL            ROUTE       STATUS DURATION TTFT TOKENS RETRIES ERROR"}
	if len(m.Snapshot.Events) == 0 {
		rows = append(rows, "No requests yet.")
	}
	for i := len(m.Snapshot.Events) - 1; i >= 0; i-- {
		e := m.Snapshot.Events[i]
		started := "—"
		if !e.StartedAt.IsZero() {
			started = e.StartedAt.Format("15:04:05")
		}
		duration := formatDuration(e.Duration)
		ttft := formatDuration(e.TTFT)
		rows = append(rows, fmt.Sprintf("%-10s %-10s %-24s %-16s %-11s %6s %-8s %-8s %6d %7d %s", started, e.RequestID, e.Endpoint, e.RequestedModel, e.RouteID, StatusCode(e.Status), duration, ttft, e.TotalTokens, e.RetryCount, e.ErrorCode))
	}
	rows = append(rows, "", muted.Render("[r] refresh"))
	return strings.Join(rows, "\n")
}
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return d.Round(time.Millisecond).String()
}
