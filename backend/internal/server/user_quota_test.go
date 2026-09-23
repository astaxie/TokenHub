package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func setupUserQuotaTest(t *testing.T, limits map[string]any) (*GormStore, Project, APIKey, APIKey) {
	t.Helper()
	store := NewMemoryStore()
	userID := "usr_user_quota"
	if _, err := store.CreateAdminUser(AdminUser{ID: userID, Username: userID, Email: userID + "@example.test", Status: StatusActive, Role: "user"}, "UserQuotaPass123!"); err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{ID: "prj_user_quota", Name: "User quota", OwnerUserID: userID, Status: StatusActive})
	keyA, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_user_quota_a", Name: "key-a", OwnerUserID: userID, Status: StatusActive}, "thk_user_quota_a")
	if err != nil {
		t.Fatal(err)
	}
	keyB, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_user_quota_b", Name: "key-b", OwnerUserID: userID, Status: StatusActive}, "thk_user_quota_b")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "user-quota-model", Modality: "chat", Status: StatusActive})
	store.CreateResource("quota-policies", AdminResource{ID: "quota_user_quota", Name: "User quota", Status: StatusActive, Fields: func() map[string]any {
		fields := map[string]any{"scope": "user", "scope_id": userID}
		for key, value := range limits {
			fields[key] = value
		}
		return fields
	}()})
	return store, project, keyA, keyB
}

func TestUserQuotaAggregatesAcrossAPIKeys(t *testing.T) {
	store, project, keyA, keyB := setupUserQuotaTest(t, map[string]any{"daily_tokens": 10})
	call, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !call.UserQuotaEnabled {
		t.Fatalf("user quota was not attached to the admitted call: key=%+v project=%+v", call.Key, call.Project)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 10}, 200, "", "127.0.0.1", "user-quota-test")
	var bucket QuotaBucket
	if err := store.db.First(&bucket, "key_id = ? AND scope = ? AND bucket = ?", userQuotaBucketKey("usr_user_quota"), "day", dayBucket(call.StartedAt)).Error; err != nil {
		t.Fatal(err)
	}
	if bucket.TotalTokens != 10 {
		t.Fatalf("user daily token counter = %d, want 10", bucket.TotalTokens)
	}

	if _, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 0); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("second key should share the user quota, got %v", err)
	} else if !reflect.DeepEqual(AsHTTPError(err).Details, map[string]string{"scope": "user"}) {
		t.Fatalf("quota rejection should identify the user scope, got %#v", AsHTTPError(err).Details)
	}
}

func TestUserQuotaReservationIsAtomicAcrossAPIKeys(t *testing.T) {
	store, project, keyA, keyB := setupUserQuotaTest(t, map[string]any{"daily_tokens": 5})
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, key := range []APIKey{keyA, keyB} {
		go func(key APIKey) {
			<-start
			call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 5)
			if err == nil {
				store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 5}, 200, "", "127.0.0.1", "user-quota-test")
			}
			results <- err
		}(key)
	}
	close(start)
	allowed := 0
	limited := 0
	for range 2 {
		if err := <-results; err == nil {
			allowed++
		} else if AsHTTPError(err).Code == "quota_exceeded" {
			limited++
		} else {
			t.Fatalf("unexpected concurrent admission error: %v", err)
		}
	}
	if allowed != 1 || limited != 1 {
		t.Fatalf("concurrent user quota admission allowed=%d limited=%d, want 1 and 1", allowed, limited)
	}
}

func TestUserQuotaReconcilesReservedAndActualTokens(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{"daily_tokens": 10, "token_limit_tpm": 10})
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 5)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 3}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	var bucket QuotaBucket
	if err := store.db.First(&bucket, "key_id = ? AND scope = ? AND bucket = ?", userQuotaBucketKey("usr_user_quota"), "day", dayBucket(call.StartedAt)).Error; err != nil {
		t.Fatal(err)
	}
	if bucket.TotalTokens != 3 {
		t.Fatalf("settled user tokens = %d, want 3", bucket.TotalTokens)
	}
	var minuteBucketCounter QuotaBucket
	if err := store.db.First(&minuteBucketCounter, "key_id = ? AND scope = ? AND bucket = ?", userQuotaBucketKey("usr_user_quota"), "minute", call.UserTokenLimitBucket).Error; err != nil {
		t.Fatal(err)
	}
	if minuteBucketCounter.TotalTokens != 3 {
		t.Fatalf("settled user minute tokens = %d, want 3", minuteBucketCounter.TotalTokens)
	}

	second, err := store.StartCall(context.Background(), project, key, "user-quota-model", 7)
	if err != nil {
		t.Fatalf("remaining user quota should admit an exact reservation: %v", err)
	}
	store.FinishCall(second, RouteSelection{}, Usage{TotalTokens: 7}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	if _, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("settled total should exhaust the user quota, got %v", err)
	}
}

