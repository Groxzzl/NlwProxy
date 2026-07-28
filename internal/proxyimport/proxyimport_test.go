package proxyimport

import (
	"context"
	"strings"
	"testing"
	"time"
)

// --- Parser unit tests ---

func TestParseRawText(t *testing.T) {
	body := "1.2.3.4:8080\n5.6.7.8:3128\ninvalid\n9.10.11.12:9999\n"
	proxies, err := parseRawText(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proxies) != 3 {
		t.Fatalf("expected 3 proxies, got %d", len(proxies))
	}
	tests := []struct {
		ip   string
		port int
	}{
		{"1.2.3.4", 8080},
		{"5.6.7.8", 3128},
		{"9.10.11.12", 9999},
	}
	for i, tc := range tests {
		if proxies[i].IP != tc.ip || proxies[i].Port != tc.port {
			t.Errorf("proxy %d: expected %s:%d, got %s:%d", i, tc.ip, tc.port, proxies[i].IP, proxies[i].Port)
		}
	}
}

func TestParseRawText_Empty(t *testing.T) {
	proxies, err := parseRawText("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proxies) != 0 {
		t.Fatalf("expected 0 proxies for empty input, got %d", len(proxies))
	}
}

func TestParseMonosansJSON(t *testing.T) {
	body := `[
		{"protocol": "http", "host": "1.2.3.4", "port": 8080},
		{"protocol": "socks4", "host": "5.6.7.8", "port": 1080},
		{"protocol": "socks5", "host": "9.10.11.12", "port": 1080},
		{"protocol": "http", "host": "invalid", "port": 0},
		{"protocol": "unknown", "host": "1.2.3.4", "port": 8080}
	]`
	proxies, err := parseMonosansJSON(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proxies) != 3 {
		t.Fatalf("expected 3 valid proxies, got %d", len(proxies))
	}
	if proxies[0].Type != HTTP || proxies[0].IP != "1.2.3.4" || proxies[0].Port != 8080 {
		t.Errorf("proxy 0 mismatch: got %s://%s:%d", proxies[0].Type, proxies[0].IP, proxies[0].Port)
	}
	if proxies[1].Type != SOCKS4 {
		t.Errorf("proxy 1 expected SOCKS4, got %s", proxies[1].Type)
	}
	if proxies[2].Type != SOCKS5 {
		t.Errorf("proxy 2 expected SOCKS5, got %s", proxies[2].Type)
	}
}

