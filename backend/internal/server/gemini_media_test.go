package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

const geminiMediaFixtureRequest = `{"contents":[{"role":"user","parts":[{"text":"A private landscape"},{"inlineData":{"mimeType":"image/png","data":"cmVmZXJlbmNl"},"thoughtSignature":"opaque-input-signature"}]}],"generationConfig":{"responseModalities":["TEXT","IMAGE"],"imageConfig":{"aspectRatio":"1:1","imageSize":"4K"},"seed":9007199254740993},"tools":[{"google_search":{"search_types":{"image_search":{}}}}]}`
const geminiMediaFixtureResponse = `{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="},"thoughtSignature":"opaque-output-signature"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":10,"totalTokenCount":15},"vendor_id":9007199254740993}`

func newGeminiMediaFixture(t *testing.T, upstream http.HandlerFunc) (*Server, *GormStore, string) {
	t.Helper()
	server, store, key := newMediaGatewayFixture(t, upstream, "image")
	provider, _ := store.GetProvider("media-provider")
	provider.Type = ProviderGemini
	provider.BaseURL = strings.TrimSuffix(provider.BaseURL, "/v1") + "/v1beta"
	if _, err := store.UpdateProvider(provider.ID, provider); err != nil {
		t.Fatal(err)
	}
	return server, store, key
}

func registerGeminiMediaHook(t *testing.T, server *Server, stage pluginmeta.GatewayHookStage, writes []pluginmeta.GatewayDataClass, handler pluginmeta.GatewayHookHandlerFunc) {
	t.Helper()
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.gemini-media", HookID: string(stage), Stage: stage, Priority: 2000, Writes: writes, Reads: writes, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{providerRouteProtocolGemini}}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
	if stage == pluginmeta.StageCacheLookup || stage == pluginmeta.StageCacheWrite {
		hook.FailurePolicy = pluginmeta.FailurePolicyFailOpen
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, handler); err != nil {
		t.Fatal(err)
	}
}

func TestGeminiMediaNativePayloadsAndStreamingPreserveProtocol(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "JSON", true: "SSE"}[stream], func(t *testing.T) {
			calls := 0
			want := geminiMediaFixtureResponse
			action := "generateContent"
			if stream {
				action = "streamGenerateContent?alt=sse"
				want = "data: " + want + "\n\n"
			}
			server, store, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.RequestURI() != "/v1beta/models/vendor-media:"+action || r.Header.Get("x-goog-api-key") != "upstream-fixture-secret" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Errorf("native path/auth mismatch: %s headers=%v", r.URL, r.Header)
				}
				var got map[string]any
				decoder := json.NewDecoder(r.Body)
				decoder.UseNumber()
				if err := decoder.Decode(&got); err != nil {
					t.Fatal(err)
				}
				var expected map[string]any
				if err := decodeResponsesJSON([]byte(geminiMediaFixtureRequest), &expected); err != nil {
					t.Fatal(err)
				}
				gotData, _ := json.Marshal(got)
				wantData, _ := json.Marshal(expected)
				if string(gotData) != string(wantData) {
					t.Errorf("native fields changed: %s want %s", gotData, wantData)
				}
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
				}
				w.Header().Set("Content-Type", contentType)
				w.Header().Set("Set-Cookie", "provider-only=private")
				_, _ = io.WriteString(w, want)
			})
			for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageCacheLookup, pluginmeta.StageCacheWrite} {
				registerGeminiMediaHook(t, server, stage, nil, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					t.Error("media reached cache")
					return pluginmeta.GatewayHookResult{}, nil
				})
			}
			for i := 0; i < 2; i++ {
				request := httptest.NewRequest(http.MethodPost, "/v1beta/models/public-media:"+action, strings.NewReader(geminiMediaFixtureRequest))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("x-goog-api-key", key)
				request.Header.Set("Cookie", "client-only=private")
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Body.String() != want || response.Header().Get("Set-Cookie") != "" {
					t.Fatalf("native output changed: %d %s", response.Code, response.Body)
				}
			}
			if calls != 2 {
				t.Fatalf("generation count=%d", calls)
			}
			usage := store.ListUsageRecords()
			if len(usage) != 2 || usage[0].InputTokens != 5 || usage[0].OutputTokens != 10 || usage[0].TotalTokens != 15 {
				t.Fatalf("native usage lost: %+v", usage)
			}
			var audits []RequestPayloadLog
			if err := store.db.Find(&audits).Error; err != nil {
				t.Fatal(err)
			}
			for _, audit := range audits {
				for _, secret := range []string{"private landscape", "cmVmZXJlbmNl", "aW1hZ2U=", "opaque-input", "opaque-output"} {
					if strings.Contains(audit.RequestBody+audit.ResponseBody, secret) {
						t.Errorf("media stored in request audit: %s", secret)
					}
				}
			}
			if !stream {
				assertMediaOperationSchema(t, document, "/v1beta/models/{model}:generateContent", "post", "", geminiMediaFixtureRequest)
				assertMediaOperationSchema(t, document, "/v1beta/models/{model}:generateContent", "post", "200", geminiMediaFixtureResponse)
			}
		})
	}
}

