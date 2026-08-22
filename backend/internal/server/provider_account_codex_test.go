package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConsumeCodexResponsesStreamTerminatesCompletedEvent(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_real","status":"completed","usage":{"input_tokens":2,"output_tokens":1}}}`,
		"",
	}, "\n")
	var destination bytes.Buffer

	response, output, usage, err := consumeCodexResponsesStream(strings.NewReader(stream), &destination)
	if err != nil {
		t.Fatalf("consume stream: %v", err)
	}
	if response["status"] != "completed" || output != "ok" {
		t.Fatalf("unexpected completed response: response=%v output=%q", response, output)
	}
	if usage.PromptTokens != 2 || usage.CompletionTokens != 1 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if !strings.HasSuffix(destination.String(), "\n\n") {
		t.Fatalf("completed SSE event is not terminated: %q", destination.String())
	}
}

func TestCodexSubscriptionModelsUsesLiveVisibleCatalog(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Get("client_version") != openAICodexVersion {
			t.Fatalf("missing client version: %s", req.URL.String())
		}
		if req.Header.Get("Authorization") != "Bearer access_real" || req.Header.Get("ChatGPT-Account-ID") != "acct_real" {
			t.Fatalf("missing Codex auth headers: %#v", req.Header)
		}
		body := `{"models":[
			{"slug":"gpt-live-codex","display_name":"GPT Live Codex","description":"Live model","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"}],"visibility":"list","supported_in_api":true,"priority":2,"additional_speed_tiers":["fast"],"minimal_client_version":"0.124.0","context_window":272000,"input_modalities":["text","image"]},
			{"slug":"codex-hidden","display_name":"Hidden","supported_reasoning_levels":[{"effort":"medium"}],"visibility":"hide","priority":1}
		]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	adapter := CodexSubscriptionAdapter{
		Client:    client,
		ModelsURL: "https://chatgpt.example/backend-api/codex/models",
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{AccessToken: "access_real", AccountID: "acct_real"}, nil
		},
	}

	catalog, err := adapter.Models(context.Background(), "resource_real")
	if err != nil {
		t.Fatalf("list Codex models: %v", err)
	}
	if catalog.DisplayName != "OpenAI Codex" || catalog.Type != ProviderOpenAICodex || catalog.BaseURL != openAICodexBaseURL || catalog.ModelsCount != 1 || len(catalog.Models) != 1 {
		t.Fatalf("unexpected catalog: %+v", catalog)
	}
	model := catalog.Models[0]
	if model.ID != "gpt-live-codex" || model.Category != "codex" || model.ContextWindow != 272000 {
		t.Fatalf("unexpected live model: %+v", model)
	}
	if model.Metadata["supported_reasoning_levels"] != "low,medium,high" ||
		model.Metadata["additional_speed_tiers"] != "fast" ||
		model.Metadata["minimal_client_version"] != "0.124.0" {
		t.Fatalf("missing live model metadata: %+v", model.Metadata)
	}
}

func TestCodexModelCatalogUsesETagAndPersistedSnapshot(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_models_etag",
		Name:    "Codex Models ETag",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_models_etag",
		ProviderID:   provider.ID,
		Name:         "ETag Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials:  &ProviderResourceCredentials{AccessToken: "access_etag", AccountID: "account_etag"},
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := New(store)
	server.codexSubscription.ModelsURL = "https://chatgpt.example/backend-api/codex/models"
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 2 {
			if req.Header.Get("If-None-Match") != `"models-v1"` {
				t.Fatalf("model ETag was not sent: %#v", req.Header)
			}
			return &http.Response{
				StatusCode: http.StatusNotModified,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Etag": []string{`"models-v1"`}},
			Body: io.NopCloser(strings.NewReader(
				`{"models":[{"slug":"gpt-etag","display_name":"GPT ETag","visibility":"list","supported_in_api":true,"priority":1,"minimal_client_version":[0,145,0]}]}`,
			)),
			Request: req,
		}, nil
	})}
	first, err := server.queryOpenAICodexModels(context.Background(), resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if providerModels := store.ListProviderModels(); len(providerModels) != 0 {
		t.Fatalf("Codex model discovery must not import Provider inventory before selection: %+v", providerModels)
	}
	second, err := server.queryOpenAICodexModels(context.Background(), resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || first.ETag != `"models-v1"` || second.Source != "openai-codex-cache" ||
		len(second.Models) != 1 || second.Models[0].Metadata["minimal_client_version"] != "0.145.0" {
		t.Fatalf("unexpected ETag model snapshots: requests=%d first=%+v second=%+v", requests, first, second)
	}
	if routes := store.ListRoutes(); len(routes) != 0 {
		t.Fatalf("Codex model discovery must not create external routes: %+v", routes)
	}
	if providerModels := store.ListProviderModels(); len(providerModels) != 0 {
		t.Fatalf("cached Codex model discovery must not import Provider inventory before selection: %+v", providerModels)
	}
	for _, model := range store.ListModels() {
		if model.Name == "gpt-etag" {
			t.Fatalf("Codex model discovery must not publish an external model: %+v", model)
		}
	}
}

func TestCodexRouteFilteringUsesPerAccountModels(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_codex_pool",
		Name:    "Codex Pool",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	solResource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_sol",
		ProviderID:   provider.ID,
		Name:         "Sol Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-5.6-sol", "gpt-5.6-luna"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_luna",
		ProviderID:   provider.ID,
		Name:         "Luna Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-5.6-luna"),
	}); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-5.6-sol", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:            "route_codex_sol",
		ModelName:     "gpt-5.6-sol",
		ProviderID:    provider.ID,
		ProviderModel: "gpt-5.6-sol",
		Status:        StatusActive,
	})

	routes, err := store.SelectRouteCandidates("gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := New(store).filterCodexRoutesByModel(context.Background(), "gpt-5.6-sol", routes)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || routeResourceID(filtered[0]) != solResource.ID {
		t.Fatalf("expected only Sol-capable account, got %+v", filtered)
	}
}

func TestCodexRouteFilteringUsesPersistedAccountCatalog(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_codex_live_pool",
		Name:    "Codex Live Pool",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	for _, resource := range []ProviderResource{
		{
			ID:           "rsrc_codex_live_sol",
			ProviderID:   provider.ID,
			Name:         "Live Sol Account",
			ResourceType: ProviderResourceOpenAISubscription,
			Status:       StatusActive,
			Healthy:      true,
			Credentials: &ProviderResourceCredentials{
				AccessToken: "access_live_sol",
				AccountID:   "account_live_sol",
			},
		},
		{
			ID:           "rsrc_codex_live_luna",
			ProviderID:   provider.ID,
			Name:         "Live Luna Account",
			ResourceType: ProviderResourceOpenAISubscription,
			Status:       StatusActive,
			Healthy:      true,
			Credentials: &ProviderResourceCredentials{
				AccessToken: "access_live_luna",
				AccountID:   "account_live_luna",
			},
		},
	} {
		if _, err := store.AddProviderResource(resource); err != nil {
			t.Fatal(err)
		}
	}
	store.AddModel(Model{Name: "gpt-5.6-sol", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:            "route_codex_live_sol",
		ModelName:     "gpt-5.6-sol",
		ProviderID:    provider.ID,
		ProviderModel: "gpt-5.6-sol",
		Status:        StatusActive,
	})

	server := New(store)
	server.codexSubscription.ModelsURL = "https://chatgpt.example/backend-api/codex/models"
	modelRequests := 0
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		modelRequests++
		model := "gpt-5.6-luna"
		if req.Header.Get("ChatGPT-Account-ID") == "account_live_sol" {
			model = "gpt-5.6-sol"
		}
		body := `{"models":[{"slug":"` + model + `","display_name":"` + model + `","visibility":"list","supported_in_api":true,"priority":1}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	for _, resourceID := range []string{"rsrc_codex_live_sol", "rsrc_codex_live_luna"} {
		if _, err := server.queryOpenAICodexModels(context.Background(), resourceID); err != nil {
			t.Fatal(err)
		}
	}
	if modelRequests != 2 {
		t.Fatalf("expected one control-plane model refresh per account, got %d", modelRequests)
	}

	routes, err := store.SelectRouteCandidates("gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := server.filterCodexRoutesByModel(context.Background(), "gpt-5.6-sol", routes)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || routeResourceID(filtered[0]) != "rsrc_codex_live_sol" {
		t.Fatalf("expected only live Sol account, got %+v", filtered)
	}
	if modelRequests != 2 {
		t.Fatalf("request routing must not refresh remote models, got %d total model requests", modelRequests)
	}
	for _, resource := range store.ListProviderResources() {
		models, fetchedAt, cached := codexResourceCachedModels(&resource)
		if !cached || fetchedAt.IsZero() || len(models) != 1 {
			t.Fatalf("account catalog was not persisted for %s: models=%v fetched_at=%s", resource.ID, models, fetchedAt)
		}
	}
}

func TestCodexUnsupportedModelFailsOverAndUpdatesAccountModels(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_codex_failover",
		Name:    "Codex Failover Pool",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	unsupported, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_unsupported",
		ProviderID:   provider.ID,
		Name:         "Unsupported Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-5.6-sol"),
		Credentials: &ProviderResourceCredentials{
			AccessToken: "access_unsupported",
			AccountID:   "account_unsupported",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	supported, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_supported",
		ProviderID:   provider.ID,
		Name:         "Supported Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-5.6-sol"),
		Credentials: &ProviderResourceCredentials{
			AccessToken: "access_supported",
			AccountID:   "account_supported",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-5.6-sol", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:            "route_codex_failover",
		ModelName:     "gpt-5.6-sol",
		ProviderID:    provider.ID,
		ProviderModel: "gpt-5.6-sol",
		Status:        StatusActive,
	})

	server := New(store)
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("ChatGPT-Account-ID") == "account_unsupported" {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}`,
				)),
				Request: req,
			}, nil
		}
		completed := strings.Join([]string{
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"ok"}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_supported","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(completed)),
			Request:    req,
		}, nil
	})}

	routes, err := store.SelectRouteCandidates("gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	ordered := make([]RouteSelection, 0, len(routes))
	for _, resourceID := range []string{unsupported.ID, supported.ID} {
		for _, route := range routes {
			if routeResourceID(route) == resourceID {
				ordered = append(ordered, route)
			}
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp, selected, _, attempts, err := server.executeRoutedResponses(request, RoutedCall{
		Call: CallContext{
			RequestID: "req_codex_failover",
			Model:     Model{Name: "gpt-5.6-sol", Status: StatusActive},
		},
		Routes: ordered,
	}, ResponsesRequest{Model: "gpt-5.6-sol", Input: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || routeResourceID(selected) != supported.ID || len(attempts) != 2 {
		t.Fatalf("expected failover to supported account, selected=%s attempts=%d response=%v", routeResourceID(selected), len(attempts), resp)
	}
	updated, ok := server.providerResourceByID(unsupported.ID)
	if !ok {
		t.Fatal("unsupported account disappeared")
	}
	models, _, cached := codexResourceCachedModels(&updated)
	if !cached || codexModelInList("gpt-5.6-sol", models) {
		t.Fatalf("unsupported model was not removed from account capabilities: %v", models)
	}
	if updated.FailureCount != 0 {
		t.Fatalf("model entitlement mismatch should not degrade account health: %+v", updated)
	}
}

func TestResponsesRawGenerationControlsAreFilteredOnlyForCodex(t *testing.T) {
	var normalPayload map[string]any
	normalUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/responses" {
			t.Errorf("unexpected normal provider request: %s %s", req.Method, req.URL.Path)
			return
		}
		if err := json.NewDecoder(req.Body).Decode(&normalPayload); err != nil {
			t.Errorf("decode normal provider request: %v", err)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_normal","object":"response","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(normalUpstream.Close)

	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Codex raw controls"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "Codex raw controls key",
		Allowed: []string{"gpt-raw-controls"},
		Status:  StatusActive,
	}, "thk_codex_raw_controls")
	if err != nil {
		t.Fatal(err)
	}
	codex := store.AddProvider(Provider{
		ID:      "prv_codex_raw_controls",
		Name:    "Codex raw controls",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_raw_controls",
		ProviderID:   codex.ID,
		Name:         "Codex raw controls account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
		Options:      codexCapabilityOptionsForTest("gpt-raw-controls"),
		Credentials: &ProviderResourceCredentials{
			AccessToken: "access_raw_controls",
			AccountID:   "account_raw_controls",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	normal := store.AddProvider(Provider{
		ID:      "prv_normal_raw_controls",
		Name:    "Normal raw controls fallback",
		Type:    ProviderOpenAI,
		BaseURL: normalUpstream.URL,
		APIKey:  "normal_raw_controls_key",
		Status:  StatusActive,
		Healthy: true,
	})
	store.AddModel(Model{Name: "gpt-raw-controls", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:                 "route_codex_raw_controls",
		ModelName:          "gpt-raw-controls",
		ProviderID:         codex.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "gpt-raw-controls",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
		Strategy:           "priority_only",
	})
	store.AddRoute(ModelRoute{
		ID:            "route_normal_raw_controls",
		ModelName:     "gpt-raw-controls",
		ProviderID:    normal.ID,
		ProviderModel: "gpt-raw-controls",
		Priority:      2,
		Weight:        100,
		Status:        StatusActive,
		Strategy:      "priority_only",
	})

	server := New(store)
	var codexPayloads []map[string]any
	server.codexSubscription.MaxRequestRetries = 1
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode Codex request: %v", err)
		}
		codexPayloads = append(codexPayloads, payload)
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"retry normal provider"}`)),
			Request:    req,
		}, nil
	})}

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model":             "gpt-raw-controls",
		"input":             "preserve the raw request for failover",
		"max_output_tokens": 321,
		"temperature":       0.25,
		"x_test_passthrough": map[string]any{
			"mode": "retain",
		},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("expected normal provider failover, got %d: %s", response.Code, response.Body)
	}
	if len(codexPayloads) != 2 {
		t.Fatalf("expected two Codex attempts before failover, got %d", len(codexPayloads))
	}
	for _, payload := range codexPayloads {
		if _, ok := payload["max_output_tokens"]; ok {
			t.Fatalf("Codex request must not send max_output_tokens: %#v", payload)
		}
		if _, ok := payload["temperature"]; ok {
			t.Fatalf("Codex request must not send temperature: %#v", payload)
		}
		passthrough := mapFromAny(payload["x_test_passthrough"])
		if passthrough["mode"] != "retain" {
			t.Fatalf("Codex request lost unknown raw field: %#v", payload)
		}
	}
	if normalPayload["max_output_tokens"] != float64(321) || normalPayload["temperature"] != 0.25 {
		t.Fatalf("normal provider failover lost generation controls: %#v", normalPayload)
	}
	passthrough := mapFromAny(normalPayload["x_test_passthrough"])
	if passthrough["mode"] != "retain" {
		t.Fatalf("normal provider failover lost unknown raw field: %#v", normalPayload)
	}
}

