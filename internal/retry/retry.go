package retry

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
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
	if status == 429 || strings.Contains(lower, "freeusagelimit") || strings.Contains(lower, "rate limit exceeded") || strings.Contains(lower, "rate_limit_exceeded") {
		return Decision{Retry: true, SwitchRoute: true, Reason: "rate_limit"}
	}
	if strings.Contains(lower, "insufficient_quota") || strings.Contains(lower, "quota_exceeded") || strings.Contains(lower, "quota exhausted") {
		return Decision{Retry: true, SwitchRoute: true, Reason: "quota"}
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

// ParseRetryAfter derives a cooldown duration from an upstream 429 response.
// Precedence: the OpenCode error body's `retry-after-ms=NNN` marker, then the
// standard `Retry-After` header (delta-seconds or HTTP-date), relative to now.
// Returns 0 when nothing parseable is present.
func ParseRetryAfter(header string, body []byte, now time.Time) time.Duration {
	if d := parseRetryAfterMS(body); d > 0 {
		return d
	}
	return parseRetryAfterHeader(header, now)
}

// parseRetryAfterMS scans for `retry-after-ms=NNN` (case-insensitive) anywhere
// in the body, tolerating quotes/whitespace around the value.
func parseRetryAfterMS(body []byte) time.Duration {
	lower := strings.ToLower(string(body))
	const key = "retry-after-ms"
	idx := strings.Index(lower, key)
	if idx < 0 {
		return 0
	}
	rest := lower[idx+len(key):]
	rest = strings.TrimLeft(rest, " 	")
	if !strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, ":") {
		return 0
	}
	rest = strings.TrimLeft(rest[1:], " 	\"'")
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	ms, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// parseRetryAfterHeader handles the RFC 7231 Retry-After header, which is
// either delta-seconds or an HTTP-date.
func parseRetryAfterHeader(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
