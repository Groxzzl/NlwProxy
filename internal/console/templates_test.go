package console

import (
	"context"
	"strings"
	"testing"
)

func TestConfigTemplatesUseGatewayCredentialsAndTransparentModel(t *testing.T) {
	data := TemplateData{BaseURL: "http://127.0.0.1:8787/v1", APIKey: "nlw-local", Model: "vendor/model-v2"}
	for _, format := range TemplateFormats() {
		got, err := RenderConfigTemplate(format, data)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		for _, want := range []string{data.BaseURL, data.APIKey, data.Model} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s missing %q:\n%s", format, want, got)
			}
		}
		if strings.Contains(got, "opencode-route") || strings.Contains(got, "UPSTREAM_SECRET") {
			t.Fatalf("%s contains synthetic model or upstream secret: %s", format, got)
		}
	}
}

func TestOpenCodeTemplateIsValidProviderJSON(t *testing.T) {
	got, err := RenderConfigTemplate(FormatOpenCodeJSON, TemplateData{BaseURL: "http://localhost:8787/v1", APIKey: "key", Model: "gpt-live"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"npm": "@ai-sdk/openai-compatible"`, `"gpt-live"`, `"baseURL"`, `"apiKey"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s:\n%s", want, got)
		}
	}
}

func TestDashboardRendersAvailableModelCatalogAndConfigAction(t *testing.T) {
	got := RenderDashboardV2(DashboardView{AvailableModels: []CatalogModel{{ID: "vendor/model-v2", Name: "Vendor Model V2"}}}, false, 110)
	for _, want := range []string{"AVAILABLE MODELS", "vendor/model-v2", "Vendor Model V2", "[C] Config templates"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
}

func TestControllerDispatchesConfigSelector(t *testing.T) {
	called := false
	ctl := Controller{Handle: func(_ context.Context, action Action) error { called = action == ActionConfig; return nil }}
	quit, err := ctl.Dispatch(context.Background(), 'c')
	if err != nil || quit || !called {
		t.Fatalf("called=%v quit=%v err=%v", called, quit, err)
	}
}
