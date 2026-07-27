package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type Health string

const (
	Unknown   Health = "unknown"
	Healthy   Health = "healthy"
	Degraded  Health = "degraded"
	Unhealthy Health = "unhealthy"
)

type Circuit string

const (
	CircuitClosed   Circuit = "closed"
	CircuitOpen     Circuit = "open"
	CircuitHalfOpen Circuit = "half-open"
)

type Strategy string

const (
	Manual        Strategy = "manual"
	Priority      Strategy = "priority"
	LowestLatency Strategy = "lowest_latency"
	RoundRobin    Strategy = "round_robin"
	LeastActive   Strategy = "least_active"
)

type Target struct {
	Name           string
	Priority       int
	Latency        time.Duration
	Enabled        bool
	MaxConcurrency int
	Transport      http.RoundTripper `json:"-"`
	TransportType  string            `json:"transport"`
}
type BreakerConfig struct {
	FailureThreshold  int
	OpenTimeout       time.Duration
	RecoverySuccesses int
}
type ExitIPProbeConfig struct {
	Enabled           bool
	URL               string
	Timeout, CacheTTL time.Duration
}
type Config struct {
	Strategy     Strategy
	Breaker      BreakerConfig
	EWMAAlpha    float64
	StickyTTL    time.Duration
	ManualTarget string
	ExitIPProbe  ExitIPProbeConfig
}
type State struct {
	Health            Health        `json:"health"`
	Circuit           Circuit       `json:"circuit"`
	Latency           time.Duration `json:"latency"`
	Failures          int           `json:"failures"`
	Active            int           `json:"active"`
	OpenUntil         time.Time     `json:"open_until,omitempty"`
	ProbeActive       bool          `json:"-"`
	RecoverySuccesses int           `json:"-"`
}
type RequestResult struct {
	Status                    int
	Latency                   time.Duration
	InputTokens, OutputTokens int64
	UsedAt                    time.Time
}
type ExitIP struct {
	IP        string    `json:"ip,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Error     string    `json:"error,omitempty"`
}
type RouteSnapshot struct {
	Health                Health  `json:"health"`
	Circuit               Circuit `json:"circuit"`
	Transport             string  `json:"transport"`
	Active, Total, Errors int64
	Latency               time.Duration `json:"latency_ns"`
	InputTokens           int64         `json:"input_tokens"`
	OutputTokens          int64         `json:"output_tokens"`
	LastUsed              time.Time     `json:"last_used,omitempty"`
	ExitIP                ExitIP        `json:"exit_ip,omitempty"`
}
type entry struct {
	target  Target
	state   State
	metrics RouteSnapshot
}
type stickyEntry struct {
	name    string
	expires time.Time
}
type Selector struct {
	mu      sync.Mutex
	entries map[string]*entry
	cfg     Config
	rr      uint64
	sticky  map[string]stickyEntry
}

func NewSelector(targets []Target, breaker BreakerConfig) *Selector {
	return New(targets, Config{Strategy: Priority, Breaker: breaker})
}
func New(targets []Target, cfg Config) *Selector {
	if cfg.Strategy == "" {
		cfg.Strategy = Priority
	}
	if cfg.Breaker.FailureThreshold <= 0 {
		cfg.Breaker.FailureThreshold = 4
	}
	if cfg.Breaker.OpenTimeout <= 0 {
		cfg.Breaker.OpenTimeout = time.Minute
	}
	if cfg.Breaker.RecoverySuccesses <= 0 {
		cfg.Breaker.RecoverySuccesses = 1
	}
	if cfg.EWMAAlpha <= 0 || cfg.EWMAAlpha > 1 {
		cfg.EWMAAlpha = .25
	}
	if cfg.StickyTTL <= 0 {
		cfg.StickyTTL = 30 * time.Minute
	}
	if cfg.ExitIPProbe.Timeout <= 0 {
		cfg.ExitIPProbe.Timeout = 3 * time.Second
	}
	if cfg.ExitIPProbe.CacheTTL <= 0 {
		cfg.ExitIPProbe.CacheTTL = 15 * time.Minute
	}
	s := &Selector{entries: map[string]*entry{}, cfg: cfg, sticky: map[string]stickyEntry{}}
	for _, t := range targets {
		kind := t.TransportType
		if kind == "" {
			kind = "direct"
		}
		st := State{Health: Unknown, Circuit: CircuitClosed, Latency: t.Latency}
		s.entries[t.Name] = &entry{target: t, state: st, metrics: RouteSnapshot{Health: Unknown, Circuit: CircuitClosed, Transport: kind, Latency: t.Latency}}
	}
	return s
}
func (s *Selector) Next(now time.Time, exclude map[string]bool) (Target, bool) {
	return s.next(now, exclude, "")
}
func (s *Selector) NextSession(now time.Time, exclude map[string]bool, session string) (Target, bool) {
	return s.next(now, exclude, session)
}
func (s *Selector) next(now time.Time, exclude map[string]bool, session string) (Target, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	eligible := func(e *entry) bool {
		return !exclude[e.target.Name] && e.state.Health != Unhealthy && (e.target.MaxConcurrency <= 0 || e.state.Active < e.target.MaxConcurrency)
	}
	if session != "" {
		if st, ok := s.sticky[session]; ok && now.Before(st.expires) {
			if e := s.entries[st.name]; e != nil && eligible(e) && e.state.Circuit != CircuitOpen {
				return e.target, true
			}
		}
	}
	var c []*entry
	for _, e := range s.entries {
		if !eligible(e) {
			continue
		}
		if e.state.Circuit == CircuitOpen {
			if now.Before(e.state.OpenUntil) || e.state.ProbeActive {
				continue
			}
			e.state.Circuit = CircuitHalfOpen
			e.state.ProbeActive = true
		}
		c = append(c, e)
	}
	if len(c) == 0 {
		return Target{}, false
	}
	sort.SliceStable(c, func(i, j int) bool {
		a, b := c[i], c[j]
		switch s.cfg.Strategy {
		case LowestLatency:
			if a.state.Latency != b.state.Latency {
				return a.state.Latency < b.state.Latency
			}
		case LeastActive:
			if a.state.Active != b.state.Active {
				return a.state.Active < b.state.Active
			}
		case Manual:
			if a.target.Name == s.cfg.ManualTarget {
				return true
			}
			if b.target.Name == s.cfg.ManualTarget {
				return false
			}
		default:
			if a.target.Priority != b.target.Priority {
				return a.target.Priority < b.target.Priority
			}
			if a.state.Latency != b.state.Latency {
				return a.state.Latency < b.state.Latency
			}
		}
		return a.target.Name < b.target.Name
	})
	chosen := c[0]
	if s.cfg.Strategy == RoundRobin {
		chosen = c[int(s.rr%uint64(len(c)))]
		s.rr++
	}
	if session != "" {
		s.sticky[session] = stickyEntry{chosen.target.Name, now.Add(s.cfg.StickyTTL)}
	}
	return chosen.target, true
}
func (s *Selector) Acquire(name string) (func(), bool) {
	s.mu.Lock()
	e := s.entries[name]
	if e == nil || (e.target.MaxConcurrency > 0 && e.state.Active >= e.target.MaxConcurrency) {
		s.mu.Unlock()
		return nil, false
	}
	e.state.Active++
	e.metrics.Active++
	s.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); e.state.Active--; e.metrics.Active--; s.mu.Unlock() }) }, true
}
func (s *Selector) SetHealth(name string, h Health, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[name]; e != nil {
		e.state.Health = h
		if latency > 0 {
			e.state.Latency = latency
		}
	}
}
func (s *Selector) RecordSuccess(name string, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[name]; e != nil {
		e.state.Health = Healthy
		e.state.Failures = 0
		e.state.OpenUntil = time.Time{}
		e.state.ProbeActive = false
		if latency > 0 {
			if e.state.Latency <= 0 {
				e.state.Latency = latency
			} else {
				a := s.cfg.EWMAAlpha
				e.state.Latency = time.Duration(a*float64(latency) + (1-a)*float64(e.state.Latency))
			}
		}
		if e.state.Circuit == CircuitHalfOpen {
			e.state.RecoverySuccesses++
			if e.state.RecoverySuccesses >= s.cfg.Breaker.RecoverySuccesses {
				e.state.Circuit = CircuitClosed
				e.state.RecoverySuccesses = 0
			}
		} else {
			e.state.Circuit = CircuitClosed
		}
	}
}
func (s *Selector) RecordFailure(name string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[name]; e != nil {
		e.state.Failures++
		e.state.ProbeActive = false
		e.state.RecoverySuccesses = 0
		if e.state.Circuit == CircuitHalfOpen || e.state.Failures >= s.cfg.Breaker.FailureThreshold {
			e.state.Circuit = CircuitOpen
			e.state.OpenUntil = now.Add(s.cfg.Breaker.OpenTimeout)
		}
	}
}
func (s *Selector) States(now time.Time) map[string]State {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]State{}
	for n, e := range s.entries {
		st := e.state
		if st.Circuit == CircuitOpen && !now.Before(st.OpenUntil) {
			st.Circuit = CircuitHalfOpen
		}
		out[n] = st
	}
	return out
}
func (s *Selector) RecordRequest(name string, r RequestResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[name]
	if e == nil {
		return
	}
	m := &e.metrics
	m.Total++
	if r.Status == 0 || r.Status >= 400 {
		m.Errors++
	}
	if r.Latency > 0 {
		m.Latency = time.Duration((int64(m.Latency)*int64(m.Total-1) + int64(r.Latency)) / int64(m.Total))
	}
	m.InputTokens += r.InputTokens
	m.OutputTokens += r.OutputTokens
	if r.UsedAt.IsZero() {
		r.UsedAt = time.Now()
	}
	m.LastUsed = r.UsedAt
}
func (s *Selector) Snapshots(now time.Time) map[string]RouteSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]RouteSnapshot{}
	for n, e := range s.entries {
		m := e.metrics
		m.Health, m.Circuit = e.state.Health, e.state.Circuit
		if m.Circuit == CircuitOpen && !now.Before(e.state.OpenUntil) {
			m.Circuit = CircuitHalfOpen
		}
		out[n] = m
	}
	return out
}
func (s *Selector) ProbeExitIP(ctx context.Context, name string, now time.Time) ExitIP {
	s.mu.Lock()
	e := s.entries[name]
	cfg := s.cfg.ExitIPProbe
	if !cfg.Enabled {
		s.mu.Unlock()
		return ExitIP{Error: "disabled"}
	}
	if e == nil {
		s.mu.Unlock()
		return ExitIP{Error: "route not found"}
	}
	if now.Before(e.metrics.ExitIP.ExpiresAt) {
		v := e.metrics.ExitIP
		s.mu.Unlock()
		return v
	}
	rt := e.target.Transport
	s.mu.Unlock()
	result := ExitIP{CheckedAt: now, ExpiresAt: now.Add(cfg.CacheTTL)}
	if rt == nil {
		result.Error = "transport unavailable"
		return result
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		result.Error = "invalid probe URL"
		return result
	}
	c, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(c, http.MethodGet, u.String(), nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		result.Error = err.Error()
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			result.Error = fmt.Sprintf("probe status %d", resp.StatusCode)
		} else {
			body, er := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if er != nil {
				result.Error = er.Error()
			} else {
				var v struct {
					IP string `json:"ip"`
				}
				if json.Unmarshal(body, &v) == nil && v.IP != "" {
					result.IP = v.IP
				} else {
					result.IP = strings.TrimSpace(string(body))
				}
				if result.IP == "" {
					result.Error = "empty probe response"
				}
			}
		}
	}
	s.mu.Lock()
	if cur := s.entries[name]; cur != nil {
		cur.metrics.ExitIP = result
	}
	s.mu.Unlock()
	return result
}
