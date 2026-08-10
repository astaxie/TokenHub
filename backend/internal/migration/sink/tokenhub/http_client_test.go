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

	client := NewAdminAPIClient(ts.URL, "test-admin-token", http.DefaultClient)
	provider := server.Provider{
		Name:    "openai",
		Type:    "openai",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "sk-super-secret",
		Status:  server.StatusActive,
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

	client := NewAdminAPIClient(ts.URL, "test-admin-token", http.DefaultClient)
	_, err := client.CreateProjectKey(context.Background(), "project-1", map[string]any{"name": "pending-key"})
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

	client := NewAdminAPIClient(ts.URL, "test-admin-token", http.DefaultClient)

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

	client := NewAdminAPIClient(ts.URL, "test-admin-token", http.DefaultClient)
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
