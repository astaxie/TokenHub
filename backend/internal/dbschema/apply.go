package dbschema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// verifyAgainstReference builds the reference snapshot and compares it with
// the actual schema; a non-nil error means adoption must be refused.
func (r *Runner) verifyAgainstReference(ctx context.Context) error {
	reference, err := r.adoptionReference(ctx)
	if err != nil {
		return err
	}
	actual, err := Introspect(ctx, r.db, r.dialect, "")
	if err != nil {
		return err
	}
	if violations := CompareObjects(reference, actual); len(violations) > 0 {
		return errors.New(FormatViolations(violations))
	}
	return nil
}

// Adopt bridges a database that has no adoption baseline: it runs the frozen
// adoption callback (if any), then records the baseline row. A database that
// already carries a baseline is only verified, after which pending expand
// migrations run. Adopt never applies contract migrations.
func (r *Runner) Adopt(ctx context.Context, frozen func(ctx context.Context) error) (Result, error) {
	if err := r.ensureLedger(ctx); err != nil {
		return Result{}, err
	}
	applied, err := r.loadApplied(ctx)
	if err != nil {
		return Result{}, err
	}
	if findApplied(applied, BaselineVersion) != nil {
		if err := r.verifyApplied(applied); err != nil {
			return Result{}, err
		}
		return r.Migrate(ctx)
	}
	release, err := r.acquireLock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	// Re-read under the lock: another executor may have adopted concurrently.
	applied, err = r.loadApplied(ctx)
	if err != nil {
		return Result{}, err
	}
	if baseline := findApplied(applied, BaselineVersion); baseline != nil {
		if err := r.verifyApplied(applied); err != nil {
			return Result{}, err
		}
		return r.migrateLocked(ctx)
	}
	// A database without business tables adopts from the frozen baseline SQL
	// instead of the legacy flow. SQLite serializes the recheck
	// and replay under BEGIN IMMEDIATE and may fall through to legacy.
	if len(r.freshBaseline) > 0 {
		if r.dialect == DialectSQLite {
			result, freshErr, handled := r.adoptFreshSQLite(ctx)
			if handled || freshErr != nil {
				return result, freshErr
			}
		} else {
			fresh, freshErr := r.databaseIsFresh(ctx)
			if freshErr != nil {
				return Result{}, freshErr
			}
			if fresh {
				return r.adoptFreshLocked(ctx)
			}
		}
	}
	attemptID, err := r.beginAttempt(ctx, BaselineVersion)
	if err != nil {
		return Result{}, err
	}
	started := time.Now()
	// Unrecognized databases refuse adoption instead of being absorbed into
	// the baseline by the frozen flow.
	if r.legacyRecognizer != nil {
		if recognizeErr := r.legacyRecognizer(ctx, r.db); recognizeErr != nil {
			r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeUnrecognizedDatabase)
			return Result{}, newError(ErrCodeUnrecognizedDatabase, 0, recognizeErr)
		}
	}
	if frozen != nil {
		if err := frozen(ctx); err != nil {
			r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeApplyFailed)
			return Result{}, newError(ErrCodeApplyFailed, BaselineVersion, err)
		}
	}
	// The frozen flow completed; before the baseline is recorded the database
	// must semantically match the reference schema.
	if r.adoptionReference != nil {
		if verifyErr := r.verifyAgainstReference(ctx); verifyErr != nil {
			r.finishAttempt(ctx, attemptID, "failed", time.Since(started), ErrCodeSchemaVerification)
			return Result{}, newError(ErrCodeSchemaVerification, BaselineVersion, verifyErr)
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

// Migrate verifies the ledger and applies pending expand migrations under a
// bounded cross-process lock. It refuses dirty ledgers, unknown applied
// versions, checksum drift, and databases without an adoption baseline.
func (r *Runner) Migrate(ctx context.Context) (Result, error) {
	if err := r.ensureLedger(ctx); err != nil {
		return Result{}, err
	}
	applied, err := r.loadApplied(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := r.verifyApplied(applied); err != nil {
		return Result{}, err
	}
	if findApplied(applied, BaselineVersion) == nil {
		return Result{}, newError(ErrCodeBaselineMissing, BaselineVersion, errNoBaseline)
	}
	if len(r.pendingByPhase(applied, PhaseExpand)) == 0 {
		return Result{}, nil
	}
	release, err := r.acquireLock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	return r.migrateLocked(ctx)
}

// migrateLocked applies pending expand migrations; the caller holds the
// coordination lock (or runs under external coordination).
func (r *Runner) migrateLocked(ctx context.Context) (Result, error) {
	applied, err := r.loadApplied(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := r.verifyApplied(applied); err != nil {
		return Result{}, err
	}
	if findApplied(applied, BaselineVersion) == nil {
		return Result{}, newError(ErrCodeBaselineMissing, BaselineVersion, errNoBaseline)
	}
	pending := r.pendingByPhase(applied, PhaseExpand)
	if len(pending) == 0 {
		return Result{}, nil
	}
	// Never apply an expand past a still-pending lower contract: the same
	// state version must not represent two different schemas (the contract's
	// removal has not happened yet). Contracts only run through maintenance.
	minPendingContract := int64(0)
	appliedSet := make(map[int64]bool, len(applied))
	for _, row := range applied {
		appliedSet[row.Version] = true
	}
	for _, m := range r.registry {
		if m.Phase == PhaseContract && m.appliesTo(r.dialect) && !appliedSet[m.Version] && (minPendingContract == 0 || m.Version < minPendingContract) {
			minPendingContract = m.Version
		}
	}
	result := Result{}
	for _, m := range pending {
		if minPendingContract != 0 && m.Version > minPendingContract {
			return result, newError(ErrCodeExpandPending, m.Version, fmt.Errorf("expand version %d is blocked by pending contract version %d; run the contract maintenance step first", m.Version, minPendingContract))
		}
		record, outcome, err := r.applyMigration(ctx, m)
		if err != nil {
			return result, err
		}
		if outcome == "success" {
			result.Applied = append(result.Applied, record)
		}
	}
	return result, nil
}

// applyMigration executes one migration and records the attempt. A
// transactional failure rolls the version back; a non-transactional failure
// leaves the dirty marker in the ledger and refuses startup until repair. An
// outcome of "skipped" means another executor completed the version first.
func (r *Runner) applyMigration(ctx context.Context, m Migration) (Applied, string, error) {
	attemptID, err := r.beginAttempt(ctx, m.Version)
	if err != nil {
		return Applied{}, "failed", err
	}
	// The per-migration lock budget bounds how long one migration
	// may hold the schema lock while it runs, body and postcondition
	// included. Exceeding it fails the migration like any other error:
	// transactional runs roll back, non-transactional runs keep the dirty
	// marker for repair.
	migrationCtx := ctx
	var cancel context.CancelFunc
	if m.LockTimeoutSeconds > 0 {
		migrationCtx, cancel = context.WithTimeout(ctx, time.Duration(m.LockTimeoutSeconds)*time.Second)
		defer cancel()
	}
	started := time.Now()
	record, outcome, applyErr := r.runMigration(migrationCtx, m)
	if errors.Is(applyErr, context.DeadlineExceeded) {
		applyErr = fmt.Errorf("lock budget of %ds exceeded: %w", m.LockTimeoutSeconds, applyErr)
	}
	r.finishAttempt(ctx, attemptID, outcome, time.Since(started), errorCode(applyErr))
	if applyErr != nil {
		return Applied{}, outcome, newError(ErrCodeApplyFailed, m.Version, applyErr)
	}
	return record, outcome, nil
}

// budgetedExecer enforces a migration's StatementBudget: every
// statement the body or postcondition executes counts against one shared
// budget, and exceeding it fails the migration instead of letting it perform
// unbounded work.
type budgetedExecer struct {
	db        Execer
	remaining int64
}

type errorRow struct {
	err error
}

func (r errorRow) Scan(_ ...any) error { return r.err }

func (b *budgetedExecer) charge() error {
	if b.remaining <= 0 {
		return errors.New("statement budget exceeded")
	}
	b.remaining--
	return nil
}

func (b *budgetedExecer) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := b.charge(); err != nil {
		return nil, err
	}
	return b.db.ExecContext(ctx, query, args...)
}

func (b *budgetedExecer) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := b.charge(); err != nil {
		return nil, err
	}
	return b.db.QueryContext(ctx, query, args...)
}

func (b *budgetedExecer) QueryRowContext(ctx context.Context, query string, args ...any) RowScanner {
	if err := b.charge(); err != nil {
		return errorRow{err: err}
	}
	return b.db.QueryRowContext(ctx, query, args...)
}

func newBudgetedExecer(db Execer, m Migration) *budgetedExecer {
	return &budgetedExecer{db: db, remaining: m.StatementBudget}
}

func runMigrationBody(ctx context.Context, db MigrationExecer, m Migration) error {
	if m.Go == nil && int64(len(m.Statements)) > m.StatementBudget {
		return fmt.Errorf("statement budget exceeded: %d statements, budget %d", len(m.Statements), m.StatementBudget)
	}
	if m.Go != nil {
		return m.Go(ctx, db)
	}
	for _, statement := range m.Statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("statement %q: %w", firstLine(statement), err)
		}
	}
	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	return ErrCodeApplyFailed
}

