package pages

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/config"
	"nlwproxy/internal/routing"
)

type RouteSource interface {
	Snapshots(time.Time) map[string]routing.RouteSnapshot
}
type Routes struct {
	Config config.Config
	Source RouteSource
	Now    time.Time
}

func NewRoutes(cfg config.Config, source RouteSource) Routes {
	return Routes{Config: cfg, Source: source, Now: time.Now()}
}
func (m Routes) Init() tea.Cmd                    { return nil }
func (m Routes) Update(tea.Msg) (Routes, tea.Cmd) { return m, nil }
func (m Routes) View() string {
	now := m.Now
	if now.IsZero() {
		now = time.Now()
	}
	snap := map[string]routing.RouteSnapshot{}
	if m.Source != nil {
		snap = m.Source.Snapshots(now)
	}
	ups := append([]config.Upstream(nil), m.Config.Upstreams...)
	sort.Slice(ups, func(i, j int) bool { return ups[i].Priority < ups[j].Priority })
	rows := []string{Title("Routes", "routing health and usage"), "", fmt.Sprintf("Strategy: %s", m.Config.Routing.Strategy), "", "ROUTE             TRANSPORT  STATE          PRIO ACTIVE REQUESTS ERRORS LATENCY TOKENS EXIT IP"}
	if len(ups) == 0 {
		rows = append(rows, "No routes configured.")
	}
	for _, u := range ups {
		s := snap[u.Name]
		state := string(s.Health) + "/" + string(s.Circuit)
		if !u.Enabled {
			state = "disabled"
		}
		transport := s.Transport
		if transport == "" {
			if u.ProxyURL == "" {
				transport = "direct"
			} else {
				transport = "proxy"
			}
		}
		latency := "—"
		if s.Latency > 0 {
			latency = s.Latency.Round(time.Millisecond).String()
		}
		ip := s.ExitIP.IP
		if ip == "" {
			ip = "—"
		}
		rows = append(rows, fmt.Sprintf("%-17s %-10s %-14s %4d %6d %8d %6d %-7s %6d %s", u.Name, transport, state, u.Priority, s.Active, s.Total, s.Errors, latency, s.InputTokens+s.OutputTokens, ip))
	}
	return strings.Join(rows, "\n")
}
