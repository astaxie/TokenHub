package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

type subscriptionWorkerModel struct {
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Protocol  string                     `json:"protocol"`
	Fallback  bool                       `json:"-"`
	Variants  map[string]json.RawMessage `json:"variants"`
	reasoning *subscriptionWorkerReasoningSetting
	API       struct {
		ID  string `json:"id"`
		URL string `json:"url"`
		NPM string `json:"npm"`
	} `json:"api"`
	Limit struct {
		Context int64 `json:"context"`
		Output  int64 `json:"output"`
	} `json:"limit"`
	Capabilities struct {
		Reasoning bool            `json:"reasoning"`
		ToolCall  bool            `json:"toolcall"`
		Input     map[string]bool `json:"input"`
		Output    map[string]bool `json:"output"`
	} `json:"capabilities"`
}

func (a *SubscriptionWorkerAdapter) workerModels(ctx context.Context, process *subscriptionWorkerProcess, resourceID string) ([]subscriptionWorkerModel, error) {
	raw, err := process.invoke(ctx, a.command(resourceID, "models"), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Models   []subscriptionWorkerModel `json:"models"`
		Fallback bool                      `json:"fallback"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return nil, NewHTTPError(502, "subscription_models_invalid", "Subscription runtime returned invalid models")
	}
	if len(result.Models) > 10000 {
		return nil, NewHTTPError(502, "subscription_models_invalid", "Subscription runtime returned too many models")
	}
	for index := range result.Models {
		result.Models[index].Fallback = result.Fallback
	}
	process.mu.Lock()
	process.models = append([]subscriptionWorkerModel(nil), result.Models...)
	process.modelsFetched = time.Now()
	process.mu.Unlock()
	return result.Models, nil
}

func (a *SubscriptionWorkerAdapter) workerModel(ctx context.Context, process *subscriptionWorkerProcess, resourceID, model string) (subscriptionWorkerModel, error) {
	process.mu.Lock()
	models := append([]subscriptionWorkerModel(nil), process.models...)
	fresh := time.Since(process.modelsFetched) < 5*time.Minute
	process.mu.Unlock()
	find := func() (subscriptionWorkerModel, bool) {
		for _, candidate := range models {
			if candidate.ID == model {
				return candidate, true
			}
		}
		return subscriptionWorkerModel{}, false
	}
	if fresh {
		if result, ok := find(); ok {
			return result, nil
		}
	}
	var err error
	models, err = a.workerModels(ctx, process, resourceID)
	if err != nil {
		return subscriptionWorkerModel{}, err
	}
	if result, ok := find(); ok {
		return result, nil
	}
	return subscriptionWorkerModel{}, NewHTTPError(404, "subscription_model_unavailable", "The selected model is unavailable for this subscription account")
}

func (a *SubscriptionWorkerAdapter) catalog(models []subscriptionWorkerModel) ProviderCatalogEntry {
	catalogModels := make([]ProviderCatalogModel, 0, len(models))
	source := "subscription-account-live"
	if len(models) == 0 {
		source = "subscription-account-empty"
	}
	for _, model := range models {
		if model.ID == "" {
			continue
		}
		item := ProviderCatalogModel{ID: model.ID, Name: firstNonEmpty(model.Name, model.ID), Type: "chat", ContextWindow: model.Limit.Context, MaxOutputTokens: model.Limit.Output, Metadata: subscriptionWorkerModelMetadata(model)}
		item.SupportedParameters = a.modelParameters(model)
		if model.Fallback {
			source = "subscription-account-fallback"
		}
		for modality, enabled := range model.Capabilities.Input {
			if enabled {
				item.InputModalities = append(item.InputModalities, modality)
			}
		}
		for modality, enabled := range model.Capabilities.Output {
			if enabled {
				item.OutputModalities = append(item.OutputModalities, modality)
			}
		}
		sort.Strings(item.InputModalities)
		sort.Strings(item.OutputModalities)
		if model.Capabilities.Reasoning {
			item.Capabilities = append(item.Capabilities, "reasoning")
		}
		if model.Capabilities.ToolCall {
			item.Capabilities = append(item.Capabilities, "tools")
		}
		catalogModels = append(catalogModels, item)
	}
	sort.Slice(catalogModels, func(i, j int) bool { return catalogModels[i].ID < catalogModels[j].ID })
	entry := subscriptionCatalog(a.Definition.providerType(), a.Definition.Name, "", source, "", catalogModels)
	entry.ID = a.Definition.ID
	return entry
}

func subscriptionWorkerModelMetadata(model subscriptionWorkerModel) map[string]string {
	metadata := map[string]string{"subscription_protocol": model.Protocol, "pricing_status": "unverified", "billing_mode": "subscription"}
	if model.Fallback {
		metadata["subscription_models_source"] = "seed"
		metadata["entitlement_status"] = "unverified"
	} else {
		metadata["subscription_models_source"] = "account"
	}
	if len(model.Variants) == 0 {
		return metadata
	}
	encoded, err := json.Marshal(model.Variants)
	if err != nil || len(encoded) > 64<<10 {
		return metadata
	}
	metadata["subscription_variants"] = string(encoded)
	efforts := subscriptionWorkerReasoningEfforts(model)
	ordered := []string{}
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"} {
		if efforts[effort] {
			ordered = append(ordered, effort)
			delete(efforts, effort)
		}
	}
	remaining := make([]string, 0, len(efforts))
	for effort := range efforts {
		remaining = append(remaining, effort)
	}
	sort.Strings(remaining)
	ordered = append(ordered, remaining...)
	if len(ordered) > 0 {
		metadata["reasoning_effort_options"] = strings.Join(ordered, ",")
	}
	return metadata
}

func subscriptionWorkerReasoningEfforts(model subscriptionWorkerModel) map[string]bool {
	efforts := map[string]bool{}
	for effort := range subscriptionWorkerReasoningSettings(model) {
		if model.Protocol == "gemini" {
			if _, encoded := geminiThinkingConfig(model.ID, effort); !encoded {
				continue
			}
		}
		efforts[effort] = true
	}
	return efforts
}

func (a *SubscriptionWorkerAdapter) resourceModels(ctx context.Context, provider Provider, resource ProviderResource, _ string) (ProviderCatalogEntry, int, error) {
	if provider.Type != a.Definition.providerType() || resource.ResourceType != a.Definition.resourceType() {
		return ProviderCatalogEntry{}, 0, NewHTTPError(400, "provider_resource_type_mismatch", "Resource does not belong to this subscription provider")
	}
	process, release, id, err := a.account(ctx, provider, &resource)
	if err != nil {
		return ProviderCatalogEntry{}, 0, err
	}
	defer release()
	models, err := a.workerModels(ctx, process, id)
	return a.catalog(models), http.StatusOK, err
}

// Preview returns rotated credentials to the authenticated caller for saving.
// Callers must use the account-credentials secret policy on this response.
func (a *SubscriptionWorkerAdapter) Preview(ctx context.Context, credentials ProviderResourceCredentials) (ProviderCatalogEntry, ProviderResourceCredentials, error) {
	if err := validateSubscriptionSession(credentials.SessionJSON); err != nil {
		return ProviderCatalogEntry{}, credentials, err
	}
	process, release, err := a.Pool.acquire(ctx, NewID("preview"), credentials.SessionJSON, nil)
	if err != nil {
		return ProviderCatalogEntry{}, credentials, err
	}
	defer process.cancel()
	defer release()
	models, err := a.workerModels(ctx, process, "preview")
	credentials.SessionJSON = process.authSnapshot()
	return a.catalog(models), credentials, err
}
