package tuiapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"nlwproxy/internal/metrics"
	"nlwproxy/internal/proxyimport"
	"nlwproxy/internal/proxymanager"
	gatewayruntime "nlwproxy/internal/runtime"
	"nlwproxy/internal/tuiapp/dashboard"
	"nlwproxy/internal/tuiapp/pages"
)

// RuntimeAdapter maps the reusable gateway runtime into presentation-safe live
// snapshots and an operations dashboard source.
type RuntimeAdapter struct {
	runtime    *gatewayruntime.GatewayRuntime
	store      *Store
	dash       *dashboard.Aggregator
	ops        *operationsSource
	proxies    *proxymanager.Manager
	dataDir    string
	importOnce sync.Once
}

func NewRuntimeAdapter(runtime *gatewayruntime.GatewayRuntime) *RuntimeAdapter {
	a := &RuntimeAdapter{runtime: runtime, dash: dashboard.New(dashboard.Config{Window: 5 * time.Minute, Buckets: 30, RecentLimit: 500, History: 60})}
	a.dataDir = resolveDataDir()
	a.proxies = proxymanager.New(nil)
	a.ops = &operationsSource{adapter: a}
	a.store = NewStore(runtime.Events(), a.snapshot())
	a.proxies.SetAliveListener(func(entries []proxymanager.ProxyEntry) {
		_ = runtime.ReloadHealthyProxies(entries)
	})
	return a
}

// resolveDataDir returns the directory that holds proxy files. It prefers a
// local ./data (developer checkout) and otherwise uses the per-user home dir
// (%APPDATA%\nlwproxy\data or ~/.config/nlwproxy/data, overridable via
// NLWPROXY_HOME), so `nlwproxy` finds the same proxies from any working dir.
func resolveDataDir() string {
	if fi, err := os.Stat("data"); err == nil && fi.IsDir() {
		return "data"
	}
	if h := os.Getenv("NLWPROXY_HOME"); h != "" {
		return filepath.Join(h, "data")
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "nlwproxy", "data")
	}
	return "data"
}

func (a *RuntimeAdapter) Source() StateSource {
	return ContextSource{SnapshotFunc: a.snapshot, ChangesFunc: a.store.Changes}
}
func (a *RuntimeAdapter) Operations() pages.OperationsSource { return a.ops }
func (a *RuntimeAdapter) Proxies() pages.ProxyManagerSource {
	return proxyManagerSource{Manager: a.proxies, reload: a.Reload}
}
func (a *RuntimeAdapter) ProxyManager() *proxymanager.Manager { return a.proxies }

type proxyManagerSource struct {
	*proxymanager.Manager
	reload func(context.Context) error
}

func (s proxyManagerSource) Reload(ctx context.Context) error { return s.reload(ctx) }
func (a *RuntimeAdapter) Start(ctx context.Context) error {
	a.importOnce.Do(func() {
		// Public scraping is opt-in. A verified local pool must not be mixed with
		// fresh untested GitHub entries every time the gateway restarts.
		if strings.EqualFold(os.Getenv("NLWPROXY_SCRAPE_PUBLIC"), "true") || os.Getenv("NLWPROXY_SCRAPE_PUBLIC") == "1" {
			func() {
				scrapeCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				results := proxyimport.ScrapeAll(scrapeCtx)
				proxies := proxyimport.Deduplicate(proxyimport.FlattenResults(results))
				const maxScraped = 200
				if len(proxies) > maxScraped {
					proxies = proxies[:maxScraped]
				}
				if len(proxies) == 0 {
					return
				}
				var sb strings.Builder
				for _, p := range proxies {
					sb.WriteString(p.URL())
					sb.WriteByte('\n')
				}
				dir := filepath.Join(a.dataDir, "proxies")
				_ = os.MkdirAll(dir, 0o755)
				_ = os.WriteFile(filepath.Join(dir, "github-auto.txt"), []byte(sb.String()), 0o600)
			}()
		}

		// Multi-file loader: auto-import every *.txt under <dataDir>/proxies/,
		// deduped by host:port, then persist the merged set to <dataDir>/proxies.json.
		proxyDir := filepath.Join(a.dataDir, "proxies")
		added, _ := a.proxies.LoadDir(proxyDir)

		// Fallback single-file import for backward compatibility.
		if added == 0 {
			candidates := []string{
				filepath.Join(a.dataDir, "webshare-proxies.txt"),
				filepath.Join(os.Getenv("USERPROFILE"), "Downloads", "Webshare 10 proxies.txt"),
			}
			for _, path := range candidates {
				if _, err := os.Stat(path); err == nil {
					_, _ = a.proxies.ImportFile(path)
					break
				}
			}
		}
		// Re-test imported entries at startup so proxy-only mode has usable routes
		// without requiring the user to open the Proxies page and press T first.
		testCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_ = a.proxies.TestAll(testCtx, "https://www.cloudflare.com/cdn-cgi/trace")
		cancel()
		alive := a.proxies.ListAlive()
		sort.SliceStable(alive, func(i, j int) bool { return alive[i].Latency < alive[j].Latency })
		const maxActiveRoutes = 120
		if len(alive) > maxActiveRoutes {
			alive = alive[:maxActiveRoutes]
		}
		_ = a.runtime.ReloadHealthyProxies(alive)
		_ = a.proxies.SaveJSON(filepath.Join(a.dataDir, "proxies.json"))
	})
	if err := a.runtime.Start(ctx); err != nil {
		a.store.Set(a.snapshot())
		return err
	}
	a.store.Set(a.snapshot())
	return nil
}
func (a *RuntimeAdapter) Stop(ctx context.Context) error {
	err := a.runtime.Stop(ctx)
	a.store.Set(a.snapshot())
	return err
}

