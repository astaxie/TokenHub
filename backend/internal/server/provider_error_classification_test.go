package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// classificationGateway wires two candidate routes to two upstreams, each with
// its own provider resource, so one request exercises the client response, the
// failover decision and the resource health counter at once.
func classificationGateway(t *testing.T, primaryURL string, secondaryURL string, providerTypes ...string) (*Server, *GormStore, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Provider Error Classification"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "classification-key", Allowed: []string{"classified-model"}, Status: StatusActive,
	}, "thk_provider_error_classification")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "classified-model", Modality: "chat", Status: StatusActive})
	providerType := ProviderOpenAICompatible
	if len(providerTypes) > 0 {
		providerType = providerTypes[0]
	}
	for index, upstreamURL := range []string{primaryURL, secondaryURL} {
		provider := store.AddProvider(Provider{
			ID: fmt.Sprintf("prv_classified_%d", index), Name: fmt.Sprintf("Classified %d", index),
			Type: providerType, BaseURL: upstreamURL, Status: StatusActive, Healthy: true,
		})
		resource, err := store.AddProviderResource(ProviderResource{
			ID: fmt.Sprintf("rsrc_classified_%d", index), ProviderID: provider.ID,
			Name: fmt.Sprintf("Classified resource %d", index), BaseURL: upstreamURL,
			Status: StatusActive, Healthy: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		store.AddRoute(ModelRoute{
			ID: fmt.Sprintf("route_classified_%d", index), ModelName: "classified-model",
			ProviderID: provider.ID, ProviderResourceID: resource.ID,
			ProviderModel: "upstream-model", Priority: index + 1, Weight: 100,
			Status: StatusActive, Strategy: RouteStrategyPriorityOnly,
		})
	}
	return New(store), store, secret
}

func failingUpstream(t *testing.T, status int, headers map[string]string, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		for key, value := range headers {
			w.Header().Set(key, value)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream said no"}}`)
	}))
}

func resourceFailureCount(t *testing.T, store *GormStore, resourceID string) int {
	t.Helper()
	for _, resource := range store.ListProviderResources() {
		if resource.ID == resourceID {
			return resource.FailureCount
		}
	}
	t.Fatalf("provider resource %q disappeared", resourceID)
	return 0
}

