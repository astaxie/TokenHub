package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/allowance"
)

func TestCodexQuotaErrorEnvelopeIsNotAnUnknownSuccess(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"synthetic-secret"}}`)), Request: r}, nil
	})}
	_, _, err := fetchOpenAIAccountQuotaWithClient(context.Background(), client, "https://quota.example.test/usage", ProviderResourceCredentials{AccessToken: "synthetic-token", AccountID: "synthetic-account"})
	if err == nil || AsHTTPError(err).Code != "openai_quota_invalid_response" || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("unsafe or successful quota error: %v", err)
	}
}

func TestCodexQuotaMissingUsageRemainsUnknown(t *testing.T) {
	now := time.Now().UTC()
	for _, raw := range []string{`{"rate_limit":{"primary_window":{"reset_after_seconds":3600}}}`, `{"rate_limit":{"primary_window":{"used_percent":null}}}`} {
		var quota OpenAIAccountQuota
		if err := json.Unmarshal([]byte(raw), &quota); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(quota)
		if err != nil {
			t.Fatal(err)
		}
		if got := allowance.Normalize(encoded, now).For("model", now, time.Hour).Status; got != allowance.Unknown {
			t.Fatalf("missing native usage became %s: %s", got, encoded)
		}
	}
}

func TestQuotaFailureDoesNotSurviveAccountReplacement(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "quota-error-provider", Name: "Quota", Type: ProviderOpenAICodex, Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{ID: "quota-error-resource", ProviderID: provider.ID, Name: "Original", ResourceType: ProviderResourceOpenAISubscription, Credentials: &ProviderResourceCredentials{AccessToken: "previous-access", AccountID: "previous"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProviderResourceQuota(resource.ID, provider.Type, `{"used_percent":25}`, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &ProviderResourceCredentials{Replace: true, AccessToken: "replacement-access", AccountID: "replacement"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.saveProviderQuotaFailure(context.Background(), resource, "old_account_error", time.Now()); err != nil {
		t.Fatal(err)
	}
	if observation, ok := store.GetProviderResourceObservation(resource.ID); ok && observation.QuotaSnapshot != "" {
		t.Fatal("late failure created a quota for the replacement account")
	}
}
