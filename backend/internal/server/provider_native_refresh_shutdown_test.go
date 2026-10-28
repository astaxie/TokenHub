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

func holdNativeRefreshMutation(t *testing.T, store *GormStore, resourceID string) func() {
	t.Helper()
	entered, unblock, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	go func() {
		defer close(done)
		if err := store.withClusterLease(context.Background(), providerResourceMutationLeaseName(resourceID), func(context.Context) error {
			close(entered)
			<-unblock
			return nil
		}); err != nil {
			t.Errorf("Persistence blocker failed: %v", err)
		}
	}()
	t.Cleanup(func() { release(); <-done })
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Persistence blocker did not acquire its lease")
	}
	return release
}

func TestNativeRefreshShutdownDrainsReceivedTokens(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		for _, expired := range []bool{false, true} {
			name := map[bool]string{false: "request", true: "scheduler"}[scheduled] + "/" + map[bool]string{false: "deadline", true: "expired"}[expired]
			t.Run(name, func(t *testing.T) {
				store := NewMemoryStore()
				t.Cleanup(func() { _ = store.Close() })
				received := make(chan struct{})
				var receivedOnce sync.Once
				_, resource := nativeRefreshCancellationFixture(t, store, ProviderChatGPTSubscription, func() { receivedOnce.Do(func() { close(received) }) })
				_, err := store.UpdateProviderResource(resource.ID, ProviderResource{Status: StatusActive, Healthy: true, Credentials: &ProviderResourceCredentials{ExpiresAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}})
				if err != nil {
					t.Fatal(err)
				}
				release := holdNativeRefreshMutation(t, store, resource.ID)
				server := &Server{store: store, stopHeartbeat: store.StartInstanceHeartbeat("native-refresh-shutdown"), responseCancel: func() {}, imageCancel: func() {}}
				t.Cleanup(server.stopHeartbeat)
				if scheduled {
					server.credentialRefresh = newProviderCredentialRefreshService(store)
					server.credentialRefresh.StartScheduler(time.Hour)
					t.Cleanup(func() { release(); _ = server.credentialRefresh.Shutdown(context.Background()) })
				} else {
					done := make(chan struct{})
					go func() {
						defer close(done)
						// Contextual views must share their parent's lifecycle.
						_, err := store.WithContext(context.Background()).RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
						if !errors.Is(err, context.Canceled) {
							t.Errorf("Shutdown did not cancel the original request: %v", err)
						}
					}()
					t.Cleanup(func() { release(); <-done })
				}
				select {
				case <-received:
				case <-time.After(3 * time.Second):
					t.Fatal("Native token rotation was not received")
				}
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
				defer cancel()
				if expired || !scheduled {
					// Force the early return before the scheduler shutdown stage.
					server.responseWorkerGroup.Add(1)
					defer server.responseWorkerGroup.Done()
				}
				if expired {
					cancel()
				}
				finished := make(chan error, 2)
				for range 2 {
					go func() { finished <- server.Shutdown(shutdownCtx) }()
				}
				select {
				case err := <-finished:
					t.Fatalf("Shutdown returned before saving received credentials: %v", err)
				case <-time.After(100 * time.Millisecond):
				}
				probeCtx, probeCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
				defer probeCancel()
				if _, err := store.RefreshProviderResourceCredentials(probeCtx, resource.ID, true); AsHTTPError(err).Code != "provider_credential_refresh_stopped" {
					t.Fatalf("Shutdown admitted another refresh: %v", err)
				}
				heartbeats, err := store.ListInstanceHeartbeats(context.Background())
				if err != nil || len(heartbeats) != 1 {
					t.Fatalf("Instance disappeared while its native save was pending: rows=%d error=%v", len(heartbeats), err)
				}
				release()
				for range 2 {
					select {
					case err := <-finished:
						if !errors.Is(err, shutdownCtx.Err()) {
							t.Fatalf("Shutdown changed its deadline result: %v", err)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("Shutdown did not finish after native persistence")
					}
				}
				current, _ := store.GetProviderResource(resource.ID)
				if store.providerResourceCredentialsForRuntime(current).RefreshToken != "synthetic-new-refresh" {
					t.Fatal("Shutdown lost the received refresh token")
				}
				heartbeats, err = store.ListInstanceHeartbeats(context.Background())
				if err != nil || len(heartbeats) != 0 {
					t.Fatalf("Shutdown retained its heartbeat: rows=%d error=%v", len(heartbeats), err)
				}
				if scheduled {
					select {
					case <-server.credentialRefresh.schedulerDone:
					default:
						t.Fatal("Shutdown returned before its credential scheduler stopped")
					}
				}
			})
		}
	}
}

