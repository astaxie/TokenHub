package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRerankHookErrorsDoNotRetainPrivateContent(t *testing.T) {
	const private = "private-rerank-hook-sentinel"
	for _, stage := range []pluginmeta.GatewayHookStage{
		pluginmeta.StageDecodeNormalize, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre,
		pluginmeta.StageContextOptimize, pluginmeta.StageRequestTransform, pluginmeta.StageProviderCall,
		pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost, pluginmeta.StageUsageAttribution,
	} {
		t.Run(string(stage), func(t *testing.T) {
			app, store, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": 0.9}}})
			})
			app.config.TracingCapturePayloads = true
			emitter := &recordingTraceEmitter{}
			app.traceEmitter = emitter
			dataClass := pluginmeta.DataRequestBody
			if stage == pluginmeta.StageRequestTransform || stage == pluginmeta.StageProviderCall {
				dataClass = pluginmeta.DataProviderRequest
			}
			if stage == pluginmeta.StageResponsePost || stage == pluginmeta.StageGuardrailPost || stage == pluginmeta.StageUsageAttribution {
				dataClass = pluginmeta.DataProviderResponse
			}
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-private", HookID: "echo", Stage: stage, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{dataClass}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := app.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				body := string(input.Data[dataClass])
				if !strings.Contains(body, private) {
					t.Fatal("test hook did not receive private content")
				}
				failure := NewHTTPError(http.StatusUnprocessableEntity, private, body)
				failure.Headers = map[string]string{"Retry-After": private, "X-RateLimit-Remaining-Tokens": private}
				return pluginmeta.GatewayHookResult{}, failure
			})); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "rank-complete", "query": private, "documents": []string{private}, "return_documents": true}, key)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("hook status changed: %d %s", response.Code, response.Body)
			}
			var attempts []RouteAttemptLog
			var requests []RequestLog
			var payloads []RequestPayloadLog
			for _, destination := range []any{&attempts, &requests, &payloads} {
				if err := store.db.Find(destination).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, value := range []any{response, attempts, requests, payloads, emitter.take()} {
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(encoded), private) {
					t.Fatalf("private content escaped through %T", value)
				}
			}
		})
	}
}

func TestRerankHookPatchesEnforceConfiguredRequestLimit(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageDecodeNormalize, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre, pluginmeta.StageContextOptimize} {
		t.Run(string(stage), func(t *testing.T) {
			calls := 0
			app, _, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
			app.config.MaxJSONRequestBytes = 1024
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-size", HookID: "expand", Stage: stage, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := app.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				var request RerankRequest
				if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &request); err != nil {
					t.Fatal(err)
				}
				request.Query = strings.Repeat("q", 1024)
				patch, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				return pluginmeta.GatewayHookResult{Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataRequestBody: {Value: patch}}}, nil
			})); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "rank-complete", "query": "q", "documents": []string{"d"}}, key)
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "gateway_hook_patch_invalid") || calls != 0 {
				t.Fatalf("oversized patch reached upstream: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestRerankRequestPatchDefaultSizeBoundary(t *testing.T) {
	original := RerankRequest{Model: "rank", Query: "q", Documents: []string{"d"}}
	for _, limit := range []int64{0, defaultMaxJSONRequestBytes, defaultMaxJSONRequestBytes + 1024} {
		for _, extra := range []int{0, 1} {
			capacity := limit
			if capacity == 0 {
				capacity = defaultMaxJSONRequestBytes
			}
			patch, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			patch = append(patch, []byte(strings.Repeat(" ", int(capacity)-len(patch)+extra))...)
			request := original
			err = applyRerankPatchLimit(&request, patch, limit)
			if (err != nil) != (extra != 0) {
				t.Fatalf("limit=%d bytes=%d error=%v", limit, len(patch), err)
			}
		}
	}
}

func TestRerankSanitizerPreservesNativeParameterErrors(t *testing.T) {
	for _, request := range []RerankRequest{
		{Model: "rank", Query: "q", Documents: []string{"d"}, Instruction: "unsupported"},
		{Model: "rank", Query: "q", Documents: []string{"d"}, Truncation: new(bool)},
	} {
		_, _, err := (OpenAICompatibleAdapter{}).Rerank(context.Background(), Provider{Options: map[string]string{"rerank_protocol": "cohere"}}, "up", request)
		sanitized := AsHTTPError(sanitizeRerankProviderError(err))
		if sanitized.Status != http.StatusBadRequest || sanitized.Code != "rerank_parameter_unsupported" {
			t.Fatalf("native parameter classification changed: %+v", sanitized)
		}
	}
}
