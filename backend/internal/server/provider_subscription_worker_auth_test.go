package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionWorkerAuthorizationBindsActorAndReturnsRotatedSession(t *testing.T) {
	pool := newSubscriptionWorkerPool(nil)
	defer pool.Close()
	var process *subscriptionWorkerProcess
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		process = fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			switch command["operation"] {
			case "describe":
				return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"methods":[{"method_id":"0","type":"api","label":"API key","prompts":[]}]}`)}}
			case "auth.start":
				if command["key"] != "initial-secret" {
					t.Errorf("Synthetic API key was not forwarded")
				}
				process.mu.Lock()
				process.auth = `{"type":"api","key":"initial-secret"}`
				process.mu.Unlock()
				return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"state":"complete"}`)}}
			case "models":
				process.mu.Lock()
				process.auth = `{"type":"oauth","access":"rotated-secret"}`
				process.mu.Unlock()
				return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"model","protocol":"chat"}]}`)}}
			}
			return nil
		})
		return process, nil
	}
	a := &SubscriptionWorkerAdapter{Pool: pool, Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}
	result, err := a.startAuthorization(context.Background(), "admin-a", subscriptionWorkerAuthInput{Inputs: map[string]string{"__tokenhub_api_key": "initial-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if result["session_json"] != nil || result["auth"] != nil {
		t.Fatalf("Start exposed credentials: %#v", result)
	}
	input := subscriptionWorkerAuthInput{SessionID: result["session_id"].(string), State: result["state"].(string)}
	if _, err := a.continueAuthorization(context.Background(), "admin-b", input, false); err == nil {
		t.Fatal("A second administrator could consume the session")
	}
	wrongState := input
	wrongState.State = "invalid"
	if _, err := a.continueAuthorization(context.Background(), "admin-a", wrongState, false); err == nil {
		t.Fatal("An invalid state could consume the session")
	}
	other := &SubscriptionWorkerAdapter{Pool: pool, Definition: subscriptionWorkerDefinition{"kiro", "Kiro"}}
	if _, err := other.continueAuthorization(context.Background(), "admin-a", input, false); err == nil {
		t.Fatal("A second provider could consume the session")
	}
	complete, err := a.continueAuthorization(context.Background(), "admin-a", input, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(complete["session_json"].(string), "rotated-secret") || complete["catalog"] == nil {
		t.Fatalf("Lost rotation or catalog: %#v", complete)
	}
	if _, err := a.continueAuthorization(context.Background(), "admin-a", input, false); err == nil {
		t.Fatal("Authorization completion could be replayed")
	}
}

func TestSubscriptionWorkerSessionIdentifiersIncludeTrustedCaller(t *testing.T) {
	payload := map[string]json.RawMessage{"prompt_cache_key": json.RawMessage(`"client-thread"`), "messages": json.RawMessage(`[{"role":"user","content":"Hello"}]`)}
	one := withSubscriptionCaller(context.Background(), "project-a", "key-a")
	two := withSubscriptionCaller(context.Background(), "project-a", "key-b")
	if subscriptionWorkerSessionID(one, payload) == subscriptionWorkerSessionID(two, payload) {
		t.Fatal("Two API keys shared a retained conversation")
	}
	first := subscriptionWorkerSessionID(one, payload)
	second := subscriptionWorkerSessionID(one, payload)
	if first != second {
		t.Fatal("Same caller conversation was not stable")
	}
	probeOne := subscriptionWorkerSessionID(context.Background(), payload)
	probeTwo := subscriptionWorkerSessionID(context.Background(), payload)
	if probeOne == probeTwo {
		t.Fatal("Unscoped probes shared a retained conversation")
	}
}

func TestSubscriptionWorkerAccountGateHonorsCancellation(t *testing.T) {
	pool := newSubscriptionWorkerPool(nil)
	defer pool.Close()
	release, err := pool.enterAccount(context.Background(), "resource")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := pool.enterAccount(ctx, "resource"); err == nil {
		t.Fatal("Expected queued account operation to cancel")
	}
	release()
	pool.mu.Lock()
	count := len(pool.accounts)
	pool.mu.Unlock()
	if count != 0 {
		t.Fatalf("Account synchronization entries leaked: %d", count)
	}
}

func TestSubscriptionWorkerImportValidationRejectsMalformedSessions(t *testing.T) {
	for _, auth := range []string{"", `null`, `[]`, `{"key":"secret"}`, `{"type":"shell","command":"command"}`} {
		if err := validateSubscriptionSession(auth); err == nil {
			t.Fatalf("Accepted malformed session: %s", auth)
		}
	}
}

func TestSubscriptionWorkerAuthExpiryReleasesPoolSlot(t *testing.T) {
	pool := newSubscriptionWorkerPool(nil)
	defer pool.Close()
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(map[string]any) []subscriptionWorkerEvent { return nil }), nil
	}
	process, release, err := pool.acquire(context.Background(), "auth-session", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	pool.mu.Lock()
	pool.sessions["auth-session"] = &subscriptionWorkerAuthorization{process: process, release: release, poolKey: "auth-session", expires: time.Now().Add(-time.Second)}
	pool.mu.Unlock()
	pool.evict(time.Now())
	pool.mu.Lock()
	count := len(pool.entries) + len(pool.sessions)
	pool.mu.Unlock()
	if count != 0 || process.ctx.Err() == nil {
		t.Fatal("Expired authorization retained its slot")
	}
}

func TestSubscriptionWorkerLeaseCoversCredentialLoadAndInference(t *testing.T) {
	pool := newSubscriptionWorkerPool(nil)
	defer pool.Close()
	held := false
	calls := 0
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			if !held {
				t.Error("Worker operation escaped the cluster lease")
			}
			calls++
			if command["operation"] == "models" {
				return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"test","protocol":"chat"}]}`)}}
			}
			return []subscriptionWorkerEvent{{Event: "response", Status: 200}, {Event: "end"}}
		}), nil
	}
	adapter := &SubscriptionWorkerAdapter{Pool: pool, Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}, SaveSession: func(context.Context, string, string, string, string) error { return nil },
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			if !held {
				t.Error("Read credentials before acquiring lease")
			}
			return ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"test"}`}, nil
		},
		RunClusterOperation: func(ctx context.Context, key string, fn func(context.Context) error) error {
			if key != "subscription-account:account" {
				t.Fatalf("Wrong lease key: %s", key)
			}
			held = true
			defer func() { held = false }()
			return fn(ctx)
		},
	}
	_, _, err := adapter.ResourceModels(context.Background(), Provider{Type: adapter.Definition.providerType()}, ProviderResource{ID: "account", ResourceType: adapter.Definition.resourceType()}, "")
	if err != nil {
		t.Fatal(err)
	}
	if held || calls != 1 {
		t.Fatalf("Unexpected lease lifetime or operations: held=%v calls=%d", held, calls)
	}
}
