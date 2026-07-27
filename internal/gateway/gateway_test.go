package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nlwproxy/internal/metrics"
	"nlwproxy/internal/routing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testGateway(t *testing.T, rt http.RoundTripper) *Gateway {
	t.Helper()
	s := routing.NewSelector([]routing.Target{{Name: "primary", Priority: 1, Transport: rt}}, routing.BreakerConfig{FailureThreshold: 2, OpenTimeout: time.Minute})
	return New(Config{Token: "local-secret", StrictOpenCode: true, ModelAlias: "opencode-route", MaxBodyBytes: 1024, Attempts: 2}, s)
}

func authorized(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer local-secret")
	r.Header.Set("User-Agent", "opencode/1.0")
	return r
}

func TestHealthIsPublicAndModelsRequiresAuth(t *testing.T) {
	g := testGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) { panic("unexpected") }))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("health: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("models status=%d", w.Code)
	}

	w = httptest.NewRecorder()
}

func TestModelsProxiesUpstreamCatalogWithoutChangingIDs(t *testing.T) {
	var gotPath string
	g := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"object":"list","data":[{"id":"provider/model:latest","object":"model","owned_by":"provider"}]}`)), Request: r}, nil
	}))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, authorized(http.MethodGet, "/v1/models", ""))
	if w.Code != 200 || gotPath != "/v1/models" || !strings.Contains(w.Body.String(), `"id":"provider/model:latest"`) || strings.Contains(w.Body.String(), "opencode-route") {
		t.Fatalf("path=%q status=%d body=%s", gotPath, w.Code, w.Body.String())
	}
}

func TestArbitraryLocalAIUserAgentAllowedWithValidToken(t *testing.T) {
	g := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	}))
	r := authorized(http.MethodPost, "/v1/responses", `{"model":"vendor/model-v2"}`)
	r.Header.Set("User-Agent", "continue.dev/1.2")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRequestedModelIsPreservedExactly(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			const body = `{"model":"vendor/model:2026-07","input":"private"}`
			g := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
				got, _ := io.ReadAll(r.Body)
				if string(got) != body {
					t.Fatalf("body changed: %s", got)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			}))
			w := httptest.NewRecorder()
			g.ServeHTTP(w, authorized(http.MethodPost, path, body))
			if w.Code != 200 {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func TestUsageTelemetryParsesJSONAndSSE(t *testing.T) {
	cases := []struct {
		name, contentType, response string
		prompt, completion, total   int64
	}{
		{"json", "application/json", `{"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18},"output":"PRIVATE"}`, 11, 7, 18},
		{"sse", "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":3,\"total_tokens\":8},\"output\":\"PRIVATE\"}}\n\ndata: [DONE]\n\n", 5, 3, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := metrics.NewEventBus(2)
			g := New(Config{Token: "local-secret", Events: bus}, routing.NewSelector([]routing.Target{{Name: "up", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.response)), Request: r}, nil
			})}}, routing.BreakerConfig{}))
			w := newFlushRecorder()
			g.ServeHTTP(w, authorized(http.MethodPost, "/v1/responses", `{"model":"m","input":"TOP SECRET"}`))
			e := bus.Snapshot().Events[0]
			if e.InputTokens != tc.prompt || e.OutputTokens != tc.completion || e.TotalTokens != tc.total {
				t.Fatalf("event=%+v", e)
			}
			encoded, _ := json.Marshal(e)
			if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "TOP SECRET") {
				t.Fatalf("content leaked: %s", encoded)
			}
		})
	}
}

func TestRejectsMalformedAuthorizationSchemes(t *testing.T) {
	g := testGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) { panic("unexpected") }))
	for _, value := range []string{"local-secret", "Basic local-secret", "Bearer", "Bearer local-secret extra"} {
		r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		r.Header.Set("Authorization", value)
		r.Header.Set("User-Agent", "opencode/1.0")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q status=%d", value, w.Code)
		}
	}
}

