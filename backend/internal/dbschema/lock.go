package dbschema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// SchemaMigrationLockName keys every schema executor. PostgreSQL uses it as
// the advisory-lock name; SQLite uses the same logical domain through a lock
// file beside the database.
const SchemaMigrationLockName = "tokenhub:schema-migration"

// AcquireMigrationLock serializes schema work across processes. Callers that
// wrap a Runner with WithExternalCoordination use this function to enter the
// same lock domain as standalone migrate, repair, and contract commands.
func AcquireMigrationLock(ctx context.Context, db *sql.DB, dialect Dialect, wait time.Duration, logf func(format string, args ...any)) (func(), error) {
	if wait <= 0 {
		wait = DefaultLockWait
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	lockCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	switch dialect {
	case DialectPostgres:
		return acquirePostgresMigrationLock(lockCtx, ctx, db, wait, logf)
	case DialectSQLite:
		return acquireSQLiteMigrationLock(lockCtx, ctx, db, wait, logf)
	default:
		return nil, fmt.Errorf("dbschema: unsupported lock dialect %q", dialect)
	}
}

func acquirePostgresMigrationLock(lockCtx, parent context.Context, db *sql.DB, wait time.Duration, logf func(string, ...any)) (func(), error) {
	conn, err := db.Conn(lockCtx)
	if err != nil {
		return nil, lockWaitError(parent, wait, err)
	}
	for {
		var acquired bool
		if err := conn.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", SchemaMigrationLockName).Scan(&acquired); err != nil {
			_ = conn.Close()
			return nil, lockWaitError(parent, wait, err)
		}
		if acquired {
			return func() {
				_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", SchemaMigrationLockName)
				_ = conn.Close()
			}, nil
		}
		logf("dbschema: waiting for schema migration lock %q (bounded at %s)", SchemaMigrationLockName, wait)
		if err := waitForLockPoll(lockCtx); err != nil {
			_ = conn.Close()
			return nil, lockWaitError(parent, wait, err)
		}
	}
}

func acquireSQLiteMigrationLock(lockCtx, parent context.Context, db *sql.DB, wait time.Duration, logf func(string, ...any)) (func(), error) {
	path, err := sqliteMainDatabasePath(lockCtx, db)
	if err != nil {
		return nil, lockWaitError(parent, wait, err)
	}
	// In-memory SQLite databases cannot be shared across processes. Their
	// existing BEGIN IMMEDIATE sections remain sufficient, and skipping a
	// host lock also avoids coupling independent private test databases.
	if path == "" {
		return func() {}, nil
	}
	lockPath := path + ".tokenhub-migrate.lock"
	handle, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("dbschema: open sqlite migration lock file: %w", err)
	}
	for {
		err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
				_ = handle.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = handle.Close()
			return nil, fmt.Errorf("dbschema: acquire sqlite migration lock: %w", err)
		}
		logf("dbschema: waiting for sqlite schema migration lock %q (bounded at %s)", lockPath, wait)
		if err := waitForLockPoll(lockCtx); err != nil {
			_ = handle.Close()
			return nil, lockWaitError(parent, wait, err)
		}
	}
}

func sqliteMainDatabasePath(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", fmt.Errorf("dbschema: inspect sqlite database path: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sequence int
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			return "", err
		}
		if name != "main" || path == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("dbschema: resolve sqlite database path: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		}
		return absolute, nil
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func waitForLockPoll(ctx context.Context) error {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func lockWaitError(parent context.Context, wait time.Duration, err error) error {
	if parentErr := parent.Err(); parentErr != nil {
		return parentErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newError(ErrCodeLockTimeout, 0, fmt.Errorf("schema migration lock %q still held after %s", SchemaMigrationLockName, wait))
	}
	return err
}
