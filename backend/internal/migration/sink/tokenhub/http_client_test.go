package tokenhub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/server"
)

// TestProviderRequestsCarryAPIKey guards against server.Provider being
// marshalled directly: its APIKey field is tagged `json:"-"`, which would
// silently drop the resolved credential from create and update requests.
func TestProviderRequestsCarryAPIKey(t *testing.T) {
	var bodies []map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		bodies = append(bodies, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"provider":{"id":"prov-1","name":"openai","type":"openai"}}`))
	}))
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	provider := server.Provider{
		Name:             "openai",
		Type:             "openai",
		BaseURL:          "https://api.openai.com/v1",
		APIKey:           "sk-super-secret",
		Status:           server.StatusActive,
		Headers:          map[string]string{"X-Tenant": "tenant-secret"},
		SensitiveHeaders: []string{"X-Tenant"},
	}

	if _, err := client.CreateProvider(context.Background(), provider); err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := client.UpdateProvider(context.Background(), "prov-1", provider); err != nil {
		t.Fatalf("update provider: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("expected 2 captured requests, got %d", len(bodies))
	}
	for i, payload := range bodies {
		if got, _ := payload["api_key"].(string); got != "sk-super-secret" {
			t.Fatalf("request %d missing api_key, payload=%v", i, payload)
		}
		if createRoutes, ok := payload["create_routes"].(bool); !ok || createRoutes {
			t.Fatalf("request %d expected create_routes=false, payload=%v", i, payload)
		}
		if sensitive, ok := payload["sensitive_headers"].([]any); !ok || len(sensitive) != 1 || sensitive[0] != "X-Tenant" {
			t.Fatalf("request %d missing sensitive_headers, payload=%v", i, payload)
		}
		options, _ := payload["options"].(map[string]any)
		if got, _ := options["claude_code_attribution_policy"].(string); got != "preserve" {
			t.Fatalf("request %d must preserve legacy attribution behavior, payload=%v", i, payload)
		}
	}
}

func TestProviderRequestsKeepExplicitAttributionPolicy(t *testing.T) {
	req := providerWriteRequestFrom(server.Provider{Options: map[string]string{
		"claude_code_attribution_policy": "strip",
	}})
	if got := req.Options["claude_code_attribution_policy"]; got != "strip" {
		t.Fatalf("expected explicit attribution policy to win, got %q", got)
	}
}

func TestAdminAPIClientSurfacesApprovalRequired(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"approval_required":true,"approval":{"id":"apr-1"}}`))
	}))
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	_, err = client.CreateProjectKey(context.Background(), "project-1", map[string]any{"name": "pending-key"})
	var approvalErr *ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected ApprovalRequiredError, got %v", err)
	}
	if approvalErr.Method != http.MethodPost || !strings.Contains(approvalErr.Endpoint, "/projects/project-1/keys") {
		t.Fatalf("unexpected approval error: %+v", approvalErr)
	}
	if !strings.Contains(string(approvalErr.Approval), `"id":"apr-1"`) {
		t.Fatalf("expected approval payload to be preserved, got %s", approvalErr.Approval)
	}
}

