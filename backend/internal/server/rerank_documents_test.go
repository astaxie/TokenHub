package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRerankDocumentIDsRoundTripAndBindCache(t *testing.T) {
	data := []byte(`{"model":"rank","query":"q","documents":["same",{"id":"second","text":"same"}]}`)
	var request RerankRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request.Documents, []string{"same", "same"}) || !reflect.DeepEqual(request.DocumentIDs, []string{"", "second"}) {
		t.Fatalf("document identity was lost: %+v", request)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip RerankRequest
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, request) {
		t.Fatalf("document identity did not round trip: %s, %v", encoded, err)
	}
	key := rerankCacheKey(CallContext{}, RouteSelection{}, request)
	roundTrip.DocumentIDs[1] = "other"
	if key == rerankCacheKey(CallContext{}, RouteSelection{}, roundTrip) {
		t.Fatal("document identity changes reused a cached ranking")
	}
	changed, _ := json.Marshal(roundTrip)
	if applyRerankPatch(&request, changed) == nil {
		t.Fatal("request hooks changed document identity")
	}
	redacted := []byte(`{"model":"rank","query":"[redacted]","documents":["[redacted]",{"id":"second","text":"[redacted]"}]}`)
	if err := applyRerankPatch(&request, redacted); err != nil {
		t.Fatalf("privacy edit with stable IDs was rejected: %v", err)
	}
}

func TestRerankDocumentIDsPreserveResponseSafetyEdits(t *testing.T) {
	request := RerankRequest{Model: "rank", Query: "q", Documents: []string{"private"}, DocumentIDs: []string{"first"}, ReturnDocuments: true}
	for _, document := range []any{nil, map[string]any{"text": "[REDACTED]"}} {
		item := map[string]any{"index": 0, "id": "first", "relevance_score": 0.9}
		if document != nil {
			item["document"] = document
		}
		response := map[string]any{"model": "rank", "results": []any{item}}
		validated, err := validateRerankResult(response, request)
		if err != nil || !reflect.DeepEqual(validated, response) {
			t.Fatalf("safety output changed: response=%v error=%v", validated, err)
		}
		item["id"] = "other"
		if _, err := validateRerankResult(response, request); err == nil {
			t.Fatal("response hooks changed document identity")
		}
	}
}

func newRerankCompletionFixture(t *testing.T, handler http.HandlerFunc) (*Server, *MemoryStore, string) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	store.AddProvider(Provider{ID: "rank-complete", Type: ProviderOpenAICompatible, BaseURL: upstream.URL, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "jina"}})
	store.AddModel(Model{Name: "rank-complete", Modality: "rerank", Status: StatusActive, InputPriceUSDPer1M: 1, Metadata: map[string]string{"retrieval_pricing_confirmed": "true"}})
	store.AddProviderModel(ProviderModel{ProviderID: "rank-complete", UpstreamModel: "upstream", Modality: "rerank", InputPriceUSDPer1M: 1})
	store.AddRoute(ModelRoute{ModelName: "rank-complete", ProviderID: "rank-complete", ProviderModel: "upstream", Status: StatusActive, Weight: 100})
	_, key, err := store.CreateAPIKey("prj_demo", APIKey{Name: "rerank completion", Allowed: []string{"rank-complete"}, Status: StatusActive}, "test-rerank-completion")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	return app, store, key
}

