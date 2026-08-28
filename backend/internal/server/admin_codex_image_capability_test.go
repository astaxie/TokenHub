package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminCodexImageCapabilityTestsOnceAndManagesRoute(t *testing.T) {
	imageBytes := realPNGFixture(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		var request codexSubscriptionImageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode capability request: %v", err)
			return
		}
		if r.URL.Path != "/backend-api/codex/images/generations" || request.Model != codexImageUpstreamModel ||
			request.Quality != "low" || request.Size != "1024x1024" || request.Prompt == "" {
			t.Errorf("unexpected capability request path=%s body=%+v", r.URL.Path, request)
		}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes)}},
		})
	}))
	defer upstream.Close()

	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()
	handler := server.Handler()
	responses := make(chan responseBody, 2)
	requestCapability := func(enabled bool) {
		payload, _ := json.Marshal(map[string]bool{"enabled": enabled})
		req := httptest.NewRequest(http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", bytes.NewReader(payload))
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer dev_admin_token")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		responses <- responseBody{Code: recorder.Code, Body: recorder.Body.String()}
	}

	go requestCapability(true)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capability test did not reach the real upstream")
	}
	go requestCapability(true)
	close(release)
	for range 2 {
		response := <-responses
		if response.Code != http.StatusOK {
			t.Fatalf("enable image capability: status=%d body=%s", response.Code, response.Body)
		}
	}
	if calls := upstreamCalls.Load(); calls != 1 {
		t.Fatalf("concurrent capability configuration sent %d upstream tests, want 1", calls)
	}
	routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID)
	if len(routes) != 1 || routes[0].Status != StatusActive || routes[0].ProviderResourceID != "" {
		t.Fatalf("expected one active provider-level Codex image route, got %+v", routes)
	}
	updated, ok := store.GetProviderResource(resource.ID)
	if !ok || updated.Options[codexImageCapabilityOption] != codexImageCapabilitySupported ||
		updated.Options[codexImageCapabilityCheckedAtOption] == "" {
		t.Fatalf("successful capability was not recorded: %+v", updated.Options)
	}

	disabled := doJSON(t, handler, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": false}, "")
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable image capability: status=%d body=%s", disabled.Code, disabled.Body)
	}
	routes = matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID)
	if len(routes) != 1 || routes[0].Status != StatusDisabled {
		t.Fatalf("disable must retain exactly one disabled route: %+v", routes)
	}
	updated, _ = store.GetProviderResource(resource.ID)
	if updated.Options[codexImageCapabilityOption] != codexImageCapabilitySupported {
		t.Fatalf("disable must preserve the tested capability: %+v", updated.Options)
	}
}

func TestAdminCodexImageCapabilityUsesPluginActionMetadataProfile(t *testing.T) {
	imageBytes := realPNGFixture(t)
	profile := codexImageCapabilityRouteProfile()
	profile.PublicModel = "plugin-codex-public-image"
	profile.UpstreamModel = codexImageUpstreamModel
	profile.CapabilityOption = "plugin_codex_image_capability"
	profile.CapabilityCheckedAtOption = "plugin_codex_image_capability_checked_at"
	profile.RouteBackfillOption = "plugin_codex_image_route_backfill_v1"
	profile.ProbePrompt = "Render a small red triangle on a plain white canvas."
	profile.ProbeBackground = "transparent"
	profile.ProbeQuality = "medium"
	profile.ProbeSize = "512x512"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request codexSubscriptionImageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode capability request: %v", err)
			return
		}
		if request.Model != profile.UpstreamModel {
			t.Errorf("capability request model = %q, want %q", request.Model, profile.UpstreamModel)
		}
		if request.Prompt != profile.ProbePrompt || request.Background != profile.ProbeBackground ||
			request.Quality != profile.ProbeQuality || request.Size != profile.ProbeSize {
			t.Errorf("capability probe request = %+v, want profile prompt/background/quality/size", request)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes)}},
		})
	}))
	defer upstream.Close()

	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()
	result, err := server.configureCodexImageCapability(context.Background(), resource.ID, true, profile)
	if err != nil {
		t.Fatalf("enable metadata-driven image capability: %v", err)
	}
	if result.Capability != profile.CapabilitySupportedValue {
		t.Fatalf("metadata-driven capability result = %+v", result)
	}
	updated, ok := store.GetProviderResource(resource.ID)
	if !ok || updated.Options[profile.CapabilityOption] != profile.CapabilitySupportedValue ||
		updated.Options[profile.CapabilityCheckedAtOption] == "" ||
		updated.Options[profile.RouteBackfillOption] != profile.RouteBackfillValue {
		t.Fatalf("metadata-driven capability was not recorded: %+v", updated.Options)
	}
	if updated.Options[codexImageCapabilityOption] != "" || updated.Options[codexImageCapabilityCheckedAtOption] != "" {
		t.Fatalf("metadata-driven capability wrote Codex fallback keys: %+v", updated.Options)
	}
	routes := store.ListRoutes()
	if len(routes) != 1 || !providerImageCapabilityRouteMatches(routes[0], resource.ProviderID, profile) {
		t.Fatalf("metadata-driven capability route = %+v", routes)
	}
}