func TestUserQuotaRateLimitHeadersUseStrictestMinuteScopes(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{"rate_limit_rpm": 10, "token_limit_tpm": 5})
	if err := store.db.Model(&APIKey{}).Where("id = ?", key.ID).Update("rate_limit_rpm", int64(1)).Error; err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := call.RateLimitHeaders["X-RateLimit-Limit-Requests"]; got != "1" {
		t.Fatalf("request limit header = %q, want strict API key limit", got)
	}
	if got := call.RateLimitHeaders["X-RateLimit-Remaining-Requests"]; got != "0" {
		t.Fatalf("request remaining header = %q, want 0", got)
	}
	if got := call.RateLimitHeaders["X-RateLimit-Limit-Tokens"]; got != "5" {
		t.Fatalf("token limit header = %q, want user TPM limit", got)
	}
	if got := call.RateLimitHeaders["X-RateLimit-Remaining-Tokens"]; got != "2" {
		t.Fatalf("token remaining header = %q, want 2", got)
	}
}

func TestUserMinuteQuotaRejectionsUseGenericQuotaError(t *testing.T) {
	tests := []struct {
		name             string
		limits           map[string]any
		tokenReservation int64
	}{
		{name: "RPM", limits: map[string]any{"rate_limit_rpm": int64(1)}},
		{name: "TPM", limits: map[string]any{"token_limit_tpm": int64(1)}, tokenReservation: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, project, key, _ := setupUserQuotaTest(t, test.limits)
			call, err := store.StartCall(context.Background(), project, key, "user-quota-model", test.tokenReservation)
			if err != nil {
				t.Fatal(err)
			}
			store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: test.tokenReservation}, http.StatusOK, "", "127.0.0.1", "user-quota-test")

			_, err = store.StartCall(context.Background(), project, key, "user-quota-model", test.tokenReservation)
			httpErr := AsHTTPError(err)
			if httpErr.Status != http.StatusTooManyRequests || httpErr.Code != "quota_exceeded" || !reflect.DeepEqual(httpErr.Details, map[string]string{"scope": "user"}) {
				t.Fatalf("user %s rejection = %+v, want 429 quota_exceeded with user scope", test.name, httpErr)
			}
		})
	}
}

func TestUserQuotaSettlementIsIdempotent(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{"daily_tokens": 10})
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 5)
	if err != nil {
		t.Fatal(err)
	}
	usage := Usage{TotalTokens: 3}
	store.FinishCall(call, RouteSelection{}, usage, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	store.FinishCall(call, RouteSelection{}, usage, http.StatusOK, "", "127.0.0.1", "user-quota-test")

	var bucket QuotaBucket
	if err := store.db.First(&bucket, "key_id = ? AND scope = ? AND bucket = ?", userQuotaBucketKey("usr_user_quota"), "day", dayBucket(call.StartedAt)).Error; err != nil {
		t.Fatal(err)
	}
	if bucket.TotalTokens != 3 {
		t.Fatalf("duplicate settlement recorded %d user tokens, want 3", bucket.TotalTokens)
	}
	var requestLogs int64
	if err := store.db.Model(&RequestLog{}).Where("request_id = ?", call.RequestID).Count(&requestLogs).Error; err != nil {
		t.Fatal(err)
	}
	if requestLogs != 1 {
		t.Fatalf("duplicate settlement recorded %d request logs, want 1", requestLogs)
	}
	var usageRecords int64
	if err := store.db.Model(&UsageRecord{}).Where("request_id = ?", call.RequestID).Count(&usageRecords).Error; err != nil {
		t.Fatal(err)
	}
	if usageRecords != 1 {
		t.Fatalf("duplicate settlement recorded %d usage records, want 1", usageRecords)
	}
}

func TestUserQuotaAlertUsesUserScopeID(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{"daily_tokens": 1})
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 1}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	var alert AlertEvent
	if err := store.db.Where("scope_type = ?", "user").Order("created_at DESC").First(&alert).Error; err != nil {
		t.Fatal(err)
	}
	if alert.ScopeID != "aggregate" {
		t.Fatalf("user quota alert scope ID = %q, want bounded aggregate marker", alert.ScopeID)
	}
}

