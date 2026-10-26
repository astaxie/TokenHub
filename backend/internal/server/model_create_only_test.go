package server

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"gorm.io/gorm"
)

func TestModelCreateOnlyPreservesExistingPublicModels(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata map[string]string
		route    bool
	}{
		{"external", map[string]string{"directory_role": "external", "source": "tokenhub-standard-catalog"}, false},
		{"legacy", nil, false},
		{"custom_source", map[string]string{"source": "custom-import"}, false},
		{"routed_reference", map[string]string{"source": "public-provider-conf"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			model := store.AddModel(Model{Name: "existing-model", Modality: "chat", Status: StatusDisabled, InputPriceUSDPer1M: 7, OutputPriceUSDPer1M: 11, Metadata: tc.metadata})
			if tc.route {
				store.AddRoute(ModelRoute{ModelName: model.Name, ProviderID: "existing-provider", ProviderModel: "existing-upstream", Status: StatusDisabled, Weight: 25})
			}
			beforeModels, beforeRoutes := store.ListModels(), store.ListRoutes()
			_, err := store.CreateModelWithRoutes(Model{Name: model.Name, Status: StatusActive, InputPriceUSDPer1M: 1}, []ModelRoute{{ModelName: model.Name, ProviderID: "other-provider", ProviderModel: "other-upstream"}}, ModelCreateOptions{CreateOnly: true})
			if err == nil || AsHTTPError(err).Code != "model_conflict" {
				t.Fatalf("create-only collision = %v, want model_conflict", err)
			}
			if !reflect.DeepEqual(store.ListModels(), beforeModels) || !reflect.DeepEqual(store.ListRoutes(), beforeRoutes) {
				t.Fatal("a rejected creation changed existing model fields or routes")
			}
		})
	}
}

func TestModelCreateOnlyPromotesReferenceWithoutChangingIdentity(t *testing.T) {
	for _, source := range []string{"tokenhub-standard-catalog", "public-provider-conf"} {
		t.Run(source, func(t *testing.T) {
			store := NewMemoryStore()
			reference := store.AddModel(Model{ID: "reference-id", Name: "reference-model", Modality: "chat", InputPriceUSDPer1M: 99, Metadata: map[string]string{"source": source}})
			created, err := store.CreateModelWithRoutes(Model{Name: reference.Name, Modality: "chat", InputPriceUSDPer1M: 3, OutputPriceUSDPer1M: 6}, []ModelRoute{{ModelName: reference.Name, ProviderID: "selected-provider", ProviderModel: reference.Name}}, ModelCreateOptions{CreateOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if created.ID != reference.ID || !created.CreatedAt.Equal(reference.CreatedAt) || created.InputPriceUSDPer1M != 3 || created.Metadata["directory_role"] != "external" {
				t.Fatalf("unexpected promoted model: %+v", created)
			}
			routes := store.ListRoutes()
			if len(routes) != 1 || routes[0].ModelName != reference.Name || routes[0].ProviderID != "selected-provider" || routes[0].ProviderModel != reference.Name {
				t.Fatalf("unexpected promoted model routes: %+v", routes)
			}
			if _, err = store.CreateModelWithRoutes(Model{Name: reference.Name}, nil, ModelCreateOptions{CreateOnly: true}); err == nil || AsHTTPError(err).Code != "model_conflict" {
				t.Fatalf("stale retry after promotion = %v, want model_conflict", err)
			}
		})
	}
}

func TestModelCreateOnlyRechecksAnInsertConflict(t *testing.T) {
	store := NewMemoryStore()
	winner := store.AddModel(withExternalModelRole(Model{Name: "raced-model", InputPriceUSDPer1M: 8}))
	before := store.ListModels()
	stale := false
	callback := "test:model-create-stale-read"
	if err := store.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "models" && !stale {
			// Simulate a lookup that missed a model committed before the insert.
			stale = true
			*tx.Statement.Dest.(*Model) = Model{}
			tx.RowsAffected, tx.Error = 0, gorm.ErrRecordNotFound
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callback) })
	_, err := store.CreateModelWithRoutes(Model{Name: winner.Name, InputPriceUSDPer1M: 1}, []ModelRoute{{ModelName: winner.Name, ProviderID: "loser", ProviderModel: "loser"}}, ModelCreateOptions{CreateOnly: true})
	if err == nil || AsHTTPError(err).Code != "model_conflict" || !stale {
		t.Fatalf("insert conflict = %v, stale lookup exercised = %t", err, stale)
	}
	if !reflect.DeepEqual(store.ListModels(), before) || len(store.ListRoutes()) != 0 {
		t.Fatal("the losing insert overwrote the winner or added a route")
	}
}

