package server

import (
	"log"
	"time"
)

// imageJobAbandonSweepInterval paces the periodic recovery sweep. It runs at
// half the heartbeat TTL so a dead worker's jobs are failed within one TTL of
// its heartbeat lapsing, without racing the live heartbeat refreshes.
const imageJobAbandonSweepInterval = InstanceHeartbeatTTL / 2

// startImageJobRecovery runs the abandonment sweep once at startup and keeps
// it running periodically for the server's lifetime. Recovery starts with the
// server rather than with the first image request: any surviving replica must
// clean up after a dead peer even when it never serves image traffic itself.
func (s *Server) startImageJobRecovery() {
	jobs, err := s.store.FailAbandonedImageJobs("image_worker_restarted", "Image generation stopped because the owning worker stopped")
	if err != nil {
		log.Printf("[tokenhub] failed to mark abandoned image jobs after startup: %v", err)
	} else if len(jobs) > 0 {
		log.Printf("[tokenhub] marked %d abandoned image jobs as failed after startup", len(jobs))
	}
	go s.sweepAbandonedImageJobs()
}

// sweepAbandonedImageJobs periodically fails unfinished image jobs whose
// owning instance stopped publishing a heartbeat, so orphans are recovered
// even when no other instance restarts. Startup runs the same sweep once.
func (s *Server) sweepAbandonedImageJobs() {
	ticker := time.NewTicker(imageJobAbandonSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.imageContext.Done():
			return
		case <-ticker.C:
			jobs, err := s.store.FailAbandonedImageJobs("image_worker_restarted", "Image generation stopped because the owning worker stopped")
			if err != nil {
				log.Printf("[tokenhub] failed to sweep abandoned image jobs: %v", err)
			} else if len(jobs) > 0 {
				log.Printf("[tokenhub] marked %d abandoned image jobs as failed", len(jobs))
			}
		}
	}
}
