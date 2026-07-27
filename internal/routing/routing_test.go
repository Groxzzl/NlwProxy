package routing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRouteSnapshotTracksRequestsErrorsLatencyTokensAndTransport(t *testing.T) {
	s := New([]Target{{Name: "proxy", TransportType: "socks5"}}, Config{})
	release, ok := s.Acquire("proxy")
	if !ok || s.Snapshots(time.Now())["proxy"].Active != 1 {
		t.Fatal("route was not marked active")
	}
	release()
	now := time.Now()
	s.RecordRequest("proxy", RequestResult{Status: 200, Latency: 20 * time.Millisecond, InputTokens: 7, OutputTokens: 11, UsedAt: now})
	s.RecordRequest("proxy", RequestResult{Status: 503, Latency: 40 * time.Millisecond, UsedAt: now.Add(time.Second)})
	got := s.Snapshots(now.Add(time.Second))["proxy"]
	if got.Total != 2 || got.Errors != 1 || got.Latency != 30*time.Millisecond || got.InputTokens != 7 || got.OutputTokens != 11 || got.Transport != "socks5" || !got.LastUsed.Equal(now.Add(time.Second)) || got.Circuit != CircuitClosed {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestExitIPProbeUsesRouteTransportAndCachesResult(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{"ip":"203.0.113.9"}`) }))
	defer srv.Close()
	rt := testRoundTripper(func(r *http.Request) (*http.Response, error) { return http.DefaultTransport.RoundTrip(r) })
	s := New([]Target{{Name: "direct", TransportType: "direct", Transport: rt}}, Config{ExitIPProbe: ExitIPProbeConfig{Enabled: true, URL: srv.URL, Timeout: time.Second, CacheTTL: time.Minute}})
	first := s.ProbeExitIP(context.Background(), "direct", time.Now())
	second := s.ProbeExitIP(context.Background(), "direct", time.Now().Add(time.Second))
	if first.IP != "203.0.113.9" || first.Error != "" || second.IP != first.IP || calls != 1 {
		t.Fatalf("first=%+v second=%+v calls=%d", first, second, calls)
	}
}

func TestExitIPProbeIsDisabledAndNeverRunsFromRequestRecording(t *testing.T) {
	var calls int
	s := New([]Target{{Name: "direct", Transport: testRoundTripper(func(*http.Request) (*http.Response, error) { calls++; return nil, nil })}}, Config{})
	s.RecordRequest("direct", RequestResult{Status: 200})
	got := s.ProbeExitIP(context.Background(), "direct", time.Now())
	if calls != 0 || got.Error != "disabled" {
		t.Fatalf("calls=%d probe=%+v", calls, got)
	}
}

func TestSelectorUsesPriorityThenLowestLatency(t *testing.T) {
	now := time.Now()
	s := NewSelector([]Target{
		{Name: "slow-high", Priority: 1, Latency: 80 * time.Millisecond},
		{Name: "fast-low", Priority: 2, Latency: time.Millisecond},
		{Name: "fast-high", Priority: 1, Latency: 10 * time.Millisecond},
	}, BreakerConfig{FailureThreshold: 2, OpenTimeout: time.Minute})

	got, ok := s.Next(now, nil)
	if !ok || got.Name != "fast-high" {
		t.Fatalf("Next()=(%q,%v), want fast-high,true", got.Name, ok)
	}
}

func TestSelectorTracksHealthAndCircuitBreaker(t *testing.T) {
	now := time.Now()
	s := NewSelector([]Target{
		{Name: "primary", Priority: 1},
		{Name: "backup", Priority: 2},
	}, BreakerConfig{FailureThreshold: 2, OpenTimeout: time.Minute})

	s.SetHealth("primary", Unhealthy, 0)
	got, _ := s.Next(now, nil)
	if got.Name != "backup" {
		t.Fatalf("unhealthy target selected: %q", got.Name)
	}

	s.SetHealth("primary", Healthy, 5*time.Millisecond)
	s.RecordFailure("primary", now)
	s.RecordFailure("primary", now)
	got, _ = s.Next(now, nil)
	if got.Name != "backup" {
		t.Fatalf("open circuit target selected: %q", got.Name)
	}

	got, ok := s.Next(now.Add(time.Minute), nil)
	if !ok || got.Name != "primary" {
		t.Fatalf("half-open target not selected: %q, %v", got.Name, ok)
	}
	s.RecordSuccess("primary", 3*time.Millisecond)
	if states := s.States(time.Now()); states["primary"].Circuit != CircuitClosed {
		t.Fatalf("circuit=%q", states["primary"].Circuit)
	}
}

func TestNextExcludesAttemptedTargets(t *testing.T) {
	s := NewSelector([]Target{{Name: "a"}, {Name: "b"}}, BreakerConfig{})
	got, ok := s.Next(time.Now(), map[string]bool{"a": true})
	if !ok || got.Name != "b" {
		t.Fatalf("Next()=(%q,%v), want b,true", got.Name, ok)
	}
}
