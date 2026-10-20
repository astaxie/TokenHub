package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func submitMediaTask(t *testing.T, server *Server, key, path string) string {
	t.Helper()
	response := doJSON(t, server.Handler(), http.MethodPost, path, json.RawMessage(`{"model":"public-media","prompt":"a mountain","image_urls":["https://example.com/start.png"],"video_urls":["https://example.com/reference.mp4"],"audio_urls":["https://example.com/voice.mp3"],"seed":9007199254740993,"duration":5,"resolution":"720p","generate_audio":true}`), key)
	if response.Code != 200 {
		t.Fatalf("submission: %d %s", response.Code, response.Body)
	}
	var body struct {
		Data []struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil || len(body.Data) == 0 || !strings.HasPrefix(body.Data[0].TaskID, "task_") {
		t.Fatalf("missing opaque task ID: %s (%v)", response.Body, err)
	}
	return body.Data[0].TaskID
}

func TestMediaTasksPreserveMultimodalSubmissionAndPollResults(t *testing.T) {
	for _, path := range []string{"/v1/videos/generations", "/v1/images/generations"} {
		t.Run(path, func(t *testing.T) {
			gets := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer upstream-fixture-secret" {
					t.Error("provider credential missing")
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					if r.URL.Path != path {
						t.Errorf("submission path: %s", r.URL.Path)
					}
					var body map[string]json.RawMessage
					decodeFixtureRequest(t, r.Body, &body)
					for field, want := range map[string]string{"model": `"vendor-media"`, "seed": "9007199254740993", "generate_audio": "true", "audio_urls": `["https://example.com/voice.mp3"]`, "video_urls": `["https://example.com/reference.mp4"]`} {
						if string(body[field]) != want {
							t.Errorf("%s: %s", field, body[field])
						}
					}
					_, _ = io.WriteString(w, `{"code":200,"data":[{"status":"submitted","task_id":9007199254740993}],"vendor_id":9007199254740995,"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`)
					return
				}
				gets++
				if r.URL.Path != "/v1/tasks/9007199254740993" || r.URL.RawQuery != "language=ja" {
					t.Errorf("poll URL: %s", r.URL)
				}
				data, _ := io.ReadAll(r.Body)
				if len(data) != 0 {
					t.Errorf("GET body should be empty: %s", data)
				}
				_, _ = io.WriteString(w, `{"code":200,"data":{"id":9007199254740993,"status":"completed","progress":100,"cost":0.8,"credits_cost":2,"result":{"videos":[{"url":"https://example.com/result.mp4"}]},"extension":9007199254740995},"usage":{"total_tokens":100000}}`)
			}, "video")
			id := submitMediaTask(t, server, key, path)
			for range 2 {
				response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id+"?language=ja", nil, key)
				if response.Code != 200 || !strings.Contains(response.Body, `"id":"`+id+`"`) || !strings.Contains(response.Body, `"extension":9007199254740995`) || !strings.Contains(response.Body, `"cost":0.8`) {
					t.Fatalf("poll: %d %s", response.Code, response.Body)
				}
			}
			if gets != 2 {
				t.Fatalf("poll count: %d", gets)
			}
			var tokens int64
			for _, usage := range store.ListUsageRecords() {
				tokens += usage.TotalTokens
			}
			if tokens != 10 || len(store.ListRequestLogs()) != 3 {
				t.Fatalf("poll duplicated usage or missing logs: tokens=%d logs=%d", tokens, len(store.ListRequestLogs()))
			}
			var binding mediaTaskBinding
			if err := store.db.First(&binding, "id = ?", id).Error; err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(binding.UpstreamCiphertext, "enc:v1:") || strings.Contains(binding.UpstreamCiphertext, "9007199254740993") {
				t.Fatal("upstream task ID was stored in plaintext")
			}
			loaded, upstream, found, err := store.LoadMediaTaskBinding(context.Background(), id)
			if err != nil || !found || upstream != "9007199254740993" || loaded.ID != id {
				t.Fatalf("durable binding: %+v %q %v", loaded, upstream, err)
			}
		})
	}
}