// Every upstream failure is graded on three axes that used to be collapsed into
// one 502: what the caller is told, whether another candidate is tried, and
// whether the resource is blamed.
func TestProviderErrorClassification(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		upstreamStatus      int
		wantStatus          int
		wantCode            string
		wantSecondaryCalled bool
		wantFailureCount    int
	}{
		{
			name: "malformed request is the caller's to fix", upstreamStatus: http.StatusBadRequest,
			wantStatus: http.StatusBadRequest, wantCode: "provider_invalid_request",
			wantSecondaryCalled: false, wantFailureCount: 0,
		},
		{
			name: "unprocessable request is the caller's to fix", upstreamStatus: http.StatusUnprocessableEntity,
			wantStatus: http.StatusUnprocessableEntity, wantCode: "provider_invalid_request",
			wantSecondaryCalled: false, wantFailureCount: 0,
		},
		{
			name: "rejected credential blames the resource", upstreamStatus: http.StatusUnauthorized,
			wantStatus: http.StatusBadGateway, wantCode: "provider_auth_error",
			wantSecondaryCalled: true, wantFailureCount: 1,
		},
		{
			name: "forbidden credential blames the resource", upstreamStatus: http.StatusForbidden,
			wantStatus: http.StatusBadGateway, wantCode: "provider_auth_error",
			wantSecondaryCalled: true, wantFailureCount: 1,
		},
		{
			name: "unpaid account blames the resource", upstreamStatus: http.StatusPaymentRequired,
			wantStatus: http.StatusBadGateway, wantCode: "provider_payment_required",
			wantSecondaryCalled: true, wantFailureCount: 1,
		},
		{
			name: "missing model tries elsewhere without blaming the resource", upstreamStatus: http.StatusNotFound,
			wantStatus: http.StatusBadGateway, wantCode: "provider_model_not_found",
			wantSecondaryCalled: true, wantFailureCount: 0,
		},
		{
			name: "rate limit stays a rate limit", upstreamStatus: http.StatusTooManyRequests,
			wantStatus: http.StatusTooManyRequests, wantCode: "provider_rate_limited",
			wantSecondaryCalled: true, wantFailureCount: 1,
		},
		{
			name: "upstream fault tries elsewhere", upstreamStatus: http.StatusInternalServerError,
			wantStatus: http.StatusBadGateway, wantCode: "provider_upstream_error",
			wantSecondaryCalled: true, wantFailureCount: 1,
		},
		{
			name: "upstream unavailability is reported as itself", upstreamStatus: http.StatusServiceUnavailable,
			wantStatus: http.StatusServiceUnavailable, wantCode: "provider_upstream_unavailable",
			wantSecondaryCalled: true, wantFailureCount: 1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			primaryCalls, secondaryCalls := 0, 0
			primary := failingUpstream(t, testCase.upstreamStatus, nil, &primaryCalls)
			defer primary.Close()
			// The secondary answers the same way, so the response the caller sees
			// describes the failure rather than whichever candidate ran last.
			secondary := failingUpstream(t, testCase.upstreamStatus, nil, &secondaryCalls)
			defer secondary.Close()

			server, store, secret := classificationGateway(t, primary.URL, secondary.URL)
			response := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
				"model": "classified-model", "messages": []map[string]any{{"role": "user", "content": "hi"}},
			}, secret)

			if response.Code != testCase.wantStatus {
				t.Fatalf("caller saw %d, want %d: %s", response.Code, testCase.wantStatus, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode gateway error: %v", err)
			}
			errorBody, _ := body["error"].(map[string]any)
			if errorBody["code"] != testCase.wantCode {
				t.Fatalf("error code = %v, want %q", errorBody["code"], testCase.wantCode)
			}
			if primaryCalls != 1 {
				t.Fatalf("primary upstream calls = %d, want 1", primaryCalls)
			}
			if secondaryCalled := secondaryCalls > 0; secondaryCalled != testCase.wantSecondaryCalled {
				t.Fatalf("second candidate tried = %v, want %v", secondaryCalled, testCase.wantSecondaryCalled)
			}
			if got := resourceFailureCount(t, store, "rsrc_classified_0"); got != testCase.wantFailureCount {
				t.Fatalf("resource failure count = %d, want %d", got, testCase.wantFailureCount)
			}
		})
	}
}

// The upstream already said how long to wait; answering 429 without it makes the
// caller guess.
func TestProviderRateLimitForwardsRetryAfter(t *testing.T) {
	calls := 0
	upstream := failingUpstream(t, http.StatusTooManyRequests, map[string]string{"retry-after": "42"}, &calls)
	defer upstream.Close()

	server, _, secret := classificationGateway(t, upstream.URL, upstream.URL)
	response := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model": "classified-model", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, secret)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("caller saw %d, want 429: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("retry-after"); got != "42" {
		t.Fatalf("retry-after = %q, want %q", got, "42")
	}
}

// The status the caller is told is deliberately not the upstream's, so an
// operator diagnosing a route needs the original recorded alongside it.
func TestRouteAttemptLogRecordsUpstreamStatus(t *testing.T) {
	calls := 0
	upstream := failingUpstream(t, http.StatusUnauthorized, nil, &calls)
	defer upstream.Close()

	server, store, secret := classificationGateway(t, upstream.URL, upstream.URL)
	response := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model": "classified-model", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, secret)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("caller saw %d, want 502: %s", response.Code, response.Body.String())
	}

	var attempts []RouteAttemptLog
	if err := store.db.Order("attempt_index asc").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) == 0 {
		t.Fatal("no route attempts were recorded")
	}
	for _, attempt := range attempts {
		if attempt.StatusCode != http.StatusBadGateway {
			t.Fatalf("attempt status = %d, want the 502 the caller saw", attempt.StatusCode)
		}
		if attempt.UpstreamStatus != http.StatusUnauthorized {
			t.Fatalf("attempt upstream_status = %d, want the 401 the provider returned", attempt.UpstreamStatus)
		}
	}
}

