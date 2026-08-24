package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type reconciliationDetailEntry struct {
	id         string
	sourceID   string
	dimensions map[string]string
	amount     reconciliationMoney
	occurredAt time.Time
	inPeriod   bool
}

type reconciliationDetailGroup struct {
	providers []reconciliationDetailEntry
	usages    []reconciliationDetailEntry
}

func calculateDetailReconciliation(
	run ReconciliationRun,
	bills []BillingRecord,
	usages []UsageRecord,
	amountTolerance reconciliationMoney,
	ratioTolerance reconciliationMoney,
	exchangeRate reconciliationMoney,
) (ReconciliationRun, []ReconciliationItem, error) {
	groups := map[string]*reconciliationDetailGroup{}
	digestRows := make([]reconciliationDigestRow, 0, len(bills)+len(usages))
	for _, record := range bills {
		if record.UsageStartAt.Before(run.PeriodStart) || !record.UsageStartAt.Before(run.PeriodEnd) ||
			(run.Currency != "" && !strings.EqualFold(record.Currency, run.Currency)) {
			continue
		}
		amount, err := parseReconciliationMoney(record.NetAmount)
		if err != nil {
			return run, nil, fmt.Errorf("billing record %s: %w", record.ID, err)
		}
		entry := reconciliationDetailEntry{
			id: record.ID, sourceID: record.ExternalID,
			dimensions: providerReconciliationDimensions(run, record), amount: amount,
			occurredAt: record.UsageStartAt.UTC(), inPeriod: true,
		}
		group := reconciliationDetailGroupFor(run, entry.dimensions, groups)
		group.providers = append(group.providers, entry)
		if err := addDetailEntryToRun(&run, "provider", entry, &digestRows); err != nil {
			return run, nil, err
		}
	}

	window := time.Duration(run.TimeWindowMinutes) * time.Minute
	usageFrom := run.PeriodStart.Add(-window)
	usageTo := run.PeriodEnd.Add(window)
	for _, record := range usages {
		if record.CreatedAt.Before(usageFrom) || !record.CreatedAt.Before(usageTo) {
			continue
		}
		localCost := record.ProviderCostUSD
		if localCost == 0 {
			localCost = record.CostUSD
		}
		amount, err := reconciliationMoneyFromFloat(localCost)
		if err != nil {
			return run, nil, fmt.Errorf("usage record %s: %w", record.ID, err)
		}
		amount, err = multiplyReconciliationMoney(amount, exchangeRate)
		if err != nil {
			return run, nil, fmt.Errorf("usage record %s: %w", record.ID, err)
		}
		entry := reconciliationDetailEntry{
			id: record.ID, sourceID: record.RequestID,
			dimensions: tokenHubReconciliationDimensions(run, record), amount: amount,
			occurredAt: record.CreatedAt.UTC(),
			inPeriod:   !record.CreatedAt.Before(run.PeriodStart) && record.CreatedAt.Before(run.PeriodEnd),
		}
		group := reconciliationDetailGroupFor(run, entry.dimensions, groups)
		group.usages = append(group.usages, entry)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]ReconciliationItem, 0, len(bills)+len(usages))
	for _, key := range keys {
		group := groups[key]
		sortReconciliationDetailEntries(group.providers)
		sortReconciliationDetailEntries(group.usages)
		matchedUsages := matchReconciliationDetailEntries(group.providers, group.usages, window)
		usedUsages := make([]bool, len(group.usages))
		for providerIndex := range group.providers {
			provider := group.providers[providerIndex]
			usageIndex, matched := matchedUsages[providerIndex]
			if matched {
				usedUsages[usageIndex] = true
				usage := group.usages[usageIndex]
				bucket := reconciliationDetailBucket(key, &provider, &usage)
				items = append(items, reconciliationItemFromBucket(run, bucket, amountTolerance, ratioTolerance, ""))
				if err := addDetailEntryToRun(&run, "tokenhub", usage, &digestRows); err != nil {
					return run, nil, err
				}
				continue
			}
			reason := ""
			if reconciliationEntryOutsideWindow(provider, group.usages, window) {
				reason = "outside_time_window"
			}
			bucket := reconciliationDetailBucket(key, &provider, nil)
			items = append(items, reconciliationItemFromBucket(run, bucket, amountTolerance, ratioTolerance, reason))
		}
		for usageIndex := range group.usages {
			if usedUsages[usageIndex] || !group.usages[usageIndex].inPeriod {
				continue
			}
			usage := group.usages[usageIndex]
			reason := ""
			if reconciliationEntryOutsideWindow(usage, group.providers, window) {
				reason = "outside_time_window"
			}
			bucket := reconciliationDetailBucket(key, nil, &usage)
			items = append(items, reconciliationItemFromBucket(run, bucket, amountTolerance, ratioTolerance, reason))
			if err := addDetailEntryToRun(&run, "tokenhub", usage, &digestRows); err != nil {
				return run, nil, err
			}
		}
	}
	return finishReconciliation(run, items, digestRows)
}

func reconciliationDetailGroupFor(run ReconciliationRun, dimensions map[string]string, groups map[string]*reconciliationDetailGroup) *reconciliationDetailGroup {
	selected := selectedReconciliationDimensions(run.MatchDimensions, dimensions)
	parts := make([]string, 0, len(run.MatchDimensions))
	for _, dimension := range run.MatchDimensions {
		parts = append(parts, dimension+"="+selected[dimension])
	}
	encoded, _ := json.Marshal(parts)
	key := string(encoded)
	if groups[key] == nil {
		groups[key] = &reconciliationDetailGroup{}
	}
	return groups[key]
}

