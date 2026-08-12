package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func responseJobTerminal(status string) bool {
	switch status {
	case responseJobStatusSucceeded, responseJobStatusFailed, responseJobStatusCancelled, responseJobStatusExpired:
		return true
	default:
		return false
	}
}

func (s *GormStore) encryptResponseJobPayload(payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", nil
	}
	encoded := s.encryptSecret(string(payload))
	if !strings.HasPrefix(encoded, "enc:v1:") {
		return "", fmt.Errorf("response job payload encryption failed")
	}
	return encoded, nil
}

func (s *GormStore) decryptResponseJobPayload(ciphertext string) ([]byte, error) {
	if ciphertext == "" {
		return nil, nil
	}
	if !strings.HasPrefix(ciphertext, "enc:v1:") {
		return nil, fmt.Errorf("response job payload is not encrypted")
	}
	plaintext := s.decryptSecret(ciphertext)
	if plaintext == "" {
		return nil, fmt.Errorf("response job payload decryption failed")
	}
	return []byte(plaintext), nil
}

func (s *GormStore) hydrateResponseJob(job *ResponseJob) error {
	requestJSON, err := s.decryptResponseJobPayload(job.RequestCiphertext)
	if err != nil {
		return err
	}
	resultJSON, err := s.decryptResponseJobPayload(job.ResultCiphertext)
	if err != nil {
		return err
	}
	job.RequestJSON = requestJSON
	job.ResultJSON = resultJSON
	return nil
}

func appendResponseJobEvent(tx *gorm.DB, jobID string, from string, to string, reason string, actor string, now time.Time) error {
	if err := tx.Create(&ResponseJobEvent{
		ID:         NewID("response-event"),
		JobID:      jobID,
		FromStatus: from,
		ToStatus:   to,
		ReasonCode: reason,
		Actor:      actor,
		CreatedAt:  now,
	}).Error; err != nil {
		return err
	}
	return tx.Create(&AuditEvent{
		ID:             NewID("audit"),
		ActorUserID:    actor,
		Action:         "response_job.transition",
		ResourceType:   "response_job",
		ResourceID:     jobID,
		Status:         "success",
		Message:        reason,
		BeforeSnapshot: fmt.Sprintf(`{"status":%q}`, from),
		AfterSnapshot:  fmt.Sprintf(`{"status":%q}`, to),
		CreatedAt:      now,
	}).Error
}

func (s *GormStore) CreateResponseJob(job ResponseJob, requestJSON []byte) (ResponseJob, error) {
	ciphertext, err := s.encryptResponseJobPayload(requestJSON)
	if err != nil {
		return ResponseJob{}, err
	}
	if strings.TrimSpace(job.ID) == "" {
		job.ID = NewID("resp")
	}
	if job.Status == "" {
		job.Status = responseJobStatusQueued
	}
	if job.Phase == "" {
		job.Phase = responseJobPhaseQueued
	}
	job.RequestCiphertext = ciphertext
	job.RequestJSON = nil

	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		now, nowErr := s.databaseNow(tx)
		if nowErr != nil {
			return nowErr
		}
		if job.CreatedAt.IsZero() {
			job.CreatedAt = now
		}
		job.UpdatedAt = now
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		return appendResponseJobEvent(tx, job.ID, "", job.Status, "response_submitted", "client", now)
	})
	if err != nil {
		return ResponseJob{}, err
	}
	job.RequestJSON = append([]byte(nil), requestJSON...)
	return job, nil
}

func (s *GormStore) GetResponseJob(id string) (ResponseJob, bool, error) {
	var job ResponseJob
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return ResponseJob{}, false, nil
		}
		return ResponseJob{}, false, err
	}
	return job, true, nil
}

func (s *GormStore) LoadResponseJobPayload(id string) ([]byte, []byte, error) {
	var job ResponseJob
	if err := s.db.Select("request_ciphertext", "result_ciphertext").First(&job, "id = ?", id).Error; err != nil {
		return nil, nil, err
	}
	if err := s.hydrateResponseJob(&job); err != nil {
		return nil, nil, err
	}
	return job.RequestJSON, job.ResultJSON, nil
}

func (s *GormStore) CountQueuedResponseJobs() (int64, error) {
	var count int64
	err := s.db.Model(&ResponseJob{}).
		Where("status = ?", responseJobStatusQueued).
		Count(&count).Error
	return count, err
}