// The classifier is what the routing and health decisions read, so pin the table
// itself rather than only its effects.
func TestClassifyProviderStatusTable(t *testing.T) {
	for _, testCase := range []struct {
		upstreamStatus  int
		wantStatus      int
		wantDisposition ProviderErrorDisposition
		wantFailover    bool
		wantOutcome     AttemptOutcome
	}{
		{http.StatusBadRequest, http.StatusBadRequest, ProviderErrorClient, false, AttemptNeutral},
		{http.StatusUnprocessableEntity, http.StatusUnprocessableEntity, ProviderErrorClient, false, AttemptNeutral},
		{http.StatusUnauthorized, http.StatusBadGateway, ProviderErrorAuthBroken, true, AttemptFailed},
		{http.StatusForbidden, http.StatusBadGateway, ProviderErrorAuthBroken, true, AttemptFailed},
		{http.StatusPaymentRequired, http.StatusBadGateway, ProviderErrorQuotaExhausted, true, AttemptFailed},
		{http.StatusNotFound, http.StatusBadGateway, ProviderErrorModelUnsupported, true, AttemptNeutral},
		{http.StatusRequestTimeout, http.StatusGatewayTimeout, ProviderErrorTransientSame, true, AttemptFailed},
		{http.StatusTooManyRequests, http.StatusTooManyRequests, ProviderErrorTransientSame, true, AttemptFailed},
		{http.StatusRequestEntityTooLarge, http.StatusRequestEntityTooLarge, ProviderErrorClient, false, AttemptNeutral},
		{http.StatusInternalServerError, http.StatusBadGateway, ProviderErrorTransientSame, true, AttemptFailed},
		{http.StatusBadGateway, http.StatusBadGateway, ProviderErrorTransientSame, true, AttemptFailed},
		{http.StatusServiceUnavailable, http.StatusServiceUnavailable, ProviderErrorTransientSame, true, AttemptFailed},
		{http.StatusGatewayTimeout, http.StatusGatewayTimeout, ProviderErrorTransientSame, true, AttemptFailed},
		// Unrecognised 4xx: the gateway status is kept, the treatment is not.
		{http.StatusConflict, http.StatusBadGateway, ProviderErrorClient, false, AttemptNeutral},
		{http.StatusMethodNotAllowed, http.StatusBadGateway, ProviderErrorClient, false, AttemptNeutral},
		{http.StatusNotAcceptable, http.StatusBadGateway, ProviderErrorClient, false, AttemptNeutral},
	} {
		t.Run(http.StatusText(testCase.upstreamStatus), func(t *testing.T) {
			err := newProviderHTTPError(testCase.upstreamStatus, nil, []byte(`{"error":{"message":"no"}}`))
			if got := providerErrorDisposition(err); got != testCase.wantDisposition {
				t.Fatalf("disposition = %q, want %q", got, testCase.wantDisposition)
			}
			if got := shouldFailoverRoutedError(err, false); got != testCase.wantFailover {
				t.Fatalf("failover = %v, want %v", got, testCase.wantFailover)
			}
			if got := providerAttemptOutcome(err); got != testCase.wantOutcome {
				t.Fatalf("attempt outcome = %v, want %v", got, testCase.wantOutcome)
			}
			if got := AsHTTPError(err).UpstreamStatus; got != testCase.upstreamStatus {
				t.Fatalf("upstream status = %d, want %d", got, testCase.upstreamStatus)
			}
			if got := AsHTTPError(err).Status; got != testCase.wantStatus {
				t.Fatalf("caller status = %d, want %d", got, testCase.wantStatus)
			}
		})
	}
}

// A candidate bound by session affinity must not be abandoned for a transient
// fault, which is the one case where the disposition alone is not the answer.
func TestBoundRouteDoesNotFailOverOnTransientFault(t *testing.T) {
	err := newProviderHTTPError(http.StatusServiceUnavailable, nil, []byte("busy"))
	if shouldFailoverRoutedError(err, true) {
		t.Fatal("a bound route must stay put on a transient upstream fault")
	}
	if !shouldFailoverRoutedError(err, false) {
		t.Fatal("an unbound route must move on from a transient upstream fault")
	}
}

