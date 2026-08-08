package server

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	requestPayloadCleanupBatchSize = 500
	requestPayloadCleanupTaskName  = "request-payload-retention"
)

type requestPayloadCleanupStore interface {
	RunClusterTask(ctx context.Context, name string, revision int64, fn func(context.Context) error) error
	DeleteRequestPayloadLogsBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
}

type requestPayloadCleanupService struct {
	store         requestPayloadCleanupStore
	retentionDays int
	now           func() time.Time
	schedulerOnce sync.Once
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
}

func newRequestPayloadCleanupService(store requestPayloadCleanupStore, retentionDays int) *requestPayloadCleanupService {
	return &requestPayloadCleanupService{
		store:         store,
		retentionDays: retentionDays,
		now:           time.Now,
	}
}

func (s *requestPayloadCleanupService) Run(ctx context.Context, now time.Time) (int64, error) {
	if s.retentionDays <= 0 {
		return 0, nil
	}
	now = now.UTC()
	revision := int64(now.Year()*10000 + int(now.Month())*100 + now.Day())
	cutoff := now.AddDate(0, 0, -s.retentionDays)
	var deleted int64
	err := s.store.RunClusterTask(ctx, requestPayloadCleanupTaskName, revision, func(taskCtx context.Context) error {
		for {
			batchDeleted, err := s.store.DeleteRequestPayloadLogsBefore(taskCtx, cutoff, requestPayloadCleanupBatchSize)
			deleted += batchDeleted
			if err != nil {
				return err
			}
			if batchDeleted < requestPayloadCleanupBatchSize {
				return nil
			}
		}
	})
	return deleted, err
}

func (s *requestPayloadCleanupService) StartScheduler(interval time.Duration) {
	if s.retentionDays <= 0 {
		return
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	s.schedulerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.schedulerStop = cancel
		s.schedulerDone = make(chan struct{})
		go func() {
			defer close(s.schedulerDone)
			s.runScheduled(ctx, s.now().UTC())
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
	})
}

func (s *Server) StartRequestPayloadCleanupScheduler() {
	s.payloadCleanup.StartScheduler(24 * time.Hour)
}

func (s *requestPayloadCleanupService) runScheduled(ctx context.Context, now time.Time) {
	deleted, err := s.Run(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[tokenhub] request payload cleanup failed: %v", err)
		}
		return
	}
	if deleted > 0 {
		log.Printf("[tokenhub] deleted %d expired request payload log(s)", deleted)
	}
}

func (s *requestPayloadCleanupService) Shutdown(ctx context.Context) error {
	if s.schedulerStop == nil {
		return nil
	}
	s.schedulerStop()
	select {
	case <-s.schedulerDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
