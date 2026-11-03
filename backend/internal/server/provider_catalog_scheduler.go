package server

import (
	"context"
	"log"
	"sync"
	"time"
)

const providerCatalogSyncTaskName = "provider-catalog-sync"

// providerCatalogScheduler refreshes the public provider catalog on a fixed
// interval. Cluster-wide dedup comes from RunClusterTask: its revision is the
// truncated UTC timestamp of the tick, so in a multi-instance deployment
// exactly one instance performs the download per interval and the rest see the
// revision as already done.
type providerCatalogScheduler struct {
	store Store
	run   func(context.Context) error

	schedulerMu   sync.Mutex
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
}

func newProviderCatalogScheduler(store Store, run func(context.Context) error) *providerCatalogScheduler {
	return &providerCatalogScheduler{store: store, run: run}
}

func (s *providerCatalogScheduler) RunDue(ctx context.Context, now time.Time) error {
	if s == nil || s.store == nil || s.run == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	revision := now.UTC().Truncate(time.Hour * 24).Unix()
	return s.store.RunClusterTask(ctx, providerCatalogSyncTaskName, revision, s.run)
}

func (s *providerCatalogScheduler) StartScheduler(interval time.Duration) {
	if s == nil {
		return
	}
	if interval <= 0 {
		return
	}
	s.schedulerMu.Lock()
	if s.schedulerStop != nil {
		s.schedulerMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.schedulerStop = cancel
	s.schedulerDone = make(chan struct{})
	s.schedulerMu.Unlock()
	go func() {
		defer close(s.schedulerDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.runScheduled(ctx, now.UTC())
			}
		}
	}()
}

func (s *providerCatalogScheduler) runScheduled(ctx context.Context, now time.Time) {
	if err := s.RunDue(ctx, now); err != nil && ctx.Err() == nil {
		log.Printf("[tokenhub] provider catalog sync scheduler failed: %v", err)
	}
}

func (s *providerCatalogScheduler) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.schedulerMu.Lock()
	stop := s.schedulerStop
	done := s.schedulerDone
	s.schedulerMu.Unlock()
	if stop == nil {
		return nil
	}
	stop()
	select {
	case <-done:
		s.schedulerMu.Lock()
		if s.schedulerDone == done {
			s.schedulerStop = nil
			s.schedulerDone = nil
		}
		s.schedulerMu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) runProviderCatalogSync(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, _, err := s.providerCatalog.reloadWithOptions(ctx, false)
	return err
}