// Content the provider refused is the caller's prompt, not a broken account.
// Counting it would cool down a working resource over content it correctly
// declined, and failing over would only get the same refusal elsewhere.
func TestContentFilterDoesNotBlameTheResource(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"promptFeedback":{"blockReason":"SAFETY"}}`)
	}))
	defer upstream.Close()

	server, store, secret := classificationGateway(t, upstream.URL, upstream.URL, ProviderGemini)

	response := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model": "classified-model", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, secret)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("caller saw %d, want 400: %s", response.Code, response.Body.String())
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d, want 1: a refusal must not be retried elsewhere", calls)
	}
	if got := resourceFailureCount(t, store, "rsrc_classified_0"); got != 0 {
		t.Fatalf("resource failure count = %d, want 0", got)
	}
}

// Session affinity binds a request to one resource. A transient fault is worth
// waiting out on that resource, but a broken credential or an unpayable account
// is not: the binding is abandoned so the request can still be served.
func TestBoundRouteFailoverDependsOnWhatFailed(t *testing.T) {
	for _, testCase := range []struct {
		upstreamStatus    int
		wantBoundFailover bool
	}{
		{http.StatusUnauthorized, true},
		{http.StatusPaymentRequired, true},
		{http.StatusNotFound, false},
		{http.StatusServiceUnavailable, false},
		{http.StatusBadRequest, false},
	} {
		t.Run(http.StatusText(testCase.upstreamStatus), func(t *testing.T) {
			err := newProviderHTTPError(testCase.upstreamStatus, nil, []byte("no"))
			if got := shouldFailoverRoutedError(err, true); got != testCase.wantBoundFailover {
				t.Fatalf("bound failover = %v, want %v", got, testCase.wantBoundFailover)
			}
		})
	}
}

// Retry-After describes a rate limit. Forwarding it on an unavailability would
// tell the caller a wait is enough when another candidate is what is needed.
func TestRetryAfterIsNotForwardedOnOtherErrors(t *testing.T) {
	calls := 0
	upstream := failingUpstream(t, http.StatusServiceUnavailable, map[string]string{"retry-after": "42"}, &calls)
	defer upstream.Close()

	server, _, secret := classificationGateway(t, upstream.URL, upstream.URL)
	response := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model": "classified-model", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, secret)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("caller saw %d, want 503: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("retry-after"); got != "" {
		t.Fatalf("retry-after = %q, want it absent on a non-429 answer", got)
	}
}

// A streaming request is classified before its first byte reaches the client, so
// the same decisions have to survive the streaming error path.
func TestStreamingRequestIsClassifiedBeforeTheFirstByte(t *testing.T) {
	primaryCalls, secondaryCalls := 0, 0
	primary := failingUpstream(t, http.StatusBadRequest, nil, &primaryCalls)
	defer primary.Close()
	secondary := failingUpstream(t, http.StatusBadRequest, nil, &secondaryCalls)
	defer secondary.Close()

	server, store, secret := classificationGateway(t, primary.URL, secondary.URL)
	response := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model": "classified-model", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, secret)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("caller saw %d, want 400: %s", response.Code, response.Body.String())
	}
	if secondaryCalls != 0 {
		t.Fatalf("second candidate tried %d times; a malformed request must not fail over", secondaryCalls)
	}
	if got := resourceFailureCount(t, store, "rsrc_classified_0"); got != 0 {
		t.Fatalf("resource failure count = %d, want 0", got)
	}
}

// The gateway credential is TokenHub's, not the caller's, and upstream bodies
// quote it back. The caller gets the fact, the operator gets the status.
func TestAuthErrorDoesNotForwardTheUpstreamBody(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key provided: sk-Xr***XYZ"}}`)
	}))
	defer upstream.Close()

	server, _, secret := classificationGateway(t, upstream.URL, upstream.URL)
	response := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model": "classified-model", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	}, secret)
	if body := response.Body.String(); strings.Contains(body, "sk-Xr") {
		t.Fatalf("the upstream credential fragment reached the caller: %s", body)
	}
}
