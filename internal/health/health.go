// Package health probes routes and calculates bounded health scores.
package health

import (
	"context"
	"net/http"
	"time"
)

type Sample struct {
	Available bool
	Latency   time.Duration
	ErrorRate float64
	Stability float64
}

func Score(s Sample) float64 {
	availability := 0.0
	if s.Available {
		availability = 1
	}
	latency := 1 - float64(s.Latency)/float64(5*time.Second)
	if latency < 0 {
		latency = 0
	}
	if latency > 1 {
		latency = 1
	}
	errorScore := 1 - s.ErrorRate
	if errorScore < 0 {
		errorScore = 0
	}
	if errorScore > 1 {
		errorScore = 1
	}
	stability := s.Stability
	if stability < 0 {
		stability = 0
	}
	if stability > 1 {
		stability = 1
	}
	score := availability*0.45 + latency*0.25 + errorScore*0.20 + stability*0.10
	if score < 0 {
		return 0
	}
	if score > 1 || 1-score < 1e-12 {
		return 1
	}
	return score
}

type Result struct {
	Healthy bool
	Status  int
	Latency time.Duration
	Err     error
}

func Probe(ctx context.Context, rt http.RoundTripper, url string) Result {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return Result{Err: err}
	}
	resp, err := rt.RoundTrip(req)
	result := Result{Latency: time.Since(start), Err: err}
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	result.Status = resp.StatusCode
	result.Healthy = resp.StatusCode >= 200 && resp.StatusCode < 500
	return result
}
