package tuiapp

import (
	"testing"
	"time"

	"nlwproxy/internal/config"
	"nlwproxy/internal/geo"
	"nlwproxy/internal/metrics"
	"nlwproxy/internal/profiles"
	"nlwproxy/internal/proxymanager"
	gatewayruntime "nlwproxy/internal/runtime"
)

func TestDashboardSnapshotEnrichesRequestWithProxyGeo(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.LocalTokenEnv = "GEO_LOCAL_TOKEN"
	cfg.ProxyOnly = true
	cfg.Upstreams = []config.Upstream{{Name: "provider", BaseURL: "https://provider.example/v1", APIKeyEnv: "GEO_PROVIDER_KEY", Enabled: true}}
	runtime, err := gatewayruntime.New(gatewayruntime.Options{Profile: profiles.Profile{ID: "geo", Config: cfg}, Credentials: testCredentials{"GEO_LOCAL_TOKEN": "local", "GEO_PROVIDER_KEY": "provider"}})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewRuntimeAdapter(runtime)
	id := adapter.proxies.Add(proxymanager.ProxyEntry{Host: "1.1.1.1", Port: 8080, Scheme: proxymanager.SchemeHTTP, Alive: true, Geo: geo.Result{Country: "Singapore", City: "Singapore", ASN: "AS1"}})
	event := metrics.Request{RequestID: "req-geo", RouteID: "provider@" + id, RequestedModel: "model-a", StartedAt: time.Now(), Status: 200, State: metrics.RequestCompleted}
	runtime.Events().Publish(event)
	snapshot := adapter.dashboardSnapshot()
	if len(snapshot.Recent) != 1 {
		t.Fatalf("recent=%d", len(snapshot.Recent))
	}
	got := snapshot.Recent[0]
	if got.ProxyID != id || got.ProxyCountry != "Singapore" || got.ProxyCity != "Singapore" || got.ProxyASN != "AS1" {
		t.Fatalf("geo metadata not enriched: %+v", got)
	}
}

type testCredentials map[string]string

func (m testCredentials) Lookup(name string) (string, error) { return m[name], nil }
