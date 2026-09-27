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

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func doMediaMultipartReviewRequest(t *testing.T, server *Server, key, path string, fields [][2]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range fields {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func TestMediaMultipartRepeatedControlFieldsCannotBypassHookMode(t *testing.T) {
	for _, control := range []string{"model", "stream"} {
		t.Run(control, func(t *testing.T) {
			calls, hookCalls := 0, 0
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = r.MultipartForm.RemoveAll() }()
				t.Logf("upstream stream=%q model=%q", r.FormValue("stream"), r.FormValue("model"))
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: fixture\n\n")
			}, "audio")
			registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "stream-policy", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				hookCalls++
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
			})
			fields := [][2]string{{"model", "public-media"}, {"stream", "true"}}
			value := "true"
			if control == "model" {
				value = "public-media"
			}
			fields = append(fields, [2]string{control, value})
			response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", fields)
			if response.Code != http.StatusBadRequest || calls != 0 || hookCalls != 0 {
				t.Fatalf("ambiguous controls escaped validation: status=%d upstream=%d hooks=%d body=%s", response.Code, calls, hookCalls, response.Body)
			}
		})
	}
}

func TestMediaMultipartPoliciesInspectEveryRepeatedTextField(t *testing.T) {
	for _, name := range []string{"prompt", "negative_prompt", "text"} {
		for _, action := range []string{guardrails.ActionBlock, guardrails.ActionMask} {
			t.Run(name+"/"+action, func(t *testing.T) {
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						return
					}
					defer func() { _ = r.MultipartForm.RemoveAll() }()
					if values := r.MultipartForm.Value[name]; len(values) != 2 || values[0] != "safe text" || values[1] != "contact [REDACTED]" {
						t.Errorf("unmasked repeated field: %s=%v", name, values)
					}
					if got := strings.Join(r.MultipartForm.Value["timestamp_granularities[]"], ","); got != "word,segment" {
						t.Errorf("repeated non-text options changed: %s", got)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"data":[]}`)
				}, "image")
				if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
					Name:           "Protect repeated media text",
					DetectionItems: []guardrails.DetectionItem{{Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: action, Config: map[string]any{"data_types": []string{"email"}}}},
					Bindings:       []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
				}); err != nil {
					t.Fatal(err)
				}
				fields := [][2]string{{"model", "public-media"}, {name, "safe text"}, {name, "contact demo@example.com"}, {"timestamp_granularities[]", "word"}, {"timestamp_granularities[]", "segment"}}
				response := doMediaMultipartReviewRequest(t, server, key, "/v1/images/edits", fields)
				if action == guardrails.ActionBlock {
					if response.Code != http.StatusForbidden || calls != 0 || !strings.Contains(response.Body.String(), "guardrail_blocked") {
						t.Fatalf("repeated text bypassed policy: status=%d upstream=%d body=%s", response.Code, calls, response.Body)
					}
				} else if response.Code != http.StatusOK || calls != 1 {
					t.Fatalf("masked request failed: status=%d upstream=%d body=%s", response.Code, calls, response.Body)
				}
			})
		}
	}
}

func TestMediaRequestRejectsAmbiguousStreamTypes(t *testing.T) {
	calls := 0
	server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { calls++ }, "audio")
	for _, raw := range []string{`"true"`, `1`, `null`, `[true,true]`, `{}`} {
		response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", json.RawMessage(`{"model":"public-media","stream":`+raw+`}`), key)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_media_request") {
			t.Errorf("invalid JSON stream accepted: %s, status=%d body=%s", raw, response.Code, response.Body)
		}
	}
	for _, value := range []string{"", "sometimes", `["true","true"]`} {
		response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}, {"stream", value}})
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_media_request") {
			t.Errorf("invalid multipart stream accepted: %q, status=%d body=%s", value, response.Code, response.Body)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid stream modes reached upstream: %d", calls)
	}
}

func TestMediaMultipartHookCannotIntroduceAmbiguousStreamMode(t *testing.T) {
	calls := 0
	server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { calls++ }, "audio")
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "test.media-multipart-invariant", HookID: "ambiguous-stream", Stage: pluginmeta.StagePrivacyPre,
		Priority: 1000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawRequestBodyPatch(t, map[string]any{"model": "public-media", "stream": []string{"true", "true"}}), nil
	})); err != nil {
		t.Fatal(err)
	}
	response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}, {"stream", "false"}})
	if response.Code != http.StatusBadGateway || calls != 0 || !strings.Contains(response.Body.String(), "gateway_hook_patch_invalid") {
		t.Fatalf("hook bypassed stream invariant: status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
}