func TestUserQuotaIncludesUsageBeforePolicyCreation(t *testing.T) {
	store, project, keyA, keyB := setupUserQuotaTest(t, map[string]any{})
	call, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 10}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	if err := store.DeleteResource("quota-policies", "quota_user_quota"); err != nil {
		t.Fatal(err)
	}
	store.CreateResource("quota-policies", AdminResource{
		ID: "quota_user_quota_recreated", Name: "User quota", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": "usr_user_quota", "daily_tokens": int64(10)},
	})
	usage, supported, err := store.GetQuotaPolicyUsage("user", "usr_user_quota")
	if err != nil || !supported || usage.Daily.TotalTokens != 10 {
		t.Fatalf("historical user usage was not surfaced: usage=%+v supported=%v err=%v", usage, supported, err)
	}
	if _, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 0); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("historical user usage should block the new policy, got %v", err)
	} else if !reflect.DeepEqual(AsHTTPError(err).Details, map[string]string{"scope": "user"}) {
		t.Fatalf("historical user rejection should identify user scope, got %#v", AsHTTPError(err).Details)
	}
}

func TestInterruptedStreamUsageAppliesToPoliciesCreatedLater(t *testing.T) {
	store, project, keyA, keyB := setupUserQuotaTest(t, map[string]any{})
	keyA, err := store.UpdateAPIKey(keyA.ID, APIKey{TokenLimitSet: true, TokenLimitTPM: int64Pointer(5)})
	if err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 5)
	if err != nil {
		t.Fatal(err)
	}
	call.StreamOutputCommitted = true
	store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusBadGateway, "provider_stream_interrupted", "127.0.0.1", "user-quota-test")

	store.CreateResource("quota-policies", AdminResource{
		ID: "quota_user_quota_after_interrupted_stream", Name: "User quota after interrupted stream", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": "usr_user_quota", "daily_tokens": int64(5)},
	})
	if _, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 0); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("interrupted stream reservation should count toward a later user quota policy, got %v", err)
	}
}

func TestUserQuotaHistoryUsesSettlementAttributionAfterKeyOwnerChanges(t *testing.T) {
	store, project, keyA, keyB := setupUserQuotaTest(t, map[string]any{})
	call, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 10}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	if _, err := store.UpdateAPIKey(keyA.ID, APIKey{OwnerUserID: "usr_new_quota_owner"}); err != nil {
		t.Fatal(err)
	}
	store.CreateResource("quota-policies", AdminResource{
		ID: "quota_user_quota_after_transfer", Name: "User quota after transfer", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": "usr_user_quota", "daily_tokens": int64(10)},
	})
	if _, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 0); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("usage settled before key owner transfer should remain with the original user, got %v", err)
	}
}

func TestQuotaHistorySplitsUsageWhenAPIKeyOwnerChangesWithinBucket(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{})
	if _, err := store.CreateAdminUser(AdminUser{ID: "usr_new_quota_owner", Username: "new-owner", Email: "new-owner@example.test", Status: StatusActive, Role: "user"}, "NewOwnerPass123!"); err != nil {
		t.Fatal(err)
	}
	first, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(first, RouteSelection{}, Usage{TotalTokens: 3}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	if _, err := store.UpdateAPIKey(key.ID, APIKey{OwnerUserID: "usr_new_quota_owner"}); err != nil {
		t.Fatal(err)
	}
	second, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(second, RouteSelection{}, Usage{TotalTokens: 7}, http.StatusOK, "", "127.0.0.1", "user-quota-test")

	for userID, want := range map[string]int64{"usr_user_quota": 3, "usr_new_quota_owner": 7} {
		usage, supported, err := store.GetQuotaPolicyUsage("user", userID)
		if err != nil || !supported || usage.Daily.TotalTokens != want {
			t.Fatalf("owner %s usage = %+v supported=%v err=%v, want %d tokens", userID, usage, supported, err, want)
		}
	}
}

func TestUserQuotaHistorySurvivesAPIKeyDeletion(t *testing.T) {
	store, project, keyA, keyB := setupUserQuotaTest(t, map[string]any{})
	call, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 10}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	if err := store.DeleteAPIKey(keyA.ID); err != nil {
		t.Fatal(err)
	}
	store.CreateResource("quota-policies", AdminResource{
		ID: "quota_user_quota_after_delete", Name: "User quota after delete", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": "usr_user_quota", "daily_tokens": int64(10)},
	})
	usage, supported, err := store.GetQuotaPolicyUsage("user", "usr_user_quota")
	if err != nil || !supported || usage.Daily.TotalTokens != 10 {
		t.Fatalf("deleted-key history was not surfaced: usage=%+v supported=%v err=%v", usage, supported, err)
	}
	if _, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 0); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("deleted-key history should still exhaust the user quota, got %v", err)
	}
}