// runMigration executes the migration on its dialect and reports the ledger
// outcome: success, rolled_back (transactional failure), dirty
// (non-transactional failure, dirty marker stays), or failed (no migration
// content ran).
func (r *Runner) runMigration(ctx context.Context, m Migration) (Applied, string, error) {
	if r.dialect == DialectSQLite {
		return r.runOnSQLite(ctx, m)
	}
	return r.runOnPostgres(ctx, m)
}

// runOnSQLite executes the migration on a dedicated connection. BEGIN
// IMMEDIATE takes the SQLite write lock up front so concurrent executors in
// other processes fail fast instead of mid-transaction.
func (r *Runner) runOnSQLite(ctx context.Context, m Migration) (Applied, string, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return Applied{}, "failed", err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Applied{}, "failed", err
	}
	open := true
	defer func() {
		if open {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	// BEGIN IMMEDIATE is the SQLite cross-process lock, so the version must be
	// re-checked under it: another executor may have committed while this
	// runner was reading the ledger.
	already, err := r.versionRecorded(ctx, conn, m.Version)
	if err != nil {
		return Applied{}, "failed", err
	}
	if already {
		open = false
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return Applied{}, "failed", err
		}
		return Applied{}, "skipped", nil
	}
	if m.transactional() {
		budgeted := newBudgetedExecer(conn, m)
		if err := runMigrationBody(ctx, budgeted, m); err != nil {
			return Applied{}, "rolled_back", err
		}
		if err := r.insertAppliedOn(ctx, conn, m.Version, m.Name, m.Phase, m.Checksum(), false); err != nil {
			return Applied{}, "rolled_back", err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return Applied{}, "rolled_back", err
		}
		open = false
		return r.appliedRecord(m), "success", nil
	}
	// Non-transactional: persist the dirty marker in its own committed
	// transaction first, so a crash mid-run leaves a ledger that refuses
	// startup instead of an unknown state.
	if err := r.insertAppliedOn(ctx, conn, m.Version, m.Name, m.Phase, m.Checksum(), true); err != nil {
		return Applied{}, "failed", err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return Applied{}, "failed", err
	}
	open = false
	budgeted := newBudgetedExecer(conn, m)
	if err := runMigrationBody(ctx, budgeted, m); err != nil {
		return Applied{}, "dirty", err
	}
	if err := m.Postcondition(ctx, budgeted); err != nil {
		return Applied{}, "dirty", fmt.Errorf("postcondition: %w", err)
	}
	// Must run on the held connection: the server pool is single-connection
	// for SQLite, so asking the pool for another one here would deadlock.
	if err := r.clearDirtyOn(ctx, conn, m.Version); err != nil {
		return Applied{}, "dirty", err
	}
	return r.appliedRecord(m), "success", nil
}

