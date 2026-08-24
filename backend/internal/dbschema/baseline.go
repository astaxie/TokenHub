package dbschema

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"time"
)

//go:embed migrations
var migrationFS embed.FS

const sqliteBaselinePath = "migrations/sqlite/000001_baseline.json"
const postgresBaselinePath = "migrations/postgres/000001_baseline.json"

type baselineFile struct {
	Version    int64    `json:"version"`
	Dialect    string   `json:"dialect"`
	Statements []string `json:"statements"`
}

// SQLiteBaselineStatements returns the frozen SQL statements that create the
// adoption-baseline schema on a fresh SQLite database (fresh
// databases are created from explicit SQL; only databases that already carry
// business tables run the frozen legacy-adoption flow). Regenerate the file
// with UPDATE_BASELINE=1 go test ./internal/server -run TestSQLiteBaselineSQLIsCurrent.
func SQLiteBaselineStatements() ([]string, error) {
	return readBaselineFile(sqliteBaselinePath, DialectSQLite)
}

// PostgresBaselineStatements returns the frozen SQL statements that create
// the adoption-baseline schema on a fresh PostgreSQL database. Regenerate the
// file with UPDATE_BASELINE=1 and TEST_POSTGRES_URL set while running
// ./internal/server -run TestPostgresBaselineSQLIsCurrent (integration tag).
func PostgresBaselineStatements() ([]string, error) {
	return readBaselineFile(postgresBaselinePath, DialectPostgres)
}

func readBaselineFile(path string, dialect Dialect) ([]string, error) {
	raw, err := migrationFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dbschema: read %s baseline: %w", dialect, err)
	}
	var file baselineFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("dbschema: parse %s baseline: %w", dialect, err)
	}
	if file.Version != BaselineVersion || file.Dialect != string(dialect) {
		return nil, fmt.Errorf("dbschema: %s baseline declares version %d dialect %q", dialect, file.Version, file.Dialect)
	}
	return file.Statements, nil
}

// databaseIsFresh reports whether the database holds no user tables beyond
// the runner's own ledger tables.
func (r *Runner) databaseIsFresh(ctx context.Context) (bool, error) {
	return r.databaseIsFreshOn(ctx, r.db)
}

// databaseIsFreshOn is the connection-safe variant used while a dedicated
// SQLite connection is held.
func (r *Runner) databaseIsFreshOn(ctx context.Context, db Execer) (bool, error) {
	query := "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'"
	if r.dialect == DialectPostgres {
		query = "SELECT table_name FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND table_schema = current_schema()"
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return false, fmt.Errorf("dbschema: list tables for freshness check: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if !isLedgerTable(name) {
			return false, nil
		}
	}
	return true, rows.Err()
}

func isLedgerTable(name string) bool {
	switch name {
	case "schema_migrations", "migration_attempts", "data_backfills":
		return true
	}
	return false
}

