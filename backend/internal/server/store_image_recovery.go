package server

import (
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
)

// FailUnfinishedImageJobs fails the unfinished image jobs owned by the named
// worker instance. Scoping by owner keeps a draining instance from failing a
// live peer's work: see the Store interface notes.
func (s *GormStore) FailUnfinishedImageJobs(workerInstance string, code string, message string) ([]ImageJob, error) {
	return s.failImageJobsWhere(func(tx *gorm.DB) *gorm.DB {
		return tx.Where("worker_instance = ?", workerInstance)
	}, code, message)
}

// FailAbandonedImageJobs fails unfinished image jobs whose owner no longer
// publishes a live heartbeat. Jobs without a recorded owner predate instance
// ownership and count as abandoned.
func (s *GormStore) FailAbandonedImageJobs(code string, message string) ([]ImageJob, error) {
	cutoff := time.Now().UTC().Add(-InstanceHeartbeatTTL).Format(time.RFC3339)
	return s.failImageJobsWhere(func(tx *gorm.DB) *gorm.DB {
		return tx.Where(
			"worker_instance IS NULL OR worker_instance = '' OR worker_instance NOT IN (SELECT instance_id FROM instance_heartbeats WHERE last_seen >= ?)",
			cutoff,
		)
	}, code, message)
}

// failImageJobsWhere fails the queued/running image jobs matched by scope and
// unwinds their admission side effects. The scoped UPDATE is the single
// decision point: see the claim comments inside.
func (s *GormStore) failImageJobsWhere(scope func(tx *gorm.DB) *gorm.DB, code string, message string) ([]ImageJob, error) {
	now := time.Now().UTC()
	var jobs []ImageJob
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Claim first: the scoped UPDATE is the single decision point. A
		// heartbeat that refreshes after it cannot un-claim a row, and a peer
		// sweep blocks on the row lock, then sees the finished transition
		// instead of re-claiming it. The claiming status is transient and only
		// ever exists inside this transaction.
		if err := scope(tx.Model(&ImageJob{})).
			Where("status IN ?", []string{imageJobStatusQueued, imageJobStatusRunning}).
			Update("status", imageJobStatusFailing).Error; err != nil {
			return err
		}
		// Clean up exactly the rows this invocation claimed: refunds, lease
		// deletes, and failure logs must match the rows the claim UPDATE
		// actually transitioned, not an earlier read of the same predicate.
		if err := tx.Where("status = ?", imageJobStatusFailing).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		if err := tx.Model(&ImageJob{}).
			Where("status = ?", imageJobStatusFailing).
			Updates(map[string]any{
				"status":        imageJobStatusFailed,
				"error_code":    code,
				"error_message": message,
				"completed_at":  now,
			}).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if err := s.rollbackImageJobAdmission(tx, job); err != nil {
				return err
			}
			if strings.TrimSpace(job.RequestID) == "" {
				continue
			}
			if err := s.deleteRequestConcurrencyLeases(tx, job.RequestID); err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&RequestLog{}).Where("request_id = ?", job.RequestID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Create(&RequestLog{
					ID:               NewID("log"),
					RequestID:        job.RequestID,
					ProjectID:        job.ProjectID,
					APIKeyID:         job.APIKeyID,
					AttributedUserID: job.AttributedUserID,
					ModelName:        job.Model,
					StatusCode:       http.StatusServiceUnavailable,
					ErrorCode:        code,
					LatencyMS:        latencyMillis(now.Sub(job.CreatedAt)),
					CreatedAt:        now,
				}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for index := range jobs {
		jobs[index].Status = imageJobStatusFailed
		jobs[index].ErrorCode = code
		jobs[index].ErrorMessage = message
		jobs[index].CompletedAt = &now
		jobs[index].Prompt = s.decryptSecret(jobs[index].PromptCiphertext)
		jobs[index].RevisedPrompt = s.decryptSecret(jobs[index].RevisedPromptCiphertext)
	}
	return jobs, nil
}
