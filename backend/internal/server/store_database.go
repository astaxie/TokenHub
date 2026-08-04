package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// parseDatabaseURL parses a database URL and returns the driver type and DSN.
// Supported formats:
//   - sqlite://path/to/db.db
//   - file:...            (SQLite DSN, e.g. in-memory stores)
//   - path/to/db.db       (bare path treated as SQLite)
//   - postgres://user:pass@host:port/dbname?params
//   - postgresql://user:pass@host:port/dbname?params
//   - host=... user=... password=... dbname=... (PostgreSQL keyword DSN)
//
// The keyword DSN form is preferred when the password contains URI delimiters
// such as #, ?, /, or %, which would otherwise be misparsed in the URL form.
func parseDatabaseURL(databaseURL string) (driver string, dsn string, err error) {
	if strings.TrimSpace(databaseURL) == "" {
		return "", "", fmt.Errorf("database URL cannot be empty")
	}

	// PostgreSQL keyword DSN (e.g. "host=db user=u password=p dbname=x").
	// It has no URL scheme, so detect it before attempting url.Parse.
	if isPostgresKeywordDSN(databaseURL) {
		return "postgres", databaseURL, nil
	}

	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid database URL: %w", err)
	}

	switch u.Scheme {
	case "postgres", "postgresql":
		// PostgreSQL URL: postgres://user:pass@host:port/dbname?params
		// Use the original URL directly as the DSN.
		return "postgres", databaseURL, nil

	case "sqlite", "file", "":
		// SQLite: sqlite:// URLs, file: DSNs (in-memory stores), or bare paths.
		// sqliteDSN handles all of these for backwards compatibility.
		dsn, err := sqliteDSN(databaseURL)
		if err != nil {
			return "", "", err
		}
		return "sqlite", dsn, nil

	default:
		return "", "", fmt.Errorf("unsupported database scheme: %s (supported: sqlite, file, postgres, postgresql)", u.Scheme)
	}
}

// isPostgresKeywordDSN reports whether the string is a PostgreSQL keyword/value
// DSN (e.g. "host=localhost user=tokenhub password=secret dbname=tokenhub")
// rather than a URL. Such DSNs have no "scheme://" prefix and begin with a
// recognized connection keyword.
func isPostgresKeywordDSN(databaseURL string) bool {
	trimmed := strings.TrimSpace(databaseURL)
	if strings.Contains(trimmed, "://") {
		return false
	}
	firstField := strings.SplitN(trimmed, "=", 2)
	if len(firstField) != 2 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(firstField[0])) {
	case "host", "hostaddr", "user", "dbname", "port", "password", "sslmode":
		return true
	}
	return false
}

// redactDatabaseURL redacts the password in database URL for safe logging
func redactDatabaseURL(databaseURL string) string {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "<invalid-url>"
	}
	if u.User != nil {
		username := u.User.Username()
		_, hasPassword := u.User.Password()
		if hasPassword {
			// Hide password only, preserve username
			u.User = url.UserPassword(username, "****")
		} else {
			// No password in original URL, keep username only
			u.User = url.User(username)
		}
	}
	// PostgreSQL URIs also allow credentials in query parameters
	// (for example, ?user=u&password=secret). Mask any password-bearing keys.
	if query := u.Query(); len(query) > 0 {
		changed := false
		for key := range query {
			switch strings.ToLower(key) {
			case "password", "passwd", "pgpassword":
				query.Set(key, "****")
				changed = true
			}
		}
		if changed {
			u.RawQuery = query.Encode()
		}
	}
	return u.String()
}

func OpenStoreWithConfig(databaseURL string, config Config) (*GormStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		databaseURL = defaultConfigDatabaseURL()
	}
	return NewStoreWithDialect(databaseURL, config)
}

func NewSQLiteStore(databaseURL string) (*GormStore, error) {
	return NewStoreWithDialect(databaseURL, ConfigFromEnv())
}

