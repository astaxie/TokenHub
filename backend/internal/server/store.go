package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

const (
	defaultSQLiteDatabaseURL = "sqlite://data/tokenhub.db"
	adaptiveRoutingWindow    = 15 * time.Minute
)

type QuotaBucket struct {
	KeyID  string `gorm:"primaryKey;index"`
	Scope  string `gorm:"primaryKey"`
	Bucket string `gorm:"primaryKey;index"`
	QuotaCounter
}

// InFlightLease makes concurrency enforcement visible to every backend
// instance. Leases are renewed by the owning process and expire automatically
// after a crash so capacity cannot remain permanently wedged.
type InFlightLease struct {
	ID        string    `gorm:"primaryKey"`
	ScopeType string    `gorm:"index:idx_in_flight_scope,priority:1"`
	ScopeID   string    `gorm:"index:idx_in_flight_scope,priority:2"`
	ExpiresAt time.Time `gorm:"index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ClusterLease is a database-backed, expiring mutex used for operations that
// must have a single writer across the whole TokenHub cluster.
type ClusterLease struct {
	Name      string    `gorm:"primaryKey"`
	OwnerID   string    `gorm:"index"`
	ExpiresAt time.Time `gorm:"index"`
	UpdatedAt time.Time
}

// ClusterTaskState prevents every replica from repeating the same startup
// task. Revisions are monotonic: an older binary never overwrites work already
// completed by a newer revision.
type ClusterTaskState struct {
	Name        string `gorm:"primaryKey"`
	Revision    int64
	CompletedAt time.Time
}

type AdapterSessionBinding struct {
	ID              string    `json:"id" gorm:"primaryKey"`
	AdapterType     string    `json:"adapter_type" gorm:"uniqueIndex:idx_adapter_session_binding,priority:1"`
	AffinityKind    string    `json:"affinity_kind"`
	ProviderID      string    `json:"provider_id" gorm:"uniqueIndex:idx_adapter_session_binding,priority:2;index"`
	AffinityKeyHash string    `json:"-" gorm:"uniqueIndex:idx_adapter_session_binding,priority:3"`
	ResourceID      string    `json:"resource_id" gorm:"index"`
	Generation      int64     `json:"generation"`
	RebindReason    string    `json:"rebind_reason,omitempty"`
	LastUsedAt      time.Time `json:"last_used_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ProviderResourceObservation struct {
	ResourceID        string            `json:"resource_id" gorm:"primaryKey"`
	AdapterType       string            `json:"adapter_type" gorm:"index"`
	RateLimitHeaders  map[string]string `json:"rate_limit_headers,omitempty" gorm:"serializer:json"`
	QuotaSnapshot     string            `json:"-" gorm:"type:text"`
	QuotaFetchedAt    *time.Time        `json:"quota_fetched_at,omitempty"`
	UpstreamRequestID string            `json:"upstream_request_id,omitempty"`
	ServedModel       string            `json:"served_model,omitempty"`
	ModelETag         string            `json:"model_etag,omitempty"`
	Transport         string            `json:"transport,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

// ProviderCatalogSnapshot keeps the last known-good public provider catalog in
// the database. SummaryJSON serves the list endpoint without decoding the full
// model catalog, while CatalogJSON retains model details for provider setup.
type ProviderCatalogSnapshot struct {
	ID          string    `gorm:"primaryKey"`
	Source      string    `gorm:"index"`
	SummaryJSON string    `gorm:"type:text"`
	CatalogJSON string    `gorm:"type:text"`
	FetchedAt   time.Time `gorm:"index"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ProviderObservation struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	ProviderID  string    `json:"provider_id" gorm:"index"`
	ResourceID  string    `json:"resource_id,omitempty" gorm:"index"`
	AdapterType string    `json:"adapter_type" gorm:"index"`
	Source      string    `json:"source" gorm:"index"`
	Operation   string    `json:"operation" gorm:"index"`
	Success     bool      `json:"success" gorm:"index"`
	LatencyMS   int64     `json:"latency_ms"`
	ErrorCode   string    `json:"error_code,omitempty"`
	ObservedAt  time.Time `json:"observed_at" gorm:"index"`
}

type Store interface {
	CreateProject(project Project) Project
	CreateProjectChecked(project Project) (Project, error)
	ListProjects() []Project
	UpdateProject(id string, patch Project) (Project, error)
	DeleteProject(id string) error
	GetProject(id string) (Project, bool)
	ListProjectTeams(projectID string, offset int, limit int) ([]ProjectTeam, int64, error)
	AddProjectTeam(link ProjectTeam) (ProjectTeam, error)
	UpdateProjectTeam(projectID string, teamID string, role string) (ProjectTeam, error)
	RemoveProjectTeam(projectID string, teamID string) error
	CreateAPIKey(projectID string, key APIKey, rawSecret string) (APIKey, string, error)
	ListProjectKeys(projectID string) []APIKey
	ListAPIKeys() []APIKey
	UpdateAPIKey(id string, patch APIKey) (APIKey, error)
	RotateAPIKey(id string, graceUntil *time.Time) (APIKey, string, error)
	DeleteAPIKey(id string) error
	ValidateAPIKey(rawSecret string, clientIP string) (Project, APIKey, error)
	AddProvider(provider Provider) (Provider, error)
	GetProvider(id string) (Provider, bool)
	ListProviders() []Provider
	AddProviderModel(model ProviderModel) ProviderModel
	ListProviderModels() []ProviderModel
	UpdateProviderModel(id string, patch ProviderModel) (ProviderModel, error)
	DeleteProviderModel(id string) error
	LoadProviderCatalogSnapshot(includeModels bool) ([]ProviderCatalogEntry, string, time.Time, bool, error)
	SaveProviderCatalogSnapshot(entries []ProviderCatalogEntry, source string, fetchedAt time.Time) error
	UpdateProvider(id string, patch Provider) (Provider, error)
	DeleteProvider(id string) error
	SetProviderHealth(providerID string, healthy bool) (Provider, error)
	AddProviderResource(resource ProviderResource) (ProviderResource, error)
	ListProviderResources() []ProviderResource
	UpdateProviderResource(id string, patch ProviderResource) (ProviderResource, error)
	UpdateProviderResourceOptions(id string, options map[string]string) (ProviderResource, error)
	DeleteProviderResource(id string) error
	SetProviderResourceHealth(resourceID string, healthy bool) (ProviderResource, error)
	BulkOperateProviderResources(action string, ids []string) (ProviderResourceBulkResult, error)
	ImportProviderResources(resources []ProviderResource) (ProviderResourceImportResult, error)
	AddModel(model Model) Model
	ListModels() []Model
	UpdateModel(name string, patch Model) (Model, error)
	DeleteModel(name string) error
	AddRoute(route ModelRoute) ModelRoute
	ListRoutes() []ModelRoute
	UpdateRoute(id string, patch ModelRoute) (ModelRoute, error)
	UpdateModelRoutePolicy(modelName string, policy ModelRoutePolicy) ([]ModelRoute, error)
	DeleteRoute(id string) error
	SelectRoute(modelName string) (RouteSelection, error)
	SelectRouteCandidates(modelName string) ([]RouteSelection, error)
	MarkRouteUsed(routeID string)
	MarkProviderResourceUsed(resourceID string)
	StartCall(ctx context.Context, project Project, key APIKey, modelName string) (CallContext, error)
	FinishCall(call CallContext, route RouteSelection, usage Usage, statusCode int, errorCode string, clientIP string, userAgent string)
	RecordPlaygroundRequest(call CallContext, route RouteSelection, statusCode int, errorCode string, clientIP string, userAgent string)
	RecordRouteAttempts(requestID string, attempts []RouteAttempt)
	RecordRejectedRequest(project Project, key APIKey, modelName string, stream bool, statusCode int, errorCode string, clientIP string, userAgent string) string
	RecordRequestPayload(requestID string, requestBody string, requestTruncated bool, responseBody string, responseTruncated bool)
	CreateImageJob(job ImageJob, prompt string) (ImageJob, error)
	ClaimImageJob(id string) (ImageJob, bool, error)
	GetImageJob(id string) (ImageJob, bool)
	ListImageJobs(limit int) []ImageJob
	FailUnfinishedImageJobs(code string, message string) ([]ImageJob, error)
	UpdateImageJob(job ImageJob, revisedPrompt string) error
	CompleteImageJob(call CallContext, job ImageJob, revisedPrompt string, asset ImageAsset, route RouteSelection, usage Usage, clientIP string, userAgent string) error
	CreateImageAsset(asset ImageAsset) (ImageAsset, error)
	ListImageAssets(jobID string) []ImageAsset
	GetImageAsset(id string) (ImageAsset, bool)
	ListUsageRecords() []UsageRecord
	UsageSummary() map[string]any
	UsageBreakdown() map[string]any
	UsageBreakdownForPeriod(period string) map[string]any
	UsageTimeseries(days int) []map[string]any
	GenerateBillingPeriod(period string) (map[string]any, error)
	ListRequestLogs() []RequestLog
	ListProviderObservations(since time.Time) []ProviderObservation
	RecordProviderObservation(observation ProviderObservation)
	GetProviderResourceObservation(resourceID string) (ProviderResourceObservation, bool)
	SaveProviderResourceQuota(resourceID string, adapterType string, snapshot string, fetchedAt time.Time) error
	GetRequestDetail(requestID string) (map[string]any, error)
	ListAlerts() []AlertEvent
	GetAlert(id string) (AlertEvent, error)
	ListAlertDeliveries() []AlertDelivery
	RecordAlertDelivery(delivery AlertDelivery) AlertDelivery
	ListAuditEvents() []AuditEvent
	RecordAuditEvent(event AuditEvent)
	CreateResource(kind string, resource AdminResource) AdminResource
	ListResources(kind string) []AdminResource
	UpdateResource(kind string, id string, patch AdminResource) (AdminResource, error)
	DeleteResource(kind string, id string) error
	DeleteTeam(id string) error
	RunMonitor(id string) (MonitorRunResult, error)
	CreateApprovalRequest(request ApprovalRequest) ApprovalRequest
	ListApprovalRequests() []ApprovalRequest
	GetApprovalRequest(id string) (ApprovalRequest, error)
	UpdateApprovalRequestStatus(id string, status string, decidedBy string, reason string) (ApprovalRequest, error)
	CreateAdminUser(user AdminUser, password string) (AdminUser, error)
	ListAdminUsers() []AdminUser
	UpdateAdminUser(id string, patch AdminUser, password string) (AdminUser, error)
	DeleteAdminUser(id string) error
	CreateAdminPasswordResetToken(userID string, createdBy string, ttl time.Duration) (string, AdminPasswordResetToken, error)
	ResetAdminUserPassword(token string, password string) (AdminUser, error)
	AuthenticateAdminUser(identity string, password string, ttl time.Duration) (AdminUser, AdminSession, error)
	CreateAdminSession(userID string, ttl time.Duration) (AdminUser, AdminSession, error)
	ValidateAdminSession(token string) (AdminUser, bool)
	RevokeAdminSession(token string)
	CreateSQLiteBackup(createdBy string, expireDays int) (SQLiteBackupRecord, error)
	ListSQLiteBackups() []SQLiteBackupRecord
	GetSQLiteBackup(id string) (SQLiteBackupRecord, error)
	RestoreSQLiteBackup(id string, restoredBy string) (SQLiteBackupRecord, error)
	DeleteSQLiteBackup(id string) error
	AccessibleModels(key APIKey) []Model
	CheckProviderResourceCapacity(ctx context.Context, resourceID string) (string, context.Context, error)
	CheckProviderResourceRetryCapacity(ctx context.Context, resourceID string, leaseID string) error
	ReleaseProviderResourceCapacity(resourceID string, leaseID string)
	FinishProviderResourceAttempt(ctx context.Context, resourceID string, leaseID string, outcome AttemptOutcome, usage Usage)
	RecoverProviderResource(resourceID string) (ProviderResource, error)
	RefreshProviderResourceCredentials(ctx context.Context, resourceID string, force bool) (ProviderResourceCredentials, error)
	GetAdapterSessionBinding(ctx context.Context, adapterType string, providerID string, affinityKeyHash string) (AdapterSessionBinding, bool, error)
	CommitAdapterSessionBinding(ctx context.Context, binding AdapterSessionBinding, expectedGeneration int64) (AdapterSessionBinding, bool, error)
	RunClusterOperation(ctx context.Context, name string, fn func(context.Context) error) error
	SaveProviderAccountOAuthSession(session providerAccountOAuthSession) error
	GetProviderAccountOAuthSessionByState(state string) (providerAccountOAuthSession, bool, error)
	ConsumeProviderAccountOAuthSession(id string, state string) (providerAccountOAuthSession, bool, error)
	TestProvider(id string) (Provider, error)
	TestProviderResource(id string) (ProviderResource, error)
	GetDatabaseStatus() (map[string]interface{}, error)
	Ping(ctx context.Context) error
}

type GormStore struct {
	db                   *gorm.DB
	mu                   *sync.RWMutex
	leaseHeartbeats      *sync.Map
	secretKey            string
	metrics              *GatewayMetrics
	failureThreshold     int
	cooldownDuration     time.Duration
	cooldownMax          time.Duration
	sqliteDSN            string
	backupDir            string
	dbDriver             string // "sqlite" or "postgres"
	inFlightLeaseTTL     time.Duration
	clusterLockTTL       time.Duration
	imageCapabilityRetry time.Duration
}

type leaseHeartbeat struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	stop    chan struct{}
	done    chan struct{}
	stopped atomic.Bool
}

// MemoryStore is kept as a compatibility alias for existing tests and callers.
// It is now backed by GORM and SQLite, not process-local maps.
type MemoryStore = GormStore

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

func OpenStore(databaseURL string) (*GormStore, error) {
	return OpenStoreWithConfig(databaseURL, ConfigFromEnv())
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
		return backfillTeamRelationships(db)
	}
	if err := runSchemaMigrationLocked(sqlDB, driver, migrate); err != nil {
		return nil, err
	}
	if postgresMaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(postgresMaxOpenConns)
	}

	return &GormStore{
		db:                   db,
		mu:                   &sync.RWMutex{},
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

func NewMemoryStore() *MemoryStore {
	store, err := NewSQLiteStoreWithConfig(fmt.Sprintf("file:%s?mode=memory&cache=shared", NewID("mem")), ConfigFromEnv())
	if err != nil {
		panic(err)
	}
	return store
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
	if !heartbeat.stopped.CompareAndSwap(false, true) {
		// Already stopped by another goroutine — don't close the channel twice.
		// Bound the wait so a concurrent stopper does not hang if the heartbeat
		// goroutine is stuck on a non-cancellable operation.
		select {
		case <-heartbeat.done:
		case <-time.After(5 * time.Second):
			log.Printf("[tokenhub] WARNING: lease heartbeat did not stop within 5s (concurrent stop), abandoning wait")
		}
		cause := context.Cause(heartbeat.ctx)
		if errors.Is(cause, ErrCoordinationLeaseLost) {
			return ErrCoordinationLeaseLost
		}
		return nil
	}
	close(heartbeat.stop)
	heartbeat.cancel(context.Canceled)
	// Wait for the heartbeat goroutine to exit, with a timeout to prevent
	// a permanent hang when the goroutine is stuck on a non-cancellable
	// database operation (e.g., TCP-level stall that ignores context).
	select {
	case <-heartbeat.done:
	case <-time.After(5 * time.Second):
		log.Printf("[tokenhub] WARNING: lease heartbeat did not stop within 5s, abandoning wait")
	}
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
		if fnErr != nil {
			return fmt.Errorf("lease lost during operation: %w (original error: %w)", leaseErr, fnErr)
		}
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

func (s *GormStore) CreateProject(project Project) Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, _ = s.createProject(project, false)
	return project
}

func (s *GormStore) CreateProjectChecked(project Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createProject(project, true)
}

func (s *GormStore) createProject(project Project, requireActiveTeam bool) (Project, error) {
	now := time.Now().UTC()
	if project.ID == "" {
		project.ID = NewID("prj")
	}
	if project.Status == "" {
		project.Status = StatusActive
	}
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	project.UpdatedAt = now
	project.Teams = nil
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if strings.TrimSpace(project.TeamID) != "" {
			team, err := lockTeamForMutation(tx, project.TeamID)
			if err != nil {
				return err
			}
			if requireActiveTeam && team.Status != "" && team.Status != StatusActive {
				return NewHTTPError(http.StatusBadRequest, "team_inactive", "Only an active team can be assigned to a project")
			}
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&project).Error; err != nil {
			return err
		}
		if strings.TrimSpace(project.TeamID) == "" {
			return nil
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ProjectTeam{
			ProjectID: project.ID,
			TeamID:    strings.TrimSpace(project.TeamID),
			Role:      "team_leader",
			CreatedAt: now,
			UpdatedAt: now,
		}).Error
	})
	if err != nil {
		return Project{}, err
	}
	_ = s.loadProjectTeams(&project)
	return project, nil
}

func (s *GormStore) ListProjects() []Project {
	var items []Project
	if err := s.db.Order("created_at asc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListProjects failed: %v", err)
	}
	_ = s.loadProjectTeamsFor(items)
	return items
}

func (s *GormStore) UpdateProject(id string, patch Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var project Project
	nextTeamID := strings.TrimSpace(patch.TeamID)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var nextTeam AdminResource
		if nextTeamID != "" {
			var err error
			nextTeam, err = lockTeamForMutation(tx, nextTeamID)
			if err != nil {
				return err
			}
		}
		if err := tx.First(&project, "id = ?", id).Error; err != nil {
			return notFound(err, "project_not_found", "Project not found")
		}
		if nextTeamID != strings.TrimSpace(project.TeamID) && nextTeam.Status != "" && nextTeam.Status != StatusActive {
			return NewHTTPError(http.StatusBadRequest, "team_inactive", "Only an active team can be assigned to a project")
		}
		if patch.Name != "" {
			project.Name = patch.Name
		}
		project.TeamID = nextTeamID
		project.OwnerUserID = patch.OwnerUserID
		project.CostCenter = patch.CostCenter
		if patch.Status != "" {
			project.Status = patch.Status
		}
		project.DefaultQuotaRef = patch.DefaultQuotaRef
		project.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&project).Error; err != nil {
			return err
		}
		if project.TeamID == "" {
			return nil
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ProjectTeam{
			ProjectID: project.ID,
			TeamID:    project.TeamID,
			Role:      "team_leader",
			CreatedAt: project.UpdatedAt,
			UpdatedAt: project.UpdatedAt,
		}).Error
	})
	if err != nil {
		return Project{}, err
	}
	_ = s.loadProjectTeams(&project)
	return project, nil
}

func (s *GormStore) DeleteProject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.First(&project, "id = ?", id).Error; err != nil {
			return notFound(err, "project_not_found", "Project not found")
		}
		var keys []APIKey
		if err := tx.Where("project_id = ?", id).Find(&keys).Error; err != nil {
			return err
		}
		keyIDs := make([]string, 0, len(keys))
		for _, key := range keys {
			keyIDs = append(keyIDs, key.ID)
		}
		if len(keyIDs) > 0 {
			if err := tx.Where("scope_type = ? AND scope_id IN ?", "api_key", keyIDs).Delete(&InFlightLease{}).Error; err != nil {
				return err
			}
			if err := tx.Where("key_id IN ?", keyIDs).Delete(&QuotaBucket{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", keyIDs).Delete(&APIKey{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("project_id = ?", id).Delete(&ProjectTeam{}).Error; err != nil {
			return err
		}
		return tx.Delete(&project).Error
	})
}

func (s *GormStore) GetProject(id string) (Project, bool) {
	var project Project
	if err := s.db.First(&project, "id = ?", id).Error; err != nil {
		return Project{}, false
	}
	_ = s.loadProjectTeams(&project)
	return project, true
}

func (s *GormStore) loadProjectTeams(project *Project) error {
	if project == nil || strings.TrimSpace(project.ID) == "" {
		return nil
	}
	var links []ProjectTeam
	if err := s.db.Where("project_id = ?", project.ID).Order("created_at asc, team_id asc").Find(&links).Error; err != nil {
		return err
	}
	for index := range links {
		links[index].IsPrimary = links[index].TeamID == project.TeamID
	}
	project.Teams = links
	return nil
}

func (s *GormStore) loadProjectTeamsFor(projects []Project) error {
	if len(projects) == 0 {
		return nil
	}
	projectIDs := make([]string, 0, len(projects))
	projectIndex := make(map[string]int, len(projects))
	for index := range projects {
		projects[index].Teams = nil
		projectIDs = append(projectIDs, projects[index].ID)
		projectIndex[projects[index].ID] = index
	}
	var links []ProjectTeam
	if err := s.db.Where("project_id IN ?", projectIDs).Order("created_at asc, team_id asc").Find(&links).Error; err != nil {
		return err
	}
	for _, link := range links {
		index, ok := projectIndex[link.ProjectID]
		if !ok {
			continue
		}
		link.IsPrimary = link.TeamID == projects[index].TeamID
		projects[index].Teams = append(projects[index].Teams, link)
	}
	return nil
}

