package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMediaTaskReferencesResolveReturnedHandles(t *testing.T) {
	document := mediaOpenAPIDocument(t)
	for _, field := range []string{"draft_task_id", "draft_from_task_id", "extend_from_task_id", "source_task_id"} {
		t.Run(field, func(t *testing.T) {
			calls := 0
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request map[string]json.RawMessage
				decodeFixtureRequest(t, r.Body, &request)
				w.Header().Set("Content-Type", "application/json")
				if calls == 1 {
					_, _ = io.WriteString(w, `{"code":200,"data":[{"status":"submitted","task_id":"vendor_original_task"}]}`)
					return
				}
				if string(request[field]) != `"vendor_original_task"` {
					t.Errorf("provider task reference = %s, want original provider ID", request[field])
				}
				for name, want := range map[string]string{
					"prompt":     `"Continue task_asset_unchanged"`,
					"image_urls": `["https://example.com/task_asset_unchanged.png"]`,
					"extension":  `{"task_id":"task_asset_unchanged"}`,
				} {
					if string(request[name]) != want {
						t.Errorf("non-reference field %s changed: got %s, want %s", name, request[name], want)
					}
				}
				_, _ = io.WriteString(w, `{"code":200,"data":[{"status":"submitted","task_id":"vendor_followup_task"}]}`)
			}, "video")
			id := reviewSubmitMediaTask(t, server, key)
			body := map[string]any{
				"model": "public-media", field: id, "prompt": "Continue task_asset_unchanged",
				"image_urls": []string{"https://example.com/task_asset_unchanged.png"},
				"extension":  map[string]string{"task_id": "task_asset_unchanged"},
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", body, key)
			if response.Code != http.StatusOK || calls != 2 {
				t.Fatalf("follow-up generation failed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			var result struct {
				Data []struct {
					TaskID string `json:"task_id"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(response.Body), &result); err != nil || len(result.Data) != 1 || !strings.HasPrefix(result.Data[0].TaskID, "task_") || result.Data[0].TaskID == id || strings.Contains(response.Body, "vendor_") {
				t.Fatalf("follow-up did not return a new isolated handle: %s (%v)", response.Body, err)
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			assertMediaOperationSchema(t, document, "/v1/videos/generations", "post", "", string(encoded))
			assertMediaOperationSchema(t, document, "/v1/videos/generations", "post", "200", response.Body)
		})
	}
}

func addMediaTaskReferenceTarget(t *testing.T, store *GormStore) {
	t.Helper()
	store.AddModel(Model{Name: "followup-media", Modality: "video", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "followup-route", ModelName: "followup-media", ProviderID: "media-provider", ProviderModel: "vendor-followup", Status: StatusActive, Priority: 1, Weight: 100})
	key := store.ListAPIKeys()[0]
	key.Allowed = []string{"public-media", "followup-media"}
	if _, err := store.UpdateAPIKey(key.ID, key); err != nil {
		t.Fatal(err)
	}
}

func TestMediaTaskReferencePinsAccountAcrossTargetModels(t *testing.T) {
	var credentials, models []string
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		credentials = append(credentials, r.Header.Get("Authorization"))
		var request map[string]json.RawMessage
		decodeFixtureRequest(t, r.Body, &request)
		models = append(models, string(request["model"]))
		if len(credentials) == 2 && string(request["source_task_id"]) != `"vendor_source_task"` {
			t.Errorf("follow-up source = %s, want original provider ID", request["source_task_id"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"data":[{"task_id":"vendor_source_task"}]}`)
	}, "video")
	if _, err := store.AddProviderResource(ProviderResource{ID: "source-account", ProviderID: "media-provider", Name: "Source account", ResourceType: ProviderResourceAPIKey, APIKey: "source-account-key", Status: StatusActive, Healthy: true, Priority: 2, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	id := reviewSubmitMediaTask(t, server, key)
	addMediaTaskReferenceTarget(t, store)
	if _, err := store.AddProviderResource(ProviderResource{ID: "preferred-account", ProviderID: "media-provider", Name: "Preferred account", ResourceType: ProviderResourceAPIKey, APIKey: "preferred-account-key", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "followup-media", "source_task_id": id}, key)
	if response.Code != http.StatusOK || len(credentials) != 2 {
		t.Fatalf("follow-up on a different model failed: status=%d calls=%d body=%s", response.Code, len(credentials), response.Body)
	}
	if credentials[0] != "Bearer source-account-key" || credentials[1] != credentials[0] || models[1] != `"vendor-followup"` {
		t.Fatalf("follow-up used the wrong account or model: credentials=%v models=%v", credentials, models)
	}
}

func TestMediaTaskReferencesRejectInaccessibleSourcesBeforeGeneration(t *testing.T) {
	for _, scenario := range []string{"foreign key", "expired binding", "unknown handle", "raw provider ID", "revoked source model", "rotated provider credential"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"code":200,"data":[{"task_id":"task_vendor_private"}]}`)
			}, "video")
			id := reviewSubmitMediaTask(t, server, key)
			addMediaTaskReferenceTarget(t, store)
			wantStatus, wantCode := http.StatusNotFound, "media_task_not_found"
			switch scenario {
			case "foreign key":
				project, _, err := store.ValidateAPIKey(key, "127.0.0.1")
				if err != nil {
					t.Fatal(err)
				}
				_, key, err = store.CreateAPIKey(project.ID, APIKey{Name: "Other key", Allowed: []string{"public-media", "followup-media"}, Status: StatusActive}, "thk_other_reference")
				if err != nil {
					t.Fatal(err)
				}
			case "expired binding":
				if err := store.db.Model(&mediaTaskBinding{}).Where("id = ?", id).Update("expires_at", time.Now().Add(-time.Second).Unix()).Error; err != nil {
					t.Fatal(err)
				}
			case "unknown handle":
				id = "task_unknown"
			case "raw provider ID":
				id = "task_vendor_private"
			case "revoked source model":
				apiKey := store.ListAPIKeys()[0]
				apiKey.Allowed = []string{"followup-media"}
				if _, err := store.UpdateAPIKey(apiKey.ID, apiKey); err != nil {
					t.Fatal(err)
				}
			case "rotated provider credential":
				provider, _ := store.GetProvider("media-provider")
				provider.APIKey = "replacement-account-key"
				if _, err := store.UpdateProvider(provider.ID, provider); err != nil {
					t.Fatal(err)
				}
				wantStatus, wantCode = http.StatusServiceUnavailable, "media_task_route_unavailable"
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/videos/generations", map[string]any{"model": "followup-media", "source_task_id": id}, key)
			if response.Code != wantStatus || !strings.Contains(response.Body, wantCode) || calls != 1 {
				t.Fatalf("inaccessible reference submitted generation: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}
