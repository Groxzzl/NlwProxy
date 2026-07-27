package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"nlwproxy/internal/config"
	"nlwproxy/internal/console"
	"nlwproxy/internal/gateway"
	"nlwproxy/internal/metrics"
	"nlwproxy/internal/opencode"
	"nlwproxy/internal/profiles"
	"nlwproxy/internal/routing"
	"nlwproxy/internal/transport"
	"nlwproxy/internal/tui"
)

const version = "0.2.0"

func defaultConfigPath() string {
	if p := os.Getenv("NLWPROXY_CONFIG"); p != "" {
		return p
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "nlwproxy.json"
	}
	return filepath.Join(d, "nlwproxy", "config.json")
}

func Run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		usage(out)
		return 0
	}
	if args[0] == "gateway" {
		return runGateway(args[1:], out, errOut)
	}
	switch args[0] {
	case "init":
		return runInit(args[1:], out, errOut)
	case "config":
		return runConfig(args[1:], out, errOut)
	case "proxy":
		return runProxy(args[1:], out, errOut)
	case "route":
		return runRoute(args[1:], out, errOut)
	case "setup":
		return runSetup(args[1:], out, errOut)
	case "uninstall":
		return runUninstall(args[1:], out, errOut)
	case "status", "dashboard":
		return runStatus(args[0], args[1:], out, errOut)
	case "serve":
		return runServe(args[1:], out, errOut)
	case "console":
		return runConsole(args[1:], out, errOut)
	case "profile":
		return runProfile(args[1:], out, errOut)
	case "version", "--version":
		fmt.Fprintln(out, version)
		return 0
	case "help", "--help", "-h":
		usage(out)
		return 0
	default:
		fmt.Fprintln(errOut, "unknown command:", args[0])
		usage(errOut)
		return 2
	}
}

func runSetup(args []string, out, errOut io.Writer) int {
	fs := commandFlags("setup", errOut)
	path := fs.String("opencode-config", "", "OpenCode configuration path")
	stateDir := fs.String("state-dir", "", "setup state directory")
	dryRun := fs.Bool("dry-run", false, "preview changes")
	rollback := fs.Bool("rollback", false, "restore exact original configuration")
	if fs.Parse(args) != nil {
		return 2
	}
	if *path == "" {
		var err error
		*path, err = opencode.Discover("", "", nil)
		if err != nil {
			fmt.Fprintln(errOut, "setup:", err)
			return 1
		}
	}
	if *rollback {
		if err := opencode.Rollback(*path, *stateDir); err != nil {
			fmt.Fprintln(errOut, "rollback:", err)
			return 1
		}
		fmt.Fprintln(out, "restored", *path)
		return 0
	}
	result, err := opencode.Setup(opencode.Options{Path: *path, StateDir: *stateDir, DryRun: *dryRun})
	if err != nil {
		fmt.Fprintln(errOut, "setup:", err)
		return 1
	}
	if *dryRun {
		fmt.Fprint(out, result.Diff)
		return 0
	}
	if !result.Changed {
		fmt.Fprintln(out, "OpenCode configuration already configured")
		return 0
	}
	fmt.Fprintf(out, "configured %s\nbackup: %s\nsha256: %s\n", result.Path, result.BackupPath, result.Checksum)
	return 0
}

func runUninstall(args []string, out, errOut io.Writer) int {
	fs := commandFlags("uninstall", errOut)
	path := fs.String("opencode-config", "", "OpenCode configuration path")
	if fs.Parse(args) != nil {
		return 2
	}
	if *path == "" {
		var err error
		*path, err = opencode.Discover("", "", nil)
		if err != nil {
			fmt.Fprintln(errOut, "uninstall:", err)
			return 1
		}
	}
	if err := opencode.Uninstall(*path); err != nil {
		fmt.Fprintln(errOut, "uninstall:", err)
		return 1
	}
	fmt.Fprintln(out, "removed NLW Proxy provider from", *path)
	return 0
}

func runInit(args []string, out, errOut io.Writer) int {
	fs := commandFlags("init", errOut)
	path := fs.String("config", defaultConfigPath(), "configuration path")
	force := fs.Bool("force", false, "overwrite")
	if fs.Parse(args) != nil {
		return 2
	}
	if err := config.WriteDefault(*path, *force); err != nil {
		fmt.Fprintln(errOut, "init:", err)
		return 1
	}
	fmt.Fprintln(out, "created", *path)
	fmt.Fprintln(out, "next: nlwproxy proxy add direct --base-url https://api.example.com/v1 --api-key-env PROVIDER_API_KEY --config", *path)
	return 0
}