func TestMediaTaskOwnershipExpiryQueryAndMethods(t *testing.T) {
	polls := 0
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			polls++
			_, _ = io.WriteString(w, `{"data":{"id":"provider-task","status":"pending"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"task_id":"provider-task","status":"submitted"}]}`)
	}, "video")
	id := submitMediaTask(t, server, key, "/v1/videos/generations")
	project, _, err := store.ValidateAPIKey(key, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	_, otherKey, err := store.CreateAPIKey(project.ID, APIKey{Name: "Other key", Allowed: []string{"public-media"}, Status: StatusActive}, "thk_other_media")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method, path, key string
		status                  int
	}{
		{"foreign key", http.MethodGet, "/v1/tasks/" + id, otherKey, 404},
		{"raw upstream ID", http.MethodGet, "/v1/tasks/provider-task", key, 404},
		{"unknown handle", http.MethodGet, "/v1/tasks/task_missing", key, 404},
		{"duplicate language", http.MethodGet, "/v1/tasks/" + id + "?language=en&language=zh", key, 400},
		{"unknown language", http.MethodGet, "/v1/tasks/" + id + "?language=xx", key, 400},
		{"arbitrary query", http.MethodGet, "/v1/tasks/" + id + "?url=https://example.com", key, 400},
		{"wrong method", http.MethodPost, "/v1/tasks/" + id, key, 405},
		{"head", http.MethodHead, "/v1/tasks/" + id, key, 405},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := doJSON(t, server.Handler(), test.method, test.path, nil, test.key)
			if response.Code != test.status {
				t.Fatalf("%d %s", response.Code, response.Body)
			}
		})
	}
	if polls != 0 {
		t.Fatalf("unauthorized upstream polls: %d", polls)
	}
	if err := store.db.Model(&mediaTaskBinding{}).Where("id = ?", id).Update("expires_at", time.Now().Add(-time.Second).Unix()).Error; err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id, nil, key)
	if response.Code != 404 || polls != 0 {
		t.Fatalf("expired task escaped: %d calls=%d", response.Code, polls)
	}
}

func TestMediaTaskAccountAndRoutePinning(t *testing.T) {
	for _, change := range []string{"credential", "endpoint", "model", "resource", "route disabled"} {
		t.Run(change, func(t *testing.T) {
			polls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					polls++
				}
				_, _ = io.WriteString(w, `{"data":[{"task_id":"upstream-task"}]}`)
			}, "video")
			id := submitMediaTask(t, server, key, "/v1/videos/generations")
			switch change {
			case "credential":
				provider, _ := store.GetProvider("media-provider")
				provider.APIKey = "different-account"
				if _, err := store.UpdateProvider("media-provider", provider); err != nil {
					t.Fatal(err)
				}
			case "endpoint":
				provider, _ := store.GetProvider("media-provider")
				provider.BaseURL = "https://different.example/v1"
				if _, err := store.UpdateProvider("media-provider", provider); err != nil {
					t.Fatal(err)
				}
			case "model":
				if err := store.db.Model(&ModelRoute{}).Where("id = ?", "media-route").Update("provider_model", "other-model").Error; err != nil {
					t.Fatal(err)
				}
			case "resource":
				if err := store.db.Model(&mediaTaskBinding{}).Where("id = ?", id).Update("resource_id", "different-resource").Error; err != nil {
					t.Fatal(err)
				}
			case "route disabled":
				if err := store.db.Model(&ModelRoute{}).Where("id = ?", "media-route").Update("status", "disabled").Error; err != nil {
					t.Fatal(err)
				}
			}
			store.AddRoute(ModelRoute{ID: "fallback-task-route", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "vendor-media", Status: StatusActive, Priority: 2, Weight: 100})
			response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id, nil, key)
			if response.Code != 503 || polls != 0 || !strings.Contains(response.Body, "media_task_route_unavailable") {
				t.Fatalf("task escaped original account: %d polls=%d %s", response.Code, polls, response.Body)
			}
		})
	}
}

