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

	"tokenhub/backend/internal/guardrails"

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
		if sqliteSupportsWAL(dsn) {
			if err := db.Exec("PRAGMA journal_mode = WAL").Error; err != nil {
				return nil, err
			}
		}
	}

	migrate := func() error {
		if err := db.AutoMigrate(
			&Agent{}, &AgentRevision{}, &AgentInstance{}, &AgentInstanceLease{}, &AgentSkill{},
			&AgentAccessBinding{}, &AgentAccessGroup{}, &AgentAccessGroupMember{},
			&AgentTask{}, &AgentTaskEvent{}, &AgentExecution{}, &AgentExecutionEdge{}, &AgentUsageRecord{},
			&BillingConnector{}, &BillingRecord{}, &BillingRawSnapshot{}, &BillingSyncRun{},
			&ReconciliationRule{}, &ReconciliationRun{}, &ReconciliationItem{},
			&Project{},
			&ProjectTeam{},
			&guardrails.Policy{},
			&guardrails.DetectionItem{},
			&guardrails.Binding{},
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
			&AnalyticsSequence{},
			&RequestLog{},
			&AnalyticsCredential{},
			&RequestPayloadLog{},
			&ImageJob{},
			&ImageAsset{},
			&ResponseJob{},
			&ResponseJobEvent{},
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
		if err := ensureRequestLogCommitSequence(db, driver); err != nil {
			return err
		}
		if err := backfillTeamRelationships(db); err != nil {
			return err
		}
		if err := backfillRequestLogAttribution(db, driver); err != nil {
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
	analyticsDB, err := openAnalyticsDatabase(driver, dsn, config)
	if err != nil {
		return nil, err
	}

	return &GormStore{
		db:                   db,
		analyticsDB:          analyticsDB,
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

func openAnalyticsDatabase(driver string, dsn string, config Config) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch driver {
	case "sqlite":
		dialector = sqlite.Open(dsn)
	case "postgres":
		dialector = postgres.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported analytics database driver: %s", driver)
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		TranslateError: true,
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if driver == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		if err := db.Exec("PRAGMA busy_timeout = 1000").Error; err != nil {
			return nil, err
		}
		if err := db.Exec("PRAGMA query_only = ON").Error; err != nil {
			return nil, err
		}
	} else {
		poolSize := maxInt(1, defaultInt(config.DBMaxOpenConns, 25))
		if poolSize > 2 {
			poolSize = 2
		}
		sqlDB.SetMaxOpenConns(poolSize)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(time.Duration(defaultInt(config.DBConnMaxLifetimeMinutes, 30)) * time.Minute)
	}
	return db, nil
}

func sqliteSupportsWAL(dsn string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(dsn))
	return trimmed != "" && trimmed != ":memory:" && !strings.Contains(trimmed, "mode=memory")
}

func ensureRequestLogCommitSequence(db *gorm.DB, driver string) error {
	sequence := AnalyticsSequence{Name: requestLogSequenceName}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&sequence).Error; err != nil {
		return fmt.Errorf("create request log sequence: %w", err)
	}
	createIndex := "CREATE INDEX IF NOT EXISTS"
	dropIndex := "DROP INDEX IF EXISTS"
	if driver == "postgres" {
		createIndex = "CREATE INDEX CONCURRENTLY IF NOT EXISTS"
		dropIndex = "DROP INDEX CONCURRENTLY IF EXISTS"
	}
	if err := db.Exec(createIndex + ` idx_request_logs_commit_sequence_v2
ON request_logs(commit_sequence)`).Error; err != nil {
		return fmt.Errorf("index request log commit sequence: %w", err)
	}
	if err := db.Exec(createIndex + ` idx_request_logs_project_commit_sequence
ON request_logs(project_id, commit_sequence)`).Error; err != nil {
		return fmt.Errorf("index project request log commit sequence: %w", err)
	}
	if err := db.Exec(dropIndex + " idx_request_logs_commit_sequence").Error; err != nil {
		return fmt.Errorf("replace request log commit sequence index: %w", err)
	}
	var triggerStatements []string
	if driver == "postgres" {
		triggerStatements = []string{`
CREATE OR REPLACE FUNCTION tokenhub_assign_request_log_commit_sequence()
RETURNS trigger AS $function$
BEGIN
  PERFORM pg_advisory_xact_lock_shared(hashtextextended('tokenhub:request-log-checkpoint', 0));
  SELECT sequence_offset + txid_current()
    INTO NEW.commit_sequence
    FROM analytics_sequences
   WHERE name = 'request_logs';
  RETURN NEW;
END;
$function$ LANGUAGE plpgsql;`, `
DO $block$
BEGIN
  IF NOT EXISTS (
    SELECT 1
      FROM pg_trigger
     WHERE tgname = 'tokenhub_request_log_commit_sequence'
       AND tgrelid = 'request_logs'::regclass
  ) THEN
    CREATE TRIGGER tokenhub_request_log_commit_sequence
    BEFORE INSERT ON request_logs
    FOR EACH ROW EXECUTE FUNCTION tokenhub_assign_request_log_commit_sequence();
  END IF;
END;
$block$;`}
	} else {
		triggerStatements = []string{`
CREATE TRIGGER IF NOT EXISTS tokenhub_request_log_commit_sequence
AFTER INSERT ON request_logs
FOR EACH ROW WHEN NEW.commit_sequence <= 0
BEGIN
  UPDATE analytics_sequences
     SET last_value = last_value + 1
   WHERE name = 'request_logs';
  UPDATE request_logs
     SET commit_sequence = (
       SELECT last_value FROM analytics_sequences WHERE name = 'request_logs'
     )
   WHERE id = NEW.id;
END;`}
	}
	for _, statement := range triggerStatements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create request log sequence trigger: %w", err)
		}
	}
	if err := backfillRequestLogCommitSequence(db, driver); err != nil {
		return err
	}
	return nil
}

