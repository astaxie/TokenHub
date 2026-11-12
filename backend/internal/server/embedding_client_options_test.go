package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEmbeddingClientOptionsDoNotChangeSemanticRequest(t *testing.T) {
	const original = `{"model":"public","input":["first","second"],"dimensions":2,"encoding_format":"base64","input_type":"query","user":"synthetic-user"}`
	var baseline EmbeddingsRequest
	if err := json.Unmarshal([]byte(original), &baseline); err != nil {
		t.Fatal(err)
	}
	baselineKey, err := embeddingCacheKey(CallContext{}, "test-space", baseline)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"true", "false", "null"} {
		t.Run(value, func(t *testing.T) {
			var request EmbeddingsRequest
			payload := strings.TrimSuffix(original, "}") + `,"drop_params":` + value + `}`
			if err := json.Unmarshal([]byte(payload), &request); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(request, baseline) {
				t.Fatalf("semantic request changed: %+v", request)
			}
			encoded, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "drop_params") {
				t.Fatalf("client option retained: %s", encoded)
			}
			key, err := embeddingCacheKey(CallContext{}, "test-space", request)
			if err != nil {
				t.Fatal(err)
			}
			if key != baselineKey {
				t.Fatalf("client option changed cache key: got %q, want %q", key, baselineKey)
			}
		})
	}
}

func TestEmbeddingGatewayRAGFlowClientOptions(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if _, ok := payload["drop_params"]; ok {
			t.Errorf("client option leaked upstream: %v", payload)
		}
		if payload["model"] != "qwen3-embedding-4b" || payload["dimensions"] != float64(2) || payload["encoding_format"] != "float" {
			t.Errorf("upstream options changed: %v", payload)
		}
		if !reflect.DeepEqual(payload["input"], []any{"first", "second"}) {
			t.Errorf("input batch changed: %v", payload["input"])
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"object":"list","data":[{"index":1,"embedding":[3,4]},{"index":0,"embedding":[1,2]}],"usage":{"prompt_tokens":4,"total_tokens":4}}`)); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "RAGFlow compatibility", Status: StatusActive})
	store.AddModel(Model{Name: "local-embedding", Modality: "embedding", Status: StatusActive, EmbeddingPriceUSDPer1M: 1})
	store.AddProvider(Provider{ID: "ragflow-provider", Type: ProviderOpenAICompatible, BaseURL: upstream.URL + "/v1", Status: StatusActive, Healthy: true})
	store.AddProviderModel(ProviderModel{ProviderID: "ragflow-provider", UpstreamModel: "qwen3-embedding-4b", Modality: "embedding", InputPriceUSDPer1M: 1})
	store.AddRoute(ModelRoute{ID: "ragflow-route", ModelName: "local-embedding", ProviderID: "ragflow-provider", ProviderModel: "qwen3-embedding-4b", Status: StatusActive, Weight: 100})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "ragflow", Status: StatusActive, Allowed: []string{"local-embedding"}}, "thk_synthetic_ragflow_compatibility")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store)
	defer func() { _ = app.Shutdown(context.Background()) }()
	tests := []struct {
		name       string
		extra      map[string]any
		wantStatus int
		wantError  string
	}{
		{name: "absent", wantStatus: 200},
		{name: "true", extra: map[string]any{"drop_params": true}, wantStatus: 200},
		{name: "false", extra: map[string]any{"drop_params": false}, wantStatus: 200},
		{name: "null", extra: map[string]any{"drop_params": nil}, wantStatus: 200},
		{name: "base64", extra: map[string]any{"drop_params": true, "encoding_format": "base64"}, wantStatus: 200},
		{name: "string", extra: map[string]any{"drop_params": "true"}, wantStatus: 400, wantError: "invalid_request"},
		{name: "number", extra: map[string]any{"drop_params": 1}, wantStatus: 400, wantError: "invalid_request"},
		{name: "object", extra: map[string]any{"drop_params": map[string]any{}}, wantStatus: 400, wantError: "invalid_request"},
		{name: "array", extra: map[string]any{"drop_params": []any{}}, wantStatus: 400, wantError: "invalid_request"},
		{name: "unknown_field", extra: map[string]any{"drop_params": true, "unexpected": true}, wantStatus: 400, wantError: `unknown field \"unexpected\"`},
		{name: "unsupported_output", extra: map[string]any{"drop_params": true, "output_type": "sparse"}, wantStatus: 400, wantError: `unknown field \"output_type\"`},
		{name: "unsupported_embedding_type", extra: map[string]any{"drop_params": true, "embedding_types": []string{"sparse"}}, wantStatus: 400, wantError: `unknown field \"embedding_types\"`},
		{name: "invalid_dimensions", extra: map[string]any{"drop_params": true, "dimensions": 0}, wantStatus: 400, wantError: "invalid_embedding_request"},
		{name: "invalid_encoding", extra: map[string]any{"drop_params": true, "encoding_format": "sparse"}, wantStatus: 400, wantError: "invalid_embedding_request"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{"model": "local-embedding", "input": []string{"first", "second"}, "dimensions": 2, "encoding_format": "float"}
			for key, value := range tc.extra {
				payload[key] = value
			}
			before := calls.Load()
			response := doJSON(t, app.Handler(), http.MethodPost, "/v1/embeddings", payload, secret)
			if response.Code != tc.wantStatus {
				t.Fatalf("status=%d, want=%d: %s", response.Code, tc.wantStatus, response.Body)
			}
			wantCalls := int64(0)
			if tc.wantStatus == http.StatusOK {
				wantCalls = 1
			}
			if got := calls.Load() - before; got != wantCalls {
				t.Fatalf("upstream calls=%d, want=%d", got, wantCalls)
			}
			if tc.wantStatus != http.StatusOK {
				if !strings.Contains(response.Body, tc.wantError) {
					t.Fatalf("missing error %q: %s", tc.wantError, response.Body)
				}
				return
			}
			var body struct {
				Model string `json:"model"`
				Data  []struct {
					Index     int `json:"index"`
					Embedding any `json:"embedding"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
				t.Fatal(err)
			}
			if body.Model != "local-embedding" || len(body.Data) != 2 {
				t.Fatalf("unexpected response: %s", response.Body)
			}
			for i, item := range body.Data {
				vector, err := embeddingVector(item.Embedding)
				if err != nil || item.Index != i || !reflect.DeepEqual(vector, []float32{float32(i*2 + 1), float32(i*2 + 2)}) {
					t.Fatalf("batch vector or index changed: %+v, %v", item, err)
				}
				_, encoded := item.Embedding.(string)
				if encoded != (payload["encoding_format"] == "base64") {
					t.Fatalf("encoding changed: %s", response.Body)
				}
			}
		})
	}
}