func TestMediaVideoDoesNotRetryAmbiguousSubmissionOrApplicationError(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"code":500,"message":"upstream-fixture-secret failed"}`)
			}, "video")
			store.AddRoute(ModelRoute{ID: "fallback-video", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "backup-model", Status: StatusActive, Priority: 2, Weight: 100})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "public-media", "prompt": "fixture"}, key)
			if response.Code == 200 || calls != 1 || strings.Contains(response.Body, "upstream-fixture-secret") {
				t.Fatalf("ambiguous video failure: status=%d calls=%d %s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestMediaTaskProviderHookReceivesOriginalIdentifier(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Error("poll should be handled by the provider hook")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"data":[{"task_id":"original-vendor-task"}]}`)
	}, "video")
	id := submitMediaTask(t, server, key, "/v1/videos/generations")
	called := 0
	registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "task-query", Metadata: map[string]string{"protocol": "tasks"}, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		called++
		var request map[string]json.RawMessage
		if err := json.Unmarshal(input.Envelope.RequestBody, &request); err != nil {
			t.Fatal(err)
		}
		if string(request["task_id"]) != `"original-vendor-task"` || string(request["language"]) != `"en"` {
			t.Errorf("incorrect provider task query: %s", input.Envelope.RequestBody)
		}
		return rawProviderCallResult(t, json.RawMessage(`{"code":200,"data":{"id":"original-vendor-task","status":"processing","progress":40}}`), Usage{TotalTokens: 999}), nil
	})
	response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id+"?language=en", nil, key)
	if response.Code != 200 || called != 1 || !strings.Contains(response.Body, `"id":"`+id+`"`) {
		t.Fatalf("task hook response: %d %s calls=%d", response.Code, response.Body, called)
	}
	for _, usage := range store.ListUsageRecords() {
		if usage.TotalTokens != 0 {
			t.Fatalf("hook poll usage was billed: %+v", usage)
		}
	}
}

func TestMediaTaskPinsResourceWhenWeightsChange(t *testing.T) {
	var submitCredential, pollCredential string
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			submitCredential = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, `{"data":[{"task_id":"resource-task"}]}`)
			return
		}
		pollCredential = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":{"id":"resource-task","status":"completed"}}`)
	}, "video")
	for i, credential := range []string{"resource-account-a", "resource-account-b"} {
		_, err := store.AddProviderResource(ProviderResource{ID: credential, ProviderID: "media-provider", Name: credential, ResourceType: ProviderResourceAPIKey, APIKey: credential, Status: StatusActive, Healthy: true, Priority: i + 1, Weight: 100})
		if err != nil {
			t.Fatal(err)
		}
	}
	id := submitMediaTask(t, server, key, "/v1/videos/generations")
	if !strings.Contains(submitCredential, "resource-account-") {
		t.Fatalf("submission did not use resource credentials: %q", submitCredential)
	}
	if err := store.db.Model(&ProviderResource{}).Where("provider_id = ?", "media-provider").Update("priority", 0).Error; err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id, nil, key)
	if response.Code != 200 || pollCredential != submitCredential {
		t.Fatalf("poll switched account: %d submit=%q poll=%q %s", response.Code, submitCredential, pollCredential, response.Body)
	}
}

func TestMediaPreservesNonHTTPVendorCodes(t *testing.T) {
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":1000,"data":[{"url":"https://example.com/image.png"}]}`)
	}, "image")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": "public-media", "prompt": "fixture"}, key)
	if response.Code != 200 || !strings.Contains(response.Body, `"code":1000`) {
		t.Fatalf("vendor code was interpreted as HTTP status: %d %s", response.Code, response.Body)
	}
}