func TestAdminCodexImageCapabilityDisablesRouteAfterLastAccountDeleted(t *testing.T) {
	imageBytes := realPNGFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes)}},
		})
	}))
	defer upstream.Close()

	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()
	handler := server.Handler()
	enabled := doJSON(t, handler, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, "")
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable image capability: status=%d body=%s", enabled.Code, enabled.Body)
	}

	deleted := doJSON(t, handler, http.MethodDelete, "/api/admin/provider-resources/"+resource.ID, nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete final Codex account: status=%d body=%s", deleted.Code, deleted.Body)
	}
	routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID)
	if len(routes) != 1 || routes[0].Status != StatusDisabled {
		t.Fatalf("deleting the final Codex account must disable its image route: %+v", routes)
	}
}

func TestAdminCodexImageCapabilitySerializesFinalAccountDeletionWithProbe(t *testing.T) {
	imageBytes := realPNGFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes)}},
		})
	}))
	defer upstream.Close()

	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()
	handler := server.Handler()
	enableDone := make(chan responseBody, 1)
	go func() {
		enableDone <- doJSON(t, handler, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, "")
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capability probe did not reach the upstream")
	}

	deleteStarted := make(chan struct{})
	deleteDone := make(chan responseBody, 1)
	go func() {
		close(deleteStarted)
		deleteDone <- doJSON(t, handler, http.MethodDelete, "/api/admin/provider-resources/"+resource.ID, nil, "")
	}()
	<-deleteStarted
	select {
	case response := <-deleteDone:
		t.Fatalf("final account deletion bypassed the capability lease: status=%d body=%s", response.Code, response.Body)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if response := <-enableDone; response.Code != http.StatusOK {
		t.Fatalf("enable image capability: status=%d body=%s", response.Code, response.Body)
	}
	if response := <-deleteDone; response.Code != http.StatusNoContent {
		t.Fatalf("delete final Codex account: status=%d body=%s", response.Code, response.Body)
	}
	if _, ok := store.GetProviderResource(resource.ID); ok {
		t.Fatal("final Codex account still exists after deletion")
	}
	routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID)
	if len(routes) != 1 || routes[0].Status != StatusDisabled {
		t.Fatalf("serialized deletion left an active or missing Codex image route: %+v", routes)
	}
}

func TestAdminCodexImageCapabilityInvalidatesReplacementCredentials(t *testing.T) {
	imageBytes := realPNGFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes)}},
		})
	}))
	defer upstream.Close()

	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()
	handler := server.Handler()
	if response := doJSON(t, handler, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, ""); response.Code != http.StatusOK {
		t.Fatalf("enable image capability: status=%d body=%s", response.Code, response.Body)
	}

	response := doJSON(t, handler, http.MethodPatch, "/api/admin/provider-resources/"+resource.ID, ProviderResource{
		ProviderID:   resource.ProviderID,
		Name:         resource.Name,
		ResourceType: resource.ResourceType,
		BaseURL:      resource.BaseURL,
		Status:       StatusActive,
		Healthy:      true,
		Weight:       resource.Weight,
		Credentials: &ProviderResourceCredentials{
			AccessToken: "replacement-access", RefreshToken: "replacement-refresh", AccountID: "replacement-account",
		},
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("replace Codex account credentials: status=%d body=%s", response.Code, response.Body)
	}
	updated, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("Codex account disappeared after credential replacement")
	}
	if updated.Options[codexImageCapabilityOption] != "" || updated.Options[codexImageCapabilityCheckedAtOption] != "" || updated.Options[codexImageRouteBackfillOption] != "" {
		t.Fatalf("replacement credentials inherited the previous account capability: %+v", updated.Options)
	}
	credentials := store.providerResourceCredentialsForRuntime(updated)
	if credentials.AccessToken != "replacement-access" || credentials.RefreshToken != "replacement-refresh" || credentials.AccountID != "replacement-account" {
		t.Fatalf("replacement credentials were not stored: %+v", credentials)
	}
	routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID)
	if len(routes) != 1 || routes[0].Status != StatusDisabled {
		t.Fatalf("replacing the only tested account must disable its image route: %+v", routes)
	}
}

