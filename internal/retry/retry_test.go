package retry

import (
	"errors"
	"testing"
)

func TestClassificationNeverRetriesAuth(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{401, ""}, {402, ""}, {403, ""}} {
		if d := Classify(tc.status, []byte(tc.body), nil, false); d.Retry || d.SwitchRoute {
			t.Fatalf("status %d body %s: %+v", tc.status, tc.body, d)
		}
	}
}
func TestClassificationRetriesRateLimitAndQuota(t *testing.T) {
	if d := Classify(429, nil, nil, false); !d.Retry || !d.SwitchRoute {
		t.Fatalf("429 should switch route: %+v", d)
	}
	if d := Classify(200, []byte(`{"error":"FreeUsageLimitError","message":"rate limit exceeded"}`), nil, false); !d.Retry || !d.SwitchRoute {
		t.Fatalf("free usage envelope should switch route: %+v", d)
	}
	if d := Classify(400, []byte(`{"code":"insufficient_quota"}`), nil, false); !d.Retry || !d.SwitchRoute {
		t.Fatalf("insufficient_quota should switch route: %+v", d)
	}
}
func TestClassificationRetriesOnlySafeFailures(t *testing.T) {
	for _, status := range []int{502, 503, 504} {
		if d := Classify(status, nil, nil, false); !d.Retry || !d.SwitchRoute {
			t.Fatalf("%d: %+v", status, d)
		}
	}
	if d := Classify(0, nil, errors.New("connection reset"), false); !d.Retry {
		t.Fatalf("%+v", d)
	}
	if d := Classify(503, nil, nil, true); d.Retry {
		t.Fatalf("stream replay: %+v", d)
	}
}
