package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRerankFinalValidationPreservesSafetyEdits(t *testing.T) {
	request := RerankRequest{Model: "m", Query: "q", Documents: []string{"private-fragment"}, ReturnDocuments: true}
	for _, document := range []any{map[string]any{"text": "[REDACTED]"}, nil} {
		item := map[string]any{"index": 0, "relevance_score": 0.9}
		if document != nil {
			item["document"] = document
		}
		safe := map[string]any{"model": "m", "results": []any{item}, "safe_metadata": "preserve"}
		got, err := validateRerankResult(safe, request)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, safe) {
			t.Fatalf("safety response rewritten: got=%v want=%v", got, safe)
		}
	}
	bad := map[string]any{"model": "m", "results": []any{map[string]any{"index": 2, "relevance_score": 0.9}}}
	if _, err := validateRerankResult(bad, request); err == nil {
		t.Fatal("invalid indices accepted")
	}
}

func TestAdminRerankLoadsProviderAndResourceExecutionConfig(t *testing.T) {
	for _, withResource := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider", true: "resource"}[withResource], func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				wantKey, wantHeader, wantPath := "Bearer synthetic-provider-key", "provider-header", "/provider/rerank"
				if withResource {
					wantKey, wantHeader, wantPath = "Bearer synthetic-resource-key", "resource-header", "/resource/native"
				}
				if r.Header.Get("Authorization") != wantKey || r.Header.Get("X-Test-Secret") != wantHeader || r.URL.Path != wantPath {
					t.Errorf("execution config was not loaded: path=%s authorization_present=%v", r.URL.Path, r.Header.Get("Authorization") != "")
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				items := []any{map[string]any{"index": 0, "relevance_score": 0.9}}
				body := map[string]any{"results": items}
				if withResource {
					if payload["top_k"] != float64(1) || payload["top_n"] != nil {
						t.Errorf("resource protocol not used: %v", payload)
					}
					body = map[string]any{"data": items}
				}
				writeJSON(w, 200, body)
			}))
			defer upstream.Close()
			store := NewMemoryStore()
			if err := SeedDemoData(store); err != nil {
				t.Fatal(err)
			}
			store.AddProvider(Provider{ID: "admin-rerank", Type: ProviderOpenAICompatible, BaseURL: upstream.URL + "/provider", APIKey: "synthetic-provider-key", Headers: map[string]string{"X-Test-Secret": "provider-header"}, SensitiveHeaders: []string{"X-Test-Secret"}, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "jina"}})
			payload := map[string]any{"provider_id": "admin-rerank", "request": map[string]any{"model": "m", "query": "q", "documents": []string{"d"}, "top_n": 1}}
			if withResource {
				resource, err := store.AddProviderResource(ProviderResource{ProviderID: "admin-rerank", Name: "resource", ResourceType: "api_key", Status: StatusActive, Healthy: true, BaseURL: upstream.URL + "/resource", APIKey: "synthetic-resource-key", Headers: map[string]string{"X-Test-Secret": "resource-header"}, SensitiveHeaders: []string{"X-Test-Secret"}, Options: map[string]string{"rerank_protocol": "voyage", "rerank_path": "/native"}})
				if err != nil {
					t.Fatal(err)
				}
				payload["resource_id"] = resource.ID
			}
			response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/playground/rerank", payload, "")
			if response.Code != 200 || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}
