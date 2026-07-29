package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusRevoked  = "revoked"

	RouteStrategyBalanced         = "balanced"
	RouteStrategyCost             = "cost"
	RouteStrategyQuality          = "quality"
	RouteStrategyPriorityWeighted = "priority_weighted"
	RouteStrategyPriorityOnly     = "priority_only"

	ProviderMock             = "mock"
	ProviderOpenAI           = "openai"
	ProviderOpenAICompatible = "openai_compatible"
	ProviderAzureOpenAI      = "azure_openai"
	ProviderAnthropic        = "anthropic"
	ProviderGemini           = "gemini"

	ProviderResourceAPIKey             = "api_key"
	ProviderResourceOpenAISubscription = "openai_subscription"

	DefaultAPIKeyPrefix       = "sk_"
	DefaultAPIKeyRandomLength = 48
	MinAPIKeyRandomLength     = 24
	MaxAPIKeyRandomLength     = 128
	MaxAPIKeyPrefixLength     = 24
)

var (
	ErrInvalidAPIKey         = NewHTTPError(401, "invalid_api_key", "Invalid API key")
	ErrAPIKeyDisabled        = NewHTTPError(403, "api_key_disabled", "API key is disabled")
	ErrAPIKeyExpired         = NewHTTPError(403, "api_key_expired", "API key has expired")
	ErrModelNotAllowed       = NewHTTPError(403, "model_not_allowed", "Model is not allowed for this API key")
	ErrRateLimitExceeded     = NewHTTPError(429, "rate_limit_exceeded", "Rate limit exceeded")
	ErrQuotaExceeded         = NewHTTPError(429, "quota_exceeded", "Quota exceeded")
	ErrBudgetExceeded        = NewHTTPError(429, "budget_exceeded", "Budget exceeded")
	ErrProviderMissing       = NewHTTPError(503, "provider_unavailable", "No available provider route")
	ErrCoordinationLeaseLost = NewHTTPError(503, "coordination_lease_lost", "Cluster coordination lease was lost")
)

type HTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *HTTPError) Error() string {
	return e.Message
}

func NewHTTPError(status int, code, message string) *HTTPError {
	return &HTTPError{Status: status, Code: code, Message: message}
}

