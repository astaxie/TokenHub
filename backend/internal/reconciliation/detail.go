package reconciliation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type detailEntry struct {
	id         string
	sourceID   string
	dimensions map[string]string
	amount     money
	occurredAt time.Time
	inPeriod   bool
}

type detailGroup struct {
	providers []detailEntry
	usages    []detailEntry
}

func calculateDetail(
	run Run,
	bills []BillingRecord,
	usages []Usage,
	amountTolerance money,
	ratioTolerance money,
	exchangeRate money,
) (Run, []Item, error) {
	groups := map[string]*detailGroup{}
	digestRows := make([]digestRow, 0, len(bills)+len(usages))
	for _, record := range bills {
		if record.UsageStartAt.Before(run.PeriodStart) || !record.UsageStartAt.Before(run.PeriodEnd) ||
			(run.Currency != "" && !strings.EqualFold(record.Currency, run.Currency)) {
			continue
		}
		amount, err := parseMoney(record.NetAmount)
		if err != nil {
			return run, nil, fmt.Errorf("billing record %s: %w", record.ID, err)
		}
		entry := detailEntry{
			id: record.ID, sourceID: record.ExternalID,
			dimensions: providerDimensions(run, record), amount: amount,
			occurredAt: record.UsageStartAt.UTC(), inPeriod: true,
		}
		group := detailGroupFor(run, entry.dimensions, groups)
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
		amount, err := moneyFromFloat(localCost)
		if err != nil {
			return run, nil, fmt.Errorf("usage record %s: %w", record.ID, err)
		}
		amount, err = multiplyMoney(amount, exchangeRate)
		if err != nil {
			return run, nil, fmt.Errorf("usage record %s: %w", record.ID, err)
		}
		entry := detailEntry{
			id: record.ID, sourceID: record.RequestID,
			dimensions: tokenHubDimensions(run, record), amount: amount,
			occurredAt: record.CreatedAt.UTC(),
			inPeriod:   !record.CreatedAt.Before(run.PeriodStart) && record.CreatedAt.Before(run.PeriodEnd),
		}
		group := detailGroupFor(run, entry.dimensions, groups)
		group.usages = append(group.usages, entry)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]Item, 0, len(bills)+len(usages))
	for _, key := range keys {
		group := groups[key]
		sortDetailEntries(group.providers)
		sortDetailEntries(group.usages)
		matchedUsages := matchDetailEntries(group.providers, group.usages, window)
		usedUsages := make([]bool, len(group.usages))
		for providerIndex := range group.providers {
			provider := group.providers[providerIndex]
			usageIndex, matched := matchedUsages[providerIndex]
			if matched {
				usedUsages[usageIndex] = true
				usage := group.usages[usageIndex]
				bucket := detailBucket(key, &provider, &usage)
				items = append(items, itemFromBucket(run, bucket, amountTolerance, ratioTolerance, ""))
				if err := addDetailEntryToRun(&run, "tokenhub", usage, &digestRows); err != nil {
					return run, nil, err
				}
				continue
			}
			reason := ""
			if entryOutsideWindow(provider, group.usages, window) {
				reason = "outside_time_window"
			}
			bucket := detailBucket(key, &provider, nil)
			items = append(items, itemFromBucket(run, bucket, amountTolerance, ratioTolerance, reason))
		}
		for usageIndex := range group.usages {
			if usedUsages[usageIndex] || !group.usages[usageIndex].inPeriod {
				continue
			}
			usage := group.usages[usageIndex]
			reason := ""
			if entryOutsideWindow(usage, group.providers, window) {
				reason = "outside_time_window"
			}
			bucket := detailBucket(key, nil, &usage)
			items = append(items, itemFromBucket(run, bucket, amountTolerance, ratioTolerance, reason))
			if err := addDetailEntryToRun(&run, "tokenhub", usage, &digestRows); err != nil {
				return run, nil, err
			}
		}
	}
	return finish(run, items, digestRows)
}

func detailGroupFor(run Run, dimensions map[string]string, groups map[string]*detailGroup) *detailGroup {
	selected := selectedDimensions(run.MatchDimensions, dimensions)
	parts := make([]string, 0, len(run.MatchDimensions))
	for _, dimension := range run.MatchDimensions {
		parts = append(parts, dimension+"="+selected[dimension])
	}
	encoded, _ := json.Marshal(parts)
	key := string(encoded)
	if groups[key] == nil {
		groups[key] = &detailGroup{}
	}
	return groups[key]
}

func sortDetailEntries(entries []detailEntry) {
	sort.Slice(entries, func(left int, right int) bool {
		if !entries[left].occurredAt.Equal(entries[right].occurredAt) {
			return entries[left].occurredAt.Before(entries[right].occurredAt)
		}
		return entries[left].id < entries[right].id
	})
}

type matchScore struct {
	count    int
	distance int64
}

func matchDetailEntries(providers []detailEntry, usages []detailEntry, window time.Duration) map[int]int {
	scores := make([][]matchScore, len(providers)+1)
	actions := make([][]byte, len(providers)+1)
	for index := range scores {
		scores[index] = make([]matchScore, len(usages)+1)
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
			if candidate := scores[providerIndex][usageIndex-1]; matchScoreBetter(candidate, best) {
				best = candidate
				action = 'u'
			}
			distance := absoluteDuration(providers[providerIndex-1].occurredAt.Sub(usages[usageIndex-1].occurredAt))
			if distance <= window {
				candidate := scores[providerIndex-1][usageIndex-1]
				candidate.count++
				candidate.distance += int64(distance)
				if matchScoreBetter(candidate, best) || candidate == best {
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

func matchScoreBetter(left matchScore, right matchScore) bool {
	return left.count > right.count || left.count == right.count && left.distance < right.distance
}

func entryOutsideWindow(entry detailEntry, candidates []detailEntry, window time.Duration) bool {
	if len(candidates) == 0 {
		return false
	}
	for _, candidate := range candidates {
		if absoluteDuration(entry.occurredAt.Sub(candidate.occurredAt)) <= window {
			return false
		}
	}
	return true
}

func absoluteDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func detailBucket(key string, provider *detailEntry, usage *detailEntry) bucket {
	entry := usage
	if provider != nil {
		entry = provider
	}
	bucket := bucket{key: key, dimensions: entry.dimensions, bucketStart: entry.occurredAt, bucketEnd: entry.occurredAt}
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

func addDetailEntryToRun(run *Run, side string, entry detailEntry, rows *[]digestRow) error {
	*rows = append(*rows, digestRow{
		Side: side, ID: entry.id, SourceID: entry.sourceID,
		Dimensions: selectedDimensions(run.MatchDimensions, entry.dimensions),
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
