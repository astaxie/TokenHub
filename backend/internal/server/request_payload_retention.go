package server

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"
)

const (
	requestPayloadRetentionTaskName  = "request-payload-retention"
	requestPayloadRetentionIndexName = "idx_request_payload_logs_created_at_id"
	requestPayloadRetentionInterval  = time.Hour
	requestPayloadRetentionBatchSize = 1000
	minimumAuditRetentionDays        = 1
	maximumAuditRetentionDays        = 3650
)

type requestPayloadRetentionStore interface {
	ListResourcesContext(ctx context.Context, kind string) ([]AdminResource, error)
	DeleteRequestPayloadLogsBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
	RunClusterTask(ctx context.Context, name string, revision int64, fn func(context.Context) error) error
}

type requestPayloadRetentionService struct {
	store requestPayloadRetentionStore

	schedulerMu   sync.Mutex
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
}

func newRequestPayloadRetentionService(store requestPayloadRetentionStore) *requestPayloadRetentionService {
	return &requestPayloadRetentionService{store: store}
}

func (s *requestPayloadRetentionService) RunDue(ctx context.Context, now time.Time) (int64, error) {
	now = now.UTC()
	revision := now.Truncate(requestPayloadRetentionInterval).Unix()
	var deleted int64
	err := s.store.RunClusterTask(ctx, requestPayloadRetentionTaskName, revision, func(taskCtx context.Context) error {
		settings, err := s.store.ListResourcesContext(taskCtx, "settings")
		if err != nil {
			return fmt.Errorf("read request payload retention settings: %w", err)
		}
		retentionDays, ok := configuredAuditRetentionDays(settings)
		if !ok {
			log.Printf("[tokenhub] request payload retention skipped: active cfg_gateway.audit_retention must be an integer from %d to %d followed by d", minimumAuditRetentionDays, maximumAuditRetentionDays)
			return nil
		}
		cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
		deleted, err = s.store.DeleteRequestPayloadLogsBefore(taskCtx, cutoff, requestPayloadRetentionBatchSize)
		if err != nil {
			return fmt.Errorf("delete expired request payload logs: %w", err)
		}
		return nil
	})
	return deleted, err
}

func configuredAuditRetentionDays(settings []AdminResource) (int, bool) {
	for _, setting := range settings {
		if setting.ID != gatewaySettingsID || setting.Status != StatusActive {
			continue
		}
		value, ok := setting.Fields["audit_retention"]
		if !ok {
			return 0, false
		}
		return parseAuditRetentionDays(value)
	}
	return 0, false
}

func parseAuditRetentionDays(value any) (int, bool) {
	raw, ok := value.(string)
	if !ok || len(raw) < 2 || raw[len(raw)-1] != 'd' {
		return 0, false
	}
	digits := raw[:len(raw)-1]
	if digits[0] == '0' {
		return 0, false
	}
	for index := range len(digits) {
		if digits[index] < '0' || digits[index] > '9' {
			return 0, false
		}
	}
	days, err := strconv.Atoi(digits)
	if err != nil || days < minimumAuditRetentionDays || days > maximumAuditRetentionDays {
		return 0, false
	}
	return days, true
}

func (s *requestPayloadRetentionService) StartScheduler(interval time.Duration) {
	if interval <= 0 {
		interval = requestPayloadRetentionInterval
	}
	s.schedulerMu.Lock()
	defer s.schedulerMu.Unlock()
	if s.schedulerStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.schedulerStop = cancel
	s.schedulerDone = make(chan struct{})
	go func() {
		defer close(s.schedulerDone)
		s.runScheduled(ctx, time.Now().UTC())
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

func (s *requestPayloadRetentionService) runScheduled(ctx context.Context, now time.Time) {
	deleted, err := s.RunDue(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[tokenhub] request payload retention failed: %v", err)
		}
		return
	}
	if deleted > 0 {
		log.Printf("[tokenhub] deleted %d expired request payload logs", deleted)
	}
}

func (s *requestPayloadRetentionService) Shutdown(ctx context.Context) error {
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

func (s *GormStore) DeleteRequestPayloadLogsBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		return 0, fmt.Errorf("positive request payload cleanup batch size is required")
	}
	var total int64
	for {
		result := s.db.WithContext(ctx).Exec(`DELETE FROM request_payload_logs
WHERE id IN (
    SELECT id FROM request_payload_logs
    WHERE created_at < ?
    ORDER BY created_at ASC, id ASC
    LIMIT ?
)`, cutoff.UTC(), batchSize)
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
		if result.RowsAffected < int64(batchSize) {
			break
		}
	}
	if err := s.gcOrphanedPayloadChunks(ctx, cutoff.UTC(), batchSize); err != nil {
		return total, err
	}
	return total, nil
}

// gcOrphanedPayloadChunks drops chunk references whose payload row is gone and
// then the unreferenced chunks older than the cutoff. Both steps run in
// indexed batches; the NOT EXISTS guard keeps chunks that newer requests still
// reference even though the chunks themselves predate the cutoff.
func (s *GormStore) gcOrphanedPayloadChunks(ctx context.Context, cutoff time.Time, batchSize int) error {
	if err := s.db.WithContext(ctx).Exec(`DELETE FROM payload_chunk_refs
WHERE request_id NOT IN (SELECT request_id FROM request_payload_logs)`).Error; err != nil {
		return fmt.Errorf("delete orphaned request payload chunk references: %w", err)
	}
	for {
		result := s.db.WithContext(ctx).Exec(`DELETE FROM payload_chunks
WHERE hash IN (
    SELECT hash FROM payload_chunks
    WHERE created_at < ? AND NOT EXISTS (
        SELECT 1 FROM payload_chunk_refs WHERE chunk_hash = payload_chunks.hash
    )
    LIMIT ?
)`, cutoff, batchSize)
		if result.Error != nil {
			return fmt.Errorf("delete orphaned request payload chunks: %w", result.Error)
		}
		if result.RowsAffected < int64(batchSize) {
			return nil
		}
	}
}

func ensureRequestPayloadRetentionIndex(db *gorm.DB, driver string) error {
	createIndex := "CREATE INDEX IF NOT EXISTS"
	if driver == "postgres" {
		createIndex = "CREATE INDEX CONCURRENTLY IF NOT EXISTS"
		var invalidCount int64
		if err := db.Raw(`SELECT COUNT(*) FROM pg_index
WHERE indexrelid = to_regclass(?) AND NOT indisvalid`, requestPayloadRetentionIndexName).Scan(&invalidCount).Error; err != nil {
			return fmt.Errorf("inspect request payload retention index: %w", err)
		}
		if invalidCount > 0 {
			if err := db.Exec("DROP INDEX CONCURRENTLY IF EXISTS " + requestPayloadRetentionIndexName).Error; err != nil {
				return fmt.Errorf("drop invalid request payload retention index: %w", err)
			}
		}
	}
	if err := db.Exec(createIndex + ` ` + requestPayloadRetentionIndexName + `
ON request_payload_logs(created_at, id)`).Error; err != nil {
		return fmt.Errorf("index request payload retention: %w", err)
	}
	return nil
}