func (s *GormStore) CountOutstandingResponseJobs() (int64, error) {
	var count int64
	err := s.db.Model(&ResponseJob{}).
		Where("status IN ?", []string{responseJobStatusQueued, responseJobStatusRunning}).
		Count(&count).Error
	return count, err
}

func (s *GormStore) CountRetainedResponseJobs() (int64, error) {
	var count int64
	err := s.db.Model(&ResponseJob{}).
		Where("status IN ?", []string{responseJobStatusSucceeded, responseJobStatusFailed, responseJobStatusCancelled}).
		Count(&count).Error
	return count, err
}

func (s *GormStore) ClaimResponseJob(owner string, leaseTTL time.Duration, resultTTL time.Duration) (ResponseJob, bool, error) {
	if strings.TrimSpace(owner) == "" || leaseTTL <= 0 {
		return ResponseJob{}, false, fmt.Errorf("response job lease owner and TTL are required")
	}
	if s.dbDriver == "sqlite" {
		return s.claimSQLiteResponseJob(owner, leaseTTL, resultTTL)
	}
	var claimed ResponseJob
	s.mu.Lock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		query := tx.Where("status = ?", responseJobStatusQueued).Order("created_at ASC")
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		if err := query.First(&claimed).Error; err != nil {
			return err
		}
		expiresAt := now.Add(leaseTTL)
		result := tx.Model(&ResponseJob{}).
			Where("id = ? AND status = ?", claimed.ID, responseJobStatusQueued).
			Updates(map[string]any{
				"status":           responseJobStatusRunning,
				"phase":            responseJobPhaseClaimed,
				"lease_owner":      owner,
				"lease_epoch":      gorm.Expr("lease_epoch + 1"),
				"lease_expires_at": expiresAt,
				"started_at":       now,
				"updated_at":       now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		claimed.Status = responseJobStatusRunning
		claimed.Phase = responseJobPhaseClaimed
		claimed.LeaseOwner = owner
		claimed.LeaseEpoch++
		claimed.LeaseExpiresAt = &expiresAt
		claimed.StartedAt = &now
		claimed.UpdatedAt = now
		return appendResponseJobEvent(tx, claimed.ID, responseJobStatusQueued, responseJobStatusRunning, "response_claimed", owner, now)
	})
	s.mu.Unlock()
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return ResponseJob{}, false, nil
		}
		return ResponseJob{}, false, err
	}
	if err := s.hydrateResponseJob(&claimed); err != nil {
		_ = s.failUnreadableResponseJob(claimed.ID, owner, claimed.LeaseEpoch, resultTTL)
		return ResponseJob{}, false, err
	}
	return claimed, true, nil
}

func (s *GormStore) claimSQLiteResponseJob(owner string, leaseTTL time.Duration, resultTTL time.Duration) (ResponseJob, bool, error) {
	var claimed ResponseJob
	now := time.Now().UTC()
	expiresAt := now.Add(leaseTTL)
	s.mu.Lock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Raw(`
			UPDATE response_jobs
			SET status = ?, phase = ?, lease_owner = ?, lease_epoch = lease_epoch + 1,
				lease_expires_at = ?, started_at = ?, updated_at = ?
			WHERE id = (
				SELECT id FROM response_jobs WHERE status = ? ORDER BY created_at ASC LIMIT 1
			) AND status = ?
			RETURNING *`,
			responseJobStatusRunning, responseJobPhaseClaimed, owner, expiresAt, now, now,
			responseJobStatusQueued, responseJobStatusQueued,
		).Scan(&claimed)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 || claimed.ID == "" {
			return gorm.ErrRecordNotFound
		}
		return appendResponseJobEvent(tx, claimed.ID, responseJobStatusQueued, responseJobStatusRunning, "response_claimed", owner, now)
	})
	s.mu.Unlock()
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return ResponseJob{}, false, nil
		}
		return ResponseJob{}, false, err
	}
	if err := s.hydrateResponseJob(&claimed); err != nil {
		_ = s.failUnreadableResponseJob(claimed.ID, owner, claimed.LeaseEpoch, resultTTL)
		return ResponseJob{}, false, err
	}
	return claimed, true, nil
}

