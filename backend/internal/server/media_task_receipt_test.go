package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaTaskVersionedImageReceiptsBindAndPoll(t *testing.T) {
	for _, shape := range []string{"nested", "array", "root", "without polling URL", "dual identifiers"} {
		t.Run(shape, func(t *testing.T) {
			const upstreamID = "task_01KZQE5CM0Y3KZ6M1N1BK619MX"
			gets := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					gets++
					if r.URL.Path != "/v1/tasks/"+upstreamID {
						t.Errorf("upstream poll: %s", r.URL)
					}
					_, _ = io.WriteString(w, `{"code":200,"data":{"id":"`+upstreamID+`","status":"completed"}}`)
					return
				}
				if r.Header.Get("X-APIMart-Response-Version") != "2026-07-27" {
					t.Error("missing configured response version")
				}
				item := map[string]any{"id": upstreamID, "object": "generation.task", "type": "image", "status": "pending", "progress": 0, "poll_url": "https://provider.example/v1/tasks/" + upstreamID}
				if shape == "without polling URL" {
					delete(item, "poll_url")
				}
				if shape == "dual identifiers" {
					item["task_id"] = upstreamID
				}
				payload := map[string]any{"code": 202, "request_id": "fixture", "data": item}
				if shape == "root" {
					payload = item
				} else if shape == "array" {
					payload["data"] = []any{item}
				}
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(payload)
			}, "image")
			provider, _ := store.GetProvider("media-provider")
			provider.Headers = map[string]string{"X-APIMart-Response-Version": "2026-07-27"}
			if _, err := store.UpdateProvider(provider.ID, provider); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": "public-media", "prompt": "an apple"}, key)
			if response.Code != http.StatusAccepted || strings.Contains(response.Body, upstreamID) {
				t.Fatalf("versioned receipt was not bound: %d %s", response.Code, response.Body)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
				t.Fatal(err)
			}
			envelope, err := parseMediaTaskEnvelope(payload, false)
			if err != nil || len(envelope.tasks) != 1 {
				t.Fatalf("receipt: %s (%v)", response.Body, err)
			}
			id, err := envelope.tasks[0].upstreamID()
			if err != nil || !strings.HasPrefix(id, "task_") {
				t.Fatalf("bound handle: %q (%v)", id, err)
			}
			binding, originalID, found, err := store.LoadMediaTaskBinding(context.Background(), id)
			if err != nil || !found || originalID != upstreamID || binding.ID != id {
				t.Fatalf("bound task: %+v %q %t %v", binding, originalID, found, err)
			}
			var pollURL string
			_ = json.Unmarshal(envelope.tasks[0].fields["poll_url"], &pollURL)
			if shape != "without polling URL" && pollURL != "/v1/tasks/"+id || shape == "without polling URL" && pollURL != "" {
				t.Fatalf("polling URL: %q", pollURL)
			}
			if pollURL == "" {
				pollURL = "/v1/tasks/" + id
			}
			project, _, err := store.ValidateAPIKey(key, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			_, otherKey, err := store.CreateAPIKey(project.ID, APIKey{Name: "Other image key", Allowed: []string{"public-media"}, Status: StatusActive}, "thk_other_receipt")
			if err != nil {
				t.Fatal(err)
			}
			foreign := doJSON(t, server.Handler(), http.MethodGet, pollURL, nil, otherKey)
			if foreign.Code != http.StatusNotFound || gets != 0 {
				t.Fatalf("foreign key polled task: %d %s; calls=%d", foreign.Code, foreign.Body, gets)
			}
			poll := doJSON(t, server.Handler(), http.MethodGet, pollURL, nil, key)
			if poll.Code != http.StatusOK || gets != 1 || !strings.Contains(poll.Body, id) {
				t.Fatalf("poll: %d %s; calls=%d", poll.Code, poll.Body, gets)
			}
		})
	}
}

func TestMediaTaskVersionedReceiptHooksCannotChangeIdentity(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		for _, field := range []string{"id", "poll_url", "object"} {
			t.Run(string(stage)+"/"+field, func(t *testing.T) {
				server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusAccepted)
					_, _ = io.WriteString(w, `{"data":{"id":"provider-task","object":"generation.task","poll_url":"/v1/tasks/provider-task"}}`)
				}, "image")
				reviewMediaTaskResponseHook(t, server, stage, "images/generations", func(payload map[string]any) { payload["data"].(map[string]any)[field] = "replacement" })
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": "public-media"}, key)
				if response.Code != http.StatusBadGateway {
					t.Fatalf("receipt mutation accepted: %d %s", response.Code, response.Body)
				}
			})
		}
	}
}

func TestMediaTaskBindingPreservesSynchronousImageIdentifiers(t *testing.T) {
	for _, body := range []string{
		`{"id":"image-root","data":[{"id":"asset-id","url":"https://example.com/image.png"}]}`,
		`{"data":[{"id":"asset-id","object":"image","url":"https://example.com/image.png"},"vendor-extension"]}`,
		`{"data":{"id":"asset-id","object":"image","poll_url":"https://example.com/asset"}}`,
	} {
		server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}, "image")
		response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": "public-media"}, key)
		var count int64
		if err := store.db.Table("media_task_bindings").Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || response.Body != body || count != 0 {
			t.Fatalf("sync response changed: %d %s; bindings=%d", response.Code, response.Body, count)
		}
	}
}
