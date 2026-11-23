package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestQuotaPolicyUsageBatchMatchesSingleLookups(t *testing.T) {
	store := NewMemoryStore()
	periods, err := currentQuotaPeriods(store.db, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"day", "month"} {
		bucket := periods.Month
		if scope == "day" {
			bucket = periods.Day
		}
		for _, row := range []QuotaBucket{
			// A legacy row without attribution must win over the attributed
			// row, matching the single-row lookup used before batching.
			{KeyID: "user:legacy", Scope: scope, Bucket: bucket, AttributedUserID: "legacy", QuotaCounter: QuotaCounter{Requests: 9}},
			{KeyID: "user:alice", Scope: scope, Bucket: bucket, AttributedUserID: "alice", QuotaCounter: QuotaCounter{Requests: 2, CostUSD: 7}},
			// Usage settled on an API key counts toward its owner.
			{KeyID: "old-key", Scope: scope, Bucket: bucket, AttributedUserID: "alice", QuotaCounter: QuotaCounter{Requests: 5, CostUSD: 1}},
			{KeyID: "key-a", Scope: scope, Bucket: bucket, AttributedUserID: unattributedQuotaUserID, QuotaCounter: QuotaCounter{Requests: 3}},
		} {
			if err := store.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
		// Raw SQL keeps the empty attribution that the model default would
		// otherwise replace.
		if err := store.db.Exec("INSERT INTO quota_buckets (key_id,scope,bucket,attributed_user_id,requests,prompt_tokens,completion_tokens,total_tokens,cost_usd) VALUES (?,?,?,?,?,?,?,?,?)", "user:legacy", scope, bucket, "", 4, 0, 0, 0, 0).Error; err != nil {
			t.Fatal(err)
		}
	}
	targets := []quotaUsageTarget{
		{Scope: "user", ScopeID: "alice"},
		{Scope: "user", ScopeID: "legacy"},
		{Scope: "api_key", ScopeID: "key-a"},
		{Scope: "key", ScopeID: "key-a"},
		{Scope: "team", ScopeID: "x"},
		{Scope: "user", ScopeID: allUsersQuotaScopeID},
		{Scope: "user", ScopeID: ""},
	}
	for i := 0; i < 2400; i++ {
		targets = append(targets,
			quotaUsageTarget{Scope: "user", ScopeID: fmt.Sprintf("user-%d", i)},
			quotaUsageTarget{Scope: "key", ScopeID: fmt.Sprintf("key-%d", i)})
	}
	queries := 0
	for _, kind := range []string{"query", "row"} {
		callback := store.db.Callback().Query()
		if kind == "row" {
			callback = store.db.Callback().Row()
		}
		name := "count_quota_usage_batch_" + kind
		if err := callback.Before("gorm:"+kind).Register(name, func(*gorm.DB) { queries++ }); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = callback.Remove(name) })
	}
	got, err := readQuotaPolicyUsages(context.Background(), store, targets)
	if err != nil {
		t.Fatal(err)
	}
	if queries > 40 {
		t.Fatalf("batch lookup used %d queries for %d targets", queries, len(targets))
	}
	if got[0].Usage.Daily.Requests != 5 || got[0].Usage.Daily.CostUSD != 7 {
		t.Fatalf("user usage did not merge key attribution by max: %+v", got[0].Usage)
	}
	if got[1].Usage.Daily.Requests != 4 || got[1].Usage.Monthly.Requests != 4 {
		t.Fatalf("legacy unattributed row did not win: %+v", got[1].Usage)
	}
	for index, target := range targets[:7] {
		want, supported, err := store.GetQuotaPolicyUsage(target.Scope, target.ScopeID)
		if err != nil || supported != got[index].Supported || want != got[index].Usage {
			t.Fatalf("%+v: got %+v supported %v, want %+v supported %v, err %v", target, got[index].Usage, got[index].Supported, want, supported, err)
		}
	}
}

func TestQuotaPolicyUsageBatchPropagatesCancellation(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readQuotaPolicyUsages(ctx, store, []quotaUsageTarget{{Scope: "user", ScopeID: "alice"}}); err == nil {
		t.Fatal("cancelled request returned usage")
	}
}

type quotaUsageStoreOverride struct {
	Store
	lookups int
}

func (s *quotaUsageStoreOverride) GetQuotaPolicyUsage(scope string, scopeID string) (QuotaPolicyUsage, bool, error) {
	s.lookups++
	if scopeID != "alice" {
		return QuotaPolicyUsage{}, false, nil
	}
	return QuotaPolicyUsage{Monthly: QuotaCounter{Requests: 42}}, true, nil
}

func TestQuotaPolicyUsageBatchHonorsStoreOverrides(t *testing.T) {
	store := &quotaUsageStoreOverride{Store: NewMemoryStore()}
	targets := []quotaUsageTarget{{Scope: "user", ScopeID: "alice"}, {Scope: "user", ScopeID: "bob"}}
	results, err := readQuotaPolicyUsages(context.Background(), store, targets)
	if err != nil {
		t.Fatal(err)
	}
	if store.lookups != len(targets) {
		t.Fatalf("wrapped store did not receive per-target lookups: %d", store.lookups)
	}
	if !results[0].Supported || results[0].Usage.Monthly.Requests != 42 || results[1].Supported {
		t.Fatalf("override results were bypassed: %+v", results)
	}
}
