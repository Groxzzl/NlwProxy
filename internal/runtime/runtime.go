// Package gatewayruntime builds and owns an executable gateway independently of its UI.
package gatewayruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"nlwproxy/internal/gateway"
	"nlwproxy/internal/metrics"
	"nlwproxy/internal/profiles"
	"nlwproxy/internal/proxymanager"
	"nlwproxy/internal/routing"
	"nlwproxy/internal/transport"
)

type CredentialSource interface{ Lookup(string) (string, error) }

type Options struct {
	Profile          profiles.Profile
	Credentials      CredentialSource
	EventCapacity    int
	TransportTimeout time.Duration
	ShutdownTimeout  time.Duration
	ModelTTL         time.Duration
	BaseTransport    *http.Transport
	DirectTransport  http.RoundTripper
	ProxyManager     *proxymanager.Manager
}

type Snapshot struct {
	Profile profiles.Profile                 `json:"profile"`
	State   gateway.GatewayState             `json:"state"`
	Listen  string                           `json:"listen"`
	Error   string                           `json:"error,omitempty"`
	Routes  map[string]routing.RouteSnapshot `json:"routes"`
	Metrics metrics.Snapshot                 `json:"metrics"`
}

type GatewayRuntime struct {
	profile          profiles.Profile
	listen           string
	shutdownTimeout  time.Duration
	selector         *routing.Selector
	providers        []providerRoute
	transportTimeout time.Duration
	events           *metrics.EventBus
	models           *gateway.CachedModelService
	handler          http.Handler

	mu           sync.RWMutex
	state        gateway.GatewayState
	actualListen string
	lastErr      error
	server       *http.Server
	listener     net.Listener
	done         chan struct{}
	ready        chan struct{}
	readyOnce    sync.Once
}

type providerRoute struct {
	name     string
	priority int
	base     *url.URL
	apiKey   string
	headers  map[string]string
}

