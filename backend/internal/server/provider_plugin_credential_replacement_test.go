package server

import (
	"context"
	"net/http"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestPluginCredentialRefreshRejectsReplacedAccount(t *testing.T) {
	for _, entry := range []string{"raw", "encoded"} {
		for _, outcome := range []string{"credentials", "reauthorization"} {
			t.Run(entry+"/"+outcome, func(t *testing.T) {
				store := NewMemoryStore()
				server := New(store)
				t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
				definition := subscriptionWorkerDefinition{"qoder", "Qoder"}
				provider := store.AddProvider(Provider{Name: "Refresh replacement", Type: definition.providerType(), Status: StatusActive})
				resource, err := store.AddProviderResource(ProviderResource{
					ProviderID: provider.ID, Name: "Refresh account", ResourceType: definition.resourceType(),
					Credentials: &ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"original"}`},
				})
				if err != nil {
					t.Fatal(err)
				}
				const replacement = `{"type":"api","key":"replacement"}`
				descriptor := pluginmeta.ActionDescriptor{PluginID: definition.pluginID(), ActionID: "test.refresh", Kind: pluginmeta.ActionKindMutate, Capability: "credentials.refresh", Subject: definition.providerType(), Metadata: map[string]string{"provider_resource_type": definition.resourceType()}}
				if err := server.pluginActions.Register(descriptor, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
					if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &ProviderResourceCredentials{Replace: true, SessionJSON: replacement}}); err != nil {
						return pluginmeta.ActionResult{}, err
					}
					if outcome == "reauthorization" {
						return pluginmeta.ActionResult{Data: map[string]any{"reauthorization_required": true}}, nil
					}
					return pluginmeta.ActionResult{Data: map[string]any{"credentials": &ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"old-refreshed"}`}}}, nil
				})); err != nil {
					t.Fatal(err)
				}
				payload := map[string]any{"resource_id": resource.ID}
				var status int
				if entry == "raw" {
					response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/"+definition.pluginID()+"/actions/"+descriptor.ActionID, payload, "dev_admin_token")
					status = response.Code
				} else {
					_, err := server.executeEncodedPluginAction(context.Background(), AdminUser{ID: "dev_admin"}, descriptor.PluginID, descriptor.ActionID, payload, providerPluginActionOptions{ApplySideEffects: true})
					status = http.StatusOK
					if err != nil {
						status = AsHTTPError(err).Status
					}
				}
				if status != http.StatusConflict {
					t.Errorf("late refresh status = %d, want 409", status)
				}
				current, _ := store.GetProviderResource(resource.ID)
				credentials := store.providerResourceCredentialsForRuntime(current)
				if credentials.SessionJSON != replacement {
					t.Error("late refresh overwrote replacement credentials")
				}
				if current.CredentialSummary[providerResourceReauthorizationRequiredOption] == "true" {
					t.Error("late refresh invalidated replacement credentials")
				}
			})
		}
	}
}

func TestPluginCredentialRefreshChecksAccountBinding(t *testing.T) {
	for _, mutation := range []string{"move", "delete", "recreate"} {
		for _, reauthorization := range []bool{false, true} {
			outcome := "credentials"
			if reauthorization {
				outcome = "reauthorization"
			}
			t.Run(mutation+"/"+outcome, func(t *testing.T) {
				store := NewMemoryStore()
				server := New(store)
				t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
				provider := store.AddProvider(Provider{Name: "Bound subscription", Type: "qoder_subscription", Status: StatusActive})
				const original = `{"type":"api","key":"original"}`
				resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Bound account", ResourceType: "qoder_account", Credentials: &ProviderResourceCredentials{SessionJSON: original}})
				if err != nil {
					t.Fatal(err)
				}
				if mutation == "move" {
					target := store.AddProvider(Provider{Name: "Target subscription", Type: provider.Type, Status: StatusActive})
					_, err = store.UpdateProviderResource(resource.ID, ProviderResource{ProviderID: target.ID})
				} else {
					err = store.DeleteProviderResource(resource.ID)
					if err == nil && mutation == "recreate" {
						_, err = store.AddProviderResource(ProviderResource{ID: resource.ID, ProviderID: provider.ID, Name: resource.Name, ResourceType: resource.ResourceType, Credentials: &ProviderResourceCredentials{SessionJSON: original}})
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				credentials := &ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"stale-refresh"}`}
				if reauthorization {
					credentials = nil
				}
				_, err = store.ApplyProviderResourceCredentialRefresh(context.Background(), resource, credentials, reauthorization)
				want := http.StatusConflict
				if mutation == "delete" {
					want = http.StatusNotFound
				}
				if err == nil || AsHTTPError(err).Status != want {
					t.Fatalf("refresh after %s returned %v, want status %d", mutation, err, want)
				}
				current, exists := store.GetProviderResource(resource.ID)
				if mutation == "delete" {
					if exists {
						t.Fatal("late refresh recreated a deleted resource")
					}
					return
				}
				if store.providerResourceCredentialsForRuntime(current).SessionJSON != original || current.CredentialSummary[providerResourceReauthorizationRequiredOption] == "true" {
					t.Fatal("late refresh changed the current account")
				}
			})
		}
	}
}

func TestPluginCredentialRefreshAllowsRotationAndRetainsCurrentSettings(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	provider := store.AddProvider(Provider{Name: "Rotating subscription", Type: "qoder_subscription", Status: StatusActive})
	const original = `{"type":"api","key":"original"}`
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Rotating account", ResourceType: "qoder_account", Credentials: &ProviderResourceCredentials{SessionJSON: original}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceProviderSession(context.Background(), resource.ID, provider.Type, original, `{"type":"api","key":"intermediate"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{Name: "Renamed account", Status: StatusDisabled, Weight: 25, RateLimitRPM: 19, Options: map[string]string{"custom_option": "preserved"}}); err != nil {
		t.Fatal(err)
	}
	const refreshed = `{"type":"api","key":"refreshed"}`
	updated, err := store.ApplyProviderResourceCredentialRefresh(context.Background(), resource, &ProviderResourceCredentials{SessionJSON: refreshed}, false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed account" || updated.Status != StatusDisabled || updated.Weight != 25 || updated.RateLimitRPM != 19 || updated.Options["custom_option"] != "preserved" {
		t.Fatal("credential refresh restored stale resource settings")
	}
	current, _ := store.GetProviderResource(resource.ID)
	if store.providerResourceCredentialsForRuntime(current).SessionJSON != refreshed || providerCredentialReplacementGeneration(updated.Options) != 0 {
		t.Fatal("same-generation rotation was not persisted")
	}
}
