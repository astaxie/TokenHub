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

func TestMediaResponsesAndChatDoNotResubmitAmbiguousGenerations(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		for _, mode := range []string{"server error", "invalid JSON", "stream open error", "upstream timeout", "rate limited", "authentication", "text model"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				calls := 0
				modality := "video"
				if mode == "text model" {
					modality = "chat"
				}
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					switch mode {
					case "invalid JSON":
						_, _ = io.WriteString(w, `{"id":`)
					case "rate limited":
						w.WriteHeader(http.StatusTooManyRequests)
					case "authentication":
						w.WriteHeader(http.StatusUnauthorized)
					case "upstream timeout":
						w.WriteHeader(http.StatusRequestTimeout)
					default:
						w.WriteHeader(http.StatusInternalServerError)
					}
				}, modality)
				store.AddRoute(ModelRoute{ID: "fallback-media-route", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
				payload := map[string]any{"model": "public-media", "stream": mode == "stream open error"}
				if path == "/v1/responses" {
					payload["input"] = "a landscape"
				} else {
					payload["messages"] = []any{map[string]any{"role": "user", "content": "a landscape"}}
				}
				response := doJSON(t, server.Handler(), http.MethodPost, path, payload, key)
				wantCalls := 1
				if mode == "rate limited" || mode == "authentication" || mode == "text model" {
					wantCalls = 2
				}
				if response.Code == http.StatusOK || calls != wantCalls {
					t.Fatalf("status=%d calls=%d, want failure after %d attempts: %s", response.Code, calls, wantCalls, response.Body)
				}
			})
		}
	}
}

func TestMediaMultipartStreamModeSurvivesHookValidation(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range [][2]string{{"model", "public-media"}, {"stream", "true"}} {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	request, err := decodeMediaRequest(httptest.NewRecorder(), req, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !request.stream() {
		t.Fatal("multipart stream=true was admitted as a non-streaming request")
	}
	if err := request.applyPatch(json.RawMessage(`{"model":"public-media","stream":"false"}`)); err == nil {
		t.Fatal("a hook changed the admitted multipart stream mode")
	}
}

func TestMediaPostHooksValidateBinaryEnvelope(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		for _, patch := range []string{`{}`, `null`, `{"data_base64":null}`, `{"data_base64":42}`, `{"data_base64":"!invalid"}`, `{"data_base64":"YXVkaW8="}`} {
			t.Run(string(stage)+"/"+patch, func(t *testing.T) {
				server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "audio/mpeg")
					_, _ = io.WriteString(w, "audio")
				}, "audio")
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-post", HookID: "patch", Stage: stage, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					return rawProviderResponsePatch(t, json.RawMessage(patch)), nil
				})); err != nil {
					t.Fatal(err)
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "input": "hello"}, key)
				if patch == `{"data_base64":"YXVkaW8="}` {
					if response.Code != http.StatusOK || response.Body != "audio" {
						t.Fatalf("valid audio patch changed: %d %s", response.Code, response.Body)
					}
				} else if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "gateway_hook_response_invalid") {
					t.Fatalf("invalid audio patch must fail closed: %d %s", response.Code, response.Body)
				}
			})
		}
	}
}
