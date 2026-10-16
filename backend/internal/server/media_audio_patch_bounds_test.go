package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTranscriptionRequestHooksEnforceMultipartBounds(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		for _, stage := range []pluginmeta.GatewayHookStage{
			pluginmeta.StageDecodeNormalize, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre,
			pluginmeta.StageContextOptimize, pluginmeta.StageRequestTransform,
		} {
			for _, mode := range []string{"field", "parts", "total", "valid repeated fields"} {
				t.Run(path+"/"+string(stage)+"/"+mode, func(t *testing.T) {
					calls := 0
					app, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
						calls++
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							return
						}
						defer func() { _ = r.MultipartForm.RemoveAll() }()
						if strings.Join(r.MultipartForm.Value["timestamp_granularities[]"], ",") != "word,segment" {
							t.Error("valid repeated fields changed")
						}
						writeJSON(w, http.StatusOK, map[string]string{"text": "fixture"})
					}, "audio")
					if mode == "total" {
						app.config.MaxMultimodalRequestBytes = 1024
					}
					dataClass := pluginmeta.DataRequestBody
					if stage == pluginmeta.StageRequestTransform {
						dataClass = pluginmeta.DataProviderRequest
					}
					hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.audio-patch-bounds", HookID: "patch", Stage: stage, Priority: 2000,
						Reads: []pluginmeta.GatewayDataClass{dataClass}, Writes: []pluginmeta.GatewayDataClass{dataClass}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
					if err := app.gatewayChain.RegisterHook(hook); err != nil {
						t.Fatal(err)
					}
					if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						var fields map[string]any
						if err := json.Unmarshal(input.Data[dataClass], &fields); err != nil {
							t.Fatal(err)
						}
						switch mode {
						case "field":
							fields["prompt"] = strings.Repeat("x", maxImageTextFieldBytes+1)
						case "parts":
							fields["timestamp_granularities[]"] = strings.Fields(strings.Repeat("word ", 127))
						case "total":
							fields["prompt"] = strings.Repeat("x", 1024)
						default:
							fields["timestamp_granularities[]"] = []string{"word", "segment"}
						}
						patch, err := json.Marshal(fields)
						if err != nil {
							t.Fatal(err)
						}
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{dataClass: {Value: patch}}}, nil
					})); err != nil {
						t.Fatal(err)
					}
					response := doMediaMultipartReviewRequest(t, app, key, path, [][2]string{{"model", "public-media"}})
					if mode == "valid repeated fields" {
						if response.Code != http.StatusOK || calls != 1 {
							t.Fatalf("valid multipart patch rejected: status=%d calls=%d body=%s", response.Code, calls, response.Body)
						}
					} else if response.Code != http.StatusBadGateway || calls != 0 || !strings.Contains(response.Body.String(), "gateway_hook_patch_invalid") {
						t.Fatalf("invalid multipart patch forwarded: status=%d calls=%d body=%s", response.Code, calls, response.Body)
					}
				})
			}
		}
	}
}

func TestTranscriptionMultipartPatchBoundaryValues(t *testing.T) {
	request := mediaRequest{Fields: map[string]json.RawMessage{"model": json.RawMessage(`"public-media"`)}, Multipart: true, Path: "/v1/audio/transcriptions",
		Files: []mediaFile{transcriptionFixtureFile("file", "fixture.wav", "audio/wav", transcriptionWAVFixture("fixture"))}}
	unchanged := request
	if err := unchanged.applyPatch(json.RawMessage(`{"model":"public-media"}`)); err != nil {
		t.Fatalf("unchanged multipart patch rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		values any
		valid  bool
	}{
		{"field at limit", strings.Repeat("x", maxImageTextFieldBytes), true},
		{"field over limit", strings.Repeat("x", maxImageTextFieldBytes+1), false},
		{"repeated field over limit", []string{"word", strings.Repeat("x", maxImageTextFieldBytes+1)}, false},
		{"parts at limit including file and model", strings.Fields(strings.Repeat("word ", 126)), true},
		{"parts over limit including file and model", strings.Fields(strings.Repeat("word ", 127)), false},
		{"default aggregate overflow", strings.Fields(strings.Repeat(strings.Repeat("x", maxImageTextFieldBytes)+" ", 32)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := request
			patch, err := json.Marshal(map[string]any{"model": "public-media", "prompt": tc.values})
			if err != nil {
				t.Fatal(err)
			}
			if err := candidate.applyPatch(patch); (err == nil) != tc.valid {
				t.Fatalf("patch acceptance=%t, want %t: %v", err == nil, tc.valid, err)
			}
		})
	}
	patch := json.RawMessage(`{"model":"public-media","prompt":"patched prompt"}`)
	candidate := request
	if err := candidate.applyPatch(patch); err != nil {
		t.Fatal(err)
	}
	body, _, err := candidate.encode("public-media")
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int64{-1, 0, 1} {
		candidate := request
		candidate.maxBytes = int64(len(body)) + delta
		if err := candidate.applyPatch(patch); (err == nil) != (delta >= 0) {
			t.Fatalf("configured wire size boundary delta=%d: %v", delta, err)
		}
		if delta == 0 {
			if _, _, err := candidate.encode("longer-upstream-model"); err == nil {
				t.Fatal("upstream model replacement exceeded the configured wire size")
			}
		}
	}
}

func TestTranscriptionPatchBoundsPreserveUnrelatedMedia(t *testing.T) {
	for _, tc := range []struct {
		path      string
		multipart bool
	}{
		{"/v1/audio/transcriptions", false},
		{"/v1/audio/speech", true},
		{"/v1/images/variations", true},
	} {
		request := mediaRequest{Fields: map[string]json.RawMessage{"model": json.RawMessage(`"public-media"`)}, Path: tc.path, Multipart: tc.multipart, maxBytes: 1}
		patch := json.RawMessage(`{"model":"public-media","prompt":"existing extension"}`)
		if err := request.applyPatch(patch); err != nil {
			t.Fatalf("unrelated media patch rejected: %s: %v", tc.path, err)
		}
		if _, _, err := request.encode("vendor-media"); err != nil {
			t.Fatalf("unrelated media encoding changed: %s: %v", tc.path, err)
		}
	}
}
