package retry

import (
	"errors"
	"testing"
)

func TestClassificationNeverRetriesPolicyErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{401, ""}, {402, ""}, {403, ""}, {429, ""}, {400, `{"code":"insufficient_quota"}`}} {
		if d := Classify(tc.status, []byte(tc.body), nil, false); d.Retry || d.SwitchRoute {
			t.Fatalf("status %d body %s: %+v", tc.status, tc.body, d)
		}
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