func TestAdminCodexImageCapabilityReplacementScopesRouteInvalidation(t *testing.T) {
	store := NewMemoryStore()
	syncBuiltInImageCapabilityProfilesForTest(store)
	provider := store.AddProvider(Provider{ID: "prv_codex_route_scope", Name: "Codex Route Scope", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	newAccount := func(id, group string) ProviderResource {
		resource, err := store.AddProviderResource(ProviderResource{
			ID: id, ProviderID: provider.ID, Name: id, Group: group,
			ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
			Options:     map[string]string{codexImageCapabilityOption: codexImageCapabilitySupported},
			Credentials: &ProviderResourceCredentials{AccessToken: id + "-access", RefreshToken: id + "-refresh", AccountID: id + "-account"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return resource
	}
	primary := newAccount("rsrc_codex_scope_primary", "blue")
	secondary := newAccount("rsrc_codex_scope_secondary", "green")
	pinnedPrimary := store.AddRoute(ModelRoute{ID: "route_codex_scope_pinned_primary", ModelName: codexImageModelName, ProviderID: provider.ID, ProviderResourceID: primary.ID, ProviderModel: codexImageUpstreamModel, Status: StatusActive})
	pinnedSecondary := store.AddRoute(ModelRoute{ID: "route_codex_scope_pinned_secondary", ModelName: codexImageModelName, ProviderID: provider.ID, ProviderResourceID: secondary.ID, ProviderModel: codexImageUpstreamModel, Status: StatusActive})
	groupPrimary := store.AddRoute(ModelRoute{ID: "route_codex_scope_group_primary", ModelName: codexImageModelName, ProviderID: provider.ID, ResourceGroup: "blue", ProviderModel: codexImageUpstreamModel, Status: StatusActive})
	groupSecondary := store.AddRoute(ModelRoute{ID: "route_codex_scope_group_secondary", ModelName: codexImageModelName, ProviderID: provider.ID, ResourceGroup: "green", ProviderModel: codexImageUpstreamModel, Status: StatusActive})
	providerWide := store.AddRoute(ModelRoute{ID: "route_codex_scope_provider", ModelName: codexImageModelName, ProviderID: provider.ID, ProviderModel: codexImageUpstreamModel, Status: StatusActive})
	if _, err := store.UpdateProviderResource(primary.ID, ProviderResource{
		ProviderID: provider.ID, Name: primary.Name, Group: primary.Group, ResourceType: primary.ResourceType,
		BaseURL: primary.BaseURL, Status: StatusActive, Healthy: true, Weight: primary.Weight,
		Credentials: &ProviderResourceCredentials{AccessToken: "replacement-access", RefreshToken: "replacement-refresh", AccountID: "replacement-account"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		route  ModelRoute
		status string
	}{
		{name: "replaced pinned resource", route: pinnedPrimary, status: StatusDisabled},
		{name: "other pinned resource", route: pinnedSecondary, status: StatusActive},
		{name: "replaced resource group", route: groupPrimary, status: StatusDisabled},
		{name: "other resource group", route: groupSecondary, status: StatusActive},
		{name: "provider wide with another account", route: providerWide, status: StatusActive},
	} {
		var actual ModelRoute
		if err := store.db.First(&actual, "id = ?", test.route.ID).Error; err != nil {
			t.Fatal(err)
		}
		if actual.Status != test.status {
			t.Fatalf("%s route status = %q, want %q (other account: %s)", test.name, actual.Status, test.status, secondary.ID)
		}
	}
}

func TestAdminCodexImageCapabilitySerializesCredentialReplacementWithProbe(t *testing.T) {
	imageBytes := realPNGFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes)}},
		})
	}))
	defer upstream.Close()

	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()
	handler := server.Handler()
	enableDone := make(chan responseBody, 1)
	go func() {
		enableDone <- doJSON(t, handler, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, "")
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capability probe did not reach the upstream")
	}

	updateDone := make(chan responseBody, 1)
	go func() {
		updateDone <- doJSON(t, handler, http.MethodPatch, "/api/admin/provider-resources/"+resource.ID, ProviderResource{
			ProviderID: resource.ProviderID, Name: resource.Name, ResourceType: resource.ResourceType, BaseURL: resource.BaseURL,
			Status: StatusActive, Healthy: true, Weight: resource.Weight,
			Credentials: &ProviderResourceCredentials{
				AccessToken: "concurrent-access", RefreshToken: "concurrent-refresh", AccountID: "concurrent-account",
			},
		}, "")
	}()
	select {
	case response := <-updateDone:
		t.Fatalf("credential replacement bypassed the capability lease: status=%d body=%s", response.Code, response.Body)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if response := <-enableDone; response.Code != http.StatusOK {
		t.Fatalf("enable image capability: status=%d body=%s", response.Code, response.Body)
	}
	if response := <-updateDone; response.Code != http.StatusOK {
		t.Fatalf("replace Codex account credentials: status=%d body=%s", response.Code, response.Body)
	}
	updated, _ := store.GetProviderResource(resource.ID)
	if updated.Options[codexImageCapabilityOption] != "" {
		t.Fatalf("stale probe result survived credential replacement: %+v", updated.Options)
	}
	routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID)
	if len(routes) != 1 || routes[0].Status != StatusDisabled {
		t.Fatalf("credential replacement left the stale image route active: %+v", routes)
	}
}

