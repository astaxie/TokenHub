package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGatewayModelsAndChatCompletion(t *testing.T) {
	app := newTestServer()

	models := doJSON(t, app, http.MethodGet, "/v1/models", nil, "thk_demo_local")
	if models.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", models.Code, models.Body)
	}
	if !strings.Contains(models.Body, "gpt-4.1-mini") {
		t.Fatalf("model list does not include demo model: %s", models.Body)
	}

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "hello tokenhub"},
		},
	}, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Echo: hello tokenhub") {
		t.Fatalf("unexpected chat body: %s", resp.Body)
	}

	usage := doJSON(t, app, http.MethodGet, "/api/admin/usage/summary", nil, "")
	if usage.Code != http.StatusOK {
		t.Fatalf("usage summary failed: %d %s", usage.Code, usage.Body)
	}
	var summary struct {
		RequestCount int   `json:"request_count"`
		TotalTokens  int64 `json:"total_tokens"`
	}
	if err := json.Unmarshal([]byte(usage.Body), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.RequestCount < 1 {
		t.Fatalf("expected audited requests: %s", usage.Body)
	}
	if summary.TotalTokens < 1 {
		t.Fatalf("expected token usage: %s", usage.Body)
	}

	breakdown := doJSON(t, app, http.MethodGet, "/api/admin/usage/breakdown", nil, "")
	if breakdown.Code != http.StatusOK {
		t.Fatalf("usage breakdown failed: %d %s", breakdown.Code, breakdown.Body)
	}
	if !strings.Contains(breakdown.Body, `"projects"`) || !strings.Contains(breakdown.Body, `"gpt-4.1-mini"`) {
		t.Fatalf("expected project and model breakdown: %s", breakdown.Body)
	}

	timeseries := doJSON(t, app, http.MethodGet, "/api/admin/usage/timeseries", nil, "")
	if timeseries.Code != http.StatusOK {
		t.Fatalf("usage timeseries failed: %d %s", timeseries.Code, timeseries.Body)
	}
	if !strings.Contains(timeseries.Body, `"data"`) || !strings.Contains(timeseries.Body, `"total_tokens"`) {
		t.Fatalf("expected timeseries data: %s", timeseries.Body)
	}
}

func TestGatewayModelsOnlyListPublishedRoutedModels(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "catalog-only-model", Modality: "chat", Status: StatusActive})
	store.AddModel(Model{Name: "disabled-route-model", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:     "disabled-route-model",
		ProviderID:    "prv_mock",
		ProviderModel: "disabled-route-model",
		Status:        StatusDisabled,
	})
	project := store.CreateProject(Project{Name: "Published Model Test", Status: StatusActive})
	if _, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Unrestricted Models"}, "thk_unrestricted_models"); err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, New(store).Handler(), http.MethodGet, "/v1/models", nil, "thk_unrestricted_models")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"id":"gpt-4.1-mini"`) {
		t.Fatalf("expected published demo model: %s", resp.Body)
	}
	for _, hidden := range []string{"catalog-only-model", "disabled-route-model"} {
		if strings.Contains(resp.Body, hidden) {
			t.Fatalf("model %q has no active route and must not be published: %s", hidden, resp.Body)
		}
	}
}

func TestGatewayRejectsTrailingJSONValue(t *testing.T) {
	app := newTestServer()
	body := `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hello"}]}{"extra":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer thk_demo_local")
	resp := httptest.NewRecorder()
	app.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for concatenated JSON values, got %d: %s", resp.Code, resp.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode gateway error: %v", err)
	}
	errorBody, _ := payload["error"].(map[string]any)
	if errorBody["code"] != "invalid_request" {
		t.Fatalf("expected invalid_request, got %#v", payload)
	}
}

func TestGatewayModelsExposeJieKouCompatibleFields(t *testing.T) {
	app := newTestServer()

	resp := doJSON(t, app, http.MethodGet, "/v1/models", nil, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			ID                   string `json:"id"`
			Created              int64  `json:"created"`
			Object               string `json:"object"`
			OwnedBy              string `json:"owned_by"`
			InputTokenPricePerM  int64  `json:"input_token_price_per_m"`
			OutputTokenPricePerM int64  `json:"output_token_price_per_m"`
			Title                string `json:"title"`
			Description          string `json:"description"`
			ContextSize          int64  `json:"context_size"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Object != "list" {
		t.Fatalf("expected list object, got %q", payload.Object)
	}
	var model struct {
		ID                   string `json:"id"`
		Created              int64  `json:"created"`
		Object               string `json:"object"`
		OwnedBy              string `json:"owned_by"`
		InputTokenPricePerM  int64  `json:"input_token_price_per_m"`
		OutputTokenPricePerM int64  `json:"output_token_price_per_m"`
		Title                string `json:"title"`
		Description          string `json:"description"`
		ContextSize          int64  `json:"context_size"`
	}
	for _, item := range payload.Data {
		if item.ID == "gpt-4.1-mini" {
			model = item
			break
		}
	}
	if model.ID == "" {
		t.Fatalf("expected gpt-4.1-mini in model list: %s", resp.Body)
	}
	if model.Created <= 0 || model.Object != "model" || model.OwnedBy != "tokenhub" {
		t.Fatalf("unexpected model identity fields: %+v", model)
	}
	if model.InputTokenPricePerM != 4000 || model.OutputTokenPricePerM != 16000 {
		t.Fatalf("unexpected jiekou-compatible price fields: %+v", model)
	}
	if model.Title != "gpt-4.1-mini" || model.Description == "" || model.ContextSize != 128000 {
		t.Fatalf("unexpected model metadata fields: %+v", model)
	}
}

func TestGatewayModelsExposeCodexCompatibleEnvelope(t *testing.T) {
	resp := doJSON(t, newTestServer(), http.MethodGet, "/v1/models", nil, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	var payload struct {
		Data   []modelListItem `json:"data"`
		Models []any           `json:"models"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) == 0 || payload.Models == nil || len(payload.Models) != 0 {
		t.Fatalf("expected standard model data and an empty Codex-compatible models list, got %+v", payload)
	}
}

func TestGatewayRetrieveModelExposeJieKouCompatibleFields(t *testing.T) {
	app := newTestServer()

	resp := doJSON(t, app, http.MethodGet, "/v1/models/gpt-4.1-mini", nil, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	var model struct {
		ID                   string `json:"id"`
		Created              int64  `json:"created"`
		Object               string `json:"object"`
		OwnedBy              string `json:"owned_by"`
		InputTokenPricePerM  int64  `json:"input_token_price_per_m"`
		OutputTokenPricePerM int64  `json:"output_token_price_per_m"`
		Title                string `json:"title"`
		Description          string `json:"description"`
		ContextSize          int64  `json:"context_size"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &model); err != nil {
		t.Fatal(err)
	}
	if model.ID != "gpt-4.1-mini" || model.Object != "model" || model.OwnedBy != "tokenhub" {
		t.Fatalf("unexpected model identity fields: %+v", model)
	}
	if model.Created <= 0 || model.InputTokenPricePerM != 4000 || model.OutputTokenPricePerM != 16000 {
		t.Fatalf("unexpected jiekou-compatible fields: %+v", model)
	}
	if model.Title != "gpt-4.1-mini" || model.Description == "" || model.ContextSize != 128000 {
		t.Fatalf("unexpected model metadata fields: %+v", model)
	}

	missing := doJSON(t, app, http.MethodGet, "/v1/models/not-a-visible-model", nil, "thk_demo_local")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing model, got %d: %s", missing.Code, missing.Body)
	}
	if !strings.Contains(missing.Body, "model_not_found") {
		t.Fatalf("expected model_not_found error, got %s", missing.Body)
	}
}

func TestGatewayRetrieveModelSupportsEscapedModelIDs(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_path_model", Name: "Path Model Project", Status: StatusActive})
	_, _, err := store.CreateAPIKey(project.ID, APIKey{
		ID:      "key_path_model",
		Name:    "Path Model Key",
		Allowed: []string{"provider/model"},
		Status:  StatusActive,
	}, "thk_path_model")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "provider/model", Modality: "chat", ContextWindow: 32000, Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:     "provider/model",
		ProviderID:    "prv_path_model",
		ProviderModel: "provider/model",
		Status:        StatusActive,
	})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodGet, "/v1/models/provider%2Fmodel", nil, "thk_path_model")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"id":"provider/model"`) || !strings.Contains(resp.Body, `"context_size":32000`) {
		t.Fatalf("expected escaped path model lookup to resolve provider/model: %s", resp.Body)
	}
}

func TestGatewayStreamingChatCompletion(t *testing.T) {
	app := newTestServer()
	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":  "gpt-4.1-mini",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "stream this"},
		},
	}, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "data:") || !strings.Contains(resp.Body, "[DONE]") {
		t.Fatalf("expected SSE stream, got: %s", resp.Body)
	}
}

func TestAdminPlaygroundChatUsesRoutesWithoutProjectBilling(t *testing.T) {
	app := newTestServer()
	before := doJSON(t, app, http.MethodGet, "/api/admin/usage/summary", nil, "")
	if before.Code != http.StatusOK {
		t.Fatalf("usage summary before failed: %d %s", before.Code, before.Body)
	}
	var beforeSummary struct {
		RequestCount int `json:"request_count"`
	}
	if err := json.Unmarshal([]byte(before.Body), &beforeSummary); err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, app, http.MethodPost, "/api/admin/playground/chat", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "playground smoke"},
		},
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Echo: playground smoke") {
		t.Fatalf("unexpected playground body: %s", resp.Body)
	}
	if !strings.Contains(resp.Body, `"provider_name":"Mock Provider"`) || !strings.Contains(resp.Body, `"provider_model":"mock-chat"`) {
		t.Fatalf("expected route summary without provider secrets: %s", resp.Body)
	}
	if strings.Contains(resp.Body, "thk_demo_local") {
		t.Fatalf("playground response leaked a key: %s", resp.Body)
	}

	after := doJSON(t, app, http.MethodGet, "/api/admin/usage/summary", nil, "")
	if after.Code != http.StatusOK {
		t.Fatalf("usage summary after failed: %d %s", after.Code, after.Body)
	}
	var afterSummary struct {
		RequestCount int `json:"request_count"`
	}
	if err := json.Unmarshal([]byte(after.Body), &afterSummary); err != nil {
		t.Fatal(err)
	}
	if afterSummary.RequestCount != beforeSummary.RequestCount {
		t.Fatalf("playground should not create project usage records: before=%d after=%d", beforeSummary.RequestCount, afterSummary.RequestCount)
	}

	var playgroundPayload struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &playgroundPayload); err != nil {
		t.Fatal(err)
	}
	if playgroundPayload.RequestID == "" {
		t.Fatalf("playground response should include request_id: %s", resp.Body)
	}
	logs := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests", nil, "")
	if logs.Code != http.StatusOK {
		t.Fatalf("request logs failed: %d %s", logs.Code, logs.Body)
	}
	if !strings.Contains(logs.Body, playgroundPayload.RequestID) || !strings.Contains(logs.Body, "admin_playground") {
		t.Fatalf("playground request should be visible in request logs: %s", logs.Body)
	}
	detail := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests/"+playgroundPayload.RequestID, nil, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("playground request detail failed: %d %s", detail.Code, detail.Body)
	}
	if !strings.Contains(detail.Body, `"attempts"`) || !strings.Contains(detail.Body, "playground smoke") {
		t.Fatalf("playground request detail should include attempts and payload: %s", detail.Body)
	}
}

func TestAdminPlaygroundChatUsesResponsesForCodexSubscription(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_playground_codex",
		Name:    "Playground Codex",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_playground_codex",
		ProviderID:   provider.ID,
		Name:         "Playground Codex Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-playground-codex"),
		Credentials: &ProviderResourceCredentials{
			AccessToken: "access_playground_codex",
			AccountID:   "account_playground_codex",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-playground-codex", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:                 "route_playground_codex",
		ModelName:          "gpt-playground-codex",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "gpt-playground-codex",
		Status:             StatusActive,
	})

	server := New(store)
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("expected Codex Responses endpoint, got %s", req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer access_playground_codex" || req.Header.Get("ChatGPT-Account-ID") != "account_playground_codex" {
			t.Fatalf("expected OAuth account credentials, got %#v", req.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "gpt-playground-codex" || payload["instructions"] != "Be concise." || payload["stream"] != true {
			t.Fatalf("unexpected Codex playground payload: %#v", payload)
		}
		if _, ok := payload["max_output_tokens"]; ok {
			t.Fatalf("Codex playground request must not send max_output_tokens: %#v", payload)
		}
		if _, ok := payload["temperature"]; ok {
			t.Fatalf("Codex playground request must not send temperature: %#v", payload)
		}
		reasoning, _ := payload["reasoning"].(map[string]any)
		if reasoning["effort"] != "high" {
			t.Fatalf("expected playground reasoning effort, got %#v", payload["reasoning"])
		}
		input, _ := payload["input"].([]any)
		if len(input) != 3 {
			t.Fatalf("expected user, assistant, and user history in Responses input, got %#v", payload["input"])
		}
		first, _ := input[0].(map[string]any)
		second, _ := input[1].(map[string]any)
		third, _ := input[2].(map[string]any)
		if first["role"] != "user" || second["role"] != "assistant" || third["role"] != "user" {
			t.Fatalf("unexpected Responses roles: %#v", input)
		}
		firstContent, _ := first["content"].([]any)
		secondContent, _ := second["content"].([]any)
		firstPart, _ := firstContent[0].(map[string]any)
		secondPart, _ := secondContent[0].(map[string]any)
		if firstPart["type"] != "input_text" || firstPart["text"] != "First question" || secondPart["type"] != "output_text" || secondPart["text"] != "First answer" {
			t.Fatalf("unexpected Responses content: %#v", input)
		}
		stream := strings.Join([]string{
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"Codex playground works."}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_playground","status":"completed","model":"gpt-playground-codex","output":[],"usage":{"input_tokens":5,"output_tokens":4,"total_tokens":9}}}`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
			Request:    req,
		}, nil
	})}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/playground/chat", map[string]any{
		"model": "gpt-playground-codex",
		"messages": []map[string]any{
			{"role": "system", "content": "Be concise."},
			{"role": "user", "content": "First question"},
			{"role": "assistant", "content": "First answer"},
			{"role": "user", "content": "Second question"},
		},
		"max_tokens":       321,
		"temperature":      0.2,
		"reasoning_effort": "high",
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected Codex playground request to succeed, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Codex playground works.") || !strings.Contains(resp.Body, `"prompt_tokens":5`) || !strings.Contains(resp.Body, `"completion_tokens":4`) {
		t.Fatalf("unexpected Codex playground response: %s", resp.Body)
	}
}

func TestGatewayEmbeddings(t *testing.T) {
	app := newTestServer()
	resp := doJSON(t, app, http.MethodPost, "/v1/embeddings", map[string]any{
		"model": "text-embedding-3-small",
		"input": "enterprise ai gateway",
	}, "thk_demo_local")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"embedding"`) {
		t.Fatalf("expected embedding response: %s", resp.Body)
	}
}

func TestBootstrapSeedsStandardModelCatalog(t *testing.T) {
	t.Setenv("TOKENHUB_MODEL_CATALOG_FILE", "../../../data/model-catalog.yaml")
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	project, ok := store.GetProject(defaultProjectID)
	if !ok {
		t.Fatalf("expected default project %s", defaultProjectID)
	}
	if project.Name != "Default Project Space" || project.Status != StatusActive {
		t.Fatalf("unexpected default project: %+v", project)
	}
	if project.OwnerUserID != "usr_admin" || project.TeamID != "team_platform" || project.CostCenter != "AI-PLATFORM" {
		t.Fatalf("default project should have enterprise ownership fields: %+v", project)
	}
	models := store.ListModels()
	if len(models) < 160 {
		t.Fatalf("expected standard model catalog, got %d models", len(models))
	}
	byName := map[string]Model{}
	for _, model := range models {
		byName[strings.ToLower(model.Name)] = model
	}
	for name, category := range map[string]string{
		"gpt-5.5":                            "openai",
		"kimi-k3":                            "kimi",
		"kimi-k3-256k":                       "kimi",
		"zai-org/glm-5.2":                    "glm",
		"moonshotai/kimi-k2.7-code":          "kimi",
		"minimax/minimax-m3":                 "minimax",
		"baidu/ernie-4.5-vl-424b-a47b":       "ernie",
		"qwen/qwen3-235b-a22b-instruct-2507": "qwen",
		"grok-4-fast-reasoning":              "grok",
	} {
		model, ok := byName[name]
		if !ok {
			t.Fatalf("expected model %s in catalog", name)
		}
		if model.Category != category {
			t.Fatalf("expected %s category %s, got %s", name, category, model.Category)
		}
	}
	if byName["zai-org/glm-5.2"].Metadata["title"] != "GLM 5.2" {
		t.Fatalf("expected GLM display title metadata, got %+v", byName["zai-org/glm-5.2"].Metadata)
	}
	if byName["gpt-5.5"].InputPriceUSDPer1M != 47.5 || byName["gpt-5.5"].OutputPriceUSDPer1M != 285 {
		t.Fatalf("expected gpt-5.5 jiekou pricing, got input=%v output=%v", byName["gpt-5.5"].InputPriceUSDPer1M, byName["gpt-5.5"].OutputPriceUSDPer1M)
	}
	if !slices.Contains(byName["gpt-5.5"].InputModalities, "image") {
		t.Fatalf("expected gpt-5.5 image input modality, got %+v", byName["gpt-5.5"].InputModalities)
	}
	if k3 := byName["kimi-k3"]; k3.ContextWindow != 1048576 || k3.InputPriceUSDPer1M != 3 || k3.CacheReadPriceUSDPer1M != 0.3 || k3.OutputPriceUSDPer1M != 15 {
		t.Fatalf("unexpected Kimi K3 limits or pricing: %+v", k3)
	}
	if k3 := byName["kimi-k3"]; !slices.Contains(k3.InputModalities, "image") || !slices.Contains(k3.InputModalities, "video") {
		t.Fatalf("expected Kimi K3 visual input modalities, got %+v", k3.InputModalities)
	}
	if k3 := byName["kimi-k3"]; slices.Contains(k3.SupportedParameters, "temperature") || !slices.Contains(k3.SupportedParameters, "reasoning") {
		t.Fatalf("unexpected Kimi K3 parameters: %+v", k3.SupportedParameters)
	}
	if k3256 := byName["kimi-k3-256k"]; k3256.ContextWindow != 262144 ||
		!slices.Contains(k3256.InputModalities, "image") ||
		slices.Contains(k3256.InputModalities, "video") ||
		!slices.Contains(k3256.SupportedParameters, "reasoning") {
		t.Fatalf("unexpected Kimi K3 256K metadata: %+v", k3256)
	}
	for name, expected := range map[string]struct {
		input, cacheRead, output float64
	}{
		"moonshotai/kimi-k2.7-code": {0.95, 0.19, 4},
		"moonshotai/kimi-k2.6":      {0.95, 0.16, 4},
		"moonshotai/kimi-k2.5":      {0.6, 0.1, 3},
	} {
		model := byName[name]
		if model.InputPriceUSDPer1M != expected.input || model.CacheReadPriceUSDPer1M != expected.cacheRead || model.OutputPriceUSDPer1M != expected.output {
			t.Fatalf("unexpected %s pricing: %+v", name, model)
		}
	}
	if byName["gpt-image-2"].Modality != "image" {
		t.Fatalf("expected gpt-image-2 image modality, got %s", byName["gpt-image-2"].Modality)
	}
	if byName[codexImageModelName].Modality != "image" ||
		byName[codexImageModelName].Metadata["execution_type"] != "codex_subscription_image_generation" ||
		byName[codexImageModelName].InputPriceUSDPer1M != 0 ||
		byName[codexImageModelName].OutputPriceUSDPer1M != 0 {
		t.Fatalf("expected subscription-backed Codex image model, got %+v", byName[codexImageModelName])
	}
	if byName["gemini-3-pro-image"].Modality != "image" {
		t.Fatalf("expected gemini-3-pro-image image modality, got %s", byName["gemini-3-pro-image"].Modality)
	}
}

func TestDefaultModelCatalogLoadsYAMLFile(t *testing.T) {
	catalogPath := filepath.Join(t.TempDir(), "model-catalog.yaml")
	content := []byte(`
version: 1
models:
  - name: "test-chat-128k"
    category: "custom"
  - name: "test-embedding"
    category: "custom"
    modality: "embedding"
    embedding_price_usd_per_1m: 0.01
`)
	if err := os.WriteFile(catalogPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	models, err := defaultModelCatalog(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "test-chat-128k" || models[0].ContextWindow != 128000 {
		t.Fatalf("unexpected chat model: %+v", models[0])
	}
	if models[1].Modality != "embedding" || models[1].EmbeddingPriceUSDPer1M != 0.01 {
		t.Fatalf("unexpected embedding model: %+v", models[1])
	}
}

func TestAdminRestoreDefaultModelCatalog(t *testing.T) {
	catalogPath := filepath.Join(t.TempDir(), "model-catalog.yaml")
	content := []byte(`
version: 1
models:
  - name: factory-chat
    category: openai
    family: factory
    modality: chat
    context_window: 128000
    input_price_usd_per_1m: 1.5
    output_price_usd_per_1m: 6
  - name: factory-embedding
    category: openai
    family: factory
    modality: embedding
    embedding_price_usd_per_1m: 0.02
`)
	if err := os.WriteFile(catalogPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewMemoryStore()
	store.AddModel(Model{Name: "factory-chat", Family: "customized", Modality: "chat", ContextWindow: 1000, Status: StatusDisabled})
	store.AddModel(Model{Name: "custom-only", Family: "custom", Modality: "chat", Status: StatusActive})
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", ModelCatalogFile: catalogPath}).Handler()

	deleteResp := doJSON(t, app, http.MethodDelete, "/api/admin/models/factory-chat", nil, "")
	if deleteResp.Code != http.StatusNoContent {
		t.Fatalf("expected delete to succeed, got %d: %s", deleteResp.Code, deleteResp.Body)
	}

	restore := doJSON(t, app, http.MethodPost, "/api/admin/models/restore-defaults", map[string]any{}, "")
	if restore.Code != http.StatusOK {
		t.Fatalf("expected restore to succeed, got %d: %s", restore.Code, restore.Body)
	}
	if !strings.Contains(restore.Body, `"restored":2`) {
		t.Fatalf("expected restore count, got %s", restore.Body)
	}

	byName := map[string]Model{}
	for _, model := range store.ListModels() {
		byName[model.Name] = model
	}
	if byName["factory-chat"].Family != "factory" || byName["factory-chat"].ContextWindow != 128000 || byName["factory-chat"].Status != StatusActive {
		t.Fatalf("factory-chat was not restored from catalog: %+v", byName["factory-chat"])
	}
	if byName["factory-embedding"].EmbeddingPriceUSDPer1M != 0.02 {
		t.Fatalf("factory embedding was not restored: %+v", byName["factory-embedding"])
	}
	if _, ok := byName["custom-only"]; !ok {
		t.Fatalf("custom model should be preserved")
	}
}

func TestAdminModelItemSupportsEscapedSlashNames(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{Name: "deepseek/deepseek-ocr-2", Family: "deepseek", Modality: "ocr", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:            "route_deepseek_ocr",
		ModelName:     "deepseek/deepseek-ocr-2",
		ProviderID:    "prv_deepseek",
		ProviderModel: "deepseek-ocr-2",
		Status:        StatusActive,
	})
	app := New(store).Handler()

	patch := doJSON(t, app, http.MethodPatch, "/api/admin/models/deepseek%2Fdeepseek-ocr-2", map[string]any{
		"family":   "deepseek-updated",
		"modality": "ocr",
		"status":   StatusActive,
	}, "")
	if patch.Code != http.StatusOK {
		t.Fatalf("expected escaped slash model patch to succeed, got %d: %s", patch.Code, patch.Body)
	}
	updated, ok := modelByNameForTest(store.ListModels(), "deepseek/deepseek-ocr-2")
	if !ok || updated.Family != "deepseek-updated" {
		t.Fatalf("expected escaped slash model to be patched, got ok=%v model=%+v", ok, updated)
	}

	deleteResp := doJSON(t, app, http.MethodDelete, "/api/admin/models/deepseek%2Fdeepseek-ocr-2", nil, "")
	if deleteResp.Code != http.StatusNoContent {
		t.Fatalf("expected escaped slash model delete to succeed, got %d: %s", deleteResp.Code, deleteResp.Body)
	}
	if _, ok := modelByNameForTest(store.ListModels(), "deepseek/deepseek-ocr-2"); ok {
		t.Fatalf("expected escaped slash model to be deleted")
	}
	if len(store.ListRoutes()) != 0 {
		t.Fatalf("expected model routes to be deleted with model, got %+v", store.ListRoutes())
	}
}

func modelByNameForTest(models []Model, name string) (Model, bool) {
	for _, model := range models {
		if model.Name == name {
			return model, true
		}
	}
	return Model{}, false
}

func TestAdminCreatesAPIKeyUnderDefaultProject(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+defaultProjectID+"/keys", map[string]any{
		"name":           "Default Project Key",
		"group":          "default",
		"allowed_models": []string{"gpt-4.1-mini"},
		"limits": map[string]any{
			"daily_requests":   1000,
			"monthly_requests": 30000,
			"daily_tokens":     1000000,
			"monthly_tokens":   20000000,
			"daily_cost_usd":   100,
			"monthly_cost_usd": 2000,
			"max_concurrency":  20,
		},
	}, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"project_id":"`+defaultProjectID+`"`) || !strings.Contains(resp.Body, `"api_key"`) {
		t.Fatalf("expected issued key under default project: %s", resp.Body)
	}

	keys := store.ListProjectKeys(defaultProjectID)
	if len(keys) != 1 {
		t.Fatalf("expected one default project key, got %d", len(keys))
	}
	if keys[0].ProjectID != defaultProjectID {
		t.Fatalf("expected key project %s, got %s", defaultProjectID, keys[0].ProjectID)
	}
}

