package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerMediaTaskRequestHook(t *testing.T, server *Server, stage pluginmeta.GatewayHookStage, protocol string, rewrite func(map[string]json.RawMessage)) {
	t.Helper()
	dataClass := pluginmeta.DataRequestBody
	if stage == pluginmeta.StageRequestTransform {
		dataClass = pluginmeta.DataProviderRequest
	}
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "test.media-task-reference", HookID: string(stage), Stage: stage, Priority: 1000,
		Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{protocol}},
		Reads: []pluginmeta.GatewayDataClass{dataClass}, Writes: []pluginmeta.GatewayDataClass{dataClass},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(input.Data[dataClass], &fields); err != nil {
			t.Fatalf("decode request hook: %v", err)
		}
		rewrite(fields)
		data, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{dataClass: {Value: data}}}, nil
	})); err != nil {
		t.Fatal(err)
	}
}

func TestMediaTaskReferenceHooksCannotChangeOwnership(t *testing.T) {
	stages := []pluginmeta.GatewayHookStage{pluginmeta.StageDecodeNormalize, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre, pluginmeta.StageContextOptimize, pluginmeta.StageRequestTransform}
	for _, stage := range stages {
		for _, mutation := range []string{"add", "replace", "remove"} {
			t.Run(string(stage)+"/"+mutation, func(t *testing.T) {
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"data":[{"task_id":"vendor-hook-reference"}]}`)
				}, "video")
				resource, err := store.AddProviderResource(ProviderResource{ID: "reference-policy-resource", ProviderID: "media-provider", Name: "Reference policy account", ResourceType: ProviderResourceAPIKey, APIKey: "reference-account-key", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100})
				if err != nil {
					t.Fatal(err)
				}
				id := submitMediaTask(t, server, key, "/v1/videos/generations")
				registerMediaTaskRequestHook(t, server, stage, "videos/generations", func(fields map[string]json.RawMessage) {
					for _, field := range mediaTaskReferenceFields {
						if mutation == "remove" {
							delete(fields, field)
						} else {
							setRawJSONField(fields, field, "vendor-forged-reference", true)
						}
					}
				})
				body := map[string]any{"model": "public-media", "prompt": "Continue the synthetic landscape"}
				if mutation != "add" {
					for _, field := range mediaTaskReferenceFields {
						body[field] = id
					}
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", body, key)
				if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "gateway_hook_patch_invalid") || calls != 1 {
					t.Fatalf("request hook changed task ownership: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
				after, ok := store.GetProviderResource(resource.ID)
				if !ok || after.FailureCount != 0 || !after.Healthy || after.CooldownUntil != nil {
					t.Fatalf("local reference refusal penalized provider health: %+v", after)
				}
			})
		}
	}
}

func TestMediaTaskReferenceHooksSeePublicAndProviderIdentifiers(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"task_id":"vendor-original-reference"}]}`)
	}, "video")
	id := submitMediaTask(t, server, key, "/v1/videos/generations")
	requestHooks, providerHooks := 0, 0
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageDecodeNormalize, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre, pluginmeta.StageContextOptimize, pluginmeta.StageRequestTransform} {
		registerMediaTaskRequestHook(t, server, stage, "videos/generations", func(fields map[string]json.RawMessage) {
			requestHooks++
			for _, field := range mediaTaskReferenceFields {
				if string(fields[field]) != `"`+id+`"` {
					t.Errorf("request hook received upstream reference in %s: %s", field, fields[field])
				}
			}
		})
	}
	registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "reference-provider", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"videos/generations"}}, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		providerHooks++
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(input.Envelope.RequestBody, &fields); err != nil {
			t.Fatal(err)
		}
		for _, field := range mediaTaskReferenceFields {
			if string(fields[field]) != `"vendor-original-reference"` {
				t.Errorf("provider hook received public reference in %s: %s", field, fields[field])
			}
		}
		return rawProviderCallResult(t, json.RawMessage(`{"data":[{"task_id":"vendor-continued-reference"}]}`), Usage{}), nil
	})
	body := map[string]any{"model": "public-media", "prompt": "Continue the synthetic landscape"}
	for _, field := range mediaTaskReferenceFields {
		body[field] = id
	}
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", body, key)
	if response.Code != http.StatusOK || requestHooks != 5 || providerHooks != 1 {
		t.Fatalf("task reference hook boundary: status=%d request=%d provider=%d body=%s", response.Code, requestHooks, providerHooks, response.Body)
	}
	var payload struct {
		Data []struct {
			TaskID string `json:"task_id"`
		}
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil || len(payload.Data) != 1 {
		t.Fatalf("decode continuation response: %v %s", err, response.Body)
	}
	_, upstream, found, err := store.LoadMediaTaskBinding(context.Background(), payload.Data[0].TaskID)
	if err != nil || !found || upstream != "vendor-continued-reference" || payload.Data[0].TaskID == id {
		t.Fatalf("continuation did not get a new bound handle: upstream=%q found=%v err=%v", upstream, found, err)
	}
}

