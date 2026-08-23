package persistence

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"tokenhub/backend/internal/billing"
)

type AuditRecorder func(billing.SyncRun)

// Store is the GORM adapter for billing persistence ports.
type Store struct {
	db          *gorm.DB
	mu          *sync.Mutex
	secretKey   string
	recordAudit AuditRecorder
}

var _ billing.Store = (*Store)(nil)
var _ billing.ManagementStore = (*Store)(nil)

func NewStore(db *gorm.DB, mu *sync.Mutex, secretKey string, recordAudit AuditRecorder) *Store {
	if mu == nil {
		mu = &sync.Mutex{}
	}
	return &Store{db: db, mu: mu, secretKey: secretKey, recordAudit: recordAudit}
}

func (s *Store) CreateBillingConnector(connector billing.Connector) (billing.Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if connector.ID == "" {
		connector.ID = newID("bcon")
	}
	if connector.Status == "" {
		connector.Status = billing.StatusActive
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
	connector.NextSyncAt = nextSyncAt(connector, now)
	if connector.Credentials != nil {
		ciphertext, err := s.encryptCredentials(connector.Credentials)
		if err != nil {
			return billing.Connector{}, err
		}
		connector.CredentialCiphertext = ciphertext
	}
	connector.Credentials = nil
	row := connectorRow(connector)
	if err := s.db.Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return billing.Connector{}, billing.WrapError(err, billing.ErrorConflict, "billing_connector_conflict", "Billing connector already exists")
		}
		return billing.Connector{}, err
	}
	return connectorSummary(domainConnector(row)), nil
}

func (s *Store) ListBillingConnectors() []billing.Connector {
	var rows []ConnectorRow
	_ = s.db.Order("created_at asc").Find(&rows).Error
	connectors := make([]billing.Connector, len(rows))
	for index, row := range rows {
		connectors[index] = connectorSummary(domainConnector(row))
	}
	return connectors
}

func (s *Store) GetBillingConnector(id string, includeCredentials bool) (billing.Connector, error) {
	var row ConnectorRow
	if err := s.db.First(&row, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return billing.Connector{}, notFound(err, "billing_connector_not_found", "Billing connector not found")
	}
	connector := domainConnector(row)
	if includeCredentials && connector.CredentialCiphertext != "" {
		credentials, err := s.decryptCredentials(connector.CredentialCiphertext)
		if err != nil {
			return billing.Connector{}, err
		}
		connector.Credentials = credentials
	}
	return connectorSummaryWithCredentials(connector, includeCredentials), nil
}

func (s *Store) UpdateBillingConnector(id string, patch billing.Connector) (billing.Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var row ConnectorRow
	if err := s.db.First(&row, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return billing.Connector{}, notFound(err, "billing_connector_not_found", "Billing connector not found")
	}
	connector := domainConnector(row)
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
		connector.Config = cloneStringMap(patch.Config)
	}
	if patch.Credentials != nil {
		ciphertext, err := s.encryptCredentials(patch.Credentials)
		if err != nil {
			return billing.Connector{}, err
		}
		connector.CredentialCiphertext = ciphertext
	}
	connector.UpdatedAt = time.Now().UTC()
	connector.NextSyncAt = nextSyncAt(connector, connector.UpdatedAt)
	updatedRow := connectorRow(connector)
	if err := s.db.Save(&updatedRow).Error; err != nil {
		return billing.Connector{}, err
	}
	return connectorSummary(domainConnector(updatedRow)), nil
}

func (s *Store) DeleteBillingConnector(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	var row ConnectorRow
	if err := s.db.First(&row, "id = ?", id).Error; err != nil {
		return notFound(err, "billing_connector_not_found", "Billing connector not found")
	}
	return s.db.Delete(&row).Error
}

