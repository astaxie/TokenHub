package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCopilotSubscriptionCatalogPreservesOutputBudget(t *testing.T) {
	for _, limit := range []int64{4096, 0, -1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
					return
				}
				_, _ = fmt.Fprintf(w, `{"data":[{"id":"limited-model","capabilities":{"type":"chat","limits":{"max_context_window_tokens":128000,"max_output_tokens":%d}}}]}`, limit)
			})
			catalog, _, err := adapter.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(catalog.Models) != 2 || catalog.Models[0].ID != "limited-model" {
				t.Fatalf("Unexpected catalog: %+v", catalog.Models)
			}
			model := catalog.Models[0]
			if model.ContextWindow != 128000 || model.MaxOutputTokens != max(0, limit) {
				t.Fatalf("Account model limits were lost: %+v", model)
			}
			imported := providerModelFromCatalog(provider.ID, model)
			if limit > 0 {
				if !catalogOutputBudgetFits(imported, int(limit)) || catalogOutputBudgetFits(imported, int(limit+1)) {
					t.Fatal("Imported account output budget is not enforced")
				}
			} else if !catalogOutputBudgetFits(imported, 8192) || imported.Metadata["max_output_tokens"] != "" {
				t.Fatal("An unknown output limit must not constrain routing")
			}
		})
	}
}
