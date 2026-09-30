package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// Exercise the real gateway HTTP handler against an instrumented local upstream.
func TestEmbeddingRouteDiagnosticsHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, modality, path, protocol string
		price                          float64
		confirmed, missing             bool
		want                           int
	}{
		{"zero_unconfirmed_default_path", "embedding", "", "openai", 0, false, false, 200},
		{"zero_unconfirmed_explicit_path", "embedding", "/embeddings", "openai", 0, false, false, 200},
		{"zero_confirmed_default_path", "embedding", "", "openai", 0, true, false, 200},
		{"paid_default_path", "embedding", "", "openai", 1, false, false, 200},
		{"wrong_inventory_modality", "chat", "", "openai", 1, false, false, 501},
		{"missing_inventory_backfilled", "embedding", "", "openai", 1, false, true, 200},
		{"missing_inventory_after_start", "embedding", "", "openai", 1, false, true, 501},
		{"jina_zero_unconfirmed", "embedding", "", "jina", 0, false, false, 200},
		{"jina_zero_confirmed", "embedding", "", "jina", 0, true, false, 200},
		{"invalid_protocol", "embedding", "", "unsupported", 1, false, false, 501},
		{"non_text_inventory", "embedding", "", "openai", 1, false, false, 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				t.Logf("UPSTREAM method=%s path=%s model=%v", r.Method, r.URL.Path, body["model"])
				if r.URL.Path != "/v1/embeddings" {
					http.Error(w, "wrong path", 404)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"object":"list","model":"qwen3-embedding-4b","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`)
			}))
			defer upstream.Close()
			store := NewMemoryStore()
			project := store.CreateProject(Project{Name: "repro", Status: StatusActive})
			store.AddModel(Model{Name: "Local-Qwen-Embedding", Modality: "embedding", Status: StatusActive, EmbeddingPriceUSDPer1M: 1})
			store.AddProvider(Provider{ID: "repro-provider", Type: ProviderOpenAICompatible, BaseURL: upstream.URL + "/v1", Status: StatusActive, Healthy: true, Options: map[string]string{"embedding_protocol": tc.protocol, "embedding_path": tc.path}})
			if !tc.missing {
				metadata := map[string]string{}
				if tc.confirmed {
					metadata["retrieval_pricing_confirmed"] = "true"
				}
				var inputs []string
				if tc.name == "non_text_inventory" {
					inputs = []string{"image"}
				}
				store.AddProviderModel(ProviderModel{InputModalities: inputs, ProviderID: "repro-provider", UpstreamModel: "qwen3-embedding-4b", Modality: tc.modality, InputPriceUSDPer1M: tc.price, Metadata: metadata})
			}
			store.AddRoute(ModelRoute{ID: "repro-route", ModelName: "Local-Qwen-Embedding", ProviderID: "repro-provider", ProviderModel: "qwen3-embedding-4b", Status: StatusActive, Weight: 100})
			_, key, err := store.CreateAPIKey(project.ID, APIKey{Name: "repro", Status: StatusActive, Allowed: []string{"Local-Qwen-Embedding"}}, "thk_synthetic_local_reproduction")
			if err != nil {
				t.Fatal(err)
			}
			app := New(store)
			defer func() { _ = app.Shutdown(context.Background()) }()
			if tc.name == "missing_inventory_after_start" {
				if err := store.db.Where("provider_id = ?", "repro-provider").Delete(&ProviderModel{}).Error; err != nil {
					t.Fatal(err)
				}
			}
			gateway := httptest.NewServer(app.Handler())
			defer gateway.Close()
			req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/embeddings", strings.NewReader(`{"model":"Local-Qwen-Embedding","input":"hello"}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			resp, err := gateway.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want
			wantCalls := int64(0)
			if want == 200 {
				wantCalls = 1
			}
			var attempts int64
			if err := store.db.Model(&RouteAttemptLog{}).Count(&attempts).Error; err != nil {
				t.Fatal(err)
			}
			t.Logf("AUDIT route_attempts=%d", attempts)
			if attempts != wantCalls {
				t.Fatalf("want route attempts=%d, got %d", wantCalls, attempts)
			}
			t.Logf("RESULT status=%d upstream_calls=%d response=%s", resp.StatusCode, calls.Load(), body)
			if resp.StatusCode != want || calls.Load() != wantCalls {
				t.Fatalf("want status=%d calls=%d", want, wantCalls)
			}
			if want == 501 {
				expectedReason := ""
				switch tc.name {
				case "wrong_inventory_modality":
					expectedReason = "upstream_model_modality_mismatch"
				case "missing_inventory_after_start":
					expectedReason = "upstream_model_inventory_missing"
				case "invalid_protocol":
					expectedReason = "provider_capability_or_protocol_unsupported"
				case "non_text_inventory":
					expectedReason = "upstream_text_input_unsupported"
				}
				var result struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
						Details struct {
							Stage             string `json:"stage"`
							UpstreamAttempted *bool  `json:"upstream_attempted"`
							Reasons           []struct {
								Code       string `json:"code"`
								RouteCount int    `json:"route_count"`
							} `json:"reasons"`
						} `json:"details"`
					} `json:"error"`
				}
				if err := json.Unmarshal(body, &result); err != nil {
					t.Fatal(err)
				}
				var persisted RequestPayloadLog
				if err := store.db.First(&persisted).Error; err != nil {
					t.Fatal(err)
				}
				var auditResult map[string]any
				var clientResult map[string]any
				if err := json.Unmarshal([]byte(persisted.ResponseBody), &auditResult); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(body, &clientResult); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(auditResult["error"], clientResult["error"]) {
					t.Fatalf("audit lost diagnostic: %s", persisted.ResponseBody)
				}
				detail := result.Error.Details
				if result.Error.Code != "provider_capability_not_supported" || detail.Stage != "route_selection" || detail.UpstreamAttempted == nil || *detail.UpstreamAttempted || len(detail.Reasons) != 1 || detail.Reasons[0].Code != expectedReason || detail.Reasons[0].RouteCount != 1 {
					t.Fatalf("missing rejection diagnosis: %s", body)
				}
				if !strings.Contains(result.Error.Message, "No upstream request was sent") {
					t.Fatal("missing preflight explanation")
				}
			}
		})
	}
}
