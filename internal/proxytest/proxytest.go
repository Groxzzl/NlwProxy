// Package proxytest provides a multi-threaded proxy health tester for
// HTTP CONNECT and SOCKS5 proxies. It measures fine-grained per-hop timing
// (DNS, TCP connect, proxy handshake, TLS, first-byte) and categorises each
// proxy as healthy, slow, or dead.
package proxytest

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// ProxyType indicates the proxy protocol.
type ProxyType string

const (
	TypeHTTP   ProxyType = "http"
	TypeSOCKS5 ProxyType = "socks5"
)

// Category classifies proxy health after a test run.
type Category string

const (
	Healthy Category = "healthy"
	Slow    Category = "slow"
	Dead    Category = "dead"
)

// Result holds the per-proxy test breakdown. All durations are in
// milliseconds for easy JSON serialisation. Latency is the wall-clock
// total from start to finish of the full test cycle.
type Result struct {
	Type        ProxyType `json:"type"`
	Host        string    `json:"host"`
	Category    Category  `json:"category"`
	LatencyMs   int64     `json:"latency_ms"`
	DNSMs       int64     `json:"dns_ms"`
	ConnectMs   int64     `json:"connect_ms"`
	HandshakeMs int64     `json:"handshake_ms"`
	TLSMs       int64     `json:"tls_ms"`
	FirstByteMs int64     `json:"first_byte_ms,omitempty"`
	Error       string    `json:"error,omitempty"`
	ProxyURL    string    `json:"proxy_url"`
	Username    string    `json:"username,omitempty"`

	// Duration versions for convenience.
	Latency time.Duration `json:"-"`
}

// IsAlive returns true when the proxy is usable (healthy or slow).
func (r Result) IsAlive() bool { return r.Category == Healthy || r.Category == Slow }

// Config configures the proxy test pool.
type Config struct {
	// Targets is a list of proxy URLs in standard form, e.g.
	// "http://user:pass@192.168.1.1:8080" or "socks5://10.0.0.1:1080".
	Targets []string

	// Concurrency limits the number of simultanous proxy tests.
	// Defaults to 64 when zero.
	Concurrency int

	// Timeout is the per-hop timeout applied to DNS, TCP, TLS and
	// HTTP operations. Defaults to 10 s when zero.
	Timeout time.Duration

	// TargetURL is the upstream endpoint used for the TLS handshake
	// and first-byte measurement. Defaults to
	// https://opencode.ai/zen/v1/models when empty.
	TargetURL string

	// TLSConfig is used for the TLS handshake through the proxy tunnel.
	// When nil a client config with InsecureSkipVerify=true is used
	// (we care about liveness, not identity in the health check).
	TLSConfig *tls.Config
}

// Pool manages concurrent proxy testing with a bounded worker pool.
type Pool struct {
	cfg    Config
	cancel context.CancelFunc
}

// New creates a test Pool with the supplied config. Defaults are applied
// to unset fields.
func New(cfg Config) *Pool {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 64
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.TargetURL == "" {
		cfg.TargetURL = "https://opencode.ai/zen/v1/models"
	}
	return &Pool{cfg: cfg}
}

// TestAll runs every proxy in Targets concurrently (bounded by
// Concurrency) and returns the results in the same order as the
// input. The context can be used to cancel the entire batch.
func (p *Pool) TestAll(ctx context.Context) []Result {
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	defer cancel()

	results := make([]Result, len(p.cfg.Targets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, p.cfg.Concurrency)

	for i, target := range p.cfg.Targets {
		idx := i
		raw := target
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r := testProxy(ctx, raw, p.cfg)
			results[idx] = r
		}()
	}

	wg.Wait()
	return results
}

// --- single-proxy test ------------------------------------------------------