func runConfig(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: nlwproxy config <check|path|print-redacted>")
		return 2
	}
	path, ok := configFlag(args[1:], errOut)
	if !ok {
		return 2
	}
	switch args[0] {
	case "path":
		fmt.Fprintln(out, path)
		return 0
	case "check":
		if _, err := config.Load(path); err != nil {
			fmt.Fprintln(errOut, "configuration invalid:", err)
			return 1
		}
		fmt.Fprintln(out, "configuration valid:", path)
		return 0
	case "print-redacted":
		cfg, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(errOut, "configuration:", err)
			return 1
		}
		for i := range cfg.Upstreams {
			if u, err := url.Parse(cfg.Upstreams[i].ProxyURL); err == nil && u.User != nil {
				u.User = url.User("REDACTED")
				cfg.Upstreams[i].ProxyURL = u.String()
			}
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Fprintln(out, string(data))
		return 0
	default:
		fmt.Fprintln(errOut, "unknown config command:", args[0])
		return 2
	}
}

func runProxy(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: nlwproxy proxy <add|edit|remove|list|enable|disable|test>")
		return 2
	}
	if args[0] == "list" {
		path, ok := configFlag(args[1:], errOut)
		if !ok {
			return 2
		}
		cfg, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(errOut, "configuration:", err)
			return 1
		}
		printProxyList(out, cfg)
		return 0
	}
	if len(args) < 2 {
		fmt.Fprintln(errOut, "proxy name is required")
		return 2
	}
	name := args[1]
	switch args[0] {
	case "add", "edit":
		return runProxyUpsert(args[0], name, args[2:], out, errOut)
	case "remove", "enable", "disable":
		path, ok := configFlag(args[2:], errOut)
		if !ok {
			return 2
		}
		cfg, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(errOut, "configuration:", err)
			return 1
		}
		idx := upstreamIndex(cfg, name)
		if idx < 0 {
			fmt.Fprintln(errOut, "proxy not found:", name)
			return 1
		}
		if args[0] == "remove" {
			cfg.Upstreams = append(cfg.Upstreams[:idx], cfg.Upstreams[idx+1:]...)
		} else {
			cfg.Upstreams[idx].Enabled = args[0] == "enable"
		}
		if err := config.Write(path, cfg); err != nil {
			fmt.Fprintln(errOut, "write configuration:", err)
			return 1
		}
		fmt.Fprintf(out, "%sd %s\n", strings.TrimSuffix(args[0], "e"), name)
		return 0
	case "test":
		fs := commandFlags("proxy test", errOut)
		path := fs.String("config", defaultConfigPath(), "configuration path")
		timeout := fs.Duration("timeout", 5*time.Second, "TCP connection timeout")
		if fs.Parse(args[2:]) != nil {
			return 2
		}
		cfg, err := config.Load(*path)
		if err != nil {
			fmt.Fprintln(errOut, "configuration:", err)
			return 1
		}
		idx := upstreamIndex(cfg, name)
		if idx < 0 {
			fmt.Fprintln(errOut, "proxy not found:", name)
			return 1
		}
		return testProxy(cfg.Upstreams[idx], *timeout, out, errOut)
	default:
		fmt.Fprintln(errOut, "unknown proxy command:", args[0])
		return 2
	}
}

func runProxyUpsert(action, name string, args []string, out, errOut io.Writer) int {
	fs := commandFlags("proxy "+action, errOut)
	path := fs.String("config", defaultConfigPath(), "configuration path")
	baseURL := fs.String("base-url", "", "authorized HTTPS provider base URL")
	proxyURL := fs.String("proxy-url", "", "authorized HTTP(S)/SOCKS5 proxy URL")
	apiKeyEnv := fs.String("api-key-env", "", "provider API key environment variable")
	priority := fs.Int("priority", 100, "route priority")
	weight := fs.Int("weight", 1, "route weight")
	enabled := fs.Bool("enabled", true, "enable route")
	if fs.Parse(args) != nil {
		return 2
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(errOut, "configuration:", err)
		return 1
	}
	idx := upstreamIndex(cfg, name)
	if action == "add" && idx >= 0 {
		fmt.Fprintln(errOut, "proxy already exists:", name)
		return 1
	}
	if action == "edit" && idx < 0 {
		fmt.Fprintln(errOut, "proxy not found:", name)
		return 1
	}
	var up config.Upstream
	if idx >= 0 {
		up = cfg.Upstreams[idx]
	}
	up.Name = name
	if *baseURL != "" {
		up.BaseURL = *baseURL
	}
	if action == "add" || flagProvided(fs, "proxy-url") {
		up.ProxyURL = *proxyURL
	}
	if action == "add" || flagProvided(fs, "api-key-env") {
		up.APIKeyEnv = *apiKeyEnv
	}
	if action == "add" || flagProvided(fs, "priority") {
		up.Priority = *priority
	}
	if action == "add" || flagProvided(fs, "weight") {
		up.Weight = *weight
	}
	if action == "add" || flagProvided(fs, "enabled") {
		up.Enabled = *enabled
	}
	if idx < 0 {
		cfg.Upstreams = append(cfg.Upstreams, up)
	} else {
		cfg.Upstreams[idx] = up
	}
	if err := config.Write(*path, cfg); err != nil {
		fmt.Fprintln(errOut, "write configuration:", err)
		return 1
	}
	fmt.Fprintf(out, "%sed %s\n", action, name)
	return 0
}

