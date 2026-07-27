package retry

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

type Decision struct {
	Retry       bool
	SwitchRoute bool
	Reason      string
}

func Classify(status int, body []byte, err error, responseStarted bool) Decision {
	if responseStarted {
		return Decision{Reason: "response_started"}
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Decision{Retry: true, SwitchRoute: true, Reason: "eof_before_response"}
		}
		var ne net.Error
		if errors.As(err, &ne) || strings.Contains(strings.ToLower(err.Error()), "connection") || strings.Contains(strings.ToLower(err.Error()), "tls") || strings.Contains(strings.ToLower(err.Error()), "proxy") {
			return Decision{Retry: true, SwitchRoute: true, Reason: "network"}
		}
		return Decision{Reason: "unknown_error"}
	}
	lower := strings.ToLower(string(body))
	if status == 401 || status == 402 || status == 403 {
		return Decision{Reason: "authentication"}
	}
	if status == 429 {
		return Decision{Reason: "rate_limit"}
	}
	if strings.Contains(lower, "insufficient_quota") || strings.Contains(lower, "quota_exceeded") || strings.Contains(lower, "quota exhausted") {
		return Decision{Reason: "quota"}
	}
	if status == 502 || status == 503 || status == 504 {
		return Decision{Retry: true, SwitchRoute: true, Reason: "transient_upstream"}
	}
	return Decision{Reason: "non_retryable"}
}

func RetryAfter(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	return resp.Header.Get("Retry-After")
}
