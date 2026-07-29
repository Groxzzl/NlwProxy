// Package geo provides IP geolocation via ip-api.com with a 30-minute cache.
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Result holds geolocation data for a single IP.
type Result struct {
	IP        string    `json:"ip,omitempty"`
	Country   string    `json:"country,omitempty"`
	City      string    `json:"city,omitempty"`
	ASN       string    `json:"asn,omitempty"`
	ISP       string    `json:"isp,omitempty"`
	Lat       float64   `json:"lat,omitempty"`
	Lon       float64   `json:"lon,omitempty"`
	Org       string    `json:"org,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type cacheEntry struct {
	result    Result
	expiresAt time.Time
}

// Service provides cached IP geolocation.
type Service struct {
	mu       sync.RWMutex
	cache    map[string]cacheEntry
	ttl      time.Duration
	client   *http.Client
	url      string
	fallback string
}

// Option configures the geo service.
type Option func(*Service)

// WithTTL overrides the default 30-minute cache TTL.
func WithTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.ttl = d
		}
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(s *Service) { s.client = c }
}

// New creates a new geo lookup service.
// Uses ip-api.com free JSON endpoint; no API key required for <=45 req/min.
func New(opts ...Option) *Service {
	s := &Service{
		cache:    map[string]cacheEntry{},
		ttl:      6 * time.Hour,
		client:   &http.Client{Timeout: 5 * time.Second},
		url:      "http://ip-api.com/json/",
		fallback: "https://ipwho.is/",
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Lookup returns geolocation for the given IP. Results are cached for the TTL.
func (s *Service) Lookup(ctx context.Context, ip string) Result {
	if ip == "" {
		return Result{Error: "empty IP"}
	}

	s.mu.RLock()
	entry, ok := s.cache[ip]
	s.mu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.result
	}

	result := s.fetch(ctx, ip)

	s.mu.Lock()
	s.cache[ip] = cacheEntry{result: result, expiresAt: time.Now().Add(s.ttl)}
	s.mu.Unlock()

	return result
}

// LookupBatch performs geolocation for multiple IPs concurrently.
func (s *Service) LookupBatch(ctx context.Context, ips []string) map[string]Result {
	results := make(map[string]Result, len(ips))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, ip := range ips {
		if ip == "" {
			continue
		}
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			r := s.Lookup(ctx, ip)
			mu.Lock()
			results[ip] = r
			mu.Unlock()
		}(ip)
	}
	wg.Wait()
	return results
}

// ClearCache removes all cached entries.
func (s *Service) ClearCache() {
	s.mu.Lock()
	s.cache = map[string]cacheEntry{}
	s.mu.Unlock()
}

// Stats returns current cache statistics.
func (s *Service) Stats() (cached, expired int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	for _, e := range s.cache {
		if now.Before(e.expiresAt) {
			cached++
		} else {
			expired++
		}
	}
	return
}

func (s *Service) fetch(ctx context.Context, ip string) Result {
	r := Result{IP: ip, CheckedAt: time.Now()}

	url := s.url + ip
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	req.Header.Set("User-Agent", "NlwProxy/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return s.fetchFallback(ctx, ip, err.Error())
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return s.fetchFallback(ctx, ip, err.Error())
	}

	var payload struct {
		Status  string  `json:"status"`
		Country string  `json:"country"`
		City    string  `json:"city"`
		ISP     string  `json:"isp"`
		Org     string  `json:"org"`
		As      string  `json:"as"`
		Lat     float64 `json:"lat"`
		Lon     float64 `json:"lon"`
		Query   string  `json:"query"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return s.fetchFallback(ctx, ip, fmt.Sprintf("decode error: %v", err))
	}

	if payload.Status != "success" {
		return s.fetchFallback(ctx, ip, fmt.Sprintf("ip-api status: %s", payload.Status))
	}

	r.IP = payload.Query
	r.Country = payload.Country
	r.City = payload.City
	r.ASN = payload.As
	r.ISP = payload.ISP
	r.Org = payload.Org
	r.Lat = payload.Lat
	r.Lon = payload.Lon
	return r
}

func (s *Service) fetchFallback(ctx context.Context, ip, primaryError string) Result {
	r := Result{IP: ip, CheckedAt: time.Now(), Error: primaryError}
	if s.fallback == "" {
		return r
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.fallback+ip, nil)
	if err != nil {
		return r
	}
	req.Header.Set("User-Agent", "NlwProxy/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return r
	}
	defer resp.Body.Close()
	var payload struct {
		Success    bool    `json:"success"`
		IP         string  `json:"ip"`
		Country    string  `json:"country"`
		City       string  `json:"city"`
		Latitude   float64 `json:"latitude"`
		Longitude  float64 `json:"longitude"`
		Connection struct {
			ASN      int `json:"asn"`
			Org, ISP string
		} `json:"connection"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload); err != nil || !payload.Success {
		return r
	}
	r.Error = ""
	r.IP, r.Country, r.City = payload.IP, payload.Country, payload.City
	r.Lat, r.Lon = payload.Latitude, payload.Longitude
	if payload.Connection.ASN > 0 {
		r.ASN = fmt.Sprintf("AS%d", payload.Connection.ASN)
	}
	r.Org, r.ISP = payload.Connection.Org, payload.Connection.ISP
	return r
}
