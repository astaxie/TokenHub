package server

import (
	"context"
	"time"
)

type subscriptionOAuthExchangeResult struct {
	providerAccountOAuthTokenInfo
	ClientSecret string                `json:"client_secret,omitempty"`
	Status       string                `json:"status"`
	Catalog      *ProviderCatalogEntry `json:"catalog,omitempty"`
	CatalogError string                `json:"catalog_error,omitempty"`
}

// Catalog discovery shares the exchanged credentials without putting an unsaved
// account into the store. Refreshes remain in the response even if discovery fails.
func (s *Server) subscriptionOAuthExchangeCatalog(ctx context.Context, definition builtinSubscriptionDefinition, credentials ProviderResourceCredentials) subscriptionOAuthExchangeResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	provider := Provider{Type: definition.providerType}
	resource := ProviderResource{ID: NewID("subscription_preview"), ResourceType: definition.resourceType}
	var catalog ProviderCatalogEntry
	var err error
	switch definition.providerType {
	case ProviderGitHubCopilotSubscription:
		adapter, ok := resolveTypedAdapter[*CopilotSubscriptionAdapter](s.adapterRegistry, definition.providerType)
		if !ok {
			err = unsupportedCapabilityError("Copilot adapter is unavailable")
			break
		}
		// Do not copy the live adapter's mutex or session caches.
		preview := &CopilotSubscriptionAdapter{Client: adapter.Client, CredentialRefreshClient: adapter.CredentialRefreshClient, APIBaseURL: adapter.APIBaseURL, TokenURL: adapter.TokenURL, CLIUserURL: adapter.CLIUserURL, RefreshCredentials: subscriptionPreviewCredentials(&credentials, nil)}
		catalog, _, err = preview.ResourceModels(ctx, provider, resource, "")
	case ProviderChatGPTSubscription:
		adapter, ok := resolveTypedAdapter[*ChatGPTSubscriptionAdapter](s.adapterRegistry, definition.providerType)
		if !ok {
			err = unsupportedCapabilityError("ChatGPT adapter is unavailable")
			break
		}
		preview := *adapter
		preview.RefreshCredentials = subscriptionPreviewCredentials(&credentials, adapter.Refresh)
		catalog, _, err = preview.ResourceModels(ctx, provider, resource, "")
	default:
		adapter, ok := resolveTypedAdapter[*GoogleSubscriptionAdapter](s.adapterRegistry, definition.providerType)
		if !ok {
			err = unsupportedCapabilityError("Google subscription adapter is unavailable")
			break
		}
		preview := *adapter
		preview.RefreshCredentials = subscriptionPreviewCredentials(&credentials, adapter.refresh)
		catalog, _, err = preview.ResourceModels(ctx, provider, resource, "")
	}
	result := subscriptionOAuthExchangeResult{Status: "complete", ClientSecret: credentials.ClientSecret, providerAccountOAuthTokenInfo: providerAccountOAuthTokenInfo{AccessToken: credentials.AccessToken, RefreshToken: credentials.RefreshToken, IDToken: credentials.IDToken, ClientID: credentials.ClientID, Scopes: credentials.Scopes, TokenType: credentials.TokenType, ExpiresAt: credentials.ExpiresAt, AccountEmail: credentials.Email, AccountID: credentials.AccountID, UserID: credentials.UserID, OrganizationID: credentials.OrganizationID, PlanType: credentials.PlanType}}
	if err != nil {
		// Never echo provider errors, URLs, or credential values in this warning.
		result.CatalogError = "subscription_models_unavailable"
	} else {
		result.Catalog = &catalog
	}
	return result
}

func subscriptionPreviewCredentials(current *ProviderResourceCredentials, refresh func(context.Context, ProviderResourceCredentials) (ProviderResourceCredentials, error)) func(context.Context, string, bool) (ProviderResourceCredentials, error) {
	return func(ctx context.Context, _ string, force bool) (ProviderResourceCredentials, error) {
		needed, _ := providerResourceCredentialsNeedRefresh(*current, 2*time.Minute)
		if refresh != nil && current.RefreshToken != "" && (force || needed || current.AccessToken == "" || current.ExpiresAt == "") {
			updated, err := refresh(ctx, *current)
			if err != nil {
				return *current, err
			}
			*current = updated
		}
		return *current, nil
	}
}