func (s *GormStore) failUnreadableResponseJob(id string, owner string, epoch int64, resultTTL time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		expiresAt := now.Add(resultTTL)
		result := tx.Model(&ResponseJob{}).
			Where("id = ? AND status = ? AND lease_owner = ? AND lease_epoch = ?", id, responseJobStatusRunning, owner, epoch).
			Updates(map[string]any{
				"status":             responseJobStatusFailed,
				"error_code":         "response_payload_unreadable",
				"error_message":      "Encrypted response job payload could not be read",
				"request_ciphertext": "",
				"result_ciphertext":  "",
				"lease_owner":        "",
				"lease_expires_at":   nil,
				"completed_at":       now,
				"expires_at":         expiresAt,
				"updated_at":         now,
			})
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		return appendResponseJobEvent(tx, id, responseJobStatusRunning, responseJobStatusFailed, "response_payload_unreadable", owner, now)
	})
}

func (s *GormStore) RenewResponseJobLease(id string, owner string, epoch int64, leaseTTL time.Duration) (time.Duration, bool, error) {
	var expiresAt time.Time
	s.mu.Lock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		expiresAt = now.Add(leaseTTL)
		result := tx.Model(&ResponseJob{}).
			Where("id = ? AND status = ? AND lease_owner = ? AND lease_epoch = ?", id, responseJobStatusRunning, owner, epoch).
			Updates(map[string]any{"lease_expires_at": expiresAt, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	s.mu.Unlock()
	if err == gorm.ErrRecordNotFound {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	confirmedFor, err := s.persistedLeaseConfirmation(s.db, expiresAt)
	return confirmedFor, err == nil, err
}

func (s *GormStore) AdmitResponseJob(ctx context.Context, id string, owner string, epoch int64, key APIKey, modelName string, tokenReservation int64) (CallContext, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	requestID := NewID("req")
	var admission callAdmissionResult
	retained := false
	s.mu.Lock()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		// This is intentionally the first write in the transaction. Besides
		// fencing ownership, it obtains SQLite's write lock before quota rows are
		// read. Any later admission failure rolls the phase change back with all
		// counters and the in-flight lease.
		result := tx.Model(&ResponseJob{}).
			Where("id = ? AND status = ? AND phase = ? AND lease_owner = ? AND lease_epoch = ? AND lease_expires_at IS NOT NULL AND lease_expires_at > ? AND cancel_requested_at IS NULL", id, responseJobStatusRunning, responseJobPhaseClaimed, owner, epoch, now).
			Updates(map[string]any{"phase": responseJobPhaseAdmitted, "request_id": requestID, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		retained = true
		if err := appendResponseJobEvent(tx, id, responseJobStatusRunning, responseJobStatusRunning, "response_admitted", owner, now); err != nil {
			return err
		}
		admission, err = s.admitCallTransaction(ctx, tx, key, modelName, tokenReservation, requestID)
		if err != nil {
			return err
		}
		return tx.Model(&ResponseJob{}).
			Where("id = ? AND status = ? AND phase = ? AND lease_owner = ? AND lease_epoch = ? AND request_id = ?", id, responseJobStatusRunning, responseJobPhaseAdmitted, owner, epoch, requestID).
			Updates(map[string]any{
				"token_limit_bucket":  admission.call.TokenLimitBucket,
				"minute_request_held": admission.call.MinuteRequestHeld,
				"reserved_tokens":     admission.call.ReservedTokens,
				"admitted_at":         admission.call.StartedAt,
			}).Error
	})
	s.mu.Unlock()
	if err == gorm.ErrRecordNotFound {
		return CallContext{}, false, nil
	}
	if err != nil {
		return CallContext{}, retained, err
	}
	return s.startAdmittedCallHeartbeat(ctx, admission), true, nil
}

// ReleaseResponseJobAdmission stops the process-local heartbeat and releases
// the API-key concurrency slot. It deliberately does not reconcile quota or
// write a RequestLog; the fenced job finalizer or recovery transaction owns the
// single durable settlement.
func (s *GormStore) ReleaseResponseJobAdmission(requestID string) {
	s.ReleaseProviderResourceCapacity("response_job", requestID)
}

// ShutdownResponseJob atomically hands a running job back to the durable queue
// when no provider request was dispatched. Admission is rolled back with the
// phase transition so another replica can safely admit the retry without double
// counting quota. Once dispatch is recorded, retry safety is unknowable and the
// job is settled with the explicit execution-lost reason instead.
func (s *GormStore) ShutdownResponseJob(id string, owner string, epoch int64, resultTTL time.Duration) (string, bool, error) {
	var status string
	var retained bool
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		query := tx.Where("id = ?", id)
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var job ResponseJob
		if err := query.First(&job).Error; err != nil {
			return err
		}
		if job.Status != responseJobStatusRunning || job.LeaseOwner != owner || job.LeaseEpoch != epoch {
			return nil
		}
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		if job.CancelRequestedAt != nil {
			expiresAt := now.Add(resultTTL)
			result := tx.Model(&ResponseJob{}).
				Where("id = ? AND status = ? AND lease_owner = ? AND lease_epoch = ? AND cancel_requested_at IS NOT NULL", id, responseJobStatusRunning, owner, epoch).
				Updates(map[string]any{
					"status":              responseJobStatusCancelled,
					"error_code":          "response_cancelled_worker_lost",
					"error_message":       "Response cancellation was retained while the server stopped",
					"token_limit_bucket":  "",
					"minute_request_held": false,
					"reserved_tokens":     0,
					"admitted_at":         nil,
					"lease_owner":         "",
					"lease_expires_at":    nil,
					"completed_at":        now,
					"expires_at":          expiresAt,
					"updated_at":          now,
				})
			if result.Error != nil || result.RowsAffected != 1 {
				return result.Error
			}
			if err := s.refundUndispatchedResponseJobReservation(tx, job); err != nil {
				return err
			}
			if job.RequestID != "" {
				if err := tx.Delete(&InFlightLease{}, "id = ?", job.RequestID).Error; err != nil {
					return err
				}
			}
			if err := recordLostResponseJobRequest(tx, job, 499, "response_cancelled_worker_lost", now); err != nil {
				return err
			}
			if err := appendResponseJobEvent(tx, id, responseJobStatusRunning, responseJobStatusCancelled, "response_cancelled_worker_lost", owner, now); err != nil {
				return err
			}
			status = responseJobStatusCancelled
			retained = true
			return nil
		}
		if job.Phase == responseJobPhaseDispatched {
			expiresAt := now.Add(resultTTL)
			result := tx.Model(&ResponseJob{}).
				Where("id = ? AND status = ? AND phase = ? AND lease_owner = ? AND lease_epoch = ?", id, responseJobStatusRunning, responseJobPhaseDispatched, owner, epoch).
				Updates(map[string]any{
					"status":              responseJobStatusFailed,
					"error_code":          "response_execution_lost",
					"error_message":       "Server stopped after provider dispatch; the request was not retried",
					"token_limit_bucket":  "",
					"minute_request_held": false,
					"reserved_tokens":     0,
					"admitted_at":         nil,
					"lease_owner":         "",
					"lease_expires_at":    nil,
					"completed_at":        now,
					"expires_at":          expiresAt,
					"updated_at":          now,
				})
			if result.Error != nil || result.RowsAffected != 1 {
				return result.Error
			}
			if job.RequestID != "" {
				if err := tx.Delete(&InFlightLease{}, "id = ?", job.RequestID).Error; err != nil {
					return err
				}
			}
			if err := recordLostResponseJobRequest(tx, job, 500, "response_execution_lost", now); err != nil {
				return err
			}
			if err := appendResponseJobEvent(tx, id, responseJobStatusRunning, responseJobStatusFailed, "response_execution_lost", owner, now); err != nil {
				return err
			}
			status = responseJobStatusFailed
			retained = true
			return nil
		}
		if job.Phase == responseJobPhaseAdmitted {
			if job.RequestID == "" || job.AdmittedAt == nil {
				return fmt.Errorf("cannot safely roll back admitted response job %s during shutdown", id)
			}
		}
		if job.Phase != responseJobPhaseClaimed && job.Phase != responseJobPhaseAdmitted {
			return fmt.Errorf("unsupported response job shutdown phase %q", job.Phase)
		}
		result := tx.Model(&ResponseJob{}).
			Where("id = ? AND status = ? AND phase = ? AND lease_owner = ? AND lease_epoch = ?", id, responseJobStatusRunning, job.Phase, owner, epoch).
			Updates(map[string]any{
				"status":              responseJobStatusQueued,
				"phase":               responseJobPhaseQueued,
				"request_id":          "",
				"token_limit_bucket":  "",
				"minute_request_held": false,
				"reserved_tokens":     0,
				"admitted_at":         nil,
				"lease_owner":         "",
				"lease_expires_at":    nil,
				"started_at":          nil,
				"updated_at":          now,
			})
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		if job.Phase == responseJobPhaseAdmitted {
			if err := s.rollbackResponseJobAdmission(tx, job); err != nil {
				return err
			}
		}
		if err := appendResponseJobEvent(tx, id, responseJobStatusRunning, responseJobStatusQueued, "response_shutdown_requeued", owner, now); err != nil {
			return err
		}
		status = responseJobStatusQueued
		retained = true
		return nil
	})
	return status, retained, err
}

func (s *GormStore) rollbackResponseJobAdmission(tx *gorm.DB, job ResponseJob) error {
	if job.AdmittedAt == nil {
		return fmt.Errorf("response job %s has no admission timestamp", job.ID)
	}
	if err := s.lockScopeForUpdate(tx, "api_key", job.APIKeyID); err != nil {
		return err
	}
	if job.MinuteRequestHeld {
		bucket, err := s.quotaBucketForUpdate(tx, job.APIKeyID, "minute", minuteBucket(*job.AdmittedAt))
		if err != nil {
			return err
		}
		if bucket.Requests > 0 {
			bucket.Requests--
		}
		if err := tx.Save(&bucket).Error; err != nil {
			return err
		}
	}
	if err := s.reconcileAPIKeyMinuteTokens(tx, CallContext{
		Key:              APIKey{ID: job.APIKeyID},
		TokenLimitBucket: job.TokenLimitBucket,
		ReservedTokens:   job.ReservedTokens,
	}, 0); err != nil {
		return err
	}
	for _, period := range []struct {
		scope  string
		bucket string
	}{
		{scope: "day", bucket: dayBucket(*job.AdmittedAt)},
		{scope: "month", bucket: monthBucket(*job.AdmittedAt)},
	} {
		bucket, err := s.quotaBucketForUpdate(tx, job.APIKeyID, period.scope, period.bucket)
		if err != nil {
			return err
		}
		if bucket.Requests > 0 {
			bucket.Requests--
		}
		if err := tx.Save(&bucket).Error; err != nil {
			return err
		}
	}
	return tx.Delete(&InFlightLease{}, "id = ?", job.RequestID).Error
}

func (s *GormStore) MarkResponseJobPhase(id string, owner string, epoch int64, phase string, requestID string) (bool, error) {
	if phase != responseJobPhaseDispatched || strings.TrimSpace(requestID) == "" {
		return false, fmt.Errorf("unsupported response job phase %q", phase)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var retained bool
	err := s.db.Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		result := tx.Model(&ResponseJob{}).
			Where("id = ? AND status = ? AND phase = ? AND request_id = ? AND lease_owner = ? AND lease_epoch = ? AND lease_expires_at IS NOT NULL AND lease_expires_at > ? AND cancel_requested_at IS NULL", id, responseJobStatusRunning, responseJobPhaseAdmitted, requestID, owner, epoch, now).
			Updates(map[string]any{"phase": responseJobPhaseDispatched, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		retained = result.RowsAffected == 1
		if !retained {
			return nil
		}
		return appendResponseJobEvent(tx, id, responseJobStatusRunning, responseJobStatusRunning, "response_"+phase, owner, now)
	})
	return retained, err
}

func (s *GormStore) CancelResponseJob(id string, actor string, resultTTL time.Duration) (ResponseJob, bool, error) {
	var job ResponseJob
	s.mu.Lock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		query := tx.Where("id = ?", id)
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&job).Error; err != nil {
			return err
		}
		if responseJobTerminal(job.Status) {
			return nil
		}
		if job.Status == responseJobStatusRunning && job.CancelRequestedAt != nil {
			return nil
		}
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		from := job.Status
		expiresAt := now.Add(resultTTL)
		if job.Status == responseJobStatusRunning {
			result := tx.Model(&ResponseJob{}).
				Where("id = ? AND status = ?", id, responseJobStatusRunning).
				Updates(map[string]any{"cancel_requested_at": now, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			job.CancelRequestedAt = &now
			job.UpdatedAt = now
			return appendResponseJobEvent(tx, id, from, from, "response_cancel_requested", actor, now)
		}
		result := tx.Model(&ResponseJob{}).
			Where("id = ? AND status = ?", id, responseJobStatusQueued).
			Updates(map[string]any{
				"status":           responseJobStatusCancelled,
				"error_code":       "response_cancelled",
				"error_message":    "Response job was cancelled",
				"lease_owner":      "",
				"lease_expires_at": nil,
				"completed_at":     now,
				"expires_at":       expiresAt,
				"updated_at":       now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		job.Status = responseJobStatusCancelled
		job.ErrorCode = "response_cancelled"
		job.ErrorMessage = "Response job was cancelled"
		job.LeaseOwner = ""
		job.LeaseExpiresAt = nil
		job.CompletedAt = &now
		job.ExpiresAt = &expiresAt
		job.UpdatedAt = now
		return appendResponseJobEvent(tx, id, from, responseJobStatusCancelled, "response_cancelled", actor, now)
	})
	s.mu.Unlock()
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return ResponseJob{}, false, nil
		}
		return ResponseJob{}, false, err
	}
	return job, true, nil
}

func (s *GormStore) FinalizeResponseJob(call CallContext, id string, owner string, epoch int64, status string, resultJSON []byte, route RouteSelection, usage Usage, statusCode int, errorCode string, errorMessage string, clientIP string, userAgent string, resultTTL time.Duration) (ResponseJob, bool, error) {
	resultCiphertext, err := s.encryptResponseJobPayload(resultJSON)
	if err != nil {
		return ResponseJob{}, false, err
	}
	elapsed := call.elapsed()
	_ = s.stopInFlightLeaseHeartbeat(call.RequestID)
	usage = priceUsage(call.Model, usage)
	usage.ProviderCostUSD = s.providerCostUSD(route, usage)

	var job ResponseJob
	var settled bool
	var observedCompletion bool
	s.mu.Lock()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		query := tx.Where("id = ?", id)
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&job).Error; err != nil {
			return err
		}
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		expiresAt := now.Add(resultTTL)
		if job.Status == responseJobStatusRunning && job.LeaseOwner == owner && job.LeaseEpoch == epoch {
			if job.CancelRequestedAt != nil {
				status = responseJobStatusCancelled
				statusCode = 499
				errorCode = "response_cancelled"
				errorMessage = "Response job was cancelled"
				resultJSON = nil
				resultCiphertext = ""
			}
			upstreamResponseID := responseIDFromJSON(resultJSON)
			updates := map[string]any{
				"status":               status,
				"result_ciphertext":    resultCiphertext,
				"provider_id":          route.Provider.ID,
				"provider_resource_id": routeResourceID(route),
				"provider_model":       route.ProviderModel,
				"upstream_request_id":  usage.UpstreamRequestID,
				"upstream_response_id": upstreamResponseID,
				"error_code":           errorCode,
				"error_message":        errorMessage,
				"token_limit_bucket":   "",
				"minute_request_held":  false,
				"reserved_tokens":      0,
				"admitted_at":          nil,
				"lease_owner":          "",
				"lease_expires_at":     nil,
				"completed_at":         now,
				"expires_at":           expiresAt,
				"updated_at":           now,
			}
			if call.RequestID != "" {
				updates["request_id"] = call.RequestID
			}
			result := tx.Model(&ResponseJob{}).
				Where("id = ? AND status = ? AND lease_owner = ? AND lease_epoch = ?", id, responseJobStatusRunning, owner, epoch).
				Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			job.Status = status
			if call.RequestID != "" {
				job.RequestID = call.RequestID
			}
			job.ResultCiphertext = resultCiphertext
			job.ProviderID = route.Provider.ID
			job.ProviderResourceID = routeResourceID(route)
			job.ProviderModel = route.ProviderModel
			job.UpstreamRequestID = usage.UpstreamRequestID
			job.UpstreamResponseID = upstreamResponseID
			job.ErrorCode = errorCode
			job.ErrorMessage = errorMessage
			job.CompletedAt = &now
			job.ExpiresAt = &expiresAt
			job.LeaseOwner = ""
			job.LeaseExpiresAt = nil
			if err := appendResponseJobEvent(tx, id, responseJobStatusRunning, status, errorCode, owner, now); err != nil {
				return err
			}
		} else if job.Status == responseJobStatusCancelled && job.RequestID == call.RequestID {
			statusCode = 499
			errorCode = "response_cancelled"
			errorMessage = "Response job was cancelled"
		} else {
			return gorm.ErrRecordNotFound
		}

		if call.RequestID != "" {
			var existing int64
			if err := tx.Model(&RequestLog{}).Where("request_id = ?", call.RequestID).Count(&existing).Error; err != nil {
				return err
			}
			if existing == 0 {
				if err := s.finishCallTransaction(tx, call, route, usage, statusCode, errorCode, clientIP, userAgent, now, elapsed); err != nil {
					return err
				}
				observedCompletion = true
			}
		}
		settled = true
		return nil
	})
	s.mu.Unlock()
	if err == gorm.ErrRecordNotFound {
		return job, false, nil
	}
	if err != nil {
		return ResponseJob{}, false, err
	}
	if settled && call.RequestID != "" && observedCompletion {
		s.observeGatewayCall(call, route, usage, statusCode, errorCode, elapsed)
	}
	job.ResultJSON = append([]byte(nil), resultJSON...)
	return job, settled, nil
}