func TestNativeRefreshStoreCloseDrainsContextualOperations(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "native-close.db")
	store, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	observer, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close() })
	received := make(chan struct{})
	_, resource := nativeRefreshCancellationFixture(t, store, ProviderChatGPTSubscription, func() { close(received) })
	release := holdNativeRefreshMutation(t, observer, resource.ID)
	stopHeartbeat := store.StartInstanceHeartbeat("native-close")
	t.Cleanup(stopHeartbeat)
	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		_, err := store.WithContext(context.Background()).RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Store close did not cancel the original request: %v", err)
		}
	}()
	t.Cleanup(func() { release(); <-refreshDone })
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("Native token rotation was not received")
	}
	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Store closed before its received token was saved: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	heartbeats, err := observer.ListInstanceHeartbeats(context.Background())
	if err != nil || len(heartbeats) != 2 {
		t.Fatalf("Store retired its heartbeat before native persistence: rows=%d error=%v", len(heartbeats), err)
	}
	release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Store close did not finish after native persistence")
	}
	current, _ := observer.GetProviderResource(resource.ID)
	if observer.providerResourceCredentialsForRuntime(current).RefreshToken != "synthetic-new-refresh" {
		t.Fatal("Database closed before the native refresh token was persisted")
	}
	store.nativeRefreshes.resume()
	if _, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true); AsHTTPError(err).Code != "provider_credential_refresh_stopped" {
		t.Fatalf("Closed store admitted another refresh: %v", err)
	}
}

func TestNativeRefreshShutdownCancelsUpstream(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	_, resource := nativeRefreshCancellationFixture(t, store, ProviderChatGPTSubscription, func() {})
	entered := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(store.drainNativeCredentialRefreshes)
	adapter := &ChatGPTSubscriptionAdapter{CredentialRefreshClient: upstream.Client(), TokenURL: upstream.URL}
	store.ConfigureProviderCredentialRefreshHandlers(adapter.ProviderResourceCredentialRefreshHandlers())
	finished := make(chan error, 1)
	go func() {
		_, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Native refresh did not reach upstream")
	}
	server := &Server{store: store}
	closed := make(chan struct{})
	go func() { server.shutdownProviderRuntimes(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown did not cancel the pending token exchange")
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("Pending upstream operation was not canceled: %v", err)
	}
}

func TestNativeRefreshRestartsWithOpenStore(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	server := NewWithConfig(store, Config{AdminToken: "synthetic-admin"})
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshProviderResourceCredentials(context.Background(), "missing", false); AsHTTPError(err).Code != "provider_credential_refresh_stopped" {
		t.Fatalf("Stopped server admitted another refresh: %v", err)
	}
	restarted := NewWithConfig(store, Config{AdminToken: "synthetic-admin"})
	t.Cleanup(func() { _ = restarted.Shutdown(context.Background()) })
	if _, err := store.RefreshProviderResourceCredentials(context.Background(), "missing", false); AsHTTPError(err).Code != "provider_resource_not_found" {
		t.Fatalf("New server did not reopen its store's credential lifecycle: %v", err)
	}
}
