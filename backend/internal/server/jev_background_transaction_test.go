package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestJevBackgroundBindingAtomicCompletion(t *testing.T) {
	testJevBackgroundBindingAtomicCompletion(t, NewMemoryStore())
}

func testJevBackgroundBindingAtomicCompletion(t *testing.T, store *GormStore) {
	t.Helper()
	for _, outcome := range []string{"completed", "cancelled", "lost ownership", "binding conflict"} {
		t.Run(outcome, func(t *testing.T) {
			payload, _ := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"synthetic","input":"task","background":true}`)})
			created, err := store.CreateResponseJob(ResponseJob{ID: NewID("resp"), ProjectID: "synthetic", APIKeyID: "synthetic", Model: "synthetic"}, payload)
			if err != nil {
				t.Fatal(err)
			}
			job, claimed, err := store.ClaimResponseJob("jev-test", time.Minute, time.Hour)
			if err != nil || !claimed || job.ID != created.ID {
				t.Fatalf("claim: %+v %v %v", job, claimed, err)
			}
			binding := jevResponseBinding{KeyHash: NewID("binding"), RouteID: "route", ProviderID: "provider", ProviderModel: "model", UpstreamID: "resp_original", ExpiresAt: time.Now().Add(time.Hour).Unix()}
			marker := binding.KeyHash + "-guard"
			call := CallContext{jevResponseBinding: &pendingJevResponseBinding{binding: binding, protectedIDs: []string{marker}}}
			t.Cleanup(func() {
				_ = store.db.Delete(&ResponseJob{}, "id = ?", job.ID).Error
				_ = store.db.Delete(&ResponseJobEvent{}, "job_id = ?", job.ID).Error
				_ = store.db.Delete(&jevResponseBinding{}, "key_hash IN ?", []string{binding.KeyHash, marker}).Error
			})
			owner := "jev-test"
			switch outcome {
			case "lost ownership":
				owner = "stale-worker"
			case "cancelled":
				if _, _, err := store.CancelResponseJob(job.ID, "test", time.Hour); err != nil {
					t.Fatal(err)
				}
			case "binding conflict":
				conflict := binding
				conflict.UpstreamID = "conflicting-response"
				if err := store.SaveJevResponseBinding(context.Background(), conflict, nil); err != nil {
					t.Fatal(err)
				}
			}
			finish := func(owner string) (ResponseJob, bool, error) {
				return store.FinalizeResponseJob(call, job.ID, owner, job.LeaseEpoch, responseJobStatusSucceeded, []byte(`{"id":"resp_transformed"}`), RouteSelection{}, Usage{}, 200, "", "", "", "", time.Hour)
			}
			_, settled, err := finish(owner)
			current, _, readErr := store.GetResponseJob(job.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if outcome == "binding conflict" {
				if err == nil || settled || current.Status != responseJobStatusRunning {
					t.Fatalf("partial completion: %+v %v %v", current, settled, err)
				}
				if err := store.db.Delete(&jevResponseBinding{}, "key_hash = ?", binding.KeyHash).Error; err != nil {
					t.Fatal(err)
				}
				if _, found, err := store.LoadJevResponseBinding(context.Background(), marker); err != nil || found {
					t.Fatalf("rolled back completion published guard: %v %v", found, err)
				}
				if _, settled, err = finish("jev-test"); err != nil || !settled {
					t.Fatalf("retry: %v %v", settled, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := outcome == "completed" || outcome == "binding conflict"
			loaded, found, err := store.LoadJevResponseBinding(context.Background(), binding.KeyHash)
			if err != nil || found != want || (found && loaded.UpstreamID != "resp_original") {
				t.Fatalf("binding visibility: %+v %v %v", loaded, found, err)
			}
			if outcome == "lost ownership" && (settled || current.Status != responseJobStatusRunning) {
				t.Fatalf("stale worker completed: %+v", current)
			}
			if outcome == "cancelled" && current.Status != responseJobStatusCancelled {
				t.Fatalf("cancelled completion: %+v", current)
			}
		})
	}
}

func TestJevResponseBindingRejectsUnserializableOutput(t *testing.T) {
	server, routed, _, _ := jevFixture(t)
	response := map[string]any{"id": "resp_invalid", "output": make(chan int)}
	if err := server.bindJevResponse(context.Background(), routed.Call, routed.Routes[0], response, ""); err == nil {
		t.Fatal("invalid output was silently accepted")
	}
	if pending, err := server.pendingJevResponseBinding(routed.Call, routed.Routes[0], response, "background"); err == nil || pending != nil {
		t.Fatalf("invalid background output produced a binding: %+v %v", pending, err)
	}
}