func New(opts Options) (*GatewayRuntime, error) {
	cfg := opts.Profile.Config
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if opts.Profile.ID == "" {
		return nil, errors.New("runtime profile is required")
	}
	lookup := opts.Credentials
	load := func(name string) (string, error) {
		if name == "" {
			return "", nil
		}
		if value := os.Getenv(name); value != "" {
			return value, nil
		}
		if lookup == nil {
			return "", nil
		}
		value, err := lookup.Lookup(name)
		if err != nil {
			return "", err
		}
		if value != "" {
			if err := os.Setenv(name, value); err != nil {
				return "", err
			}
		}
		return value, nil
	}
	localToken, err := load(cfg.Server.LocalTokenEnv)
	if err != nil {
		return nil, fmt.Errorf("local token %s: %w", cfg.Server.LocalTokenEnv, err)
	}
	if cfg.Server.LocalTokenEnv == "" || localToken == "" {
		return nil, errors.New("server.local_token_env must name a non-empty credential")
	}

	timeout := opts.TransportTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	targets := make([]routing.Target, 0, len(cfg.Upstreams))
	providers := make([]providerRoute, 0, len(cfg.Upstreams))
	var modelTransport http.RoundTripper
	for _, up := range cfg.Upstreams {
		if !up.Enabled {
			continue
		}
		base, err := url.Parse(up.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("route %s: %w", up.Name, err)
		}
		apiKey, err := load(up.APIKeyEnv)
		if err != nil {
			return nil, fmt.Errorf("route %s credential %s: %w", up.Name, up.APIKeyEnv, err)
		}
		if up.APIKeyEnv == "" || apiKey == "" {
			return nil, fmt.Errorf("route %s requires non-empty environment variable %s", up.Name, up.APIKeyEnv)
		}
		mode := transport.Direct
		if up.ProxyURL != "" {
			proxyURL, err := url.Parse(up.ProxyURL)
			if err != nil {
				return nil, fmt.Errorf("route %s proxy: %w", up.Name, err)
			}
			if proxyURL.Scheme == "socks5" || proxyURL.Scheme == "socks5h" {
				mode = transport.SOCKS5
			} else {
				mode = transport.HTTPProxy
			}
		}
		var next http.RoundTripper
		if opts.DirectTransport != nil && mode == transport.Direct {
			next = opts.DirectTransport
		} else if opts.BaseTransport != nil && mode == transport.Direct {
			next = opts.BaseTransport.Clone()
		} else {
			next, err = transport.New(transport.Config{Mode: mode, ProxyURL: up.ProxyURL, Timeout: timeout})
		}
		if err != nil {
			return nil, fmt.Errorf("route %s: %w", up.Name, err)
		}
		wrapped := &upstreamTransport{base: base, apiKey: apiKey, headers: up.Headers, next: next}
		providers = append(providers, providerRoute{name: up.Name, priority: up.Priority, base: base, apiKey: apiKey, headers: up.Headers})
		if modelTransport == nil {
			modelTransport = wrapped
		}
		targets = append(targets, routing.Target{Name: up.Name, Priority: up.Priority, Enabled: true, MaxConcurrency: 8, TransportType: string(mode), Transport: wrapped})
	}
	if cfg.ProxyOnly {
		targets = nil
		if opts.ProxyManager != nil {
			var buildErr error
			targets, buildErr = buildProxyTargets(providers, opts.ProxyManager.ListAlive(), timeout)
			if buildErr != nil {
				return nil, buildErr
			}
		}
	}
	if len(targets) == 0 && !cfg.ProxyOnly {
		return nil, errors.New("no enabled upstream routes")
	}
	strategy := routing.Priority
	if cfg.Routing.Strategy == "round_robin" {
		strategy = routing.RoundRobin
	}
	probe := cfg.Observability.ExitIPProbe
	probeTimeout, _ := time.ParseDuration(probe.Timeout)
	probeTTL, _ := time.ParseDuration(probe.CacheTTL)
	selector := routing.New(targets, routing.Config{Strategy: strategy, ExitIPProbe: routing.ExitIPProbeConfig{Enabled: probe.Enabled, URL: probe.URL, Timeout: probeTimeout, CacheTTL: probeTTL}})
	if cfg.ProxyOnly {
		for _, target := range targets {
			selector.SetHealth(target.Name, routing.Healthy, target.Latency)
		}
	}
	events := metrics.NewEventBus(opts.EventCapacity)
	modelTTL := opts.ModelTTL
	if modelTTL <= 0 {
		modelTTL = 5 * time.Minute
	}
	models := &gateway.CachedModelService{Transport: modelTransport, TTL: modelTTL}
	handler := gateway.New(gateway.Config{Token: localToken, StrictOpenCode: cfg.Server.StrictOpenCodeClient, MaxBodyBytes: cfg.Server.MaxBodyBytes, Attempts: 12, Events: events, Models: models, NoRouteCode: proxyOnlyCode(cfg.ProxyOnly)}, selector)
	shutdown := opts.ShutdownTimeout
	if shutdown <= 0 {
		shutdown = 15 * time.Second
	}
	runtime := &GatewayRuntime{profile: opts.Profile, listen: cfg.Server.Listen, shutdownTimeout: shutdown, selector: selector, providers: providers, transportTimeout: timeout, events: events, models: models, handler: handler, state: gateway.StateStopped, ready: make(chan struct{})}
	if opts.ProxyManager != nil {
		opts.ProxyManager.SetAliveListener(func(entries []proxymanager.ProxyEntry) { _ = runtime.ReloadHealthyProxies(entries) })
	}
	return runtime, nil
}

func proxyOnlyCode(enabled bool) string {
	if enabled {
		return "NO_HEALTHY_PROXY"
	}
	return ""
}

func (r *GatewayRuntime) ReloadHealthyProxies(entries []proxymanager.ProxyEntry) error {
	targets, err := buildProxyTargets(r.providers, entries, r.transportTimeout)
	if err != nil {
		return err
	}
	r.selector.Reload(targets)
	for _, target := range targets {
		r.selector.SetHealth(target.Name, routing.Healthy, target.Latency)
	}
	return nil
}

func buildProxyTargets(providers []providerRoute, entries []proxymanager.ProxyEntry, timeout time.Duration) ([]routing.Target, error) {
	targets := make([]routing.Target, 0, len(providers)*len(entries))
	for _, provider := range providers {
		for _, proxy := range entries {
			if !proxy.Alive {
				continue
			}
			mode := transport.HTTPProxy
			if proxy.Scheme == proxymanager.SchemeSOCKS5 {
				mode = transport.SOCKS5
			}
			next, err := transport.New(transport.Config{Mode: mode, ProxyURL: proxy.ProxyURL(), Timeout: timeout})
			if err != nil {
				return nil, fmt.Errorf("proxy route %s/%s: %w", provider.name, proxy.ID, err)
			}
			name := provider.name + "@" + proxy.ID
			targets = append(targets, routing.Target{Name: name, Priority: provider.priority, Latency: proxy.Latency, Enabled: true, MaxConcurrency: 8, TransportType: string(mode), Transport: &upstreamTransport{base: provider.base, apiKey: provider.apiKey, headers: provider.headers, next: next}})
		}
	}
	return targets, nil
}