func TestRerankGatewayPreservesIDsAndCountsWithoutProviderUsage(t *testing.T) {
	app, store, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Documents []string `json:"documents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || !reflect.DeepEqual(payload.Documents, []string{"same", "same", "third"}) {
			t.Errorf("provider did not receive text-only documents: %+v, %v", payload, err)
		}
		writeJSON(w, 200, map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": 0.1}, map[string]any{"index": 1, "relevance_score": 0.9}}})
	})
	payload := map[string]any{"model": "rank-complete", "query": "q", "documents": []any{"same", map[string]any{"id": "second", "text": "same"}, map[string]any{"id": "third", "text": "third"}}, "top_n": 2}
	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", payload, key)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 2 || body.Results[0]["index"] != float64(1) || body.Results[0]["id"] != "second" || body.Results[1]["index"] != float64(0) || body.Results[1]["id"] != nil || body.Results[0]["document"] != nil {
		t.Fatalf("original indices or IDs were lost: %s", response.Body)
	}
	var settlement meteringEntry
	if err := store.db.Where("kind = ? AND scope IN (?)", "shadow_settlement", store.db.Model(&RequestLog{}).Select("request_id").Where("model_name = ?", "rank-complete")).First(&settlement).Error; err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Tenant   meteringShadowCharge    `json:"tenant"`
		Attempts []meteringAttemptCharge `json:"attempts"`
	}
	if err := json.Unmarshal([]byte(settlement.Payload), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Tenant.DocumentCount != 3 || evidence.Tenant.Evidence == nil || evidence.Tenant.Evidence.Quantity != nil || evidence.Tenant.Charge != nil || len(evidence.Attempts) != 1 || evidence.Attempts[0].Charge.DocumentCount != 3 {
		t.Fatalf("missing usage invented a charge or lost count evidence: %s", settlement.Payload)
	}
}

func TestRerankGatewayRejectsInvalidDocumentShapesAndSizes(t *testing.T) {
	calls := 0
	app, _, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
	for _, documents := range []any{
		[]any{nil}, []any{17}, []any{map[string]any{"text": "text"}}, []any{map[string]any{"id": " ", "text": "text"}},
		[]any{map[string]any{"id": "id", "text": "text", "extra": true}}, []any{map[string]any{"id": strings.Repeat("i", maxRerankDocumentIDBytes+1), "text": "text"}},
		[]string{strings.Repeat("x", maxRerankDocumentBytes+1)}, []any{map[string]any{"id": "id", "text": strings.Repeat("界", maxRerankDocumentBytes/3+1)}},
		make([]string, maxRerankDocuments+1),
	} {
		response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "rank-complete", "query": "q", "documents": documents}, key)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid documents accepted: status=%d", response.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid documents reached upstream %d times", calls)
	}
}

func TestRerankGatewayUpstreamErrorsDoNotEchoInput(t *testing.T) {
	const query = "private-query-sentinel"
	const document = "private-document-sentinel"
	app, store, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 422, map[string]any{"error": map[string]any{"message": query + " " + document}})
	})
	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "rank-complete", "query": query, "documents": []any{map[string]any{"id": "id", "text": document}}}, key)
	if response.Code != http.StatusUnprocessableEntity || strings.Contains(response.Body, query) || strings.Contains(response.Body, document) {
		t.Fatalf("upstream failure changed status or exposed input: %d %s", response.Code, response.Body)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || strings.Contains(attempts[0].ErrorMessage, query) || strings.Contains(attempts[0].ErrorMessage, document) {
		t.Fatalf("attempt error retained private input: %+v", attempts)
	}
}

func TestRerankProviderResponseHasStrictSizeLimit(t *testing.T) {
	const response = `{"results":[{"index":0,"relevance_score":0.9}]}`
	for _, size := range []int{maxRerankResponseBytes, maxRerankResponseBytes + 1} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, response+strings.Repeat(" ", size-len(response)))
		}))
		_, _, err := (OpenAICompatibleAdapter{Client: upstream.Client()}).Rerank(context.Background(), Provider{BaseURL: upstream.URL, Options: map[string]string{"rerank_protocol": "jina"}}, "upstream", RerankRequest{Model: "rank", Query: "q", Documents: []string{"d"}})
		upstream.Close()
		if (err != nil) != (size > maxRerankResponseBytes) {
			t.Fatalf("size=%d error=%v", size, err)
		}
	}
}

func TestRerankHookResponsesHaveStrictSizeLimit(t *testing.T) {
	response := map[string]any{"model": "rank", "results": []any{map[string]any{"index": 0, "relevance_score": 0.9}}, "metadata": strings.Repeat("x", maxRerankResponseBytes)}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := decodeGatewayHookResponsePayload(encoded, &decoded, providerRouteProtocolRerank); err == nil {
		t.Fatal("provider plugin bypassed the response size limit")
	}
	if err := decodeGatewayPostHookResponsePayload(encoded, &decoded, providerRouteProtocolRerank); err == nil {
		t.Fatal("post hook bypassed the response size limit")
	}
	if _, err := validateRerankResult(response, RerankRequest{Model: "rank", Query: "q", Documents: []string{"d"}}); err == nil {
		t.Fatal("final response bypassed the size limit")
	}
	app := newEmbeddingsCacheHookTestServer(t)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rank-bound", HookID: "hit", Stage: pluginmeta.StageCacheLookup, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataCacheKey}, FailurePolicy: pluginmeta.FailurePolicyFailOpen}
	if err := app.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataProviderResponse: {Value: encoded}, pluginmeta.DataCacheKey: {Value: input.Data[pluginmeta.DataCacheKey]},
		}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	call := gatewayPluginTestCall()
	call.RerankCacheKey = "rerank:v1:bounded"
	if _, _, _, err := app.runGatewayCacheLookupHooks(context.Background(), call, RerankRequest{}); err == nil {
		t.Fatal("cache plugin bypassed the response size limit")
	}
}

func TestRerankTransportDeadlineReturnsGatewayTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	_, _, err := (OpenAICompatibleAdapter{Client: client}).Rerank(context.Background(), Provider{BaseURL: "https://example.com", Options: map[string]string{"rerank_protocol": "jina"}}, "upstream", RerankRequest{Model: "rank", Query: "q", Documents: []string{"d"}})
	if httpErr := AsHTTPError(err); httpErr.Status != http.StatusGatewayTimeout || providerErrorDisposition(err) != ProviderErrorTransientSame {
		t.Fatalf("deadline classification lost: %v", err)
	}
}

func TestRerankPluginDocumentCountCannotOverrideInput(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageProviderCall, pluginmeta.StageCacheLookup} {
		t.Run(string(stage), func(t *testing.T) {
			app, store, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("plugin response reached upstream") })
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-count", HookID: "response", Stage: stage, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage, pluginmeta.DataCacheKey}, FailurePolicy: pluginmeta.FailurePolicyFailOpen}
			if stage == pluginmeta.StageProviderCall {
				hook.Reads = nil
				hook.Writes = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}
				hook.FailurePolicy = pluginmeta.FailurePolicyFailClosed
			}
			if err := app.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				result := rawProviderCallResult(t, map[string]any{"model": "rank-complete", "results": []any{map[string]any{"index": 0, "id": "first", "relevance_score": 0.9}}}, Usage{DocumentCount: 999})
				if stage == pluginmeta.StageCacheLookup {
					result.Writes[pluginmeta.DataCacheKey] = pluginmeta.RawPatch{Value: input.Data[pluginmeta.DataCacheKey]}
				}
				return result, nil
			})); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "rank-complete", "query": "q", "documents": []any{map[string]any{"id": "first", "text": "d"}}}, key)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			var entry meteringEntry
			if err := store.db.Where("kind = ? AND scope IN (?)", "shadow_settlement", store.db.Model(&RequestLog{}).Select("request_id").Where("model_name = ?", "rank-complete")).First(&entry).Error; err != nil {
				t.Fatal(err)
			}
			var evidence struct {
				Tenant meteringShadowCharge `json:"tenant"`
			}
			if err := json.Unmarshal([]byte(entry.Payload), &evidence); err != nil || evidence.Tenant.DocumentCount != 1 {
				t.Fatalf("plugin controlled document count: %s, %v", entry.Payload, err)
			}
		})
	}
}
