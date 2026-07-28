package proxyimport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// source describes a single proxy list source to scrape.
type source struct {
	Name string
	URLs []sourceURL // URLs to fetch; each carries its implied proxy type
}

type sourceURL struct {
	URL  string
	Type ProxyType
}

// defaultSources returns the built-in list of public proxy sources.
func defaultSources() []source {
	return []source{
		{
			Name: "TheSpeedX/PROXY-List",
			URLs: []sourceURL{
				{URL: "https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/http.txt", Type: HTTP},
				{URL: "https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/socks4.txt", Type: SOCKS4},
				{URL: "https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/socks5.txt", Type: SOCKS5},
			},
		},
		{
			Name: "monosans/proxy-list",
			URLs: []sourceURL{
				{URL: "https://raw.githubusercontent.com/monosans/proxy-list/main/proxies.json", Type: HTTP}, // type from JSON protocol field
			},
		},
		{
			Name: "proxifly/free-proxy-list",
			URLs: []sourceURL{
				{URL: "https://raw.githubusercontent.com/proxifly/free-proxy-list/main/proxies/all/data.txt", Type: HTTP}, // type from URL prefix
			},
		},
		{
			Name: "jetkai/proxy-list",
			URLs: []sourceURL{
				{URL: "https://raw.githubusercontent.com/jetkai/proxy-list/main/online-proxies/json/proxies.json", Type: HTTP}, // type from JSON keys
			},
		},
		{
			Name: "roosterkid/openproxylist",
			URLs: []sourceURL{
				{URL: "https://raw.githubusercontent.com/roosterkid/openproxylist/main/HTTPS_RAW.txt", Type: HTTP},
				{URL: "https://raw.githubusercontent.com/roosterkid/openproxylist/main/SOCKS4_RAW.txt", Type: SOCKS4},
				{URL: "https://raw.githubusercontent.com/roosterkid/openproxylist/main/SOCKS5_RAW.txt", Type: SOCKS5},
			},
		},
	}
}

// ScrapeSources scrapes the given sources concurrently.
// Each source URL is fetched with a 10-second timeout.
// The overall operation is bounded by ctx; unresponsive URLs are skipped.
func ScrapeSources(ctx context.Context, sources []source) []SourceResult {
	results := make([]SourceResult, 0, len(sources))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, src := range sources {
		wg.Add(1)
		go func(src source) {
			defer wg.Done()
			sr := scrapeSource(ctx, src)
			mu.Lock()
			results = append(results, sr)
			mu.Unlock()
		}(src)
	}

	wg.Wait()
	return results
}

// scrapeSource fetches and parses all URLs for a single source.
func scrapeSource(ctx context.Context, src source) SourceResult {
	sr := SourceResult{Source: src.Name}
	seen := make(map[string]bool) // dedup within source

	for _, su := range src.URLs {
		proxies, err := fetchAndParse(ctx, su)
		if err != nil {
			// skip unresponsive URLs; keep partial results
			continue
		}
		for _, p := range proxies {
			key := p.Addr() + "@" + p.Type.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			sr.Proxies = append(sr.Proxies, p)
		}
	}
	return sr
}

// fetchAndParse fetches a single URL and parses the response into proxies.
func fetchAndParse(ctx context.Context, su sourceURL) ([]Proxy, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, su.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nlwproxy/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB max
	if err != nil {
		return nil, err
	}

	return parseBody(su.URL, string(body))
}

// parseBody dispatches to the correct parser based on URL patterns.
func parseBody(url, body string) ([]Proxy, error) {
	switch {
	case strings.Contains(url, "jetkai") && strings.HasSuffix(url, ".json"):
		return parseJetkaiJSON(body)
	case strings.Contains(url, "monosans") && strings.HasSuffix(url, ".json"):
		return parseMonosansJSON(body)
	case strings.Contains(url, "proxifly") && strings.HasSuffix(url, ".json"):
		return parseProxiflyJSON(body)
	case strings.Contains(url, "proxifly") && strings.HasSuffix(url, ".txt"):
		return parseProxiflyTXT(body)
	default:
		return parseRawText(body)
	}
}

