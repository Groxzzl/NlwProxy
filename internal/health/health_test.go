package health

import (
	"testing"
	"time"
)

func TestScoreIsWeightedAndBounded(t *testing.T) {
	got := Score(Sample{Available: true, Latency: 0, ErrorRate: 0, Stability: 1})
	if got != 1 {
		t.Fatalf("%f", got)
	}
	got = Score(Sample{Available: false, Latency: 10 * time.Second, ErrorRate: 2, Stability: -1})
	if got != 0 {
		t.Fatalf("%f", got)
	}
}
