package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodexImageForbiddenMarksResourceUnsupportedAndAllowsFailover(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_image_capability",
		Name:    "Codex Image Capability",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_image_capability",
		ProviderID:   provider.ID,
		Name:         "Codex Image Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{
		AdminToken: "test-admin-token",
		SecretKey:  "image-capability-test-secret",
	})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	forbidden := &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusForbidden, "codex_image_forbidden", "This Codex subscription account is not allowed to use image generation"),
		Disposition: ProviderErrorModelUnsupported,
	}
	server.recordProviderImageGenerationCapabilityError(codexImageAdapterRoute(provider, resource), forbidden)
	if !shouldFailoverRoutedError(forbidden, false) {
		t.Fatal("image entitlement failure must allow failover to another account")
	}
	if !providerAttemptOutcome(forbidden).CountsAsHealthy() {
		t.Fatal("image entitlement failure must not degrade account health")
	}
	updated, ok := server.providerResourceByID(resource.ID)
	if !ok {
		t.Fatal("provider resource disappeared")
	}
	if updated.Options[codexImageCapabilityOption] != codexImageCapabilityUnsupported ||
		updated.Options[codexImageCapabilityCheckedAtOption] == "" {
		t.Fatalf("image capability was not persisted: %+v", updated.Options)
	}
}

func TestCodexImageAdapterSuccessMarksResourceSupported(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(realPNGFixture(t))}},
		})
	}))
	defer upstream.Close()
	store, server, provider, resource := newCodexImageAdapterServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()

	imageBytes, _, _, err := server.executeProviderImage(context.Background(), codexImageAdapterRoute(provider, resource), ImageJob{
		Action: "generate", Model: codexImageModelName, Prompt: "Draw one red square.", Quality: "low", Size: "1024x1024",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(imageBytes) == 0 {
		t.Fatal("Codex adapter image generation returned empty image data")
	}
	updated, ok := store.GetProviderResource(resource.ID)
	if !ok || updated.Options[codexImageCapabilityOption] != codexImageCapabilitySupported ||
		updated.Options[codexImageCapabilityCheckedAtOption] == "" {
		t.Fatalf("adapter image success did not persist supported capability: %+v", updated.Options)
	}
}

func TestCodexImageAdapterForbiddenMarksResourceUnsupported(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{"message": "not available"}})
	}))
	defer upstream.Close()
	store, server, provider, resource := newCodexImageAdapterServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()

	_, _, _, err := server.executeProviderImage(context.Background(), codexImageAdapterRoute(provider, resource), ImageJob{
		Action: "generate", Model: codexImageModelName, Prompt: "Draw one red square.", Quality: "low", Size: "1024x1024",
	})
	if AsHTTPError(err).Code != "codex_image_forbidden" {
		t.Fatalf("Codex adapter image error = %v", err)
	}
	updated, ok := store.GetProviderResource(resource.ID)
	if !ok || updated.Options[codexImageCapabilityOption] != codexImageCapabilityUnsupported ||
		updated.Options[codexImageCapabilityCheckedAtOption] == "" {
		t.Fatalf("adapter image forbidden did not persist unsupported capability: %+v", updated.Options)
	}
}

func newCodexImageAdapterServer(t *testing.T, upstreamURL string) (*GormStore, *Server, Provider, ProviderResource) {
	t.Helper()
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_image_adapter_forbidden",
		Name:    "Codex Image Adapter Forbidden",
		Type:    ProviderOpenAICodex,
		BaseURL: upstreamURL + "/backend-api/codex",
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_image_adapter_forbidden",
		ProviderID:   provider.ID,
		Name:         "Codex Image Adapter Account",
		ResourceType: ProviderResourceOpenAISubscription,
		BaseURL:      upstreamURL + "/backend-api/codex",
		Status:       StatusActive,
		Healthy:      true,
		Credentials: &ProviderResourceCredentials{
			AccessToken: "access_forbidden",
			AccountID:   "account_forbidden",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{
		AdminToken: "test-admin-token",
		SecretKey:  "image-adapter-forbidden-secret",
	})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return store, server, provider, resource
}

func codexImageAdapterRoute(provider Provider, resource ProviderResource) RouteSelection {
	return RouteSelection{
		Route: ModelRoute{
			ModelName: codexImageModelName, ProviderID: provider.ID, ProviderModel: codexImageUpstreamModel,
			Status: StatusActive,
		},
		Provider:      provider,
		Resource:      &resource,
		ProviderModel: codexImageUpstreamModel,
	}
}
