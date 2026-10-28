package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSubscriptionWorkerShutdownWaitsForReceivedRotation(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	provider := store.AddProvider(Provider{Name: "Shutdown rotation fixture", Type: "grok_subscription", Status: StatusActive})
	const original = `{"type":"oauth","access":"synthetic-old","refresh":"synthetic-old-refresh","expires":1}`
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Shutdown account", ResourceType: "grok_account", Credentials: &ProviderResourceCredentials{SessionJSON: original}})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "auth.x.ai" || request.URL.Path != "/oauth2/token" {
			t.Errorf("Unexpected upstream request: %s", request.URL)
		}
		body := `{"access_token":"synthetic-new","refresh_token":"synthetic-new-refresh","expires_in":3600}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	pool := newSubscriptionWorkerPool(client)
	t.Cleanup(pool.Close)
	entered, unblock, saved := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	releaseSave := func() { once.Do(func() { close(unblock) }) }
	t.Cleanup(releaseSave)
	process, release, err := pool.acquire(context.Background(), "shutdown-fixture", original, func(ctx context.Context, previous, next string) error {
		close(entered)
		select {
		case <-unblock:
		case <-ctx.Done():
		}
		if !strings.Contains(next, "synthetic-new-refresh") {
			t.Error("The received rotation was lost")
		}
		err := store.ReplaceProviderSession(ctx, resource.ID, provider.Type, previous, next)
		saved <- err
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invoked := make(chan error, 1)
	go func() {
		_, err := process.invoke(ctx, map[string]any{"operation": "refresh", "provider": "grok", "resource_id": "shutdown-account"}, nil)
		invoked <- err
	}()
	select {
	case <-entered:
	case err := <-invoked:
		t.Fatalf("Rotation ended before persistence: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Rotation did not reach persistence")
	}
	// An exhausted HTTP drain deadline closes request contexts before the
	// application closes its runtimes. The received token must still be saved.
	cancel()
	closed := make(chan struct{}, 2)
	for range 2 {
		go func() { pool.Close(); closed <- struct{}{} }()
	}
	select {
	case <-closed:
		t.Fatal("Runtime shutdown returned before received credentials were persisted")
	case <-time.After(3200 * time.Millisecond):
	}
	releaseSave()
	for range 2 {
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Fatal("Runtime shutdown did not finish after credential persistence")
		}
	}
	if err := <-saved; err != nil {
		t.Fatalf("Shutdown canceled the durable credential save: %v", err)
	}
	current, _ := store.GetProviderResource(resource.ID)
	if !strings.Contains(store.providerResourceCredentialsForRuntime(current).SessionJSON, "synthetic-new-refresh") {
		t.Fatal("Shutdown lost the rotated refresh token")
	}
	select {
	case <-process.done:
	default:
		t.Fatal("Runtime shutdown returned before worker cleanup")
	}
	pool.Close()
}
