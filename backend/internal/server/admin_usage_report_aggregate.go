package server

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

type usageReportAggregate struct {
	user             AdminUser
	selectedRange    string
	end              time.Time
	location         *time.Location
	granularity      string
	earliest         time.Time
	summary          UsageSummary
	buckets          map[string]*UsageSummary
	dimensions       map[string]map[string]*UsageSummary
	projectsByID     map[string]Project
	keysByID         map[string]APIKey
	usersByID        map[string]AdminUser
	costCenters      map[string]string
	ownedKeyCount    map[string]int
	usedKeysByMember map[string]map[string]bool
}

func (s *Server) newUsageReportAggregate(user AdminUser, selectedRange string, end time.Time, location *time.Location) *usageReportAggregate {
	aggregate := &usageReportAggregate{
		user: user, selectedRange: selectedRange, end: end, location: location,
		granularity: usageReportGranularity(selectedRange, time.Time{}, end, location),
		buckets:     map[string]*UsageSummary{}, dimensions: map[string]map[string]*UsageSummary{},
		projectsByID: indexProjectsByID(s.store.ListProjects()), keysByID: map[string]APIKey{}, usersByID: map[string]AdminUser{},
		costCenters: map[string]string{}, ownedKeyCount: map[string]int{}, usedKeysByMember: map[string]map[string]bool{},
	}
	for _, dimension := range []string{"projects", "models", "providers", "provider_resources", "cost_centers", "api_keys", "members"} {
		aggregate.dimensions[dimension] = map[string]*UsageSummary{}
	}
	for _, user := range s.store.ListAdminUsers() {
		aggregate.usersByID[user.ID] = user
	}
	for _, key := range s.store.ListAPIKeys() {
		aggregate.keysByID[key.ID] = key
		owner := usageAttributionUserID(key, aggregate.projectsByID[key.ProjectID])
		if key.Status != StatusRevoked && canAttributeUsageToMember(user, aggregate.usersByID, owner) {
			aggregate.ownedKeyCount[owner]++
		}
	}
	// Resolve metadata before opening SQL cursors, including on single-connection
	// databases. Retained state grows with attribution dimensions, not requests.
	teamsByID, quotasByID := map[string]AdminResource{}, map[string]AdminResource{}
	for _, team := range s.store.ListResources("teams") {
		teamsByID[team.ID] = team
	}
	for _, quota := range s.store.ListResources("quota-policies") {
		quotasByID[quota.ID] = quota
	}
	for id, project := range aggregate.projectsByID {
		aggregate.costCenters[id] = costCenterForProject(project, teamsByID, quotasByID)
	}
	return aggregate
}

func (a *usageReportAggregate) observe(timestamp time.Time) {
	if !a.earliest.IsZero() && !timestamp.Before(a.earliest) {
		return
	}
	a.earliest = timestamp
	if a.granularity == "day" && usageReportGranularity(a.selectedRange, a.earliest, a.end, a.location) == "month" {
		monthly := map[string]*UsageSummary{}
		for date, bucket := range a.buckets {
			mergeUsageReportSummary(usageReportBucket(monthly, date[:7]+"-01"), *bucket)
		}
		a.buckets, a.granularity = monthly, "month"
	}
}

func (a *usageReportAggregate) addUsage(record UsageRecord) {
	a.observe(record.CreatedAt)
	addUsageReportTokens(&a.summary, record)
	addUsageReportTokens(usageReportBucket(a.buckets, usageReportBucketKey(record.CreatedAt, a.location, a.granularity)), record)
	member := usageReportMemberID(a.user, record, a.keysByID, a.projectsByID, a.usersByID)
	for dimension, key := range map[string]string{
		"projects": record.ProjectID, "models": record.ModelName, "providers": record.ProviderID,
		"provider_resources": record.ProviderResourceID, "cost_centers": a.costCenters[record.ProjectID],
		"api_keys": record.APIKeyID, "members": member,
	} {
		if key == "" {
			key = "unknown"
		}
		addUsageReportTokens(usageReportBucket(a.dimensions[dimension], key), record)
	}
	if a.usedKeysByMember[member] == nil {
		a.usedKeysByMember[member] = map[string]bool{}
	}
	if record.APIKeyID != "" {
		a.usedKeysByMember[member][record.APIKeyID] = true
	}
}

