package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKronkCatalogTemplateHasLocalDefaults(t *testing.T) {
	entry, _, found, err := newProviderCatalogService(NewMemoryStore(), "").Get(context.Background(), ProviderKronk, false)
	if err != nil || !found {
		t.Fatalf("get Kronk catalog: found=%v err=%v", found, err)
	}
	if entry.Type != ProviderKronk || entry.BaseURL != kronkDefaultBaseURL || entry.DocURL != kronkDocURL {
		t.Fatalf("unexpected Kronk catalog entry: %+v", entry)
	}
}

func TestKronkDiscoveryPreservesModelIDsAndOptionalBearerToken(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		token string
	}{
		{name: "without authentication"},
		{name: "with application token", token: "kronk-secret"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				wantAuth := ""
				if testCase.token != "" {
					wantAuth = "Bearer " + testCase.token
				}
				if got := r.Header.Get("authorization"); got != wantAuth {
					t.Fatalf("authorization = %q, want %q", got, wantAuth)
				}
				writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{
					{"id": "org/model:Q4_K_M.gguf"},
					{"id": "whisper/large-v3:fp16"},
				}})
			}))
			defer upstream.Close()

			entry, err := KronkProviderCatalogFromUpstream(context.Background(), upstream.Client(), ProviderCreateRequest{
				BaseURL: upstream.URL + "/v1", APIKey: testCase.token,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := []string{entry.Models[0].ID, entry.Models[1].ID}
			want := []string{"org/model:Q4_K_M.gguf", "whisper/large-v3:fp16"}
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("model IDs = %q, want %q", got, want)
			}
		})
	}
}