func TestGeminiMediaPreflightPoliciesAndControlPatches(t *testing.T) {
	for _, mode := range []string{"mask", "block", "model patch", "stream patch", "stream type patch"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server, store, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				data, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(data), "[REDACTED]") || strings.Contains(string(data), "fixture@example.com") || !strings.Contains(string(data), "cmVmZXJlbmNl") {
					t.Errorf("text masking changed assets or leaked text: %s", data)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, geminiMediaFixtureResponse)
			})
			if mode == "mask" || mode == "block" {
				if _, err := store.CreateGuardrailPolicy(guardrails.Policy{Name: "Protect native media text", Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}}, DetectionItems: []guardrails.DetectionItem{{Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: mode, Config: map[string]any{"data_types": []string{"email"}}}}}); err != nil {
					t.Fatal(err)
				}
			} else {
				registerGeminiMediaHook(t, server, pluginmeta.StageRequestTransform, []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					var payload map[string]any
					if err := decodeResponsesJSON(input.Data[pluginmeta.DataProviderRequest], &payload); err != nil {
						t.Fatal(err)
					}
					switch mode {
					case "model patch":
						payload["model"] = "unapproved-model"
					case "stream patch":
						payload["stream"] = true
					default:
						payload["stream"] = "false"
					}
					return rawProviderRequestPatch(t, payload), nil
				})
			}
			request := strings.Replace(geminiMediaFixtureRequest, "A private landscape", "Email fixture@example.com", 1)
			response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:generateContent", json.RawMessage(request), key)
			wantStatus := http.StatusBadGateway
			wantCalls := 0
			if mode == "mask" {
				wantStatus = http.StatusOK
				wantCalls = 1
			} else if mode == "block" {
				wantStatus = http.StatusForbidden
			}
			if response.Code != wantStatus || calls != wantCalls {
				t.Fatalf("policy result=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestGeminiMediaErrorsDoNotRetryAndRetainUsage(t *testing.T) {
	for _, mode := range []string{"HTTP 500", "HTTP 408", "JSON error", "invalid JSON", "truncated stream", "stream error"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			stream := strings.Contains(mode, "stream")
			server, store, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "HTTP 500":
					w.WriteHeader(500)
				case "HTTP 408":
					w.WriteHeader(408)
				case "JSON error":
					_, _ = io.WriteString(w, `{"error":{"message":"upstream-fixture-secret"},"usageMetadata":{"totalTokenCount":15}}`)
				case "invalid JSON":
					_, _ = io.WriteString(w, geminiMediaFixtureResponse+" invalid")
				default:
					w.Header().Set("Content-Type", "text/event-stream")
					body := strings.Replace(geminiMediaFixtureResponse, `"finishReason":"STOP",`, "", 1)
					_, _ = io.WriteString(w, "data: "+body+"\n\n")
					if mode == "stream error" {
						_, _ = io.WriteString(w, "data: {\"error\":{\"message\":\"upstream-fixture-secret\"}}\n\n")
					}
				}
			})
			store.AddRoute(ModelRoute{ID: "fallback-gemini-media", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "vendor-media-fallback", Status: StatusActive, Priority: 2, Weight: 100})
			action := "generateContent"
			if stream {
				action = "streamGenerateContent?alt=sse"
			}
			response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:"+action, json.RawMessage(geminiMediaFixtureRequest), key)
			if response.Code == http.StatusOK || calls != 1 || strings.Contains(response.Body, "upstream-fixture-secret") {
				t.Fatalf("failure retried/leaked: %d calls=%d %s", response.Code, calls, response.Body)
			}
			if !strings.HasPrefix(mode, "HTTP") {
				usage := store.ListUsageRecords()
				if len(usage) != 1 || usage[0].TotalTokens != 15 {
					t.Fatalf("failed generation usage lost: %+v", usage)
				}
			}
		})
	}
}
