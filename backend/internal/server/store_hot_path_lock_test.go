package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"
)

type routeStatsFailureContextKey struct{}

func TestGatewayHotPathsDoNotAcquireStoreMutex(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Unlocked hot paths", Status: StatusActive})
	model := store.AddModel(Model{Name: "unlocked-hot-path-model", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{Name: "Unlocked provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ModelName: model.Name, ProviderID: provider.ID, ProviderModel: model.Name,
		Status: StatusActive, Priority: 1, Weight: 100,
	})
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "Unlocked key", Allowed: []string{model.Name}, Status: StatusActive,
	}, "thk_unlocked_hot_paths")
	if err != nil {
		t.Fatal(err)
	}

	assertCompletesWhileStoreMutexHeld(t, store, "ValidateAPIKey", func() error {
		_, _, err := store.ValidateAPIKey(secret, "127.0.0.1")
		return err
	})
	assertCompletesWhileStoreMutexHeld(t, store, "SelectRouteCandidates", func() error {
		_, err := store.SelectRouteCandidates(model.Name)
		return err
	})

	var call CallContext
	assertCompletesWhileStoreMutexHeld(t, store, "StartCall", func() error {
		var err error
		call, err = store.StartCall(context.Background(), project, key, model.Name, 0)
		return err
	})
	assertCompletesWhileStoreMutexHeld(t, store, "FinishCall", func() error {
		store.FinishCall(call, RouteSelection{Provider: provider}, Usage{}, http.StatusOK, "", "127.0.0.1", "hot-path-lock-test")
		return nil
	})

	var logs int64
	if err := store.db.Model(&RequestLog{}).Where("request_id = ?", call.RequestID).Count(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if logs != 1 {
		t.Fatalf("finished request logs = %d, want 1", logs)
	}
}

func TestSelectRouteCandidatesFallsBackWhenRuntimeStatsFail(t *testing.T) {
	store := NewMemoryStore()
	model := store.AddModel(Model{Name: "stats-fallback-model", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{Name: "Stats fallback provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	route := store.AddRoute(ModelRoute{
		ModelName: model.Name, ProviderID: provider.ID, ProviderModel: model.Name,
		Status: StatusActive, Strategy: RouteStrategyAdaptive, Priority: 1, Weight: 100,
	})

	callbackName := "test:fail-route-runtime-stats"
	if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		shouldFail, _ := tx.Statement.Context.Value(routeStatsFailureContextKey{}).(bool)
		if shouldFail && tx.Statement.Table == "route_attempt_logs" {
			_ = tx.AddError(errors.New("route runtime stats unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callbackName) })

	ctx := context.WithValue(context.Background(), routeStatsFailureContextKey{}, true)
	selections, err := store.WithContext(ctx).SelectRouteCandidates(model.Name)
	if err != nil {
		t.Fatalf("SelectRouteCandidates failed instead of falling back: %v", err)
	}
	if len(selections) != 1 || selections[0].Route.ID != route.ID {
		t.Fatalf("fallback selections = %+v", selections)
	}
}

func TestUpdateAPIKeyKeepsNotFoundErrorPrecedence(t *testing.T) {
	store := NewMemoryStore()
	invalidLimit := int64(-1)
	_, err := store.UpdateAPIKey("key_missing", APIKey{RateLimitRPM: &invalidLimit})
	if code := AsHTTPError(err).Code; code != "api_key_not_found" {
		t.Fatalf("error code = %q, want api_key_not_found (err=%v)", code, err)
	}
}

func assertCompletesWhileStoreMutexHeld(t *testing.T, store *GormStore, name string, action func() error) {
	t.Helper()
	store.mu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- action()
	}()

	select {
	case err := <-done:
		store.mu.Unlock()
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
	case <-time.After(2 * time.Second):
		store.mu.Unlock()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatalf("%s waited for the store mutex", name)
	}
}