func TestKronkConnectionTestChecksServiceAndModelsWithoutToken(t *testing.T) {
	var mu sync.Mutex
	paths := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("authorization") != "" {
			t.Fatal("unauthenticated Kronk received an authorization header")
		}
		switch r.URL.Path {
		case "/v1/liveness", "/v1/readiness":
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		case "/v1/models":
			writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": "local/model:q4"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	response := doJSON(t, newTestServer(), http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
		"name": "Kronk", "type": ProviderKronk, "base_url": upstream.URL + "/v1",
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("connection test = %d: %s", response.Code, response.Body)
	}
	var result struct {
		Healthy bool              `json:"healthy"`
		Health  KronkHealthResult `json:"health"`
	}
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Healthy || !result.Health.Live || !result.Health.Ready || !result.Health.ModelReady || result.Health.ModelsCount != 1 {
		t.Fatalf("unexpected health result: %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"/v1/liveness", "/v1/readiness", "/v1/models"} {
		if !stringInList(want, paths) {
			t.Fatalf("health check did not call %s: %v", want, paths)
		}
	}
}

func TestKronkConnectionTestDistinguishesHealthyServiceWithoutModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/liveness", "/v1/readiness":
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		case "/v1/models":
			writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	response := doJSON(t, newTestServer(), http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
		"name": "Kronk", "type": ProviderKronk, "base_url": upstream.URL + "/v1",
	}, "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("connection test = %d: %s", response.Code, response.Body)
	}
	var result struct {
		Error struct {
			Code    string            `json:"code"`
			Details KronkHealthResult `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error.Code != "kronk_models_unavailable" || !result.Error.Details.Live || !result.Error.Details.Ready || result.Error.Details.ModelReady || result.Error.Details.ModelsCount != 0 {
		t.Fatalf("unexpected structured health failure: %+v", result)
	}
}

func TestKronkAdapterForwardsInferenceAndUsage(t *testing.T) {
	const token = "kronk-application-token"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("authorization"); got != "Bearer "+token {
			t.Fatalf("authorization = %q", got)
		}
		switch r.URL.Path {
		case "/v1/chat/completions":
			var request map[string]any
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request["stream"] == true {
				w.Header().Set("content-type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
				_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": "hello"}}},
				"usage":   map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5},
			})
		case "/v1/responses":
			var request map[string]any
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request["stream"] == true {
				w.Header().Set("content-type", "text/event-stream")
				_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":4,\"output_tokens\":6,\"total_tokens\":10}}}\n\n")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"output_text": "response", "usage": map[string]any{"input_tokens": 4, "output_tokens": 6, "total_tokens": 10},
			})
		case "/v1/embeddings":
			writeJSON(w, http.StatusOK, map[string]any{
				"data":  []map[string]any{{"embedding": []float64{0.1, 0.2}}},
				"usage": map[string]any{"prompt_tokens": 7, "total_tokens": 7},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	adapter := KronkAdapter{OpenAICompatibleAdapter: OpenAICompatibleAdapter{Client: upstream.Client(), StreamClient: upstream.Client()}}
	provider := Provider{Type: ProviderKronk, BaseURL: upstream.URL + "/v1", APIKey: token}
	_, chatUsage, err := adapter.Chat(context.Background(), provider, "org/model:q4", simpleChatRequest())
	if err != nil || chatUsage.TotalTokens != 5 {
		t.Fatalf("chat usage=%+v err=%v", chatUsage, err)
	}
	var stream bytes.Buffer
	streamUsage, err := adapter.ChatStream(context.Background(), provider, "org/model:q4", simpleChatRequest(), &stream)
	if err != nil || streamUsage.TotalTokens != 5 || !strings.Contains(stream.String(), "hello") {
		t.Fatalf("stream usage=%+v body=%q err=%v", streamUsage, stream.String(), err)
	}
	_, responseUsage, err := adapter.Responses(context.Background(), provider, "org/model:q4", ResponsesRequest{})
	if err != nil || responseUsage.TotalTokens != 10 {
		t.Fatalf("responses usage=%+v err=%v", responseUsage, err)
	}
	responseStream, err := adapter.OpenResponses(context.Background(), provider, "org/model:q4", ResponsesRequest{}, nil)
	if err != nil {
		t.Fatalf("open responses stream: %v", err)
	}
	responseStreamBody, readErr := io.ReadAll(responseStream.Body)
	_ = responseStream.Body.Close()
	if readErr != nil || !strings.Contains(string(responseStreamBody), "response.completed") {
		t.Fatalf("responses stream body=%q err=%v", responseStreamBody, readErr)
	}
	_, embeddingUsage, err := adapter.Embeddings(context.Background(), provider, "embed/model:f16", EmbeddingsRequest{})
	if err != nil || embeddingUsage.TotalTokens != 7 {
		t.Fatalf("embeddings usage=%+v err=%v", embeddingUsage, err)
	}
}

func TestKronkTopLevelErrorIsNormalizedAndSecretIsRedacted(t *testing.T) {
	const token = "never-leak-kronk-token"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"code": "resource_exhausted", "message": "GPU memory exhausted for " + token,
		})
	}))
	defer upstream.Close()

	adapter := KronkAdapter{OpenAICompatibleAdapter: OpenAICompatibleAdapter{Client: upstream.Client()}}
	_, _, err := adapter.Chat(context.Background(), Provider{Type: ProviderKronk, BaseURL: upstream.URL, APIKey: token}, "model", simpleChatRequest())
	if err == nil {
		t.Fatal("expected Kronk error")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusServiceUnavailable || httpErr.Code != "provider_resource_exhausted" {
		t.Fatalf("unexpected normalized error: %+v", httpErr)
	}
	if strings.Contains(httpErr.Message, token) || !strings.Contains(httpErr.Message, "resource_exhausted") {
		t.Fatalf("unsafe or incomplete error message: %q", httpErr.Message)
	}
	if providerErrorDisposition(err) != ProviderErrorTransientSame {
		t.Fatalf("unexpected retry classification: %s", providerErrorDisposition(err))
	}
}

func TestKronkNonStreamingTimeoutIsClassified(t *testing.T) {
	releaseUpstream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseUpstream
	}))
	t.Cleanup(func() {
		close(releaseUpstream)
		upstream.CloseClientConnections()
		upstream.Close()
	})

	client := upstream.Client()
	client.Timeout = 50 * time.Millisecond
	adapter := KronkAdapter{OpenAICompatibleAdapter: OpenAICompatibleAdapter{Client: client}}
	startedAt := time.Now()
	_, _, err := adapter.Chat(context.Background(), Provider{BaseURL: upstream.URL}, "model", simpleChatRequest())
	elapsed := time.Since(startedAt)
	if err == nil {
		t.Fatal("expected Kronk inference timeout")
	}
	if elapsed >= time.Second {
		t.Fatalf("Kronk timeout returned after %s, want less than 1s", elapsed)
	}
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusGatewayTimeout || httpErr.Code != "provider_upstream_timeout" {
		t.Fatalf("timeout = %+v, want 504 provider_upstream_timeout", httpErr)
	}
	if providerErrorDisposition(err) != ProviderErrorTransientSame {
		t.Fatalf("timeout retry classification = %q", providerErrorDisposition(err))
	}
}

func TestKronkDiscoveryRejectsDestinationOverrideBeforeSendingStoredSecrets(t *testing.T) {
	var storedCalls int
	stored := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storedCalls++
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": "local/model"}}})
	}))
	defer stored.Close()
	var overriddenCalls int
	overridden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		overriddenCalls++
		if r.Header.Get("authorization") != "" || r.Header.Get("x-kronk-secret") != "" {
			t.Fatal("stored Kronk credentials were sent to an overridden destination")
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": "attacker/model"}}})
	}))
	defer overridden.Close()

	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_kronk_saved", Name: "Kronk", Type: ProviderKronk, BaseURL: stored.URL + "/v1",
		APIKey: "saved-kronk-token", Headers: map[string]string{"X-Kronk-Secret": "saved-header"},
		SensitiveHeaders: []string{"X-Kronk-Secret"}, Status: StatusActive, Healthy: true,
	})
	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/provider-catalog/kronk", map[string]any{
		"provider_id": provider.ID,
		"base_url":    overridden.URL + "/v1",
	}, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "provider_base_url_override_forbidden") {
		t.Fatalf("destination override = %d: %s", response.Code, response.Body)
	}
	if storedCalls != 0 || overriddenCalls != 0 {
		t.Fatalf("discovery contacted an upstream before rejecting override: stored=%d overridden=%d", storedCalls, overriddenCalls)
	}
}

func TestKronkUpstreamStreamInterruptionIsReported(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("content-length", "4096")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	adapter := KronkAdapter{OpenAICompatibleAdapter: OpenAICompatibleAdapter{
		Client: upstream.Client(), StreamClient: upstream.Client(), StreamIdleTimeout: time.Second,
	}}
	var output bytes.Buffer
	_, err := adapter.ChatStream(context.Background(), Provider{BaseURL: upstream.URL}, "model", simpleChatRequest(), &output)
	if err == nil {
		t.Fatal("expected interrupted Kronk stream")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadGateway || httpErr.Code != "provider_stream_interrupted" {
		t.Fatalf("interrupted stream = %+v, want 502 provider_stream_interrupted", httpErr)
	}
	if !strings.Contains(output.String(), "partial") {
		t.Fatalf("partial upstream data was discarded: %q", output.String())
	}
}

func TestKronkDiscoveryReconcilesRemovedModelsOnlyAfterSuccess(t *testing.T) {
	models := []string{"org/model:a", "org/model:b"}
	fail := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		data := make([]map[string]any, 0, len(models))
		for _, id := range models {
			data = append(data, map[string]any{"id": id})
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_kronk", Name: "Kronk", Type: ProviderKronk, BaseURL: upstream.URL + "/v1", Status: StatusActive, Healthy: true})
	for _, id := range models {
		store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: id, DisplayName: id, Source: "kronk-discovery", Status: StatusActive, Metadata: map[string]string{"kronk_available": "true"}})
	}
	app := New(store).Handler()
	models = models[:1]
	response := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/kronk", map[string]any{"provider_id": provider.ID}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("discovery = %d: %s", response.Code, response.Body)
	}
	assertKronkModelStatus(t, store, "org/model:a", StatusActive)
	assertKronkModelStatus(t, store, "org/model:b", StatusDisabled)

	fail = true
	response = doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/kronk", map[string]any{"provider_id": provider.ID}, "")
	if response.Code == http.StatusOK {
		t.Fatal("expected failed discovery")
	}
	assertKronkModelStatus(t, store, "org/model:a", StatusActive)
	assertKronkModelStatus(t, store, "org/model:b", StatusDisabled)
}

func TestKronkImportIsIdempotentAndKeepsExactRouteModel(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()
	modelID := "org/private/model:Q5_K_M.gguf"
	catalogModel := ProviderCatalogModel{
		ID: modelID, Name: modelID, DisplayName: modelID, Type: "chat",
		Metadata: map[string]string{"source": "kronk-discovery", "kronk_available": "true"},
	}
	create := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id": "prv_kronk_import", "catalog_id": ProviderKronk, "name": "Kronk", "type": ProviderKronk,
		"base_url": "http://127.0.0.1:11435/v1", "api_key": "saved-kronk-token", "selected_models": []string{modelID},
		"custom_models": []ProviderCatalogModel{catalogModel},
	}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create Kronk provider = %d: %s", create.Code, create.Body)
	}
	update := doJSON(t, app, http.MethodPatch, "/api/admin/providers/prv_kronk_import", map[string]any{
		"catalog_id": ProviderKronk, "selected_models": []string{modelID},
		"custom_models": []ProviderCatalogModel{catalogModel}, "clear_api_key": true,
	}, "")
	if update.Code != http.StatusOK {
		t.Fatalf("repeat Kronk import = %d: %s", update.Code, update.Body)
	}
	providerModels := store.ListProviderModels()
	if len(providerModels) != 1 || providerModels[0].UpstreamModel != modelID {
		t.Fatalf("idempotent inventory = %+v", providerModels)
	}
	updatedProvider, found := store.GetProvider("prv_kronk_import")
	if !found || updatedProvider.APIKey != "" {
		t.Fatalf("Kronk token was not cleared: found=%v provider=%+v", found, updatedProvider)
	}
	store.AddModel(Model{Name: "standard-local-chat", Family: "local", Modality: "chat", Status: StatusActive})
	route := store.AddRoute(ModelRoute{ModelName: "standard-local-chat", ProviderID: "prv_kronk_import", ProviderModel: modelID, Status: StatusActive})
	if route.ProviderModel != modelID {
		t.Fatalf("route model = %q, want %q", route.ProviderModel, modelID)
	}
}

func TestKronkStreamingCancellationStopsUpstream(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(upstreamCanceled)
	}))
	defer upstream.Close()

	adapter := KronkAdapter{OpenAICompatibleAdapter: OpenAICompatibleAdapter{Client: upstream.Client(), StreamClient: upstream.Client()}}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := adapter.ChatStream(ctx, Provider{Type: ProviderKronk, BaseURL: upstream.URL}, "model", simpleChatRequest(), io.Discard)
		finished <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("Kronk stream did not stop after client cancellation")
	}
	select {
	case <-upstreamCanceled:
	case <-time.After(time.Second):
		t.Fatal("Kronk upstream kept running after cancellation")
	}
}

func assertKronkModelStatus(t *testing.T, store Store, id string, want string) {
	t.Helper()
	for _, model := range store.ListProviderModels() {
		if model.UpstreamModel == id {
			if model.Status != want {
				t.Fatalf("model %s status=%s, want %s", id, model.Status, want)
			}
			return
		}
	}
	t.Fatalf("model %s not found", id)
}