func runRoute(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: nlwproxy route <status|set-strategy|set-priority>")
		return 2
	}
	switch args[0] {
	case "status":
		path, ok := configFlag(args[1:], errOut)
		if !ok {
			return 2
		}
		cfg, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(errOut, "configuration:", err)
			return 1
		}
		fmt.Fprintln(out, "strategy:", cfg.Routing.Strategy)
		printProxyList(out, cfg)
		return 0
	case "set-strategy":
		if len(args) < 2 {
			fmt.Fprintln(errOut, "strategy is required: round_robin or failover")
			return 2
		}
		path, ok := configFlag(args[2:], errOut)
		if !ok {
			return 2
		}
		cfg, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(errOut, "configuration:", err)
			return 1
		}
		cfg.Routing.Strategy = args[1]
		if err := config.Write(path, cfg); err != nil {
			fmt.Fprintln(errOut, "write configuration:", err)
			return 1
		}
		fmt.Fprintln(out, "strategy set:", args[1])
		return 0
	case "set-priority":
		if len(args) < 3 {
			fmt.Fprintln(errOut, "usage: nlwproxy route set-priority <name> <priority> [--config path]")
			return 2
		}
		var priority int
		if _, err := fmt.Sscan(args[2], &priority); err != nil {
			fmt.Fprintln(errOut, "invalid priority:", args[2])
			return 2
		}
		path, ok := configFlag(args[3:], errOut)
		if !ok {
			return 2
		}
		cfg, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(errOut, "configuration:", err)
			return 1
		}
		idx := upstreamIndex(cfg, args[1])
		if idx < 0 {
			fmt.Fprintln(errOut, "proxy not found:", args[1])
			return 1
		}
		cfg.Upstreams[idx].Priority = priority
		if err := config.Write(path, cfg); err != nil {
			fmt.Fprintln(errOut, "write configuration:", err)
			return 1
		}
		fmt.Fprintf(out, "priority set: %s=%d\n", args[1], priority)
		return 0
	default:
		fmt.Fprintln(errOut, "unknown route command:", args[0])
		return 2
	}
}

func runGateway(args []string, out, errOut io.Writer) int {
	fs := commandFlags("gateway", errOut)
	path := fs.String("config", defaultConfigPath(), "configuration path")
	if fs.Parse(args) != nil {
		return 2
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(errOut, "configuration:", err)
		return 1
	}
	tokenEnv := cfg.Server.LocalTokenEnv
	fmt.Fprintln(out, "NLW Proxy gateway")
	fmt.Fprintln(out, "Base URL : http://"+cfg.Server.Listen+"/v1")
	fmt.Fprintln(out, "API key  : environment variable "+tokenEnv)
	fmt.Fprintln(out, "Models   : upstream catalog (transparent IDs)")
	fmt.Fprintln(out, "Provider : OpenAI-compatible")
	return 0
}

