package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
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

func TestQwenWorkEnterpriseSeedFallbackRetainsCatalogProvenance(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node runtime is not installed")
	}
	client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "tenant.example" || request.URL.Path != "/algo/api/v2/model/list" {
			t.Errorf("unexpected catalog request: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"message":"Model catalog is unavailable"}`)), Request: request}, nil
	})}
	pool := newSubscriptionWorkerPool(client)
	defer pool.Close()
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"qwenwork-enterprise", "QwenWork Enterprise"}, Pool: pool}
	auth, err := json.Marshal(map[string]any{"type": "oauth", "uid": "synthetic-user", "access": "synthetic-device", "refresh": "synthetic-refresh",
		"deviceToken": "synthetic-device", "deviceRefresh": "synthetic-refresh", "machineId": "synthetic-machine",
		"deviceChat": true, "enterpriseHost": "tenant.example", "expires": time.Now().Add(time.Hour).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	catalog, _, err := adapter.Preview(ctx, ProviderResourceCredentials{SessionJSON: string(auth)})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Source != "subscription-account-fallback" || len(catalog.Models) != 3 {
		t.Fatalf("enterprise seed list was presented as a live catalog: %+v", catalog)
	}
	for _, model := range catalog.Models {
		if model.Metadata["subscription_models_source"] != "seed" || model.Metadata["entitlement_status"] != "unverified" {
			t.Fatalf("enterprise seed model lost its unverified provenance: %+v", model)
		}
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
