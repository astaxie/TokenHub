//go:build integration

package server

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

type hotPathBlockContextKey struct{}
type admissionLockBlockContextKey struct{}
type projectLockBlockContextKey struct{}
type adaptiveStatsFailureContextKey struct{}

func testPostgresGatewayHotPathConcurrency(t *testing.T, store *GormStore) {
	t.Helper()
	suffix := NewID("hot-path-e2e")
	project := store.CreateProject(Project{ID: "prj_" + suffix, Name: "Hot path concurrency", Status: StatusActive})
	model := store.AddModel(Model{ID: "model_" + suffix, Name: "model-" + suffix, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_" + suffix, Name: "Hot path provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ID: "route_" + suffix, ModelName: model.Name, ProviderID: provider.ID,
		ProviderModel: model.Name, Status: StatusActive, Priority: 1, Weight: 100,
	})
	keyA, _, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_a_" + suffix, Name: "Blocked key", Allowed: []string{model.Name}, Status: StatusActive,
	}, "thk_blocked_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	keyB, secretB, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_b_" + suffix, Name: "Independent key", Allowed: []string{model.Name}, Status: StatusActive,
	}, "thk_independent_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.DeleteAPIKey(keyA.ID)
		_ = store.DeleteAPIKey(keyB.ID)
		_ = store.DeleteProvider(provider.ID)
		_ = store.DeleteModel(model.Name)
		_ = store.DeleteProject(project.ID)
	})

	callbackName := "test:block-hot-path-query:" + suffix
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if shouldBlock, _ := tx.Statement.Context.Value(hotPathBlockContextKey{}).(bool); !shouldBlock {
			return
		}
		once.Do(func() {
			close(blocked)
			<-release
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callbackName) })

	blockedContext := context.WithValue(context.Background(), hotPathBlockContextKey{}, true)
	blockedStart := make(chan struct {
		call CallContext
		err  error
	}, 1)
	go func() {
		call, err := store.StartCall(blockedContext, project, keyA, model.Name, 0)
		blockedStart <- struct {
			call CallContext
			err  error
		}{call: call, err: err}
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("blocked StartCall did not reach its first database query")
	}

	independentDone := make(chan error, 1)
	go func() {
		if _, _, err := store.ValidateAPIKey(secretB, "127.0.0.1"); err != nil {
			independentDone <- err
			return
		}
		if _, err := store.SelectRouteCandidates(model.Name); err != nil {
			independentDone <- err
			return
		}
		call, err := store.StartCall(context.Background(), project, keyB, model.Name, 0)
		if err != nil {
			independentDone <- err
			return
		}
		store.FinishCall(call, RouteSelection{Provider: provider}, Usage{}, http.StatusOK, "", "127.0.0.1", "postgres-hot-path-test")
		independentDone <- nil
	}()
	select {
	case err := <-independentDone:
		if err != nil {
			close(release)
			t.Fatalf("independent hot path failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("independent API key was serialized behind a blocked StartCall")
	}

	close(release)
	select {
	case result := <-blockedStart:
		if result.err != nil {
			t.Fatalf("blocked StartCall failed after release: %v", result.err)
		}
		store.FinishCall(result.call, RouteSelection{Provider: provider}, Usage{}, http.StatusOK, "", "127.0.0.1", "postgres-hot-path-test")
	case <-time.After(5 * time.Second):
		t.Fatal("blocked StartCall did not finish after release")
	}
}

func testPostgresGatewayReadSnapshot(t *testing.T, store *GormStore) {
	t.Helper()
	var isolation string
	var readOnly string
	err := store.withReadSnapshot(func(tx *gorm.DB) error {
		if err := tx.Raw("SHOW transaction_isolation").Scan(&isolation).Error; err != nil {
			return err
		}
		return tx.Raw("SHOW transaction_read_only").Scan(&readOnly).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	if isolation != "repeatable read" {
		t.Fatalf("transaction_isolation = %q, want repeatable read", isolation)
	}
	if readOnly != "on" {
		t.Fatalf("transaction_read_only = %q, want on", readOnly)
	}
}

func testPostgresAPIKeyDeletionOrdering(t *testing.T, store *GormStore) {
	t.Helper()
	suffix := NewID("key-delete-order")
	project := store.CreateProject(Project{ID: "prj_" + suffix, Name: "Key deletion ordering", Status: StatusActive})
	model := store.AddModel(Model{ID: "model_" + suffix, Name: "model-" + suffix, Modality: "chat", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_" + suffix, Name: "Deletion ordering key", Allowed: []string{model.Name}, Status: StatusActive,
		Limits: QuotaLimits{MaxConcurrency: 1},
	}, "thk_delete_order_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.DeleteModel(model.Name)
		_ = store.DeleteProject(project.ID)
	})

	callbackName := "test:block-admission-key-read:" + suffix
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	if err := store.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if shouldBlock, _ := tx.Statement.Context.Value(admissionLockBlockContextKey{}).(bool); !shouldBlock {
			return
		}
		once.Do(func() {
			close(blocked)
			<-release
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callbackName) })

	ctx := context.WithValue(context.Background(), admissionLockBlockContextKey{}, true)
	admissionDone := make(chan struct {
		call CallContext
		err  error
	}, 1)
	go func() {
		call, err := store.StartCall(ctx, project, key, model.Name, 0)
		admissionDone <- struct {
			call CallContext
			err  error
		}{call: call, err: err}
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("StartCall did not pause after acquiring the API key lock")
	}

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- store.DeleteAPIKey(key.ID) }()
	select {
	case err := <-deleteDone:
		close(release)
		t.Fatalf("DeleteAPIKey completed before admission released its key lock: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	close(release)

	var call CallContext
	select {
	case result := <-admissionDone:
		if result.err != nil {
			t.Fatalf("StartCall failed after release: %v", result.err)
		}
		call = result.call
	case <-time.After(5 * time.Second):
		t.Fatal("StartCall did not complete after release")
	}
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("DeleteAPIKey failed after admission completed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DeleteAPIKey did not complete after admission released its key lock")
	}

	store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "postgres-key-delete-ordering")
	var quotaRows int64
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ?", key.ID).Count(&quotaRows).Error; err != nil {
		t.Fatal(err)
	}
	var leaseRows int64
	if err := store.db.Model(&InFlightLease{}).Where("scope_type = ? AND scope_id = ?", "api_key", key.ID).Count(&leaseRows).Error; err != nil {
		t.Fatal(err)
	}
	if quotaRows != 0 || leaseRows != 0 {
		t.Fatalf("deleted key retained quota rows=%d lease rows=%d", quotaRows, leaseRows)
	}
	if _, err := store.StartCall(context.Background(), project, key, model.Name, 0); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("deleted API key admission error = %v, want ErrInvalidAPIKey", err)
	}
}

