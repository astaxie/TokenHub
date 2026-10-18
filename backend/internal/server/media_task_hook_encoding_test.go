package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerMediaTaskEncodingHook(t *testing.T, server *Server, stage pluginmeta.GatewayHookStage, protocol string, escape bool) {
	t.Helper()
	dataClass := pluginmeta.DataRequestBody
	if stage == pluginmeta.StageRequestTransform {
		dataClass = pluginmeta.DataProviderRequest
	}
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "test.media-task-encoding", HookID: string(stage), Stage: stage, Priority: 1000,
		Scope:         pluginmeta.GatewayHookScope{RouteProtocols: []string{protocol}},
		Reads:         []pluginmeta.GatewayDataClass{dataClass},
		Writes:        []pluginmeta.GatewayDataClass{dataClass},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var fields map[string]any
		if err := decodeResponsesJSON(input.Data[dataClass], &fields); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if escape {
			data = bytes.ReplaceAll(data, []byte(`"task_`), []byte(`"\u0074ask_`))
			data = bytes.ReplaceAll(data, []byte(`"en"`), []byte(`"\u0065n"`))
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{dataClass: {Value: data}}}, nil
	})); err != nil {
		t.Fatal(err)
	}
}

func TestMediaTaskReferenceHooksPreserveEquivalentJSONStrings(t *testing.T) {
	stages := []pluginmeta.GatewayHookStage{pluginmeta.StageDecodeNormalize, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre, pluginmeta.StageContextOptimize, pluginmeta.StageRequestTransform}
	for _, stage := range stages {
		for _, field := range mediaTaskReferenceFields {
			for _, encoding := range []string{"normalized", "escaped"} {
				t.Run(string(stage)+"/"+field+"/"+encoding, func(t *testing.T) {
					calls := 0
					server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
						calls++
						if calls > 1 {
							var fields map[string]any
							if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
								t.Fatal(err)
							}
							if fields[field] != "vendor-encoding-reference" {
								t.Errorf("incorrect upstream reference: %#v", fields[field])
							}
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"data":[{"task_id":"vendor-encoding-reference"}]}`)
					}, "video")
					id := submitMediaTask(t, server, key, "/v1/videos/generations")
					registerMediaTaskEncodingHook(t, server, stage, "videos/generations", encoding == "escaped")
					body, err := json.Marshal(map[string]any{"model": "public-media", field: id})
					if err != nil {
						t.Fatal(err)
					}
					if encoding == "normalized" {
						body = bytes.ReplaceAll(body, []byte(`"task_`), []byte(`"\u0074ask_`))
					}
					response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", json.RawMessage(body), key)
					if response.Code != http.StatusOK || calls != 2 {
						t.Fatalf("equivalent reference rejected: status=%d upstream_calls=%d body=%s", response.Code, calls, response.Body)
					}
				})
			}
		}
	}
}

func TestMediaTaskPollHooksPreserveEquivalentJSONStrings(t *testing.T) {
	stages := []pluginmeta.GatewayHookStage{pluginmeta.StageDecodeNormalize, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre, pluginmeta.StageContextOptimize, pluginmeta.StageRequestTransform}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			calls := 0
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					_, _ = io.WriteString(w, `{"data":[{"task_id":"vendor-encoding-poll"}]}`)
					return
				}
				if r.URL.Path != "/v1/tasks/vendor-encoding-poll" || r.URL.Query().Get("language") != "en" {
					t.Errorf("incorrect upstream poll target: %s", r.URL)
				}
				_, _ = io.WriteString(w, `{"data":{"id":"vendor-encoding-poll","status":"completed"}}`)
			}, "video")
			id := submitMediaTask(t, server, key, "/v1/videos/generations")
			registerMediaTaskEncodingHook(t, server, stage, "tasks", true)
			response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id+"?language=en", nil, key)
			if response.Code != http.StatusOK || calls != 2 || !strings.Contains(response.Body, id) {
				t.Fatalf("equivalent poll rejected: status=%d upstream_calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestMediaTaskHookStringsRejectTypeAndPresenceChanges(t *testing.T) {
	for _, operation := range []string{"reference", "poll"} {
		for _, mutation := range []string{"null", "number", "boolean", "array", "object", "remove", "add", "replace"} {
			t.Run(operation+"/"+mutation, func(t *testing.T) {
				field := "draft_task_id"
				request := mediaRequest{Path: "/v1/videos/generations", Fields: map[string]json.RawMessage{"model": json.RawMessage(`"public-media"`)}}
				if operation == "poll" {
					field, request.Task = "language", &mediaTaskBinding{}
					request.Fields["task_id"] = json.RawMessage(`"task_fixture"`)
				}
				request.Fields[field] = json.RawMessage(`"en"`)
				patched := cloneRawJSON(request.Fields, 0)
				switch mutation {
				case "remove":
					delete(patched, field)
				case "add":
					delete(request.Fields, field)
				default:
					patched[field] = json.RawMessage(map[string]string{"null": `null`, "number": `1`, "boolean": `true`, "array": `["en"]`, "object": `{"value":"en"}`, "replace": `"ja"`}[mutation])
				}
				data, err := json.Marshal(patched)
				if err != nil {
					t.Fatal(err)
				}
				err = request.applyPatch(data)
				if err == nil || AsHTTPError(err).Code != "gateway_hook_patch_invalid" || providerErrorDisposition(err) != ProviderErrorPolicy {
					t.Fatalf("invalid field change was not rejected neutrally: %v", err)
				}
			})
		}
	}
}
