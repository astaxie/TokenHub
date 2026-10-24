package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionWorkerCancellationUnblocksUnconsumedStream(t *testing.T) {
	p := fakeSubscriptionProcess("", func(map[string]any) []subscriptionWorkerEvent {
		return []subscriptionWorkerEvent{{Event: "response", Status: 200}, {Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte("pending"))}}
	})
	defer p.cancel()
	a := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}
	transport := subscriptionWorkerTransport{adapter: a, process: p, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}, stream: true}
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{}`))
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	cancel()
	select {
	case <-p.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Cancellation left the blocked stream running")
	}
}

func TestSubscriptionWorkerRelayRetainsConfiguredSSRFGuard(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ACCESS_MODE", "strict")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", "false")
	client, _, _ := newUpstreamClients(Config{})
	p := fakeSubscriptionProcess("", func(map[string]any) []subscriptionWorkerEvent { return nil })
	defer p.cancel()
	p.client = subscriptionClient(client)
	var event map[string]any
	p.input = subscriptionTestInput{write: func(data []byte) (int, error) { err := json.Unmarshal(data, &event); return len(data), err }}
	p.network(subscriptionWorkerEvent{RequestID: "blocked", URL: upstream.URL, Method: http.MethodGet})
	if calls.Load() != 0 || event["operation"] != "network.error" {
		t.Fatalf("SSRF guard did not reject local egress: calls=%d event=%#v", calls.Load(), event)
	}
}

func TestSubscriptionWorkerScopesClientPromptCacheKey(t *testing.T) {
	var captured map[string]any
	p := fakeSubscriptionProcess("", func(command map[string]any) []subscriptionWorkerEvent {
		captured = command
		return []subscriptionWorkerEvent{{Event: "response", Status: 200}, {Event: "end"}}
	})
	defer p.cancel()
	a := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}
	transport := subscriptionWorkerTransport{adapter: a, process: p, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}}
	ctx := withSubscriptionCaller(context.Background(), "private-project", "private-api-key")
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{"prompt_cache_key":"untrusted-thread","messages":[{"role":"user","content":"Hello"}]}`))
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	body := captured["body"].(map[string]any)
	if body["prompt_cache_key"] == "untrusted-thread" || body["prompt_cache_key"] != captured["session_id"] {
		t.Fatalf("Client key bypassed caller scope: %#v", captured)
	}
	encoded, _ := json.Marshal(captured)
	if strings.Contains(string(encoded), "private-project") || strings.Contains(string(encoded), "private-api-key") {
		t.Fatal("Internal caller identifiers were forwarded upstream")
	}
}

func TestSubscriptionWorkerRejectsUnsupportedNodeVersions(t *testing.T) {
	for _, version := range []string{"v20.19.0", "v22.22.0", "v22.23.0", "invalid", "v22.23.1-custom"} {
		if subscriptionNodeVersionSupported(version) {
			t.Fatalf("Accepted unsupported runtime: %s", version)
		}
	}
	for _, version := range []string{"v22.23.1", "v22.24.0", "v24.0.0", "v26.0.0\n"} {
		if !subscriptionNodeVersionSupported(version) {
			t.Fatalf("Rejected supported runtime: %s", version)
		}
	}
}

func TestSubscriptionWorkerClassifiesRedactedFailures(t *testing.T) {
	for _, test := range []struct {
		failure subscriptionWorkerFailure
		want    ProviderErrorDisposition
	}{
		{subscriptionWorkerFailure{Code: "invalid_credentials"}, ProviderErrorAuthBroken},
		{subscriptionWorkerFailure{SignIn: "expired"}, ProviderErrorAuthBroken},
		{subscriptionWorkerFailure{Code: "model_not_found"}, ProviderErrorModelUnsupported},
		{subscriptionWorkerFailure{Code: "invalid_request"}, ProviderErrorClient},
		{subscriptionWorkerFailure{Status: http.StatusPaymentRequired}, ProviderErrorQuotaExhausted},
		{subscriptionWorkerFailure{Code: "credential_save_failed"}, ProviderErrorTransientSame},
	} {
		err := subscriptionWorkerInvocationError(&test.failure)
		if providerErrorDisposition(err) != test.want {
			t.Fatalf("Wrong failure classification: got %s want %s", providerErrorDisposition(err), test.want)
		}
	}
}
