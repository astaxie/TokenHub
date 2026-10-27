package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubscriptionWorkerReceivedRotationIgnoresCallerCancellation(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "save", true: "administrator-replacement"}[replaced], func(t *testing.T) {
			store := NewMemoryStore()
			t.Cleanup(func() { _ = store.Close() })
			provider := store.AddProvider(Provider{Name: "Rotation fixture", Type: "grok_subscription", Status: StatusActive})
			const previous = `{"type":"oauth","access":"synthetic-old","refresh":"synthetic-old-refresh"}`
			const next = `{"type":"oauth","access":"synthetic-new","refresh":"synthetic-new-refresh"}`
			const replacement = `{"type":"oauth","access":"replacement","refresh":"replacement-refresh"}`
			resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Rotation account", ResourceType: "grok_account", Credentials: &ProviderResourceCredentials{SessionJSON: previous}})
			if err != nil {
				t.Fatal(err)
			}
			requestCtx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			process := fakeSubscriptionProcess(previous, func(map[string]any) []subscriptionWorkerEvent { return nil })
			defer process.cancel()
			process.operationCtx = requestCtx
			var ack map[string]any
			process.input = subscriptionTestInput{write: func(data []byte) (int, error) {
				return len(data), json.Unmarshal(data, &ack)
			}}
			process.saveAuth = func(ctx context.Context, before, after string) error {
				cancelRequest()
				deadline, bounded := ctx.Deadline()
				if ctx.Err() != nil || !bounded || time.Until(deadline) > 10*time.Second {
					t.Error("received rotation did not get a bounded cancellation-independent save")
				}
				if replaced {
					if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &ProviderResourceCredentials{Replace: true, SessionJSON: replacement}}); err != nil {
						t.Fatal(err)
					}
				}
				return store.ReplaceProviderSession(ctx, resource.ID, provider.Type, before, after)
			}
			process.updateAuth(subscriptionWorkerEvent{UpdateID: "rotation", Auth: json.RawMessage(next)})
			current, _ := store.GetProviderResource(resource.ID)
			want := next
			if replaced {
				want = replacement
				if ack["error"] != "credential_persistence_failed" || process.authSnapshot() != previous {
					t.Error("rejected rotation was acknowledged or installed in the worker")
				}
			} else if ack["error"] != nil || process.authSnapshot() != next {
				t.Error("saved rotation was not acknowledged or installed in the worker")
			}
			if store.providerResourceCredentialsForRuntime(current).SessionJSON != want {
				t.Fatal("cancellation discarded the rotation or overwrote administrator credentials")
			}
		})
	}
}