func (s *GormStore) ListProjectTeams(projectID string, offset int, limit int) ([]ProjectTeam, int64, error) {
	var project Project
	if err := s.db.First(&project, "id = ?", projectID).Error; err != nil {
		return nil, 0, notFound(err, "project_not_found", "Project not found")
	}
	query := s.db.Model(&ProjectTeam{}).Where("project_id = ?", projectID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var links []ProjectTeam
	if err := query.Order("created_at asc, team_id asc").Offset(offset).Limit(limit).Find(&links).Error; err != nil {
		return nil, 0, err
	}
	for index := range links {
		links[index].IsPrimary = links[index].TeamID == project.TeamID
	}
	return links, total, nil
}

func (s *GormStore) AddProjectTeam(link ProjectTeam) (ProjectTeam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	link.ProjectID = strings.TrimSpace(link.ProjectID)
	link.TeamID = strings.TrimSpace(link.TeamID)
	now := time.Now().UTC()
	if link.CreatedAt.IsZero() {
		link.CreatedAt = now
	}
	link.UpdatedAt = now
	var project Project
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := lockActiveTeamForMutation(tx, link.TeamID); err != nil {
			return err
		}
		if err := tx.First(&project, "id = ?", link.ProjectID).Error; err != nil {
			return notFound(err, "project_not_found", "Project not found")
		}
		if err := tx.Create(&link).Error; err != nil {
			return writeConflict(err, "project_team_conflict", "Team is already linked to this project")
		}
		return nil
	})
	if err != nil {
		return ProjectTeam{}, err
	}
	link.IsPrimary = link.TeamID == project.TeamID
	return link, nil
}

func (s *GormStore) UpdateProjectTeam(projectID string, teamID string, role string) (ProjectTeam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var link ProjectTeam
	if err := s.db.First(&link, "project_id = ? AND team_id = ?", projectID, teamID).Error; err != nil {
		return ProjectTeam{}, notFound(err, "project_team_not_found", "Project team link not found")
	}
	link.Role = role
	link.UpdatedAt = time.Now().UTC()
	if err := s.db.Save(&link).Error; err != nil {
		return ProjectTeam{}, err
	}
	var project Project
	_ = s.db.First(&project, "id = ?", projectID).Error
	link.IsPrimary = link.TeamID == project.TeamID
	return link, nil
}

func (s *GormStore) RemoveProjectTeam(projectID string, teamID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		project, err := lockProjectForTeamMutation(tx, projectID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(project.TeamID) == strings.TrimSpace(teamID) {
			return NewHTTPError(http.StatusConflict, "project_primary_team", "The primary team cannot be removed; assign another primary team first")
		}
		var count int64
		if err := tx.Model(&ProjectTeam{}).Where("project_id = ?", projectID).Count(&count).Error; err != nil {
			return err
		}
		if count <= 1 {
			return NewHTTPError(http.StatusConflict, "project_last_team", "The last project team cannot be removed")
		}
		result := tx.Where("project_id = ? AND team_id = ?", projectID, teamID).Delete(&ProjectTeam{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return NewHTTPError(http.StatusNotFound, "project_team_not_found", "Project team link not found")
		}
		return nil
	})
}

func lockProjectForTeamMutation(tx *gorm.DB, projectID string) (Project, error) {
	result := tx.Model(&Project{}).Where("id = ?", projectID).UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return Project{}, result.Error
	}
	if result.RowsAffected == 0 {
		return Project{}, NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found")
	}
	var project Project
	if err := tx.First(&project, "id = ?", projectID).Error; err != nil {
		return Project{}, notFound(err, "project_not_found", "Project not found")
	}
	return project, nil
}

func lockAdminResourceForMutation(tx *gorm.DB, kind string, id string) (AdminResource, error) {
	result := tx.Model(&AdminResource{}).Where("kind = ? AND id = ?", kind, id).UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return AdminResource{}, result.Error
	}
	if result.RowsAffected == 0 {
		return AdminResource{}, NewHTTPError(http.StatusNotFound, "resource_not_found", "Resource not found")
	}
	var resource AdminResource
	if err := tx.First(&resource, "kind = ? AND id = ?", kind, id).Error; err != nil {
		return AdminResource{}, notFound(err, "resource_not_found", "Resource not found")
	}
	return resource, nil
}

func lockActiveTeamForMutation(tx *gorm.DB, teamID string) error {
	team, err := lockTeamForMutation(tx, teamID)
	if err != nil {
		return err
	}
	if team.Status != "" && team.Status != StatusActive {
		return NewHTTPError(http.StatusBadRequest, "team_inactive", "Only an active team can be assigned to a project")
	}
	return nil
}

func lockTeamForMutation(tx *gorm.DB, teamID string) (AdminResource, error) {
	team, err := lockAdminResourceForMutation(tx, "teams", strings.TrimSpace(teamID))
	if err != nil {
		if AsHTTPError(err).Status == http.StatusNotFound {
			return AdminResource{}, NewHTTPError(http.StatusNotFound, "team_not_found", "Team not found")
		}
		return AdminResource{}, err
	}
	return team, nil
}

func lockUserTeamsForMutation(tx *gorm.DB, primaryTeamID string, teamIDs []string) error {
	ids := normalizedTeamIDs(primaryTeamID, teamIDs)
	sort.Strings(ids)
	for _, teamID := range ids {
		team, err := lockTeamForMutation(tx, teamID)
		if err != nil {
			return err
		}
		if team.Status != "" && team.Status != StatusActive {
			return NewHTTPError(http.StatusBadRequest, "team_inactive", "Only an active team can be assigned to a user")
		}
	}
	return nil
}

func (s *GormStore) CreateAPIKey(projectID string, key APIKey, rawSecret string) (APIKey, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.db.First(&Project{}, "id = ?", projectID).Error; err != nil {
		return APIKey{}, "", notFound(err, "project_not_found", "Project not found")
	}
	if rawSecret == "" {
		rawSecret = s.generateAPIKeySecret()
	}
	prefix, suffix := PrefixSuffix(rawSecret)
	now := time.Now().UTC()
	if key.ID == "" {
		key.ID = NewID("key")
	}
	if key.Status == "" {
		key.Status = StatusActive
	}
	if key.Group == "" {
		key.Group = "default"
	}
	key.ProjectID = projectID
	key.KeyHash = HashSecret(rawSecret)
	key.KeyPrefix = prefix
	key.KeySuffix = suffix
	if key.CreatedAt.IsZero() {
		key.CreatedAt = now
	}
	if key.Allowed == nil {
		key.Allowed = []string{}
	}
	key.AllowedModels = AllowedModelSet(key.Allowed)
	if err := s.db.Create(&key).Error; err != nil {
		return APIKey{}, "", writeConflict(err, "api_key_conflict", "API key already exists")
	}
	return publicKey(key), rawSecret, nil
}

func (s *GormStore) generateAPIKeySecret() string {
	prefix, randomLength := s.apiKeyGenerationConfig()
	return GenerateAPIKeyWithOptions(prefix, randomLength)
}

func (s *GormStore) apiKeyGenerationConfig() (string, int) {
	var settings []AdminResource
	if err := s.db.Where("kind = ? AND status = ?", "settings", StatusActive).Order("created_at asc").Find(&settings).Error; err != nil {
		log.Printf("[tokenhub] ERROR: apiKeyGenerationConfig failed to load settings: %v", err)
	}
	var fields map[string]any
	for _, item := range settings {
		if item.ID == "cfg_gateway" {
			fields = item.Fields
			break
		}
	}
	if fields == nil && len(settings) > 0 {
		fields = settings[0].Fields
	}
	prefix := stringField(fields, "api_key_prefix")
	randomLength := DefaultAPIKeyRandomLength
	if value := strings.TrimSpace(stringField(fields, "api_key_random_length")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			randomLength = parsed
		}
	}
	return NormalizeAPIKeyPrefix(prefix), NormalizeAPIKeyRandomLength(randomLength)
}

func (s *GormStore) ListProjectKeys(projectID string) []APIKey {
	var items []APIKey
	if err := s.db.Where("project_id = ?", projectID).Order("created_at asc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListProjectKeys failed: %v", err)
	}
	return publicKeys(items)
}

func (s *GormStore) ListAPIKeys() []APIKey {
	var items []APIKey
	if err := s.db.Order("created_at asc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListAPIKeys failed: %v", err)
	}
	return publicKeys(items)
}

func (s *GormStore) UpdateAPIKey(id string, patch APIKey) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var key APIKey
	if err := s.db.First(&key, "id = ?", id).Error; err != nil {
		return APIKey{}, notFound(err, "api_key_not_found", "API key not found")
	}
	hydrateAPIKey(&key)
	if patch.Name != "" {
		key.Name = patch.Name
	}
	if patch.Group != "" {
		key.Group = patch.Group
	}
	if patch.OwnerUserID != "" {
		key.OwnerUserID = patch.OwnerUserID
	}
	if patch.Status != "" {
		key.Status = patch.Status
	}
	if patch.Allowed != nil {
		key.Allowed = patch.Allowed
		key.AllowedModels = AllowedModelSet(patch.Allowed)
	}
	if patch.IPAllowlist != nil {
		key.IPAllowlist = patch.IPAllowlist
	}
	if patch.Limits != (QuotaLimits{}) {
		key.Limits = patch.Limits
	}
	if patch.ExpiresAt != nil {
		key.ExpiresAt = patch.ExpiresAt
	}
	if err := s.db.Save(&key).Error; err != nil {
		return APIKey{}, err
	}
	return publicKey(key), nil
}

func (s *GormStore) RotateAPIKey(id string, graceUntil *time.Time) (APIKey, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var oldKey APIKey
	if err := s.db.First(&oldKey, "id = ?", id).Error; err != nil {
		return APIKey{}, "", notFound(err, "api_key_not_found", "API key not found")
	}
	hydrateAPIKey(&oldKey)
	now := time.Now().UTC()
	newSecret := s.generateAPIKeySecret()
	prefix, suffix := PrefixSuffix(newSecret)
	newKey := oldKey
	newKey.ID = NewID("key")
	newKey.KeyHash = HashSecret(newSecret)
	newKey.KeyPrefix = prefix
	newKey.KeySuffix = suffix
	newKey.RotatedFromID = oldKey.ID
	newKey.GraceUntil = nil
	newKey.CreatedAt = now
	newKey.LastUsedAt = nil
	newKey.Status = StatusActive
	if newKey.Metadata == nil {
		newKey.Metadata = map[string]string{}
	}
	newKey.Metadata["rotated_from"] = oldKey.ID

	if graceUntil != nil {
		oldKey.GraceUntil = graceUntil
		oldKey.Status = StatusActive
	} else {
		oldKey.Status = StatusRevoked
		oldKey.GraceUntil = &now
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&oldKey).Error; err != nil {
			return err
		}
		return tx.Create(&newKey).Error
	})
	if err != nil {
		return APIKey{}, "", err
	}
	return publicKey(newKey), newSecret, nil
}

func (s *GormStore) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var key APIKey
		if err := tx.First(&key, "id = ?", id).Error; err != nil {
			return notFound(err, "api_key_not_found", "API key not found")
		}
		if err := tx.Where("key_id = ?", id).Delete(&QuotaBucket{}).Error; err != nil {
			return err
		}
		if err := tx.Where("scope_type = ? AND scope_id = ?", "api_key", id).Delete(&InFlightLease{}).Error; err != nil {
			return err
		}
		return tx.Delete(&key).Error
	})
}

func (s *GormStore) ValidateAPIKey(rawSecret string, clientIP string) (Project, APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var key APIKey
	if err := s.db.First(&key, "key_hash = ?", HashSecret(rawSecret)).Error; err != nil {
		return Project{}, APIKey{}, ErrInvalidAPIKey
	}
	hydrateAPIKey(&key)
	if key.Status == StatusDisabled || key.Status == StatusRevoked {
		if !(key.Status == StatusRevoked && key.GraceUntil != nil && time.Now().UTC().Before(*key.GraceUntil)) {
			return Project{}, APIKey{}, ErrAPIKeyDisabled
		}
	}
	if len(key.IPAllowlist) > 0 && !ipAllowed(clientIP, key.IPAllowlist) {
		return Project{}, APIKey{}, ErrAPIKeyDisabled
	}
	if key.ExpiresAt != nil && time.Now().UTC().After(*key.ExpiresAt) {
		return Project{}, APIKey{}, ErrAPIKeyExpired
	}
	var project Project
	if err := s.db.First(&project, "id = ?", key.ProjectID).Error; err != nil || project.Status != StatusActive {
		return Project{}, APIKey{}, ErrAPIKeyDisabled
	}
	now := time.Now().UTC()
	key.LastUsedAt = &now
	if err := s.db.Model(&key).Update("last_used_at", now).Error; err != nil {
		return Project{}, APIKey{}, err
	}
	return project, publicKey(key), nil
}

func (s *GormStore) AddProvider(provider Provider) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if provider.ID == "" {
		provider.ID = NewID("prv")
	}
	if provider.Status == "" {
		provider.Status = StatusActive
	}
	if !provider.Healthy {
		provider.Healthy = true
	}
	if provider.CreatedAt.IsZero() {
		provider.CreatedAt = time.Now().UTC()
	}
	if provider.Type == ProviderOpenAICodex {
		provider.APIKey = ""
		if codexProviderBaseURLNeedsNormalization(provider.BaseURL) {
			provider.BaseURL = openAICodexBaseURL
		}
	}
	encrypted, err := s.encryptSecret(provider.APIKey)
	if err != nil {
		return Provider{}, fmt.Errorf("encrypt provider API key: %w", err)
	}
	provider.APIKey = encrypted
	if err := s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&provider).Error; err != nil {
		return Provider{}, err
	}
	provider.APIKey = ""
	return provider, nil
}

func (s *GormStore) ListProviders() []Provider {
	var items []Provider
	if err := s.db.Order("priority asc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListProviders failed: %v", err)
	}
	for i := range items {
		items[i].APIKey = ""
	}
	return items
}

func (s *GormStore) LoadProviderCatalogSnapshot(includeModels bool) ([]ProviderCatalogEntry, string, time.Time, bool, error) {
	var snapshot ProviderCatalogSnapshot
	query := s.db
	if !includeModels {
		query = query.Select("id", "source", "summary_json", "fetched_at")
	}
	if err := query.First(&snapshot, "id = ?", providerCatalogSnapshotID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", time.Time{}, false, nil
		}
		return nil, "", time.Time{}, false, err
	}
	payload := snapshot.SummaryJSON
	if includeModels || strings.TrimSpace(payload) == "" {
		payload = snapshot.CatalogJSON
	}
	var entries []ProviderCatalogEntry
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		return nil, "", time.Time{}, false, fmt.Errorf("decode provider catalog snapshot: %w", err)
	}
	if !includeModels && strings.TrimSpace(snapshot.SummaryJSON) == "" {
		entries = cloneCatalogEntries(entries, false)
	}
	return entries, snapshot.Source, snapshot.FetchedAt, true, nil
}

func (s *GormStore) SaveProviderCatalogSnapshot(entries []ProviderCatalogEntry, source string, fetchedAt time.Time) error {
	if len(entries) == 0 {
		return fmt.Errorf("provider catalog snapshot cannot be empty")
	}
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}
	catalogJSON, err := json.Marshal(cloneCatalogEntries(entries, true))
	if err != nil {
		return fmt.Errorf("encode provider catalog snapshot: %w", err)
	}
	summaryJSON, err := json.Marshal(cloneCatalogEntries(entries, false))
	if err != nil {
		return fmt.Errorf("encode provider catalog summaries: %w", err)
	}
	snapshot := ProviderCatalogSnapshot{
		ID:          providerCatalogSnapshotID,
		Source:      firstNonEmpty(strings.TrimSpace(source), "builtin"),
		SummaryJSON: string(summaryJSON),
		CatalogJSON: string(catalogJSON),
		FetchedAt:   fetchedAt.UTC(),
	}
	return s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&snapshot).Error
}

func (s *GormStore) GetProvider(id string) (Provider, bool) {
	var provider Provider
	if err := s.db.First(&provider, "id = ?", id).Error; err != nil {
		return Provider{}, false
	}
	decrypted, err := s.decryptSecret(provider.APIKey)
	if err != nil {
		log.Printf("[tokenhub] ERROR: GetProvider failed to decrypt API key for provider %s: %v", id, err)
		provider.APIKey = ""
	} else {
		provider.APIKey = decrypted
	}
	return provider, true
}

func (s *GormStore) UpdateProvider(id string, patch Provider) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var provider Provider
	if err := s.db.First(&provider, "id = ?", id).Error; err != nil {
		return Provider{}, notFound(err, "provider_not_found", "Provider not found")
	}
	if patch.Name != "" {
		provider.Name = patch.Name
	}
	if patch.Type != "" {
		if err := validateProviderAdapterResources(s.db, id, patch.Type); err != nil {
			return Provider{}, err
		}
		provider.Type = patch.Type
	}
	provider.BaseURL = patch.BaseURL
	if patch.APIKey != "" {
		if firstNonEmpty(patch.Type, provider.Type) == ProviderOpenAICodex {
			return Provider{}, NewHTTPError(409, "provider_adapter_credential_conflict", "Codex Subscription credentials must be stored on account resources")
		}
		encrypted, err := s.encryptSecret(patch.APIKey)
		if err != nil {
			return Provider{}, fmt.Errorf("encrypt API key: %w", err)
		}
		provider.APIKey = encrypted
	}
	if patch.Status != "" {
		provider.Status = patch.Status
	}
	provider.Healthy = patch.Healthy
	if patch.Priority != 0 {
		provider.Priority = patch.Priority
	}
	if patch.Headers != nil {
		provider.Headers = patch.Headers
	}
	if patch.Options != nil {
		provider.Options = patch.Options
	}
	if err := s.db.Save(&provider).Error; err != nil {
		return Provider{}, err
	}
	provider.APIKey = ""
	return provider, nil
}

func (s *GormStore) DeleteProvider(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var provider Provider
		if err := tx.First(&provider, "id = ?", id).Error; err != nil {
			return notFound(err, "provider_not_found", "Provider not found")
		}
		if err := tx.Where("provider_id = ?", id).Delete(&ModelRoute{}).Error; err != nil {
			return err
		}
		if err := tx.Where("provider_id = ?", id).Delete(&ProviderModel{}).Error; err != nil {
			return err
		}
		var resourceIDs []string
		if err := tx.Model(&ProviderResource{}).Where("provider_id = ?", id).Pluck("id", &resourceIDs).Error; err != nil {
			return err
		}
		if len(resourceIDs) > 0 {
			if err := tx.Where("scope_type = ? AND scope_id IN ?", "provider_resource", resourceIDs).Delete(&InFlightLease{}).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_id IN ?", resourceIDs).Delete(&ProviderResourceBucket{}).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_id IN ?", resourceIDs).Delete(&ProviderResourceObservation{}).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_id IN ?", resourceIDs).Delete(&ProviderObservation{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("provider_id = ?", id).Delete(&AdapterSessionBinding{}).Error; err != nil {
			return err
		}
		if err := tx.Where("provider_id = ?", id).Delete(&ProviderObservation{}).Error; err != nil {
			return err
		}
		if err := tx.Where("provider_id = ?", id).Delete(&ProviderResource{}).Error; err != nil {
			return err
		}
		return tx.Delete(&provider).Error
	})
}

func (s *GormStore) SetProviderHealth(providerID string, healthy bool) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var provider Provider
	if err := s.db.First(&provider, "id = ?", providerID).Error; err != nil {
		return Provider{}, notFound(err, "provider_not_found", "Provider not found")
	}
	if err := s.db.Model(&Provider{}).Where("id = ?", providerID).Update("healthy", healthy).Error; err != nil {
		return Provider{}, err
	}
	provider.Healthy = healthy
	provider.APIKey = ""
	return provider, nil
}

