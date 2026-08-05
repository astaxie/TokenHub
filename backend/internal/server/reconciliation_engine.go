package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type reconciliationBucket struct {
	key               string
	dimensions        map[string]string
	bucketStart       time.Time
	bucketEnd         time.Time
	providerAmount    reconciliationMoney
	tokenHubAmount    reconciliationMoney
	providerRecordIDs []string
	tokenHubRecordIDs []string
	providerTimes     []time.Time
	tokenHubTimes     []time.Time
}

type reconciliationDigestRow struct {
	Side       string            `json:"side"`
	ID         string            `json:"id"`
	SourceID   string            `json:"source_id"`
	Dimensions map[string]string `json:"dimensions"`
	Amount     string            `json:"amount"`
	OccurredAt string            `json:"occurred_at"`
}

func calculateReconciliation(run ReconciliationRun, bills []BillingRecord, usages []UsageRecord) (ReconciliationRun, []ReconciliationItem, error) {
	amountTolerance, err := parseReconciliationMoney(run.AmountTolerance)
	if err != nil {
		return run, nil, err
	}
	ratioTolerance, err := parseReconciliationMoney(run.RatioTolerance)
	if err != nil {
		return run, nil, err
	}
	location, err := time.LoadLocation(run.Timezone)
	if err != nil {
		return run, nil, fmt.Errorf("invalid reconciliation timezone: %w", err)
	}
	exchangeRate, err := parseReconciliationMoney(run.USDExchangeRate)
	if err != nil {
		return run, nil, fmt.Errorf("invalid reconciliation exchange rate: %w", err)
	}
	if run.Granularity == ReconciliationGranularityDetail {
		return calculateDetailReconciliation(run, bills, usages, amountTolerance, ratioTolerance, exchangeRate)
	}

	buckets := map[string]*reconciliationBucket{}
	digestRows := make([]reconciliationDigestRow, 0, len(bills)+len(usages))
	for _, record := range bills {
		if record.UsageStartAt.Before(run.PeriodStart) || !record.UsageStartAt.Before(run.PeriodEnd) {
			continue
		}
		if run.Currency != "" && !strings.EqualFold(record.Currency, run.Currency) {
			continue
		}
		amount, parseErr := parseReconciliationMoney(record.NetAmount)
		if parseErr != nil {
			return run, nil, fmt.Errorf("billing record %s: %w", record.ID, parseErr)
		}
		dimensions := providerReconciliationDimensions(run, record)
		bucket := reconciliationBucketFor(run, dimensions, record.UsageStartAt, location, buckets)
		bucket.providerAmount, err = addReconciliationMoney(bucket.providerAmount, amount)
		if err != nil {
			return run, nil, err
		}
		bucket.providerRecordIDs = append(bucket.providerRecordIDs, record.ID)
		bucket.providerTimes = append(bucket.providerTimes, record.UsageStartAt.UTC())
		digestRows = append(digestRows, reconciliationDigestRow{
			Side:       "provider",
			ID:         record.ID,
			SourceID:   record.ExternalID,
			Dimensions: selectedReconciliationDimensions(run.MatchDimensions, dimensions),
			Amount:     amount.String(),
			OccurredAt: record.UsageStartAt.UTC().Format(time.RFC3339Nano),
		})
		run.ProviderRecordCount++
		run.ProviderAmount, err = addMoneyString(run.ProviderAmount, amount)
		if err != nil {
			return run, nil, err
		}
	}

	for _, record := range usages {
		if record.CreatedAt.Before(run.PeriodStart) || !record.CreatedAt.Before(run.PeriodEnd) {
			continue
		}
		localCost := record.ProviderCostUSD
		if localCost == 0 {
			localCost = record.CostUSD
		}
		amount, parseErr := reconciliationMoneyFromFloat(localCost)
		if parseErr != nil {
			return run, nil, fmt.Errorf("usage record %s: %w", record.ID, parseErr)
		}
		amount, err = multiplyReconciliationMoney(amount, exchangeRate)
		if err != nil {
			return run, nil, fmt.Errorf("usage record %s: %w", record.ID, err)
		}
		dimensions := tokenHubReconciliationDimensions(run, record)
		bucket := reconciliationBucketFor(run, dimensions, record.CreatedAt, location, buckets)
		bucket.tokenHubAmount, err = addReconciliationMoney(bucket.tokenHubAmount, amount)
		if err != nil {
			return run, nil, err
		}
		bucket.tokenHubRecordIDs = append(bucket.tokenHubRecordIDs, record.ID)
		bucket.tokenHubTimes = append(bucket.tokenHubTimes, record.CreatedAt.UTC())
		digestRows = append(digestRows, reconciliationDigestRow{
			Side:       "tokenhub",
			ID:         record.ID,
			SourceID:   record.RequestID,
			Dimensions: selectedReconciliationDimensions(run.MatchDimensions, dimensions),
			Amount:     amount.String(),
			OccurredAt: record.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
		run.TokenHubRecordCount++
		run.TokenHubAmount, err = addMoneyString(run.TokenHubAmount, amount)
		if err != nil {
			return run, nil, err
		}
	}

	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]ReconciliationItem, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		sort.Strings(bucket.providerRecordIDs)
		sort.Strings(bucket.tokenHubRecordIDs)
		items = append(items, reconciliationItemFromBucket(run, *bucket, amountTolerance, ratioTolerance, ""))
	}
	return finishReconciliation(run, items, digestRows)
}

