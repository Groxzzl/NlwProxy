package dashboard

import (
	"reflect"
	"testing"
	"time"

	"nlwproxy/internal/metrics"
	"nlwproxy/internal/routing"
)

func TestAggregatorBuildsGlobalModelRouteAndRecentViews(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 5, 30, 0, time.UTC)
	events := metrics.Snapshot{
		Total: 20, Errors: 3, Active: 2, Revision: 9,
		Events: []metrics.Request{
			{RequestID: "one", RouteID: "direct", RequestedModel: "gpt-5", Status: 200, StartedAt: now.Add(-90 * time.Second), InputTokens: 10, OutputTokens: 20, TotalTokens: 30, TTFT: 100 * time.Millisecond, Duration: time.Second},
			{RequestID: "two", RouteID: "proxy", RequestedModel: "gpt-5", Status: 500, StartedAt: now.Add(-30 * time.Second), InputTokens: 5, OutputTokens: 5, TotalTokens: 10, TTFT: 300 * time.Millisecond, Duration: 3 * time.Second},
			{RequestID: "three", RouteID: "direct", RequestedModel: "claude", Status: 200, StartedAt: now.Add(-10 * time.Second), InputTokens: 7, OutputTokens: 13, TTFT: 200 * time.Millisecond, Duration: 2 * time.Second},
		},
	}
	routes := map[string]routing.RouteSnapshot{
		"direct": {Total: 12, Errors: 1, Active: 1, InputTokens: 100, OutputTokens: 200, Health: routing.Healthy},
		"proxy":  {Total: 8, Errors: 2, Active: 1, InputTokens: 50, OutputTokens: 75, Health: routing.Degraded},
	}

	a := New(Config{Window: 2 * time.Minute, Buckets: 2, RecentLimit: 2, History: 4})
	got := a.Update(now, events, routes)

	if got.Revision != 9 || got.Global.Total != 20 || got.Global.Errors != 3 || got.Global.Active != 2 || got.Global.InputTokens != 22 || got.Global.OutputTokens != 38 || got.Global.TotalTokens != 60 {
		t.Fatalf("global=%+v revision=%d", got.Global, got.Revision)
	}
	if got.Global.TTFT != (Percentiles{P50: 200 * time.Millisecond, P95: 300 * time.Millisecond}) || got.Global.Duration != (Percentiles{P50: 2 * time.Second, P95: 3 * time.Second}) {
		t.Fatalf("latency global=%+v", got.Global)
	}
	if got.Models["gpt-5"].Total != 2 || got.Models["gpt-5"].Errors != 1 || got.Models["claude"].TotalTokens != 20 {
		t.Fatalf("models=%+v", got.Models)
	}
	if got.Routes["direct"].Stats.Total != 12 || got.Routes["direct"].Stats.WindowTotal != 2 || got.Routes["proxy"].Stats.WindowErrors != 1 {
		t.Fatalf("routes=%+v", got.Routes)
	}
	if len(got.Recent) != 2 || got.Recent[0].RequestID != "three" || got.Recent[1].RequestID != "two" {
		t.Fatalf("recent=%+v", got.Recent)
	}
	if got.Rates.RequestsPerMinute != 1.5 || got.Rates.TokensPerMinute != 30 {
		t.Fatalf("rates=%+v", got.Rates)
	}
	if got.Buckets[0].Requests != 1 || got.Buckets[1].Requests != 2 || got.Buckets[1].Errors != 1 || got.Buckets[1].Tokens != 30 {
		t.Fatalf("buckets=%+v", got.Buckets)
	}
}

func TestAggregatorDeduplicatesSnapshotsAndMaintainsBoundedHistory(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	a := New(Config{Window: time.Minute, Buckets: 4, History: 3})

	first := a.Update(now, metrics.Snapshot{Total: 1, Revision: 1}, nil)
	duplicate := a.Update(now.Add(time.Second), metrics.Snapshot{Total: 99, Revision: 1}, nil)
	if duplicate.Global.Total != first.Global.Total || len(duplicate.Sparklines.Requests) != 1 {
		t.Fatalf("duplicate changed snapshot: %+v", duplicate)
	}
	for i := 2; i <= 5; i++ {
		a.Update(now.Add(time.Duration(i)*time.Second), metrics.Snapshot{Total: int64(i), Revision: uint64(i)}, nil)
	}
	got := a.Snapshot()
	if !reflect.DeepEqual(got.Sparklines.Requests, []float64{3, 4, 5}) {
		t.Fatalf("requests sparkline=%v", got.Sparklines.Requests)
	}
	if got.Sparkline != "▁▅█" {
		t.Fatalf("sparkline=%q", got.Sparkline)
	}
}

func TestRingPercentileAndSparklineEdgeCases(t *testing.T) {
	r := NewRing[int](3)
	for _, value := range []int{1, 2, 3, 4, 5} {
		r.Push(value)
	}
	if got := r.Values(); !reflect.DeepEqual(got, []int{3, 4, 5}) {
		t.Fatalf("ring=%v", got)
	}
	if Percentile(nil, .95) != 0 || Percentile([]time.Duration{4 * time.Millisecond, time.Millisecond, 3 * time.Millisecond, 2 * time.Millisecond}, .50) != 2*time.Millisecond || Percentile([]time.Duration{4 * time.Millisecond, time.Millisecond, 3 * time.Millisecond, 2 * time.Millisecond}, .95) != 4*time.Millisecond {
		t.Fatal("percentile mismatch")
	}
	if Sparkline(nil) != "" || Sparkline([]float64{7, 7, 7}) != "▁▁▁" || Sparkline([]float64{0, 10, 20}) != "▁▅█" {
		t.Fatal("sparkline mismatch")
	}
}

func TestSnapshotIsDefensiveCopy(t *testing.T) {
	a := New(Config{History: 2})
	a.Update(time.Now(), metrics.Snapshot{Revision: 1, Events: []metrics.Request{{RequestedModel: "m"}}}, nil)
	one := a.Snapshot()
	one.Models["m"] = Stats{Total: 99}
	one.Sparklines.Requests[0] = 99
	two := a.Snapshot()
	if two.Models["m"].Total == 99 || two.Sparklines.Requests[0] == 99 {
		t.Fatal("snapshot aliases internal state")
	}
}