func responseIDFromJSON(payload []byte) string {
	var envelope struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(payload, &envelope)
	return envelope.ID
}

func recordLostResponseJobRequest(tx *gorm.DB, job ResponseJob, statusCode int, errorCode string, now time.Time) error {
	if job.RequestID == "" {
		return nil
	}
	var existing int64
	if err := tx.Model(&RequestLog{}).Where("request_id = ?", job.RequestID).Count(&existing).Error; err != nil || existing > 0 {
		return err
	}
	var latencyMS int64
	if job.StartedAt != nil && now.After(*job.StartedAt) {
		latencyMS = now.Sub(*job.StartedAt).Milliseconds()
	}
	return tx.Create(&RequestLog{
		ID:               NewID("log"),
		RequestID:        job.RequestID,
		ProjectID:        job.ProjectID,
		APIKeyID:         job.APIKeyID,
		AttributedUserID: job.AttributedUserID,
		ModelName:        job.Model,
		StatusCode:       statusCode,
		ErrorCode:        errorCode,
		LatencyMS:        latencyMS,
		CreatedAt:        now,
	}).Error
}

func (s *GormStore) refundUndispatchedResponseJobReservation(tx *gorm.DB, job ResponseJob) error {
	if job.Phase != responseJobPhaseAdmitted || job.TokenLimitBucket == "" || job.ReservedTokens <= 0 {
		return nil
	}
	return s.reconcileAPIKeyMinuteTokens(tx, CallContext{
		Key:              APIKey{ID: job.APIKeyID},
		TokenLimitBucket: job.TokenLimitBucket,
		ReservedTokens:   job.ReservedTokens,
	}, 0)
}