func TestQuotaSettlementSurvivesAPIKeyDeletionDuringCall(t *testing.T) {
	store, project, keyA, keyB := setupUserQuotaTest(t, map[string]any{"token_limit_tpm": int64(10)})
	keyA, err := store.UpdateAPIKey(keyA.ID, APIKey{TokenLimitSet: true, TokenLimitTPM: int64Pointer(10)})
	if err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAPIKey(keyA.ID); err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 8}, http.StatusOK, "", "127.0.0.1", "user-quota-test")

	usage, supported, err := store.GetQuotaPolicyUsage("user", "usr_user_quota")
	if err != nil || !supported || usage.Daily.TotalTokens != 8 {
		t.Fatalf("usage after deleting in-flight key = %+v supported=%v err=%v, want 8 daily tokens", usage, supported, err)
	}
	for _, bucket := range []struct {
		name               string
		keyID              string
		scope              string
		bucket             string
		attributedUserID   string
		expectedTokenTotal int64
	}{
		{name: "API key minute", keyID: keyA.ID, scope: "minute", bucket: call.TokenLimitBucket, attributedUserID: unattributedQuotaUserID, expectedTokenTotal: 8},
		{name: "user minute", keyID: userQuotaBucketKey("usr_user_quota"), scope: "minute", bucket: call.UserTokenLimitBucket, attributedUserID: "usr_user_quota", expectedTokenTotal: 8},
		{name: "attributed API key day", keyID: keyA.ID, scope: "day", bucket: dayBucket(call.StartedAt), attributedUserID: "usr_user_quota", expectedTokenTotal: 8},
	} {
		var counter QuotaBucket
		if err := store.db.First(&counter, "key_id = ? AND scope = ? AND bucket = ? AND attributed_user_id = ?", bucket.keyID, bucket.scope, bucket.bucket, bucket.attributedUserID).Error; err != nil {
			t.Fatalf("load %s bucket: %v", bucket.name, err)
		}
		if counter.TotalTokens != bucket.expectedTokenTotal {
			t.Fatalf("%s tokens = %d, want %d", bucket.name, counter.TotalTokens, bucket.expectedTokenTotal)
		}
	}

	exact, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 2)
	if err != nil {
		t.Fatalf("remaining minute quota should admit exact reservation: %v", err)
	}
	defer store.FinishCall(exact, RouteSelection{}, Usage{TotalTokens: 2}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	if _, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 1); err == nil || AsHTTPError(err).Code != "quota_exceeded" || !reflect.DeepEqual(AsHTTPError(err).Details, map[string]string{"scope": "user"}) {
		t.Fatalf("settled usage should leave no additional minute quota, got %v", err)
	}
}

func TestInactiveUserQuotaPolicyCanBeDisabled(t *testing.T) {
	store := NewMemoryStore()
	admin, err := store.CreateAdminUser(AdminUser{ID: "usr_quota_admin", Username: "quota-admin", Email: "quota-admin@example.test", Role: "admin", Status: StatusActive}, "QuotaAdminPass123!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAdminUser(AdminUser{ID: "usr_inactive_quota", Username: "inactive-quota", Email: "inactive-quota@example.test", Role: "user", Status: StatusDisabled}, "InactiveQuotaPass123!"); err != nil {
		t.Fatal(err)
	}
	policy := store.CreateResource("quota-policies", AdminResource{
		ID: "quota_inactive_user", Name: "Inactive user quota", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": "usr_inactive_quota", "daily_tokens": int64(10)},
	})
	server := New(store)
	if err := server.validateScopedResourceMutation(admin, "quota-policies", policy.ID, AdminResource{Status: StatusDisabled}); err != nil {
		t.Fatalf("disabling a policy for an inactive user should remain allowed: %v", err)
	}
}

func TestUserQuotaSurvivesKeyRotation(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{"daily_requests": 1})
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{}, 200, "", "127.0.0.1", "user-quota-test")

	graceUntil := time.Now().UTC().Add(time.Hour)
	rotated, _, err := store.RotateAPIKey(key.ID, &graceUntil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCall(context.Background(), project, rotated, "user-quota-model", 0); AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("rotated key should retain the user quota, got %v", err)
	}
}

