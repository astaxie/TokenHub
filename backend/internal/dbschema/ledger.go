package dbschema

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ensureLedger creates the migration ledger tables. The columns are kept
// portable across dialects: dirty is an integer flag and timestamps are
// written as RFC3339 text so both drivers scan them the same way.
func (r *Runner) ensureLedger(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version ` + r.integerType() + ` PRIMARY KEY,
			name TEXT NOT NULL,
			phase TEXT NOT NULL,
			checksum TEXT NOT NULL,
			dirty INTEGER NOT NULL DEFAULT 0,
			applied_at TEXT NOT NULL DEFAULT '',
			applied_release TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS migration_attempts (
			id ` + r.autoIncrementType() + `,
			version ` + r.integerType() + ` NOT NULL,
			app_release TEXT NOT NULL DEFAULT '',
			executor TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL DEFAULT '',
			finished_at TEXT,
			outcome TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			error_code TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS migration_attempts_version_idx ON migration_attempts (version)`,
	}
	for _, statement := range statements {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("dbschema: ensure ledger: %w", err)
		}
	}
	return nil
}

func (r *Runner) integerType() string {
	if r.dialect == DialectPostgres {
		return "BIGINT"
	}
	return "INTEGER"
}

func (r *Runner) autoIncrementType() string {
	if r.dialect == DialectPostgres {
		return "BIGSERIAL PRIMARY KEY"
	}
	return "INTEGER PRIMARY KEY AUTOINCREMENT"
}

func (r *Runner) nowText() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// loadApplied reads the ledger ordered by version.
func (r *Runner) loadApplied(ctx context.Context) ([]Applied, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT version, name, phase, checksum, dirty, applied_at, applied_release FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("dbschema: load ledger: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var applied []Applied
	for rows.Next() {
		var row Applied
		var dirty int
		if err := rows.Scan(&row.Version, &row.Name, &row.Phase, &row.Checksum, &dirty, &row.AppliedAt, &row.AppliedRelease); err != nil {
			return nil, fmt.Errorf("dbschema: scan ledger row: %w", err)
		}
		row.Dirty = dirty != 0
		applied = append(applied, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dbschema: iterate ledger: %w", err)
	}
	return applied, nil
}

func (r *Runner) placeholders(count int) []string {
	return placeholderMarks(r.dialect, count)
}

func placeholderMarks(dialect Dialect, count int) []string {
	marks := make([]string, count)
	for i := range marks {
		if dialect == DialectPostgres {
			marks[i] = fmt.Sprintf("$%d", i+1)
		} else {
			marks[i] = "?"
		}
	}
	return marks
}

func (r *Runner) insertAppliedSQL() string {
	marks := r.placeholders(7)
	return fmt.Sprintf(
		"INSERT INTO schema_migrations (version, name, phase, checksum, dirty, applied_at, applied_release) VALUES (%s, %s, %s, %s, %s, %s, %s)",
		marks[0], marks[1], marks[2], marks[3], marks[4], marks[5], marks[6],
	)
}

// insertAppliedTx writes one applied row in its own transaction.
func (r *Runner) insertAppliedTx(ctx context.Context, version int64, name string, phase Phase, checksum string, dirty bool) (Applied, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Applied{}, fmt.Errorf("dbschema: begin applied-row transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	dirtyFlag := 0
	if dirty {
		dirtyFlag = 1
	}
	if _, err := tx.ExecContext(ctx, r.insertAppliedSQL(), version, name, phase, checksum, dirtyFlag, r.nowText(), r.appRelease); err != nil {
		return Applied{}, fmt.Errorf("dbschema: insert applied row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Applied{}, fmt.Errorf("dbschema: commit applied row: %w", err)
	}
	return Applied{
		Version:        version,
		Name:           name,
		Phase:          phase,
		Checksum:       checksum,
		AppliedAt:      r.nowText(),
		AppliedRelease: r.appRelease,
	}, nil
}

// insertAppliedOn writes one applied row on an existing transactional Execer.
func (r *Runner) insertAppliedOn(ctx context.Context, db Execer, version int64, name string, phase Phase, checksum string, dirty bool) error {
	dirtyFlag := 0
	if dirty {
		dirtyFlag = 1
	}
	if _, err := db.ExecContext(ctx, r.insertAppliedSQL(), version, name, phase, checksum, dirtyFlag, r.nowText(), r.appRelease); err != nil {
		return fmt.Errorf("dbschema: insert applied row: %w", err)
	}
	return nil
}

func (r *Runner) clearDirty(ctx context.Context, version int64) error {
	return r.clearDirtyOn(ctx, r.db, version)
}

// clearDirtyOn clears the dirty marker on the given executor. SQLite paths
// pass the connection they already hold: the server pool is single-connection
// there, so routing through the pool would deadlock.
func (r *Runner) clearDirtyOn(ctx context.Context, db Execer, version int64) error {
	mark := r.placeholders(1)[0]
	if _, err := db.ExecContext(ctx, "UPDATE schema_migrations SET dirty = 0 WHERE version = "+mark, version); err != nil {
		return fmt.Errorf("dbschema: clear dirty marker for version %d: %w", version, err)
	}
	return nil
}

// deleteAppliedRow removes a dirty row so a SafeRetry repair can re-apply the
// version. It is the only ledger deletion the runner performs.
func (r *Runner) deleteAppliedRow(ctx context.Context, version int64) error {
	mark := r.placeholders(1)[0]
	if _, err := r.db.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = "+mark+" AND dirty = 1", version); err != nil {
		return fmt.Errorf("dbschema: drop dirty row for version %d: %w", version, err)
	}
	return nil
}

// finishAttemptOn writes the attempt outcome on the given executor (used by
// SQLite paths that hold a dedicated connection).
func (r *Runner) finishAttemptOn(ctx context.Context, db Execer, id int64, outcome string, duration time.Duration, errorCode string) {
	finishedExpr := "datetime('now')"
	if r.dialect == DialectPostgres {
		finishedExpr = "now()::text"
	}
	marks := r.placeholders(4)
	query := fmt.Sprintf("UPDATE migration_attempts SET finished_at = %s, outcome = %s, duration_ms = %s, error_code = %s WHERE id = %s",
		finishedExpr, marks[0], marks[1], marks[2], marks[3])
	if _, err := db.ExecContext(ctx, query, outcome, duration.Milliseconds(), errorCode, id); err != nil {
		r.log("dbschema: finish attempt %d: %v", id, err)
	}
}

// beginAttempt records the start of one migration execution. The database
// never stores raw driver errors, only stable outcome and error codes.
func (r *Runner) beginAttempt(ctx context.Context, version int64) (int64, error) {
	if r.dialect == DialectPostgres {
		var id int64
		err := r.db.QueryRowContext(ctx,
			"INSERT INTO migration_attempts (version, app_release, executor, started_at) VALUES ($1, $2, $3, now()::text) RETURNING id",
			version, r.appRelease, r.executor).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("dbschema: record attempt: %w", err)
		}
		return id, nil
	}
	result, err := r.db.ExecContext(ctx,
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

func (r *Runner) finishAttempt(ctx context.Context, id int64, outcome string, duration time.Duration, errorCode string) {
	r.finishAttemptOn(ctx, r.db, id, outcome, duration, errorCode)
}

// versionRecorded reports whether the ledger already holds a row for the
// version. It is re-checked under the SQLite write lock before a migration
// body runs.
func (r *Runner) versionRecorded(ctx context.Context, db Execer, version int64) (bool, error) {
	mark := r.placeholders(1)[0]
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = "+mark, version).Scan(&count); err != nil {
		return false, fmt.Errorf("dbschema: check recorded version %d: %w", version, err)
	}
	return count > 0, nil
}

// findApplied returns the ledger row for a version, or nil.
func findApplied(applied []Applied, version int64) *Applied {
	for i := range applied {
		if applied[i].Version == version {
			return &applied[i]
		}
	}
	return nil
}

var errNoBaseline = errors.New("database has no adoption baseline; run adoption before migrating")
