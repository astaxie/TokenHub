package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSubscriptionWorkerRotationRejectsReplacedAccountWithIdenticalSession(t *testing.T) {
	for _, recreate := range []bool{false, true} {
		t.Run(map[bool]string{false: "reauthorize", true: "recreate"}[recreate], func(t *testing.T) {
			store := NewMemoryStore()
			t.Cleanup(func() { _ = store.Close() })
			provider := store.AddProvider(Provider{Name: "Generation fixture", Type: "grok_subscription", Status: StatusActive})
			const original = `{"type":"oauth","access":"same-access","refresh":"same-refresh"}`
			resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Generation account", ResourceType: "grok_account", Credentials: &ProviderResourceCredentials{SessionJSON: original}})
			if err != nil {
				t.Fatal(err)
			}
			provider.Options = map[string]string{"resource_id": resource.ID}
			pool := newSubscriptionWorkerPool(nil)
			t.Cleanup(pool.Close)
			pool.start = func(_ *http.Client, auth string, save func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
				process := fakeSubscriptionProcess(auth, func(map[string]any) []subscriptionWorkerEvent { return nil })
				process.saveAuth = save
				return process, nil
			}
			adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"grok", "Grok"}, Pool: pool,
				RefreshCredentials: providerCredentialLoaderForType(store, provider.Type), SaveSession: store.ReplaceProviderSession}
			ctx := withSubscriptionCaller(context.Background(), "generation-project", "generation-key")
			process, release, _, err := adapter.account(ctx, provider, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if recreate {
				if err := store.DeleteProviderResource(resource.ID); err != nil {
					t.Fatal(err)
				}
				_, err = store.AddProviderResource(ProviderResource{ID: resource.ID, ProviderID: provider.ID, Name: resource.Name, ResourceType: resource.ResourceType, Credentials: &ProviderResourceCredentials{SessionJSON: original}})
			} else {
				_, err = store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &ProviderResourceCredentials{Replace: true, SessionJSON: original}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := process.saveAuth(ctx, original, `{"type":"oauth","access":"stale-rotation"}`); err == nil || AsHTTPError(err).Code != "provider_credentials_changed" {
				t.Fatalf("old worker saved credentials into a replaced account: %v", err)
			}
			release()
			fresh, releaseFresh, _, err := adapter.account(ctx, provider, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseFresh()
			if fresh == process || fresh.authSnapshot() != original {
				t.Fatal("replacement reused the previous account's retained worker")
			}
			const rotated = `{"type":"oauth","access":"current-rotation"}`
			if err := fresh.saveAuth(ctx, original, rotated); err != nil {
				t.Fatalf("new account could not persist its rotation: %v", err)
			}
			current, _ := store.GetProviderResource(resource.ID)
			if store.providerResourceCredentialsForRuntime(current).SessionJSON != rotated {
				t.Fatal("the current worker's credentials were not saved")
			}
		})
	}
}

func TestClaudeSubscriptionRotationRejectsIdenticalReauthorization(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	adapter, _, ctx := claudeSubscriptionFixture(t)
	provider := store.AddProvider(Provider{Name: "Claude generation", Type: providerClaudeSubscription, Status: StatusActive})
	const original = `{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"refresh-original"}}`
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Claude generation account", ResourceType: providerResourceClaudeSubscription, Credentials: &ProviderResourceCredentials{SessionJSON: original}})
	if err != nil {
		t.Fatal(err)
	}
	provider.Options = map[string]string{"resource_id": resource.ID}
	adapter.RefreshCredentials = providerCredentialLoaderForType(store, provider.Type)
	adapter.SaveSession = func(ctx context.Context, id, providerType, previous, next string) error {
		if _, err := store.UpdateProviderResource(id, ProviderResource{Credentials: &ProviderResourceCredentials{Replace: true, SessionJSON: original}}); err != nil {
			return err
		}
		return store.ReplaceProviderSession(ctx, id, providerType, previous, next)
	}
	_, _, err = adapter.Chat(ctx, provider, "refresh-tools", claudeSubscriptionToolRequest())
	if err == nil || AsHTTPError(err).Code != "provider_credentials_changed" {
		t.Fatalf("CLI rotation replaced an explicitly reauthorized account: %v", err)
	}
	adapter.Close()
	current, _ := store.GetProviderResource(resource.ID)
	if store.providerResourceCredentialsForRuntime(current).SessionJSON != original {
		t.Fatal("late CLI rotation overwrote administrator credentials")
	}
}

func TestClaudeSubscriptionContinuationRejectsIdenticalReauthorization(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	adapter, _, ctx := claudeSubscriptionFixture(t)
	provider := store.AddProvider(Provider{Name: "Claude continuation generation", Type: providerClaudeSubscription, Status: StatusActive})
	const original = `{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"refresh-original"}}`
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Claude continuation account", ResourceType: providerResourceClaudeSubscription, Credentials: &ProviderResourceCredentials{SessionJSON: original}})
	if err != nil {
		t.Fatal(err)
	}
	provider.Options = map[string]string{"resource_id": resource.ID}
	adapter.RefreshCredentials = providerCredentialLoaderForType(store, provider.Type)
	adapter.SaveSession = store.ReplaceProviderSession
	request := claudeSubscriptionToolRequest()
	body, _, err := adapter.Chat(ctx, provider, "tools", request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &ProviderResourceCredentials{Replace: true, SessionJSON: original}}); err != nil {
		t.Fatal(err)
	}
	_, _, err = adapter.Chat(ctx, provider, "tools", claudeSubscriptionContinue(t, request, body))
	if err == nil || AsHTTPError(err).Code != "claude_subscription_continuation_invalid" {
		t.Fatalf("old CLI continuation accepted the replaced account: %v", err)
	}
	credentials, err := adapter.RefreshCredentials(ctx, resource.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(credentials)
	if err != nil || strings.Contains(string(encoded), resource.ID) || strings.Contains(string(encoded), "replacement_generation") {
		t.Fatal("runtime account binding leaked into serialized credentials")
	}
}
