package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestModelPricingSourcePlaygroundUsesReferenceAcrossRoutes(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "stream"}[stream], func(t *testing.T) {
			store, source := pricingSourceFixture(t)
			project := store.CreateProject(Project{Name: "Playground pricing", Status: StatusActive})
			model := linkedPricingModel(source)
			store.AddModel(model)
			store.AddProvider(Provider{ID: "actual-route", Name: "Serving provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
			store.AddProviderModel(ProviderModel{ProviderID: "actual-route", UpstreamModel: "served-chat", Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 99, OutputPriceUSDPer1M: 99})
			store.AddRoute(ModelRoute{ModelName: model.Name, ProviderID: "actual-route", ProviderModel: "served-chat", Status: StatusActive, Weight: 100})
			path := "/api/admin/playground/chat"
			if stream {
				path += "/stream"
			}
			response := doJSON(t, New(store).Handler(), http.MethodPost, path, map[string]any{"project_id": project.ID, "model": model.Name, "messages": []ChatMessage{{Role: "user", Content: "price reference"}}}, "")
			if response.Code != http.StatusOK {
				t.Fatalf("playground failed: %d %s", response.Code, response.Body)
			}
			payload := response.Body
			if stream {
				payload = ""
				for _, line := range strings.Split(response.Body, "\n") {
					if strings.HasPrefix(line, "data:") && strings.Contains(line, `"type":"completed"`) {
						payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					}
				}
			}
			var result struct {
				Usage Usage `json:"usage"`
			}
			if err := json.Unmarshal([]byte(payload), &result); err != nil {
				t.Fatalf("decode result: %v body=%s", err, response.Body)
			}
			want := float64(result.Usage.PromptTokens)*2/1e6 + float64(result.Usage.CompletionTokens)*6/1e6
			if want == 0 || math.Abs(result.Usage.CostUSD-want) > 1e-12 {
				t.Fatalf("serving route changed public charge: got=%+v want=%v", result.Usage, want)
			}
			source.Status = StatusDisabled
			store.AddProviderModel(source)
			response = doJSON(t, New(store).Handler(), http.MethodPost, path, map[string]any{"project_id": project.ID, "model": model.Name, "messages": []ChatMessage{{Role: "user", Content: "unavailable"}}}, "")
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body, "model_pricing_source_unavailable") {
				t.Fatalf("playground did not reject unavailable reference: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestModelPricingSourceBackgroundAdmissionUsesFrozenPolicy(t *testing.T) {
	store, source := pricingSourceFixture(t)
	model := linkedPricingModel(source)
	store.AddModel(model)
	project := store.CreateProject(Project{Name: "Background pricing", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Background key", Status: StatusActive}, "background-pricing-key")
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Minute)
	job := ResponseJob{ID: "pricing-job", ProjectID: project.ID, APIKeyID: key.ID, Model: model.Name, Status: responseJobStatusRunning, Phase: responseJobPhaseClaimed, LeaseOwner: "pricing-worker", LeaseEpoch: 1, LeaseExpiresAt: &expires}
	if err := store.db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	call, retained, err := store.AdmitResponseJob(context.Background(), job.ID, job.LeaseOwner, job.LeaseEpoch, key, model.Name, 0)
	if err != nil || !retained {
		t.Fatalf("admit background response: retained=%v err=%v", retained, err)
	}
	defer store.ReleaseResponseJobAdmission(call.RequestID)
	if err := store.DeleteProviderModel(source.ID); err != nil {
		t.Fatal(err)
	}
	if got := priceUsage(call.Model, Usage{PromptTokens: 1000000}).CostUSD; got != 2 {
		t.Fatalf("in-flight background price changed after source deletion: %v", got)
	}
	var entry meteringEntry
	if err := store.db.First(&entry, "id = ?", call.RequestID+":admission").Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entry.Payload, source.ID) {
		t.Fatalf("background admission omitted source provenance: %s", entry.Payload)
	}
}

func TestModelPricingSourceExplicitlyFreeBasePrices(t *testing.T) {
	store, source := pricingSourceFixture(t)
	source.InputPriceUSDPer1M, source.OutputPriceUSDPer1M = 0, 0
	store.AddProviderModel(source)
	custom := linkedPricingModel(source)
	custom.EmbeddingPriceUSDPer1M = 99
	model, err := store.CreateModelWithRoutes(custom, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveModelPricingSource(store.db, model)
	if err != nil {
		t.Fatal(err)
	}
	got := priceUsage(resolved, Usage{PromptTokens: 1000000, CachedInputTokens: 200000, CacheWriteInputTokens: 100000, CompletionTokens: 500000})
	if got.CostUSD != 0 {
		t.Fatalf("confirmed free source fell back to retained custom prices: %+v", got)
	}
	store.AddRoute(ModelRoute{ModelName: model.Name, ProviderID: source.ProviderID, ProviderModel: source.UpstreamModel, Status: StatusActive})
	project := store.CreateProject(Project{Name: "Free discovery", Status: StatusActive})
	if _, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Free key", Status: StatusActive}, "free-discovery-key"); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, New(store).Handler(), http.MethodGet, "/v1/models/"+model.Name, nil, "free-discovery-key")
	if response.Code != http.StatusOK || !strings.Contains(response.Body, `"input_token_price_per_m":0`) {
		t.Fatalf("free source quote inherited custom embedding rate: %d %s", response.Code, response.Body)
	}
	if stored := store.ListModels()[0]; stored.EmbeddingPriceUSDPer1M != 99 {
		t.Fatalf("resolution rewrote stored embedding price: %+v", stored)
	}
}