func TestUserCreatesPersonalAPIKeyWithoutProjectMembership(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateAdminUser(AdminUser{
		Username: "personal-key-user",
		Name:     "Personal Key User",
		Email:    "personal-key-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": user.Username,
		"password": "user123456",
	}, "")
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &session); err != nil {
		t.Fatal(err)
	}

	created := doJSON(t, app, http.MethodPost, "/api/admin/api-keys", map[string]any{
		"name": "Personal Key",
	}, session.Token)
	if created.Code != http.StatusCreated {
		t.Fatalf("ordinary user should create a personal key without project membership, got %d: %s", created.Code, created.Body)
	}
	if !strings.Contains(created.Body, `"project_id":"`+defaultProjectID+`"`) || !strings.Contains(created.Body, `"api_key"`) {
		t.Fatalf("personal key should fall back to the default project: %s", created.Body)
	}
	keys := store.ListProjectKeys(defaultProjectID)
	if len(keys) != 1 || keys[0].Metadata["created_by"] != user.ID {
		t.Fatalf("personal key should remain attributable to its creator: %+v", keys)
	}

	assignedProject := store.CreateProject(Project{Name: "Assigned Project", Status: StatusActive})
	store.CreateResource("project-members", AdminResource{
		Name:   "Personal Key User Membership",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": assignedProject.ID,
			"user_id":    user.ID,
			"role":       "developer",
		},
	})
	assigned := doJSON(t, app, http.MethodPost, "/api/admin/api-keys", map[string]any{
		"name": "Assigned Project Key",
	}, session.Token)
	if assigned.Code != http.StatusCreated || !strings.Contains(assigned.Body, `"project_id":"`+assignedProject.ID+`"`) {
		t.Fatalf("personal key should prefer an assigned project, got %d: %s", assigned.Code, assigned.Body)
	}
}

func TestUserCanReadRoutedAdminModels(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{
		Username: "model.viewer",
		Email:    "model.viewer@tokenhub.local",
		Role:     "user",
		TeamID:   "team_platform",
		Status:   StatusActive,
	}, "viewer123456"); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddModel(Model{Name: "text-embedding-3-small", Modality: "embedding", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: "provider_mock", ProviderModel: "mock-chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "text-embedding-3-small", ProviderID: "provider_mock", ProviderModel: "mock-embedding", Status: StatusDisabled})
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "model.viewer@tokenhub.local",
		"password": "viewer123456",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d: %s", login.Code, login.Body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}

	models := doJSON(t, app, http.MethodGet, "/api/admin/models", nil, payload.Token)
	if models.Code != http.StatusOK {
		t.Fatalf("expected user to read accessible models, got %d: %s", models.Code, models.Body)
	}
	if !strings.Contains(models.Body, `"name":"gpt-4.1-mini"`) || strings.Contains(models.Body, `"name":"text-embedding-3-small"`) {
		t.Fatalf("expected only active routed models: %s", models.Body)
	}
	overview := doJSON(t, app, http.MethodGet, "/api/admin/overview", nil, payload.Token)
	if overview.Code != http.StatusOK || !strings.Contains(overview.Body, `"name":"gpt-4.1-mini"`) {
		t.Fatalf("expected overview to include accessible models, got %d: %s", overview.Code, overview.Body)
	}
	create := doJSON(t, app, http.MethodPost, "/api/admin/models", map[string]any{
		"name":   "viewer-created-model",
		"status": StatusActive,
	}, payload.Token)
	if create.Code != http.StatusForbidden {
		t.Fatalf("expected user model create to be forbidden, got %d: %s", create.Code, create.Body)
	}
}

func TestAdminCannotDeleteOwnAccount(t *testing.T) {
	store := NewMemoryStore()
	actor, err := store.CreateAdminUser(AdminUser{
		Username: "platform.admin",
		Email:    "platform.admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456")
	if err != nil {
		t.Fatal(err)
	}
	victim, err := store.CreateAdminUser(AdminUser{
		Username: "ordinary.user",
		Email:    "ordinary.user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "platform.admin@tokenhub.local",
		"password": "admin123456",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d: %s", login.Code, login.Body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}

	self := doJSON(t, app, http.MethodDelete, "/api/admin/users/"+actor.ID, nil, payload.Token)
	if self.Code != http.StatusBadRequest {
		t.Fatalf("expected self deletion to be rejected with 400, got %d: %s", self.Code, self.Body)
	}
	if !strings.Contains(self.Body, "cannot_delete_self") {
		t.Fatalf("expected cannot_delete_self error, got %s", self.Body)
	}
	stillExists := false
	for _, user := range store.ListAdminUsers() {
		if user.ID == actor.ID {
			stillExists = true
			break
		}
	}
	if !stillExists {
		t.Fatalf("expected actor account to survive self deletion attempt")
	}

	other := doJSON(t, app, http.MethodDelete, "/api/admin/users/"+victim.ID, nil, payload.Token)
	if other.Code != http.StatusNoContent {
		t.Fatalf("expected deleting another user to succeed, got %d: %s", other.Code, other.Body)
	}
}

func TestBootstrapBaseDataSeedsGovernanceResources(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}

	policies := store.ListResources("security-policies")
	var found AdminResource
	for _, policy := range policies {
		if policy.ID == "sec_ip_allowlist" {
			found = policy
			break
		}
	}
	if found.ID == "" {
		t.Fatalf("expected seeded security policy, got %+v", policies)
	}
	if found.Name != "Production IP Allowlist Policy" || found.Status != StatusActive {
		t.Fatalf("unexpected security policy metadata: %+v", found)
	}
	if stringField(found.Fields, "error_passthrough") != "sanitized" || !strings.Contains(stringField(found.Fields, "ip_allowlist"), "127.0.0.1/32") {
		t.Fatalf("unexpected security policy fields: %+v", found.Fields)
	}

	settings := store.ListResources("settings")
	if len(settings) != 1 || settings[0].ID != "cfg_gateway" {
		t.Fatalf("expected gateway system setting, got %+v", settings)
	}
	if stringField(settings[0].Fields, "public_base_url") == "" || stringField(settings[0].Fields, "audit_retention") == "" {
		t.Fatalf("expected configurable system setting fields, got %+v", settings[0].Fields)
	}

	roles := store.ListResources("role-configs")
	if len(roles) != 3 {
		t.Fatalf("expected three role configs, got %+v", roles)
	}
	roleKeys := map[string]bool{}
	for _, role := range roles {
		roleKeys[stringField(role.Fields, "role_key")] = true
		if role.Status != StatusActive || stringField(role.Fields, "display_name") == "" {
			t.Fatalf("unexpected role config: %+v", role)
		}
	}
	for _, key := range []string{"user", "team_leader", "admin"} {
		if !roleKeys[key] {
			t.Fatalf("expected seeded role key %s, got %+v", key, roleKeys)
		}
	}

	identityProviders := store.ListResources("identity-providers")
	if len(identityProviders) != 1 || identityProviders[0].ID != "idp_oidc_template" {
		t.Fatalf("expected default identity provider template, got %+v", identityProviders)
	}
	if stringField(identityProviders[0].Fields, "provider_type") != "oidc" || stringField(identityProviders[0].Fields, "client_id") == "" {
		t.Fatalf("unexpected identity provider fields: %+v", identityProviders[0].Fields)
	}
}

func TestAdminImportsUsersFromExistingSystemCSV(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	messages := configureTestSMTPChannel(t, store)
	app := New(store).Handler()

	content := "username,name,email,role,team_id,status\nimported_user,导入用户,imported@example.com,user,team_platform,active\n"
	resp := doJSON(t, app, http.MethodPost, "/api/admin/users/import", map[string]any{
		"source":  "manual_csv",
		"format":  "csv",
		"content": content,
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected import 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"created":1`) || !strings.Contains(resp.Body, `"updated":0`) {
		t.Fatalf("expected one created user: %s", resp.Body)
	}
	assertPasswordResetEmail(t, messages, "imported@example.com")

	update := "username,name,email,role,team_id,status\nimported_user,导入用户已更新,imported@example.com,team_leader,team_platform,active\n"
	updated := doJSON(t, app, http.MethodPost, "/api/admin/users/import", map[string]any{
		"source":  "manual_csv",
		"format":  "csv",
		"content": update,
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("expected import update 200, got %d: %s", updated.Code, updated.Body)
	}
	if !strings.Contains(updated.Body, `"created":0`) || !strings.Contains(updated.Body, `"updated":1`) {
		t.Fatalf("expected one updated user: %s", updated.Body)
	}
	assertPasswordResetEmail(t, messages, "imported@example.com")
	users := store.ListAdminUsers()
	var found AdminUser
	for _, user := range users {
		if user.Email == "imported@example.com" {
			found = user
			break
		}
	}
	if found.ID == "" || found.Name != "导入用户已更新" || found.Role != "team_leader" {
		t.Fatalf("expected imported user update, got %+v", found)
	}
}

func TestBootstrapUsesConfiguredAdminPassword(t *testing.T) {
	store := NewMemoryStore()
	config := ConfigFromEnv()
	config.BootstrapAdminPassword = "configured-bootstrap-password"
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.AuthenticateAdminUser("admin", config.BootstrapAdminPassword, time.Hour); err != nil {
		t.Fatalf("expected configured bootstrap password to authenticate: %v", err)
	}
	if _, _, err := store.AuthenticateAdminUser("admin", "admin123456", time.Hour); AsHTTPError(err).Code != "invalid_credentials" {
		t.Fatalf("expected hard-coded default password to be rejected, got %v", err)
	}
}

func TestAdminImportsUsersFromHeaderlessCSV(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	messages := configureTestSMTPChannel(t, store)
	app := New(store).Handler()

	content := "xiemengjun,谢孟军,xiemengjun@e-lead.cn,admin,team_UK8jwEcIIoFmNVmJ,active\n" +
		"lisk,李世康,lisk@e-lead.cn,admin,team_UK8jwEcIIoFmNVmJ,active\n"
	resp := doJSON(t, app, http.MethodPost, "/api/admin/users/import", map[string]any{
		"source":  "manual_csv",
		"format":  "csv",
		"content": content,
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected import 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"created":2`) || !strings.Contains(resp.Body, `"skipped":0`) {
		t.Fatalf("expected two created users: %s", resp.Body)
	}
	assertPasswordResetEmail(t, messages, "xiemengjun@e-lead.cn")
	assertPasswordResetEmail(t, messages, "lisk@e-lead.cn")
}

func TestGatewayRejectsUnauthorizedModel(t *testing.T) {
	app := newTestServer()
	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "not-allowed",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, "thk_demo_local")
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "model_not_allowed") {
		t.Fatalf("expected model_not_allowed: %s", resp.Body)
	}
}

func TestGatewayQuotaExceeded(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Limited"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "limited",
		Allowed: []string{"gpt-4.1-mini"},
		Limits:  QuotaLimits{DailyRequests: 1, MonthlyRequests: 1, MaxConcurrency: 1},
		Status:  StatusActive,
	}, "thk_limited")
	if err != nil {
		t.Fatal(err)
	}
	mock := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: mock.ID, ProviderModel: "mock-chat", Status: StatusActive})
	app := New(store).Handler()

	for i := 0; i < 2; i++ {
		resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "gpt-4.1-mini",
			"messages": []map[string]any{
				{"role": "user", "content": "hello"},
			},
		}, secret)
		if i == 0 && resp.Code != http.StatusOK {
			t.Fatalf("first request expected 200, got %d: %s", resp.Code, resp.Body)
		}
		if i == 1 && resp.Code != http.StatusTooManyRequests {
			t.Fatalf("second request expected 429, got %d: %s", resp.Code, resp.Body)
		}
	}
}

func TestQuotaPolicyAppliesAtRuntime(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Policy Limited", TeamID: "team_policy"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "policy-key",
		Allowed: []string{"gpt-4.1-mini"},
		Limits:  QuotaLimits{DailyRequests: 100},
		Status:  StatusActive,
	}, "thk_policy_limited")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("quota-policies", AdminResource{
		Name:   "Project hard cap",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":          "project",
			"scope_id":       project.ID,
			"daily_requests": 1,
		},
	})
	mock := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: mock.ID, ProviderModel: "mock-chat", Status: StatusActive})
	app := New(store).Handler()

	for i := 0; i < 2; i++ {
		resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "gpt-4.1-mini",
			"messages": []map[string]any{
				{"role": "user", "content": "quota policy"},
			},
		}, secret)
		if i == 0 && resp.Code != http.StatusOK {
			t.Fatalf("first request expected 200, got %d: %s", resp.Code, resp.Body)
		}
		if i == 1 && resp.Code != http.StatusTooManyRequests {
			t.Fatalf("second request expected 429 from quota policy, got %d: %s", resp.Code, resp.Body)
		}
	}
}

func TestBudgetExceededBlocksRuntimeCalls(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Budget Limited", CostCenter: "CC-BLOCK"})
	apiKey, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "budget-key",
		Allowed: []string{"gpt-4.1-mini"},
		Limits:  QuotaLimits{DailyRequests: 100},
		Status:  StatusActive,
	}, "thk_budget_limited")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	period := now.Format("2006-01")
	store.CreateResource("budgets", AdminResource{
		Name:   "Blocking budget",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":       "cost_center",
			"scope_id":    "CC-BLOCK",
			"period_ref":  period,
			"amount_usd":  1,
			"enforcement": "block",
		},
	})
	if err := store.db.Create(&UsageRecord{
		ID:          NewID("usage"),
		RequestID:   NewID("req"),
		ProjectID:   project.ID,
		APIKeyID:    apiKey.ID,
		ModelName:   "gpt-4.1-mini",
		InputTokens: 10,
		TotalTokens: 10,
		CostUSD:     1,
		CreatedAt:   now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	mock := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: mock.ID, ProviderModel: "mock-chat", Status: StatusActive})
	app := New(store).Handler()

	blocked := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "budget should block"},
		},
	}, secret)
	if blocked.Code != http.StatusTooManyRequests || !strings.Contains(blocked.Body, "budget_exceeded") {
		t.Fatalf("expected budget_exceeded, got %d: %s", blocked.Code, blocked.Body)
	}
	budgets := store.ListResources("budgets")
	budgets[0].Fields["enforcement"] = "warn"
	if _, err := store.UpdateResource("budgets", budgets[0].ID, budgets[0]); err != nil {
		t.Fatal(err)
	}
	allowed := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "budget warn only"},
		},
	}, secret)
	if allowed.Code != http.StatusOK {
		t.Fatalf("warn-only budget should allow runtime call, got %d: %s", allowed.Code, allowed.Body)
	}
}

func TestRuntimeBudgetUsesActualUsageInsteadOfCachedUsedField(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Fresh Budget", CostCenter: "CC-FRESH"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "fresh-budget-key",
		Allowed: []string{"gpt-4.1-mini"},
		Limits:  QuotaLimits{DailyRequests: 100},
		Status:  StatusActive,
	}, "thk_fresh_budget")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("budgets", AdminResource{
		Name:   "Stale report cache",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":       "cost_center",
			"scope_id":    "CC-FRESH",
			"period_ref":  time.Now().UTC().Format("2006-01"),
			"amount_usd":  1,
			"used_usd":    99,
			"enforcement": "block",
		},
	})
	mock := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: mock.ID, ProviderModel: "mock-chat", Status: StatusActive})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "budget should use actual usage"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("stale used_usd should not block runtime call, got %d: %s", resp.Code, resp.Body)
	}
}

func TestAPIKeyIPAllowlistAndRotation(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Key Ops"})
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:        "restricted",
		Group:       "dedicated",
		Allowed:     []string{"gpt-4.1-mini"},
		IPAllowlist: []string{"10.0.0.0/8"},
		Status:      StatusActive,
	}, "thk_restricted")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ValidateAPIKey(secret, "127.0.0.1"); AsHTTPError(err).Code != "api_key_disabled" {
		t.Fatalf("expected ip allowlist rejection, got %v", err)
	}
	if _, valid, err := store.ValidateAPIKey(secret, "10.1.2.3"); err != nil || valid.Group != "dedicated" {
		t.Fatalf("expected valid key with group, got key=%+v err=%v", valid, err)
	}
	rotated, newSecret, err := store.RotateAPIKey(key.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RotatedFromID != key.ID || newSecret == "" {
		t.Fatalf("unexpected rotated key: %+v secret=%q", rotated, newSecret)
	}
	if _, _, err := store.ValidateAPIKey(secret, "10.1.2.3"); AsHTTPError(err).Code != "api_key_disabled" {
		t.Fatalf("old key should be revoked, got %v", err)
	}
	if _, _, err := store.ValidateAPIKey(newSecret, "10.1.2.3"); err != nil {
		t.Fatalf("new key should work: %v", err)
	}
}

func TestAPIKeyStatusUpdatePreservesExpiration(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Expiring Key Ops"})
	expiresAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:      "expiring",
		Status:    StatusActive,
		ExpiresAt: &expiresAt,
	}, "thk_expiring")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateAPIKey(key.ID, APIKey{Status: StatusDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusDisabled {
		t.Fatalf("expected disabled key, got %s", updated.Status)
	}
	if updated.ExpiresAt == nil || !updated.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expected expiration to be preserved, got %v want %v", updated.ExpiresAt, expiresAt)
	}
}

func TestAdminCreatesProjectAndKey(t *testing.T) {
	app := newTestServer()
	project := doJSON(t, app, http.MethodPost, "/api/admin/projects", map[string]any{
		"name":    "Production App",
		"team_id": "team_ai",
	}, "")
	if project.Code != http.StatusCreated {
		t.Fatalf("expected project created, got %d: %s", project.Code, project.Body)
	}
	var created Project
	if err := json.Unmarshal([]byte(project.Body), &created); err != nil {
		t.Fatal(err)
	}

	key := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+created.ID+"/keys", map[string]any{
		"name":           "prod-key",
		"allowed_models": []string{"gpt-4.1-mini"},
		"limits": map[string]any{
			"daily_requests":  10,
			"max_concurrency": 2,
		},
	}, "")
	if key.Code != http.StatusCreated {
		t.Fatalf("expected key created, got %d: %s", key.Code, key.Body)
	}
	if !strings.Contains(key.Body, `"plain_text_visible_once":true`) || !strings.Contains(key.Body, `"api_key":"sk_`) {
		t.Fatalf("expected one-time key response: %s", key.Body)
	}
}

func TestAPIKeyGenerationUsesSystemSettings(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	store.CreateResource("settings", AdminResource{
		ID:          "cfg_gateway",
		Name:        "Gateway Base Settings",
		Description: "Default OpenAI-compatible gateway configuration.",
		Status:      StatusActive,
		Fields: map[string]any{
			"public_base_url":       "http://localhost:8080",
			"default_timeout":       "120s",
			"audit_retention":       "180d",
			"api_key_prefix":        "corp_",
			"api_key_random_length": 32,
		},
	})
	app := New(store).Handler()

	key := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+defaultProjectID+"/keys", map[string]any{
		"name":           "custom-format-key",
		"allowed_models": []string{"gpt-4.1-mini"},
	}, "")
	if key.Code != http.StatusCreated {
		t.Fatalf("expected key created, got %d: %s", key.Code, key.Body)
	}
	var created struct {
		ID     string `json:"id"`
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal([]byte(key.Body), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.APIKey, "corp_") {
		t.Fatalf("expected custom prefix, got %s", created.APIKey)
	}
	if randomPart := strings.TrimPrefix(created.APIKey, "corp_"); len(randomPart) != 32 {
		t.Fatalf("expected 32 random characters, got %d in %s", len(randomPart), created.APIKey)
	}

	rotated := doJSON(t, app, http.MethodPost, "/api/admin/api-keys/"+created.ID+"/rotate", map[string]any{}, "")
	if rotated.Code != http.StatusCreated {
		t.Fatalf("expected rotated key, got %d: %s", rotated.Code, rotated.Body)
	}
	var rotatedPayload struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal([]byte(rotated.Body), &rotatedPayload); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rotatedPayload.APIKey, "corp_") {
		t.Fatalf("expected rotated key to use custom prefix, got %s", rotatedPayload.APIKey)
	}
	if randomPart := strings.TrimPrefix(rotatedPayload.APIKey, "corp_"); len(randomPart) != 32 {
		t.Fatalf("expected rotated key to use 32 random characters, got %d in %s", len(randomPart), rotatedPayload.APIKey)
	}
}

