package server

import (
	"testing"
	"time"
)

// A draining instance must fail only the jobs it owns: jobs a live peer
// claimed keep running, and a later provider result can still complete them.
func TestFailUnfinishedImageJobsOnlyFailsOwnedJobs(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Recovery Ownership"})

	owned := ImageJob{
		ProjectID: project.ID, RequestID: "req_owned_job",
		Status: imageJobStatusRunning, Model: openAIImageModelName, Action: "generate",
		WorkerInstance: store.InstanceID(),
	}
	peer := ImageJob{
		ProjectID: project.ID, RequestID: "req_peer_job",
		Status: imageJobStatusRunning, Model: openAIImageModelName, Action: "generate",
		WorkerInstance: "instance-live-peer",
	}
	var ownedID, peerID string
	for _, job := range []ImageJob{owned, peer} {
		persisted, err := store.CreateImageJob(job, "prompt "+job.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if job.RequestID == "req_owned_job" {
			ownedID = persisted.ID
		} else {
			peerID = persisted.ID
		}
	}

	failed, err := store.FailUnfinishedImageJobs(store.InstanceID(), "image_worker_stopped", "shutdown")
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || failed[0].RequestID != "req_owned_job" {
		t.Fatalf("owner-scoped recovery failed the wrong jobs: %+v", failed)
	}
	afterOwned, ok := store.GetImageJob(ownedID)
	if !ok || afterOwned.Status != imageJobStatusFailed {
		t.Fatalf("owned job was not failed: %+v", afterOwned)
	}
	afterPeer, ok := store.GetImageJob(peerID)
	if !ok || afterPeer.Status != imageJobStatusRunning {
		t.Fatalf("live peer's job must keep running: %+v", afterPeer)
	}
}

// Recovery sweeps (startup and the periodic janitor) fail the jobs of
// instances whose heartbeat lapsed, plus legacy jobs without an owner, and
// leave instances with live heartbeats untouched.
func TestFailAbandonedImageJobsKeepsLiveInstances(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Recovery Abandonment"})

	if err := upsertInstanceHeartbeat(store.db, "instance-live", "test"); err != nil {
		t.Fatal(err)
	}
	if err := upsertInstanceHeartbeat(store.db, "instance-dead", "test"); err != nil {
		t.Fatal(err)
	}
	staleCutoff := time.Now().UTC().Add(-2 * InstanceHeartbeatTTL).Format(time.RFC3339)
	if err := store.db.Exec("UPDATE instance_heartbeats SET last_seen = ? WHERE instance_id = ?",
		staleCutoff, "instance-dead").Error; err != nil {
		t.Fatal(err)
	}

	live := ImageJob{ProjectID: project.ID, RequestID: "req_live_owner", Status: imageJobStatusRunning,
		Model: openAIImageModelName, Action: "generate", WorkerInstance: "instance-live"}
	dead := ImageJob{ProjectID: project.ID, RequestID: "req_dead_owner", Status: imageJobStatusRunning,
		Model: openAIImageModelName, Action: "generate", WorkerInstance: "instance-dead"}
	legacy := ImageJob{ProjectID: project.ID, RequestID: "req_legacy_owner", Status: imageJobStatusQueued,
		Model: openAIImageModelName, Action: "generate"}
	ids := map[string]string{}
	for _, job := range []ImageJob{live, dead, legacy} {
		persisted, err := store.CreateImageJob(job, "prompt "+job.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		ids[job.RequestID] = persisted.ID
	}

	failed, err := store.FailAbandonedImageJobs("image_worker_restarted", "owning worker stopped")
	if err != nil {
		t.Fatal(err)
	}
	failedRequests := map[string]bool{}
	for _, job := range failed {
		failedRequests[job.RequestID] = true
	}
	if len(failed) != 2 || !failedRequests["req_dead_owner"] || !failedRequests["req_legacy_owner"] {
		t.Fatalf("abandonment sweep failed the wrong jobs: %+v", failed)
	}
	afterLive, ok := store.GetImageJob(ids["req_live_owner"])
	if !ok || afterLive.Status != imageJobStatusRunning {
		t.Fatalf("live instance's job must keep running: %+v", afterLive)
	}
	afterDead, ok := store.GetImageJob(ids["req_dead_owner"])
	if !ok || afterDead.Status != imageJobStatusFailed || afterDead.ErrorCode != "image_worker_restarted" {
		t.Fatalf("dead instance's job was not failed: %+v", afterDead)
	}
}