// NewStoreWithDialect creates a Store with the appropriate driver based on the database URL.
// It supports SQLite and PostgreSQL.
func NewStoreWithDialect(databaseURL string, config Config) (*GormStore, error) {
	driver, dsn, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return nil, err
	}

	log.Printf("[tokenhub] initializing database: driver=%s url=%s", driver, redactDatabaseURL(databaseURL))

	var dialector gorm.Dialector
	switch driver {
	case "sqlite":
		dialector = sqlite.Open(dsn)
	case "postgres":
		dialector = postgres.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", driver)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		TranslateError: true,
		Logger: gormlogger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			gormlogger.Config{
				SlowThreshold:             time.Second,
				LogLevel:                  gormlogger.Silent,
				IgnoreRecordNotFoundError: true,
			},
		),
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Configure the connection pool based on database type. PostgreSQL keeps a
	// dedicated connection for the migration advisory lock, so migrations need
	// at least one additional connection even when the runtime pool is set to 1.
	postgresMaxOpenConns := 0
	if driver == "postgres" {
		// PostgreSQL uses connection pooling.
		maxOpenConns := defaultInt(config.DBMaxOpenConns, 25)
		maxIdleConns := defaultInt(config.DBMaxIdleConns, 5)
		connMaxLifetime := time.Duration(defaultInt(config.DBConnMaxLifetimeMinutes, 30)) * time.Minute

		postgresMaxOpenConns = maxOpenConns
		sqlDB.SetMaxOpenConns(maxInt(2, maxOpenConns))
		sqlDB.SetMaxIdleConns(maxIdleConns)
		sqlDB.SetConnMaxLifetime(connMaxLifetime)
	} else {
		// SQLite maintains a single connection.
		sqlDB.SetMaxOpenConns(1)
	}

	// SQLite-specific configuration.
	if driver == "sqlite" {
		if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
			return nil, err
		}
		if err := db.Exec("PRAGMA busy_timeout = 5000").Error; err != nil {
			return nil, err
		}
	}

	migrate := func() error {
		if err := db.AutoMigrate(
			&BillingConnector{}, &BillingRecord{}, &BillingRawSnapshot{}, &BillingSyncRun{},
			&GatewayTenant{},
			&GatewayOrganization{},
			&GatewayPrincipal{},
			&GatewayPrincipalOrganizationBinding{},
			&GatewayProject{},
			&GatewayWorkload{},
			&IntegrationInbox{},
			&Project{},
			&ProjectTeam{},
			&APIKey{},
			&Provider{},
			&ProviderResource{},
			&ProviderModel{},
			&Model{},
			&ModelRoute{},
			&QuotaBucket{},
			&InFlightLease{},
			&ClusterLease{},
			&ClusterTaskState{},
			&AdapterSessionBinding{},
			&ProviderResourceObservation{},
			&ProviderObservation{},
			&ProviderCatalogSnapshot{},
			&providerAccountOAuthSessionRecord{},
			&UsageRecord{},
			&RequestLog{},
			&RequestPayloadLog{},
			&ImageJob{},
			&ImageAsset{},
			&RouteAttemptLog{},
			&AlertEvent{},
			&AlertDelivery{},
			&ProviderResourceBucket{},
			&AuditEvent{},
			&AdminResource{},
			&ApprovalRequest{},
			&AdminUser{},
			&AdminSession{},
			&AdminPasswordResetToken{},
			&SQLiteBackupRecord{},
		); err != nil {
			return err
		}
		if err := backfillTeamRelationships(db); err != nil {
			return err
		}
		return backfillRoutingPolicyBindingKeys(db)
	}
	if err := runSchemaMigrationLocked(sqlDB, driver, migrate); err != nil {
		return nil, err
	}
	if postgresMaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(postgresMaxOpenConns)
	}

	return &GormStore{
		db:                   db,
		mu:                   &sync.Mutex{},
		leaseHeartbeats:      &sync.Map{},
		secretKey:            config.SecretKey,
		failureThreshold:     defaultInt(config.ResourceFailureThreshold, 3),
		cooldownDuration:     cooldownSecondsToDuration(defaultInt(config.ResourceCooldownSeconds, 300)),
		cooldownMax:          cooldownSecondsToDuration(defaultInt(config.ResourceCooldownMaxSeconds, 3600)),
		sqliteDSN:            dsn,
		backupDir:            defaultString(config.SQLiteBackupDir, "data/backups"),
		dbDriver:             driver,
		inFlightLeaseTTL:     time.Duration(defaultInt(config.InFlightLeaseTTLSeconds, 300)) * time.Second,
		clusterLockTTL:       time.Duration(defaultInt(config.ClusterLockTTLSeconds, 180)) * time.Second,
		imageCapabilityRetry: time.Duration(defaultInt(config.ImageCapabilityRetrySecs, 86400)) * time.Second,
	}, nil
}

