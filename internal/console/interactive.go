package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	ActionConfig    Action = 'C'
	ActionCopyURL   Action = 'U'
	ActionCopyKey   Action = 'B'
	ActionCopyAll   Action = 'A'
	ActionFreeze    Action = 'F'
	ActionNew       Action = 'N'
	ActionEdit      Action = 'E'
	ActionSwitch    Action = 'W'
	ActionDelete    Action = 'D'
	ActionToggleKey Action = 'M'
	ActionProvider  Action = 'P'
	ActionLogs      Action = 'L'
	ActionHelp      Action = 'H'
)

type Controller struct {
	Cancel func()
	Handle func(context.Context, Action) error
}

type TerminalCapabilities struct {
	Interactive bool
	Color       bool
	Width       int
}

func TerminalIsInteractive(in, out *os.File) bool {
	return isTerminalFile(in) && isTerminalFile(out)
}

func TerminalWidth(out *os.File) int {
	width, _ := terminalSize(out)
	return width
}

func (c Controller) Dispatch(ctx context.Context, key byte) (bool, error) {
	key = byte(strings.ToUpper(string([]byte{key}))[0])
	if key == 'Q' {
		if c.Cancel != nil {
			c.Cancel()
		}
		return true, nil
	}
	if !strings.ContainsRune("RTSCUBAFNEWPDMLH", rune(key)) {
		return false, nil
	}
	if c.Handle == nil {
		return false, nil
	}
	return false, c.Handle(ctx, Action(key))
}

func RunEventLoop(ctx context.Context, in io.Reader, out io.Writer, refresh time.Duration, ctl Controller, render func() string) error {
	return RunEventLoopChanges(ctx, in, out, refresh, ctl, render, nil)
}

// RunEventLoopChanges redraws on explicit metadata changes and keeps refresh
// only as a bounded fallback for time-derived fields such as uptime.
func RunEventLoopChanges(ctx context.Context, in io.Reader, out io.Writer, refresh time.Duration, ctl Controller, render func() string, changes <-chan struct{}) error {
	caps := TerminalCapabilities{}
	inputFile, inputOK := in.(*os.File)
	outputFile, outputOK := out.(*os.File)
	if inputOK && outputOK && isTerminalFile(inputFile) && isTerminalFile(outputFile) && enableANSI(outputFile) {
		caps.Interactive = true
		caps.Color = os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
		caps.Width, _ = terminalSize(outputFile)
		return withRawInput(inputFile, func(reader io.Reader) error {
			return runEventLoop(ctx, reader, out, refresh, ctl, render, changes, caps)
		})
	}
	return runEventLoop(ctx, in, out, refresh, ctl, render, changes, caps)
}

