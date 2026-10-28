//go:build integration

package server

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func testPostgresModelCreateOnly(t *testing.T, first, second *GormStore) {
	for _, reference := range []bool{false, true} {
		t.Run(fmt.Sprintf("reference_%t", reference), func(t *testing.T) {
			name := NewID("strict_create")
			t.Cleanup(func() { _ = first.DeleteModel(name) })
			if reference {
				first.AddModel(Model{Name: name, Metadata: map[string]string{"source": "tokenhub-standard-catalog"}})
			}
			readDone := make(chan struct{}, 2)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			if !reference {
				for _, store := range []*GormStore{first, second} {
					callback := "test:strict-model-missing-snapshot"
					var once sync.Once
					if err := store.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
						if tx.Statement.Table == "models" && errors.Is(tx.Error, gorm.ErrRecordNotFound) {
							once.Do(func() {
								readDone <- struct{}{}
								<-release
							})
						}
					}); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callback) })
				}
			}
			type result struct {
				index int
				err   error
			}
			results := make(chan result, 2)
			for index, store := range []*GormStore{first, second} {
				go func() {
					_, err := store.CreateModelWithRoutes(Model{Name: name, InputPriceUSDPer1M: float64(index + 1)}, []ModelRoute{{ModelName: name, ProviderID: fmt.Sprintf("provider-%d", index), ProviderModel: name}}, ModelCreateOptions{CreateOnly: true})
					results <- result{index, err}
				}()
			}
			if !reference {
				for range 2 {
					select {
					case <-readDone:
					case <-time.After(5 * time.Second):
						t.Fatal("concurrent creators did not both observe the absent model")
					}
				}
			}
			unblock()
			winner := -1
			for range 2 {
				select {
				case result := <-results:
					if result.err == nil {
						if winner >= 0 {
							t.Fatal("multiple PostgreSQL creators succeeded")
						}
						winner = result.index
					} else if AsHTTPError(result.err).Code != "model_conflict" {
						t.Fatalf("losing PostgreSQL creator returned %v", result.err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent PostgreSQL creators did not finish")
				}
			}
			var model Model
			var routes []ModelRoute
			if err := first.db.First(&model, "name = ?", name).Error; err != nil {
				t.Fatal(err)
			}
			if err := second.db.Where("model_name = ?", name).Find(&routes).Error; err != nil {
				t.Fatal(err)
			}
			if winner < 0 || model.InputPriceUSDPer1M != float64(winner+1) || model.Metadata["directory_role"] != "external" || len(routes) != 1 || routes[0].ProviderID != fmt.Sprintf("provider-%d", winner) {
				t.Fatalf("the losing creator changed winner state: winner=%d model=%+v routes=%+v", winner, model, routes)
			}
		})
	}
}
