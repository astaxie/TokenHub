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

func TestRerankAuditAndTracingContainOnlyMetadata(t *testing.T) {
	const private = "private-rerank-sentinel"
	for _, mode := range []string{"native", "cache", "error", "guardrail"} {
		t.Run(mode, func(t *testing.T) {
			app, store, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "cache" {
					t.Error("cached request reached upstream")
				}
				if mode == "error" {
					writeJSON(w, 422, map[string]any{"error": map[string]any{"message": private}})
					return
				}
				writeJSON(w, 200, map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": 0.9}}, "meta": map[string]any{"provider_note": private}})
			})
			app.config.TracingCapturePayloads = true
			emitter := &recordingTraceEmitter{}
			app.traceEmitter = emitter
			if mode == "guardrail" {
				if _, err := store.CreateGuardrailPolicy(guardrails.Policy{Name: "Audit email", DetectionItems: []guardrails.DetectionItem{{Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: guardrails.ActionAudit, Config: map[string]any{"data_types": []string{"email"}}}}, Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "cache" {
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-audit", HookID: "cache", Stage: pluginmeta.StageCacheLookup, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage, pluginmeta.DataCacheKey}, FailurePolicy: pluginmeta.FailurePolicyFailOpen}
				if err := app.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					result := rawProviderCallResult(t, map[string]any{"model": "rank-complete", "results": []any{map[string]any{"index": 0, "id": private, "relevance_score": 0.9, "document": map[string]any{"text": private}}}}, Usage{})
					result.Writes[pluginmeta.DataCacheKey] = pluginmeta.RawPatch{Value: input.Data[pluginmeta.DataCacheKey]}
					return result, nil
				})); err != nil {
					t.Fatal(err)
				}
			}
			response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "rank-complete", "query": private + " fixture@example.com", "instruction": private, "documents": []any{map[string]any{"id": private, "text": private}}, "return_documents": true}, key)
			wantStatus := http.StatusOK
			if mode == "error" {
				wantStatus = http.StatusUnprocessableEntity
			}
			if response.Code != wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			var records []RequestPayloadLog
			if err := store.db.Where("request_id IN (?)", store.db.Model(&RequestLog{}).Select("request_id").Where("model_name = ?", "rank-complete")).Find(&records).Error; err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 {
				t.Fatalf("audit records=%d", len(records))
			}
			var request rerankAuditRequest
			if err := json.Unmarshal([]byte(records[0].RequestBody), &request); err != nil || request.DocumentCount != 1 {
				t.Fatalf("metadata count missing: %s, %v", records[0].RequestBody, err)
			}
			if mode == "guardrail" && (request.Guardrail == nil || len(request.Guardrail.Findings) == 0) {
				t.Fatal("guardrail evidence was lost")
			}
			assertPrivateAbsent := func(data string) {
				t.Helper()
				if strings.Contains(data, private) || strings.Contains(data, "fixture@example.com") {
					t.Fatal("rerank content leaked to audit or tracing")
				}
			}
			assertPrivateAbsent(records[0].RequestBody + records[0].ResponseBody)
			completions := emitter.take()
			if len(completions) != 1 || completions[0].RequestBody == "" {
				t.Fatalf("captured trace completions=%d", len(completions))
			}
			for _, completion := range completions {
				assertPrivateAbsent(completion.RequestBody + completion.ResponseBody + completion.ErrorMessage)
			}
		})
	}
}