func runProfile(args []string, out, errOut io.Writer) int {
	fs := commandFlags("profile", errOut)
	dir := fs.String("dir", "profiles", "profiles directory")
	if fs.Parse(args) != nil {
		return 2
	}
	store, err := profiles.Open(*dir)
	if err != nil {
		fmt.Fprintln(errOut, "profile:", err)
		return 1
	}
	commandArgs := fs.Args()
	if len(commandArgs) == 0 {
		fmt.Fprintln(out, "usage: nlwproxy profile <list|show|create|update|delete|use|activate> [id]")
		return 0
	}
	command := commandArgs[0]
	switch command {
	case "list":
		entries, err := store.List()
		if err != nil {
			fmt.Fprintln(errOut, "profile:", err)
			return 1
		}
		idx, _ := store.Index()
		fmt.Fprintln(out, "ID\tNAME\tACTIVE\tAPI KEY ENV")
		for _, entry := range entries {
			fmt.Fprintf(out, "%s\t%s\t%t\t%s\n", entry.ID, entry.Name, entry.ID == idx.Active, entry.APIKeyEnv)
		}
		return 0
	case "activate":
		if len(commandArgs) < 2 {
			fmt.Fprintln(errOut, "usage: nlwproxy profile activate <id>")
			return 2
		}
		profile, err := store.Activate(commandArgs[1])
		if err != nil {
			fmt.Fprintln(errOut, "profile:", err)
			return 1
		}
		fmt.Fprintln(out, "active profile:", profile.Name)
		return 0
	case "delete":
		if len(commandArgs) < 2 {
			fmt.Fprintln(errOut, "usage: nlwproxy profile delete <id>")
			return 2
		}
		if err := store.Delete(commandArgs[1]); err != nil {
			fmt.Fprintln(errOut, "profile:", err)
			return 1
		}
		fmt.Fprintln(out, "deleted profile:", commandArgs[1])
		return 0
	default:
		fmt.Fprintln(errOut, "usage: nlwproxy profile <list|show|create|update|delete|use|activate> [id]")
		return 2
	}
}

