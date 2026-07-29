package proxymanager

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nlwproxy/internal/geo"
)

// proxyKey is the dedup key for a proxy: host:port (case-insensitive host).
func proxyKey(host string, port int) string {
	return fmt.Sprintf("%s:%d", strings.ToLower(host), port)
}

// hasKey reports whether an entry with the given host:port already exists.
// Caller must hold at least a read lock.
func (m *Manager) hasKey(key string) bool {
	for _, e := range m.entries {
		if proxyKey(e.Host, e.Port) == key {
			return true
		}
	}
	return false
}

// AddDedup inserts an entry only if no existing entry shares its host:port.
// Returns the new ID and true when added, or "" and false when a duplicate.
func (m *Manager) AddDedup(entry ProxyEntry) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hasKey(proxyKey(entry.Host, entry.Port)) {
		return "", false
	}
	m.nextID++
	id := fmt.Sprintf("proxy-%d", m.nextID)
	entry.ID = id
	if entry.AddedAt.IsZero() {
		entry.AddedAt = time.Now()
	}
	m.entries[id] = &entry
	return id, true
}

// importFileDedup parses a text file, adding valid proxies while deduping by
// host:port across the whole manager. Returns count added (net of dupes).
func (m *Manager) importFileDedup(path string) (added, skipped int, errors []string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, []string{fmt.Sprintf("open file: %v", err)}
	}
	defer f.Close()

	base := filepath.Base(path)
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries, errs := parseProxyLine(line)
		for _, entry := range entries {
			if entry.Source == "" {
				entry.Source = base
			}
			if _, ok := m.AddDedup(entry); ok {
				added++
			} else {
				skipped++
			}
		}
		for _, e := range errs {
			errors = append(errors, fmt.Sprintf("%s line %d: %s", base, lineNum, e))
		}
	}
	if err := scanner.Err(); err != nil {
		errors = append(errors, fmt.Sprintf("%s read error: %v", base, err))
	}
	return
}

// LoadDir walks *.txt files in dir (non-recursive), importing and deduping all
// proxies by host:port. The directory is created if missing. Files are
// processed in lexical order for deterministic dedup precedence. Returns the
// total number of newly added entries.
func (m *Manager) LoadDir(dir string) (added int, errors []string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, []string{fmt.Sprintf("mkdir %s: %v", dir, err)}
	}
	glob := filepath.Join(dir, "*.txt")
	matches, err := filepath.Glob(glob)
	if err != nil {
		return 0, []string{fmt.Sprintf("glob %s: %v", glob, err)}
	}
	sort.Strings(matches)
	for _, path := range matches {
		info, statErr := os.Stat(path)
		if statErr != nil || info.IsDir() {
			continue
		}
		a, _, errs := m.importFileDedup(path)
		added += a
		errors = append(errors, errs...)
	}
	return added, errors
}

// persistedProxy is the on-disk JSON shape. Passwords are included: this is
// local user data, not a shared secret store.
type persistedProxy struct {
	ID        string        `json:"id"`
	Host      string        `json:"host"`
	Port      int           `json:"port"`
	Scheme    ProxyScheme   `json:"scheme"`
	Username  string        `json:"username,omitempty"`
	Password  string        `json:"password,omitempty"`
	Label     string        `json:"label,omitempty"`
	Source    string        `json:"source,omitempty"`
	CheckedAt time.Time     `json:"checked_at,omitempty"`
	Latency   time.Duration `json:"latency_ns,omitempty"`
	Alive     bool          `json:"alive"`
	Error     string        `json:"error,omitempty"`
	Geo       geo.Result    `json:"geo,omitempty"`
}

// RestoreJSON merges cached health and geo fields into proxies already loaded
// from text files. Addresses and credentials continue to come from the text
// files, so a stale cache cannot re-introduce a removed proxy.
func (m *Manager) RestoreJSON(path string, maxAge time.Duration, now time.Time) (restored int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var saved []persistedProxy
	if err := json.Unmarshal(data, &saved); err != nil {
		return 0, err
	}
	byKey := make(map[string]persistedProxy, len(saved))
	for _, item := range saved {
		byKey[proxyKey(item.Host, item.Port)] = item
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.entries {
		item, ok := byKey[proxyKey(entry.Host, entry.Port)]
		if !ok || item.CheckedAt.IsZero() || (maxAge > 0 && now.Sub(item.CheckedAt) > maxAge) {
			continue
		}
		entry.CheckedAt = item.CheckedAt
		entry.Latency = item.Latency
		entry.Alive = item.Alive
		entry.Error = item.Error
		entry.Geo = item.Geo
		restored++
	}
	return restored, nil
}

// SaveJSON persists the merged proxy set to path, including local proxy
// credentials. It writes atomically and uses owner-only permissions.
func (m *Manager) SaveJSON(path string) error {
	m.mu.RLock()
	out := make([]persistedProxy, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, persistedProxy{
			ID:        e.ID,
			Host:      e.Host,
			Port:      e.Port,
			Scheme:    e.Scheme,
			Username:  e.Username,
			Password:  e.Password,
			Label:     e.Label,
			Source:    e.Source,
			CheckedAt: e.CheckedAt,
			Latency:   e.Latency,
			Alive:     e.Alive,
			Error:     e.Error,
			Geo:       e.Geo,
		})
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host == out[j].Host {
			return out[i].Port < out[j].Port
		}
		return out[i].Host < out[j].Host
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return atomicReplace(tmp, path)
}