func runEventLoop(ctx context.Context, in io.Reader, out io.Writer, refresh time.Duration, ctl Controller, render func() string, changes <-chan struct{}, caps TerminalCapabilities) error {
	if !caps.Interactive {
		_, err := io.WriteString(out, render())
		return err
	}
	if _, err := io.WriteString(out, EnterScreen(true)); err != nil {
		return err
	}
	defer io.WriteString(out, LeaveScreen(true))
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
	lastFrame := render()
	drawFrame := func(frame string) error {
		_, err := io.WriteString(out, ClearFrame(true)+frame)
		return err
	}
	if err := drawFrame(lastFrame); err != nil {
		return err
	}
	frozen := false
	processKey := func(key byte) (bool, error) {
		upper := byte(strings.ToUpper(string([]byte{key}))[0])
		if upper == byte(ActionFreeze) {
			frozen = !frozen
		}
		quit, err := ctl.Dispatch(ctx, key)
		if err != nil || quit {
			return quit, err
		}
		if upper == byte(ActionFreeze) {
			lastFrame = render()
			return false, drawFrame(lastFrame)
		}
		if frozen && upper != byte(ActionRefresh) {
			return false, nil
		}

		lastFrame = render()
		return false, drawFrame(lastFrame)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			if errors.Is(err, io.EOF) {
				for len(keys) > 0 {
					quit, dispatchErr := processKey(<-keys)
					if dispatchErr != nil || quit {
						return dispatchErr
					}
				}
				return nil
			}
			return err
		case _, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			if frozen {
				continue
			}
			frame := render()
			if frame != lastFrame {
				lastFrame = frame
				if err := drawFrame(frame); err != nil {
					return err
				}
			}
		case key := <-keys:
			quit, err := processKey(key)
			if err != nil || quit {
				return err
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
	Name, Transport, State, ExitIP string
	Requests, Errors, Active       int64
	Latency                        time.Duration
	Load                           int
}
type DashboardView struct {
	Profile, Status, BaseURL, APIKey, Provider, ModelAlias, Message string
	ShowAPIKey                                                      bool
	Frozen                                                          bool
	Started, Now                                                    time.Time
	Requests, Errors, Active, InputTokens, OutputTokens             int64
	Models                                                          []ModelStat
	AvailableModels                                                 []CatalogModel
	Routes                                                          []RouteStat
	Recent                                                          []metrics.Request
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
		input += e.InputTokens
		output += e.OutputTokens
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
		m.InputTokens += e.InputTokens
		m.OutputTokens += e.OutputTokens
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
	paint := func(code, text string) string {
		if !color {
			return text
		}
		return code + text + themeReset
	}
	if width < 48 {
		width = 48
	}
	// Never render into the terminal's last column: Windows terminals may
	// auto-wrap there and corrupt the entire frame.
	width--
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
		visible := stripANSI(s)
		visibleRunes := []rune(visible)
		if len(visibleRunes) > width-4 {
			// Truncating colored fragments safely is complex; use their plain
			// representation when the current terminal is narrow.
			s = string(visibleRunes[:width-5]) + "…"
			visible = stripANSI(s)
		}
		padding := width - 4 - len([]rune(visible))
		if padding < 0 {
			padding = 0
		}
		fmt.Fprintf(&b, "│ %s%s │\r\n", s, strings.Repeat(" ", padding))
	}
	section := func(name string) {
		label := " " + name + " "
		fmt.Fprintf(&b, "├%s%s┤\r\n", label, strings.Repeat("─", width-2-len([]rune(label))))
	}
	fmt.Fprintf(&b, "┌%s┐\r\n", line)
	row(paint(themeBold+themePrimary, "NLWPROXY PREMIUM") + paint(themeMuted, "  /  LIVE GATEWAY CONTROL"))
	fmt.Fprintf(&b, "├%s┤\r\n", line)
	state := v.Status
	if v.Frozen {
		state += "  ❄ FROZEN"
	}
	row(fmt.Sprintf("STATUS  ● %-20s  UPTIME %-10s  REQUESTS %-8d ERRORS %-6d ACTIVE %d", state, uptime, v.Requests, v.Errors, v.Active))
	row("PROFILE   " + paint(themeAccent, v.Profile))
	row("BASE URL  " + paint(themePrimary, v.BaseURL))
	row("API KEY   " + key)
	row("PROVIDER  " + v.Provider + "    MODEL ALIAS  " + v.ModelAlias)
	section("TOKEN TOTALS")
	row(fmt.Sprintf("INPUT  %s    OUTPUT  %s    TOTAL  %s", comma(v.InputTokens), comma(v.OutputTokens), comma(v.InputTokens+v.OutputTokens)))
	section("PER-MODEL")
	row("MODEL                         REQUESTS   ERRORS      INPUT     OUTPUT")
	if len(v.Models) == 0 {
		row("No model activity yet.")
	}
	for _, m := range v.Models {
		row(fmt.Sprintf("%-29s %8d %8d %10s %10s", m.Name, m.Requests, m.Errors, comma(m.InputTokens), comma(m.OutputTokens)))
	}
	section("AVAILABLE MODELS")
	if len(v.AvailableModels) == 0 {
		row("Catalog loading or unavailable. /v1/models remains transparent.")
	}
	for _, m := range v.AvailableModels {
		row(fmt.Sprintf("%-48s %s", m.ID, m.Name))
	}
	section("ROUTES / PROXIES")
	row("ROUTE              TRANSPORT STATE       EXIT IP          LOAD REQUESTS ERRORS ACTIVE LATENCY")
	if len(v.Routes) == 0 {
		row("No routes configured.")
	}
	for _, r := range v.Routes {
		latency := "—"
		if r.Latency > 0 {
			latency = r.Latency.Round(time.Millisecond).String()
		}
		exitIP := r.ExitIP
		if exitIP == "" {
			exitIP = "—"
		}
		row(fmt.Sprintf("%-18s %-9s %-11s %-16s %3d%% %8d %6d %6d %s", r.Name, r.Transport, r.State, exitIP, r.Load, r.Requests, r.Errors, r.Active, latency))
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
	row("[B] Copy key  [U] Copy URL  [A] Copy details  [C] Config templates")
	row("[F] Freeze/unfreeze  [R] Refresh  [N] New  [E] Edit  [W] Switch  [D] Delete")
	row("[P] Probe exit IP  [M] Mask key  [L] Logs  [H] Help  [Q] Quit gracefully")
	if v.Message != "" {
		row("NOTICE  " + v.Message)
	}
	fmt.Fprintf(&b, "└%s┘\r\n", line)
	return b.String()
}

func stripANSI(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '[' {
			i += 2
			for i < len(value) {
				c := value[i]
				i++
				if c >= 0x40 && c <= 0x7e {
					break
				}
			}
			continue
		}
		b.WriteByte(value[i])
		i++
	}
	return b.String()
}

func comma(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