// runOnPostgres executes the migration on a dedicated session. The caller
// holds the advisory lock, so the extra connection only scopes the
// transaction.
func (r *Runner) runOnPostgres(ctx context.Context, m Migration) (Applied, string, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return Applied{}, "failed", err
	}
	defer conn.Close()
	if m.transactional() {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return Applied{}, "failed", err
		}
		defer func() { _ = tx.Rollback() }()
		budgeted := newBudgetedExecer(tx, m)
		if err := runMigrationBody(ctx, budgeted, m); err != nil {
			return Applied{}, "rolled_back", err
		}
		if err := r.insertAppliedOn(ctx, tx, m.Version, m.Name, m.Phase, m.Checksum(), false); err != nil {
			return Applied{}, "rolled_back", err
		}
		if err := tx.Commit(); err != nil {
			return Applied{}, "rolled_back", err
		}
		return r.appliedRecord(m), "success", nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return Applied{}, "failed", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.insertAppliedOn(ctx, tx, m.Version, m.Name, m.Phase, m.Checksum(), true); err != nil {
		return Applied{}, "failed", err
	}
	if err := tx.Commit(); err != nil {
		return Applied{}, "failed", err
	}
	budgeted := newBudgetedExecer(r.db, m)
	if err := runMigrationBody(ctx, budgeted, m); err != nil {
		return Applied{}, "dirty", err
	}
	if err := m.Postcondition(ctx, budgeted); err != nil {
		return Applied{}, "dirty", fmt.Errorf("postcondition: %w", err)
	}
	if err := r.clearDirty(ctx, m.Version); err != nil {
		return Applied{}, "dirty", err
	}
	return r.appliedRecord(m), "success", nil
}

func (r *Runner) appliedRecord(m Migration) Applied {
	return Applied{
		Version:        m.Version,
		Name:           m.Name,
		Phase:          m.Phase,
		Checksum:       m.Checksum(),
		AppliedAt:      r.nowText(),
		AppliedRelease: "",
	}
}

// acquireLock serializes migration executors across processes. PostgreSQL uses
// a session advisory lock; SQLite uses a host lock file that remains held
// across non-transactional dirty-marker commits and contract preconditions.
// Runners created with WithExternalCoordination skip locking because the
// caller already holds the same migration lock around a larger startup flow.
func (r *Runner) acquireLock(ctx context.Context) (func(), error) {
	if r.externalCoordination {
		return func() {}, nil
	}
	return AcquireMigrationLock(ctx, r.db, r.dialect, r.lockWait, r.log)
}
