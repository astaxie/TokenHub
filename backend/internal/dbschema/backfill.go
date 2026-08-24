package dbschema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// BackfillMode classifies a one-time data conversion. Blocking
// backfills must finish before the release reports ready; online backfills
// run in idempotent batches while the service keeps serving.
type BackfillMode string

const (
	BackfillBlocking BackfillMode = "blocking"
	BackfillOnline   BackfillMode = "online"
)

// Backfill states recorded in the data_backfills ledger.
const (
	BackfillStatePending  = "pending"
	BackfillStateRunning  = "running"
	BackfillStateComplete = "complete"
)

const (
	// DefaultBackfillBatchLimit bounds one online batch so leases stay fresh.
	DefaultBackfillBatchLimit = 500
	// DefaultBackfillLeaseTTL is how long an online executor's claim survives
	// without extending it; another instance takes over afterwards.
	DefaultBackfillLeaseTTL = 2 * time.Minute
	// ErrCodeBackfillFailed marks a backfill that did not complete.
	ErrCodeBackfillFailed = "backfill_failed"
)

// Backfill is one registered one-time data conversion. Operations that drift
// again after completion (process interruption aside) are continuous repairs
// and must not be registered here.
type Backfill struct {
	ID   string
	Mode BackfillMode
	// RunBlocking converts everything in one call. It must be idempotent so a
	// retry after failure is safe.
	RunBlocking func(ctx context.Context, db Execer) error
	// RunBatch converts up to limit rows and reports how many rows still need
	// conversion (0 means complete). It must resume safely from any committed
	// state: batches commit independently and a crashed executor's partial
	// work is simply re-processed.
	RunBatch   func(ctx context.Context, db Execer, limit int) (remaining int64, err error)
	BatchLimit int
}

// BackfillState is one row of the data_backfills ledger.
type BackfillState struct {
	ID             string
	Mode           BackfillMode
	State          string
	Remaining      int64
	LeaseOwner     string
	LeaseExpiresAt string
	UpdatedAt      string
}

// OnlineProgress reports the outcome of one online batch round.
type OnlineProgress struct {
	ID        string
	Remaining int64
}

// BackfillExecutor drives the data_backfills ledger. It takes no cross-process
// lock itself: blocking runs happen inside the startup schema section, and
// online batches coordinate purely through lease rows, so any instance may
// drive them while at most one holds a lease per backfill.
type BackfillExecutor struct {
	db         *sql.DB
	dialect    Dialect
	backfills  []Backfill
	batchLimit int
	leaseTTL   time.Duration
	owner      string
	now        func() time.Time
	log        func(format string, args ...any)
}

// BackfillOption customizes a BackfillExecutor.
type BackfillOption func(*BackfillExecutor)

// WithBackfillBatchLimit overrides the default online batch size.
func WithBackfillBatchLimit(limit int) BackfillOption {
	return func(e *BackfillExecutor) { e.batchLimit = limit }
}

// WithBackfillLeaseTTL overrides the online lease time-to-live.
func WithBackfillLeaseTTL(ttl time.Duration) BackfillOption {
	return func(e *BackfillExecutor) { e.leaseTTL = ttl }
}

// WithBackfillOwner names this executor in lease rows; defaults to a random
// instance identifier.
func WithBackfillOwner(owner string) BackfillOption {
	return func(e *BackfillExecutor) {
		if owner != "" {
			e.owner = owner
		}
	}
}

// WithBackfillLogger receives lease contention diagnostics.
func WithBackfillLogger(log func(format string, args ...any)) BackfillOption {
	return func(e *BackfillExecutor) {
		if log != nil {
			e.log = log
		}
	}
}

// WithBackfillClock overrides the lease clock for tests.
func WithBackfillClock(now func() time.Time) BackfillOption {
	return func(e *BackfillExecutor) {
		if now != nil {
			e.now = now
		}
	}
}

