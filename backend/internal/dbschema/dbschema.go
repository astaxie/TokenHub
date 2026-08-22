// Package dbschema owns TokenHub's forward-only database schema evolution: a
// narrow migration runner with a per-version ledger, checksum verification,
// dirty-state handling, and bounded cross-process locking. It executes only
// registered SQL statements or Go callbacks and deliberately does not grow
// into a general migration framework or SQL parser. The normative lifecycle
// and safety contract is documented in docs/database-evolution.md.
package dbschema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Dialect identifies the database engine the runner operates on.
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

// Phase classifies a migration. Expand migrations stay compatible with the
// supported rollback window and may run at startup; contract migrations only
// run through an explicit maintenance operation and are never applied by
// Runner.Migrate.
type Phase string

const (
	PhaseExpand   Phase = "expand"
	PhaseContract Phase = "contract"
)

// Stable error codes surfaced through *Error and recorded in
// migration_attempts. Raw driver errors never reach the ledger.
const (
	ErrCodeChecksumMismatch   = "checksum_mismatch"
	ErrCodeDirtyState         = "dirty_state"
	ErrCodeLockTimeout        = "lock_timeout"
	ErrCodeApplyFailed        = "apply_failed"
	ErrCodeUnknownApplied     = "unknown_applied_version"
	ErrCodeBaselineMissing    = "baseline_missing"
	ErrCodeInvalidRegistry    = "invalid_registry"
	ErrCodeSchemaVerification = "schema_verification_failed"
	// ErrCodeContractPrecondition marks a contract execution refused because a
	// caller-supplied precondition (backfills, cluster, backup, maintenance
	// window) failed before any statement ran.
	ErrCodeContractPrecondition = "contract_precondition_failed"
	// ErrCodeRepairRefused marks a repair that could neither prove the target
	// state nor safely retry the migration.
	ErrCodeRepairRefused = "repair_refused"
	// ErrCodeNotDirty marks a repair requested for a version without a dirty
	// marker.
	ErrCodeNotDirty = "not_dirty"
	// ErrCodeExpandPending marks a contract attempt while expand migrations
	// are still unapplied.
	ErrCodeExpandPending = "expand_pending"
	// ErrCodeUnrecognizedDatabase marks a refused legacy adoption: the
	// database does not look like a known TokenHub release.
	ErrCodeUnrecognizedDatabase = "unrecognized_database"
)

// Error carries a stable machine-readable code so startup refusals and audit
// records do not depend on driver-specific error text.
type Error struct {
	Code    string
	Version int64
	Err     error
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil && e.Version != 0:
		return fmt.Sprintf("dbschema: %s (version %d): %v", e.Code, e.Version, e.Err)
	case e.Err != nil:
		return fmt.Sprintf("dbschema: %s: %v", e.Code, e.Err)
	default:
		return fmt.Sprintf("dbschema: %s", e.Code)
	}
}

func (e *Error) Unwrap() error { return e.Err }

func newError(code string, version int64, err error) *Error {
	return &Error{Code: code, Version: version, Err: err}
}

// Execer is the execution surface a migration body receives. It is satisfied
// by *sql.DB, *sql.Conn, and *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// RowScanner is the result of a single-row migration query. The interface lets
// the budget wrapper return a deferred budget error from Scan without issuing
// another database statement.
type RowScanner interface {
	Scan(dest ...any) error
}

// MigrationExecer is the budgeted execution surface exposed to Go migrations.
type MigrationExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) RowScanner
}

// GoMigration is a migration implemented as a Go callback instead of SQL.
type GoMigration func(ctx context.Context, db MigrationExecer) error

// Migration is one registered schema change. Exactly one of Statements or Go
// must be set. Content is frozen once released: the checksum of an applied
// migration is verified on every startup.
type Migration struct {
	Version int64
	Name    string
	Phase   Phase
	// Dialect restricts the migration to one engine; empty means all engines.
	Dialect Dialect
	// Statements are executed verbatim in order.
	Statements []string
	Go         GoMigration
	// NonTransactional opts out of per-migration transaction atomicity. A
	// non-transactional migration must declare Postcondition; while it runs the
	// ledger holds a dirty marker that refuses startup until the migration is
	// proven complete.
	NonTransactional bool
	Postcondition    func(ctx context.Context, db MigrationExecer) error
	// SafeRetry declares that re-executing the statements after a failed
	// non-transactional run is harmless. Repair may then drop the dirty row
	// and re-apply the migration; without it a failed postcondition refuses
	// repair (no generic force-version escape hatch).
	SafeRetry bool
	// ChecksumOverride pins the checksum explicitly. It is required for Go
	// migrations, whose source checksum is produced by the build-time manifest.
	ChecksumOverride string
	// LockTimeoutSeconds declares how long this migration may hold the
	// database lock. Expands keep the short default so startup never blocks
	// the cluster for long; contracts may declare a longer budget because
	// they only run inside the maintenance window.
	LockTimeoutSeconds int64
	// StatementBudget bounds the work one migration may perform: an expand
	// must not rewrite unbounded tables, and a contract may claim a longer
	// budget only when its maintenance conditions are met.
	StatementBudget int64
}

