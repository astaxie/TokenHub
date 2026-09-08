//go:build integration

package server

import "testing"

func TestPostgresBillingPriceConcurrency(t *testing.T) {
	admin, url := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "billing_price_")
	dsn, err := withSearchPath(url, schema)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewStoreWithDialect(dsn, Config{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStoreWithDialect(dsn, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	})
	first.AddModel(Model{ID: "concurrent-model", Name: "concurrent-model", Modality: "chat", InputPriceUSDPer1M: 2, OutputPriceUSDPer1M: 6, Status: StatusActive})
	draft, err := first.BillingModelPricing("concurrent-model")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i, store := range []*GormStore{first, second} {
		go func(i int, store *GormStore) {
			card := draft.Card
			card.Rates.Input = []string{"3", "4"}[i]
			_, err := store.ApplyBillingModel(card, draft.Fingerprint, []string{"concurrent-price-one", "concurrent-price-two"}[i], AdminUser{ID: "admin", Username: "admin"})
			results <- err
		}(i, store)
	}
	applied, conflicted := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			applied++
		} else if AsHTTPError(err).Code == "model_price_changed" {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if applied != 1 || conflicted != 1 {
		t.Fatalf("applied=%d conflicted=%d", applied, conflicted)
	}
	changes, err := first.BillingPriceChanges()
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("price changes=%d", len(changes))
	}
}