func TestApprovalFlowInterceptsAPIKeyCreate(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Approval Project"})
	store.CreateResource("approval-flows", AdminResource{
		Name:   "Key approval",
		Status: StatusActive,
		Fields: map[string]any{
			"trigger":       "api_key_create",
			"approver_role": "admin",
		},
	})
	app := New(store).Handler()

	key := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{
		"name":           "needs-approval",
		"allowed_models": []string{"gpt-4.1-mini"},
		"limits": map[string]any{
			"daily_requests": 10,
		},
	}, "")
	if key.Code != http.StatusAccepted {
		t.Fatalf("expected approval response, got %d: %s", key.Code, key.Body)
	}
	var pendingKeyResponse struct {
		ApprovalRequired bool   `json:"approval_required"`
		APIKey           string `json:"api_key"`
	}
	if err := json.Unmarshal([]byte(key.Body), &pendingKeyResponse); err != nil {
		t.Fatal(err)
	}
	if !pendingKeyResponse.ApprovalRequired || pendingKeyResponse.APIKey != "" {
		t.Fatalf("expected pending approval without secret: %s", key.Body)
	}

	approvals := doJSON(t, app, http.MethodGet, "/api/admin/approvals", nil, "")
	if approvals.Code != http.StatusOK {
		t.Fatalf("expected approvals list, got %d: %s", approvals.Code, approvals.Body)
	}
	var list struct {
		Data []ApprovalRequest `json:"data"`
	}
	if err := json.Unmarshal([]byte(approvals.Body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].Status != "pending" || list.Data[0].Trigger != "api_key_create" {
		t.Fatalf("unexpected approvals: %s", approvals.Body)
	}

	approved := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+list.Data[0].ID+"/approve", map[string]any{}, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("expected approval apply, got %d: %s", approved.Code, approved.Body)
	}
	if !strings.Contains(approved.Body, `"api_key":"sk_`) || !strings.Contains(approved.Body, `"status":"approved"`) {
		t.Fatalf("expected approved key result: %s", approved.Body)
	}
	if len(store.ListAPIKeys()) != 1 {
		t.Fatalf("expected key created after approval")
	}
}

func TestProjectQuotaIncreaseApprovalCreatesAndLinksPolicy(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Quota Project", Status: StatusActive})
	app := New(store).Handler()

	request := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/quota-increase", map[string]any{
		"name": "Quota Project 提升额度",
		"fields": map[string]any{
			"daily_requests":   20,
			"monthly_requests": 500,
			"monthly_cost_usd": 25,
		},
	}, "")
	if request.Code != http.StatusAccepted {
		t.Fatalf("expected quota approval request, got %d: %s", request.Code, request.Body)
	}
	if !strings.Contains(request.Body, `"approval_required":true`) || !strings.Contains(request.Body, `"trigger":"quota_increase"`) {
		t.Fatalf("expected quota approval payload: %s", request.Body)
	}

	approvals := store.ListApprovalRequests()
	if len(approvals) != 1 || approvals[0].ResourceType != "quota-policies" || approvals[0].ResourceID != "" {
		t.Fatalf("unexpected quota approvals: %+v", approvals)
	}

	approved := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("expected quota approval apply, got %d: %s", approved.Code, approved.Body)
	}

	quotas := store.ListResources("quota-policies")
	if len(quotas) != 1 {
		t.Fatalf("expected one quota policy after approval, got %+v", quotas)
	}
	if stringField(quotas[0].Fields, "scope") != "project" || stringField(quotas[0].Fields, "scope_id") != project.ID {
		t.Fatalf("expected project-scoped quota policy, got %+v", quotas[0].Fields)
	}
	if int64Field(quotas[0].Fields, "daily_requests") != 20 || int64Field(quotas[0].Fields, "monthly_requests") != 500 {
		t.Fatalf("expected approved quota limits, got %+v", quotas[0].Fields)
	}
	updatedProject, ok := store.GetProject(project.ID)
	if !ok || updatedProject.DefaultQuotaRef != quotas[0].ID {
		t.Fatalf("expected project quota ref %s, got %+v", quotas[0].ID, updatedProject)
	}
}

func TestAlertWebhookDeliveryIsRecorded(t *testing.T) {
	store := NewMemoryStore()
	var received bytes.Buffer
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST webhook, got %s", r.Method)
		}
		_, _ = io.Copy(&received, r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()

	store.CreateResource("notification-channels", AdminResource{
		Name:   "Webhook",
		Status: StatusActive,
		Fields: map[string]any{
			"type":        "webhook",
			"webhook_url": webhook.URL,
		},
	})
	alert := AlertEvent{
		ID:        "alt_test",
		ScopeType: "api_key",
		ScopeID:   "key_demo",
		Severity:  "warning",
		Code:      "monthly_cost_near_limit",
		Message:   "Monthly cost quota is near or above limit",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected delivery success, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"status":"success"`) || !strings.Contains(resp.Body, `"status_code":202`) {
		t.Fatalf("unexpected delivery response: %s", resp.Body)
	}
	if !strings.Contains(received.String(), "monthly_cost_near_limit") {
		t.Fatalf("webhook did not receive alert payload: %s", received.String())
	}
	deliveries := store.ListAlertDeliveries()
	if len(deliveries) < 1 || deliveries[0].AlertID != alert.ID || deliveries[0].Status != "success" {
		t.Fatalf("expected recorded delivery, got %+v", deliveries)
	}
}

func TestAlertBotDeliveryFormats(t *testing.T) {
	tests := []struct {
		channelType string
		bodyMarker  string
		fields      map[string]any
		headerKey   string
		headerValue string
	}{
		{channelType: "feishu", bodyMarker: `"msg_type":"text"`},
		{channelType: "dingtalk", bodyMarker: `"msgtype":"text"`},
		{channelType: "wecom", bodyMarker: `"msgtype":"text"`},
		{channelType: "slack", bodyMarker: `"text":"[TokenHub] monitor_check_failed`},
		{channelType: "discord", bodyMarker: `"content":"[TokenHub] monitor_check_failed`},
		{channelType: "telegram", bodyMarker: `"chat_id":"chat_123"`, fields: map[string]any{
			"telegram_bot_token": "telegram-token",
			"telegram_chat_id":   "chat_123",
		}},
		{channelType: "whatsapp", bodyMarker: `"messaging_product":"whatsapp"`, fields: map[string]any{
			"whatsapp_to":     "+15550001111",
			"access_token":    "wa-token",
			"phone_number_id": "phone-number-id",
		}, headerKey: "Authorization", headerValue: "Bearer wa-token"},
	}
	for _, tt := range tests {
		t.Run(tt.channelType, func(t *testing.T) {
			store := NewMemoryStore()
			var received bytes.Buffer
			webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Fatalf("expected POST webhook, got %s", r.Method)
				}
				if tt.headerKey != "" && r.Header.Get(tt.headerKey) != tt.headerValue {
					t.Fatalf("expected %s header %q, got %q", tt.headerKey, tt.headerValue, r.Header.Get(tt.headerKey))
				}
				_, _ = io.Copy(&received, r.Body)
				w.WriteHeader(http.StatusOK)
			}))
			defer webhook.Close()

			fields := map[string]any{
				"type":        tt.channelType,
				"webhook_url": webhook.URL,
			}
			for key, value := range tt.fields {
				fields[key] = value
			}
			store.CreateResource("notification-channels", AdminResource{
				Name:   tt.channelType,
				Status: StatusActive,
				Fields: fields,
			})
			alert := AlertEvent{
				ID:        "alt_" + tt.channelType,
				ScopeType: "provider",
				ScopeID:   "prv_test",
				Severity:  "warning",
				Code:      "monitor_check_failed",
				Message:   "Provider failed",
				CreatedAt: time.Now().UTC(),
			}
			if err := store.db.Create(&alert).Error; err != nil {
				t.Fatal(err)
			}
			app := New(store).Handler()

			resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
			if resp.Code != http.StatusOK || !strings.Contains(resp.Body, `"status":"success"`) {
				t.Fatalf("expected delivery success, got %d: %s", resp.Code, resp.Body)
			}
			if !strings.Contains(received.String(), tt.bodyMarker) || !strings.Contains(received.String(), "monitor_check_failed") {
				t.Fatalf("unexpected %s payload: %s", tt.channelType, received.String())
			}
		})
	}
}

func TestDingTalkDeliverySignsWebhookWhenSecretConfigured(t *testing.T) {
	store := NewMemoryStore()
	const secret = "SECtestSecret"
	var gotTimestamp, gotSign string
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTimestamp = r.URL.Query().Get("timestamp")
		gotSign = r.URL.Query().Get("sign")
		if gotTimestamp == "" || gotSign == "" {
			t.Fatalf("expected signed dingtalk webhook URL, got %s", r.URL.RawQuery)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(gotTimestamp + "\n" + secret))
		expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if gotSign != expected {
			t.Fatalf("unexpected dingtalk sign: got %q want %q", gotSign, expected)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer webhook.Close()

	store.CreateResource("notification-channels", AdminResource{
		Name:   "DingTalk",
		Status: StatusActive,
		Fields: map[string]any{
			"type":        "dingtalk",
			"webhook_url": webhook.URL,
			"secret":      secret,
		},
	})
	alert := AlertEvent{ID: "alt_dingtalk_signed", ScopeType: "provider", ScopeID: "prv_test", Severity: "warning", Code: "monitor_check_failed", Message: "Provider failed", CreatedAt: time.Now().UTC()}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, `"status":"success"`) {
		t.Fatalf("expected signed dingtalk delivery success, got %d: %s", resp.Code, resp.Body)
	}
	if gotTimestamp == "" || gotSign == "" {
		t.Fatal("dingtalk server was not called with a signature")
	}
}

func TestDingTalkDeliveryRecordsBusinessError(t *testing.T) {
	store := NewMemoryStore()
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"sign mismatch"}`))
	}))
	defer webhook.Close()

	store.CreateResource("notification-channels", AdminResource{
		Name:   "DingTalk",
		Status: StatusActive,
		Fields: map[string]any{
			"type":        "dingtalk",
			"webhook_url": webhook.URL,
		},
	})
	alert := AlertEvent{ID: "alt_dingtalk_error", ScopeType: "provider", ScopeID: "prv_test", Severity: "warning", Code: "monitor_check_failed", Message: "Provider failed", CreatedAt: time.Now().UTC()}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, `"status":"failed"`) || !strings.Contains(resp.Body, "errcode=310000") {
		t.Fatalf("expected dingtalk business error to be recorded, got %d: %s", resp.Code, resp.Body)
	}
}

func TestAlertEmailDeliveryMissingConfigIsRecorded(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("notification-channels", AdminResource{
		Name:   "Email",
		Status: StatusActive,
		Fields: map[string]any{
			"type":     "email",
			"email_to": "ops@example.com",
		},
	})
	alert := AlertEvent{
		ID:        "alt_email",
		ScopeType: "provider",
		ScopeID:   "prv_test",
		Severity:  "warning",
		Code:      "monitor_check_failed",
		Message:   "Provider failed",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, `"status":"failed"`) || !strings.Contains(resp.Body, "smtp_host is required") {
		t.Fatalf("expected recorded email config failure, got %d: %s", resp.Code, resp.Body)
	}
	deliveries := store.ListAlertDeliveries()
	if len(deliveries) < 1 || deliveries[0].Channel != "email" || deliveries[0].Status != "failed" {
		t.Fatalf("expected failed email delivery record, got %+v", deliveries)
	}
}

func TestBillingGenerationUpdatesBudgetsAndInvoices(t *testing.T) {
	store := NewMemoryStore()
	period := time.Now().UTC().Format("2006-01")
	store.CreateResource("teams", AdminResource{
		ID:     "team_finance",
		Name:   "Finance",
		Status: StatusActive,
		Fields: map[string]any{
			"cost_center": "CC-FIN",
		},
	})
	project := store.CreateProject(Project{Name: "Finance App", TeamID: "team_finance"})
	directProject := store.CreateProject(Project{Name: "Direct Cost Center App", TeamID: "team_finance", CostCenter: "CC-DIRECT"})
	store.CreateResource("budgets", AdminResource{
		ID:     "bdg_finance",
		Name:   "Finance monthly budget",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":        "cost_center",
			"scope_id":     "CC-FIN",
			"period":       "monthly",
			"period_ref":   period,
			"amount_usd":   1,
			"warn_percent": 50,
		},
	})
	store.CreateResource("budgets", AdminResource{
		ID:     "bdg_project",
		Name:   "Project monthly budget",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":        "project",
			"scope_id":     project.ID,
			"period":       "monthly",
			"period_ref":   period,
			"amount_usd":   2,
			"warn_percent": 90,
		},
	})
	store.CreateResource("budgets", AdminResource{
		ID:     "bdg_direct",
		Name:   "Direct cost center monthly budget",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":        "cost_center",
			"scope_id":     "CC-DIRECT",
			"period":       "monthly",
			"period_ref":   period,
			"amount_usd":   2,
			"warn_percent": 90,
		},
	})
	if err := store.db.Create(&UsageRecord{
		ID:           "use_finance_1",
		RequestID:    "req_finance_1",
		ProjectID:    project.ID,
		APIKeyID:     "key_finance",
		ModelName:    "gpt-4.1-mini",
		ProviderID:   "prv_mock",
		InputTokens:  1000,
		OutputTokens: 1000,
		TotalTokens:  2000,
		CostUSD:      0.75,
		CreatedAt:    time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&UsageRecord{
		ID:           "use_direct_1",
		RequestID:    "req_direct_1",
		ProjectID:    directProject.ID,
		APIKeyID:     "key_direct",
		ModelName:    "gpt-4.1-mini",
		ProviderID:   "prv_mock",
		InputTokens:  100,
		OutputTokens: 100,
		TotalTokens:  200,
		CostUSD:      0.25,
		CreatedAt:    time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/billing/generate", map[string]any{"period": period}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected billing generation, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"chargebacks":2`) || !strings.Contains(resp.Body, `"invoices":2`) {
		t.Fatalf("expected generated chargeback and invoice: %s", resp.Body)
	}
	chargebacks := store.ListResources("chargebacks")
	invoices := store.ListResources("invoices")
	if len(chargebacks) != 2 {
		t.Fatalf("unexpected chargebacks: %+v", chargebacks)
	}
	var hasFinanceChargeback, hasDirectChargeback bool
	for _, chargeback := range chargebacks {
		if stringField(chargeback.Fields, "cost_center") == "CC-FIN" {
			hasFinanceChargeback = true
		}
		if stringField(chargeback.Fields, "cost_center") == "CC-DIRECT" {
			hasDirectChargeback = true
		}
	}
	if !hasFinanceChargeback || !hasDirectChargeback {
		t.Fatalf("expected finance and direct cost center chargebacks: %+v", chargebacks)
	}
	if len(invoices) != 2 {
		t.Fatalf("unexpected invoices: %+v", invoices)
	}
	var hasDirectInvoice bool
	for _, invoice := range invoices {
		if strings.Contains(stringField(invoice.Fields, "invoice_note"), "CC-DIRECT") {
			hasDirectInvoice = true
		}
	}
	if !hasDirectInvoice {
		t.Fatalf("expected direct cost center invoice: %+v", invoices)
	}
	budgets := store.ListResources("budgets")
	if len(budgets) != 3 {
		t.Fatalf("expected two budgets, got %+v", budgets)
	}
	var costCenterBudget, projectBudget, directBudget AdminResource
	for _, budget := range budgets {
		switch budget.ID {
		case "bdg_finance":
			costCenterBudget = budget
		case "bdg_project":
			projectBudget = budget
		case "bdg_direct":
			directBudget = budget
		}
	}
	if float64Field(costCenterBudget.Fields, "used_usd") != 0.75 ||
		float64Field(projectBudget.Fields, "used_usd") != 0.75 ||
		float64Field(directBudget.Fields, "used_usd") != 0.25 {
		t.Fatalf("expected budget usage update: %+v", budgets)
	}
	alerts := store.ListAlerts()
	if len(alerts) != 1 || alerts[0].Code != "budget_warn_threshold" {
		t.Fatalf("expected budget threshold alert, got %+v", alerts)
	}
}

func TestInvoiceConfirmRejectAndStructuredExport(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{
		Username: "finance-admin",
		Name:     "Finance Admin",
		Email:    "finance-admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	invoice := store.CreateResource("invoices", AdminResource{
		ID:     "inv_confirm_me",
		Name:   "2026-06 CC-FIN internal invoice",
		Status: "pending",
		Fields: map[string]any{
			"period":       "2026-06",
			"cost_center":  "CC-FIN",
			"amount_usd":   12.34,
			"invoice_note": "Initial note",
		},
	})
	rejected := store.CreateResource("invoices", AdminResource{
		ID:     "inv_reject_me",
		Name:   "2026-06 CC-RND internal invoice",
		Status: "pending",
		Fields: map[string]any{
			"period":       "2026-06",
			"cost_center":  "CC-RND",
			"amount_usd":   2.5,
			"invoice_note": "Needs review",
		},
	})
	app := New(store).Handler()

	confirmed := doJSON(t, app, http.MethodPost, "/api/admin/resources/invoices/"+invoice.ID+"/confirm", map[string]any{
		"invoice_note": "PO-2026-06-FIN",
	}, "")
	if confirmed.Code != http.StatusOK {
		t.Fatalf("expected invoice confirm 200, got %d: %s", confirmed.Code, confirmed.Body)
	}
	if !strings.Contains(confirmed.Body, `"status":"confirmed"`) ||
		!strings.Contains(confirmed.Body, `"confirmed_by":"Finance Admin"`) ||
		!strings.Contains(confirmed.Body, `"invoice_note":"PO-2026-06-FIN"`) {
		t.Fatalf("unexpected confirm body: %s", confirmed.Body)
	}
	again := doJSON(t, app, http.MethodPost, "/api/admin/resources/invoices/"+invoice.ID+"/confirm", map[string]any{}, "")
	if again.Code != http.StatusConflict || !strings.Contains(again.Body, "invoice_already_decided") {
		t.Fatalf("expected already decided conflict, got %d: %s", again.Code, again.Body)
	}

	rejectResp := doJSON(t, app, http.MethodPost, "/api/admin/resources/invoices/"+rejected.ID+"/reject", map[string]any{
		"reject_reason": "department disputed allocation",
	}, "")
	if rejectResp.Code != http.StatusOK {
		t.Fatalf("expected invoice reject 200, got %d: %s", rejectResp.Code, rejectResp.Body)
	}
	if !strings.Contains(rejectResp.Body, `"status":"rejected"`) ||
		!strings.Contains(rejectResp.Body, `"reject_reason":"department disputed allocation"`) {
		t.Fatalf("unexpected reject body: %s", rejectResp.Body)
	}

	exported := doJSON(t, app, http.MethodGet, "/api/admin/export/invoices", nil, "")
	if exported.Code != http.StatusOK {
		t.Fatalf("expected invoice export 200, got %d: %s", exported.Code, exported.Body)
	}
	if !strings.HasPrefix(exported.Body, "period,cost_center,amount_usd,invoice_note,confirmed_by,confirmed_at,reject_reason,status,updated_at") {
		t.Fatalf("expected structured invoice csv, got: %s", exported.Body)
	}
	if !strings.Contains(exported.Body, "2026-06,CC-FIN,12.34,PO-2026-06-FIN,Finance Admin") ||
		!strings.Contains(exported.Body, "2026-06,CC-RND,2.5,Needs review,,,department disputed allocation,rejected") {
		t.Fatalf("expected invoice rows in export: %s", exported.Body)
	}
	filtered := doJSON(t, app, http.MethodGet, "/api/admin/export/invoices?period=2026-05", nil, "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("expected filtered invoice export 200, got %d: %s", filtered.Code, filtered.Body)
	}
	if strings.Contains(filtered.Body, "CC-FIN") || strings.Contains(filtered.Body, "CC-RND") {
		t.Fatalf("period filtered export should not include 2026-06 rows: %s", filtered.Body)
	}
	audit := store.ListAuditEvents()
	if len(audit) < 3 {
		t.Fatalf("expected audit events for invoice actions and export, got %+v", audit)
	}
}

func TestInvoiceConfirmCanRequireApproval(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{
		Username: "approver",
		Name:     "Approver",
		Email:    "approver@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	projectApprover, err := store.CreateAdminUser(AdminUser{
		Username: "project-approver",
		Name:     "Project Approver",
		Email:    "project-approver@tokenhub.local",
		Role:     "project_admin",
		Status:   StatusActive,
	}, "admin123456")
	if err != nil {
		t.Fatal(err)
	}
	_, projectSession, err := store.AuthenticateAdminUser(projectApprover.Email, "admin123456", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	invoice := store.CreateResource("invoices", AdminResource{
		ID:     "inv_needs_approval",
		Name:   "2026-06 CC-AI internal invoice",
		Status: "pending",
		Fields: map[string]any{
			"period":       "2026-06",
			"cost_center":  "CC-AI",
			"amount_usd":   100,
			"invoice_note": "Pending approval",
		},
	})
	store.CreateResource("approval-flows", AdminResource{
		Name:   "Invoice confirmation approval",
		Status: StatusActive,
		Fields: map[string]any{
			"trigger":       "invoice_confirm",
			"approver_role": "admin",
			"threshold_usd": 50,
		},
	})
	app := New(store).Handler()

	confirm := doJSON(t, app, http.MethodPost, "/api/admin/resources/invoices/"+invoice.ID+"/confirm", map[string]any{
		"invoice_note": "Approve this invoice",
	}, "")
	if confirm.Code != http.StatusAccepted {
		t.Fatalf("expected invoice confirmation approval, got %d: %s", confirm.Code, confirm.Body)
	}
	if !strings.Contains(confirm.Body, `"approval_required":true`) || !strings.Contains(confirm.Body, `"trigger":"invoice_confirm"`) {
		t.Fatalf("expected invoice approval payload: %s", confirm.Body)
	}
	pendingInvoices := store.ListResources("invoices")
	if len(pendingInvoices) != 1 || pendingInvoices[0].Status != "pending" {
		t.Fatalf("invoice should remain pending before approval, got %+v", pendingInvoices)
	}
	approvals := store.ListApprovalRequests()
	if len(approvals) != 1 || approvals[0].ResourceID != invoice.ID {
		t.Fatalf("expected one invoice approval, got %+v", approvals)
	}
	forbidden := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, projectSession.Token)
	if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body, "approval_role_forbidden") {
		t.Fatalf("expected approval role forbidden, got %d: %s", forbidden.Code, forbidden.Body)
	}
	approved := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("expected invoice approval apply, got %d: %s", approved.Code, approved.Body)
	}
	items := store.ListResources("invoices")
	if len(items) != 1 || items[0].Status != "confirmed" || stringField(items[0].Fields, "confirmed_by") != "Approver" {
		t.Fatalf("expected approved invoice confirmation, got %+v", items)
	}
	var applied bool
	for _, event := range store.ListAuditEvents() {
		if event.Action == "apply_approval" && event.ResourceType == "invoices" && event.ResourceID == invoice.ID {
			applied = true
		}
	}
	if !applied {
		t.Fatalf("expected apply_approval audit event, got %+v", store.ListAuditEvents())
	}
}