func (s *Store) StartBillingSyncRun(run billing.SyncRun) (billing.SyncRun, error) {
	if run.ID == "" {
		run.ID = newID("bsync")
	}
	if run.Status == "" {
		run.Status = billing.SyncRunning
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	row := syncRunRow(run)
	return run, s.db.Create(&row).Error
}

func (s *Store) SaveBillingPage(connectorID, checkpoint string, records []billing.Record) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inserted := 0
	updated := 0
	err := s.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		for _, candidate := range records {
			candidate.ConnectorID = connectorID
			if strings.TrimSpace(candidate.ExternalID) == "" {
				return billing.NewError(billing.ErrorUpstream, "billing_record_invalid", "Billing source returned a record without an external identifier")
			}
			snapshot, err := s.storeSnapshot(tx, connectorID, candidate.ExternalID, candidate.RawPayload, now)
			if err != nil {
				return err
			}
			candidate.RawSnapshotID = snapshot.ID
			candidate.RawPayload = ""
			var existing RecordRow
			err = tx.First(&existing, "connector_id = ? AND external_id = ?", connectorID, candidate.ExternalID).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				candidate.ID = newID("bill")
				candidate.CreatedAt = now
				candidate.UpdatedAt = now
				row := recordRow(candidate)
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				inserted++
			case err != nil:
				return err
			default:
				candidate.ID = existing.ID
				candidate.CreatedAt = existing.CreatedAt
				candidate.UpdatedAt = now
				row := recordRow(candidate)
				if err := tx.Save(&row).Error; err != nil {
					return err
				}
				updated++
			}
		}
		return tx.Model(&ConnectorRow{}).Where("id = ?", connectorID).Updates(map[string]any{
			"checkpoint": checkpoint,
			"updated_at": time.Now().UTC(),
		}).Error
	})
	return inserted, updated, err
}

func (s *Store) storeSnapshot(tx *gorm.DB, connectorID, externalID, rawPayload string, now time.Time) (RawSnapshotRow, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(rawPayload)))
	var snapshot RawSnapshotRow
	err := tx.First(&snapshot, "connector_id = ? AND external_id = ? AND payload_hash = ?", connectorID, externalID, hash).Error
	if err == nil {
		return snapshot, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return RawSnapshotRow{}, err
	}
	ciphertext, err := s.encryptStrict(rawPayload)
	if err != nil {
		return RawSnapshotRow{}, billing.WrapError(err, billing.ErrorUpstream, "billing_snapshot_encryption_failed", "Billing snapshot could not be encrypted")
	}
	snapshot = RawSnapshotRow{
		ID: newID("bsnap"), ConnectorID: connectorID, ExternalID: externalID,
		PayloadHash: hash, PayloadCiphertext: ciphertext, CapturedAt: now,
	}
	return snapshot, tx.Create(&snapshot).Error
}

func (s *Store) FinishBillingSyncRun(run billing.SyncRun) (billing.SyncRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if run.FinishedAt == nil {
		finishedAt := time.Now().UTC()
		run.FinishedAt = &finishedAt
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		row := syncRunRow(run)
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		var connectorRowValue ConnectorRow
		if err := tx.First(&connectorRowValue, "id = ?", run.ConnectorID).Error; err != nil {
			return notFound(err, "billing_connector_not_found", "Billing connector not found")
		}
		connector := domainConnector(connectorRowValue)
		connector.LastSyncStatus = run.Status
		connector.LastSyncMessage = run.ErrorMessage
		connector.LastSyncAt = cloneTimePointer(run.FinishedAt)
		connector.UpdatedAt = *run.FinishedAt
		if run.Status == billing.SyncSucceeded {
			connector.Checkpoint = ""
			through := run.RangeEnd
			connector.LastSyncedThrough = &through
		}
		connector.NextSyncAt = nextSyncAt(connector, *run.FinishedAt)
		updated := connectorRow(connector)
		return tx.Save(&updated).Error
	})
	return run, err
}

func (s *Store) ListBillingRecords(connectorID string, limit int) []billing.Record {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	query := s.db.Order("usage_start_at desc").Limit(limit)
	if connectorID = strings.TrimSpace(connectorID); connectorID != "" {
		query = query.Where("connector_id = ?", connectorID)
	}
	var rows []RecordRow
	_ = query.Find(&rows).Error
	records := make([]billing.Record, len(rows))
	for index, row := range rows {
		records[index] = domainRecord(row)
	}
	return records
}

