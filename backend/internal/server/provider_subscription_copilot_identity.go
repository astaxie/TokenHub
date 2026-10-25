package server

import (
	"context"
	"crypto/sha256"
	"strings"
)

type copilotSubscriptionCredentialKey struct{}

type copilotSubscriptionCredentialSnapshot struct {
	resourceID  string
	credentials ProviderResourceCredentials
}

// Model discovery, Auto selection, and inference must use the same account even
// when its saved credentials are replaced while an upstream request is running.
func (a *CopilotSubscriptionAdapter) withCredentialSnapshot(ctx context.Context, provider Provider) (context.Context, ProviderResourceCredentials, error) {
	resourceID := strings.TrimSpace(provider.Options["resource_id"])
	if saved, ok := ctx.Value(copilotSubscriptionCredentialKey{}).(copilotSubscriptionCredentialSnapshot); ok && saved.resourceID == resourceID {
		return ctx, saved.credentials, nil
	}
	credentials, err := subscriptionCredentials(ctx, provider, a.RefreshCredentials, false)
	if err != nil {
		return ctx, ProviderResourceCredentials{}, err
	}
	saved := copilotSubscriptionCredentialSnapshot{resourceID: resourceID, credentials: credentials}
	return context.WithValue(ctx, copilotSubscriptionCredentialKey{}, saved), credentials, nil
}

func copilotSubscriptionAccountKey(resourceID string, credentials ProviderResourceCredentials) [32]byte {
	return sha256.Sum256([]byte(strings.TrimSpace(resourceID) + "\x00" + credentials.AccessToken + "\x00" + credentials.TokenType + "\x00" + credentials.SessionJSON))
}