func testPostgresProjectDisableOrdering(t *testing.T, store *GormStore) {
	t.Helper()
	suffix := NewID("project-disable-order")
	project := store.CreateProject(Project{ID: "prj_" + suffix, Name: "Project disable ordering", Status: StatusActive})
	model := store.AddModel(Model{ID: "model_" + suffix, Name: "model-" + suffix, Modality: "chat", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_" + suffix, Name: "Project ordering key", Allowed: []string{model.Name}, Status: StatusActive,
	}, "thk_project_order_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.DeleteAPIKey(key.ID)
		_ = store.DeleteModel(model.Name)
		_ = store.DeleteProject(project.ID)
	})

	callbackName := "test:block-admission-project-read:" + suffix
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	if err := store.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if shouldBlock, _ := tx.Statement.Context.Value(projectLockBlockContextKey{}).(bool); !shouldBlock || tx.Statement.Table != "projects" {
			return
		}
		once.Do(func() {
			close(blocked)
			<-release
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callbackName) })

	ctx := context.WithValue(context.Background(), projectLockBlockContextKey{}, true)
	admissionDone := make(chan struct {
		call CallContext
		err  error
	}, 1)
	go func() {
		call, err := store.StartCall(ctx, project, key, model.Name, 0)
		admissionDone <- struct {
			call CallContext
			err  error
		}{call: call, err: err}
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("StartCall did not pause after acquiring the project lock")
	}

	disableDone := make(chan error, 1)
	go func() {
		_, err := store.UpdateProject(project.ID, Project{Name: project.Name, Status: StatusDisabled})
		disableDone <- err
	}()
	select {
	case err := <-disableDone:
		close(release)
		t.Fatalf("UpdateProject completed before admission released its shared project lock: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	close(release)

	var call CallContext
	select {
	case result := <-admissionDone:
		if result.err != nil {
			t.Fatalf("StartCall failed after release: %v", result.err)
		}
		call = result.call
	case <-time.After(5 * time.Second):
		t.Fatal("StartCall did not complete after release")
	}
	select {
	case err := <-disableDone:
		if err != nil {
			t.Fatalf("UpdateProject failed after admission completed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("UpdateProject did not complete after admission released its project lock")
	}
	store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "postgres-project-disable-ordering")
	if _, err := store.StartCall(context.Background(), project, key, model.Name, 0); !errors.Is(err, ErrAPIKeyDisabled) {
		t.Fatalf("disabled project admission error = %v, want ErrAPIKeyDisabled", err)
	}
}

func testPostgresAdaptiveStatsFailureFallback(t *testing.T, store *GormStore) {
	t.Helper()
	suffix := NewID("adaptive-fallback")
	model := store.AddModel(Model{ID: "model_" + suffix, Name: "model-" + suffix, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_" + suffix, Name: "Adaptive fallback provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ID: "route_" + suffix, ModelName: model.Name, ProviderID: provider.ID,
		ProviderModel: model.Name, Status: StatusActive, Strategy: RouteStrategyAdaptive, Priority: 1, Weight: 100,
	})
	t.Cleanup(func() {
		_ = store.DeleteProvider(provider.ID)
		_ = store.DeleteModel(model.Name)
	})

	callbackName := "test:fail-adaptive-stats:" + suffix
	if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		shouldFail, _ := tx.Statement.Context.Value(adaptiveStatsFailureContextKey{}).(bool)
		if !shouldFail || tx.Statement.Table != "route_attempt_logs" {
			return
		}
		failed := tx.Session(&gorm.Session{NewDB: true}).Exec("SELECT * FROM tokenhub_missing_route_runtime_stats")
		if failed.Error != nil {
			_ = tx.AddError(failed.Error)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callbackName) })

	ctx := context.WithValue(context.Background(), adaptiveStatsFailureContextKey{}, true)
	selections, err := store.WithContext(ctx).SelectRouteCandidates(model.Name)
	if err != nil {
		t.Fatalf("SelectRouteCandidates failed instead of falling back: %v", err)
	}
	if len(selections) != 1 || selections[0].Route.ID != "route_"+suffix {
		t.Fatalf("fallback selections = %+v", selections)
	}
}
