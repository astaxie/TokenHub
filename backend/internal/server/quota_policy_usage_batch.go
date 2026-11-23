package server

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	"gorm.io/gorm"
)

// quotaUsageTarget names one quota-policy scope whose usage is wanted.
type quotaUsageTarget struct {
	Scope   string
	ScopeID string
}

// quotaUsageResult mirrors the per-target return of GetQuotaPolicyUsage.
type quotaUsageResult struct {
	Usage     QuotaPolicyUsage
	Supported bool
}

// quotaUsageBatchSize keeps each IN (...) clause well below the parameter
// limits of SQLite and PostgreSQL.
const quotaUsageBatchSize = 400

// readQuotaPolicyUsages resolves many quota-policy usages with a bounded
// number of queries. The concrete store path batches by scope; any other Store
// implementation, including test wrappers that override behavior, falls back
// to the existing per-target lookup so its overrides keep applying.
func readQuotaPolicyUsages(ctx context.Context, store Store, targets []quotaUsageTarget) ([]quotaUsageResult, error) {
	if batch, ok := store.(*GormStore); ok {
		return batch.readQuotaPolicyUsages(ctx, targets)
	}
	results := make([]quotaUsageResult, len(targets))
	for index, target := range targets {
		usage, supported, err := store.GetQuotaPolicyUsage(target.Scope, target.ScopeID)
		if err != nil {
			return nil, err
		}
		results[index] = quotaUsageResult{Usage: usage, Supported: supported}
	}
	return results, nil
}

func (s *GormStore) readQuotaPolicyUsages(ctx context.Context, targets []quotaUsageTarget) ([]quotaUsageResult, error) {
	results := make([]quotaUsageResult, len(targets))
	userIDs, keyIDs := []string{}, []string{}
	seenUsers, seenKeys := map[string]bool{}, map[string]bool{}
	for index, target := range targets {
		scope := strings.ToLower(strings.TrimSpace(target.Scope))
		scopeID := strings.TrimSpace(target.ScopeID)
		if scopeID == "" {
			continue
		}
		switch scope {
		case "user":
			if scopeID == allUsersQuotaScopeID {
				continue
			}
			if !seenUsers[scopeID] {
				seenUsers[scopeID] = true
				userIDs = append(userIDs, scopeID)
			}
		case "key", "api_key":
			if !seenKeys[scopeID] {
				seenKeys[scopeID] = true
				keyIDs = append(keyIDs, scopeID)
			}
		default:
			continue
		}
		results[index].Supported = true
	}
	if len(userIDs) == 0 && len(keyIDs) == 0 {
		return results, nil
	}
	// Sort so chunk boundaries, and therefore the query budget, are stable.
	slices.Sort(userIDs)
	slices.Sort(keyIDs)
	userUsage, keyUsage := map[string]QuotaPolicyUsage{}, map[string]QuotaPolicyUsage{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		periods, err := currentQuotaPeriods(tx, now)
		if err != nil {
			return err
		}
		periodQuery := "((scope = ? AND bucket = ?) OR (scope = ? AND bucket = ?))"
		periodArgs := []any{"day", periods.Day, "month", periods.Month}
		userBuckets := make([]string, 0, len(userIDs))
		for _, id := range userIDs {
			userBuckets = append(userBuckets, userQuotaBucketKey(id))
		}
		for _, group := range []struct {
			ids   []string
			users bool
		}{{userBuckets, true}, {keyIDs, false}} {
			for start := 0; start < len(group.ids); start += quotaUsageBatchSize {
				end := min(start+quotaUsageBatchSize, len(group.ids))
				var rows []QuotaBucket
				query := tx.Where("key_id IN ?", group.ids[start:end]).Where(periodQuery, periodArgs...)
				if group.users {
					query = query.Where("attributed_user_id = '' OR key_id = 'user:' || attributed_user_id")
				} else {
					query = query.Where("attributed_user_id = ?", unattributedQuotaUserID)
				}
				if err := query.Order("key_id, scope, bucket, attributed_user_id").Find(&rows).Error; err != nil {
					return err
				}
				// GetQuotaPolicyUsage reads a single row per bucket, so keep the
				// first row of each (key, scope) pair and merge counters by max.
				seen := map[string]bool{}
				for _, row := range rows {
					id := row.KeyID
					if group.users {
						id = strings.TrimPrefix(id, "user:")
						if row.AttributedUserID != "" && row.AttributedUserID != id {
							continue
						}
					}
					pair := id + "\x00" + row.Scope
					if seen[pair] {
						continue
					}
					seen[pair] = true
					if group.users {
						mergeQuotaUsageCounter(userUsage, id, row.Scope, row.QuotaCounter)
					} else {
						mergeQuotaUsageCounter(keyUsage, id, row.Scope, row.QuotaCounter)
					}
				}
			}
		}
		// Usage settled on API-key buckets counts toward the owning user even
		// when no per-user bucket exists yet.
		for start := 0; start < len(userIDs); start += quotaUsageBatchSize {
			end := min(start+quotaUsageBatchSize, len(userIDs))
			var rows []struct {
				AttributedUserID string
				Scope            string
				QuotaCounter
			}
			if err := tx.Table("quota_buckets").
				Select("attributed_user_id, scope, COALESCE(SUM(requests), 0) AS requests, COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens, COALESCE(SUM(completion_tokens), 0) AS completion_tokens, COALESCE(SUM(total_tokens), 0) AS total_tokens, COALESCE(SUM(cost_usd), 0) AS cost_usd").
				Where("attributed_user_id IN ? AND key_id NOT LIKE ?", userIDs[start:end], "user:%").
				Where(periodQuery, periodArgs...).
				Group("attributed_user_id, scope").
				Scan(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				mergeQuotaUsageCounter(userUsage, row.AttributedUserID, row.Scope, row.QuotaCounter)
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	for index, target := range targets {
		if !results[index].Supported {
			continue
		}
		id := strings.TrimSpace(target.ScopeID)
		if strings.EqualFold(strings.TrimSpace(target.Scope), "user") {
			results[index].Usage = userUsage[id]
		} else {
			results[index].Usage = keyUsage[id]
		}
	}
	return results, nil
}

func mergeQuotaUsageCounter(usage map[string]QuotaPolicyUsage, id string, scope string, counter QuotaCounter) {
	value := usage[id]
	switch scope {
	case "day":
		mergeQuotaCounterMax(&value.Daily, counter)
	case "month":
		mergeQuotaCounterMax(&value.Monthly, counter)
	default:
		return
	}
	usage[id] = value
}
