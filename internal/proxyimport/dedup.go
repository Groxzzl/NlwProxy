package proxyimport

// Deduplicate removes duplicate proxies across the entire result set.
// Two proxies are considered equal if they share the same IP:port AND proxy type.
func Deduplicate(proxies []Proxy) []Proxy {
	seen := make(map[string]bool, len(proxies))
	out := make([]Proxy, 0, len(proxies))
	for _, p := range proxies {
		key := p.Addr() + "@" + p.Type.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

// Categorize splits a flat list of proxies into a Result grouped by type.
func Categorize(proxies []Proxy) *Result {
	r := &Result{}
	for _, p := range proxies {
		switch p.Type {
		case HTTP:
			r.HTTP = append(r.HTTP, p)
		case SOCKS4:
			r.SOCKS4 = append(r.SOCKS4, p)
		case SOCKS5:
			r.SOCKS5 = append(r.SOCKS5, p)
		}
	}
	return r
}
