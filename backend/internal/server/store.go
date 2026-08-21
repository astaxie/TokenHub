package server

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"tokenhub/backend/internal/guardrails"

	"gorm.io/gorm"
)

const (
	defaultSQLiteDatabaseURL = "sqlite://data/tokenhub.db"
	adaptiveRoutingWindow    = 15 * time.Minute
)

type QuotaBucket struct {
	KeyID            string `gorm:"primaryKey;index"`
	Scope            string `gorm:"primaryKey"`
	Bucket           string `gorm:"primaryKey;index"`
	AttributedUserID string `gorm:"primaryKey;index;default:__tokenhub_unattributed__"`
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
	BillingStore
	ReconciliationStore
	CreateProject(project Project) Project
	CreateProjectChecked(project Project) (Project, error)
	ListProjects() []Project
	UpdateProject(id string, patch Project) (Project, error)
	DeleteProject(id string) error
	GetProject(id string) (Project, bool)
	CreateGuardrailPolicy(policy guardrails.Policy) (guardrails.Policy, error)
	ListGuardrailPolicies() ([]guardrails.Policy, error)
	GetGuardrailPolicy(id string) (guardrails.Policy, error)
	UpdateGuardrailPolicy(id string, policy guardrails.Policy) (guardrails.Policy, guardrails.Policy, error)
	DeleteGuardrailPolicy(id string) (guardrails.Policy, error)
	ListProjectTeams(projectID string, offset int, limit int) ([]ProjectTeam, int64, error)
	AddProjectTeam(link ProjectTeam) (ProjectTeam, error)
	UpdateProjectTeam(projectID string, teamID string, role string) (ProjectTeam, error)
	RemoveProjectTeam(projectID string, teamID string) error
	CreateAPIKey(projectID string, key APIKey, rawSecret string) (APIKey, string, error)
	ListProjectKeys(projectID string) []APIKey
	ListAPIKeys() []APIKey
	GetAPIKey(id string) (APIKey, bool)
	UpdateAPIKey(id string, patch APIKey) (APIKey, error)
	RotateAPIKey(id string, graceUntil *time.Time) (APIKey, string, error)
	DeleteAPIKey(id string) error
	ValidateAPIKey(rawSecret string, clientIP string) (Project, APIKey, error)
	AddProvider(provider Provider) Provider
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
	GetProviderResource(id string) (ProviderResource, bool)
	UpdateProviderResource(id string, patch ProviderResource) (ProviderResource, error)
	UpdateProviderResourceOptions(id string, options map[string]string) (ProviderResource, error)
	DeleteProviderResource(id string) error
	SetProviderResourceHealth(resourceID string, healthy bool) (ProviderResource, error)
	BulkOperateProviderResources(action string, ids []string) (ProviderResourceBulkResult, error)
	ImportProviderResources(resources []ProviderResource) (ProviderResourceImportResult, error)
	AddModel(model Model) Model
	CreateModelWithRoutes(model Model, routes []ModelRoute) (Model, error)
	ListModels() []Model
	UpdateModel(name string, patch Model) (Model, error)
	DeleteModel(name string) error
	AddRoute(route ModelRoute) ModelRoute
	CreateRoute(route ModelRoute) (ModelRoute, error)
	ListRoutes() []ModelRoute
	UpdateRoute(id string, patch ModelRoute) (ModelRoute, error)
	UpdateModelRoutePolicy(modelName string, policy ModelRoutePolicy) ([]ModelRoute, error)
	DeleteRoute(id string) error
	SelectRoute(modelName string) (RouteSelection, error)
	SelectRouteCandidates(modelName string) ([]RouteSelection, error)
	MarkRouteUsed(routeID string)
	MarkProviderResourceUsed(resourceID string)
	StartCall(ctx context.Context, project Project, key APIKey, modelName string, tokenReservation int64) (CallContext, error)
	FinishCall(call CallContext, route RouteSelection, usage Usage, statusCode int, errorCode string, clientIP string, userAgent string)
	RecordPlaygroundRequest(call CallContext, route RouteSelection, statusCode int, errorCode string, clientIP string, userAgent string)
	RecordRouteAttempts(requestID string, attempts []RouteAttempt)
	RecordRejectedRequest(project Project, key APIKey, modelName string, stream bool, statusCode int, errorCode string, clientIP string, userAgent string) string
	RecordRequestPayload(requestID string, requestBody string, requestTruncated bool, responseBody string, responseTruncated bool)
	CreateImageJob(job ImageJob, prompt string) (ImageJob, error)
	ClaimImageJob(id string) (ImageJob, bool, error)
	GetImageJob(id string) (ImageJob, bool)
	ListImageJobs(limit int) []ImageJob
	ListImageJobsForAudit(query ImageJobAuditQuery) []ImageJob
	FailUnfinishedImageJobs(code string, message string) ([]ImageJob, error)
	UpdateImageJob(job ImageJob, revisedPrompt string) error
	CompleteImageJob(call CallContext, job ImageJob, revisedPrompt string, asset ImageAsset, route RouteSelection, usage Usage, clientIP string, userAgent string) error
	CreateResponseJob(job ResponseJob, requestJSON []byte) (ResponseJob, error)
	GetResponseJob(id string) (ResponseJob, bool, error)
	LoadResponseJobPayload(id string) ([]byte, []byte, error)
	CountQueuedResponseJobs() (int64, error)
	CountOutstandingResponseJobs() (int64, error)
	CountRetainedResponseJobs() (int64, error)
	ClaimResponseJob(owner string, leaseTTL time.Duration, resultTTL time.Duration) (ResponseJob, bool, error)
	RenewResponseJobLease(id string, owner string, epoch int64, leaseTTL time.Duration) (time.Duration, bool, error)
	AdmitResponseJob(ctx context.Context, id string, owner string, epoch int64, key APIKey, modelName string, tokenReservation int64) (CallContext, bool, error)
	ReleaseResponseJobAdmission(requestID string)
	ShutdownResponseJob(id string, owner string, epoch int64, resultTTL time.Duration) (string, bool, error)
	MarkResponseJobPhase(id string, owner string, epoch int64, phase string, requestID string) (bool, error)
	CancelResponseJob(id string, actor string, resultTTL time.Duration) (ResponseJob, bool, error)
	FinalizeResponseJob(call CallContext, id string, owner string, epoch int64, status string, resultJSON []byte, route RouteSelection, usage Usage, statusCode int, errorCode string, errorMessage string, clientIP string, userAgent string, resultTTL time.Duration) (ResponseJob, bool, error)
	RecoverResponseJobs(resultTTL time.Duration) (int64, int64, int64, error)
	ExpireResponseJobs() (int64, error)
	CreateImageAsset(asset ImageAsset) (ImageAsset, error)
	ListImageAssets(jobID string) []ImageAsset
	GetImageAsset(id string) (ImageAsset, bool)
	ListUsageRecords() []UsageRecord
	QueryUsageSummary(ctx context.Context, query UsageSummaryQuery) (UsageSummary, error)
	QueryAPIKeyUsage(ctx context.Context, query APIKeyUsageQuery) (APIKeyUsage, error)
	CreateAnalyticsCredential(credential AnalyticsCredential, rawSecret string) (AnalyticsCredential, string, error)
	ListAnalyticsCredentials() []AnalyticsCredential
	RevokeAnalyticsCredential(id string) (AnalyticsCredential, error)
	ValidateAnalyticsCredential(rawSecret string) (AnalyticsCredential, error)
	QueryTokenCostPage(ctx context.Context, query TokenCostQuery) (TokenCostPage, error)
	GenerateBillingPeriod(period string) (map[string]any, error)
	ListRequestLogs() []RequestLog
	QueryRequestLogs(query RequestLogQuery) (RequestLogPage, error)
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
	CreateResourceChecked(kind string, resource AdminResource) (AdminResource, error)
	CreateRoutingPolicy(resource AdminResource) (AdminResource, error)
	ListResources(kind string) []AdminResource
	ListResourcesChecked(kind string) ([]AdminResource, error)
	ListResourcesContext(ctx context.Context, kind string) ([]AdminResource, error)
	UpdateResource(kind string, id string, patch AdminResource) (AdminResource, error)
	DeleteResource(kind string, id string) error
	GetQuotaPolicyUsage(scope string, scopeID string) (QuotaPolicyUsage, bool, error)
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
	SaveAdminOAuthFlow(flow adminOAuthFlow) error
	ConsumeAdminOAuthFlow(state string, browserNonce string) (adminOAuthFlow, bool, error)
	SaveAdminOAuthExchange(exchange adminOAuthExchange) error
	ConsumeAdminOAuthExchange(code string, codeVerifier string) (adminOAuthExchange, bool, error)
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
	DeleteRequestPayloadLogsBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
	RunClusterTask(ctx context.Context, name string, revision int64, fn func(context.Context) error) error
	RunClusterOperation(ctx context.Context, name string, fn func(context.Context) error) error
	SaveProviderAccountOAuthSession(session providerAccountOAuthSession) error
	GetProviderAccountOAuthSessionByState(state string) (providerAccountOAuthSession, bool, error)
	ConsumeProviderAccountOAuthSession(id string, state string) (providerAccountOAuthSession, bool, error)
	TestProvider(id string) (Provider, error)
	TestProviderResource(id string) (ProviderResource, error)
	GetDatabaseStatus() (map[string]interface{}, error)
	Ping(ctx context.Context) error
}

var _ Store = (*GormStore)(nil)

type GormStore struct {
	db               *gorm.DB
	analyticsDB      *gorm.DB
	mu               *sync.Mutex
	leaseHeartbeats  *sync.Map
	lastUsed         *lastUsedThrottle
	modelLabels      *modelLabelCache
	secretKey        string
	metrics          *GatewayMetrics
	failureThreshold int
	cooldownDuration time.Duration
	cooldownMax      time.Duration
	sqliteDSN        string
	backupDir        string
	dbDriver         string        // "sqlite" or "postgres"
	heartbeatState   *atomic.Int32 // shared across value copies of the store
	// instanceHeartbeatID identifies the row this instance published while it
	// still held the schema migration lock; StartInstanceHeartbeat refreshes
	// that row instead of creating a second one.
	instanceHeartbeatID  string
	inFlightLeaseTTL     time.Duration
	clusterLockTTL       time.Duration
	imageCapabilityRetry time.Duration
}
type leaseHeartbeat struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	stop   chan struct{}
	done   chan struct{}
}

// MemoryStore is kept as a compatibility alias for existing tests and callers.
// It is now backed by GORM and SQLite, not process-local maps.
type MemoryStore = GormStore