func backfillRoutingPolicyBindingKeys(db *gorm.DB) error {
	var policies []AdminResource
	if err := db.Where("kind = ?", routingPolicyResourceKind).Find(&policies).Error; err != nil {
		return err
	}
	for _, policy := range policies {
		bindingKey := routingPolicyBindingKey(policy.Fields)
		if bindingKey == nil {
			continue
		}
		if err := db.Model(&AdminResource{}).
			Where("kind = ? AND id = ?", routingPolicyResourceKind, policy.ID).
			Update("routing_policy_binding_key", *bindingKey).Error; err != nil {
			return fmt.Errorf("backfill routing policy binding %q: %w", policy.ID, err)
		}
	}
	return nil
}

func backfillTeamRelationships(db *gorm.DB) error {
	var projects []Project
	if err := db.Select("id", "team_id", "created_at", "updated_at").Where("team_id <> ''").Find(&projects).Error; err != nil {
		return err
	}
	for _, project := range projects {
		createdAt := project.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		updatedAt := project.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = createdAt
		}
		link := ProjectTeam{
			ProjectID: project.ID,
			TeamID:    strings.TrimSpace(project.TeamID),
			Role:      "team_leader",
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&AdminResource{
			ID:          link.TeamID,
			Kind:        "teams",
			Name:        link.TeamID,
			Description: "Compatibility team migrated from a legacy project assignment.",
			Status:      StatusActive,
			Fields:      map[string]any{},
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
		}).Error; err != nil {
			return err
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; err != nil {
			return err
		}
	}

	type storedAdminUserTeams struct {
		ID         string
		TeamID     string
		TeamIDsRaw sql.NullString `gorm:"column:team_ids"`
	}
	var users []storedAdminUserTeams
	if err := db.Table("admin_users").Select("id", "team_id", "team_ids").Scan(&users).Error; err != nil {
		return err
	}
	for _, user := range users {
		additionalTeamIDs := []string{}
		rawTeamIDs := strings.TrimSpace(user.TeamIDsRaw.String)
		if rawTeamIDs != "" {
			if err := json.Unmarshal([]byte(rawTeamIDs), &additionalTeamIDs); err != nil {
				// A previous migration wrote the primary team as plain text. Treat
				// other malformed values as untrusted and recover from TeamID only.
				additionalTeamIDs = nil
			}
		}
		teamIDs := normalizedTeamIDs(user.TeamID, additionalTeamIDs)
		serializedTeamIDs, err := json.Marshal(teamIDs)
		if err != nil {
			return err
		}
		if rawTeamIDs == string(serializedTeamIDs) {
			continue
		}
		if err := db.Model(&AdminUser{}).Where("id = ?", user.ID).UpdateColumn("team_ids", string(serializedTeamIDs)).Error; err != nil {
			return err
		}
	}
	return nil
}

// WithContext returns a store view whose database operations inherit ctx.
// Synchronization and lease bookkeeping remain shared with the parent store.
func (s *GormStore) WithContext(ctx context.Context) *GormStore {
	if ctx == nil {
		ctx = context.Background()
	}
	contextual := *s
	contextual.db = s.db.WithContext(ctx)
	return &contextual
}

func runSchemaMigrationLocked(sqlDB *sql.DB, driver string, migrate func() error) error {
	if driver != "postgres" {
		return migrate()
	}
	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	const lockName = "tokenhub:schema-migration"
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", lockName); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockName)
	}()
	return migrate()
}

// NewSQLiteStoreWithConfig is retained as a compatibility alias.
func NewSQLiteStoreWithConfig(databaseURL string, config Config) (*GormStore, error) {
	return NewStoreWithDialect(databaseURL, config)
}

// NewMemoryStoreWithConfig opens a private in-memory SQLite store using an
// explicit configuration. Callers that must not inherit process environment
// settings — migration sinks and tests — use this instead of NewMemoryStore.
func NewMemoryStoreWithConfig(config Config) *MemoryStore {
	store, err := NewSQLiteStoreWithConfig(fmt.Sprintf("file:%s?mode=memory&cache=shared", NewID("mem")), config)
	if err != nil {
		panic(err)
	}
	return store
}

// NewMemoryStore opens a private in-memory SQLite store configured from the
// process environment.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithConfig(ConfigFromEnv())
}

