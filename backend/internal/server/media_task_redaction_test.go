package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaTaskRedactionPreservesProtocolAndRouting(t *testing.T) {
	for _, secret := range []string{"task", "id", "data", "status", "poll_url", "generation.task", "url", "completed"} {
		for _, stage := range []pluginmeta.GatewayHookStage{"", pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
			t.Run(secret+"/"+string(stage), func(t *testing.T) {
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == http.MethodPost {
						_, _ = io.WriteString(w, `{"data":[{"task_id":"upstream123"}]}`)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
						"id": "upstream123", "task_id": "upstream123", "object": "generation.task",
						"poll_url": "https://provider.example/tasks/upstream123", "status": "completed",
						"result": map[string]any{
							"id": "[" + secret + "]", "echo-" + secret: "[" + secret + "]",
							"sequence": json.Number("9007199254740993"),
							"videos":   []any{map[string]any{"url": "https://example.com/result.mp4"}},
						},
						"error": map[string]any{"message": "Provider rejected [" + secret + "]"},
					}})
				}, "video")
				provider, _ := store.GetProvider("media-provider")
				provider.Headers = map[string]string{"X-Tenant": secret}
				provider.SensitiveHeaders = []string{"X-Tenant"}
				if _, err := store.UpdateProvider(provider.ID, provider); err != nil {
					t.Fatal(err)
				}
				id := submitMediaTask(t, server, key, "/v1/videos/generations")
				if stage != "" {
					reviewMediaTaskResponseHook(t, server, stage, "tasks", func(payload map[string]any) {
						item := payload["data"].(map[string]any)
						message := item["error"].(map[string]any)["message"].(string)
						if strings.Contains(message, "["+secret+"]") || item["id"] != id || item["poll_url"] != "/v1/tasks/"+id {
							t.Fatalf("hook received unsafe or corrupted task: %v", payload)
						}
						item["error"] = map[string]any{"message": "Hook injected [" + secret + "]"}
					})
				}
				response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id, nil, key)
				if response.Code != http.StatusOK {
					t.Fatalf("poll incorrectly failed: %d %s", response.Code, response.Body)
				}
				var payload struct {
					Data struct {
						ID, Object, Status string
						TaskID             string `json:"task_id"`
						PollURL            string `json:"poll_url"`
						Result             struct {
							ID       string
							Sequence json.Number
							Videos   []struct{ URL string }
						}
						Error struct{ Message string }
					}
				}
				if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Data.ID != id || payload.Data.TaskID != id || payload.Data.Object != "generation.task" || payload.Data.PollURL != "/v1/tasks/"+id || payload.Data.Status != "completed" || len(payload.Data.Result.Videos) != 1 || payload.Data.Result.Videos[0].URL != "https://example.com/result.mp4" {
					t.Fatalf("task protocol changed: %s", response.Body)
				}
				if payload.Data.Result.ID != "["+providerHeaderMask+"]" || payload.Data.Result.Sequence.String() != "9007199254740993" || strings.Contains(response.Body, `"echo-`+secret+`"`) {
					t.Fatalf("extension redaction or numeric precision was lost: %s", response.Body)
				}
				if strings.Contains(payload.Data.Error.Message, "["+secret+"]") || payload.Data.Error.Message == "" {
					t.Fatalf("task error was not safely retained: %s", response.Body)
				}
			})
		}
	}
}
