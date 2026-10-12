package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestGeminiMediaScopedHookDiscoveryAndUsageOverrides(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, explicitZero := range []bool{false, true} {
			name := map[bool]string{false: "JSON", true: "SSE"}[stream] + map[bool]string{false: "/body usage", true: "/zero usage"}[explicitZero]
			t.Run(name, func(t *testing.T) {
				server, store, key := newGeminiMediaFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("hook-only route reached upstream") })
				provider, _ := store.GetProvider("media-provider")
				provider.Type = "gemini-media-hook-only"
				if _, err := store.UpdateProvider(provider.ID, provider); err != nil {
					t.Fatal(err)
				}
				apiKey := store.ListAPIKeys()[0]
				output := pluginmeta.DataProviderResponse
				if stream {
					output = pluginmeta.DataStreamEvents
				}
				calls := 0
				scope := pluginmeta.GatewayHookScope{RouteProtocols: []string{providerRouteProtocolGemini}, ProjectIDs: []string{apiKey.ProjectID}, APIKeyIDs: []string{apiKey.ID}}
				registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "native-media", Scope: scope, Writes: []pluginmeta.GatewayDataClass{output, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					calls++
					value := json.RawMessage(geminiMediaFixtureResponse)
					if stream {
						value, _ = json.Marshal([]gatewayStreamEventView{{Data: geminiMediaFixtureResponse}})
					}
					writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{output: {Value: value}}
					if explicitZero {
						writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: json.RawMessage(`{"total_tokens":0}`)}
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
				})
				for _, path := range []string{"/v1beta/models", "/v1beta/models/public-media"} {
					response := doGeminiJSON(t, server.Handler(), http.MethodGet, path, nil, key)
					if response.Code != 200 || !strings.Contains(response.Body, "models/public-media") {
						t.Fatalf("scoped model missing: %d %s", response.Code, response.Body)
					}
				}
				count := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:countTokens", json.RawMessage(geminiMediaFixtureRequest), key)
				if count.Code != 200 {
					t.Fatalf("discovered model denied countTokens: %d %s", count.Code, count.Body)
				}
				action := "generateContent"
				if stream {
					action = "streamGenerateContent?alt=sse"
				}
				response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:"+action, json.RawMessage(geminiMediaFixtureRequest), key)
				if response.Code != 200 || calls != 1 || !strings.Contains(response.Body, "inlineData") {
					t.Fatalf("native hook failed: %d calls=%d %s", response.Code, calls, response.Body)
				}
				records := store.ListUsageRecords()
				if explicitZero {
					for _, record := range records {
						if record.TotalTokens != 0 {
							t.Fatalf("explicit zero ignored: %+v", records)
						}
					}
				} else if len(records) != 1 || records[0].TotalTokens != 15 {
					t.Fatalf("native hook usage lost: %+v", records)
				}
				otherProject := store.CreateProject(Project{Name: "Other Gemini tenant", Status: StatusActive})
				otherKey, otherSecret, err := store.CreateAPIKey(otherProject.ID, APIKey{Name: "Other key", Allowed: []string{"public-media"}, Status: StatusActive}, "thk_other_gemini_media")
				if err != nil {
					t.Fatal(err)
				}
				if geminiModelAllowed(server.geminiAccessibleModels(otherKey), "public-media") {
					t.Fatal("project-scoped hook leaked into model discovery")
				}
				denied := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:"+action, json.RawMessage(geminiMediaFixtureRequest), otherSecret)
				if denied.Code != http.StatusNotImplemented || calls != 1 {
					t.Fatalf("scope bypassed: %d calls=%d %s", denied.Code, calls, denied.Body)
				}
			})
		}
	}
}

func TestGeminiMediaPostValidationRetainsAttributedUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "JSON", true: "SSE"}[stream], func(t *testing.T) {
			calls := 0
			server, store, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				body := geminiMediaFixtureResponse
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
					body = "data: " + body + "\n\n"
				}
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, body)
			})
			registerGeminiMediaHook(t, server, pluginmeta.StageResponsePost, []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return rawProviderResponsePatch(t, json.RawMessage(`{"candidates":[{"content":{"parts":[{"text":"unfinished"}]}}]}`)), nil
			})
			registerGeminiMediaHook(t, server, pluginmeta.StageUsageAttribution, []pluginmeta.GatewayDataClass{pluginmeta.DataUsage}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return rawUsagePatch(t, Usage{CompletionTokens: 7, TotalTokens: 7}), nil
			})
			// A JSON post-hook replacement lacking candidates is invalid too.
			if !stream {
				registerGeminiMediaHook(t, server, pluginmeta.StageGuardrailPost, []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					return rawProviderResponsePatch(t, json.RawMessage(`{"unrelated":"object"}`)), nil
				})
			}
			action := "generateContent"
			if stream {
				action = "streamGenerateContent?alt=sse"
			}
			response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:"+action, json.RawMessage(geminiMediaFixtureRequest), key)
			if response.Code != http.StatusBadGateway || calls != 1 {
				t.Fatalf("malformed native output accepted: %d calls=%d %s", response.Code, calls, response.Body)
			}
			records := store.ListUsageRecords()
			if len(records) != 1 || records[0].TotalTokens != 7 {
				t.Fatalf("attributed usage lost: %+v", records)
			}
			var attempts []RouteAttemptLog
			if err := store.db.Find(&attempts).Error; err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 1 || attempts[0].TotalTokens != 7 {
				t.Fatalf("attributed attempt usage lost: %+v", attempts)
			}
		})
	}
}

func TestGeminiMediaStreamRetainsLateUsageAndLargeParts(t *testing.T) {
	part := strings.Repeat("A", maxSSEEventBytes+4)
	stream := `data: {"candidates":[{"index":0,"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + part + `"}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5}}` + "\n\n" + `data: {"usageMetadata":{"candidatesTokenCount":10,"totalTokenCount":15}}` + "\n\n"
	server, store, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	})
	hookCalls := 0
	registerGeminiMediaHook(t, server, pluginmeta.StageStreamTransform, []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		hookCalls++
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})
	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:streamGenerateContent?alt=sse", json.RawMessage(geminiMediaFixtureRequest), key)
	if response.Code != 200 || response.Body != stream || hookCalls != 2 {
		t.Fatalf("large native stream changed: status=%d bytes=%d hooks=%d", response.Code, len(response.Body), hookCalls)
	}
	records := store.ListUsageRecords()
	if len(records) != 1 || records[0].InputTokens != 5 || records[0].OutputTokens != 10 || records[0].TotalTokens != 15 {
		t.Fatalf("late usage discarded: %+v", records)
	}
}