func TestModelCreateOnlyConcurrentAttemptsHaveOneWinner(t *testing.T) {
	for _, reference := range []bool{false, true} {
		t.Run(fmt.Sprintf("reference_%t", reference), func(t *testing.T) {
			store := NewMemoryStore()
			const name = "concurrent-model"
			if reference {
				store.AddModel(Model{Name: name, Metadata: map[string]string{"source": "tokenhub-standard-catalog"}})
			}
			type result struct {
				index int
				err   error
			}
			results := make(chan result, 8)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for i := range 8 {
				workers.Go(func() {
					<-start
					_, err := store.CreateModelWithRoutes(Model{Name: name, InputPriceUSDPer1M: float64(i + 1)}, []ModelRoute{{ModelName: name, ProviderID: fmt.Sprintf("provider-%d", i), ProviderModel: name}}, ModelCreateOptions{CreateOnly: true})
					results <- result{i, err}
				})
			}
			close(start)
			workers.Wait()
			close(results)
			winner := -1
			for result := range results {
				if result.err == nil {
					if winner != -1 {
						t.Fatal("multiple create-only attempts succeeded")
					}
					winner = result.index
				} else if AsHTTPError(result.err).Code != "model_conflict" {
					t.Fatalf("losing create-only attempt returned %v", result.err)
				}
			}
			models, routes := store.ListModels(), store.ListRoutes()
			if winner < 0 || len(models) != 1 || len(routes) != 1 || models[0].InputPriceUSDPer1M != float64(winner+1) || routes[0].ProviderID != fmt.Sprintf("provider-%d", winner) {
				t.Fatalf("winner and persisted state disagree: winner=%d models=%+v routes=%+v", winner, models, routes)
			}
		})
	}
}

func TestModelCreateOnlyRollsBackModelAndRoutes(t *testing.T) {
	for _, reference := range []bool{false, true} {
		t.Run(fmt.Sprintf("reference_%t", reference), func(t *testing.T) {
			store := NewMemoryStore()
			const name = "rollback-model"
			if reference {
				store.AddModel(Model{Name: name, InputPriceUSDPer1M: 9, Metadata: map[string]string{"source": "public-provider-conf"}})
			}
			beforeModels, beforeRoutes := store.ListModels(), store.ListRoutes()
			failure := errors.New("synthetic route persistence failure")
			callback := "test:model-create-route-failure"
			if err := store.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if route, ok := tx.Statement.Dest.(*ModelRoute); ok && route.ProviderID == "failing-provider" {
					_ = tx.AddError(failure)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.db.Callback().Create().Remove(callback) })
			_, err := store.CreateModelWithRoutes(Model{Name: name, InputPriceUSDPer1M: 1}, []ModelRoute{{ModelName: name, ProviderID: "successful-provider", ProviderModel: name}, {ModelName: name, ProviderID: "failing-provider", ProviderModel: name}}, ModelCreateOptions{CreateOnly: true})
			if !errors.Is(err, failure) {
				t.Fatalf("create with failed route = %v", err)
			}
			if !reflect.DeepEqual(store.ListModels(), beforeModels) || !reflect.DeepEqual(store.ListRoutes(), beforeRoutes) {
				t.Fatal("route failure left a partial model, promotion, or route")
			}
		})
	}
}

func TestModelCreateOnlyDoesNotReuseClientRouteIDs(t *testing.T) {
	store := NewMemoryStore()
	existing := store.AddRoute(ModelRoute{ID: "existing-route", ModelName: "existing-model", ProviderID: "original-provider", ProviderModel: "original-upstream", Weight: 35})
	_, err := store.CreateModelWithRoutes(Model{Name: "new-model"}, []ModelRoute{{ID: existing.ID, ModelName: "new-model", ProviderID: "new-provider", ProviderModel: "new-upstream"}}, ModelCreateOptions{CreateOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var saved ModelRoute
	if err = store.db.First(&saved, "id = ?", existing.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, existing) || len(store.ListRoutes()) != 2 {
		t.Fatal("creation reused a client route ID and overwrote an unrelated route")
	}
}

func TestModelCreateOnlyRejectsUnrelatedReferenceID(t *testing.T) {
	store := NewMemoryStore()
	reference := store.AddModel(Model{ID: "shared-id", Name: "original-model", Metadata: map[string]string{"source": "public-provider-conf"}})
	before := store.ListModels()
	_, err := store.CreateModelWithRoutes(Model{ID: reference.ID, Name: "different-model"}, nil, ModelCreateOptions{CreateOnly: true})
	if err == nil || AsHTTPError(err).Code != "model_conflict" || !reflect.DeepEqual(store.ListModels(), before) {
		t.Fatalf("unrelated reference ID collision did not preserve models: %v", err)
	}
}
