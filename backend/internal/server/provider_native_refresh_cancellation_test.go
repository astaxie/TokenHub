package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type nativeRefreshReceivedBody struct {
	io.ReadCloser
	once     sync.Once
	received func()
}

func (b *nativeRefreshReceivedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.once.Do(b.received)
	}
	return n, err
}

func nativeRefreshCancellationFixture(t *testing.T, store *GormStore, providerType string, received func()) (Provider, ProviderResource) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "synthetic-old-refresh" {
			t.Error("Unexpected token exchange request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600,"scope":"chatgpt.tokens.use.direct"}`)
	}))
	t.Cleanup(upstream.Close)
	client := upstream.Client()
	transport := client.Transport
	client.Transport = subscriptionWorkerRoundTripper(func(req *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(req)
		if err == nil {
			response.Body = &nativeRefreshReceivedBody{ReadCloser: response.Body, received: received}
		}
		return response, err
	})
	var registrations []providerResourceCredentialRefreshRegistration
	resourceType := ProviderResourceChatGPTSubscription
	if providerType == ProviderChatGPTSubscription {
		adapter := &ChatGPTSubscriptionAdapter{CredentialRefreshClient: client, TokenURL: upstream.URL}
		registrations = adapter.ProviderResourceCredentialRefreshHandlers()
	} else {
		adapter := &GoogleSubscriptionAdapter{ProviderType: providerType, Client: client, TokenURL: upstream.URL}
		registrations = adapter.ProviderResourceCredentialRefreshHandlers()
		resourceType = "gemini_cli_account"
		if providerType == providerAntigravitySubscription {
			resourceType = "antigravity_account"
		}
	}
	store.ConfigureProviderResourceTypePolicy(map[string][]string{providerType: {resourceType}})
	store.ConfigureProviderCredentialRefreshHandlers(registrations)
	provider := store.AddProvider(Provider{Name: "Native refresh fixture", Type: providerType, Options: map[string]string{providerCredentialRefreshProfileOption: registrations[0].Profile}})
	credentials := ProviderResourceCredentials{AccessToken: "synthetic-old-access", RefreshToken: "synthetic-old-refresh", ClientID: "synthetic-client", ClientSecret: "synthetic-secret", Scopes: chatGPTSubscriptionDirectScope}
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Native refresh fixture account", ResourceType: resourceType, Credentials: &credentials})
	if err != nil {
		t.Fatal(err)
	}
	return provider, resource
}

func TestNativeCredentialRefreshSavesReceivedRotationAfterCancellation(t *testing.T) {
	for _, providerType := range []string{ProviderChatGPTSubscription, providerGeminiCLISubscription, providerAntigravitySubscription} {
		for _, deadline := range []bool{false, true} {
			name := providerType + "/canceled"
			if deadline {
				name = providerType + "/deadline"
			}
			t.Run(name, func(t *testing.T) {
				store := NewMemoryStore()
				t.Cleanup(func() { _ = store.Close() })
				ctx, cancel := context.WithCancel(context.Background())
				if deadline {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				}
				defer cancel()
				provider, resource := nativeRefreshCancellationFixture(t, store, providerType, func() {
					if deadline {
						<-ctx.Done()
					} else {
						cancel()
					}
				})
				_, err := providerCredentialLoaderForType(store, provider.Type)(ctx, resource.ID, true)
				want := context.Canceled
				if deadline {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("Caller cancellation changed: %v", err)
				}
				current, _ := store.GetProviderResource(resource.ID)
				if saved := store.providerResourceCredentialsForRuntime(current); saved.RefreshToken != "synthetic-new-refresh" || saved.AccessToken != "synthetic-new-access" {
					t.Fatal("Received native token rotation was discarded after cancellation")
				}
			})
		}
	}
}

func TestNativeCredentialRefreshCancellationRetainsLeaseUntilSaved(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "native-refresh-lease.db")
	store, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = other.Close() })
	store.clusterLockTTL, other.clusterLockTTL = time.Second, time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan struct{})
	_, resource := nativeRefreshCancellationFixture(t, store, ProviderChatGPTSubscription, func() { cancel(); close(received) })
	blocked, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	defer release()
	blockDone := make(chan error, 1)
	go func() {
		blockDone <- other.withClusterLease(context.Background(), providerResourceMutationLeaseName(resource.ID), func(context.Context) error {
			close(blocked)
			<-unblock
			return nil
		})
	}()
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("Persistence blocker did not acquire its lease")
	}
	finished := make(chan error, 1)
	go func() {
		_, err := store.RefreshProviderResourceCredentials(ctx, resource.ID, true)
		finished <- err
	}()
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("Token exchange did not complete")
	}
	contender, stop := context.WithTimeout(context.Background(), 1300*time.Millisecond)
	defer stop()
	err = other.withClusterLease(contender, "credential-refresh:"+resource.ID, func(context.Context) error {
		t.Error("Another replica acquired the refresh lease before persistence finished")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Competing refresh did not remain blocked: %v", err)
	}
	release()
	if err := <-blockDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Caller cancellation changed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Credential persistence did not finish")
	}
	err = other.withClusterLease(context.Background(), "credential-refresh:"+resource.ID, func(context.Context) error {
		current, _ := other.GetProviderResource(resource.ID)
		if other.providerResourceCredentialsForRuntime(current).RefreshToken != "synthetic-new-refresh" {
			t.Error("Next replica loaded the spent refresh token")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeCredentialRefreshCancellationPreservesReplacement(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var resourceID string
	_, resource := nativeRefreshCancellationFixture(t, store, ProviderChatGPTSubscription, func() {
		_, err := store.UpdateProviderResource(resourceID, ProviderResource{Credentials: &ProviderResourceCredentials{Replace: true, AccessToken: "synthetic-replacement-access", RefreshToken: "synthetic-replacement-refresh"}})
		if err != nil {
			t.Errorf("Account replacement failed: %v", err)
		}
		cancel()
	})
	resourceID = resource.ID
	_, err := store.RefreshProviderResourceCredentials(ctx, resource.ID, true)
	if err == nil || AsHTTPError(err).Code != "provider_resource_changed" {
		t.Fatalf("Changed account was not rejected: %v", err)
	}
	current, _ := store.GetProviderResource(resource.ID)
	if store.providerResourceCredentialsForRuntime(current).RefreshToken != "synthetic-replacement-refresh" {
		t.Fatal("Canceled refresh overwrote the administrator's replacement account")
	}
}

func TestNativeCredentialRefreshLeaseAcquisitionRemainsCancellable(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[deadline], func(t *testing.T) {
			store := NewMemoryStore()
			t.Cleanup(func() { _ = store.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
			} else {
				timer := time.AfterFunc(30*time.Millisecond, cancel)
				defer timer.Stop()
			}
			defer cancel()
			err := store.withClusterLease(context.Background(), "credential-refresh:blocked-account", func(context.Context) error {
				return store.withNativeCredentialRefreshLease(ctx, "blocked-account", func(context.Context, context.Context) error {
					t.Error("Canceled acquisition reached the credential operation")
					return nil
				})
			})
			want := context.Canceled
			if deadline {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) {
				t.Fatalf("Canceled lease acquisition changed its cause: %v", err)
			}
		})
	}
}
