package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTranscriptionNativeMasksEnforceMultipartBounds(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		for _, mode := range []string{"field overflow", "total overflow", "upstream model overflow", "field at limit", "total at limit"} {
			t.Run(path+"/"+mode, func(t *testing.T) {
				calls, hookCalls := 0, 0
				app, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if err := r.ParseMultipartForm(2 << 20); err != nil {
						t.Error(err)
						return
					}
					defer func() { _ = r.MultipartForm.RemoveAll() }()
					prompt := r.FormValue("prompt")
					if !strings.HasSuffix(prompt, " [REDACTED]") || len(prompt) > maxImageTextFieldBytes {
						t.Error("forwarded prompt is unmasked or over its field limit")
					}
					writeJSON(w, http.StatusOK, map[string]string{"text": "fixture"})
				}, "audio")
				createTranscriptionMaskPolicy(t, store)
				registerMediaProviderTestHook(t, app, pluginmeta.GatewayHookDescriptor{HookID: "observe-mask"}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					hookCalls++
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
				})
				prompt := strings.Repeat("x", maxImageTextFieldBytes-7) + " a@b.co"
				if mode == "field at limit" {
					prompt = strings.Repeat("x", maxImageTextFieldBytes-11) + " a@b.co"
				} else if strings.HasPrefix(mode, "total") || mode == "upstream model overflow" {
					prompt = strings.Repeat("x", 100) + " a@b.co"
				}
				body, contentType := transcriptionGuardrailMultipart(t, prompt, "")
				if strings.HasPrefix(mode, "total") || mode == "upstream model overflow" {
					app.config.MaxMultimodalRequestBytes = int64(len(body))
					if mode != "total overflow" {
						app.config.MaxMultimodalRequestBytes += 4 // Email masking expands six bytes to ten.
					}
				}
				if mode == "upstream model overflow" {
					if _, err := store.UpdateRoute("media-route", ModelRoute{ProviderModel: "longer-upstream-model"}); err != nil {
						t.Fatal(err)
					}
				}
				response := sendTranscriptionGuardrailMultipart(app, key, path, body, contentType)
				if strings.HasSuffix(mode, "at limit") {
					if response.Code != http.StatusOK || calls != 1 || hookCalls != 1 {
						t.Fatalf("valid masked request rejected: status=%d calls=%d hooks=%d body=%s", response.Code, calls, hookCalls, response.Body)
					}
				} else if response.Code != http.StatusRequestEntityTooLarge || calls != 0 || hookCalls != 0 || !strings.Contains(response.Body.String(), `"code":"request_too_large"`) {
					t.Fatalf("oversized native mask reached invocation: status=%d calls=%d hooks=%d body=%s", response.Code, calls, hookCalls, response.Body)
				}
			})
		}
	}
}

func TestTranscriptionNativeMaskPreservesUnmodifiedMultipart(t *testing.T) {
	calls := 0
	app, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		writeJSON(w, http.StatusOK, map[string]string{"text": "fixture"})
	}, "audio")
	createTranscriptionMaskPolicy(t, store)
	// The original short boundary fits exactly. Reencoding expands its overhead;
	// a policy that does not mask anything must preserve this accepted upload.
	body, contentType := transcriptionGuardrailMultipart(t, "unmatched prompt", "x")
	app.config.MaxMultimodalRequestBytes = int64(len(body))
	response := sendTranscriptionGuardrailMultipart(app, key, "/v1/audio/transcriptions", body, contentType)
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("unmodified upload changed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
}

func TestTranscriptionNativeMaskRechecksUpstreamModelSize(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		request := mediaRequest{Fields: map[string]json.RawMessage{"model": json.RawMessage(`"public-media"`), "prompt": json.RawMessage(`"[REDACTED]"`)}, Multipart: true, Path: path,
			Files: []mediaFile{transcriptionFixtureFile("file", "fixture.wav", "audio/wav", transcriptionWAVFixture("fixture"))}}
		body, _, err := request.encode(request.model())
		if err != nil {
			t.Fatal(err)
		}
		request.maxBytes = int64(len(body))
		if err := request.validateAudioGuardrailBounds(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := request.encode("longer-upstream-model"); err == nil || err.Error() != invalidAudioGuardrailBounds().Error() {
			t.Fatalf("native masking lost its bound or error classification after model substitution: %v", err)
		}
	}
}

func createTranscriptionMaskPolicy(t *testing.T, store *GormStore) {
	t.Helper()
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name: "Mask transcription emails",
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: guardrails.ActionMask,
			Config: map[string]any{"data_types": []string{"email"}},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
}

func transcriptionGuardrailMultipart(t *testing.T, prompt, boundary string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if boundary != "" {
		if err := writer.SetBoundary(boundary); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range [][2]string{{"model", "public-media"}, {"prompt", prompt}} {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	writeTranscriptionFixtureFile(t, writer, "file", "fixture.wav", "application/octet-stream", transcriptionWAVFixture("fixture audio"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func sendTranscriptionGuardrailMultipart(app *Server, key, path string, body []byte, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	return response
}
