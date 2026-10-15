package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJevBoundRouteRejectionsAreAudited(t *testing.T) {
	for _, tc := range []struct{ name, reason string }{{"policy_removed", "bound_candidate_removed"}, {"route_missing", "bound_route_unavailable"}, {"resource_replaced", "bound_route_unavailable"}} {
		t.Run(tc.name, func(t *testing.T) {
			server, routed, _, policy := jevFixture(t)
			bound := routed.Routes[1]
			bound.Resource = &ProviderResource{ID: "private-original-resource"}
			routed.Routes[1] = bound
			if err := server.bindJevResponse(context.Background(), routed.Call, bound, map[string]any{"id": "private-response-id"}, ""); err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "policy_removed":
				policy.SemanticRouting.Candidates = []SemanticRoutingCandidate{policy.SemanticRouting.Candidates[0], policy.SemanticRouting.Candidates[2]}
				encoded, err := json.Marshal(policy.SemanticRouting)
				if err != nil {
					t.Fatal(err)
				}
				routed.Call.Model.Metadata = cloneStringMap(routed.Call.Model.Metadata)
				routed.Call.Model.Metadata[semanticRoutingMetadataKey] = string(encoded)
			case "route_missing":
				routed.Routes = []RouteSelection{routed.Routes[0], routed.Routes[2]}
			case "resource_replaced":
				routed.Routes[1].Resource = &ProviderResource{ID: "private-other-resource"}
			}
			routed.Call.RequestID = "req_bound_rejection"
			var req ResponsesRequest
			if err := json.Unmarshal([]byte(`{"model":"auto-chat","previous_response_id":"private-response-id","input":"private-prompt","metadata":{"private":"private-value"}}`), &req); err != nil {
				t.Fatal(err)
			}
			err := server.applyJevResponsesRouting(context.Background(), &routed, &req, nil)
			if err == nil || AsHTTPError(err).Status != 409 {
				t.Fatalf("expected preserved 409, got %v", err)
			}
			found := false
			for _, event := range server.store.ListAuditEvents() {
				if event.Action != "routing.jev_admission" {
					continue
				}
				found = true
				var snapshot struct {
					Rejections []jevCandidateRejection `json:"rejections"`
				}
				if e := json.Unmarshal([]byte(event.AfterSnapshot), &snapshot); e != nil {
					t.Fatal(e)
				}
				if event.ResourceID != routed.Call.RequestID || len(snapshot.Rejections) != 1 || snapshot.Rejections[0].RouteID != bound.Route.ID || snapshot.Rejections[0].Reason != tc.reason {
					t.Fatalf("incorrect bound diagnostic: %+v", event)
				}
				for _, secret := range []string{"private-response-id", "private-prompt", "private-value", "private-original-resource", "private-other-resource", "provider-secret", "thk_semantic_test"} {
					if strings.Contains(event.AfterSnapshot, secret) || strings.Contains(err.Error(), secret) {
						t.Fatalf("secret exposed in bound rejection: %s", secret)
					}
				}
			}
			if !found {
				t.Fatal("bound rejection was not audited")
			}
		})
	}
}
