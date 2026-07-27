package opencode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const providerName = "nlwproxy"

type Options struct {
	Path     string
	StateDir string
	DryRun   bool
	BaseURL  string
	APIKey   string
	Models   []string
}

type Result struct {
	Path       string
	BackupPath string
	Checksum   string
	Diff       string
	Changed    bool
}

type manifest struct {
	ConfigPath string `json:"config_path"`
	BackupPath string `json:"backup_path"`
	SHA256     string `json:"sha256"`
}

func Discover(explicit, home string, environ []string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("NLP-CFG-001: %w", err)
		}
		return explicit, nil
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	env := envMap(environ)
	candidates := []string{}
	if p := env["OPENCODE_CONFIG"]; p != "" {
		candidates = append(candidates, p)
	}
	if x := env["XDG_CONFIG_HOME"]; x != "" {
		candidates = append(candidates, filepath.Join(x, "opencode", "opencode.json"), filepath.Join(x, "opencode", "opencode.jsonc"))
	}
	if runtime.GOOS == "windows" {
		if app := env["APPDATA"]; app != "" {
			candidates = append(candidates, filepath.Join(app, "opencode", "opencode.json"), filepath.Join(app, "opencode", "opencode.jsonc"))
		}
	}
	candidates = append(candidates,
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		filepath.Join(home, ".config", "opencode", "opencode.jsonc"),
		filepath.Join(home, ".opencode.json"),
	)
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("NLP-CFG-001: OpenCode config not found")
}

func Setup(opts Options) (Result, error) {
	original, root, err := readRoot(opts.Path)
	if err != nil {
		return Result{}, err
	}
	patchRoot(root, opts)
	patched, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return Result{}, err
	}
	patched = append(patched, '\n')
	result := Result{Path: opts.Path, Changed: !jsonEquivalent(original, patched), Diff: diffPreview(original, patched)}
	if opts.DryRun || !result.Changed {
		return result, nil
	}
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(filepath.Dir(opts.Path), ".nlwproxy")
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return Result{}, err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backup := filepath.Join(stateDir, "opencode."+stamp+".bak")
	if err := writeAtomic(backup, original, 0600); err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(original)
	result.BackupPath, result.Checksum = backup, hex.EncodeToString(sum[:])
	m := manifest{ConfigPath: opts.Path, BackupPath: backup, SHA256: result.Checksum}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := writeAtomic(filepath.Join(stateDir, "setup.json"), append(mb, '\n'), 0600); err != nil {
		_ = os.Remove(backup)
		return Result{}, err
	}
	if err := writeAtomic(opts.Path, patched, 0600); err != nil {
		return Result{}, err
	}
	return result, nil
}

func Rollback(path, stateDir string) error {
	if stateDir == "" {
		stateDir = filepath.Join(filepath.Dir(path), ".nlwproxy")
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "setup.json"))
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if path != "" && filepath.Clean(path) != filepath.Clean(m.ConfigPath) {
		return errors.New("rollback manifest belongs to another config")
	}
	original, err := os.ReadFile(m.BackupPath)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(original)
	if hex.EncodeToString(sum[:]) != m.SHA256 {
		return errors.New("backup checksum mismatch")
	}
	return writeAtomic(m.ConfigPath, original, 0600)
}

func Uninstall(path string) error {
	_, root, err := readRoot(path)
	if err != nil {
		return err
	}
	providers, _ := root["provider"].(map[string]any)
	delete(providers, providerName)
	if len(providers) == 0 {
		delete(root, "provider")
	}
	if model, ok := root["model"].(string); ok && strings.HasPrefix(model, providerName+"/") {
		delete(root, "model")
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(out, '\n'), 0600)
}

func readRoot(path string) ([]byte, map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("NLP-CFG-001: %w", err)
	}
	var root map[string]any
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(&root); err != nil {
		return nil, nil, fmt.Errorf("NLP-CFG-002: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return b, root, nil
}

func patchRoot(root map[string]any, opts Options) {
	providers, ok := root["provider"].(map[string]any)
	if !ok {
		providers = map[string]any{}
		root["provider"] = providers
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8787/v1"
	}
	apiKey := opts.APIKey
	if apiKey == "" {
		apiKey = "{env:NLW_PROXY_LOCAL_TOKEN}"
	}
	models := map[string]any{}
	for _, id := range opts.Models {
		if id != "" && id != "opencode-route" {
			models[id] = map[string]any{"name": id}
		}
	}
	providers[providerName] = map[string]any{
		"npm": "@ai-sdk/openai-compatible", "name": "NLW Proxy",
		"options": map[string]any{"baseURL": baseURL, "apiKey": apiKey},
		"models":  models,
	}
	if len(opts.Models) > 0 && opts.Models[0] != "" && opts.Models[0] != "opencode-route" {
		root["model"] = "nlwproxy/" + opts.Models[0]
	}
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nlwproxy-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	return os.Rename(name, path)
}

func jsonEquivalent(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && fmt.Sprint(x) == fmt.Sprint(y)
}
func diffPreview(old, next []byte) string {
	if jsonEquivalent(old, next) {
		return "no changes\n"
	}
	return "--- current\n+++ proposed\n-" + strings.ReplaceAll(strings.TrimSpace(string(old)), "\n", "\n-") + "\n+" + strings.ReplaceAll(strings.TrimSpace(string(next)), "\n", "\n+") + "\n"
}
func envMap(environ []string) map[string]string {
	if environ == nil {
		environ = os.Environ()
	}
	m := map[string]string{}
	for _, item := range environ {
		if i := strings.IndexByte(item, '='); i >= 0 {
			m[item[:i]] = item[i+1:]
		}
	}
	return m
}

// Candidates returns discovery locations for diagnostics without probing their contents.
func Candidates(home string, environ []string) []string {
	m := envMap(environ)
	out := []string{}
	for _, key := range []string{"OPENCODE_CONFIG", "XDG_CONFIG_HOME", "APPDATA"} {
		if m[key] != "" {
			out = append(out, m[key])
		}
	}
	out = append(out, filepath.Join(home, ".config", "opencode", "opencode.json"))
	sort.Strings(out)
	return out
}
