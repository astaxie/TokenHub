package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"time"

	"gorm.io/gorm"
)

const maxAPIKeyUsageBreakdownRows = 100

type APIKeyUsageQuery struct {
	Key              APIKey
	Project          Project
	From             time.Time
	To               time.Time
	IncludeProviders bool
}

type APIKeyUsageMetrics struct {
	RequestCount      int64   `json:"request_count"`
	ErrorCount        int64   `json:"error_count"`
	AverageLatencyMS  int64   `json:"average_latency_ms"`
	InputTokens       int64   `json:"input_tokens"`
	CachedInputTokens int64   `json:"cached_input_tokens"`
	CacheWriteTokens  int64   `json:"cache_write_input_tokens"`
	InputAudioTokens  int64   `json:"input_audio_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	ReasoningTokens   int64   `json:"reasoning_output_tokens"`
	OutputAudioTokens int64   `json:"output_audio_tokens"`
	AcceptedTokens    int64   `json:"accepted_prediction_tokens"`
	RejectedTokens    int64   `json:"rejected_prediction_tokens"`
	TotalTokens       int64   `json:"total_tokens"`
	EstimatedCostUSD  float64 `json:"estimated_cost_usd"`
}

type APIKeyUsagePoint struct {
	Bucket string `json:"date"`
	APIKeyUsageMetrics
}

type APIKeyUsageBreakdownRow struct {
	ID string `json:"id"`
	APIKeyUsageMetrics
	LastOccurredAt *time.Time `json:"last_occurred_at,omitempty"`
	StatusCode     int        `json:"status_code,omitempty"`
	ErrorCode      string     `json:"error_code,omitempty"`
	ResourceID     string     `json:"resource_id,omitempty"`
}

type APIKeyQuotaPeriod struct {
	Bucket string       `json:"bucket"`
	Usage  QuotaCounter `json:"usage"`
}

type APIKeyQuotaSnapshot struct {
	EffectiveLimits QuotaLimits       `json:"effective_limits"`
	Day             APIKeyQuotaPeriod `json:"day"`
	Month           APIKeyQuotaPeriod `json:"month"`
}

type APIKeyUsage struct {
	Summary    APIKeyUsageMetrics        `json:"summary"`
	Quota      APIKeyQuotaSnapshot       `json:"quota"`
	Timeseries []APIKeyUsagePoint        `json:"timeseries"`
	Models     []APIKeyUsageBreakdownRow `json:"models"`
	Errors     []APIKeyUsageBreakdownRow `json:"errors"`
	Providers  []APIKeyUsageBreakdownRow `json:"providers,omitempty"`
}

type apiKeyUsageDatabaseRow struct {
	ID                string
	Bucket            string
	ResourceID        string
	StatusCode        int
	ErrorCode         string
	LastOccurredAt    apiKeyUsageTime
	RequestCount      int64
	ErrorCount        int64
	AverageLatency    float64
	InputTokens       int64
	CachedInputTokens int64
	CacheWriteTokens  int64
	InputAudioTokens  int64
	OutputTokens      int64
	ReasoningTokens   int64
	OutputAudioTokens int64
	AcceptedTokens    int64
	RejectedTokens    int64
	TotalTokens       int64
	EstimatedCostUSD  float64
}

type apiKeyUsageTime struct {
	time.Time
	Valid bool
}

func (value apiKeyUsageTime) Value() (driver.Value, error) {
	if !value.Valid {
		return nil, nil
	}
	return value.Time, nil
}

func (value *apiKeyUsageTime) Scan(source any) error {
	if source == nil {
		*value = apiKeyUsageTime{}
		return nil
	}
	if parsed, ok := source.(time.Time); ok {
		*value = apiKeyUsageTime{Time: parsed.UTC(), Valid: true}
		return nil
	}
	var text string
	switch source := source.(type) {
	case string:
		text = source
	case []byte:
		text = string(source)
	default:
		return fmt.Errorf("unsupported database time type %T", source)
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, text); err == nil {
			*value = apiKeyUsageTime{Time: parsed.UTC(), Valid: true}
			return nil
		}
	}
	return fmt.Errorf("unsupported database time value %q", text)
}

func (s *GormStore) QueryAPIKeyUsage(ctx context.Context, query APIKeyUsageQuery) (APIKeyUsage, error) {
	var result APIKeyUsage
	readSnapshot := func(tx *gorm.DB) error {
		base := func() *gorm.DB {
			return s.apiKeyUsageBaseQuery(tx.WithContext(ctx), query)
		}
		var summary apiKeyUsageDatabaseRow
		if err := base().Select(apiKeyUsageMetricSelect()).Scan(&summary).Error; err != nil {
			return err
		}
		result.Summary = apiKeyUsageMetricsFromRow(summary)

		var err error
		result.Timeseries, err = s.queryAPIKeyUsageTimeseries(base(), query)
		if err != nil {
			return err
		}
		result.Models, err = queryAPIKeyUsageBreakdown(base(), "rl.model_name", "rl.model_name AS id", "request_count DESC, total_tokens DESC, id ASC")
		if err != nil {
			return err
		}
		result.Errors, err = queryAPIKeyUsageErrors(base())
		if err != nil {
			return err
		}
		if query.IncludeProviders {
			result.Providers, err = queryAPIKeyUsageBreakdown(
				base(),
				"rl.provider_id, rl.provider_resource_id",
				"rl.provider_id AS id, rl.provider_resource_id AS resource_id",
				"request_count DESC, error_count DESC, id ASC, resource_id ASC",
			)
			if err != nil {
				return err
			}
		}
		result.Quota, err = s.apiKeyQuotaSnapshot(tx.WithContext(ctx), query.Key, query.Project)
		return err
	}
	db := s.db.WithContext(ctx)
	var err error
	if s.dbDriver == "postgres" {
		err = db.Transaction(readSnapshot, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	} else {
		err = db.Transaction(readSnapshot)
	}
	return result, err
}

func (s *GormStore) apiKeyUsageBaseQuery(db *gorm.DB, query APIKeyUsageQuery) *gorm.DB {
	requestIDs := db.Model(&RequestLog{}).
		Select("request_id").
		Where("api_key_id = ? AND created_at >= ? AND created_at < ?", query.Key.ID, query.From, query.To)
	usage := db.Model(&UsageRecord{}).
		Select(`request_id,
			SUM(input_tokens) AS input_tokens,
			SUM(cached_input_tokens) AS cached_input_tokens,
			SUM(cache_write_tokens) AS cache_write_tokens,
			SUM(input_audio_tokens) AS input_audio_tokens,
			SUM(output_tokens) AS output_tokens,
			SUM(reasoning_tokens) AS reasoning_tokens,
			SUM(output_audio_tokens) AS output_audio_tokens,
			SUM(accepted_prediction_tokens) AS accepted_tokens,
			SUM(rejected_prediction_tokens) AS rejected_tokens,
			SUM(total_tokens) AS total_tokens,
			SUM(cost_usd) AS estimated_cost_usd`).
		Where("api_key_id = ? AND request_id IN (?)", query.Key.ID, requestIDs).
		Group("request_id")
	return db.Table("request_logs AS rl").
		Joins("LEFT JOIN (?) AS usage_agg ON usage_agg.request_id = rl.request_id", usage).
		Where("rl.api_key_id = ? AND rl.created_at >= ? AND rl.created_at < ?", query.Key.ID, query.From, query.To)
}

func apiKeyUsageMetricSelect() string {
	return `COUNT(*) AS request_count,
		COALESCE(SUM(CASE WHEN rl.status_code >= 400 THEN 1 ELSE 0 END), 0) AS error_count,
		COALESCE(AVG(rl.latency_ms), 0) AS average_latency,
		COALESCE(SUM(usage_agg.input_tokens), 0) AS input_tokens,
		COALESCE(SUM(usage_agg.cached_input_tokens), 0) AS cached_input_tokens,
		COALESCE(SUM(usage_agg.cache_write_tokens), 0) AS cache_write_tokens,
		COALESCE(SUM(usage_agg.input_audio_tokens), 0) AS input_audio_tokens,
		COALESCE(SUM(usage_agg.output_tokens), 0) AS output_tokens,
		COALESCE(SUM(usage_agg.reasoning_tokens), 0) AS reasoning_tokens,
		COALESCE(SUM(usage_agg.output_audio_tokens), 0) AS output_audio_tokens,
		COALESCE(SUM(usage_agg.accepted_tokens), 0) AS accepted_tokens,
		COALESCE(SUM(usage_agg.rejected_tokens), 0) AS rejected_tokens,
		COALESCE(SUM(usage_agg.total_tokens), 0) AS total_tokens,
		COALESCE(SUM(usage_agg.estimated_cost_usd), 0) AS estimated_cost_usd`
}

func (s *GormStore) queryAPIKeyUsageTimeseries(base *gorm.DB, query APIKeyUsageQuery) ([]APIKeyUsagePoint, error) {
	bucket := s.tokenCostBucketExpression("day")
	var rows []apiKeyUsageDatabaseRow
	if err := base.Select(bucket + " AS bucket, " + apiKeyUsageMetricSelect()).
		Group(bucket).Order(bucket).Scan(&rows).Error; err != nil {
		return nil, err
	}
	byBucket := make(map[string]APIKeyUsageMetrics, len(rows))
	for _, row := range rows {
		byBucket[row.Bucket] = apiKeyUsageMetricsFromRow(row)
	}
	start := utcDay(query.From)
	end := utcDay(query.To)
	if query.To.UTC().After(end) {
		end = end.AddDate(0, 0, 1)
	}
	points := make([]APIKeyUsagePoint, 0, int(end.Sub(start).Hours()/24)+1)
	for current := start; current.Before(end); current = current.AddDate(0, 0, 1) {
		key := current.Format("2006-01-02")
		points = append(points, APIKeyUsagePoint{Bucket: key, APIKeyUsageMetrics: byBucket[key]})
	}
	return points, nil
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func queryAPIKeyUsageBreakdown(base *gorm.DB, group, selection, order string) ([]APIKeyUsageBreakdownRow, error) {
	var rows []apiKeyUsageDatabaseRow
	if err := base.Select(selection + ", " + apiKeyUsageMetricSelect()).
		Group(group).Order(order).Limit(maxAPIKeyUsageBreakdownRows).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return apiKeyUsageBreakdownRows(rows), nil
}

func queryAPIKeyUsageErrors(base *gorm.DB) ([]APIKeyUsageBreakdownRow, error) {
	var rows []apiKeyUsageDatabaseRow
	if err := base.Where("rl.status_code >= ?", http.StatusBadRequest).
		Select(`CASE WHEN COALESCE(rl.error_code, '') = '' THEN 'http_' || CAST(rl.status_code AS TEXT) ELSE rl.error_code END AS id,
			rl.status_code, rl.error_code, MAX(rl.created_at) AS last_occurred_at, ` + apiKeyUsageMetricSelect()).
		Group("rl.status_code, rl.error_code").Order("request_count DESC, last_occurred_at DESC").Limit(maxAPIKeyUsageBreakdownRows).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return apiKeyUsageBreakdownRows(rows), nil
}

func apiKeyUsageBreakdownRows(rows []apiKeyUsageDatabaseRow) []APIKeyUsageBreakdownRow {
	result := make([]APIKeyUsageBreakdownRow, 0, len(rows))
	for _, row := range rows {
		var lastOccurredAt *time.Time
		if row.LastOccurredAt.Valid {
			lastOccurredAt = &row.LastOccurredAt.Time
		}
		result = append(result, APIKeyUsageBreakdownRow{
			ID: row.ID, APIKeyUsageMetrics: apiKeyUsageMetricsFromRow(row), LastOccurredAt: lastOccurredAt,
			StatusCode: row.StatusCode, ErrorCode: row.ErrorCode, ResourceID: row.ResourceID,
		})
	}
	return result
}

func apiKeyUsageMetricsFromRow(row apiKeyUsageDatabaseRow) APIKeyUsageMetrics {
	return APIKeyUsageMetrics{
		RequestCount: row.RequestCount, ErrorCount: row.ErrorCount, AverageLatencyMS: int64(row.AverageLatency + 0.5),
		InputTokens: row.InputTokens, CachedInputTokens: row.CachedInputTokens, CacheWriteTokens: row.CacheWriteTokens,
		InputAudioTokens: row.InputAudioTokens, OutputTokens: row.OutputTokens, ReasoningTokens: row.ReasoningTokens,
		OutputAudioTokens: row.OutputAudioTokens, AcceptedTokens: row.AcceptedTokens, RejectedTokens: row.RejectedTokens,
		TotalTokens: row.TotalTokens, EstimatedCostUSD: row.EstimatedCostUSD,
	}
}

func (s *GormStore) apiKeyQuotaSnapshot(tx *gorm.DB, key APIKey, project Project) (APIKeyQuotaSnapshot, error) {
	keyLimits := key.Limits
	keyLimits.RateLimitRPM = 0
	keyLimits.TokenLimitTPM = 0
	if key.RateLimitRPM != nil {
		keyLimits.RateLimitRPM = *key.RateLimitRPM
	}
	if key.TokenLimitTPM != nil {
		keyLimits.TokenLimitTPM = *key.TokenLimitTPM
	}
	policyLimits, _, _, err := quotaPolicyLimits(tx, project, key)
	if err != nil {
		return APIKeyQuotaSnapshot{}, err
	}
	now, err := s.databaseNow(tx)
	if err != nil {
		return APIKeyQuotaSnapshot{}, err
	}
	day := dayBucket(now)
	month := monthBucket(now)
	dayCounter, err := readQuotaCounter(tx, key.ID, "day", day)
	if err != nil {
		return APIKeyQuotaSnapshot{}, err
	}
	monthCounter, err := readQuotaCounter(tx, key.ID, "month", month)
	if err != nil {
		return APIKeyQuotaSnapshot{}, err
	}
	return APIKeyQuotaSnapshot{
		EffectiveLimits: mergeQuotaLimits(keyLimits, policyLimits),
		Day:             APIKeyQuotaPeriod{Bucket: day, Usage: dayCounter},
		Month:           APIKeyQuotaPeriod{Bucket: month, Usage: monthCounter},
	}, nil
}

func readQuotaCounter(tx *gorm.DB, keyID, scope, bucket string) (QuotaCounter, error) {
	var item QuotaBucket
	err := tx.First(&item, "key_id = ? AND scope = ? AND bucket = ?", keyID, scope, bucket).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return QuotaCounter{}, nil
	}
	return item.QuotaCounter, err
}
