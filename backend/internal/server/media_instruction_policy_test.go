package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaMultipartInstructionsApplyTextPolicies(t *testing.T) {
	for _, action := range []string{guardrails.ActionBlock, guardrails.ActionMask} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = r.MultipartForm.RemoveAll() }()
				values := r.MultipartForm.Value["instructions"]
				if len(values) != 2 || values[0] != "Speak softly" || values[1] != "Contact [REDACTED]" {
					t.Errorf("instructions escaped masking: %v", values)
				}
				w.Header().Set("Content-Type", "audio/mpeg")
				_, _ = io.WriteString(w, "fixture audio")
			}, "audio")
			if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
				Name: "Protect media instructions", Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
				DetectionItems: []guardrails.DetectionItem{{Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: action, Config: map[string]any{"data_types": []string{"email"}}}},
			}); err != nil {
				t.Fatal(err)
			}
			response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/speech", [][2]string{{"model", "public-media"}, {"input", "hello"}, {"instructions", "Speak softly"}, {"instructions", "Contact demo@example.com"}})
			if action == guardrails.ActionBlock {
				if response.Code != http.StatusForbidden || calls != 0 || !strings.Contains(response.Body.String(), "guardrail_blocked") {
					t.Fatalf("instructions bypassed blocking: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
			} else if response.Code != http.StatusOK || calls != 1 {
				t.Fatalf("masked request failed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestMediaInstructionGuardrailHookSeesEveryTextValueOnce(t *testing.T) {
	server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("denied request reached provider") }, "audio")
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-instructions", HookID: "text", Stage: pluginmeta.StageGuardrailPre, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataNormalizedText}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var segments []pluginmeta.TextSegment
		if err := json.Unmarshal(input.Data[pluginmeta.DataNormalizedText], &segments); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"input.0": "Hello", "input.1": "World", "instructions.0": "Speak softly", "instructions.1": "Speak slowly"}
		if len(segments) != len(want) {
			t.Errorf("text count=%d want=%d: %+v", len(segments), len(want), segments)
		}
		for _, segment := range segments {
			if want[segment.ID] != segment.Text {
				t.Errorf("unexpected text segment: %+v", segment)
			}
			delete(want, segment.ID)
		}
		if len(want) != 0 {
			t.Errorf("missing text segments: %v", want)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
	})); err != nil {
		t.Fatal(err)
	}
	response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/speech", [][2]string{{"model", "public-media"}, {"input", "Hello"}, {"input", "World"}, {"instructions", "Speak softly"}, {"instructions", "Speak slowly"}})
	if response.Code != http.StatusForbidden {
		t.Fatalf("guardrail response=%d %s", response.Code, response.Body)
	}
}
