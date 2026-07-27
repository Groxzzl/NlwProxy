package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCheckStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	var out, errOut bytes.Buffer
	if code := Run([]string{"init", "--config", path}, &out, &errOut); code != 0 {
		t.Fatalf("init=%d stderr=%s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"config", "check", "--config", path}, &out, &errOut); code != 0 {
		t.Fatalf("check=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "configuration valid") {
		t.Fatalf("out=%q", out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"status", "--config", path}, &out, &errOut); code != 0 {
		t.Fatalf("status=%d stderr=%s", code, errOut.String())
	}
	for _, want := range []string{"client: opencode", "gateway: stopped", "configured upstreams: 0"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %q", want, out.String())
		}
	}
}

func TestProxyLifecycleAndRouteCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	var out, errOut bytes.Buffer
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		if code := Run(args, &out, &errOut); code != 0 {
			t.Fatalf("Run(%v)=%d stderr=%s", args, code, errOut.String())
		}
		return out.String()
	}
	run("init", "--config", path)
	run("proxy", "add", "direct", "--base-url", "https://api.example.test/v1", "--api-key-env", "EXAMPLE_KEY", "--priority", "20", "--config", path)
	listed := run("proxy", "list", "--config", path)
	for _, want := range []string{"direct", "true", "20", "EXAMPLE_KEY"} {
		if !strings.Contains(listed, want) {
			t.Fatalf("list missing %q: %s", want, listed)
		}
	}
	run("proxy", "disable", "direct", "--config", path)
	if got := run("proxy", "list", "--config", path); !strings.Contains(got, "false") {
		t.Fatal(got)
	}
	run("proxy", "enable", "direct", "--config", path)
	run("proxy", "edit", "direct", "--proxy-url", "socks5h://127.0.0.1:9050", "--config", path)
	run("route", "set-strategy", "failover", "--config", path)
	run("route", "set-priority", "direct", "5", "--config", path)
	got := run("route", "status", "--config", path)
	for _, want := range []string{"strategy: failover", "socks5h", "5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	run("proxy", "remove", "direct", "--config", path)
	if got := run("proxy", "list", "--config", path); !strings.Contains(got, "No proxies") {
		t.Fatal(got)
	}
}

func TestConfigPathAndDashboard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	var out, errOut bytes.Buffer
	if Run([]string{"init", "--config", path}, &out, &errOut) != 0 {
		t.Fatal(errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if Run([]string{"config", "path", "--config", path}, &out, &errOut) != 0 || !strings.Contains(out.String(), path) {
		t.Fatalf("out=%q err=%q", out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if Run([]string{"dashboard", "--config", path}, &out, &errOut) != 0 {
		t.Fatal(errOut.String())
	}
	for _, want := range []string{"NLWPROXY", "CONNECTIONS", "No routes configured"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"bogus"}, &out, &errOut); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestSetupDryRunRollbackAndUninstall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	original := []byte(`{"provider":{"other":{"name":"Other"}}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	state := filepath.Join(dir, "state")
	if code := Run([]string{"setup", "--opencode-config", path, "--state-dir", state, "--dry-run"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "proposed") {
		t.Fatalf("dry-run code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("dry-run wrote config")
	}
	out.Reset()
	errOut.Reset()
	if code := Run([]string{"setup", "--opencode-config", path, "--state-dir", state}, &out, &errOut); code != 0 {
		t.Fatalf("setup=%d %s", code, errOut.String())
	}
	if code := Run([]string{"setup", "--opencode-config", path, "--state-dir", state, "--rollback"}, &out, &errOut); code != 0 {
		t.Fatalf("rollback=%d %s", code, errOut.String())
	}
	after, _ = os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("rollback mismatch")
	}
	if code := Run([]string{"setup", "--opencode-config", path, "--state-dir", state}, &out, &errOut); code != 0 {
		t.Fatal(errOut.String())
	}
	if code := Run([]string{"uninstall", "--opencode-config", path}, &out, &errOut); code != 0 {
		t.Fatalf("uninstall=%d %s", code, errOut.String())
	}
}