func TestCodexSessionAffinityPersistsRebindsAndPreservesProtocol(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Codex Session Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "Codex Session Key",
		Allowed: []string{"gpt-session"},
		Status:  StatusActive,
	}, "thk_codex_session")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:      "prv_codex_session",
		Name:    "Codex Session Provider",
		Type:    ProviderOpenAICodex,
		BaseURL: openAICodexBaseURL,
		Status:  StatusActive,
		Healthy: true,
	})
	for _, account := range []string{"account_session_a", "account_session_b"} {
		options := codexCapabilityOptionsForTest("gpt-session")
		options[codexFingerprintModeOption] = string(codexFingerprintOff)
		if _, err := store.AddProviderResource(ProviderResource{
			ID:           "rsrc_" + account,
			ProviderID:   provider.ID,
			Name:         account,
			ResourceType: ProviderResourceOpenAISubscription,
			Status:       StatusActive,
			Healthy:      true,
			Weight:       100,
			Options:      options,
			Credentials: &ProviderResourceCredentials{
				AccessToken: "access_" + account,
				AccountID:   account,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	store.AddModel(Model{Name: "gpt-session", Category: "codex", Family: "codex", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:            "route_codex_session",
		ModelName:     "gpt-session",
		ProviderID:    provider.ID,
		ProviderModel: "gpt-session",
		Status:        StatusActive,
		Weight:        100,
	})

	var accountCalls []string
	quotaAccount := ""
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		account := req.Header.Get("ChatGPT-Account-ID")
		accountCalls = append(accountCalls, account)
		if req.Header.Get("session-id") != "session-root" {
			t.Fatalf("session-id was not forwarded: %#v", req.Header)
		}
		if req.Header.Get("thread-id") == "" {
			t.Fatalf("thread-id was not forwarded: %#v", req.Header)
		}
		if account == quotaAccount {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header: http.Header{
					"X-Codex-Rate-Limit-Reached-Type": []string{"primary"},
				},
				Body:    io.NopCloser(strings.NewReader(`{"error":{"code":"usage_limit_reached","message":"Codex usage limit reached"}}`)),
				Request: req,
			}, nil
		}
		stream := strings.Join([]string{
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_session","status":"completed","model":"gpt-session","output":[],"usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":8,"cache_write_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":15}}}`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":                 []string{"text/event-stream"},
				"X-Codex-Turn-State":           []string{"turn-state-real"},
				"X-Codex-Primary-Used-Percent": []string{"25"},
				"X-Request-Id":                 []string{"upstream-session-request"},
				"Openai-Model":                 []string{"gpt-session-served"},
				"X-Models-Etag":                []string{`"models-session"`},
			},
			Body:    io.NopCloser(strings.NewReader(stream)),
			Request: req,
		}, nil
	})

	newServer := func() *Server {
		server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "session-affinity-secret"})
		server.codexSubscription.Client = &http.Client{Transport: transport}
		server.codexSubscription.MaxRequestRetries = 1
		return server
	}
	invoke := func(server *Server, threadID string) *httptest.ResponseRecorder {
		t.Helper()
		body := strings.NewReader(`{"model":"gpt-session","input":[{"role":"user","content":[{"type":"input_text","text":"real session request"}]}],"stream":false,"client_metadata":{"session_id":"session-root"}}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", body)
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("session-id", "session-root")
		req.Header.Set("thread-id", threadID)
		rr := httptest.NewRecorder()
		server.Handler().ServeHTTP(rr, req)
		return rr
	}

	server := newServer()
	first := invoke(server, "thread-root")
	if first.Code != http.StatusOK {
		t.Fatalf("first Codex response failed: %d %s", first.Code, first.Body.String())
	}
	if first.Header().Get("X-Codex-Turn-State") != "turn-state-real" ||
		first.Header().Get("X-Tokenhub-Upstream-Request-Id") != "upstream-session-request" ||
		first.Header().Get("X-Request-Id") == "" {
		t.Fatalf("Codex response headers were not preserved: %#v", first.Header())
	}
	if len(accountCalls) != 1 {
		t.Fatalf("expected one first request, got %v", accountCalls)
	}
	firstAccount := accountCalls[0]

	second := invoke(server, "thread-subagent")
	if second.Code != http.StatusOK {
		t.Fatalf("subagent Codex response failed: %d %s", second.Code, second.Body.String())
	}
	if accountCalls[len(accountCalls)-1] != firstAccount {
		t.Fatalf("same Session changed account across Thread IDs: %v", accountCalls)
	}

	restarted := newServer()
	third := invoke(restarted, "thread-after-restart")
	if third.Code != http.StatusOK {
		t.Fatalf("restarted Codex response failed: %d %s", third.Code, third.Body.String())
	}
	if accountCalls[len(accountCalls)-1] != firstAccount {
		t.Fatalf("same Session changed account after server restart: %v", accountCalls)
	}

	quotaAccount = firstAccount
	beforeFailover := len(accountCalls)
	failover := invoke(restarted, "thread-hard-failover")
	if failover.Code != http.StatusOK {
		t.Fatalf("quota failover failed: %d %s", failover.Code, failover.Body.String())
	}
	failoverCalls := accountCalls[beforeFailover:]
	if len(failoverCalls) != 2 || failoverCalls[0] != firstAccount || failoverCalls[1] == firstAccount {
		t.Fatalf("expected one hard-failure rebind: %v", failoverCalls)
	}
	reboundAccount := failoverCalls[1]

	afterRebind := invoke(restarted, "thread-after-rebind")
	if afterRebind.Code != http.StatusOK {
		t.Fatalf("post-rebind request failed: %d %s", afterRebind.Code, afterRebind.Body.String())
	}
	if accountCalls[len(accountCalls)-1] != reboundAccount {
		t.Fatalf("Session switched back after successful rebind: %v", accountCalls)
	}

	var bindings []AdapterSessionBinding
	if err := store.db.Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].Generation != 2 || bindings[0].ResourceID != "rsrc_"+reboundAccount {
		t.Fatalf("unexpected durable Session binding: %+v", bindings)
	}
	if strings.Contains(bindings[0].AffinityKeyHash, "session-root") {
		t.Fatalf("raw Session ID leaked into binding: %+v", bindings[0])
	}
	records := store.ListUsageRecords()
	if len(records) == 0 {
		t.Fatal("Codex usage was not persisted")
	}
	lastUsage := records[len(records)-1]
	if lastUsage.CachedInputTokens != 8 || lastUsage.CacheWriteTokens != 2 || lastUsage.ReasoningTokens != 1 {
		t.Fatalf("extended Codex usage was not persisted: %+v", lastUsage)
	}
	resources := store.ListProviderResources()
	var observed *ProviderResourceObservation
	for _, resource := range resources {
		if resource.ID == "rsrc_"+reboundAccount {
			observed = resource.Observation
		}
	}
	if observed == nil || observed.RateLimitHeaders["x-codex-primary-used-percent"] != "25" ||
		observed.UpstreamRequestID != "upstream-session-request" ||
		observed.ServedModel != "gpt-session-served" {
		t.Fatalf("Codex response observation was not persisted: %+v", observed)
	}
}

func TestCodexSessionIdentifierPriority(t *testing.T) {
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"gpt-test","input":[],"prompt_cache_key":"cache-session","client_metadata":{"session_id":"metadata-session"}}`), &request); err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	headers.Set("session-id", "header-session")
	headers.Set("thread-id", "thread-fallback")
	identifier, ok := codexSessionIdentifier(headers, request)
	if !ok || identifier != "header-session" {
		t.Fatalf("expected header Session priority, got %q %v", identifier, ok)
	}
	headers.Del("session-id")
	headers.Set("session_id", "underscore-session")
	identifier, ok = codexSessionIdentifier(headers, request)
	if !ok || identifier != "underscore-session" {
		t.Fatalf("expected underscore header Session priority, got %q %v", identifier, ok)
	}
	headers.Del("session_id")
	identifier, ok = codexSessionIdentifier(headers, request)
	if !ok || identifier != "metadata-session" {
		t.Fatalf("expected client_metadata Session priority, got %q %v", identifier, ok)
	}
}

