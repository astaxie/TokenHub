package server

import (
	"context"
	"errors"
	"net/http"

	"tokenhub/backend/internal/analytics"
)

// analyticsStoreAdapter keeps the W07 application boundary usable while the
// GORM implementation remains in server until the adapter phase.
type analyticsStoreAdapter struct{ store Store }

type analyticsContextStore interface {
	CreateAnalyticsCredentialContext(context.Context, AnalyticsCredential, string) (AnalyticsCredential, string, error)
	ListAnalyticsCredentialsContext(context.Context) ([]AnalyticsCredential, error)
	RevokeAnalyticsCredentialContext(context.Context, string) (AnalyticsCredential, error)
	ValidateAnalyticsCredentialContext(context.Context, string) (AnalyticsCredential, error)
}

func newAnalyticsStoreAdapter(store Store) *analyticsStoreAdapter {
	return &analyticsStoreAdapter{store: store}
}

func (a *analyticsStoreAdapter) CreateCredential(ctx context.Context, credential analytics.Credential, rawSecret string) (analytics.Credential, string, error) {
	projection := AnalyticsCredential{
		Name: credential.Name, ScopeType: credential.ScopeType,
		ProjectID: credential.ProjectID, CreatedBy: credential.CreatedBy, ExpiresAt: credential.ExpiresAt,
	}
	var created AnalyticsCredential
	var token string
	var err error
	if contextStore, ok := a.store.(analyticsContextStore); ok {
		created, token, err = contextStore.CreateAnalyticsCredentialContext(ctx, projection, rawSecret)
	} else {
		if err := ctx.Err(); err != nil {
			return analytics.Credential{}, "", err
		}
		created, token, err = a.store.CreateAnalyticsCredential(projection, rawSecret)
	}
	if err != nil {
		return analytics.Credential{}, "", err
	}
	return analyticsCredentialFromServer(created), token, nil
}

func (a *analyticsStoreAdapter) ListCredentials(ctx context.Context) ([]analytics.Credential, error) {
	var credentials []AnalyticsCredential
	var err error
	if contextStore, ok := a.store.(analyticsContextStore); ok {
		credentials, err = contextStore.ListAnalyticsCredentialsContext(ctx)
	} else {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		credentials = a.store.ListAnalyticsCredentials()
	}
	if err != nil {
		return nil, err
	}
	result := make([]analytics.Credential, len(credentials))
	for i, credential := range credentials {
		result[i] = analyticsCredentialFromServer(credential)
	}
	return result, nil
}

func (a *analyticsStoreAdapter) RevokeCredential(ctx context.Context, id string) (analytics.Credential, error) {
	var credential AnalyticsCredential
	var err error
	if contextStore, ok := a.store.(analyticsContextStore); ok {
		credential, err = contextStore.RevokeAnalyticsCredentialContext(ctx, id)
	} else {
		if err := ctx.Err(); err != nil {
			return analytics.Credential{}, err
		}
		credential, err = a.store.RevokeAnalyticsCredential(id)
	}
	if err != nil {
		return analytics.Credential{}, err
	}
	return analyticsCredentialFromServer(credential), nil
}

func (a *analyticsStoreAdapter) ValidateCredential(ctx context.Context, rawSecret string) (analytics.Credential, error) {
	var credential AnalyticsCredential
	var err error
	if contextStore, ok := a.store.(analyticsContextStore); ok {
		credential, err = contextStore.ValidateAnalyticsCredentialContext(ctx, rawSecret)
	} else {
		if err := ctx.Err(); err != nil {
			return analytics.Credential{}, err
		}
		credential, err = a.store.ValidateAnalyticsCredential(rawSecret)
	}
	if err != nil {
		return analytics.Credential{}, err
	}
	return analyticsCredentialFromServer(credential), nil
}

func (a *analyticsStoreAdapter) QueryTokenCostPage(ctx context.Context, query analytics.Query) (analytics.Page, error) {
	page, err := a.store.QueryTokenCostPage(ctx, query)
	if err != nil {
		return analytics.Page{}, err
	}
	rows := make([]analytics.Row, len(page.Rows))
	for i, row := range page.Rows {
		rows[i] = analyticsRowFromServer(row)
	}
	return analytics.Page{Rows: rows, HasMore: page.HasMore, Checkpoint: page.Checkpoint}, nil
}

