package operations

import (
	"time"

	"nlwproxy/internal/metrics"
	"nlwproxy/internal/routing"
	"nlwproxy/internal/tuiapp/dashboard"
	"nlwproxy/internal/tuiapp/pages"
)

type MetricsSource interface{ Snapshot() metrics.Snapshot }
type RouteSource interface {
	Snapshots(time.Time) map[string]routing.RouteSnapshot
}

// Aggregator adapts live metrics and route snapshots to the UI-only operations contract.
type Aggregator struct {
	Metrics   MetricsSource
	Routes    RouteSource
	Dashboard *dashboard.Aggregator
	Now       func() time.Time
}

func New(metricsSource MetricsSource, routeSource RouteSource) *Aggregator {
	return &Aggregator{Metrics: metricsSource, Routes: routeSource, Dashboard: dashboard.New(dashboard.Config{Window: 5 * time.Minute, Buckets: 20, RecentLimit: 100, History: 32}), Now: time.Now}
}

func (a *Aggregator) Snapshot() pages.OperationsSnapshot {
	if a == nil {
		return pages.OperationsSnapshot{}
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	ms := metrics.Snapshot{}
	if a.Metrics != nil {
		ms = a.Metrics.Snapshot()
	}
	routes := map[string]routing.RouteSnapshot{}
	if a.Routes != nil {
		routes = a.Routes.Snapshots(now)
	}
	if a.Dashboard == nil {
		a.Dashboard = dashboard.New(dashboard.Config{})
	}
	s := a.Dashboard.Update(now, ms, routes)
	latency := make([]float64, 0, len(s.Buckets))
	for _, bucket := range s.Buckets {
		latency = append(latency, float64(bucket.Requests))
	}
	out := pages.OperationsSnapshot{Revision: s.Revision, Recent: append([]metrics.Request(nil), s.Recent...), RequestsPerMinute: s.Rates.RequestsPerMinute, TokensPerMinute: s.Rates.TokensPerMinute, RequestSparkline: append([]float64(nil), s.Sparklines.Requests...), LatencySparkline: latency, Models: map[string]pages.OperationStats{}, Routes: map[string]pages.OperationStats{}}
	out.Global = convert(s.Global)
	for name, st := range s.Models {
		out.Models[name] = convert(st)
	}
	for name, route := range s.Routes {
		st := convert(route.Stats)
		st.Latency = route.Snapshot.Latency
		st.Health = string(route.Snapshot.Health)
		st.Circuit = string(route.Snapshot.Circuit)
		st.CooldownUntil = route.Snapshot.CooldownUntil
		out.Routes[name] = st
	}
	return out
}
func convert(s dashboard.Stats) pages.OperationStats {
	return pages.OperationStats{Total: s.Total, Errors: s.Errors, Active: s.Active, InputTokens: s.InputTokens, OutputTokens: s.OutputTokens, TTFTP50: s.TTFT.P50, TTFTP95: s.TTFT.P95, DurationP50: s.Duration.P50, DurationP95: s.Duration.P95}
}

var _ pages.OperationsSource = (*Aggregator)(nil)
