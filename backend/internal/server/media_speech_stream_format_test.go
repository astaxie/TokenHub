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

func TestMediaSpeechSSEFormatSelectsStreamingProviderHooks(t *testing.T) {
	for _, hookOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "upstream", true: "plugin"}[hookOnly], func(t *testing.T) {
			upstreamCalls, hookCalls := 0, 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls++
				var request map[string]json.RawMessage
				decodeFixtureRequest(t, r.Body, &request)
				if string(request["stream_format"]) != `"sse"` || request["stream"] != nil {
					t.Errorf("speech controls changed: %s", request)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: speech.audio.done\ndata: {\"type\":\"speech.audio.done\"}\n\n")
			}, "audio")
			if hookOnly {
				if _, err := store.UpdateProvider("media-provider", Provider{Type: "media-hook-only", Healthy: true}); err != nil {
					t.Fatal(err)
				}
			}
			registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "speech-stream", Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				hookCalls++
				var auth struct{ Stream bool }
				if json.Unmarshal(input.Data[pluginmeta.DataAuthContext], &auth) != nil || !auth.Stream {
					t.Error("speech SSE request was admitted as non-streaming")
				}
				if !hookOnly {
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
				}
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
					pluginmeta.DataStreamEvents: {Value: json.RawMessage(`[{"event":"speech.audio.done","data":"{\"type\":\"speech.audio.done\"}"}]`)},
				}}, nil
			})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "input": "Hello", "stream_format": "sse"}, key)
			if response.Code != http.StatusOK || hookCalls != 1 || !strings.Contains(response.Body, "speech.audio.done") || (hookOnly && upstreamCalls != 0) || (!hookOnly && upstreamCalls != 1) {
				t.Fatalf("speech SSE mode failed: status=%d hooks=%d upstream=%d body=%s", response.Code, hookCalls, upstreamCalls, response.Body)
			}
		})
	}
}

func TestMediaSpeechHooksCannotChangeSSEFormatMode(t *testing.T) {
	for _, original := range []string{"audio", "sse"} {
		t.Run(original, func(t *testing.T) {
			calls := 0
			server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { calls++ }, "audio")
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.speech-format", HookID: "change-mode", Stage: pluginmeta.StagePrivacyPre, Priority: 1000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				format := "sse"
				if original == "sse" {
					format = "audio"
				}
				return rawRequestBodyPatch(t, map[string]any{"model": "public-media", "stream_format": format}), nil
			})); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "stream_format": original}, key)
			if response.Code != http.StatusBadGateway || calls != 0 || !strings.Contains(response.Body, "gateway_hook_patch_invalid") {
				t.Fatalf("speech hook changed stream mode: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestMediaSpeechRejectsAmbiguousStreamFormat(t *testing.T) {
	calls := 0
	server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { calls++ }, "audio")
	for _, raw := range []string{`null`, `true`, `42`, `["sse"]`, `""`} {
		response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", json.RawMessage(`{"model":"public-media","stream_format":`+raw+`}`), key)
		if response.Code != http.StatusBadRequest {
			t.Errorf("invalid format accepted: %s status=%d body=%s", raw, response.Code, response.Body)
		}
	}
	response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/speech", [][2]string{{"model", "public-media"}, {"stream_format", "audio"}, {"stream_format", "sse"}})
	if response.Code != http.StatusBadRequest {
		t.Errorf("duplicate format accepted: %d %s", response.Code, response.Body)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "public-media"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("stream_format", "format.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "sse"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", &body)
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("invalid format reached upstream: status=%d calls=%d body=%s", recorder.Code, calls, recorder.Body)
	}
}

func TestMediaStreamFormatIsScopedToSpeech(t *testing.T) {
	for _, tc := range []struct {
		path, fields string
		stream       bool
	}{
		{"/v1/audio/speech", `"stream_format":"audio"`, false},
		{"/v1/audio/speech", `"stream_format":"sse"`, true},
		{"/v1/audio/speech", `"stream_format":"sse","stream":false`, true},
		{"/v1/audio/speech", `"stream":true`, true},
		{"/v1/images/generations", `"stream_format":"sse"`, false},
		{"/v1/audio/transcriptions", `"stream_format":["vendor-extension"],"stream":true`, true},
	} {
		request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"model":"public-media",`+tc.fields+`}`))
		request.Header.Set("Content-Type", "application/json")
		decoded, err := decodeMediaRequest(httptest.NewRecorder(), request, 1024)
		if err != nil || decoded.stream() != tc.stream {
			t.Errorf("unexpected stream mode: path=%s fields=%s stream=%t err=%v", tc.path, tc.fields, decoded.stream(), err)
		}
	}
}
