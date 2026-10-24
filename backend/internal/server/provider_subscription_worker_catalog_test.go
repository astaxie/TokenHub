package server

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSubscriptionWorkerCatalogRetainsFallbackAndNativeVariants(t *testing.T) {
	process := fakeSubscriptionProcess(`{"type":"api","key":"synthetic"}`, func(command map[string]any) []subscriptionWorkerEvent {
		return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"fallback":true,"models":[{"id":"grok-4.7@500k","protocol":"responses","limit":{"context":500000},"capabilities":{"reasoning":true},"variants":{"high":{"reasoningEffort":"high"},"low":{"reasoningEffort":"low"},"budget":{"thinking":{"budgetTokens":4096}}}}]}`)}}
	})
	defer process.cancel()
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"grok", "Grok"}}
	models, err := adapter.workerModels(context.Background(), process, "account")
	if err != nil {
		t.Fatal(err)
	}
	cached, err := adapter.workerModel(context.Background(), process, "account", "grok-4.7@500k")
	if err != nil || !cached.Fallback {
		t.Fatalf("cached model lost fallback state: %+v %v", cached, err)
	}
	entry := adapter.catalog(models)
	if entry.Source != "subscription-account-fallback" || len(entry.Models) != 1 {
		t.Fatalf("unexpected fallback catalog: %+v", entry)
	}
	model := entry.Models[0]
	if model.ContextWindow != 500000 || model.Metadata["reasoning_effort_options"] != "low,high" || model.Metadata["subscription_protocol"] != "responses" || model.Metadata["subscription_models_source"] != "seed" || model.Metadata["entitlement_status"] != "unverified" {
		t.Fatalf("model facts were not retained: %+v", model)
	}
	var variants map[string]json.RawMessage
	if json.Unmarshal([]byte(model.Metadata["subscription_variants"]), &variants) != nil || len(variants) != 3 {
		t.Fatalf("native variants were not preserved: %v", model.Metadata)
	}
	if model.Metadata["pricing_status"] != "unverified" {
		t.Fatal("subscription allowance must not be interpreted as a free price")
	}
}

func TestSubscriptionWorkerCatalogDistinguishesLiveAndEmptyInventory(t *testing.T) {
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"grok", "Grok"}}
	if entry := adapter.catalog(nil); entry.Source == "subscription-account-live" {
		t.Fatal("an empty inventory must not claim a live model list")
	}
	entry := adapter.catalog([]subscriptionWorkerModel{{ID: "account-model", Protocol: "chat"}})
	if entry.Source != "subscription-account-live" || entry.Models[0].Metadata["subscription_models_source"] != "account" {
		t.Fatalf("live inventory source lost: %+v", entry)
	}
}