func finishReconciliation(run ReconciliationRun, items []ReconciliationItem, rows []reconciliationDigestRow) (ReconciliationRun, []ReconciliationItem, error) {
	run.InputHash = reconciliationInputHash(run, rows)
	for _, item := range items {
		switch item.Status {
		case ReconciliationMatched:
			run.MatchedCount++
		case ReconciliationProviderOnly:
			run.ProviderOnlyCount++
		case ReconciliationTokenHubOnly:
			run.TokenHubOnlyCount++
		case ReconciliationAmountMismatch:
			run.AmountMismatchCount++
		}
	}
	providerAmount, err := parseReconciliationMoney(run.ProviderAmount)
	if err != nil {
		return run, nil, err
	}
	tokenHubAmount, err := parseReconciliationMoney(run.TokenHubAmount)
	if err != nil {
		return run, nil, err
	}
	run.ProviderAmount = providerAmount.OutputString()
	run.TokenHubAmount = tokenHubAmount.OutputString()
	run.DifferenceAmount = (providerAmount - tokenHubAmount).OutputString()
	run.Status = ReconciliationRunSucceeded
	finishedAt := time.Now().UTC()
	run.FinishedAt = &finishedAt
	return run, items, nil
}

func reconciliationBucketFor(run ReconciliationRun, dimensions map[string]string, occurredAt time.Time, location *time.Location, buckets map[string]*reconciliationBucket) *reconciliationBucket {
	start, end := reconciliationBucketRange(run.Granularity, occurredAt, location)
	selected := selectedReconciliationDimensions(run.MatchDimensions, dimensions)
	keyParts := make([]string, 0, len(run.MatchDimensions)+1)
	if run.Granularity != ReconciliationGranularityDetail {
		keyParts = append(keyParts, start.Format(time.RFC3339Nano))
	}
	for _, dimension := range run.MatchDimensions {
		keyParts = append(keyParts, dimension+"="+selected[dimension])
	}
	encoded, _ := json.Marshal(keyParts)
	key := string(encoded)
	bucket := buckets[key]
	if bucket == nil {
		bucket = &reconciliationBucket{key: key, dimensions: selected, bucketStart: start, bucketEnd: end}
		buckets[key] = bucket
	}
	if run.Granularity == ReconciliationGranularityDetail {
		if bucket.bucketStart.IsZero() || occurredAt.Before(bucket.bucketStart) {
			bucket.bucketStart = occurredAt.UTC()
		}
		if occurredAt.After(bucket.bucketEnd) {
			bucket.bucketEnd = occurredAt.UTC()
		}
	}
	return bucket
}

func reconciliationBucketRange(granularity string, value time.Time, location *time.Location) (time.Time, time.Time) {
	local := value.In(location)
	var start time.Time
	switch granularity {
	case ReconciliationGranularityHour:
		start = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, location)
	case ReconciliationGranularityDay:
		start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	case ReconciliationGranularityMonth:
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
	default:
		instant := value.UTC()
		return instant, instant
	}
	var end time.Time
	switch granularity {
	case ReconciliationGranularityHour:
		end = start.Add(time.Hour)
	case ReconciliationGranularityDay:
		end = start.AddDate(0, 0, 1)
	default:
		end = start.AddDate(0, 1, 0)
	}
	return start.UTC(), end.UTC()
}

func providerReconciliationDimensions(run ReconciliationRun, record BillingRecord) map[string]string {
	dimensions := map[string]string{
		"request_id":       strings.TrimSpace(record.ExternalRequestID),
		"provider":         firstNonEmpty(record.Metadata["provider_id"], record.SourceType),
		"resource_account": firstNonEmpty(record.Metadata["provider_resource_id"], record.Metadata["resource_id"], record.AccountID),
		"model":            strings.TrimSpace(record.Model),
		"project":          strings.TrimSpace(record.Metadata["project_id"]),
		"currency":         strings.ToUpper(strings.TrimSpace(record.Currency)),
	}
	for dimension, values := range run.DimensionMappings {
		if canonical := strings.TrimSpace(values[dimensions[dimension]]); canonical != "" {
			dimensions[dimension] = canonical
		}
	}
	return dimensions
}

