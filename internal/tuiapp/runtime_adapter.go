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
	mu         sync.Mutex
}

func NewRuntimeAdapter(runtime *gatewayruntime.GatewayRuntime) *RuntimeAdapter {
	a := &RuntimeAdapter{runtime: runtime, dash: dashboard.New(dashboard.Config{Window: 5 * time.Minute, Buckets: 30, RecentLimit: 500, History: 60})}
	a.dataDir = resolveDataDir()
	a.proxies = proxymanager.New(nil)
	a.ops = &operationsSource{adapter: a}
	a.store = NewStore(runtime.Events(), a.snapshot())
	a.proxies.SetAliveListener(func([]proxymanager.ProxyEntry) {
		_ = a.applyProxyState()
	})
	return a
}

// resolveDataDir returns the persistent per-user data directory regardless of
// the current working directory. NLWPROXY_HOME is the explicit override for
// development and portable installs.
func resolveDataDir() string {
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

func (s proxyManagerSource) Reload(ctx context.Context) error {
	if s.reload == nil {
		return nil
	}
	return s.reload(ctx)
}
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
		cachePath := filepath.Join(a.dataDir, "proxies.json")
		const healthCacheTTL = 6 * time.Hour
		restored, _ := a.proxies.RestoreJSON(cachePath, healthCacheTTL, time.Now())
		total, _ := a.proxies.Count()
		// Only test on startup when no fresh cache entries exist. Fresh saved
		// results are restored immediately; new/uncached entries stay inactive
		// until the user presses T for an explicit refresh.
		if total > 0 && restored == 0 {
			testCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			_ = a.proxies.TestAll(testCtx, "https://www.cloudflare.com/cdn-cgi/trace")
			cancel()
		}
		_ = a.applyProxyState()
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

func (a *RuntimeAdapter) applyProxyState() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	alive := a.proxies.ListAlive()
	sort.SliceStable(alive, func(i, j int) bool { return alive[i].Latency < alive[j].Latency })
	const maxActiveRoutes = 120
	if len(alive) > maxActiveRoutes {
		alive = alive[:maxActiveRoutes]
	}
	if err := a.runtime.ReloadHealthyProxies(alive); err != nil {
		return err
	}
	if err := a.proxies.SaveJSON(filepath.Join(a.dataDir, "proxies.json")); err != nil {
		return err
	}
	a.store.Set(a.snapshot())
	return nil
}

// Reload persists freshly tested proxy state and hot-reloads the selector. It
// does not restart the listener, so in-memory request telemetry remains intact.
func (a *RuntimeAdapter) Reload(context.Context) error { return a.applyProxyState() }

func (a *RuntimeAdapter) dashboardSnapshot() dashboard.Snapshot {
	raw := a.runtime.Snapshot()
	metricsSnapshot := raw.Metrics
	entries := a.proxies.List()
	byID := make(map[string]proxymanager.ProxyEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for i := range metricsSnapshot.Events {
		event := &metricsSnapshot.Events[i]
		at := strings.LastIndex(event.RouteID, "@")
		if at < 0 {
			continue
		}
		proxyID := event.RouteID[at+1:]
		entry, ok := byID[proxyID]
		if !ok {
			continue
		}
		event.ProxyID = proxyID
		event.ProxyCountry = entry.Geo.Country
		event.ProxyCity = entry.Geo.City
		event.ProxyASN = entry.Geo.ASN
	}
	return a.dash.Update(time.Now(), metricsSnapshot, raw.Routes)
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
	dash := a.dashboardSnapshot()
	proxyStats := a.proxies.Stats()
	result := Snapshot{Profile: raw.Profile.Name, Gateway: "http://" + raw.Listen + "/v1", Status: status, Notice: raw.Error, Requests: dash.Global.Total, Errors: dash.Global.Errors, Active: dash.Global.Active, Connections: len(raw.Routes), Models: len(dash.Models), InputTokens: dash.Global.InputTokens, OutputTokens: dash.Global.OutputTokens, ProxyOnly: strings.EqualFold(raw.Profile.ID, "reffaunlimited"), HealthyProxies: proxyStats.Healthy}
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