func TestSQLiteBackupCreateDownloadRestoreAndDelete(t *testing.T) {
	tmp := t.TempDir()
	store, err := NewSQLiteStoreWithConfig("sqlite:"+filepath.Join(tmp, "tokenhub.db"), Config{
		AdminToken:      "dev_admin_token",
		SQLiteBackupDir: filepath.Join(tmp, "backups"),
		SecretKey:       "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Backup Restore Project", Status: StatusActive})
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/sqlite/backups", map[string]any{"expire_days": 7}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create backup failed: %d %s", created.Code, created.Body)
	}
	var backup SQLiteBackupRecord
	if err := json.Unmarshal([]byte(created.Body), &backup); err != nil {
		t.Fatal(err)
	}
	if backup.ID == "" || backup.Status != "ready" || backup.SizeBytes <= 0 || backup.ChecksumSHA256 == "" {
		t.Fatalf("unexpected backup payload: %+v body=%s", backup, created.Body)
	}

	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.GetProject(project.ID); ok {
		t.Fatal("project should be deleted before restore")
	}

	invalidRestore := doJSON(t, app, http.MethodPost, "/api/admin/sqlite/backups/"+backup.ID+"/restore", map[string]any{
		"confirmation": "RESTORE wrong",
	}, "")
	if invalidRestore.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid restore confirmation, got %d %s", invalidRestore.Code, invalidRestore.Body)
	}

	restored := doJSON(t, app, http.MethodPost, "/api/admin/sqlite/backups/"+backup.ID+"/restore", map[string]any{
		"confirmation": "RESTORE " + backup.ID,
	}, "")
	if restored.Code != http.StatusOK {
		t.Fatalf("restore failed: %d %s", restored.Code, restored.Body)
	}
	if _, ok := store.GetProject(project.ID); !ok {
		t.Fatalf("project %s should exist after restore", project.ID)
	}

	download := doJSON(t, app, http.MethodGet, "/api/admin/sqlite/backups/"+backup.ID+"/download", nil, "")
	if download.Code != http.StatusOK || !strings.Contains(download.Body, "SQLite format") {
		t.Fatalf("download failed: %d %q", download.Code, download.Body[:minInt(len(download.Body), 80)])
	}

	listed := doJSON(t, app, http.MethodGet, "/api/admin/sqlite/backups", nil, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body, backup.ID) {
		t.Fatalf("list backups failed: %d %s", listed.Code, listed.Body)
	}

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/sqlite/backups/"+backup.ID, nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete backup failed: %d %s", deleted.Code, deleted.Body)
	}
}

func TestAdminAPIRequiresToken(t *testing.T) {
	app := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_admin_token") {
		t.Fatalf("expected invalid_admin_token: %s", rr.Body.String())
	}
}

func TestAdminLoginAndUserManagement(t *testing.T) {
	app := newTestServer()
	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "admin@tokenhub.local",
		"password": "admin123456",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d: %s", login.Code, login.Body)
	}
	var payload struct {
		Token string    `json:"token"`
		User  AdminUser `json:"user"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" || payload.User.Email != "admin@tokenhub.local" {
		t.Fatalf("unexpected login payload: %s", login.Body)
	}

	users := doJSON(t, app, http.MethodGet, "/api/admin/users", nil, payload.Token)
	if users.Code != http.StatusOK {
		t.Fatalf("expected users 200, got %d: %s", users.Code, users.Body)
	}
	if !strings.Contains(users.Body, `"email":"admin@tokenhub.local"`) || strings.Contains(users.Body, "PasswordHash") {
		t.Fatalf("unexpected users payload: %s", users.Body)
	}
}

func TestAdminAuthIdentityProvidersListActiveOAuthSources(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("identity-providers", AdminResource{
		ID:     "idp_gitlab",
		Name:   "GitLab OAuth",
		Status: StatusActive,
		Fields: map[string]any{
			"provider_type": "oauth2",
			"issuer_url":    "http://gitlab.example.test",
			"client_id":     "gitlab-client",
			"client_secret": "secret-value",
			"authorize_url": "http://gitlab.example.test/oauth/authorize",
			"token_url":     "http://gitlab.example.test/oauth/token",
			"userinfo_url":  "http://gitlab.example.test/api/v4/user",
		},
	})
	store.CreateResource("identity-providers", AdminResource{
		ID:     "idp_disabled",
		Name:   "Disabled OAuth",
		Status: StatusDisabled,
		Fields: map[string]any{
			"provider_type": "oauth2",
			"client_id":     "disabled-client",
			"authorize_url": "http://disabled.example.test/oauth/authorize",
			"token_url":     "http://disabled.example.test/oauth/token",
			"userinfo_url":  "http://disabled.example.test/userinfo",
		},
	})
	store.CreateResource("identity-providers", AdminResource{
		ID:     "idp_google",
		Name:   "Company SSO",
		Status: StatusActive,
		Fields: map[string]any{
			"provider_type": "oauth2",
			"icon_key":      "google",
			"client_id":     "google-client",
			"authorize_url": "http://accounts.example.test/oauth/authorize",
			"token_url":     "http://accounts.example.test/oauth/token",
			"userinfo_url":  "http://accounts.example.test/userinfo",
		},
	})
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "test-secret"}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/auth/identity-providers", nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected providers 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"id":"idp_gitlab"`) ||
		!strings.Contains(rr.Body.String(), `"icon_key":"gitlab"`) ||
		!strings.Contains(rr.Body.String(), `"display_name":"GitLab"`) ||
		!strings.Contains(rr.Body.String(), `"id":"idp_google"`) ||
		!strings.Contains(rr.Body.String(), `"icon_key":"google"`) ||
		!strings.Contains(rr.Body.String(), `"display_name":"Google"`) ||
		strings.Contains(rr.Body.String(), "secret-value") ||
		strings.Contains(rr.Body.String(), "idp_disabled") {
		t.Fatalf("unexpected providers payload: %s", rr.Body.String())
	}
}

func TestAdminOAuthLoginCreatesSession(t *testing.T) {
	var receivedTokenRequest bool
	var receivedUserInfoRequest bool
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			receivedTokenRequest = true
			if r.Method != http.MethodPost {
				t.Fatalf("token method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("grant_type") != "authorization_code" ||
				r.FormValue("code") != "oauth-code" ||
				r.FormValue("client_id") != "gitlab-client" ||
				r.FormValue("client_secret") != "gitlab-secret" ||
				r.FormValue("redirect_uri") != "http://localhost:8080/api/admin/auth/oauth/callback" {
				t.Fatalf("unexpected token form: %+v", r.Form)
			}
			writeJSON(w, http.StatusOK, map[string]any{"access_token": "gitlab-access-token", "token_type": "Bearer"})
		case "/api/v4/user":
			receivedUserInfoRequest = true
			if r.Header.Get("authorization") != "Bearer gitlab-access-token" {
				t.Fatalf("unexpected userinfo authorization: %s", r.Header.Get("authorization"))
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"name":       "GitLab User",
				"email":      "gitlab.user@example.test",
				"department": "Product",
			})
		default:
			t.Fatalf("unexpected oauth path: %s", r.URL.Path)
		}
	}))
	defer oauthServer.Close()

	store := NewMemoryStore()
	store.CreateResource("identity-providers", AdminResource{
		ID:     "idp_gitlab",
		Name:   "GitLab OAuth",
		Status: StatusActive,
		Fields: map[string]any{
			"provider_type":  "oauth2",
			"issuer_url":     oauthServer.URL,
			"client_id":      "gitlab-client",
			"client_secret":  "gitlab-secret",
			"authorize_url":  oauthServer.URL + "/oauth/authorize",
			"token_url":      oauthServer.URL + "/oauth/token",
			"userinfo_url":   oauthServer.URL + "/api/v4/user",
			"redirect_uri":   "http://localhost:8080/api/admin/auth/oauth/callback",
			"scopes":         "openid profile email read_user",
			"username_claim": "name",
			"email_claim":    "email",
			"team_claim":     "department",
		},
	})
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "test-secret"}).Handler()

	startReq := httptest.NewRequest(http.MethodGet, "/api/admin/auth/oauth/start?id=idp_gitlab&return_url=http%3A%2F%2Flocalhost%3A3001%2Foverview", nil)
	startReq.Host = "127.0.0.1:8080"
	startResp := httptest.NewRecorder()
	app.ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusFound {
		t.Fatalf("expected start redirect, got %d: %s", startResp.Code, startResp.Body.String())
	}
	authorizeLocation, err := url.Parse(startResp.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if authorizeLocation.Path != "/oauth/authorize" {
		t.Fatalf("unexpected authorize location: %s", authorizeLocation.String())
	}
	authorizeQuery := authorizeLocation.Query()
	if authorizeQuery.Get("client_id") != "gitlab-client" ||
		authorizeQuery.Get("redirect_uri") != "http://localhost:8080/api/admin/auth/oauth/callback" ||
		authorizeQuery.Get("scope") != "openid profile email read_user" ||
		authorizeQuery.Get("response_type") != "code" ||
		authorizeQuery.Get("state") == "" {
		t.Fatalf("unexpected authorize query: %s", authorizeLocation.RawQuery)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/admin/auth/oauth/callback?code=oauth-code&state="+url.QueryEscape(authorizeQuery.Get("state")), nil)
	callbackReq.Host = "localhost:8080"
	callbackResp := httptest.NewRecorder()
	app.ServeHTTP(callbackResp, callbackReq)
	if callbackResp.Code != http.StatusFound {
		t.Fatalf("expected callback redirect, got %d: %s", callbackResp.Code, callbackResp.Body.String())
	}
	if !receivedTokenRequest || !receivedUserInfoRequest {
		t.Fatalf("expected token and userinfo requests, token=%v userinfo=%v", receivedTokenRequest, receivedUserInfoRequest)
	}
	returnLocation, err := url.Parse(callbackResp.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if returnLocation.String() == "" || returnLocation.Scheme != "http" || returnLocation.Host != "localhost:3001" || returnLocation.Path != "/overview" {
		t.Fatalf("unexpected return location: %s", returnLocation.String())
	}
	returnParams, err := url.ParseQuery(returnLocation.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := returnParams.Get("oauth_token")
	if sessionToken == "" || returnParams.Get("oauth_expires_at") == "" {
		t.Fatalf("missing oauth session fragment: %s", returnLocation.Fragment)
	}
	me := doJSON(t, app, http.MethodGet, "/api/admin/auth/me", nil, sessionToken)
	if me.Code != http.StatusOK || !strings.Contains(me.Body, `"email":"gitlab.user@example.test"`) || !strings.Contains(me.Body, `"role":"user"`) {
		t.Fatalf("unexpected me response: %d %s", me.Code, me.Body)
	}
}

func TestOAuthDefaultProvisioningAssignsTeamRoleAndProject(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{
		ID:     "team_product",
		Name:   "Product",
		Status: StatusActive,
		Fields: map[string]any{
			"code": "PRODUCT",
		},
	})
	project := store.CreateProject(Project{Name: "AI Platform", TeamID: "team_product", Status: StatusActive})
	provider := AdminResource{
		ID:     "idp_enterprise",
		Name:   "Enterprise SSO",
		Status: StatusActive,
		Fields: map[string]any{
			"username_claim":       "name",
			"email_claim":          "email",
			"team_claim":           "department",
			"default_role":         "team_leader",
			"default_team_id":      "team_product",
			"default_project_id":   project.ID,
			"default_project_role": "developer",
		},
	}
	server := New(store)

	user, err := server.upsertOAuthAdminUser(provider, map[string]any{
		"name":       "OAuth Leader",
		"email":      "leader@example.test",
		"department": "Unknown Department",
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != "team_leader" || user.TeamID != "team_product" {
		t.Fatalf("unexpected oauth user defaults: role=%s team=%s", user.Role, user.TeamID)
	}
	member, ok := findProjectMember(store.ListResources("project-members"), project.ID, user.ID)
	if !ok {
		t.Fatalf("expected default project membership for %s in %s", user.ID, project.ID)
	}
	if stringField(member.Fields, "role") != "developer" || !truthyField(member.Fields, "can_issue_keys") {
		t.Fatalf("unexpected default project member fields: %+v", member.Fields)
	}

	existing, err := store.CreateAdminUser(AdminUser{
		Username: "existing-oauth",
		Name:     "Existing OAuth",
		Email:    "existing@example.test",
		Role:     "team_leader",
		Status:   StatusActive,
	}, "existing123456")
	if err != nil {
		t.Fatal(err)
	}
	provider.Fields["default_role"] = "user"
	provider.Fields["default_project_role"] = "viewer"
	updated, err := server.upsertOAuthAdminUser(provider, map[string]any{
		"name":  "Existing OAuth Renamed",
		"email": existing.Email,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Role != "team_leader" {
		t.Fatalf("existing oauth user role should not be overwritten, got %s", updated.Role)
	}
	if updated.TeamID != "team_product" {
		t.Fatalf("existing oauth user without team should receive default team, got %s", updated.TeamID)
	}
	if _, ok := findProjectMember(store.ListResources("project-members"), project.ID, updated.ID); !ok {
		t.Fatalf("expected default project membership for existing user")
	}
	if _, err := server.upsertOAuthAdminUser(provider, map[string]any{
		"name":  "Existing OAuth Renamed",
		"email": existing.Email,
	}); err != nil {
		t.Fatal(err)
	}
	if got := countProjectMembers(store.ListResources("project-members"), project.ID, updated.ID); got != 1 {
		t.Fatalf("expected one default project membership after repeated login, got %d", got)
	}
}

func findProjectMember(items []AdminResource, projectID string, userID string) (AdminResource, bool) {
	for _, item := range items {
		if strings.TrimSpace(stringField(item.Fields, "project_id")) == projectID &&
			strings.TrimSpace(stringField(item.Fields, "user_id")) == userID {
			return item, true
		}
	}
	return AdminResource{}, false
}

func countProjectMembers(items []AdminResource, projectID string, userID string) int {
	count := 0
	for _, item := range items {
		if strings.TrimSpace(stringField(item.Fields, "project_id")) == projectID &&
			strings.TrimSpace(stringField(item.Fields, "user_id")) == userID {
			count++
		}
	}
	return count
}

func TestRBACAndAdminAuditEvents(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateAdminUser(AdminUser{
		Username: "viewer",
		Name:     "Viewer",
		Email:    "viewer@tokenhub.local",
		Role:     "viewer",
		Status:   StatusActive,
	}, "viewer123456")
	if err != nil {
		t.Fatal(err)
	}
	_ = viewer
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "viewer@tokenhub.local",
		"password": "viewer123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	forbidden := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Forbidden Provider",
		"type": "mock",
	}, payload.Token)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("viewer should not create provider, got %d: %s", forbidden.Code, forbidden.Body)
	}

	created := doJSON(t, app, http.MethodPost, "/api/admin/projects", map[string]any{"name": "Audited Project"}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("admin project create failed: %d %s", created.Code, created.Body)
	}
	audit := doJSON(t, app, http.MethodGet, "/api/admin/audit/events", nil, "")
	if audit.Code != http.StatusOK {
		t.Fatalf("audit events failed: %d %s", audit.Code, audit.Body)
	}
	if !strings.Contains(audit.Body, `"resource_type":"project"`) || !strings.Contains(audit.Body, `"action":"create"`) {
		t.Fatalf("expected project create audit event: %s", audit.Body)
	}
}

func TestRolePermissionsForDeveloperAndTeamLeaderWorkspaces(t *testing.T) {
	if !canAdmin("user", "playground", http.MethodPost) {
		t.Fatal("regular user should be allowed to use playground")
	}
	if canAdmin("user", "routing", http.MethodPost) {
		t.Fatal("regular user should not manage routing")
	}
	if !canAdmin("team_leader", "project", http.MethodPost) {
		t.Fatal("team leader should be allowed to manage team projects")
	}
	if !canAdmin("team_leader", "quota", http.MethodGet) {
		t.Fatal("team leader should be allowed to read visible project quotas")
	}
	if !canAdmin("team_leader", "quota", http.MethodPost) {
		t.Fatal("team leader should be allowed to request or save visible project quotas")
	}
}

func TestRegularUserModelsComeFromActiveRoutesNotKeys(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.CreateAdminUser(AdminUser{
		Username: "model-viewer",
		Name:     "Model Viewer",
		Email:    "model-viewer@tokenhub.local",
		Role:     "user",
		TeamID:   "team_platform",
		Status:   StatusActive,
	}, "viewer123456")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "routed-chat", Modality: "chat", Status: StatusActive})
	store.AddModel(Model{Name: "unrouted-chat", Modality: "chat", Status: StatusActive})
	store.AddModel(Model{Name: "disabled-routed-chat", Modality: "chat", Status: StatusDisabled})
	store.AddModel(Model{Name: "disabled-route-chat", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "routed-chat", ProviderID: "provider_mock", ProviderModel: "routed-chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "disabled-routed-chat", ProviderID: "provider_mock", ProviderModel: "disabled-routed-chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "disabled-route-chat", ProviderID: "provider_mock", ProviderModel: "disabled-route-chat", Status: StatusDisabled})
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "model-viewer@tokenhub.local",
		"password": "viewer123456",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", login.Code, login.Body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	models := doJSON(t, app, http.MethodGet, "/api/admin/models", nil, payload.Token)
	if models.Code != http.StatusOK {
		t.Fatalf("models failed: %d %s", models.Code, models.Body)
	}
	if !strings.Contains(models.Body, "routed-chat") {
		t.Fatalf("expected routed model without any user key: %s", models.Body)
	}
	for _, hidden := range []string{"unrouted-chat", "disabled-routed-chat", "disabled-route-chat"} {
		if strings.Contains(models.Body, hidden) {
			t.Fatalf("model %s should not be visible: %s", hidden, models.Body)
		}
	}
}

func TestTeamLeaderProjectManagementIsTeamScoped(t *testing.T) {
	store := NewMemoryStore()
	leader, err := store.CreateAdminUser(AdminUser{
		Username: "project-leader",
		Name:     "Project Leader",
		Email:    "project-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_project",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	teamProject := store.CreateProject(Project{Name: "Existing Team Project", TeamID: leader.TeamID})
	otherProject := store.CreateProject(Project{Name: "Other Team Project", TeamID: "team_other"})
	teamQuota := store.CreateResource("quota-policies", AdminResource{
		ID:     "quota_team_project",
		Name:   "Team Project Quota",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":          "project",
			"scope_id":       teamProject.ID,
			"daily_requests": 100,
		},
	})
	otherQuota := store.CreateResource("quota-policies", AdminResource{
		ID:     "quota_other_project",
		Name:   "Other Project Quota",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":          "project",
			"scope_id":       otherProject.ID,
			"daily_requests": 200,
		},
	})
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "project-leader@tokenhub.local",
		"password": "leader123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	created := doJSON(t, app, http.MethodPost, "/api/admin/projects", map[string]any{
		"name":          "Team Project",
		"team_id":       "team_other",
		"owner_user_id": "",
	}, payload.Token)
	if created.Code != http.StatusCreated {
		t.Fatalf("team leader project create failed: %d %s", created.Code, created.Body)
	}
	var project Project
	if err := json.Unmarshal([]byte(created.Body), &project); err != nil {
		t.Fatal(err)
	}
	if project.TeamID != leader.TeamID || project.OwnerUserID != leader.ID {
		t.Fatalf("team leader project should be scoped to own team/user: %+v", project)
	}
	quotas := doJSON(t, app, http.MethodGet, "/api/admin/resources/quota-policies", nil, payload.Token)
	if quotas.Code != http.StatusOK {
		t.Fatalf("team leader should read scoped project quotas, got %d: %s", quotas.Code, quotas.Body)
	}
	if !strings.Contains(quotas.Body, teamQuota.ID) || strings.Contains(quotas.Body, otherQuota.ID) {
		t.Fatalf("quota list should be scoped to team projects: %s", quotas.Body)
	}
	createdQuota := doJSON(t, app, http.MethodPost, "/api/admin/resources/quota-policies", map[string]any{
		"name":   "Created Team Project Quota",
		"status": StatusActive,
		"fields": map[string]any{
			"scope":            "project",
			"scope_id":         teamProject.ID,
			"monthly_requests": 500,
		},
	}, payload.Token)
	if createdQuota.Code != http.StatusCreated {
		t.Fatalf("team leader should create quota for own project, got %d: %s", createdQuota.Code, createdQuota.Body)
	}
	forbiddenQuota := doJSON(t, app, http.MethodPost, "/api/admin/resources/quota-policies", map[string]any{
		"name":   "Other Team Quota",
		"status": StatusActive,
		"fields": map[string]any{
			"scope":    "project",
			"scope_id": otherProject.ID,
		},
	}, payload.Token)
	if forbiddenQuota.Code != http.StatusForbidden {
		t.Fatalf("team leader should not create quota for another team project, got %d: %s", forbiddenQuota.Code, forbiddenQuota.Body)
	}
	forbidden := doJSON(t, app, http.MethodPatch, "/api/admin/projects/"+otherProject.ID, map[string]any{
		"name":    "Hijacked",
		"team_id": leader.TeamID,
	}, payload.Token)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("team leader should not update another team project, got %d: %s", forbidden.Code, forbidden.Body)
	}
}

func TestProjectMembersAssignMultipleProjectsAndKeyIssueScope(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "project-member",
		Name:     "Project Member",
		Email:    "project-member@tokenhub.local",
		Role:     "user",
		TeamID:   "team_member",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := store.CreateAdminUser(AdminUser{
		Username: "other-member",
		Name:     "Other Member",
		Email:    "other-member@tokenhub.local",
		Role:     "user",
		TeamID:   "team_member",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	developerProject := store.CreateProject(Project{Name: "Developer Project", TeamID: user.TeamID})
	viewerProject := store.CreateProject(Project{Name: "Viewer Project", TeamID: user.TeamID})
	sameTeamProject := store.CreateProject(Project{Name: "Same Team Unassigned Project", TeamID: user.TeamID})
	otherMemberProject := store.CreateProject(Project{Name: "Other Member Project", TeamID: user.TeamID})
	store.CreateResource("project-members", AdminResource{
		Name:   "Developer Project Member",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": developerProject.ID,
			"user_id":    user.ID,
			"role":       "developer",
		},
	})
	store.CreateResource("project-members", AdminResource{
		Name:   "Viewer Project Member",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": viewerProject.ID,
			"user_id":    user.ID,
			"role":       "viewer",
		},
	})
	store.CreateResource("project-members", AdminResource{
		Name:   "Other Project Member",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": otherMemberProject.ID,
			"user_id":    otherUser.ID,
			"role":       "developer",
		},
	})
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "project-member@tokenhub.local",
		"password": "user123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	projects := doJSON(t, app, http.MethodGet, "/api/admin/projects", nil, payload.Token)
	if projects.Code != http.StatusOK {
		t.Fatalf("user project list failed: %d %s", projects.Code, projects.Body)
	}
	if !strings.Contains(projects.Body, developerProject.ID) || !strings.Contains(projects.Body, viewerProject.ID) {
		t.Fatalf("assigned projects should be visible: %s", projects.Body)
	}
	for _, hidden := range []string{sameTeamProject.ID, otherMemberProject.ID} {
		if strings.Contains(projects.Body, hidden) {
			t.Fatalf("unassigned project %s should not be visible: %s", hidden, projects.Body)
		}
	}
	memberships := doJSON(t, app, http.MethodGet, "/api/admin/resources/project-members", nil, payload.Token)
	if memberships.Code != http.StatusOK {
		t.Fatalf("user project memberships failed: %d %s", memberships.Code, memberships.Body)
	}
	if !strings.Contains(memberships.Body, developerProject.ID) || !strings.Contains(memberships.Body, viewerProject.ID) ||
		strings.Contains(memberships.Body, otherMemberProject.ID) {
		t.Fatalf("user should only read own project memberships: %s", memberships.Body)
	}
	createdKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+developerProject.ID+"/keys", map[string]any{
		"name": "Developer Key",
	}, payload.Token)
	if createdKey.Code != http.StatusCreated || !strings.Contains(createdKey.Body, `"api_key"`) {
		t.Fatalf("developer member should issue key, got %d: %s", createdKey.Code, createdKey.Body)
	}
	forbiddenOwner := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+developerProject.ID+"/keys", map[string]any{
		"name":          "Wrong Owner Key",
		"owner_user_id": otherUser.ID,
	}, payload.Token)
	if forbiddenOwner.Code != http.StatusForbidden {
		t.Fatalf("ordinary user should not assign a key to another user, got %d: %s", forbiddenOwner.Code, forbiddenOwner.Body)
	}
	viewerKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+viewerProject.ID+"/keys", map[string]any{
		"name": "Viewer Key",
	}, payload.Token)
	if viewerKey.Code != http.StatusForbidden {
		t.Fatalf("viewer member should not issue key, got %d: %s", viewerKey.Code, viewerKey.Body)
	}
	unassignedKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+sameTeamProject.ID+"/keys", map[string]any{
		"name": "Unassigned Key",
	}, payload.Token)
	if unassignedKey.Code != http.StatusForbidden {
		t.Fatalf("same-team unassigned user should not issue key, got %d: %s", unassignedKey.Code, unassignedKey.Body)
	}
}

