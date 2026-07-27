package security

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

const Redacted = "[REDACTED]"

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*:\s*)([^\r\n]+)`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+\-/=]+`),
	regexp.MustCompile(`(?i)(cookie\s*[:=]\s*)([^\r\n\s]+)`),
	regexp.MustCompile(`(?i)(api[_-]?key\s*[:=]\s*)([^\r\n\s]+)`),
}

func RedactString(value string) string {
	out := value
	for _, pattern := range secretPatterns {
		out = pattern.ReplaceAllString(out, `${1}`+Redacted)
	}
	// Remove credentials from URLs while retaining enough endpoint context for diagnostics.
	for _, field := range strings.Fields(out) {
		candidate := strings.TrimPrefix(field, "proxy=")
		if u, err := url.Parse(candidate); err == nil && u.User != nil {
			u.User = url.User(Redacted)
			out = strings.ReplaceAll(out, candidate, u.String())
		}
	}
	return out
}

func RedactJSON(data []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	redactValue(value)
	return json.Marshal(value)
}

func redactValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if sensitiveKey(key) {
				typed[key] = Redacted
				continue
			}
			if text, ok := item.(string); ok {
				typed[key] = RedactString(text)
			} else {
				redactValue(item)
			}
		}
	case []any:
		for _, item := range typed {
			redactValue(item)
		}
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	switch normalized {
	case "authorization", "apikey", "token", "cookie", "password", "secret", "prompt", "content", "messages", "response", "proxyurl":
		return true
	default:
		return false
	}
}
