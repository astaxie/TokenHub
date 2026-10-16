package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tokenhub/backend/internal/guardrails"
)

func TestTranscriptionGuardrailDenialsPreserveClassification(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		t.Run(path, func(t *testing.T) {
			const private = "private-transcription-guardrail-marker"
			app, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) {
				t.Error("blocked request reached upstream")
			}, "audio")
			if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
				Name: "Block sensitive marker",
				DetectionItems: []guardrails.DetectionItem{{
					Name: "Marker", DetectorType: guardrails.DetectorPattern, Action: guardrails.ActionBlock,
					Config: map[string]any{"keywords": []string{private}},
				}},
				Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
			}); err != nil {
				t.Fatal(err)
			}
			traces := &recordingTraceEmitter{}
			app.traceEmitter = traces
			app.config.TracingCapturePayloads = true
			response := doMediaMultipartReviewRequest(t, app, key, path, [][2]string{{"model", "public-media"}, {"prompt", private}})
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"guardrail_blocked"`) {
				t.Fatalf("content policy rejection lost its classification: status=%d body=%s", response.Code, response.Body)
			}
			logs := store.ListRequestLogs()
			if len(logs) != 1 || logs[0].ErrorCode != "guardrail_blocked" {
				t.Fatalf("request log lost the content policy classification: %+v", logs)
			}
			assertTranscriptionFailurePrivate(t, store, response, traces.take(), private)
		})
	}
}

func TestSensitiveGuardrailErrorsPreserveCodesWithoutPayloads(t *testing.T) {
	for code, status := range map[string]int{
		"guardrail_blocked":                    http.StatusForbidden,
		"guardrail_policy_load_failed":         http.StatusInternalServerError,
		"guardrail_evaluation_failed":          http.StatusInternalServerError,
		"guardrail_evaluation_budget_exceeded": http.StatusServiceUnavailable,
	} {
		for _, protocol := range []string{"audio/transcriptions", "audio/translations", "rerank"} {
			t.Run(protocol+"/"+code, func(t *testing.T) {
				const private = "private-guardrail-error-payload"
				original := &HTTPError{Status: status, Code: code, Message: private, Details: map[string]string{"text": private}}
				call := CallContext{RouteProtocol: protocol}
				if protocol == "rerank" {
					call.Model.Modality = "rerank"
				}
				err := sanitizeSensitiveGatewayHookError(call, original)
				var safe *HTTPError
				if !errors.As(err, &safe) || safe.Status != status || safe.Code != code {
					t.Fatalf("guardrail failure classification changed: %v", err)
				}
				response := httptest.NewRecorder()
				writeError(response, httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil), err)
				if strings.Contains(response.Body.String(), private) || safe.Details != nil {
					t.Fatal("guardrail classification retained private error content")
				}
			})
		}
	}
}
