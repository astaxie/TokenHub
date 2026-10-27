package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

type providerCredentialSnapshotCaptureKey struct{}

// Capture identity with the credential load, before upstream work starts.
// Only runtime loaders request this private snapshot; it is never serialized.
func captureProviderCredentialSnapshot(ctx context.Context, resource ProviderResource) {
	if snapshot, ok := ctx.Value(providerCredentialSnapshotCaptureKey{}).(*ProviderResource); ok {
		*snapshot = ProviderResource{ID: resource.ID, ProviderID: resource.ProviderID, ResourceType: resource.ResourceType, CreatedAt: resource.CreatedAt,
			Options: map[string]string{providerCredentialReplacementGenerationOption: strconv.FormatInt(providerCredentialReplacementGeneration(resource.Options), 10)}}
	}
}

func providerSessionPersistenceContext(ctx context.Context, snapshot *ProviderResource) context.Context {
	if snapshot == nil {
		return ctx
	}
	return context.WithValue(ctx, providerCredentialRefreshPersistenceKey{}, *snapshot)
}

func providerCredentialSnapshotKey(snapshot *ProviderResource) string {
	if snapshot == nil {
		return ""
	}
	encoded, _ := json.Marshal([]any{snapshot.ID, snapshot.ProviderID, snapshot.ResourceType, snapshot.CreatedAt, providerCredentialReplacementGeneration(snapshot.Options)})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func claudeSubscriptionScope(resourceID, caller, accessToken, session, model string, snapshot *ProviderResource) string {
	binding, _ := json.Marshal([]string{resourceID, caller, accessToken, session, model, providerCredentialSnapshotKey(snapshot)})
	sum := sha256.Sum256(binding)
	return hex.EncodeToString(sum[:])
}