// RunClusterTask runs fn once for the requested monotonic revision across all
// replicas sharing the database. A failed task is not recorded and is retried
// by the next replica.
func (s *GormStore) RunClusterTask(ctx context.Context, name string, revision int64, fn func(context.Context) error) error {
	name = strings.TrimSpace(name)
	if name == "" || revision <= 0 {
		return fmt.Errorf("cluster task name and positive revision are required")
	}
	return s.withClusterLease(ctx, "task:"+name, func(leaseCtx context.Context) error {
		var state ClusterTaskState
		err := s.db.WithContext(leaseCtx).First(&state, "name = ?", name).Error
		if err == nil && state.Revision >= revision {
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := fn(leaseCtx); err != nil {
			return err
		}
		if err := context.Cause(leaseCtx); err != nil {
			return err
		}
		state = ClusterTaskState{Name: name, Revision: revision, CompletedAt: time.Now().UTC()}
		return s.db.WithContext(leaseCtx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&state).Error
	})
}

// RunClusterOperation serializes an idempotent operation across all replicas.
// Unlike RunClusterTask, it runs once for every caller instead of recording a
// completed revision.
func (s *GormStore) RunClusterOperation(ctx context.Context, name string, fn func(context.Context) error) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("cluster operation name is required")
	}
	return s.withClusterLease(ctx, "operation:"+name, fn)
}

func effectiveLeaseTTL(value time.Duration, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func leaseRenewalInterval(ttl time.Duration) time.Duration {
	interval := ttl / 3
	if interval < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	return interval
}

func leaseSafetyWindow(ttl time.Duration) time.Duration {
	window := ttl / 10
	if window < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	if window > time.Second {
		return time.Second
	}
	return window
}

func startLeaseHeartbeat(parent context.Context, ttl time.Duration, confirmedFor time.Duration, renew func(context.Context) (time.Duration, bool, error)) *leaseHeartbeat {
	if parent == nil {
		parent = context.Background()
	}
	confirmedUntil := time.Now().Add(confirmedFor)
	leaseCtx, cancel := context.WithCancelCause(parent)
	heartbeat := &leaseHeartbeat{
		ctx:    leaseCtx,
		cancel: cancel,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(heartbeat.done)
		nextDelay := leaseRenewalInterval(ttl)
		safetyWindow := leaseSafetyWindow(ttl)
		for {
			timer := time.NewTimer(nextDelay)
			select {
			case <-heartbeat.stop:
				timer.Stop()
				return
			case <-leaseCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}

			remaining := time.Until(confirmedUntil)
			if remaining <= safetyWindow {
				cancel(ErrCoordinationLeaseLost)
				return
			}
			attemptTimeout := (remaining - safetyWindow) / 2
			if attemptTimeout > 2*time.Second {
				attemptTimeout = 2 * time.Second
			}
			if attemptTimeout < 50*time.Millisecond {
				attemptTimeout = 50 * time.Millisecond
			}
			attemptCtx, stopAttempt := context.WithTimeout(leaseCtx, attemptTimeout)
			renewedFor, retained, err := renew(attemptCtx)
			stopAttempt()
			if err == nil && retained {
				if renewedFor <= safetyWindow {
					cancel(ErrCoordinationLeaseLost)
					return
				}
				confirmedUntil = time.Now().Add(renewedFor)
				nextDelay = leaseRenewalInterval(ttl)
				continue
			}
			if err == nil {
				cancel(ErrCoordinationLeaseLost)
				return
			}

			remaining = time.Until(confirmedUntil)
			if remaining <= safetyWindow {
				cancel(ErrCoordinationLeaseLost)
				return
			}
			nextDelay = (remaining - safetyWindow) / 2
			if nextDelay > time.Second {
				nextDelay = time.Second
			}
			if nextDelay < 50*time.Millisecond {
				nextDelay = 50 * time.Millisecond
			}
		}
	}()
	return heartbeat
}

func stopLeaseHeartbeat(heartbeat *leaseHeartbeat) error {
	if heartbeat == nil {
		return nil
	}
	close(heartbeat.stop)
	heartbeat.cancel(context.Canceled)
	<-heartbeat.done
	cause := context.Cause(heartbeat.ctx)
	if errors.Is(cause, ErrCoordinationLeaseLost) {
		return ErrCoordinationLeaseLost
	}
	return nil
}

func (s *GormStore) databaseNow(db *gorm.DB) (time.Time, error) {
	var epoch float64
	query := "SELECT (julianday('now') - 2440587.5) * 86400"
	if s.dbDriver == "postgres" {
		query = "SELECT EXTRACT(EPOCH FROM clock_timestamp())::double precision"
	}
	if err := db.Raw(query).Scan(&epoch).Error; err != nil {
		return time.Time{}, err
	}
	seconds, fraction := math.Modf(epoch)
	return time.Unix(int64(seconds), int64(math.Round(fraction*float64(time.Second)))).UTC(), nil
}

func (s *GormStore) persistedLeaseConfirmation(db *gorm.DB, expiresAt time.Time) (time.Duration, error) {
	now, err := s.databaseNow(db)
	if err != nil {
		return 0, err
	}
	remaining := expiresAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

func (s *GormStore) tryAcquireClusterLease(ctx context.Context, name string, ownerID string, ttl time.Duration) (bool, time.Duration, error) {
	var acquired bool
	var confirmedFor time.Duration
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		expiresAt := now.Add(ttl)
		result := tx.Exec(`
			INSERT INTO cluster_leases (name, owner_id, expires_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (name) DO UPDATE SET
				owner_id = excluded.owner_id,
				expires_at = excluded.expires_at,
				updated_at = excluded.updated_at
			WHERE cluster_leases.expires_at <= ?`, name, ownerID, expiresAt, now, now)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		var lease ClusterLease
		if err := tx.Select("expires_at").First(&lease, "name = ? AND owner_id = ?", name, ownerID).Error; err != nil {
			return err
		}
		confirmedFor, err = s.persistedLeaseConfirmation(tx, lease.ExpiresAt)
		if err != nil {
			return err
		}
		acquired = confirmedFor > 0
		return nil
	})
	return acquired, confirmedFor, err
}

