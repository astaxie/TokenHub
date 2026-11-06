package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCustomProviderModelFieldsSlugFallback(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  map[string]any
		want string
	}{
		{"slug only", map[string]any{"slug": "glm-example"}, "glm-example"},
		{"id takes precedence", map[string]any{"id": "model-id", "slug": "model-slug"}, "model-id"},
		{"name takes precedence", map[string]any{"name": "model-name", "slug": "model-slug"}, "model-name"},
		{"model takes precedence", map[string]any{"model": "model-field", "slug": "model-slug"}, "model-field"},
		{"empty identifiers", map[string]any{"id": " ", "name": "", "model": "", "slug": "glm-example"}, "glm-example"},
		{"non-string slug", map[string]any{"slug": 42}, ""},
		{"empty slug", map[string]any{"slug": " "}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _, _ := customProviderModelFields(tc.raw)
			if got != tc.want {
				t.Fatalf("model ID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAdminCustomProviderCatalogLoadsSlugModels(t *testing.T) {
	app := newTestServer()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Errorf("unexpected discovery path %q", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"models": []map[string]any{
				{"slug": "glm-example", "display_name": "GLM Example"},
				{"slug": "glm-example", "display_name": "Duplicate"},
				{"slug": "", "display_name": "Missing identifier"},
			},
		})
	}))
	defer upstream.Close()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"name":     "Slug Models",
		"type":     ProviderOpenAICompatible,
		"base_url": upstream.URL + "/api/v1",
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected a discovered catalog, got %d: %s", resp.Code, resp.Body)
	}
	var payload struct {
		Data ProviderCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.ModelsCount != 1 || len(payload.Data.Models) != 1 {
		t.Fatalf("expected one valid unique model, got %+v", payload.Data)
	}
	model := payload.Data.Models[0]
	if model.ID != "glm-example" || model.Name != "glm-example" || model.DisplayName != "GLM Example" {
		t.Fatalf("discovered model lost its slug or display name: %+v", model)
	}
}
