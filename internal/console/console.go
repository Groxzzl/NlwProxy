// Package console provides NlwProxy's interactive setup and live dashboard.
package console

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"nlwproxy/internal/config"
)

const LocalTokenEnv = "NLW_PROXY_LOCAL_TOKEN"

type Settings struct {
	Provider, BaseURL, APIKey, APIKeyEnv, LocalToken, Listen, ModelAlias string
}

func MaskSecret(value string) string {
	if value == "" {
		return "not set"
	}
	if len(value) <= 7 {
		return "***"
	}
	return value[:3] + "…" + value[len(value)-4:]
}

func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func BuildConfig(s Settings) config.Config {
	cfg := config.Default()
	if s.Listen != "" {
		cfg.Server.Listen = s.Listen
	}
	cfg.Server.LocalTokenEnv = LocalTokenEnv
	cfg.Upstreams = []config.Upstream{{Name: s.Provider, BaseURL: strings.TrimRight(s.BaseURL, "/"), APIKeyEnv: s.APIKeyEnv, Priority: 10, Weight: 1, Enabled: true}}
	return cfg
}

func PersistSetup(path string, cfg config.Config, s Settings, persist func(string, string) error) error {
	if err := persist(s.APIKeyEnv, s.APIKey); err != nil {
		return fmt.Errorf("persist provider key: %w", err)
	}
	if err := persist(LocalTokenEnv, s.LocalToken); err != nil {
		return fmt.Errorf("persist local token: %w", err)
	}
	if err := os.Setenv(s.APIKeyEnv, s.APIKey); err != nil {
		return err
	}
	if err := os.Setenv(LocalTokenEnv, s.LocalToken); err != nil {
		return err
	}
	return config.Write(path, cfg)
}

func PersistUserEnvironment(name, value string) error {
	if runtime.GOOS != "windows" {
		return os.Setenv(name, value)
	}
	if strings.ContainsAny(name, "\r\n\x00") || strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("environment value contains unsafe characters")
	}
	cmd := exec.Command("reg.exe", "add", `HKCU\Environment`, "/v", name, "/t", "REG_SZ", "/d", value, "/f")
	cmd.Stdin = nil
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg.exe: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func TestProvider(ctx context.Context, client *http.Client, baseURL, apiKey string) error {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned %s", resp.Status)
	}
	return nil
}

type RecentRequest struct {
	At               time.Time
	Method, Endpoint string
	Status           int
	Duration         time.Duration
	Route            string
}
type StatsSnapshot struct {
	Requests, Errors, Active int64
	Recent                   []RecentRequest
}
type Stats struct {
	mu                       sync.RWMutex
	requests, errors, active int64
	max                      int
	recent                   []RecentRequest
}

func NewStats(max int) *Stats {
	if max < 1 {
		max = 10
	}
	return &Stats{max: max}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (s *Stats) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		s.mu.Lock()
		s.active++
		s.mu.Unlock()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		s.mu.Lock()
		s.active--
		s.requests++
		if sw.status >= 400 {
			s.errors++
		}
		s.recent = append(s.recent, RecentRequest{At: start, Method: r.Method, Endpoint: r.URL.Path, Status: sw.status, Duration: time.Since(start)})
		if len(s.recent) > s.max {
			s.recent = append([]RecentRequest(nil), s.recent[len(s.recent)-s.max:]...)
		}
		s.mu.Unlock()
	})
}
func (s *Stats) Snapshot() StatsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return StatsSnapshot{Requests: s.requests, Errors: s.errors, Active: s.active, Recent: append([]RecentRequest(nil), s.recent...)}
}

type View struct {
	BaseURL, APIKey, ModelAlias, Provider, Status string
	Started                                       time.Time
	Stats                                         StatsSnapshot
}

