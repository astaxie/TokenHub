package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerMediaProviderTestHook(t *testing.T, server *Server, hook pluginmeta.GatewayHookDescriptor, handler pluginmeta.GatewayHookHandlerFunc) {
	t.Helper()
	hook.PluginID = "test.media-provider"
	hook.Stage = pluginmeta.StageProviderCall
	hook.Priority = 2000
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, handler); err != nil {
		t.Fatal(err)
	}
}

func TestMediaProviderPolicyHooksPrecedeAdapter(t *testing.T) {
	for _, action := range []string{"deny", "fail-closed", "skip-route", "observe"} {
		t.Run(action, func(t *testing.T) {
			upstreamCalls, hookCalls := 0, 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls++
				w.Header().Set("Content-Type", "audio/mpeg")
				_, _ = io.WriteString(w, "audio")
			}, "audio")
			provider, _ := store.GetProvider("media-provider")
			provider.ID = "media-fallback"
			store.AddProvider(provider)
			store.AddRoute(ModelRoute{ID: "media-fallback-route", ModelName: "public-media", ProviderID: provider.ID, ProviderModel: "vendor-media", Status: StatusActive, Priority: 2, Weight: 100})
			hook := pluginmeta.GatewayHookDescriptor{HookID: "policy", Scope: pluginmeta.GatewayHookScope{ProviderIDs: []string{"media-provider"}}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if action == "skip-route" {
				hook.FailurePolicy = pluginmeta.FailurePolicySkipRoute
			}
			registerMediaProviderTestHook(t, server, hook, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				hookCalls++
				switch action {
				case "deny":
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
				case "fail-closed", "skip-route":
					return pluginmeta.GatewayHookResult{}, errors.New("fixture policy failure")
				default:
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
				}
			})
			recorder := doReasoningJSON(t, server.Handler(), "/v1/audio/speech", map[string]any{"model": "public-media", "input": "fixture"}, key)
			response := responseBody{Code: recorder.Code, Body: recorder.Body.String(), Header: recorder.Header()}
			if hookCalls != 1 {
				t.Fatalf("hook calls = %d", hookCalls)
			}
			if action == "deny" || action == "fail-closed" {
				if response.Code == http.StatusOK || upstreamCalls != 0 {
					t.Fatalf("policy bypass: status=%d calls=%d body=%s", response.Code, upstreamCalls, response.Body)
				}
			} else if response.Code != http.StatusOK || upstreamCalls != 1 {
				t.Fatalf("allowed media failed: status=%d calls=%d body=%s", response.Code, upstreamCalls, response.Body)
			}
			if action == "skip-route" && response.Header.Get("x-tokenhub-provider") != provider.ID {
				t.Fatalf("skipped route was used: %v", response.Header)
			}
		})
	}
}

func TestMediaProviderHookOnlyRoutes(t *testing.T) {
	for _, format := range []string{"json", "binary", "stream"} {
		t.Run(format, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream call") }, "image")
			if _, err := store.UpdateProvider("media-provider", Provider{Type: "media-hook-only", Healthy: true}); err != nil {
				t.Fatal(err)
			}
			output := pluginmeta.DataProviderResponse
			if format == "stream" {
				output = pluginmeta.DataStreamEvents
			}
			hook := pluginmeta.GatewayHookDescriptor{HookID: "respond", Writes: []pluginmeta.GatewayDataClass{output, pluginmeta.DataUsage}}
			registerMediaProviderTestHook(t, server, hook, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				usage := Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}
				switch format {
				case "binary":
					return rawProviderCallResult(t, map[string]any{"data_base64": base64.StdEncoding.EncodeToString([]byte{0, 255, 42}), "content_type": "image/png"}, usage), nil
				case "stream":
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
						pluginmeta.DataStreamEvents: {Value: json.RawMessage(`[{"event":"media.done","data":"{\"id\":9007199254740993}"}]`)},
						pluginmeta.DataUsage:        {Value: json.RawMessage(`{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}`)},
					}}, nil
				default:
					return rawProviderCallResult(t, json.RawMessage(`{"data":[{"url":"https://example.com/hook.png"}],"id":9007199254740993}`), usage), nil
				}
			})
			recorder := doReasoningJSON(t, server.Handler(), "/v1/images/generations", map[string]any{"model": "public-media", "prompt": "fixture", "stream": format == "stream"}, key)
			response := responseBody{Code: recorder.Code, Body: recorder.Body.String(), Header: recorder.Header()}
			if response.Code != http.StatusOK {
				t.Fatalf("hook response: %d %s", response.Code, response.Body)
			}
			switch format {
			case "binary":
				if response.Header.Get("Content-Type") != "image/png" || response.Body != string([]byte{0, 255, 42}) {
					t.Fatalf("binary changed: %v %q", response.Header, response.Body)
				}
			case "stream":
				if response.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(response.Body, "event: media.done\ndata: {\"id\":9007199254740993}") {
					t.Fatalf("stream changed: %v %q", response.Header, response.Body)
				}
			default:
				if response.Header.Get("Content-Type") != "application/json" || !strings.Contains(response.Body, `"id":9007199254740993`) {
					t.Fatalf("JSON changed: %v %s", response.Header, response.Body)
				}
			}
			if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 5 {
				t.Fatalf("hook usage lost: %+v", records)
			}
		})
	}
}

