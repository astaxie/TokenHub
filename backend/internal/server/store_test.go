package server

import (
	"math"
	"testing"
	"time"
)

func TestPriceUsageAppliesConfiguredCacheReadPrice(t *testing.T) {
	model := Model{
		Modality:               "chat",
		InputPriceUSDPer1M:     2,
		CacheReadPriceUSDPer1M: 0.5,
		OutputPriceUSDPer1M:    8,
	}
	usage := priceUsage(model, Usage{
		PromptTokens:      1000,
		CachedInputTokens: 400,
		CompletionTokens:  100,
	})

	if math.Abs(usage.CostUSD-0.0022) > 1e-12 {
		t.Fatalf("cost = %.12f, want 0.0022", usage.CostUSD)
	}
	if usage.TotalTokens != 1100 {
		t.Fatalf("total tokens = %d, want 1100", usage.TotalTokens)
	}
}

func TestEffectiveCacheReadPriceUsesCategoryEstimateWhenUnconfigured(t *testing.T) {
	tests := []struct {
		name  string
		model Model
		want  float64
	}{
		{
			name:  "default ten percent",
			model: Model{Name: "gpt-test", Category: "openai", InputPriceUSDPer1M: 2},
			want:  0.2,
		},
		{
			name:  "deepseek two percent",
			model: Model{Name: "deepseek-test", Category: "deepseek", InputPriceUSDPer1M: 2},
			want:  0.04,
		},
		{
			name:  "deepseek v4 pro current ratio",
			model: Model{Name: "deepseek-v4-pro", Category: "deepseek", InputPriceUSDPer1M: 2},
			want:  2.0 / 120,
		},
		{
			name: "legacy metadata remains supported",
			model: Model{
				Name:               "legacy",
				InputPriceUSDPer1M: 2,
				Metadata:           map[string]string{"cached_input_price_usd_per_1m": "0.3"},
			},
			want: 0.3,
		},
		{
			name: "embedding cache price is unavailable",
			model: Model{
				Name:                   "embedding",
				Modality:               "embedding",
				InputPriceUSDPer1M:     2,
				CacheReadPriceUSDPer1M: 0.3,
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveCacheReadPriceUSDPer1M(tt.model); math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("effective cache price = %.12f, want %.12f", got, tt.want)
			}
		})
	}
}

func TestEmbeddingModelDoesNotStoreCacheReadPrice(t *testing.T) {
	store := NewMemoryStore()
	created := store.AddModel(Model{
		Name:                   "embedding-cache-price",
		Modality:               "embedding",
		CacheReadPriceUSDPer1M: 0.3,
		EmbeddingPriceUSDPer1M: 0.5,
	})
	if created.CacheReadPriceUSDPer1M != 0 {
		t.Fatalf("created embedding cache read price = %v, want 0", created.CacheReadPriceUSDPer1M)
	}

	updated, err := store.UpdateModel(created.Name, Model{
		Modality:               "embedding",
		CacheReadPriceUSDPer1M: 0.4,
		EmbeddingPriceUSDPer1M: 0.6,
	})
	if err != nil {
		t.Fatalf("update embedding model: %v", err)
	}
	if updated.CacheReadPriceUSDPer1M != 0 {
		t.Fatalf("updated embedding cache read price = %v, want 0", updated.CacheReadPriceUSDPer1M)
	}
}

func TestFinishCallPersistsCachedInputTokensInUsageAggregates(t *testing.T) {
	store := NewMemoryStore()
	call := CallContext{
		RequestID: "req_cached_usage",
		Project:   Project{ID: "project_cached_usage"},
		Key:       APIKey{ID: "key_cached_usage", OwnerUserID: "user_cached_usage"},
		Model: Model{
			Name:                   "cached-chat",
			Modality:               "chat",
			InputPriceUSDPer1M:     2,
			CacheReadPriceUSDPer1M: 0.5,
			OutputPriceUSDPer1M:    8,
		},
		StartedAt: time.Now(),
	}
	route := RouteSelection{Provider: Provider{ID: "provider_cached_usage"}}

	store.FinishCall(call, route, Usage{
		PromptTokens:      1000,
		CachedInputTokens: 400,
		CompletionTokens:  100,
	}, 200, "", "127.0.0.1", "store-test")

	records := store.ListUsageRecords()
	if len(records) != 1 {
		t.Fatalf("usage records = %d, want 1", len(records))
	}
	if records[0].CachedInputTokens != 400 {
		t.Fatalf("persisted cached input tokens = %d, want 400", records[0].CachedInputTokens)
	}
	if records[0].AttributedUserID != "user_cached_usage" {
		t.Fatalf("persisted attributed user = %q, want user_cached_usage", records[0].AttributedUserID)
	}
	if math.Abs(records[0].CostUSD-0.0022) > 1e-12 {
		t.Fatalf("persisted cost = %.12f, want 0.0022", records[0].CostUSD)
	}

	summary := store.UsageSummary()
	if got := summary["cached_input_tokens"]; got != int64(400) {
		t.Fatalf("summary cached input tokens = %#v, want 400", got)
	}

	breakdown := store.UsageBreakdown()
	models, ok := breakdown["models"].([]map[string]any)
	if !ok || len(models) != 1 {
		t.Fatalf("model breakdown = %#v, want one row", breakdown["models"])
	}
	if got := models[0]["cached_input_tokens"]; got != int64(400) {
		t.Fatalf("breakdown cached input tokens = %#v, want 400", got)
	}
}

func TestDeleteAdminUserProtectsLastActivePlatformAdmin(t *testing.T) {
	store := NewMemoryStore()
	admin := createTestAdminUser(t, store, "only-admin", "admin")
	member := createTestAdminUser(t, store, "member", "user")

	if err := store.DeleteAdminUser(admin.ID); AsHTTPError(err).Code != "last_admin_user" {
		t.Fatalf("expected last admin deletion to be rejected, got %v", err)
	}
	if err := store.DeleteAdminUser(member.ID); err != nil {
		t.Fatalf("expected ordinary user deletion to remain allowed, got %v", err)
	}
}

func TestUpdateAdminUserProtectsLastActivePlatformAdmin(t *testing.T) {
	tests := []struct {
		name  string
		patch AdminUser
	}{
		{name: "disable", patch: AdminUser{Status: StatusDisabled}},
		{name: "demote", patch: AdminUser{Role: "user"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			admin := createTestAdminUser(t, store, "only-admin-"+tt.name, "system_admin")
			createTestAdminUser(t, store, "member-"+tt.name, "user")

			if _, err := store.UpdateAdminUser(admin.ID, tt.patch, ""); AsHTTPError(err).Code != "last_admin_user" {
				t.Fatalf("expected last admin update to be rejected, got %v", err)
			}
		})
	}
}

func TestAdminUserChangesAllowedWhenAnotherAdminRemains(t *testing.T) {
	store := NewMemoryStore()
	first := createTestAdminUser(t, store, "first-admin", "admin")
	createTestAdminUser(t, store, "second-admin", "system_admin")

	updated, err := store.UpdateAdminUser(first.ID, AdminUser{Role: "user"}, "")
	if err != nil {
		t.Fatalf("expected demotion with another active admin to succeed, got %v", err)
	}
	if updated.Role != "user" {
		t.Fatalf("expected demoted user role, got %q", updated.Role)
	}
}

func createTestAdminUser(t *testing.T, store *GormStore, username string, role string) AdminUser {
	t.Helper()
	user, err := store.CreateAdminUser(AdminUser{
		Username: username,
		Email:    username + "@example.com",
		Role:     role,
		Status:   StatusActive,
	}, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	return user
}
