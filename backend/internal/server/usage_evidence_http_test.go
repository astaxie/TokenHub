package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"tokenhub/backend/internal/metering"
)

func TestUsageEvidenceHTTPPreservesTokensWithoutPrice(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"id":"upstream-response","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":200,"total_tokens":1200,"prompt_tokens_details":{"cached_tokens":600},"cache_write_input_tokens":100,"cache_write_5m_input_tokens":60,"cache_write_1h_input_tokens":30}}`)
	}))
	defer upstream.Close()
	handler, store, key := newAnthropicGateway(t, upstream.URL, ProviderOpenAICompatible)
	defer func() { _ = store.Close() }()
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_claude_code" && model.UpstreamModel == "upstream-model" {
			if err := store.DeleteProviderModel(model.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	response := doAnthropicRequest(t, handler, "/v1/chat/completions", map[string]any{"model": "claude-tokenhub-test", "messages": []map[string]string{{"role": "user", "content": "hello"}}}, "Bearer "+key, "")
	if response.Code != 200 {
		t.Fatalf("gateway: %d %s", response.Code, response.Body.String())
	}
	requestID := response.Header().Get("x-request-id")
	if requestID == "" {
		t.Fatal("gateway did not identify the request")
	}
	result := doJSON(t, handler, "GET", "/api/admin/billing/evidence/"+requestID, nil, billingAdminToken(t, store))
	if result.Code != 200 {
		t.Fatalf("evidence: %d %s", result.Code, result.Body)
	}
	var body struct {
		Data []struct {
			Kind string `json:"kind"`
			Data struct {
				Attempts []struct {
					Pricing struct {
						Units  metering.Units `json:"units"`
						Reason string         `json:"reason"`
					} `json:"pricing"`
				} `json:"attempts"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(result.Body), &body); err != nil {
		t.Fatal(err)
	}
	for _, row := range body.Data {
		if row.Kind == "shadow_settlement" && len(row.Data.Attempts) == 1 {
			charge := row.Data.Attempts[0].Pricing
			if charge.Units != (metering.Units{Input: 300, CacheRead: 600, CacheWrite: 10, CacheWrite5m: 60, CacheWrite1h: 30, Output: 200}) {
				t.Fatalf("missing-price attempt lost usage: %+v", charge)
			}
			if charge.Reason != "missing_price" {
				t.Fatalf("expected unknown price: %+v", charge)
			}
			return
		}
	}
	t.Fatal("missing attempt evidence")
}