func tokenHubReconciliationDimensions(run ReconciliationRun, record UsageRecord) map[string]string {
	return map[string]string{
		"request_id":       strings.TrimSpace(record.RequestID),
		"provider":         strings.TrimSpace(record.ProviderID),
		"resource_account": strings.TrimSpace(record.ProviderResourceID),
		"model":            strings.TrimSpace(record.ModelName),
		"project":          strings.TrimSpace(record.ProjectID),
		"currency":         run.Currency,
	}
}

func selectedReconciliationDimensions(names []string, dimensions map[string]string) map[string]string {
	selected := make(map[string]string, len(names))
	for _, name := range names {
		selected[name] = strings.TrimSpace(dimensions[name])
	}
	return selected
}

func reconciliationItemFromBucket(run ReconciliationRun, bucket reconciliationBucket, amountTolerance reconciliationMoney, ratioTolerance reconciliationMoney, reasonOverride string) ReconciliationItem {
	difference := bucket.providerAmount - bucket.tokenHubAmount
	ratio := reconciliationRatio(difference, bucket.providerAmount)
	status := ReconciliationAmountMismatch
	reason := reasonOverride
	switch {
	case len(bucket.providerRecordIDs) == 0:
		status = ReconciliationTokenHubOnly
		if reason == "" {
			reason = "provider_bill_delayed_or_unmapped"
		}
	case len(bucket.tokenHubRecordIDs) == 0:
		status = ReconciliationProviderOnly
		if reason == "" {
			reason = "missing_tokenhub_usage_or_late_data"
		}
	case absoluteReconciliationMoney(difference) <= amountTolerance || ratio <= ratioTolerance:
		status = ReconciliationMatched
		if reason == "" {
			reason = "within_tolerance"
		}
	default:
		if reason == "" && difference > 0 {
			reason = "provider_amount_higher"
		} else if reason == "" {
			reason = "tokenhub_amount_higher"
		}
	}
	digest := sha256.Sum256([]byte(run.ID + "\x00" + bucket.key + "\x00" + status))
	now := time.Now().UTC()
	return ReconciliationItem{
		ID:                    "recitem_" + hex.EncodeToString(digest[:12]),
		RunID:                 run.ID,
		MatchKey:              hex.EncodeToString(digest[:16]),
		Status:                status,
		BucketStart:           bucket.bucketStart,
		BucketEnd:             bucket.bucketEnd,
		RequestID:             bucket.dimensions["request_id"],
		Provider:              bucket.dimensions["provider"],
		ResourceAccount:       bucket.dimensions["resource_account"],
		ResourceAccountMasked: maskReconciliationIdentifier(bucket.dimensions["resource_account"]),
		Model:                 bucket.dimensions["model"],
		Project:               bucket.dimensions["project"],
		Currency:              bucket.dimensions["currency"],
		ProviderAmount:        bucket.providerAmount.OutputString(),
		TokenHubAmount:        bucket.tokenHubAmount.OutputString(),
		DifferenceAmount:      difference.OutputString(),
		DifferenceRatio:       ratio.OutputString(),
		PossibleReason:        reason,
		ProviderRecordIDs:     bucket.providerRecordIDs,
		TokenHubRecordIDs:     bucket.tokenHubRecordIDs,
		CreatedAt:             now,
	}
}

func reconciliationInputHash(run ReconciliationRun, rows []reconciliationDigestRow) string {
	sort.Slice(rows, func(left int, right int) bool {
		if rows[left].Side != rows[right].Side {
			return rows[left].Side < rows[right].Side
		}
		if rows[left].ID != rows[right].ID {
			return rows[left].ID < rows[right].ID
		}
		return rows[left].SourceID < rows[right].SourceID
	})
	payload, _ := json.Marshal(struct {
		RuleHash string                    `json:"rule_hash"`
		From     string                    `json:"from"`
		To       string                    `json:"to"`
		Rows     []reconciliationDigestRow `json:"rows"`
	}{run.RuleHash, run.PeriodStart.UTC().Format(time.RFC3339Nano), run.PeriodEnd.UTC().Format(time.RFC3339Nano), rows})
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func addMoneyString(current string, amount reconciliationMoney) (string, error) {
	parsed, err := parseReconciliationMoney(current)
	if err != nil {
		return "", err
	}
	total, err := addReconciliationMoney(parsed, amount)
	if err != nil {
		return "", err
	}
	return total.String(), nil
}

func maskReconciliationIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + "****" + value[len(value)-2:]
}