func backfillRequestLogCommitSequence(db *gorm.DB, driver string) error {
	if driver == "postgres" {
		return db.Transaction(func(tx *gorm.DB) error {
			var sequence AnalyticsSequence
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				First(&sequence, "name = ?", requestLogSequenceName).Error; err != nil {
				return err
			}
			// Frozen history only needs event-time ordering once. PostgreSQL's MVCC
			// lets new request logs keep committing while this update scans old rows.
			if !sequence.HistoryMigrated {
				if err := tx.Exec(`
WITH ranked AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY created_at ASC, id ASC) AS sequence
    FROM request_logs
)
UPDATE request_logs AS rl
   SET commit_sequence = ranked.sequence
  FROM ranked
 WHERE rl.id = ranked.id`).Error; err != nil {
					return fmt.Errorf("convert legacy request log commit sequence: %w", err)
				}
				sequence.HistoryMigrated = true
			}
			// Inserts share this advisory lock, so the indexed MAX and offset update
			// form a short rebase barrier without serializing inserts with each other.
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", requestLogCheckpointLockName).Error; err != nil {
				return err
			}
			var maximum int64
			if err := tx.Model(&RequestLog{}).Select("COALESCE(MAX(commit_sequence), 0)").Scan(&maximum).Error; err != nil {
				return err
			}
			var transactionID int64
			if err := tx.Raw("SELECT txid_current()::bigint").Scan(&transactionID).Error; err != nil {
				return err
			}
			requiredOffset := maximum - transactionID + 1
			offset := max(int64(0), sequence.SequenceOffset, requiredOffset)
			if err := tx.Model(&AnalyticsSequence{}).
				Where("name = ?", requestLogSequenceName).
				Updates(map[string]any{
					"sequence_offset":  offset,
					"history_migrated": sequence.HistoryMigrated,
				}).Error; err != nil {
				return err
			}
			return nil
		})
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var sequence AnalyticsSequence
		if err := tx.Model(&AnalyticsSequence{}).
			Where("name = ?", requestLogSequenceName).
			UpdateColumn("last_value", gorm.Expr("last_value")).Error; err != nil {
			return err
		}
		if err := tx.First(&sequence, "name = ?", requestLogSequenceName).Error; err != nil {
			return err
		}
		var maximum int64
		if err := tx.Model(&RequestLog{}).Select("COALESCE(MAX(commit_sequence), 0)").Scan(&maximum).Error; err != nil {
			return err
		}
		base := max(sequence.LastValue, maximum)
		var count int64
		if err := tx.Model(&RequestLog{}).Where("commit_sequence <= 0").Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			var statement string
			if driver == "postgres" {
				statement = `
WITH ranked AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY created_at ASC, id ASC) AS position
    FROM request_logs
   WHERE commit_sequence <= 0
)
UPDATE request_logs AS rl
   SET commit_sequence = ? + ranked.position
  FROM ranked
 WHERE rl.id = ranked.id`
			} else {
				statement = `
WITH ranked AS MATERIALIZED (
  SELECT id, ? + ROW_NUMBER() OVER (ORDER BY created_at ASC, id ASC) AS sequence
    FROM request_logs
   WHERE commit_sequence <= 0
)
UPDATE request_logs
   SET commit_sequence = (
     SELECT sequence FROM ranked WHERE ranked.id = request_logs.id
   )
 WHERE commit_sequence <= 0`
			}
			if err := tx.Exec(statement, base).Error; err != nil {
				return fmt.Errorf("backfill request log commit sequence: %w", err)
			}
			base += count
		}
		if sequence.LastValue != base {
			if err := tx.Model(&AnalyticsSequence{}).
				Where("name = ?", requestLogSequenceName).
				Update("last_value", base).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func backfillRequestLogAttribution(db *gorm.DB, driver string) error {
	emptyAttribution := "COALESCE(attributed_user_id, '') = ''"
	type attributionSource struct {
		name   string
		value  *gorm.DB
		exists *gorm.DB
	}
	sources := []attributionSource{
		{
			name: "usage record",
			value: db.Table("usage_records AS ur").Select("MAX(ur.attributed_user_id)").
				Where("ur.request_id = request_logs.request_id").Where("ur.attributed_user_id <> ''"),
			exists: db.Table("usage_records AS ur").Select("1").
				Where("ur.request_id = request_logs.request_id").Where("ur.attributed_user_id <> ''"),
		},
		{
			name: "API key owner",
			value: db.Table("api_keys AS ak").Select("ak.owner_user_id").
				Where("ak.id = request_logs.api_key_id").Where("ak.owner_user_id <> ''").Limit(1),
			exists: db.Table("api_keys AS ak").Select("1").
				Where("ak.id = request_logs.api_key_id").Where("ak.owner_user_id <> ''"),
		},
		{
			name: "API key creator",
			value: db.Table("api_keys AS ak").Select(apiKeyCreatorExpression(driver)).
				Where("ak.id = request_logs.api_key_id").
				Where(apiKeyCreatorExpression(driver) + " <> ''").Limit(1),
			exists: db.Table("api_keys AS ak").Select("1").
				Where("ak.id = request_logs.api_key_id").
				Where(apiKeyCreatorExpression(driver) + " <> ''"),
		},
		{
			name: "project owner",
			value: db.Table("projects AS p").Select("p.owner_user_id").
				Where("p.id = request_logs.project_id").Where("p.owner_user_id <> ''").Limit(1),
			exists: db.Table("projects AS p").Select("1").
				Where("p.id = request_logs.project_id").Where("p.owner_user_id <> ''"),
		},
	}
	for _, source := range sources {
		if err := db.Model(&RequestLog{}).
			Where(emptyAttribution).
			Where("EXISTS (?)", source.exists).
			Update("attributed_user_id", source.value).Error; err != nil {
			return fmt.Errorf("backfill request log attribution from %s: %w", source.name, err)
		}
	}
	return nil
}

func apiKeyCreatorExpression(driver string) string {
	if driver == "postgres" {
		return "COALESCE(CAST(NULLIF(BTRIM(CAST(ak.metadata AS text)), '') AS jsonb) ->> 'created_by', '')"
	}
	return "CASE WHEN json_valid(ak.metadata) THEN COALESCE(json_extract(ak.metadata, '$.created_by'), '') ELSE '' END"
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
	// A blocking advisory-lock statement remains active while it waits. That
	// would keep CREATE INDEX CONCURRENTLY in the lock holder waiting for this
	// session, so poll with completed statements until the lock is available.
	for {
		var acquired bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", lockName).Scan(&acquired); err != nil {
			return err
		}
		if acquired {
			break
		}
		time.Sleep(50 * time.Millisecond)
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
