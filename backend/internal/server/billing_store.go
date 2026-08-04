package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

func (s *GormStore) CreateBillingConnector(connector BillingConnector) (BillingConnector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if connector.ID == "" {
		connector.ID = NewID("bcon")
	}
	if connector.Status == "" {
		connector.Status = StatusActive
	}
	if connector.ScheduleIntervalMinutes < 0 {
		connector.ScheduleIntervalMinutes = 0
	}
	if connector.Config == nil {
		connector.Config = map[string]string{}
	}
	if connector.CreatedAt.IsZero() {
		connector.CreatedAt = now
	}
	connector.UpdatedAt = now
	connector.NextSyncAt = nextBillingSyncAt(connector, now)
	if connector.Credentials != nil {
		ciphertext, err := s.encryptBillingCredentials(connector.Credentials)
		if err != nil {
			return BillingConnector{}, err
		}
		connector.CredentialCiphertext = ciphertext
	}
	connector.Credentials = nil
	if err := s.db.Create(&connector).Error; err != nil {
		return BillingConnector{}, writeConflict(err, "billing_connector_conflict", "Billing connector already exists")
	}
	return billingConnectorSummary(connector), nil
}

func (s *GormStore) ListBillingConnectors() []BillingConnector {
	var connectors []BillingConnector
	_ = s.db.Order("created_at asc").Find(&connectors).Error
	for index := range connectors {
		connectors[index] = billingConnectorSummary(connectors[index])
	}
	return connectors
}

func (s *GormStore) GetBillingConnector(id string, includeCredentials bool) (BillingConnector, error) {
	var connector BillingConnector
	if err := s.db.First(&connector, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return BillingConnector{}, notFound(err, "billing_connector_not_found", "Billing connector not found")
	}
	if includeCredentials && connector.CredentialCiphertext != "" {
		credentials, err := s.decryptBillingCredentials(connector.CredentialCiphertext)
		if err != nil {
			return BillingConnector{}, err
		}
		connector.Credentials = credentials
	}
	return billingConnectorSummaryWithCredentials(connector, includeCredentials), nil
}

func (s *GormStore) UpdateBillingConnector(id string, patch BillingConnector) (BillingConnector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var connector BillingConnector
	if err := s.db.First(&connector, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return BillingConnector{}, notFound(err, "billing_connector_not_found", "Billing connector not found")
	}
	if patch.Name != "" {
		connector.Name = patch.Name
	}
	if patch.BaseURL != "" {
		connector.BaseURL = patch.BaseURL
	}
	if patch.Status != "" {
		connector.Status = patch.Status
	}
	if patch.ScheduleIntervalMinutes >= 0 {
		connector.ScheduleIntervalMinutes = patch.ScheduleIntervalMinutes
	}
	if patch.Config != nil {
		connector.Config = patch.Config
	}
	if patch.Credentials != nil {
		ciphertext, err := s.encryptBillingCredentials(patch.Credentials)
		if err != nil {
			return BillingConnector{}, err
		}
		connector.CredentialCiphertext = ciphertext
	}
	connector.UpdatedAt = time.Now().UTC()
	connector.NextSyncAt = nextBillingSyncAt(connector, connector.UpdatedAt)
	if err := s.db.Save(&connector).Error; err != nil {
		return BillingConnector{}, err
	}
	return billingConnectorSummary(connector), nil
}

func (s *GormStore) DeleteBillingConnector(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	var connector BillingConnector
	if err := s.db.First(&connector, "id = ?", id).Error; err != nil {
		return notFound(err, "billing_connector_not_found", "Billing connector not found")
	}
	// Billing records, raw snapshots, and sync runs are audit evidence. Keep
	// them addressable by connector_id after the mutable connector is removed.
	return s.db.Delete(&connector).Error
}