func (s *GormStore) AddProviderResource(resource ProviderResource) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var provider Provider
	if err := s.db.First(&provider, "id = ?", resource.ProviderID).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_not_found", "Provider not found")
	}
	if err := ensureProviderResourceAdapterCompatibility(s.db, &provider, resource.ResourceType); err != nil {
		return ProviderResource{}, err
	}
	resource.Name = strings.TrimSpace(resource.Name)
	now := time.Now().UTC()
	if resource.ID == "" {
		resource.ID = NewID("rsrc")
	}
	if err := s.ensureProviderResourceNameUnique(resource.ID, resource.Name); err != nil {
		return ProviderResource{}, err
	}
	if resource.Status == "" {
		resource.Status = StatusActive
	}
	if resource.ResourceType == "" {
		resource.ResourceType = "api_key"
	}
	if !resource.Healthy {
		resource.Healthy = true
	}
	if resource.Weight <= 0 {
		resource.Weight = 100
	}
	if resource.CreatedAt.IsZero() {
		resource.CreatedAt = now
	}
	resource.UpdatedAt = now
	if err := s.prepareProviderResourceForCreate(&resource); err != nil {
		return ProviderResource{}, fmt.Errorf("prepare resource for create: %w", err)
	}
	encrypted, encErr := s.encryptSecret(resource.APIKey)
	if encErr != nil {
		return ProviderResource{}, fmt.Errorf("encrypt API key: %w", encErr)
	}
	resource.APIKey = encrypted
	if err := s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&resource).Error; err != nil {
		return ProviderResource{}, err
	}
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) ListProviderResources() []ProviderResource {
	var items []ProviderResource
	if err := s.db.Order("provider_id asc, priority asc, weight desc, created_at asc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListProviderResources failed: %v", err)
	}
	var observations []ProviderResourceObservation
	if err := s.db.Find(&observations).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListProviderResources observations failed: %v", err)
	}
	observationByResource := make(map[string]ProviderResourceObservation, len(observations))
	for _, observation := range observations {
		observationByResource[observation.ResourceID] = observation
	}
	for i := range items {
		if observation, ok := observationByResource[items[i].ID]; ok {
			copy := observation
			items[i].Observation = &copy
		}
		redactProviderResourceSecrets(&items[i])
	}
	return items
}

func (s *GormStore) UpdateProviderResource(id string, patch ProviderResource) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource ProviderResource
	if err := s.db.First(&resource, "id = ?", id).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_resource_not_found", "Provider resource not found")
	}
	if patch.ProviderID != "" && patch.ProviderID != resource.ProviderID {
		if err := s.db.First(&Provider{}, "id = ?", patch.ProviderID).Error; err != nil {
			return ProviderResource{}, notFound(err, "provider_not_found", "Provider not found")
		}
		resource.ProviderID = patch.ProviderID
	}
	if patch.Name != "" {
		nextName := strings.TrimSpace(patch.Name)
		if err := s.ensureProviderResourceNameUnique(resource.ID, nextName); err != nil {
			return ProviderResource{}, err
		}
		resource.Name = nextName
	}
	if patch.Group != "" {
		resource.Group = patch.Group
	}
	if patch.ResourceType != "" {
		resource.ResourceType = patch.ResourceType
	}
	resource.BaseURL = patch.BaseURL
	shouldEncryptAPIKey := false
	if patch.APIKey != "" {
		resource.APIKey = patch.APIKey
		shouldEncryptAPIKey = true
	}
	resource.Region = patch.Region
	resource.Environment = patch.Environment
	if patch.Status != "" {
		resource.Status = patch.Status
	}
	resource.Healthy = patch.Healthy
	if patch.Priority != 0 {
		resource.Priority = patch.Priority
	}
	if patch.Weight != 0 {
		resource.Weight = patch.Weight
	}
	resource.RateLimitRPM = patch.RateLimitRPM
	resource.TokenLimitTPM = patch.TokenLimitTPM
	resource.MaxConcurrency = patch.MaxConcurrency
	if patch.Headers != nil {
		resource.Headers = patch.Headers
	}
	if patch.Options != nil {
		resource.Options = patch.Options
	}
	var provider Provider
	if err := s.db.First(&provider, "id = ?", resource.ProviderID).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_not_found", "Provider not found")
	}
	if err := ensureProviderResourceAdapterCompatibility(s.db, &provider, resource.ResourceType); err != nil {
		return ProviderResource{}, err
	}
	resource.UpdatedAt = time.Now().UTC()
	if err := s.prepareProviderResourceForUpdate(&resource, patch); err != nil {
		return ProviderResource{}, fmt.Errorf("prepare resource for update: %w", err)
	}
	if patch.Credentials != nil && strings.TrimSpace(patch.Credentials.AccessToken) != "" {
		shouldEncryptAPIKey = true
	}
	if shouldEncryptAPIKey {
		encrypted, encErr := s.encryptSecret(resource.APIKey)
		if encErr != nil {
			return ProviderResource{}, fmt.Errorf("encrypt API key: %w", encErr)
		}
		resource.APIKey = encrypted
	}
	if err := s.db.Save(&resource).Error; err != nil {
		return ProviderResource{}, err
	}
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) ensureProviderResourceNameUnique(resourceID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_provider_resource", "Provider resource name is required")
	}
	var count int64
	err := s.db.Model(&ProviderResource{}).
		Where("LOWER(TRIM(name)) = ?", strings.ToLower(name)).
		Where("id <> ?", resourceID).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return NewHTTPError(http.StatusConflict, "provider_resource_name_conflict", "Provider resource name already exists")
	}
	return nil
}

func (s *GormStore) UpdateProviderResourceOptions(id string, options map[string]string) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource ProviderResource
	if err := s.db.First(&resource, "id = ?", id).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_resource_not_found", "Provider resource not found")
	}
	if resource.Options == nil {
		resource.Options = map[string]string{}
	}
	for key, value := range options {
		resource.Options[key] = value
	}
	resource.UpdatedAt = time.Now().UTC()
	if err := s.db.Save(&resource).Error; err != nil {
		return ProviderResource{}, err
	}
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) DeleteProviderResource(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var resource ProviderResource
		if err := tx.First(&resource, "id = ?", id).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		if err := tx.Model(&ModelRoute{}).
			Where("provider_resource_id = ?", id).
			Update("provider_resource_id", "").Error; err != nil {
			return err
		}
		if err := tx.Where("scope_type = ? AND scope_id = ?", "provider_resource", id).Delete(&InFlightLease{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_id = ?", id).Delete(&ProviderResourceBucket{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_id = ?", id).Delete(&ProviderResourceObservation{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_id = ?", id).Delete(&ProviderObservation{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_id = ?", id).Delete(&AdapterSessionBinding{}).Error; err != nil {
			return err
		}
		return tx.Delete(&resource).Error
	})
}

func (s *GormStore) SetProviderResourceHealth(resourceID string, healthy bool) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource ProviderResource
	if err := s.db.First(&resource, "id = ?", resourceID).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_resource_not_found", "Provider resource not found")
	}
	now := time.Now().UTC()
	if err := s.db.Model(&ProviderResource{}).
		Where("id = ?", resourceID).
		Updates(map[string]any{"healthy": healthy, "last_checked_at": now, "updated_at": now}).Error; err != nil {
		return ProviderResource{}, err
	}
	resource.Healthy = healthy
	resource.LastCheckedAt = &now
	resource.UpdatedAt = now
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) BulkOperateProviderResources(action string, ids []string) (ProviderResourceBulkResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	action = strings.TrimSpace(action)
	if !validProviderResourceBulkAction(action) {
		return ProviderResourceBulkResult{}, NewHTTPError(400, "invalid_provider_resource_action", "Invalid provider resource bulk action")
	}
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return ProviderResourceBulkResult{}, NewHTTPError(400, "missing_provider_resource_ids", "Provider resource ids are required")
	}
	now := time.Now().UTC()
	result := ProviderResourceBulkResult{Action: action, Resources: make([]ProviderResource, 0, len(ids))}
	for _, id := range ids {
		var resource ProviderResource
		if err := s.db.First(&resource, "id = ?", id).Error; err != nil {
			result.Failed++
			result.Errors = append(result.Errors, id+": "+notFound(err, "provider_resource_not_found", "Provider resource not found").Error())
			continue
		}
		updates := map[string]any{"updated_at": now}
		switch action {
		case "enable":
			updates["status"] = StatusActive
			updates["healthy"] = true
			updates["failure_count"] = 0
			updates["cooldown_until"] = nil
			updates["last_checked_at"] = now
			resource.Status = StatusActive
			resource.Healthy = true
			resource.FailureCount = 0
			resource.CooldownUntil = nil
			resource.LastCheckedAt = &now
		case "disable":
			updates["status"] = StatusDisabled
			updates["healthy"] = false
			updates["cooldown_until"] = nil
			updates["last_checked_at"] = now
			resource.Status = StatusDisabled
			resource.Healthy = false
			resource.CooldownUntil = nil
			resource.LastCheckedAt = &now
		case "test":
			healthy := resource.Status == StatusActive
			updates["healthy"] = healthy
			updates["last_checked_at"] = now
			resource.Healthy = healthy
			resource.LastCheckedAt = &now
			if healthy {
				updates["failure_count"] = 0
				updates["cooldown_until"] = nil
				resource.FailureCount = 0
				resource.CooldownUntil = nil
			}
		case "clear_error":
			updates["healthy"] = true
			updates["failure_count"] = 0
			updates["cooldown_until"] = nil
			updates["last_checked_at"] = now
			resource.Healthy = true
			resource.FailureCount = 0
			resource.CooldownUntil = nil
			resource.LastCheckedAt = &now
			// Clean up expired leases only; active leases must remain until their
			// owners finish, otherwise MaxConcurrency can be exceeded by replacements.
			if err := s.db.Where("scope_type = ? AND scope_id = ? AND expires_at <= ?", "provider_resource", resource.ID, now).Delete(&InFlightLease{}).Error; err != nil {
				log.Printf("[tokenhub] WARNING: clear_error failed to clean expired leases for resource %s: %v", resource.ID, err)
			}
		case "reset_usage":
			if err := s.db.Where("resource_id = ?", resource.ID).Delete(&ProviderResourceBucket{}).Error; err != nil {
				result.Failed++
				result.Errors = append(result.Errors, id+": "+err.Error())
				continue
			}
			// Clean up expired leases only; active leases must remain until their owners finish.
			if err := s.db.Where("scope_type = ? AND scope_id = ? AND expires_at <= ?", "provider_resource", resource.ID, now).Delete(&InFlightLease{}).Error; err != nil {
				log.Printf("[tokenhub] WARNING: reset_usage failed to clean expired leases for resource %s: %v", resource.ID, err)
			}
		}
		if err := s.db.Model(&ProviderResource{}).Where("id = ?", resource.ID).Updates(updates).Error; err != nil {
			result.Failed++
			result.Errors = append(result.Errors, id+": "+err.Error())
			continue
		}
		resource.UpdatedAt = now
		redactProviderResourceSecrets(&resource)
		result.Success++
		result.Resources = append(result.Resources, resource)
	}
	return result, nil
}

func (s *GormStore) ImportProviderResources(resources []ProviderResource) (ProviderResourceImportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(resources) == 0 {
		return ProviderResourceImportResult{}, NewHTTPError(400, "missing_provider_resources", "Provider resources are required")
	}
	if len(resources) > 200 {
		return ProviderResourceImportResult{}, NewHTTPError(400, "too_many_provider_resources", "Provider resource import is limited to 200 rows")
	}
	result := ProviderResourceImportResult{Resources: make([]ProviderResource, 0, len(resources))}
	for index, resource := range resources {
		row := strconv.Itoa(index + 1)
		resource.ProviderID = strings.TrimSpace(resource.ProviderID)
		resource.Name = strings.TrimSpace(resource.Name)
		if resource.ProviderID == "" || resource.Name == "" {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": provider_id and name are required")
			continue
		}
		if err := s.db.First(&Provider{}, "id = ?", resource.ProviderID).Error; err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+notFound(err, "provider_not_found", "Provider not found").Error())
			continue
		}
		now := time.Now().UTC()
		if resource.ID == "" {
			resource.ID = NewID("rsrc")
		}
		if err := s.ensureProviderResourceNameUnique(resource.ID, resource.Name); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+err.Error())
			continue
		}
		if resource.Status == "" {
			resource.Status = StatusActive
		}
		if resource.ResourceType == "" {
			resource.ResourceType = "api_key"
		}
		if !resource.Healthy {
			resource.Healthy = true
		}
		if resource.Weight <= 0 {
			resource.Weight = 100
		}
		if resource.CreatedAt.IsZero() {
			resource.CreatedAt = now
		}
		resource.UpdatedAt = now
		if err := s.prepareProviderResourceForCreate(&resource); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": prep error: "+err.Error())
			continue
		}
		encrypted, encErr := s.encryptSecret(resource.APIKey)
		if encErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": encrypt error: "+encErr.Error())
			continue
		}
		resource.APIKey = encrypted
		if err := s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&resource).Error; err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+err.Error())
			continue
		}
		redactProviderResourceSecrets(&resource)
		result.Success++
		result.Resources = append(result.Resources, resource)
	}
	return result, nil
}

func (s *GormStore) lockScopeForUpdate(tx *gorm.DB, scopeType string, scopeID string) error {
	if s.dbDriver != "postgres" {
		return nil
	}
	key := "tokenhub:" + scopeType + ":" + scopeID
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error
}

func (s *GormStore) acquireInFlightLease(tx *gorm.DB, scopeType string, scopeID string, limit int64, leaseID string) (time.Duration, error) {
	if limit <= 0 {
		return 0, nil
	}
	if err := s.lockScopeForUpdate(tx, scopeType, scopeID); err != nil {
		return 0, err
	}
	now, err := s.databaseNow(tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Where("scope_type = ? AND scope_id = ? AND expires_at <= ?", scopeType, scopeID, now).
		Delete(&InFlightLease{}).Error; err != nil {
		return 0, err
	}
	var count int64
	if err := tx.Model(&InFlightLease{}).
		Where("scope_type = ? AND scope_id = ? AND expires_at > ?", scopeType, scopeID, now).
		Count(&count).Error; err != nil {
		return 0, err
	}
	if count >= limit {
		return 0, ErrRateLimitExceeded
	}
	ttl := effectiveLeaseTTL(s.inFlightLeaseTTL, 300*time.Second)
	expiresAt := now.Add(ttl)
	lease := InFlightLease{
		ID:        leaseID,
		ScopeType: scopeType,
		ScopeID:   scopeID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := tx.Create(&lease).Error; err != nil {
		return 0, err
	}
	var persisted InFlightLease
	if err := tx.Select("expires_at").First(&persisted, "id = ?", leaseID).Error; err != nil {
		return 0, err
	}
	return s.persistedLeaseConfirmation(tx, persisted.ExpiresAt)
}

func (s *GormStore) renewInFlightLease(ctx context.Context, leaseID string, ttl time.Duration) (time.Duration, bool, error) {
	var confirmedFor time.Duration
	var retained bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		result := tx.Model(&InFlightLease{}).Where("id = ?", leaseID).
			Updates(map[string]any{"expires_at": now.Add(ttl), "updated_at": now})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		var persisted InFlightLease
		if err := tx.Select("expires_at").First(&persisted, "id = ?", leaseID).Error; err != nil {
			return err
		}
		confirmedFor, err = s.persistedLeaseConfirmation(tx, persisted.ExpiresAt)
		if err != nil {
			return err
		}
		retained = confirmedFor > 0
		return nil
	})
	return confirmedFor, retained, err
}

func (s *GormStore) startInFlightLeaseHeartbeat(parent context.Context, leaseID string, confirmedFor time.Duration) context.Context {
	if strings.TrimSpace(leaseID) == "" {
		return parent
	}
	ttl := effectiveLeaseTTL(s.inFlightLeaseTTL, 300*time.Second)
	heartbeat := startLeaseHeartbeat(parent, ttl, confirmedFor, func(attemptCtx context.Context) (time.Duration, bool, error) {
		return s.renewInFlightLease(attemptCtx, leaseID, ttl)
	})
	if previous, loaded := s.leaseHeartbeats.LoadOrStore(leaseID, heartbeat); loaded {
		if previousHeartbeat, ok := previous.(*leaseHeartbeat); ok {
			_ = stopLeaseHeartbeat(previousHeartbeat)
		}
		s.leaseHeartbeats.Store(leaseID, heartbeat)
	}
	return heartbeat.ctx
}

func (s *GormStore) stopInFlightLeaseHeartbeat(leaseID string) error {
	if value, ok := s.leaseHeartbeats.LoadAndDelete(leaseID); ok {
		if heartbeat, ok := value.(*leaseHeartbeat); ok {
			return stopLeaseHeartbeat(heartbeat)
		}
	}
	return nil
}

// ReleaseProviderResourceCapacity releases concurrency bookkeeping without
// treating a local coordination failure as an upstream provider failure.
func (s *GormStore) ReleaseProviderResourceCapacity(resourceID string, leaseID string) {
	_ = s.stopInFlightLeaseHeartbeat(leaseID)
	if strings.TrimSpace(leaseID) == "" {
		return
	}
	if err := s.db.Delete(&InFlightLease{}, "id = ?", leaseID).Error; err != nil {
		log.Printf("[tokenhub] failed to release provider concurrency lease resource=%s lease=%s: %v", resourceID, leaseID, err)
	}
}

func (s *GormStore) providerResourceBucketForUpdate(tx *gorm.DB, resourceID string, bucket string) (ProviderResourceBucket, error) {
	seed := ProviderResourceBucket{ResourceID: resourceID, Bucket: bucket, UpdatedAt: time.Now().UTC()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return ProviderResourceBucket{}, err
	}
	query := tx
	if s.dbDriver == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var item ProviderResourceBucket
	if err := query.First(&item, "resource_id = ? AND bucket = ?", resourceID, bucket).Error; err != nil {
		return ProviderResourceBucket{}, err
	}
	return item, nil
}

func (s *GormStore) consumeProviderResourceRequestCapacity(tx *gorm.DB, resource ProviderResource, now time.Time) error {
	if resource.RateLimitRPM <= 0 && resource.TokenLimitTPM <= 0 {
		return nil
	}
	bucket, err := s.providerResourceBucketForUpdate(tx, resource.ID, minuteBucket(now))
	if err != nil {
		return err
	}
	if resource.RateLimitRPM > 0 && bucket.Requests >= resource.RateLimitRPM {
		return NewHTTPError(http.StatusTooManyRequests, "provider_resource_rpm_exceeded", "Provider resource RPM limit exceeded")
	}
	if resource.TokenLimitTPM > 0 && bucket.Tokens >= resource.TokenLimitTPM {
		return NewHTTPError(http.StatusTooManyRequests, "provider_resource_tpm_exceeded", "Provider resource TPM limit exceeded")
	}
	bucket.Requests++
	bucket.UpdatedAt = now
	return tx.Save(&bucket).Error
}

func (s *GormStore) CheckProviderResourceCapacity(ctx context.Context, resourceID string) (string, context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if resourceID == "" {
		return "", ctx, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	leaseID := NewID("lease")
	acquiredLease := false
	halfOpenClaimed := false
	var leaseConfirmedFor time.Duration
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "provider_resource", resourceID); err != nil {
			return err
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var resource ProviderResource
		if err := query.First(&resource, "id = ?", resourceID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		if !resource.Healthy {
			// Half-open admission. The resource is parked; the trial is claimed by
			// pushing cooldown_until into the future, which both rejects every
			// concurrent request below and pre-arms the next window if this trial
			// fails. The UPDATE is guarded by the deadline it read, so across
			// replicas exactly one caller can win.
			if resource.CooldownUntil == nil || now.Before(*resource.CooldownUntil) {
				return NewHTTPError(http.StatusTooManyRequests, "provider_resource_cooling_down", "Provider resource is cooling down")
			}
			nextDeadline := now.Add(s.cooldownWindow(resource.FailureCount))
			claim := tx.Model(&ProviderResource{}).
				Where("id = ? AND healthy = ? AND cooldown_until = ?", resourceID, false, resource.CooldownUntil).
				Updates(map[string]any{"cooldown_until": &nextDeadline, "updated_at": now})
			if claim.Error != nil {
				return claim.Error
			}
			if claim.RowsAffected == 0 {
				return NewHTTPError(http.StatusTooManyRequests, "provider_resource_cooling_down", "Provider resource is cooling down")
			}
			resource.CooldownUntil = &nextDeadline
			halfOpenClaimed = true
		} else if resource.CooldownUntil != nil && now.Before(*resource.CooldownUntil) {
			return NewHTTPError(http.StatusTooManyRequests, "provider_resource_cooling_down", "Provider resource is cooling down")
		}
		if resource.MaxConcurrency > 0 {
			confirmedFor, err := s.acquireInFlightLease(tx, "provider_resource", resource.ID, resource.MaxConcurrency, leaseID)
			if err != nil {
				if errors.Is(err, ErrRateLimitExceeded) {
					return NewHTTPError(http.StatusTooManyRequests, "provider_resource_concurrency_exceeded", "Provider resource concurrency limit exceeded")
				}
				return err
			}
			leaseConfirmedFor = confirmedFor
			acquiredLease = true
		}
		if err := s.consumeProviderResourceRequestCapacity(tx, resource, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", ctx, err
	}
	// Tag the call context before deriving the lease context, so the marker reaches
	// FinishProviderResourceAttempt on both the leased and the unleased path.
	if halfOpenClaimed {
		ctx = withHalfOpenClaim(ctx)
	}
	if acquiredLease {
		leaseCtx := s.startInFlightLeaseHeartbeat(ctx, leaseID, leaseConfirmedFor)
		return leaseID, leaseCtx, nil
	}
	return "", ctx, nil
}

// CheckProviderResourceRetryCapacity accounts for another physical upstream
// request while retaining the concurrency lease acquired for the logical call.
func (s *GormStore) CheckProviderResourceRetryCapacity(ctx context.Context, resourceID string, leaseID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if resourceID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "provider_resource", resourceID); err != nil {
			return err
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var resource ProviderResource
		if err := query.First(&resource, "id = ?", resourceID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		if strings.TrimSpace(leaseID) != "" {
			var count int64
			if err := tx.Model(&InFlightLease{}).
				Where("id = ? AND scope_type = ? AND scope_id = ? AND expires_at > ?", leaseID, "provider_resource", resourceID, now).
				Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return ErrCoordinationLeaseLost
			}
		}
		return s.consumeProviderResourceRequestCapacity(tx, resource, now)
	})
}

