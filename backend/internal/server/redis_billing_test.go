package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"gorm.io/gorm"
)

func newRedisBillingTestStore(t *testing.T) (*GormStore, Project, APIKey) {
	t.Helper()
	redisServer := miniredis.RunT(t)
	store := NewMemoryStoreWithConfig(Config{
		BillingRedisURL: "redis://" + redisServer.Addr() + "/0",
		SecretKey:       "redis-billing-test-secret",
	})
	project := store.CreateProject(Project{ID: "prj_redis_billing", Name: "Redis billing", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_redis_billing", Name: "Redis billing", Status: StatusActive}, "thk_redis_billing")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "redis-billing-model", Modality: "chat", Status: StatusActive})
	return store, project, key
}

func TestRedisBillingSettlesMinuteTokenReservations(t *testing.T) {
	store, project, key := newRedisBillingTestStore(t)
	tpm := int64(10)
	if _, err := store.UpdateAPIKey(key.ID, APIKey{TokenLimitTPM: &tpm, TokenLimitSet: true}); err != nil {
		t.Fatal(err)
	}

	first, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !first.RedisBillingAdmitted || first.TokenLimitBucket == "" {
		t.Fatalf("call was not admitted through Redis billing: %+v", first)
	}
	if _, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 6); err == nil || AsHTTPError(err).Code != "api_key_tpm_exceeded" {
		t.Fatalf("expected Redis TPM rejection, got %v", err)
	}

	store.FinishCall(first, RouteSelection{}, Usage{TotalTokens: 3}, http.StatusOK, "", "127.0.0.1", "redis-billing-test")
	second, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 7)
	if err != nil {
		t.Fatalf("settled Redis reservation should leave seven tokens: %v", err)
	}
	store.FinishCall(second, RouteSelection{}, Usage{TotalTokens: 7}, http.StatusOK, "", "127.0.0.1", "redis-billing-test")

	var minuteRows int64
	if err := store.db.Model(&QuotaBucket{}).Where("scope = ?", "minute").Count(&minuteRows).Error; err != nil {
		t.Fatal(err)
	}
	if minuteRows != 0 {
		t.Fatalf("Redis billing should keep minute counters out of the DB, got %d rows", minuteRows)
	}
}

func TestRedisBillingEnforcesConcurrencyWithoutDatabaseLeases(t *testing.T) {
	store, project, key := newRedisBillingTestStore(t)
	key.Limits.MaxConcurrency = 1
	if _, err := store.UpdateAPIKey(key.ID, APIKey{Limits: key.Limits, LimitsSet: true}); err != nil {
		t.Fatal(err)
	}

	first, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !first.RedisKeyLeaseHeld {
		t.Fatalf("Redis billing did not hold the key concurrency lease: %+v", first)
	}
	if _, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 0); err == nil || AsHTTPError(err).Code != "rate_limit_exceeded" {
		t.Fatalf("expected Redis concurrency rejection, got %v", err)
	}
	var leaseRows int64
	if err := store.db.Model(&InFlightLease{}).Count(&leaseRows).Error; err != nil {
		t.Fatal(err)
	}
	if leaseRows != 0 {
		t.Fatalf("Redis billing should keep key concurrency leases out of the DB, got %d rows", leaseRows)
	}

	store.FinishCall(first, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "redis-billing-test")
	third, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 0)
	if err != nil {
		t.Fatalf("released Redis concurrency lease should admit a new call: %v", err)
	}
	store.FinishCall(third, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "redis-billing-test")
}

func TestRedisBillingURLInitializationErrorsRedactCredentials(t *testing.T) {
	secret := "super-secret-password"
	_, err := newRedisBillingCoordinator(context.Background(), "redis://:"+secret+"@127.0.0.1:0/%zz", 0)
	if err == nil {
		t.Fatal("expected invalid Redis URL error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Redis URL error leaked credentials: %v", err)
	}

	_, err = newRedisBillingCoordinator(context.Background(), "redis://:"+secret+"@127.0.0.1:1/0", 0)
	if err == nil {
		t.Fatal("expected Redis connection error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Redis connection error leaked credentials: %v", err)
	}
}