func TestUserQuotaUsesUsageAttributionFallbacks(t *testing.T) {
	tests := []struct {
		name           string
		projectOwner   string
		keyMetadata    map[string]string
		attributedUser string
	}{
		{
			name:           "key creator",
			projectOwner:   "usr_project_owner",
			keyMetadata:    map[string]string{"created_by": "usr_key_creator"},
			attributedUser: "usr_key_creator",
		},
		{
			name:           "project owner",
			projectOwner:   "usr_project_owner",
			attributedUser: "usr_project_owner",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			project := store.CreateProject(Project{Name: "Attribution fallback", OwnerUserID: test.projectOwner, Status: StatusActive})
			keyA, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "fallback-a", Metadata: test.keyMetadata, Status: StatusActive}, "thk_fallback_a_"+strings.ReplaceAll(test.name, " ", "_"))
			if err != nil {
				t.Fatal(err)
			}
			keyB, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "fallback-b", Metadata: test.keyMetadata, Status: StatusActive}, "thk_fallback_b_"+strings.ReplaceAll(test.name, " ", "_"))
			if err != nil {
				t.Fatal(err)
			}
			store.AddModel(Model{Name: "fallback-model-" + strings.ReplaceAll(test.name, " ", "-"), Modality: "chat", Status: StatusActive})
			store.CreateResource("quota-policies", AdminResource{
				Name: "Fallback quota", Status: StatusActive,
				Fields: map[string]any{"scope": "user", "scope_id": test.attributedUser, "daily_requests": 1},
			})

			model := "fallback-model-" + strings.ReplaceAll(test.name, " ", "-")
			call, err := store.StartCall(context.Background(), project, keyA, model, 0)
			if err != nil {
				t.Fatal(err)
			}
			store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
			if _, err := store.StartCall(context.Background(), project, keyB, model, 0); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
				t.Fatalf("fallback-attributed keys should share the user quota, got %v", err)
			}
		})
	}
}

func TestUserQuotaEnforcesUserAndAPIKeyConcurrencyTogether(t *testing.T) {
	store, project, keyA, keyB := setupUserQuotaTest(t, map[string]any{"max_concurrency": 2})
	for _, key := range []APIKey{keyA, keyB} {
		if err := store.db.Model(&APIKey{}).Where("id = ?", key.ID).Update("limit_max_concurrency", 1).Error; err != nil {
			t.Fatal(err)
		}
	}
	keyC, _, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_user_quota_c", Name: "key-c", OwnerUserID: "usr_user_quota",
		Limits: QuotaLimits{MaxConcurrency: 1}, Status: StatusActive,
	}, "thk_user_quota_c")
	if err != nil {
		t.Fatal(err)
	}

	callA, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.FinishCall(callA, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	if _, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 0); err == nil || AsHTTPError(err).Code != "rate_limit_exceeded" {
		t.Fatalf("second call through the same key should hit the key concurrency limit, got %v", err)
	}

	callB, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer store.FinishCall(callB, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "user-quota-test")
	if _, err := store.StartCall(context.Background(), project, keyC, "user-quota-model", 0); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("third key should hit the aggregate user concurrency limit, got %v", err)
	} else if !reflect.DeepEqual(AsHTTPError(err).Details, map[string]string{"scope": "user"}) {
		t.Fatalf("user concurrency rejection should identify the user scope, got %#v", AsHTTPError(err).Details)
	}
}