func (s *GormStore) FinishProviderResourceAttempt(ctx context.Context, resourceID string, leaseID string, outcome AttemptOutcome, usage Usage) {
	if resourceID == "" {
		return
	}
	success := outcome.CountsAsHealthy()
	// Only the request that won the half-open trial may close the breaker.
	closesBreaker := outcome == AttemptSucceeded && hasHalfOpenClaim(ctx)
	_ = s.stopInFlightLeaseHeartbeat(leaseID)
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "provider_resource", resourceID); err != nil {
			return err
		}
		if strings.TrimSpace(leaseID) != "" {
			if err := tx.Delete(&InFlightLease{}, "id = ?", leaseID).Error; err != nil {
				return err
			}
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var resource ProviderResource
		if err := query.First(&resource, "id = ?", resourceID).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		updates := map[string]any{"updated_at": now}
		if success {
			if usage.TotalTokens == 0 {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			if usage.TotalTokens > 0 {
				bucket, err := s.providerResourceBucketForUpdate(tx, resourceID, minuteBucket(now))
				if err != nil {
					return err
				}
				bucket.Tokens += usage.TotalTokens
				bucket.UpdatedAt = now
				if err := tx.Save(&bucket).Error; err != nil {
					return err
				}
			}
			switch {
			case outcome != AttemptSucceeded:
				// A neutral outcome (client disconnect, policy refusal, unsupported
				// model) is not the resource's fault, so it must not add a failure —
				// but it is not evidence the upstream works either. Leave the failure
				// count, health and cooldown exactly as they were: clearing the count
				// here would let an alternating failure/disconnect pattern keep the
				// breaker from ever tripping, and would reset the backoff of a
				// half-open trial whose client merely hung up.
			case resource.Healthy:
				// Ordinary success on a live resource: reset the consecutive failure
				// run and drop a stale deadline left behind by an earlier trip.
				updates["failure_count"] = 0
				if resource.CooldownUntil != nil && now.After(*resource.CooldownUntil) {
					updates["cooldown_until"] = nil
				}
			case closesBreaker && resource.Status == StatusActive:
				// The half-open claimant confirmed the upstream: close the breaker.
				updates["failure_count"] = 0
				updates["healthy"] = true
				updates["cooldown_until"] = nil
				updates["last_checked_at"] = now
				if err := tx.Create(&AlertEvent{
					ID:         NewID("alt"),
					ScopeType:  "provider_resource",
					ScopeID:    resource.ID,
					Severity:   "info",
					Code:       "provider_resource_recovered",
					Message:    "Provider resource recovered after a successful half-open request",
					ResourceID: resource.ProviderID,
					CreatedAt:  now,
				}).Error; err != nil {
					return err
				}
			default:
				// The breaker is open and this success came from a request that never
				// held the trial permit — typically one already in flight when the
				// breaker tripped. It proves nothing about the state being probed, so
				// it must not touch the failure count, the deadline, or health.
			}
		} else {
			nextFailures := s.nextFailureCount(resource.FailureCount)
			updates["failure_count"] = nextFailures
			if nextFailures >= s.failureThreshold {
				cooldownUntil := now.Add(s.cooldownWindow(nextFailures))
				updates["healthy"] = false
				updates["cooldown_until"] = &cooldownUntil
				updates["last_checked_at"] = now
				if err := tx.Create(&AlertEvent{
					ID:         NewID("alt"),
					ScopeType:  "provider_resource",
					ScopeID:    resource.ID,
					Severity:   "warning",
					Code:       "provider_resource_cooling_down",
					Message:    "Provider resource entered cooldown after repeated failures",
					ResourceID: resource.ProviderID,
					CreatedAt:  now,
				}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&ProviderResource{}).Where("id = ?", resourceID).Updates(updates).Error
	})
	if err != nil {
		log.Printf("[tokenhub] failed to finish provider resource attempt resource=%s: %v", resourceID, err)
		if strings.TrimSpace(leaseID) != "" {
			if releaseErr := s.db.Delete(&InFlightLease{}, "id = ?", leaseID).Error; releaseErr != nil {
				log.Printf("[tokenhub] failed to release provider concurrency lease resource=%s lease=%s: %v", resourceID, leaseID, releaseErr)
			}
		}
	}
}

func (s *GormStore) TestProvider(id string) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var provider Provider
	if err := s.db.First(&provider, "id = ?", id).Error; err != nil {
		return Provider{}, notFound(err, "provider_not_found", "Provider not found")
	}
	healthy := provider.Status == StatusActive
	if err := s.db.Model(&Provider{}).Where("id = ?", id).Update("healthy", healthy).Error; err != nil {
		return Provider{}, err
	}
	provider.Healthy = healthy
	provider.APIKey = ""
	return provider, nil
}

func (s *GormStore) TestProviderResource(id string) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource ProviderResource
	if err := s.db.First(&resource, "id = ?", id).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_resource_not_found", "Provider resource not found")
	}
	now := time.Now().UTC()
	healthy := resource.Status == StatusActive
	updates := map[string]any{
		"healthy":         healthy,
		"last_checked_at": now,
		"updated_at":      now,
	}
	if healthy {
		updates["failure_count"] = 0
		updates["cooldown_until"] = nil
	}
	if err := s.db.Model(&ProviderResource{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return ProviderResource{}, err
	}
	resource.Healthy = healthy
	resource.LastCheckedAt = &now
	resource.FailureCount = 0
	resource.CooldownUntil = nil
	resource.UpdatedAt = now
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) AddModel(model Model) Model {
	s.mu.Lock()
	defer s.mu.Unlock()
	var existing Model
	if err := s.db.First(&existing, "name = ?", model.Name).Error; err == nil &&
		existing.Metadata[modelDirectoryRoleKey] == modelDirectoryRoleExternal &&
		model.Metadata[modelDirectoryRoleKey] != modelDirectoryRoleExternal {
		model = withExternalModelRole(model)
		model.Status = existing.Status
		model.CreatedAt = existing.CreatedAt
	}

	if model.Modality == "embedding" {
		model.CacheReadPriceUSDPer1M = 0
	}
	if model.ID == "" {
		model.ID = model.Name
	}
	if model.Status == "" {
		model.Status = StatusActive
	}
	if model.CreatedAt.IsZero() {
		model.CreatedAt = time.Now().UTC()
	}
	_ = s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model).Error
	return model
}

func (s *GormStore) ListModels() []Model {
	var items []Model
	if err := s.db.Order("name asc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListModels failed: %v", err)
	}
	return items
}

func (s *GormStore) UpdateModel(name string, patch Model) (Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var updated Model
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var model Model
		if err := tx.First(&model, "name = ?", name).Error; err != nil {
			return notFound(err, "model_not_found", "Model not found")
		}
		originalID := model.ID
		originalName := model.Name
		renamed := patch.Name != "" && patch.Name != name
		if renamed {
			model.Name = patch.Name
			model.ID = patch.Name
		}
		if patch.Family != "" {
			model.Family = patch.Family
		}
		if patch.Modality != "" {
			model.Modality = patch.Modality
		}
		if patch.ContextWindow != 0 {
			model.ContextWindow = patch.ContextWindow
		}
		model.InputPriceUSDPer1M = patch.InputPriceUSDPer1M
		model.CacheReadPriceUSDPer1M = patch.CacheReadPriceUSDPer1M
		model.OutputPriceUSDPer1M = patch.OutputPriceUSDPer1M
		model.EmbeddingPriceUSDPer1M = patch.EmbeddingPriceUSDPer1M
		if model.Modality == "embedding" {
			model.CacheReadPriceUSDPer1M = 0
		}
		if patch.InputModalities != nil {
			model.InputModalities = patch.InputModalities
		}
		if patch.OutputModalities != nil {
			model.OutputModalities = patch.OutputModalities
		}
		if patch.Capabilities != nil {
			model.Capabilities = patch.Capabilities
		}
		if patch.SupportedParameters != nil {
			model.SupportedParameters = patch.SupportedParameters
		}
		if patch.Metadata != nil {
			model.Metadata = patch.Metadata
		}
		if patch.Status != "" {
			model.Status = patch.Status
		}
		if renamed {
			if err := tx.Delete(&Model{}, "id = ?", originalID).Error; err != nil {
				return err
			}
			if err := tx.Create(&model).Error; err != nil {
				return writeConflict(err, "model_conflict", "Model already exists")
			}
			if err := tx.Model(&ModelRoute{}).Where("model_name = ?", originalName).Update("model_name", model.Name).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&model).Error; err != nil {
			return err
		}
		updated = model
		return nil
	})
	return updated, err
}

func (s *GormStore) DeleteModel(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var model Model
		if err := tx.First(&model, "name = ?", name).Error; err != nil {
			return notFound(err, "model_not_found", "Model not found")
		}
		if err := tx.Where("model_name = ?", name).Delete(&ModelRoute{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model).Error
	})
}

func (s *GormStore) AddRoute(route ModelRoute) ModelRoute {
	s.mu.Lock()
	defer s.mu.Unlock()

	if route.ID == "" {
		route.ID = NewID("route")
	}
	if route.Status == "" {
		route.Status = StatusActive
	}
	if route.Weight <= 0 {
		route.Weight = 1
	}
	if route.Strategy == "" {
		route.Strategy = RouteStrategyBalanced
	}
	route.ProjectScope, route.ProjectIDs = normalizeRouteProjectScope(route.ProjectScope, route.ProjectIDs)
	if route.CreatedAt.IsZero() {
		route.CreatedAt = time.Now().UTC()
	}
	_ = s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&route).Error
	return route
}

func (s *GormStore) ListRoutes() []ModelRoute {
	var items []ModelRoute
	if err := s.db.Order("model_name asc, priority asc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListRoutes failed: %v", err)
	}
	for index := range items {
		items[index].ProjectScope, items[index].ProjectIDs = normalizeRouteProjectScope(items[index].ProjectScope, items[index].ProjectIDs)
	}
	return items
}

func (s *GormStore) UpdateRoute(id string, patch ModelRoute) (ModelRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var route ModelRoute
	if err := s.db.First(&route, "id = ?", id).Error; err != nil {
		return ModelRoute{}, notFound(err, "route_not_found", "Route not found")
	}
	if patch.ModelName != "" {
		route.ModelName = patch.ModelName
	}
	if patch.ProviderID != "" {
		route.ProviderID = patch.ProviderID
	}
	route.ProviderResourceID = patch.ProviderResourceID
	route.ResourceGroup = patch.ResourceGroup
	route.StickySession = patch.StickySession
	if patch.ProviderModel != "" {
		route.ProviderModel = patch.ProviderModel
	}
	if patch.Priority != 0 {
		route.Priority = patch.Priority
	}
	if patch.Weight != 0 {
		route.Weight = patch.Weight
	}
	if patch.QualityScore != 0 {
		route.QualityScore = patch.QualityScore
	}
	if patch.CostScore != 0 {
		route.CostScore = patch.CostScore
	}
	if patch.Status != "" {
		route.Status = patch.Status
	}
	if patch.Strategy != "" {
		route.Strategy = patch.Strategy
	}
	if patch.ProjectScope != "" || patch.ProjectIDs != nil {
		route.ProjectScope, route.ProjectIDs = normalizeRouteProjectScope(patch.ProjectScope, patch.ProjectIDs)
	}
	return route, s.db.Save(&route).Error
}

func (s *GormStore) UpdateModelRoutePolicy(modelName string, policy ModelRoutePolicy) ([]ModelRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	modelName = strings.TrimSpace(modelName)
	var updated []ModelRoute
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var routes []ModelRoute
		if err := tx.Where("model_name = ?", modelName).Order("priority asc, created_at asc, id asc").Find(&routes).Error; err != nil {
			return err
		}
		if len(routes) == 0 {
			return NewHTTPError(http.StatusNotFound, "model_routes_not_found", "Model has no routing rules")
		}
		if len(policy.Routes) != len(routes) {
			return NewHTTPError(http.StatusBadRequest, "invalid_model_route_policy", "Routing policy must include every route for the model")
		}

		routeByID := make(map[string]*ModelRoute, len(routes))
		for index := range routes {
			routeByID[routes[index].ID] = &routes[index]
		}
		seen := make(map[string]bool, len(policy.Routes))
		for _, patch := range policy.Routes {
			if seen[patch.RouteID] || routeByID[patch.RouteID] == nil {
				return NewHTTPError(http.StatusBadRequest, "invalid_model_route_policy", "Routing policy contains an unknown or duplicate route")
			}
			if patch.Weight <= 0 || patch.QualityScore < 1 || patch.QualityScore > 100 || patch.CostScore < 1 || patch.CostScore > 100 {
				return NewHTTPError(http.StatusBadRequest, "invalid_model_route_parameters", "Weight must be positive and route scores must be between 1 and 100")
			}
			seen[patch.RouteID] = true
		}

		updated = make([]ModelRoute, 0, len(routes))
		for index, patch := range policy.Routes {
			route := routeByID[patch.RouteID]
			route.Strategy = policy.Strategy
			route.Weight = patch.Weight
			route.QualityScore = patch.QualityScore
			route.CostScore = patch.CostScore
			if policy.Strategy == RouteStrategyPriorityOnly {
				route.Priority = index + 1
			} else {
				route.Priority = 1
			}
			if err := tx.Save(route).Error; err != nil {
				return err
			}
			updated = append(updated, *route)
		}
		return nil
	})
	return updated, err
}

func (s *GormStore) DeleteRoute(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var route ModelRoute
	if err := s.db.First(&route, "id = ?", id).Error; err != nil {
		return notFound(err, "route_not_found", "Route not found")
	}
	return s.db.Delete(&route).Error
}

func (s *GormStore) SelectRoute(modelName string) (RouteSelection, error) {
	routes, err := s.SelectRouteCandidates(modelName)
	if err != nil {
		return RouteSelection{}, err
	}
	if len(routes) == 0 {
		return RouteSelection{}, ErrProviderMissing
	}
	return routes[0], nil
}

func (s *GormStore) SelectRouteCandidates(modelName string) ([]RouteSelection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var routes []ModelRoute
	if err := s.db.Where("model_name = ? AND status = ?", modelName, StatusActive).
		Order("priority asc, weight desc, created_at asc").
		Find(&routes).Error; err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	selections := make([]RouteSelection, 0, len(routes))

	// Batch load all referenced providers
	providerIDs := make([]string, 0, len(routes))
	for _, route := range routes {
		providerIDs = append(providerIDs, route.ProviderID)
	}
	var allProviders []Provider
	if err := s.db.Where("id IN ?", providerIDs).Find(&allProviders).Error; err != nil {
		return nil, err
	}
	providerMap := make(map[string]Provider, len(allProviders))
	for _, p := range allProviders {
		providerMap[p.ID] = p
	}

	// Batch load all referenced ProviderResources
	resourceIDs := make([]string, 0, len(routes))
	for _, route := range routes {
		if route.ProviderResourceID != "" {
			resourceIDs = append(resourceIDs, route.ProviderResourceID)
		}
	}
	var allResources []ProviderResource
	if len(resourceIDs) > 0 {
		if err := s.db.Where("id IN ?", resourceIDs).Find(&allResources).Error; err != nil {
			return nil, err
		}
	}
	resourceMap := make(map[string]ProviderResource, len(allResources))
	for _, r := range allResources {
		resourceMap[r.ID] = r
	}

	// Batch load active resources for providers referenced by unpinned routes,
	// so the route loop does not issue one query per unpinned route.
	resourcesByProvider := make(map[string][]ProviderResource)
	unpinnedProviderIDs := make([]string, 0)
	seenProvider := map[string]bool{}
	for _, route := range routes {
		if route.ProviderResourceID == "" && !seenProvider[route.ProviderID] {
			unpinnedProviderIDs = append(unpinnedProviderIDs, route.ProviderID)
			seenProvider[route.ProviderID] = true
		}
	}
	if len(unpinnedProviderIDs) > 0 {
		var unpinnedResources []ProviderResource
		if err := s.db.Where("provider_id IN ? AND status = ? AND (healthy = ? OR cooldown_until <= ?)",
			unpinnedProviderIDs, StatusActive, true, now).
			Order("priority asc, weight desc, created_at asc").
			Find(&unpinnedResources).Error; err != nil {
			return nil, err
		}
		for _, r := range unpinnedResources {
			resourcesByProvider[r.ProviderID] = append(resourcesByProvider[r.ProviderID], r)
		}
	}

	for _, route := range routes {
		provider, ok := providerMap[route.ProviderID]
		if !ok {
			continue
		}
		if provider.Status != StatusActive || !provider.Healthy {
			continue
		}
		if route.ProviderResourceID != "" {
			resource, exists := resourceMap[route.ProviderResourceID]
			if !exists || resource.ProviderID != provider.ID {
				continue
			}
			if resource.Status != StatusActive || !halfOpenEligible(resource, now) {
				continue
			}
			selection, err := s.routeSelection(provider, &resource, route)
			if err != nil {
				log.Printf("[tokenhub] WARNING: skipping route %s for resource %s: %v", route.ID, resource.ID, err)
				continue
			}
			selections = append(selections, selection)
			continue
		}

		var resources []ProviderResource
		if group := strings.TrimSpace(route.ResourceGroup); group != "" {
			for _, r := range resourcesByProvider[provider.ID] {
				if r.Group == group {
					resources = append(resources, r)
				}
			}
		} else {
			resources = resourcesByProvider[provider.ID]
		}
		if len(resources) == 0 {
			selection, err := s.routeSelection(provider, nil, route)
			if err != nil {
				log.Printf("[tokenhub] WARNING: skipping route %s: %v", route.ID, err)
				continue
			}
			selections = append(selections, selection)
			continue
		}
		for _, resource := range resources {
			resourceRoute := route
			resourceRoute.ProviderResourceID = resource.ID
			if resource.Weight > 0 {
				resourceRoute.Weight = resource.Weight
			}
			selection, err := s.routeSelection(provider, &resource, resourceRoute)
			if err != nil {
				log.Printf("[tokenhub] WARNING: skipping route %s for resource %s: %v", resourceRoute.ID, resource.ID, err)
				continue
			}
			selections = append(selections, selection)
		}
	}
	s.attachRouteRuntimeStats(selections, now)
	if len(selections) == 0 {
		return nil, ErrProviderMissing
	}
	return selections, nil
}

type routeRuntimeStatsRow struct {
	RouteID            string
	ProviderResourceID string
	Samples            int64
	Successes          int64
	LatencyMS          float64
}

func (s *GormStore) attachRouteRuntimeStats(selections []RouteSelection, now time.Time) {
	routeIDs := make([]string, 0, len(selections))
	seen := map[string]bool{}
	for _, selection := range selections {
		if routeStrategy(selection.Route) != RouteStrategyAdaptive || seen[selection.Route.ID] {
			continue
		}
		seen[selection.Route.ID] = true
		routeIDs = append(routeIDs, selection.Route.ID)
	}
	if len(routeIDs) == 0 {
		return
	}

	var rows []routeRuntimeStatsRow
	err := s.db.Model(&RouteAttemptLog{}).
		Select(`route_id, provider_resource_id, COUNT(*) AS samples,
			SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END) AS successes,
			COALESCE(AVG(CASE WHEN status_code >= 200 AND status_code < 400 THEN latency_ms ELSE NULL END), 0) AS latency_ms`).
		Where("invoked = ? AND created_at >= ? AND route_id IN ?", true, now.Add(-adaptiveRoutingWindow), routeIDs).
		Group("route_id, provider_resource_id").
		Scan(&rows).Error
	if err != nil {
		log.Printf("[tokenhub] failed to load adaptive routing observations: %v", err)
		return
	}
	stats := make(map[string]RouteRuntimeStats, len(rows))
	for _, row := range rows {
		successRate := float64(0)
		if row.Samples > 0 {
			successRate = float64(row.Successes) / float64(row.Samples)
		}
		stats[routeRuntimeStatsKey(row.RouteID, row.ProviderResourceID)] = RouteRuntimeStats{
			Samples:     row.Samples,
			SuccessRate: successRate,
			LatencyMS:   int64(math.Round(row.LatencyMS)),
		}
	}
	for index := range selections {
		selection := &selections[index]
		selection.Runtime = stats[routeRuntimeStatsKey(selection.Route.ID, routeResourceID(*selection))]
	}
}

