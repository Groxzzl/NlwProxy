// Package proxymanager manages imported proxies — CRUD, import parsing, and batch testing.
package proxymanager

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"nlwproxy/internal/geo"
)

// ProxyScheme represents the proxy protocol type.
type ProxyScheme string

const (
	SchemeHTTP   ProxyScheme = "http"
	SchemeHTTPS  ProxyScheme = "https"
	SchemeSOCKS5 ProxyScheme = "socks5"
)

// ProxyEntry is a single imported proxy with optional auth and geo.
type ProxyEntry struct {
	ID        string        `json:"id"`
	Host      string        `json:"host"`
	Port      int           `json:"port"`
	Scheme    ProxyScheme   `json:"scheme"`
	Username  string        `json:"username,omitempty"`
	Password  string        `json:"-"`
	Label     string        `json:"label,omitempty"`
	Source    string        `json:"source,omitempty"` // file it was imported from
	AddedAt   time.Time     `json:"added_at"`
	CheckedAt time.Time     `json:"checked_at,omitempty"`
	Latency   time.Duration `json:"latency_ns,omitempty"`
	Alive     bool          `json:"alive"`
	Error     string        `json:"error,omitempty"`
	Geo       geo.Result    `json:"geo,omitempty"`
}

// ProxyURL constructs the proxy URL string for use in transports.
func (p ProxyEntry) ProxyURL() string {
	auth := ""
	if p.Username != "" {
		auth = p.Username
		if p.Password != "" {
			auth += ":" + p.Password
		}
	}
	u := &url.URL{Scheme: string(p.Scheme), Host: net.JoinHostPort(p.Host, fmt.Sprint(p.Port))}
	if auth != "" {
		u.User = url.UserPassword(p.Username, p.Password)
	}
	return u.String()
}

// Manager manages a collection of proxy entries.
type Manager struct {
	mu            sync.RWMutex
	entries       map[string]*ProxyEntry
	nextID        int
	geo           *geo.Service
	client        *http.Client
	aliveListener func([]ProxyEntry)
}

func (m *Manager) SetAliveListener(listener func([]ProxyEntry)) {
	m.mu.Lock()
	m.aliveListener = listener
	m.mu.Unlock()
}

// New creates a new proxy manager.
func New(gs *geo.Service) *Manager {
	if gs == nil {
		gs = geo.New()
	}
	return &Manager{
		entries: make(map[string]*ProxyEntry),
		geo:     gs,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// List returns all managed proxy entries sorted by ID.
func (m *Manager) List() []ProxyEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ProxyEntry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListAlive returns only alive proxy entries.
func (m *Manager) ListAlive() []ProxyEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ProxyEntry, 0)
	for _, e := range m.entries {
		if e.Alive {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns a proxy entry by ID.
func (m *Manager) Get(id string) (ProxyEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[id]
	if !ok {
		return ProxyEntry{}, false
	}
	return *e, true
}

// Add inserts a new proxy entry.
func (m *Manager) Add(entry ProxyEntry) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("proxy-%d", m.nextID)
	entry.ID = id
	if entry.AddedAt.IsZero() {
		entry.AddedAt = time.Now()
	}
	m.entries[id] = &entry
	return id
}

// Remove deletes a proxy entry by ID.
func (m *Manager) Remove(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.entries[id]
	if ok {
		delete(m.entries, id)
	}
	return ok
}

// Count returns the total tally.
func (m *Manager) Count() (total, alive int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.entries {
		total++
		if e.Alive {
			alive++
		}
	}
	return
}

// ImportFile parses a text file and adds all valid proxies found.
// Format per line: ip:port or ip:port:user:pass or scheme://ip:port
// Lines starting with # and empty lines are skipped.
func (m *Manager) ImportFile(path string) (added int, errors []string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, []string{fmt.Sprintf("open file: %v", err)}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		entries, errs := parseProxyLine(line)
		for _, entry := range entries {
			m.Add(entry)
			added++
		}
		for _, e := range errs {
			errors = append(errors, fmt.Sprintf("line %d: %s", lineNum, e))
		}
	}

	if err := scanner.Err(); err != nil {
		errors = append(errors, fmt.Sprintf("read error: %v", err))
	}
	return
}

func parseProxyLine(line string) ([]ProxyEntry, []string) {
	// Try URL scheme format first: scheme://host:port
	if strings.Contains(line, "://") {
		u, err := url.Parse(line)
		if err != nil || u.Host == "" {
			return nil, []string{fmt.Sprintf("invalid URL %q", line)}
		}
		scheme := ProxyScheme(u.Scheme)
		host, portS, err := net.SplitHostPort(u.Host)
		if err != nil {
			return nil, []string{fmt.Sprintf("invalid host:port in %q", line)}
		}
		port := parsePort(portS)
		if port <= 0 {
			return nil, []string{fmt.Sprintf("invalid port in %q", line)}
		}
		entry := ProxyEntry{
			Host:   host,
			Port:   port,
			Scheme: scheme,
		}
		if u.User != nil {
			entry.Username = u.User.Username()
			entry.Password, _ = u.User.Password()
		}
		return []ProxyEntry{entry}, nil
	}

	// Plain format: ip:port or ip:port:user:pass
	parts := strings.Split(line, ":")
	switch len(parts) {
	case 2:
		// ip:port
		port := parsePort(parts[1])
		if port <= 0 {
			return nil, []string{fmt.Sprintf("invalid port in %q", line)}
		}
		return []ProxyEntry{{Host: parts[0], Port: port, Scheme: SchemeHTTP}}, nil
	case 4:
		// ip:port:user:pass
		port := parsePort(parts[1])
		if port <= 0 {
			return nil, []string{fmt.Sprintf("invalid port in %q", line)}
		}
		return []ProxyEntry{{
			Host:     parts[0],
			Port:     port,
			Scheme:   SchemeHTTP,
			Username: parts[2],
			Password: parts[3],
		}}, nil
	default:
		return nil, []string{fmt.Sprintf("unrecognized format %q", line)}
	}
}

func parsePort(s string) int {
	p := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		p = p*10 + int(c-'0')
	}
	if p < 1 || p > 65535 {
		return -1
	}
	return p
}