func AsHTTPError(err error) *HTTPError {
	if err == nil {
		return nil
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	return NewHTTPError(500, "internal_error", err.Error())
}

type Project struct {
	ID              string    `json:"id" gorm:"primaryKey"`
	Name            string    `json:"name"`
	TeamID          string    `json:"team_id,omitempty"`
	OwnerUserID     string    `json:"owner_user_id,omitempty"`
	CostCenter      string    `json:"cost_center,omitempty" gorm:"index"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	DefaultQuotaRef string    `json:"default_quota_ref,omitempty"`
}

type APIKey struct {
	ID                   string            `json:"id" gorm:"primaryKey"`
	ProjectID            string            `json:"project_id" gorm:"index"`
	TenantExternalID     string            `json:"tenant_id,omitempty" gorm:"index:idx_api_key_gateway_scope,priority:1"`
	ProjectExternalID    string            `json:"external_project_id,omitempty" gorm:"index:idx_api_key_gateway_scope,priority:2"`
	PrincipalType        string            `json:"principal_type,omitempty" gorm:"index"`
	PrincipalExternalID  string            `json:"principal_id,omitempty" gorm:"index"`
	Environment          string            `json:"environment,omitempty" gorm:"index"`
	ManagedBy            string            `json:"managed_by,omitempty" gorm:"index"`
	ControlRequestID     *string           `json:"-" gorm:"uniqueIndex"`
	ControlRequestDigest string            `json:"-"`
	Name                 string            `json:"name"`
	Group                string            `json:"group,omitempty" gorm:"index"`
	KeyHash              string            `json:"-" gorm:"uniqueIndex"`
	KeyCiphertext        string            `json:"-"`
	KeyPrefix            string            `json:"key_prefix"`
	KeySuffix            string            `json:"key_suffix"`
	AllowedModels        map[string]bool   `json:"-" gorm:"-"`
	Allowed              []string          `json:"allowed_models" gorm:"serializer:json"`
	IPAllowlist          []string          `json:"ip_allowlist,omitempty" gorm:"serializer:json"`
	Limits               QuotaLimits       `json:"limits" gorm:"embedded;embeddedPrefix:limit_"`
	Status               string            `json:"status"`
	ExpiresAt            *time.Time        `json:"expires_at,omitempty"`
	RotatedFromID        string            `json:"rotated_from_id,omitempty" gorm:"index"`
	GraceUntil           *time.Time        `json:"grace_until,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	LastUsedAt           *time.Time        `json:"last_used_at,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty" gorm:"serializer:json"`
}

type QuotaLimits struct {
	DailyRequests   int64   `json:"daily_requests"`
	MonthlyRequests int64   `json:"monthly_requests"`
	DailyTokens     int64   `json:"daily_tokens"`
	MonthlyTokens   int64   `json:"monthly_tokens"`
	DailyCostUSD    float64 `json:"daily_cost_usd"`
	MonthlyCostUSD  float64 `json:"monthly_cost_usd"`
	MaxConcurrency  int64   `json:"max_concurrency"`
}

type QuotaCounter struct {
	Requests         int64   `json:"requests"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

type Model struct {
	ID                     string            `json:"id" gorm:"primaryKey"`
	Name                   string            `json:"name" gorm:"uniqueIndex"`
	Category               string            `json:"category,omitempty" gorm:"index"`
	Family                 string            `json:"family"`
	Modality               string            `json:"modality"`
	ContextWindow          int64             `json:"context_window"`
	InputPriceUSDPer1M     float64           `json:"input_price_usd_per_1m"`
	OutputPriceUSDPer1M    float64           `json:"output_price_usd_per_1m"`
	EmbeddingPriceUSDPer1M float64           `json:"embedding_price_usd_per_1m"`
	InputModalities        []string          `json:"input_modalities,omitempty" gorm:"serializer:json"`
	OutputModalities       []string          `json:"output_modalities,omitempty" gorm:"serializer:json"`
	Capabilities           []string          `json:"capabilities,omitempty" gorm:"serializer:json"`
	SupportedParameters    []string          `json:"supported_parameters,omitempty" gorm:"serializer:json"`
	Metadata               map[string]string `json:"metadata,omitempty" gorm:"serializer:json"`
	Status                 string            `json:"status"`
	CreatedAt              time.Time         `json:"created_at"`
}

type ProviderCatalogModel struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	DisplayName         string            `json:"display_name,omitempty"`
	CanonicalName       string            `json:"canonical_name,omitempty"`
	Category            string            `json:"category,omitempty"`
	Family              string            `json:"family,omitempty"`
	Type                string            `json:"type,omitempty"`
	ContextWindow       int64             `json:"context_window,omitempty"`
	MaxOutputTokens     int64             `json:"max_output_tokens,omitempty"`
	InputPriceUSDPer1M  float64           `json:"input_price_usd_per_1m,omitempty"`
	OutputPriceUSDPer1M float64           `json:"output_price_usd_per_1m,omitempty"`
	InputModalities     []string          `json:"input_modalities,omitempty"`
	OutputModalities    []string          `json:"output_modalities,omitempty"`
	Capabilities        []string          `json:"capabilities,omitempty"`
	SupportedParameters []string          `json:"supported_parameters,omitempty"`
	LastUpdated         string            `json:"last_updated,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
}

type ProviderCatalogEntry struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	DisplayName    string                 `json:"display_name"`
	Type           string                 `json:"type"`
	BaseURL        string                 `json:"base_url,omitempty"`
	DocURL         string                 `json:"doc_url,omitempty"`
	Categories     []string               `json:"categories,omitempty"`
	CategoryCounts map[string]int         `json:"category_counts,omitempty"`
	ModelsCount    int                    `json:"models_count"`
	Source         string                 `json:"source"`
	Models         []ProviderCatalogModel `json:"models,omitempty"`
}

