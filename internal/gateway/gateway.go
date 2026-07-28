// Package gateway implements the loopback OpenAI-compatible HTTP gateway.
package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"sync/atomic"
	"time"

	"nlwproxy/internal/metrics"
	"nlwproxy/internal/retry"
	"nlwproxy/internal/routing"
	"nlwproxy/internal/stream"
)

type Config struct {
	Token          string
	StrictOpenCode bool
	ModelAlias     string
	MaxBodyBytes   int64
	Attempts       int
	Events         *metrics.EventBus
	Models         ModelService
	NoRouteCode    string
}

type Gateway struct {
	cfg       Config
	selector  *routing.Selector
	requestID atomic.Uint64
}

type Model struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
	RouteID string `json:"route_id,omitempty"`
}
type ModelTest struct {
	Model     string        `json:"model"`
	Available bool          `json:"available"`
	RouteID   string        `json:"route_id,omitempty"`
	Status    int           `json:"status,omitempty"`
	Latency   time.Duration `json:"latency_ns,omitempty"`
	ErrorCode string        `json:"error_code,omitempty"`
}
type ModelService interface {
	Discover(context.Context) ([]Model, error)
	Test(context.Context, string) ModelTest
}

func New(cfg Config, selector *routing.Selector) *Gateway {
	if cfg.ModelAlias == "" {
		cfg.ModelAlias = "opencode-route"
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 20 << 20
	}
	if cfg.Attempts <= 0 {
		cfg.Attempts = 2
	}
	return &Gateway{cfg: cfg, selector: selector}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "routes": g.selector.Snapshots(time.Now())})
		return
	}
	if !g.authorized(r) {
		writeError(w, http.StatusUnauthorized, "NLP-SEC-002", "invalid local token")
		return
	}
	switch r.URL.Path {
	case "/v1/models":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		if g.cfg.Models != nil {
			models, err := g.cfg.Models.Discover(r.Context())
			if err != nil {
				writeError(w, http.StatusBadGateway, "model_discovery_failed", "model discovery failed")
				return
			}
			for i := range models {
				if models[i].Object == "" {
					models[i].Object = "model"
				}
				if models[i].OwnedBy == "" {
					models[i].OwnedBy = "nlwproxy"
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
			return
		}
		g.proxy(w, r)
	case "/v1/chat/completions", "/v1/responses":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		g.proxy(w, r)
	default:
		if g.cfg.Models != nil && r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/models/") && strings.HasSuffix(r.URL.Path, "/test") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/models/"), "/test")
			if id == "" || strings.Contains(id, "/") {
				writeError(w, http.StatusNotFound, "not_found", "model not found")
				return
			}
			writeJSON(w, http.StatusOK, g.cfg.Models.Test(r.Context(), id))
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
	}
}

func (g *Gateway) authorized(r *http.Request) bool {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	got := parts[1]
	if got == "" || len(got) != len(g.cfg.Token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(g.cfg.Token)) == 1
}

