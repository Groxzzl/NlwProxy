package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"nlwproxy/internal/metrics"
)

type Action byte

const (
	ActionRefresh   Action = 'R'
	ActionTest      Action = 'T'
	ActionSetup     Action = 'S'
	ActionCopyURL   Action = 'C'
	ActionCopyKey   Action = 'B'
	ActionCopyAll   Action = 'A'
	ActionToggleKey Action = 'M'
	ActionProvider  Action = 'P'
	ActionLogs      Action = 'L'
	ActionHelp      Action = 'H'
)

type Controller struct {
	Cancel func()
	Handle func(context.Context, Action) error
}

func (c Controller) Dispatch(ctx context.Context, key byte) (bool, error) {
	key = byte(strings.ToUpper(string([]byte{key}))[0])
	if key == 'Q' {
		if c.Cancel != nil {
			c.Cancel()
		}
		return true, nil
	}
	if !strings.ContainsRune("RTSCBAMPLH", rune(key)) {
		return false, nil
	}
	if c.Handle == nil {
		return false, nil
	}
	return false, c.Handle(ctx, Action(key))
}

func RunEventLoop(ctx context.Context, in io.Reader, out io.Writer, refresh time.Duration, ctl Controller, render func() string) error {
	if refresh <= 0 {
		refresh = time.Second
	}
	keys := make(chan byte, 8)
	errs := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				keys <- buf[0]
			}
			if err != nil {
				errs <- err
				return
			}
		}
	}()
	draw := func() error { _, err := io.WriteString(out, "\x1b[2J\x1b[H"+render()); return err }
	if err := draw(); err != nil {
		return err
	}
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			if errors.Is(err, io.EOF) {
				// A redirected input stream may reach EOF after buffering keys.
				// Keep processing those keys before returning.
				for len(keys) > 0 {
					key := <-keys
					quit, dispatchErr := ctl.Dispatch(ctx, key)
					if dispatchErr != nil {
						return dispatchErr
					}
					if drawErr := draw(); drawErr != nil {
						return drawErr
					}
					if quit {
						return nil
					}
				}
				return nil
			}
			return err
		case <-ticker.C:
			if err := draw(); err != nil {
				return err
			}
		case key := <-keys:
			quit, err := ctl.Dispatch(ctx, key)
			if err != nil {
				return err
			}
			if err := draw(); err != nil {
				return err
			}
			if quit {
				return nil
			}
		}
	}
}