func (s *GormStore) renewClusterLease(ctx context.Context, name string, ownerID string, ttl time.Duration) (time.Duration, bool, error) {
	var confirmedFor time.Duration
	var retained bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		result := tx.Model(&ClusterLease{}).
			Where("name = ? AND owner_id = ?", name, ownerID).
			Updates(map[string]any{"expires_at": now.Add(ttl), "updated_at": now})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		var lease ClusterLease
		if err := tx.Select("expires_at").First(&lease, "name = ? AND owner_id = ?", name, ownerID).Error; err != nil {
			return err
		}
		confirmedFor, err = s.persistedLeaseConfirmation(tx, lease.ExpiresAt)
		if err != nil {
			return err
		}
		retained = confirmedFor > 0
		return nil
	})
	return confirmedFor, retained, err
}

func (s *GormStore) withClusterLease(ctx context.Context, name string, fn func(context.Context) error) error {
	ownerID := NewID("lock")
	ttl := effectiveLeaseTTL(s.clusterLockTTL, 180*time.Second)
	var confirmedFor time.Duration
	for {
		acquired, confirmation, err := s.tryAcquireClusterLease(ctx, name, ownerID, ttl)
		if err != nil {
			return err
		}
		if acquired {
			confirmedFor = confirmation
			break
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	heartbeat := startLeaseHeartbeat(ctx, ttl, confirmedFor, func(attemptCtx context.Context) (time.Duration, bool, error) {
		return s.renewClusterLease(attemptCtx, name, ownerID, ttl)
	})
	fnErr := fn(heartbeat.ctx)
	leaseErr := stopLeaseHeartbeat(heartbeat)
	_ = s.db.Delete(&ClusterLease{}, "name = ? AND owner_id = ?", name, ownerID).Error
	if leaseErr != nil {
		return leaseErr
	}
	return fnErr
}

func sqliteDSN(databaseURL string) (string, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		databaseURL = defaultConfigDatabaseURL()
	}
	if strings.HasPrefix(databaseURL, "sqlite://") {
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			return "", err
		}
		path := parsed.Path
		if parsed.Host != "" {
			path = filepath.Join(parsed.Host, strings.TrimPrefix(parsed.Path, "/"))
		} else if !strings.HasPrefix(databaseURL, "sqlite:///") {
			path = strings.TrimPrefix(parsed.Path, "/")
		}
		if path == "" {
			path = "data/tokenhub.db"
		}
		if parsed.RawQuery != "" {
			path += "?" + parsed.RawQuery
		}
		return prepareSQLitePath(path)
	}
	if strings.HasPrefix(databaseURL, "sqlite:") {
		return prepareSQLitePath(strings.TrimPrefix(databaseURL, "sqlite:"))
	}
	if strings.Contains(databaseURL, "://") {
		return "", fmt.Errorf("unsupported database URL %q: only sqlite is configured", databaseURL)
	}
	return prepareSQLitePath(databaseURL)
}

func prepareSQLitePath(dsn string) (string, error) {
	if dsn == "" || dsn == ":memory:" || strings.HasPrefix(dsn, "file:") {
		return dsn, nil
	}
	path := dsn
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	if path != "" {
		dir := filepath.Dir(path)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
		}
	}
	return dsn, nil
}