// ListBillingRecordsInRange returns records in deterministic reconciliation
// order without exposing persistence rows to the consumer.
func (s *Store) ListBillingRecordsInRange(connectorID string, from, to time.Time) ([]billing.Record, error) {
	connectorID = strings.TrimSpace(connectorID)
	var rows []RecordRow
	if err := s.db.Where("connector_id = ? AND usage_start_at >= ? AND usage_start_at < ?", connectorID, from.UTC(), to.UTC()).
		Order("usage_start_at asc, id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	records := make([]billing.Record, len(rows))
	for index, row := range rows {
		records[index] = domainRecord(row)
	}
	return records, nil
}

func (s *Store) ListBillingSyncRuns(connectorID string, limit int) []billing.SyncRun {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := s.db.Order("started_at desc").Limit(limit)
	if connectorID = strings.TrimSpace(connectorID); connectorID != "" {
		query = query.Where("connector_id = ?", connectorID)
	}
	var rows []SyncRunRow
	_ = query.Find(&rows).Error
	runs := make([]billing.SyncRun, len(rows))
	for index, row := range rows {
		runs[index] = domainSyncRun(row)
	}
	return runs
}

func (s *Store) ListDueBillingConnectors(now time.Time, limit int) []billing.Connector {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var rows []ConnectorRow
	_ = s.db.Where("status = ? AND schedule_interval_minutes > 0 AND next_sync_at IS NOT NULL AND next_sync_at <= ?", billing.StatusActive, now.UTC()).
		Order("next_sync_at asc").Limit(limit).Find(&rows).Error
	connectors := make([]billing.Connector, len(rows))
	for index, row := range rows {
		connectors[index] = connectorSummary(domainConnector(row))
	}
	return connectors
}

func (s *Store) RecordScheduledBillingAudit(run billing.SyncRun) {
	if s.recordAudit != nil {
		s.recordAudit(run)
	}
}

func (s *Store) encryptCredentials(credentials map[string]string) (string, error) {
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
	ciphertext, err := s.encryptStrict(string(data))
	if err != nil {
		return "", billing.WrapError(err, billing.ErrorUpstream, "billing_credential_encryption_failed", "Billing connector credentials could not be encrypted")
	}
	return ciphertext, nil
}

func (s *Store) decryptCredentials(ciphertext string) (map[string]string, error) {
	credentials := map[string]string{}
	plaintext := decryptSecret(s.secretKey, ciphertext)
	if plaintext == "" {
		return credentials, billing.NewError(billing.ErrorUpstream, "billing_credentials_unavailable", "Billing connector credentials could not be decrypted")
	}
	if err := json.Unmarshal([]byte(plaintext), &credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

func (s *Store) encryptStrict(plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" || strings.HasPrefix(plaintext, "enc:v1:") {
		return "", errors.New("protected value must be non-empty plaintext")
	}
	ciphertext := encryptSecret(s.secretKey, plaintext)
	if ciphertext == plaintext || !strings.HasPrefix(ciphertext, "enc:v1:") {
		return "", errors.New("protected value encryption failed")
	}
	return ciphertext, nil
}

func connectorSummary(connector billing.Connector) billing.Connector {
	return connectorSummaryWithCredentials(connector, false)
}

func connectorSummaryWithCredentials(connector billing.Connector, includeCredentials bool) billing.Connector {
	fields := make([]string, 0)
	if includeCredentials {
		for key := range connector.Credentials {
			fields = append(fields, key)
		}
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

func nextSyncAt(connector billing.Connector, from time.Time) *time.Time {
	if connector.Status != billing.StatusActive || connector.ScheduleIntervalMinutes <= 0 {
		return nil
	}
	next := from.Add(time.Duration(connector.ScheduleIntervalMinutes) * time.Minute)
	return &next
}

func notFound(err error, code, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return billing.WrapError(err, billing.ErrorNotFound, code, message)
	}
	return err
}

func newID(prefix string) string {
	var buffer [12]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(buffer[:])
}

func encryptSecret(secretKey, secret string) string {
	if strings.TrimSpace(secret) == "" || strings.HasPrefix(secret, "enc:v1:") {
		return secret
	}
	block, err := aes.NewCipher(secretKeyBytes(secretKey))
	if err != nil {
		return secret
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return secret
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return secret
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(secret), nil)
	return "enc:v1:" + base64.RawURLEncoding.EncodeToString(append(nonce, ciphertext...))
}

func decryptSecret(secretKey, secret string) string {
	if !strings.HasPrefix(secret, "enc:v1:") {
		return secret
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, "enc:v1:"))
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(secretKeyBytes(secretKey))
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(data) < gcm.NonceSize() {
		return ""
	}
	plaintext, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return ""
	}
	return string(plaintext)
}

func secretKeyBytes(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}