func TestBackgroundUserQuotaRefundsDailyReservationWithoutTPM(t *testing.T) {
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{"daily_tokens": 10})
	envelopeJSON, err := json.Marshal(responseJobEnvelope{Request: json.RawMessage(`{"model":"user-quota-model","input":"cancel","background":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateResponseJob(ResponseJob{
		ID: NewID("resp"), ProjectID: project.ID, APIKeyID: key.ID,
		AttributedUserID: usageAttributionUserID(key, project), Model: "user-quota-model",
	}, envelopeJSON)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimResponseJob("user-quota-worker", time.Second, time.Minute)
	if err != nil || !ok || claimed.ID != job.ID {
		t.Fatalf("claim response job: job=%+v ok=%v err=%v", claimed, ok, err)
	}
	call, retained, err := store.AdmitResponseJob(context.Background(), job.ID, "user-quota-worker", claimed.LeaseEpoch, key, "user-quota-model", 5)
	if err != nil || !retained {
		t.Fatalf("admit response job: retained=%v err=%v", retained, err)
	}
	if call.TokenLimitBucket != "" {
		t.Fatalf("daily-only quota unexpectedly created a TPM bucket %q", call.TokenLimitBucket)
	}
	if call.UserTokenLimitBucket != "" {
		t.Fatalf("daily-only quota unexpectedly created a user TPM bucket %q", call.UserTokenLimitBucket)
	}
	if _, ok, err := store.CancelResponseJob(job.ID, "test", time.Minute); err != nil || !ok {
		t.Fatalf("request response job cancellation: ok=%v err=%v", ok, err)
	}
	status, retained, err := store.ShutdownResponseJob(job.ID, "user-quota-worker", claimed.LeaseEpoch, time.Minute)
	if err != nil || !retained || status != responseJobStatusCancelled {
		t.Fatalf("settle cancelled response job: status=%s retained=%v err=%v", status, retained, err)
	}
	var counter QuotaBucket
	if err := store.db.First(&counter, "key_id = ? AND scope = ? AND bucket = ?", userQuotaBucketKey("usr_user_quota"), "day", dayBucket(call.StartedAt)).Error; err != nil {
		t.Fatal(err)
	}
	if counter.TotalTokens != 0 {
		t.Fatalf("cancelled daily-only response retained %d reserved user tokens", counter.TotalTokens)
	}
}

func TestAdminAPIManagesUserQuotaPolicies(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{Username: "quota-admin", Email: "quota-admin@example.test", Status: StatusActive, Role: "admin"}, "AdminQuotaPass123!"); err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateAdminUser(AdminUser{Username: "quota-admin-target", Email: "quota-admin-target@example.test", Status: StatusActive, Role: "user"}, "UserQuotaPass123!")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/resources/quota-policies", map[string]any{
		"name":   "User hard cap",
		"status": StatusActive,
		"fields": map[string]any{"scope": "user", "scope_id": user.ID, "daily_tokens": 100},
	}, "dev_admin_token")
	if created.Code != http.StatusCreated {
		t.Fatalf("create user quota policy: %d %s", created.Code, created.Body)
	}
	var policy AdminResource
	if err := json.Unmarshal([]byte(created.Body), &policy); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.db.Create(&QuotaBucket{
		KeyID: userQuotaBucketKey(user.ID), Scope: "day", Bucket: dayBucket(now), AttributedUserID: user.ID,
		QuotaCounter: QuotaCounter{Requests: 3, TotalTokens: 37, CostUSD: 1.25},
	}).Error; err != nil {
		t.Fatal(err)
	}
	listed := doJSON(t, app, http.MethodGet, "/api/admin/resources/quota-policies", nil, "dev_admin_token")
	if listed.Code != http.StatusOK {
		t.Fatalf("list user quota policies: %d %s", listed.Code, listed.Body)
	}
	var collection struct {
		Data []AdminResource `json:"data"`
	}
	if err := json.Unmarshal([]byte(listed.Body), &collection); err != nil {
		t.Fatal(err)
	}
	foundUsage := false
	for _, item := range collection.Data {
		if item.ID == policy.ID && item.CurrentUsage != nil {
			foundUsage = item.CurrentUsage.Daily.Requests == 3 && item.CurrentUsage.Daily.TotalTokens == 37 && item.CurrentUsage.Daily.CostUSD == 1.25
		}
	}
	if !foundUsage {
		t.Fatalf("user quota list did not include current usage: %+v", collection.Data)
	}
	disabled := doJSON(t, app, http.MethodPatch, "/api/admin/resources/quota-policies/"+policy.ID, map[string]any{
		"status": StatusDisabled,
	}, "dev_admin_token")
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable user quota policy: %d %s", disabled.Code, disabled.Body)
	}
	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/resources/quota-policies/"+policy.ID, nil, "dev_admin_token")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete user quota policy: %d %s", deleted.Code, deleted.Body)
	}

	missing := doJSON(t, app, http.MethodPost, "/api/admin/resources/quota-policies", map[string]any{
		"name":   "Missing user",
		"status": StatusActive,
		"fields": map[string]any{"scope": "user", "scope_id": "usr_missing", "daily_tokens": 100},
	}, "dev_admin_token")
	if missing.Code != http.StatusNotFound || !containsJSONCode(missing.Body, "admin_user_not_found") {
		t.Fatalf("missing user quota target: %d %s", missing.Code, missing.Body)
	}
}

func containsJSONCode(body string, code string) bool {
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal([]byte(body), &payload) == nil && payload.Error.Code == code
}
