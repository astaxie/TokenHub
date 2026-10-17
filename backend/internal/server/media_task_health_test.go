package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMediaTaskAccountChangesDoNotAffectProviderResourceHealth(t *testing.T) {
	for _, change := range []string{"credential", "options"} {
		t.Run(change, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"task_id":"vendor-health-task"}]}`)
			}, "video")
			store.failureThreshold = 2
			resource, err := store.AddProviderResource(ProviderResource{
				ID: "media-task-health-resource", ProviderID: "media-provider", Name: "Media task health resource",
				ResourceType: ProviderResourceAPIKey, APIKey: "original-account", Options: map[string]string{"account": "original"},
				Status: StatusActive, Healthy: true, Priority: 1, Weight: 100,
			})
			if err != nil {
				t.Fatal(err)
			}
			id := reviewSubmitMediaTask(t, server, key)
			if change == "credential" {
				resource.APIKey = "replacement-account"
			} else {
				resource.Options = map[string]string{"account": "replacement"}
			}
			if _, err := store.UpdateProviderResource(resource.ID, resource); err != nil {
				t.Fatal(err)
			}
			before, ok := store.GetProviderResource(resource.ID)
			if !ok {
				t.Fatal("missing provider resource before polling")
			}
			for range store.failureThreshold + 1 {
				response := doJSON(t, server.Handler(), http.MethodGet, "/v1/tasks/"+id, nil, key)
				if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body, "media_task_route_unavailable") {
					t.Fatalf("expected local task binding refusal: status=%d body=%s", response.Code, response.Body)
				}
			}
			if calls != 1 {
				t.Fatalf("rejected task polls reached the replacement account: calls=%d", calls)
			}
			after, ok := store.GetProviderResource(resource.ID)
			if !ok {
				t.Fatal("missing provider resource after polling")
			}
			if after.FailureCount != before.FailureCount || after.Healthy != before.Healthy || after.CooldownUntil != nil {
				t.Fatalf("local task refusals changed resource health: failures=%d healthy=%v cooldown=%v", after.FailureCount, after.Healthy, after.CooldownUntil)
			}
		})
	}
}