func testProxy(ctx context.Context, rawURL string, cfg Config) Result {
	start := time.Now()
	result := Result{ProxyURL: rawURL}

	// 1. Parse and validate URL.
	u, err := url.Parse(rawURL)
	if err != nil {
		return fail(result, start, fmt.Sprintf("invalid proxy URL: %v", err))
	}

	switch u.Scheme {
	case "http", "https":
		result.Type = TypeHTTP
	case "socks5", "socks5h":
		result.Type = TypeSOCKS5
	default:
		return fail(result, start, fmt.Sprintf("unsupported scheme %q", u.Scheme))
	}

	result.Host = u.Host
	if u.User != nil {
		result.Username = u.User.Username()
	}

	// 2. DNS resolution of the proxy host.
	proxyHost, proxyPort, err := net.SplitHostPort(u.Host)
	if err != nil {
		proxyHost = u.Host
		switch result.Type {
		case TypeHTTP:
			proxyPort = "8080"
		case TypeSOCKS5:
			proxyPort = "1080"
		}
	}

	dnsStart := time.Now()
	addrs, err := net.DefaultResolver.LookupHost(ctx, proxyHost)
	result.DNSMs = msSince(dnsStart)
	if err != nil || len(addrs) == 0 {
		return fail(result, start, fmt.Sprintf("dns resolution failed: %v", err))
	}

	// 3. TCP connect.
	destAddr := net.JoinHostPort(addrs[0], proxyPort)
	dialer := &net.Dialer{Timeout: cfg.Timeout}
	connStart := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", destAddr)
	result.ConnectMs = msSince(connStart)
	if err != nil {
		return fail(result, start, fmt.Sprintf("tcp connect failed: %v", err))
	}

	// 4. Proxy handshake (HTTP CONNECT or SOCKS5).
	targetAddr := targetAddr(cfg.TargetURL)
	hsStart := time.Now()

	var tunnelConn net.Conn
	switch result.Type {
	case TypeHTTP:
		tunnelConn, err = httpConnectHandshake(ctx, conn, targetAddr, u.User, cfg.Timeout)
	case TypeSOCKS5:
		socksUser, socksPass := "", ""
		if u.User != nil {
			socksUser = u.User.Username()
			socksPass, _ = u.User.Password()
		}
		tunnelConn, err = socks5Handshake(conn, targetAddr, socksUser, socksPass, cfg.Timeout)
	}
	result.HandshakeMs = msSince(hsStart)

	if err != nil {
		conn.Close()
		return fail(result, start, fmt.Sprintf("proxy handshake failed: %v", err))
	}
	if tunnelConn != nil {
		conn = tunnelConn
	}

	// 5. TLS handshake through the tunnel.
	tlsCfg := cloneTLSConfig(cfg.TLSConfig)
	tlsCfg.ServerName = hostname(cfg.TargetURL)

	tlsStart := time.Now()
	tlsConn, tlsErr := tlsHandshake(conn, tlsCfg, cfg.Timeout)
	result.TLSMs = msSince(tlsStart)

	if tlsErr != nil {
		conn.Close()
		return fail(result, start, fmt.Sprintf("tls handshake failed: %v", tlsErr))
	}

	// 6. First-byte time — send a GET through the tunnel and read
	//    the response headers.
	fbStart := time.Now()
	firstByteErr := measureFirstByte(tlsConn, cfg.TargetURL, cfg.Timeout)
	if firstByteErr != nil {
		result.FirstByteMs = -1
	} else {
		result.FirstByteMs = msSince(fbStart)
	}

	tlsConn.Close()
	result.Latency = time.Since(start)
	result.LatencyMs = result.Latency.Milliseconds()

	// 7. Categorise.
	categorise(&result, cfg.Timeout)
	return result
}

// --- HTTP CONNECT handshake -------------------------------------------------

func httpConnectHandshake(ctx context.Context, conn net.Conn, targetHost string, user *url.Userinfo, timeout time.Duration) (net.Conn, error) {
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: targetHost},
		Host:   targetHost,
		Header: make(http.Header),
	}
	if user != nil {
		pw, _ := user.Password()
		auth := base64.StdEncoding.EncodeToString([]byte(user.Username() + ":" + pw))
		req.Header.Set("Proxy-Authorization", "Basic "+auth)
	}

	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := req.Write(conn); err != nil {
		return nil, fmt.Errorf("write CONNECT: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CONNECT rejected: %s", resp.Status)
	}

	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// --- SOCKS5 handshake -------------------------------------------------------
// Mirror of internal/transport.socks5Connect but with explicit timeout
// support and re-exported for the proxytest package.