type ProviderCreateRequest struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	BaseURL        string            `json:"base_url"`
	APIKey         string            `json:"api_key"`
	Status         string            `json:"status"`
	Healthy        bool              `json:"healthy"`
	Priority       int               `json:"priority"`
	Headers        map[string]string `json:"headers"`
	Options        map[string]string `json:"options"`
	CatalogID      string            `json:"catalog_id"`
	ModelCategory  string            `json:"model_category"`
	CreateRoutes   *bool             `json:"create_routes"`
	SelectedModels []string          `json:"selected_models"`
}

type ProviderCreateResult struct {
	Provider      Provider `json:"provider"`
	CreatedRoutes int      `json:"created_routes"`
	ModelNames    []string `json:"model_names,omitempty"`
	RouteIDs      []string `json:"route_ids,omitempty"`
	CatalogSource string   `json:"catalog_source,omitempty"`
}

type Provider struct {
	ID        string            `json:"id" gorm:"primaryKey"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	BaseURL   string            `json:"base_url,omitempty"`
	APIKey    string            `json:"-"`
	Status    string            `json:"status"`
	Healthy   bool              `json:"healthy"`
	Priority  int               `json:"priority"`
	Headers   map[string]string `json:"headers,omitempty" gorm:"serializer:json"`
	Options   map[string]string `json:"options,omitempty" gorm:"serializer:json"`
	CreatedAt time.Time         `json:"created_at"`
}

type ProviderResource struct {
	ID                string                       `json:"id" gorm:"primaryKey"`
	ProviderID        string                       `json:"provider_id" gorm:"index"`
	Name              string                       `json:"name"`
	Group             string                       `json:"group,omitempty" gorm:"index"`
	ResourceType      string                       `json:"resource_type"`
	BaseURL           string                       `json:"base_url,omitempty"`
	APIKey            string                       `json:"api_key,omitempty"`
	Region            string                       `json:"region,omitempty"`
	Environment       string                       `json:"environment,omitempty"`
	Status            string                       `json:"status"`
	Healthy           bool                         `json:"healthy"`
	Priority          int                          `json:"priority"`
	Weight            int                          `json:"weight"`
	RateLimitRPM      int64                        `json:"rate_limit_rpm"`
	TokenLimitTPM     int64                        `json:"token_limit_tpm"`
	MaxConcurrency    int64                        `json:"max_concurrency"`
	Headers           map[string]string            `json:"headers,omitempty" gorm:"serializer:json"`
	Options           map[string]string            `json:"options,omitempty" gorm:"serializer:json"`
	Credentials       *ProviderResourceCredentials `json:"credentials,omitempty" gorm:"-"`
	CredentialBlob    string                       `json:"-" gorm:"column:credential_blob"`
	CredentialSummary map[string]string            `json:"credential_summary,omitempty" gorm:"-"`
	FailureCount      int                          `json:"failure_count"`
	CooldownUntil     *time.Time                   `json:"cooldown_until,omitempty"`
	LastUsedAt        *time.Time                   `json:"last_used_at,omitempty"`
	LastCheckedAt     *time.Time                   `json:"last_checked_at,omitempty"`
	CreatedAt         time.Time                    `json:"created_at"`
	UpdatedAt         time.Time                    `json:"updated_at"`
}

type ProviderResourceCredentials struct {
	AuthType       string `json:"auth_type,omitempty"`
	AccessToken    string `json:"access_token,omitempty"`
	RefreshToken   string `json:"refresh_token,omitempty"`
	IDToken        string `json:"id_token,omitempty"`
	ClientID       string `json:"client_id,omitempty"`
	Scopes         string `json:"scopes,omitempty"`
	TokenType      string `json:"token_type,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	AccountID      string `json:"account_id,omitempty"`
	UserID         string `json:"user_id,omitempty"`
	Email          string `json:"email,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	PlanType       string `json:"plan_type,omitempty"`
}

type ProviderResourceBulkResult struct {
	Action    string             `json:"action"`
	Success   int                `json:"success"`
	Failed    int                `json:"failed"`
	Resources []ProviderResource `json:"resources"`
	Errors    []string           `json:"errors,omitempty"`
}

type ProviderResourceImportResult struct {
	Success   int                `json:"success"`
	Failed    int                `json:"failed"`
	Resources []ProviderResource `json:"resources"`
	Errors    []string           `json:"errors,omitempty"`
}

type ModelRoute struct {
	ID                 string     `json:"id" gorm:"primaryKey"`
	ModelName          string     `json:"model_name" gorm:"index"`
	ProviderID         string     `json:"provider_id" gorm:"index"`
	ProviderResourceID string     `json:"provider_resource_id,omitempty" gorm:"index"`
	ResourceGroup      string     `json:"resource_group,omitempty" gorm:"index"`
	StickySession      bool       `json:"sticky_session"`
	ProviderModel      string     `json:"provider_model"`
	Priority           int        `json:"priority"`
	Weight             int        `json:"weight"`
	QualityScore       int        `json:"quality_score,omitempty"`
	CostScore          int        `json:"cost_score,omitempty"`
	Status             string     `json:"status"`
	Strategy           string     `json:"strategy,omitempty"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

type Usage struct {
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CostUSD          float64 `json:"estimated_cost_usd,omitempty"`
}

type UsageRecord struct {
	ID                 string    `json:"id" gorm:"primaryKey"`
	RequestID          string    `json:"request_id" gorm:"index"`
	ProjectID          string    `json:"project_id" gorm:"index"`
	APIKeyID           string    `json:"api_key_id" gorm:"index"`
	ModelName          string    `json:"model" gorm:"index"`
	ProviderID         string    `json:"provider_id" gorm:"index"`
	ProviderResourceID string    `json:"provider_resource_id,omitempty" gorm:"index"`
	InputTokens        int64     `json:"input_tokens"`
	OutputTokens       int64     `json:"output_tokens"`
	TotalTokens        int64     `json:"total_tokens"`
	CostUSD            float64   `json:"estimated_cost_usd"`
	CreatedAt          time.Time `json:"created_at"`
}

type RequestLog struct {
	ID                 string    `json:"id" gorm:"primaryKey"`
	RequestID          string    `json:"request_id" gorm:"index"`
	ProjectID          string    `json:"project_id" gorm:"index"`
	APIKeyID           string    `json:"api_key_id" gorm:"index"`
	ModelName          string    `json:"model" gorm:"index"`
	ProviderID         string    `json:"provider_id,omitempty" gorm:"index"`
	ProviderResourceID string    `json:"provider_resource_id,omitempty" gorm:"index"`
	ProviderModel      string    `json:"provider_model,omitempty"`
	StatusCode         int       `json:"status_code"`
	ErrorCode          string    `json:"error_code,omitempty"`
	LatencyMS          int64     `json:"latency_ms"`
	ClientIP           string    `json:"client_ip,omitempty"`
	UserAgent          string    `json:"user_agent,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type RequestPayloadLog struct {
	ID                string    `json:"id" gorm:"primaryKey"`
	RequestID         string    `json:"request_id" gorm:"uniqueIndex"`
	RequestBody       string    `json:"request_body,omitempty"`
	ResponseBody      string    `json:"response_body,omitempty"`
	RequestTruncated  bool      `json:"request_truncated"`
	ResponseTruncated bool      `json:"response_truncated"`
	CreatedAt         time.Time `json:"created_at"`
}

type RouteAttemptLog struct {
	ID                 string    `json:"id" gorm:"primaryKey"`
	RequestID          string    `json:"request_id" gorm:"index"`
	AttemptIndex       int       `json:"attempt_index"`
	RouteID            string    `json:"route_id,omitempty" gorm:"index"`
	ProviderID         string    `json:"provider_id,omitempty" gorm:"index"`
	ProviderResourceID string    `json:"provider_resource_id,omitempty" gorm:"index"`
	ProviderModel      string    `json:"provider_model,omitempty"`
	StatusCode         int       `json:"status_code"`
	ErrorCode          string    `json:"error_code,omitempty"`
	ErrorMessage       string    `json:"error_message,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type AlertEvent struct {
	ID         string    `json:"id" gorm:"primaryKey"`
	ScopeType  string    `json:"scope_type" gorm:"index"`
	ScopeID    string    `json:"scope_id" gorm:"index"`
	Severity   string    `json:"severity"`
	Code       string    `json:"code"`
	Message    string    `json:"message"`
	ResourceID string    `json:"resource_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type AlertDelivery struct {
	ID         string    `json:"id" gorm:"primaryKey"`
	AlertID    string    `json:"alert_id" gorm:"index"`
	ChannelID  string    `json:"channel_id,omitempty" gorm:"index"`
	Channel    string    `json:"channel"`
	Target     string    `json:"target,omitempty"`
	Status     string    `json:"status" gorm:"index"`
	StatusCode int       `json:"status_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	Payload    string    `json:"payload,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type ProviderResourceBucket struct {
	ResourceID string `gorm:"primaryKey;index"`
	Bucket     string `gorm:"primaryKey;index"`
	Requests   int64  `json:"requests"`
	Tokens     int64  `json:"tokens"`
	UpdatedAt  time.Time
}

type AuditEvent struct {
	ID             string    `json:"id" gorm:"primaryKey"`
	ActorUserID    string    `json:"actor_user_id" gorm:"index"`
	ActorName      string    `json:"actor_name,omitempty"`
	ActorRole      string    `json:"actor_role,omitempty"`
	Action         string    `json:"action" gorm:"index"`
	ResourceType   string    `json:"resource_type" gorm:"index"`
	ResourceID     string    `json:"resource_id" gorm:"index"`
	Status         string    `json:"status"`
	Message        string    `json:"message,omitempty"`
	BeforeSnapshot string    `json:"before_snapshot,omitempty"`
	AfterSnapshot  string    `json:"after_snapshot,omitempty"`
	IP             string    `json:"ip,omitempty"`
	UserAgent      string    `json:"user_agent,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type AdminResource struct {
	ID          string         `json:"id" gorm:"primaryKey"`
	Kind        string         `json:"kind" gorm:"primaryKey;index"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Status      string         `json:"status"`
	Fields      map[string]any `json:"fields,omitempty" gorm:"serializer:json"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type MonitorRunResult struct {
	MonitorID  string    `json:"monitor_id"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id,omitempty"`
	Status     string    `json:"status"`
	Message    string    `json:"message,omitempty"`
	LatencyMS  int64     `json:"latency_ms"`
	CheckedAt  time.Time `json:"checked_at"`
	AlertID    string    `json:"alert_id,omitempty"`
	ProviderID string    `json:"provider_id,omitempty"`
	ResourceID string    `json:"resource_id,omitempty"`
	ModelName  string    `json:"model,omitempty"`
}

type ApprovalRequest struct {
	ID           string     `json:"id" gorm:"primaryKey"`
	FlowID       string     `json:"flow_id,omitempty" gorm:"index"`
	Trigger      string     `json:"trigger" gorm:"index"`
	ResourceType string     `json:"resource_type" gorm:"index"`
	ResourceID   string     `json:"resource_id,omitempty" gorm:"index"`
	RequesterID  string     `json:"requester_id,omitempty" gorm:"index"`
	Requester    string     `json:"requester,omitempty"`
	Status       string     `json:"status" gorm:"index"`
	Reason       string     `json:"reason,omitempty"`
	Payload      string     `json:"payload,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	DecidedBy    string     `json:"decided_by,omitempty"`
}

type AdminUser struct {
	ID           string     `json:"id" gorm:"primaryKey"`
	Username     string     `json:"username" gorm:"uniqueIndex"`
	Name         string     `json:"name"`
	Email        string     `json:"email" gorm:"uniqueIndex"`
	Role         string     `json:"role"`
	TeamID       string     `json:"team_id,omitempty"`
	Status       string     `json:"status"`
	PasswordHash string     `json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

type AdminSession struct {
	Token     string    `json:"token" gorm:"primaryKey"`
	UserID    string    `json:"user_id" gorm:"index"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type AdminPasswordResetToken struct {
	ID        string     `json:"id" gorm:"primaryKey"`
	UserID    string     `json:"user_id" gorm:"index"`
	TokenHash string     `json:"-" gorm:"uniqueIndex"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type SQLiteBackupRecord struct {
	ID             string     `json:"id" gorm:"primaryKey"`
	Name           string     `json:"name"`
	FileName       string     `json:"file_name"`
	FilePath       string     `json:"-"`
	Status         string     `json:"status" gorm:"index"`
	Trigger        string     `json:"trigger"`
	SizeBytes      int64      `json:"size_bytes"`
	ChecksumSHA256 string     `json:"checksum_sha256,omitempty"`
	CreatedBy      string     `json:"created_by,omitempty" gorm:"index"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RestoredBy     string     `json:"restored_by,omitempty"`
	RestoredAt     *time.Time `json:"restored_at,omitempty"`
	Error          string     `json:"error,omitempty"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type ChatCompletionRequest struct {
	Model       string         `json:"model"`
	Messages    []ChatMessage  `json:"messages"`
	Stream      bool           `json:"stream,omitempty"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type PlaygroundChatResponse struct {
	Response  any                      `json:"response"`
	Route     PlaygroundRouteSummary   `json:"route"`
	Usage     Usage                    `json:"usage"`
	Attempts  []PlaygroundRouteAttempt `json:"attempts"`
	RequestID string                   `json:"request_id"`
}

type PlaygroundRouteSummary struct {
	RouteID          string `json:"route_id,omitempty"`
	ProviderID       string `json:"provider_id,omitempty"`
	ProviderName     string `json:"provider_name,omitempty"`
	ResourceID       string `json:"resource_id,omitempty"`
	ResourceName     string `json:"resource_name,omitempty"`
	ProviderModel    string `json:"provider_model,omitempty"`
	Priority         int    `json:"priority,omitempty"`
	ResourcePriority int    `json:"resource_priority,omitempty"`
	Weight           int    `json:"weight,omitempty"`
	QualityScore     int    `json:"quality_score,omitempty"`
	CostScore        int    `json:"cost_score,omitempty"`
	Strategy         string `json:"strategy,omitempty"`
}

type PlaygroundRouteAttempt struct {
	Route  PlaygroundRouteSummary `json:"route"`
	Status int                    `json:"status"`
	Code   string                 `json:"code,omitempty"`
	Error  string                 `json:"error,omitempty"`
}

type ResponsesRequest struct {
	Model       string   `json:"model"`
	Input       any      `json:"input"`
	Stream      bool     `json:"stream,omitempty"`
	MaxTokens   int      `json:"max_output_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type EmbeddingsRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type RouteSelection struct {
	Provider      Provider
	Resource      *ProviderResource
	ProviderModel string
	Route         ModelRoute
}

type RouteExplainStep struct {
	RouteID          string `json:"route_id"`
	ProviderID       string `json:"provider_id"`
	ResourceID       string `json:"resource_id,omitempty"`
	ProviderModel    string `json:"provider_model"`
	Priority         int    `json:"priority"`
	ResourcePriority int    `json:"resource_priority"`
	Weight           int    `json:"weight"`
	QualityScore     int    `json:"quality_score,omitempty"`
	CostScore        int    `json:"cost_score,omitempty"`
	Strategy         string `json:"strategy"`
	Status           string `json:"status"`
}

type RouteAttempt struct {
	Selection RouteSelection `json:"selection"`
	Status    int            `json:"status"`
	ErrorCode string         `json:"error_code,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type RoutedCall struct {
	Call   CallContext
	Routes []RouteSelection
}

type CallContext struct {
	RequestID      string
	Project        Project
	Key            APIKey
	Model          Model
	StartedAt      time.Time
	requestContext context.Context
}

func NewID(prefix string) string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(buf[:])
}

func GenerateAPIKey() string {
	return GenerateAPIKeyWithOptions(DefaultAPIKeyPrefix, DefaultAPIKeyRandomLength)
}

func GenerateAPIKeyWithOptions(prefix string, randomLength int) string {
	prefix = NormalizeAPIKeyPrefix(prefix)
	randomLength = NormalizeAPIKeyRandomLength(randomLength)
	byteLen := (randomLength*3 + 3) / 4
	buf := make([]byte, byteLen+2)
	if _, err := rand.Read(buf); err != nil {
		randomPart := ""
		for len(randomPart) < randomLength {
			randomPart += strings.TrimPrefix(NewID("live"), "live_")
		}
		return prefix + randomPart[:randomLength]
	}
	randomPart := base64.RawURLEncoding.EncodeToString(buf)
	for len(randomPart) < randomLength {
		var extra [12]byte
		if _, err := rand.Read(extra[:]); err != nil {
			randomPart += strings.TrimPrefix(NewID("live"), "live_")
			continue
		}
		randomPart += base64.RawURLEncoding.EncodeToString(extra[:])
	}
	return prefix + randomPart[:randomLength]
}

func NormalizeAPIKeyPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return DefaultAPIKeyPrefix
	}
	var builder strings.Builder
	for _, char := range prefix {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			builder.WriteRune(char)
		}
		if builder.Len() >= MaxAPIKeyPrefixLength {
			break
		}
	}
	if builder.Len() == 0 {
		return DefaultAPIKeyPrefix
	}
	return builder.String()
}

func NormalizeAPIKeyRandomLength(length int) int {
	if length <= 0 {
		return DefaultAPIKeyRandomLength
	}
	if length < MinAPIKeyRandomLength {
		return MinAPIKeyRandomLength
	}
	if length > MaxAPIKeyRandomLength {
		return MaxAPIKeyRandomLength
	}
	return length
}

func GenerateAdminSessionToken() string {
	return "tha_" + NewID("session")
}

func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func PrefixSuffix(secret string) (string, string) {
	if len(secret) <= 12 {
		return secret, secret
	}
	return secret[:8], secret[len(secret)-6:]
}

func AllowedModelSet(models []string) map[string]bool {
	set := make(map[string]bool, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model != "" {
			set[model] = true
		}
	}
	return set
}

func EstimateTextTokens(text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	words := int64(len(strings.Fields(text)))
	chars := int64(math.Ceil(float64(len([]rune(text))) / 4.0))
	if words > chars {
		return words
	}
	return chars
}

func ChatPromptText(messages []ChatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		parts = append(parts, contentToText(msg.Content))
	}
	return strings.Join(parts, "\n")
}

func ResponsesInputText(input any) string {
	return contentToText(input)
}

func EmbeddingInputText(input any) string {
	return contentToText(input)
}

func contentToText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		var parts []string
		for _, item := range typed {
			parts = append(parts, contentToText(item))
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
		if value, ok := typed["content"]; ok {
			return contentToText(value)
		}
		var parts []string
		for _, item := range typed {
			parts = append(parts, contentToText(item))
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(value)
	}
}
