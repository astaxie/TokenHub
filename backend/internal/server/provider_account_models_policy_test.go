package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCodexResourceModelsUsesInjectedResourcePolicy(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("If-None-Match") != `"custom-etag"` {
			t.Fatalf("model ETag header = %q, want custom-etag", req.Header.Get("If-None-Match"))
		}
		body := `{"models":[{"slug":"gpt-custom-codex","display_name":"GPT Custom Codex","visibility":"list","supported_in_api":true}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	adapter := CodexSubscriptionAdapter{
		Client:    client,
		ModelsURL: "https://chatgpt.example/backend-api/codex/models",
		RefreshCredentials: func(_ context.Context, resourceID string, force bool) (ProviderResourceCredentials, error) {
			if resourceID != "rsrc_custom_models" || force {
				t.Fatalf("refresh credentials called with resourceID=%q force=%v", resourceID, force)
			}
			return ProviderResourceCredentials{AccessToken: "access_custom", AccountID: "account_custom"}, nil
		},
		SupportsResourceModels: func(providerType string, resourceType string) bool {
			return providerType == "custom_subscription" && resourceType == "custom_subscription_account"
		},
	}

	catalog, status, err := adapter.ResourceModels(
		context.Background(),
		Provider{Type: "custom_subscription"},
		ProviderResource{ID: "rsrc_custom_models", ResourceType: "custom_subscription_account"},
		`"custom-etag"`,
	)
	if err != nil {
		t.Fatalf("resource models: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("resource models status = %d, want 200", status)
	}
	if catalog.ModelsCount != 1 || len(catalog.Models) != 1 || catalog.Models[0].ID != "gpt-custom-codex" {
		t.Fatalf("unexpected custom resource catalog: %+v", catalog)
	}
}