func runConsole(args []string, out, errOut io.Writer) int {
	fs := commandFlags("console", errOut)
	path := fs.String("config", defaultConfigPath(), "configuration path")
	profilesDir := fs.String("profiles-dir", "profiles", "profiles directory")
	if fs.Parse(args) != nil {
		return 2
	}
	store, err := profiles.Open(*profilesDir)
	if err != nil {
		fmt.Fprintln(errOut, "console profiles:", err)
		return 1
	}
	selected, err := prepareConsoleProfile(store, *path)
	if errors.Is(err, profiles.ErrSelectionRequired) {
		entries, listErr := store.List()
		if listErr != nil {
			fmt.Fprintln(errOut, "console profiles:", listErr)
			return 1
		}
		choices := make([]console.Profile, 0, len(entries))
		entryIDs := make(map[string]string, len(entries))
		for _, entry := range entries {
			profile, getErr := store.Get(entry.ID)
			if getErr != nil {
				continue
			}
			detail := "configured"
			if len(profile.Config.Upstreams) > 0 {
				detail = profile.Config.Upstreams[0].BaseURL
			}
			choices = append(choices, console.Profile{Name: profile.Name, Detail: detail, Enabled: true})
			entryIDs[profile.Name] = profile.ID
		}
		var chosenID string
		selectorErr := console.RunProfileSelector(os.Stdin, os.Stdout, choices, console.SelectorHandlers{Select: func(choice console.Profile) error {
			chosenID = entryIDs[choice.Name]
			return nil
		}})
		if selectorErr != nil {
			fmt.Fprintln(errOut, "console selector:", selectorErr)
			return 1
		}
		if chosenID == "" {
			return 0
		}
		selected, err = store.Activate(chosenID)
	}
	if errors.Is(err, profiles.ErrWizardRequired) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err = (console.Wizard{In: os.Stdin, Out: out}).Run(ctx, *path); err != nil {
			fmt.Fprintln(errOut, "console setup:", err)
			return 1
		}
		if _, _, err = store.Migrate(*path); err != nil {
			fmt.Fprintln(errOut, "console profiles:", err)
			return 1
		}
		selected, err = store.Select()
	}
	if err != nil {
		fmt.Fprintln(errOut, "console profiles:", err)
		return 1
	}
	cfg := selected.Config
	credentialNames := []string{cfg.Server.LocalTokenEnv}
	for _, candidate := range cfg.Upstreams {
		if candidate.Enabled {
			credentialNames = append(credentialNames, candidate.APIKeyEnv)
		}
	}
	for _, name := range credentialNames {
		if _, credentialErr := loadCredential(name, registryCredentialSource{}); credentialErr != nil {
			// A missing registry value is equivalent to a missing process variable.
			continue
		}
	}
	needsSetup := len(cfg.Upstreams) == 0 || cfg.Server.LocalTokenEnv == "" || os.Getenv(cfg.Server.LocalTokenEnv) == ""
	if !needsSetup {
		for _, candidate := range cfg.Upstreams {
			if candidate.Enabled && (candidate.APIKeyEnv == "" || os.Getenv(candidate.APIKeyEnv) == "") {
				needsSetup = true
				break
			}
		}
	}
	if needsSetup {
		fmt.Fprintln(errOut, "console: required environment credentials are missing")
		return 1
	}
	if len(cfg.Upstreams) == 0 {
		fmt.Fprintln(errOut, "console: no provider configured")
		return 1
	}
	up := cfg.Upstreams[0]
	localToken := os.Getenv(cfg.Server.LocalTokenEnv)
	providerKey := os.Getenv(up.APIKeyEnv)
	if localToken == "" || providerKey == "" {
		fmt.Fprintln(errOut, "console: required environment credentials are missing")
		return 1
	}
	events := metrics.NewEventBus(256)
	targets := make([]routing.Target, 0, len(cfg.Upstreams))
	var modelTransport http.RoundTripper
	for _, route := range cfg.Upstreams {
		if !route.Enabled {
			continue
		}
		base, parseErr := url.Parse(route.BaseURL)
		if parseErr != nil {
			fmt.Fprintf(errOut, "console: route %s: %v\n", route.Name, parseErr)
			return 1
		}
		mode := transport.Direct
		if route.ProxyURL != "" {
			proxyURL, proxyErr := url.Parse(route.ProxyURL)
			if proxyErr != nil {
				fmt.Fprintf(errOut, "console: route %s proxy: %v\n", route.Name, proxyErr)
				return 1
			}
			if proxyURL.Scheme == "socks5" || proxyURL.Scheme == "socks5h" {
				mode = transport.SOCKS5
			} else {
				mode = transport.HTTPProxy
			}
		}
		rt, transportErr := transport.New(transport.Config{Mode: mode, ProxyURL: route.ProxyURL, Timeout: 30 * time.Second})
		if transportErr != nil {
			fmt.Fprintf(errOut, "console: route %s: %v\n", route.Name, transportErr)
			return 1
		}
		key := ""
		if route.APIKeyEnv != "" {
			key = os.Getenv(route.APIKeyEnv)
		}
		wrapped := &upstreamTransport{base: base, apiKey: key, headers: route.Headers, next: rt}
		if modelTransport == nil {
			modelTransport = wrapped
		}
		targets = append(targets, routing.Target{Name: route.Name, Priority: route.Priority, Enabled: true, MaxConcurrency: 8, TransportType: string(mode), Transport: wrapped})
	}
	if len(targets) == 0 {
		fmt.Fprintln(errOut, "console: no enabled routes")
		return 1
	}
	strategy := routing.Priority
	if cfg.Routing.Strategy == "round_robin" {
		strategy = routing.RoundRobin
	}
	selector := routing.New(targets, routing.Config{Strategy: strategy})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	modelService := &gateway.CachedModelService{Transport: modelTransport, TTL: 5 * time.Minute}
	handler := gateway.New(gateway.Config{Token: localToken, MaxBodyBytes: cfg.Server.MaxBodyBytes, Attempts: 2, Events: events, Models: modelService}, selector)
	modelCatalog := []console.CatalogModel{}
	var modelCatalogMu sync.RWMutex
	started := time.Now()
	go func() {
		catalogCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if models, discoverErr := modelService.Discover(catalogCtx); discoverErr == nil {
			items := make([]console.CatalogModel, 0, len(models))
			for _, model := range models {
				items = append(items, console.CatalogModel{ID: model.ID, Name: model.Name})
			}
			modelCatalogMu.Lock()
			modelCatalog = console.NormalizeCatalog(items)
			modelCatalogMu.Unlock()
		}
	}()
	gatewayCtx, cancelGateway := context.WithCancel(ctx)
	defer cancelGateway()
	serveErr := make(chan error, 1)
	go func() { serveErr <- gateway.Serve(gatewayCtx, cfg.Server.Listen, handler, 15*time.Second) }()

	showKey := true
	message := "Gateway starting..."
	configuredRoutes := make([]console.RouteStat, 0, len(cfg.Upstreams))
	for _, route := range cfg.Upstreams {
		transportName := "direct"
		if route.ProxyURL != "" {
			transportName = strings.SplitN(route.ProxyURL, ":", 2)[0]
		}
		state := "healthy"
		if !route.Enabled {
			state = "disabled"
		}
		configuredRoutes = append(configuredRoutes, console.RouteStat{Name: route.Name, Transport: transportName, State: state})
	}
	makeView := func() console.DashboardView {
		modelCatalogMu.RLock()
		catalog := append([]console.CatalogModel(nil), modelCatalog...)
		modelCatalogMu.RUnlock()
		snap := events.Snapshot()
		models, routes, input, output := console.AggregateMetadata(snap.Events, configuredRoutes)
		status := "ONLINE"
		select {
		case err := <-serveErr:
			if err != nil {
				message = err.Error()
				status = "FAILED"
			} else {
				status = "STOPPED"
			}
		default:
		}
		return console.DashboardView{Status: status, Started: started, BaseURL: "http://" + cfg.Server.Listen + "/v1", APIKey: localToken, ShowAPIKey: showKey, Provider: up.Name, ModelAlias: "transparent", Requests: snap.Total, Errors: snap.Errors, Active: snap.Active, InputTokens: input, OutputTokens: output, Models: models, AvailableModels: catalog, Routes: routes, Recent: snap.Events, Message: message}
	}
	controller := console.Controller{Cancel: cancelGateway, Handle: func(actionCtx context.Context, action console.Action) error {
		switch action {
		case console.ActionRefresh:
			message = "Dashboard refreshed."
		case console.ActionTest:
			testCtx, cancel := context.WithTimeout(actionCtx, 10*time.Second)
			defer cancel()
			if err := console.TestProvider(testCtx, nil, up.BaseURL, providerKey); err != nil {
				message = "Provider test failed: " + err.Error()
			} else {
				message = "Provider test passed."
			}
		case console.ActionSetup, console.ActionProvider:
			settings, wizardErr := (console.Wizard{In: os.Stdin, Out: out}).Run(actionCtx, *path)
			if wizardErr != nil {
				message = "Setup failed: " + wizardErr.Error()
			} else {
				updated := selected
				updated.Name = settings.Provider
				updated.Config = console.BuildConfig(settings)
				if _, updateErr := store.Update(selected.ID, updated); updateErr != nil {
					message = "Profile update failed: " + updateErr.Error()
				} else {
					message = "Provider profile saved; restart to apply routing changes."
				}
			}
		case console.ActionConfig:
			model := "MODEL_ID"
			modelCatalogMu.RLock()
			if len(modelCatalog) > 0 {
				model = modelCatalog[0].ID
			}
			modelCatalogMu.RUnlock()
			formats := console.TemplateFormats()
			labels := make([]string, len(formats))
			for i, format := range formats {
				labels[i] = string(format)
			}
			selected, ok, selectErr := console.SelectFormat(os.Stdin, out, labels)
			if selectErr != nil {
				message = selectErr.Error()
				break
			}
			if !ok {
				message = "Configuration copy cancelled."
				break
			}
			modelCatalogMu.RLock()
			catalogCopy := append([]console.CatalogModel(nil), modelCatalog...)
			modelCatalogMu.RUnlock()
			text, renderErr := console.RenderConfigTemplate(formats[selected], console.TemplateData{Provider: up.Name, BaseURL: "http://" + cfg.Server.Listen + "/v1", APIKey: localToken, Model: model, Models: catalogCopy})
			if renderErr != nil {
				message = renderErr.Error()
				break
			}
			if err := console.CopyClipboard(text); err != nil {
				message = err.Error()
			} else {
				message = string(formats[selected]) + " copied."
			}
		case console.ActionCopyKey:
			if err := console.CopyClipboard(localToken); err != nil {
				message = err.Error()
			} else {
				message = "Gateway API key copied."
			}
		case console.ActionCopyAll:
			text := "Base URL: http://" + cfg.Server.Listen + "/v1\nAPI key: " + localToken + "\nModels: use IDs from /v1/models"
			if err := console.CopyClipboard(text); err != nil {
				message = err.Error()
			} else {
				message = "Connection details copied."
			}
		case console.ActionToggleKey:
			showKey = !showKey
		case console.ActionLogs:
			message = fmt.Sprintf("%d recent metadata records in memory.", len(events.Snapshot().Events))
		case console.ActionHelp:
			message = "Keys are active immediately. Q gracefully drains and stops the gateway."
		}
		return nil
	}}
	if err := console.RunEventLoop(gatewayCtx, os.Stdin, out, time.Second, controller, func() string {
		color := console.TerminalIsInteractive(os.Stdin, os.Stdout) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
		return console.RenderDashboardV2(makeView(), color, console.TerminalWidth(os.Stdout))
	}); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(errOut, "console:", err)
		cancelGateway()
		return 1
	}
	cancelGateway()
	if err := <-serveErr; err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(errOut, "console:", err)
		return 1
	}
	return 0
}