func (s *GormStore) StartBillingSyncRun(run BillingSyncRun) (BillingSyncRun, error) {
	if run.ID == "" {
		run.ID = NewID("bsync")
	}
	if run.Status == "" {
		run.Status = BillingSyncRunning
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	return run, s.db.Create(&run).Error
}

func (s *GormStore) SaveBillingPage(connectorID string, checkpoint string, records []BillingRecord) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inserted := 0
	updated := 0
	err := s.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		for _, candidate := range records {
			candidate.ConnectorID = connectorID
			if strings.TrimSpace(candidate.ExternalID) == "" {
				return NewHTTPError(502, "billing_record_invalid", "Billing source returned a record without an external identifier")
			}
			snapshot, err := s.storeBillingSnapshot(tx, connectorID, candidate.ExternalID, candidate.RawPayload, now)
			if err != nil {
				return err
			}
			candidate.RawSnapshotID = snapshot.ID
			candidate.RawPayload = ""

			var existing BillingRecord
			err = tx.First(&existing, "connector_id = ? AND external_id = ?", connectorID, candidate.ExternalID).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				candidate.ID = NewID("bill")
				candidate.CreatedAt = now
				candidate.UpdatedAt = now
				if err := tx.Create(&candidate).Error; err != nil {
					return err
				}
				inserted++
			case err != nil:
				return err
			default:
				candidate.ID = existing.ID
				candidate.CreatedAt = existing.CreatedAt
				candidate.UpdatedAt = now
				if err := tx.Save(&candidate).Error; err != nil {
					return err
				}
				updated++
			}
		}
		return tx.Model(&BillingConnector{}).Where("id = ?", connectorID).Updates(map[string]any{
			"checkpoint": checkpoint,
			"updated_at": time.Now().UTC(),
		}).Error
	})
	return inserted, updated, err
}

func (s *GormStore) storeBillingSnapshot(tx *gorm.DB, connectorID string, externalID string, rawPayload string, now time.Time) (BillingRawSnapshot, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(rawPayload)))
	var snapshot BillingRawSnapshot
	err := tx.First(&snapshot, "connector_id = ? AND external_id = ? AND payload_hash = ?", connectorID, externalID, hash).Error
	if err == nil {
		return snapshot, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return BillingRawSnapshot{}, err
	}
	ciphertext, err := s.encryptSecretStrict(rawPayload)
	if err != nil {
		return BillingRawSnapshot{}, NewHTTPError(500, "billing_snapshot_encryption_failed", "Billing snapshot could not be encrypted")
	}
	snapshot = BillingRawSnapshot{
		ID:                NewID("bsnap"),
		ConnectorID:       connectorID,
		ExternalID:        externalID,
		PayloadHash:       hash,
		PayloadCiphertext: ciphertext,
		CapturedAt:        now,
	}
	return snapshot, tx.Create(&snapshot).Error
}

func (s *GormStore) FinishBillingSyncRun(run BillingSyncRun) (BillingSyncRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if run.FinishedAt == nil {
		finishedAt := time.Now().UTC()
		run.FinishedAt = &finishedAt
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&run).Error; err != nil {
			return err
		}
		var connector BillingConnector
		if err := tx.First(&connector, "id = ?", run.ConnectorID).Error; err != nil {
			return notFound(err, "billing_connector_not_found", "Billing connector not found")
		}
		connector.LastSyncStatus = run.Status
		connector.LastSyncMessage = run.ErrorMessage
		connector.LastSyncAt = run.FinishedAt
		connector.UpdatedAt = *run.FinishedAt
		if run.Status == BillingSyncSucceeded {
			connector.Checkpoint = ""
			through := run.RangeEnd
			connector.LastSyncedThrough = &through
		}
		connector.NextSyncAt = nextBillingSyncAt(connector, *run.FinishedAt)
		return tx.Save(&connector).Error
	})
	return run, err
}

func (s *GormStore) ListBillingRecords(connectorID string, limit int) []BillingRecord {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	query := s.db.Order("usage_start_at desc").Limit(limit)
	if connectorID = strings.TrimSpace(connectorID); connectorID != "" {
		query = query.Where("connector_id = ?", connectorID)
	}
	var records []BillingRecord
	_ = query.Find(&records).Error
	for index := range records {
		records[index].RawPayload = ""
	}
	return records
}

