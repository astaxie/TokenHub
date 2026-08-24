package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"

	"tokenhub/backend/internal/dbschema"
)

const (
	// InstanceHeartbeatInterval is how often a running instance refreshes its
	// heartbeat row.
	InstanceHeartbeatInterval = 30 * time.Second
	// InstanceHeartbeatTTL is how long a heartbeat row counts as live; the db
	// CLI's cluster preflight uses the same window.
	InstanceHeartbeatTTL = 90 * time.Second
)

// InstanceHeartbeat is one published instance row (every running
// instance publishes its release through a TTL'd database heartbeat).
type InstanceHeartbeat struct {
	InstanceID string `json:"instance_id"`
	Release    string `json:"release"`
	StartedAt  string `json:"started_at"`
	LastSeen   string `json:"last_seen"`
}

// Heartbeat publication states: off before Start, ok while rows publish,
// failing when refreshes keep failing. A serving instance that stops
// publishing must pull itself out of readiness, otherwise contract
// maintenance would see zero live instances.
const (
	heartbeatOff     = int32(0)
	heartbeatOK      = int32(1)
	heartbeatFailing = int32(2)
)

const instanceHeartbeatTableDDL = `CREATE TABLE IF NOT EXISTS instance_heartbeats (
	instance_id TEXT PRIMARY KEY,
	release TEXT NOT NULL,
	started_at TEXT NOT NULL,
	last_seen TEXT NOT NULL
)`

// upsertInstanceHeartbeat publishes or refreshes one instance row.
func upsertInstanceHeartbeat(db *gorm.DB, instanceID, release string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return db.Exec(`INSERT INTO instance_heartbeats (instance_id, release, started_at, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (instance_id) DO UPDATE SET release = excluded.release, last_seen = excluded.last_seen`,
		instanceID, release, now, now).Error
}

// publishInitialInstanceHeartbeat creates the heartbeat table and publishes
// the first row for this instance. OpenStoreWithConfig calls it while the
// schema migration lock is still held: once that lock is released, a
// `tokenhub db contract` preflight must never observe a serving boot-in-progress
// as zero live instances. The returned ID is kept on the store so
// StartInstanceHeartbeat refreshes this row instead of adding another one.
func publishInitialInstanceHeartbeat(db *gorm.DB, release string) (string, error) {
	if err := db.Exec(instanceHeartbeatTableDDL).Error; err != nil {
		return "", err
	}
	instanceID := NewID("instance")
	if err := upsertInstanceHeartbeat(db, instanceID, release); err != nil {
		return "", err
	}
	return instanceID, nil
}

// StartInstanceHeartbeat publishes this instance and refreshes it until the
// returned stop function removes the row. Publication failures fail closed via
// DatabaseEvolutionStatus so contract maintenance can never miss a serving
// instance merely because its heartbeat row is absent.
func (s *GormStore) StartInstanceHeartbeat(release string) (stop func()) {
	instanceID := s.instanceHeartbeatID
	if instanceID == "" {
		instanceID = NewID("instance")
	}
	if err := s.db.Exec(instanceHeartbeatTableDDL).Error; err != nil {
		if s.heartbeatState != nil {
			s.heartbeatState.Store(heartbeatFailing)
		}
		log.Printf("[tokenhub] failed to create instance heartbeat table: %v", err)
		return func() {}
	}
	refresh := func() error {
		return upsertInstanceHeartbeat(s.db, instanceID, release)
	}
	publish := func() {
		if err := refresh(); err != nil {
			if s.heartbeatState != nil {
				s.heartbeatState.Store(heartbeatFailing)
			}
			log.Printf("[tokenhub] failed to publish instance heartbeat: %v", err)
		} else if s.heartbeatState != nil {
			s.heartbeatState.Store(heartbeatOK)
		}
	}
	publish()
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(InstanceHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				publish()
			}
		}
	}()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			close(done)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.db.WithContext(ctx).
				Exec("DELETE FROM instance_heartbeats WHERE instance_id = ?", instanceID).Error; err != nil {
				log.Printf("[tokenhub] failed to remove instance heartbeat: %v", err)
			}
		})
	}
}

// ListInstanceHeartbeats returns every published heartbeat row.
func (s *GormStore) ListInstanceHeartbeats(ctx context.Context) ([]InstanceHeartbeat, error) {
	rows, err := s.db.WithContext(ctx).
		Raw("SELECT instance_id, release, started_at, last_seen FROM instance_heartbeats ORDER BY instance_id").Rows()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var heartbeats []InstanceHeartbeat
	for rows.Next() {
		var heartbeat InstanceHeartbeat
		if err := rows.Scan(&heartbeat.InstanceID, &heartbeat.Release, &heartbeat.StartedAt, &heartbeat.LastSeen); err != nil {
			return nil, err
		}
		heartbeats = append(heartbeats, heartbeat)
	}
	return heartbeats, rows.Err()
}

