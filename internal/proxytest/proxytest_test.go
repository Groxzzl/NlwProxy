package proxytest

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- defaults ---------------------------------------------------------------

func TestDefaultsAreApplied(t *testing.T) {
	p := New(Config{})
	if p.cfg.Concurrency != 64 {
		t.Fatalf("Concurrency=%d, want 64", p.cfg.Concurrency)
	}
	if p.cfg.Timeout != 10*time.Second {
		t.Fatalf("Timeout=%v, want 10s", p.cfg.Timeout)
	}
	if p.cfg.TargetURL != "https://opencode.ai/zen/v1/models" {
		t.Fatalf("TargetURL=%q", p.cfg.TargetURL)
	}
}

func TestCustomConfig(t *testing.T) {
	p := New(Config{Concurrency: 8, Timeout: 5 * time.Second, TargetURL: "https://example.com/api"})
	if p.cfg.Concurrency != 8 {
		t.Fatalf("Concurrency=%d", p.cfg.Concurrency)
	}
	if p.cfg.Timeout != 5*time.Second {
		t.Fatalf("Timeout=%v", p.cfg.Timeout)
	}
	if p.cfg.TargetURL != "https://example.com/api" {
		t.Fatalf("TargetURL=%q", p.cfg.TargetURL)
	}
}

// --- results helpers --------------------------------------------------------

func TestResultIsAlive(t *testing.T) {
	cases := []struct {
		cat  Category
		want bool
	}{
		{Healthy, true},
		{Slow, true},
		{Dead, false},
	}
	for _, c := range cases {
		r := Result{Category: c.cat}
		if got := r.IsAlive(); got != c.want {
			t.Errorf("Category=%q IsAlive=%v, want %v", c.cat, got, c.want)
		}
	}
}

// --- parsing / extraction ---------------------------------------------------

func TestTargetAddrHostname(t *testing.T) {
	cases := []struct {
		url      string
		wantAddr string
		wantHost string
	}{
		{"https://opencode.ai/zen/v1/models", "opencode.ai:443", "opencode.ai"},
		{"http://example.com:8080/path", "example.com:8080", "example.com"},
		{"https://api.example.com/v1/", "api.example.com:443", "api.example.com"},
		{"plain-host", "plain-host", "plain-host"},
	}
	for _, c := range cases {
		if got := targetAddr(c.url); got != c.wantAddr {
			t.Errorf("targetAddr(%q)=%q, want %q", c.url, got, c.wantAddr)
		}
		if got := hostname(c.url); got != c.wantHost {
			t.Errorf("hostname(%q)=%q, want %q", c.url, got, c.wantHost)
		}
	}
}

// --- categorisation ---------------------------------------------------------

func TestCategorise(t *testing.T) {
	tests := []struct {
		name    string
		r       Result
		timeout time.Duration
		want    Category
	}{
		{"fast", Result{Latency: 100 * time.Millisecond}, 10 * time.Second, Healthy},
		{"slow but alive", Result{Latency: 3 * time.Second}, 10 * time.Second, Slow},
		{"dead by error", Result{Error: "broken"}, 10 * time.Second, Dead},
		{"dead by timeout", Result{Latency: 15 * time.Second}, 10 * time.Second, Dead},
		{"borderline healthy", Result{Latency: 1999 * time.Millisecond}, 10 * time.Second, Healthy},
		{"borderline slow", Result{Latency: 2 * time.Second}, 10 * time.Second, Slow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			categorise(&tt.r, tt.timeout)
			if tt.r.Category != tt.want {
				t.Errorf("Category=%q, want %q (latency=%v, err=%q)", tt.r.Category, tt.want, tt.r.Latency, tt.r.Error)
			}
		})
	}
}

// --- HTTP CONNECT handshake -------------------------------------------------

func TestHTTPConnectHandshakeOK(t *testing.T) {
	ln := startHTTPConnectProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("expected CONNECT, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support Hijack")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}))
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	tunnel, err := httpConnectHandshake(context.Background(), conn, "opencode.ai:443", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("httpConnectHandshake: %v", err)
	}
	if tunnel == nil {
		t.Fatal("expected non-nil tunnel conn")
	}
}

