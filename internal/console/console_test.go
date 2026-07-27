package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nlwproxy/internal/config"
)

func TestMaskSecret(t *testing.T) {
	for in, want := range map[string]string{"": "not set", "abc": "***", "sk-1234567890": "sk-…7890"} {
		if got := MaskSecret(in); got != want {
			t.Fatalf("MaskSecret(%q)=%q want %q", in, got, want)
		}
	}
}

func TestGenerateToken(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 40 || a == b {
		t.Fatalf("tokens are not sufficiently random: %q %q", a, b)
	}
}

func TestBuildConfigAndPersistSetup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nlwproxy.json")
	var persisted = map[string]string{}
	settings := Settings{Provider: "Acme", BaseURL: "https://api.acme.test/v1", APIKey: "secret-key", APIKeyEnv: "ACME_API_KEY", LocalToken: "local-token", Listen: "127.0.0.1:8787", ModelAlias: "opencode-route"}
	cfg := BuildConfig(settings)
	if err := PersistSetup(path, cfg, settings, func(k, v string) error { persisted[k] = v; return nil }); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Upstreams) != 1 || loaded.Upstreams[0].Name != "Acme" || loaded.Upstreams[0].APIKeyEnv != "ACME_API_KEY" {
		t.Fatalf("config=%+v", loaded)
	}
	if persisted["ACME_API_KEY"] != "secret-key" || persisted["NLW_PROXY_LOCAL_TOKEN"] != "local-token" {
		t.Fatalf("persisted=%v", persisted)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "secret-key") || strings.Contains(string(data), "local-token") {
		t.Fatal("secret leaked to config")
	}
}

func TestTestProviderUsesModelsEndpointAndAuthorization(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := TestProvider(ctx, srv.Client(), srv.URL+"/v1", "abc"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models" || gotAuth != "Bearer abc" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
}

func TestMetricsHandlerCapturesMetadataOnly(t *testing.T) {
	stats := NewStats(4)
	h := stats.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"secret-model","messages":[{"content":"private prompt"}]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	s := stats.Snapshot()
	if s.Requests != 1 || len(s.Recent) != 1 || s.Recent[0].Endpoint != "/v1/chat/completions" || s.Recent[0].Status != 201 {
		t.Fatalf("snapshot=%+v", s)
	}
	text := RenderDashboard(View{BaseURL: "http://127.0.0.1:8787/v1", APIKey: "local-secret", Provider: "Acme", ModelAlias: "opencode-route", Status: "ONLINE", Started: time.Now().Add(-time.Second), Stats: s}, false)
	if strings.Contains(text, "private prompt") || strings.Contains(text, "secret-model") || strings.Contains(text, "local-secret") {
		t.Fatalf("secret/content leak: %s", text)
	}
	for _, want := range []string{"Acme", "opencode-route", "REQUESTS", "RECENT REQUESTS", "loc…cret"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}