func TestAdminCodexImageCapabilitySerializesProviderDeletionWithProbe(t *testing.T) {
	imageBytes := realPNGFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes)}},
		})
	}))
	defer upstream.Close()

	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()
	handler := server.Handler()
	enableDone := make(chan responseBody, 1)
	go func() {
		enableDone <- doJSON(t, handler, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, "")
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capability probe did not reach the upstream")
	}

	deleteDone := make(chan responseBody, 1)
	go func() {
		deleteDone <- doJSON(t, handler, http.MethodDelete, "/api/admin/providers/"+resource.ProviderID, nil, "")
	}()
	select {
	case response := <-deleteDone:
		t.Fatalf("Provider deletion bypassed the capability lease: status=%d body=%s", response.Code, response.Body)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if response := <-enableDone; response.Code != http.StatusOK {
		t.Fatalf("enable image capability: status=%d body=%s", response.Code, response.Body)
	}
	if response := <-deleteDone; response.Code != http.StatusNoContent {
		t.Fatalf("delete Codex Provider: status=%d body=%s", response.Code, response.Body)
	}
	if _, ok := store.GetProvider(resource.ProviderID); ok {
		t.Fatal("Codex Provider still exists after deletion")
	}
	if routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID); len(routes) != 0 {
		t.Fatalf("Provider deletion left orphan Codex image routes: %+v", routes)
	}
}

func TestAdminCodexImageCapabilityClassifiesUnsupportedWithoutRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{"message": "not available"}})
	}))
	defer upstream.Close()
	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, "")
	if response.Code != http.StatusForbidden || !bytes.Contains([]byte(response.Body), []byte(`"code":"codex_image_forbidden"`)) {
		t.Fatalf("unsupported capability: status=%d body=%s", response.Code, response.Body)
	}
	updated, _ := store.GetProviderResource(resource.ID)
	if updated.Options[codexImageCapabilityOption] != codexImageCapabilityUnsupported ||
		updated.Options[codexImageCapabilityCheckedAtOption] == "" {
		t.Fatalf("unsupported capability was not recorded: %+v", updated.Options)
	}
	if routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID); len(routes) != 0 {
		t.Fatalf("unsupported capability created routes: %+v", routes)
	}
}

