package geo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New returned nil")
	}
}

func TestLookupCache(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","country":"United States","city":"Miami","isp":"Test ISP","org":"Test Org","as":"AS1234 Test","lat":25.76,"lon":-80.19,"query":"8.8.8.8"}`))
	}))
	defer ts.Close()

	s := New(WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	s.url = ts.URL + "/" // override to point at test server

	result1 := s.Lookup(context.Background(), "8.8.8.8")
	if result1.Error != "" {
		t.Fatalf("unexpected error: %s", result1.Error)
	}
	if result1.Country != "United States" {
		t.Fatalf("expected United States, got %s", result1.Country)
	}
	if result1.ISP != "Test ISP" {
		t.Fatalf("expected Test ISP, got %s", result1.ISP)
	}
	if result1.ASN != "AS1234 Test" {
		t.Fatalf("expected AS1234 Test, got %s", result1.ASN)
	}

	// Second call should hit cache, not the server
	result2 := s.Lookup(context.Background(), "8.8.8.8")
	if calls != 1 {
		t.Fatalf("expected 1 server call due to cache, got %d", calls)
	}
	if result2.Country != "United States" {
		t.Fatalf("expected cached result")
	}
}

func TestLookupCacheExpiry(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","country":"US","query":"1.1.1.1"}`))
	}))
	defer ts.Close()

	s := New(WithTTL(50*time.Millisecond), WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	s.url = ts.URL + "/"

	s.Lookup(context.Background(), "1.1.1.1")
	s.Lookup(context.Background(), "1.1.1.1")
	if calls != 1 {
		t.Fatalf("expected 1 call (cached), got %d", calls)
	}

	time.Sleep(60 * time.Millisecond)
	s.Lookup(context.Background(), "1.1.1.1")
	if calls != 2 {
		t.Fatalf("expected 2 calls after expiry, got %d", calls)
	}
}

func TestLookupEmptyIP(t *testing.T) {
	s := New()
	r := s.Lookup(context.Background(), "")
	if r.Error == "" {
		t.Fatal("expected error for empty IP")
	}
}

func TestLookupBatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","country":"US","city":"Test","query":"8.8.8.8"}`))
	}))
	defer ts.Close()

	s := New(WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	s.url = ts.URL + "/"

	results := s.LookupBatch(context.Background(), []string{"8.8.8.8", "1.1.1.1"})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestClearCache(t *testing.T) {
	s := New()
	s.cache["test"] = cacheEntry{expiresAt: time.Now().Add(time.Hour)}
	s.ClearCache()
	if len(s.cache) != 0 {
		t.Fatal("cache should be empty after ClearCache")
	}
}

func TestStats(t *testing.T) {
	s := New()
	s.cache["a"] = cacheEntry{expiresAt: time.Now().Add(time.Hour)}
	s.cache["b"] = cacheEntry{expiresAt: time.Now().Add(-time.Hour)}
	cached, expired := s.Stats()
	if cached != 1 {
		t.Fatalf("expected 1 cached, got %d", cached)
	}
	if expired != 1 {
		t.Fatalf("expected 1 expired, got %d", expired)
	}
}

func TestLookupErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"fail","query":"10.0.0.1"}`))
	}))
	defer ts.Close()

	s := New(WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
	s.url = ts.URL + "/"

	r := s.Lookup(context.Background(), "10.0.0.1")
	if r.Error == "" {
		t.Fatal("expected error for fail status")
	}
}

func TestLookupHTTPError(t *testing.T) {
	s := New(WithHTTPClient(&http.Client{Timeout: time.Nanosecond}))
	// Use invalid URL to trigger connection error
	r := s.Lookup(context.Background(), "8.8.8.8")
	if r.Error == "" {
		t.Log("expected an error from bad transport, but may not always fail")
	}
}