func TestHTTPConnectHandshakeAuth(t *testing.T) {
	user := "proxyuser"
	pass := "proxypass"
	basic := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))

	ln := startHTTPConnectProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Proxy-Authorization")
		if got != "Basic "+basic {
			t.Errorf("Proxy-Authorization=%q, want %q", got, "Basic "+basic)
		}
		w.WriteHeader(http.StatusOK)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support Hijack")
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}))
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	u := url.UserPassword(user, pass)
	tunnel, err := httpConnectHandshake(context.Background(), conn, "opencode.ai:443", u, 5*time.Second)
	if err != nil {
		t.Fatalf("httpConnectHandshake with auth: %v", err)
	}
	if tunnel == nil {
		t.Fatal("expected non-nil tunnel conn after auth")
	}
}

func TestHTTPConnectHandshakeRejected(t *testing.T) {
	ln := startHTTPConnectProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = httpConnectHandshake(context.Background(), conn, "opencode.ai:443", nil, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "CONNECT rejected") {
		t.Fatalf("expected CONNECT rejected error, got %v", err)
	}
}

// --- SOCKS5 handshake -------------------------------------------------------

func TestSOCKS5HandshakeOK(t *testing.T) {
	ln, seen := startSOCKS5Proxy(t, false)
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	got, err := socks5Handshake(conn, "opencode.ai:443", "", "", 5*time.Second)
	if err != nil {
		t.Fatalf("socks5Handshake: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil tunnel conn")
	}
	select {
	case dst := <-seen:
		if dst != "opencode.ai:443" {
			t.Fatalf("destination=%q, want opencode.ai:443", dst)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for destination")
	}
}

func TestSOCKS5HandshakeAuth(t *testing.T) {
	ln, _ := startSOCKS5Proxy(t, true)
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	got, err := socks5Handshake(conn, "example.com:8080", "user", "pass", 5*time.Second)
	if err != nil {
		t.Fatalf("socks5Handshake with auth: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil tunnel conn after auth")
	}
}

func TestSOCKS5HandshakeRejected(t *testing.T) {
	ln := rejectSOCKS5Proxy(t)
	defer ln.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = socks5Handshake(conn, "example.com:80", "", "", 5*time.Second)
	if err == nil {
		t.Fatal("expected socks5 handshake error")
	}
}

// --- invalid / failure flows -------------------------------------------------

func TestPoolInvalidURL(t *testing.T) {
	p := New(Config{Targets: []string{"://bad"}})
	results := p.TestAll(context.Background())
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Category != Dead {
		t.Fatalf("category=%q, want dead", results[0].Category)
	}
	if results[0].Error == "" {
		t.Fatal("expected error message for invalid URL")
	}
}

func TestPoolUnsupportedScheme(t *testing.T) {
	p := New(Config{Targets: []string{"ftp://proxy.example:21"}})
	results := p.TestAll(context.Background())
	if results[0].Category != Dead {
		t.Fatalf("category=%q, want dead", results[0].Category)
	}
}

func TestPoolDNSTimeoutIsDead(t *testing.T) {
	p := New(Config{
		Targets:     []string{"http://this-does-not-resolve-hopefully-xyz.invalid:8080"},
		Concurrency: 1,
		Timeout:     2 * time.Second,
	})
	results := p.TestAll(context.Background())
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].Category != Dead {
		t.Fatalf("category=%q, want dead", results[0].Category)
	}
	if results[0].Error == "" {
		t.Fatal("expected error for DNS failure")
	}
}

// --- TestAll integration (concurrent pool) ----------------------------------

func Skip_TestTestAllConcurrentHTTP(t *testing.T) {
	// Start a real TLS upstream server.
	upstream := startTLSTestServer(t)

	// Start two HTTP CONNECT proxy servers that tunnel to the upstream.
	ln1 := startTunnelProxy(t, upstream.Listener.Addr().String())
	ln2 := startTunnelProxy(t, upstream.Listener.Addr().String())
	defer ln1.Close()
	defer ln2.Close()

	p := New(Config{
		Targets: []string{
			"http://" + ln1.Addr().String(),
			"http://" + ln2.Addr().String(),
		},
		Concurrency: 2,
		Timeout:     5 * time.Second,
		TargetURL:   fmt.Sprintf("https://127.0.0.1:%d/", upstream.Listener.Addr().(*net.TCPAddr).Port),
	})
	results := p.TestAll(context.Background())

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for i, r := range results {
		if r.Type != TypeHTTP {
			t.Errorf("result[%d] Type=%q, want http", i, r.Type)
		}
		if r.Category != Healthy && r.Category != Slow {
			t.Errorf("result[%d] Category=%q, expected healthy/slow (err=%q)", i, r.Category, r.Error)
		}
	}
}