func runServe(args []string, out, errOut io.Writer) int {
	fs := commandFlags("serve", errOut)
	path := fs.String("config", defaultConfigPath(), "configuration path")
	if fs.Parse(args) != nil {
		return 2
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(errOut, "configuration:", err)
		return 1
	}
	token := os.Getenv(cfg.Server.LocalTokenEnv)
	if cfg.Server.LocalTokenEnv == "" || token == "" {
		fmt.Fprintln(errOut, "serve: server.local_token_env must name a non-empty environment variable")
		return 1
	}
	targets := make([]routing.Target, 0, len(cfg.Upstreams))
	for _, up := range cfg.Upstreams {
		if !up.Enabled {
			continue
		}
		base, parseErr := url.Parse(up.BaseURL)
		if parseErr != nil {
			fmt.Fprintf(errOut, "serve: route %s: %v\n", up.Name, parseErr)
			return 1
		}
		mode := transport.Direct
		if up.ProxyURL != "" {
			proxy, proxyErr := url.Parse(up.ProxyURL)
			if proxyErr != nil {
				fmt.Fprintf(errOut, "serve: route %s proxy: %v\n", up.Name, proxyErr)
				return 1
			}
			if proxy.Scheme == "socks5" || proxy.Scheme == "socks5h" {
				mode = transport.SOCKS5
			} else {
				mode = transport.HTTPProxy
			}
		}
		roundTripper, transportErr := transport.New(transport.Config{Mode: mode, ProxyURL: up.ProxyURL, Timeout: 30 * time.Second})
		if transportErr != nil {
			fmt.Fprintf(errOut, "serve: route %s: %v\n", up.Name, transportErr)
			return 1
		}
		apiKey := ""
		if up.APIKeyEnv != "" {
			apiKey = os.Getenv(up.APIKeyEnv)
			if apiKey == "" {
				fmt.Fprintf(errOut, "serve: route %s requires non-empty environment variable %s\n", up.Name, up.APIKeyEnv)
				return 1
			}
		}
		targets = append(targets, routing.Target{Name: up.Name, Priority: up.Priority, Enabled: true, MaxConcurrency: 8, TransportType: string(mode), Transport: &upstreamTransport{base: base, apiKey: apiKey, headers: up.Headers, next: roundTripper}})
	}
	if len(targets) == 0 {
		fmt.Fprintln(errOut, "serve: no enabled upstream routes")
		return 1
	}
	strategy := routing.Priority
	if cfg.Routing.Strategy == "round_robin" {
		strategy = routing.RoundRobin
	}
	probe := cfg.Observability.ExitIPProbe
	probeTimeout, _ := time.ParseDuration(probe.Timeout)
	probeTTL, _ := time.ParseDuration(probe.CacheTTL)
	selector := routing.New(targets, routing.Config{Strategy: strategy, ExitIPProbe: routing.ExitIPProbeConfig{Enabled: probe.Enabled, URL: probe.URL, Timeout: probeTimeout, CacheTTL: probeTTL}})
	modelService := &gateway.CachedModelService{Transport: targets[0].Transport, TTL: 5 * time.Minute}
	handler := gateway.New(gateway.Config{Token: token, StrictOpenCode: cfg.Server.StrictOpenCodeClient, MaxBodyBytes: cfg.Server.MaxBodyBytes, Attempts: 2, Models: modelService}, selector)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		catalogCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, _ = modelService.Discover(catalogCtx)
	}()
	fmt.Fprintln(out, "NLW Proxy listening on", cfg.Server.Listen)
	if err := gateway.Serve(ctx, cfg.Server.Listen, handler, 15*time.Second); err != nil {
		fmt.Fprintln(errOut, "serve:", err)
		return 1
	}
	return 0
}