func TestMediaProviderHookCapabilityMatchesScopeAndMode(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, scenario := range []string{"matching", "project-mismatch", "key-mismatch", "protocol-mismatch", "operation-mismatch", "wrong-mode", "observer-only"} {
			t.Run(scenario+map[bool]string{false: "/buffered", true: "/stream"}[stream], func(t *testing.T) {
				server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream call") }, "audio")
				if _, err := store.UpdateProvider("media-provider", Provider{Type: "media-hook-only", Healthy: true}); err != nil {
					t.Fatal(err)
				}
				apiKey := store.ListAPIKeys()[0]
				scope := pluginmeta.GatewayHookScope{ProjectIDs: []string{apiKey.ProjectID}, APIKeyIDs: []string{apiKey.ID}, RouteProtocols: []string{"audio/speech"}, Operations: []string{"provider_call"}}
				outputs := []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}
				if stream {
					outputs = []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}
				}
				switch scenario {
				case "project-mismatch":
					scope.ProjectIDs = []string{"another-project"}
				case "key-mismatch":
					scope.APIKeyIDs = []string{"another-key"}
				case "protocol-mismatch":
					scope.RouteProtocols = []string{"responses"}
				case "operation-mismatch":
					scope.Operations = []string{"response_post"}
				case "wrong-mode":
					outputs = []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}
					if stream {
						outputs = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}
					}
				case "observer-only":
					outputs = nil
				}
				calls := 0
				registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "scope", Scope: scope, Writes: outputs}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					calls++
					value := json.RawMessage(`{"text":"hook output"}`)
					if stream {
						value = json.RawMessage(`[{"data":"hook output"}]`)
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{outputs[0]: {Value: value}}}, nil
				})
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "input": "fixture", "stream": stream}, key)
				if scenario == "matching" {
					if calls != 1 || response.Code != http.StatusOK || !strings.Contains(response.Body, "hook output") {
						t.Fatalf("matching hook failed: calls=%d status=%d body=%s", calls, response.Code, response.Body)
					}
				} else if calls != 0 || response.Code != http.StatusNotImplemented || !strings.Contains(response.Body, "provider_capability_not_supported") {
					t.Fatalf("mismatched hook admitted: calls=%d status=%d body=%s", calls, response.Code, response.Body)
				}
			})
		}
	}
}

func TestMediaProviderHookRejectsInvalidBinaryEnvelope(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"data_base64":"YQ=="}`, `{"data_base64":null,"content_type":"audio/mpeg"}`, `{"data_base64":42,"content_type":"audio/mpeg"}`, `{"data_base64":"YQ==","content_type":null}`, `{"data_base64":"!","content_type":"audio/mpeg"}`, `{"data_base64":"YQ==","content_type":"audio/mpeg\r\nX-Injected: true"}`, `{"data_base64":"YQ==","content_type":""}`} {
		_, err := mediaProviderHookResponse(json.RawMessage(raw))
		if err == nil || providerErrorDisposition(err) != ProviderErrorPolicy {
			t.Errorf("invalid hook response accepted: %s, err=%v", raw, err)
		}
	}
}

func TestMediaProviderMultipartStreamUsesStreamHook(t *testing.T) {
	server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream call") }, "audio")
	for _, output := range []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataStreamEvents} {
		registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: string(output), Writes: []pluginmeta.GatewayDataClass{output}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
			if output != pluginmeta.DataStreamEvents {
				t.Error("multipart stream used a non-streaming hook")
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
			}
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataStreamEvents: {Value: json.RawMessage(`[{"event":"transcript.text.done","data":"{\"text\":\"fixture\"}"}]`)},
			}}, nil
		})
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{"model": "public-media", "stream": "true"} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := writer.CreateFormFile("file", "fixture.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0, 255}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(recorder.Body.String(), "event: transcript.text.done") {
		t.Fatalf("multipart stream failed: %d %v %s", recorder.Code, recorder.Header(), recorder.Body)
	}
}

func TestMediaProviderHookStreamBufferIsBounded(t *testing.T) {
	buffer := mediaHookStreamBuffer{limit: 4}
	if n, err := buffer.Write([]byte("data")); n != 4 || err != nil {
		t.Fatalf("bounded write: %d %v", n, err)
	}
	if n, err := buffer.Write([]byte("more")); n != 0 || err == nil || providerErrorDisposition(err) != ProviderErrorPolicy || buffer.String() != "data" {
		t.Fatalf("buffer overflow: n=%d err=%v data=%q", n, err, buffer.String())
	}
}
