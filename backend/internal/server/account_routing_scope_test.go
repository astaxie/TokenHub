package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"tokenhub/backend/internal/accountrouting"
)

func TestAccountStrategiesRespectScopedResourcePolicy(t *testing.T) {
	for _, strategy := range []accountrouting.Strategy{accountrouting.Smart, accountrouting.Expiry, accountrouting.LeastUsed, accountrouting.Drain} {
		t.Run(string(strategy), func(t *testing.T) {
			server, store, call, _ := accountRoutingFixture(t, strategy)
			project := store.CreateProject(Project{ID: "scoped-account-project", Name: "Scoped", Status: StatusActive})
			key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Scoped", Status: StatusActive}, "thk_scoped_accounts")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateResourceChecked("routing-policies", AdminResource{Name: "Restricted accounts", Status: StatusActive, Fields: map[string]any{"scope": RoutingPolicyScopeProject, "scope_id": project.ID, "allowed_provider_resource_ids": []string{"a"}}}); err != nil {
				t.Fatal(err)
			}
			saveAccountQuota(t, store, "a", `{"used_percent":90}`)
			saveAccountQuota(t, store, "b", `{"used_percent":0}`)
			call.Project, call.Key = project, key
			routed, err := server.prepareAdmittedRoutedCall(context.Background(), call, call.Model.Name)
			if err != nil {
				t.Fatal(err)
			}
			if len(routed.Routes) != 1 || routed.Routes[0].Resource.ID != "a" {
				t.Fatalf("strategy widened resource access: %+v", routed.Routes)
			}
		})
	}
}

func TestAccountRoutingSimulationLoadsPolicyWithoutAdvancingDrain(t *testing.T) {
	server, store, call, _ := accountRoutingFixture(t, accountrouting.Drain)
	project := store.CreateProject(Project{ID: "simulation-account-project", Name: "Simulation", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Simulation", Status: StatusActive}, "thk_account_simulation")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/routing-policies/simulate", map[string]any{"project_id": project.ID, "api_key_id": key.ID, "model": call.Model.Name}, "dev_admin_token")
		if response.Code != http.StatusOK {
			t.Fatalf("simulation failed: %s", response.Body)
		}
		var result RoutingPolicyResolution
		if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
			t.Fatal(err)
		}
		if result.SelectedResourceID != "a" {
			t.Fatalf("simulation ignored account policy: %+v", result)
		}
	}
	if states := store.ListResources(accountRoutingStateKind); len(states) != 0 {
		t.Fatal("simulation advanced persistent account state")
	}
}
