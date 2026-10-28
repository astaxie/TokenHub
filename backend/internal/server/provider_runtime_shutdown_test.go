package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestShutdownKeepsHeartbeatDuringSubscriptionCredentialCleanup(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "graceful", true: "canceled-drain"}[canceled], func(t *testing.T) {
			store := NewMemoryStore()
			t.Cleanup(func() { _ = store.Close() })
			provider := store.AddProvider(Provider{Name: "Shutdown account", Type: providerClaudeSubscription, Status: StatusActive})
			const original = `{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"original-refresh"}}`
			const rotated = `{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"rotated-refresh"}}`
			resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Shutdown resource", ResourceType: providerResourceClaudeSubscription, Credentials: &ProviderResourceCredentials{SessionJSON: original}})
			if err != nil {
				t.Fatal(err)
			}
			provider.Options = map[string]string{"resource_id": resource.ID}
			adapter, _, requestCtx := claudeSubscriptionFixture(t)
			adapter.ParkTimeout = time.Minute
			adapter.RefreshCredentials = providerCredentialLoaderForType(store, provider.Type)
			adapter.RunClusterOperation = store.RunClusterOperation
			entered, unblock := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(unblock) }) }
			defer release()
			saved := make(chan error, 1)
			adapter.SaveSession = func(ctx context.Context, id, providerType, previous, next string) error {
				close(entered)
				select {
				case <-unblock:
				case <-ctx.Done():
				}
				err := store.ReplaceProviderSession(ctx, id, providerType, previous, next)
				saved <- err
				return err
			}
			if _, _, err := adapter.Chat(requestCtx, provider, "tools", claudeSubscriptionToolRequest()); err != nil {
				t.Fatal(err)
			}
			adapter.mu.Lock()
			run := adapter.runs[resource.ID]
			adapter.mu.Unlock()
			if run == nil {
				t.Fatal("tool request did not retain its account process")
			}
			// Simulate a CLI rotation while its tool continuation is parked.
			if err := os.WriteFile(filepath.Join(run.configDir, ".credentials.json"), []byte(rotated), 0o600); err != nil {
				t.Fatal(err)
			}
			server := &Server{store: store, adapterRegistry: &AdapterRegistry{adapters: map[string]any{provider.Type: adapter}},
				stopHeartbeat: store.StartInstanceHeartbeat("subscription-shutdown-test"), responseCancel: func() {}, imageCancel: func() {}}
			t.Cleanup(server.stopHeartbeat)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if canceled {
				// Keep the first drain pending so cancellation takes its early return.
				server.responseWorkerGroup.Add(1)
				defer server.responseWorkerGroup.Done()
				cancel()
			}
			finished := make(chan error, 1)
			go func() { finished <- server.Shutdown(ctx) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown did not reach credential cleanup")
			}
			heartbeats, err := store.ListInstanceHeartbeats(context.Background())
			if err != nil || len(heartbeats) != 1 {
				t.Errorf("instance disappeared before account credentials were saved: rows=%d error=%v", len(heartbeats), err)
			}
			select {
			case <-finished:
				t.Fatal("shutdown returned before credential cleanup finished")
			default:
			}
			release()
			select {
			case err := <-finished:
				if (!canceled && err != nil) || (canceled && !errors.Is(err, context.Canceled)) {
					t.Fatalf("unexpected shutdown result: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown did not finish after credential persistence")
			}
			if err := <-saved; err != nil {
				t.Fatalf("shutdown lost rotated account credentials: %v", err)
			}
			current, _ := store.GetProviderResource(resource.ID)
			if !strings.Contains(store.providerResourceCredentialsForRuntime(current).SessionJSON, "rotated-refresh") {
				t.Fatal("rotated account credentials were not retained")
			}
			heartbeats, err = store.ListInstanceHeartbeats(context.Background())
			if err != nil || len(heartbeats) != 0 {
				t.Fatalf("shutdown retained its heartbeat: rows=%d error=%v", len(heartbeats), err)
			}
		})
	}
}
