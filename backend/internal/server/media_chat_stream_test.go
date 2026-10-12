package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaChatPreservesLargeSingleAudioEvent(t *testing.T) {
	// MiMo voice cloning can return all generated PCM audio in one SSE event.
	audio := strings.Repeat("A", maxSSEEventBytes+4)
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"audio\":{\"data\":\"" + audio + "\"}}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n\n"
	for _, withHook := range []bool{false, true} {
		name := map[bool]string{false: "direct", true: "response hook"}[withHook]
		t.Run(name, func(t *testing.T) {
			calls, hookCalls := 0, 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, stream)
			}, "audio")
			if withHook {
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-chat-stream", HookID: "observe", Stage: pluginmeta.StageResponsePost, Priority: 1000, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					hookCalls++
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
				})); err != nil {
					t.Fatal(err)
				}
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", mediaChatAudioRequest(), key)
			if response.Code != http.StatusOK || response.Body != stream || calls != 1 {
				t.Fatalf("audio event lost: status=%d response_bytes=%d want_bytes=%d calls=%d", response.Code, len(response.Body), len(stream), calls)
			}
			if withHook && hookCalls == 0 {
				t.Fatal("large audio stream bypassed response hooks")
			}
			usage := store.ListUsageRecords()
			if len(usage) != 1 || usage[0].InputTokens != 2 || usage[0].OutputTokens != 3 || usage[0].TotalTokens != 5 {
				t.Fatalf("audio stream usage changed: %+v", usage)
			}
			logs := store.ListRequestLogs()
			if len(logs) != 1 || logs[0].StatusCode != http.StatusOK {
				t.Fatalf("audio stream was not recorded as successful: %+v", logs)
			}
		})
	}
}

func TestMediaChatRequestFieldsCannotWidenTextStreamLimit(t *testing.T) {
	calls := 0
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"audio\":{\"data\":\"")
		_, _ = io.WriteString(w, strings.Repeat("A", maxSSEEventBytes+4))
		_, _ = io.WriteString(w, "\"}}}]}\n\ndata: [DONE]\n\n")
	}, "chat")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", mediaChatAudioRequest(), key)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "provider_invalid_response") || calls != 1 {
		t.Fatalf("text event ceiling changed: status=%d calls=%d response_bytes=%d", response.Code, calls, len(response.Body))
	}
	if usage := store.ListUsageRecords(); len(usage) != 0 {
		t.Fatalf("rejected event invented usage: %+v", usage)
	}
}

func mediaChatAudioRequest() map[string]any {
	return map[string]any{
		"model": "public-media", "stream": true,
		"messages": []any{map[string]any{"role": "user", "content": ""}, map[string]any{"role": "assistant", "content": "A long narration"}},
		"audio":    map[string]any{"format": "pcm16", "voice": "data:audio/wav;base64,YXVkaW8="}, "modalities": []string{"audio"},
	}
}