func TestAdminAPIKeyOwnerAttributionAndUsageSnapshot(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{
		ID:       "usr_admin",
		Username: "key-owner-admin",
		Name:     "Key Owner Admin",
		Email:    "key-owner-admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAdminUser(AdminUser{
		Username: "key-owner",
		Name:     "Key Owner",
		Email:    "key-owner@tokenhub.local",
		Role:     "user",
		TeamID:   "team_key_owner",
		Status:   StatusActive,
	}, "owner123456")
	if err != nil {
		t.Fatal(err)
	}
	otherOwner, err := store.CreateAdminUser(AdminUser{
		Username: "key-owner-other",
		Name:     "Other Key Owner",
		Email:    "key-owner-other@tokenhub.local",
		Role:     "user",
		TeamID:   "team_key_owner",
		Status:   StatusActive,
	}, "owner123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Key Attribution Project", TeamID: owner.TeamID, Status: StatusActive})
	server := New(store)
	app := server.Handler()

	createKey := func(name string) APIKey {
		t.Helper()
		resp := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{
			"name":          name,
			"owner_user_id": owner.ID,
		}, "")
		if resp.Code != http.StatusCreated {
			t.Fatalf("create owned key failed: %d %s", resp.Code, resp.Body)
		}
		var payload struct {
			ID          string `json:"id"`
			OwnerUserID string `json:"owner_user_id"`
		}
		if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.OwnerUserID != owner.ID {
			t.Fatalf("created key owner = %q, want %q", payload.OwnerUserID, owner.ID)
		}
		for _, key := range store.ListAPIKeys() {
			if key.ID == payload.ID {
				return key
			}
		}
		t.Fatalf("created key %q not found", payload.ID)
		return APIKey{}
	}

	keyA := createKey("Owner Key A")
	keyB := createKey("Owner Key B")
	if keyA.Metadata["created_by"] != "usr_admin" {
		t.Fatalf("key issuer metadata = %q, want usr_admin", keyA.Metadata["created_by"])
	}
	finishUsage := func(requestID string, key APIKey, totalTokens int64) {
		store.FinishCall(CallContext{
			RequestID: requestID,
			Project:   project,
			Key:       key,
			Model:     Model{Name: "gpt-4.1-mini"},
			StartedAt: time.Now(),
		}, RouteSelection{}, Usage{PromptTokens: totalTokens, TotalTokens: totalTokens}, http.StatusOK, "", "127.0.0.1", "owner-test")
	}
	finishUsage("req_owner_a_before_transfer", keyA, 100)

	transfer := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+keyA.ID, map[string]any{
		"owner_user_id": otherOwner.ID,
	}, "")
	if transfer.Code != http.StatusOK {
		t.Fatalf("transfer key owner failed: %d %s", transfer.Code, transfer.Body)
	}
	updatedKeyA, err := server.findAPIKey(keyA.ID)
	if err != nil {
		t.Fatal(err)
	}
	finishUsage("req_owner_a_after_transfer", updatedKeyA, 300)
	finishUsage("req_owner_b", keyB, 200)

	rotate := doJSON(t, app, http.MethodPost, "/api/admin/api-keys/"+keyA.ID+"/rotate", map[string]any{}, "")
	if rotate.Code != http.StatusCreated {
		t.Fatalf("rotate transferred key failed: %d %s", rotate.Code, rotate.Body)
	}
	var rotatedPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(rotate.Body), &rotatedPayload); err != nil {
		t.Fatal(err)
	}
	rotatedKey, err := server.findAPIKey(rotatedPayload.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotatedKey.OwnerUserID != otherOwner.ID {
		t.Fatalf("rotated key owner = %q, want %q", rotatedKey.OwnerUserID, otherOwner.ID)
	}

	resp := doJSON(t, app, http.MethodGet, "/api/admin/usage/breakdown", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("usage breakdown failed: %d %s", resp.Code, resp.Body)
	}
	var breakdown struct {
		Members []struct {
			ID            string `json:"id"`
			RequestCount  int64  `json:"request_count"`
			TotalTokens   int64  `json:"total_tokens"`
			OwnedKeyCount int    `json:"owned_key_count"`
			UsedKeyCount  int    `json:"used_key_count"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &breakdown); err != nil {
		t.Fatal(err)
	}
	rows := map[string]struct {
		requests int64
		tokens   int64
		owned    int
		used     int
	}{}
	for _, row := range breakdown.Members {
		rows[row.ID] = struct {
			requests int64
			tokens   int64
			owned    int
			used     int
		}{row.RequestCount, row.TotalTokens, row.OwnedKeyCount, row.UsedKeyCount}
	}
	if got := rows[owner.ID]; got.requests != 2 || got.tokens != 300 || got.owned != 1 || got.used != 2 {
		t.Fatalf("original owner usage = %+v, want requests=2 tokens=300 owned=1 used=2", got)
	}
	if got := rows[otherOwner.ID]; got.requests != 1 || got.tokens != 300 || got.owned != 1 || got.used != 1 {
		t.Fatalf("new owner usage = %+v, want requests=1 tokens=300 owned=1 used=1", got)
	}
}

func TestAPIKeyCreateApprovalPreservesOwnerAndIssuer(t *testing.T) {
	store := NewMemoryStore()
	requester, err := store.CreateAdminUser(AdminUser{
		Username: "approval-key-requester",
		Name:     "Approval Key Requester",
		Email:    "approval-key-requester@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_approval_key",
		Status:   StatusActive,
	}, "requester123456")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAdminUser(AdminUser{
		Username: "approval-key-owner",
		Name:     "Approval Key Owner",
		Email:    "approval-key-owner@tokenhub.local",
		Role:     "user",
		TeamID:   requester.TeamID,
		Status:   StatusActive,
	}, "owner123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Approval Key Project", TeamID: requester.TeamID, Status: StatusActive})
	server := New(store)
	result, err := server.applyApprovalRequest(ApprovalRequest{
		ID:           "approval_key_create",
		Trigger:      "api_key_create",
		ResourceType: "api_key",
		RequesterID:  requester.ID,
		Status:       "pending",
		Payload: snapshotJSON(map[string]any{
			"project_id":    project.ID,
			"name":          "Approved Owned Key",
			"owner_user_id": owner.ID,
		}),
	}, AdminUser{ID: "approval-admin", Role: "admin", Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := result.(map[string]any)
	if !ok || payload["owner_user_id"] != owner.ID {
		t.Fatalf("approval result = %#v, want owner %q", result, owner.ID)
	}
	keys := store.ListAPIKeys()
	if len(keys) != 1 {
		t.Fatalf("approved keys = %d, want 1", len(keys))
	}
	if keys[0].OwnerUserID != owner.ID || keys[0].Metadata["created_by"] != requester.ID {
		t.Fatalf("approved key attribution = owner %q issuer %q", keys[0].OwnerUserID, keys[0].Metadata["created_by"])
	}
}

func TestUserRequestAuditIsScopedToOwnLogs(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "request-auditor",
		Name:     "Request Auditor",
		Email:    "request-auditor@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := store.CreateAdminUser(AdminUser{
		Username: "other-request-auditor",
		Name:     "Other Request Auditor",
		Email:    "other-request-auditor@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "other123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "User Audit Project", OwnerUserID: user.ID})
	otherProject := store.CreateProject(Project{Name: "Other Audit Project", OwnerUserID: otherUser.ID})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:     "user-owned-key",
		Status:   StatusActive,
		Metadata: map[string]string{"created_by": user.ID},
	}, "thk_user_audit")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := store.CreateAPIKey(otherProject.ID, APIKey{
		Name:     "other-owned-key",
		Status:   StatusActive,
		Metadata: map[string]string{"created_by": otherUser.ID},
	}, "thk_other_audit")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.db.Create(&RequestLog{
		ID:         "log_user_visible",
		RequestID:  "req_user_visible",
		ProjectID:  project.ID,
		APIKeyID:   key.ID,
		ModelName:  "gpt-4.1-mini",
		StatusCode: http.StatusOK,
		LatencyMS:  120,
		CreatedAt:  now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{
		ID:         "log_other_hidden",
		RequestID:  "req_other_hidden",
		ProjectID:  otherProject.ID,
		APIKeyID:   otherKey.ID,
		ModelName:  "gpt-4.1-mini",
		StatusCode: http.StatusOK,
		LatencyMS:  95,
		CreatedAt:  now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestPayloadLog{
		ID:           "payload_user_visible",
		RequestID:    "req_user_visible",
		RequestBody:  `{"model":"gpt-4.1-mini"}`,
		ResponseBody: `{"id":"chatcmpl_user"}`,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "request-auditor@tokenhub.local",
		"password": "user123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	logs := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests", nil, payload.Token)
	if logs.Code != http.StatusOK {
		t.Fatalf("expected user request audit 200, got %d: %s", logs.Code, logs.Body)
	}
	if !strings.Contains(logs.Body, "req_user_visible") || strings.Contains(logs.Body, "req_other_hidden") {
		t.Fatalf("request audit should only include user's logs: %s", logs.Body)
	}
	detail := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests/req_user_visible", nil, payload.Token)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body, "chatcmpl_user") {
		t.Fatalf("expected own request detail, got %d: %s", detail.Code, detail.Body)
	}
	hiddenDetail := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests/req_other_hidden", nil, payload.Token)
	if hiddenDetail.Code != http.StatusForbidden {
		t.Fatalf("expected hidden request detail 403, got %d: %s", hiddenDetail.Code, hiddenDetail.Body)
	}
	adminAudit := doJSON(t, app, http.MethodGet, "/api/admin/audit/events", nil, payload.Token)
	if adminAudit.Code != http.StatusForbidden {
		t.Fatalf("user should not read admin audit events, got %d: %s", adminAudit.Code, adminAudit.Body)
	}
}

func TestTeamLeaderUsageBreakdownIncludesMembers(t *testing.T) {
	store := NewMemoryStore()
	leader, err := store.CreateAdminUser(AdminUser{
		Username: "usage-leader",
		Name:     "Usage Leader",
		Email:    "usage-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_usage",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	memberA, err := store.CreateAdminUser(AdminUser{
		Username: "usage-member-a",
		Name:     "Usage Member A",
		Email:    "usage-member-a@tokenhub.local",
		Role:     "user",
		TeamID:   leader.TeamID,
		Status:   StatusActive,
	}, "member123456")
	if err != nil {
		t.Fatal(err)
	}
	memberB, err := store.CreateAdminUser(AdminUser{
		Username: "usage-member-b",
		Name:     "Usage Member B",
		Email:    "usage-member-b@tokenhub.local",
		Role:     "user",
		TeamID:   leader.TeamID,
		Status:   StatusActive,
	}, "member123456")
	if err != nil {
		t.Fatal(err)
	}
	otherMember, err := store.CreateAdminUser(AdminUser{
		Username: "usage-member-other",
		Name:     "Usage Member Other",
		Email:    "usage-member-other@tokenhub.local",
		Role:     "user",
		TeamID:   "team_other",
		Status:   StatusActive,
	}, "member123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Team Usage App", TeamID: leader.TeamID})
	otherProject := store.CreateProject(Project{Name: "Other Usage App", TeamID: otherMember.TeamID})
	keyA, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "member-a-key", Status: StatusActive, Metadata: map[string]string{"created_by": memberA.ID}}, "thk_usage_member_a")
	if err != nil {
		t.Fatal(err)
	}
	keyB, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "member-b-key", Status: StatusActive, Metadata: map[string]string{"created_by": memberB.ID}}, "thk_usage_member_b")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := store.CreateAPIKey(otherProject.ID, APIKey{Name: "other-member-key", Status: StatusActive, Metadata: map[string]string{"created_by": otherMember.ID}}, "thk_usage_other")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	records := []UsageRecord{
		{ID: "usage_member_a", RequestID: "req_member_a", ProjectID: project.ID, APIKeyID: keyA.ID, ModelName: "gpt-4.1-mini", TotalTokens: 100, CostUSD: 0.1, CreatedAt: now},
		{ID: "usage_member_b", RequestID: "req_member_b", ProjectID: project.ID, APIKeyID: keyB.ID, ModelName: "gpt-4.1-mini", TotalTokens: 250, CostUSD: 0.2, CreatedAt: now},
		{ID: "usage_other", RequestID: "req_other", ProjectID: otherProject.ID, APIKeyID: otherKey.ID, ModelName: "gpt-4.1-mini", TotalTokens: 999, CostUSD: 9.9, CreatedAt: now},
	}
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "usage-leader@tokenhub.local",
		"password": "leader123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	resp := doJSON(t, app, http.MethodGet, "/api/admin/usage/breakdown", nil, payload.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected team leader usage breakdown, got %d: %s", resp.Code, resp.Body)
	}
	var breakdown struct {
		Members []struct {
			ID           string `json:"id"`
			RequestCount int64  `json:"request_count"`
			TotalTokens  int64  `json:"total_tokens"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &breakdown); err != nil {
		t.Fatal(err)
	}
	totals := map[string]int64{}
	for _, row := range breakdown.Members {
		totals[row.ID] = row.TotalTokens
		if row.RequestCount != 1 {
			t.Fatalf("expected one request per member row, got %+v", row)
		}
	}
	if totals[memberA.ID] != 100 || totals[memberB.ID] != 250 {
		t.Fatalf("expected member totals for team members, got %+v", totals)
	}
	if _, ok := totals[otherMember.ID]; ok {
		t.Fatalf("other team member should not be included: %+v", totals)
	}
}

func TestAdminCreatesProviderModelAndRoute(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	providerResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "Local vLLM",
		"type":     "local",
		"base_url": "http://localhost:8000/v1",
		"status":   "active",
		"healthy":  true,
		"priority": 2,
	}, "")
	if providerResp.Code != http.StatusCreated {
		t.Fatalf("expected provider created, got %d: %s", providerResp.Code, providerResp.Body)
	}
	var providerPayload struct {
		Provider Provider `json:"provider"`
	}
	if err := json.Unmarshal([]byte(providerResp.Body), &providerPayload); err != nil {
		t.Fatal(err)
	}
	provider := providerPayload.Provider
	for _, upstreamModel := range []string{"qwen2.5-coder", "qwen2.5-coder-backup"} {
		store.AddProviderModel(ProviderModel{
			ProviderID:    provider.ID,
			UpstreamModel: upstreamModel,
			DisplayName:   upstreamModel,
			Status:        StatusActive,
		})
	}

	modelResp := doJSON(t, app, http.MethodPost, "/api/admin/models", map[string]any{
		"name":                    "local-coder",
		"family":                  "qwen",
		"modality":                "chat",
		"context_window":          32768,
		"input_price_usd_per_1m":  0.1,
		"output_price_usd_per_1m": 0.2,
	}, "")
	if modelResp.Code != http.StatusCreated {
		t.Fatalf("expected model created, got %d: %s", modelResp.Code, modelResp.Body)
	}

	routeResp := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name":     "local-coder",
		"provider_id":    provider.ID,
		"provider_model": "qwen2.5-coder",
		"priority":       1,
		"weight":         100,
		"status":         "active",
	}, "")
	if routeResp.Code != http.StatusCreated {
		t.Fatalf("expected route created, got %d: %s", routeResp.Code, routeResp.Body)
	}

	secondRouteResp := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name":     "local-coder",
		"provider_id":    provider.ID,
		"provider_model": "qwen2.5-coder-backup",
		"weight":         100,
		"status":         "active",
	}, "")
	if secondRouteResp.Code != http.StatusCreated {
		t.Fatalf("expected second route created, got %d: %s", secondRouteResp.Code, secondRouteResp.Body)
	}
	var secondRoute ModelRoute
	if err := json.Unmarshal([]byte(secondRouteResp.Body), &secondRoute); err != nil {
		t.Fatal(err)
	}
	if secondRoute.Priority != 2 {
		t.Fatalf("expected second route to append with priority 2, got %d: %s", secondRoute.Priority, secondRouteResp.Body)
	}

	routes := doJSON(t, app, http.MethodGet, "/api/admin/routing-rules", nil, "")
	if routes.Code != http.StatusOK {
		t.Fatalf("expected routes list, got %d: %s", routes.Code, routes.Body)
	}
	if !strings.Contains(routes.Body, "local-coder") || !strings.Contains(routes.Body, "qwen2.5-coder") {
		t.Fatalf("expected new route in list: %s", routes.Body)
	}
}

func TestAdminProviderConfigurationFailsEarlyAndPatchPreservesFields(t *testing.T) {
	app := newTestServer()

	invalid := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "Invalid Adapter",
		"type":     "openai-compatible",
		"base_url": "https://example.invalid/v1",
		"healthy":  true,
	}, "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body, `"code":"provider_adapter_missing"`) {
		t.Fatalf("expected unknown adapter to fail during creation, got %d: %s", invalid.Code, invalid.Body)
	}

	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":       "prv_patch_preserve",
		"name":     "Patch Preserve",
		"type":     ProviderOpenAICompatible,
		"base_url": "https://example.invalid/v1",
		"status":   StatusActive,
		"healthy":  true,
		"priority": 7,
		"headers":  map[string]string{"x-provider": "preserved"},
		"options":  map[string]string{"region": "test"},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected provider creation, got %d: %s", created.Code, created.Body)
	}

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/providers/prv_patch_preserve", map[string]any{
		"type": "deepseek",
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("expected partial provider patch, got %d: %s", updated.Code, updated.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(updated.Body), &result); err != nil {
		t.Fatal(err)
	}
	provider := result.Provider
	if provider.Type != "deepseek" ||
		provider.Name != "Patch Preserve" ||
		provider.BaseURL != "https://example.invalid/v1" ||
		provider.Status != StatusActive ||
		!provider.Healthy ||
		provider.Priority != 7 ||
		provider.Headers["x-provider"] != "preserved" ||
		provider.Options["region"] != "test" {
		t.Fatalf("partial patch erased provider fields: %+v", provider)
	}
}

func TestAdminProviderCatalogAndTemplateRouteMapping(t *testing.T) {
	app := newTestServer()

	catalogResp := doJSON(t, app, http.MethodGet, "/api/admin/provider-catalog/openai", nil, "")
	if catalogResp.Code != http.StatusOK {
		t.Fatalf("expected openai catalog, got %d: %s", catalogResp.Code, catalogResp.Body)
	}
	if !strings.Contains(catalogResp.Body, `"gpt-5"`) || !strings.Contains(catalogResp.Body, `"category":"openai"`) {
		t.Fatalf("expected openai model details: %s", catalogResp.Body)
	}

	createResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":      "openai",
		"id":              "prv_openai_test",
		"name":            "OpenAI Test",
		"base_url":        "https://api.openai.com/v1",
		"status":          "active",
		"healthy":         true,
		"create_routes":   true,
		"selected_models": []string{"gpt-5"},
	}, "")
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected template provider created, got %d: %s", createResp.Code, createResp.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(createResp.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Provider.ID != "prv_openai_test" || result.CreatedRoutes != 1 {
		t.Fatalf("unexpected route result: %s", createResp.Body)
	}

	models := doJSON(t, app, http.MethodGet, "/api/admin/models", nil, "")
	if models.Code != http.StatusOK {
		t.Fatalf("expected models list, got %d: %s", models.Code, models.Body)
	}
	if !strings.Contains(models.Body, `"gpt-5"`) || !strings.Contains(models.Body, `"claude-sonnet-5"`) {
		t.Fatalf("expected default model catalog: %s", models.Body)
	}

	routes := doJSON(t, app, http.MethodGet, "/api/admin/routing-rules", nil, "")
	if routes.Code != http.StatusOK {
		t.Fatalf("expected routes list, got %d: %s", routes.Code, routes.Body)
	}
	if !strings.Contains(routes.Body, `"provider_id":"prv_openai_test"`) || !strings.Contains(routes.Body, `"model_name":"gpt-5"`) || !strings.Contains(routes.Body, `"provider_model":"gpt-5"`) {
		t.Fatalf("expected mapped route: %s", routes.Body)
	}

	autoResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":     "openai",
		"id":             "prv_openai_auto",
		"name":           "OpenAI Auto",
		"base_url":       "https://api.openai.com/v1",
		"status":         "active",
		"healthy":        true,
		"model_category": "openai",
	}, "")
	if autoResp.Code != http.StatusCreated {
		t.Fatalf("expected auto provider created, got %d: %s", autoResp.Code, autoResp.Body)
	}
	var autoResult ProviderCreateResult
	if err := json.Unmarshal([]byte(autoResp.Body), &autoResult); err != nil {
		t.Fatal(err)
	}
	if autoResult.CreatedRoutes < 2 {
		t.Fatalf("expected default openai routes, got %d: %s", autoResult.CreatedRoutes, autoResp.Body)
	}
	hasGPT5 := false
	for _, modelName := range autoResult.ModelNames {
		if modelName == "gpt-5" {
			hasGPT5 = true
			break
		}
	}
	if !hasGPT5 {
		t.Fatalf("expected auto-created gpt-5 route: %s", autoResp.Body)
	}
	routesAfterAuto := doJSON(t, app, http.MethodGet, "/api/admin/routing-rules", nil, "")
	if routesAfterAuto.Code != http.StatusOK {
		t.Fatalf("expected routes list after auto provider, got %d: %s", routesAfterAuto.Code, routesAfterAuto.Body)
	}
	var routeList struct {
		Data []ModelRoute `json:"data"`
	}
	if err := json.Unmarshal([]byte(routesAfterAuto.Body), &routeList); err != nil {
		t.Fatal(err)
	}
	gpt5Priorities := map[string]int{}
	for _, route := range routeList.Data {
		if route.ModelName == "gpt-5" {
			gpt5Priorities[route.ProviderID] = route.Priority
		}
	}
	if gpt5Priorities["prv_openai_test"] != 1 || gpt5Priorities["prv_openai_auto"] != 2 {
		t.Fatalf("expected gpt-5 provider routes to append by priority, got %#v", gpt5Priorities)
	}

	autoAgainResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":     "openai",
		"id":             "prv_openai_auto",
		"name":           "OpenAI Auto",
		"base_url":       "https://api.openai.com/v1",
		"status":         "active",
		"healthy":        true,
		"model_category": "openai",
	}, "")
	if autoAgainResp.Code != http.StatusCreated {
		t.Fatalf("expected idempotent auto provider create, got %d: %s", autoAgainResp.Code, autoAgainResp.Body)
	}
	var autoAgainResult ProviderCreateResult
	if err := json.Unmarshal([]byte(autoAgainResp.Body), &autoAgainResult); err != nil {
		t.Fatal(err)
	}
	if autoAgainResult.CreatedRoutes != 0 {
		t.Fatalf("expected existing routes to be preserved, got %d: %s", autoAgainResult.CreatedRoutes, autoAgainResp.Body)
	}

	off := false
	disabledReq := map[string]any{
		"catalog_id":     "openai",
		"id":             "prv_openai_no_routes",
		"name":           "OpenAI No Routes",
		"base_url":       "https://api.openai.com/v1",
		"status":         "active",
		"healthy":        true,
		"model_category": "openai",
		"create_routes":  off,
	}
	disabledResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", disabledReq, "")
	if disabledResp.Code != http.StatusCreated {
		t.Fatalf("expected disabled auto provider created, got %d: %s", disabledResp.Code, disabledResp.Body)
	}
	var disabledResult ProviderCreateResult
	if err := json.Unmarshal([]byte(disabledResp.Body), &disabledResult); err != nil {
		t.Fatal(err)
	}
	if disabledResult.CreatedRoutes != 0 {
		t.Fatalf("expected no routes when create_routes is false: %s", disabledResp.Body)
	}
}

