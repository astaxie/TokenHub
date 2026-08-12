package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type OneAPIBillingAdapter struct {
	Client *http.Client
}

func (a OneAPIBillingAdapter) Fetch(ctx context.Context, connector BillingConnector, request BillingFetchRequest) (BillingFetchPage, error) {
	endpoint := strings.TrimSpace(connector.Config["endpoint"])
	if endpoint == "" {
		endpoint = "/api/log/self"
	}
	target, err := url.Parse(strings.TrimRight(connector.BaseURL, "/") + "/" + strings.TrimLeft(endpoint, "/"))
	if err != nil {
		return BillingFetchPage{}, NewHTTPError(http.StatusBadRequest, "invalid_billing_base_url", "Billing connector base_url is invalid")
	}
	page := 1
	if request.Cursor != "" {
		if parsed, parseErr := strconv.Atoi(request.Cursor); parseErr == nil && parsed > 0 {
			page = parsed
		}
	}
	query := target.Query()
	query.Set("p", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(request.PageSize))
	query.Set("start_timestamp", strconv.FormatInt(request.From.Unix(), 10))
	query.Set("end_timestamp", strconv.FormatInt(request.To.Unix(), 10))
	target.RawQuery = query.Encode()

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return BillingFetchPage{}, err
	}
	token := firstNonEmpty(connector.Credentials["api_token"], connector.Credentials["token"])
	if token == "" {
		return BillingFetchPage{}, NewHTTPError(http.StatusBadRequest, "billing_credentials_required", "OneAPI api_token credential is required")
	}
	httpRequest.Header.Set("authorization", "Bearer "+token)
	httpRequest.Header.Set("accept", "application/json")

	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return BillingFetchPage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return BillingFetchPage{}, billingResponseError(response)
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_invalid_response", message: "OneAPI returned invalid JSON", retryable: false}
	}
	if success, exists := payload["success"].(bool); exists && !success {
		return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_rejected", message: "OneAPI rejected the billing request", retryable: false}
	}
	items, total, hasMore := oneAPIBillingItems(payload)
	records := make([]BillingRecord, 0, len(items))
	for _, item := range items {
		record, err := normalizeOneAPIBillingRecord(item, connector)
		if err != nil {
			return BillingFetchPage{}, err
		}
		records = append(records, record)
	}
	nextCursor := ""
	if hasMore || (total > 0 && page*request.PageSize < total) {
		nextCursor = strconv.Itoa(page + 1)
	}
	return BillingFetchPage{Records: records, NextCursor: nextCursor}, nil
}

func oneAPIBillingItems(payload map[string]any) ([]map[string]any, int, bool) {
	container := payload
	if data, ok := payload["data"].(map[string]any); ok {
		container = data
	}
	var rawItems []any
	for _, key := range []string{"items", "logs", "data", "list"} {
		if value, ok := container[key].([]any); ok {
			rawItems = value
			break
		}
	}
	if rawItems == nil {
		if value, ok := payload["data"].([]any); ok {
			rawItems = value
		}
	}
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if item, ok := raw.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items, int(numberValue(container["total"])), boolValue(container["has_more"])
}

func normalizeOneAPIBillingRecord(item map[string]any, connector BillingConnector) (BillingRecord, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return BillingRecord{}, err
	}
	createdAt, err := billingTimeValue(item["created_at"], time.UTC)
	if err != nil {
		return BillingRecord{}, NewHTTPError(http.StatusBadGateway, "billing_record_invalid_time", "OneAPI billing record has an invalid created_at")
	}
	quota := int64NumberValue(item["quota"])
	quotaPerUnit := int64(billingConfigInt(connector.Config, "quota_per_unit", 500000, 1, 2_000_000_000))
	amount := billingDecimalRatio(quota, quotaPerUnit)
	gross := amount
	refund := "0"
	if quota < 0 {
		gross = "0"
		refund = billingDecimalRatio(-quota, quotaPerUnit)
	}
	externalID := stringValue(item["id"])
	if externalID == "" {
		sum := sha256.Sum256(raw)
		externalID = fmt.Sprintf("sha256:%x", sum[:])
	}
	promptTokens := int64NumberValue(firstValue(item, "prompt_tokens", "prompt_tokens_used"))
	completionTokens := int64NumberValue(firstValue(item, "completion_tokens", "completion_tokens_used"))
	metadata := map[string]string{}
	if tokenName := stringValue(firstValue(item, "token_name", "key_name")); tokenName != "" {
		metadata["token_name"] = tokenName
	}
	return BillingRecord{
		ExternalID:        externalID,
		SourceType:        BillingConnectorOneAPI,
		AccountID:         stringValue(firstValue(item, "user_id", "username")),
		Service:           "oneapi",
		Product:           stringValue(firstValue(item, "channel_name", "channel")),
		Model:             stringValue(firstValue(item, "model_name", "model")),
		Currency:          strings.ToUpper(defaultString(connector.Config["currency"], "USD")),
		GrossAmount:       gross,
		DiscountAmount:    "0",
		TaxAmount:         "0",
		RefundAmount:      refund,
		NetAmount:         amount,
		UsageQuantity:     promptTokens + completionTokens,
		UsageUnit:         "token",
		UsageStartAt:      createdAt.UTC(),
		UsageEndAt:        createdAt.UTC(),
		SourceTimezone:    "UTC",
		BillingPeriod:     createdAt.UTC().Format("2006-01"),
		ExternalRequestID: stringValue(firstValue(item, "request_id", "requestId")),
		Metadata:          metadata,
		RawPayload:        string(raw),
	}, nil
}

func billingResponseError(response *http.Response) error {
	retryAfter := time.Duration(0)
	if seconds, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("retry-after"))); err == nil && seconds > 0 {
		retryAfter = time.Duration(seconds) * time.Second
	}
	retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
	return &billingUpstreamError{
		status:     http.StatusBadGateway,
		code:       "billing_upstream_http_error",
		message:    fmt.Sprintf("Billing source returned HTTP %d", response.StatusCode),
		retryable:  retryable,
		retryAfter: retryAfter,
	}
}

func billingTimeValue(value any, location *time.Location) (time.Time, error) {
	switch typed := value.(type) {
	case json.Number:
		seconds, err := typed.Int64()
		if err != nil {
			return time.Time{}, err
		}
		if seconds > 10_000_000_000 {
			seconds /= 1000
		}
		return time.Unix(seconds, 0).In(location), nil
	case float64:
		return time.Unix(int64(typed), 0).In(location), nil
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if parsed, err := time.ParseInLocation(layout, typed, location); err == nil {
				return parsed, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("unsupported billing time %v", value)
}

func billingDecimalRatio(numerator int64, denominator int64) string {
	ratio := new(big.Rat).SetFrac(big.NewInt(numerator), big.NewInt(denominator))
	text := ratio.FloatString(12)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	if text == "" || text == "-0" {
		return "0"
	}
	return text
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed
	default:
		return 0
	}
}

// int64NumberValue parses numeric billing fields without converting through
// float64, preserving precision for large integer values such as token counts
// and quota balances.
func int64NumberValue(value any) int64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return parsed
		}
		f, _ := typed.Float64()
		return int64(f)
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err == nil {
			return parsed
		}
		f, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return int64(f)
	default:
		return 0
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(typed)
		return parsed
	default:
		return false
	}
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}