func TestUsageEvidenceHTTPDistinguishesAbsentAndExplicitZero(t *testing.T) {
	for _, tc := range []struct{ name, payload, state string }{{"absent", ``, "missing"}, {"zero", `,"usage":{"prompt_tokens":0,"completion_tokens":0}`, "reported"}} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "application/json")
				_, _ = io.WriteString(w, `{"id":"reply","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]`+tc.payload+`}`)
			}))
			defer upstream.Close()
			handler, store, key := newAnthropicGateway(t, upstream.URL, ProviderOpenAICompatible)
			defer func() { _ = store.Close() }()
			response := doAnthropicRequest(t, handler, "/v1/chat/completions", map[string]any{"model": "claude-tokenhub-test", "messages": []map[string]string{{"role": "user", "content": "hello"}}}, "Bearer "+key, "")
			if response.Code != 200 {
				t.Fatalf("gateway: %d %s", response.Code, response.Body.String())
			}
			result := doJSON(t, handler, "GET", "/api/admin/billing/evidence/"+response.Header().Get("x-request-id"), nil, billingAdminToken(t, store))
			var body struct {
				Data []struct {
					Kind string `json:"kind"`
					Data struct {
						Attempts []struct {
							Pricing struct {
								Evidence struct {
									Fields map[string]struct {
										State string `json:"state"`
										Value *int64 `json:"value"`
									} `json:"fields"`
								} `json:"usage_evidence"`
							} `json:"pricing"`
						} `json:"attempts"`
					} `json:"data"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(result.Body), &body); err != nil {
				t.Fatal(err)
			}
			for _, row := range body.Data {
				if row.Kind == "shadow_settlement" && len(row.Data.Attempts) == 1 {
					field := row.Data.Attempts[0].Pricing.Evidence.Fields["input_total"]
					if field.State != tc.state {
						t.Fatalf("wrong persisted presence: %+v", field)
					}
					if tc.state == "reported" && (field.Value == nil || *field.Value != 0) {
						t.Fatalf("explicit zero lost: %+v", field)
					}
					if tc.state == "missing" && field.Value != nil {
						t.Fatalf("missing converted to zero: %+v", field)
					}
					return
				}
			}
			t.Fatal("missing attempt evidence")
		})
	}
}

// Existing parser tests compare counters; provenance is checked through HTTP read-back.
func usageCountsOnly(usage Usage) Usage { usage.Evidence = nil; return usage }

func TestUsageEvidenceHTTPStreamCompleteness(t *testing.T) {
	for _, complete := range []bool{true, false} {
		name := "truncated"
		if complete {
			name = "complete"
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000,\"prompt_tokens_details\":{\"cached_tokens\":600},\"completion_tokens\":0}}\n\n")
				for range 2 {
					_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"completion_tokens\":200}}\n\n")
				}
				if complete {
					_, _ = io.WriteString(w, "data: [DONE]\n\n")
				}
			}))
			defer upstream.Close()
			handler, store, key := newAnthropicGateway(t, upstream.URL, ProviderOpenAICompatible)
			defer func() { _ = store.Close() }()
			response := doAnthropicRequest(t, handler, "/v1/chat/completions", map[string]any{"model": "claude-tokenhub-test", "stream": true, "messages": []map[string]string{{"role": "user", "content": "hello"}}}, "Bearer "+key, "")
			result := doJSON(t, handler, "GET", "/api/admin/billing/evidence/"+response.Header().Get("x-request-id"), nil, billingAdminToken(t, store))
			var body struct {
				Data []struct {
					Kind string `json:"kind"`
					Data struct {
						Attempts []struct {
							Pricing struct {
								Units    metering.Units `json:"units"`
								Evidence struct {
									Complete *bool `json:"stream_complete"`
								} `json:"usage_evidence"`
							} `json:"pricing"`
						} `json:"attempts"`
					} `json:"data"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(result.Body), &body); err != nil {
				t.Fatal(err)
			}
			for _, row := range body.Data {
				if row.Kind == "shadow_settlement" && len(row.Data.Attempts) == 1 {
					pricing := row.Data.Attempts[0].Pricing
					if pricing.Units.Input != 400 || pricing.Units.CacheRead != 600 || pricing.Units.Output != 200 {
						t.Fatalf("cumulative usage lost or duplicated: %+v", pricing)
					}
					if pricing.Evidence.Complete == nil || *pricing.Evidence.Complete != complete {
						t.Fatalf("incorrect stream completion evidence: %+v", pricing)
					}
					return
				}
			}
			t.Fatalf("missing stream evidence: %s", result.Body)
		})
	}
}

func TestUsageEvidenceHTTPSupplierIdentifiersRemainDistinct(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("x-request-id", "invocation-123")
		w.Header().Set("cf-ray", "edge-123")
		_, _ = io.WriteString(w, `{"id":"response-123","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
	}))
	defer upstream.Close()
	handler, store, key := newAnthropicGateway(t, upstream.URL, ProviderOpenAICompatible)
	defer func() { _ = store.Close() }()
	response := doAnthropicRequest(t, handler, "/v1/chat/completions", map[string]any{"model": "claude-tokenhub-test", "messages": []map[string]string{{"role": "user", "content": "hello"}}}, "Bearer "+key, "")
	result := doJSON(t, handler, "GET", "/api/admin/billing/evidence/"+response.Header().Get("x-request-id"), nil, billingAdminToken(t, store))
	var body struct {
		Data []struct {
			Kind string `json:"kind"`
			Data struct {
				Attempts []struct {
					Pricing struct {
						Evidence struct {
							Invocation string `json:"invocation_id"`
							Response   string `json:"response_id"`
							Trace      string `json:"trace_id"`
						} `json:"usage_evidence"`
					} `json:"pricing"`
				} `json:"attempts"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(result.Body), &body); err != nil {
		t.Fatal(err)
	}
	for _, row := range body.Data {
		if row.Kind == "shadow_settlement" && len(row.Data.Attempts) == 1 {
			e := row.Data.Attempts[0].Pricing.Evidence
			if e.Invocation != "invocation-123" || e.Response != "response-123" || e.Trace != "edge-123" {
				t.Fatalf("identifier provenance lost: %+v", e)
			}
			return
		}
	}
	t.Fatal("missing attempt evidence")
}

