package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestSubscriptionAuthorizationCancellationInterruptsPendingWorkerCall(t *testing.T) {
	for _, stage := range []string{"auth.status", "auth.callback", "api.models", "oauth.models"} {
		t.Run(stage, func(t *testing.T) {
			operation := stage
			if stage == "api.models" || stage == "oauth.models" {
				operation = "models"
			}
			pool := newSubscriptionWorkerPool(nil)
			defer pool.Close()
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
				return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
					if command["operation"] == operation {
						close(started)
						<-release
					} else if command["operation"] != "auth.status" || (operation != "auth.callback" && stage != "oauth.models") {
						t.Errorf("unexpected worker operation: %v", command["operation"])
					}
					result := `{"state":"pending"}`
					if stage == "oauth.models" {
						result = `{"state":"complete"}`
					}
					if command["operation"] == "models" {
						result = `{"models":[{"id":"model","protocol":"chat"}]}`
					}
					return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(result)}}
				}), nil
			}
			process, releaseProcess, err := pool.acquire(context.Background(), "pending-login", `{"type":"api","key":"synthetic-key"}`, nil)
			if err != nil {
				t.Fatal(err)
			}
			workerSession := "native-session"
			if stage == "api.models" {
				workerSession = ""
			}
			session := &subscriptionWorkerAuthorization{actorID: "admin", providerID: "cursor", state: "synthetic-state", poolKey: "pending-login", workerSession: workerSession,
				process: process, release: releaseProcess, expires: time.Now().Add(time.Minute)}
			pool.mu.Lock()
			pool.sessions[session.poolKey] = session
			pool.mu.Unlock()
			adapter := &SubscriptionWorkerAdapter{Pool: pool, Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}
			input := subscriptionWorkerAuthInput{SessionID: session.poolKey, State: session.state}
			if operation == "auth.callback" {
				input.Code = "synthetic-code"
			}
			exchangeDone := make(chan error, 1)
			go func() {
				_, err := adapter.continueAuthorization(context.Background(), "admin", input, false)
				exchangeDone <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("authorization did not reach the worker")
			}
			if _, err := adapter.continueAuthorization(context.Background(), "other-admin", input, true); err == nil || process.ctx.Err() != nil {
				t.Fatal("another administrator interrupted the authorization")
			}
			wrongState := input
			wrongState.State = "invalid"
			if _, err := adapter.continueAuthorization(context.Background(), "admin", wrongState, true); err == nil || process.ctx.Err() != nil {
				t.Fatal("an invalid state interrupted the authorization")
			}
			other := &SubscriptionWorkerAdapter{Pool: pool, Definition: subscriptionWorkerDefinition{"kiro", "Kiro"}}
			if _, err := other.continueAuthorization(context.Background(), "admin", input, true); err == nil || process.ctx.Err() != nil {
				t.Fatal("another provider interrupted the authorization")
			}
			cancelDone := make(chan error, 1)
			go func() {
				_, err := adapter.continueAuthorization(context.Background(), "admin", input, true)
				cancelDone <- err
			}()
			select {
			case err := <-cancelDone:
				if err != nil {
					t.Fatalf("cancellation failed: %v", err)
				}
			case <-time.After(time.Second):
				t.Error("cancellation waited for the blocked worker operation")
				unblock()
				<-cancelDone
			}
			select {
			case err := <-exchangeDone:
				if err == nil {
					t.Error("cancelled authorization returned a successful worker result")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled authorization did not finish")
			}
			pool.mu.Lock()
			_, pending := pool.sessions[session.poolKey]
			pool.mu.Unlock()
			if pending || process.ctx.Err() == nil {
				t.Fatal("cancellation retained the authorization or worker")
			}
		})
	}
}

func TestSubscriptionAuthorizationCancellationWinsBeforeWorkerStops(t *testing.T) {
	for _, workerSession := range []string{"", "native-session"} {
		t.Run("session="+workerSession, func(t *testing.T) {
			pool := newSubscriptionWorkerPool(nil)
			defer pool.Close()
			modelsStarted, modelsRelease := make(chan struct{}), make(chan struct{})
			var modelsOnce sync.Once
			unblockModels := func() { modelsOnce.Do(func() { close(modelsRelease) }) }
			defer unblockModels()
			pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
				return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
					if command["operation"] == "auth.status" {
						return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"state":"complete"}`)}}
					}
					close(modelsStarted)
					<-modelsRelease
					return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[]}`)}}
				}), nil
			}
			process, release, err := pool.acquire(context.Background(), "completion-race", `{"type":"api","key":"synthetic-key"}`, nil)
			if err != nil {
				t.Fatal(err)
			}
			cancelStarted, cancelRelease := make(chan struct{}), make(chan struct{})
			var cancelOnce, releaseOnce sync.Once
			unblockCancel := func() { releaseOnce.Do(func() { close(cancelRelease) }) }
			defer unblockCancel()
			stop := process.cancel
			process.cancel = func() {
				cancelOnce.Do(func() { close(cancelStarted) })
				<-cancelRelease
				stop()
			}
			session := &subscriptionWorkerAuthorization{actorID: "admin", providerID: "cursor", state: "synthetic-state", poolKey: "completion-race", workerSession: workerSession,
				process: process, release: release, expires: time.Now().Add(time.Minute)}
			pool.mu.Lock()
			pool.sessions[session.poolKey] = session
			pool.mu.Unlock()
			adapter := &SubscriptionWorkerAdapter{Pool: pool, Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}
			input := subscriptionWorkerAuthInput{SessionID: session.poolKey, State: session.state}
			exchangeDone, cancelDone := make(chan error, 1), make(chan error, 1)
			go func() {
				_, err := adapter.continueAuthorization(context.Background(), "admin", input, false)
				exchangeDone <- err
			}()
			select {
			case <-modelsStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("authorization did not reach model discovery")
			}
			go func() {
				_, err := adapter.continueAuthorization(context.Background(), "admin", input, true)
				cancelDone <- err
			}()
			select {
			case <-cancelStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation did not consume the session")
			}
			unblockModels()
			select {
			case err := <-exchangeDone:
				if err == nil {
					t.Error("authorization returned credentials after cancellation consumed the session")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("completed worker operation did not return")
			}
			unblockCancel()
			if err := <-cancelDone; err != nil {
				t.Fatalf("cancellation failed: %v", err)
			}
		})
	}
}

func TestSubscriptionAuthorizationCatalogOutagePreservesCredentials(t *testing.T) {
	const auth = `{"type":"api","key":"synthetic-key"}`
	process := fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
		if command["operation"] != "models" {
			t.Errorf("unexpected worker operation: %v", command["operation"])
		}
		return []subscriptionWorkerEvent{{Event: "error"}}
	})
	defer process.cancel()
	adapter := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}
	result, err := adapter.authorizationCredentials(context.Background(), process, "completed-login")
	if err != nil {
		t.Fatalf("catalog outage discarded completed login: %v", err)
	}
	if result["session_json"] != auth || result["catalog_unavailable"] != true || result["catalog"] != nil {
		t.Fatal("catalog outage lost credentials or reported a successful catalog")
	}
}
