package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const newAPIMaxRange = 30 * 24 * time.Hour

type NewAPIBillingAdapter struct {
	Client *http.Client
}

func (a NewAPIBillingAdapter) Fetch(ctx context.Context, connector BillingConnector, request BillingFetchRequest) (BillingFetchPage, error) {
	target, err := url.Parse(strings.TrimRight(connector.BaseURL, "/") + "/api/data/self")
	if err != nil {
		return BillingFetchPage{}, NewHTTPError(http.StatusBadRequest, "invalid_billing_base_url", "Billing connector base_url is invalid")
	}

	start := request.From.UTC().Unix()
	end := request.To.UTC().Unix()
	if request.Cursor != "" {
		start, err = strconv.ParseInt(request.Cursor, 10, 64)
		if err != nil || start < request.From.UTC().Unix() || start > end {
			return BillingFetchPage{}, NewHTTPError(http.StatusBadGateway, "billing_cursor_invalid", "NewAPI billing cursor is invalid")
		}
	}
	windowEnd := end
	if maximum := start + int64(newAPIMaxRange/time.Second); windowEnd > maximum {
		windowEnd = maximum
	}
	query := target.Query()
	query.Set("start_timestamp", strconv.FormatInt(start, 10))
	query.Set("end_timestamp", strconv.FormatInt(windowEnd, 10))
	target.RawQuery = query.Encode()

	token := firstNonEmpty(connector.Credentials["api_token"], connector.Credentials["token"])
	userID := strings.TrimSpace(connector.Config["user_id"])
	parsedUserID, parseUserErr := strconv.ParseInt(userID, 10, 64)
	if token == "" || parseUserErr != nil || parsedUserID <= 0 {
		return BillingFetchPage{}, NewHTTPError(http.StatusBadRequest, "billing_credentials_required", "NewAPI api_token credential and user_id config are required")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return BillingFetchPage{}, err
	}
	httpRequest.Header.Set("authorization", "Bearer "+token)
	httpRequest.Header.Set("New-Api-User", userID)
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
		return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_invalid_response", message: "NewAPI returned invalid JSON", retryable: false}
	}
	success, exists := payload["success"].(bool)
	if !exists {
		return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_invalid_response", message: "NewAPI returned an invalid response envelope", retryable: false}
	}
	if !success {
		return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_rejected", message: "NewAPI rejected the billing request", retryable: false}
	}
	rawItems, ok := payload["data"].([]any)
	if !ok {
		return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_invalid_response", message: "NewAPI returned an invalid quota data payload", retryable: false}
	}
	records := make([]BillingRecord, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return BillingFetchPage{}, &billingUpstreamError{status: http.StatusBadGateway, code: "billing_upstream_invalid_response", message: "NewAPI returned an invalid quota data item", retryable: false}
		}
		record, normalizeErr := normalizeNewAPIBillingRecord(item, connector)
		if normalizeErr != nil {
			return BillingFetchPage{}, normalizeErr
		}
		records = append(records, record)
	}

	nextCursor := ""
	if windowEnd < end {
		nextCursor = strconv.FormatInt(windowEnd+1, 10)
	}
	return BillingFetchPage{Records: records, NextCursor: nextCursor}, nil
}

func normalizeNewAPIBillingRecord(item map[string]any, connector BillingConnector) (BillingRecord, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return BillingRecord{}, err
	}
	createdAt, err := billingTimeValue(item["created_at"], time.UTC)
	if err != nil || createdAt.Unix() <= 0 {
		return BillingRecord{}, NewHTTPError(http.StatusBadGateway, "billing_record_invalid_time", "NewAPI billing record has an invalid created_at")
	}
	userID, err := newAPIIntegerField(item, "user_id")
	if err != nil || userID <= 0 || strconv.FormatInt(userID, 10) != strings.TrimSpace(connector.Config["user_id"]) {
		return BillingRecord{}, NewHTTPError(http.StatusBadGateway, "billing_upstream_invalid_response", "NewAPI billing record has an invalid user_id")
	}
	accountID := strconv.FormatInt(userID, 10)
	username := stringValue(item["username"])
	model := stringValue(item["model_name"])
	if username == "" || model == "" {
		return BillingRecord{}, NewHTTPError(http.StatusBadGateway, "billing_upstream_invalid_response", "NewAPI billing record is missing username or model_name")
	}
	tokenUsed, err := newAPIIntegerField(item, "token_used")
	if err != nil || tokenUsed < 0 {
		return BillingRecord{}, NewHTTPError(http.StatusBadGateway, "billing_upstream_invalid_response", "NewAPI billing record has an invalid token_used")
	}
	requestCount, err := newAPIIntegerField(item, "count")
	if err != nil || requestCount < 0 {
		return BillingRecord{}, NewHTTPError(http.StatusBadGateway, "billing_upstream_invalid_response", "NewAPI billing record has an invalid count")
	}
	quota, err := newAPIIntegerField(item, "quota")
	if err != nil {
		return BillingRecord{}, NewHTTPError(http.StatusBadGateway, "billing_upstream_invalid_response", "NewAPI billing record has an invalid quota")
	}
	identity := strings.Join([]string{accountID, model, strconv.FormatInt(createdAt.UTC().Unix(), 10)}, "\x00")
	sum := sha256.Sum256([]byte(identity))

	quotaPerUnit := int64(billingConfigInt(connector.Config, "quota_per_unit", 500000, 1, 2_000_000_000))
	amount := billingDecimalRatio(quota, quotaPerUnit)
	gross := amount
	refund := "0"
	if quota < 0 {
		gross = "0"
		refund = billingDecimalRatio(-quota, quotaPerUnit)
	}
	metadata := map[string]string{
		"request_count": strconv.FormatInt(requestCount, 10),
		"username":      username,
	}
	return BillingRecord{
		ExternalID:     fmt.Sprintf("quota:%x", sum[:]),
		SourceType:     BillingConnectorNewAPI,
		AccountID:      accountID,
		Service:        "newapi",
		Product:        "quota_data",
		Model:          model,
		Currency:       strings.ToUpper(defaultString(connector.Config["currency"], "USD")),
		GrossAmount:    gross,
		DiscountAmount: "0",
		TaxAmount:      "0",
		RefundAmount:   refund,
		NetAmount:      amount,
		UsageQuantity:  tokenUsed,
		UsageUnit:      "token",
		UsageStartAt:   createdAt.UTC(),
		UsageEndAt:     createdAt.UTC().Add(time.Hour),
		SourceTimezone: "UTC",
		BillingPeriod:  createdAt.UTC().Format("2006-01"),
		Metadata:       metadata,
		RawPayload:     string(raw),
	}, nil
}

func newAPIIntegerField(item map[string]any, key string) (int64, error) {
	value, ok := item[key]
	if !ok {
		return 0, fmt.Errorf("missing %s", key)
	}
	switch typed := value.(type) {
	case json.Number:
		return typed.Int64()
	case float64:
		if typed != math.Trunc(typed) {
			return 0, fmt.Errorf("%s is not an integer", key)
		}
		return int64(typed), nil
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	default:
		return 0, fmt.Errorf("%s is not numeric", key)
	}
}