func routeRuntimeStatsKey(routeID string, resourceID string) string {
	return routeID + "\x00" + resourceID
}

func (s *GormStore) routeSelection(provider Provider, resource *ProviderResource, route ModelRoute) (RouteSelection, error) {
	decrypted, err := s.decryptSecret(provider.APIKey)
	if err != nil {
		return RouteSelection{}, fmt.Errorf("decrypt provider API key for %s: %w", provider.ID, err)
	}
	provider.APIKey = decrypted
	if resource == nil {
		return RouteSelection{
			Provider:      provider,
			ProviderModel: route.ProviderModel,
			Route:         route,
		}, nil
	}
	effective := provider
	if resource.BaseURL != "" {
		effective.BaseURL = resource.BaseURL
	}
	if resource.APIKey != "" {
		decrypted, err := s.decryptSecret(resource.APIKey)
		if err != nil {
			return RouteSelection{}, fmt.Errorf("decrypt resource API key for %s: %w", resource.ID, err)
		}
		effective.APIKey = decrypted
	}
	if len(resource.Headers) > 0 {
		headers := map[string]string{}
		for key, value := range provider.Headers {
			headers[key] = value
		}
		for key, value := range resource.Headers {
			headers[key] = value
		}
		effective.Headers = headers
	}
	if len(resource.Options) > 0 {
		options := map[string]string{}
		for key, value := range provider.Options {
			options[key] = value
		}
		for key, value := range resource.Options {
			options[key] = value
		}
		effective.Options = options
	}
	publicResource := *resource
	redactProviderResourceSecrets(&publicResource)
	return RouteSelection{
		Provider:      effective,
		Resource:      &publicResource,
		ProviderModel: route.ProviderModel,
		Route:         route,
	}, nil
}

func (s *GormStore) MarkRouteUsed(routeID string) {
	if routeID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	_ = s.db.Model(&ModelRoute{}).Where("id = ?", routeID).Update("last_used_at", now).Error
}

func (s *GormStore) MarkProviderResourceUsed(resourceID string) {
	if resourceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	_ = s.db.Model(&ProviderResource{}).
		Where("id = ?", resourceID).
		Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error
}

func (s *GormStore) StartCall(ctx context.Context, project Project, key APIKey, modelName string) (CallContext, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var call CallContext
	requestID := NewID("req")
	leaseAcquired := false
	var leaseConfirmedFor time.Duration
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "api_key", key.ID); err != nil {
			return err
		}
		var privateKey APIKey
		if err := tx.First(&privateKey, "id = ?", key.ID).Error; err != nil {
			return ErrInvalidAPIKey
		}
		hydrateAPIKey(&privateKey)
		var model Model
		if err := tx.First(&model, "name = ? AND status = ?", modelName, StatusActive).Error; err != nil {
			return ErrModelNotAllowed
		}
		if len(privateKey.AllowedModels) > 0 && !privateKey.AllowedModels[modelName] {
			return ErrModelNotAllowed
		}
		effectiveLimits := mergeQuotaLimits(privateKey.Limits, quotaPolicyLimits(tx, project, privateKey))
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		dayCounter, err := s.quotaBucketForUpdate(tx, privateKey.ID, "day", dayBucket(now))
		if err != nil {
			return err
		}
		monthCounter, err := s.quotaBucketForUpdate(tx, privateKey.ID, "month", monthBucket(now))
		if err != nil {
			return err
		}
		if effectiveLimits.MaxConcurrency > 0 {
			confirmedFor, err := s.acquireInFlightLease(tx, "api_key", privateKey.ID, effectiveLimits.MaxConcurrency, requestID)
			if err != nil {
				return err
			}
			leaseConfirmedFor = confirmedFor
			leaseAcquired = true
		}
		if exceedsRequestQuota(effectiveLimits, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter) ||
			exceedsTokenQuota(effectiveLimits, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter) ||
			exceedsCostQuota(effectiveLimits, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter) {
			return ErrQuotaExceeded
		}
		if err := s.checkRuntimeBudget(tx, project); err != nil {
			return err
		}
		dayCounter.Requests++
		monthCounter.Requests++
		if err := tx.Save(&dayCounter).Error; err != nil {
			return err
		}
		if err := tx.Save(&monthCounter).Error; err != nil {
			return err
		}
		call = CallContext{
			RequestID:      requestID,
			Project:        project,
			Key:            publicKey(privateKey),
			Model:          model,
			StartedAt:      now,
			requestContext: ctx,
		}
		return nil
	})
	if err != nil {
		return CallContext{}, err
	}
	if leaseAcquired {
		call.requestContext = s.startInFlightLeaseHeartbeat(ctx, requestID, leaseConfirmedFor)
	}
	return call, nil
}

func (s *GormStore) FinishCall(call CallContext, route RouteSelection, usage Usage, statusCode int, errorCode string, clientIP string, userAgent string) {
	// Measured here rather than inside the deferred observation, so latency reflects
	// what the client waited for and excludes the persistence and lock time that
	// follows. FinishCall is invoked after the last streamed byte is written.
	elapsed := time.Duration(0)
	if !call.StartedAt.IsZero() {
		elapsed = time.Since(call.StartedAt)
	}
	_ = s.stopInFlightLeaseHeartbeat(call.RequestID)
	// priceUsage is pure, so it runs outside the lock and its result is final here.
	usage = priceUsage(call.Model, usage)
	// Registered before the lock is taken, so LIFO ordering runs it *after* the
	// unlock: reporting metrics must not extend how long this request holds the
	// store-wide mutex. Deferring also means the request is still counted when the
	// transaction below fails or panics — losing persistence must not also lose the
	// observation that the request happened.
	defer s.observeGatewayCall(call, route, usage, statusCode, errorCode, elapsed)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		return s.finishCallTransaction(tx, call, route, usage, statusCode, errorCode, clientIP, userAgent, now)
	})
	if err != nil {
		log.Printf("[tokenhub] failed to finish call request=%s: %v", call.RequestID, err)
		if releaseErr := s.db.Delete(&InFlightLease{}, "id = ?", call.RequestID).Error; releaseErr != nil {
			log.Printf("[tokenhub] failed to release API key concurrency lease request=%s: %v", call.RequestID, releaseErr)
		}
	}
}

func (s *GormStore) finishCallTransaction(tx *gorm.DB, call CallContext, route RouteSelection, usage Usage, statusCode int, errorCode string, clientIP string, userAgent string, now time.Time) error {
	if call.Key.ID != "" {
		if err := s.lockScopeForUpdate(tx, "api_key", call.Key.ID); err != nil {
			return err
		}
	}
	var key APIKey
	if err := tx.First(&key, "id = ?", call.Key.ID).Error; err == nil {
		dayCounter, err := s.quotaBucketForUpdate(tx, key.ID, "day", dayBucket(now))
		if err != nil {
			return err
		}
		monthCounter, err := s.quotaBucketForUpdate(tx, key.ID, "month", monthBucket(now))
		if err != nil {
			return err
		}
		addUsage(&dayCounter.QuotaCounter, usage)
		addUsage(&monthCounter.QuotaCounter, usage)
		if err := tx.Save(&dayCounter).Error; err != nil {
			return err
		}
		if err := tx.Save(&monthCounter).Error; err != nil {
			return err
		}
		if err := raiseQuotaAlerts(tx, key, &dayCounter.QuotaCounter, &monthCounter.QuotaCounter); err != nil {
			return err
		}
	}
	if usage.TotalTokens > 0 || usage.CostUSD > 0 {
		if err := tx.Create(newUsageRecord(call, route, usage, now)).Error; err != nil {
			return err
		}
	}
	if err := tx.Create(&RequestLog{
		ID:                 NewID("log"),
		RequestID:          call.RequestID,
		ProjectID:          call.Project.ID,
		APIKeyID:           call.Key.ID,
		ModelName:          call.Model.Name,
		ProviderID:         route.Provider.ID,
		ProviderResourceID: routeResourceID(route),
		ProviderModel:      route.ProviderModel,
		UpstreamRequestID:  usage.UpstreamRequestID,
		ServedModel:        usage.ServedModel,
		ModelETag:          usage.ModelETag,
		Transport:          usage.Transport,
		StatusCode:         statusCode,
		ErrorCode:          errorCode,
		LatencyMS:          time.Since(call.StartedAt).Milliseconds(),
		ClientIP:           clientIP,
		UserAgent:          userAgent,
		CreatedAt:          now,
	}).Error; err != nil {
		return err
	}
	if route.Provider.ID != "" {
		if err := tx.Create(&ProviderObservation{
			ID:          NewID("pob"),
			ProviderID:  route.Provider.ID,
			ResourceID:  routeResourceID(route),
			AdapterType: route.Provider.Type,
			Source:      "gateway_request",
			Operation:   "inference",
			Success:     providerObservationSuccess(statusCode, errorCode),
			LatencyMS:   time.Since(call.StartedAt).Milliseconds(),
			ErrorCode:   errorCode,
			ObservedAt:  now,
		}).Error; err != nil {
			return err
		}
	}
	if resourceID := routeResourceID(route); resourceID != "" && (len(usage.ResponseHeaders) > 0 || usage.UpstreamRequestID != "" || usage.ServedModel != "") {
		observation := ProviderResourceObservation{
			ResourceID:        resourceID,
			AdapterType:       route.Provider.Type,
			RateLimitHeaders:  codexRateLimitHeaders(usage.ResponseHeaders),
			UpstreamRequestID: usage.UpstreamRequestID,
			ServedModel:       usage.ServedModel,
			ModelETag:         usage.ModelETag,
			Transport:         usage.Transport,
			UpdatedAt:         now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "resource_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"adapter_type",
				"rate_limit_headers",
				"upstream_request_id",
				"served_model",
				"model_e_tag",
				"transport",
				"updated_at",
			}),
		}).Create(&observation).Error; err != nil {
			return err
		}
	}
	return tx.Delete(&InFlightLease{}, "id = ?", call.RequestID).Error
}

func (s *GormStore) RecordPlaygroundRequest(call CallContext, route RouteSelection, statusCode int, errorCode string, clientIP string, userAgent string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.db.Create(&RequestLog{
		ID:                 NewID("log"),
		RequestID:          call.RequestID,
		ProjectID:          call.Project.ID,
		APIKeyID:           call.Key.ID,
		ModelName:          call.Model.Name,
		ProviderID:         route.Provider.ID,
		ProviderResourceID: routeResourceID(route),
		ProviderModel:      route.ProviderModel,
		StatusCode:         statusCode,
		ErrorCode:          errorCode,
		LatencyMS:          time.Since(call.StartedAt).Milliseconds(),
		ClientIP:           clientIP,
		UserAgent:          userAgent,
		CreatedAt:          time.Now().UTC(),
	}).Error
}

func (s *GormStore) RecordRouteAttempts(requestID string, attempts []RouteAttempt) {
	if requestID == "" || len(attempts) == 0 {
		return
	}
	now := time.Now().UTC()
	items := make([]RouteAttemptLog, 0, len(attempts))
	for index, attempt := range attempts {
		items = append(items, RouteAttemptLog{
			ID:                 NewID("rat"),
			RequestID:          requestID,
			AttemptIndex:       index + 1,
			RouteID:            attempt.Selection.Route.ID,
			ProviderID:         attempt.Selection.Provider.ID,
			ProviderResourceID: routeResourceID(attempt.Selection),
			ProviderModel:      attempt.Selection.ProviderModel,
			StatusCode:         attempt.Status,
			ErrorCode:          attempt.ErrorCode,
			ErrorMessage:       attempt.Error,
			Invoked:            attempt.Invoked,
			LatencyMS:          attempt.LatencyMS,
			CreatedAt:          now,
		})
	}
	_ = s.db.Create(&items).Error
}

func (s *GormStore) RecordRejectedRequest(project Project, key APIKey, modelName string, stream bool, statusCode int, errorCode string, clientIP string, userAgent string) string {
	requestID := NewID("req")
	_ = s.db.Create(&RequestLog{
		ID:         NewID("log"),
		RequestID:  requestID,
		ProjectID:  project.ID,
		APIKeyID:   key.ID,
		ModelName:  modelName,
		StatusCode: statusCode,
		ErrorCode:  errorCode,
		ClientIP:   clientIP,
		UserAgent:  userAgent,
		CreatedAt:  time.Now().UTC(),
	}).Error
	// A rejected request never reached a provider, so it contributes to the request
	// counter only: no duration, no tokens, no cost. Emitting zeroes for those would
	// create series that dilute every rate() over them.
	//
	// The model name here is unvalidated client input — this path is reached precisely
	// when a request is refused, including for naming a model that does not exist.
	// Using it verbatim as a label would let anyone mint unbounded series by looping
	// over random model names, so it is collapsed unless the catalog knows it.
	s.metrics.ObserveGatewayCall(GatewayCallSample{
		Model:      s.knownModelLabel(modelName),
		ProjectID:  project.ID,
		Stream:     stream,
		StatusCode: statusCode,
		ErrorCode:  errorCode,
	})
	return requestID
}

// observeGatewayCall reports a completed gateway request. Kept separate from FinishCall
// so the deferred call reads as one statement and the label mapping lives in one place.
func (s *GormStore) observeGatewayCall(call CallContext, route RouteSelection, usage Usage, statusCode int, errorCode string, elapsed time.Duration) {
	if s.metrics == nil {
		return
	}
	sample := GatewayCallSample{
		Model:        call.Model.Name,
		ProviderType: route.Provider.Type,
		ProviderID:   route.Provider.ID,
		ResourceID:   routeResourceID(route),
		ProjectID:    call.Project.ID,
		StatusCode:   statusCode,
		ErrorCode:    errorCode,
		Stream:       call.Stream,
		Usage:        usage,
		Duration:     elapsed,
	}
	s.metrics.ObserveGatewayCall(sample)
}

// knownModelLabel keeps a model name as a label only when the catalog knows it,
// bounding the label to configured models instead of arbitrary client input.
func (s *GormStore) knownModelLabel(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" || s.metrics == nil {
		return ""
	}
	var count int64
	if err := s.db.Model(&Model{}).Where("name = ?", modelName).Limit(1).Count(&count).Error; err != nil || count == 0 {
		return "unknown"
	}
	return modelName
}

func (s *GormStore) RecordRequestPayload(requestID string, requestBody string, requestTruncated bool, responseBody string, responseTruncated bool) {
	if requestID == "" {
		return
	}
	_ = s.db.Create(&RequestPayloadLog{
		ID:                NewID("pay"),
		RequestID:         requestID,
		RequestBody:       requestBody,
		ResponseBody:      responseBody,
		RequestTruncated:  requestTruncated,
		ResponseTruncated: responseTruncated,
		CreatedAt:         time.Now().UTC(),
	}).Error
}

func (s *GormStore) UsageSummary() map[string]any {
	var records []UsageRecord
	var logs []RequestLog
	if err := s.db.Find(&records).Error; err != nil {
		log.Printf("[tokenhub] ERROR: UsageSummary failed to load usage records: %v", err)
	}
	if err := s.db.Find(&logs).Error; err != nil {
		log.Printf("[tokenhub] ERROR: UsageSummary failed to load request logs: %v", err)
	}

	var input, cachedInput, cacheWrite, output, reasoningOutput, total int64
	var cost float64
	errorsCount := 0
	for _, record := range records {
		input += record.InputTokens
		cachedInput += record.CachedInputTokens
		cacheWrite += record.CacheWriteTokens
		output += record.OutputTokens
		reasoningOutput += record.ReasoningTokens
		total += record.TotalTokens
		cost += record.CostUSD
	}
	for _, log := range logs {
		if isPlaygroundRequestLog(log) {
			continue
		}
		if log.StatusCode >= 400 {
			errorsCount++
		}
	}
	return map[string]any{
		"request_count":            billableRequestLogCount(logs),
		"usage_record_count":       len(records),
		"input_tokens":             input,
		"cached_input_tokens":      cachedInput,
		"cache_write_input_tokens": cacheWrite,
		"output_tokens":            output,
		"reasoning_output_tokens":  reasoningOutput,
		"total_tokens":             total,
		"estimated_cost_usd":       cost,
		"errors":                   errorsCount,
	}
}

func billableRequestLogCount(logs []RequestLog) int {
	count := 0
	for _, log := range logs {
		if !isPlaygroundRequestLog(log) {
			count++
		}
	}
	return count
}

func isPlaygroundRequestLog(log RequestLog) bool {
	return log.ProjectID == "admin_playground"
}

func (s *GormStore) ListUsageRecords() []UsageRecord {
	var records []UsageRecord
	if err := s.db.Find(&records).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListUsageRecords failed: %v", err)
	}
	return records
}

func (s *GormStore) UsageBreakdown() map[string]any {
	var records []UsageRecord
	if err := s.db.Find(&records).Error; err != nil {
		log.Printf("[tokenhub] ERROR: UsageBreakdown failed: %v", err)
	}
	return s.usageBreakdownFromRecords(records)
}

func (s *GormStore) UsageBreakdownForPeriod(period string) map[string]any {
	period = normalizeBillingPeriod(period, time.Now().UTC())
	var records []UsageRecord
	if err := s.db.Where("created_at >= ? AND created_at < ?", periodStart(period), periodEnd(period)).Find(&records).Error; err != nil {
		log.Printf("[tokenhub] ERROR: UsageBreakdownForPeriod failed: %v", err)
	}
	return s.usageBreakdownFromRecords(records)
}

func (s *GormStore) usageBreakdownFromRecords(records []UsageRecord) map[string]any {
	return map[string]any{
		"projects":  aggregateUsage(records, func(record UsageRecord) string { return record.ProjectID }),
		"models":    aggregateUsage(records, func(record UsageRecord) string { return record.ModelName }),
		"providers": aggregateUsage(records, func(record UsageRecord) string { return record.ProviderID }),
		"provider_resources": aggregateUsage(records, func(record UsageRecord) string {
			return record.ProviderResourceID
		}),
		"cost_centers": aggregateUsage(records, func(record UsageRecord) string {
			project, ok := s.GetProject(record.ProjectID)
			if !ok {
				return "unknown"
			}
			return s.costCenterForProject(project)
		}),
	}
}

func (s *GormStore) UsageTimeseries(days int) []map[string]any {
	if days <= 0 {
		days = 31
	}
	if days > 90 {
		days = 90
	}
	now := time.Now().UTC()
	series := make([]map[string]any, 0, days)
	indexByDay := map[string]int{}
	for i := days - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		indexByDay[day] = len(series)
		series = append(series, map[string]any{
			"date":                day,
			"request_count":       int64(0),
			"input_tokens":        int64(0),
			"cached_input_tokens": int64(0),
			"output_tokens":       int64(0),
			"total_tokens":        int64(0),
			"estimated_cost_usd":  float64(0),
		})
	}
	var records []UsageRecord
	if err := s.db.Where("created_at >= ?", now.AddDate(0, 0, -days+1)).Find(&records).Error; err != nil {
		log.Printf("[tokenhub] ERROR: UsageTimeseries failed: %v", err)
	}
	for _, record := range records {
		day := record.CreatedAt.UTC().Format("2006-01-02")
		idx, ok := indexByDay[day]
		if !ok {
			continue
		}
		series[idx]["request_count"] = series[idx]["request_count"].(int64) + 1
		series[idx]["input_tokens"] = series[idx]["input_tokens"].(int64) + record.InputTokens
		series[idx]["cached_input_tokens"] = series[idx]["cached_input_tokens"].(int64) + record.CachedInputTokens
		series[idx]["output_tokens"] = series[idx]["output_tokens"].(int64) + record.OutputTokens
		series[idx]["total_tokens"] = series[idx]["total_tokens"].(int64) + record.TotalTokens
		series[idx]["estimated_cost_usd"] = series[idx]["estimated_cost_usd"].(float64) + record.CostUSD
	}
	return series
}

