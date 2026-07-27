package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRenderPlainSnapshot(t *testing.T) {
	var out bytes.Buffer
	d := New(&out)
	d.Color = false
	s := Snapshot{Version: "test", Listen: "127.0.0.1:8787", Strategy: "failover", Started: time.Now().Add(-time.Minute), Requests: 42, Routes: []Route{{Name: "direct", Transport: "direct", State: "healthy", Priority: 10, Requests: 42, Latency: 18 * time.Millisecond, Score: 97}}}
	if err := d.Draw(s); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NLWPROXY", "OPENCODE NETWORK CONTROL", "CONNECTIONS", "127.0.0.1:8787", "direct", "HEALTHY", "42", "18ms", "● healthy", "— disabled"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatal("unexpected ANSI sequence")
	}
}

func TestMinimumWidthAndLineBounds(t *testing.T) {
	d := New(&bytes.Buffer{})
	d.Color = false
	d.Width = 1
	got := d.Render(Snapshot{Routes: []Route{{Name: strings.Repeat("long-route-", 30), State: "auth_required"}}})
	if !strings.Contains(got, "AUTH_REQUIR") || !strings.Contains(got, "× auth required") {
		t.Fatal(got)
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if utf8.RuneCountInString(line) > 78 {
			t.Fatalf("line exceeds minimum width: %d: %q", utf8.RuneCountInString(line), line)
		}
	}
}

func TestStatusSymbolsWithoutColor(t *testing.T) {
	d := New(&bytes.Buffer{})
	d.Color = false
	got := d.Render(Snapshot{Routes: []Route{{Name: "ok", State: "healthy"}, {Name: "slow", State: "degraded"}, {Name: "down", State: "open"}, {Name: "off", State: "disabled"}}})
	for _, want := range []string{"● HEALTHY", "◐ DEGRADED", "○ OPEN", "— DISABLED"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q", want)
		}
	}
}