func isOpenCode(r *http.Request) bool {
	if r.Header.Get("Origin") != "" {
		return false
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	marker := strings.ToLower(r.Header.Get("X-OpenCode-Client"))
	return strings.Contains(ua, "opencode") || marker == "opencode"
}

func (g *Gateway) proxy(w http.ResponseWriter, incoming *http.Request) {
	started := time.Now()
	body, err := readLimited(incoming.Body, g.cfg.MaxBodyBytes)
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds configured limit")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_body", "could not read request body")
		}
		return
	}
	event := metrics.Request{RequestID: fmt.Sprint(g.requestID.Add(1)), Endpoint: incoming.URL.Path, StartedAt: started, RequestBytes: int64(len(body))}
	var envelope struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &envelope)
	event.RequestedModel = envelope.Model
	if g.cfg.Events != nil {
		g.cfg.Events.Start(event)
		defer func() {
			event.Duration = time.Since(started)
			if event.Duration <= 0 {
				event.Duration = time.Nanosecond
			}
			g.cfg.Events.Publish(event)
		}()
	}
	exclude := map[string]bool{}
	for attempt := 0; attempt < g.cfg.Attempts; attempt++ {
		target, ok := g.selector.NextSession(time.Now(), exclude, incoming.Header.Get("X-OpenCode-Session"))
		if !ok {
			code, message := g.cfg.NoRouteCode, "no eligible upstream route"
			if code == "" {
				code = "NLP-UP-001"
			} else {
				message = "no healthy proxy route"
				if soonest := g.selector.SoonestRecovery(time.Now()); !soonest.IsZero() {
					wait := time.Until(soonest).Round(time.Second)
					message = fmt.Sprintf("all proxies rate-limited; soonest recovery in %s", wait)
				}
			}
			event.Status, event.ErrorCode, event.RetryCount = http.StatusServiceUnavailable, code, attempt
			writeError(w, http.StatusServiceUnavailable, code, message)
			return
		}
		exclude[target.Name] = true
		event.RouteID, event.RetryCount = target.Name, attempt
		if g.cfg.Events != nil {
			event.State = metrics.RequestActive
			g.cfg.Events.Update(event)
		}
		if target.Transport == nil {
			g.selector.RecordFailure(target.Name, time.Now())
			continue
		}
		req := incoming.Clone(incoming.Context())
		req.RequestURI = ""
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header = incoming.Header.Clone()
		stripHopHeaders(req.Header)
		release, acquired := g.selector.Acquire(target.Name)
		if !acquired {
			continue
		}
		start := time.Now()
		resp, roundErr := target.Transport.RoundTrip(req)
		if roundErr != nil {
			release()
			g.selector.RecordRequest(target.Name, routing.RequestResult{Latency: time.Since(start), UsedAt: time.Now()})
			g.selector.RecordFailure(target.Name, time.Now())
			if incoming.Context().Err() != nil {
				return
			}
			continue
		}
		decision := retry.Classify(resp.StatusCode, nil, nil, false)
		var rlBody []byte
		if resp.StatusCode == 429 || resp.StatusCode == 400 {
			rlBody, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			decision = retry.Classify(resp.StatusCode, rlBody, nil, false)
			resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(rlBody), resp.Body))
		}
		if decision.Reason == "rate_limit" || decision.Reason == "quota" {
			cd := retry.ParseRetryAfter(resp.Header.Get("Retry-After"), rlBody, time.Now())
			if cd <= 0 {
				cd = 15 * time.Minute // conservative default when provider gives no hint
			}
			g.selector.SetCooldown(target.Name, time.Now(), cd)
		}
		if decision.Retry && attempt+1 < g.cfg.Attempts {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			release()
			g.selector.RecordFailure(target.Name, time.Now())
			continue
		}
		latency := time.Since(start)
		g.selector.RecordSuccess(target.Name, latency)
		event.Status = resp.StatusCode
		copyHeader(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		result := g.relay(incoming.Context(), w, resp.Body, started, resp.Header.Get("Content-Type"), func(ttft time.Duration) {
			event.TTFT = ttft
			if event.TTFT <= 0 {
				event.TTFT = time.Nanosecond
			}
			event.State = metrics.RequestStreaming
			if g.cfg.Events != nil {
				g.cfg.Events.Update(event)
			}
		})
		event.TTFT, event.ResponseBytes = result.TTFT, result.Bytes
		event.InputTokens, event.OutputTokens, event.TotalTokens = result.InputTokens, result.OutputTokens, result.TotalTokens
		g.selector.RecordRequest(target.Name, routing.RequestResult{Status: resp.StatusCode, Latency: latency, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens, UsedAt: time.Now()})
		if result.Err != nil && incoming.Context().Err() == nil {
			event.ErrorCode = "stream_relay_failed"
		}
		release()
		return
	}
	event.Status, event.ErrorCode = http.StatusBadGateway, "NLP-UP-001"
	writeError(w, http.StatusBadGateway, "NLP-UP-001", "upstream connection failed")
}

func (g *Gateway) relay(ctx context.Context, w http.ResponseWriter, body io.ReadCloser, started time.Time, contentType string, firstByte func(time.Duration)) stream.Result {
	defer body.Close()
	return stream.RelayObserved(ctx, w, body, started, firstByte, contentType)
}

var errBodyTooLarge = errors.New("body too large")

func readLimited(r io.ReadCloser, max int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errBodyTooLarge
	}
	return b, nil
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func stripHopHeaders(h http.Header) {
	for _, v := range h.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			h.Del(textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name)))
		}
	}
	for _, name := range hopHeaders {
		h.Del(name)
	}
}
func copyHeader(dst, src http.Header) {
	clone := src.Clone()
	stripHopHeaders(clone)
	for k, vv := range clone {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Serve(ctx context.Context, listen string, handler http.Handler, shutdownTimeout time.Duration) error {
	return ServeWithLifecycle(ctx, listen, handler, shutdownTimeout, nil)
}

type GatewayState string

const (
	StateStarting GatewayState = "starting"
	StateRunning  GatewayState = "running"
	StateStopping GatewayState = "stopping"
	StateStopped  GatewayState = "stopped"
	StateFailed   GatewayState = "failed"
)

type LifecycleEvent struct {
	State  GatewayState `json:"state"`
	Listen string       `json:"listen"`
	At     time.Time    `json:"at"`
	Error  string       `json:"error,omitempty"`
}

type Lifecycle interface{ GatewayLifecycle(LifecycleEvent) }

func notifyLifecycle(h Lifecycle, state GatewayState, listen string, err error) {
	if h == nil {
		return
	}
	event := LifecycleEvent{State: state, Listen: listen, At: time.Now()}
	if err != nil {
		event.Error = err.Error()
	}
	h.GatewayLifecycle(event)
}

func ServeWithLifecycle(ctx context.Context, listen string, handler http.Handler, shutdownTimeout time.Duration, lifecycle Lifecycle) error {
	notifyLifecycle(lifecycle, StateStarting, listen, nil)
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		notifyLifecycle(lifecycle, StateFailed, listen, err)
		return err
	}
	server := &http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ln) }()
	notifyLifecycle(lifecycle, StateRunning, ln.Addr().String(), nil)
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			notifyLifecycle(lifecycle, StateStopped, listen, nil)
			return nil
		}
		notifyLifecycle(lifecycle, StateFailed, listen, err)
		return err
	case <-ctx.Done():
		notifyLifecycle(lifecycle, StateStopping, listen, nil)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			notifyLifecycle(lifecycle, StateFailed, listen, err)
			return fmt.Errorf("shutdown: %w", err)
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			notifyLifecycle(lifecycle, StateStopped, listen, nil)
			return nil
		}
		notifyLifecycle(lifecycle, StateFailed, listen, err)
		return err
	}
}
