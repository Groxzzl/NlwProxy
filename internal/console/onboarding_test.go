package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProfileEnvironmentNames(t *testing.T) {
	got := ProfileEnvironmentNames("Reffa Unlimited")
	want := ProfileEnvNames{
		ProviderAPIKey: "REFFAUNLIMITED_PROVIDER_API_KEY",
		GatewayAPIKey:  "REFFAUNLIMITED_GATEWAY_API_KEY",
		BaseURL:        "REFFAUNLIMITED_BASE_URL",
		DefaultModel:   "REFFAUNLIMITED_DEFAULT_MODEL",
	}
	if got != want {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestOnboardingRunPersistsProfileSpecificValuesAndProcessEnvironment(t *testing.T) {
	for _, name := range []string{"REFFAUNLIMITED_PROVIDER_API_KEY", "REFFAUNLIMITED_GATEWAY_API_KEY", "REFFAUNLIMITED_BASE_URL", "REFFAUNLIMITED_DEFAULT_MODEL"} {
		t.Setenv(name, "")
	}
	var persisted = map[string]string{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Fatalf("path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-b"},{"id":"model-a"}]}`))
	}))
	defer srv.Close()

	input := strings.Join([]string{
		"Reffa Unlimited",
		srv.URL + "/v1",
		"provider-secret",
		"gateway-secret",
		"",
		"model-b",
	}, "\n") + "\n"
	result, err := (OnboardingWizard{
		In:      strings.NewReader(input),
		Out:     os.Stdout,
		Persist: func(k, v string) error { persisted[k] = v; return nil },
		Client:  srv.Client(),
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ProfileID != "reffaunlimited" || result.Settings.APIKeyEnv != "REFFAUNLIMITED_PROVIDER_API_KEY" || result.Settings.GatewayAPIKeyEnv != "REFFAUNLIMITED_GATEWAY_API_KEY" || result.Settings.BaseURLEnv != "REFFAUNLIMITED_BASE_URL" || result.Settings.DefaultModelEnv != "REFFAUNLIMITED_DEFAULT_MODEL" || result.Settings.DefaultModel != "model-b" {
		t.Fatalf("result=%+v", result)
	}
	want := map[string]string{
		"REFFAUNLIMITED_PROVIDER_API_KEY": "provider-secret",
		"REFFAUNLIMITED_GATEWAY_API_KEY":  "gateway-secret",
		"REFFAUNLIMITED_BASE_URL":         srv.URL + "/v1",
		"REFFAUNLIMITED_DEFAULT_MODEL":    "model-b",
	}
	for name, value := range want {
		if persisted[name] != value || os.Getenv(name) != value {
			t.Fatalf("%s persisted=%q env=%q want=%q", name, persisted[name], os.Getenv(name), value)
		}
	}
}

func TestOnboardingRunRejectsDefaultOutsideDiscoveredModels(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer srv.Close()
	input := "Acme\n" + srv.URL + "/v1\nprovider\ngateway\n\nmissing-model\n"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := (OnboardingWizard{In: strings.NewReader(input), Out: os.Stdout, Persist: func(string, string) error { return nil }, Client: srv.Client()}).Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "discovered models") {
		t.Fatalf("err=%v", err)
	}
}