// Reload applies the newly tested healthy proxy set to the managed runtime.
func (a *RuntimeAdapter) Reload(ctx context.Context) error {
	if err := a.runtime.Stop(ctx); err != nil {
		return err
	}
	if err := a.runtime.Start(ctx); err != nil {
		return err
	}
	a.store.Set(a.snapshot())
	return nil
}

func (a *RuntimeAdapter) dashboardSnapshot() dashboard.Snapshot {
	raw := a.runtime.Snapshot()
	return a.dash.Update(time.Now(), raw.Metrics, raw.Routes)
}

func (a *RuntimeAdapter) snapshot() Snapshot {
	if a == nil || a.runtime == nil {
		return Snapshot{}
	}
	raw := a.runtime.Snapshot()
	status := string(raw.State)
	if raw.State == "running" {
		status = "ONLINE"
	}
	dash := a.dash.Update(time.Now(), raw.Metrics, raw.Routes)
	_, healthy := a.proxies.Count()
	result := Snapshot{Profile: raw.Profile.Name, Gateway: "http://" + raw.Listen + "/v1", Status: status, Notice: raw.Error, Requests: dash.Global.Total, Errors: dash.Global.Errors, Active: dash.Global.Active, Connections: len(raw.Routes), Models: len(dash.Models), InputTokens: dash.Global.InputTokens, OutputTokens: dash.Global.OutputTokens, ProxyOnly: strings.EqualFold(raw.Profile.ID, "reffaunlimited"), HealthyProxies: healthy}
	if alive := a.proxies.ListAlive(); len(alive) > 0 {
		result.ActiveProxy = fmt.Sprintf("%s:%d", alive[0].Host, alive[0].Port)
		result.ProxyCountry = alive[0].Geo.Country
	}
	for name, route := range raw.Routes {
		result.Routes = append(result.Routes, Route{Name: name, Health: string(route.Health), Circuit: string(route.Circuit), Transport: route.Transport, Requests: route.Total, Errors: route.Errors, InputTokens: route.InputTokens, OutputTokens: route.OutputTokens})
	}
	for _, event := range raw.Metrics.Events {
		result.Events = append(result.Events, Event{RequestID: event.RequestID, RouteID: event.RouteID, Model: event.RequestedModel, Endpoint: event.Endpoint, ErrorCode: event.ErrorCode, Status: event.Status, State: string(event.State), StartedAt: event.StartedAt, TTFT: event.TTFT, Duration: event.Duration, RetryCount: event.RetryCount, TotalTokens: event.TotalTokens, InputTokens: event.InputTokens, OutputTokens: event.OutputTokens})
	}
	return result
}

type operationsSource struct {
	adapter *RuntimeAdapter
	mu      sync.Mutex
}

func (o *operationsSource) Snapshot() pages.OperationsSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	d := o.adapter.dashboardSnapshot()
	models := make(map[string]pages.OperationStats, len(d.Models))
	for name, s := range d.Models {
		models[name] = toPageStats(s)
	}
	routes := make(map[string]pages.OperationStats, len(d.Routes))
	for name, r := range d.Routes {
		routes[name] = toPageStats(r.Stats)
	}
	latency := make([]float64, 0, len(d.Recent))
	for i := len(d.Recent) - 1; i >= 0; i-- {
		latency = append(latency, float64(d.Recent[i].TTFT.Milliseconds()))
	}
	return pages.OperationsSnapshot{Revision: d.Revision, Status: o.adapter.snapshot().Status, Global: toPageStats(d.Global), Models: models, Routes: routes, Recent: append([]metrics.Request(nil), d.Recent...), Active: append([]metrics.Request(nil), d.Active...), RequestsPerMinute: d.Rates.RequestsPerMinute, TokensPerMinute: d.Rates.TokensPerMinute, RequestSparkline: append([]float64(nil), d.Sparklines.Requests...), LatencySparkline: latency}
}
func toPageStats(s dashboard.Stats) pages.OperationStats {
	return pages.OperationStats{Total: s.Total, Errors: s.Errors, Active: s.Active, InputTokens: s.InputTokens, OutputTokens: s.OutputTokens, TTFTP50: s.TTFT.P50, TTFTP95: s.TTFT.P95, DurationP50: s.Duration.P50, DurationP95: s.Duration.P95}
}

var _ Lifecycle = (*RuntimeAdapter)(nil)