func (s *GormStore) RecoverResponseJobs(resultTTL time.Duration) (int64, int64, int64, error) {
	var requeued int64
	var failed int64
	var cancelled int64
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		var jobs []ResponseJob
		query := tx.Where("status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?", responseJobStatusRunning, now)
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		if err := query.Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if job.CancelRequestedAt != nil {
				expiresAt := now.Add(resultTTL)
				result := tx.Model(&ResponseJob{}).
					Where("id = ? AND status = ? AND lease_epoch = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ? AND cancel_requested_at IS NOT NULL", job.ID, responseJobStatusRunning, job.LeaseEpoch, now).
					Updates(map[string]any{
						"status":              responseJobStatusCancelled,
						"error_code":          "response_cancelled_worker_lost",
						"error_message":       "Response cancellation was retained after worker ownership was lost",
						"token_limit_bucket":  "",
						"minute_request_held": false,
						"reserved_tokens":     0,
						"admitted_at":         nil,
						"lease_owner":         "",
						"lease_expires_at":    nil,
						"completed_at":        now,
						"expires_at":          expiresAt,
						"updated_at":          now,
					})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 1 {
					cancelled++
					if err := s.refundUndispatchedResponseJobReservation(tx, job); err != nil {
						return err
					}
					if job.RequestID != "" {
						if err := tx.Delete(&InFlightLease{}, "id = ?", job.RequestID).Error; err != nil {
							return err
						}
					}
					if err := recordLostResponseJobRequest(tx, job, 499, "response_cancelled_worker_lost", now); err != nil {
						return err
					}
					if err := appendResponseJobEvent(tx, job.ID, responseJobStatusRunning, responseJobStatusCancelled, "response_cancelled_worker_lost", "recovery", now); err != nil {
						return err
					}
				}
				continue
			}
			recoverableClaim := job.Phase == responseJobPhaseClaimed && job.RequestID == ""
			recoverableAdmission := job.Phase == responseJobPhaseAdmitted && job.RequestID != "" && job.AdmittedAt != nil
			if recoverableClaim || recoverableAdmission {
				result := tx.Model(&ResponseJob{}).
					Where("id = ? AND status = ? AND lease_epoch = ? AND phase = ? AND request_id = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ? AND cancel_requested_at IS NULL", job.ID, responseJobStatusRunning, job.LeaseEpoch, job.Phase, job.RequestID, now).
					Updates(map[string]any{
						"status":              responseJobStatusQueued,
						"phase":               responseJobPhaseQueued,
						"request_id":          "",
						"token_limit_bucket":  "",
						"minute_request_held": false,
						"reserved_tokens":     0,
						"admitted_at":         nil,
						"lease_owner":         "",
						"lease_expires_at":    nil,
						"started_at":          nil,
						"updated_at":          now,
					})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 1 {
					if recoverableAdmission {
						if err := s.rollbackResponseJobAdmission(tx, job); err != nil {
							return err
						}
					}
					requeued++
					if err := appendResponseJobEvent(tx, job.ID, responseJobStatusRunning, responseJobStatusQueued, "response_worker_recovered", "recovery", now); err != nil {
						return err
					}
				}
				continue
			}
			expiresAt := now.Add(resultTTL)
			result := tx.Model(&ResponseJob{}).
				Where("id = ? AND status = ? AND lease_epoch = ? AND phase = ? AND request_id = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ? AND cancel_requested_at IS NULL", job.ID, responseJobStatusRunning, job.LeaseEpoch, job.Phase, job.RequestID, now).
				Updates(map[string]any{
					"status":              responseJobStatusFailed,
					"error_code":          "response_execution_lost",
					"error_message":       "Worker ownership was lost after admission; the request was not retried",
					"token_limit_bucket":  "",
					"minute_request_held": false,
					"reserved_tokens":     0,
					"admitted_at":         nil,
					"lease_owner":         "",
					"lease_expires_at":    nil,
					"completed_at":        now,
					"expires_at":          expiresAt,
					"updated_at":          now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				failed++
				if err := s.refundUndispatchedResponseJobReservation(tx, job); err != nil {
					return err
				}
				if job.RequestID != "" {
					if err := tx.Delete(&InFlightLease{}, "id = ?", job.RequestID).Error; err != nil {
						return err
					}
				}
				if err := recordLostResponseJobRequest(tx, job, 500, "response_execution_lost", now); err != nil {
					return err
				}
				if err := appendResponseJobEvent(tx, job.ID, responseJobStatusRunning, responseJobStatusFailed, "response_execution_lost", "recovery", now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return requeued, failed, cancelled, err
}

func (s *GormStore) ExpireResponseJobs() (int64, error) {
	var expired int64
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		var jobs []ResponseJob
		if err := tx.Where("status IN ? AND expires_at IS NOT NULL AND expires_at <= ?", []string{responseJobStatusSucceeded, responseJobStatusFailed, responseJobStatusCancelled}, now).Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			result := tx.Model(&ResponseJob{}).
				Where("id = ? AND status = ?", job.ID, job.Status).
				Updates(map[string]any{
					"status":             responseJobStatusExpired,
					"request_ciphertext": "",
					"result_ciphertext":  "",
					"error_code":         "response_expired",
					"error_message":      "Response job content expired",
					"updated_at":         now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				expired++
				if err := appendResponseJobEvent(tx, job.ID, job.Status, responseJobStatusExpired, "response_expired", "retention", now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return expired, err
}