type evidenceRetryAdapter struct{ MockAdapter }

func (evidenceRetryAdapter) Chat(context.Context, Provider, string, ChatCompletionRequest) (any, Usage, error) {
	return nil, Usage{PromptTokens: 100, CachedInputTokens: 30, CacheWriteInputTokens: 20, CacheWrite5mInputTokens: 10, CacheWrite1hInputTokens: 5, CompletionTokens: 50, TotalTokens: 150}, NewHTTPError(503, "provider_error", "Fixture failure with observed usage")
}
func TestUsageEvidenceHTTPKeepsRetryDurationComponents(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()
	project := store.CreateProject(Project{Name: "Retry evidence", Status: StatusActive})
	_, key, err := store.CreateAPIKey(project.ID, APIKey{Name: "retry", Status: StatusActive, Allowed: []string{"retry-model"}}, "thk_retry_evidence_fixture")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "retry-model", Modality: "chat", Status: StatusActive})
	for i, kind := range []string{"retry_evidence_fixture", ProviderMock} {
		id := fmt.Sprintf("retry-provider-%d", i)
		store.AddProvider(Provider{ID: id, Name: id, Type: kind, Status: StatusActive, Healthy: true})
		store.AddRoute(ModelRoute{ID: id, ModelName: "retry-model", ProviderID: id, ProviderModel: "upstream", Status: StatusActive, Priority: i + 1, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	}
	server := New(store)
	registerTestAdapter(server, "retry_evidence_fixture", evidenceRetryAdapter{})
	app := server.Handler()
	response := doAnthropicRequest(t, app, "/v1/chat/completions", map[string]any{"model": "retry-model", "messages": []map[string]string{{"role": "user", "content": "hello"}}}, "Bearer "+key, "")
	if response.Code != 200 {
		t.Fatalf("gateway: %d %s", response.Code, response.Body.String())
	}
	result := doJSON(t, app, "GET", "/api/admin/billing/evidence/"+response.Header().Get("x-request-id"), nil, billingAdminToken(t, store))
	var body struct {
		Data []struct {
			Kind string `json:"kind"`
			Data struct {
				Attempts []struct {
					Status  int `json:"status"`
					Pricing struct {
						Units metering.Units `json:"units"`
					} `json:"pricing"`
				} `json:"attempts"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(result.Body), &body); err != nil {
		t.Fatal(err)
	}
	for _, row := range body.Data {
		if row.Kind == "shadow_settlement" {
			if len(row.Data.Attempts) != 2 {
				t.Fatalf("retry evidence count=%d", len(row.Data.Attempts))
			}
			first := row.Data.Attempts[0]
			if first.Status != 503 || first.Pricing.Units != (metering.Units{Input: 50, CacheRead: 30, CacheWrite: 5, CacheWrite5m: 10, CacheWrite1h: 5, Output: 50}) {
				t.Fatalf("retry components lost: %+v", first)
			}
			return
		}
	}
	t.Fatal("missing settlement")
}

func TestAnthropicReplayRequiresCachePresenceEvenWithEqualRates(t *testing.T) {
	rates := metering.Rates{Input: "1", CacheRead: "1", CacheWrite: "1", CacheWrite5m: "1", CacheWrite1h: "1", Output: "1"}
	for _, tc := range []struct {
		name  string
		cache bool
	}{{"missing cache counters", false}, {"explicit zero cache counters", true}} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"input_tokens": int64(100), "output_tokens": int64(10)}
			if tc.cache {
				raw["cache_read_input_tokens"] = int64(0)
				raw["cache_creation_input_tokens"] = int64(0)
			}
			usage := anthropicUsageFromRawMap(raw)
			if got := usage.Evidence.has("input_total"); got != tc.cache {
				t.Fatalf("complete input evidence = %v, expected %v", got, tc.cache)
			}
			if got := replayEvidenceKnown(usage.Evidence, rates, metering.Units{Input: 100, Output: 10}); got != tc.cache {
				t.Fatalf("replay eligibility = %v, expected %v", got, tc.cache)
			}
		})
	}
}
