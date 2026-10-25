package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type subscriptionOAuthCancelledLeaseStore struct {
	*MemoryStore
	cancel      context.CancelFunc
	cancelLease bool
	err         error
	reads       int
	consumes    int
}

func (s *subscriptionOAuthCancelledLeaseStore) RunClusterOperation(ctx context.Context, _ string, operation func(context.Context) error) error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.err != nil {
		return s.err
	}
	if s.cancelLease {
		leaseCtx, cancel := context.WithCancel(ctx)
		cancel()
		return operation(leaseCtx)
	}
	return operation(ctx)
}

func (s *subscriptionOAuthCancelledLeaseStore) GetProviderAccountOAuthSessionByState(state string) (providerAccountOAuthSession, bool, error) {
	s.reads++
	return s.MemoryStore.GetProviderAccountOAuthSessionByState(state)
}

func (s *subscriptionOAuthCancelledLeaseStore) ConsumeProviderAccountOAuthSession(id, state string) (providerAccountOAuthSession, bool, error) {
	s.consumes++
	return s.MemoryStore.ConsumeProviderAccountOAuthSession(id, state)
}

func TestSubscriptionOAuthPreservesCancellationAtLeaseBoundary(t *testing.T) {
	storageError := errors.New("synthetic storage failure")
	for _, test := range []struct {
		name        string
		cancelled   bool
		cancelLease bool
		leaseError  error
		wantError   error
	}{
		{name: "rollback wins transaction completion", cancelled: true, leaseError: sql.ErrTxDone, wantError: context.Canceled},
		{name: "cancelled acquired lease does not consume session", cancelled: true, wantError: context.Canceled},
		{name: "cancelled lease does not consume session with live caller", cancelLease: true, wantError: context.Canceled},
		{name: "live database error remains visible", leaseError: sql.ErrTxDone, wantError: sql.ErrTxDone},
		{name: "cancellation does not replace unrelated storage failure", cancelled: true, leaseError: storageError, wantError: storageError},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := &subscriptionOAuthCancelledLeaseStore{MemoryStore: NewMemoryStore(), err: test.leaseError, cancelLease: test.cancelLease}
			t.Cleanup(func() { _ = store.Close() })
			if test.cancelled {
				store.cancel = cancel
			}
			now := time.Now().UTC()
			secret, _ := json.Marshal(subscriptionOAuthSecret{ActorID: "administrator", ExpiresAt: now.Add(time.Minute)})
			session := providerAccountOAuthSession{ID: NewID("subscription_oauth"), State: "synthetic-state", ClientID: "tokenhub:subscription:" + ProviderGitHubCopilotSubscription, CodeVerifier: string(secret), CreatedAt: now}
			if err := store.SaveProviderAccountOAuthSession(session); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]string{"session_id": session.ID, "state": session.State})
			server := &Server{store: store}
			_, err := server.runSubscriptionOAuthAction(ctx, builtinSubscriptionDefinition{providerType: ProviderGitHubCopilotSubscription}, "oauth.cancel", pluginmeta.ActionInvocation{Actor: pluginmeta.ActionActor{ID: "administrator", Role: "admin"}, Payload: payload})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("OAuth operation returned %v, want %v", err, test.wantError)
			}
			if store.reads != 0 || store.consumes != 0 {
				t.Fatalf("OAuth action accessed the session after lease failure: reads=%d consumes=%d", store.reads, store.consumes)
			}
			if _, exists, err := store.MemoryStore.GetProviderAccountOAuthSessionByState(session.State); err != nil || !exists {
				t.Fatalf("cancelled operation consumed the pending session: exists=%t error=%v", exists, err)
			}
		})
	}
}