const (
	// DefaultExpandLockTimeoutSeconds is the short lock budget every expand
	// migration carries unless it declares its own.
	DefaultExpandLockTimeoutSeconds int64 = 30
	// DefaultExpandStatementBudget bounds the statements of an expand so it
	// cannot rewrite unbounded tables.
	DefaultExpandStatementBudget int64 = 50
	// DefaultContractLockTimeoutSeconds is the longer lock budget a contract
	// may use; contracts only run inside the maintenance window.
	DefaultContractLockTimeoutSeconds int64 = 600
	// DefaultContractStatementBudget bounds the work of one contract run.
	DefaultContractStatementBudget int64 = 200
)

func (m Migration) transactional() bool { return !m.NonTransactional }

// Checksum returns the stable checksum of the migration's frozen content.
func (m Migration) Checksum() string {
	if m.ChecksumOverride != "" {
		return m.ChecksumOverride
	}
	dialectTag := string(m.Dialect)
	if dialectTag == "" {
		dialectTag = "*"
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "tokenhub-migration-v1\n%s\n%s\n", m.Phase, dialectTag)
	for _, statement := range m.Statements {
		_, _ = fmt.Fprintf(hash, "%s\n--\n", statement)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Applied is one row of the schema_migrations ledger.
type Applied struct {
	Version        int64
	Name           string
	Phase          Phase
	Checksum       string
	Dirty          bool
	AppliedAt      string
	AppliedRelease string
}

// Status is the runner's read-only view of the ledger.
type Status struct {
	BaselineRecorded bool
	CurrentVersion   int64
	Applied          []Applied
	PendingExpand    []Migration
	PendingContract  []Migration
	Dirty            bool
	DirtyVersion     int64
}

// Result reports what a Migrate or Adopt call changed.
type Result struct {
	Adopted bool
	Applied []Applied
}

const (
	// BaselineVersion is the ledger version recorded when a legacy or fresh
	// database is adopted by the frozen bridge-release flow.
	BaselineVersion int64 = 1
	baselineName          = "legacy-adoption-baseline"
	// DefaultLockWait bounds how long the runner waits for the cross-process
	// migration lock before staying not-ready instead of blocking forever.
	DefaultLockWait = 2 * time.Minute
)

// AdoptionChecksum pins the adoption pseudo-migration. A later release binds it
// to the release manifest checksum.
var AdoptionChecksum = func() string {
	sum := sha256.Sum256([]byte("tokenhub:legacy-adoption:v1"))
	return hex.EncodeToString(sum[:])
}()

// Runner applies and verifies schema migrations for one database handle.
type Runner struct {
	db                   *sql.DB
	dialect              Dialect
	registry             []Migration
	lockWait             time.Duration
	appRelease           string
	executor             string
	externalCoordination bool
	adoptionReference    func(ctx context.Context) (ObjectSet, error)
	freshBaseline        []string
	legacyRecognizer     func(ctx context.Context, db *sql.DB) error
	log                  func(format string, args ...any)
}

// Option customizes a Runner.
type Option func(*Runner)

// WithLockWait overrides the bounded wait for the migration lock.
func WithLockWait(wait time.Duration) Option {
	return func(r *Runner) { r.lockWait = wait }
}

// WithAppRelease stamps the release identifier into ledger and attempt rows.
func WithAppRelease(release string) Option {
	return func(r *Runner) { r.appRelease = release }
}

// WithExecutor stamps the executing instance into migration_attempts rows
// (the audit records who ran each attempt). Callers that run
// migrations from a known context — the db maintenance CLI or server startup —
// should pass a stable identifier such as the host name.
func WithExecutor(executor string) Option {
	return func(r *Runner) { r.executor = executor }
}

// WithLogger receives bounded-wait diagnostics while the runner queues behind
// another executor.
func WithLogger(log func(format string, args ...any)) Option {
	return func(r *Runner) {
		if log != nil {
			r.log = log
		}
	}
}

// WithExternalCoordination declares that the caller already serializes schema
// work across processes (for example the store's advisory-locked startup
// section); the runner then skips its own lock acquisition.
func WithExternalCoordination() Option {
	return func(r *Runner) { r.externalCoordination = true }
}

// WithAdoptionReference supplies a reference schema snapshot that the runner
// verifies semantically against the database before recording the adoption
// baseline. The builder only runs on the adoption path, never
// on ordinary restarts.
func WithAdoptionReference(builder func(ctx context.Context) (ObjectSet, error)) Option {
	return func(r *Runner) { r.adoptionReference = builder }
}

// WithFreshBaseline supplies the frozen SQL that creates the baseline schema
// on a database that holds no business tables yet. Databases that already
// carry business tables ignore it and run the frozen legacy-adoption callback
// instead.
func WithFreshBaseline(statements []string) Option {
	return func(r *Runner) { r.freshBaseline = statements }
}

// WithLegacyRecognizer supplies a gate that runs before the frozen legacy
// flow and refuses adoption of databases that do not look like a known
// TokenHub release (unrecognized databases refuse to start instead
// of being absorbed into the baseline).
func WithLegacyRecognizer(fn func(ctx context.Context, db *sql.DB) error) Option {
	return func(r *Runner) { r.legacyRecognizer = fn }
}

// NewRunner validates the migration registry and returns a runner for the
// given database handle. Registry versions must be unique, positive, and
// greater than the reserved baseline version.
func NewRunner(db *sql.DB, dialect Dialect, migrations []Migration, opts ...Option) (*Runner, error) {
	if db == nil {
		return nil, errors.New("dbschema: nil database handle")
	}
	switch dialect {
	case DialectSQLite, DialectPostgres:
	default:
		return nil, fmt.Errorf("dbschema: unsupported dialect %q", dialect)
	}
	registry, err := NormalizeMigrations(migrations)
	if err != nil {
		return nil, newError(ErrCodeInvalidRegistry, 0, err)
	}
	runner := &Runner{
		db:       db,
		dialect:  dialect,
		registry: registry,
		lockWait: DefaultLockWait,
		log:      func(string, ...any) {},
	}
	for _, opt := range opts {
		opt(runner)
	}
	if runner.executor == "" {
		runner.executor = "tokenhub"
	}
	return runner, nil
}

// NormalizeMigrations validates and canonicalizes a migration registry:
// unique positive versions above the baseline, a recognized phase and
// dialect, exactly one of Statements or Go, and positive lock and statement
// budgets. Undeclared budgets are filled from the phase defaults so the
// release manifest always carries concrete values.
func NormalizeMigrations(migrations []Migration) ([]Migration, error) {
	registry := make([]Migration, len(migrations))
	copy(registry, migrations)
	sort.Slice(registry, func(i, j int) bool { return registry[i].Version < registry[j].Version })
	seen := make(map[int64]bool, len(registry))
	for i := range registry {
		m := &registry[i]
		if m.Version <= BaselineVersion {
			return nil, fmt.Errorf("migration %q: version %d is reserved or invalid (must be > %d)", m.Name, m.Version, BaselineVersion)
		}
		if seen[m.Version] {
			return nil, fmt.Errorf("duplicate migration version %d", m.Version)
		}
		seen[m.Version] = true
		if m.Name == "" {
			return nil, fmt.Errorf("migration version %d: empty name", m.Version)
		}
		switch m.Phase {
		case "":
			m.Phase = PhaseExpand
		case PhaseExpand, PhaseContract:
		default:
			return nil, fmt.Errorf("migration %q: invalid phase %q", m.Name, m.Phase)
		}
		switch m.Dialect {
		case "", DialectSQLite, DialectPostgres:
		default:
			return nil, fmt.Errorf("migration %q: invalid dialect %q", m.Name, m.Dialect)
		}
		hasSQL, hasGo := len(m.Statements) > 0, m.Go != nil
		switch {
		case hasSQL && hasGo:
			return nil, fmt.Errorf("migration %q: statements and Go callback are mutually exclusive", m.Name)
		case !hasSQL && !hasGo:
			return nil, fmt.Errorf("migration %q: no statements and no Go callback", m.Name)
		case hasGo && m.ChecksumOverride == "":
			return nil, fmt.Errorf("migration %q: Go migrations require ChecksumOverride", m.Name)
		}
		if m.NonTransactional && m.Postcondition == nil {
			return nil, fmt.Errorf("migration %q: non-transactional migrations require Postcondition", m.Name)
		}
		if m.SafeRetry && !m.NonTransactional {
			return nil, fmt.Errorf("migration %q: SafeRetry requires NonTransactional", m.Name)
		}
		if m.Phase == PhaseContract {
			if m.LockTimeoutSeconds == 0 {
				m.LockTimeoutSeconds = DefaultContractLockTimeoutSeconds
			}
			if m.StatementBudget == 0 {
				m.StatementBudget = DefaultContractStatementBudget
			}
		} else {
			if m.LockTimeoutSeconds == 0 {
				m.LockTimeoutSeconds = DefaultExpandLockTimeoutSeconds
			}
			if m.StatementBudget == 0 {
				m.StatementBudget = DefaultExpandStatementBudget
			}
		}
		if m.LockTimeoutSeconds < 0 || m.StatementBudget < 0 {
			return nil, fmt.Errorf("migration %q: lock and statement budgets must be non-negative", m.Name)
		}
	}
	return registry, nil
}

// Status ensures the ledger tables exist and reports the current migration
// state, including pending expand and contract migrations.
func (r *Runner) Status(ctx context.Context) (Status, error) {
	if err := r.ensureLedger(ctx); err != nil {
		return Status{}, err
	}
	applied, err := r.loadApplied(ctx)
	if err != nil {
		return Status{}, err
	}
	status := Status{Applied: applied}
	appliedSet := make(map[int64]bool, len(applied))
	for _, row := range applied {
		appliedSet[row.Version] = true
		if row.Version > status.CurrentVersion {
			status.CurrentVersion = row.Version
		}
		if row.Version == BaselineVersion {
			status.BaselineRecorded = true
		}
		if row.Dirty {
			status.Dirty = true
			if row.Version > status.DirtyVersion {
				status.DirtyVersion = row.Version
			}
		}
	}
	status.PendingExpand = r.pendingByPhase(applied, PhaseExpand)
	status.PendingContract = r.pendingByPhase(applied, PhaseContract)
	return status, nil
}

// pendingByPhase returns the registry migrations of the given phase that are
// not yet applied and apply to the runner's dialect.
func (r *Runner) pendingByPhase(applied []Applied, phase Phase) []Migration {
	appliedSet := make(map[int64]bool, len(applied))
	for _, row := range applied {
		appliedSet[row.Version] = true
	}
	var pending []Migration
	for _, m := range r.registry {
		if appliedSet[m.Version] || m.Phase != phase || !m.appliesTo(r.dialect) {
			continue
		}
		pending = append(pending, m)
	}
	return pending
}

// Verify checks ledger integrity without applying anything: the adoption
// baseline must be present, applied versions must match registry checksums,
// and no unknown versions or dirty state may exist. An empty ledger is a
// missing baseline, not a clean one.
func (r *Runner) Verify(ctx context.Context) error {
	if err := r.ensureLedger(ctx); err != nil {
		return err
	}
	applied, err := r.loadApplied(ctx)
	if err != nil {
		return err
	}
	if findApplied(applied, BaselineVersion) == nil {
		return newError(ErrCodeBaselineMissing, BaselineVersion, errNoBaseline)
	}
	return r.verifyApplied(applied)
}

func (r *Runner) verifyApplied(applied []Applied) error {
	registryByVer := make(map[int64]Migration, len(r.registry))
	for _, m := range r.registry {
		registryByVer[m.Version] = m
	}
	for _, row := range applied {
		if row.Dirty {
			return newError(ErrCodeDirtyState, row.Version, errors.New("ledger holds a dirty migration; repair required before startup"))
		}
		if row.Version == BaselineVersion {
			if row.Checksum != AdoptionChecksum {
				return newError(ErrCodeChecksumMismatch, row.Version, errors.New("adoption baseline checksum mismatch"))
			}
			continue
		}
		m, ok := registryByVer[row.Version]
		if !ok {
			return newError(ErrCodeUnknownApplied, row.Version, fmt.Errorf("applied version is not in the registry"))
		}
		if m.Checksum() != row.Checksum {
			return newError(ErrCodeChecksumMismatch, row.Version, errors.New("applied migration content changed after release"))
		}
	}
	return nil
}

func (m Migration) appliesTo(dialect Dialect) bool {
	return m.Dialect == "" || m.Dialect == dialect
}