func TestRedisBillingSettlesResponseJobReservations(t *testing.T) {
	store, project, key := newRedisBillingTestStore(t)
	tpm := int64(10)
	key.Limits.MaxConcurrency = 1
	if _, err := store.UpdateAPIKey(key.ID, APIKey{
		TokenLimitTPM: &tpm,
		TokenLimitSet: true,
		Limits:        key.Limits,
		LimitsSet:     true,
	}); err != nil {
		t.Fatal(err)
	}
	requestJSON := []byte(`{"model":"redis-billing-model","input":"background","background":true}`)
	job, err := store.CreateResponseJob(ResponseJob{
		ID:               NewID("resp"),
		ProjectID:        project.ID,
		APIKeyID:         key.ID,
		AttributedUserID: usageAttributionUserID(key, project),
		Model:            "redis-billing-model",
	}, requestJSON)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimResponseJob("redis-worker", time.Second, time.Minute)
	if err != nil || !ok || claimed.ID != job.ID {
		t.Fatalf("claim response job: job=%+v ok=%v err=%v", claimed, ok, err)
	}
	call, retained, err := store.AdmitResponseJob(context.Background(), job.ID, "redis-worker", claimed.LeaseEpoch, key, "redis-billing-model", 5)
	if err != nil || !retained || !call.RedisBillingAdmitted || !call.RedisKeyLeaseHeld {
		t.Fatalf("admit response job through Redis: retained=%v call=%+v err=%v", retained, call, err)
	}
	if _, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 0); err == nil || AsHTTPError(err).Code != "rate_limit_exceeded" {
		t.Fatalf("expected Redis concurrency rejection while response job is running, got %v", err)
	}
	if _, settled, err := store.FinalizeResponseJob(call, job.ID, "redis-worker", claimed.LeaseEpoch, responseJobStatusSucceeded, []byte(`{"id":"resp_done"}`), RouteSelection{}, Usage{TotalTokens: 3}, http.StatusOK, "", "", "", "", time.Minute); err != nil || !settled {
		t.Fatalf("finalize response job: settled=%v err=%v", settled, err)
	}
	next, err := store.StartCall(context.Background(), project, key, "redis-billing-model", 7)
	if err != nil {
		t.Fatalf("finalized response job should release Redis reservation and lease: %v", err)
	}
	store.FinishCall(next, RouteSelection{}, Usage{TotalTokens: 7}, http.StatusOK, "", "127.0.0.1", "redis-billing-test")
}

func TestRedisBillingPersistedJobsTolerateRedisDisabledAfterRestart(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_redis_restart", Name: "Redis restart", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_redis_restart", Name: "Redis restart", Status: StatusActive}, "thk_redis_restart")
	if err != nil {
		t.Fatal(err)
	}
	admittedAt := time.Now().UTC()
	responseJob := ResponseJob{
		ID:                   "resp_redis_restart",
		ProjectID:            project.ID,
		APIKeyID:             key.ID,
		RequestID:            "req_redis_restart",
		RedisBillingAdmitted: true,
		ReservedTokens:       5,
		AdmittedAt:           &admittedAt,
		Phase:                responseJobPhaseAdmitted,
	}
	imageJob := ImageJob{
		ID:                   "img_redis_restart",
		ProjectID:            project.ID,
		APIKeyID:             key.ID,
		RequestID:            "req_img_redis_restart",
		RedisBillingAdmitted: true,
		ReservedTokens:       5,
		AdmittedAt:           &admittedAt,
	}

	if err := store.db.Transaction(func(tx *gorm.DB) error {
		if err := store.rollbackResponseJobAdmission(tx, responseJob); err != nil {
			return err
		}
		if err := store.refundUndispatchedResponseJobReservation(tx, responseJob); err != nil {
			return err
		}
		return store.rollbackImageJobAdmission(tx, imageJob)
	}); err != nil {
		t.Fatalf("persisted Redis-admitted jobs should tolerate disabled Redis after restart: %v", err)
	}
}
