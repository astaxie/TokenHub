package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const quotaResetTestResourceID = "rsrc_quota_reset_test"

type quotaResetUpstream struct {
	availableCount int
	creditID       string
	consumeStatus  int
	consumeBody    string
	consumeCalls   int
	getCalls       int
	authorizations []string
	getAuth        []string
	getAccountIDs  []string
}

func (u *quotaResetUpstream) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "application/json")
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/rate-limit-reset-credits"):
		u.getCalls++
		u.getAuth = append(u.getAuth, r.Header.Get("authorization"))
		u.getAccountIDs = append(u.getAccountIDs, r.Header.Get("chatgpt-account-id"))
		_, _ = io.WriteString(w, `{"available_count":`+stringifyValueForTest(u.availableCount)+`,"credits":[{"id":"`+u.creditID+`","status":"available","expires_at":"2099-01-01T00:00:00Z"}]}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rate-limit-reset-credits/consume"):
		u.consumeCalls++
		u.authorizations = append(u.authorizations, r.Header.Get("authorization"))
		status := u.consumeStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		body := u.consumeBody
		if body == "" {
			body = `{"code":"reset","windows_reset":2}`
		}
		_, _ = io.WriteString(w, body)
	default:
		http.NotFound(w, r)
	}
}

func newQuotaResetTestServer(t *testing.T, upstream *quotaResetUpstream, credentials ProviderResourceCredentials) (*Server, *GormStore, *httptest.Server) {
	t.Helper()
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_quota_reset_test", Name: "Quota Reset Codex", Type: ProviderOpenAICodex,
		Status: StatusActive, Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID: quotaResetTestResourceID, ProviderID: provider.ID, Name: "Quota Reset Account",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &credentials,
	}); err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(upstream.handler))
	t.Cleanup(fake.Close)
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "quota-reset-test-secret"})
	server.codexSubscription.QuotaURL = fake.URL + "/backend-api/wham/usage"
	server.codexSubscription.Client = fake.Client()
	return server, store, fake
}

func quotaResetTestCredentials() ProviderResourceCredentials {
	return ProviderResourceCredentials{
		AuthType: "oauth", AccessToken: "access_initial", AccountID: "account_reset_test",
	}
}

func quotaResetRequest(t *testing.T, handler http.Handler, token string, idempotencyKey string, expectedCount int, creditID string, dangerous bool) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{
		"confirm": true, "idempotency_key": idempotencyKey,
		"expected_available_count": expectedCount, "credit_id": creditID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/provider-resources/"+quotaResetTestResourceID+"/quota/reset", bytes.NewReader(encoded))
	request.Header.Set("authorization", "Bearer "+token)
	request.Header.Set("content-type", "application/json")
	request.Header.Set("idempotency-key", idempotencyKey)
	if dangerous {
		request.Header.Set(openAIAccountQuotaResetDangerHeader, openAIAccountQuotaResetDangerValue)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func quotaResetErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode reset error: %v: %s", err, response.Body.String())
	}
	return payload.Error.Code
}

func TestCodexQuotaResetRequiresRBACAndDangerousConfirmation(t *testing.T) {
	upstream := &quotaResetUpstream{availableCount: 1, creditID: "credit_rbac"}
	server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())
	if _, err := store.CreateAdminUser(AdminUser{
		Username: "quota-admin", Name: "Quota Admin", Email: "quota-admin@example.com",
		Role: "admin", Status: StatusActive,
	}, "quota-admin-password"); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateAdminUser(AdminUser{
		Username: "quota-viewer", Name: "Quota Viewer", Email: "quota-viewer@example.com",
		Role: "viewer", Status: StatusActive,
	}, "quota-viewer-password")
	if err != nil {
		t.Fatal(err)
	}
	_ = viewer
	login := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "quota-viewer@example.com", "password": "quota-viewer-password",
	}, "")
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &session); err != nil || session.Token == "" {
		t.Fatalf("viewer login failed: %v %s", err, login.Body)
	}
	forbidden := quotaResetRequest(t, server.Handler(), session.Token, "rbac-op", 1, "credit_rbac", true)
	if forbidden.Code != http.StatusForbidden || upstream.consumeCalls != 0 {
		t.Fatalf("viewer reset = %d, consumes=%d: %s", forbidden.Code, upstream.consumeCalls, forbidden.Body.String())
	}
	missingDanger := quotaResetRequest(t, server.Handler(), "dev_admin_token", "danger-op", 1, "credit_rbac", false)
	if missingDanger.Code != http.StatusBadRequest || quotaResetErrorCode(t, missingDanger) != "quota_reset_danger_confirmation_required" {
		t.Fatalf("missing dangerous confirmation: %d %s", missingDanger.Code, missingDanger.Body.String())
	}
	confirmationRequest := httptest.NewRequest(http.MethodPost, "/api/admin/provider-resources/"+quotaResetTestResourceID+"/quota/reset", strings.NewReader(`{"confirm":false,"idempotency_key":"confirm-op","expected_available_count":1,"credit_id":"credit_rbac"}`))
	confirmationRequest.Header.Set("authorization", "Bearer dev_admin_token")
	confirmationRequest.Header.Set("content-type", "application/json")
	confirmationRequest.Header.Set("idempotency-key", "confirm-op")
	confirmationRequest.Header.Set(openAIAccountQuotaResetDangerHeader, openAIAccountQuotaResetDangerValue)
	confirmationResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(confirmationResponse, confirmationRequest)
	if confirmationResponse.Code != http.StatusBadRequest || quotaResetErrorCode(t, confirmationResponse) != "quota_reset_confirmation_required" {
		t.Fatalf("missing body confirmation: %d %s", confirmationResponse.Code, confirmationResponse.Body.String())
	}
	if upstream.consumeCalls != 0 {
		t.Fatalf("unsafe requests reached consume endpoint: %d", upstream.consumeCalls)
	}
}

func TestCodexQuotaResetPreflightIdempotencyAndAudit(t *testing.T) {
	upstream := &quotaResetUpstream{availableCount: 1, creditID: "credit_once"}
	server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())
	handler := server.Handler()

	countChanged := quotaResetRequest(t, handler, "dev_admin_token", "count-changed", 2, "credit_once", true)
	if countChanged.Code != http.StatusConflict || quotaResetErrorCode(t, countChanged) != "quota_reset_available_count_changed" {
		t.Fatalf("count preflight: %d %s", countChanged.Code, countChanged.Body.String())
	}
	creditChanged := quotaResetRequest(t, handler, "dev_admin_token", "credit-changed", 1, "different-credit", true)
	if creditChanged.Code != http.StatusConflict || quotaResetErrorCode(t, creditChanged) != "quota_reset_credit_unavailable" {
		t.Fatalf("credit preflight: %d %s", creditChanged.Code, creditChanged.Body.String())
	}

	first := quotaResetRequest(t, handler, "dev_admin_token", "reset-once", 1, "credit_once", true)
	if first.Code != http.StatusOK || upstream.consumeCalls != 1 {
		t.Fatalf("first reset: %d consumes=%d %s", first.Code, upstream.consumeCalls, first.Body.String())
	}
	replay := quotaResetRequest(t, handler, "dev_admin_token", "reset-once", 1, "credit_once", true)
	if replay.Code != http.StatusOK || upstream.consumeCalls != 1 {
		t.Fatalf("idempotent replay: %d consumes=%d %s", replay.Code, upstream.consumeCalls, replay.Body.String())
	}
	mismatch := quotaResetRequest(t, handler, "dev_admin_token", "reset-once", 2, "credit_once", true)
	if mismatch.Code != http.StatusConflict || quotaResetErrorCode(t, mismatch) != "quota_reset_idempotency_payload_mismatch" || upstream.consumeCalls != 1 {
		t.Fatalf("payload mismatch: %d consumes=%d %s", mismatch.Code, upstream.consumeCalls, mismatch.Body.String())
	}

	operationID := openAIAccountQuotaResetOperationID(quotaResetTestResourceID, "reset-once")
	operation, ok := openAIAccountQuotaResetOperationByID(store.ListResources(openAIAccountQuotaResetOperationKind), operationID)
	if !ok || operation.State != openAIAccountQuotaResetSucceeded || operation.WindowsReset != 2 {
		t.Fatalf("persisted operation = %+v, found=%v", operation, ok)
	}
	var sawSuccess, sawFailure bool
	for _, event := range store.ListAuditEvents() {
		if event.Action != "reset_quota" || event.ResourceID != quotaResetTestResourceID {
			continue
		}
		sawSuccess = sawSuccess || event.Status == "success"
		sawFailure = sawFailure || event.Status == "failed"
	}
	if !sawSuccess || !sawFailure {
		t.Fatalf("quota reset audit trail missing success or failure: %+v", store.ListAuditEvents())
	}
}

func TestCodexQuotaResetRecoversPendingAndUnknownOperations(t *testing.T) {
	for _, state := range []string{openAIAccountQuotaResetPending, openAIAccountQuotaResetUnknown} {
		t.Run(state, func(t *testing.T) {
			upstream := &quotaResetUpstream{availableCount: 1, creditID: "credit_recovery"}
			server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())
			key := "recover-" + state
			count := 1
			creditID := "credit_recovery"
			req := openAIAccountQuotaResetRequest{Confirm: true, IdempotencyKey: key, ExpectedAvailableCount: &count, CreditID: &creditID}
			operation, err := server.createOpenAIAccountQuotaResetOperation(
				openAIAccountQuotaResetOperationID(quotaResetTestResourceID, key), quotaResetTestResourceID, req,
				openAIAccountQuotaResetCredit{ID: creditID, Status: "available"},
			)
			if err != nil {
				t.Fatal(err)
			}
			if state == openAIAccountQuotaResetUnknown {
				if _, err := server.updateOpenAIAccountQuotaResetOperation(operation, state, openAIAccountQuotaResetResult{}, openAIAccountQuotaResetOutcomeUnknownError(http.StatusBadGateway)); err != nil {
					t.Fatal(err)
				}
				upstream.consumeBody = `{"code":"already_redeemed","windows_reset":2}`
			}
			response := quotaResetRequest(t, server.Handler(), "dev_admin_token", key, 1, creditID, true)
			if response.Code != http.StatusOK || upstream.getCalls != 0 || upstream.consumeCalls != 1 {
				t.Fatalf("recover %s: %d gets=%d consumes=%d %s", state, response.Code, upstream.getCalls, upstream.consumeCalls, response.Body.String())
			}
			persisted, ok := openAIAccountQuotaResetOperationByID(store.ListResources(openAIAccountQuotaResetOperationKind), operation.Resource.ID)
			if !ok || persisted.State != openAIAccountQuotaResetSucceeded {
				t.Fatalf("recovered operation = %+v, found=%v", persisted, ok)
			}
		})
	}
}

func TestCodexQuotaResetRefreshesCredentialsAfterUnauthorized(t *testing.T) {
	upstream := &quotaResetUpstream{availableCount: 1, creditID: "credit_refresh"}
	credentials := quotaResetTestCredentials()
	credentials.RefreshToken = "refresh_reset_test"
	credentials.ClientID = openAIAccountOAuthClientID
	credentials.ExpiresAt = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	server, _, _ := newQuotaResetTestServer(t, upstream, credentials)

	tokenCalls := 0
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access_refreshed","refresh_token":"refresh_reset_test","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"available_count":1,"credits":[{"id":"credit_refresh","status":"available"}]}`)), Request: request}, nil
		}
		upstream.consumeCalls++
		upstream.authorizations = append(upstream.authorizations, request.Header.Get("authorization"))
		if request.Header.Get("authorization") == "Bearer access_initial" {
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"unauthorized"}}`)), Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"reset","windows_reset":1}`)), Request: request}, nil
	})}

	response := quotaResetRequest(t, server.Handler(), "dev_admin_token", "refresh-401", 1, "credit_refresh", true)
	if response.Code != http.StatusOK || tokenCalls != 1 || upstream.consumeCalls != 2 {
		t.Fatalf("credential refresh: status=%d tokenCalls=%d consumes=%d body=%s", response.Code, tokenCalls, upstream.consumeCalls, response.Body.String())
	}
	if len(upstream.authorizations) != 2 || upstream.authorizations[1] != "Bearer access_refreshed" {
		t.Fatalf("unexpected authorization sequence: %#v", upstream.authorizations)
	}
}