func TestValidTokenAllowsAnyLocalClientHeaders(t *testing.T) {
	g := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"object":"list","data":[]}`)), Request: r}, nil
	}))
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("User-Agent", "curl/8") },
		func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
	} {
		r := authorized(http.MethodGet, "/v1/models", "")
		mutate(r)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestForwardsBothPostEndpointsAndFiltersHopHeaders(t *testing.T) {
	var paths []string
	g := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Connection") != "" || r.Header.Get("X-Hop") != "" {
			t.Fatalf("hop headers leaked: %#v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "Connection": {"close"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
	}))
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		r := authorized(http.MethodPost, path, `{"model":"x"}`)
		r.Header.Set("Connection", "X-Hop")
		r.Header.Set("X-Hop", "secret")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Connection") != "" || w.Body.String() != `{"model":"x"}` {
			t.Fatalf("%s: %d %#v %s", path, w.Code, w.Header(), w.Body.String())
		}
	}
	if len(paths) != 2 {
		t.Fatalf("paths=%v", paths)
	}
}

func TestBodyLimitAndMethodValidation(t *testing.T) {
	g := testGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) { panic("unexpected") }))
	g.cfg.MaxBodyBytes = 4
	w := httptest.NewRecorder()
	g.ServeHTTP(w, authorized(http.MethodPost, "/v1/chat/completions", "12345"))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d", w.Code)
	}
	w = httptest.NewRecorder()
	g.ServeHTTP(w, authorized(http.MethodGet, "/v1/chat/completions", ""))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestRetriesNetworkAnd503ButNeverAuthRateLimitOrQuota(t *testing.T) {
	cases := []struct {
		name      string
		first     int
		body      string
		wantCalls int
	}{
		{"transient", 503, `temporary`, 2}, {"auth", 401, `bad key`, 1}, {"forbidden", 403, `denied`, 1}, {"rate", 429, `rate limit`, 1}, {"quota", 400, `{"error":{"code":"insufficient_quota"}}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			first := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: tc.first, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			second := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
			})
			s := routing.NewSelector([]routing.Target{{Name: "a", Priority: 1, Transport: first}, {Name: "b", Priority: 2, Transport: second}}, routing.BreakerConfig{})
			g := New(Config{Token: "local-secret", StrictOpenCode: true, MaxBodyBytes: 1024, Attempts: 2}, s)
			w := httptest.NewRecorder()
			g.ServeHTTP(w, authorized(http.MethodPost, "/v1/responses", `{"x":1}`))
			if int(calls.Load()) != tc.wantCalls {
				t.Fatalf("calls=%d status=%d", calls.Load(), w.Code)
			}
		})
	}
}

func TestRetriesNetworkErrorBeforeResponse(t *testing.T) {
	var calls atomic.Int32
	bad := roundTripFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("connection reset") })
	good := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	})
	s := routing.NewSelector([]routing.Target{{Name: "a", Priority: 1, Transport: bad}, {Name: "b", Priority: 2, Transport: good}}, routing.BreakerConfig{})
	g := New(Config{Token: "local-secret", StrictOpenCode: true, MaxBodyBytes: 1024, Attempts: 2}, s)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, authorized(http.MethodPost, "/v1/responses", `{}`))
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d", w.Code, calls.Load())
	}
}

type cancelBody struct {
	ctx  context.Context
	sent bool
}

func (b *cancelBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, "data: one\n\n"), nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (*cancelBody) Close() error { return nil }

func TestSSEFlushesAndClientCancellationReachesUpstream(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	g := testGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		go func() { <-r.Context().Done(); close(upstreamCanceled) }()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &cancelBody{ctx: r.Context()}, Request: r}, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	r := authorized(http.MethodPost, "/v1/chat/completions", `{"stream":true}`).WithContext(ctx)
	w := newFlushRecorder()
	done := make(chan struct{})
	go func() { g.ServeHTTP(w, r); close(done) }()
	select {
	case <-w.flushed:
	case <-time.After(time.Second):
		t.Fatal("SSE was not flushed")
	}
	cancel()
	select {
	case <-upstreamCanceled:
	case <-time.After(time.Second):
		t.Fatal("upstream not canceled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop")
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{httptest.NewRecorder(), make(chan struct{}, 1)}
}
func (f *flushRecorder) Flush() {
	f.ResponseRecorder.Flush()
	select {
	case f.flushed <- struct{}{}:
	default:
	}
}