func TestAdminCodexImageCapabilityLeavesTransientFailureRetryable(t *testing.T) {
	const upstreamSecret = "sentinel-codex-session-secret"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]any{"message": upstreamSecret}})
	}))
	defer upstream.Close()
	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, "")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("transient capability failure: status=%d body=%s", response.Code, response.Body)
	}
	if strings.Contains(response.Body, upstreamSecret) || !strings.Contains(response.Body, "Codex image capability test is temporarily unavailable") {
		t.Fatalf("transient capability response exposed upstream detail: %s", response.Body)
	}
	updated, _ := store.GetProviderResource(resource.ID)
	if updated.Options[codexImageCapabilityOption] != "" || updated.Options[codexImageCapabilityCheckedAtOption] != "" {
		t.Fatalf("transient failure must not become unsupported: %+v", updated.Options)
	}
	if routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID); len(routes) != 0 {
		t.Fatalf("transient failure created routes: %+v", routes)
	}
}

func TestAdminCodexImageCapabilityRejectsNonImageResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64([]byte("not an image"))}},
		})
	}))
	defer upstream.Close()
	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, "")
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, `"code":"image_result_invalid"`) {
		t.Fatalf("non-image capability result: status=%d body=%s", response.Code, response.Body)
	}
	updated, _ := store.GetProviderResource(resource.ID)
	if updated.Options[codexImageCapabilityOption] != "" || updated.Options[codexImageCapabilityCheckedAtOption] != "" {
		t.Fatalf("non-image result changed capability state: %+v", updated.Options)
	}
	if routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID); len(routes) != 0 {
		t.Fatalf("non-image result created routes: %+v", routes)
	}
}

func TestAdminCodexImageCapabilityRejectsTruncatedImageResult(t *testing.T) {
	imageBytes := realPNGFixture(t)
	if len(imageBytes) < 16 {
		t.Fatal("PNG fixture is too small for a truncated-image test")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes[:16])}},
		})
	}))
	defer upstream.Close()
	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, "")
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, `"code":"image_result_invalid"`) {
		t.Fatalf("truncated image capability result: status=%d body=%s", response.Code, response.Body)
	}
	updated, _ := store.GetProviderResource(resource.ID)
	if updated.Options[codexImageCapabilityOption] != "" || updated.Options[codexImageCapabilityCheckedAtOption] != "" {
		t.Fatalf("truncated image result changed capability state: %+v", updated.Options)
	}
	if routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID); len(routes) != 0 {
		t.Fatalf("truncated image result created routes: %+v", routes)
	}
}

func TestAdminCodexImageCapabilityStateSurvivesOrdinaryAccountEdit(t *testing.T) {
	imageBytes := realPNGFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"b64_json": encodeBase64(imageBytes)}},
		})
	}))
	defer upstream.Close()

	store, server, resource := newCodexImageCapabilityTestServer(t, upstream.URL)
	server.codexSubscription.Client = upstream.Client()
	handler := server.Handler()
	if response := doJSON(t, handler, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, ""); response.Code != http.StatusOK {
		t.Fatalf("enable image capability: status=%d body=%s", response.Code, response.Body)
	}
	if _, err := store.UpdateProviderResourceOptions(resource.ID, map[string]string{
		openAIAccountReauthorizationRequiredOption: "true",
	}); err != nil {
		t.Fatal(err)
	}
	before, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("Codex account disappeared before edit")
	}

	response := doJSON(t, handler, http.MethodPatch, "/api/admin/provider-resources/"+resource.ID, ProviderResource{
		ProviderID:   before.ProviderID,
		Name:         "Edited Codex Image Capability Account",
		ResourceType: before.ResourceType,
		BaseURL:      before.BaseURL,
		Status:       before.Status,
		Healthy:      before.Healthy,
		Priority:     before.Priority,
		Weight:       before.Weight,
		Options:      map[string]string{"auth_type": "oauth", "operator_note": "retained"},
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("edit Codex account: status=%d body=%s", response.Code, response.Body)
	}
	after, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("Codex account disappeared after edit")
	}
	for key, want := range map[string]string{
		codexImageCapabilityOption:                 codexImageCapabilitySupported,
		codexImageCapabilityCheckedAtOption:        before.Options[codexImageCapabilityCheckedAtOption],
		codexImageRouteBackfillOption:              codexImageRouteBackfillCompleted,
		"has_refresh_token":                        "true",
		openAIAccountReauthorizationRequiredOption: "true",
		"operator_note":                            "retained",
	} {
		if after.Options[key] != want {
			t.Fatalf("ordinary edit changed protected option %s: got %q want %q; options=%+v", key, after.Options[key], want, after.Options)
		}
	}
	if route := activeCodexImageRoute(store.ListRoutes(), resource.ProviderID); route == nil {
		t.Fatal("ordinary account edit disabled the active Codex image route")
	}
}

