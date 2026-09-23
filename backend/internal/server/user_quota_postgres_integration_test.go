//go:build integration

package server

import (
	"context"
	"net/http"
	"testing"
)

func TestUserQuotaIsAtomicAcrossPostgresInstances(t *testing.T) {
	storeA, storeB, _ := openSharedPostgresStores(t)
	suffix := NewID("user_quota")
	userID := "usr_" + suffix
	project := storeA.CreateProject(Project{ID: "prj_" + suffix, Name: "PostgreSQL user quota", OwnerUserID: userID, Status: StatusActive})
	modelName := "model-" + suffix
	storeA.AddModel(Model{ID: modelName, Name: modelName, Modality: "chat", Status: StatusActive})
	keyA, _, err := storeA.CreateAPIKey(project.ID, APIKey{ID: "key_a_" + suffix, Name: "key-a", OwnerUserID: userID, Status: StatusActive}, "thk_a_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	keyB, _, err := storeA.CreateAPIKey(project.ID, APIKey{ID: "key_b_" + suffix, Name: "key-b", OwnerUserID: userID, Status: StatusActive}, "thk_b_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	policy := storeA.CreateResource("quota-policies", AdminResource{
		ID: "quota_" + suffix, Name: "PostgreSQL aggregate user quota", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": userID, "daily_tokens": 5},
	})
	t.Cleanup(func() {
		_ = storeA.DeleteResource("quota-policies", policy.ID)
		_ = storeA.DeleteAPIKey(keyA.ID)
		_ = storeA.DeleteAPIKey(keyB.ID)
		_ = storeA.DeleteModel(modelName)
		_ = storeA.DeleteProject(project.ID)
		_ = storeA.db.Where("key_id = ?", userQuotaBucketKey(userID)).Delete(&QuotaBucket{}).Error
	})

	type result struct {
		store *GormStore
		call  CallContext
		err   error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, attempt := range []struct {
		store *GormStore
		key   APIKey
	}{{store: storeA, key: keyA}, {store: storeB, key: keyB}} {
		go func(attempt struct {
			store *GormStore
			key   APIKey
		}) {
			<-start
			call, err := attempt.store.StartCall(context.Background(), project, attempt.key, modelName, 5)
			results <- result{store: attempt.store, call: call, err: err}
		}(attempt)
	}
	close(start)
	allowed := 0
	limited := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			allowed++
			result.store.FinishCall(result.call, RouteSelection{}, Usage{TotalTokens: 5}, http.StatusOK, "", "127.0.0.1", "postgres-user-quota-test")
			continue
		}
		details, _ := AsHTTPError(result.err).Details.(map[string]string)
		if AsHTTPError(result.err).Code == "quota_exceeded" && details["scope"] == "user" {
			limited++
			continue
		}
		t.Fatalf("unexpected PostgreSQL user quota admission error: %v", result.err)
	}
	if allowed != 1 || limited != 1 {
		t.Fatalf("PostgreSQL aggregate admission allowed=%d limited=%d, want 1 and 1", allowed, limited)
	}
}