type upstreamTransport struct {
	base    *url.URL
	apiKey  string
	headers map[string]string
	next    http.RoundTripper
}

func (t *upstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.base.Scheme
	clone.URL.Host = t.base.Host
	clone.URL.Path = strings.TrimRight(t.base.Path, "/") + req.URL.Path
	clone.URL.RawPath = ""
	if t.base.RawQuery != "" {
		if clone.URL.RawQuery != "" {
			clone.URL.RawQuery = t.base.RawQuery + "&" + clone.URL.RawQuery
		} else {
			clone.URL.RawQuery = t.base.RawQuery
		}
	}
	clone.Host = t.base.Host
	if t.apiKey != "" {
		clone.Header.Set("Authorization", "Bearer "+t.apiKey)
	} else {
		clone.Header.Del("Authorization")
	}
	for key, value := range t.headers {
		if strings.EqualFold(key, "Host") || strings.ContainsAny(key+value, "\r\n") {
			return nil, errors.New("unsafe upstream header")
		}
		clone.Header.Set(key, value)
	}
	return t.next.RoundTrip(clone)
}

func runStatus(command string, args []string, out, errOut io.Writer) int {
	path, ok := configFlag(args, errOut)
	if !ok {
		return 2
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(errOut, "configuration:", err)
		return 1
	}
	if command == "status" {
		enabled := 0
		for _, up := range cfg.Upstreams {
			if up.Enabled {
				enabled++
			}
		}
		fmt.Fprintf(out, "client: %s\ngateway: stopped\nlisten: %s\nstrategy: %s\nconfigured upstreams: %d\nenabled upstreams: %d\n", cfg.Client, cfg.Server.Listen, cfg.Routing.Strategy, len(cfg.Upstreams), enabled)
		return 0
	}
	routes := make([]tui.Route, 0, len(cfg.Upstreams))
	for _, u := range cfg.Upstreams {
		transport := "direct"
		if u.ProxyURL != "" {
			transport = strings.SplitN(u.ProxyURL, ":", 2)[0]
		}
		state := "unknown"
		if !u.Enabled {
			state = "disabled"
		}
		routes = append(routes, tui.Route{Name: u.Name, Transport: transport, State: state, Priority: u.Priority})
	}
	d := tui.New(out)
	return boolCode(d.Draw(tui.Snapshot{Version: version, Listen: cfg.Server.Listen, Strategy: cfg.Routing.Strategy, Routes: routes}), errOut)
}

