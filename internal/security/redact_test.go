package security

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactStringSecrets(t *testing.T) {
	input := "Authorization: Bearer sk-supersecret proxy=https://user:pass@example.com key=sk-abcdefghijklmnop cookie=session=abcdef"
	got := RedactString(input)
	for _, secret := range []string{"sk-supersecret", "user:pass", "sk-abcdefghijklmnop", "session=abcdef"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q leaked: %s", secret, got)
		}
	}
	if !strings.Contains(got, Redacted) {
		t.Fatalf("not redacted: %s", got)
	}
}

func TestRedactJSONRecursivelyDropsContentAndSecrets(t *testing.T) {
	input := []byte(`{"authorization":"Bearer topsecret","nested":{"apiKey":"abc123","proxy_url":"http://u:p@host:1","prompt":"private words","safe":"ok"},"messages":[{"content":"hello"}]}`)
	got, err := RedactJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"topsecret", "abc123", "u:p", "private words", "hello"} {
		if strings.Contains(string(got), secret) {
			t.Fatalf("%q leaked in %s", secret, got)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["authorization"] != Redacted {
		t.Fatalf("decoded=%v", decoded)
	}
}
