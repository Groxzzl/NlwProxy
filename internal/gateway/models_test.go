package gateway

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCachedModelServiceFetchesTransparentCatalogAndCaches(t *testing.T) {
	calls := 0
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"z/model","name":"Zed"},{"id":"opencode-route"},{"id":"a/model"}]}`)), Request: r}, nil
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := &CachedModelService{Transport: rt, TTL: time.Hour, Now: func() time.Time { return now }}
	first, err := svc.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(first) != 2 || len(second) != 2 || first[0].ID != "a/model" || first[1].Name != "Zed" {
		t.Fatalf("calls=%d first=%+v", calls, first)
	}
}