func TestAdminKimiCodingTemplateMapsOfficialModels(t *testing.T) {
	store := NewMemoryStore()
	config := Config{
		AdminToken:             "dev_admin_token",
		BootstrapAdminPassword: "kimi-coding-test-password",
		ModelCatalogFile:       "../../../data/model-catalog.yaml",
		ProviderCatalogFile:    "../../../data/provider-catalog.json",
	}
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, config)
	if _, err := server.InitializeProviderCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := server.Handler()

	catalogResp := doJSON(t, app, http.MethodGet, "/api/admin/provider-catalog/kimi-for-coding", nil, "")
	if catalogResp.Code != http.StatusOK {
		t.Fatalf("expected Kimi catalog, got %d: %s", catalogResp.Code, catalogResp.Body)
	}
	var catalogPayload struct {
		Data ProviderCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal([]byte(catalogResp.Body), &catalogPayload); err != nil {
		t.Fatal(err)
	}
	expectedModels := map[string]string{
		"k3":                        "kimi-k3",
		"k3-256k":                   "kimi-k3-256k",
		"kimi-for-coding":           "kimi-k2.7-code",
		"kimi-for-coding-highspeed": "kimi-k2.7-code-highspeed",
	}
	if len(catalogPayload.Data.Models) != len(expectedModels) {
		t.Fatalf("expected %d Kimi models, got %+v", len(expectedModels), catalogPayload.Data.Models)
	}
	for _, model := range catalogPayload.Data.Models {
		if canonical, ok := expectedModels[model.ID]; !ok || canonical != model.CanonicalName {
			t.Fatalf("unexpected Kimi catalog model: %+v", model)
		}
	}

	createResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":      "kimi-for-coding",
		"id":              "prv_kimi_coding",
		"name":            "Kimi Coding",
		"base_url":        "https://api.kimi.com/coding/v1",
		"api_key":         "test-key",
		"status":          "active",
		"healthy":         true,
		"model_category":  "kimi",
		"create_routes":   true,
		"selected_models": []string{"k3", "k3-256k", "kimi-for-coding", "kimi-for-coding-highspeed"},
	}, "")
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected Kimi provider creation, got %d: %s", createResp.Code, createResp.Body)
	}

	expectedRoutes := map[string]string{
		"kimi-k3":                  "k3",
		"kimi-k3-256k":             "k3-256k",
		"kimi-k2.7-code":           "kimi-for-coding",
		"kimi-k2.7-code-highspeed": "kimi-for-coding-highspeed",
	}
	for _, route := range store.ListRoutes() {
		if route.ProviderID != "prv_kimi_coding" {
			continue
		}
		if upstream, ok := expectedRoutes[route.ModelName]; !ok || upstream != route.ProviderModel {
			t.Fatalf("unexpected Kimi route: %+v", route)
		}
		delete(expectedRoutes, route.ModelName)
	}
	if len(expectedRoutes) != 0 {
		t.Fatalf("missing Kimi routes: %+v", expectedRoutes)
	}
}

func TestAdminCustomProviderCatalogLoadsUpstreamModels(t *testing.T) {
	app := newTestServer()
	seenAuth := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		seenAuth = r.Header.Get("authorization")
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "gpt-4.1-mini", "object": "model", "owned_by": "agnes"},
				{"id": "agnes-special", "object": "model", "owned_by": "agnes"},
			},
		})
	}))
	defer upstream.Close()

	catalogResp := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"name":     "Agnes",
		"type":     ProviderOpenAICompatible,
		"base_url": upstream.URL + "/v1",
		"api_key":  "upstream-secret",
	}, "")
	if catalogResp.Code != http.StatusOK {
		t.Fatalf("expected custom provider catalog, got %d: %s", catalogResp.Code, catalogResp.Body)
	}
	if seenAuth != "Bearer upstream-secret" {
		t.Fatalf("expected upstream authorization header, got %q", seenAuth)
	}
	var catalogPayload struct {
		Data ProviderCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal([]byte(catalogResp.Body), &catalogPayload); err != nil {
		t.Fatal(err)
	}
	if catalogPayload.Data.Source != "custom-upstream" || catalogPayload.Data.ModelsCount != 2 {
		t.Fatalf("expected upstream custom models, got %+v", catalogPayload.Data)
	}
	if catalogPayload.Data.Models[0].ID == "gpt-5" || !strings.Contains(catalogResp.Body, `"agnes-special"`) {
		t.Fatalf("expected real upstream models instead of standard OpenAI catalog: %s", catalogResp.Body)
	}

	createResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":      "custom",
		"id":              "prv_agnes",
		"name":            "Agnes",
		"type":            ProviderOpenAICompatible,
		"base_url":        upstream.URL + "/v1",
		"api_key":         "upstream-secret",
		"status":          "active",
		"healthy":         true,
		"create_routes":   true,
		"selected_models": []string{"gpt-4.1-mini"},
		"custom_models":   catalogPayload.Data.Models,
	}, "")
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected custom provider created, got %d: %s", createResp.Code, createResp.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(createResp.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.CreatedRoutes != 1 || result.ModelNames[0] != "gpt-4.1-mini" {
		t.Fatalf("expected one custom upstream route, got %s", createResp.Body)
	}

	seenAuth = ""
	savedCatalogResp := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"provider_id": "prv_agnes",
	}, "")
	if savedCatalogResp.Code != http.StatusOK {
		t.Fatalf("expected saved custom provider catalog, got %d: %s", savedCatalogResp.Code, savedCatalogResp.Body)
	}
	if seenAuth != "Bearer upstream-secret" {
		t.Fatalf("expected saved provider key to be used, got %q", seenAuth)
	}
}

func TestProviderCatalogUsesStandardModelCategories(t *testing.T) {
	entries := []ProviderCatalogEntry{
		{
			ID: "mixed",
			Models: []ProviderCatalogModel{
				{ID: "deepseekv4", DisplayName: "DeepSeek V4"},
				{ID: "Phi-4-multimodal-instruct"},
				{ID: "agent-max-preview"},
			},
		},
	}

	categories, counts := catalogCategorySummary(entries[0].Models)
	joined := strings.Join(categories, ",")
	if joined != "custom,deepseek,microsoft" {
		t.Fatalf("expected standard categories, got %s", joined)
	}
	if counts["deepseek"] != 1 || counts["microsoft"] != 1 || counts["custom"] != 1 {
		t.Fatalf("unexpected standard category counts: %+v", counts)
	}
	if counts["agent"] != 0 || counts["phi"] != 0 {
		t.Fatalf("unexpected raw long-tail categories: %+v", counts)
	}
	if normalizeModelLookupName("DeepSeekV4") != "deepseek-v4" || normalizeModelLookupName("openai/gpt5") != "gpt-5" {
		t.Fatalf("expected compact provider model names to normalize")
	}
	if got := normalizeProviderBaseURL("302ai", "https://api.highwayapi.ai/openai"); got != "https://api.highwayapi.ai/openai/v1" {
		t.Fatalf("expected JieKou OpenAI-compatible base URL to include /v1, got %s", got)
	}
	if got := normalizeProviderBaseURL("dmxapi", "https://www.dmxapi.cn"); got != "https://www.dmxapi.cn/v1" {
		t.Fatalf("expected dmxapi OpenAI-compatible base URL to include /v1, got %s", got)
	}
}

func TestAdminImportsProviderModelWithoutPublishing(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	imported := doJSON(t, app, http.MethodPost, "/api/admin/provider-models/import", map[string]any{
		"provider_id": "prv_mock",
		"publish":     false,
		"models": []map[string]any{
			{
				"id":             "vendor/private-alpha",
				"display_name":   "Private Alpha",
				"category":       "custom",
				"type":           "chat",
				"context_window": 131072,
				"capabilities":   []string{"chat", "tools"},
			},
		},
	}, "")
	if imported.Code != http.StatusCreated {
		t.Fatalf("expected provider model import 201, got %d: %s", imported.Code, imported.Body)
	}

	providerModels := doJSON(t, app, http.MethodGet, "/api/admin/provider-models", nil, "")
	if providerModels.Code != http.StatusOK || !strings.Contains(providerModels.Body, `"upstream_model":"vendor/private-alpha"`) {
		t.Fatalf("expected imported provider model inventory: %d %s", providerModels.Code, providerModels.Body)
	}
	if strings.Contains(providerModels.Body, `"published_model":"vendor/private-alpha"`) {
		t.Fatalf("unpublished provider model must not claim an external model: %s", providerModels.Body)
	}
	for _, route := range store.ListRoutes() {
		if route.ProviderModel == "vendor/private-alpha" {
			t.Fatalf("import-only operation must not create a route: %+v", route)
		}
	}
}

func TestAdminPublishesProviderModelWithCustomExternalName(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Alias Mapping Test", Status: StatusActive})
	if _, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Alias Test Key"}, "thk_alias_models"); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	imported := doJSON(t, app, http.MethodPost, "/api/admin/provider-models/import", map[string]any{
		"provider_id": "prv_mock",
		"publish":     true,
		"external_names": map[string]string{
			"vendor/gpt-4.5": "DeepSeek",
		},
		"models": []map[string]any{
			{
				"id":             "vendor/gpt-4.5",
				"display_name":   "GPT 4.5",
				"category":       "openai",
				"type":           "chat",
				"context_window": 128000,
				"capabilities":   []string{"chat", "tools"},
			},
		},
	}, "")
	if imported.Code != http.StatusCreated {
		t.Fatalf("expected published provider model import 201, got %d: %s", imported.Code, imported.Body)
	}
	if !strings.Contains(imported.Body, `"created_models":1`) || !strings.Contains(imported.Body, `"created_routes":1`) {
		t.Fatalf("expected one external model and mapping: %s", imported.Body)
	}

	models := doJSON(t, app, http.MethodGet, "/v1/models", nil, "thk_alias_models")
	if models.Code != http.StatusOK || !strings.Contains(models.Body, `"id":"DeepSeek"`) {
		t.Fatalf("expected custom external model to be published: %d %s", models.Code, models.Body)
	}
	routes := store.ListRoutes()
	found := false
	for _, route := range routes {
		if route.ModelName == "DeepSeek" && route.ProviderID == "prv_mock" && route.ProviderModel == "vendor/gpt-4.5" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected DeepSeek to map to provider model vendor/gpt-4.5: %+v", routes)
	}
}

func TestAdminProviderImportUsesExactExternalModelIdentity(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Exact Alias Test", Status: StatusActive})
	if _, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Exact Alias Key"}, "thk_exact_alias"); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	imported := doJSON(t, app, http.MethodPost, "/api/admin/provider-models/import", map[string]any{
		"provider_id": "prv_mock",
		"publish":     true,
		"external_names": map[string]string{
			"vendor/gpt-4.1-mini": "openai/gpt-4.1-mini",
		},
		"models": []map[string]any{{
			"id":           "vendor/gpt-4.1-mini",
			"display_name": "GPT 4.1 Mini Vendor Deployment",
			"type":         "chat",
		}},
	}, "")
	if imported.Code != http.StatusCreated {
		t.Fatalf("expected exact alias import 201, got %d: %s", imported.Code, imported.Body)
	}
	if _, ok := modelByNameForTest(store.ListModels(), "openai/gpt-4.1-mini"); !ok {
		t.Fatalf("external aliases must use exact API identity even when their canonical name already exists: %+v", store.ListModels())
	}
	models := doJSON(t, app, http.MethodGet, "/v1/models", nil, "thk_exact_alias")
	if models.Code != http.StatusOK || !strings.Contains(models.Body, `"id":"openai/gpt-4.1-mini"`) {
		t.Fatalf("expected exact slash-qualified external alias in /v1/models: %d %s", models.Code, models.Body)
	}
}

func TestAdminProviderImportKeepsDistinctExactAliases(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	for _, externalName := range []string{"gpt-4.1-mini", "openai/gpt-4.1-mini"} {
		imported := doJSON(t, app, http.MethodPost, "/api/admin/provider-models/import", map[string]any{
			"provider_id": "prv_mock",
			"publish":     true,
			"external_names": map[string]string{
				"vendor/gpt-4.1-mini": externalName,
			},
			"models": []map[string]any{{
				"id":           "vendor/gpt-4.1-mini",
				"display_name": "GPT 4.1 Mini Vendor Deployment",
				"type":         "chat",
			}},
		}, "")
		if imported.Code != http.StatusCreated {
			t.Fatalf("expected exact alias import 201 for %q, got %d: %s", externalName, imported.Code, imported.Body)
		}
	}

	found := map[string]bool{}
	for _, route := range store.ListRoutes() {
		if route.ProviderID == "prv_mock" && route.ProviderModel == "vendor/gpt-4.1-mini" {
			found[route.ModelName] = true
		}
	}
	if !found["gpt-4.1-mini"] || !found["openai/gpt-4.1-mini"] {
		t.Fatalf("exact external aliases must keep distinct routes: %+v", store.ListRoutes())
	}
}

func TestAdminProviderImportRepublishesExistingExternalModel(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "DeepSeek", Modality: "chat", Status: StatusDisabled})
	app := New(store).Handler()

	imported := doJSON(t, app, http.MethodPost, "/api/admin/provider-models/import", map[string]any{
		"provider_id": "prv_mock",
		"publish":     true,
		"external_names": map[string]string{
			"vendor/gpt-4.5": "DeepSeek",
		},
		"models": []map[string]any{{"id": "vendor/gpt-4.5", "type": "chat"}},
	}, "")
	if imported.Code != http.StatusCreated {
		t.Fatalf("expected existing external model publication 201, got %d: %s", imported.Code, imported.Body)
	}
	model, ok := modelByNameForTest(store.ListModels(), "DeepSeek")
	if !ok || model.Status != StatusActive {
		t.Fatalf("publish import must reactivate an existing external model: %+v", model)
	}
}

func TestAdminProviderCreationImportsSelectedModelsWithoutPublishing(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":              "prv_inventory_only",
		"catalog_id":      "openai",
		"name":            "OpenAI Inventory Only",
		"type":            ProviderOpenAI,
		"base_url":        "https://api.openai.com/v1",
		"status":          StatusActive,
		"create_routes":   false,
		"selected_models": []string{"gpt-4.1-mini"},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected provider creation 201, got %d: %s", created.Code, created.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(created.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ImportedModels != 1 || result.CreatedRoutes != 0 {
		t.Fatalf("expected inventory import without publication, got %+v", result)
	}
	for _, route := range store.ListRoutes() {
		if route.ProviderID == "prv_inventory_only" {
			t.Fatalf("inventory-only provider creation must not publish a route: %+v", route)
		}
	}
	found := false
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_inventory_only" && model.UpstreamModel == "gpt-4.1-mini" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected selected model in provider inventory")
	}
}

func TestAdminRejectsDeletingProviderModelUsedByRoute(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	var providerModel ProviderModel
	for _, item := range store.ListProviderModels() {
		if item.ProviderID == "prv_mock" && item.UpstreamModel == "mock-chat" {
			providerModel = item
			break
		}
	}
	if providerModel.ID == "" {
		t.Fatal("expected route backfill to create provider inventory")
	}

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/provider-models/"+providerModel.ID, nil, "")
	if deleted.Code != http.StatusConflict || !strings.Contains(deleted.Body, "provider_model_in_use") {
		t.Fatalf("expected in-use conflict, got %d: %s", deleted.Code, deleted.Body)
	}
}

func TestAdminUpdatesProviderModelInventory(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	providerModel := store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "vendor/editable-model",
		DisplayName:   "Editable Model",
		Modality:      "chat",
		Status:        StatusActive,
	})
	app := New(store).Handler()

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/provider-models/"+providerModel.ID, map[string]any{
		"display_name":   "Edited Provider Model",
		"context_window": 131072,
		"capabilities":   []string{"chat", "tools"},
		"status":         StatusDisabled,
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("expected provider model patch 200, got %d: %s", updated.Code, updated.Body)
	}
	var result ProviderModel
	if err := json.Unmarshal([]byte(updated.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != providerModel.ID ||
		result.ProviderID != "prv_mock" ||
		result.UpstreamModel != "vendor/editable-model" ||
		result.DisplayName != "Edited Provider Model" ||
		result.ContextWindow != 131072 ||
		result.Status != StatusDisabled ||
		!slices.Equal(result.Capabilities, []string{"chat", "tools"}) {
		t.Fatalf("unexpected updated provider model: %+v", result)
	}
}

func TestAdminDeletesUnusedProviderModelInventory(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	providerModel := store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "vendor/unused-model",
		DisplayName:   "Unused Model",
		Status:        StatusActive,
	})
	app := New(store).Handler()

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/provider-models/"+providerModel.ID, nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("expected unused provider model delete 204, got %d: %s", deleted.Code, deleted.Body)
	}
	for _, item := range store.ListProviderModels() {
		if item.ID == providerModel.ID {
			t.Fatalf("deleted provider model remains in inventory: %+v", item)
		}
	}
}

func TestAdminDeletingProviderRemovesProviderModelInventory(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:      "prv_inventory_cascade",
		Name:    "Inventory Cascade Provider",
		Type:    ProviderMock,
		Status:  StatusActive,
		Healthy: true,
	})
	store.AddProviderModel(ProviderModel{
		ProviderID:    provider.ID,
		UpstreamModel: "vendor/cascade-model",
		DisplayName:   "Cascade Model",
		Status:        StatusActive,
	})
	app := New(store).Handler()

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/providers/"+provider.ID, nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("expected provider delete 204, got %d: %s", deleted.Code, deleted.Body)
	}
	for _, item := range store.ListProviderModels() {
		if item.ProviderID == provider.ID {
			t.Fatalf("provider deletion left inventory behind: %+v", item)
		}
	}
}

func TestAdminRouteUpdateRequiresImportedProviderModelInventory(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	var route ModelRoute
	for _, item := range store.ListRoutes() {
		if item.ProviderID == "prv_mock" && item.ProviderModel == "mock-chat" {
			route = item
			break
		}
	}
	if route.ID == "" {
		t.Fatal("expected demo route for provider inventory update")
	}
	app := New(store).Handler()

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/routing-rules/"+route.ID, map[string]any{
		"provider_model": "vendor/changed-upstream",
	}, "")
	if updated.Code != http.StatusConflict || !strings.Contains(updated.Body, "provider_model_not_imported") {
		t.Fatalf("expected unimported provider model conflict, got %d: %s", updated.Code, updated.Body)
	}
	store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "vendor/changed-upstream",
		DisplayName:   "Changed Upstream",
		Status:        StatusActive,
	})
	updated = doJSON(t, app, http.MethodPatch, "/api/admin/routing-rules/"+route.ID, map[string]any{
		"provider_model": "vendor/changed-upstream",
	}, "")
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body, `"provider_model":"vendor/changed-upstream"`) {
		t.Fatalf("expected imported provider model patch 200, got %d: %s", updated.Code, updated.Body)
	}
}

func TestAdminCreatesExternalModelWithValidatedImportedRoute(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	invalid := doJSON(t, app, http.MethodPost, "/api/admin/models", map[string]any{
		"name":   "invalid-partial-model",
		"status": StatusActive,
		"routes": []map[string]any{{
			"provider_id":    "missing-provider",
			"provider_model": "gpt-4.5",
			"status":         StatusActive,
		}},
	}, "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body, "route_provider_not_found") {
		t.Fatalf("expected nested route validation failure, got %d: %s", invalid.Code, invalid.Body)
	}
	if _, ok := modelByNameForTest(store.ListModels(), "invalid-partial-model"); ok {
		t.Fatal("route validation failure must not leave a partial external model")
	}
	store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "gpt-4.5",
		DisplayName:   "GPT 4.5",
		Status:        StatusActive,
	})

	created := doJSON(t, app, http.MethodPost, "/api/admin/models", map[string]any{
		"name":         "DeepSeek",
		"family":       "deepseek",
		"modality":     "chat",
		"status":       StatusActive,
		"capabilities": []string{"chat", "tools"},
		"routes": []map[string]any{{
			"provider_id":    "prv_mock",
			"provider_model": "gpt-4.5",
			"status":         StatusActive,
		}},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected model and route creation 201, got %d: %s", created.Code, created.Body)
	}
	foundRoute := false
	for _, route := range store.ListRoutes() {
		if route.ModelName == "DeepSeek" && route.ProviderID == "prv_mock" && route.ProviderModel == "gpt-4.5" {
			foundRoute = route.Priority > 0
		}
	}
	if !foundRoute {
		t.Fatalf("expected prioritized DeepSeek alias route: %+v", store.ListRoutes())
	}
	foundInventory := false
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_mock" && model.UpstreamModel == "gpt-4.5" {
			foundInventory = true
		}
	}
	if !foundInventory {
		t.Fatal("expected manual nested route to retain imported Provider inventory")
	}
}

func TestAdminRejectsUnimportedAndDuplicateProviderModelRoutes(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	unimported := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name":     "gpt-4.1-mini",
		"provider_id":    "prv_mock",
		"provider_model": "vendor/not-imported",
		"status":         StatusActive,
	}, "")
	if unimported.Code != http.StatusConflict || !strings.Contains(unimported.Body, "provider_model_not_imported") {
		t.Fatalf("expected unimported provider model conflict, got %d: %s", unimported.Code, unimported.Body)
	}

	duplicate := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name":     "gpt-4.1-mini",
		"provider_id":    "prv_mock",
		"provider_model": "mock-chat",
		"status":         StatusActive,
	}, "")
	if duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body, "model_route_conflict") {
		t.Fatalf("expected duplicate model route conflict, got %d: %s", duplicate.Code, duplicate.Body)
	}
}

