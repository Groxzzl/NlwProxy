// Package proxyimport scrapes public GitHub proxy lists, deduplicates, and
// categorizes proxies by type (HTTP, SOCKS4, SOCKS5).
//
// Usage:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//
//	results := proxyimport.ScrapeAll(ctx)
//	list := proxyimport.Categorize(proxyimport.Deduplicate(proxyimport.FlattenResults(results)))
//	fmt.Print(list.ExportText())
package proxyimport

import "context"

// ScrapeAll scrapes all built-in sources concurrently and returns raw results.
// Each source URL has a 10-second HTTP timeout. The overall context bounds the
// entire operation — unresponsive URLs are skipped.
func ScrapeAll(ctx context.Context) []SourceResult {
	return ScrapeSources(ctx, defaultSources())
}

// FlattenResults merges multiple SourceResult slices into a single flat list.
func FlattenResults(results []SourceResult) []Proxy {
	var out []Proxy
	for _, r := range results {
		out = append(out, r.Proxies...)
	}
	return out
}
