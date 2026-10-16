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

func TestMultipartTranscriptionHooksPreserveNativeFilesAndPolicy(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		for _, mode := range []string{"observe", "deny", "handle JSON", "handle stream"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				upstreamCalls, hookCalls := 0, 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					upstreamCalls++
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						return
					}
					defer func() { _ = r.MultipartForm.RemoveAll() }()
					file, header, err := r.FormFile("file")
					if err != nil {
						t.Error(err)
						return
					}
					defer func() { _ = file.Close() }()
					data, err := io.ReadAll(file)
					if err != nil || header.Filename != "fixture.wav" || !bytes.Equal(data, transcriptionWAVFixture("fixture audio")) || r.FormValue("model") != "vendor-media" {
						t.Errorf("native transcription upload changed: filename=%q model=%q error=%v", header.Filename, r.FormValue("model"), err)
						return
					}
					writeJSON(w, http.StatusOK, map[string]any{"text": "native transcript"})
				}, "audio")
				store.AddRoute(ModelRoute{ID: "hook-file-fallback", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "fallback-model", Priority: 2, Weight: 100, Status: StatusActive})
				output := pluginmeta.DataProviderResponse
				if mode == "handle stream" {
					output = pluginmeta.DataStreamEvents
				}
				registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "multipart-policy", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{strings.TrimPrefix(path, "/v1/")}}, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest}, Writes: []pluginmeta.GatewayDataClass{output}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					hookCalls++
					var fields map[string]any
					if err := json.Unmarshal(input.Data[pluginmeta.DataProviderRequest], &fields); err != nil || fields["prompt"] != "fixture prompt" || fields["file"] != nil {
						t.Fatalf("multipart text policy input changed: %v", err)
					}
					switch mode {
					case "deny":
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
					case "handle JSON", "handle stream":
						value := json.RawMessage(`{"text":"unverified hook transcript"}`)
						if mode == "handle stream" {
							value = json.RawMessage(`[{"event":"transcript.text.done","data":"{\"text\":\"unverified hook transcript\"}"}]`)
						}
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{output: {Value: value}}}, nil
					default:
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
					}
				})
				fields := [][2]string{{"model", "public-media"}, {"prompt", "fixture prompt"}}
				if mode == "handle stream" {
					fields = append(fields, [2]string{"stream", "true"})
				}
				response := doMediaMultipartReviewRequest(t, server, key, path, fields)
				if hookCalls != 1 {
					t.Fatalf("multipart policy hook retried or skipped: calls=%d", hookCalls)
				}
				if mode == "observe" {
					if response.Code != http.StatusOK || upstreamCalls != 1 || !strings.Contains(response.Body.String(), "native transcript") {
						t.Fatalf("native transcription failed: status=%d calls=%d", response.Code, upstreamCalls)
					}
				} else if upstreamCalls != 0 || response.Code < http.StatusBadRequest {
					t.Fatalf("multipart hook bypassed restriction: status=%d calls=%d", response.Code, upstreamCalls)
				} else if strings.HasPrefix(mode, "handle") && (response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "multipart_transcription_hook_unsupported") || strings.Contains(response.Body.String(), "unverified hook transcript")) {
					t.Fatalf("unsupported multipart handler lost its structured failure: status=%d body=%s", response.Code, response.Body)
				}
			})
		}
	}
}