func TestParseMonosansJSON_Malformed(t *testing.T) {
	_, err := parseMonosansJSON("not json")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseJetkaiJSON(t *testing.T) {
	body := `{
		"http": ["1.2.3.4:8080", "5.6.7.8:3128"],
		"socks4": ["10.0.0.1:1080"],
		"socks5": ["10.0.0.2:1080"],
		"unknown": ["1.1.1.1:80"]
	}`
	proxies, err := parseJetkaiJSON(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proxies) != 4 {
		t.Fatalf("expected 4 proxies, got %d", len(proxies))
	}
	counts := map[ProxyType]int{}
	for _, p := range proxies {
		counts[p.Type]++
	}
	if counts[HTTP] != 2 {
		t.Errorf("expected 2 HTTP proxies, got %d", counts[HTTP])
	}
	if counts[SOCKS4] != 1 {
		t.Errorf("expected 1 SOCKS4 proxy, got %d", counts[SOCKS4])
	}
	if counts[SOCKS5] != 1 {
		t.Errorf("expected 1 SOCKS5 proxy, got %d", counts[SOCKS5])
	}
}

func TestParseJetkaiJSON_InvalidEntry(t *testing.T) {
	body := `{"http": ["invalid", "1.2.3.4:8080"]}`
	proxies, err := parseJetkaiJSON(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proxies) != 1 {
		t.Fatalf("expected 1 valid proxy, got %d", len(proxies))
	}
}

func TestParseProxiflyJSON(t *testing.T) {
	body := `[
		{"proxy": "http://1.2.3.4:8080", "protocol": "http"},
		{"proxy": "socks4://5.6.7.8:1080", "protocol": "socks4"},
		{"proxy": "socks5://9.10.11.12:1080", "protocol": "socks5"}
	]`
	proxies, err := parseProxiflyJSON(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proxies) != 3 {
		t.Fatalf("expected 3 proxies, got %d", len(proxies))
	}
	if proxies[0].Type != HTTP || proxies[0].IP != "1.2.3.4" || proxies[0].Port != 8080 {
		t.Errorf("proxy 0 mismatch: got %s://%s:%d", proxies[0].Type, proxies[0].IP, proxies[0].Port)
	}
	if proxies[1].Type != SOCKS4 {
		t.Errorf("proxy 1 expected SOCKS4, got %s", proxies[1].Type)
	}
	if proxies[2].Type != SOCKS5 {
		t.Errorf("proxy 2 expected SOCKS5, got %s", proxies[2].Type)
	}
}

func TestParseProxiflyTXT(t *testing.T) {
	body := "http://1.2.3.4:8080\nsocks4://5.6.7.8:1080\nsocks5://9.10.11.12:1080\ninvalid\n"
	proxies, err := parseProxiflyTXT(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proxies) != 3 {
		t.Fatalf("expected 3 proxies, got %d", len(proxies))
	}
	if proxies[0].Type != HTTP || proxies[0].IP != "1.2.3.4" || proxies[0].Port != 8080 {
		t.Errorf("proxy 0 mismatch: got %s://%s:%d", proxies[0].Type, proxies[0].IP, proxies[0].Port)
	}
	if proxies[1].Type != SOCKS4 {
		t.Errorf("proxy 1 expected SOCKS4, got %s", proxies[1].Type)
	}
	if proxies[2].Type != SOCKS5 {
		t.Errorf("proxy 2 expected SOCKS5, got %s", proxies[2].Type)
	}
}

func TestParseURLProxy(t *testing.T) {
	tests := []struct {
		input    string
		wantOK   bool
		wantIP   string
		wantPort int
		wantType ProxyType
	}{
		{"http://1.2.3.4:8080", true, "1.2.3.4", 8080, HTTP},
		{"https://1.2.3.4:8443", true, "1.2.3.4", 8443, HTTP},
		{"socks4://5.6.7.8:1080", true, "5.6.7.8", 1080, SOCKS4},
		{"socks5://9.10.11.12:1080", true, "9.10.11.12", 1080, SOCKS5},
		{"not-a-url", false, "", 0, 0},
		{"http://no-port", false, "", 0, 0},
	}
	for _, tc := range tests {
		p, ok := parseURLProxy(tc.input)
		if ok != tc.wantOK {
			t.Errorf("parseURLProxy(%q): got ok=%v, want %v", tc.input, ok, tc.wantOK)
			continue
		}
		if ok {
			if p.IP != tc.wantIP || p.Port != tc.wantPort || p.Type != tc.wantType {
				t.Errorf("parseURLProxy(%q): got %s://%s:%d, want %s://%s:%d",
					tc.input, p.Type, p.IP, p.Port, tc.wantType, tc.wantIP, tc.wantPort)
			}
		}
	}
}

// --- Dedup tests ---

func TestDeduplicate(t *testing.T) {
	proxies := []Proxy{
		{IP: "1.2.3.4", Port: 8080, Type: HTTP},
		{IP: "1.2.3.4", Port: 8080, Type: HTTP},   // same
		{IP: "1.2.3.4", Port: 8080, Type: SOCKS4}, // same addr, different type
		{IP: "5.6.7.8", Port: 3128, Type: HTTP},
		{IP: "1.2.3.4", Port: 8080, Type: HTTP}, // duplicate again
	}
	deduped := Deduplicate(proxies)
	if len(deduped) != 3 {
		t.Fatalf("expected 3 deduped proxies, got %d", len(deduped))
	}
}

func TestDeduplicate_Empty(t *testing.T) {
	deduped := Deduplicate(nil)
	if len(deduped) != 0 {
		t.Fatalf("expected empty, got %d", len(deduped))
	}
}

func TestDeduplicate_AllUnique(t *testing.T) {
	proxies := []Proxy{
		{IP: "1.2.3.4", Port: 8080, Type: HTTP},
		{IP: "5.6.7.8", Port: 3128, Type: HTTP},
		{IP: "9.10.11.12", Port: 1080, Type: SOCKS5},
	}
	deduped := Deduplicate(proxies)
	if len(deduped) != 3 {
		t.Fatalf("expected 3, got %d", len(deduped))
	}
}

// --- Categorize tests ---

func TestCategorize(t *testing.T) {
	proxies := []Proxy{
		{IP: "1.2.3.4", Port: 80, Type: HTTP},
		{IP: "5.6.7.8", Port: 1080, Type: SOCKS4},
		{IP: "9.10.11.12", Port: 1080, Type: SOCKS5},
		{IP: "10.0.0.1", Port: 8080, Type: HTTP},
		{IP: "10.0.0.2", Port: 1080, Type: SOCKS5},
	}
	r := Categorize(proxies)
	if len(r.HTTP) != 2 {
		t.Errorf("expected 2 HTTP, got %d", len(r.HTTP))
	}
	if len(r.SOCKS4) != 1 {
		t.Errorf("expected 1 SOCKS4, got %d", len(r.SOCKS4))
	}
	if len(r.SOCKS5) != 2 {
		t.Errorf("expected 2 SOCKS5, got %d", len(r.SOCKS5))
	}
	if r.Total() != 5 {
		t.Errorf("expected Total()=5, got %d", r.Total())
	}
}

func TestCategorize_Empty(t *testing.T) {
	r := Categorize(nil)
	if r.Total() != 0 {
		t.Fatalf("expected Total()=0, got %d", r.Total())
	}
}

// --- Export tests ---

func TestExportText(t *testing.T) {
	r := &Result{
		HTTP:   []Proxy{{IP: "1.2.3.4", Port: 80, Type: HTTP}},
		SOCKS4: []Proxy{{IP: "5.6.7.8", Port: 1080, Type: SOCKS4}},
		SOCKS5: nil,
	}
	text := r.ExportText()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "http://1.2.3.4:80" {
		t.Errorf("line 0: got %q", lines[0])
	}
	if lines[1] != "socks4://5.6.7.8:1080" {
		t.Errorf("line 1: got %q", lines[1])
	}
}

func TestExportLines(t *testing.T) {
	r := &Result{
		HTTP:   []Proxy{{IP: "1.2.3.4", Port: 80, Type: HTTP}},
		SOCKS5: []Proxy{{IP: "9.10.11.12", Port: 1080, Type: SOCKS5}},
	}
	lines := r.ExportLines()
	if len(lines) != 2 {
		t.Fatalf("expected 2, got %d", len(lines))
	}
}

// --- FlattenResults test ---

func TestFlattenResults(t *testing.T) {
	results := []SourceResult{
		{Source: "src1", Proxies: []Proxy{{IP: "1.2.3.4", Port: 80, Type: HTTP}}},
		{Source: "src2", Proxies: []Proxy{{IP: "5.6.7.8", Port: 1080, Type: SOCKS5}}},
	}
	flat := FlattenResults(results)
	if len(flat) != 2 {
		t.Fatalf("expected 2, got %d", len(flat))
	}
}

// --- Integration test: scrape real sources ---

func TestScrapeAll_Live(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live scrape in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	results := ScrapeAll(ctx)

	if len(results) == 0 {
		t.Fatal("expected at least 1 source result")
	}

	total := 0
	for _, r := range results {
		if r.Err != nil {
			t.Logf("source %s error (expected for some): %v", r.Source, r.Err)
		}
		total += len(r.Proxies)
		t.Logf("source %s: %d proxies", r.Source, len(r.Proxies))
	}
	t.Logf("total raw proxies: %d", total)

	if total == 0 {
		t.Fatal("expected at least some proxies from live sources")
	}

	// Dedup and categorize
	flat := FlattenResults(results)
	deduped := Deduplicate(flat)
	categorized := Categorize(deduped)

	t.Logf("after dedup: %d", len(deduped))
	t.Logf("HTTP: %d, SOCKS4: %d, SOCKS5: %d", len(categorized.HTTP), len(categorized.SOCKS4), len(categorized.SOCKS5))

	if categorized.Total() == 0 {
		t.Fatal("expected non-zero total after dedup")
	}
}

// --- Proxy type string test ---

func TestProxyTypeString(t *testing.T) {
	tests := map[ProxyType]string{
		HTTP:          "http",
		SOCKS4:        "socks4",
		SOCKS5:        "socks5",
		ProxyType(99): "unknown",
	}
	for pt, want := range tests {
		if pt.String() != want {
			t.Errorf("ProxyType(%d).String() = %q, want %q", pt, pt.String(), want)
		}
	}
}

// --- Addr / URL test ---

func TestProxyAddrURL(t *testing.T) {
	p := Proxy{IP: "1.2.3.4", Port: 8080, Type: HTTP}
	if p.Addr() != "1.2.3.4:8080" {
		t.Errorf("Addr: got %q", p.Addr())
	}
	if p.URL() != "http://1.2.3.4:8080" {
		t.Errorf("URL: got %q", p.URL())
	}
}

// --- parseIPPort tests ---

func TestParseIPPort(t *testing.T) {
	tests := []struct {
		input    string
		wantOK   bool
		wantIP   string
		wantPort int
	}{
		{"1.2.3.4:8080", true, "1.2.3.4", 8080},
		{" 1.2.3.4:8080 ", true, "1.2.3.4", 8080},
		{"", false, "", 0},
		{"invalid", false, "", 0},
		{"1.2.3.4:0", false, "", 0},
		{"1.2.3.4:65536", false, "", 0},
		{"1.2.3.4:abc", false, "", 0},
	}
	for _, tc := range tests {
		ip, port, ok := parseIPPort(tc.input)
		if ok != tc.wantOK {
			t.Errorf("parseIPPort(%q): ok=%v, want %v", tc.input, ok, tc.wantOK)
			continue
		}
		if ok && (ip != tc.wantIP || port != tc.wantPort) {
			t.Errorf("parseIPPort(%q): got %s:%d, want %s:%d", tc.input, ip, port, tc.wantIP, tc.wantPort)
		}
	}
}
