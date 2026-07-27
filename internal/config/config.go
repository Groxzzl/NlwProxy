package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Client        string        `json:"client"`
	Server        Server        `json:"server"`
	Routing       Routing       `json:"routing"`
	Observability Observability `json:"observability,omitempty"`
	Upstreams     []Upstream    `json:"upstreams"`
}

type Observability struct {
	ExitIPProbe ExitIPProbe `json:"exit_ip_probe,omitempty"`
}

type ExitIPProbe struct {
	Enabled  bool   `json:"enabled,omitempty"`
	URL      string `json:"url,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
	CacheTTL string `json:"cache_ttl,omitempty"`
}

type Server struct {
	Listen               string `json:"listen"`
	LocalTokenEnv        string `json:"local_token_env,omitempty"`
	StrictOpenCodeClient bool   `json:"strict_opencode_client,omitempty"`
	MaxBodyBytes         int64  `json:"max_body_bytes,omitempty"`
}

type Routing struct {
	Strategy string `json:"strategy"`
}

type Upstream struct {
	Name      string            `json:"name"`
	BaseURL   string            `json:"base_url"`
	ProxyURL  string            `json:"proxy_url,omitempty"`
	APIKeyEnv string            `json:"api_key_env,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Priority  int               `json:"priority,omitempty"`
	Weight    int               `json:"weight,omitempty"`
	Enabled   bool              `json:"enabled"`
}

func Default() Config {
	return Config{
		Client: "opencode",
		Server: Server{
			Listen:               "127.0.0.1:8787",
			LocalTokenEnv:        "NLW_PROXY_LOCAL_TOKEN",
			StrictOpenCodeClient: false,
			MaxBodyBytes:         20 << 20,
		},
		Routing:       Routing{Strategy: "round_robin"},
		Observability: Observability{ExitIPProbe: ExitIPProbe{URL: "https://api.ipify.org?format=json", Timeout: "3s", CacheTTL: "15m"}},
		Upstreams:     []Upstream{},
	}
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	var cfg Config
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Write(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nlwproxy-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		// Windows cannot atomically replace an existing file with os.Rename.
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		err = os.Rename(tmpName, path)
	}
	return err
}

func WriteDefault(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return os.ErrExist
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return Write(path, Default())
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Client) == "" {
		return errors.New("client must not be empty")
	}
	host, _, err := net.SplitHostPort(c.Server.Listen)
	if err != nil {
		return fmt.Errorf("server.listen: %w", err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("server.listen must use a loopback host")
	}
	if c.Routing.Strategy != "" && c.Routing.Strategy != "round_robin" && c.Routing.Strategy != "failover" {
		return errors.New("routing.strategy must be round_robin or failover")
	}
	probe := c.Observability.ExitIPProbe
	if probe.Enabled {
		u, err := url.Parse(probe.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errors.New("observability.exit_ip_probe.url must be credential-free HTTPS")
		}
	}
	for field, value := range map[string]string{"timeout": probe.Timeout, "cache_ttl": probe.CacheTTL} {
		if value == "" {
			continue
		}
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return fmt.Errorf("observability.exit_ip_probe.%s must be a positive duration", field)
		}
	}
	seen := map[string]bool{}
	for i, up := range c.Upstreams {
		if strings.TrimSpace(up.Name) == "" || seen[up.Name] {
			return fmt.Errorf("upstreams[%d].name must be non-empty and unique", i)
		}
		seen[up.Name] = true
		u, err := url.Parse(up.BaseURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("upstreams[%d].base_url must be HTTPS without credentials", i)
		}
		if strings.ContainsAny(up.BaseURL, "\r\n") || strings.ContainsAny(up.ProxyURL, "\r\n") {
			return fmt.Errorf("upstreams[%d] URLs must not contain CRLF", i)
		}
		if up.ProxyURL != "" {
			p, err := url.Parse(up.ProxyURL)
			if err != nil || p.Host == "" || (p.Scheme != "http" && p.Scheme != "https" && p.Scheme != "socks5" && p.Scheme != "socks5h") {
				return fmt.Errorf("upstreams[%d].proxy_url has unsupported scheme", i)
			}
			if p.User != nil {
				return fmt.Errorf("upstreams[%d].proxy_url must not contain plaintext credentials", i)
			}
		}
		if up.APIKeyEnv != "" && !validEnvName(up.APIKeyEnv) {
			return fmt.Errorf("upstreams[%d].api_key_env must be an environment variable name", i)
		}
		if up.Priority < 0 || up.Weight < 0 {
			return fmt.Errorf("upstreams[%d] priority and weight must not be negative", i)
		}
	}
	return nil
}

func validEnvName(value string) bool {
	if value == "" || !(value[0] == '_' || value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
