package console

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type ProfileEnvNames struct {
	ProviderAPIKey string
	GatewayAPIKey  string
	BaseURL        string
	DefaultModel   string
}

type OnboardingSettings struct {
	Provider, BaseURL, ProviderAPIKey, GatewayAPIKey, DefaultModel string
	APIKeyEnv, GatewayAPIKeyEnv, BaseURLEnv, DefaultModelEnv       string
}

type OnboardingResult struct {
	ProfileID string
	Settings  OnboardingSettings
	Models    []string
}

type OnboardingWizard struct {
	In      io.Reader
	Out     io.Writer
	Persist func(string, string) error
	Client  *http.Client
}

var envPartPattern = regexp.MustCompile(`[^A-Za-z0-9]+`)

func ProfileEnvironmentNames(profile string) ProfileEnvNames {
	prefix := strings.ToUpper(envPartPattern.ReplaceAllString(strings.TrimSpace(profile), ""))
	if prefix == "" || prefix[0] >= '0' && prefix[0] <= '9' {
		prefix = "PROFILE" + prefix
	}
	return ProfileEnvNames{
		ProviderAPIKey: prefix + "_PROVIDER_API_KEY",
		GatewayAPIKey:  prefix + "_GATEWAY_API_KEY",
		BaseURL:        prefix + "_BASE_URL",
		DefaultModel:   prefix + "_DEFAULT_MODEL",
	}
}

func ProfileID(profile string) string {
	id := strings.ToLower(envPartPattern.ReplaceAllString(strings.TrimSpace(profile), ""))
	if id == "" || id[0] >= '0' && id[0] <= '9' {
		return "profile" + id
	}
	return id
}

type wizardModel struct {
	step    int
	labels  []string
	values  []string
	models  []string
	done    bool
	aborted bool
}

func newWizardModel() wizardModel {
	return wizardModel{labels: []string{"Profile", "Provider URL", "Provider key", "Gateway key", "Test / models", "Default model"}, values: make([]string, 6)}
}
func (m wizardModel) Init() tea.Cmd { return nil }
func (m wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c", "esc":
			m.aborted = true
			return m, tea.Quit
		case "enter":
			m.step++
			if m.step >= len(m.labels) {
				m.done = true
				return m, tea.Quit
			}
		case "backspace":
			if m.step < len(m.values) && len(m.values[m.step]) > 0 {
				m.values[m.step] = m.values[m.step][:len(m.values[m.step])-1]
			}
		default:
			if m.step < len(m.values) && len(key.Runes) > 0 {
				m.values[m.step] += string(key.Runes)
			}
		}
	}
	return m, nil
}
func (m wizardModel) View() string {
	if m.done || m.aborted {
		return ""
	}
	value := m.values[m.step]
	if m.step == 2 || m.step == 3 {
		value = strings.Repeat("•", len(value))
	}
	return fmt.Sprintf("NLWPROXY ONBOARDING  %d/6\n%s\n> %s\n\nEnter: next  Esc: cancel\n", m.step+1, m.labels[m.step], value)
}

// Run uses Bubble Tea for terminals and the same six-step workflow with injected
// streams in tests and redirected launcher smoke runs.
func (w OnboardingWizard) Run(ctx context.Context) (OnboardingResult, error) {
	if w.In == nil {
		w.In = os.Stdin
	}
	if w.Out == nil {
		w.Out = os.Stdout
	}
	if w.Persist == nil {
		w.Persist = PersistUserEnvironment
	}
	if w.In == os.Stdin && w.Out == os.Stdout {
		final, err := tea.NewProgram(newWizardModel(), tea.WithContext(ctx), tea.WithInput(w.In), tea.WithOutput(w.Out)).Run()
		if err != nil {
			return OnboardingResult{}, err
		}
		model := final.(wizardModel)
		if model.aborted {
			return OnboardingResult{}, errors.New("onboarding cancelled")
		}
		return w.finish(ctx, model.values[0], model.values[1], model.values[2], model.values[3], model.values[5])
	}
	return w.runRedirected(ctx)
}