func TestCodexResponsesLiteEnvelopePreservesReasoningFields(t *testing.T) {
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"gpt-test","input":[],"reasoning":{"effort":"medium","summary":"auto"}}`), &request); err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	headers.Set("x-openai-internal-codex-responses-lite", "true")
	applyCodexRequestEnvelope(&request, headers)

	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	reasoning := mapFromAny(decoded["reasoning"])
	if reasoning["effort"] != "medium" || reasoning["summary"] != "auto" || reasoning["context"] != "all_turns" {
		t.Fatalf("Codex Responses Lite envelope lost reasoning fields: %#v", reasoning)
	}
}

func TestCodexSessionBindingCommitsAfterClientCancellation(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_cancelled_session",
		Name:    "Cancelled Session",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_cancelled_session",
		ProviderID:   provider.ID,
		Name:         "Cancelled Session Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	routed := RoutedCall{
		Affinity: &RequestAffinity{
			AdapterType: ProviderOpenAICodex,
			Kind:        AffinityKindCodexSession,
			KeyHash:     "cancelled-client-affinity",
		},
		Routes: []RouteSelection{{
			Provider: provider,
			Resource: &resource,
			Route:    ModelRoute{ID: "route_cancelled_session", ProviderID: provider.ID, Status: StatusActive},
		}},
	}
	_, _, _, _, err = executeRoutedWithStore(ctx, store, routed, false, func(context.Context, RouteSelection, bool, int) (map[string]any, Usage, error) {
		cancel()
		return map[string]any{"status": "completed"}, Usage{}, nil
	})
	if err != nil {
		t.Fatalf("successful response did not commit after client cancellation: %v", err)
	}
	var bindings []AdapterSessionBinding
	if err := store.db.Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].ResourceID != resource.ID {
		t.Fatalf("durable Session binding missing after client cancellation: %+v", bindings)
	}
}

func TestCodexCompactConvergesFingerprintAcrossRetriesAndPreservesUpstreamMetadata(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Compact Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "Compact Key",
		Allowed: []string{"gpt-compact"},
		Status:  StatusActive,
	}, "thk_compact_real")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:      "prv_compact",
		Name:    "Codex Compact",
		Type:    ProviderOpenAICodex,
		BaseURL: "https://chatgpt.example/backend-api/codex",
		Status:  StatusActive,
		Healthy: true,
		Options: map[string]string{"allowed_codex_hosts": "chatgpt.example"},
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_compact",
		ProviderID:   provider.ID,
		Name:         "Compact Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Options:      codexCapabilityOptionsForTest("gpt-compact-upstream"),
		Credentials:  &ProviderResourceCredentials{AccessToken: "access_compact", AccountID: "account_compact"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-compact", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:            "route_compact",
		ModelName:     "gpt-compact",
		ProviderID:    provider.ID,
		ProviderModel: "gpt-compact-upstream",
		Status:        StatusActive,
	})

	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "compact-secret"})
	server.codexSubscription.MaxRequestRetries = 1
	var fingerprintHeaders []http.Header
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/backend-api/codex/responses/compact" {
			t.Fatalf("unexpected compact path: %s", req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer access_compact" {
			t.Fatalf("compact protocol headers missing: %#v", req.Header)
		}
		if req.Header.Get("session-id") == "" || req.Header.Get("session-id") == "session-compact" ||
			req.Header.Get("session_id") != req.Header.Get("session-id") || req.Header.Get("thread-id") == "" ||
			req.Header.Get("x-codex-installation-id") == "client-installation" || req.Header.Get("x-codex-parent-thread-id") != "" {
			t.Fatalf("compact fingerprint was not converged: %#v", req.Header)
		}
		fingerprintHeaders = append(fingerprintHeaders, req.Header.Clone())
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "gpt-compact-upstream" || payload["instructions"] != "preserve this" {
			t.Fatalf("compact request was rewritten incorrectly: %#v", payload)
		}
		if _, ok := payload["client_metadata"]; ok {
			t.Fatalf("compact request forwarded unsupported client_metadata: %#v", payload)
		}
		if len(fingerprintHeaders) == 1 {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"retry compact"}`)),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":       []string{"application/json"},
				"X-Codex-Turn-State": []string{"compact-turn-state"},
				"X-Request-Id":       []string{"upstream-compact-request"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"output":[{"type":"message","role":"assistant","content":[]}]}`)),
			Request: req,
		}, nil
	})}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(
		`{"model":"gpt-compact","input":[],"instructions":"preserve this","client_metadata":{"session_id":"session-compact"}}`,
	))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-codex-installation-id", "client-installation")
	req.Header.Set("x-codex-parent-thread-id", "client-parent-thread")
	req.Header.Set("x-codex-turn-metadata", `{"installation_id":"client-installation","session_id":"session-compact","thread_id":"client-thread","unrelated":9007199254740993,"parent_thread_id":"compact-parent-thread","forked_from_thread_id":"compact-fork-thread","parent_turn_id":"compact-parent-turn"}`)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("compact request failed: %d %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Codex-Turn-State") != "compact-turn-state" ||
		rr.Header().Get("X-Tokenhub-Upstream-Request-Id") != "upstream-compact-request" {
		t.Fatalf("compact response metadata missing: %#v", rr.Header())
	}
	if len(fingerprintHeaders) != 2 {
		t.Fatalf("expected one compact retry, got %d attempts", len(fingerprintHeaders))
	}
	for _, key := range []string{
		"session-id", "session_id", "thread-id", "x-client-request-id",
		"x-codex-installation-id", "x-codex-window-id", "x-codex-turn-metadata",
	} {
		if fingerprintHeaders[0].Get(key) != fingerprintHeaders[1].Get(key) {
			t.Fatalf("compact retry changed fingerprint header %s: first=%q second=%q", key, fingerprintHeaders[0].Get(key), fingerprintHeaders[1].Get(key))
		}
	}
	var compactTurnMetadata map[string]any
	if err := decodeCodexMetadataJSON([]byte(fingerprintHeaders[0].Get("x-codex-turn-metadata")), &compactTurnMetadata); err != nil {
		t.Fatalf("decode compact turn metadata: %v", err)
	}
	if number, ok := compactTurnMetadata["unrelated"].(json.Number); !ok || number.String() != "9007199254740993" {
		t.Fatalf("compact turn metadata large integer changed: %#v", compactTurnMetadata["unrelated"])
	}
	assertCodexLineageFieldsAbsent(t, compactTurnMetadata, "compact turn metadata")
	var bindings []AdapterSessionBinding
	if err := store.db.Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].ResourceID != resource.ID {
		t.Fatalf("compact request did not use durable Session affinity: %+v", bindings)
	}
	descriptor, ok := server.adapterRegistry.Describe(ProviderOpenAICodex)
	if !ok || !adapterSupports(descriptor, AdapterCapabilityCompact) || adapterSupports(descriptor, AdapterCapabilityWebSocket) {
		t.Fatalf("Codex capabilities are not truthful: %+v", descriptor)
	}
}

func TestProviderMonitoringUsesBackendProbeAndCachedQuota(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_monitoring",
		Name:    "Codex Monitoring",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_monitoring",
		ProviderID:   provider.ID,
		Name:         "Monitoring Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials:  &ProviderResourceCredentials{AccessToken: "access_monitoring", AccountID: "account_monitoring"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.RecordProviderObservation(ProviderObservation{
		ProviderID:  provider.ID,
		ResourceID:  resource.ID,
		AdapterType: provider.Type,
		Source:      "active_probe",
		Operation:   "responses",
		Success:     true,
		LatencyMS:   321,
	})
	quotaCalls := 0
	server := New(store)
	server.codexSubscription.QuotaURL = "https://chatgpt.example/backend-api/wham/usage"
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		quotaCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"plan_type":"pro","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":25,"reset_at":1999999999}}}`,
			)),
			Request: req,
		}, nil
	})}
	invoke := func() []ProviderMonitoringSnapshot {
		request := httptest.NewRequest(http.MethodGet, "/api/admin/providers/monitoring", nil)
		request.Header.Set("Authorization", "Bearer dev_admin_token")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("monitoring request failed: %d %s", response.Code, response.Body.String())
		}
		var payload struct {
			Data []ProviderMonitoringSnapshot `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Data
	}
	first := invoke()
	second := invoke()
	if quotaCalls != 1 {
		t.Fatalf("quota cache did not prevent duplicate upstream requests: %d", quotaCalls)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("unexpected monitoring snapshots: first=%+v second=%+v", first, second)
	}
	snapshot := first[0]
	if snapshot.State != "healthy" || snapshot.ActiveProbe.Source != "active_probe" ||
		snapshot.ActiveProbe.LatencyMS != 321 || snapshot.Quota.RemainingPercent != 75 ||
		snapshot.Quota.SuccessfulAccounts != 1 {
		t.Fatalf("monitoring did not preserve source semantics or quota: %+v", snapshot)
	}
}

func TestProviderMonitoringRecoversFromHistoricalFailure(t *testing.T) {
	now := time.Now().UTC()
	signal := observationMonitoringSignal(now, "gateway_request", []ProviderObservation{
		{Success: false, ErrorCode: "internal_error", ObservedAt: now.Add(-time.Minute)},
		{Success: true, ObservedAt: now},
	})
	if signal.State != "degraded" || signal.SuccessRate != 50 {
		t.Fatalf("a successful latest request must recover Functional Down: %+v", signal)
	}
}

func TestCodexSubscriptionProbeAllowsFastModeForAnyModel(t *testing.T) {
	adapter := CodexSubscriptionAdapter{
		Client: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			reasoning, _ := payload["reasoning"].(map[string]any)
			if payload["model"] != "gpt-5.4" || payload["service_tier"] != "priority" || reasoning["effort"] != "high" {
				t.Fatalf("unexpected fast probe payload: %#v", payload)
			}
			stream := strings.Join([]string{
				"event: response.output_text.delta",
				`data: {"type":"response.output_text.delta","delta":"Fast probe works."}`,
				"",
				"event: response.completed",
				`data: {"type":"response.completed","response":{"id":"resp_fast_probe","status":"completed","service_tier":"priority","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
				"",
			}, "\n")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(stream)),
				Request:    req,
			}, nil
		})},
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{AccessToken: "access_fast_probe", AccountID: "account_fast_probe"}, nil
		},
	}
	provider := Provider{
		ID:      "prv_fast_probe",
		Name:    "Fast Probe",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
		Options: map[string]string{"resource_id": "rsrc_fast_probe"},
	}
	resource := ProviderResource{
		ID:           "rsrc_fast_probe",
		ProviderID:   provider.ID,
		Name:         "Fast Probe Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	}

	result, err := adapter.Probe(context.Background(), provider, resource, ProviderProbeRequest{
		Model:           "gpt-5.4",
		ReasoningEffort: "high",
		Speed:           "fast",
		Prompt:          "Confirm fast mode.",
	})
	if err != nil {
		t.Fatalf("fast probe with non-Luna model failed: %v", err)
	}
	if result.Model != "gpt-5.4" || result.Speed != "fast" || result.UpstreamServiceTier != "priority" || result.OutputText != "Fast probe works." {
		t.Fatalf("unexpected fast probe result: %+v", result)
	}
}

