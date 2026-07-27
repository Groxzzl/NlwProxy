package opencode

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupUsesTransparentCatalogWithoutSyntheticRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(Options{Path: path, BaseURL: "http://gateway/v1", APIKey: "local-key", Models: []string{"vendor/model-v2"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	for _, want := range []string{"http://gateway/v1", "local-key", "vendor/model-v2"} {
		if !bytes.Contains(got, []byte(want)) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	if bytes.Contains(got, []byte("opencode-route")) {
		t.Fatalf("synthetic route leaked: %s", got)
	}
}
