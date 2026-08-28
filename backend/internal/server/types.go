package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusRevoked  = "revoked"

	RouteStrategyBalanced         = "balanced"
	RouteStrategyAdaptive         = "adaptive"
	RouteStrategyCost             = "cost"
	RouteStrategyQuality          = "quality"
	RouteStrategyPriorityWeighted = "priority_weighted"
	RouteStrategyPriorityOnly     = "priority_only"
	ModelAccessModeInherit        = "inherit"
	ModelAccessModeRestricted     = "restricted"

	RouteProjectScopeAll     = "all"
	RouteProjectScopeInclude = "include"
	RouteProjectScopeExclude = "exclude"

	ProviderMock             = "mock"
	ProviderOpenAI           = "openai"
	ProviderOpenAICodex      = "openai_codex"
	ProviderOpenAICompatible = "openai_compatible"
	ProviderAzureOpenAI      = "azure_openai"
	ProviderAnthropic        = "anthropic"
	ProviderGemini           = "gemini"
	ProviderKronk            = "kronk"

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
	Status         int
	Code           string
	Message        string
	Details        any               `json:"-"`
	UpstreamStatus int               `json:"-"`
	Headers        map[string]string `json:"-"`
}

func (e *HTTPError) Error() string {
	return e.Message
}

func NewHTTPError(status int, code, message string) *HTTPError {
	return &HTTPError{Status: status, Code: code, Message: message}
}

