package reconciliation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type bucket struct {
	key               string
	dimensions        map[string]string
	bucketStart       time.Time
	bucketEnd         time.Time
	providerAmount    money
	tokenHubAmount    money
	providerRecordIDs []string
	tokenHubRecordIDs []string
	providerTimes     []time.Time
	tokenHubTimes     []time.Time
}

type digestRow struct {
	Side       string            `json:"side"`
	ID         string            `json:"id"`
	SourceID   string            `json:"source_id"`
	Dimensions map[string]string `json:"dimensions"`
	Amount     string            `json:"amount"`
	OccurredAt string            `json:"occurred_at"`
}

func calculate(run Run, bills []BillingRecord, usages []Usage) (Run, []Item, error) {
	amountTolerance, err := parseMoney(run.AmountTolerance)
	if err != nil {
		return run, nil, err
	}
	ratioTolerance, err := parseMoney(run.RatioTolerance)
	if err != nil {
		return run, nil, err
	}
	location, err := time.LoadLocation(run.Timezone)
	if err != nil {
		return run, nil, fmt.Errorf("invalid reconciliation timezone: %w", err)
	}
	exchangeRate, err := parseMoney(run.USDExchangeRate)
	if err != nil {
		return run, nil, fmt.Errorf("invalid reconciliation exchange rate: %w", err)
	}
	if run.Granularity == GranularityDetail {
		return calculateDetail(run, bills, usages, amountTolerance, ratioTolerance, exchangeRate)
	}

	buckets := map[string]*bucket{}
	digestRows := make([]digestRow, 0, len(bills)+len(usages))
	for _, record := range bills {
		if record.UsageStartAt.Before(run.PeriodStart) || !record.UsageStartAt.Before(run.PeriodEnd) {
			continue
		}
		if run.Currency != "" && !strings.EqualFold(record.Currency, run.Currency) {
			continue
		}
		amount, parseErr := parseMoney(record.NetAmount)
		if parseErr != nil {
			return run, nil, fmt.Errorf("billing record %s: %w", record.ID, parseErr)
		}
		dimensions := providerDimensions(run, record)
		bucket := bucketFor(run, dimensions, record.UsageStartAt, location, buckets)
		bucket.providerAmount, err = addMoney(bucket.providerAmount, amount)
		if err != nil {
			return run, nil, err
		}
		bucket.providerRecordIDs = append(bucket.providerRecordIDs, record.ID)
		bucket.providerTimes = append(bucket.providerTimes, record.UsageStartAt.UTC())
		digestRows = append(digestRows, digestRow{
			Side:       "provider",
			ID:         record.ID,
			SourceID:   record.ExternalID,
			Dimensions: selectedDimensions(run.MatchDimensions, dimensions),
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
		amount, parseErr := moneyFromFloat(localCost)
		if parseErr != nil {
			return run, nil, fmt.Errorf("usage record %s: %w", record.ID, parseErr)
		}
		amount, err = multiplyMoney(amount, exchangeRate)
		if err != nil {
			return run, nil, fmt.Errorf("usage record %s: %w", record.ID, err)
		}
		dimensions := tokenHubDimensions(run, record)
		bucket := bucketFor(run, dimensions, record.CreatedAt, location, buckets)
		bucket.tokenHubAmount, err = addMoney(bucket.tokenHubAmount, amount)
		if err != nil {
			return run, nil, err
		}
		bucket.tokenHubRecordIDs = append(bucket.tokenHubRecordIDs, record.ID)
		bucket.tokenHubTimes = append(bucket.tokenHubTimes, record.CreatedAt.UTC())
		digestRows = append(digestRows, digestRow{
			Side:       "tokenhub",
			ID:         record.ID,
			SourceID:   record.RequestID,
			Dimensions: selectedDimensions(run.MatchDimensions, dimensions),
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
	items := make([]Item, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		sort.Strings(bucket.providerRecordIDs)
		sort.Strings(bucket.tokenHubRecordIDs)
		items = append(items, itemFromBucket(run, *bucket, amountTolerance, ratioTolerance, ""))
	}
	return finish(run, items, digestRows)
}

func finish(run Run, items []Item, rows []digestRow) (Run, []Item, error) {
	run.InputHash = inputHash(run, rows)
	for _, item := range items {
		switch item.Status {
		case Matched:
			run.MatchedCount++
		case ProviderOnly:
			run.ProviderOnlyCount++
		case TokenHubOnly:
			run.TokenHubOnlyCount++
		case AmountMismatch:
			run.AmountMismatchCount++
		}
	}
	providerAmount, err := parseMoney(run.ProviderAmount)
	if err != nil {
		return run, nil, err
	}
	tokenHubAmount, err := parseMoney(run.TokenHubAmount)
	if err != nil {
		return run, nil, err
	}
	run.ProviderAmount = providerAmount.OutputString()
	run.TokenHubAmount = tokenHubAmount.OutputString()
	run.DifferenceAmount = (providerAmount - tokenHubAmount).OutputString()
	run.Status = RunSucceeded
	finishedAt := time.Now().UTC()
	run.FinishedAt = &finishedAt
	return run, items, nil
}

func bucketFor(run Run, dimensions map[string]string, occurredAt time.Time, location *time.Location, buckets map[string]*bucket) *bucket {
	start, end := bucketRange(run.Granularity, occurredAt, location)
	selected := selectedDimensions(run.MatchDimensions, dimensions)
	keyParts := make([]string, 0, len(run.MatchDimensions)+1)
	if run.Granularity != GranularityDetail {
		keyParts = append(keyParts, start.Format(time.RFC3339Nano))
	}
	for _, dimension := range run.MatchDimensions {
		keyParts = append(keyParts, dimension+"="+selected[dimension])
	}
	encoded, _ := json.Marshal(keyParts)
	key := string(encoded)
	result := buckets[key]
	if result == nil {
		result = &bucket{key: key, dimensions: selected, bucketStart: start, bucketEnd: end}
		buckets[key] = result
	}
	if run.Granularity == GranularityDetail {
		if result.bucketStart.IsZero() || occurredAt.Before(result.bucketStart) {
			result.bucketStart = occurredAt.UTC()
		}
		if occurredAt.After(result.bucketEnd) {
			result.bucketEnd = occurredAt.UTC()
		}
	}
	return result
}

func bucketRange(granularity string, value time.Time, location *time.Location) (time.Time, time.Time) {
	local := value.In(location)
	var start time.Time
	switch granularity {
	case GranularityHour:
		start = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, location)
	case GranularityDay:
		start = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	case GranularityMonth:
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location)
	default:
		instant := value.UTC()
		return instant, instant
	}
	var end time.Time
	switch granularity {
	case GranularityHour:
		end = start.Add(time.Hour)
	case GranularityDay:
		end = start.AddDate(0, 0, 1)
	default:
		end = start.AddDate(0, 1, 0)
	}
	return start.UTC(), end.UTC()
}

func providerDimensions(run Run, record BillingRecord) map[string]string {
	dimensions := map[string]string{
		"request_id":       strings.TrimSpace(record.ExternalRequestID),
		"provider":         firstNonEmpty(record.ProviderID, record.SourceType),
		"resource_account": firstNonEmpty(record.ProviderResourceID, record.ResourceID, record.AccountID),
		"model":            strings.TrimSpace(record.Model),
		"project":          strings.TrimSpace(record.ProjectID),
		"currency":         strings.ToUpper(strings.TrimSpace(record.Currency)),
	}
	for dimension, values := range run.DimensionMappings {
		if canonical := strings.TrimSpace(values[dimensions[dimension]]); canonical != "" {
			dimensions[dimension] = canonical
		}
	}
	return dimensions
}

func tokenHubDimensions(run Run, record Usage) map[string]string {
	return map[string]string{
		"request_id":       strings.TrimSpace(record.RequestID),
		"provider":         strings.TrimSpace(record.ProviderID),
		"resource_account": strings.TrimSpace(record.ProviderResourceID),
		"model":            strings.TrimSpace(record.ModelName),
		"project":          strings.TrimSpace(record.ProjectID),
		"currency":         run.Currency,
	}
}

func selectedDimensions(names []string, dimensions map[string]string) map[string]string {
	selected := make(map[string]string, len(names))
	for _, name := range names {
		selected[name] = strings.TrimSpace(dimensions[name])
	}
	return selected
}

func itemFromBucket(run Run, bucket bucket, amountTolerance money, ratioTolerance money, reasonOverride string) Item {
	difference := bucket.providerAmount - bucket.tokenHubAmount
	ratio := ratio(difference, bucket.providerAmount)
	status := AmountMismatch
	reason := reasonOverride
	switch {
	case len(bucket.providerRecordIDs) == 0:
		status = TokenHubOnly
		if reason == "" {
			reason = "provider_bill_delayed_or_unmapped"
		}
	case len(bucket.tokenHubRecordIDs) == 0:
		status = ProviderOnly
		if reason == "" {
			reason = "missing_tokenhub_usage_or_late_data"
		}
	case absoluteMoney(difference) <= amountTolerance || ratio <= ratioTolerance:
		status = Matched
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
	return Item{
		ID:                    "recitem_" + hex.EncodeToString(digest[:12]),
		RunID:                 run.ID,
		MatchKey:              hex.EncodeToString(digest[:16]),
		Status:                status,
		BucketStart:           bucket.bucketStart,
		BucketEnd:             bucket.bucketEnd,
		RequestID:             bucket.dimensions["request_id"],
		Provider:              bucket.dimensions["provider"],
		ResourceAccount:       bucket.dimensions["resource_account"],
		ResourceAccountMasked: maskIdentifier(bucket.dimensions["resource_account"]),
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

func inputHash(run Run, rows []digestRow) string {
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
		RuleHash string      `json:"rule_hash"`
		From     string      `json:"from"`
		To       string      `json:"to"`
		Rows     []digestRow `json:"rows"`
	}{run.RuleHash, run.PeriodStart.UTC().Format(time.RFC3339Nano), run.PeriodEnd.UTC().Format(time.RFC3339Nano), rows})
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func addMoneyString(current string, amount money) (string, error) {
	parsed, err := parseMoney(current)
	if err != nil {
		return "", err
	}
	total, err := addMoney(parsed, amount)
	if err != nil {
		return "", err
	}
	return total.String(), nil
}

func maskIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + "****" + value[len(value)-2:]
}
