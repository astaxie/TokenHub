package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (a *GoogleSubscriptionAdapter) ResourceModels(ctx context.Context, p Provider, r ProviderResource, _ string) (ProviderCatalogEntry, int, error) {
	if p.Type != a.ProviderType || r.ResourceType != a.resourceType() {
		return ProviderCatalogEntry{}, 0, NewHTTPError(400, "provider_resource_models_unsupported", "Account does not belong to this Google subscription provider")
	}
	creds, err := a.credentials(ctx, p, &r)
	if err != nil {
		return ProviderCatalogEntry{}, 0, err
	}
	if r.BaseURL != "" {
		p.BaseURL = r.BaseURL
	}
	catalog, err := a.models(ctx, p, creds)
	return catalog, http.StatusOK, err
}

func (a *GoogleSubscriptionAdapter) ModelsWithCredentials(ctx context.Context, creds ProviderResourceCredentials) (ProviderCatalogEntry, error) {
	needsRefresh, _ := providerResourceCredentialsNeedRefresh(creds, 2*time.Minute)
	if (creds.AccessToken == "" || creds.ExpiresAt == "" || needsRefresh) && creds.RefreshToken != "" {
		var err error
		creds, err = a.refresh(ctx, creds)
		if err != nil {
			return ProviderCatalogEntry{}, err
		}
	}
	return a.models(ctx, Provider{}, creds)
}

func (a *GoogleSubscriptionAdapter) models(ctx context.Context, p Provider, creds ProviderResourceCredentials) (ProviderCatalogEntry, error) {
	project, err := a.project(ctx, p, creds)
	if err != nil {
		return ProviderCatalogEntry{}, err
	}
	models := []ProviderCatalogModel{}
	if a.antigravity() {
		var result struct {
			Models map[string]struct {
				DisplayName     string `json:"displayName"`
				Disabled        bool   `json:"disabled"`
				MaxTokens       int64  `json:"maxTokens"`
				MaxOutputTokens int64  `json:"maxOutputTokens"`
				SupportsImages  bool   `json:"supportsImages"`
			} `json:"models"`
		}
		tiers := []string{}
		for _, tier := range []string{project.PaidTier, project.Tier} {
			if tier != "" && tier != "free-tier" && tier != "legacy-tier" && (len(tiers) == 0 || tiers[0] != tier) {
				tiers = append(tiers, tier)
			}
		}
		tiers = append(tiers, "")
		for _, tier := range tiers {
			body := map[string]any{"project": project.ID}
			if tier != "" {
				body["entitlement"] = map[string]any{"userTier": tier}
			}
			err = a.call(ctx, a.inferenceBase(p), "fetchAvailableModels", creds, body, &result)
			if err == nil {
				break
			}
			failure := AsHTTPError(err)
			if failure == nil || (failure.UpstreamStatus != http.StatusBadRequest && failure.UpstreamStatus != http.StatusForbidden) {
				return ProviderCatalogEntry{}, err
			}
		}
		if err != nil {
			return ProviderCatalogEntry{}, err
		}
		for id, remote := range result.Models {
			if id == "" || remote.Disabled || strings.HasPrefix(id, "chat_") || strings.HasPrefix(id, "tab_") {
				continue
			}
			entry := googleSubscriptionModel(id, firstNonEmpty(remote.DisplayName, id))
			entry.ContextWindow = remote.MaxTokens
			entry.MaxOutputTokens = remote.MaxOutputTokens
			if remote.SupportsImages {
				entry.InputModalities = append(entry.InputModalities, "image")
			}
			models = append(models, entry)
		}
	} else {
		// Quotas gate preview access; the account experiments determine the CLI's
		// advertised model names and how those aliases map to backend IDs.
		var result struct {
			Buckets []struct {
				ModelID string `json:"modelId"`
			} `json:"buckets"`
		}
		if err = a.call(ctx, a.inferenceBase(p), "retrieveUserQuota", creds, map[string]any{"project": project.ID}, &result); err != nil {
			return ProviderCatalogEntry{}, err
		}
		preview := false
		for _, bucket := range result.Buckets {
			if strings.Contains(bucket.ModelID, "-preview") {
				preview = true
			}
		}
		flags, err := a.flags(ctx, p, creds, project.ID)
		if err != nil {
			return ProviderCatalogEntry{}, err
		}
		models = flags.models(preview)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	categories, counts := catalogCategorySummary(models)
	return ProviderCatalogEntry{ID: a.catalogID(), Name: a.name(), DisplayName: a.name(), Type: a.ProviderType, BaseURL: a.inferenceBase(p), DocURL: a.docURL(), Source: "google-code-assist-live", Models: models, ModelsCount: len(models), Categories: categories, CategoryCounts: counts}, nil
}

func googleSubscriptionModel(id, name string) ProviderCatalogModel {
	category := "gemini"
	if strings.HasPrefix(id, "claude") {
		category = "claude"
	} else if strings.HasPrefix(id, "gpt") {
		category = "openai"
	}
	return ProviderCatalogModel{ID: id, Name: name, DisplayName: name, Category: category, Type: "chat", InputModalities: []string{"text"}, OutputModalities: []string{"text"}, Capabilities: []string{"chat", "tool_call"}}
}

func (a *GoogleSubscriptionAdapter) DefaultProbeRequest() ProviderProbeRequest {
	model := "gemini-3-flash"
	if a.antigravity() {
		model = "gemini-3.1-flash-lite"
	}
	return ProviderProbeRequest{Model: model, Prompt: "Reply with OK."}
}
func (a *GoogleSubscriptionAdapter) Probe(ctx context.Context, p Provider, r ProviderResource, request ProviderProbeRequest) (ProviderProbeResult, error) {
	creds, err := a.credentials(ctx, p, &r)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	p.APIKey = creds.AccessToken
	p.Options = map[string]string{"organization_id": creds.OrganizationID}
	if r.BaseURL != "" {
		p.BaseURL = r.BaseURL
	}
	model := firstNonEmpty(request.Model, a.DefaultProbeRequest().Model)
	start := time.Now()
	response, usage, err := a.Chat(ctx, p, model, ChatCompletionRequest{Model: model, Messages: []ChatMessage{{Role: "user", Content: firstNonEmpty(request.Prompt, "Reply with OK.")}}})
	if err != nil {
		return ProviderProbeResult{}, err
	}
	result, _ := response.(map[string]any)
	return ProviderProbeResult{ResourceID: r.ID, Model: model, OutputText: choiceText(result), Usage: usage, LatencyMS: time.Since(start).Milliseconds(), Response: result}, nil
}