func TestExternalModelRoleSurvivesCandidateCatalogRefresh(t *testing.T) {
	store := NewMemoryStore()
	external := store.AddModel(Model{
		Name:     "catalog-backed-external",
		Modality: "chat",
		Metadata: map[string]string{
			"source":              "tokenhub-standard-catalog",
			modelDirectoryRoleKey: modelDirectoryRoleExternal,
		},
		Status: StatusDisabled,
	})
	store.AddModel(Model{
		Name:     external.Name,
		Modality: "chat",
		Metadata: map[string]string{"source": "tokenhub-standard-catalog"},
		Status:   StatusActive,
	})

	model, ok := modelByNameForTest(store.ListModels(), external.Name)
	if !ok || model.Metadata[modelDirectoryRoleKey] != modelDirectoryRoleExternal || model.Status != StatusDisabled {
		t.Fatalf("candidate refresh must preserve external role and publication state: %+v", model)
	}
}

func TestAdminCreatesProviderResource(t *testing.T) {
	app := newTestServer()

	resourceResp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
		"provider_id":     "prv_mock",
		"name":            "Mock Backup Resource",
		"resource_type":   "mock",
		"region":          "sg",
		"environment":     "backup",
		"status":          "active",
		"healthy":         true,
		"priority":        2,
		"weight":          80,
		"rate_limit_rpm":  600,
		"token_limit_tpm": 90000,
		"api_key":         "secret-resource-key",
	}, "")
	if resourceResp.Code != http.StatusCreated {
		t.Fatalf("expected provider resource created, got %d: %s", resourceResp.Code, resourceResp.Body)
	}
	if strings.Contains(resourceResp.Body, "secret-resource-key") {
		t.Fatalf("resource secret should not be returned: %s", resourceResp.Body)
	}
	var resource ProviderResource
	if err := json.Unmarshal([]byte(resourceResp.Body), &resource); err != nil {
		t.Fatal(err)
	}
	if resource.ID == "" || resource.ProviderID != "prv_mock" || resource.APIKey != "" {
		t.Fatalf("unexpected provider resource response: %s", resourceResp.Body)
	}

	resources := doJSON(t, app, http.MethodGet, "/api/admin/provider-resources", nil, "")
	if resources.Code != http.StatusOK {
		t.Fatalf("expected resources list, got %d: %s", resources.Code, resources.Body)
	}
	if !strings.Contains(resources.Body, "Mock Backup Resource") || strings.Contains(resources.Body, "secret-resource-key") {
		t.Fatalf("unexpected resources list: %s", resources.Body)
	}

	health := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/health", map[string]any{
		"healthy": false,
	}, "")
	if health.Code != http.StatusOK {
		t.Fatalf("expected resource health update, got %d: %s", health.Code, health.Body)
	}
	if !strings.Contains(health.Body, `"healthy":false`) {
		t.Fatalf("expected unhealthy resource: %s", health.Body)
	}
}

func TestAdminDeletesProviderAccountRuntimeData(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		Name:    "Delete Account Provider",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID:   provider.ID,
		Name:         "Delete Account Resource",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials: &ProviderResourceCredentials{
			AccessToken:  "delete-account-access-token",
			RefreshToken: "delete-account-refresh-token",
			AccountID:    "delete-account-id",
			Email:        "delete.account@example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	route := store.AddRoute(ModelRoute{
		ModelName:          "delete-account-model",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "delete-account-model",
		Status:             StatusActive,
	})
	now := time.Now().UTC()
	for _, record := range []any{
		&InFlightLease{ID: "lease_delete_account", ScopeType: "provider_resource", ScopeID: resource.ID, ExpiresAt: now.Add(time.Minute)},
		&ProviderResourceBucket{ResourceID: resource.ID, Bucket: "minute", Requests: 1, Tokens: 2, UpdatedAt: now},
		&ProviderResourceObservation{ResourceID: resource.ID, AdapterType: ProviderOpenAICodex, QuotaSnapshot: `{"plan_type":"pro"}`, QuotaFetchedAt: &now, UpdatedAt: now},
		&ProviderObservation{ID: "obs_delete_account", ProviderID: provider.ID, ResourceID: resource.ID, AdapterType: ProviderOpenAICodex, Source: "real_request", Operation: "responses", Success: true, ObservedAt: now},
		&AdapterSessionBinding{ID: "binding_delete_account", AdapterType: ProviderOpenAICodex, AffinityKind: AffinityKindCodexSession, ProviderID: provider.ID, AffinityKeyHash: "delete-account-affinity", ResourceID: resource.ID, LastUsedAt: now},
	} {
		if err := store.db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}

	app := New(store).Handler()
	resp := doJSON(t, app, http.MethodDelete, "/api/admin/provider-resources/"+resource.ID, nil, "")
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected account delete 204, got %d: %s", resp.Code, resp.Body)
	}

	checks := []struct {
		name  string
		model any
		where string
		args  []any
	}{
		{name: "provider resource", model: &ProviderResource{}, where: "id = ?", args: []any{resource.ID}},
		{name: "in-flight lease", model: &InFlightLease{}, where: "scope_type = ? AND scope_id = ?", args: []any{"provider_resource", resource.ID}},
		{name: "rate-limit bucket", model: &ProviderResourceBucket{}, where: "resource_id = ?", args: []any{resource.ID}},
		{name: "resource observation", model: &ProviderResourceObservation{}, where: "resource_id = ?", args: []any{resource.ID}},
		{name: "provider observation", model: &ProviderObservation{}, where: "resource_id = ?", args: []any{resource.ID}},
		{name: "session binding", model: &AdapterSessionBinding{}, where: "resource_id = ?", args: []any{resource.ID}},
	}
	for _, check := range checks {
		var count int64
		if err := store.db.Model(check.model).Where(check.where, check.args...).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("expected %s data to be deleted, found %d row(s)", check.name, count)
		}
	}
	var detachedRoute ModelRoute
	if err := store.db.First(&detachedRoute, "id = ?", route.ID).Error; err != nil {
		t.Fatal(err)
	}
	if detachedRoute.ProviderResourceID != "" {
		t.Fatalf("expected route to be detached from deleted account, got %q", detachedRoute.ProviderResourceID)
	}
}

func TestAdminRejectsDuplicateProviderResourceName(t *testing.T) {
	app := newTestServer()
	for index, name := range []string{"OpenAI Codex Primary Account", "  openai codex primary account  "} {
		resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
			"id":            "rsrc_openai_account_" + strconv.Itoa(index+1),
			"provider_id":   "prv_mock",
			"name":          name,
			"resource_type": ProviderResourceOpenAISubscription,
			"status":        StatusActive,
			"healthy":       true,
		}, "")
		if index == 0 && resp.Code != http.StatusCreated {
			t.Fatalf("expected first provider resource created, got %d: %s", resp.Code, resp.Body)
		}
		if index == 1 {
			if resp.Code != http.StatusConflict {
				t.Fatalf("expected duplicate provider resource name conflict, got %d: %s", resp.Code, resp.Body)
			}
			if !strings.Contains(resp.Body, `"code":"provider_resource_name_conflict"`) {
				t.Fatalf("expected provider resource name conflict code, got: %s", resp.Body)
			}
		}
	}

	secondary := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
		"id":            "rsrc_openai_account_secondary",
		"provider_id":   "prv_mock",
		"name":          "OpenAI Codex Secondary Account",
		"resource_type": ProviderResourceOpenAISubscription,
		"status":        StatusActive,
		"healthy":       true,
	}, "")
	if secondary.Code != http.StatusCreated {
		t.Fatalf("expected secondary provider resource created, got %d: %s", secondary.Code, secondary.Body)
	}
	rename := doJSON(t, app, http.MethodPatch, "/api/admin/provider-resources/rsrc_openai_account_secondary", map[string]any{
		"name": " OPENAI CODEX PRIMARY ACCOUNT ",
	}, "")
	if rename.Code != http.StatusConflict || !strings.Contains(rename.Body, `"code":"provider_resource_name_conflict"`) {
		t.Fatalf("expected provider resource rename conflict, got %d: %s", rename.Code, rename.Body)
	}
}

func TestAdminCreatesOpenAISubscriptionProviderResource(t *testing.T) {
	store := NewMemoryStore()
	store.AddProvider(Provider{
		ID:      "prv_openai_sub",
		Name:    "OpenAI Subscription Pool",
		Type:    ProviderOpenAI,
		Status:  StatusActive,
		Healthy: true,
	})
	app := New(store).Handler()
	idToken := testJWT(map[string]any{
		"email": "codex.user@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acc_openai_sub",
			"chatgpt_user_id":    "usr_openai_sub",
			"chatgpt_plan_type":  "plus",
			"user_id":            "user_openai_sub",
			"organizations": []map[string]any{
				{"id": "org_openai_default", "is_default": true},
			},
		},
	})

	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
		"provider_id":   "prv_openai_sub",
		"name":          "OpenAI Plus Account A",
		"resource_type": ProviderResourceOpenAISubscription,
		"status":        StatusActive,
		"healthy":       true,
		"priority":      1,
		"weight":        100,
		"credentials": map[string]any{
			"auth_type":     "oauth",
			"access_token":  "access-secret",
			"refresh_token": "refresh-secret",
			"id_token":      idToken,
			"client_id":     "app_EMoamEEZ73f0CkXaXp7hrann",
			"scopes":        "openid profile email offline_access",
		},
	}, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected openai subscription resource created, got %d: %s", resp.Code, resp.Body)
	}
	for _, secret := range []string{"access-secret", "refresh-secret", idToken} {
		if strings.Contains(resp.Body, secret) {
			t.Fatalf("resource response leaked secret %q: %s", secret, resp.Body)
		}
	}
	if !strings.Contains(resp.Body, `"credential_summary"`) ||
		!strings.Contains(resp.Body, `"account_email":"codex.user@example.com"`) ||
		!strings.Contains(resp.Body, `"organization_id":"org_openai_default"`) ||
		!strings.Contains(resp.Body, `"has_refresh_token":"true"`) {
		t.Fatalf("expected OpenAI account summary, got: %s", resp.Body)
	}

	var resource ProviderResource
	if err := json.Unmarshal([]byte(resp.Body), &resource); err != nil {
		t.Fatal(err)
	}
	var persisted ProviderResource
	if err := store.db.First(&persisted, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.APIKey == "access-secret" || !strings.HasPrefix(persisted.APIKey, "enc:v1:") {
		t.Fatalf("access token should be stored encrypted, got %q", persisted.APIKey)
	}
	if persisted.CredentialBlob == "" || persisted.CredentialBlob == "refresh-secret" || !strings.HasPrefix(persisted.CredentialBlob, "enc:v1:") {
		t.Fatalf("refresh token blob should be stored encrypted, got %q", persisted.CredentialBlob)
	}

	list := doJSON(t, app, http.MethodGet, "/api/admin/provider-resources", nil, "")
	if list.Code != http.StatusOK {
		t.Fatalf("expected resources list, got %d: %s", list.Code, list.Body)
	}
	for _, secret := range []string{"access-secret", "refresh-secret", idToken} {
		if strings.Contains(list.Body, secret) {
			t.Fatalf("resource list leaked secret %q: %s", secret, list.Body)
		}
	}
}

func TestProviderCredentialsAreEncryptedAndUsable(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Encrypted Credentials App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "encrypted-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_encrypted")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:      "prv_encrypted",
		Name:    "Encrypted Provider",
		Type:    "capture",
		APIKey:  "provider-secret",
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_encrypted",
		ProviderID:   provider.ID,
		Name:         "Encrypted Resource",
		ResourceType: "api_key",
		APIKey:       "resource-secret",
		Status:       StatusActive,
		Healthy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var persisted ProviderResource
	if err := store.db.First(&persisted, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.APIKey == "resource-secret" || !strings.HasPrefix(persisted.APIKey, "enc:v1:") {
		t.Fatalf("resource secret should be stored encrypted, got %q", persisted.APIKey)
	}
	if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{
		Name:    "Encrypted Resource Updated",
		Status:  StatusActive,
		Healthy: true,
	}); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "encrypted-chat",
		Status:             StatusActive,
	})
	adapter := &captureAdapter{}
	server := New(store)
	server.adapters["capture"] = adapter
	app := server.Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "secret route"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenKey != "resource-secret" {
		t.Fatalf("expected decrypted resource secret, got %q", adapter.seenKey)
	}
	if strings.Contains(resp.Body, "resource-secret") {
		t.Fatalf("secret should not be returned: %s", resp.Body)
	}
}

func TestOpenAISubscriptionResourceSuppliesRouteCredentials(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "OpenAI Subscription App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "openai-subscription-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_openai_subscription")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_capture_openai", Name: "Capture OpenAI", Type: "capture", Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_openai_account",
		ProviderID:   provider.ID,
		Name:         "OpenAI Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-4.1-mini"),
		Credentials: &ProviderResourceCredentials{
			AuthType:       "oauth",
			AccessToken:    "openai-access-token",
			RefreshToken:   "openai-refresh-token",
			Email:          "owner@example.com",
			AccountID:      "acc_capture",
			OrganizationID: "org_capture",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "gpt-4.1-mini",
		Status:             StatusActive,
	})
	adapter := &captureAdapter{}
	server := New(store)
	server.adapters["capture"] = adapter
	app := server.Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "hello from subscription"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenKey != "openai-access-token" {
		t.Fatalf("expected OpenAI account access token, got %q", adapter.seenKey)
	}
	if adapter.seenOptions["credential_source"] != ProviderResourceOpenAISubscription ||
		adapter.seenOptions["account_email"] != "owner@example.com" ||
		adapter.seenOptions["organization_id"] != "org_capture" {
		t.Fatalf("expected OpenAI account options, got %+v", adapter.seenOptions)
	}
}

func TestOpenAISubscriptionResourceRefreshesBeforeGatewayCall(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("grant_type") != "refresh_token" ||
			r.FormValue("refresh_token") != "refresh-old" ||
			r.FormValue("client_id") != openAIAccountOAuthClientID ||
			r.FormValue("scope") != openAIAccountOAuthRefreshScope {
			t.Fatalf("unexpected refresh form: %s", r.Form.Encode())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-refreshed",
			"id_token": testJWT(map[string]any{
				"email": "refreshed.owner@example.com",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "acc_refreshed",
					"chatgpt_plan_type":  "pro",
					"organizations": []map[string]any{
						{"id": "org_refreshed", "is_default": true},
					},
				},
			}),
			"token_type": "Bearer",
			"expires_in": 3600,
		})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Refreshing Account App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "refreshing-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_refreshing")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_refreshing", Name: "Refreshing Provider", Type: "capture", Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_refreshing",
		ProviderID:   provider.ID,
		Name:         "Refreshing OpenAI Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-4.1-mini"),
		Credentials: &ProviderResourceCredentials{
			AuthType:     "oauth",
			AccessToken:  "access-expired",
			RefreshToken: "refresh-old",
			ClientID:     openAIAccountOAuthClientID,
			ExpiresAt:    time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "gpt-4.1-mini",
		Status:             StatusActive,
	})
	adapter := &captureAdapter{}
	server := New(store)
	server.adapters["capture"] = adapter
	app := server.Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "refresh before call"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenKey != "access-refreshed" {
		t.Fatalf("expected refreshed access token, got %q", adapter.seenKey)
	}
	if adapter.seenOptions["account_email"] != "refreshed.owner@example.com" ||
		adapter.seenOptions["account_id"] != "acc_refreshed" ||
		adapter.seenOptions["organization_id"] != "org_refreshed" ||
		adapter.seenOptions["has_refresh_token"] != "true" {
		t.Fatalf("expected refreshed account options, got %+v", adapter.seenOptions)
	}
}

func TestOpenAIProviderAccountOAuthGenerateAuthURLAndCallback(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/generate-auth-url", map[string]any{
		"return_url": "http://localhost:3001/providers",
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected auth URL generated, got %d: %s", resp.Code, resp.Body)
	}
	var payload providerAccountOAuthGenerateResponse
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AuthURL == "" || payload.SessionID == "" || payload.State == "" || payload.RedirectURI == "" {
		t.Fatalf("unexpected auth URL payload: %+v", payload)
	}
	authURL, err := url.Parse(payload.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	if authURL.Host != "auth.openai.com" ||
		authURL.Query().Get("client_id") != openAIAccountOAuthClientID ||
		authURL.Query().Get("redirect_uri") != openAIAccountOAuthRedirectURI ||
		authURL.Query().Get("code_challenge_method") != "S256" ||
		authURL.Query().Get("codex_cli_simplified_flow") != "true" ||
		authURL.Query().Get("state") != payload.State {
		t.Fatalf("unexpected authorize URL: %s", payload.AuthURL)
	}

	callback := httptest.NewRequest(http.MethodGet, "/api/admin/provider-account-oauth/openai/oauth/callback?code=oauth-code&state="+url.QueryEscape(payload.State), nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, callback)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected callback redirect, got %d: %s", rr.Code, rr.Body.String())
	}
	location := rr.Header().Get("location")
	redirect, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	if redirect.String() == "" ||
		redirect.Query().Get("provider_account_oauth") != "1" ||
		redirect.Query().Get("provider_account_oauth_session_id") != payload.SessionID ||
		redirect.Query().Get("provider_account_oauth_state") != payload.State ||
		redirect.Query().Get("code") != "oauth-code" {
		t.Fatalf("unexpected callback redirect: %s", location)
	}
}

func TestOpenAIProviderAccountOAuthCallbackSurfacesDatabaseFailure(t *testing.T) {
	store := NewMemoryStore()
	session := providerAccountOAuthSession{
		ID:           "oauth-db-error",
		State:        "oauth-db-error-state",
		CodeVerifier: "oauth-db-error-verifier",
		CreatedAt:    time.Now().UTC(),
	}
	if err := store.SaveProviderAccountOAuthSession(session); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	callback := httptest.NewRequest(http.MethodGet, "/api/admin/provider-account-oauth/openai/oauth/callback?code=oauth-code&state="+url.QueryEscape(session.State), nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, callback)
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "internal_error") {
		t.Fatalf("expected database failure to surface as 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

type oauthRestoreFailureStore struct {
	Store
	saveCalls int
}

func (s *oauthRestoreFailureStore) SaveProviderAccountOAuthSession(session providerAccountOAuthSession) error {
	s.saveCalls++
	if s.saveCalls > 1 {
		return errors.New("restore failed")
	}
	return s.Store.SaveProviderAccountOAuthSession(session)
}

func TestOpenAIProviderAccountOAuthExchangeSurfacesSessionRestoreFailure(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "token endpoint unavailable", http.StatusBadGateway)
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	baseStore := NewMemoryStore()
	store := &oauthRestoreFailureStore{Store: baseStore}
	app := New(store).Handler()
	generated := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/generate-auth-url", map[string]any{
		"return_url": "http://localhost:3001/providers",
	}, "")
	if generated.Code != http.StatusOK {
		t.Fatalf("expected generate 200, got %d: %s", generated.Code, generated.Body)
	}
	var auth providerAccountOAuthGenerateResponse
	if err := json.Unmarshal([]byte(generated.Body), &auth); err != nil {
		t.Fatal(err)
	}
	exchanged := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/exchange-code", map[string]any{
		"session_id": auth.SessionID,
		"state":      auth.State,
		"code":       "oauth-code",
	}, "")
	if exchanged.Code != http.StatusInternalServerError || !strings.Contains(exchanged.Body, "internal_error") {
		t.Fatalf("expected restore failure to surface as 500, got %d: %s", exchanged.Code, exchanged.Body)
	}
}

func TestOpenAIProviderAccountOAuthExchangeCode(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("user-agent") != "codex-cli/0.91.0" {
			t.Fatalf("expected Codex user-agent, got %q", r.Header.Get("user-agent"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("grant_type") != "authorization_code" ||
			r.FormValue("client_id") != openAIAccountOAuthClientID ||
			r.FormValue("code") != "oauth-code" ||
			r.FormValue("redirect_uri") != openAIAccountOAuthRedirectURI ||
			r.FormValue("code_verifier") == "" {
			t.Fatalf("unexpected token form: %s", r.Form.Encode())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-from-code",
			"refresh_token": "refresh-from-code",
			"id_token": testJWT(map[string]any{
				"email": "codex.owner@example.com",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "acc_oauth",
					"chatgpt_user_id":    "usr_oauth",
					"chatgpt_plan_type":  "plus",
					"organizations": []map[string]any{
						{"id": "org_oauth", "is_default": true},
					},
				},
			}),
			"token_type": "Bearer",
			"expires_in": 3600,
			"scope":      openAIAccountOAuthScopes,
		})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	store := NewMemoryStore()
	app := New(store).Handler()
	generated := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/generate-auth-url", map[string]any{
		"return_url": "http://localhost:3001/providers",
	}, "")
	if generated.Code != http.StatusOK {
		t.Fatalf("expected generate 200, got %d: %s", generated.Code, generated.Body)
	}
	var auth providerAccountOAuthGenerateResponse
	if err := json.Unmarshal([]byte(generated.Body), &auth); err != nil {
		t.Fatal(err)
	}
	exchanged := doJSON(t, app, http.MethodPost, "/api/admin/provider-account-oauth/openai/exchange-code", map[string]any{
		"session_id": auth.SessionID,
		"state":      auth.State,
		"code":       "oauth-code",
	}, "")
	if exchanged.Code != http.StatusOK {
		t.Fatalf("expected exchange 200, got %d: %s", exchanged.Code, exchanged.Body)
	}
	var info providerAccountOAuthTokenInfo
	if err := json.Unmarshal([]byte(exchanged.Body), &info); err != nil {
		t.Fatal(err)
	}
	if info.AccessToken != "access-from-code" ||
		info.RefreshToken != "refresh-from-code" ||
		info.AccountEmail != "codex.owner@example.com" ||
		info.AccountID != "acc_oauth" ||
		info.OrganizationID != "org_oauth" ||
		info.ClientID != openAIAccountOAuthClientID {
		t.Fatalf("unexpected exchanged token info: %+v", info)
	}
}

func TestProviderAndResourceTestEndpoints(t *testing.T) {
	app := newTestServer()
	provider := doJSON(t, app, http.MethodPost, "/api/admin/providers/prv_mock/test", nil, "")
	if provider.Code != http.StatusOK {
		t.Fatalf("expected provider test 200, got %d: %s", provider.Code, provider.Body)
	}
	if !strings.Contains(provider.Body, `"healthy":true`) {
		t.Fatalf("expected healthy provider response: %s", provider.Body)
	}

	resource := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/rsrc_mock_primary/test", nil, "")
	if resource.Code != http.StatusOK {
		t.Fatalf("expected resource test 200, got %d: %s", resource.Code, resource.Body)
	}
	if !strings.Contains(resource.Body, `"healthy":true`) || !strings.Contains(resource.Body, `"last_checked_at"`) {
		t.Fatalf("expected checked healthy resource: %s", resource.Body)
	}
}

