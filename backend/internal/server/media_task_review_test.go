package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func reviewSubmitMediaTask(t *testing.T, server *Server, key string) string {
	t.Helper()
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "public-media", "prompt": "A synthetic landscape"}, key)
	if response.Code != http.StatusOK {
		t.Fatalf("submit task: status=%d body=%s", response.Code, response.Body)
	}
	var payload struct {
		Data []struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil || len(payload.Data) != 1 || payload.Data[0].TaskID == "" {
		t.Fatalf("missing submitted task handle: %s", response.Body)
	}
	return payload.Data[0].TaskID
}

func reviewMediaTaskResponseHook(t *testing.T, server *Server, stage pluginmeta.GatewayHookStage, protocol string, rewrite func(map[string]any)) {
	t.Helper()
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "test.media-task-review", HookID: string(stage), Stage: stage, Priority: 1000,
		Scope:         pluginmeta.GatewayHookScope{RouteProtocols: []string{protocol}},
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var payload map[string]any
		if err := decodeResponsesJSON(input.Data[pluginmeta.DataProviderResponse], &payload); err != nil {
			t.Fatalf("decode hook response: %v", err)
		}
		rewrite(payload)
		return rawProviderResponsePatch(t, payload), nil
	})); err != nil {
		t.Fatal(err)
	}
}

func TestMediaTaskReviewPostHooksCannotChangeTaskIdentity(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		for _, operation := range []string{"submission", "poll"} {
			for _, mutation := range []string{"replace", "remove"} {
				t.Run(string(stage)+"/"+operation+"/"+mutation, func(t *testing.T) {
					calls := 0
					server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
						calls++
						w.Header().Set("Content-Type", "application/json")
						if r.Method == http.MethodPost {
							_, _ = io.WriteString(w, `{"code":200,"data":[{"status":"submitted","task_id":"vendor_review_task"}]}`)
							return
						}
						_, _ = io.WriteString(w, `{"code":200,"data":{"id":"vendor_review_task","status":"completed","result":{"videos":[{"url":["https://example.com/fixture.mp4"]}]}}}`)
					}, "video")
					protocol := "videos/generations"
					method, path, wantCalls := http.MethodPost, "/v1/videos/generations", 1
					var body any = map[string]any{"model": "public-media", "prompt": "A synthetic landscape"}
					if operation == "poll" {
						id := reviewSubmitMediaTask(t, server, key)
						protocol, method, path, body, wantCalls = "tasks", http.MethodGet, "/v1/tasks/"+id, nil, 2
					}
					reviewMediaTaskResponseHook(t, server, stage, protocol, func(payload map[string]any) {
						var item map[string]any
						field := "id"
						if operation == "submission" {
							item = payload["data"].([]any)[0].(map[string]any)
							field = "task_id"
						} else {
							item = payload["data"].(map[string]any)
						}
						if mutation == "remove" {
							delete(item, field)
						} else {
							item[field] = "task_replaced_by_response_hook"
						}
					})
					response := doJSON(t, server.Handler(), method, path, body, key)
					if response.Code != http.StatusBadGateway || calls != wantCalls {
						t.Fatalf("task identity modification was accepted or retried: status=%d calls=%d body=%s", response.Code, calls, response.Body)
					}
				})
			}
		}
	}
}

func TestMediaTaskReviewFailedTaskRedactsProviderCredentials(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{"", pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		name := string(stage)
		if name == "" {
			name = "upstream"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					_, _ = io.WriteString(w, `{"code":200,"data":[{"status":"submitted","task_id":"vendor_review_failure"}]}`)
					return
				}
				_, _ = io.WriteString(w, `{"code":200,"data":{"id":"vendor_review_failure","status":"failed","error":{"message":"Provider rejected upstream-fixture-secret with review-header-secret","code":"task_failed"}}}`)
			}, "video")
			provider, ok := store.GetProvider("media-provider")
			if !ok {
				t.Fatal("missing fixture provider")
			}
			provider.Headers = map[string]string{"X-Provider-Credential": "review-header-secret"}
			provider.SensitiveHeaders = []string{"X-Provider-Credential"}
			if _, err := store.UpdateProvider(provider.ID, provider); err != nil {
				t.Fatal(err)
			}
			id := reviewSubmitMediaTask(t, server, key)
			if stage != "" {
				reviewMediaTaskResponseHook(t, server, stage, "tasks", func(payload map[string]any) {
					payload["data"].(map[string]any)["error"] = map[string]any{"message": "Provider rejected upstream-fixture-secret with review-header-secret", "code": "task_failed"}
				})
			}
			response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id, nil, key)
			if response.Code != http.StatusOK || calls != 2 {
				t.Fatalf("failed task should remain readable without resubmission: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			var payload struct {
				Data struct {
					ID, Status string
					Error      struct{ Message, Code string }
				}
			}
			if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Data.ID != id || payload.Data.Status != "failed" || payload.Data.Error.Code != "task_failed" || !strings.Contains(payload.Data.Error.Message, "Provider rejected") {
				t.Fatalf("task failure context was lost: %s", response.Body)
			}
			for _, secret := range []string{"upstream-fixture-secret", "review-header-secret"} {
				if strings.Contains(response.Body, secret) {
					t.Errorf("task failure exposed provider credentials: %s", response.Body)
				}
			}
		})
	}
}

