package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"tokenhub/backend/internal/guardrails"
)

func TestMediaResponsesMultishotPromptsRespectTextPolicies(t *testing.T) {
	for _, mode := range []string{"synchronous", "background"} {
		for _, action := range []string{guardrails.ActionBlock, guardrails.ActionMask} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				var calls atomic.Int32
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var request struct {
						MultiShot   bool   `json:"multi_shot"`
						ShotType    string `json:"shot_type"`
						MultiPrompt []struct {
							Index    int    `json:"index"`
							Prompt   string `json:"prompt"`
							Duration string `json:"duration"`
							ImageURL string `json:"image_url"`
							Asset    struct {
								FileID json.Number `json:"file_id"`
								Prompt string      `json:"prompt"`
							} `json:"asset"`
						} `json:"multi_prompt"`
					}
					decodeFixtureRequest(t, r.Body, &request)
					if !request.MultiShot || request.ShotType != "customize" || len(request.MultiPrompt) != 2 {
						t.Errorf("multishot structure changed: %+v", request)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					first, second := request.MultiPrompt[0], request.MultiPrompt[1]
					if first.Index != 1 || first.Prompt != "A quiet landscape" || first.Duration != "2" || second.Index != 2 || second.Prompt != "contact [REDACTED]" || second.Duration != "3" {
						t.Errorf("multishot text policy or metadata preservation failed: %+v", request.MultiPrompt)
					}
					if second.ImageURL != "https://example.com/demo@example.com.png" || second.Asset.FileID != "9007199254740993" || second.Asset.Prompt != "asset@example.com" {
						t.Errorf("opaque multishot asset changed: %+v", second)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"multishot_fixture"}`)
				}, "video")
				if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
					Name:           "Protect multishot prompts",
					DetectionItems: []guardrails.DetectionItem{{Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: action, Config: map[string]any{"data_types": []string{"email"}}}},
					Bindings:       []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
				}); err != nil {
					t.Fatal(err)
				}
				request := map[string]any{
					"model": "public-media", "input": "Ignored for custom shots", "multi_shot": true, "shot_type": "customize", "background": mode == "background",
					"multi_prompt": []map[string]any{
						{"index": 1, "prompt": "A quiet landscape", "duration": "2"},
						{"index": 2, "prompt": "contact demo@example.com", "duration": "3", "image_url": "https://example.com/demo@example.com.png", "asset": map[string]any{"file_id": json.Number("9007199254740993"), "prompt": "asset@example.com"}},
					},
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", request, key)
				if mode == "background" {
					if response.Code != http.StatusOK {
						t.Fatalf("background submission failed: %d %s", response.Code, response.Body)
					}
					var submitted struct {
						ID string `json:"id"`
					}
					if err := json.Unmarshal([]byte(response.Body), &submitted); err != nil || submitted.ID == "" {
						t.Fatalf("background submission has no job ID: %s, error: %v", response.Body, err)
					}
					status := "completed"
					if action == guardrails.ActionBlock {
						status = "failed"
					}
					result := waitForResponseJobStatus(t, server.Handler(), key, submitted.ID, status)
					if action == guardrails.ActionBlock {
						failure, _ := result["error"].(map[string]any)
						if failure["code"] != "guardrail_blocked" {
							t.Fatalf("background prompt failed for another reason: %v", result)
						}
					}
				} else if action == guardrails.ActionBlock {
					if response.Code != http.StatusForbidden || !strings.Contains(response.Body, "guardrail_blocked") {
						t.Fatalf("multishot prompt bypassed policy: %d %s", response.Code, response.Body)
					}
				} else if response.Code != http.StatusOK {
					t.Fatalf("masked multishot request failed: %d %s", response.Code, response.Body)
				}
				wantCalls := int32(1)
				if action == guardrails.ActionBlock {
					wantCalls = 0
				}
				if got := calls.Load(); got != wantCalls {
					t.Fatalf("upstream calls = %d, want %d", got, wantCalls)
				}
			})
		}
	}
}
