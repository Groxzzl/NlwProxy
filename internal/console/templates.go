package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type TemplateFormat string

const (
	FormatGenericYAML  TemplateFormat = "Generic YAML"
	FormatOpenCodeJSON TemplateFormat = "OpenCode JSON"
	FormatPythonOpenAI TemplateFormat = "Python OpenAI SDK"
	FormatNodeOpenAI   TemplateFormat = "Node OpenAI SDK"
	FormatEnv          TemplateFormat = "Environment"
)

func TemplateFormats() []TemplateFormat {
	return []TemplateFormat{FormatGenericYAML, FormatOpenCodeJSON, FormatPythonOpenAI, FormatNodeOpenAI, FormatEnv}
}

type TemplateData struct {
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	Models   []CatalogModel
}

type CatalogModel struct{ ID, Name string }

func RenderConfigTemplate(format TemplateFormat, d TemplateData) (string, error) {
	if d.BaseURL == "" || d.APIKey == "" || d.Model == "" {
		return "", errors.New("base URL, gateway API key, and model are required")
	}
	switch format {
	case FormatGenericYAML:
		provider := d.Provider
		if provider == "" {
			provider = "NLWProxy"
		}
		models := d.Models
		if len(models) == 0 {
			models = []CatalogModel{{ID: d.Model, Name: d.Model}}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "providers:\n  %s:\n    baseUrl: %s\n    apiKey: %s\n    api: openai-completions\n    models:\n", provider, d.BaseURL, d.APIKey)
		for _, model := range models {
			fmt.Fprintf(&b, "      - id: %s\n        name: %s\n", model.ID, model.Name)
		}
		return b.String(), nil
	case FormatOpenCodeJSON:
		root := map[string]any{
			"model": "nlwproxy/" + d.Model,
			"provider": map[string]any{"nlwproxy": map[string]any{
				"npm": "@ai-sdk/openai-compatible", "name": "NLW Proxy",
				"options": map[string]string{"baseURL": d.BaseURL, "apiKey": d.APIKey},
				"models":  map[string]any{d.Model: map[string]string{"name": d.Model}},
			}},
		}
		b, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b) + "\n", nil
	case FormatPythonOpenAI:
		return fmt.Sprintf("from openai import OpenAI\n\nclient = OpenAI(base_url=%q, api_key=%q)\nresponse = client.responses.create(model=%q, input=\"Hello\")\n", d.BaseURL, d.APIKey, d.Model), nil
	case FormatNodeOpenAI:
		return fmt.Sprintf("import OpenAI from \"openai\";\n\nconst client = new OpenAI({ baseURL: %q, apiKey: %q });\nconst response = await client.responses.create({ model: %q, input: \"Hello\" });\n", d.BaseURL, d.APIKey, d.Model), nil
	case FormatEnv:
		return fmt.Sprintf("OPENAI_BASE_URL=%s\nOPENAI_API_KEY=%s\nOPENAI_MODEL=%s\n", d.BaseURL, d.APIKey, d.Model), nil
	default:
		return "", fmt.Errorf("unknown config format %q", format)
	}
}

func NormalizeCatalog(models []CatalogModel) []CatalogModel {
	seen := map[string]bool{}
	out := make([]CatalogModel, 0, len(models))
	for _, m := range models {
		m.ID, m.Name = strings.TrimSpace(m.ID), strings.TrimSpace(m.Name)
		if m.ID == "" || seen[m.ID] || m.ID == "opencode-route" {
			continue
		}
		seen[m.ID] = true
		if m.Name == "" {
			m.Name = m.ID
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