// parseRawText parses "ip:port" per line, inferring type from URL.
func parseRawText(body string) ([]Proxy, error) {
	var proxies []Proxy
	for _, line := range strings.Split(body, "\n") {
		ip, port, ok := parseIPPort(line)
		if !ok {
			continue
		}
		// Type will be set by the caller from the sourceURL.Type
		proxies = append(proxies, Proxy{IP: ip, Port: port})
	}
	return proxies, nil
}

// parseMonosansJSON parses the monosans/proxy-list proxies.json format.
// Each entry: {"protocol": "http", "host": "1.2.3.4", "port": 8080}
func parseMonosansJSON(body string) ([]Proxy, error) {
	type entry struct {
		Protocol string `json:"protocol"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
	}
	entries, err := unmarshalJSON[[]entry](body)
	if err != nil {
		return nil, fmt.Errorf("monosans JSON: %w", err)
	}
	proxies := make([]Proxy, 0, len(entries))
	for _, e := range entries {
		t, ok := parseProtocol(e.Protocol)
		if !ok {
			continue
		}
		if e.Port < 1 || e.Port > 65535 {
			continue
		}
		proxies = append(proxies, Proxy{IP: e.Host, Port: e.Port, Type: t})
	}
	return proxies, nil
}

// parseJetkaiJSON parses jetkai/proxy-list proxies.json format.
// Object: {"http": ["ip:port", ...], "socks4": [...], "socks5": [...]}
func parseJetkaiJSON(body string) ([]Proxy, error) {
	raw, err := unmarshalJSON[map[string][]string](body)
	if err != nil {
		return nil, fmt.Errorf("jetkai JSON: %w", err)
	}
	var proxies []Proxy
	for proto, addrs := range raw {
		t, ok := parseProtocol(proto)
		if !ok {
			continue
		}
		for _, addr := range addrs {
			ip, port, ok := parseIPPort(addr)
			if !ok {
				continue
			}
			proxies = append(proxies, Proxy{IP: ip, Port: port, Type: t})
		}
	}
	return proxies, nil
}

// parseProxiflyJSON parses proxifly/free-proxy-list JSON format.
// Array of: {"proxy": "http://ip:port", "protocol": "http", ...}
func parseProxiflyJSON(body string) ([]Proxy, error) {
	type entry struct {
		Proxy    string `json:"proxy"`
		Protocol string `json:"protocol"`
	}
	entries, err := unmarshalJSON[[]entry](body)
	if err != nil {
		return nil, fmt.Errorf("proxifly JSON: %w", err)
	}
	proxies := make([]Proxy, 0, len(entries))
	for _, e := range entries {
		// Try extracting type and address from the "proxy" field: "type://ip:port"
		if e.Proxy != "" {
			if p, ok := parseURLProxy(e.Proxy); ok {
				proxies = append(proxies, p)
				continue
			}
		}
		// Fallback: use protocol field + raw address
		t, ok := parseProtocol(e.Protocol)
		if !ok {
			continue
		}
		ip, port, ok := parseIPPort(e.Proxy)
		if !ok {
			continue
		}
		proxies = append(proxies, Proxy{IP: ip, Port: port, Type: t})
	}
	return proxies, nil
}

// parseProxiflyTXT parses proxifly's data.txt format: "protocol://ip:port" per line.
func parseProxiflyTXT(body string) ([]Proxy, error) {
	var proxies []Proxy
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p, ok := parseURLProxy(line)
		if ok {
			proxies = append(proxies, p)
		}
	}
	return proxies, nil
}

// parseURLProxy parses "type://ip:port" format.
func parseURLProxy(s string) (Proxy, bool) {
	protoIdx := strings.Index(s, "://")
	if protoIdx < 0 {
		return Proxy{}, false
	}
	proto := strings.ToLower(s[:protoIdx])
	t, ok := parseProtocol(proto)
	if !ok {
		return Proxy{}, false
	}
	addr := s[protoIdx+3:]
	ip, port, ok := parseIPPort(addr)
	if !ok {
		return Proxy{}, false
	}
	return Proxy{IP: ip, Port: port, Type: t}, true
}

// Known protocol mappings.
var protocolMap = map[string]ProxyType{
	"http":   HTTP,
	"https":  HTTP,
	"socks4": SOCKS4,
	"socks5": SOCKS5,
}

func parseProtocol(s string) (ProxyType, bool) {
	t, ok := protocolMap[strings.ToLower(s)]
	return t, ok
}