func (s *GormStore) GenerateBillingPeriod(period string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	period = normalizeBillingPeriod(period, time.Now().UTC())
	var records []UsageRecord
	if err := s.db.Where("created_at >= ? AND created_at < ?", periodStart(period), periodEnd(period)).
		Find(&records).Error; err != nil {
		return nil, err
	}

	type bucket struct {
		CostCenter        string
		ProjectID         string
		TeamID            string
		RequestCount      int64
		InputTokens       int64
		CachedInputTokens int64
		OutputTokens      int64
		TotalTokens       int64
		CostUSD           float64
	}
	buckets := map[string]*bucket{}
	projectTotals := map[string]float64{}
	teamTotals := map[string]float64{}
	for _, record := range records {
		project, _ := s.GetProject(record.ProjectID)
		costCenter := s.costCenterForProject(project)
		key := costCenter + "\x00" + record.ProjectID
		item, ok := buckets[key]
		if !ok {
			item = &bucket{CostCenter: costCenter, ProjectID: record.ProjectID, TeamID: project.TeamID}
			buckets[key] = item
		}
		item.RequestCount++
		item.InputTokens += record.InputTokens
		item.CachedInputTokens += record.CachedInputTokens
		item.OutputTokens += record.OutputTokens
		item.TotalTokens += record.TotalTokens
		item.CostUSD += record.CostUSD
		if strings.TrimSpace(record.ProjectID) != "" {
			projectTotals[record.ProjectID] += record.CostUSD
		}
		if strings.TrimSpace(project.TeamID) != "" {
			teamTotals[project.TeamID] += record.CostUSD
		}
	}

	chargebacks := 0
	invoices := 0
	totals := map[string]float64{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := deleteGeneratedResourcesByPeriod(tx, "chargebacks", period); err != nil {
			return err
		}
		if err := deleteGeneratedResourcesByPeriod(tx, "invoices", period); err != nil {
			return err
		}
		for _, item := range buckets {
			if item.CostUSD <= 0 && item.TotalTokens <= 0 {
				continue
			}
			totals[item.CostCenter] += item.CostUSD
			if err := tx.Create(&AdminResource{
				ID:          NewID(resourcePrefix("chargebacks")),
				Kind:        "chargebacks",
				Name:        fmt.Sprintf("%s %s %s 分摊", period, item.CostCenter, item.ProjectID),
				Description: "由 TokenHub 用量记录自动生成",
				Status:      StatusActive,
				Fields: map[string]any{
					"period":              period,
					"cost_center":         item.CostCenter,
					"project_id":          item.ProjectID,
					"team_id":             item.TeamID,
					"allocated_cost_usd":  roundMoney(item.CostUSD),
					"request_count":       item.RequestCount,
					"input_tokens":        item.InputTokens,
					"cached_input_tokens": item.CachedInputTokens,
					"output_tokens":       item.OutputTokens,
					"total_tokens":        item.TotalTokens,
					"allocation_rule":     "actual_usage_cost",
					"generated_by":        "tokenhub",
				},
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}).Error; err != nil {
				return err
			}
			chargebacks++
		}
		for costCenter, amount := range totals {
			if err := tx.Create(&AdminResource{
				ID:          NewID(resourcePrefix("invoices")),
				Kind:        "invoices",
				Name:        fmt.Sprintf("%s %s 内部账单", period, costCenter),
				Description: "由部门分摊记录汇总生成",
				Status:      "pending",
				Fields: map[string]any{
					"period":       period,
					"cost_center":  costCenter,
					"amount_usd":   roundMoney(amount),
					"invoice_note": defaultInvoiceNote(period, costCenter, amount),
					"confirmed_by": "",
					"generated_by": "tokenhub",
				},
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}).Error; err != nil {
				return err
			}
			invoices++
		}
		return updateBudgetsFromUsage(tx, period, totals, projectTotals, teamTotals)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"period":             period,
		"usage_records":      len(records),
		"cost_centers":       len(totals),
		"chargebacks":        chargebacks,
		"invoices":           invoices,
		"allocated_cost_usd": roundMoney(sumFloatMap(totals)),
	}, nil
}

func (s *GormStore) ListRequestLogs() []RequestLog {
	var items []RequestLog
	if err := s.db.Order("created_at desc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListRequestLogs failed: %v", err)
	}
	return items
}

func (s *GormStore) ListProviderObservations(since time.Time) []ProviderObservation {
	var items []ProviderObservation
	query := s.db
	if !since.IsZero() {
		query = query.Where("observed_at >= ?", since.UTC())
	}
	_ = query.Order("observed_at desc").Find(&items).Error
	return items
}

func (s *GormStore) RecordProviderObservation(observation ProviderObservation) {
	if observation.ID == "" {
		observation.ID = NewID("pob")
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now().UTC()
	}
	_ = s.db.Create(&observation).Error
}

func (s *GormStore) GetProviderResourceObservation(resourceID string) (ProviderResourceObservation, bool) {
	var observation ProviderResourceObservation
	err := s.db.First(&observation, "resource_id = ?", strings.TrimSpace(resourceID)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ProviderResourceObservation{}, false
	}
	return observation, err == nil
}

func (s *GormStore) SaveProviderResourceQuota(resourceID string, adapterType string, snapshot string, fetchedAt time.Time) error {
	observation := ProviderResourceObservation{
		ResourceID:     strings.TrimSpace(resourceID),
		AdapterType:    strings.TrimSpace(adapterType),
		QuotaSnapshot:  snapshot,
		QuotaFetchedAt: &fetchedAt,
		UpdatedAt:      time.Now().UTC(),
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "resource_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"adapter_type",
			"quota_snapshot",
			"quota_fetched_at",
			"updated_at",
		}),
	}).Create(&observation).Error
}

func (s *GormStore) GetRequestDetail(requestID string) (map[string]any, error) {
	var requestLog RequestLog
	if err := s.db.First(&requestLog, "request_id = ?", requestID).Error; err != nil {
		return nil, notFound(err, "request_not_found", "Request not found")
	}
	var usage []UsageRecord
	if err := s.db.Where("request_id = ?", requestID).Find(&usage).Error; err != nil {
		log.Printf("[tokenhub] ERROR: GetRequestDetail failed to load usage for request %s: %v", requestID, err)
	}
	var attempts []RouteAttemptLog
	if err := s.db.Where("request_id = ?", requestID).Order("attempt_index asc").Find(&attempts).Error; err != nil {
		log.Printf("[tokenhub] ERROR: GetRequestDetail failed to load attempts for request %s: %v", requestID, err)
	}
	var payload RequestPayloadLog
	var payloadValue any
	if err := s.db.First(&payload, "request_id = ?", requestID).Error; err == nil {
		payloadValue = payload
	}
	return map[string]any{
		"log":      requestLog,
		"usage":    usage,
		"attempts": attempts,
		"payload":  payloadValue,
	}, nil
}

func (s *GormStore) ListAlerts() []AlertEvent {
	var items []AlertEvent
	if err := s.db.Order("created_at desc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListAlerts failed: %v", err)
	}
	return items
}

func (s *GormStore) GetAlert(id string) (AlertEvent, error) {
	var item AlertEvent
	if err := s.db.First(&item, "id = ?", id).Error; err != nil {
		return AlertEvent{}, notFound(err, "alert_not_found", "Alert not found")
	}
	return item, nil
}

func (s *GormStore) ListAlertDeliveries() []AlertDelivery {
	var items []AlertDelivery
	if err := s.db.Order("created_at desc").Limit(500).Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListAlertDeliveries failed: %v", err)
	}
	return items
}

func (s *GormStore) RecordAlertDelivery(delivery AlertDelivery) AlertDelivery {
	if delivery.ID == "" {
		delivery.ID = NewID("dlv")
	}
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = time.Now().UTC()
	}
	if delivery.Status == "" {
		delivery.Status = "pending"
	}
	_ = s.db.Create(&delivery).Error
	return delivery
}

func (s *GormStore) ListAuditEvents() []AuditEvent {
	var items []AuditEvent
	if err := s.db.Order("created_at desc").Limit(500).Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListAuditEvents failed: %v", err)
	}
	return items
}

func (s *GormStore) RecordAuditEvent(event AuditEvent) {
	if event.ID == "" {
		event.ID = NewID("audit")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.Status == "" {
		event.Status = "success"
	}
	_ = s.db.Create(&event).Error
}

func (s *GormStore) CreateResource(kind string, resource AdminResource) AdminResource {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if resource.ID == "" {
		resource.ID = NewID(resourcePrefix(kind))
	}
	if resource.Status == "" {
		resource.Status = StatusActive
	}
	if resource.Fields == nil {
		resource.Fields = map[string]any{}
	}
	resource.Kind = kind
	if resource.CreatedAt.IsZero() {
		resource.CreatedAt = now
	}
	resource.UpdatedAt = now
	_ = s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&resource).Error
	return resource
}

func (s *GormStore) ListResources(kind string) []AdminResource {
	var items []AdminResource
	if err := s.db.Where("kind = ?", kind).Order("created_at asc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListResources failed: %v", err)
	}
	return items
}

func (s *GormStore) UpdateResource(kind string, id string, patch AdminResource) (AdminResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource AdminResource
	if err := s.db.First(&resource, "kind = ? AND id = ?", kind, id).Error; err != nil {
		return AdminResource{}, notFound(err, "resource_not_found", "Resource not found")
	}
	if patch.Name != "" {
		resource.Name = patch.Name
	}
	resource.Description = patch.Description
	if patch.Status != "" {
		resource.Status = patch.Status
	}
	if patch.Fields != nil {
		resource.Fields = patch.Fields
	}
	resource.UpdatedAt = time.Now().UTC()
	return resource, s.db.Save(&resource).Error
}

func (s *GormStore) DeleteResource(kind string, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource AdminResource
	if err := s.db.First(&resource, "kind = ? AND id = ?", kind, id).Error; err != nil {
		return notFound(err, "resource_not_found", "Resource not found")
	}
	return s.db.Delete(&resource).Error
}

func (s *GormStore) DeleteTeam(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		team, err := lockAdminResourceForMutation(tx, "teams", id)
		if err != nil {
			return err
		}
		var projectLinkCount int64
		if err := tx.Model(&ProjectTeam{}).Where("team_id = ?", id).Count(&projectLinkCount).Error; err != nil {
			return err
		}
		var primaryProjectCount int64
		if err := tx.Model(&Project{}).Where("team_id = ?", id).Count(&primaryProjectCount).Error; err != nil {
			return err
		}
		if projectLinkCount > 0 || primaryProjectCount > 0 {
			return NewHTTPError(http.StatusConflict, "team_has_projects", "Team is linked to one or more projects; unlink or transfer those projects first")
		}
		var users []AdminUser
		if err := tx.Find(&users).Error; err != nil {
			return err
		}
		for _, user := range users {
			if userHasTeam(user, id) {
				return NewHTTPError(http.StatusConflict, "team_has_users", "Team still has users; reassign them before deleting the team")
			}
		}
		return tx.Delete(&team).Error
	})
}

func (s *GormStore) RunMonitor(id string) (MonitorRunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var monitor AdminResource
	if err := s.db.First(&monitor, "kind = ? AND id = ?", "monitors", id).Error; err != nil {
		return MonitorRunResult{}, notFound(err, "monitor_not_found", "Monitor not found")
	}
	started := time.Now().UTC()
	result := MonitorRunResult{
		MonitorID: monitor.ID,
		CheckedAt: started,
		Status:    "ok",
	}
	fields := cloneFields(monitor.Fields)
	targetType := strings.ToLower(strings.TrimSpace(stringField(fields, "target_type")))
	if targetType == "" {
		targetType = inferMonitorTargetType(fields)
	}
	result.TargetType = targetType
	switch targetType {
	case "provider":
		providerID := strings.TrimSpace(firstStringField(fields, "provider_id", "provider"))
		result.ProviderID = providerID
		result.TargetID = providerID
		var provider Provider
		if err := s.db.First(&provider, "id = ?", providerID).Error; err != nil {
			result.Status = "failed"
			result.Message = "Provider 不存在"
		} else {
			healthy := provider.Status == StatusActive
			result.Status = okFailed(healthy)
			result.Message = monitorProviderMessage(provider, healthy)
			now := time.Now().UTC()
			_ = s.db.Model(&Provider{}).Where("id = ?", provider.ID).Update("healthy", healthy).Error
			if !healthy {
				_ = s.db.Model(&ProviderResource{}).Where("provider_id = ?", provider.ID).Updates(map[string]any{"healthy": false, "last_checked_at": now, "updated_at": now}).Error
			}
		}
	case "resource", "provider_resource":
		resourceID := strings.TrimSpace(firstStringField(fields, "provider_resource_id", "resource_id", "resource"))
		result.ResourceID = resourceID
		result.TargetID = resourceID
		var resource ProviderResource
		if err := s.db.First(&resource, "id = ?", resourceID).Error; err != nil {
			result.Status = "failed"
			result.Message = "Provider 资源实例不存在"
		} else {
			healthy := resource.Status == StatusActive
			result.ProviderID = resource.ProviderID
			result.Status = okFailed(healthy)
			result.Message = monitorResourceMessage(resource, healthy)
			now := time.Now().UTC()
			updates := map[string]any{"healthy": healthy, "last_checked_at": now, "updated_at": now}
			if healthy {
				updates["failure_count"] = 0
				updates["cooldown_until"] = nil
			}
			_ = s.db.Model(&ProviderResource{}).Where("id = ?", resource.ID).Updates(updates).Error
		}
	case "model":
		modelName := strings.TrimSpace(firstStringField(fields, "model", "model_name"))
		result.ModelName = modelName
		result.TargetID = modelName
		var routes int64
		if modelName == "" {
			result.Status = "failed"
			result.Message = "模型名为空"
		} else if err := s.db.Model(&ModelRoute{}).Where("model_name = ? AND status = ?", modelName, StatusActive).Count(&routes).Error; err != nil {
			return MonitorRunResult{}, err
		} else if routes == 0 {
			result.Status = "failed"
			result.Message = "没有可用模型路由"
		} else {
			result.Status = "ok"
			result.Message = fmt.Sprintf("模型路由可用，候选路由 %d 条", routes)
		}
	default:
		result.Status = "failed"
		result.Message = "不支持的监控目标"
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	if result.Message == "" {
		result.Message = "监控执行完成"
	}
	fields["target_type"] = result.TargetType
	fields["last_status"] = result.Status
	fields["last_result"] = result.Status
	fields["last_message"] = result.Message
	fields["last_checked_at"] = result.CheckedAt.Format(time.RFC3339)
	fields["latency_ms"] = result.LatencyMS
	fields["provider_id"] = result.ProviderID
	fields["provider_resource_id"] = result.ResourceID
	fields["model"] = result.ModelName
	monitor.Fields = fields
	monitor.UpdatedAt = time.Now().UTC()
	if err := s.db.Save(&monitor).Error; err != nil {
		return MonitorRunResult{}, err
	}
	if result.Status != "ok" {
		alert := AlertEvent{
			ID:         NewID("alt"),
			ScopeType:  "monitor",
			ScopeID:    monitor.ID,
			Severity:   "warning",
			Code:       "monitor_check_failed",
			Message:    result.Message,
			ResourceID: result.TargetID,
			CreatedAt:  time.Now().UTC(),
		}
		if err := s.db.Create(&alert).Error; err != nil {
			return MonitorRunResult{}, err
		}
		result.AlertID = alert.ID
	}
	return result, nil
}

func (s *GormStore) CreateApprovalRequest(request ApprovalRequest) ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	if request.ID == "" {
		request.ID = NewID("apr")
	}
	if request.Status == "" {
		request.Status = "pending"
	}
	if request.CreatedAt.IsZero() {
		request.CreatedAt = time.Now().UTC()
	}
	_ = s.db.Create(&request).Error
	return request
}

func (s *GormStore) ListApprovalRequests() []ApprovalRequest {
	var items []ApprovalRequest
	if err := s.db.Order("created_at desc").Limit(500).Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListApprovalRequests failed: %v", err)
	}
	return items
}

func (s *GormStore) GetApprovalRequest(id string) (ApprovalRequest, error) {
	var item ApprovalRequest
	if err := s.db.First(&item, "id = ?", id).Error; err != nil {
		return ApprovalRequest{}, notFound(err, "approval_not_found", "Approval request not found")
	}
	return item, nil
}

func (s *GormStore) UpdateApprovalRequestStatus(id string, status string, decidedBy string, reason string) (ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var item ApprovalRequest
	if err := s.db.First(&item, "id = ?", id).Error; err != nil {
		return ApprovalRequest{}, notFound(err, "approval_not_found", "Approval request not found")
	}
	if item.Status != "pending" {
		return ApprovalRequest{}, NewHTTPError(http.StatusConflict, "approval_already_decided", "Approval request has already been decided")
	}
	now := time.Now().UTC()
	item.Status = status
	item.Reason = reason
	item.DecidedAt = &now
	item.DecidedBy = decidedBy
	if err := s.db.Save(&item).Error; err != nil {
		return ApprovalRequest{}, err
	}
	return item, nil
}

func (s *GormStore) CreateAdminUser(user AdminUser, password string) (AdminUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var created AdminUser
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTeamsForMutation(tx, user.TeamID, user.TeamIDs); err != nil {
			return err
		}
		var err error
		created, err = createAdminUser(tx, user, password)
		return err
	})
	return created, err
}

func (s *GormStore) ListAdminUsers() []AdminUser {
	var items []AdminUser
	if err := s.db.Order("created_at asc").Find(&items).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListAdminUsers failed: %v", err)
	}
	for i := range items {
		items[i] = publicAdminUser(items[i])
	}
	return items
}

func (s *GormStore) UpdateAdminUser(id string, patch AdminUser, password string) (AdminUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var updated AdminUser
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := lockUserTeamsForMutation(tx, patch.TeamID, patch.TeamIDs); err != nil {
			return err
		}
		var err error
		updated, err = updateAdminUser(tx, id, patch, password)
		return err
	})
	return updated, err
}

func updateAdminUser(db *gorm.DB, id string, patch AdminUser, password string) (AdminUser, error) {
	var user AdminUser
	if err := db.First(&user, "id = ?", id).Error; err != nil {
		return AdminUser{}, notFound(err, "admin_user_not_found", "Admin user not found")
	}
	wasActivePlatformAdmin := activePlatformAdmin(user)
	if patch.Username != "" {
		var count int64
		if err := db.Model(&AdminUser{}).Where("id <> ? AND username = ?", id, patch.Username).Count(&count).Error; err != nil {
			return AdminUser{}, err
		}
		if count > 0 {
			return AdminUser{}, NewHTTPError(409, "admin_user_conflict", "Username already exists")
		}
		user.Username = patch.Username
	}
	if patch.Name != "" {
		user.Name = patch.Name
	}
	if patch.Email != "" {
		var count int64
		if err := db.Model(&AdminUser{}).Where("id <> ? AND email = ?", id, patch.Email).Count(&count).Error; err != nil {
			return AdminUser{}, err
		}
		if count > 0 {
			return AdminUser{}, NewHTTPError(409, "admin_user_conflict", "Email already exists")
		}
		user.Email = patch.Email
	}
	if patch.Role != "" {
		user.Role = patch.Role
	}
	user.TeamID = patch.TeamID
	user.TeamIDs = normalizedTeamIDs(patch.TeamID, patch.TeamIDs)
	if patch.Status != "" {
		user.Status = patch.Status
	}
	if password != "" {
		passwordHash, err := hashPassword(password)
		if err != nil {
			return AdminUser{}, err
		}
		user.PasswordHash = passwordHash
	}
	if wasActivePlatformAdmin && !activePlatformAdmin(user) {
		if err := ensureAnotherActivePlatformAdmin(db, user.ID); err != nil {
			return AdminUser{}, err
		}
	}
	user.UpdatedAt = time.Now().UTC()
	if err := db.Save(&user).Error; err != nil {
		return AdminUser{}, err
	}
	return publicAdminUser(user), nil
}

func (s *GormStore) DeleteAdminUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var user AdminUser
		if err := tx.First(&user, "id = ?", id).Error; err != nil {
			return notFound(err, "admin_user_not_found", "Admin user not found")
		}
		if activePlatformAdmin(user) {
			if err := ensureAnotherActivePlatformAdmin(tx, user.ID); err != nil {
				return err
			}
		}
		if err := tx.Where("user_id = ?", id).Delete(&AdminSession{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&AdminPasswordResetToken{}).Error; err != nil {
			return err
		}
		return tx.Delete(&user).Error
	})
}

func activePlatformAdmin(user AdminUser) bool {
	role := strings.ToLower(strings.TrimSpace(user.Role))
	return user.Status == StatusActive && (role == "admin" || role == "system_admin")
}

func ensureAnotherActivePlatformAdmin(db *gorm.DB, excludedUserID string) error {
	var count int64
	if err := db.Model(&AdminUser{}).
		Where("id <> ? AND status = ? AND lower(role) IN ?", excludedUserID, StatusActive, []string{"admin", "system_admin"}).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return NewHTTPError(400, "last_admin_user", "Cannot remove, disable, or demote the last active platform administrator")
	}
	return nil
}

func usageAttributionUserID(key APIKey, project Project) string {
	if ownerUserID := strings.TrimSpace(key.OwnerUserID); ownerUserID != "" {
		return ownerUserID
	}
	if key.Metadata != nil {
		if creatorUserID := strings.TrimSpace(key.Metadata["created_by"]); creatorUserID != "" {
			return creatorUserID
		}
	}
	return strings.TrimSpace(project.OwnerUserID)
}