func TestProviderTestUsesCodexDefaultProbeProfile(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_default_probe",
		Name:    "Codex Default Probe",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_default_probe",
		ProviderID:   provider.ID,
		Name:         "Default Probe Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials:  &ProviderResourceCredentials{AccessToken: "access_probe", AccountID: "account_probe"},
	}); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		reasoning, _ := payload["reasoning"].(map[string]any)
		if payload["model"] != openAICodexDefaultProbeModel || payload["service_tier"] != nil || reasoning["effort"] != "medium" {
			t.Fatalf("unexpected default probe profile: %#v", payload)
		}
		if _, ok := payload["background"]; ok {
			t.Fatalf("default Codex probe must omit unsupported background parameter: %#v", payload)
		}
		stream := strings.Join([]string{
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"Codex connection works."}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_probe","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
			Request:    req,
		}, nil
	})}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/providers/"+provider.ID+"/test", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer dev_admin_token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("provider probe failed: %d %s", response.Code, response.Body.String())
	}
	var result ProviderProbeBatchResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Succeeded != 1 || result.Failed != 0 || !result.Healthy ||
		len(result.Results) != 1 || result.Results[0].Speed != "standard" {
		t.Fatalf("unexpected provider probe result: %+v", result)
	}
}

func TestProviderAdapterCompatibilityAndLegacyMigration(t *testing.T) {
	store := NewMemoryStore()
	codex := store.AddProvider(Provider{
		ID:      "prv_strict_codex",
		Name:    "Strict Codex",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ProviderID:   codex.ID,
		Name:         "Invalid API Key",
		ResourceType: ProviderResourceAPIKey,
		Status:       StatusActive,
		Healthy:      true,
	}); AsHTTPError(err).Code != "provider_adapter_resource_conflict" {
		t.Fatalf("Codex Provider accepted API-key resource: %v", err)
	}
	openAIWithKey := store.AddProvider(Provider{
		ID:      "prv_strict_openai",
		Name:    "Strict OpenAI",
		Type:    ProviderOpenAI,
		APIKey:  "upstream-real-key",
		Status:  StatusActive,
		Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ProviderID:   openAIWithKey.ID,
		Name:         "Invalid Subscription",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	}); AsHTTPError(err).Code != "provider_adapter_resource_conflict" {
		t.Fatalf("OpenAI API Provider accepted subscription resource: %v", err)
	}
	emptyOpenAI := store.AddProvider(Provider{
		ID:      "prv_auto_codex",
		Name:    "Auto Codex",
		Type:    ProviderOpenAI,
		Status:  StatusActive,
		Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ProviderID:   emptyOpenAI.ID,
		Name:         "First Subscription",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	}); err != nil {
		t.Fatal(err)
	}
	normalized, ok := integrationProvider(store, emptyOpenAI.ID)
	if !ok || normalized.Type != ProviderOpenAICodex || normalized.BaseURL != openAICodexBaseURL {
		t.Fatalf("empty OpenAI Provider was not normalized to Codex: %+v", normalized)
	}

	legacy := store.AddProvider(Provider{
		ID:               "prv_legacy_mixed",
		Name:             "Legacy Mixed",
		Type:             ProviderOpenAI,
		APIKey:           "legacy-upstream-key",
		Status:           StatusActive,
		Healthy:          true,
		Priority:         3,
		Headers:          map[string]string{"X-Tenant": "legacy-tenant-secret"},
		SensitiveHeaders: []string{"X-Tenant"},
	})
	direct := ProviderResource{
		ID:           "rsrc_legacy_direct",
		ProviderID:   legacy.ID,
		Name:         "Legacy Direct",
		ResourceType: ProviderResourceAPIKey,
		Status:       StatusActive,
		Healthy:      true,
	}
	subscription := ProviderResource{
		ID:           "rsrc_legacy_subscription",
		ProviderID:   legacy.ID,
		Name:         "Legacy Subscription",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	}
	if err := store.db.Create(&direct).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&subscription).Error; err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-legacy-codex", Category: "codex", Family: "codex", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:                 "route_legacy_subscription",
		ModelName:          "gpt-legacy-codex",
		ProviderID:         legacy.ID,
		ProviderResourceID: subscription.ID,
		ProviderModel:      "gpt-legacy-codex",
		Status:             StatusActive,
	})
	store.AddRoute(ModelRoute{
		ID:            "route_legacy_generic",
		ModelName:     "gpt-legacy-codex",
		ProviderID:    legacy.ID,
		ProviderModel: "gpt-legacy-codex",
		Status:        StatusActive,
	})
	if err := store.NormalizeProviderAdapterTypes(context.Background()); err != nil {
		t.Fatal(err)
	}
	providersAfterFirst := store.ListProviders()
	routesAfterFirst := store.ListRoutes()
	if err := store.NormalizeProviderAdapterTypes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.ListProviders()) != len(providersAfterFirst) || len(store.ListRoutes()) != len(routesAfterFirst) {
		t.Fatalf("legacy migration is not idempotent: providers %d/%d routes %d/%d",
			len(providersAfterFirst), len(store.ListProviders()), len(routesAfterFirst), len(store.ListRoutes()))
	}
	var splitProvider Provider
	for _, provider := range store.ListProviders() {
		if provider.Type == ProviderOpenAICodex && provider.ID != emptyOpenAI.ID && provider.ID != codex.ID {
			splitProvider = provider
		}
	}
	if splitProvider.ID == "" {
		t.Fatal("mixed legacy Provider was not split")
	}
	if len(splitProvider.Headers) != 0 || len(splitProvider.SensitiveHeaders) != 0 {
		t.Fatalf("Codex split inherited unsupported custom headers: %+v", splitProvider)
	}
	directProvider, ok := integrationProvider(store, legacy.ID)
	if !ok || directProvider.Headers["X-Tenant"] != "legacy-tenant-secret" {
		t.Fatalf("direct Provider lost its sensitive custom header: %+v", directProvider)
	}
	migratedSubscription, ok := integrationProviderResource(store, subscription.ID)
	if !ok || migratedSubscription.ProviderID != splitProvider.ID {
		t.Fatalf("subscription resource was not moved to Codex Provider: %+v", migratedSubscription)
	}
	migratedRoutes := 0
	for _, route := range store.ListRoutes() {
		if route.ProviderID == splitProvider.ID {
			migratedRoutes++
		}
	}
	if migratedRoutes != 2 {
		t.Fatalf("expected resource route and cloned generic route on split Provider, got %d", migratedRoutes)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func codexCapabilityOptionsForTest(models ...string) map[string]string {
	encoded, _ := json.Marshal(models)
	return map[string]string{
		codexResourceSupportedModelsOption: string(encoded),
		codexResourceModelsFetchedAtOption: time.Now().UTC().Format(time.RFC3339Nano),
	}
}