// NewBackfillExecutor validates the backfill registry and returns an executor.
func NewBackfillExecutor(db *sql.DB, dialect Dialect, backfills []Backfill, opts ...BackfillOption) (*BackfillExecutor, error) {
	if db == nil {
		return nil, errors.New("dbschema: nil database handle")
	}
	switch dialect {
	case DialectSQLite, DialectPostgres:
	default:
		return nil, fmt.Errorf("dbschema: unsupported dialect %q", dialect)
	}
	seen := make(map[string]bool, len(backfills))
	for i := range backfills {
		b := &backfills[i]
		if b.ID == "" {
			return nil, errors.New("dbschema: backfill with empty ID")
		}
		if seen[b.ID] {
			return nil, fmt.Errorf("dbschema: duplicate backfill ID %q", b.ID)
		}
		seen[b.ID] = true
		switch b.Mode {
		case BackfillBlocking:
			if b.RunBlocking == nil {
				return nil, fmt.Errorf("dbschema: blocking backfill %q requires RunBlocking", b.ID)
			}
		case BackfillOnline:
			if b.RunBatch == nil {
				return nil, fmt.Errorf("dbschema: online backfill %q requires RunBatch", b.ID)
			}
		default:
			return nil, fmt.Errorf("dbschema: backfill %q has invalid mode %q", b.ID, b.Mode)
		}
	}
	executor := &BackfillExecutor{
		db:         db,
		dialect:    dialect,
		backfills:  backfills,
		batchLimit: DefaultBackfillBatchLimit,
		leaseTTL:   DefaultBackfillLeaseTTL,
		owner:      fmt.Sprintf("executor-%d", time.Now().UnixNano()),
		now:        time.Now,
		log:        func(string, ...any) {},
	}
	for _, opt := range opts {
		opt(executor)
	}
	return executor, nil
}

// EnsureLedger creates the data_backfills table and seeds one pending row per
// registered backfill; existing rows are never modified.
func (e *BackfillExecutor) EnsureLedger(ctx context.Context) error {
	intType := "INTEGER"
	if e.dialect == DialectPostgres {
		intType = "BIGINT"
	}
	// remaining starts at -1 (unknown) until the first batch reports progress.
	if _, err := e.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS data_backfills (
		id TEXT PRIMARY KEY,
		mode TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT '`+BackfillStatePending+`',
		remaining `+intType+` NOT NULL DEFAULT -1,
		lease_owner TEXT NOT NULL DEFAULT '',
		lease_expires_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("dbschema: ensure data backfill ledger: %w", err)
	}
	for _, b := range e.backfills {
		marks := strings.Join(placeholderMarks(e.dialect, 4), ", ")
		query := fmt.Sprintf(
			"INSERT INTO data_backfills (id, mode, state, updated_at) VALUES (%s) ON CONFLICT (id) DO NOTHING",
			marks)
		if _, err := e.db.ExecContext(ctx, query, b.ID, b.Mode, BackfillStatePending, e.nowText()); err != nil {
			return fmt.Errorf("dbschema: seed backfill %q: %w", b.ID, err)
		}
	}
	return nil
}

