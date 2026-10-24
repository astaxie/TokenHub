package server

import (
	"context"
	"log"
	"os"
	"time"
)

func (r *claudeSubscriptionRun) cleanup() {
	defer func() {
		r.adapter.mu.Lock()
		if r.adapter.runs[r.resourceID] == r {
			delete(r.adapter.runs, r.resourceID)
		}
		r.adapter.mu.Unlock()
		close(r.cleanupDone)
	}()
	if r.command != nil && r.command.Process != nil {
		<-r.exited
		// Refresh can happen before the first assistant event. A cancelled HTTP
		// request must not discard the only current refresh token on teardown.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 10*time.Second)
		defer cancel()
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			err = r.persist(ctx)
			if err == nil || AsHTTPError(err).Code == "provider_credentials_changed" || AsHTTPError(err).Code == "provider_resource_reauthorization_required" || ctx.Err() != nil {
				break
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
		if err != nil && AsHTTPError(err).Code != "provider_credentials_changed" && AsHTTPError(err).Code != "provider_resource_reauthorization_required" {
			// Never replace a concurrent account edit. On a transient storage failure,
			// keep the private recovery directory instead of destroying rotated tokens.
			log.Printf("[tokenhub] Claude account rotation could not be saved; private recovery directory retained: %s", r.root)
			return
		}
	}
	if r.root != "" {
		_ = os.RemoveAll(r.root)
	}
}