func testProxy(up config.Upstream, timeout time.Duration, out, errOut io.Writer) int {
	target := up.BaseURL
	label := "upstream"
	if up.ProxyURL != "" {
		target = up.ProxyURL
		label = "proxy"
	}
	u, err := url.Parse(target)
	if err != nil {
		fmt.Fprintln(errOut, "invalid route URL:", err)
		return 1
	}
	host := u.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		port := "443"
		if u.Scheme == "http" || u.Scheme == "socks5" || u.Scheme == "socks5h" {
			port = "80"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	started := time.Now()
	conn, err := net.DialTimeout("tcp", host, timeout)
	if err != nil {
		fmt.Fprintf(errOut, "%s %s failed: %v\n", label, host, err)
		return 1
	}
	conn.Close()
	fmt.Fprintf(out, "%s %s reachable in %s (TCP only; provider authorization not tested)\n", label, host, time.Since(started).Round(time.Millisecond))
	return 0
}

func printProxyList(out io.Writer, cfg config.Config) {
	if len(cfg.Upstreams) == 0 {
		fmt.Fprintln(out, "No proxies configured.")
		return
	}
	items := append([]config.Upstream(nil), cfg.Upstreams...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].Name < items[j].Name
	})
	fmt.Fprintln(out, "NAME\tTYPE\tENABLED\tPRIORITY\tBASE URL\tKEY ENV")
	for _, up := range items {
		typeName := "direct"
		if up.ProxyURL != "" {
			typeName = strings.SplitN(up.ProxyURL, ":", 2)[0]
		}
		fmt.Fprintf(out, "%s\t%s\t%t\t%d\t%s\t%s\n", up.Name, typeName, up.Enabled, up.Priority, up.BaseURL, up.APIKeyEnv)
	}
}

func upstreamIndex(cfg config.Config, name string) int {
	for i := range cfg.Upstreams {
		if cfg.Upstreams[i].Name == name {
			return i
		}
	}
	return -1
}
func commandFlags(name string, errOut io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	return fs
}
func flagProvided(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
func configFlag(args []string, errOut io.Writer) (string, bool) {
	fs := commandFlags("config", errOut)
	p := fs.String("config", defaultConfigPath(), "configuration path")
	if fs.Parse(args) != nil {
		return "", false
	}
	return *p, true
}
func boolCode(err error, errOut io.Writer) int {
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `nlwproxy — OpenCode-only local proxy manager

Usage:
  nlwproxy init [--config path] [--force]
  nlwproxy config <check|path|print-redacted> [--config path]
  nlwproxy proxy add <name> --base-url URL [--proxy-url URL] [--api-key-env ENV]
  nlwproxy proxy edit <name> [route flags]
  nlwproxy proxy <remove|enable|disable|test> <name> [--config path]
  nlwproxy proxy list [--config path]
  nlwproxy route status [--config path]
  nlwproxy route set-strategy <round_robin|failover> [--config path]
  nlwproxy route set-priority <name> <number> [--config path]
  nlwproxy setup [--opencode-config path] [--dry-run|--rollback] [--state-dir path]
  nlwproxy uninstall [--opencode-config path]
  nlwproxy console [--config path]
  nlwproxy profile <list|create|update|activate|use|delete> [id] [--dir profiles]
  nlwproxy gateway [--config path]
  nlwproxy serve [--config path]
  nlwproxy status [--config path]
  nlwproxy dashboard [--config path]
  nlwproxy version`)
}