func TestAdminCodexImageCapabilityRequiresReauthorizationWithoutRoute(t *testing.T) {
	store, server, resource := newCodexImageCapabilityTestServer(t, "http://127.0.0.1:1")
	server.codexSubscription.RefreshCredentials = func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		return ProviderResourceCredentials{}, NewHTTPError(http.StatusUnauthorized, "provider_resource_reauthorization_required", "OpenAI account session ended; reauthorization is required")
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]bool{"enabled": true}, "")
	if response.Code != http.StatusUnauthorized || !bytes.Contains([]byte(response.Body), []byte(`"code":"provider_resource_reauthorization_required"`)) {
		t.Fatalf("reauthorization failure: status=%d body=%s", response.Code, response.Body)
	}
	updated, _ := store.GetProviderResource(resource.ID)
	if updated.Options[codexImageCapabilityOption] != "" {
		t.Fatalf("reauthorization failure changed capability: %+v", updated.Options)
	}
	if routes := matchingCodexImageRoutes(store.ListRoutes(), resource.ProviderID); len(routes) != 0 {
		t.Fatalf("reauthorization failure created routes: %+v", routes)
	}
}

func TestCodexImageRouteBackfillPreservesDisabledRoute(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_codex_backfill", Name: "Codex Backfill", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_codex_backfill", ProviderID: provider.ID, Name: "Codex Backfill Account",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Options: map[string]string{codexImageCapabilityOption: codexImageCapabilitySupported},
	}); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "codex-image-backfill-secret"})
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	routes := matchingCodexImageRoutes(store.ListRoutes(), provider.ID)
	if len(routes) != 1 || routes[0].Status != StatusActive {
		t.Fatalf("supported account was not backfilled: %+v", routes)
	}
	routes[0].Status = StatusDisabled
	if _, err := store.UpdateRoute(routes[0].ID, routes[0]); err != nil {
		t.Fatal(err)
	}
	restarted := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "codex-image-backfill-secret"})
	if err := restarted.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	routes = matchingCodexImageRoutes(store.ListRoutes(), provider.ID)
	if len(routes) != 1 || routes[0].Status != StatusDisabled {
		t.Fatalf("backfill re-enabled or duplicated an explicitly disabled route: %+v", routes)
	}
	resource, _ := store.GetProviderResource("rsrc_codex_backfill")
	if resource.Options[codexImageRouteBackfillOption] != codexImageRouteBackfillCompleted {
		t.Fatalf("backfill completion was not recorded: %+v", resource.Options)
	}
	if err := store.DeleteRoute(routes[0].ID); err != nil {
		t.Fatal(err)
	}
	afterDeletion := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "codex-image-backfill-secret"})
	if err := afterDeletion.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if routes := matchingCodexImageRoutes(store.ListRoutes(), provider.ID); len(routes) != 0 {
		t.Fatalf("one-time backfill recreated an explicitly deleted route: %+v", routes)
	}
}

func TestProviderImageRouteBackfillUsesPluginProfiles(t *testing.T) {
	store := NewMemoryStore()
	profile := providerImageCapabilityRouteProfile{
		ProviderType:        "kimi_subscription",
		ResourceType:        "kimi_subscription_account",
		PublicModel:         "kimi-image",
		UpstreamModel:       "moonshot-image",
		CapabilityOption:    "kimi_image_capability",
		RouteBackfillOption: "kimi_image_route_backfill_v1",
	}
	profile.withDefaults()
	store.setProviderImageCapabilityRouteProfiles([]providerImageCapabilityRouteProfile{profile})
	provider := store.AddProvider(Provider{
		ID: "prv_kimi_image_backfill", Name: "Kimi Image Backfill", Type: profile.ProviderType, Status: StatusActive, Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_kimi_image_backfill", ProviderID: provider.ID, Name: "Kimi Image Account",
		ResourceType: profile.ResourceType, Status: StatusActive, Healthy: true,
		Options: map[string]string{profile.CapabilityOption: profile.CapabilitySupportedValue},
	}); err != nil {
		t.Fatal(err)
	}

	backfillProviderImageCapabilityRoutes(store)

	var matches []ModelRoute
	for _, route := range store.ListRoutes() {
		if providerImageCapabilityRouteMatches(route, provider.ID, profile) {
			matches = append(matches, route)
		}
	}
	if len(matches) != 1 || matches[0].Status != StatusActive {
		t.Fatalf("plugin image route was not backfilled: %+v", matches)
	}
	resource, _ := store.GetProviderResource("rsrc_kimi_image_backfill")
	if resource.Options[profile.RouteBackfillOption] != profile.RouteBackfillValue {
		t.Fatalf("plugin backfill completion was not recorded: %+v", resource.Options)
	}
}