func RenderDashboard(v View, color bool) string {
	green, cyan, yellow, reset := "", "", "", ""
	if color {
		green = "\x1b[32m"
		cyan = "\x1b[36m"
		yellow = "\x1b[33m"
		reset = "\x1b[0m"
	}
	uptime := "—"
	if !v.Started.IsZero() {
		uptime = time.Since(v.Started).Round(time.Second).String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%sNLWPROXY LIVE CONTROL%s\n=====================\n", cyan, reset)
	fmt.Fprintf(&b, "STATUS     %s%s%s\nUPTIME     %s\nBASE URL   %s\nAPI KEY    %s\nMODEL      %s\nPROVIDER   %s\n", green, v.Status, reset, uptime, v.BaseURL, MaskSecret(v.APIKey), v.ModelAlias, v.Provider)
	fmt.Fprintf(&b, "\nREQUESTS   %d   ERRORS %d   ACTIVE %d\n\nRECENT REQUESTS (metadata only)\n", v.Stats.Requests, v.Stats.Errors, v.Stats.Active)
	if len(v.Stats.Recent) == 0 {
		b.WriteString("  No requests yet. Prompt and response content are never stored.\n")
	}
	for i := len(v.Stats.Recent) - 1; i >= 0; i-- {
		r := v.Stats.Recent[i]
		fmt.Fprintf(&b, "  %s %-4s %-23s %3d %s\n", r.At.Format("15:04:05"), r.Method, r.Endpoint, r.Status, r.Duration.Round(time.Millisecond))
	}
	fmt.Fprintf(&b, "\n%s[R]%s refresh  [T] test provider  [S] setup  [Q] quit\n", yellow, reset)
	return b.String()
}

type Wizard struct {
	In      io.Reader
	Out     io.Writer
	Persist func(string, string) error
	Client  *http.Client
}

func (w Wizard) Run(ctx context.Context, path string) (Settings, error) {
	if w.In == nil {
		w.In = os.Stdin
	}
	if w.Out == nil {
		w.Out = os.Stdout
	}
	if w.Persist == nil {
		w.Persist = PersistUserEnvironment
	}
	r := bufio.NewReader(w.In)
	fmt.Fprintln(w.Out, "NLWPROXY FIRST-RUN SETUP")
	fmt.Fprintln(w.Out, "Authorized OpenAI-compatible providers only. Secrets are stored in HKCU Environment, never in JSON.")
	ask := func(label, def string) (string, error) {
		fmt.Fprintf(w.Out, "%s [%s]: ", label, def)
		line, e := r.ReadString('\n')
		if e != nil && !errors.Is(e, io.EOF) {
			return "", e
		}
		line = strings.TrimSpace(line)
		if line == "" {
			line = def
		}
		return line, nil
	}
	provider, e := ask("Provider name", "provider")
	if e != nil {
		return Settings{}, e
	}
	base, e := ask("Provider base URL", "https://api.openai.com/v1")
	if e != nil {
		return Settings{}, e
	}
	if u, err := url.Parse(base); err != nil || u.Scheme != "https" || u.Host == "" {
		return Settings{}, errors.New("provider base URL must be HTTPS")
	}
	envDefault := strings.ToUpper(strings.NewReplacer("-", "_", " ", "_").Replace(provider)) + "_API_KEY"
	env, e := ask("API key environment variable", envDefault)
	if e != nil {
		return Settings{}, e
	}
	fmt.Fprint(w.Out, "API key (input may be visible in redirected terminals): ")
	key, e := r.ReadString('\n')
	if e != nil && !errors.Is(e, io.EOF) {
		return Settings{}, e
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return Settings{}, errors.New("API key is required")
	}
	token, e := GenerateToken()
	if e != nil {
		return Settings{}, e
	}
	s := Settings{Provider: provider, BaseURL: base, APIKey: key, APIKeyEnv: env, LocalToken: token, Listen: "127.0.0.1:8787", ModelAlias: "opencode-route"}
	if err := PersistSetup(path, BuildConfig(s), s, w.Persist); err != nil {
		return Settings{}, err
	}
	fmt.Fprintln(w.Out, "Testing provider authorization...")
	if err := TestProvider(ctx, w.Client, base, key); err != nil {
		fmt.Fprintln(w.Out, "Warning: provider test failed:", err)
	} else {
		fmt.Fprintln(w.Out, "Provider test passed.")
	}
	return s, nil
}
