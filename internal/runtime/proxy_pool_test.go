package gatewayruntime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"nlwproxy/internal/config"
	"nlwproxy/internal/profiles"
	"nlwproxy/internal/proxymanager"
)

func TestReloadHealthyProxiesReplacesRoutesAndRoundRobinsWithoutDirect(t *testing.T) {
	var mu sync.Mutex
	requests := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Header.Get("X-Mock-Proxy"))
		mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer provider-secret" {
			t.Errorf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	proxy := func(id string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.RequestURI = ""
			r.Header.Set("X-Mock-Proxy", id)
			resp, err := http.DefaultTransport.RoundTrip(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			for key, values := range resp.Header {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(resp.StatusCode)
			var body [4096]byte
			for {
				n, readErr := resp.Body.Read(body[:])
				if n > 0 {
					_, _ = w.Write(body[:n])
				}
				if readErr != nil {
					break
				}
			}
		}))
	}
	p1, p2, dead := proxy("p1"), proxy("p2"), proxy("dead")
	defer p1.Close()
	defer p2.Close()
	defer dead.Close()

	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.LocalTokenEnv = "POOL_LOCAL_TOKEN"
	cfg.Upstreams = []config.Upstream{{Name: "provider", BaseURL: "https://provider.example", APIKeyEnv: "POOL_PROVIDER_KEY", Enabled: true}}
	rt, err := New(Options{Profile: profiles.Profile{ID: "pool", Config: cfg}, Credentials: mapCredentials{"POOL_LOCAL_TOKEN": "local", "POOL_PROVIDER_KEY": "provider-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	providerURL, _ := url.Parse(upstream.URL)
	rt.providers[0].base = providerURL
	entry := func(id string, server *httptest.Server, alive bool, latency time.Duration) proxymanager.ProxyEntry {
		u, _ := url.Parse(server.URL)
		return proxymanager.ProxyEntry{ID: id, Host: u.Hostname(), Port: mustPort(t, u.Port()), Scheme: proxymanager.SchemeHTTP, Alive: alive, Latency: latency}
	}
	if err := rt.ReloadHealthyProxies([]proxymanager.ProxyEntry{entry("p1", p1, true, time.Millisecond), entry("dead", dead, false, time.Millisecond), entry("p2", p2, true, 2*time.Millisecond)}); err != nil {
		t.Fatal(err)
	}

	if got := rt.Snapshot().Routes; len(got) != 2 || got["provider@p1"].Health != "healthy" || got["provider@p2"].Health != "healthy" {
		t.Fatalf("routes after reload = %#v", got)
	}
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("Authorization", "Bearer local")
		rec := httptest.NewRecorder()
		rt.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	mu.Lock()
	gotRequests := append([]string(nil), requests...)
	mu.Unlock()
	want := []string{"p1", "p2", "p1", "p2"}
	if fmt.Sprint(gotRequests) != fmt.Sprint(want) {
		t.Fatalf("proxy order = %v, want %v", gotRequests, want)
	}

	if err := rt.ReloadHealthyProxies([]proxymanager.ProxyEntry{entry("p2", p2, true, 2*time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	if got := rt.Snapshot().Routes; len(got) != 1 || got["provider@p2"].Health != "healthy" {
		t.Fatalf("routes after hot reload = %#v", got)
	}
}

func mustPort(t *testing.T, value string) int {
	t.Helper()
	var port int
	if _, err := fmt.Sscan(value, &port); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestProxyManagerTestAllReloadsRuntimeSelector(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer health.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RequestURI = ""
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	}))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)

	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.LocalTokenEnv = "HOOK_LOCAL_TOKEN"
	cfg.Upstreams = []config.Upstream{{Name: "provider", BaseURL: "https://provider.example/v1", APIKeyEnv: "HOOK_PROVIDER_KEY", Enabled: true}}
	rt, err := New(Options{Profile: profiles.Profile{ID: "hook", Config: cfg}, Credentials: mapCredentials{"HOOK_LOCAL_TOKEN": "local", "HOOK_PROVIDER_KEY": "key"}})
	if err != nil {
		t.Fatal(err)
	}
	manager := proxymanager.New(nil)
	manager.SetAliveListener(func(entries []proxymanager.ProxyEntry) { _ = rt.ReloadHealthyProxies(entries) })
	manager.Add(proxymanager.ProxyEntry{Host: u.Hostname(), Port: mustPort(t, u.Port()), Scheme: proxymanager.SchemeHTTP})
	results := manager.TestAll(context.Background(), health.URL)
	if len(results) != 1 || !results[0].Alive {
		t.Fatalf("test results = %#v", results)
	}
	if got := rt.Snapshot().Routes; len(got) != 1 || got["provider@proxy-1"].Health != "healthy" {
		t.Fatalf("selector routes = %#v", got)
	}
}