func (a *usageReportAggregate) addRequest(log UsageReportRequest) {
	a.observe(log.CreatedAt)
	bucket := usageReportBucket(a.buckets, usageReportBucketKey(log.CreatedAt, a.location, a.granularity))
	a.summary.RequestCount++
	bucket.RequestCount++
	if log.StatusCode >= http.StatusBadRequest {
		a.summary.Errors++
		bucket.Errors++
	}
}

func (a *usageReportAggregate) breakdown() map[string]any {
	result := map[string]any{}
	for dimension, buckets := range a.dimensions {
		if !isPlatformAdminRole(a.user.Role) && (dimension == "providers" || dimension == "provider_resources") {
			continue
		}
		keys := make([]string, 0, len(buckets))
		for key := range buckets {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			left, right := buckets[keys[i]], buckets[keys[j]]
			if dimension == "members" {
				if left.TotalTokens != right.TotalTokens {
					return left.TotalTokens > right.TotalTokens
				}
				if left.UsageRecordCount != right.UsageRecordCount {
					return left.UsageRecordCount > right.UsageRecordCount
				}
			}
			if left.EstimatedCostUSD != right.EstimatedCostUSD {
				return left.EstimatedCostUSD > right.EstimatedCostUSD
			}
			if left.TotalTokens != right.TotalTokens {
				return left.TotalTokens > right.TotalTokens
			}
			return keys[i] < keys[j]
		})
		rows := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			bucket := buckets[key]
			row := map[string]any{
				"id": key, "request_count": bucket.UsageRecordCount, "input_tokens": bucket.InputTokens,
				"cached_input_tokens": bucket.CachedInputTokens, "output_tokens": bucket.OutputTokens,
				"total_tokens": bucket.TotalTokens, "estimated_cost_usd": bucket.EstimatedCostUSD,
			}
			if dimension == "members" {
				row["owned_key_count"], row["used_key_count"] = a.ownedKeyCount[key], len(a.usedKeysByMember[key])
			}
			rows = append(rows, row)
		}
		result[dimension] = rows
	}
	return result
}

func usageReportMemberID(user AdminUser, record UsageRecord, keysByID map[string]APIKey, projectsByID map[string]Project, usersByID map[string]AdminUser) string {
	memberID := strings.TrimSpace(record.AttributedUserID)
	if !canAttributeUsageToMember(user, usersByID, memberID) {
		memberID = ""
	}
	if memberID == "" {
		if key, ok := keysByID[record.APIKeyID]; ok {
			candidate := usageAttributionUserID(key, projectsByID[key.ProjectID])
			if canAttributeUsageToMember(user, usersByID, candidate) {
				memberID = candidate
			}
		}
	}
	if memberID == "" {
		if project, ok := projectsByID[record.ProjectID]; ok && canAttributeUsageToMember(user, usersByID, project.OwnerUserID) {
			memberID = project.OwnerUserID
		}
	}
	if memberID == "" {
		return "unknown"
	}
	return memberID
}

func usageReportBucket(buckets map[string]*UsageSummary, key string) *UsageSummary {
	if buckets[key] == nil {
		buckets[key] = &UsageSummary{}
	}
	return buckets[key]
}

func addUsageReportTokens(summary *UsageSummary, record UsageRecord) {
	summary.UsageRecordCount++
	summary.InputTokens += record.InputTokens
	summary.CachedInputTokens += record.CachedInputTokens
	summary.CacheWriteTokens += record.CacheWriteTokens
	summary.OutputTokens += record.OutputTokens
	summary.ReasoningTokens += record.ReasoningTokens
	summary.TotalTokens += record.TotalTokens
	summary.EstimatedCostUSD += record.CostUSD
}

func mergeUsageReportSummary(summary *UsageSummary, other UsageSummary) {
	summary.RequestCount += other.RequestCount
	summary.UsageRecordCount += other.UsageRecordCount
	summary.InputTokens += other.InputTokens
	summary.CachedInputTokens += other.CachedInputTokens
	summary.CacheWriteTokens += other.CacheWriteTokens
	summary.OutputTokens += other.OutputTokens
	summary.ReasoningTokens += other.ReasoningTokens
	summary.TotalTokens += other.TotalTokens
	summary.EstimatedCostUSD += other.EstimatedCostUSD
	summary.Errors += other.Errors
}