func (s *GormStore) CreateAdminPasswordResetToken(userID string, createdBy string, ttl time.Duration) (string, AdminPasswordResetToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var user AdminUser
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		return "", AdminPasswordResetToken{}, notFound(err, "admin_user_not_found", "Admin user not found")
	}
	now := time.Now().UTC()
	plainToken := NewID("rst") + NewID("tok")
	item := AdminPasswordResetToken{
		ID:        NewID("rtk"),
		UserID:    userID,
		TokenHash: HashSecret(plainToken),
		ExpiresAt: now.Add(ttl),
		CreatedBy: createdBy,
		CreatedAt: now,
	}
	if err := s.db.Create(&item).Error; err != nil {
		return "", AdminPasswordResetToken{}, err
	}
	return plainToken, item, nil
}

func (s *GormStore) ResetAdminUserPassword(token string, password string) (AdminUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	password = strings.TrimSpace(password)
	if strings.TrimSpace(token) == "" || password == "" {
		return AdminUser{}, NewHTTPError(400, "invalid_reset_request", "token and password are required")
	}
	if len(password) < 8 {
		return AdminUser{}, NewHTTPError(400, "weak_password", "Password must be at least 8 characters")
	}
	var item AdminPasswordResetToken
	if err := s.db.First(&item, "token_hash = ?", HashSecret(token)).Error; err != nil {
		return AdminUser{}, NewHTTPError(400, "invalid_reset_token", "Reset token is invalid or expired")
	}
	if item.UsedAt != nil || time.Now().UTC().After(item.ExpiresAt) {
		return AdminUser{}, NewHTTPError(400, "invalid_reset_token", "Reset token is invalid or expired")
	}
	var user AdminUser
	if err := s.db.First(&user, "id = ?", item.UserID).Error; err != nil {
		return AdminUser{}, notFound(err, "admin_user_not_found", "Admin user not found")
	}
	now := time.Now().UTC()
	passwordHash, err := hashPassword(password)
	if err != nil {
		return AdminUser{}, err
	}
	user.PasswordHash = passwordHash
	user.UpdatedAt = now
	item.UsedAt = &now
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		if err := tx.Save(&item).Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", user.ID).Delete(&AdminSession{}).Error
	}); err != nil {
		return AdminUser{}, err
	}
	return publicAdminUser(user), nil
}

func (s *GormStore) AuthenticateAdminUser(identity string, password string, ttl time.Duration) (AdminUser, AdminSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	identity = strings.ToLower(strings.TrimSpace(identity))
	var user AdminUser
	if err := s.db.Where("LOWER(email) = ? OR LOWER(username) = ?", identity, identity).First(&user).Error; err != nil {
		return AdminUser{}, AdminSession{}, NewHTTPError(401, "invalid_credentials", "Invalid username or password")
	}
	if user.Status != StatusActive {
		return AdminUser{}, AdminSession{}, NewHTTPError(403, "admin_user_disabled", "Admin user is disabled")
	}
	validPassword, needsPasswordUpgrade := verifyPassword(user.PasswordHash, password)
	if !validPassword {
		return AdminUser{}, AdminSession{}, NewHTTPError(401, "invalid_credentials", "Invalid username or password")
	}
	if needsPasswordUpgrade {
		upgradedHash, err := hashPasswordForUpgrade(password)
		if err != nil {
			return AdminUser{}, AdminSession{}, err
		}
		user.PasswordHash = upgradedHash
	}
	now := time.Now().UTC()
	session := AdminSession{
		Token:     GenerateAdminSessionToken(),
		UserID:    user.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	user.LastLoginAt = &now
	user.UpdatedAt = now
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return tx.Create(&session).Error
	})
	if err != nil {
		return AdminUser{}, AdminSession{}, err
	}
	return publicAdminUser(user), session, nil
}

func (s *GormStore) CreateAdminSession(userID string, ttl time.Duration) (AdminUser, AdminSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var user AdminUser
	if err := s.db.First(&user, "id = ?", userID).Error; err != nil {
		return AdminUser{}, AdminSession{}, notFound(err, "admin_user_not_found", "Admin user not found")
	}
	if user.Status != StatusActive {
		return AdminUser{}, AdminSession{}, NewHTTPError(403, "admin_user_disabled", "Admin user is disabled")
	}
	now := time.Now().UTC()
	session := AdminSession{
		Token:     GenerateAdminSessionToken(),
		UserID:    user.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	user.LastLoginAt = &now
	user.UpdatedAt = now
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return tx.Create(&session).Error
	})
	if err != nil {
		return AdminUser{}, AdminSession{}, err
	}
	return publicAdminUser(user), session, nil
}

func (s *GormStore) ValidateAdminSession(token string) (AdminUser, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var session AdminSession
	if err := s.db.First(&session, "token = ?", token).Error; err != nil {
		return AdminUser{}, false
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		_ = s.db.Delete(&session).Error
		return AdminUser{}, false
	}
	var user AdminUser
	if err := s.db.First(&user, "id = ? AND status = ?", session.UserID, StatusActive).Error; err != nil {
		return AdminUser{}, false
	}
	return publicAdminUser(user), true
}

func (s *GormStore) RevokeAdminSession(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.db.Delete(&AdminSession{}, "token = ?", token).Error
}