// TestResult holds the outcome of testing a single proxy.
type TestResult struct {
	ID      string
	Alive   bool
	Latency time.Duration
	Error   string
	Geo     geo.Result
}

// TestSingle tests a single proxy by making a request to the given test URL.
func (m *Manager) TestSingle(ctx context.Context, id, testURL string) TestResult {
	m.mu.Lock()
	entry, ok := m.entries[id]
	m.mu.Unlock()
	if !ok {
		return TestResult{ID: id, Error: "not found"}
	}

	result := m.testProxy(ctx, entry, testURL)

	m.mu.Lock()
	if cur, ok := m.entries[id]; ok {
		cur.Alive = result.Alive
		cur.Latency = result.Latency
		cur.Error = result.Error
		cur.CheckedAt = time.Now()
		cur.Geo = result.Geo
	}
	m.mu.Unlock()

	return result
}

// TestAll tests all proxies. Returns results in order.
func (m *Manager) TestAll(ctx context.Context, testURL string) []TestResult {
	entries := m.List()
	results := make([]TestResult, len(entries))
	var wg sync.WaitGroup
	// Bound the whole batch so a malformed proxy can never pin the TUI.
	batchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for i, e := range entries {
		wg.Add(1)
		go func(i int, e ProxyEntry) {
			defer wg.Done()
			results[i] = m.TestSingle(batchCtx, e.ID, testURL)
		}(i, e)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-batchCtx.Done():
		for i := range results {
			if results[i].ID == "" {
				results[i] = TestResult{ID: entries[i].ID, Error: "test timeout"}
			}
		}
	}
	m.mu.RLock()
	listener := m.aliveListener
	m.mu.RUnlock()
	if listener != nil {
		listener(m.ListAlive())
	}
	return results
}

func (m *Manager) testProxy(ctx context.Context, entry *ProxyEntry, testURL string) TestResult {
	result := TestResult{ID: entry.ID}

	proxyURL := entry.ProxyURL()
	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		result.Error = fmt.Sprintf("invalid proxy URL: %v", err)
		return result
	}

	transport := &http.Transport{
		Proxy:             http.ProxyURL(proxyParsed),
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}
	defer transport.CloseIdleConnections()

	if testURL == "" {
		testURL = "https://www.cloudflare.com/cdn-cgi/trace"
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "NlwProxy/1.0")

	resp, err := client.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	result.Latency = time.Since(start)

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		result.Error = fmt.Sprintf("status %d", resp.StatusCode)
		return result
	}

	result.Alive = true

	// Geo-lookup on the proxy's host if it's an IP
	if ip := net.ParseIP(entry.Host); ip != nil {
		geoResult := m.geo.Lookup(ctx, entry.Host)
		result.Geo = geoResult
	}

	return result
}

// LookupGeo performs geo lookup for all proxies that don't have fresh geo data.
func (m *Manager) LookupGeo(ctx context.Context) {
	entries := m.List()
	for _, e := range entries {
		ip := net.ParseIP(e.Host)
		if ip == nil {
			continue
		}
		geoResult := m.geo.Lookup(ctx, e.Host)
		m.mu.Lock()
		if cur, ok := m.entries[e.ID]; ok {
			cur.Geo = geoResult
		}
		m.mu.Unlock()
	}
}

// Stats returns aggregated proxy statistics.
type Stats struct {
	Total     int
	Alive     int
	Healthy   int
	Slow      int
	Dead      int
	HTTP      int
	HTTPS     int
	SOCKS5    int
	GeoCount  int
	Countries []string
}

func (m *Manager) Stats() Stats {
	entries := m.List()
	s := Stats{Total: len(entries)}
	countrySet := map[string]bool{}
	for _, e := range entries {
		if e.Alive {
			s.Alive++
			if e.Latency >= 2*time.Second {
				s.Slow++
			} else {
				s.Healthy++
			}
		} else {
			s.Dead++
		}
		switch e.Scheme {
		case SchemeHTTP:
			s.HTTP++
		case SchemeHTTPS:
			s.HTTPS++
		case SchemeSOCKS5:
			s.SOCKS5++
		}
		if e.Geo.Country != "" {
			s.GeoCount++
			countrySet[e.Geo.Country] = true
		}
	}
	s.Countries = make([]string, 0, len(countrySet))
	for c := range countrySet {
		s.Countries = append(s.Countries, c)
	}
	sort.Strings(s.Countries)
	return s
}

// GeoService exposes the underlying geo service for direct access.
func (m *Manager) GeoService() *geo.Service { return m.geo }