// DatabaseEvolutionStatus reports whether the database evolution state allows
// serving: the migration ledger verifies (no dirty migration, no checksum or
// unknown-version drift) and every registered blocking data backfill is
// complete. Pending online backfills never affect readiness.
// ReasonCode is the stable machine identifier of the failure for localized
// status surfaces; Reason stays the English diagnostic for logs.
type DatabaseEvolutionStatus struct {
	Ready                    bool
	ReasonCode               string
	Reason                   string
	SchemaVersion            int64
	BlockingBackfillsPending []string
}

func (s *GormStore) DatabaseEvolutionStatus(ctx context.Context) DatabaseEvolutionStatus {
	sqlDB, err := s.db.DB()
	if err != nil {
		return DatabaseEvolutionStatus{ReasonCode: "handle_unavailable", Reason: fmt.Sprintf("database handle unavailable: %v", err)}
	}
	runner, err := dbschema.NewRunner(sqlDB, dbschema.Dialect(s.dbDriver), SchemaMigrationRegistry())
	if err != nil {
		return DatabaseEvolutionStatus{ReasonCode: "runner_error", Reason: fmt.Sprintf("migration runner: %v", err)}
	}
	status, err := runner.Status(ctx)
	if err != nil {
		return DatabaseEvolutionStatus{ReasonCode: "ledger_unreadable", Reason: fmt.Sprintf("migration ledger unreadable: %v", err)}
	}
	if !status.BaselineRecorded {
		return DatabaseEvolutionStatus{ReasonCode: "baseline_missing", Reason: "database has no adoption baseline; start the server once to adopt it"}
	}
	if s.heartbeatState != nil && s.heartbeatState.Load() == heartbeatFailing {
		return DatabaseEvolutionStatus{
			ReasonCode:    "heartbeat_failing",
			Reason:        "instance heartbeat is not publishing; contract maintenance cannot see this serving instance",
			SchemaVersion: status.CurrentVersion,
		}
	}
	if status.Dirty {
		return DatabaseEvolutionStatus{
			ReasonCode:    "dirty_migration",
			Reason:        fmt.Sprintf("dirty schema migration at version %d; repair required", status.DirtyVersion),
			SchemaVersion: status.CurrentVersion,
		}
	}
	if err := runner.Verify(ctx); err != nil {
		return DatabaseEvolutionStatus{
			ReasonCode:    "ledger_verification_failed",
			Reason:        fmt.Sprintf("migration ledger verification failed: %v", err),
			SchemaVersion: status.CurrentVersion,
		}
	}
	if len(status.PendingExpand) > 0 {
		return DatabaseEvolutionStatus{
			ReasonCode:    "expand_pending",
			Reason:        fmt.Sprintf("%d expand migration(s) pending; run tokenhub db migrate or restart the server", len(status.PendingExpand)),
			SchemaVersion: status.CurrentVersion,
		}
	}
	pending, err := pendingBlockingBackfills(ctx, sqlDB, s.dbDriver)
	if err != nil {
		return DatabaseEvolutionStatus{
			ReasonCode:    "backfill_ledger_unreadable",
			Reason:        fmt.Sprintf("data backfill ledger unreadable: %v", err),
			SchemaVersion: status.CurrentVersion,
		}
	}
	if len(pending) > 0 {
		return DatabaseEvolutionStatus{
			ReasonCode:               "blocking_backfills_pending",
			Reason:                   fmt.Sprintf("blocking data backfills incomplete: %v", pending),
			SchemaVersion:            status.CurrentVersion,
			BlockingBackfillsPending: pending,
		}
	}
	return DatabaseEvolutionStatus{Ready: true, SchemaVersion: status.CurrentVersion}
}

// pendingBlockingBackfills returns the IDs of incomplete blocking backfills.
// The bridge release registers none; the executor still ensures the ledger
// table exists so status surfaces stay available.
func pendingBlockingBackfills(ctx context.Context, db *sql.DB, driver string) ([]string, error) {
	executor, err := dbschema.NewBackfillExecutor(db, dbschema.Dialect(driver), nil)
	if err != nil {
		return nil, err
	}
	states, err := executor.Status(ctx)
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, state := range states {
		if state.Mode == dbschema.BackfillBlocking && state.State != dbschema.BackfillStateComplete {
			pending = append(pending, state.ID)
		}
	}
	return pending, nil
}