func TestProviderResourceBulkOperations(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{Name: "Bulk Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		Name:           "Bulk Resource",
		ProviderID:     provider.ID,
		ResourceType:   "api_key",
		Status:         StatusActive,
		Healthy:        true,
		RateLimitRPM:   1,
		TokenLimitTPM:  1,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, "", AttemptFailed, Usage{})
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, "", AttemptFailed, Usage{})
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, "", AttemptFailed, Usage{})
	if _, _, err := store.CheckProviderResourceCapacity(context.Background(), resource.ID); AsHTTPError(err).Code != "provider_resource_cooling_down" {
		t.Fatalf("expected cooldown before clear_error, got %v", err)
	}
	app := New(store).Handler()

	disabled := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/bulk", map[string]any{
		"action": "disable",
		"ids":    []string{resource.ID},
	}, "")
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body, `"success":1`) {
		t.Fatalf("disable failed: %d %s", disabled.Code, disabled.Body)
	}
	found := findResource(t, store, resource.ID)
	if found.Status != StatusDisabled || found.Healthy {
		t.Fatalf("expected disabled unhealthy resource, got %+v", found)
	}

	cleared := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/bulk", map[string]any{
		"action": "clear_error",
		"ids":    []string{resource.ID, resource.ID},
	}, "")
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body, `"success":1`) {
		t.Fatalf("clear error failed: %d %s", cleared.Code, cleared.Body)
	}
	leaseID, _, err := store.CheckProviderResourceCapacity(context.Background(), resource.ID)
	if err != nil {
		t.Fatalf("capacity should be available after clear_error: %v", err)
	}
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, leaseID, AttemptSucceeded, Usage{TotalTokens: 5})
	if _, _, err := store.CheckProviderResourceCapacity(context.Background(), resource.ID); AsHTTPError(err).Code != "provider_resource_rpm_exceeded" {
		t.Fatalf("expected rpm limit before reset, got %v", err)
	}
	reset := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/bulk", map[string]any{
		"action": "reset_usage",
		"ids":    []string{resource.ID},
	}, "")
	if reset.Code != http.StatusOK || !strings.Contains(reset.Body, `"success":1`) {
		t.Fatalf("reset usage failed: %d %s", reset.Code, reset.Body)
	}
	leaseID, _, err = store.CheckProviderResourceCapacity(context.Background(), resource.ID)
	if err != nil {
		t.Fatalf("capacity should be available after reset_usage: %v", err)
	}
	store.FinishProviderResourceAttempt(context.Background(), resource.ID, leaseID, AttemptSucceeded, Usage{})
}

func TestProviderResourceImport(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{Name: "Import Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/import", map[string]any{
		"resources": []map[string]any{
			{
				"provider_id":     provider.ID,
				"name":            "Imported Primary",
				"group":           "prod-east",
				"resource_type":   "api_key",
				"api_key":         "import-secret-1",
				"region":          "us-east-1",
				"environment":     "prod",
				"priority":        1,
				"weight":          80,
				"rate_limit_rpm":  120,
				"token_limit_tpm": 60000,
				"max_concurrency": 8,
			},
			{
				"provider_id": "missing-provider",
				"name":        "Broken Resource",
			},
		},
	}, "")
	if resp.Code != http.StatusMultiStatus || !strings.Contains(resp.Body, `"success":1`) || !strings.Contains(resp.Body, `"failed":1`) {
		t.Fatalf("expected partial import result, got %d %s", resp.Code, resp.Body)
	}
	if strings.Contains(resp.Body, "import-secret-1") {
		t.Fatalf("resource secret should not be returned: %s", resp.Body)
	}
	resources := store.ListProviderResources()
	var imported ProviderResource
	for _, item := range resources {
		if item.Name == "Imported Primary" {
			imported = item
			break
		}
	}
	if imported.ID == "" || imported.Group != "prod-east" || imported.RateLimitRPM != 120 || imported.APIKey != "" {
		t.Fatalf("expected imported resource with redacted key, got %+v", imported)
	}
}

func TestMonitorRunUpdatesResourceAndCreatesAlert(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	monitor := store.CreateResource("monitors", AdminResource{
		Name:   "Mock resource monitor",
		Status: StatusActive,
		Fields: map[string]any{
			"target_type":          "resource",
			"provider_resource_id": "rsrc_mock_primary",
		},
	})
	app := New(store).Handler()

	okRun := doJSON(t, app, http.MethodPost, "/api/admin/resources/monitors/"+monitor.ID+"/run", map[string]any{}, "")
	if okRun.Code != http.StatusOK || !strings.Contains(okRun.Body, `"status":"ok"`) {
		t.Fatalf("monitor ok run failed: %d %s", okRun.Code, okRun.Body)
	}
	updated, err := store.UpdateProviderResource("rsrc_mock_primary", ProviderResource{Status: StatusDisabled, Healthy: false})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusDisabled {
		t.Fatalf("expected disabled resource before failed monitor, got %+v", updated)
	}
	failedRun := doJSON(t, app, http.MethodPost, "/api/admin/resources/monitors/"+monitor.ID+"/run", map[string]any{}, "")
	if failedRun.Code != http.StatusOK || !strings.Contains(failedRun.Body, `"status":"failed"`) || !strings.Contains(failedRun.Body, `"alert_id"`) {
		t.Fatalf("monitor failed run did not create alert: %d %s", failedRun.Code, failedRun.Body)
	}
	alerts := store.ListAlerts()
	if len(alerts) == 0 || alerts[0].Code != "monitor_check_failed" || alerts[0].ScopeID != monitor.ID {
		t.Fatalf("expected monitor alert, got %+v", alerts)
	}
	monitors := store.ListResources("monitors")
	var found AdminResource
	for _, item := range monitors {
		if item.ID == monitor.ID {
			found = item
			break
		}
	}
	if stringifyValueForTest(found.Fields["last_status"]) != "failed" || stringifyValueForTest(found.Fields["last_checked_at"]) == "" {
		t.Fatalf("monitor fields not updated: %+v", found.Fields)
	}
}

func TestMonitorRunInfersLegacyModelMonitor(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	monitor := store.CreateResource("monitors", AdminResource{
		Name:   "Legacy model monitor",
		Status: StatusActive,
		Fields: map[string]any{
			"provider": "mock",
			"model":    "gpt-4.1-mini",
		},
	})
	app := New(store).Handler()

	run := doJSON(t, app, http.MethodPost, "/api/admin/resources/monitors/"+monitor.ID+"/run", map[string]any{}, "")
	if run.Code != http.StatusOK || !strings.Contains(run.Body, `"target_type":"model"`) || !strings.Contains(run.Body, `"status":"ok"`) {
		t.Fatalf("expected legacy monitor to run as model monitor, got %d %s", run.Code, run.Body)
	}
}

func TestDefaultMonitorsAreAutoDiscovered(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:       "prv_health_default",
		Name:     "Health Default Provider",
		Type:     ProviderMock,
		Status:   StatusActive,
		Healthy:  true,
		Priority: 1,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_health_default",
		ProviderID:   provider.ID,
		Name:         "Health Default Resource",
		ResourceType: "mock",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{
		ID:       "health-default-model",
		Name:     "health-default-model",
		Family:   "test",
		Modality: "chat",
		Status:   StatusActive,
	})
	store.AddRoute(ModelRoute{
		ID:                 "route_health_default",
		ModelName:          "health-default-model",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "health-default-upstream",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
	})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodGet, "/api/admin/resources/monitors", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("default monitor list failed: %d %s", resp.Code, resp.Body)
	}
	monitors := store.ListResources("monitors")
	if len(monitors) != 3 {
		t.Fatalf("expected provider/resource/model monitors, got %d: %+v", len(monitors), monitors)
	}
	found := map[string]bool{}
	for _, monitor := range monitors {
		key := monitorTargetKey(monitor.Fields)
		found[key] = true
		if stringifyValueForTest(monitor.Fields["managed_by"]) != "tokenhub_auto" {
			t.Fatalf("expected auto-managed monitor, got %+v", monitor.Fields)
		}
		if stringifyValueForTest(monitor.Fields["last_status"]) != "ok" || stringifyValueForTest(monitor.Fields["last_checked_at"]) == "" {
			t.Fatalf("expected monitor to run immediately, got %+v", monitor.Fields)
		}
	}
	for _, key := range []string{"provider:" + provider.ID, "resource:" + resource.ID, "model:health-default-model"} {
		if !found[key] {
			t.Fatalf("missing default monitor target %s in %+v", key, found)
		}
	}

	resp = doJSON(t, app, http.MethodGet, "/api/admin/resources/monitors", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("second default monitor list failed: %d %s", resp.Code, resp.Body)
	}
	if got := len(store.ListResources("monitors")); got != len(monitors) {
		t.Fatalf("default monitor discovery should be idempotent, before=%d after=%d", len(monitors), got)
	}
}

func TestDefaultAlertRulesAreAutoDiscovered(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodGet, "/api/admin/resources/alert-rules", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("default alert rule list failed: %d %s", resp.Code, resp.Body)
	}
	rules := store.ListResources("alert-rules")
	if len(rules) != 5 {
		t.Fatalf("expected default provider and quota alert rules, got %d: %+v", len(rules), rules)
	}
	found := map[string]bool{}
	for _, rule := range rules {
		key := alertRuleKey(rule.Fields)
		found[key] = true
		if stringifyValueForTest(rule.Fields["managed_by"]) != "tokenhub_auto" {
			t.Fatalf("expected auto-managed alert rule, got %+v", rule.Fields)
		}
		if stringifyValueForTest(rule.Fields["metric"]) == "" || stringifyValueForTest(rule.Fields["threshold"]) == "" {
			t.Fatalf("expected metric and threshold, got %+v", rule.Fields)
		}
	}
	for _, key := range []string{
		"provider_health_failed",
		"provider_resource_health_failed",
		"request_quota_near_limit",
		"token_quota_near_limit",
		"cost_quota_near_limit",
	} {
		if !found[key] {
			t.Fatalf("missing default alert rule %s in %+v", key, found)
		}
	}

	resp = doJSON(t, app, http.MethodGet, "/api/admin/resources/alert-rules", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("second default alert rule list failed: %d %s", resp.Code, resp.Body)
	}
	if got := len(store.ListResources("alert-rules")); got != len(rules) {
		t.Fatalf("default alert rule discovery should be idempotent, before=%d after=%d", len(rules), got)
	}
}

func TestProviderResourceCooldownAfterFailures(t *testing.T) {
	store, secret, resourceID := newResourceRoutedStore(t, "failing_resource")
	store.failureThreshold = 2
	server := New(store)
	server.adapters["failing_resource"] = failingAdapter{}
	app := server.Handler()

	for i := 0; i < 2; i++ {
		resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "gpt-4.1-mini",
			"messages": []map[string]any{
				{"role": "user", "content": "cooldown"},
			},
		}, secret)
		if resp.Code != http.StatusBadGateway {
			t.Fatalf("request %d expected 502 provider error, got %d: %s", i+1, resp.Code, resp.Body)
		}
	}

	resource := findResource(t, store, resourceID)
	if resource.FailureCount < 2 || resource.CooldownUntil == nil || resource.Healthy {
		t.Fatalf("expected resource in cooldown, got failures=%d healthy=%v cooldown=%v", resource.FailureCount, resource.Healthy, resource.CooldownUntil)
	}
	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/"+resourceID+"/test", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("resource test should clear cooldown, got %d: %s", resp.Code, resp.Body)
	}
	resource = findResource(t, store, resourceID)
	if resource.FailureCount != 0 || resource.CooldownUntil != nil || !resource.Healthy {
		t.Fatalf("expected test to restore resource, got failures=%d healthy=%v cooldown=%v", resource.FailureCount, resource.Healthy, resource.CooldownUntil)
	}
}

func TestProviderResourceRPMLimit(t *testing.T) {
	store, secret, resourceID := newResourceRoutedStore(t, ProviderMock)
	if err := store.db.Model(&ProviderResource{}).Where("id = ?", resourceID).Update("rate_limit_rpm", 1).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	first := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "first"},
		},
	}, secret)
	if first.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d: %s", first.Code, first.Body)
	}
	second := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "second"},
		},
	}, secret)
	if second.Code != http.StatusTooManyRequests || !strings.Contains(second.Body, "provider_resource_rpm_exceeded") {
		t.Fatalf("second request expected RPM limit, got %d: %s", second.Code, second.Body)
	}
}

func TestGatewayRoutesThroughProviderResource(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Resource Routed App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "resource-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_resource_route")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_resource", Name: "Resource Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_primary",
		ProviderID:   provider.ID,
		Name:         "Primary Resource",
		ResourceType: "mock",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:                 "route_resource",
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "resource-chat",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
	})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "resource hit"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}

	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %d", len(logs))
	}
	if logs[0].ProviderID != provider.ID || logs[0].ProviderResourceID != resource.ID {
		t.Fatalf("expected provider resource audit log, got provider=%s resource=%s", logs[0].ProviderID, logs[0].ProviderResourceID)
	}
	resources := store.ListProviderResources()
	var touched bool
	for _, item := range resources {
		if item.ID == resource.ID && item.LastUsedAt != nil {
			touched = true
		}
	}
	if !touched {
		t.Fatalf("provider resource should be marked last used")
	}
}

func TestProviderHealthAffectsRouting(t *testing.T) {
	app := newTestServer()
	health := doJSON(t, app, http.MethodPost, "/api/admin/providers/prv_mock/health", map[string]any{
		"healthy": false,
	}, "")
	if health.Code != http.StatusOK {
		t.Fatalf("expected health update, got %d: %s", health.Code, health.Body)
	}

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, "thk_demo_local")
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "provider_unavailable") {
		t.Fatalf("expected provider_unavailable: %s", resp.Body)
	}
}

func TestGatewayFailoverUsesBackupRoute(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Failover App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "failover-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_failover")
	if err != nil {
		t.Fatal(err)
	}
	failing := store.AddProvider(Provider{ID: "prv_failing", Name: "Failing", Type: "failing_mock", Status: StatusActive, Healthy: true})
	backup := store.AddProvider(Provider{ID: "prv_backup", Name: "Backup", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_failing", ModelName: "gpt-4.1-mini", ProviderID: failing.ID, ProviderModel: "failing-chat", Priority: 1, Weight: 100, Status: StatusActive, Strategy: "priority_only"})
	store.AddRoute(ModelRoute{ID: "route_backup", ModelName: "gpt-4.1-mini", ProviderID: backup.ID, ProviderModel: "backup-chat", Priority: 2, Weight: 100, Status: StatusActive, Strategy: "priority_only"})

	server := New(store)
	server.adapters["failing_mock"] = failingAdapter{}
	app := server.Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "fail over please"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected failover success, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Echo: fail over please") {
		t.Fatalf("expected backup mock response: %s", resp.Body)
	}

	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %d", len(logs))
	}
	if logs[0].ProviderID != backup.ID || logs[0].ProviderModel != "backup-chat" {
		t.Fatalf("expected backup route audit log, got provider=%s model=%s", logs[0].ProviderID, logs[0].ProviderModel)
	}
	routes := store.ListRoutes()
	var backupTouched bool
	for _, route := range routes {
		if route.ID == "route_backup" && route.LastUsedAt != nil {
			backupTouched = true
		}
		if route.ID == "route_failing" && route.LastUsedAt != nil {
			t.Fatalf("failing route should not be marked last used")
		}
	}
	if !backupTouched {
		t.Fatalf("backup route should be marked last used")
	}

	detail := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests/"+logs[0].RequestID, nil, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("request detail failed: %d %s", detail.Code, detail.Body)
	}
	if !strings.Contains(detail.Body, `"attempts"`) || !strings.Contains(detail.Body, `"route_failing"`) || !strings.Contains(detail.Body, `"route_backup"`) {
		t.Fatalf("expected route attempts in detail: %s", detail.Body)
	}
}

func TestModelRouterStrategiesRankCandidates(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Router Strategy App"})
	key := APIKey{ID: "key_router_strategy", ProjectID: project.ID, Name: "router-key", Status: StatusActive}
	model := Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive}
	call := CallContext{RequestID: "req_router_strategy", Project: project, Key: key, Model: model}
	fast := store.AddProvider(Provider{ID: "prv_fast", Name: "Fast", Type: ProviderMock, Status: StatusActive, Healthy: true})
	cheap := store.AddProvider(Provider{ID: "prv_cheap", Name: "Cheap", Type: ProviderMock, Status: StatusActive, Healthy: true})
	quality := store.AddProvider(Provider{ID: "prv_quality", Name: "Quality", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(model)
	store.AddRoute(ModelRoute{ID: "route_fast", ModelName: model.Name, ProviderID: fast.ID, ProviderModel: "fast-chat", Priority: 1, Weight: 100, QualityScore: 50, CostScore: 50, Status: StatusActive, Strategy: RouteStrategyQuality})
	store.AddRoute(ModelRoute{ID: "route_cheap", ModelName: model.Name, ProviderID: cheap.ID, ProviderModel: "cheap-chat", Priority: 1, Weight: 80, QualityScore: 40, CostScore: 95, Status: StatusActive, Strategy: RouteStrategyQuality})
	store.AddRoute(ModelRoute{ID: "route_quality", ModelName: model.Name, ProviderID: quality.ID, ProviderModel: "quality-chat", Priority: 1, Weight: 60, QualityScore: 95, CostScore: 35, Status: StatusActive, Strategy: RouteStrategyQuality})

	server := New(store)
	candidates, err := store.SelectRouteCandidates(model.Name)
	if err != nil {
		t.Fatal(err)
	}
	planned := server.planRouteOrder(call, candidates)
	if planned[0].Route.ID != "route_quality" {
		t.Fatalf("quality strategy should pick highest quality first, got %s", planned[0].Route.ID)
	}

	for _, route := range store.ListRoutes() {
		route.Strategy = RouteStrategyCost
		if _, err := store.UpdateRoute(route.ID, route); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err = store.SelectRouteCandidates(model.Name)
	if err != nil {
		t.Fatal(err)
	}
	planned = server.planRouteOrder(call, candidates)
	if planned[0].Route.ID != "route_cheap" {
		t.Fatalf("cost strategy should pick highest cost score first, got %s", planned[0].Route.ID)
	}
}

func TestHealth(t *testing.T) {
	app := newTestServer()
	resp := doJSON(t, app, http.MethodGet, "/healthz", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
}

func TestReadinessFailsWhenDatabaseIsUnavailableButLivenessRemainsHealthy(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	ready := doJSON(t, app, http.MethodGet, "/readyz", nil, "")
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness 503 after database close, got %d: %s", ready.Code, ready.Body)
	}
	live := doJSON(t, app, http.MethodGet, "/livez", nil, "")
	if live.Code != http.StatusOK {
		t.Fatalf("expected liveness 200 after database close, got %d: %s", live.Code, live.Body)
	}
}

func TestClientIPIgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	server := &Server{config: Config{TrustedProxyCIDRs: []string{"10.0.0.0/8"}}}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "198.51.100.7:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.25")

	if got := server.clientIP(request); got != "198.51.100.7" {
		t.Fatalf("expected direct peer IP, got %q", got)
	}
}

func TestClientIPUsesForwardedChainFromTrustedProxy(t *testing.T) {
	server := &Server{config: Config{TrustedProxyCIDRs: []string{"10.0.0.0/8", "192.0.2.10"}}}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "10.0.0.8:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.25, 192.0.2.10")

	if got := server.clientIP(request); got != "203.0.113.25" {
		t.Fatalf("expected first untrusted address from the right, got %q", got)
	}
}

func TestClientIPRejectsMalformedForwardedChain(t *testing.T) {
	server := &Server{config: Config{TrustedProxyCIDRs: []string{"10.0.0.0/8"}}}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "10.0.0.8:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.25, not-an-ip")

	if got := server.clientIP(request); got != "10.0.0.8" {
		t.Fatalf("expected malformed chain to fall back to direct peer, got %q", got)
	}
}

func newTestServer() http.Handler {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		panic(err)
	}
	return New(store).Handler()
}

func configureTestSMTPChannel(t *testing.T, store *GormStore) <-chan string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 10)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveTestSMTPConnection(conn, messages)
		}
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("notification-channels", AdminResource{
		Name:   "Test SMTP",
		Status: StatusActive,
		Fields: map[string]any{
			"type":      "email",
			"smtp_host": host,
			"smtp_port": port,
			"smtp_from": "tokenhub@example.com",
		},
	})
	return messages
}

func serveTestSMTPConnection(conn net.Conn, messages chan<- string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	write := func(response string) bool {
		if _, err := writer.WriteString(response + "\r\n"); err != nil {
			return false
		}
		return writer.Flush() == nil
	}
	if !write("220 localhost ESMTP") {
		return
	}
	var message strings.Builder
	readingData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimRight(line, "\r\n")
		if readingData {
			if command == "." {
				messages <- message.String()
				message.Reset()
				readingData = false
				if !write("250 queued") {
					return
				}
				continue
			}
			message.WriteString(strings.TrimPrefix(command, "."))
			message.WriteByte('\n')
			continue
		}
		switch {
		case strings.HasPrefix(command, "EHLO "), strings.HasPrefix(command, "HELO "):
			if !write("250 localhost") {
				return
			}
		case command == "DATA":
			readingData = true
			if !write("354 end with <CRLF>.<CRLF>") {
				return
			}
		case command == "QUIT":
			_ = write("221 bye")
			return
		default:
			if !write("250 ok") {
				return
			}
		}
	}
}

func assertPasswordResetEmail(t *testing.T, messages <-chan string, recipient string) {
	t.Helper()
	select {
	case message := <-messages:
		if !strings.Contains(message, "To: "+recipient) || !strings.Contains(message, "reset_token=") {
			t.Fatalf("unexpected password reset email: %s", message)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for password reset email to %s", recipient)
	}
}

type responseBody struct {
	Code int
	Body string
}

func doJSON(t *testing.T, handler http.Handler, method string, path string, payload any, token string) responseBody {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, body)
	if payload != nil {
		req.Header.Set("content-type", "application/json")
	}
	if token == "" && strings.HasPrefix(path, "/api/admin") {
		token = "dev_admin_token"
	}
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return responseBody{Code: rr.Code, Body: rr.Body.String()}
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func stringifyValueForTest(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, _ := json.Marshal(value)
	return strings.Trim(string(data), `"`)
}

func newResourceRoutedStore(t *testing.T, providerType string) (*GormStore, string, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Resource Ops App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "resource-ops-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_resource_ops_"+providerType)
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_" + providerType, Name: "Resource Ops Provider", Type: providerType, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:             "rsrc_" + providerType,
		ProviderID:     provider.ID,
		Name:           "Resource Ops Instance",
		ResourceType:   "mock",
		Status:         StatusActive,
		Healthy:        true,
		Priority:       1,
		Weight:         100,
		RateLimitRPM:   0,
		TokenLimitTPM:  100000,
		MaxConcurrency: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "resource-ops-chat",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
		Strategy:           "priority_only",
	})
	return store, secret, resource.ID
}

func findResource(t *testing.T, store *GormStore, id string) ProviderResource {
	t.Helper()
	for _, resource := range store.ListProviderResources() {
		if resource.ID == id {
			return resource
		}
	}
	t.Fatalf("resource %s not found", id)
	return ProviderResource{}
}

type captureAdapter struct {
	seenKey     string
	seenOptions map[string]string
}

func (a *captureAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	a.seenKey = provider.APIKey
	a.seenOptions = provider.Options
	return MockAdapter{}.Chat(ctx, provider, providerModel, req)
}

func (a *captureAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	a.seenKey = provider.APIKey
	a.seenOptions = provider.Options
	return MockAdapter{}.ChatStream(ctx, provider, providerModel, req, w)
}

func (a *captureAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	a.seenKey = provider.APIKey
	a.seenOptions = provider.Options
	return MockAdapter{}.Responses(ctx, provider, providerModel, req)
}

func (a *captureAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	a.seenKey = provider.APIKey
	a.seenOptions = provider.Options
	return MockAdapter{}.Embeddings(ctx, provider, providerModel, req)
}

func testJWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

type failingAdapter struct{}

func (a failingAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed")
}

func (a failingAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	return Usage{}, NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed")
}

func (a failingAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed")
}

func (a failingAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusBadGateway, "provider_error", "upstream failed")
}
