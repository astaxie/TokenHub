package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestJevBackgroundBindingWaitsForOutputApproval(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		for _, outcome := range []string{"approved", "denied", "cancelled"} {
			t.Run(fmt.Sprintf("%s_%s", stage, outcome), func(t *testing.T) {
				server, _, _, _ := jevFixture(t)
				hits := jevHTTPUpstreams(t, server, false)
				server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
					return semanticDecision{Choice: "choice_1", Confidence: .9}, nil
				})
				entered, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				defer unblock()
				var hookCalls atomic.Int32
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-jev-approval", HookID: "approval", Stage: stage, Priority: 1000, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(ctx context.Context, _ pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					if hookCalls.Add(1) == 1 {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
							return pluginmeta.GatewayHookResult{}, ctx.Err()
						}
						if outcome == "denied" {
							return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
						}
					}
					return pluginmeta.GatewayHookResult{}, nil
				})); err != nil {
					t.Fatal(err)
				}
				body := jevHTTPBody(true, false)
				body["background"] = true
				first := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
				if first.Code != 200 {
					t.Fatal(first.Body)
				}
				var envelope map[string]any
				if err := json.Unmarshal([]byte(first.Body), &envelope); err != nil {
					t.Fatal(err)
				}
				id := jevResponseID(envelope)
				if id == "" {
					t.Fatalf("missing background job ID: %s", first.Body)
				}
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("output hook not entered")
				}
				body = jevHTTPBody(true, false)
				body["previous_response_id"] = id
				pending := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
				if pending.Code != 409 || hits.Load() != 1 {
					t.Errorf("pending output became usable: %d %s hits=%d", pending.Code, pending.Body, hits.Load())
				}
				if outcome == "cancelled" {
					cancelled := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses/"+id+"/cancel", nil, "thk_semantic_test")
					if cancelled.Code != 200 {
						t.Fatalf("cancel background response: %d %s", cancelled.Code, cancelled.Body)
					}
				}
				unblock()
				status := "completed"
				if outcome == "denied" {
					status = "failed"
				}
				if outcome == "cancelled" {
					status = "cancelled"
				}
				waitForResponseJobStatus(t, server.Handler(), "thk_semantic_test", id, status)
				result := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
				wantStatus, wantHits := 200, int32(2)
				if outcome != "approved" {
					wantStatus, wantHits = 409, 1
				}
				if result.Code != wantStatus || hits.Load() != wantHits {
					t.Fatalf("approved/denied continuation: %d %s hits=%d", result.Code, result.Body, hits.Load())
				}
			})
		}
	}
}

func TestJevContinuationRevalidatesCandidateMetadata(t *testing.T) {
	for _, state := range []string{"disabled", "missing", "parameters", "context", "modalities"} {
		t.Run(state, func(t *testing.T) {
			server, _, _, _ := jevFixture(t)
			hits := jevHTTPUpstreams(t, server, false)
			var evaluations atomic.Int32
			server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
				evaluations.Add(1)
				return semanticDecision{Choice: "choice_1", Confidence: .9}, nil
			})
			first := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", jevHTTPBody(true, false), "thk_semantic_test")
			if first.Code != 200 {
				t.Fatal(first.Body)
			}
			models := server.store.(*GormStore).db.Model(&ProviderModel{}).Where("provider_id = ?", "provider_1")
			var err error
			switch state {
			case "disabled":
				err = models.Update("status", StatusDisabled).Error
			case "missing":
				err = models.Delete(&ProviderModel{}).Error
			case "parameters":
				err = models.Update("supported_parameters", `["max_tokens"]`).Error
			case "context":
				err = models.Update("context_window", 1).Error
			case "modalities":
				err = models.Update("output_modalities", `["image"]`).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			body := jevHTTPBody(true, false)
			body["previous_response_id"] = "resp_model_1"
			denied := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
			if denied.Code != 409 || hits.Load() != 1 || evaluations.Load() != 1 {
				t.Fatalf("unavailable candidate invoked: %d %s hits=%d evaluations=%d", denied.Code, denied.Body, hits.Load(), evaluations.Load())
			}
			fresh := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", jevHTTPBody(true, false), "thk_semantic_test")
			if fresh.Code != 200 || hits.Load() != 1 {
				t.Fatalf("fresh request failed to fall back: %d %s", fresh.Code, fresh.Body)
			}
		})
	}
}