func CopyClipboard(text string) error {
	cmd := exec.Command("clip.exe")
	cmd.Stdin = strings.NewReader(text)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("clip.exe: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

type ModelStat struct {
	Name                                        string
	Requests, Errors, InputTokens, OutputTokens int64
}
type RouteStat struct {
	Name, Transport, State   string
	Requests, Errors, Active int64
	Latency                  time.Duration
}
type DashboardView struct {
	Status, BaseURL, APIKey, Provider, ModelAlias, Message string
	ShowAPIKey                                             bool
	Started, Now                                           time.Time
	Requests, Errors, Active, InputTokens, OutputTokens    int64
	Models                                                 []ModelStat
	Routes                                                 []RouteStat
	Recent                                                 []metrics.Request
}

func AggregateMetadata(events []metrics.Request, configured []RouteStat) ([]ModelStat, []RouteStat, int64, int64) {
	models := map[string]*ModelStat{}
	routes := map[string]*RouteStat{}
	for i := range configured {
		r := configured[i]
		routes[r.Name] = &r
	}
	var input, output int64
	for _, e := range events {
		input += e.RequestBytes
		output += e.ResponseBytes
		model := e.RequestedModel
		if model == "" {
			model = "—"
		}
		m := models[model]
		if m == nil {
			m = &ModelStat{Name: model}
			models[model] = m
		}
		m.Requests++
		m.InputTokens += e.RequestBytes
		m.OutputTokens += e.ResponseBytes
		route := e.RouteID
		if route == "" {
			route = "unassigned"
		}
		r := routes[route]
		if r == nil {
			r = &RouteStat{Name: route, State: "unknown"}
			routes[route] = r
		}
		r.Requests++
		if e.Status >= 400 || e.ErrorCode != "" {
			m.Errors++
			r.Errors++
		}
	}
	ms := make([]ModelStat, 0, len(models))
	for _, m := range models {
		ms = append(ms, *m)
	}
	rs := make([]RouteStat, 0, len(routes))
	for _, r := range routes {
		rs = append(rs, *r)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	sort.Slice(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
	return ms, rs, input, output
}

func RenderDashboardV2(v DashboardView, color bool, width int) string {
	if width < 88 {
		width = 88
	}
	if width > 132 {
		width = 132
	}
	now := v.Now
	if now.IsZero() {
		now = time.Now()
	}
	uptime := "—"
	if !v.Started.IsZero() {
		uptime = now.Sub(v.Started).Round(time.Second).String()
	}
	key := v.APIKey
	if !v.ShowAPIKey {
		key = MaskSecret(key)
	}
	line := strings.Repeat("─", width-2)
	var b strings.Builder
	row := func(s string) {
		r := []rune(s)
		if len(r) > width-4 {
			s = string(r[:width-5]) + "…"
		}
		fmt.Fprintf(&b, "│ %-*s │\n", width-4, s)
	}
	section := func(name string) {
		label := " " + name + " "
		fmt.Fprintf(&b, "├%s%s┤\n", label, strings.Repeat("─", width-2-len([]rune(label))))
	}
	fmt.Fprintf(&b, "┌%s┐\n", line)
	row("NLWPROXY CONSOLE V2  /  LIVE GATEWAY CONTROL")
	fmt.Fprintf(&b, "├%s┤\n", line)
	row(fmt.Sprintf("STATUS  ● %-10s  UPTIME %-10s  REQUESTS %-8d ERRORS %-6d ACTIVE %d", v.Status, uptime, v.Requests, v.Errors, v.Active))
	row("BASE URL  " + v.BaseURL)
	row("API KEY   " + key)
	row("PROVIDER  " + v.Provider + "    MODEL ALIAS  " + v.ModelAlias)
	section("TOKEN TOTALS (metadata bytes)")
	row(fmt.Sprintf("INPUT  %s    OUTPUT  %s    TOTAL  %s", comma(v.InputTokens), comma(v.OutputTokens), comma(v.InputTokens+v.OutputTokens)))
	section("PER-MODEL")
	row("MODEL                         REQUESTS   ERRORS      INPUT     OUTPUT")
	if len(v.Models) == 0 {
		row("No model activity yet.")
	}
	for _, m := range v.Models {
		row(fmt.Sprintf("%-29s %8d %8d %10s %10s", m.Name, m.Requests, m.Errors, comma(m.InputTokens), comma(m.OutputTokens)))
	}
	section("ROUTES / PROXIES")
	row("ROUTE                   TRANSPORT   STATE       REQUESTS ERRORS ACTIVE LATENCY")
	if len(v.Routes) == 0 {
		row("No routes configured.")
	}
	for _, r := range v.Routes {
		latency := "—"
		if r.Latency > 0 {
			latency = r.Latency.Round(time.Millisecond).String()
		}
		row(fmt.Sprintf("%-23s %-11s %-11s %8d %6d %6d %s", r.Name, r.Transport, r.State, r.Requests, r.Errors, r.Active, latency))
	}
	section("RECENT REQUESTS · METADATA ONLY")
	row("TIME      ID       METHOD/ENDPOINT                    MODEL          ROUTE      STATUS DURATION")
	if len(v.Recent) == 0 {
		row("No requests yet. Prompt and response content are never stored.")
	}
	for i := len(v.Recent) - 1; i >= 0; i-- {
		e := v.Recent[i]
		row(fmt.Sprintf("%-9s %-8s %-34s %-14s %-10s %6d %s", e.StartedAt.Format("15:04:05"), e.RequestID, e.Endpoint, e.RequestedModel, e.RouteID, e.Status, e.Duration.Round(time.Millisecond)))
	}
	section("ACTIONS")
	row("[R] Refresh  [T] Test  [S] Setup  [C] Copy URL  [B] Copy key  [A] Copy all")
	row("[M] Mask key  [P] Provider  [L] Logs  [H] Help  [Q] Quit gracefully")
	if v.Message != "" {
		row("NOTICE  " + v.Message)
	}
	fmt.Fprintf(&b, "└%s┘\n", line)
	return b.String()
}

func comma(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
