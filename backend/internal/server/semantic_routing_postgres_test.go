//go:build integration

package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestSemanticRoutingPostgresPolicy(t *testing.T) {
	storeA, storeB, _ := openSharedPostgresStores(t)
	suffix := NewID("semantic")
	model := storeA.AddModel(Model{Name: "semantic-" + suffix, Modality: "chat", Status: StatusActive, Metadata: map[string]string{"owner": "synthetic"}})
	provider := storeA.AddProvider(Provider{ID: "provider_" + suffix, Name: "Semantic PostgreSQL test", Type: ProviderMock, Healthy: true, Status: StatusActive})
	t.Cleanup(func() { _ = storeA.DeleteProvider(provider.ID); _ = storeA.DeleteModel(model.Name) })
	route := storeA.AddRoute(ModelRoute{ID: "route_" + suffix, ModelName: model.Name, ProviderID: provider.ID, ProviderModel: "synthetic", Priority: 1, Weight: 100, QualityScore: 50, CostScore: 50, Status: StatusActive})
	policy := ModelRoutePolicy{Strategy: RouteStrategyQuality, SemanticRouting: &SemanticRoutingPolicy{Mode: "shadow", MinConfidence: 0.8}, Routes: []ModelRoutePolicyRoute{{RouteID: route.ID, Weight: 75, QualityScore: 60, CostScore: 50}}}
	if _, err := storeA.UpdateModelRoutePolicy(model.Name, policy); err != nil {
		t.Fatal(err)
	}
	var saved Model
	if err := storeB.db.First(&saved, "name = ?", model.Name).Error; err != nil {
		t.Fatal(err)
	}
	if modelSemanticRoutingPolicy(saved).Mode != "shadow" || saved.Metadata["owner"] != "synthetic" {
		t.Fatalf("policy not visible to another instance: %+v", saved.Metadata)
	}
	policy.SemanticRouting = nil
	if _, err := storeB.UpdateModelRoutePolicy(model.Name, policy); err != nil {
		t.Fatal(err)
	}
	if err := storeA.db.First(&saved, "name = ?", model.Name).Error; err != nil {
		t.Fatal(err)
	}
	if modelSemanticRoutingPolicy(saved).Mode != "shadow" {
		t.Fatal("legacy client erased semantic policy")
	}
	// Force a late metadata failure after route updates to verify transaction rollback.
	callback := "semantic_metadata_failure"
	if err := storeB.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "models" {
			_ = tx.AddError(errors.New("synthetic metadata failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeB.db.Callback().Update().Remove(callback) })
	policy.SemanticRouting = &SemanticRoutingPolicy{Mode: "enforce", MinConfidence: 0.9}
	policy.Routes[0].Weight = 10
	if _, err := storeB.UpdateModelRoutePolicy(model.Name, policy); err == nil {
		t.Fatal("expected metadata-write failure")
	}
	var after ModelRoute
	if err := storeA.db.First(&after, "id = ?", route.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Weight != 75 {
		t.Fatalf("failed transaction changed route weight to %d", after.Weight)
	}
}

func TestSemanticRoutingPostgresConcurrentModelEdit(t *testing.T) {
	storeA, storeB, _ := openSharedPostgresStores(t)
	suffix := NewID("semantic_concurrent")
	model := storeA.AddModel(Model{Name: suffix, Modality: "chat", Status: StatusActive, Metadata: map[string]string{semanticRoutingMetadataKey: `{"mode":"enforce","min_confidence":0.8}`}})
	provider := storeA.AddProvider(Provider{ID: "provider_" + suffix, Name: "Concurrent policy test", Type: ProviderMock, Healthy: true, Status: StatusActive})
	route := storeA.AddRoute(ModelRoute{ID: "route_" + suffix, ModelName: model.Name, ProviderID: provider.ID, ProviderModel: "synthetic", Priority: 1, Weight: 100, QualityScore: 50, CostScore: 50, Status: StatusActive})
	t.Cleanup(func() { _ = storeA.DeleteProvider(provider.ID); _ = storeA.DeleteModel(model.Name) })
	readDone := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce, snapshotOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	callback := "semantic_pause_model_edit"
	if err := storeB.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "models" {
			snapshotOnce.Do(func() { close(readDone); <-release })
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeB.db.Callback().Query().Remove(callback) })
	editDone := make(chan error, 1)
	go func() {
		_, err := storeB.UpdateModel(model.Name, Model{Family: "updated", Metadata: model.Metadata})
		editDone <- err
	}()
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("model edit did not reach read barrier")
	}
	pid := make(chan int, 1)
	startedCallback := "semantic_policy_backend_pid"
	if err := storeA.db.Callback().Query().Before("gorm:query").Register(startedCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == "models" {
			var backendPID int
			if err := tx.Statement.ConnPool.QueryRowContext(context.Background(), "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
				_ = tx.AddError(err)
				return
			}
			select {
			case pid <- backendPID:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeA.db.Callback().Query().Remove(startedCallback) })
	policy := ModelRoutePolicy{Strategy: RouteStrategyQuality, SemanticRouting: &SemanticRoutingPolicy{Mode: "off", MinConfidence: 0.8}, Routes: []ModelRoutePolicyRoute{{RouteID: route.ID, Weight: 100, QualityScore: 50, CostScore: 50}}}
	policyDone := make(chan error, 1)
	go func() { _, err := storeA.UpdateModelRoutePolicy(model.Name, policy); policyDone <- err }()
	var backendPID int
	select {
	case backendPID = <-pid:
	case <-time.After(5 * time.Second):
		t.Fatal("policy update did not start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-policyDone:
			t.Fatalf("policy did not wait for the model row lock: %v", err)
		default:
		}
		var waiting int64
		if err := storeA.db.Raw("SELECT count(*) FROM pg_stat_activity WHERE pid = ? AND wait_event_type = 'Lock'", backendPID).Scan(&waiting).Error; err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("policy never waited for model lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	unblock()
	for _, done := range []chan error{editDone, policyDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent update did not finish")
		}
	}
	// A stale browser payload after the disable must not restore its old setting.
	if _, err := storeB.UpdateModel(model.Name, Model{Metadata: model.Metadata}); err != nil {
		t.Fatal(err)
	}
	var current Model
	if err := storeA.db.First(&current, "name = ?", model.Name).Error; err != nil {
		t.Fatal(err)
	}
	if modelSemanticRoutingPolicy(current).Mode != "off" {
		t.Fatal("model edit restored disabled semantic routing")
	}
}