func TestCodexQuotaResetMapsDefinitiveAndUnknownUpstreamOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
		wantState  string
	}{
		{name: "ineligible", status: http.StatusForbidden, body: `{"detail":{"code":"rate_limit_reset_ineligible"}}`, wantStatus: http.StatusConflict, wantCode: "quota_reset_ineligible", wantState: openAIAccountQuotaResetFailed},
		{name: "server outcome unknown", status: http.StatusInternalServerError, body: `{"error":{"code":"internal_error"}}`, wantStatus: http.StatusBadGateway, wantCode: "openai_quota_reset_outcome_unknown", wantState: openAIAccountQuotaResetUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := &quotaResetUpstream{availableCount: 1, creditID: "credit_outcome", consumeStatus: test.status, consumeBody: test.body}
			server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())
			key := "outcome-" + strings.ReplaceAll(test.name, " ", "-")
			response := quotaResetRequest(t, server.Handler(), "dev_admin_token", key, 1, "credit_outcome", true)
			if response.Code != test.wantStatus || quotaResetErrorCode(t, response) != test.wantCode {
				t.Fatalf("outcome mapping: %d %s", response.Code, response.Body.String())
			}
			operation, ok := openAIAccountQuotaResetOperationByID(store.ListResources(openAIAccountQuotaResetOperationKind), openAIAccountQuotaResetOperationID(quotaResetTestResourceID, key))
			if !ok || operation.State != test.wantState {
				t.Fatalf("operation state = %+v, found=%v", operation, ok)
			}
		})
	}
}