func (w OnboardingWizard) runRedirected(ctx context.Context) (OnboardingResult, error) {
	r := bufio.NewReader(w.In)
	ask := func(step int, label string) (string, error) {
		fmt.Fprintf(w.Out, "[%d/6] %s: ", step, label)
		line, err := r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	profile, err := ask(1, "Profile")
	if err != nil {
		return OnboardingResult{}, err
	}
	baseURL, err := ask(2, "Provider URL")
	if err != nil {
		return OnboardingResult{}, err
	}
	providerKey, err := ask(3, "Provider key")
	if err != nil {
		return OnboardingResult{}, err
	}
	gatewayKey, err := ask(4, "Gateway key")
	if err != nil {
		return OnboardingResult{}, err
	}
	if _, err = ask(5, "Test and discover models (Enter)"); err != nil {
		return OnboardingResult{}, err
	}
	models, err := DiscoverModels(ctx, w.Client, baseURL, providerKey)
	if err != nil {
		return OnboardingResult{}, fmt.Errorf("provider test: %w", err)
	}
	fmt.Fprintln(w.Out, "Models:", strings.Join(models, ", "))
	defaultModel, err := ask(6, "Default model")
	if err != nil {
		return OnboardingResult{}, err
	}
	return w.finishWithModels(profile, baseURL, providerKey, gatewayKey, defaultModel, models)
}

func (w OnboardingWizard) finish(ctx context.Context, profile, baseURL, providerKey, gatewayKey, defaultModel string) (OnboardingResult, error) {
	models, err := DiscoverModels(ctx, w.Client, baseURL, providerKey)
	if err != nil {
		return OnboardingResult{}, fmt.Errorf("provider test: %w", err)
	}
	return w.finishWithModels(profile, baseURL, providerKey, gatewayKey, defaultModel, models)
}

func (w OnboardingWizard) finishWithModels(profile, baseURL, providerKey, gatewayKey, defaultModel string, models []string) (OnboardingResult, error) {
	if strings.TrimSpace(profile) == "" || strings.TrimSpace(providerKey) == "" || strings.TrimSpace(gatewayKey) == "" {
		return OnboardingResult{}, errors.New("profile, provider key, and gateway key are required")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return OnboardingResult{}, errors.New("provider URL must be credential-free HTTPS")
	}
	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0]
	}
	if !containsModel(models, defaultModel) {
		return OnboardingResult{}, errors.New("default model must be one of the discovered models")
	}
	names := ProfileEnvironmentNames(profile)
	values := map[string]string{names.ProviderAPIKey: providerKey, names.GatewayAPIKey: gatewayKey, names.BaseURL: strings.TrimRight(baseURL, "/"), names.DefaultModel: defaultModel}
	for _, name := range []string{names.ProviderAPIKey, names.GatewayAPIKey, names.BaseURL, names.DefaultModel} {
		if err := w.Persist(name, values[name]); err != nil {
			return OnboardingResult{}, fmt.Errorf("persist %s: %w", name, err)
		}
		if err := os.Setenv(name, values[name]); err != nil {
			return OnboardingResult{}, err
		}
	}
	settings := OnboardingSettings{Provider: profile, BaseURL: strings.TrimRight(baseURL, "/"), ProviderAPIKey: providerKey, GatewayAPIKey: gatewayKey, DefaultModel: defaultModel, APIKeyEnv: names.ProviderAPIKey, GatewayAPIKeyEnv: names.GatewayAPIKey, BaseURLEnv: names.BaseURL, DefaultModelEnv: names.DefaultModel}
	return OnboardingResult{ProfileID: ProfileID(profile), Settings: settings, Models: models}, nil
}

func DiscoverModels(ctx context.Context, client *http.Client, baseURL, apiKey string) ([]string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned %s", resp.Status)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(body.Data))
	for _, item := range body.Data {
		if strings.TrimSpace(item.ID) != "" {
			models = append(models, item.ID)
		}
	}
	sort.Strings(models)
	if len(models) == 0 {
		return nil, errors.New("provider returned no models")
	}
	return models, nil
}

func containsModel(models []string, model string) bool {
	for _, item := range models {
		if item == model {
			return true
		}
	}
	return false
}