func (s *GormStore) CreateSQLiteBackup(createdBy string, expireDays int) (SQLiteBackupRecord, error) {
	if s.IsPostgreSQL() {
		return s.CreatePostgreSQLBackup(createdBy, expireDays)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	backupID := NewID("bak")
	fileName := backupID + ".sqlite3"
	filePath := filepath.Join(defaultString(s.backupDir, "data/backups"), fileName)
	record := SQLiteBackupRecord{
		ID:        backupID,
		Name:      "SQLite Backup " + now.Format("2006-01-02 15:04:05"),
		FileName:  fileName,
		FilePath:  filePath,
		Status:    "creating",
		Trigger:   "manual",
		CreatedBy: createdBy,
		CreatedAt: now,
	}
	if expireDays > 0 {
		expiresAt := now.AddDate(0, 0, expireDays)
		record.ExpiresAt = &expiresAt
	}
	if err := s.db.Create(&record).Error; err != nil {
		return SQLiteBackupRecord{}, err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	if err := s.copySQLiteDatabase(filePath, false); err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	checksum, err := fileSHA256(filePath)
	if err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	record.Status = "ready"
	record.SizeBytes = info.Size()
	record.ChecksumSHA256 = checksum
	record.Error = ""
	if err := s.db.Save(&record).Error; err != nil {
		return SQLiteBackupRecord{}, err
	}
	return record, nil
}

func (s *GormStore) ListSQLiteBackups() []SQLiteBackupRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	var records []SQLiteBackupRecord
	if err := s.db.Order("created_at desc").Find(&records).Error; err != nil {
		log.Printf("[tokenhub] ERROR: ListSQLiteBackups failed: %v", err)
	}
	return records
}

func (s *GormStore) GetSQLiteBackup(id string) (SQLiteBackupRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getSQLiteBackupLocked(id)
}

func (s *GormStore) RestoreSQLiteBackup(id string, restoredBy string) (SQLiteBackupRecord, error) {
	if s.IsPostgreSQL() {
		return s.RestorePostgreSQLBackup(id, restoredBy)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.getSQLiteBackupLocked(id)
	if err != nil {
		return SQLiteBackupRecord{}, err
	}
	if record.Status != "ready" && record.Status != "restored" {
		return SQLiteBackupRecord{}, NewHTTPError(409, "backup_not_ready", "Backup is not ready to restore")
	}
	if _, err := os.Stat(record.FilePath); err != nil {
		return SQLiteBackupRecord{}, NewHTTPError(404, "backup_file_missing", "Backup file is missing")
	}
	if record.ChecksumSHA256 != "" {
		checksum, err := fileSHA256(record.FilePath)
		if err != nil {
			return SQLiteBackupRecord{}, err
		}
		if !strings.EqualFold(checksum, record.ChecksumSHA256) {
			return SQLiteBackupRecord{}, NewHTTPError(409, "backup_checksum_mismatch", "Backup checksum does not match")
		}
	}
	if err := s.copySQLiteDatabase(record.FilePath, true); err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		_ = s.db.Save(&record).Error
		return record, err
	}
	now := time.Now().UTC()
	record.Status = "restored"
	record.RestoredBy = restoredBy
	record.RestoredAt = &now
	record.Error = ""
	if err := s.db.Save(&record).Error; err != nil {
		return SQLiteBackupRecord{}, err
	}
	return record, nil
}

func (s *GormStore) DeleteSQLiteBackup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.getSQLiteBackupLocked(id)
	if err != nil {
		return err
	}
	if record.FilePath != "" {
		if err := os.Remove(record.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := s.db.Delete(&SQLiteBackupRecord{}, "id = ?", id).Error; err != nil {
		return err
	}
	return nil
}

func (s *GormStore) getSQLiteBackupLocked(id string) (SQLiteBackupRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SQLiteBackupRecord{}, NewHTTPError(404, "backup_not_found", "Backup not found")
	}
	var record SQLiteBackupRecord
	if err := s.db.First(&record, "id = ?", id).Error; err != nil {
		return SQLiteBackupRecord{}, notFound(err, "backup_not_found", "Backup not found")
	}
	return record, nil
}

func (s *GormStore) copySQLiteDatabase(path string, restore bool) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	srcDB := sqlDB
	destDB := sqlDB
	var external *sql.DB
	if restore {
		external, err = sql.Open("sqlite3", path)
		srcDB = external
	} else {
		_ = os.Remove(path)
		external, err = sql.Open("sqlite3", path)
		destDB = external
	}
	if err != nil {
		return err
	}
	defer external.Close()
	destConn, err := destDB.Conn(context.Background())
	if err != nil {
		return err
	}
	defer destConn.Close()
	srcConn, err := srcDB.Conn(context.Background())
	if err != nil {
		return err
	}
	defer srcConn.Close()
	return withSQLiteConn(destConn, func(dest *sqlite3.SQLiteConn) error {
		return withSQLiteConn(srcConn, func(src *sqlite3.SQLiteConn) error {
			backup, err := dest.Backup("main", src, "main")
			if err != nil {
				return err
			}
			// Finish only releases the backup handle; the copy's success is decided
			// by Step above, and this runs in a defer where nothing could act on a
			// failure anyway.
			defer backup.Finish() //nolint:errcheck // release-only, result not actionable
			for {
				done, err := backup.Step(64)
				if err != nil {
					return err
				}
				if done {
					return nil
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	})
}

func withSQLiteConn(conn *sql.Conn, fn func(*sqlite3.SQLiteConn) error) error {
	var err error
	rawErr := conn.Raw(func(driverConn any) error {
		sqliteConn, ok := driverConn.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("sqlite connection expected, got %T", driverConn)
		}
		err = fn(sqliteConn)
		return err
	})
	if rawErr != nil {
		return rawErr
	}
	return err
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *GormStore) AccessibleModels(key APIKey) []Model {
	s.mu.Lock()
	defer s.mu.Unlock()

	var privateKey APIKey
	if err := s.db.First(&privateKey, "id = ?", key.ID).Error; err != nil {
		return nil
	}
	hydrateAPIKey(&privateKey)
	var routes []ModelRoute
	if err := s.db.Where("status = ?", StatusActive).Find(&routes).Error; err != nil {
		return nil
	}
	publishedModelNames := make([]string, 0, len(routes))
	seenModelNames := map[string]bool{}
	for _, route := range routes {
		if !routeMatchesProject(route, privateKey.ProjectID) || seenModelNames[route.ModelName] {
			continue
		}
		seenModelNames[route.ModelName] = true
		publishedModelNames = append(publishedModelNames, route.ModelName)
	}
	var models []Model
	if err := s.db.Where("status = ?", StatusActive).
		Where("name IN ? OR name = ?", publishedModelNames, codexImageModelName).
		Order("name asc").
		Find(&models).Error; err != nil {
		return nil
	}
	codexImageAvailable := s.codexImageGenerationAvailableLocked()
	items := make([]Model, 0, len(models))
	for _, model := range models {
		if model.Name == codexImageModelName && !codexImageAvailable {
			continue
		}
		if len(privateKey.AllowedModels) > 0 && !privateKey.AllowedModels[model.Name] {
			continue
		}
		items = append(items, model)
	}
	return items
}

func (s *GormStore) codexImageGenerationAvailableLocked() bool {
	var providers []Provider
	if err := s.db.Where("type = ? AND status = ? AND healthy = ?", ProviderOpenAICodex, StatusActive, true).
		Find(&providers).Error; err != nil || len(providers) == 0 {
		return false
	}
	providerIDs := make([]string, 0, len(providers))
	for _, provider := range providers {
		providerIDs = append(providerIDs, provider.ID)
	}
	var resources []ProviderResource
	if err := s.db.Where("provider_id IN ? AND status = ? AND healthy = ?", providerIDs, StatusActive, true).
		Find(&resources).Error; err != nil {
		return false
	}
	for _, resource := range resources {
		switch strings.TrimSpace(resource.Options[codexImageCapabilityOption]) {
		case codexImageCapabilitySupported:
			return true
		case codexImageCapabilityUnsupported:
			checkedAt, err := time.Parse(time.RFC3339Nano, resource.Options[codexImageCapabilityCheckedAtOption])
			if err == nil && s.imageCapabilityRetry > 0 && !time.Now().Before(checkedAt.Add(s.imageCapabilityRetry)) {
				return true
			}
		}
	}
	return false
}

func (s *GormStore) quotaBucketForUpdate(tx *gorm.DB, keyID, scope, bucket string) (QuotaBucket, error) {
	seed := QuotaBucket{KeyID: keyID, Scope: scope, Bucket: bucket}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return QuotaBucket{}, err
	}
	query := tx
	if s.dbDriver == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var item QuotaBucket
	if err := query.First(&item, "key_id = ? AND scope = ? AND bucket = ?", keyID, scope, bucket).Error; err != nil {
		return QuotaBucket{}, err
	}
	return item, nil
}

func priceUsage(model Model, usage Usage) Usage {
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	usage.CachedInputTokens = minInt64(maxInt64(usage.CachedInputTokens, 0), usage.PromptTokens)
	if usage.CostUSD == 0 {
		if model.Modality == "embedding" && model.EmbeddingPriceUSDPer1M > 0 {
			usage.CostUSD = float64(usage.TotalTokens) * model.EmbeddingPriceUSDPer1M / 1_000_000
		} else {
			uncachedInputTokens := usage.PromptTokens - usage.CachedInputTokens
			cacheReadPrice := effectiveCacheReadPriceUSDPer1M(model)
			usage.CostUSD = float64(uncachedInputTokens)*model.InputPriceUSDPer1M/1_000_000 +
				float64(usage.CachedInputTokens)*cacheReadPrice/1_000_000 +
				float64(usage.CompletionTokens)*model.OutputPriceUSDPer1M/1_000_000
		}
	}
	return usage
}

const (
	defaultCacheReadEstimateRatio  = 0.10
	deepSeekCacheReadEstimateRatio = 0.02
	deepSeekV4ProCacheReadRatio    = 1.0 / 120
)

func effectiveCacheReadPriceUSDPer1M(model Model) float64 {
	if model.Modality == "embedding" {
		return 0
	}
	if model.CacheReadPriceUSDPer1M > 0 {
		return model.CacheReadPriceUSDPer1M
	}
	for _, key := range []string{"cached_input_price_usd_per_1m", "cache_read_price_usd_per_1m", "cached_read_price_usd_per_1m"} {
		if value, err := strconv.ParseFloat(strings.TrimSpace(model.Metadata[key]), 64); err == nil && value > 0 {
			return value
		}
	}
	if model.InputPriceUSDPer1M <= 0 {
		return 0
	}
	ratio := defaultCacheReadEstimateRatio
	category := standardModelCategory(firstNonEmpty(model.Category, inferModelCategory(model.Name, model.Family)))
	if category == "deepseek" {
		ratio = deepSeekCacheReadEstimateRatio
		if strings.Contains(strings.ToLower(model.Name+" "+model.Family), "v4-pro") {
			ratio = deepSeekV4ProCacheReadRatio
		}
	}
	return model.InputPriceUSDPer1M * ratio
}

func minInt64(left int64, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func maxInt64(left int64, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func raiseQuotaAlerts(tx *gorm.DB, key APIKey, dayCounter, monthCounter *QuotaCounter) error {
	checks := []struct {
		limit     float64
		current   float64
		code      string
		message   string
		scopeType string
	}{
		{float64(key.Limits.DailyTokens), float64(dayCounter.TotalTokens), "daily_tokens_near_limit", "Daily token quota is near or above limit", "api_key"},
		{float64(key.Limits.MonthlyTokens), float64(monthCounter.TotalTokens), "monthly_tokens_near_limit", "Monthly token quota is near or above limit", "api_key"},
		{key.Limits.DailyCostUSD, dayCounter.CostUSD, "daily_cost_near_limit", "Daily cost quota is near or above limit", "api_key"},
		{key.Limits.MonthlyCostUSD, monthCounter.CostUSD, "monthly_cost_near_limit", "Monthly cost quota is near or above limit", "api_key"},
	}
	for _, check := range checks {
		if check.limit <= 0 || check.current < check.limit {
			continue
		}
		if err := tx.Create(&AlertEvent{
			ID:         NewID("alt"),
			ScopeType:  check.scopeType,
			ScopeID:    key.ID,
			Severity:   "warning",
			Code:       check.code,
			Message:    check.message,
			ResourceID: key.ProjectID,
			CreatedAt:  time.Now().UTC(),
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func quotaPolicyLimits(tx *gorm.DB, project Project, key APIKey) QuotaLimits {
	var resources []AdminResource
	if err := tx.Where("kind = ? AND status = ?", "quota-policies", StatusActive).Find(&resources).Error; err != nil {
		log.Printf("[tokenhub] ERROR: quotaPolicyLimits failed: %v", err)
	}
	var limits QuotaLimits
	for _, resource := range resources {
		scope := strings.ToLower(strings.TrimSpace(stringField(resource.Fields, "scope")))
		if scope == "" {
			scope = strings.ToLower(strings.TrimSpace(stringField(resource.Fields, "scope_type")))
		}
		scopeID := strings.TrimSpace(stringField(resource.Fields, "scope_id"))
		if !quotaPolicyApplies(scope, scopeID, project, key) {
			continue
		}
		limits = mergeQuotaLimits(limits, QuotaLimits{
			DailyRequests:   int64Field(resource.Fields, "daily_requests"),
			MonthlyRequests: int64Field(resource.Fields, "monthly_requests"),
			DailyTokens:     int64Field(resource.Fields, "daily_tokens"),
			MonthlyTokens:   int64Field(resource.Fields, "monthly_tokens"),
			DailyCostUSD:    float64Field(resource.Fields, "daily_cost_usd"),
			MonthlyCostUSD:  float64Field(resource.Fields, "monthly_cost_usd"),
			MaxConcurrency:  int64Field(resource.Fields, "max_concurrency"),
		})
	}
	return limits
}

func quotaPolicyApplies(scope string, scopeID string, project Project, key APIKey) bool {
	if scope == "" || scope == "global" || scope == "organization" {
		return scopeID == "" || scopeID == "default" || scopeID == "global"
	}
	switch scope {
	case "project":
		return scopeID == "" || scopeID == project.ID
	case "api_key", "key":
		return scopeID == "" || scopeID == key.ID
	case "team":
		return scopeID == "" || scopeID == project.TeamID
	default:
		return false
	}
}

func (s *GormStore) checkRuntimeBudget(tx *gorm.DB, project Project) error {
	now := time.Now().UTC()
	period := now.Format("2006-01")
	costCenter := s.costCenterForProjectWithDB(tx, project)
	var budgets []AdminResource
	if err := tx.Where("kind = ? AND status = ?", "budgets", StatusActive).Find(&budgets).Error; err != nil {
		return err
	}
	for _, budget := range budgets {
		if !budgetEnforced(budget) {
			continue
		}
		if !budgetAppliesToProject(budget, project, costCenter) {
			continue
		}
		if budgetPeriod := strings.TrimSpace(stringField(budget.Fields, "period_ref")); budgetPeriod != "" && normalizeBillingPeriod(budgetPeriod, now) != period {
			continue
		}
		amount := float64Field(budget.Fields, "amount_usd")
		if amount <= 0 {
			continue
		}
		used, err := s.runtimeBudgetUsed(tx, budget, period)
		if err != nil {
			return err
		}
		if used >= amount {
			return ErrBudgetExceeded
		}
	}
	return nil
}

func (s *GormStore) runtimeBudgetUsed(tx *gorm.DB, budget AdminResource, period string) (float64, error) {
	scope := strings.ToLower(strings.TrimSpace(stringField(budget.Fields, "scope")))
	scopeID := budgetScopeID(budget, scope)
	start := periodStart(period)
	end := periodEnd(period)
	switch scope {
	case "project":
		var total sql.NullFloat64
		if err := tx.Model(&UsageRecord{}).
			Where("project_id = ? AND created_at >= ? AND created_at < ?", scopeID, start, end).
			Select("sum(cost_usd)").Scan(&total).Error; err != nil {
			return 0, err
		}
		return total.Float64, nil
	case "global", "organization":
		var total sql.NullFloat64
		if err := tx.Model(&UsageRecord{}).
			Where("created_at >= ? AND created_at < ?", start, end).
			Select("sum(cost_usd)").Scan(&total).Error; err != nil {
			return 0, err
		}
		return total.Float64, nil
	case "team", "cost_center", "cost-center":
		var records []UsageRecord
		if err := tx.Where("created_at >= ? AND created_at < ?", start, end).Find(&records).Error; err != nil {
			return 0, err
		}
		projectCache := map[string]Project{}
		var total float64
		for _, record := range records {
			project, ok := projectCache[record.ProjectID]
			if !ok {
				if err := tx.First(&project, "id = ?", record.ProjectID).Error; err != nil {
					continue
				}
				projectCache[record.ProjectID] = project
			}
			if scope == "team" && project.TeamID == scopeID {
				total += record.CostUSD
				continue
			}
			if (scope == "cost_center" || scope == "cost-center") && s.costCenterForProjectWithDB(tx, project) == scopeID {
				total += record.CostUSD
			}
		}
		return total, nil
	default:
		return 0, nil
	}
}

func budgetScopeID(budget AdminResource, scope string) string {
	scopeID := strings.TrimSpace(stringField(budget.Fields, "scope_id"))
	switch scope {
	case "project":
		if scopeID == "" {
			scopeID = strings.TrimSpace(stringField(budget.Fields, "project_id"))
		}
	case "team":
		if scopeID == "" {
			scopeID = strings.TrimSpace(stringField(budget.Fields, "team_id"))
		}
	case "cost_center", "cost-center":
		if scopeID == "" {
			scopeID = strings.TrimSpace(stringField(budget.Fields, "cost_center"))
		}
	}
	return scopeID
}

func budgetEnforced(budget AdminResource) bool {
	mode := strings.ToLower(strings.TrimSpace(stringField(budget.Fields, "enforcement")))
	if mode == "warn" || mode == "monitor" || mode == "off" || mode == "disabled" {
		return false
	}
	return true
}

func budgetAppliesToProject(budget AdminResource, project Project, costCenter string) bool {
	scope := strings.ToLower(strings.TrimSpace(stringField(budget.Fields, "scope")))
	scopeID := budgetScopeID(budget, scope)
	switch scope {
	case "project":
		return scopeID != "" && scopeID == project.ID
	case "team":
		return scopeID != "" && scopeID == project.TeamID
	case "cost_center", "cost-center":
		return scopeID != "" && scopeID == costCenter
	case "global", "organization":
		return true
	default:
		return false
	}
}

func mergeQuotaLimits(base QuotaLimits, override QuotaLimits) QuotaLimits {
	return QuotaLimits{
		DailyRequests:   strictInt64(base.DailyRequests, override.DailyRequests),
		MonthlyRequests: strictInt64(base.MonthlyRequests, override.MonthlyRequests),
		DailyTokens:     strictInt64(base.DailyTokens, override.DailyTokens),
		MonthlyTokens:   strictInt64(base.MonthlyTokens, override.MonthlyTokens),
		DailyCostUSD:    strictFloat64(base.DailyCostUSD, override.DailyCostUSD),
		MonthlyCostUSD:  strictFloat64(base.MonthlyCostUSD, override.MonthlyCostUSD),
		MaxConcurrency:  strictInt64(base.MaxConcurrency, override.MaxConcurrency),
	}
}

func strictInt64(a int64, b int64) int64 {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func strictFloat64(a float64, b float64) float64 {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func int64Field(fields map[string]any, key string) int64 {
	if fields == nil {
		return 0
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		var parsed int64
		for _, ch := range strings.TrimSpace(typed) {
			if ch < '0' || ch > '9' {
				return 0
			}
			parsed = parsed*10 + int64(ch-'0')
		}
		return parsed
	default:
		return 0
	}
}

func float64Field(fields map[string]any, key string) float64 {
	if fields == nil {
		return 0
	}
	value, ok := fields[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		var parsed float64
		var scale float64
		for _, ch := range strings.TrimSpace(typed) {
			if ch == '.' && scale == 0 {
				scale = 1
				continue
			}
			if ch < '0' || ch > '9' {
				return 0
			}
			if scale > 0 {
				scale *= 10
				parsed += float64(ch-'0') / scale
			} else {
				parsed = parsed*10 + float64(ch-'0')
			}
		}
		return parsed
	default:
		return 0
	}
}

func (s *GormStore) costCenterForProject(project Project) string {
	return s.costCenterForProjectWithDB(s.db, project)
}

func (s *GormStore) costCenterForProjectWithDB(db *gorm.DB, project Project) string {
	if costCenter := strings.TrimSpace(project.CostCenter); costCenter != "" {
		return costCenter
	}
	if strings.TrimSpace(project.TeamID) != "" {
		var team AdminResource
		if err := db.First(&team, "kind = ? AND id = ?", "teams", project.TeamID).Error; err == nil {
			if costCenter := strings.TrimSpace(stringField(team.Fields, "cost_center")); costCenter != "" {
				return costCenter
			}
		}
	}
	if strings.TrimSpace(project.DefaultQuotaRef) != "" {
		var quota AdminResource
		if err := db.First(&quota, "kind = ? AND id = ?", "quota-policies", project.DefaultQuotaRef).Error; err == nil {
			if costCenter := strings.TrimSpace(stringField(quota.Fields, "cost_center")); costCenter != "" {
				return costCenter
			}
		}
	}
	if strings.TrimSpace(project.TeamID) != "" {
		return project.TeamID
	}
	if strings.TrimSpace(project.ID) != "" {
		return "project:" + project.ID
	}
	return "unknown"
}

func normalizeBillingPeriod(period string, now time.Time) string {
	period = strings.TrimSpace(period)
	if period == "" {
		return now.UTC().Format("2006-01")
	}
	if len(period) >= 7 {
		return period[:7]
	}
	return now.UTC().Format("2006-01")
}

func periodStart(period string) time.Time {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		t, _ = time.Parse("2006-01", time.Now().UTC().Format("2006-01"))
	}
	return t.UTC()
}

func periodEnd(period string) time.Time {
	return periodStart(period).AddDate(0, 1, 0)
}

func defaultInvoiceNote(period string, costCenter string, amount float64) string {
	return fmt.Sprintf("TokenHub %s AI 用量内部结算，成本中心 %s，金额 USD %.4f。", period, costCenter, roundMoney(amount))
}

func roundMoney(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func sumFloatMap(items map[string]float64) float64 {
	var total float64
	for _, value := range items {
		total += value
	}
	return total
}

func deleteGeneratedResourcesByPeriod(tx *gorm.DB, kind string, period string) error {
	var items []AdminResource
	if err := tx.Where("kind = ?", kind).Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		if stringField(item.Fields, "period") != period {
			continue
		}
		if generated := strings.TrimSpace(stringField(item.Fields, "generated_by")); generated != "tokenhub" {
			continue
		}
		if err := tx.Delete(&item).Error; err != nil {
			return err
		}
	}
	return nil
}

func updateBudgetsFromUsage(tx *gorm.DB, period string, costCenterTotals map[string]float64, projectTotals map[string]float64, teamTotals map[string]float64) error {
	var budgets []AdminResource
	if err := tx.Where("kind = ? AND status = ?", "budgets", StatusActive).Find(&budgets).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, budget := range budgets {
		scope := strings.ToLower(strings.TrimSpace(stringField(budget.Fields, "scope")))
		scopeID := strings.TrimSpace(stringField(budget.Fields, "scope_id"))
		switch scope {
		case "cost_center", "cost-center":
			if scopeID == "" {
				scopeID = strings.TrimSpace(stringField(budget.Fields, "cost_center"))
			}
		case "project":
			if scopeID == "" {
				scopeID = strings.TrimSpace(stringField(budget.Fields, "project_id"))
			}
		case "team":
			if scopeID == "" {
				scopeID = strings.TrimSpace(stringField(budget.Fields, "team_id"))
			}
		default:
			continue
		}
		if scopeID == "" {
			continue
		}
		budgetPeriod := normalizeBillingPeriod(stringField(budget.Fields, "period_ref"), now)
		if budgetPeriod != period && strings.TrimSpace(stringField(budget.Fields, "period_ref")) != "" {
			continue
		}
		amount := float64Field(budget.Fields, "amount_usd")
		used := budgetUsedByScope(scope, scopeID, costCenterTotals, projectTotals, teamTotals)
		budget.Fields["used_usd"] = roundMoney(used)
		budget.Fields["remaining_usd"] = roundMoney(amount - used)
		budget.Fields["usage_percent"] = float64(0)
		if amount > 0 {
			budget.Fields["usage_percent"] = roundMoney(used / amount * 100)
		}
		budget.Fields["last_calculated_period"] = period
		budget.UpdatedAt = now
		if err := tx.Save(&budget).Error; err != nil {
			return err
		}
		warnPercent := float64Field(budget.Fields, "warn_percent")
		if warnPercent <= 0 {
			warnPercent = 80
		}
		if amount > 0 && used/amount*100 >= warnPercent {
			if err := tx.Create(&AlertEvent{
				ID:         NewID("alt"),
				ScopeType:  "budget",
				ScopeID:    budget.ID,
				Severity:   "warning",
				Code:       "budget_warn_threshold",
				Message:    fmt.Sprintf("Budget %s reached %.2f%% for %s", budget.Name, used/amount*100, period),
				ResourceID: scopeID,
				CreatedAt:  now,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func budgetUsedByScope(scope string, scopeID string, costCenterTotals map[string]float64, projectTotals map[string]float64, teamTotals map[string]float64) float64 {
	switch scope {
	case "cost_center", "cost-center":
		return costCenterTotals[scopeID]
	case "project":
		return projectTotals[scopeID]
	case "team":
		return teamTotals[scopeID]
	default:
		return 0
	}
}

func createAdminUser(db *gorm.DB, user AdminUser, password string) (AdminUser, error) {
	now := time.Now().UTC()
	if user.ID == "" {
		user.ID = NewID("usr")
	}
	if user.Username == "" {
		user.Username = user.Email
	}
	if user.Email == "" {
		return AdminUser{}, NewHTTPError(400, "invalid_admin_user", "email is required")
	}
	if user.Name == "" {
		user.Name = user.Username
	}
	if user.Role == "" {
		user.Role = "user"
	}
	if user.Status == "" {
		user.Status = StatusActive
	}
	user.TeamIDs = normalizedTeamIDs(user.TeamID, user.TeamIDs)
	if password == "" && user.PasswordHash == "" {
		return AdminUser{}, NewHTTPError(400, "invalid_admin_user", "password is required")
	}
	var count int64
	if err := db.Model(&AdminUser{}).
		Where("username = ? OR email = ?", user.Username, user.Email).
		Count(&count).Error; err != nil {
		return AdminUser{}, err
	}
	if count > 0 {
		return AdminUser{}, NewHTTPError(409, "admin_user_conflict", "Username or email already exists")
	}
	if user.PasswordHash == "" {
		passwordHash, err := hashPassword(password)
		if err != nil {
			return AdminUser{}, err
		}
		user.PasswordHash = passwordHash
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = now
	if err := db.Create(&user).Error; err != nil {
		return AdminUser{}, writeConflict(err, "admin_user_conflict", "Username or email already exists")
	}
	return publicAdminUser(user), nil
}

func publicKeys(keys []APIKey) []APIKey {
	items := make([]APIKey, 0, len(keys))
	for _, key := range keys {
		hydrateAPIKey(&key)
		items = append(items, publicKey(key))
	}
	return items
}

func hydrateAPIKey(key *APIKey) {
	key.AllowedModels = AllowedModelSet(key.Allowed)
}

func publicKey(key APIKey) APIKey {
	key.KeyHash = ""
	if key.Allowed == nil && key.AllowedModels != nil {
		key.Allowed = make([]string, 0, len(key.AllowedModels))
		for model := range key.AllowedModels {
			key.Allowed = append(key.Allowed, model)
		}
		sort.Strings(key.Allowed)
	}
	return key
}

func publicAdminUser(user AdminUser) AdminUser {
	user.PasswordHash = ""
	return user
}

func ipAllowed(clientIP string, allowlist []string) bool {
	clientIP = strings.TrimSpace(clientIP)
	if clientIP == "" {
		return false
	}
	parsedIP := net.ParseIP(clientIP)
	for _, item := range allowlist {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == "*" || item == clientIP {
			return true
		}
		if strings.Contains(item, "/") && parsedIP != nil {
			if _, network, err := net.ParseCIDR(item); err == nil && network.Contains(parsedIP) {
				return true
			}
		}
	}
	return false
}

func notFound(err error, code, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NewHTTPError(404, code, message)
	}
	return err
}

func writeConflict(err error, code, message string) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return NewHTTPError(409, code, message)
	}
	return err
}

func (s *GormStore) encryptSecret(secret string) (string, error) {
	if strings.TrimSpace(secret) == "" || strings.HasPrefix(secret, "enc:v1:") {
		return secret, nil
	}
	key := secretKeyBytes(s.secretKey)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("encrypt: init AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("encrypt: init GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("encrypt: generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(secret), nil)
	return "enc:v1:" + base64.RawURLEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

func (s *GormStore) decryptSecret(secret string) (string, error) {
	if !strings.HasPrefix(secret, "enc:v1:") {
		return secret, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, "enc:v1:"))
	if err != nil {
		return "", fmt.Errorf("decrypt: decode base64: %w", err)
	}
	key := secretKeyBytes(s.secretKey)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("decrypt: init AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("decrypt: init GCM: %w", err)
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("decrypt: ciphertext too short (%d bytes)", len(data))
	}
	nonce := data[:gcm.NonceSize()]
	ciphertext := data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: AEAD open: %w", err)
	}
	return string(plaintext), nil
}

func secretKeyBytes(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func defaultInt(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func validProviderResourceBulkAction(action string) bool {
	switch action {
	case "enable", "disable", "test", "clear_error", "reset_usage":
		return true
	default:
		return false
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func normalizeRouteProjectScope(scope string, projectIDs []string) (string, []string) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope != RouteProjectScopeInclude && scope != RouteProjectScopeExclude {
		return RouteProjectScopeAll, nil
	}
	projectIDs = uniqueStrings(projectIDs)
	sort.Strings(projectIDs)
	return scope, projectIDs
}

func cloneFields(fields map[string]any) map[string]any {
	cloned := map[string]any{}
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}

func inferMonitorTargetType(fields map[string]any) string {
	if strings.TrimSpace(firstStringField(fields, "provider_resource_id", "resource_id", "resource")) != "" {
		return "resource"
	}
	if strings.TrimSpace(firstStringField(fields, "model", "model_name")) != "" {
		return "model"
	}
	if strings.TrimSpace(firstStringField(fields, "provider_id", "provider")) != "" {
		return "provider"
	}
	return "unknown"
}

func firstStringField(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		value := stringField(fields, key)
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func okFailed(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func monitorProviderMessage(provider Provider, healthy bool) string {
	if healthy {
		return "Provider 状态正常"
	}
	return "Provider 未启用或不可用：" + provider.Status
}

func monitorResourceMessage(resource ProviderResource, healthy bool) string {
	if healthy {
		return "Provider 资源实例状态正常"
	}
	return "Provider 资源实例未启用或不可用：" + resource.Status
}

func exceedsRequestQuota(limits QuotaLimits, day, month *QuotaCounter) bool {
	return (limits.DailyRequests > 0 && day.Requests >= limits.DailyRequests) ||
		(limits.MonthlyRequests > 0 && month.Requests >= limits.MonthlyRequests)
}

func exceedsTokenQuota(limits QuotaLimits, day, month *QuotaCounter) bool {
	return (limits.DailyTokens > 0 && day.TotalTokens >= limits.DailyTokens) ||
		(limits.MonthlyTokens > 0 && month.TotalTokens >= limits.MonthlyTokens)
}

func exceedsCostQuota(limits QuotaLimits, day, month *QuotaCounter) bool {
	return (limits.DailyCostUSD > 0 && day.CostUSD >= limits.DailyCostUSD) ||
		(limits.MonthlyCostUSD > 0 && month.CostUSD >= limits.MonthlyCostUSD)
}

func addUsage(counter *QuotaCounter, usage Usage) {
	counter.PromptTokens += usage.PromptTokens
	counter.CompletionTokens += usage.CompletionTokens
	counter.TotalTokens += usage.TotalTokens
	counter.CostUSD += usage.CostUSD
}

func aggregateUsage(records []UsageRecord, keyFn func(UsageRecord) string) []map[string]any {
	type bucket struct {
		Key               string
		Requests          int64
		InputTokens       int64
		CachedInputTokens int64
		OutputTokens      int64
		TotalTokens       int64
		CostUSD           float64
	}
	buckets := map[string]*bucket{}
	for _, record := range records {
		key := keyFn(record)
		if key == "" {
			key = "unknown"
		}
		item, ok := buckets[key]
		if !ok {
			item = &bucket{Key: key}
			buckets[key] = item
		}
		item.Requests++
		item.InputTokens += record.InputTokens
		item.CachedInputTokens += record.CachedInputTokens
		item.OutputTokens += record.OutputTokens
		item.TotalTokens += record.TotalTokens
		item.CostUSD += record.CostUSD
	}
	items := make([]bucket, 0, len(buckets))
	for _, item := range buckets {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CostUSD == items[j].CostUSD {
			return items[i].TotalTokens > items[j].TotalTokens
		}
		return items[i].CostUSD > items[j].CostUSD
	})
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"id":                  item.Key,
			"request_count":       item.Requests,
			"input_tokens":        item.InputTokens,
			"cached_input_tokens": item.CachedInputTokens,
			"output_tokens":       item.OutputTokens,
			"total_tokens":        item.TotalTokens,
			"estimated_cost_usd":  item.CostUSD,
		})
	}
	return result
}

func dayBucket(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func monthBucket(t time.Time) string {
	return t.UTC().Format("2006-01")
}

func minuteBucket(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04")
}

func resourcePrefix(kind string) string {
	switch kind {
	case "teams":
		return "team"
	case "role-configs":
		return "role"
	case "project-members":
		return "pm"
	case "identity-providers":
		return "idp"
	case "users":
		return "usr"
	case "monitors":
		return "mon"
	case "proxies":
		return "prx"
	case "announcements":
		return "ann"
	case "settings":
		return "cfg"
	case "security-policies":
		return "sec"
	case "alert-rules":
		return "alr"
	case "quota-policies":
		return "quo"
	case "notification-channels":
		return "ntf"
	case "cost-centers":
		return "cc"
	case "budgets":
		return "bdg"
	case "chargebacks":
		return "cbk"
	case "invoices":
		return "inv"
	case "reports":
		return "rpt"
	case "approval-flows":
		return "apf"
	default:
		return "res"
	}
}

// GetDatabaseStatus returns the database type, Docker environment, and connection status.
func (s *GormStore) GetDatabaseStatus() (map[string]interface{}, error) {
	status := make(map[string]interface{})

	// 1. Detect the database type.
	dbType := "sqlite"
	if s.db.Dialector.Name() == "postgres" {
		dbType = "postgres"
	}
	status["database_type"] = dbType

	// 2. Detect whether running in Docker.
	isDocker := false
	if _, err := os.Stat("/.dockerenv"); err == nil {
		isDocker = true
	}
	status["is_docker"] = isDocker

	// 3. Test the database connection.
	sqlDB, err := s.db.DB()
	if err != nil {
		status["connection_ok"] = false
		return status, nil
	}

	if err := sqlDB.Ping(); err != nil {
		status["connection_ok"] = false
		return status, nil
	}
	status["connection_ok"] = true

	// 4. If PostgreSQL, retrieve the version information.
	if dbType == "postgres" {
		var version string
		if err := s.db.Raw("SELECT version()").Scan(&version).Error; err == nil {
			status["postgres_version"] = version
		}
	}

	// 5. Get the redacted database URL.
	if databaseURL := os.Getenv("TOKENHUB_DATABASE_URL"); databaseURL != "" {
		status["database_url"] = redactDatabaseURL(databaseURL)
	}

	return status, nil
}

func (s *GormStore) Ping(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// SetGatewayMetrics attaches the metrics collectors. It is called once during server
// construction, before the store serves traffic.
func (s *GormStore) SetGatewayMetrics(metrics *GatewayMetrics) {
	s.metrics = metrics
}
