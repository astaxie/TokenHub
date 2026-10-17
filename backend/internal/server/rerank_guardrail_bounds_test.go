package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRerankNativeMaskEnforcesRequestBoundsBeforeDispatch(t *testing.T) {
	for _, mode := range []string{"native", "provider_hook", "cache"} {
		for _, bound := range []string{"document", "total", "document_limit", "total_limit", "valid"} {
			t.Run(mode+"/"+bound, func(t *testing.T) {
				calls := 0
				app, store, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					writeJSON(w, http.StatusOK, map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": 0.9}}})
				})
				if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
					Name:           "Mask marker",
					DetectionItems: []guardrails.DetectionItem{{Name: "Marker", DetectorType: guardrails.DetectorSensitiveData, Action: guardrails.ActionMask, Config: map[string]any{"data_types": []string{"email"}}}},
					Bindings:       []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
				}); err != nil {
					t.Fatal(err)
				}
				request := RerankRequest{Model: "rank-complete", Query: "q", Documents: []string{"a@b.c"}, DocumentIDs: []string{"first"}, ReturnDocuments: true}
				if strings.HasPrefix(bound, "document") {
					suffixBytes := len("a@b.c")
					if bound == "document_limit" {
						suffixBytes = len("[REDACTED]")
					}
					request.Documents[0] = strings.Repeat("x", maxRerankDocumentBytes-suffixBytes-1) + " a@b.c"
				}
				if strings.HasPrefix(bound, "total") {
					data, err := json.Marshal(request)
					if err != nil {
						t.Fatal(err)
					}
					app.config.MaxJSONRequestBytes = int64(len(data))
					if bound == "total_limit" {
						app.config.MaxJSONRequestBytes += int64(len("[REDACTED]") - len("a@b.c"))
					}
				}
				if mode != "native" {
					stage, dataClass := pluginmeta.StageProviderCall, pluginmeta.DataProviderRequest
					if mode == "cache" {
						stage, dataClass = pluginmeta.StageCacheLookup, pluginmeta.DataRequestBody
					}
					hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-mask-bounds", HookID: mode, Stage: stage, Priority: 2000,
						Reads: []pluginmeta.GatewayDataClass{dataClass}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
					if mode == "cache" {
						hook.FailurePolicy = pluginmeta.FailurePolicyFailOpen
						hook.Reads = append(hook.Reads, pluginmeta.DataCacheKey)
						hook.Writes = append(hook.Writes, pluginmeta.DataCacheKey)
					}
					if err := app.gatewayChain.RegisterHook(hook); err != nil {
						t.Fatal(err)
					}
					if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						calls++
						response, _ := json.Marshal(map[string]any{"model": "rank-complete", "results": []any{map[string]any{"index": 0, "id": "first", "relevance_score": 0.9, "document": map[string]any{"text": "[REDACTED]"}}}})
						result := pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: response}}}
						if mode == "cache" {
							result.Writes[pluginmeta.DataCacheKey] = pluginmeta.RawPatch{Value: input.Data[pluginmeta.DataCacheKey]}
						}
						return result, nil
					})); err != nil {
						t.Fatal(err)
					}
				}
				response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", request, key)
				if bound == "valid" || strings.HasSuffix(bound, "_limit") {
					if response.Code != http.StatusOK || calls != 1 || !strings.Contains(response.Body, `"id":"first"`) || !strings.Contains(response.Body, "[REDACTED]") || strings.Contains(response.Body, "a@b.c") {
						t.Fatalf("valid masked request failed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
					}
				} else if response.Code != http.StatusRequestEntityTooLarge || calls != 0 || !strings.Contains(response.Body, `"code":"request_too_large"`) {
					t.Fatalf("oversized masked request was not rejected before dispatch: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
			})
		}
	}
}
