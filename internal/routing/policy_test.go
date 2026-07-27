package routing

import (
	"testing"
	"time"
)

func TestStrategies(t *testing.T) {
	targets := []Target{{Name: "a", Priority: 2, Latency: 30 * time.Millisecond}, {Name: "b", Priority: 1, Latency: 20 * time.Millisecond}, {Name: "c", Priority: 1, Latency: 10 * time.Millisecond}}
	cases := []struct {
		strategy Strategy
		want     string
	}{{Priority, "c"}, {LowestLatency, "c"}, {LeastActive, "a"}}
	for _, tc := range cases {
		t.Run(string(tc.strategy), func(t *testing.T) {
			s := New(targets, Config{Strategy: tc.strategy})
			got, ok := s.NextSession(time.Now(), nil, "")
			if !ok || got.Name != tc.want {
				t.Fatalf("got=%s,%v", got.Name, ok)
			}
		})
	}
}

func TestRoundRobinUsesEligibleTargets(t *testing.T) {
	s := New([]Target{{Name: "a"}, {Name: "b"}}, Config{Strategy: RoundRobin})
	first, _ := s.NextSession(time.Now(), nil, "")
	second, _ := s.NextSession(time.Now(), nil, "")
	if first.Name == second.Name {
		t.Fatalf("did not rotate: %s %s", first.Name, second.Name)
	}
	s.SetHealth(first.Name, Unhealthy, 0)
	for i := 0; i < 3; i++ {
		got, _ := s.NextSession(time.Now(), nil, "")
		if got.Name == first.Name {
			t.Fatal("selected unhealthy route")
		}
	}
}

func TestStickySessionExpires(t *testing.T) {
	now := time.Now()
	s := New([]Target{{Name: "a"}, {Name: "b"}}, Config{Strategy: RoundRobin, StickyTTL: time.Second})
	first, _ := s.NextSession(now, nil, "session")
	same, _ := s.NextSession(now.Add(time.Millisecond), nil, "session")
	if first.Name != same.Name {
		t.Fatalf("sticky changed: %s %s", first.Name, same.Name)
	}
	after, _ := s.NextSession(now.Add(2*time.Second), nil, "session")
	if after.Name == first.Name {
		t.Fatalf("expired sticky did not return to policy: %s", after.Name)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	s := New([]Target{{Name: "a", Priority: 1, MaxConcurrency: 1}, {Name: "b", Priority: 2}}, Config{Strategy: Priority})
	a, _ := s.NextSession(time.Now(), nil, "")
	if a.Name != "a" {
		t.Fatal(a.Name)
	}
	release, ok := s.Acquire("a")
	if !ok {
		t.Fatal("acquire")
	}
	defer release()
	b, _ := s.NextSession(time.Now(), nil, "")
	if b.Name != "b" {
		t.Fatalf("got %s", b.Name)
	}
}

func TestEWMALatency(t *testing.T) {
	s := New([]Target{{Name: "a", Latency: 100 * time.Millisecond}}, Config{EWMAAlpha: .25})
	s.RecordSuccess("a", 20*time.Millisecond)
	got := s.States(time.Now())["a"].Latency
	if got != 80*time.Millisecond {
		t.Fatalf("EWMA=%s", got)
	}
}

func TestHalfOpenNeedsConfiguredRecoverySuccesses(t *testing.T) {
	now := time.Now()
	s := New([]Target{{Name: "a"}}, Config{Breaker: BreakerConfig{FailureThreshold: 1, OpenTimeout: time.Second, RecoverySuccesses: 2}})
	s.RecordFailure("a", now)
	_, ok := s.NextSession(now.Add(time.Second), nil, "")
	if !ok {
		t.Fatal("missing half-open probe")
	}
	s.RecordSuccess("a", time.Millisecond)
	if got := s.States(now)["a"].Circuit; got != CircuitHalfOpen {
		t.Fatalf("closed early: %s", got)
	}
	s.RecordSuccess("a", time.Millisecond)
	if got := s.States(now)["a"].Circuit; got != CircuitClosed {
		t.Fatalf("not closed: %s", got)
	}
}
