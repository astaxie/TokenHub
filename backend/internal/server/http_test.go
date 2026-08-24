package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
		"project_id": "prj_demo",
		"model":      "gpt-4.1-mini",
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

func TestAdminPlaygroundModesPreserveImagesForCodexSubscription(t *testing.T) {
	dataURI := "data:image/png;base64,YWJj"
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Playground Codex Project"})
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
		thirdContent, _ := third["content"].([]any)
		firstPart, _ := firstContent[0].(map[string]any)
		secondPart, _ := secondContent[0].(map[string]any)
		if firstPart["type"] != "input_text" || firstPart["text"] != "First question" || secondPart["type"] != "output_text" || secondPart["text"] != "First answer" {
			t.Fatalf("unexpected Responses content: %#v", input)
		}
		if len(thirdContent) != 2 {
			t.Fatalf("expected text and image parts, got %#v", thirdContent)
		}
		thirdText, _ := thirdContent[0].(map[string]any)
		thirdImage, _ := thirdContent[1].(map[string]any)
		if thirdText["type"] != "input_text" || thirdText["text"] != "Describe this image" || strings.Contains(anyString(thirdText["text"]), dataURI) {
			t.Fatalf("image data must not be flattened into text: %#v", thirdContent)
		}
		if thirdImage["type"] != "input_image" || thirdImage["image_url"] != dataURI {
			t.Fatalf("expected Responses input_image part, got %#v", thirdImage)
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

	payload := map[string]any{
		"project_id": project.ID,
		"model":      "gpt-playground-codex",
		"messages": []map[string]any{
			{"role": "system", "content": "Be concise."},
			{"role": "user", "content": "First question"},
			{"role": "assistant", "content": "First answer"},
			{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "Describe this image"},
				playgroundImagePart(dataURI),
			}},
		},
		"max_tokens":       321,
		"temperature":      0.2,
		"reasoning_effort": "high",
	}
	for _, path := range []string{"/api/admin/playground/chat", "/api/admin/playground/chat/stream"} {
		resp := doJSON(t, server.Handler(), http.MethodPost, path, payload, "")
		if resp.Code != http.StatusOK {
			t.Fatalf("expected Codex playground request to succeed for %s, got %d: %s", path, resp.Code, resp.Body)
		}
		if !strings.Contains(resp.Body, "Codex playground works.") || !strings.Contains(resp.Body, `"prompt_tokens":5`) || !strings.Contains(resp.Body, `"completion_tokens":4`) {
			t.Fatalf("unexpected Codex playground response for %s: %s", path, resp.Body)
		}
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
	store.AddModel(Model{Name: "factory-embedding", Family: "customized", Modality: "embedding", EmbeddingPriceUSDPer1M: 9, Status: StatusDisabled})
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
