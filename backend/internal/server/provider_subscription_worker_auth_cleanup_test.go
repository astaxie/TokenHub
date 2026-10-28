package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestSubscriptionWorkerCanceledExchangeReleasesAuthorization(t *testing.T) {
	for _, stage := range []string{"callback", "completed-models", "api-key-models"} {
		t.Run(stage, func(t *testing.T) {
			pool := newSubscriptionWorkerPool(nil)
			defer pool.Close()
			entered := make(chan struct{})
			pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
				return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
					if command["operation"] == "auth.status" {
						return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"state":"complete"}`)}}
					}
					close(entered)
					return nil
				}), nil
			}
			process, release, err := pool.acquire(context.Background(), "cancelled-exchange", `{"type":"api","key":"synthetic-key"}`, nil)
			if err != nil {
				t.Fatal(err)
			}
			session := &subscriptionWorkerAuthorization{actorID: "admin", providerID: "cursor", state: "synthetic-state", poolKey: "cancelled-exchange",
				workerSession: "worker-session", expires: time.Now().Add(time.Minute), process: process, release: release}
			if stage == "api-key-models" {
				session.workerSession = ""
			}
			pool.sessions[session.poolKey] = session
			adapter := &SubscriptionWorkerAdapter{Pool: pool, Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}
			input := subscriptionWorkerAuthInput{SessionID: session.poolKey, State: session.state}
			if stage == "callback" {
				input.Code = "synthetic-code"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, exchangeErr := adapter.continueAuthorization(ctx, "admin", input, false)
				finished <- exchangeErr
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("Authorization did not reach the blocked exchange stage")
			}
			cancel()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Expected canceled exchange, got %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Canceled authorization did not finish")
			}
			pool.mu.Lock()
			pending, references := len(pool.sessions), pool.entries[session.poolKey].references
			pool.mu.Unlock()
			if pending != 0 || references != 0 {
				t.Fatalf("Stopped authorization retained its pool slot: sessions=%d references=%d", pending, references)
			}
		})
	}
}
