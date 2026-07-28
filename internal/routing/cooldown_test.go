package routing

import (
	"testing"
	"time"
)

func TestCooldownSkipsRouteUntilRecovery(t *testing.T) {
	now := time.Now()
	s := NewSelector([]Target{
		{Name: "a", Priority: 1, Transport: nil},
		{Name: "b", Priority: 2, Transport: nil},
	}, BreakerConfig{})
	s.SetHealth("a", Healthy, 0)
	s.SetHealth("b", Healthy, 0)

	// Cool down "a" for 1h; every pick should return "b".
	s.SetCooldown("a", now, time.Hour)
	for i := 0; i < 5; i++ {
		tgt, ok := s.Next(now, map[string]bool{})
		if !ok || tgt.Name != "b" {
			t.Fatalf("expected b while a cooled down, got %q ok=%v", tgt.Name, ok)
		}
	}

	// After cooldown expires, "a" is eligible again.
	tgt, ok := s.Next(now.Add(2*time.Hour), map[string]bool{"b": true})
	if !ok || tgt.Name != "a" {
		t.Fatalf("expected a after cooldown, got %q ok=%v", tgt.Name, ok)
	}
}

func TestSoonestRecoveryReturnsEarliest(t *testing.T) {
	now := time.Now()
	s := NewSelector([]Target{{Name: "a"}, {Name: "b"}}, BreakerConfig{})
	s.SetCooldown("a", now, 2*time.Hour)
	s.SetCooldown("b", now, 30*time.Minute)
	got := s.SoonestRecovery(now)
	want := now.Add(30 * time.Minute)
	if got.Sub(want) > time.Second || want.Sub(got) > time.Second {
		t.Fatalf("soonest=%v want=%v", got, want)
	}
}

func TestAllCooledDownReturnsNoRoute(t *testing.T) {
	now := time.Now()
	s := NewSelector([]Target{{Name: "a"}, {Name: "b"}}, BreakerConfig{})
	s.SetCooldown("a", now, time.Hour)
	s.SetCooldown("b", now, time.Hour)
	if _, ok := s.Next(now, map[string]bool{}); ok {
		t.Fatal("expected no eligible route when all cooled down")
	}
}