func TestProviderImageRouteProfilesComeFromPluginSync(t *testing.T) {
	store := NewMemoryStore()
	if profiles := store.providerImageCapabilityRouteProfiles(); len(profiles) != 0 {
		t.Fatalf("store image profiles = %+v, want none before plugin sync", profiles)
	}

	syncBuiltInImageCapabilityProfilesForTest(store)
	profiles := store.providerImageCapabilityRouteProfiles()
	if len(profiles) != 1 || profiles[0].ProviderType != ProviderOpenAICodex || profiles[0].PublicModel != codexImageModelName {
		t.Fatalf("store image profiles = %+v, want Codex profile from plugin action metadata", profiles)
	}
	if profiles[0].ProbePrompt == "" || profiles[0].ProbeQuality != "low" || profiles[0].ProbeTimeoutErrorCode != "codex_upstream_timeout" {
		t.Fatalf("store image profile missing Codex probe metadata: %+v", profiles[0])
	}
}

func TestOpenAICodexImageCapabilityActionExposesErrorMetadata(t *testing.T) {
	descriptor := openAICodexImageCapabilityActionDescriptor()
	for _, code := range []string{
		"codex_image_forbidden",
		"codex_rate_limited",
		"codex_upstream_unavailable",
		"codex_upstream_timeout",
		"codex_image_request_failed",
		"codex_image_response_failed",
		"codex_quota_exhausted",
		"provider_resource_reauthorization_required",
		"image_result_missing",
		"image_result_invalid",
	} {
		if descriptor.Metadata["error_message."+code] == "" {
			t.Fatalf("descriptor missing error message metadata for %s", code)
		}
	}
	for _, code := range []string{
		"codex_image_forbidden",
		"codex_rate_limited",
		"codex_upstream_unavailable",
		"codex_upstream_timeout",
		"codex_quota_exhausted",
		"provider_resource_reauthorization_required",
	} {
		if descriptor.Metadata["probe_error_message."+code] == "" {
			t.Fatalf("descriptor missing probe error message metadata for %s", code)
		}
	}
	for _, key := range []string{
		"probe_request.prompt",
		"probe_request.background",
		"probe_request.quality",
		"probe_request.size",
		"probe_error.timeout.code",
		"probe_error.timeout.message",
	} {
		if descriptor.Metadata[key] == "" {
			t.Fatalf("descriptor missing probe request metadata for %s", key)
		}
	}
}

func syncBuiltInImageCapabilityProfilesForTest(store *GormStore) {
	store.setProviderImageCapabilityRouteProfiles(providerImageCapabilityRouteProfilesFromActions([]pluginmeta.ActionDescriptor{
		openAICodexImageCapabilityActionDescriptor(),
	}))
}

func newCodexImageCapabilityTestServer(t *testing.T, baseURL string) (*GormStore, *Server, ProviderResource) {
	t.Helper()
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_codex_image_capability", Name: "Codex Image Capability", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_image_capability",
		ProviderID:   provider.ID,
		Name:         "Codex Image Capability Account",
		ResourceType: ProviderResourceOpenAISubscription,
		BaseURL:      baseURL + "/backend-api/codex",
		Status:       StatusActive,
		Healthy:      true,
		Credentials: &ProviderResourceCredentials{
			AccessToken:  "access_capability_test",
			RefreshToken: "refresh_capability_test",
			AccountID:    "account_capability_test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "codex-image-capability-secret"})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return store, server, resource
}

func matchingCodexImageRoutes(routes []ModelRoute, providerID string) []ModelRoute {
	var matches []ModelRoute
	for _, route := range routes {
		if codexImageRouteMatches(route, providerID) {
			matches = append(matches, route)
		}
	}
	return matches
}