func (r *GatewayRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.state == gateway.StateRunning || r.state == gateway.StateStarting {
		r.mu.Unlock()
		return nil
	}
	r.state, r.lastErr = gateway.StateStarting, nil
	r.mu.Unlock()
	ln, err := net.Listen("tcp", r.listen)
	if err != nil {
		r.setFailed(err)
		return err
	}
	server := &http.Server{Addr: r.listen, Handler: r.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	r.mu.Lock()
	r.listener, r.server, r.actualListen, r.done, r.state = ln, server, ln.Addr().String(), make(chan struct{}), gateway.StateRunning
	done := r.done
	r.readyOnce.Do(func() { close(r.ready) })
	r.mu.Unlock()
	go func() {
		err := server.Serve(ln)
		r.mu.Lock()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.state, r.lastErr = gateway.StateFailed, err
		} else if r.state != gateway.StateFailed {
			r.state = gateway.StateStopped
		}
		close(done)
		r.mu.Unlock()
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Stop(context.Background())
		case <-done:
		}
	}()
	return nil
}

func (r *GatewayRuntime) Stop(ctx context.Context) error {
	r.mu.Lock()
	if r.state == gateway.StateStopped {
		r.mu.Unlock()
		return nil
	}
	if r.server == nil {
		r.state = gateway.StateStopped
		r.mu.Unlock()
		return nil
	}
	r.state = gateway.StateStopping
	server, done, timeout := r.server, r.done, r.shutdownTimeout
	r.mu.Unlock()
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		r.setFailed(err)
		return err
	}
	select {
	case <-done:
		return nil
	case <-shutdownCtx.Done():
		r.setFailed(shutdownCtx.Err())
		return shutdownCtx.Err()
	}
}

func (r *GatewayRuntime) setFailed(err error) {
	r.mu.Lock()
	r.state, r.lastErr = gateway.StateFailed, err
	r.mu.Unlock()
}
func (r *GatewayRuntime) Ready() <-chan struct{}              { return r.ready }
func (r *GatewayRuntime) Handler() http.Handler               { return r.handler }
func (r *GatewayRuntime) Events() *metrics.EventBus           { return r.events }
func (r *GatewayRuntime) Models() *gateway.CachedModelService { return r.models }
func (r *GatewayRuntime) Selector() *routing.Selector         { return r.selector }
func (r *GatewayRuntime) BaseURL() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return "http://" + r.actualListen
}
func (r *GatewayRuntime) Snapshot() Snapshot {
	r.mu.RLock()
	state, listen, err := r.state, r.actualListen, r.lastErr
	r.mu.RUnlock()
	if listen == "" {
		listen = r.listen
	}
	s := Snapshot{Profile: r.profile, State: state, Listen: listen, Routes: r.selector.Snapshots(time.Now()), Metrics: r.events.Snapshot()}
	if err != nil {
		s.Error = err.Error()
	}
	return s
}

type upstreamTransport struct {
	base    *url.URL
	apiKey  string
	headers map[string]string
	next    http.RoundTripper
}

func (t *upstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = t.base.Scheme, t.base.Host
	basePath := strings.TrimRight(t.base.Path, "/")
	requestPath := req.URL.Path
	// Local gateway endpoints already include /v1. Avoid duplicating it when
	// the configured upstream base URL also ends in /v1.
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(requestPath, "/v1/") {
		requestPath = strings.TrimPrefix(requestPath, "/v1")
	}
	clone.URL.Path, clone.URL.RawPath, clone.Host = basePath+requestPath, "", t.base.Host
	if t.base.RawQuery != "" {
		if clone.URL.RawQuery != "" {
			clone.URL.RawQuery = t.base.RawQuery + "&" + clone.URL.RawQuery
		} else {
			clone.URL.RawQuery = t.base.RawQuery
		}
	}
	clone.Header = req.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.apiKey)
	for key, value := range t.headers {
		if strings.EqualFold(key, "Host") || strings.ContainsAny(key+value, "\r\n") {
			return nil, errors.New("unsafe upstream header")
		}
		clone.Header.Set(key, value)
	}
	return t.next.RoundTrip(clone)
}