func TestMediaTaskReviewPollingRequiresNoTokenReservationOrCharge(t *testing.T) {
	calls := 0
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"code":200,"data":[{"status":"submitted","task_id":"vendor_review_quota"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"code":200,"data":{"id":"vendor_review_quota","status":"completed","cost":0.25,"credits_cost":2.5},"usage":{"input_tokens":100,"output_tokens":200,"total_tokens":300}}`)
	}, "video")
	id := reviewSubmitMediaTask(t, server, key)
	apiKey := store.ListAPIKeys()[0]
	apiKey.TokenLimitTPM = int64Pointer(1)
	if _, err := store.UpdateAPIKey(apiKey.ID, apiKey); err != nil {
		t.Fatal(err)
	}
	hookCalls := 0
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "test.media-task-review", HookID: "poll-usage", Stage: pluginmeta.StageUsageAttribution, Priority: 1000,
		Scope:         pluginmeta.GatewayHookScope{RouteProtocols: []string{"tasks"}},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		hookCalls++
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataUsage: {Value: json.RawMessage(`{"completion_tokens":50,"total_tokens":50}`)},
		}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id, nil, key)
		if response.Code != http.StatusOK {
			t.Fatalf("metadata poll required generation token capacity: status=%d body=%s", response.Code, response.Body)
		}
	}
	if calls != 3 || hookCalls != 2 {
		t.Fatalf("polling skipped upstream or attribution hook: calls=%d hooks=%d", calls, hookCalls)
	}
	records := store.ListUsageRecords()
	if logs := store.ListRequestLogs(); len(logs) != 3 {
		t.Fatalf("requests were not accounted: logs=%d", len(logs))
	}
	for _, record := range records {
		if record.TotalTokens != 0 || record.CostUSD != 0 || record.ProviderCostUSD != 0 {
			t.Errorf("task poll charged repeated provider usage: %+v", record)
		}
	}
}

func TestMediaTaskReviewBoundsSubmissionBindings(t *testing.T) {
	for _, count := range []int{128, 129} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				items := make([]map[string]any, count)
				for i := range items {
					items[i] = map[string]any{"status": "submitted", "task_id": "vendor_review_batch_" + strconv.Itoa(i)}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": items})
			}, "video")
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "public-media", "prompt": "A synthetic landscape"}, key)
			wantStatus, wantBindings := http.StatusOK, int64(count)
			if count > 128 {
				wantStatus, wantBindings = http.StatusBadGateway, 0
			}
			if response.Code != wantStatus || calls != 1 {
				t.Fatalf("task batch limit failed or resubmitted upstream: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			var bindings int64
			if err := store.db.Table("media_task_bindings").Count(&bindings).Error; err != nil {
				t.Fatal(err)
			}
			if bindings != wantBindings {
				t.Errorf("task batch was partially or unexpectedly retained: got=%d want=%d", bindings, wantBindings)
			}
		})
	}
}

func TestMediaTaskReviewRejectsUnverifiablePollResults(t *testing.T) {
	for _, tc := range []struct{ name, result string }{
		{"missing nested identifier", `{"code":200,"data":{"status":"completed"}}`},
		{"missing root identifier", `{"status":"completed"}`},
		{"different task", `{"code":200,"data":{"id":"another_vendor_task","status":"completed"}}`},
		{"conflicting identifiers", `{"code":200,"data":{"id":"vendor_review_identity","task_id":"another_vendor_task","status":"completed"}}`},
		{"array task object", `{"code":200,"data":[{"id":"vendor_review_identity","status":"completed"}]}`},
		{"null task object", `{"code":200,"data":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					_, _ = io.WriteString(w, `{"code":200,"data":[{"status":"submitted","task_id":"vendor_review_identity"}]}`)
					return
				}
				_, _ = io.WriteString(w, tc.result)
			}, "video")
			id := reviewSubmitMediaTask(t, server, key)
			response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id, nil, key)
			if response.Code != http.StatusBadGateway || calls != 2 {
				t.Fatalf("unverifiable task result was accepted or retried: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}
