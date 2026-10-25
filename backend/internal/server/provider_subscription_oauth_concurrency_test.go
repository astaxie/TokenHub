package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type subscriptionOAuthOperationStore struct {
	*MemoryStore
	operations atomic.Int32
	queued     chan struct{}
}

func (s *subscriptionOAuthOperationStore) RunClusterOperation(ctx context.Context, name string, operation func(context.Context) error) error {
	if s.operations.Add(1) > 1 {
		s.queued <- struct{}{}
	}
	return s.MemoryStore.RunClusterOperation(ctx, name, operation)
}

func TestCopilotCancellationCannotRestoreAnInFlightDeviceSession(t *testing.T) {
	for _, test := range []struct {
		name, status, capability, actor, errorCode string
		cancelContext, sessionRemains              bool
	}{
		{name: "cancel-pending", status: "authorization_pending", capability: "oauth.cancel", actor: "administrator"},
		{name: "cancel-slow-down", status: "slow_down", capability: "oauth.cancel", actor: "administrator"},
		{name: "concurrent-poll", status: "authorization_pending", capability: "oauth.exchange", actor: "administrator", sessionRemains: true},
		{name: "other-actor", status: "authorization_pending", capability: "oauth.cancel", actor: "other-administrator", errorCode: "invalid_oauth_state", sessionRemains: true},
		{name: "cancelled-context", status: "authorization_pending", capability: "oauth.cancel", actor: "administrator", cancelContext: true, sessionRemains: true},
		{name: "cancel-complete", capability: "oauth.cancel", actor: "administrator", errorCode: "invalid_oauth_state"},
		{name: "concurrent-complete", capability: "oauth.exchange", actor: "administrator", errorCode: "invalid_oauth_state"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &subscriptionOAuthOperationStore{MemoryStore: NewMemoryStore(), queued: make(chan struct{}, 8)}
			server := New(store)
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			var polls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					_, _ = fmt.Fprintf(w, `{"token":"synthetic-copilot","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
					return
				}
				if r.URL.Path == "/models" {
					_, _ = fmt.Fprint(w, `{"data":[{"id":"synthetic-model"}]}`)
					return
				}
				if polls.Add(1) == 1 {
					close(started)
				}
				select {
				case <-release:
					if test.status == "" {
						_, _ = fmt.Fprint(w, `{"access_token":"synthetic-github","scope":"read:user"}`)
					} else {
						_, _ = fmt.Fprintf(w, `{"error":%q}`, test.status)
					}
				case <-r.Context().Done():
				}
			}))
			defer upstream.Close()
			defer unblock()
			adapter, _ := resolveTypedAdapter[*CopilotSubscriptionAdapter](server.adapterRegistry, ProviderGitHubCopilotSubscription)
			adapter.DeviceTokenURL, adapter.CredentialRefreshClient = upstream.URL, upstream.Client()
			adapter.TokenURL, adapter.APIBaseURL, adapter.Client = upstream.URL+"/token", upstream.URL, upstream.Client()
			now := time.Now().UTC()
			secret, _ := json.Marshal(subscriptionOAuthSecret{ActorID: "administrator", DeviceCode: "synthetic-device", Interval: 5, NextPollAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)})
			session := providerAccountOAuthSession{ID: NewID("subscription_oauth"), State: "synthetic-state", ClientID: "tokenhub:subscription:" + ProviderGitHubCopilotSubscription, CodeVerifier: string(secret), CreatedAt: now}
			if err := store.SaveProviderAccountOAuthSession(session); err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"session_id": session.ID, "state": session.State}
			encoded, _ := json.Marshal(payload)
			invoke := func(ctx context.Context, capability, actor string) (pluginmeta.ActionResult, error) {
				return server.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{PluginID: "tokenhub.provider.github-copilot-subscription", ActionID: ProviderGitHubCopilotSubscription + "." + capability, Actor: pluginmeta.ActionActor{ID: actor, Role: "admin"}, Payload: encoded})
			}
			pollDone, cancelDone := make(chan error, 1), make(chan error, 1)
			go func() {
				result, err := invoke(context.Background(), "oauth.exchange", "administrator")
				if err == nil && test.status == "" && result.Data.(subscriptionOAuthExchangeResult).AccessToken != "synthetic-github" {
					t.Error("Completed exchange omitted the authorized credentials")
				}
				pollDone <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("Device poll did not reach the upstream")
			}
			queuedCtx, cancelQueued := context.WithCancel(context.Background())
			defer cancelQueued()
			go func() {
				_, err := invoke(queuedCtx, test.capability, test.actor)
				cancelDone <- err
			}()
			var cancelErr error
			cancelReturned := false
			select {
			case cancelErr = <-cancelDone:
				cancelReturned = true
			case <-store.queued:
			case <-time.After(5 * time.Second):
				t.Fatal("Queued OAuth action did not start")
			}
			if test.cancelContext && !cancelReturned {
				cancelQueued()
				cancelErr = <-cancelDone
				cancelReturned = true
			}
			unblock()
			if err := <-pollDone; err != nil {
				t.Fatalf("Pending device poll failed: %v", err)
			}
			if !cancelReturned {
				cancelErr = <-cancelDone
			}
			if _, exists, err := store.GetProviderAccountOAuthSessionByState(session.State); err != nil || exists != test.sessionRemains {
				t.Fatalf("Incorrect device session lifecycle: exists=%t error=%v action_error=%v", exists, err, cancelErr)
			}
			if test.cancelContext {
				if !errors.Is(cancelErr, context.Canceled) {
					t.Fatalf("Cancelled context returned %v", cancelErr)
				}
			} else if test.errorCode != "" {
				if AsHTTPError(cancelErr).Code != test.errorCode {
					t.Fatalf("Queued action returned %v", cancelErr)
				}
			} else if cancelErr != nil {
				t.Fatalf("Queued action failed: %v", cancelErr)
			}
			if !test.sessionRemains {
				if _, err := invoke(context.Background(), "oauth.exchange", "administrator"); AsHTTPError(err).Code != "invalid_oauth_state" {
					t.Fatalf("Consumed device authorization could be exchanged: %v", err)
				}
			}
			if polls.Load() != 1 {
				t.Fatalf("Cancelled session reached GitHub again: polls=%d", polls.Load())
			}
		})
	}
}