func socks5Handshake(conn net.Conn, address, username, password string, timeout time.Duration) (net.Conn, error) {
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	buf := make([]byte, 260)

	// Methods negotiation.
	methods := []byte{0} // no auth
	if username != "" || password != "" {
		if len(username) > 255 || len(password) > 255 {
			return nil, errors.New("SOCKS5 credentials are too long")
		}
		methods = append(methods, 2) // username/password
	}
	greeting := append([]byte{5, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		return nil, fmt.Errorf("write greeting: %w", err)
	}
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return nil, fmt.Errorf("read greeting response: %w", err)
	}
	if buf[0] != 5 || (buf[1] != 0 && buf[1] != 2) {
		return nil, errors.New("SOCKS5 proxy rejected authentication method")
	}

	// Auth sub-negotiation if required.
	if buf[1] == 2 {
		auth := []byte{1, byte(len(username))}
		auth = append(auth, username...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, password...)
		if _, err := conn.Write(auth); err != nil {
			return nil, fmt.Errorf("write auth: %w", err)
		}
		if _, err := io.ReadFull(conn, buf[:2]); err != nil {
			return nil, fmt.Errorf("read auth response: %w", err)
		}
		if buf[1] != 0 {
			return nil, errors.New("SOCKS5 proxy authentication failed")
		}
	}

	// Connect request.
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid target address %q: %w", address, err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid target port %q", portText)
	}

	request := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 1) // IPv4
			request = append(request, ip4...)
		} else {
			request = append(request, 4) // IPv6
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, errors.New("destination hostname is too long")
		}
		request = append(request, 3, byte(len(host))) // domain
		request = append(request, host...)
	}
	request = append(request, byte(port>>8), byte(port))

	if _, err = conn.Write(request); err != nil {
		return nil, fmt.Errorf("write connect request: %w", err)
	}
	if _, err = io.ReadFull(conn, buf[:4]); err != nil {
		return nil, fmt.Errorf("read connect response: %w", err)
	}
	if buf[0] != 5 || buf[1] != 0 {
		return nil, fmt.Errorf("SOCKS5 connect failed with code %d", buf[1])
	}

	// Drain the remainder of the response (bind address).
	var addressLen int
	switch buf[3] {
	case 1:
		addressLen = 4 // IPv4
	case 4:
		addressLen = 16 // IPv6
	case 3:
		if _, err = io.ReadFull(conn, buf[:1]); err != nil {
			return nil, fmt.Errorf("read domain length: %w", err)
		}
		addressLen = int(buf[0])
	default:
		return nil, errors.New("SOCKS5 proxy returned unknown address type")
	}
	_, _ = io.CopyN(io.Discard, conn, int64(addressLen+2)) // address + port
	return conn, nil
}

// --- TLS helpers ------------------------------------------------------------

func tlsHandshake(conn net.Conn, cfg *tls.Config, timeout time.Duration) (*tls.Conn, error) {
	tlsConn := tls.Client(conn, cfg)
	_ = tlsConn.SetDeadline(time.Now().Add(timeout))
	if err := tlsConn.Handshake(); err != nil {
		tlsConn.Close()
		return nil, err
	}
	_ = tlsConn.SetDeadline(time.Time{})
	return tlsConn, nil
}

// --- first-byte measurement -------------------------------------------------

func measureFirstByte(tlsConn *tls.Conn, targetURL string, timeout time.Duration) error {
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return err
	}
	req.Close = true

	_ = tlsConn.SetDeadline(time.Now().Add(timeout))
	defer func() { _ = tlsConn.SetDeadline(time.Time{}) }()

	if err := req.Write(tlsConn); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return nil
}

// --- categorisation ---------------------------------------------------------

func categorise(r *Result, timeout time.Duration) {
	switch {
	case r.Error != "":
		r.Category = Dead
	case r.Latency >= timeout:
		r.Category = Dead
		if r.Error == "" {
			r.Error = "exceeded per-hop timeout"
		}
	case r.Latency >= timeout/5: // >= 2 s for default 10 s timeout
		r.Category = Slow
	default:
		r.Category = Healthy
	}
}

// --- misc helpers -----------------------------------------------------------

func fail(r Result, start time.Time, msg string) Result {
	r.Latency = time.Since(start)
	r.LatencyMs = r.Latency.Milliseconds()
	r.Error = msg
	r.Category = Dead
	return r
}

func msSince(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}

func targetAddr(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	if _, _, err := net.SplitHostPort(u.Host); err != nil {
		// No port — add default based on scheme.
		switch u.Scheme {
		case "https":
			return net.JoinHostPort(u.Host, "443")
		case "http":
			return net.JoinHostPort(u.Host, "80")
		default:
			return u.Host // caller will see SplitHostPort error
		}
	}
	return u.Host
}

// hostname strips the port from a URL, returning just the hostname.
func hostname(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	h, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		return u.Host
	}
	return h
}

func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{
			InsecureSkipVerify: true,
		}
	}
	return cfg.Clone()
}