// adoptFreshSQLite serializes fresh adoption through BEGIN IMMEDIATE: under
// the write lock it re-checks that no baseline was recorded and the database
// still holds no business tables, replays the frozen baseline SQL, and records
// the baseline. It returns handled=false when another path should take over
// (baseline already recorded elsewhere, or business tables appeared), letting
// the caller fall through to the legacy flow. Everything after COMMIT runs on
// the pool once the dedicated connection is closed, because server SQLite
// pools are single-connection.
func (r *Runner) adoptFreshSQLite(ctx context.Context) (result Result, err error, handled bool) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return Result{}, err, true
	}
	rollback := true
	defer func() {
		if rollback {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
		conn.Close()
	}()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Result{}, err, true
	}
	recorded, err := r.versionRecorded(ctx, conn, BaselineVersion)
	if err != nil {
		return Result{}, err, true
	}
	if recorded {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return Result{}, err, true
		}
		rollback = false
		conn.Close()
		result, err = r.migrateLocked(ctx)
		return result, err, true
	}
	fresh, err := r.databaseIsFreshOn(ctx, conn)
	if err != nil {
		return Result{}, err, true
	}
	if !fresh {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return Result{}, err, true
		}
		rollback = false
		return Result{}, nil, false
	}
	attemptID, err := r.beginAttemptOn(ctx, conn, BaselineVersion)
	if err != nil {
		return Result{}, err, true
	}
	started := time.Now()
	fail := func(code, outcome string, cause error) (Result, error, bool) {
		r.finishAttempt(ctx, attemptID, outcome, time.Since(started), code)
		return Result{}, newError(code, BaselineVersion, cause), true
	}
	for _, statement := range r.freshBaseline {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			rollback = false
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
			conn.Close()
			return fail(ErrCodeApplyFailed, "failed", fmt.Errorf("baseline statement %q: %w", firstLine(statement), err))
		}
	}
	// Commit the schema WITHOUT the baseline row first: the semantic
	// verification runs on the pool once the connection is released, and the
	// baseline row is only recorded after it passes. A crash in between leaves
	// a business-table database without a baseline, which the next start
	// adopts through the legacy flow — never a recorded-but-unverified state.
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		rollback = false
		conn.Close()
		return fail(ErrCodeApplyFailed, "failed", err)
	}
	rollback = false
	conn.Close()
	if r.adoptionReference != nil {
		if verifyErr := r.verifyAgainstReference(ctx); verifyErr != nil {
			return fail(ErrCodeSchemaVerification, "failed", verifyErr)
		}
	}
	record, err := r.insertAppliedTx(ctx, BaselineVersion, baselineName, PhaseExpand, AdoptionChecksum, false)
	if err != nil {
		return fail(ErrCodeApplyFailed, "failed", err)
	}
	r.finishAttempt(ctx, attemptID, "success", time.Since(started), "")
	result = Result{Adopted: true, Applied: []Applied{record}}
	migrated, err := r.migrateLocked(ctx)
	if err != nil {
		return result, err, true
	}
	result.Applied = append(result.Applied, migrated.Applied...)
	return result, nil, true
}

// beginAttemptOn records an attempt start on a held SQLite connection and is
// the single-connection-pool-safe variant of beginAttempt.
func (r *Runner) beginAttemptOn(ctx context.Context, db Execer, version int64) (int64, error) {
	result, err := db.ExecContext(ctx,
		"INSERT INTO migration_attempts (version, app_release, executor, started_at) VALUES (?, ?, ?, ?)",
		version, r.appRelease, r.executor, r.nowText())
	if err != nil {
		return 0, fmt.Errorf("dbschema: record attempt: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("dbschema: record attempt id: %w", err)
	}
	return id, nil
}

// adoptFreshLocked creates the schema from the frozen baseline SQL on an empty
// PostgreSQL database, semantically verifies it against the reference
// snapshot, and only then records the baseline. The caller holds the advisory
// lock, which serializes concurrent executors.
func (r *Runner) adoptFreshLocked(ctx context.Context) (Result, error) {
	attemptID, err := r.beginAttempt(ctx, BaselineVersion)
	if err != nil {
		return Result{}, err
	}
	started := time.Now()
	if err := r.execStatements(ctx, r.freshBaseline); err != nil {
		r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeApplyFailed)
		return Result{}, newError(ErrCodeApplyFailed, BaselineVersion, err)
	}
	if r.adoptionReference != nil {
		if err := r.verifyAgainstReference(ctx); err != nil {
			r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeSchemaVerification)
			return Result{}, newError(ErrCodeSchemaVerification, BaselineVersion, err)
		}
	}
	record, err := r.insertAppliedTx(ctx, BaselineVersion, baselineName, PhaseExpand, AdoptionChecksum, false)
	if err != nil {
		r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeApplyFailed)
		return Result{}, newError(ErrCodeApplyFailed, BaselineVersion, err)
	}
	r.finishAttempt(ctx, attemptID, "success", time.Since(started), "")
	result := Result{Adopted: true, Applied: []Applied{record}}
	migrated, err := r.migrateLocked(ctx)
	if err != nil {
		return result, err
	}
	result.Applied = append(result.Applied, migrated.Applied...)
	return result, nil
}

// execStatements runs the frozen baseline statements. SQLite runs them in one
// transaction; PostgreSQL runs each statement standalone because the baseline
// includes CREATE INDEX CONCURRENTLY, which cannot run inside a transaction.
// A failed fresh PostgreSQL adoption self-heals: the half-built database no
// longer counts as fresh, so the next start completes it through the frozen
// legacy flow before recording the baseline.
func (r *Runner) execStatements(ctx context.Context, statements []string) error {
	if r.dialect == DialectPostgres {
		for _, statement := range statements {
			if _, err := r.db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("baseline statement %q: %w", firstLine(statement), err)
			}
		}
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("baseline statement %q: %w", firstLine(statement), err)
		}
	}
	return tx.Commit()
}
