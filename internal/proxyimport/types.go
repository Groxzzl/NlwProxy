// Package proxyimport scrapes public GitHub proxy lists, deduplicates, and
// categorizes proxies by type (HTTP, SOCKS4, SOCKS5).
package proxyimport

import (
	"fmt"
	"strconv"
	"strings"
)

// ProxyType classifies a proxy protocol.
type ProxyType int

const (
	HTTP   ProxyType = iota // HTTP/HTTPS proxy
	SOCKS4                  // SOCKS4 proxy
	SOCKS5                  // SOCKS5 proxy
)

func (t ProxyType) String() string {
	switch t {
	case HTTP:
		return "http"
	case SOCKS4:
		return "socks4"
	case SOCKS5:
		return "socks5"
	default:
		return "unknown"
	}
}

// Proxy represents a single scraped proxy entry.
type Proxy struct {
	IP   string
	Port int
	Type ProxyType
}

// Addr returns the "ip:port" string.
func (p Proxy) Addr() string {
	return fmt.Sprintf("%s:%d", p.IP, p.Port)
}

// URL returns the "type://ip:port" string.
func (p Proxy) URL() string {
	return fmt.Sprintf("%s://%s:%d", p.Type, p.IP, p.Port)
}

// SourceResult holds the outcome of scraping one source.
type SourceResult struct {
	Source  string
	Proxies []Proxy
	Err     error
}

// Result holds deduplicated proxies grouped by type.
type Result struct {
	HTTP   []Proxy
	SOCKS4 []Proxy
	SOCKS5 []Proxy
}

// Total returns the total number of proxies across all types.
func (r *Result) Total() int {
	return len(r.HTTP) + len(r.SOCKS4) + len(r.SOCKS5)
}

// ExportText returns one "type://ip:port" line per proxy, grouped by type.
func (r *Result) ExportText() string {
	var b strings.Builder
	for _, p := range r.HTTP {
		b.WriteString(p.URL())
		b.WriteByte('\n')
	}
	for _, p := range r.SOCKS4 {
		b.WriteString(p.URL())
		b.WriteByte('\n')
	}
	for _, p := range r.SOCKS5 {
		b.WriteString(p.URL())
		b.WriteByte('\n')
	}
	return b.String()
}

// ExportLines returns all proxies as a flat []Proxy.
func (r *Result) ExportLines() []Proxy {
	out := make([]Proxy, 0, r.Total())
	out = append(out, r.HTTP...)
	out = append(out, r.SOCKS4...)
	out = append(out, r.SOCKS5...)
	return out
}

// parseIPPort parses "ip:port" into (ip, port, ok).
func parseIPPort(s string) (string, int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, false
	}
	host, portStr, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return host, port, true
}
