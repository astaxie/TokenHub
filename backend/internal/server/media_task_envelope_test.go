package server

import (
	"io"
	"net/http"
	"testing"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaTaskEnvelopeRejectsMalformedOrAmbiguousTasks(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"task_id":"upstream_private_task"},"invalid"]}`,
		`{"data":[null,{"task_id":"upstream_private_task"}]}`,
		`{"task_id":"upstream_root_task","data":[{"task_id":"upstream_private_task"}]}`,
		`{"task_id":"upstream_root_task","data":{"id":"upstream_private_task","object":"generation.task"}}`,
		`{"data":{"object":"generation.task","id":"upstream_private_task","task_id":"other"}}`,
		`{"data":{"object":"generation.task"}}`,
		`{"data":{"object":"generation.task","id":null}}`,
		`{"data":[{"task_id":"valid_before_invalid"},{"object":"generation.task","id":{}}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}, "video")
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "public-media"}, key)
			var count int64
			if err := store.db.Table("media_task_bindings").Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadGateway || count != 0 || calls != 1 {
				t.Fatalf("invalid receipt escaped or persisted: status=%d bindings=%d calls=%d body=%s", response.Code, count, calls, response.Body)
			}
		})
	}
}

func TestMediaTaskEnvelopePostHookCannotForgeNestedHandle(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		t.Run(string(stage), func(t *testing.T) {
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"task_id":"upstream_root_task"}`)
			}, "video")
			reviewMediaTaskResponseHook(t, server, stage, "videos/generations", func(payload map[string]any) {
				payload["data"] = []any{map[string]any{"task_id": "task_forged_by_hook"}}
			})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "public-media"}, key)
			if response.Code != http.StatusBadGateway {
				t.Fatalf("accepted forged task identity: status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestMediaTaskEnvelopeRejectsMixedPollIdentity(t *testing.T) {
	for _, body := range []string{
		`{"task_id":"another_private_task","data":{"id":"original_task","status":"completed"}}`,
		`{"id":"original_task","data":{"id":"original_task","status":"completed"}}`,
		`{"data":{"id":"original_task","task_id":"other_task"}}`,
	} {
		response := mediaResponse{ContentType: "application/json", Body: []byte(body)}
		if err := rewriteMediaTaskResult(&response, mediaTaskBinding{ID: "task_public"}, "original_task"); err == nil {
			t.Fatalf("accepted ambiguous task identity: %s", response.Body)
		}
	}
}