// upstreamStatusOrZero is nil-safe: the attempt records call it on the result of
// AsHTTPError, which is nil when there was no error to describe.
func (e *HTTPError) upstreamStatusOrZero() int {
	if e == nil {
		return 0
	}
	return e.UpstreamStatus
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
	ID              string        `json:"id" gorm:"primaryKey"`
	Name            string        `json:"name"`
	TeamID          string        `json:"team_id,omitempty"`
	Teams           []ProjectTeam `json:"teams,omitempty" gorm:"foreignKey:ProjectID;references:ID"`
	OwnerUserID     string        `json:"owner_user_id,omitempty"`
	CostCenter      string        `json:"cost_center,omitempty" gorm:"index"`
	ModelAccessMode string        `json:"model_access_mode"`
	AllowedModels   []string      `json:"allowed_models" gorm:"serializer:json"`
	Status          string        `json:"status"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	DefaultQuotaRef string        `json:"default_quota_ref,omitempty"`
}

type ProjectTeam struct {
	ProjectID string    `json:"project_id" gorm:"primaryKey;index"`
	TeamID    string    `json:"team_id" gorm:"primaryKey;index"`
	Role      string    `json:"role" gorm:"index"`
	IsPrimary bool      `json:"is_primary" gorm:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type APIKey struct {
	ID              string            `json:"id" gorm:"primaryKey"`
	ProjectID       string            `json:"project_id" gorm:"index"`
	OwnerUserID     string            `json:"owner_user_id,omitempty" gorm:"index"`
	Name            string            `json:"name"`
	Group           string            `json:"group,omitempty" gorm:"index"`
	KeyHash         string            `json:"-" gorm:"uniqueIndex"`
	KeyPrefix       string            `json:"key_prefix"`
	KeySuffix       string            `json:"key_suffix"`
	AllowedModels   map[string]bool   `json:"-" gorm:"-"`
	Allowed         []string          `json:"allowed_models" gorm:"serializer:json"`
	ModelAccessMode string            `json:"model_access_mode"`
	IPAllowlist     []string          `json:"ip_allowlist,omitempty" gorm:"serializer:json"`
	Limits          QuotaLimits       `json:"limits" gorm:"embedded;embeddedPrefix:limit_"`
	LimitsSet       bool              `json:"-" gorm:"-"`
	RateLimitRPM    *int64            `json:"rate_limit_rpm,omitempty"`
	RateLimitSet    bool              `json:"-" gorm:"-"`
	TokenLimitTPM   *int64            `json:"token_limit_tpm,omitempty"`
	TokenLimitSet   bool              `json:"-" gorm:"-"`
	Status          string            `json:"status"`
	ExpiresAt       *time.Time        `json:"expires_at,omitempty"`
	RotatedFromID   string            `json:"rotated_from_id,omitempty" gorm:"index"`
	GraceUntil      *time.Time        `json:"grace_until,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	LastUsedAt      *time.Time        `json:"last_used_at,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty" gorm:"serializer:json"`
}

type QuotaLimits struct {
	RateLimitRPM    int64   `json:"rate_limit_rpm,omitempty"`
	TokenLimitTPM   int64   `json:"token_limit_tpm,omitempty"`
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

type QuotaPolicyUsage struct {
	Daily   QuotaCounter `json:"daily"`
	Monthly QuotaCounter `json:"monthly"`
}

type Model struct {
	ID                        string  `json:"id" gorm:"primaryKey"`
	Name                      string  `json:"name" gorm:"uniqueIndex"`
	Category                  string  `json:"category,omitempty" gorm:"index"`
	Family                    string  `json:"family"`
	Modality                  string  `json:"modality"`
	ContextWindow             int64   `json:"context_window"`
	InputPriceUSDPer1M        float64 `json:"input_price_usd_per_1m"`
	CacheReadPriceUSDPer1M    float64 `json:"cache_read_price_usd_per_1m"`
	CacheWritePriceUSDPer1M   float64 `json:"cache_write_price_usd_per_1m"`
	CacheWrite5mPriceUSDPer1M float64 `json:"cache_write_5m_price_usd_per_1m"`
	CacheWrite1hPriceUSDPer1M float64 `json:"cache_write_1h_price_usd_per_1m"`
	CacheWritePriceConfiguration
	OutputPriceUSDPer1M    float64              `json:"output_price_usd_per_1m"`
	EmbeddingPriceUSDPer1M float64              `json:"embedding_price_usd_per_1m"`
	PricingPeriods         []ModelPricingPeriod `json:"pricing_periods,omitempty" gorm:"serializer:json"`
	InputModalities        []string             `json:"input_modalities,omitempty" gorm:"serializer:json"`
	OutputModalities       []string             `json:"output_modalities,omitempty" gorm:"serializer:json"`
	Capabilities           []string             `json:"capabilities,omitempty" gorm:"serializer:json"`
	SupportedParameters    []string             `json:"supported_parameters,omitempty" gorm:"serializer:json"`
	Metadata               map[string]string    `json:"metadata,omitempty" gorm:"serializer:json"`
	Status                 string               `json:"status"`
	CreatedAt              time.Time            `json:"created_at"`
}

type ProviderCatalogModel struct {
	ID                        string  `json:"id"`
	Name                      string  `json:"name"`
	DisplayName               string  `json:"display_name,omitempty"`
	CanonicalName             string  `json:"canonical_name,omitempty"`
	Category                  string  `json:"category,omitempty"`
	Family                    string  `json:"family,omitempty"`
	Type                      string  `json:"type,omitempty"`
	ContextWindow             int64   `json:"context_window,omitempty"`
	MaxOutputTokens           int64   `json:"max_output_tokens,omitempty"`
	InputPriceUSDPer1M        float64 `json:"input_price_usd_per_1m,omitempty"`
	CacheReadPriceUSDPer1M    float64 `json:"cache_read_price_usd_per_1m,omitempty"`
	CacheWritePriceUSDPer1M   float64 `json:"cache_write_price_usd_per_1m,omitempty"`
	CacheWrite5mPriceUSDPer1M float64 `json:"cache_write_5m_price_usd_per_1m,omitempty"`
	CacheWrite1hPriceUSDPer1M float64 `json:"cache_write_1h_price_usd_per_1m,omitempty"`
	CacheWritePriceConfiguration
	OutputPriceUSDPer1M float64              `json:"output_price_usd_per_1m,omitempty"`
	PricingPeriods      []ModelPricingPeriod `json:"pricing_periods,omitempty"`
	InputModalities     []string             `json:"input_modalities,omitempty"`
	OutputModalities    []string             `json:"output_modalities,omitempty"`
	Capabilities        []string             `json:"capabilities,omitempty"`
	SupportedParameters []string             `json:"supported_parameters,omitempty"`
	LastUpdated         string               `json:"last_updated,omitempty"`
	Metadata            map[string]string    `json:"metadata,omitempty"`
}

// ProviderModel is an upstream model imported into a concrete Provider. It is
// inventory, not a public API model: publication happens only through a
// ModelRoute that connects a Model to this provider/upstream-model pair.
type ProviderModel struct {
	ID                        string  `json:"id" gorm:"primaryKey"`
	ProviderID                string  `json:"provider_id" gorm:"uniqueIndex:idx_provider_upstream;index"`
	UpstreamModel             string  `json:"upstream_model" gorm:"uniqueIndex:idx_provider_upstream"`
	DisplayName               string  `json:"display_name,omitempty"`
	CanonicalName             string  `json:"canonical_name,omitempty"`
	Category                  string  `json:"category,omitempty" gorm:"index"`
	Family                    string  `json:"family,omitempty"`
	Modality                  string  `json:"modality,omitempty"`
	ContextWindow             int64   `json:"context_window,omitempty"`
	InputPriceUSDPer1M        float64 `json:"input_price_usd_per_1m,omitempty"`
	CacheReadPriceUSDPer1M    float64 `json:"cache_read_price_usd_per_1m,omitempty"`
	CacheWritePriceUSDPer1M   float64 `json:"cache_write_price_usd_per_1m,omitempty"`
	CacheWrite5mPriceUSDPer1M float64 `json:"cache_write_5m_price_usd_per_1m,omitempty"`
	CacheWrite1hPriceUSDPer1M float64 `json:"cache_write_1h_price_usd_per_1m,omitempty"`
	CacheWritePriceConfiguration
	OutputPriceUSDPer1M float64              `json:"output_price_usd_per_1m,omitempty"`
	PricingPeriods      []ModelPricingPeriod `json:"pricing_periods,omitempty" gorm:"serializer:json"`
	InputModalities     []string             `json:"input_modalities,omitempty" gorm:"serializer:json"`
	OutputModalities    []string             `json:"output_modalities,omitempty" gorm:"serializer:json"`
	Capabilities        []string             `json:"capabilities,omitempty" gorm:"serializer:json"`
	SupportedParameters []string             `json:"supported_parameters,omitempty" gorm:"serializer:json"`
	Metadata            map[string]string    `json:"metadata,omitempty" gorm:"serializer:json"`
	Source              string               `json:"source,omitempty" gorm:"index"`
	Status              string               `json:"status" gorm:"index"`
	LastSeenAt          *time.Time           `json:"last_seen_at,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

type ProviderModelImportRequest struct {
	ProviderID string                 `json:"provider_id"`
	Models     []ProviderCatalogModel `json:"models"`
	Publish    bool                   `json:"publish"`
}

type ProviderModelImportResult struct {
	ImportedModels int             `json:"imported_models"`
	ProviderModels []ProviderModel `json:"provider_models"`
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
	ETag           string                 `json:"etag,omitempty"`
	Models         []ProviderCatalogModel `json:"models,omitempty"`
}

type ProviderCreateRequest struct {
	ID                          string            `json:"id"`
	ProviderID                  string            `json:"provider_id"`
	Name                        string            `json:"name"`
	Type                        string            `json:"type"`
	BaseURL                     string            `json:"base_url"`
	APIKey                      string            `json:"api_key"`
	ClearAPIKey                 bool              `json:"clear_api_key"`
	Status                      string            `json:"status"`
	Healthy                     *bool             `json:"healthy"`
	Priority                    int               `json:"priority"`
	Headers                     map[string]string `json:"headers"`
	SensitiveHeaders            []string          `json:"sensitive_headers"`
	Options                     map[string]string `json:"options"`
	CatalogID                   string            `json:"catalog_id"`
	ModelCategory               string            `json:"model_category"`
	SystemPromptTransformPolicy *string           `json:"system_prompt_transform_policy,omitempty"`
	// ClaudeCodeAttributionPolicy is a legacy write-only alias for system prompt transform policy.
	ClaudeCodeAttributionPolicy *string `json:"claude_code_attribution_policy,omitempty"`
	// CreateRoutes is accepted only to reject the retired automatic-route workflow.
	CreateRoutes     *bool                  `json:"create_routes"`
	SelectedModels   []string               `json:"selected_models"`
	CustomModels     []ProviderCatalogModel `json:"custom_models"`
	ProviderAuthMode string                 `json:"provider_auth_mode"`
	// AnthropicAuthType is a legacy write-only alias for provider auth mode.
	AnthropicAuthType string `json:"anthropic_auth_type"`
}

type ProviderCreateResult struct {
	Provider       Provider `json:"provider"`
	ImportedModels int      `json:"imported_models"`
	CatalogSource  string   `json:"catalog_source,omitempty"`
}

type Provider struct {
	ID                     string            `json:"id" gorm:"primaryKey"`
	Name                   string            `json:"name"`
	Type                   string            `json:"type"`
	BaseURL                string            `json:"base_url,omitempty"`
	APIKey                 string            `json:"-"`
	ClearAPIKey            bool              `json:"-" gorm:"-"`
	Status                 string            `json:"status"`
	Healthy                bool              `json:"healthy"`
	Priority               int               `json:"priority"`
	Headers                map[string]string `json:"headers,omitempty" gorm:"serializer:json"`
	SensitiveHeaders       []string          `json:"sensitive_headers,omitempty" gorm:"serializer:json"`
	HeaderValidationErrors []string          `json:"header_validation_errors,omitempty" gorm:"-"`
	Options                map[string]string `json:"options,omitempty" gorm:"serializer:json"`
	CreatedAt              time.Time         `json:"created_at"`
}

type ProviderResource struct {
	ID                     string                       `json:"id" gorm:"primaryKey"`
	ProviderID             string                       `json:"provider_id" gorm:"index"`
	Name                   string                       `json:"name"`
	Group                  string                       `json:"group,omitempty" gorm:"index"`
	ResourceType           string                       `json:"resource_type"`
	BaseURL                string                       `json:"base_url,omitempty"`
	APIKey                 string                       `json:"api_key,omitempty"`
	Region                 string                       `json:"region,omitempty"`
	Environment            string                       `json:"environment,omitempty"`
	Status                 string                       `json:"status"`
	Healthy                bool                         `json:"healthy"`
	Priority               int                          `json:"priority"`
	Weight                 int                          `json:"weight"`
	RateLimitRPM           int64                        `json:"rate_limit_rpm"`
	TokenLimitTPM          int64                        `json:"token_limit_tpm"`
	MaxConcurrency         int64                        `json:"max_concurrency"`
	Headers                map[string]string            `json:"headers,omitempty" gorm:"serializer:json"`
	SensitiveHeaders       []string                     `json:"sensitive_headers,omitempty" gorm:"serializer:json"`
	HeaderValidationErrors []string                     `json:"header_validation_errors,omitempty" gorm:"-"`
	Options                map[string]string            `json:"options,omitempty" gorm:"serializer:json"`
	Credentials            *ProviderResourceCredentials `json:"credentials,omitempty" gorm:"-"`
	CredentialBlob         string                       `json:"-" gorm:"column:credential_blob"`
	CredentialSummary      map[string]string            `json:"credential_summary,omitempty" gorm:"-"`
	Observation            *ProviderResourceObservation `json:"observation,omitempty" gorm:"-"`
	FailureCount           int                          `json:"failure_count"`
	CooldownUntil          *time.Time                   `json:"cooldown_until,omitempty"`
	LastUsedAt             *time.Time                   `json:"last_used_at,omitempty"`
	LastCheckedAt          *time.Time                   `json:"last_checked_at,omitempty"`
	CreatedAt              time.Time                    `json:"created_at"`
	UpdatedAt              time.Time                    `json:"updated_at"`
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
	ProjectScope       string     `json:"project_scope,omitempty"`
	ProjectIDs         []string   `json:"project_ids,omitempty" gorm:"serializer:json"`
	Tags               []string   `json:"tags,omitempty" gorm:"serializer:json"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

type ModelRoutePolicyRoute struct {
	RouteID      string `json:"route_id"`
	Weight       int    `json:"weight"`
	QualityScore int    `json:"quality_score"`
	CostScore    int    `json:"cost_score"`
}

type ModelRoutePolicy struct {
	Strategy string                  `json:"strategy"`
	Routes   []ModelRoutePolicyRoute `json:"routes"`
}

type Usage struct {
	PromptTokens             int64       `json:"prompt_tokens"`
	CachedInputTokens        int64       `json:"cached_input_tokens,omitempty"`
	CacheWriteInputTokens    int64       `json:"cache_write_input_tokens,omitempty"`
	CacheWrite5mInputTokens  int64       `json:"cache_write_5m_input_tokens,omitempty"`
	CacheWrite1hInputTokens  int64       `json:"cache_write_1h_input_tokens,omitempty"`
	InputAudioTokens         int64       `json:"input_audio_tokens,omitempty"`
	CompletionTokens         int64       `json:"completion_tokens"`
	ReasoningOutputTokens    int64       `json:"reasoning_output_tokens,omitempty"`
	OutputAudioTokens        int64       `json:"output_audio_tokens,omitempty"`
	AcceptedPredictionTokens int64       `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int64       `json:"rejected_prediction_tokens,omitempty"`
	TotalTokens              int64       `json:"total_tokens"`
	InputCostUSD             float64     `json:"input_cost_usd,omitempty"`
	CacheReadCostUSD         float64     `json:"cache_read_cost_usd,omitempty"`
	CacheWriteCostUSD        float64     `json:"cache_write_cost_usd,omitempty"`
	OutputCostUSD            float64     `json:"output_cost_usd,omitempty"`
	CostUSD                  float64     `json:"estimated_cost_usd,omitempty"`
	ProviderCostUSD          float64     `json:"-"`
	UpstreamRequestID        string      `json:"upstream_request_id,omitempty"`
	ServedModel              string      `json:"served_model,omitempty"`
	ModelETag                string      `json:"model_etag,omitempty"`
	Transport                string      `json:"transport,omitempty"`
	ResponseHeaders          http.Header `json:"-"`
	// RateLimitTokens is the total metered across every invoked failover attempt.
	// It is internal quota state: billing, request logs and provider attribution
	// continue to use the usage reported by the final route only.
	RateLimitTokens int64 `json:"-" gorm:"-"`
}

type UsageRecord struct {
	ID                       string    `json:"id" gorm:"primaryKey"`
	RequestID                string    `json:"request_id" gorm:"index"`
	ProjectID                string    `json:"project_id" gorm:"index;index:idx_usage_records_project_created,priority:1"`
	APIKeyID                 string    `json:"api_key_id" gorm:"index"`
	AttributedUserID         string    `json:"attributed_user_id,omitempty" gorm:"index"`
	ModelName                string    `json:"model" gorm:"index"`
	ProviderID               string    `json:"provider_id" gorm:"index"`
	ProviderResourceID       string    `json:"provider_resource_id,omitempty" gorm:"index"`
	InputTokens              int64     `json:"input_tokens"`
	CachedInputTokens        int64     `json:"cached_input_tokens,omitempty"`
	CacheWriteTokens         int64     `json:"cache_write_input_tokens,omitempty"`
	CacheWrite5mTokens       int64     `json:"cache_write_5m_input_tokens,omitempty"`
	CacheWrite1hTokens       int64     `json:"cache_write_1h_input_tokens,omitempty"`
	InputAudioTokens         int64     `json:"input_audio_tokens,omitempty"`
	OutputTokens             int64     `json:"output_tokens"`
	ReasoningTokens          int64     `json:"reasoning_output_tokens,omitempty"`
	OutputAudioTokens        int64     `json:"output_audio_tokens,omitempty"`
	AcceptedPredictionTokens int64     `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int64     `json:"rejected_prediction_tokens,omitempty"`
	TotalTokens              int64     `json:"total_tokens"`
	InputCostUSD             float64   `json:"input_cost_usd,omitempty"`
	CacheReadCostUSD         float64   `json:"cache_read_cost_usd,omitempty"`
	CacheWriteCostUSD        float64   `json:"cache_write_cost_usd,omitempty"`
	OutputCostUSD            float64   `json:"output_cost_usd,omitempty"`
	CostUSD                  float64   `json:"estimated_cost_usd"`
	ProviderCostUSD          float64   `json:"provider_cost_usd,omitempty"`
	CreatedAt                time.Time `json:"created_at" gorm:"index;index:idx_usage_records_project_created,priority:2"`
}

type RequestLog struct {
	ID                       string    `json:"id" gorm:"primaryKey"`
	RequestID                string    `json:"request_id" gorm:"index"`
	CommitSequence           int64     `json:"-" gorm:"not null;default:0"`
	ProjectID                string    `json:"project_id" gorm:"index;index:idx_request_logs_project_created,priority:1"`
	APIKeyID                 string    `json:"api_key_id" gorm:"index;index:idx_request_logs_api_key_created,priority:1"`
	AttributedUserID         string    `json:"attributed_user_id,omitempty" gorm:"index"`
	ModelName                string    `json:"model" gorm:"index"`
	ProviderID               string    `json:"provider_id,omitempty" gorm:"index"`
	ProviderResourceID       string    `json:"provider_resource_id,omitempty" gorm:"index"`
	ProviderModel            string    `json:"provider_model,omitempty"`
	RoutingPolicyID          string    `json:"routing_policy_id,omitempty" gorm:"index"`
	RoutingPolicyScope       string    `json:"routing_policy_scope,omitempty" gorm:"index"`
	RoutingPolicyPriority    int       `json:"routing_policy_priority,omitempty"`
	UpstreamRequestID        string    `json:"upstream_request_id,omitempty"`
	ServedModel              string    `json:"served_model,omitempty"`
	ModelETag                string    `json:"model_etag,omitempty"`
	Transport                string    `json:"transport,omitempty"`
	StatusCode               int       `json:"status_code"`
	ErrorCode                string    `json:"error_code,omitempty"`
	LatencyMS                int64     `json:"latency_ms"`
	ClientIP                 string    `json:"client_ip,omitempty"`
	UserAgent                string    `json:"user_agent,omitempty"`
	CreatedAt                time.Time `json:"created_at" gorm:"index:idx_request_logs_created_at;index:idx_request_logs_project_created,priority:2;index:idx_request_logs_api_key_created,priority:2"`
	InputTokens              int64     `json:"input_tokens,omitempty" gorm:"-"`
	CachedInputTokens        int64     `json:"cached_input_tokens,omitempty" gorm:"-"`
	CacheWriteTokens         int64     `json:"cache_write_input_tokens,omitempty" gorm:"-"`
	CacheWrite5mTokens       int64     `json:"cache_write_5m_input_tokens,omitempty" gorm:"-"`
	CacheWrite1hTokens       int64     `json:"cache_write_1h_input_tokens,omitempty" gorm:"-"`
	InputAudioTokens         int64     `json:"input_audio_tokens,omitempty" gorm:"-"`
	OutputTokens             int64     `json:"output_tokens,omitempty" gorm:"-"`
	ReasoningTokens          int64     `json:"reasoning_output_tokens,omitempty" gorm:"-"`
	OutputAudioTokens        int64     `json:"output_audio_tokens,omitempty" gorm:"-"`
	AcceptedPredictionTokens int64     `json:"accepted_prediction_tokens,omitempty" gorm:"-"`
	RejectedPredictionTokens int64     `json:"rejected_prediction_tokens,omitempty" gorm:"-"`
	TotalTokens              int64     `json:"total_tokens,omitempty" gorm:"-"`
	InputCostUSD             float64   `json:"input_cost_usd,omitempty" gorm:"-"`
	CacheReadCostUSD         float64   `json:"cache_read_cost_usd,omitempty" gorm:"-"`
	CacheWriteCostUSD        float64   `json:"cache_write_cost_usd,omitempty" gorm:"-"`
	OutputCostUSD            float64   `json:"output_cost_usd,omitempty" gorm:"-"`
	EstimatedCostUSD         float64   `json:"estimated_cost_usd,omitempty" gorm:"-"`
	ProviderCostUSD          float64   `json:"provider_cost_usd,omitempty" gorm:"-"`
	UsageRecordCount         int64     `json:"usage_record_count,omitempty" gorm:"-"`
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

type ImageJob struct {
	ID                                                          string     `json:"id" gorm:"primaryKey"`
	ProjectID                                                   string     `json:"project_id" gorm:"index"`
	APIKeyID                                                    string     `json:"api_key_id" gorm:"index"`
	AttributedUserID                                            string     `json:"attributed_user_id,omitempty" gorm:"index"`
	RequestID                                                   string     `json:"request_id,omitempty" gorm:"index"`
	UserQuotaEnabled                                            bool       `json:"-"`
	UserMinuteRequestHeld                                       bool       `json:"-"`
	UserTokenLimitBucket                                        string     `json:"-"`
	RedisBillingAdmitted, RedisKeyLeaseHeld, RedisUserLeaseHeld bool       `json:"-"`
	TokenLimitBucket                                            string     `json:"-"`
	MinuteRequestHeld                                           bool       `json:"-"`
	ReservedTokens                                              int64      `json:"-"`
	AdmittedAt                                                  *time.Time `json:"-"`
	Status                                                      string     `json:"status" gorm:"index"`
	Model                                                       string     `json:"model"`
	Action                                                      string     `json:"action"`
	Count                                                       int        `json:"n,omitempty" gorm:"-"`
	PromptCiphertext                                            string     `json:"-" gorm:"type:text"`
	Prompt                                                      string     `json:"prompt,omitempty" gorm:"-"`
	RevisedPromptCiphertext                                     string     `json:"-" gorm:"type:text"`
	RevisedPrompt                                               string     `json:"revised_prompt,omitempty" gorm:"-"`
	Quality                                                     string     `json:"quality,omitempty"`
	Size                                                        string     `json:"size,omitempty"`
	ProviderID                                                  string     `json:"provider_id,omitempty" gorm:"index"`
	ProviderResourceID                                          string     `json:"provider_resource_id,omitempty" gorm:"index"`
	ProviderModel                                               string     `json:"provider_model,omitempty"`
	UpstreamRequestID                                           string     `json:"upstream_request_id,omitempty"`
	InputTokens                                                 int64      `json:"input_tokens,omitempty"`
	CachedInputTokens                                           int64      `json:"cached_input_tokens,omitempty"`
	OutputTokens                                                int64      `json:"output_tokens,omitempty"`
	TotalTokens                                                 int64      `json:"total_tokens,omitempty"`
	ErrorCode                                                   string     `json:"error_code,omitempty"`
	ErrorMessage                                                string     `json:"error_message,omitempty"`
	CreatedAt                                                   time.Time  `json:"created_at"`
	StartedAt                                                   *time.Time `json:"started_at,omitempty"`
	CompletedAt                                                 *time.Time `json:"completed_at,omitempty"`
}

const (
	responseJobStatusQueued    = "queued"
	responseJobStatusRunning   = "running"
	responseJobStatusSucceeded = "succeeded"
	responseJobStatusFailed    = "failed"
	responseJobStatusCancelled = "cancelled"
	responseJobStatusExpired   = "expired"

	responseJobPhaseQueued     = "queued"
	responseJobPhaseClaimed    = "claimed"
	responseJobPhaseAdmitted   = "admitted"
	responseJobPhaseDispatched = "dispatched"
)

// ResponseJob is a durable, gateway-owned execution record for an OpenAI
// Responses request submitted with background=true. Request and result content is
// stored only in the encrypted columns; the plaintext fields are transient values
// populated after authenticated reads.
type ResponseJob struct {
	ID                                                          string     `json:"id" gorm:"primaryKey"`
	ProjectID                                                   string     `json:"project_id" gorm:"index"`
	APIKeyID                                                    string     `json:"api_key_id" gorm:"index"`
	AttributedUserID                                            string     `json:"attributed_user_id,omitempty" gorm:"index"`
	UserQuotaEnabled                                            bool       `json:"-"`
	UserMinuteRequestHeld                                       bool       `json:"-"`
	UserTokenLimitBucket                                        string     `json:"-"`
	RedisBillingAdmitted, RedisKeyLeaseHeld, RedisUserLeaseHeld bool       `json:"-"`
	RequestID                                                   string     `json:"request_id,omitempty" gorm:"index"`
	TokenLimitBucket                                            string     `json:"-"`
	MinuteRequestHeld                                           bool       `json:"-"`
	ReservedTokens                                              int64      `json:"-"`
	AdmittedAt                                                  *time.Time `json:"-"`
	Status                                                      string     `json:"status" gorm:"index:idx_response_job_claim,priority:1;index:idx_response_job_lease,priority:1"`
	Phase                                                       string     `json:"phase" gorm:"index"`
	Model                                                       string     `json:"model" gorm:"index"`
	RequestCiphertext                                           string     `json:"-" gorm:"type:text"`
	ResultCiphertext                                            string     `json:"-" gorm:"type:text"`
	RequestJSON                                                 []byte     `json:"-" gorm:"-"`
	ResultJSON                                                  []byte     `json:"-" gorm:"-"`
	ClientIP                                                    string     `json:"-" gorm:"-"`
	UserAgent                                                   string     `json:"-" gorm:"-"`
	LeaseOwner                                                  string     `json:"-" gorm:"index"`
	LeaseEpoch                                                  int64      `json:"-"`
	LeaseExpiresAt                                              *time.Time `json:"-" gorm:"index:idx_response_job_lease,priority:2"`
	CancelRequestedAt                                           *time.Time `json:"-" gorm:"index"`
	ProviderID                                                  string     `json:"provider_id,omitempty" gorm:"index"`
	ProviderResourceID                                          string     `json:"provider_resource_id,omitempty" gorm:"index"`
	ProviderModel                                               string     `json:"provider_model,omitempty"`
	UpstreamRequestID                                           string     `json:"upstream_request_id,omitempty"`
	UpstreamResponseID                                          string     `json:"upstream_response_id,omitempty"`
	ErrorCode                                                   string     `json:"error_code,omitempty"`
	ErrorMessage                                                string     `json:"error_message,omitempty"`
	CreatedAt                                                   time.Time  `json:"created_at" gorm:"index:idx_response_job_claim,priority:2"`
	StartedAt                                                   *time.Time `json:"started_at,omitempty"`
	CompletedAt                                                 *time.Time `json:"completed_at,omitempty"`
	ExpiresAt                                                   *time.Time `json:"expires_at,omitempty" gorm:"index"`
	UpdatedAt                                                   time.Time  `json:"updated_at"`
}

// ResponseJobEvent is the append-only audit trail for every durable state
// transition. Details are deliberately bounded operational codes, never request or
// response content.
type ResponseJobEvent struct {
	ID         string    `json:"id" gorm:"primaryKey"`
	JobID      string    `json:"job_id" gorm:"index"`
	FromStatus string    `json:"from_status,omitempty"`
	ToStatus   string    `json:"to_status"`
	ReasonCode string    `json:"reason_code,omitempty"`
	Actor      string    `json:"actor"`
	CreatedAt  time.Time `json:"created_at" gorm:"index"`
}

type ImageAsset struct {
	ID           string    `json:"id" gorm:"primaryKey"`
	JobID        string    `json:"job_id" gorm:"index"`
	ProjectID    string    `json:"project_id" gorm:"index"`
	Role         string    `json:"role" gorm:"index"`
	RelativePath string    `json:"-"`
	ContentType  string    `json:"content_type"`
	ByteSize     int64     `json:"bytes"`
	SHA256       string    `json:"sha256"`
	CreatedAt    time.Time `json:"created_at"`
}

type RouteAttemptLog struct {
	ID                       string  `json:"id" gorm:"primaryKey"`
	RequestID                string  `json:"request_id" gorm:"index"`
	AttemptIndex             int     `json:"attempt_index"`
	RouteID                  string  `json:"route_id,omitempty" gorm:"index;index:idx_route_attempt_adaptive,priority:1"`
	ProviderID               string  `json:"provider_id,omitempty" gorm:"index"`
	ProviderResourceID       string  `json:"provider_resource_id,omitempty" gorm:"index"`
	ProviderModel            string  `json:"provider_model,omitempty"`
	StatusCode               int     `json:"status_code"`
	UpstreamStatus           int     `json:"upstream_status,omitempty"`
	ErrorCode                string  `json:"error_code,omitempty"`
	ErrorMessage             string  `json:"error_message,omitempty"`
	Invoked                  bool    `json:"invoked" gorm:"index;index:idx_route_attempt_adaptive,priority:2"`
	LatencyMS                int64   `json:"latency_ms,omitempty"`
	ServedModel              string  `json:"served_model,omitempty"`
	UpstreamRequestID        string  `json:"upstream_request_id,omitempty"`
	Transport                string  `json:"transport,omitempty"`
	InputTokens              int64   `json:"input_tokens,omitempty"`
	CachedInputTokens        int64   `json:"cached_input_tokens,omitempty"`
	CacheWriteTokens         int64   `json:"cache_write_input_tokens,omitempty"`
	InputAudioTokens         int64   `json:"input_audio_tokens,omitempty"`
	OutputTokens             int64   `json:"output_tokens,omitempty"`
	ReasoningTokens          int64   `json:"reasoning_output_tokens,omitempty"`
	OutputAudioTokens        int64   `json:"output_audio_tokens,omitempty"`
	AcceptedPredictionTokens int64   `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int64   `json:"rejected_prediction_tokens,omitempty"`
	TotalTokens              int64   `json:"total_tokens,omitempty"`
	CostUSD                  float64 `json:"estimated_cost_usd,omitempty"`
	// StartedAt and EndedAt record when this candidate was actually invoked. CreatedAt
	// cannot substitute: every attempt of a request is written in one batch and so
	// shares a single CreatedAt.
	StartedAt time.Time `json:"started_at,omitzero"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	CreatedAt time.Time `json:"created_at" gorm:"index:idx_route_attempt_adaptive,priority:3"`
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
	ID                      string            `json:"id" gorm:"primaryKey"`
	Kind                    string            `json:"kind" gorm:"primaryKey;index"`
	Name                    string            `json:"name"`
	Description             string            `json:"description,omitempty"`
	Status                  string            `json:"status"`
	Fields                  map[string]any    `json:"fields,omitempty" gorm:"serializer:json"`
	CurrentUsage            *QuotaPolicyUsage `json:"current_usage,omitempty" gorm:"-"`
	RoutingPolicyBindingKey *string           `json:"-" gorm:"uniqueIndex:idx_admin_resource_routing_policy_binding"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
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
	TeamIDs      []string   `json:"team_ids,omitempty" gorm:"serializer:json"`
	Status       string     `json:"status"`
	PasswordHash string     `json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

func normalizedTeamIDs(primary string, additional []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(additional)+1)
	for _, teamID := range append([]string{primary}, additional...) {
		teamID = strings.TrimSpace(teamID)
		if teamID == "" || seen[teamID] {
			continue
		}
		seen[teamID] = true
		result = append(result, teamID)
	}
	return result
}

func userHasTeam(user AdminUser, teamID string) bool {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return false
	}
	for _, candidate := range normalizedTeamIDs(user.TeamID, user.TeamIDs) {
		if candidate == teamID {
			return true
		}
	}
	return false
}

func equalStringSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
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

// BootstrapCredential retains a generated first-run credential only long
// enough for an operator with database/config access to retrieve it. The
// plaintext never enters the database.
type BootstrapCredential struct {
	Name       string    `json:"-" gorm:"primaryKey"`
	Ciphertext string    `json:"-" gorm:"type:text"`
	CreatedAt  time.Time `json:"-"`
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
	Role       string `json:"role"`
	Content    any    `json:"content"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolCalls  any    `json:"tool_calls,omitempty"`

	// The OpenAI Chat Completions schema has no field for a provider's chain of
	// thought, so the fields below are TokenHub extensions. ReasoningContent
	// follows the convention DeepSeek introduced; the signature fields carry the
	// opaque continuation blobs Anthropic and Gemini require to be echoed back
	// verbatim on the next turn of a multi-step tool exchange.
	//
	// Signatures are prefixed with the provider that minted them so one
	// provider's blob is never replayed to another. A missing or foreign
	// signature degrades to dropping the reasoning block rather than failing the
	// request.
	ReasoningContent         string `json:"reasoning_content,omitempty"`
	ReasoningSignature       string `json:"reasoning_signature,omitempty"`
	RedactedReasoningContent string `json:"redacted_reasoning_content,omitempty"`
	raw                      map[string]json.RawMessage
}

type ChatCompletionRequest struct {
	Model             string         `json:"model"`
	Messages          []ChatMessage  `json:"messages"`
	Stream            bool           `json:"stream,omitempty"`
	StreamOptions     map[string]any `json:"stream_options,omitempty"`
	MaxTokens         int            `json:"max_tokens,omitempty"`
	Temperature       *float64       `json:"temperature,omitempty"`
	TopP              *float64       `json:"top_p,omitempty"`
	PresencePenalty   *float64       `json:"presence_penalty,omitempty"`
	FrequencyPenalty  *float64       `json:"frequency_penalty,omitempty"`
	MinP              *float64       `json:"min_p,omitempty"`
	TopK              *int           `json:"top_k,omitempty"`
	Stop              any            `json:"stop,omitempty"`
	Tools             any            `json:"tools,omitempty"`
	ToolChoice        any            `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool          `json:"parallel_tool_calls,omitempty"`
	ResponseFormat    any            `json:"response_format,omitempty"`
	ReasoningEffort   *string        `json:"reasoning_effort,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	// PromptCacheKey and User are hints upstreams use to route requests sharing a
	// prefix to the same cache shard. The gateway must forward them verbatim, and
	// they double as session affinity identifier sources.
	//
	// Typed as any rather than string: these fields were previously absent from the
	// struct, so non-string values were silently ignored. Declaring them as string
	// would make requests like `{"user": 123}` fail with 400 at decode time, and the
	// gateway must not be stricter than the upstream. Affinity extraction only reads
	// string values; other types are forwarded for the upstream to judge.
	PromptCacheKey any `json:"prompt_cache_key,omitempty"`
	User           any `json:"user,omitempty"`
	raw            map[string]json.RawMessage
}

// UnmarshalJSON keeps provider-specific message fields so compatible upstreams
// can receive opaque continuation data without TokenHub needing to understand it.
func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	type messageAlias ChatMessage
	var decoded messageAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = ChatMessage(decoded)
	m.raw = raw
	return nil
}

func (m ChatMessage) MarshalJSON() ([]byte, error) {
	type messageAlias ChatMessage
	if m.raw == nil {
		return json.Marshal(messageAlias(m))
	}
	raw := cloneRawJSON(m.raw, 8)
	setOrDeleteRawJSONField(raw, "role", m.Role, m.Role != "")
	setOrDeleteRawJSONField(raw, "content", m.Content, m.Content != nil)
	setOrDeleteRawJSONField(raw, "name", m.Name, m.Name != "")
	setOrDeleteRawJSONField(raw, "tool_call_id", m.ToolCallID, m.ToolCallID != "")
	setOrDeleteRawJSONField(raw, "tool_calls", m.ToolCalls, m.ToolCalls != nil)
	setOrDeleteRawJSONField(raw, "reasoning_content", m.ReasoningContent, m.ReasoningContent != "")
	setOrDeleteRawJSONField(raw, "reasoning_signature", m.ReasoningSignature, m.ReasoningSignature != "")
	setOrDeleteRawJSONField(raw, "redacted_reasoning_content", m.RedactedReasoningContent, m.RedactedReasoningContent != "")
	return json.Marshal(raw)
}

// UnmarshalJSON keeps top-level provider extensions such as DeepSeek's thinking
// switch while typed fields remain available to routing and policy code.
func (r *ChatCompletionRequest) UnmarshalJSON(data []byte) error {
	type requestAlias ChatCompletionRequest
	var decoded requestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ChatCompletionRequest(decoded)
	r.raw = raw
	return nil
}

func (r ChatCompletionRequest) MarshalJSON() ([]byte, error) {
	type requestAlias ChatCompletionRequest
	if r.raw == nil {
		return json.Marshal(requestAlias(r))
	}
	raw := cloneRawJSON(r.raw, 16)
	setOrDeleteRawJSONField(raw, "model", r.Model, r.Model != "")
	setOrDeleteRawJSONField(raw, "messages", r.Messages, r.Messages != nil)
	setOrDeleteRawJSONField(raw, "stream", r.Stream, r.Stream)
	setOrDeleteRawJSONField(raw, "stream_options", r.StreamOptions, r.StreamOptions != nil)
	setOrDeleteRawJSONField(raw, "max_tokens", r.MaxTokens, r.MaxTokens != 0)
	setOrDeleteRawJSONField(raw, "temperature", r.Temperature, r.Temperature != nil)
	setOrDeleteRawJSONField(raw, "top_p", r.TopP, r.TopP != nil)
	setOrDeleteRawJSONField(raw, "presence_penalty", r.PresencePenalty, r.PresencePenalty != nil)
	setOrDeleteRawJSONField(raw, "frequency_penalty", r.FrequencyPenalty, r.FrequencyPenalty != nil)
	setOrDeleteRawJSONField(raw, "min_p", r.MinP, r.MinP != nil)
	setOrDeleteRawJSONField(raw, "top_k", r.TopK, r.TopK != nil)
	setOrDeleteRawJSONField(raw, "stop", r.Stop, r.Stop != nil)
	setOrDeleteRawJSONField(raw, "tools", r.Tools, r.Tools != nil)
	setOrDeleteRawJSONField(raw, "tool_choice", r.ToolChoice, r.ToolChoice != nil)
	setOrDeleteRawJSONField(raw, "parallel_tool_calls", r.ParallelToolCalls, r.ParallelToolCalls != nil)
	setOrDeleteRawJSONField(raw, "response_format", r.ResponseFormat, r.ResponseFormat != nil)
	setOrDeleteRawJSONField(raw, "reasoning_effort", r.ReasoningEffort, r.ReasoningEffort != nil)
	setOrDeleteRawJSONField(raw, "metadata", r.Metadata, r.Metadata != nil)
	setOrDeleteRawJSONField(raw, "prompt_cache_key", r.PromptCacheKey, r.PromptCacheKey != nil)
	setOrDeleteRawJSONField(raw, "user", r.User, r.User != nil)
	return json.Marshal(raw)
}

func (r ChatCompletionRequest) requestedMaxTokens() int64 {
	maximum := int64(r.MaxTokens)
	if raw := r.raw["max_completion_tokens"]; len(raw) > 0 {
		var compatibleMaximum int64
		if json.Unmarshal(raw, &compatibleMaximum) == nil && compatibleMaximum > maximum {
			maximum = compatibleMaximum
		}
	}
	return maximum
}

type PlaygroundChatResponse struct {
	Response  any                      `json:"response"`
	Route     PlaygroundRouteSummary   `json:"route"`
	Usage     Usage                    `json:"usage"`
	Attempts  []PlaygroundRouteAttempt `json:"attempts"`
	RequestID string                   `json:"request_id"`
}

type PlaygroundRouteSummary struct {
	RouteID           string  `json:"route_id,omitempty"`
	ProviderID        string  `json:"provider_id,omitempty"`
	ProviderName      string  `json:"provider_name,omitempty"`
	ResourceID        string  `json:"resource_id,omitempty"`
	ResourceName      string  `json:"resource_name,omitempty"`
	ProviderModel     string  `json:"provider_model,omitempty"`
	UpstreamRequestID string  `json:"upstream_request_id,omitempty"`
	ServedModel       string  `json:"served_model,omitempty"`
	ModelETag         string  `json:"model_etag,omitempty"`
	Transport         string  `json:"transport,omitempty"`
	Priority          int     `json:"priority,omitempty"`
	ResourcePriority  int     `json:"resource_priority,omitempty"`
	Weight            int     `json:"weight,omitempty"`
	QualityScore      int     `json:"quality_score,omitempty"`
	CostScore         int     `json:"cost_score,omitempty"`
	Strategy          string  `json:"strategy,omitempty"`
	EffectiveWeight   int     `json:"effective_weight,omitempty"`
	Samples           int64   `json:"samples,omitempty"`
	SuccessRate       float64 `json:"success_rate,omitempty"`
	LatencyMS         int64   `json:"latency_ms,omitempty"`
}

type PlaygroundRouteAttempt struct {
	Route          PlaygroundRouteSummary `json:"route,omitzero"`
	Status         int                    `json:"status"`
	UpstreamStatus int                    `json:"upstream_status,omitempty"`
	Code           string                 `json:"code,omitempty"`
	Error          string                 `json:"error,omitempty"`
	Invoked        bool                   `json:"invoked"`
	LatencyMS      int64                  `json:"latency_ms,omitempty"`
	Usage          Usage                  `json:"usage,omitzero"`
	StartedAt      time.Time              `json:"started_at,omitzero"`
	EndedAt        time.Time              `json:"ended_at,omitzero"`
}

type ResponsesRequest struct {
	Model        string              `json:"model"`
	Input        any                 `json:"input"`
	Stream       bool                `json:"stream,omitempty"`
	Background   bool                `json:"background,omitempty"`
	MaxTokens    int                 `json:"max_output_tokens,omitempty"`
	Temperature  *float64            `json:"temperature,omitempty"`
	Instructions string              `json:"instructions,omitempty"`
	Store        *bool               `json:"store,omitempty"`
	Reasoning    *ResponsesReasoning `json:"reasoning,omitempty"`
	ServiceTier  string              `json:"service_tier,omitempty"`
	raw          map[string]json.RawMessage
}

type ResponsesReasoning struct {
	Effort  *string `json:"effort,omitempty"`
	Mode    string  `json:"mode,omitempty"`
	Context string  `json:"context,omitempty"`
}

// UnmarshalJSON keeps fields TokenHub does not interpret so the Responses API
// remains a transparent protocol surface for tools and future request fields.
func (r *ResponsesRequest) UnmarshalJSON(data []byte) error {
	type requestAlias ResponsesRequest
	var decoded requestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ResponsesRequest(decoded)
	r.raw = raw
	return nil
}

func (r ResponsesRequest) MarshalJSON() ([]byte, error) {
	type requestAlias ResponsesRequest
	if r.raw == nil {
		return json.Marshal(requestAlias(r))
	}
	raw := cloneRawJSON(r.raw, 8)
	setRawJSONField(raw, "model", r.Model, r.Model != "")
	setRawJSONField(raw, "input", r.Input, r.Input != nil)
	setRawJSONField(raw, "stream", r.Stream, true)
	background, backgroundPresent := raw["background"]
	if !backgroundPresent || r.Background || strings.TrimSpace(string(background)) != "null" {
		setRawJSONField(raw, "background", r.Background, r.Background || backgroundPresent)
	}
	setRawJSONField(raw, "max_output_tokens", r.MaxTokens, r.MaxTokens != 0)
	setRawJSONField(raw, "temperature", r.Temperature, r.Temperature != nil)
	setRawJSONField(raw, "instructions", r.Instructions, r.Instructions != "")
	setRawJSONField(raw, "store", r.Store, r.Store != nil)
	if r.Reasoning != nil {
		setResponsesReasoningField(raw, *r.Reasoning)
	} else {
		delete(raw, "reasoning")
	}
	setRawJSONField(raw, "service_tier", r.ServiceTier, r.ServiceTier != "")
	return json.Marshal(raw)
}

func setResponsesReasoningField(raw map[string]json.RawMessage, reasoning ResponsesReasoning) {
	merged := map[string]any{}
	if existing, ok := raw["reasoning"]; ok {
		_ = json.Unmarshal(existing, &merged)
	}
	if reasoning.Effort != nil {
		merged["effort"] = *reasoning.Effort
	} else {
		delete(merged, "effort")
	}
	if reasoning.Mode != "" {
		merged["mode"] = reasoning.Mode
	}
	if reasoning.Context != "" {
		merged["context"] = reasoning.Context
	}
	if len(merged) == 0 {
		delete(raw, "reasoning")
		return
	}
	if encoded, err := json.Marshal(merged); err == nil {
		raw["reasoning"] = encoded
	}
}

func setRawJSONField(raw map[string]json.RawMessage, key string, value any, present bool) {
	if !present {
		return
	}
	encoded, err := json.Marshal(value)
	if err == nil {
		raw[key] = encoded
	}
}

func setOrDeleteRawJSONField(raw map[string]json.RawMessage, key string, value any, present bool) {
	if !present {
		delete(raw, key)
		return
	}
	setRawJSONField(raw, key, value, true)
}

func cloneRawJSON(source map[string]json.RawMessage, extra int) map[string]json.RawMessage {
	cloned := make(map[string]json.RawMessage, len(source)+extra)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
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
	Runtime       RouteRuntimeStats
}

type RouteRuntimeStats struct {
	Samples         int64   `json:"samples,omitempty"`
	SuccessRate     float64 `json:"success_rate,omitempty"`
	LatencyMS       int64   `json:"latency_ms,omitempty"`
	EffectiveWeight int     `json:"effective_weight,omitempty"`
}

type RouteExplainStep struct {
	RouteID          string   `json:"route_id"`
	ProviderID       string   `json:"provider_id"`
	ResourceID       string   `json:"resource_id,omitempty"`
	ProviderModel    string   `json:"provider_model"`
	Priority         int      `json:"priority"`
	ResourcePriority int      `json:"resource_priority"`
	Weight           int      `json:"weight"`
	QualityScore     int      `json:"quality_score,omitempty"`
	CostScore        int      `json:"cost_score,omitempty"`
	Strategy         string   `json:"strategy"`
	ProjectScope     string   `json:"project_scope"`
	ProjectIDs       []string `json:"project_ids,omitempty"`
	EffectiveWeight  int      `json:"effective_weight"`
	Samples          int64    `json:"samples,omitempty"`
	SuccessRate      float64  `json:"success_rate,omitempty"`
	LatencyMS        int64    `json:"latency_ms,omitempty"`
	Status           string   `json:"status"`
}

type RouteAttempt struct {
	Selection RouteSelection `json:"selection"`
	// Status is what the caller was told. UpstreamStatus is what the provider
	// actually answered, which the mapping to Status deliberately does not
	// preserve — an upstream 401 is reported as 502 so a caller does not read it
	// as their own key being rejected. Diagnosing a route needs both.
	Status         int    `json:"status"`
	UpstreamStatus int    `json:"upstream_status,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	Error          string `json:"error,omitempty"`
	// Invoked reports that this candidate entered the invocation path, not that a
	// request necessarily reached the upstream: acquiring credentials or resolving an
	// adapter can still fail first. It is the boundary that distinguishes a candidate
	// that was tried from one skipped for lack of capacity.
	Invoked   bool  `json:"invoked"`
	LatencyMS int64 `json:"latency_ms,omitempty"`
	// Usage is what this candidate alone consumed, priced with the requested model.
	// A failed attempt keeps whatever the upstream reported before failing: those
	// tokens were billed regardless of the request failing over afterwards.
	//
	// Only invoked attempts have usage. A candidate skipped because capacity could
	// not be acquired never reached a provider.
	Usage Usage `json:"usage,omitzero"`
	// StartedAt and EndedAt bound the upstream invocation. They are zero for a
	// candidate that was never invoked.
	StartedAt time.Time `json:"started_at,omitzero"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
}

type RoutedCall struct {
	Call     CallContext
	Routes   []RouteSelection
	Affinity *RequestAffinity
}

type CallContext struct {
	RequestID             string
	Project               Project
	Key                   APIKey
	Model                 Model
	RoutingPolicyID       string
	RoutingPolicyScope    string
	RoutingPolicyPriority int
	// StartedAt is the database clock reading taken when the call was admitted:
	// StartCall derives the quota buckets and the lease expiry from that reading
	// so every replica agrees on them, and reports it here for callers that want
	// the admission timestamp. It is *not* a valid reference for measuring how
	// long the call ran — no production path reads it for that any more.
	StartedAt time.Time
	// measuredAt is the local reference used to measure how long the call ran.
	// It is deliberately separate from StartedAt: on PostgreSQL deployments the
	// database and the application usually run on different hosts, and any clock
	// skew between them would otherwise land directly in request_logs.latency_ms
	// (a database host running four minutes ahead produced latencies near
	// -240000ms). time.Now keeps its monotonic reading here, so the measurement
	// also survives wall-clock adjustments on this host.
	measuredAt time.Time
	// RateLimitHeaders is calculated atomically with minute-bucket admission so
	// every compatible HTTP surface reports the same effective limits.
	RateLimitHeaders  map[string]string
	TokenLimitBucket  string
	MinuteRequestHeld bool
	ReservedTokens    int64
	// UserQuotaID identifies the durable aggregate bucket when a user-scoped
	// quota policy applies. It is intentionally internal and never exposed to
	// gateway clients.
	UserQuotaID                                                 string
	UserQuotaEnabled                                            bool
	UserMinuteRequestHeld                                       bool
	UserTokenLimitBucket                                        string
	UserQuotaLimits                                             QuotaLimits
	RedisBillingAdmitted, RedisKeyLeaseHeld, RedisUserLeaseHeld bool
	// AttributedUserID is captured at admission so settlement remains attached
	// to the owner who actually started the request, even if the key is transferred.
	AttributedUserID string
	// StreamOutputCommitted keeps the reservation when a stream delivered data but
	// ended before an authoritative usage event was received.
	StreamOutputCommitted bool
	// Stream records whether the client asked for a streamed response. It only
	// labels observability output and never influences routing.
	Stream bool
	// GatewayAuthMetadata carries plugin-provided authentication context
	// annotations. Plugins may enrich downstream gateway hooks with these values,
	// but they cannot change core authentication facts such as project, key,
	// model, or stream mode.
	GatewayAuthMetadata map[string]json.RawMessage
	// RouteAttempts carries the per-candidate outcomes for observability output.
	// It is filled from the completion's Attempts just before FinishCall and never
	// influences routing.
	RouteAttempts []RouteAttempt
	// FirstByteAt records when the first response byte reached the client on a
	// streamed request. Zero means no byte was ever written. Like Stream, it only
	// labels observability output and never influences routing.
	FirstByteAt time.Time
	// StreamFailed records that a streamed request ended in failure after the
	// response started. It replaces the status-code projection for interruption
	// classification: once a stream commits, the HTTP status cannot change, so
	// the failure fact must travel as a flag rather than a derived status.
	StreamFailed   bool
	Affinity       *RequestAffinity
	requestContext context.Context
}

// measuredStart reports when the call began, on the clock its duration is
// measured against. Anything paired with a timestamp this process stamps — an
// elapsed time, a trace span end — has to start here rather than at StartedAt,
// which the database clock produced.
func (c CallContext) measuredStart() time.Time {
	if !c.measuredAt.IsZero() {
		return c.measuredAt
	}
	// Contexts assembled by hand rather than by StartCall carry only StartedAt.
	// Falling back to it keeps their reporting working; callers clamp the result.
	return c.StartedAt
}

// elapsed reports how long the call has been running. It never returns a
// negative duration: callers persist the result as latency_ms, and a negative
// latency is always a clock artefact rather than a real measurement.
func (c CallContext) elapsed() time.Duration {
	reference := c.measuredStart()
	if reference.IsZero() {
		return 0
	}
	if elapsed := time.Since(reference); elapsed > 0 {
		return elapsed
	}
	return 0
}

// latencyMillis converts an interval into the non-negative value stored in a
// latency_ms column. Intervals derived from a persisted timestamp can come out
// negative when the replica that wrote it ran ahead of the replica reading it,
// and a negative latency is never a real measurement.
func latencyMillis(interval time.Duration) int64 {
	if interval <= 0 {
		return 0
	}
	return interval.Milliseconds()
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