func TestHealthJSONDoesNotExposeTransport(t *testing.T) {
	g := testGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, nil }))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if _, ok := v["routes"]; !ok {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestPublishesMetadataOnlyRequestEventWithModelRouteAndRetries(t *testing.T) {
	bus := metrics.NewEventBus(8)
	first := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("temporary")), Request: r}, nil
	})
	second := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"safe"}`)), Request: r}, nil
	})
	s := routing.NewSelector([]routing.Target{{Name: "a", Priority: 1, Transport: first}, {Name: "b", Priority: 2, Transport: second}}, routing.BreakerConfig{})
	g := New(Config{Token: "local-secret", StrictOpenCode: true, MaxBodyBytes: 1024, Attempts: 2, Events: bus}, s)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, authorized(http.MethodPost, "/v1/responses", `{"model":"gpt-test","input":"TOP SECRET"}`))

	snapshot := bus.Snapshot()
	if snapshot.Total != 1 || snapshot.Active != 0 || len(snapshot.Events) != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	e := snapshot.Events[0]
	if e.RequestedModel != "gpt-test" || e.Endpoint != "/v1/responses" || e.RouteID != "b" || e.Status != 200 || e.RetryCount != 1 || e.Duration <= 0 || e.ResponseBytes == 0 {
		t.Fatalf("event=%+v", e)
	}
	encoded, _ := json.Marshal(e)
	if strings.Contains(string(encoded), "TOP SECRET") || strings.Contains(string(encoded), `"input"`) {
		t.Fatalf("request content leaked: %s", encoded)
	}
}

func TestStreamingEventRecordsTTFTAfterFirstChunk(t *testing.T) {
	bus := metrics.NewEventBus(4)
	g := New(Config{Token: "local-secret", StrictOpenCode: true, MaxBodyBytes: 1024, Events: bus}, routing.NewSelector([]routing.Target{{Name: "stream", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		time.Sleep(5 * time.Millisecond)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: one\n\n")), Request: r}, nil
	})}}, routing.BreakerConfig{}))
	w := newFlushRecorder()
	g.ServeHTTP(w, authorized(http.MethodPost, "/v1/chat/completions", `{"model":"stream-model","stream":true}`))
	e := bus.Snapshot().Events[0]
	if e.TTFT <= 0 || e.Duration < e.TTFT || e.RequestedModel != "stream-model" || e.ResponseBytes == 0 {
		t.Fatalf("event=%+v", e)
	}
}

func TestModelDiscoveryAndTestUseConfiguredHook(t *testing.T) {
	models := &staticModels{models: []Model{{ID: "alpha", RouteID: "a"}, {ID: "beta", RouteID: "b"}}}
	g := New(Config{Token: "local-secret", StrictOpenCode: true, Models: models}, routing.NewSelector(nil, routing.BreakerConfig{}))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, authorized(http.MethodGet, "/v1/models", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"alpha"`) || !strings.Contains(w.Body.String(), `"route_id":"a"`) {
		t.Fatalf("models: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	g.ServeHTTP(w, authorized(http.MethodPost, "/v1/models/alpha/test", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":true`) || models.tested != "alpha" {
		t.Fatalf("test: %d %s tested=%q", w.Code, w.Body.String(), models.tested)
	}
}

type staticModels struct {
	models []Model
	tested string
}

func (s *staticModels) Discover(context.Context) ([]Model, error) { return s.models, nil }
func (s *staticModels) Test(_ context.Context, id string) ModelTest {
	s.tested = id
	return ModelTest{Model: id, Available: true, RouteID: "a", Status: 200}
}

func TestLifecycleReceivesGatewayStateTransitions(t *testing.T) {
	lifecycle := &recordingLifecycle{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ServeWithLifecycle(ctx, "127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), time.Second, lifecycle); err != nil {
		t.Fatal(err)
	}
	if len(lifecycle.states) < 2 || lifecycle.states[0].State != StateStarting || lifecycle.states[len(lifecycle.states)-1].State != StateStopped {
		t.Fatalf("states=%+v", lifecycle.states)
	}
}

func TestActiveSnapshotIncludesInFlightStream(t *testing.T) {
	bus := metrics.NewEventBus(4)
	release := make(chan struct{})
	entered := make(chan struct{})
	g := New(Config{Token: "local-secret", StrictOpenCode: true, Events: bus}, routing.NewSelector([]routing.Target{{Name: "slow", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	})}}, routing.BreakerConfig{}))
	done := make(chan struct{})
	go func() {
		g.ServeHTTP(httptest.NewRecorder(), authorized(http.MethodPost, "/v1/responses", `{"model":"slow"}`))
		close(done)
	}()
	<-entered
	if got := bus.Snapshot().Active; got != 1 {
		t.Fatalf("active=%d", got)
	}
	close(release)
	<-done
	if got := bus.Snapshot().Active; got != 0 {
		t.Fatalf("active after completion=%d", got)
	}
}

func TestGatewayRecordsPerRouteTokensAndDoesNotFailOverAuthOrRateLimit(t *testing.T) {
	for _, status := range []int{401, 403, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var secondCalls atomic.Int32
			first := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"usage":{"prompt_tokens":3,"completion_tokens":5}}`)), Request: r}, nil
			})
			second := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				secondCalls.Add(1)
				return nil, errors.New("must not rotate")
			})
			s := routing.New([]routing.Target{{Name: "a", TransportType: "http", Transport: first}, {Name: "b", TransportType: "direct", Transport: second}}, routing.Config{Strategy: routing.Priority})
			g := New(Config{Token: "local-secret", StrictOpenCode: true, Attempts: 2}, s)
			w := httptest.NewRecorder()
			g.ServeHTTP(w, authorized(http.MethodPost, "/v1/responses", `{}`))
			got := s.Snapshots(time.Now())["a"]
			if secondCalls.Load() != 0 || got.Total != 1 || got.Errors != 1 || got.InputTokens != 3 || got.OutputTokens != 5 || got.LastUsed.IsZero() {
				t.Fatalf("second=%d snapshot=%+v", secondCalls.Load(), got)
			}
		})
	}
}

type recordingLifecycle struct{ states []LifecycleEvent }

func (r *recordingLifecycle) GatewayLifecycle(e LifecycleEvent) { r.states = append(r.states, e) }