func TestMediaTaskReferenceRejectsMalformedFields(t *testing.T) {
	calls := 0
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusInternalServerError) }, "video")
	for _, field := range mediaTaskReferenceFields {
		for _, value := range []any{nil, true, 123, []string{"task_unknown"}, map[string]string{"task_id": "task_unknown"}} {
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "public-media", field: value}, key)
			if response.Code != http.StatusNotFound {
				t.Errorf("invalid %s accepted: value=%v status=%d body=%s", field, value, response.Code, response.Body)
			}
		}
		for _, kind := range []string{"file", "repeated"} {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			_ = writer.WriteField("model", "public-media")
			if kind == "file" {
				part, err := writer.CreateFormFile(field, "task.txt")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(part, "vendor-reference")
			} else {
				_ = writer.WriteField(field, "task_unknown")
				_ = writer.WriteField(field, "vendor-reference")
			}
			_ = writer.Close()
			request := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", &body)
			request.Header.Set("Authorization", "Bearer "+key)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Errorf("multipart %s %s accepted: status=%d body=%s", field, kind, response.Code, response.Body)
			}
		}
	}
	if calls != 0 {
		t.Fatalf("malformed references reached the provider %d times", calls)
	}
}

func TestMediaTaskReferenceRejectsMixedAccountsAndLostSourceAccess(t *testing.T) {
	calls := 0
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"task_id":"vendor-reference-access"}]}`)
	}, "video")
	id := submitMediaTask(t, server, key, "/v1/videos/generations")
	binding, _, found, err := store.LoadMediaTaskBinding(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("load fixture task: %v", err)
	}
	for _, change := range []string{"provider", "resource", "identity"} {
		other := binding
		other.ID = NewID("task")
		switch change {
		case "provider":
			other.ProviderID = "other-provider"
		case "resource":
			other.ResourceID = "other-resource"
		case "identity":
			other.IdentityHash = "other-account-identity"
		}
		if err := store.SaveMediaTaskBindings(context.Background(), []mediaTaskBinding{other}, []string{"other-upstream"}); err != nil {
			t.Fatal(err)
		}
		response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "public-media", "draft_task_id": id, "source_task_id": other.ID}, key)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_media_task_reference") {
			t.Errorf("mixed %s reference accepted: status=%d body=%s", change, response.Code, response.Body)
		}
	}
	if err := store.db.Model(&mediaTaskBinding{}).Where("id = ?", id).Update("model", "inaccessible-source-model").Error; err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "public-media", "draft_task_id": id}, key)
	if response.Code != http.StatusNotFound || calls != 1 {
		t.Fatalf("source model access bypass: status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
}

func TestMediaTaskPollHookIdentityRefusalPreservesResourceHealth(t *testing.T) {
	for _, field := range []string{"task_id", "language"} {
		t.Run(field, func(t *testing.T) {
			calls, hookCalls := 0, 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"task_id":"vendor-poll-hook"}]}`)
			}, "video")
			resource, err := store.AddProviderResource(ProviderResource{ID: "poll-policy-resource", ProviderID: "media-provider", Name: "Poll policy account", ResourceType: ProviderResourceAPIKey, APIKey: "poll-account-key", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100})
			if err != nil {
				t.Fatal(err)
			}
			id := submitMediaTask(t, server, key, "/v1/videos/generations")
			registerMediaTaskRequestHook(t, server, pluginmeta.StageRequestTransform, "tasks", func(fields map[string]json.RawMessage) {
				hookCalls++
				setRawJSONField(fields, field, "replacement", true)
			})
			response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id+"?language=en", nil, key)
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "gateway_hook_patch_invalid") || calls != 1 || hookCalls != 1 {
				t.Fatalf("poll hook changed identity or retried: status=%d calls=%d hooks=%d body=%s", response.Code, calls, hookCalls, response.Body)
			}
			after, ok := store.GetProviderResource(resource.ID)
			if !ok || after.FailureCount != 0 || !after.Healthy || after.CooldownUntil != nil {
				t.Fatalf("local poll refusal penalized provider health: %+v", after)
			}
		})
	}
}