// Status reports the ledger state of every registered backfill.
func (e *BackfillExecutor) Status(ctx context.Context) ([]BackfillState, error) {
	if err := e.EnsureLedger(ctx); err != nil {
		return nil, err
	}
	query := "SELECT id, mode, state, remaining, lease_owner, lease_expires_at, updated_at FROM data_backfills ORDER BY id"
	rows, err := e.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("dbschema: load data backfills: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var states []BackfillState
	for rows.Next() {
		var state BackfillState
		if err := rows.Scan(&state.ID, &state.Mode, &state.State, &state.Remaining, &state.LeaseOwner, &state.LeaseExpiresAt, &state.UpdatedAt); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

// RunBlocking executes every incomplete blocking backfill to completion. A
// failure leaves the ledger row in running state and returns a typed error;
// the release must not report ready until a retry succeeds.
func (e *BackfillExecutor) RunBlocking(ctx context.Context) error {
	if err := e.EnsureLedger(ctx); err != nil {
		return err
	}
	for _, b := range e.backfills {
		if b.Mode != BackfillBlocking {
			continue
		}
		state, err := e.loadOne(ctx, b.ID)
		if err != nil {
			return err
		}
		if state.State == BackfillStateComplete {
			continue
		}
		if err := e.markRunning(ctx, b.ID); err != nil {
			return err
		}
		if err := b.RunBlocking(ctx, e.db); err != nil {
			return newError(ErrCodeBackfillFailed, 0, fmt.Errorf("backfill %q: %w", b.ID, err))
		}
		if err := e.markComplete(ctx, b.ID); err != nil {
			return err
		}
	}
	return nil
}

// RunOnlineBatch executes at most one batch per incomplete online backfill for
// which this executor can claim the lease. Progress rows update after the
// batch commits; a crashed executor's stale lease expires and another instance
// resumes from the committed data state.
func (e *BackfillExecutor) RunOnlineBatch(ctx context.Context) ([]OnlineProgress, error) {
	if err := e.EnsureLedger(ctx); err != nil {
		return nil, err
	}
	var progress []OnlineProgress
	for _, b := range e.backfills {
		if b.Mode != BackfillOnline {
			continue
		}
		state, err := e.loadOne(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		if state.State == BackfillStateComplete {
			continue
		}
		claimed, err := e.claimLease(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		if !claimed {
			e.log("dbschema: online backfill %q leased by %s until %s", b.ID, state.LeaseOwner, state.LeaseExpiresAt)
			continue
		}
		limit := e.batchLimit
		if b.BatchLimit > 0 {
			limit = b.BatchLimit
		}
		// Renew the lease while the batch runs: a batch longer than the TTL
		// would otherwise be taken over by another instance while this one is
		// still writing. Losing the lease cancels the batch context; batches
		// are expected to honor it.
		batchCtx, cancelBatch := context.WithCancel(ctx)
		leaseLost := e.renewLeaseDuringBatch(batchCtx, cancelBatch, b.ID)
		remaining, err := b.RunBatch(batchCtx, e.db, limit)
		cancelBatch()
		if lost := <-leaseLost; lost {
			return progress, newError(ErrCodeBackfillFailed, 0, fmt.Errorf("backfill %q lost its lease mid-batch", b.ID))
		}
		if err != nil {
			return progress, newError(ErrCodeBackfillFailed, 0, fmt.Errorf("backfill %q: %w", b.ID, err))
		}
		if err := e.recordProgress(ctx, b.ID, remaining); err != nil {
			return progress, newError(ErrCodeBackfillFailed, 0, err)
		}
		progress = append(progress, OnlineProgress{ID: b.ID, Remaining: remaining})
	}
	return progress, nil
}

func (e *BackfillExecutor) loadOne(ctx context.Context, id string) (BackfillState, error) {
	mark := placeholderMarks(e.dialect, 1)[0]
	var state BackfillState
	err := e.db.QueryRowContext(ctx,
		"SELECT id, mode, state, remaining, lease_owner, lease_expires_at, updated_at FROM data_backfills WHERE id = "+mark, id).
		Scan(&state.ID, &state.Mode, &state.State, &state.Remaining, &state.LeaseOwner, &state.LeaseExpiresAt, &state.UpdatedAt)
	if err != nil {
		return BackfillState{}, fmt.Errorf("dbschema: load backfill %q: %w", id, err)
	}
	return state, nil
}

func (e *BackfillExecutor) markRunning(ctx context.Context, id string) error {
	marks := placeholderMarks(e.dialect, 3)
	query := fmt.Sprintf("UPDATE data_backfills SET state = %s, updated_at = %s WHERE id = %s", marks[0], marks[1], marks[2])
	if _, err := e.db.ExecContext(ctx, query, BackfillStateRunning, e.nowText(), id); err != nil {
		return fmt.Errorf("dbschema: mark backfill %q running: %w", id, err)
	}
	return nil
}

func (e *BackfillExecutor) markComplete(ctx context.Context, id string) error {
	marks := placeholderMarks(e.dialect, 3)
	query := fmt.Sprintf(
		"UPDATE data_backfills SET state = %s, remaining = 0, lease_owner = '', lease_expires_at = '', updated_at = %s WHERE id = %s",
		marks[0], marks[1], marks[2])
	if _, err := e.db.ExecContext(ctx, query, BackfillStateComplete, e.nowText(), id); err != nil {
		return fmt.Errorf("dbschema: mark backfill %q complete: %w", id, err)
	}
	return nil
}

// claimLease atomically takes or renews the online lease for a backfill. The
// lease predicate is part of the UPDATE and the claim only succeeds when a
// row was affected, so two replicas racing on an expired lease cannot both
// win; an unexpired foreign lease matches no row and refuses the claim.
func (e *BackfillExecutor) claimLease(ctx context.Context, id string) (bool, error) {
	now := e.now().UTC()
	expires := now.Add(e.leaseTTL).Format(time.RFC3339)
	nowText := now.Format(time.RFC3339)
	marks := placeholderMarks(e.dialect, 7)
	query := fmt.Sprintf(
		"UPDATE data_backfills SET lease_owner = %s, lease_expires_at = %s, state = %s, updated_at = %s "+
			"WHERE id = %s AND (lease_owner = '' OR lease_owner = %s OR lease_expires_at < %s)",
		marks[0], marks[1], marks[2], marks[3], marks[4], marks[5], marks[6])
	result, err := e.db.ExecContext(ctx, query, e.owner, expires, BackfillStateRunning, nowText, id, e.owner, nowText)
	if err != nil {
		return false, fmt.Errorf("dbschema: claim backfill lease %q: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("dbschema: claim backfill lease %q: %w", id, err)
	}
	return affected == 1, nil
}

// recordProgress publishes batch progress and extends the lease (the
// heartbeat). The owner fence keeps a stale executor from overwriting the
// state of the instance that took over its expired lease.
func (e *BackfillExecutor) recordProgress(ctx context.Context, id string, remaining int64) error {
	state := BackfillStateRunning
	if remaining <= 0 {
		state = BackfillStateComplete
	}
	expires := e.now().UTC().Add(e.leaseTTL).Format(time.RFC3339)
	marks := placeholderMarks(e.dialect, 6)
	query := fmt.Sprintf(
		"UPDATE data_backfills SET remaining = %s, lease_expires_at = %s, state = %s, updated_at = %s WHERE id = %s AND lease_owner = %s",
		marks[0], marks[1], marks[2], marks[3], marks[4], marks[5])
	result, err := e.db.ExecContext(ctx, query, remaining, expires, state, e.nowText(), id, e.owner)
	if err != nil {
		return fmt.Errorf("dbschema: record backfill progress %q: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("dbschema: record backfill progress %q: %w", id, err)
	}
	if affected != 1 {
		return fmt.Errorf("dbschema: record backfill progress %q: lease lost", id)
	}
	return nil
}

// renewLeaseDuringBatch extends the lease every TTL/3 while a batch runs. It
// returns a channel receiving true when the lease was lost (renewal failed or
// another owner took over); the loss also cancels the batch so a
// well-behaved batch stops writing.
func (e *BackfillExecutor) renewLeaseDuringBatch(batchCtx context.Context, cancelBatch context.CancelFunc, id string) <-chan bool {
	lost := make(chan bool, 1)
	ticker := time.NewTicker(e.leaseTTL / 3)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-batchCtx.Done():
				// Batch finished or failed while the lease was still held.
				lost <- false
				return
			case <-ticker.C:
				claimed, err := e.claimLease(context.Background(), id)
				if err != nil || !claimed {
					lost <- true
					cancelBatch()
					return
				}
			}
		}
	}()
	return lost
}

func (e *BackfillExecutor) nowText() string {
	return e.now().UTC().Format(time.RFC3339)
}