func sortReconciliationDetailEntries(entries []reconciliationDetailEntry) {
	sort.Slice(entries, func(left int, right int) bool {
		if !entries[left].occurredAt.Equal(entries[right].occurredAt) {
			return entries[left].occurredAt.Before(entries[right].occurredAt)
		}
		return entries[left].id < entries[right].id
	})
}

type reconciliationMatchScore struct {
	count    int
	distance int64
}

func matchReconciliationDetailEntries(providers []reconciliationDetailEntry, usages []reconciliationDetailEntry, window time.Duration) map[int]int {
	scores := make([][]reconciliationMatchScore, len(providers)+1)
	actions := make([][]byte, len(providers)+1)
	for index := range scores {
		scores[index] = make([]reconciliationMatchScore, len(usages)+1)
		actions[index] = make([]byte, len(usages)+1)
	}
	for providerIndex := 1; providerIndex <= len(providers); providerIndex++ {
		actions[providerIndex][0] = 'p'
	}
	for usageIndex := 1; usageIndex <= len(usages); usageIndex++ {
		actions[0][usageIndex] = 'u'
	}
	for providerIndex := 1; providerIndex <= len(providers); providerIndex++ {
		for usageIndex := 1; usageIndex <= len(usages); usageIndex++ {
			best := scores[providerIndex-1][usageIndex]
			action := byte('p')
			if candidate := scores[providerIndex][usageIndex-1]; reconciliationMatchScoreBetter(candidate, best) {
				best = candidate
				action = 'u'
			}
			distance := absoluteReconciliationDuration(providers[providerIndex-1].occurredAt.Sub(usages[usageIndex-1].occurredAt))
			if distance <= window {
				candidate := scores[providerIndex-1][usageIndex-1]
				candidate.count++
				candidate.distance += int64(distance)
				if reconciliationMatchScoreBetter(candidate, best) || candidate == best {
					best = candidate
					action = 'm'
				}
			}
			scores[providerIndex][usageIndex] = best
			actions[providerIndex][usageIndex] = action
		}
	}

	matches := map[int]int{}
	for providerIndex, usageIndex := len(providers), len(usages); providerIndex > 0 || usageIndex > 0; {
		switch actions[providerIndex][usageIndex] {
		case 'm':
			matches[providerIndex-1] = usageIndex - 1
			providerIndex--
			usageIndex--
		case 'p':
			providerIndex--
		default:
			usageIndex--
		}
	}
	return matches
}

func reconciliationMatchScoreBetter(left reconciliationMatchScore, right reconciliationMatchScore) bool {
	return left.count > right.count || left.count == right.count && left.distance < right.distance
}

func reconciliationEntryOutsideWindow(entry reconciliationDetailEntry, candidates []reconciliationDetailEntry, window time.Duration) bool {
	if len(candidates) == 0 {
		return false
	}
	for _, candidate := range candidates {
		if absoluteReconciliationDuration(entry.occurredAt.Sub(candidate.occurredAt)) <= window {
			return false
		}
	}
	return true
}

func absoluteReconciliationDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func reconciliationDetailBucket(key string, provider *reconciliationDetailEntry, usage *reconciliationDetailEntry) reconciliationBucket {
	entry := usage
	if provider != nil {
		entry = provider
	}
	bucket := reconciliationBucket{key: key, dimensions: entry.dimensions, bucketStart: entry.occurredAt, bucketEnd: entry.occurredAt}
	if provider != nil {
		bucket.key += "\x00provider=" + provider.id
		bucket.providerAmount = provider.amount
		bucket.providerRecordIDs = []string{provider.id}
		bucket.providerTimes = []time.Time{provider.occurredAt}
	}
	if usage != nil {
		bucket.key += "\x00tokenhub=" + usage.id
		bucket.tokenHubAmount = usage.amount
		bucket.tokenHubRecordIDs = []string{usage.id}
		bucket.tokenHubTimes = []time.Time{usage.occurredAt}
		if usage.occurredAt.Before(bucket.bucketStart) {
			bucket.bucketStart = usage.occurredAt
		}
		if usage.occurredAt.After(bucket.bucketEnd) {
			bucket.bucketEnd = usage.occurredAt
		}
	}
	return bucket
}

func addDetailEntryToRun(run *ReconciliationRun, side string, entry reconciliationDetailEntry, rows *[]reconciliationDigestRow) error {
	*rows = append(*rows, reconciliationDigestRow{
		Side: side, ID: entry.id, SourceID: entry.sourceID,
		Dimensions: selectedReconciliationDimensions(run.MatchDimensions, entry.dimensions),
		Amount:     entry.amount.String(), OccurredAt: entry.occurredAt.Format(time.RFC3339Nano),
	})
	if side == "provider" {
		run.ProviderRecordCount++
		total, err := addMoneyString(run.ProviderAmount, entry.amount)
		run.ProviderAmount = total
		return err
	}
	run.TokenHubRecordCount++
	total, err := addMoneyString(run.TokenHubAmount, entry.amount)
	run.TokenHubAmount = total
	return err
}