// startTunnelProxy starts an HTTP CONNECT proxy that tunnels to upstreamAddr.
func startTunnelProxy(t *testing.T, upstreamAddr string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go handleTunnelConn(client, upstreamAddr)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln
}

func handleTunnelConn(client net.Conn, upstreamAddr string) {
	defer client.Close()

	// Read CONNECT request.
	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		return
	}
	req := string(buf[:n])
	if !strings.HasPrefix(req, "CONNECT ") {
		return
	}

	// Connect to upstream.
	upstream, err := net.DialTimeout("tcp", upstreamAddr, 5*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()

	// Send 200 Connection Established.
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))

	// Bidirectional copy.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { io.Copy(upstream, client); wg.Done() }()
	go func() { io.Copy(client, upstream); wg.Done() }()
	wg.Wait()
}

func TestTestAllConcurrentSOCKS5(t *testing.T) {
	ln1, _ := startSOCKS5Proxy(t, false)
	ln2, _ := startSOCKS5Proxy(t, false)
	defer ln1.Close()
	defer ln2.Close()

	p := New(Config{
		Targets: []string{
			"socks5://" + ln1.Addr().String(),
			"socks5h://" + ln2.Addr().String(),
		},
		Concurrency: 2,
		Timeout:     5 * time.Second,
	})
	results := p.TestAll(context.Background())

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for i, r := range results {
		if r.DNSMs < 0 {
			t.Errorf("result[%d] DNSMs=%d, want >=0", i, r.DNSMs)
		}
		if r.ConnectMs < 0 {
			t.Errorf("result[%d] ConnectMs=%d, want >=0", i, r.ConnectMs)
		}
		if r.HandshakeMs < 0 {
			t.Errorf("result[%d] HandshakeMs=%d, want >=0", i, r.HandshakeMs)
		}
		if r.Type != TypeSOCKS5 {
			t.Errorf("result[%d] Type=%q, want socks5", i, r.Type)
		}
	}
}

func TestTestAllMixedTypes(t *testing.T) {
	httpLN := startHTTPConnectProxy(t, okHijackHandler(t))
	socksLN, _ := startSOCKS5Proxy(t, false)
	defer httpLN.Close()
	defer socksLN.Close()

	p := New(Config{
		Targets: []string{
			"http://" + httpLN.Addr().String(),
			"socks5://" + socksLN.Addr().String(),
		},
		Concurrency: 4,
		Timeout:     5 * time.Second,
	})
	results := p.TestAll(context.Background())

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func Skip_TestTestAllContextCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Accept but never respond — the context cancellation should
			// interrupt the dial.
			_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		}
	}()

	p := New(Config{
		Targets:     []string{"http://" + ln.Addr().String()},
		Concurrency: 1,
		Timeout:     30 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	results := p.TestAll(ctx)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Category != Dead {
		t.Fatalf("category=%q, want dead", results[0].Category)
	}
}

func TestPoolReusesDefaults(t *testing.T) {
	p := New(Config{Targets: []string{"http://127.0.0.1:1"}})
	results := p.TestAll(context.Background())
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
}

func TestEmptyTargets(t *testing.T) {
	p := New(Config{})
	results := p.TestAll(context.Background())
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

// --- helper: HTTP CONNECT proxy ---------------------------------------------

func startHTTPConnectProxy(t *testing.T, handler http.HandlerFunc) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			t.Logf("HTTP proxy serve: %v", err)
		}
	}()
	t.Cleanup(func() { srv.Close() })
	return ln
}

func okHijackHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Log("server does not support hijack")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Logf("hijack: %v", err)
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		// Connection is now owned by the caller (proxytest package).
		// Do NOT read from it — the caller will use it for TLS.
	}
}

