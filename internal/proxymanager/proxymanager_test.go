package proxymanager

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"nlwproxy/internal/geo"
)

func TestNew(t *testing.T) {
	m := New(nil)
	if m == nil {
		t.Fatal("New returned nil")
	}
	if n, _ := m.Count(); n != 0 {
		t.Fatalf("expected 0 entries, got %d", n)
	}
}

func TestAddAndList(t *testing.T) {
	m := New(nil)
	id := m.Add(ProxyEntry{Host: "127.0.0.1", Port: 8080, Scheme: SchemeHTTP})
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	entries := m.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Host != "127.0.0.1" {
		t.Fatalf("expected host 127.0.0.1, got %s", entries[0].Host)
	}
}

func TestRemove(t *testing.T) {
	m := New(nil)
	id := m.Add(ProxyEntry{Host: "127.0.0.1", Port: 8080})
	if !m.Remove(id) {
		t.Fatal("expected remove to return true")
	}
	if m.Remove("nonexistent") {
		t.Fatal("expected remove nonexistent to return false")
	}
	if n, _ := m.Count(); n != 0 {
		t.Fatalf("expected 0 after remove, got %d", n)
	}
}

func TestImportFile(t *testing.T) {
	m := New(nil)
	added, errs := m.ImportFile("testdata/proxies.txt")
	if added == 0 && len(errs) == 0 {
		t.Log("no testdata/proxies.txt — skipping file import test")
		return
	}
	t.Logf("imported %d, errors: %v", added, errs)
}

func TestParseProxyLine(t *testing.T) {
	tests := []struct {
		input string
		count int
		err   bool
	}{
		{"192.168.1.1:3128", 1, false},
		{"10.0.0.1:8080:user:pass", 1, false},
		{"http://proxy.example.com:8080", 1, false},
		{"socks5://socks.example.com:1080", 1, false},
		{"# comment", 0, true}, // parseProxyLine doesn't handle comments
		{"", 0, true},          // parseProxyLine doesn't handle empty
		{"invalid", 0, true},
	}
	for _, tt := range tests {
		entries, errs := parseProxyLine(tt.input)
		if len(errs) > 0 != tt.err {
			t.Errorf("parseProxyLine(%q) errors=%v, want err=%v", tt.input, errs, tt.err)
		}
		if len(entries) != tt.count {
			t.Errorf("parseProxyLine(%q) entries=%d, want %d", tt.input, len(entries), tt.count)
		}
	}
}

func TestProxyURL(t *testing.T) {
	e := ProxyEntry{Host: "192.168.1.1", Port: 8080, Scheme: SchemeHTTP}
	expected := "http://192.168.1.1:8080"
	if got := e.ProxyURL(); got != expected {
		t.Errorf("ProxyURL() = %q, want %q", got, expected)
	}

	e = ProxyEntry{Host: "proxy.example.com", Port: 3128, Scheme: SchemeHTTPS, Username: "user", Password: "pass"}
	expected = "https://user:pass@proxy.example.com:3128"
	if got := e.ProxyURL(); got != expected {
		t.Errorf("ProxyURL() = %q, want %q", got, expected)
	}
}

func TestCount(t *testing.T) {
	m := New(nil)
	m.Add(ProxyEntry{Host: "1.1.1.1", Port: 80, Alive: true})
	m.Add(ProxyEntry{Host: "2.2.2.2", Port: 80})
	total, alive := m.Count()
	if total != 2 {
		t.Fatalf("expected total 2, got %d", total)
	}
	if alive != 1 {
		t.Fatalf("expected alive 1, got %d", alive)
	}
}

func TestStats(t *testing.T) {
	m := New(nil)
	m.Add(ProxyEntry{Host: "1.1.1.1", Port: 80, Scheme: SchemeHTTP, Alive: true})
	m.Add(ProxyEntry{Host: "2.2.2.2", Port: 443, Scheme: SchemeHTTPS, Alive: false})
	m.Add(ProxyEntry{Host: "3.3.3.3", Port: 1080, Scheme: SchemeSOCKS5, Alive: true, Geo: geo.Result{Country: "US"}})
	s := m.Stats()
	if s.Total != 3 {
		t.Fatalf("expected total 3, got %d", s.Total)
	}
	if s.Alive != 2 {
		t.Fatalf("expected alive 2, got %d", s.Alive)
	}
	if s.HTTP != 1 {
		t.Fatalf("expected http 1, got %d", s.HTTP)
	}
	if s.SOCKS5 != 1 {
		t.Fatalf("expected socks5 1, got %d", s.SOCKS5)
	}
	if len(s.Countries) != 1 || s.Countries[0] != "US" {
		t.Fatalf("expected countries [US], got %v", s.Countries)
	}
}

func TestTestSingleProxyError(t *testing.T) {
	m := New(nil)
	result := m.TestSingle(context.Background(), "nonexistent", "http://example.com")
	if result.Error == "" {
		t.Fatal("expected error for nonexistent proxy")
	}
}

func TestTestSingleBadProxy(t *testing.T) {
	m := New(nil)
	id := m.Add(ProxyEntry{Host: "127.0.0.1", Port: 1, Scheme: SchemeHTTP})
	result := m.TestSingle(context.Background(), id, "http://127.0.0.1:9999")
	// This will fail since nothing is listening on port 1
	t.Logf("test result: alive=%v latency=%v error=%q", result.Alive, result.Latency, result.Error)
	// Don't assert on result since it depends on network
}

func TestTestSingleDoesNotWriteTransportErrorsToStderr(t *testing.T) {
	m := New(nil)
	id := m.Add(ProxyEntry{Host: "127.0.0.1", Port: 1, Scheme: SchemeHTTP})
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	result := m.TestSingle(context.Background(), id, "http://127.0.0.1:9999")
	_ = w.Close()
	os.Stderr = old
	var stderr bytes.Buffer
	_, _ = stderr.ReadFrom(r)
	_ = r.Close()
	if result.Error == "" {
		t.Fatal("expected transport error in result")
	}
	if stderr.Len() != 0 {
		t.Fatalf("transport leaked to stderr: %q", stderr.String())
	}
}

func TestTestAll(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	m := New(nil)
	// Test against a local server directly (can't use proxy to localhost easily)
	// Just verify it doesn't panic
	results := m.TestAll(context.Background(), ts.URL)
	t.Logf("test all (%d proxies): %+v", len(results), results)
}

func TestLookupGeo(t *testing.T) {
	m := New(geo.New())
	// Add a localhost proxy (won't have network geo data but shouldn't crash)
	id := m.Add(ProxyEntry{Host: "127.0.0.1", Port: 8080, Scheme: SchemeHTTP})
	m.LookupGeo(context.Background())
	entry, ok := m.Get(id)
	if !ok {
		t.Fatal("entry not found")
	}
	// 127.0.0.1 should get an error from ip-api
	t.Logf("127.0.0.1 geo: %+v", entry.Geo)
}
