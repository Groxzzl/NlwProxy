package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nlwproxy/internal/proxyimport"
	"nlwproxy/internal/proxytest"
	"nlwproxy/internal/transport"
)

type candidate struct {
	URL    string
	Source string
}

type checked struct {
	Candidate candidate
	Latency   time.Duration
	Status    int
	Err       string
}

func main() {
	out := flag.String("out", "", "verified proxy output file")
	private := flag.String("private", "", "private proxy file to merge")
	limit := flag.Int("limit", 160, "maximum proxies to write")
	concurrency := flag.Int("concurrency", 96, "provider probe concurrency")
	scrape := flag.Bool("scrape", true, "include built-in public proxy sources")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(2)
	}
	key := os.Getenv("REFFAUNLIMITED_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "REFFAUNLIMITED_API_KEY is empty")
		os.Exit(2)
	}

	scrapeCtx, scrapeCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer scrapeCancel()

	seen := map[string]bool{}
	var candidates []candidate
	add := func(raw, source string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "#") {
			return
		}
		if !strings.Contains(raw, "://") {
			parts := strings.Split(raw, ":")
			if len(parts) == 4 {
				raw = fmt.Sprintf("http://%s:%s@%s:%s", url.QueryEscape(parts[2]), url.QueryEscape(parts[3]), parts[0], parts[1])
			} else {
				raw = "http://" + raw
			}
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
			return
		}
		id := strings.ToLower(u.Host)
		if seen[id] {
			return
		}
		seen[id] = true
		candidates = append(candidates, candidate{URL: raw, Source: source})
	}

	if *private != "" {
		if b, err := os.ReadFile(*private); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				add(line, "private")
			}
		}
	}

	if *scrape {
		scraped := proxyimport.ScrapeAll(scrapeCtx)
		for _, result := range scraped {
			for _, p := range result.Proxies {
				if p.Type == proxyimport.SOCKS4 {
					continue
				}
				add(p.URL(), result.Source)
			}
		}
	}
	fmt.Printf("candidates=%d\n", len(candidates))

	targets := make([]string, len(candidates))
	for i := range candidates {
		targets[i] = candidates[i].URL
	}
	tlsCtx, tlsCancel := context.WithTimeout(context.Background(), 6*time.Minute)
	tlsConcurrency := *concurrency
	if tlsConcurrency > 128 {
		tlsConcurrency = 128
	}
	layer1 := proxytest.New(proxytest.Config{Targets: targets, Concurrency: tlsConcurrency, Timeout: 5 * time.Second, TargetURL: "https://www.cloudflare.com/cdn-cgi/trace", TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}).TestAll(tlsCtx)
	tlsCancel()

	var passed1 []candidate
	for i, r := range layer1 {
		if r.IsAlive() {
			passed1 = append(passed1, candidates[i])
		}
	}
	fmt.Printf("tls_pass=%d\n", len(passed1))
	providerCtx, providerCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer providerCancel()

	jobs := make(chan candidate)
	results := make(chan checked, len(passed1))
	workers := *concurrency
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	var done atomic.Int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				r := probeProvider(providerCtx, c, key)
				results <- r
				n := done.Add(1)
				if n%25 == 0 || n == int64(len(passed1)) {
					fmt.Printf("provider_progress=%d/%d\n", n, len(passed1))
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, c := range passed1 {
			select {
			case jobs <- c:
			case <-providerCtx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	var good []checked
	errorCounts := map[string]int{}
	for r := range results {
		if r.Err == "" && r.Status >= 200 && r.Status < 400 {
			good = append(good, r)
			continue
		}
		key := r.Err
		if key == "" {
			key = fmt.Sprintf("status %d", r.Status)
		}
		if len(key) > 120 {
			key = key[:120]
		}
		errorCounts[key]++
	}
	for key, count := range errorCounts {
		fmt.Printf("provider_error count=%d reason=%s\n", count, key)
	}
	sort.SliceStable(good, func(i, j int) bool {
		if good[i].Candidate.Source == "private" && good[j].Candidate.Source != "private" {
			return true
		}
		if good[j].Candidate.Source == "private" && good[i].Candidate.Source != "private" {
			return false
		}
		return good[i].Latency < good[j].Latency
	})
	if len(good) > *limit {
		good = good[:*limit]
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		panic(err)
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	for _, r := range good {
		fmt.Fprintln(f, r.Candidate.URL)
	}
	if err := f.Close(); err != nil {
		panic(err)
	}
	summary := map[string]any{"candidates": len(candidates), "tls_pass": len(passed1), "provider_pass": len(good), "output": *out}
	b, _ := json.Marshal(summary)
	fmt.Println(string(b))
	if len(good) == 0 {
		os.Exit(1)
	}
}

func probeProvider(ctx context.Context, c candidate, key string) checked {
	start := time.Now()
	u, err := url.Parse(c.URL)
	if err != nil {
		return checked{Candidate: c, Err: err.Error()}
	}
	mode := transport.HTTPProxy
	if u.Scheme == "socks5" || u.Scheme == "socks5h" {
		mode = transport.SOCKS5
	}
	user, pass := "", ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	rt, err := transport.New(transport.Config{Mode: mode, ProxyURL: c.URL, Timeout: 8 * time.Second, ProxyUsername: user, ProxyPassword: pass, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}})
	if err != nil {
		return checked{Candidate: c, Err: err.Error()}
	}
	client := &http.Client{Transport: rt, Timeout: 10 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://opencode.ai/zen/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "opencode/1.17.17")
	resp, err := client.Do(req)
	if err != nil {
		return checked{Candidate: c, Latency: time.Since(start), Err: err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return checked{Candidate: c, Latency: time.Since(start), Status: resp.StatusCode, Err: fmt.Sprintf("upstream status %d", resp.StatusCode)}
	}
	return checked{Candidate: c, Latency: time.Since(start), Status: resp.StatusCode}
}

// Keep sync imported for the worker pool on all supported Go versions.
var _ sync.Locker