// TestAPIKeyUpdateOmitsUnownedQuotaLimits guards against server.APIKey being
// marshalled directly on update. The Admin API decodes "limits" into a pointer
// and treats its presence as an explicit assignment, so always emitting the
// value field would clear the target's quota whenever the bundle carries none.
func TestAPIKeyUpdateOmitsUnownedQuotaLimits(t *testing.T) {
	var bodies []map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		bodies = append(bodies, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"key-1","name":"migrated"}`))
	}))
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}

	if _, err := client.UpdateAPIKey(context.Background(), "key-1", server.APIKey{
		Name:   "migrated",
		Group:  "default",
		Status: server.StatusActive,
	}); err != nil {
		t.Fatalf("update without limits: %v", err)
	}
	if _, err := client.UpdateAPIKey(context.Background(), "key-1", server.APIKey{
		Name:   "migrated",
		Group:  "default",
		Status: server.StatusActive,
		Limits: server.QuotaLimits{MonthlyCostUSD: 100},
	}); err != nil {
		t.Fatalf("update with limits: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("expected 2 captured requests, got %d", len(bodies))
	}
	if _, present := bodies[0]["limits"]; present {
		t.Fatalf("expected limits to be omitted when the bundle carries none, payload=%v", bodies[0])
	}
	limits, ok := bodies[1]["limits"].(map[string]any)
	if !ok {
		t.Fatalf("expected limits to be sent when the bundle carries them, payload=%v", bodies[1])
	}
	if cost, _ := limits["monthly_cost_usd"].(float64); cost != 100 {
		t.Fatalf("expected monthly_cost_usd=100, got %v", limits)
	}
}

func TestAPIKeyUpdateCarriesExplicitModelAccessMode(t *testing.T) {
	var payload map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"key-1","name":"restricted"}`))
	}))
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	if _, err := client.UpdateAPIKey(context.Background(), "key-1", server.APIKey{
		Name: "restricted", Status: server.StatusActive,
		ModelAccessMode: server.ModelAccessModeRestricted, Allowed: []string{},
	}); err != nil {
		t.Fatalf("update API key: %v", err)
	}
	if payload["model_access_mode"] != server.ModelAccessModeRestricted {
		t.Fatalf("model_access_mode missing from update payload: %+v", payload)
	}
	allowed, ok := payload["allowed_models"].([]any)
	if !ok || len(allowed) != 0 {
		t.Fatalf("restricted-empty allowed_models must be explicit: %+v", payload)
	}
}

// TestNewAdminAPIClientRejectsMalformedBaseURL guards the constructor
// validation: a malformed --to value must surface as a controlled error at
// construction time, never as a panic on first request.
func TestNewAdminAPIClientRejectsMalformedBaseURL(t *testing.T) {
	for _, baseURL := range []string{"", "not-a-url", "://missing-scheme", "https://"} {
		if _, err := NewAdminAPIClient(baseURL, "token", nil); err == nil {
			t.Errorf("expected error for base url %q", baseURL)
		}
	}
	if _, err := NewAdminAPIClient("https://api.example.com", "token", nil); err != nil {
		t.Fatalf("valid base url rejected: %v", err)
	}
}

// TestNewAdminAPIClientDefaultClientHasTimeout ensures the nil-client branch
// installs an explicit total timeout, which the production CLI relies on.
func TestNewAdminAPIClientDefaultClientHasTimeout(t *testing.T) {
	client, err := NewAdminAPIClient("https://api.example.com", "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient == nil || client.httpClient.Timeout != 30*time.Second {
		t.Fatalf("expected default 30s total timeout, got %+v", client.httpClient)
	}
}

// TestAdminAPIClientEndpointPreservesEncodedSlashes verifies that a "/"
// inside a resource ID is escaped as %2F rather than being treated as a path
// separator.
func TestAdminAPIClientEndpointPreservesEncodedSlashes(t *testing.T) {
	client, err := NewAdminAPIClient("https://api.example.com/base", "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := client.endpoint("/api/admin/resources", "team with/slash")
	if !strings.Contains(endpoint, "team%20with%2Fslash") {
		t.Fatalf("expected encoded slash preserved, got %q", endpoint)
	}
	if strings.Contains(endpoint, "team with/slash") {
		t.Fatalf("expected no raw space or slash in endpoint, got %q", endpoint)
	}
}

// TestAdminAPIClientCapsResponseBody guards the response size cap so an
// oversized admin API response fails loudly instead of being buffered whole.
func TestAdminAPIClientCapsResponseBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(make([]byte, maxAdminAPIResponseBytes+16))
	}))
	defer ts.Close()
	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = client.doJSON(context.Background(), http.MethodGet, client.endpoint("/api/admin/providers"), nil, &listResponse[server.Provider]{})
	if err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected response body cap error, got %v", err)
	}
}
