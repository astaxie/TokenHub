package server

import (
	"context"
	"net/http"

	"tokenhub/backend/internal/dbschema"
)

// SchemaLedgerStatus exposes the migration ledger view for read-only status
// surfaces.
func (s *GormStore) SchemaLedgerStatus(ctx context.Context) (dbschema.Status, error) {
	sqlDB, err := s.db.DB()
	if err != nil {
		return dbschema.Status{}, err
	}
	runner, err := dbschema.NewRunner(sqlDB, dbschema.Dialect(s.dbDriver), SchemaMigrationRegistry())
	if err != nil {
		return dbschema.Status{}, err
	}
	return runner.Status(ctx)
}

// DataBackfillStates exposes the data backfill ledger for read-only status
// surfaces.
func (s *GormStore) DataBackfillStates(ctx context.Context) ([]dbschema.BackfillState, error) {
	sqlDB, err := s.db.DB()
	if err != nil {
		return nil, err
	}
	executor, err := dbschema.NewBackfillExecutor(sqlDB, dbschema.Dialect(s.dbDriver), nil)
	if err != nil {
		return nil, err
	}
	return executor.Status(ctx)
}

// Migration and backfill ledger rows are mapped to explicit DTOs: the runner
// types carry function fields that encoding/json cannot serialize, and the
// admin contract uses snake_case keys.
type schemaStatusPendingMigration struct {
	Version int64  `json:"version"`
	Name    string `json:"name"`
	Phase   string `json:"phase"`
}

type schemaStatusBackfill struct {
	ID        string `json:"id"`
	Mode      string `json:"mode"`
	State     string `json:"state"`
	Remaining int64  `json:"remaining"`
}

// handleAdminSchemaStatus serves the read-only database evolution status for
// the admin console: ledger state, readiness reason, pending migrations,
// data-backfill progress, live instances, and this release's compatibility
// manifest (the Admin API only ever shows status; contract and
// repair stay on the CLI).
func (s *Server) handleAdminSchemaStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "system", r.Method); !ok {
		return
	}
	payload := map[string]any{
		"compatibility":              CurrentCompatibilityManifest(),
		"ready":                      true,
		"schema_version":             int64(0),
		"reason":                     "",
		"reason_code":                "",
		"dirty_version":              int64(0),
		"pending_expand":             []schemaStatusPendingMigration{},
		"pending_contract":           []schemaStatusPendingMigration{},
		"blocking_backfills_pending": []string{},
		"backfills":                  []schemaStatusBackfill{},
		"instances":                  []InstanceHeartbeat{},
	}
	if gormStore, ok := s.store.(*GormStore); ok {
		ctx := r.Context()
		state := gormStore.DatabaseEvolutionStatus(ctx)
		payload["ready"] = state.Ready
		payload["reason"] = state.Reason
		payload["reason_code"] = state.ReasonCode
		payload["schema_version"] = state.SchemaVersion
		payload["blocking_backfills_pending"] = state.BlockingBackfillsPending
		if ledger, err := gormStore.SchemaLedgerStatus(ctx); err == nil {
			payload["schema_version"] = ledger.CurrentVersion
			payload["dirty_version"] = ledger.DirtyVersion
			payload["pending_expand"] = mapPending(ledger.PendingExpand)
			payload["pending_contract"] = mapPending(ledger.PendingContract)
		}
		if backfills, err := gormStore.DataBackfillStates(ctx); err == nil {
			mapped := make([]schemaStatusBackfill, 0, len(backfills))
			for _, state := range backfills {
				mapped = append(mapped, schemaStatusBackfill{ID: state.ID, Mode: string(state.Mode), State: state.State, Remaining: state.Remaining})
			}
			payload["backfills"] = mapped
		}
		if heartbeats, err := gormStore.ListInstanceHeartbeats(ctx); err == nil {
			payload["instances"] = heartbeats
		}
	}
	writeJSON(w, http.StatusOK, payload)
}

func mapPending(migrations []dbschema.Migration) []schemaStatusPendingMigration {
	mapped := make([]schemaStatusPendingMigration, 0, len(migrations))
	for _, m := range migrations {
		mapped = append(mapped, schemaStatusPendingMigration{Version: m.Version, Name: m.Name, Phase: string(m.Phase)})
	}
	return mapped
}

// annotateRollbackCompatibility marks each rollback candidate with its
// database compatibility verdict so the admin UI can disable unknown or
// incompatible targets.
func (s *Server) annotateRollbackCompatibility(ctx context.Context, versions []rollbackVersionInfo) {
	for i := range versions {
		verdict := s.rollbackCompatibility(ctx, versions[i].Version)
		versions[i].Compatibility = verdict.Compatibility
		versions[i].CompatibilityReason = verdict.Reason
		versions[i].CompatibilityReasonCode = verdict.ReasonCode
		versions[i].CompatibilityReasonParams = verdict.ReasonParams
	}
}