func TestSubscriptionWorkerCancellationRetainsLeaseUntilRotationIsSaved(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "rotation-lease.db")
	store, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = other.Close() })
	store.clusterLockTTL, other.clusterLockTTL = 500*time.Millisecond, 500*time.Millisecond
	provider := store.AddProvider(Provider{Name: "Lease fixture", Type: "grok_subscription", Status: StatusActive})
	const original = `{"type":"oauth","access":"synthetic-old","refresh":"synthetic-old-refresh","expires":1}`
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Lease account", ResourceType: "grok_account", Credentials: &ProviderResourceCredentials{SessionJSON: original}})
	if err != nil {
		t.Fatal(err)
	}
	provider.Options = map[string]string{"resource_id": resource.ID}
	client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "auth.x.ai" || request.URL.Path != "/oauth2/token" {
			t.Errorf("unexpected upstream request: %s", request.URL)
		}
		body := `{"access_token":"synthetic-new","refresh_token":"synthetic-new-refresh","expires_in":3600}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	pool := newSubscriptionWorkerPool(client)
	t.Cleanup(pool.Close)
	entered, unblock := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseSave := func() { releaseOnce.Do(func() { close(unblock) }) }
	t.Cleanup(releaseSave)
	saveResult := make(chan error, 1)
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"grok", "Grok"}, Pool: pool,
		RefreshCredentials: providerCredentialLoaderForType(store, provider.Type), RunClusterOperation: store.RunClusterOperation,
		SaveSession: func(ctx context.Context, id, providerType, previous, next string) error {
			close(entered)
			select {
			case <-unblock:
			case <-ctx.Done():
			}
			err := store.ReplaceProviderSession(ctx, id, providerType, previous, next)
			saveResult <- err
			return err
		}}
	ctx, cancel := context.WithCancel(withSubscriptionCaller(context.Background(), "fixture-project", "fixture-key"))
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- adapter.withAccountLease(ctx, provider, nil, func(leaseCtx context.Context) error {
			process, release, id, err := adapter.account(leaseCtx, provider, nil)
			if err != nil {
				return err
			}
			defer release()
			_, err = process.invoke(leaseCtx, adapter.command(id, "refresh"), nil)
			return err
		})
	}()
	select {
	case <-entered:
	case err := <-finished:
		t.Fatalf("rotation ended before persistence: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("rotation did not reach persistence")
	}
	cancel()
	// Wait longer than the lease TTL: cancellation must not stop its heartbeat
	// while the received refresh token is waiting for durable storage.
	contender, stopContender := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer stopContender()
	err = other.RunClusterOperation(contender, "subscription-account:"+resource.ID, func(context.Context) error {
		t.Error("another replica acquired the account before rotation persistence finished")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("competing replica did not remain blocked: %v", err)
	}
	select {
	case err := <-finished:
		t.Errorf("account operation ended before its received rotation was saved: %v", err)
	default:
	}
	releaseSave()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled operation returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled operation did not finish after persistence")
	}
	if err := <-saveResult; err != nil {
		t.Fatalf("received rotation was not saved: %v", err)
	}
	err = other.RunClusterOperation(context.Background(), "subscription-account:"+resource.ID, func(context.Context) error {
		current, _ := other.GetProviderResource(resource.ID)
		if !strings.Contains(other.providerResourceCredentialsForRuntime(current).SessionJSON, "synthetic-new-refresh") {
			t.Error("next replica acquired an account containing the spent refresh token")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionWorkerCancellationStopsLeaseAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"grok", "Grok"}, RunClusterOperation: func(ctx context.Context, _ string, _ func(context.Context) error) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}}
	err := adapter.withAccountLease(ctx, Provider{Options: map[string]string{"resource_id": "pending-account"}}, nil, func(context.Context) error {
		t.Error("canceled acquisition started an account operation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lease acquisition did not preserve cancellation: %v", err)
	}
}

func TestSubscriptionWorkerOperationPreservesCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"grok", "Grok"}, RunClusterOperation: func(heldCtx context.Context, _ string, operation func(context.Context) error) error {
		err := operation(heldCtx)
		if heldCtx.Err() != nil {
			t.Error("caller deadline canceled the lease before operation cleanup finished")
		}
		return err
	}}
	err := adapter.withAccountLease(ctx, Provider{Options: map[string]string{"resource_id": "deadline-account"}}, nil, func(operationCtx context.Context) error {
		if got, ok := operationCtx.Deadline(); !ok || !got.Equal(deadline) {
			t.Error("account operation lost its caller deadline")
		}
		<-operationCtx.Done()
		return operationCtx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller timeout became cancellation: %v", err)
	}
}

func TestSubscriptionWorkerLeaseAcquisitionPreservesCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"grok", "Grok"}, RunClusterOperation: func(heldCtx context.Context, _ string, _ func(context.Context) error) error {
		<-heldCtx.Done()
		return heldCtx.Err()
	}}
	err := adapter.withAccountLease(ctx, Provider{Options: map[string]string{"resource_id": "busy-account"}}, nil, func(context.Context) error {
		t.Error("timed-out acquisition started an account operation")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lease acquisition timeout became cancellation: %v", err)
	}
}
