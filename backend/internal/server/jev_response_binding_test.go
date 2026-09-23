package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func testJevBindingPersistence(t *testing.T, first, second *GormStore) {
	t.Helper()
	ctx := context.Background()
	binding := jevResponseBinding{KeyHash: NewID("binding"), RouteID: "route", ProviderID: "provider", ProviderModel: "model", ResourceID: "resource", UpstreamID: "resp_test", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	t.Cleanup(func() {
		_ = first.db.Delete(&jevResponseBinding{}, "key_hash IN ?", []string{binding.KeyHash, binding.KeyHash + "-guard"}).Error
	})
	if err := first.SaveJevResponseBinding(ctx, binding, []string{binding.KeyHash + "-guard"}); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := second.LoadJevResponseBinding(ctx, binding.KeyHash)
	if err != nil || !found || loaded != binding {
		t.Fatalf("shared binding: %+v %v %v", loaded, found, err)
	}
	if err := second.SaveJevResponseBinding(ctx, binding, []string{binding.KeyHash + "-guard"}); err != nil {
		t.Fatalf("idempotent binding: %v", err)
	}
	conflicting := binding
	conflicting.ResourceID = "another-account"
	if err := second.SaveJevResponseBinding(ctx, conflicting, nil); err == nil {
		t.Fatal("binding switched resource")
	}
	if err := first.db.Model(&jevResponseBinding{}).Where("key_hash = ?", binding.KeyHash).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if _, found, err := second.LoadJevResponseBinding(ctx, binding.KeyHash); err != nil || found {
		t.Fatalf("expired binding: %v %v", found, err)
	}
	if marker, found, err := second.LoadJevResponseBinding(ctx, binding.KeyHash+"-guard"); err != nil || !found || marker.UpstreamID != "" || marker.ProviderID != "" {
		t.Fatalf("minimal ownership marker lost: %+v %v %v", marker, found, err)
	}
}

func TestJevBindingSQLitePersistence(t *testing.T) {
	url := "sqlite://" + filepath.Join(t.TempDir(), "bindings.db")
	first, err := NewSQLiteStore(url)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSQLiteStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, store := range []*GormStore{first, second} {
			db, _ := store.db.DB()
			_ = db.Close()
		}
	})
	testJevBindingPersistence(t, first, second)
}

func TestJevContinuationSurvivesStrategyChangeAndEnforcesResource(t *testing.T) {
	server, routed, _, policy := jevFixture(t)
	route := routed.Routes[1]
	route.Resource = &ProviderResource{ID: "original-account"}
	if err := server.bindJevResponse(context.Background(), routed.Call, route, map[string]any{"id": "resp_first"}, ""); err != nil {
		t.Fatal(err)
	}
	policy.Strategy = RouteStrategyQuality
	policy.SemanticRouting.Mode = "off"
	if _, err := server.store.UpdateModelRoutePolicy("auto-chat", policy); err != nil {
		t.Fatal(err)
	}
	route.Route.Strategy = RouteStrategyQuality
	routed.Routes[1] = route
	for i := range routed.Routes {
		routed.Routes[i].Route.Strategy = RouteStrategyQuality
	}
	var req ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"auto-chat","previous_response_id":"resp_first","input":"continue"}`), &req); err != nil {
		t.Fatal(err)
	}
	original := append([]RouteSelection(nil), routed.Routes...)
	if err := server.applyJevResponsesRouting(context.Background(), &routed, &req, nil); err != nil {
		t.Fatal(err)
	}
	if len(routed.Routes) != 1 || routeResourceID(routed.Routes[0]) != "original-account" || !routed.Call.JevResponseBound {
		t.Fatalf("continuation lost binding: %+v", routed.Routes)
	}
	if err := server.bindJevResponse(context.Background(), routed.Call, routed.Routes[0], map[string]any{"id": "resp_second"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, found, err := server.store.(jevResponseBindingStore).LoadJevResponseBinding(context.Background(), server.jevResponseKey(routed.Call, "resp_second")); err != nil || !found {
		t.Fatalf("successor binding missing: %v %v", found, err)
	}
	routed.Routes = original
	routed.Routes[1].Resource = &ProviderResource{ID: "different-account"}
	if err := server.applyJevResponsesRouting(context.Background(), &routed, &req, nil); err == nil || AsHTTPError(err).Status != 409 {
		t.Fatalf("continued on different account: %v", err)
	}
}

func TestJevBackgroundResponseContinuation(t *testing.T) {
	server, _, _, _ := jevFixture(t)
	hits := jevHTTPUpstreams(t, server, false)
	server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
		return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
	})
	body := jevHTTPBody(true, false)
	body["background"] = true
	result := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
	if result.Code != 200 {
		t.Fatalf("background submit: %d %s", result.Code, result.Body)
	}
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(result.Body), &envelope); err != nil {
		t.Fatal(err)
	}
	waitForResponseJobStatus(t, server.Handler(), "thk_semantic_test", envelope.ID, "completed")
	body = jevHTTPBody(true, false)
	body["previous_response_id"] = envelope.ID
	continuation := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
	if continuation.Code != 200 || hits.Load() != 2 {
		t.Fatalf("background continuation: %d %s hits=%d", continuation.Code, continuation.Body, hits.Load())
	}
}
