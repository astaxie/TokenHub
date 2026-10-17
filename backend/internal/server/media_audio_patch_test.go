package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTranscriptionRequestHooksPreserveMultipartFileInvariant(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		for _, stage := range []pluginmeta.GatewayHookStage{
			pluginmeta.StageDecodeNormalize, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre,
			pluginmeta.StageContextOptimize, pluginmeta.StageRequestTransform,
		} {
			for _, mode := range []string{"reject file field", "preserve upload"} {
				t.Run(path+"/"+string(stage)+"/"+mode, func(t *testing.T) {
					calls := 0
					audio := transcriptionWAVFixture("original uploaded audio")
					app, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
						calls++
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							return
						}
						defer func() { _ = r.MultipartForm.RemoveAll() }()
						if len(r.MultipartForm.Value["file"]) != 0 || len(r.MultipartForm.File["file"]) != 1 {
							t.Error("request hook introduced a conflicting file field")
							return
						}
						file, header, err := r.FormFile("file")
						if err != nil {
							t.Error(err)
							return
						}
						defer func() { _ = file.Close() }()
						data, err := io.ReadAll(file)
						if err != nil || header.Filename != "fixture.wav" || !bytes.Equal(data, audio) || r.FormValue("prompt") != "patched prompt" || r.FormValue("model") != "vendor-media" {
							t.Errorf("valid text patch changed the upload: filename=%q prompt=%q model=%q error=%v", header.Filename, r.FormValue("prompt"), r.FormValue("model"), err)
							return
						}
						writeJSON(w, http.StatusOK, map[string]string{"text": "fixture transcript"})
					}, "audio")
					dataClass := pluginmeta.DataRequestBody
					if stage == pluginmeta.StageRequestTransform {
						dataClass = pluginmeta.DataProviderRequest
					}
					hook := pluginmeta.GatewayHookDescriptor{
						PluginID: "test.audio-request-patch", HookID: "patch", Stage: stage, Priority: 2000,
						Reads: []pluginmeta.GatewayDataClass{dataClass}, Writes: []pluginmeta.GatewayDataClass{dataClass},
						FailurePolicy: pluginmeta.FailurePolicyFailClosed,
						Scope:         pluginmeta.GatewayHookScope{RouteProtocols: []string{strings.TrimPrefix(path, "/v1/")}},
					}
					if err := app.gatewayChain.RegisterHook(hook); err != nil {
						t.Fatal(err)
					}
					if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						var fields map[string]string
						if err := json.Unmarshal(input.Data[dataClass], &fields); err != nil {
							t.Fatal(err)
						}
						fields["prompt"] = "patched prompt"
						if mode == "reject file field" {
							fields["file"] = "conflicting-file-field"
						}
						patch, err := json.Marshal(fields)
						if err != nil {
							t.Fatal(err)
						}
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{dataClass: {Value: patch}}}, nil
					})); err != nil {
						t.Fatal(err)
					}
					request := transcriptionFixtureRequest(t, key, [][2]string{{"model", "public-media"}, {"prompt", "original prompt"}}, []mediaFile{transcriptionFixtureFile("file", "fixture.wav", "audio/wav", audio)})
					request.URL.Path = path
					response := httptest.NewRecorder()
					app.Handler().ServeHTTP(response, request)
					if mode == "reject file field" {
						if response.Code != http.StatusBadGateway || calls != 0 || !strings.Contains(response.Body.String(), "gateway_hook_patch_invalid") || strings.Contains(response.Body.String(), "conflicting-file-field") {
							t.Fatalf("invalid multipart patch reached upstream: status=%d calls=%d body=%s", response.Code, calls, response.Body)
						}
					} else if response.Code != http.StatusOK || calls != 1 {
						t.Fatalf("valid multipart patch failed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
					}
				})
			}
		}
	}
}