func analyticsCredentialFromServer(credential AnalyticsCredential) analytics.Credential {
	return analytics.Credential{
		ID: credential.ID, Name: credential.Name, KeyPrefix: credential.KeyPrefix, KeySuffix: credential.KeySuffix,
		ScopeType: credential.ScopeType, ProjectID: credential.ProjectID, Status: credential.Status,
		CreatedBy: credential.CreatedBy, ExpiresAt: credential.ExpiresAt, LastUsedAt: credential.LastUsedAt,
		CreatedAt: credential.CreatedAt, UpdatedAt: credential.UpdatedAt,
	}
}

func serverCredentialFromAnalytics(credential analytics.Credential) AnalyticsCredential {
	return AnalyticsCredential{
		ID: credential.ID, Name: credential.Name, KeyPrefix: credential.KeyPrefix, KeySuffix: credential.KeySuffix,
		ScopeType: credential.ScopeType, ProjectID: credential.ProjectID, Status: credential.Status,
		CreatedBy: credential.CreatedBy, ExpiresAt: credential.ExpiresAt, LastUsedAt: credential.LastUsedAt,
		CreatedAt: credential.CreatedAt, UpdatedAt: credential.UpdatedAt,
	}
}

func analyticsRowFromServer(row TokenCostRow) analytics.Row {
	return analytics.Row{
		DedupeKey: row.DedupeKey, Bucket: row.Bucket, RequestID: row.RequestID, OccurredAt: row.OccurredAt,
		ProjectID: row.ProjectID, UserID: row.UserID, APIKeyID: row.APIKeyID, ProviderID: row.ProviderID,
		Model: row.Model, Status: row.Status, StatusCode: row.StatusCode,
		Metrics: analytics.Metrics{
			RequestCount: row.Metrics.RequestCount, ErrorCount: row.Metrics.ErrorCount,
			InputTokens: row.Metrics.InputTokens, CachedInputTokens: row.Metrics.CachedInputTokens,
			CacheWriteTokens: row.Metrics.CacheWriteTokens, OutputTokens: row.Metrics.OutputTokens,
			ReasoningTokens: row.Metrics.ReasoningTokens, TotalTokens: row.Metrics.TotalTokens,
			EstimatedCostUSD: row.Metrics.EstimatedCostUSD,
		},
	}
}

func serverRowFromAnalytics(row analytics.Row) TokenCostRow {
	return TokenCostRow{
		DedupeKey: row.DedupeKey, Bucket: row.Bucket, RequestID: row.RequestID, OccurredAt: row.OccurredAt,
		ProjectID: row.ProjectID, UserID: row.UserID, APIKeyID: row.APIKeyID, ProviderID: row.ProviderID,
		Model: row.Model, Status: row.Status, StatusCode: row.StatusCode,
		Metrics: TokenCostMetrics{
			RequestCount: row.Metrics.RequestCount, ErrorCount: row.Metrics.ErrorCount,
			InputTokens: row.Metrics.InputTokens, CachedInputTokens: row.Metrics.CachedInputTokens,
			CacheWriteTokens: row.Metrics.CacheWriteTokens, OutputTokens: row.Metrics.OutputTokens,
			ReasoningTokens: row.Metrics.ReasoningTokens, TotalTokens: row.Metrics.TotalTokens,
			EstimatedCostUSD: row.Metrics.EstimatedCostUSD,
		},
	}
}

func analyticsHTTPError(err error) error {
	if err == nil {
		return err
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return err
	}
	status, code, message := http.StatusBadRequest, "invalid_analytics_query", err.Error()
	switch {
	case errors.Is(err, analytics.ErrMissingName):
		code, message = "invalid_analytics_credential", "Analytics credential name is required"
	case errors.Is(err, analytics.ErrInvalidScope), errors.Is(err, analytics.ErrMissingProject):
		code, message = "invalid_analytics_scope", "Analytics scope_type must be organization or project"
	case errors.Is(err, analytics.ErrInvalidExpiry):
		code, message = "invalid_analytics_expiry", "expires_at must be in the future"
	case errors.Is(err, analytics.ErrInvalidTimeRange):
		code, message = "invalid_analytics_time_range", "from must be earlier than to"
	case errors.Is(err, analytics.ErrInvalidGranularity):
		code, message = "invalid_analytics_granularity", "granularity must be request, none, hour, day, or month"
	case errors.Is(err, analytics.ErrInvalidLimit):
		code, message = "invalid_analytics_limit", "limit must be between 1 and 1000"
	case errors.Is(err, analytics.ErrInvalidGrouping):
		code, message = "invalid_analytics_grouping", "request granularity cannot be combined with group_by"
	case errors.Is(err, analytics.ErrInvalidSequence):
		code, message = "invalid_analytics_cursor", "analytics query sequence range is invalid"
	default:
		return err
	}
	return NewHTTPError(status, code, message)
}