func (s *GormStore) ListBillingSyncRuns(connectorID string, limit int) []BillingSyncRun {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := s.db.Order("started_at desc").Limit(limit)
	if connectorID = strings.TrimSpace(connectorID); connectorID != "" {
		query = query.Where("connector_id = ?", connectorID)
	}
	var runs []BillingSyncRun
	_ = query.Find(&runs).Error
	return runs
}

func (s *GormStore) ListDueBillingConnectors(now time.Time, limit int) []BillingConnector {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var connectors []BillingConnector
	_ = s.db.Where("status = ? AND schedule_interval_minutes > 0 AND next_sync_at IS NOT NULL AND next_sync_at <= ?", StatusActive, now.UTC()).
		Order("next_sync_at asc").Limit(limit).Find(&connectors).Error
	for index := range connectors {
		connectors[index] = billingConnectorSummary(connectors[index])
	}
	return connectors
}

func (s *GormStore) RecordScheduledBillingAudit(run BillingSyncRun) {
	status := "success"
	if run.Status != BillingSyncSucceeded {
		status = "failed"
	}
	s.RecordAuditEvent(AuditEvent{
		ActorUserID:   "system",
		ActorName:     "TokenHub Scheduler",
		ActorRole:     "system",
		Action:        "sync",
		ResourceType:  "billing_connector",
		ResourceID:    run.ConnectorID,
		Status:        status,
		Message:       run.ErrorCode,
		AfterSnapshot: snapshotJSON(run),
	})
}

func (s *GormStore) encryptBillingCredentials(credentials map[string]string) (string, error) {
	cleaned := make(map[string]string, len(credentials))
	for key, value := range credentials {
		key = strings.TrimSpace(key)
		if key != "" && strings.TrimSpace(value) != "" {
			cleaned[key] = value
		}
	}
	data, err := json.Marshal(cleaned)
	if err != nil {
		return "", err
	}
	ciphertext, err := s.encryptSecretStrict(string(data))
	if err != nil {
		return "", NewHTTPError(500, "billing_credential_encryption_failed", "Billing connector credentials could not be encrypted")
	}
	return ciphertext, nil
}

func (s *GormStore) decryptBillingCredentials(ciphertext string) (map[string]string, error) {
	credentials := map[string]string{}
	plaintext := s.decryptSecret(ciphertext)
	if plaintext == "" {
		return credentials, NewHTTPError(500, "billing_credentials_unavailable", "Billing connector credentials could not be decrypted")
	}
	if err := json.Unmarshal([]byte(plaintext), &credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

func (s *GormStore) encryptSecretStrict(plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" || strings.HasPrefix(plaintext, "enc:v1:") {
		return "", errors.New("billing protected value must be non-empty plaintext")
	}
	ciphertext := s.encryptSecret(plaintext)
	if ciphertext == plaintext || !strings.HasPrefix(ciphertext, "enc:v1:") {
		return "", errors.New("billing protected value encryption failed")
	}
	return ciphertext, nil
}

func billingConnectorSummary(connector BillingConnector) BillingConnector {
	return billingConnectorSummaryWithCredentials(connector, false)
}

func billingConnectorSummaryWithCredentials(connector BillingConnector, includeCredentials bool) BillingConnector {
	fields := make([]string, 0)
	if includeCredentials {
		for key := range connector.Credentials {
			fields = append(fields, key)
		}
	} else if connector.CredentialCiphertext != "" {
		// Credential names are intentionally not stored separately: the public
		// summary only needs to say that a protected credential set exists.
		fields = nil
	}
	sort.Strings(fields)
	connector.CredentialsConfigured = connector.CredentialCiphertext != "" || len(connector.Credentials) > 0
	connector.CredentialFields = fields
	connector.CredentialCiphertext = ""
	if !includeCredentials {
		connector.Credentials = nil
	}
	return connector
}

func nextBillingSyncAt(connector BillingConnector, from time.Time) *time.Time {
	if connector.Status != StatusActive || connector.ScheduleIntervalMinutes <= 0 {
		return nil
	}
	next := from.Add(time.Duration(connector.ScheduleIntervalMinutes) * time.Minute)
	return &next
}

var _ BillingStore = (*GormStore)(nil)
