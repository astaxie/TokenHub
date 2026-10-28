package server

import (
	"context"
	"testing"
)

func TestSubscriptionReauthorizationKeepsConflictStatusAndEnablesFailover(t *testing.T) {
	original := NewHTTPError(409, "provider_resource_reauthorization_required", "account must be authorized again")
	wrapped := subscriptionCredentialError(original)
	if AsHTTPError(wrapped).Status != 409 || AsHTTPError(wrapped).Code != original.Code {
		t.Fatalf("reauthorization status changed: %+v", AsHTTPError(wrapped))
	}
	if providerErrorDisposition(wrapped) != ProviderErrorAuthBroken || !shouldFailoverRoutedError(wrapped, false) {
		t.Fatalf("reauthorization was not classified as a broken account: disposition=%s failover=%v", providerErrorDisposition(wrapped), shouldFailoverRoutedError(wrapped, false))
	}
}

func TestGoogleSubscriptionCredentialsWrapCachedReauthorization(t *testing.T) {
	adapter := &GoogleSubscriptionAdapter{
		ProviderType: providerGeminiCLISubscription,
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{}, NewHTTPError(409, "provider_resource_reauthorization_required", "reauthorize")
		},
	}
	_, err := adapter.credentials(context.Background(), Provider{Type: providerGeminiCLISubscription, Options: map[string]string{"resource_id": "account"}}, nil)
	if AsHTTPError(err).Status != 409 || providerErrorDisposition(err) != ProviderErrorAuthBroken {
		t.Fatalf("cached Google reauthorization was not routable: status=%d disposition=%s", AsHTTPError(err).Status, providerErrorDisposition(err))
	}
}