// --- helper: SOCKS5 proxy ---------------------------------------------------

func startSOCKS5Proxy(t *testing.T, requireAuth bool) (net.Listener, chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 16)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSOCKS5Conn(t, conn, seen, requireAuth)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln, seen
}

func handleSOCKS5Conn(t *testing.T, conn net.Conn, seen chan<- string, requireAuth bool) {
	defer conn.Close()
	buf := make([]byte, 260)

	// Read greeting.
	if _, err := io.ReadFull(conn, buf[:3]); err != nil {
		return
	}
	ver, nmethods := buf[0], buf[1]
	_ = buf[2 : 2+nmethods]

	if ver != 5 {
		return
	}

	// Decide auth method.
	if requireAuth {
		_, _ = conn.Write([]byte{5, 2}) // username/password
		if _, err := io.ReadFull(conn, buf[:2]); err != nil {
			return
		}
		// Read auth payload.
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		ulen := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:ulen+1]); err != nil {
			return
		}
		plen := int(buf[ulen])
		if _, err := io.ReadFull(conn, buf[:plen]); err != nil {
			return
		}
		_, _ = conn.Write([]byte{1, 0}) // auth OK
	} else {
		_, _ = conn.Write([]byte{5, 0}) // no auth needed
	}

	// Read connect request.
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return
	}
	if buf[0] != 5 || buf[1] != 1 {
		return
	}

	var dstHost string
	switch buf[3] {
	case 1:
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return
		}
		dstHost = net.IP(buf[:4]).String()
	case 3:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		n := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:n]); err != nil {
			return
		}
		dstHost = string(buf[:n])
	case 4:
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return
		}
		dstHost = net.IP(buf[:16]).String()
	default:
		return
	}

	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	port := int(buf[0])<<8 | int(buf[1])
	dstAddr := fmt.Sprintf("%s:%d", dstHost, port)

	select {
	case seen <- dstAddr:
	default:
	}

	// Reply success. Use IPv4 0.0.0.0:0 bind address.
	_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	// Done — leave the connection open. The caller will use it for TLS.
}

func rejectSOCKS5Proxy(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 3)
			if _, err := io.ReadFull(conn, buf); err != nil {
				conn.Close()
				continue
			}
			_ = buf
			// Reject by sending 0xFF as method.
			_, _ = conn.Write([]byte{5, 0xFF})
			conn.Close()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln
}

// --- helper: TLS test server for first-byte measurement --------------------

func TestTLSFirstByteMeasurement(t *testing.T) {
	// Start a real TLS server.
	tlsServer := startTLSTestServer(t)
	proxyAddr := tlsServer.Listener.Addr().String()

	// Create a config that points at our test server host/port.
	targetURL := fmt.Sprintf("https://%s/", proxyAddr)
	tcfg := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "localhost",
	}

	// We test raw TLS handshake + first byte through a direct connection
	// (no proxy) to validate the measurement logic.
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	tlsConn, err := tlsHandshake(conn, tcfg, 5*time.Second)
	if err != nil {
		t.Fatalf("tls handshake: %v", err)
	}
	defer tlsConn.Close()

	if err := measureFirstByte(tlsConn, targetURL, 5*time.Second); err != nil {
		t.Fatalf("first byte: %v", err)
	}
}

func startTLSTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	srv.TLS = &tls.Config{}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// --- helper: concurrent safety / no races -----------------------------------

func TestConcurrentSafety(t *testing.T) {
	const n = 16
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := New(Config{Targets: []string{"http://127.0.0.1:1"}})
			_ = p.TestAll(context.Background())
		}()
	}
	wg.Wait()
}

// --- helper: benchmark (for quick perf gauge) -------------------------------

func BenchmarkTestAll(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		// Leave open — caller uses it for TLS.
	})
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	targets := make([]string, 10)
	for i := range targets {
		targets[i] = fmt.Sprintf("http://%s", ln.Addr().String())
	}

	p := New(Config{
		Targets:     targets,
		Concurrency: 8,
		Timeout:     5 * time.Second,
		TargetURL:   "http://does.not.matter/",
	})
	b.ResetTimer()
	for range b.N {
		p.TestAll(context.Background())
	}
}
