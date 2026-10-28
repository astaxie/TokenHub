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

func TestClaudeSubscriptionCancellationRetainsLeaseUntilRotationIsSaved(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "claude-rotation-lease.db")
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
	provider := store.AddProvider(Provider{Name: "Claude lease fixture", Type: providerClaudeSubscription, Status: StatusActive})
	const original = `{"claudeAiOauth":{"accessToken":"fixture-token","refreshToken":"refresh-original"}}`
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Claude account", ResourceType: providerResourceClaudeSubscription, Credentials: &ProviderResourceCredentials{SessionJSON: original}})
	if err != nil {
		t.Fatal(err)
	}
	provider.Options = map[string]string{"resource_id": resource.ID}
	adapter, _, base := claudeSubscriptionFixture(t)
	adapter.RefreshCredentials = providerCredentialLoaderForType(store, provider.Type)
	adapter.RunClusterOperation = store.RunClusterOperation
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseSave := func() { once.Do(func() { close(unblock) }) }
	defer releaseSave()
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
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, _, err := adapter.Chat(ctx, provider, "refresh-idle", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Wait"}}})
		finished <- err
	}()
	rotated := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		adapter.mu.Lock()
		run := adapter.runs[resource.ID]
		adapter.mu.Unlock()
		if run != nil {
			run.mu.Lock()
			config := run.configDir
			run.mu.Unlock()
			if config != "" {
				raw, _ := os.ReadFile(filepath.Join(config, ".credentials.json"))
				if strings.Contains(string(raw), "rotated-refresh") {
					rotated = true
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !rotated {
		t.Fatal("CLI did not rotate its account credentials")
	}
	cancel()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not reach credential persistence")
	}
	// Exceed the configured lease TTL while cleanup still holds the new token.
	contender, stop := context.WithTimeout(context.Background(), 1300*time.Millisecond)
	defer stop()
	err = other.RunClusterOperation(contender, "subscription-account:"+resource.ID, func(context.Context) error {
		t.Error("another replica acquired the account before rotation was saved")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("competing lease did not remain blocked: %v", err)
	}
	releaseSave()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatalf("received rotation was not saved: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("credential persistence did not finish")
	}
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("request cancellation changed: %v", err)
	}
	next, stopNext := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopNext()
	err = other.RunClusterOperation(next, "subscription-account:"+resource.ID, func(context.Context) error {
		current, _ := other.GetProviderResource(resource.ID)
		if !strings.Contains(other.providerResourceCredentialsForRuntime(current).SessionJSON, "rotated-refresh") {
			t.Error("next replica loaded the spent refresh token")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSubscriptionLeaseAcquisitionPreservesCancellation(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "deadline"}[timeout], func(t *testing.T) {
			adapter, provider, base := claudeSubscriptionFixture(t)
			ctx, cancel := context.WithTimeout(base, 30*time.Millisecond)
			defer cancel()
			adapter.RunClusterOperation = func(leaseCtx context.Context, _ string, _ func(context.Context) error) error {
				if !timeout {
					cancel()
				}
				<-leaseCtx.Done()
				return leaseCtx.Err()
			}
			_, _, err := adapter.Chat(ctx, provider, "stream", ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "Hello"}}})
			want := context.Canceled
			if timeout {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) {
				t.Fatalf("lease acquisition returned %v, want %v", err, want)
			}
		})
	}
}
