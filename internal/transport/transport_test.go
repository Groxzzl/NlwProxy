package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestDirectRoundTripper(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	client, err := New(Config{Mode: Direct, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.RoundTrip(mustRequest(t, upstream.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHTTPProxyRoundTripper(t *testing.T) {
	seen := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.String()
		w.WriteHeader(http.StatusCreated)
	}))
	defer proxy.Close()

	client, err := New(Config{Mode: HTTPProxy, ProxyURL: proxy.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.RoundTrip(mustRequest(t, "http://upstream.invalid/v1/models"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := <-seen; got != "http://upstream.invalid/v1/models" {
		t.Fatalf("proxy saw %q", got)
	}
}

func TestSOCKS5DialerHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	seen := make(chan string, 1)
	go serveSOCKS5(t, ln, seen)

	rt, err := New(Config{Mode: SOCKS5, ProxyURL: "socks5://" + ln.Addr().String(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("http://example.com:8080")
	conn, err := rt.(*http.Transport).DialContext(context.Background(), "tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if got := <-seen; got != "example.com:8080" {
		t.Fatalf("destination=%q", got)
	}
}

func TestRejectsUnknownModeAndBadProxyURL(t *testing.T) {
	if _, err := New(Config{Mode: "vpn"}); err == nil {
		t.Fatal("expected unknown mode error")
	}
	if _, err := New(Config{Mode: HTTPProxy, ProxyURL: "://bad"}); err == nil {
		t.Fatal("expected invalid proxy URL error")
	}
	if _, err := New(Config{Mode: HTTPProxy, ProxyURL: "http://proxy.example\r\nInjected: yes"}); err == nil {
		t.Fatal("expected CRLF rejection")
	}
}

func mustRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func serveSOCKS5(t *testing.T, ln net.Listener, seen chan<- string) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	buf := make([]byte, 260)
	if _, err = io.ReadFull(conn, buf[:3]); err != nil {
		return
	}
	conn.Write([]byte{5, 0})
	if _, err = io.ReadFull(conn, buf[:4]); err != nil {
		return
	}
	var host string
	switch buf[3] {
	case 3:
		if _, err = io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		n := int(buf[0])
		if _, err = io.ReadFull(conn, buf[:n+2]); err != nil {
			return
		}
		host = string(buf[:n]) + ":" + itoaPort(buf[n], buf[n+1])
	default:
		return
	}
	seen <- host
	conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
}

func itoaPort(hi, lo byte) string {
	return fmt.Sprint(int(hi)<<8 | int(lo))
}
