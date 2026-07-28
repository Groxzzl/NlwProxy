package dashboard

import (
	"sort"
	"sync"
	"time"

	"nlwproxy/internal/metrics"
	"nlwproxy/internal/routing"
)

type Config struct {
	Window      time.Duration
	Buckets     int
	RecentLimit int
	History     int
}

type Percentiles struct{ P50, P95 time.Duration }
type Rates struct{ RequestsPerMinute, TokensPerMinute float64 }
type Stats struct {
	Total, Errors, Active                  int64
	InputTokens, OutputTokens, TotalTokens int64
	WindowTotal, WindowErrors              int64
	TTFT, Duration                         Percentiles
}
type Bucket struct {
	Start                    time.Time
	Requests, Errors, Tokens int64
}
type Route struct {
	Stats    Stats
	Snapshot routing.RouteSnapshot
}
type Sparklines struct {
	Requests, Errors, Tokens []float64
}
type Snapshot struct {
	At         time.Time
	Revision   uint64
	Global     Stats
	Models     map[string]Stats
	Routes     map[string]Route
	Active     []metrics.Request
	Recent     []metrics.Request
	Buckets    []Bucket
	Rates      Rates
	Sparklines Sparklines
	Sparkline  string
}

type Aggregator struct {
	mu                       sync.RWMutex
	cfg                      Config
	lastRevision             uint64
	snapshot                 Snapshot
	requests, errors, tokens *Ring[float64]
}

func New(cfg Config) *Aggregator {
	if cfg.Window <= 0 {
		cfg.Window = 5 * time.Minute
	}
	if cfg.Buckets <= 0 {
		cfg.Buckets = 5
	}
	if cfg.RecentLimit <= 0 {
		cfg.RecentLimit = 20
	}
	if cfg.History <= 0 {
		cfg.History = 60
	}
	return &Aggregator{cfg: cfg, requests: NewRing[float64](cfg.History), errors: NewRing[float64](cfg.History), tokens: NewRing[float64](cfg.History), snapshot: Snapshot{Models: map[string]Stats{}, Routes: map[string]Route{}}}
}

func (a *Aggregator) Update(now time.Time, input metrics.Snapshot, routes map[string]routing.RouteSnapshot) Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if input.Revision != 0 && input.Revision == a.lastRevision {
		return clone(a.snapshot)
	}

	windowStart := now.Add(-a.cfg.Window)
	width := a.cfg.Window / time.Duration(a.cfg.Buckets)
	if width <= 0 {
		width = time.Nanosecond
	}
	buckets := make([]Bucket, a.cfg.Buckets)
	for i := range buckets {
		buckets[i].Start = windowStart.Add(time.Duration(i) * width)
	}

	models := map[string]Stats{}
	routeStats := map[string]Stats{}
	var global Stats
	var active []metrics.Request
	var ttfts, durations []time.Duration
	var windowRequests, windowTokens int64
	for _, event := range input.Events {
		if event.State == metrics.RequestActive || event.State == metrics.RequestStreaming {
			active = append(active, event)
		}
		tokens := event.TotalTokens
		if tokens == 0 {
			tokens = event.InputTokens + event.OutputTokens
		}
		addEvent(&global, event, tokens)
		if event.TTFT > 0 {
			ttfts = append(ttfts, event.TTFT)
		}
		if event.Duration > 0 {
			durations = append(durations, event.Duration)
		}
		if event.RequestedModel != "" {
			stat := models[event.RequestedModel]
			addEvent(&stat, event, tokens)
			models[event.RequestedModel] = stat
		}
		if event.RouteID != "" {
			stat := routeStats[event.RouteID]
			addEvent(&stat, event, tokens)
			routeStats[event.RouteID] = stat
		}
		if event.StartedAt.Before(windowStart) || event.StartedAt.After(now) {
			continue
		}
		index := int(event.StartedAt.Sub(windowStart) / width)
		if index >= a.cfg.Buckets {
			index = a.cfg.Buckets - 1
		}
		if index < 0 {
			continue
		}
		buckets[index].Requests++
		windowRequests++
		buckets[index].Tokens += tokens
		windowTokens += tokens
		if failed(event) {
			buckets[index].Errors++
		}
	}
	global.Total, global.Errors, global.Active = input.Total, input.Errors, input.Active
	global.TTFT = Percentiles{Percentile(ttfts, .50), Percentile(ttfts, .95)}
	global.Duration = Percentiles{Percentile(durations, .50), Percentile(durations, .95)}

	viewRoutes := make(map[string]Route, len(routes))
	for name, route := range routes {
		window := routeStats[name]
		viewRoutes[name] = Route{Stats: Stats{Total: route.Total, Errors: route.Errors, Active: route.Active, InputTokens: route.InputTokens, OutputTokens: route.OutputTokens, TotalTokens: route.InputTokens + route.OutputTokens, WindowTotal: window.Total, WindowErrors: window.Errors, TTFT: window.TTFT, Duration: window.Duration}, Snapshot: route}
	}

	recent := append([]metrics.Request(nil), input.Events...)
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].StartedAt.After(recent[j].StartedAt) })
	if len(recent) > a.cfg.RecentLimit {
		recent = recent[:a.cfg.RecentLimit]
	}

	minutes := a.cfg.Window.Minutes()
	rates := Rates{}
	if minutes > 0 {
		rates.RequestsPerMinute = float64(windowRequests) / minutes
		rates.TokensPerMinute = float64(windowTokens) / minutes
	}
	a.requests.Push(float64(input.Total))
	a.errors.Push(float64(input.Errors))
	a.tokens.Push(float64(global.TotalTokens))
	sparks := Sparklines{a.requests.Values(), a.errors.Values(), a.tokens.Values()}
	a.lastRevision = input.Revision
	a.snapshot = Snapshot{At: now, Revision: input.Revision, Global: global, Models: models, Routes: viewRoutes, Active: active, Recent: recent, Buckets: buckets, Rates: rates, Sparklines: sparks, Sparkline: Sparkline(sparks.Requests)}
	return clone(a.snapshot)
}

func addEvent(stat *Stats, event metrics.Request, tokens int64) {
	stat.Total++
	if failed(event) {
		stat.Errors++
	}
	stat.InputTokens += event.InputTokens
	stat.OutputTokens += event.OutputTokens
	stat.TotalTokens += tokens
}
func failed(event metrics.Request) bool { return event.Status >= 400 || event.ErrorCode != "" }

func (a *Aggregator) Snapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return clone(a.snapshot)
}
func clone(in Snapshot) Snapshot {
	out := in
	out.Models = make(map[string]Stats, len(in.Models))
	for k, v := range in.Models {
		out.Models[k] = v
	}
	out.Routes = make(map[string]Route, len(in.Routes))
	for k, v := range in.Routes {
		out.Routes[k] = v
	}
	out.Active = append([]metrics.Request(nil), in.Active...)
	out.Recent = append([]metrics.Request(nil), in.Recent...)
	out.Buckets = append([]Bucket(nil), in.Buckets...)
	out.Sparklines.Requests = append([]float64(nil), in.Sparklines.Requests...)
	out.Sparklines.Errors = append([]float64(nil), in.Sparklines.Errors...)
	out.Sparklines.Tokens = append([]float64(nil), in.Sparklines.Tokens...)
	return out
}
