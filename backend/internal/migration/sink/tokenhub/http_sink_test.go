package tokenhub

import (
	"bufio"
	"context"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/migration/bundle"
	"tokenhub/backend/internal/server"
)

func writeTestCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "model-catalog.yaml")
	content := []byte("version: 1\nmodels:\n  - name: seeded-test-model\n    category: test\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func newHTTPMigrationTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	config := server.Config{
		AdminToken:             "test-admin-token",
		BootstrapAdminPassword: "admin123456",
		ModelCatalogFile:       writeTestCatalog(t),
	}
	store := server.NewMemoryStoreWithConfig(config)
	if err := server.SeedDemoDataWithConfig(store, config); err != nil {
		t.Fatalf("seed demo data: %v", err)
	}
	configureMigrationTestSMTP(t, store)
	return httptest.NewServer(server.NewWithConfig(store, config).Handler())
}

func newHTTPMigrationTestServerWithoutSMTP(t *testing.T) *httptest.Server {
	t.Helper()
	config := server.Config{
		AdminToken:             "test-admin-token",
		BootstrapAdminPassword: "admin123456",
		ModelCatalogFile:       writeTestCatalog(t),
	}
	store := server.NewMemoryStoreWithConfig(config)
	if err := server.SeedDemoDataWithConfig(store, config); err != nil {
		t.Fatalf("seed demo data: %v", err)
	}
	return httptest.NewServer(server.NewWithConfig(store, config).Handler())
}

// configureMigrationTestSMTP registers an in-process fake SMTP channel so
// that the user import endpoint, which mails password resets, is usable.
func configureMigrationTestSMTP(t *testing.T, store *server.GormStore) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen smtp: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveMigrationTestSMTP(conn)
		}
	}()
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split smtp addr: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse smtp port: %v", err)
	}
	store.CreateResource("notification-channels", server.AdminResource{
		Name:   "Migration Test SMTP",
		Status: server.StatusActive,
		Fields: map[string]any{
			"type":      "email",
			"smtp_host": host,
			"smtp_port": port,
			"smtp_from": "tokenhub@example.com",
		},
	})
}

func serveMigrationTestSMTP(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	write("220 migration-test ready")
	inData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		text := strings.TrimSpace(line)
		if inData {
			if text == "." {
				inData = false
				write("250 OK")
			}
			continue
		}
		upper := strings.ToUpper(text)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			write("250 migration-test")
		case strings.HasPrefix(upper, "DATA"):
			inData = true
			write("354 end data with <CR><LF>.<CR><LF>")
		case strings.HasPrefix(upper, "QUIT"):
			write("221 bye")
			return
		default:
			write("250 OK")
		}
	}
}

func TestHTTPSinkApplyAndVerifyModelOnly(t *testing.T) {
	ts := newHTTPMigrationTestServer(t)
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	sink := NewHTTPSink(client, bundle.StaticResolver{})
	migrationBundle := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		Models: []bundle.ModelRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "model/http-gpt-4o-mini"},
			Spec:        server.Model{Name: "http-gpt-4o-mini", Family: "gpt-4o", Modality: "text", Status: server.StatusActive},
		}},
	}

	applyResult, err := sink.Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applyResult.Report.Created == 0 {
		t.Fatalf("expected created resources, got %+v", applyResult.Report)
	}

	verifyResult, err := sink.Verify(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !verifyResult.OK {
		t.Fatalf("expected verify success, got %+v", verifyResult)
	}
}

func TestHTTPSinkVerifyDetectsDrift(t *testing.T) {
	ts := newHTTPMigrationTestServer(t)
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	sink := NewHTTPSink(client, bundle.StaticResolver{})
	migrationBundle := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		Models: []bundle.ModelRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "model/http-drift"},
			Spec:        server.Model{Name: "http-drift-model", Family: "gpt-4o", Modality: "text", Status: server.StatusActive},
		}},
	}

	if _, err := sink.Apply(context.Background(), migrationBundle); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := client.UpdateModel(context.Background(), "http-drift-model", server.Model{Name: "http-drift-model", Family: "changed-family", Modality: "text", Status: server.StatusActive}); err != nil {
		t.Fatalf("update drift: %v", err)
	}

	verifyResult, err := sink.Verify(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verifyResult.OK {
		t.Fatal("expected drift verification failure")
	}
	if len(verifyResult.Issues) == 0 {
		t.Fatal("expected verify issues")
	}
	if !strings.Contains(verifyResult.Issues[0].Message, "differs") {
		t.Fatalf("unexpected verify issues: %+v", verifyResult.Issues)
	}
}

// TestHTTPSinkApplyUserCreatesAndUpdates covers the HTTP user path: a new
// user must be created with its team resolved from TeamRef and registered in
// the ref index under the server-assigned ID, and a subsequent apply with a
// changed spec must issue a real update instead of only reporting one.
func TestHTTPSinkApplyUserCreatesAndUpdates(t *testing.T) {
	ts := newHTTPMigrationTestServer(t)
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	sink := NewHTTPSink(client, bundle.StaticResolver{})
	migrationBundle := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		Teams: []bundle.TeamRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "team/eng"},
			ID:          "team-eng",
			Name:        "Engineering",
		}},
		Users: []bundle.UserRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "user/alice"},
			TeamRef:     "team/eng",
			Spec:        server.AdminUser{Username: "alice", Name: "Alice", Email: "alice@example.com", Role: "viewer", Status: server.StatusActive},
		}},
	}

	firstResult, err := sink.Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	users, err := client.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	var created server.AdminUser
	for _, user := range users {
		if user.Email == "alice@example.com" {
			created = user
		}
	}
	if created.ID == "" {
		t.Fatal("expected imported user to exist")
	}
	if created.TeamID != "team-eng" {
		t.Fatalf("expected team resolved from TeamRef, got %q", created.TeamID)
	}
	verifyResult, err := sink.Verify(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("verify imported user: %v", err)
	}
	if !verifyResult.OK {
		t.Fatalf("expected imported user role normalization to verify, got %+v", verifyResult.Issues)
	}

	rollbackResult, err := sink.Rollback(context.Background(), firstResult.Checkpoint)
	if err != nil {
		t.Fatalf("rollback imported user: %v", err)
	}
	// The team was created by this apply too, so rollback removes it as well,
	// and only after the user that referenced it.
	if len(rollbackResult.Changes) != 2 ||
		rollbackResult.Changes[0].Resource != "user" ||
		rollbackResult.Changes[1].Resource != "team" {
		t.Fatalf("expected rollback to delete the imported user then the team, got %+v", rollbackResult.Changes)
	}
	users, err = client.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("list users after rollback: %v", err)
	}
	for _, user := range users {
		if user.Email == "alice@example.com" {
			t.Fatalf("expected imported user to be removed by rollback, got %+v", user)
		}
	}

	// Recreate the user for update and idempotency coverage below.
	if _, err := sink.Apply(context.Background(), migrationBundle); err != nil {
		t.Fatalf("recreate after rollback: %v", err)
	}

	// Second apply with a changed spec must apply the update for real. Reuse
	// the same sink instance: Plan and Apply must both be safe to call on it.
	migrationBundle.Users[0].Spec.Name = "Alice Zhang"
	if _, err := sink.Plan(context.Background(), migrationBundle); err != nil {
		t.Fatalf("plan before second apply: %v", err)
	}
	result, err := sink.Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if result.Report.Updated == 0 {
		t.Fatalf("expected user update to be reported, got %+v", result.Report)
	}
	users, err = client.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("list users after update: %v", err)
	}
	for _, user := range users {
		if user.Email == "alice@example.com" && user.Name != "Alice Zhang" {
			t.Fatalf("expected user name updated on server, got %q", user.Name)
		}
	}

	// Re-applying the unchanged spec must converge to Skip.
	result, err = sink.Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if result.Report.Created != 0 || result.Report.Updated != 0 {
		t.Fatalf("expected unchanged re-apply to skip, got %+v", result.Report)
	}

	// A fresh sink instance (the CLI constructs one per run) must converge to
	// Skip as well, starting from an empty refIndex.
	result, err = NewHTTPSink(client, bundle.StaticResolver{}).Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("fresh sink apply: %v", err)
	}
	if result.Report.Created != 0 || result.Report.Updated != 0 {
		t.Fatalf("expected fresh sink re-apply to skip, got %+v", result.Report)
	}
}

func TestHTTPSinkApplyAPIKeyMinuteLimits(t *testing.T) {
	ts := newHTTPMigrationTestServer(t)
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	sink := NewHTTPSink(client, bundle.StaticResolver{})
	rpm, tpm := int64(60), int64(10_000)
	migrationBundle := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "tokenhub", AdapterVersion: "1.0.0"},
		GeneratedAt:   time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		Projects: []bundle.ProjectRef{{
			ExternalRef: bundle.ExternalRef{System: "tokenhub", ID: "project/limited"},
			Spec:        server.Project{Name: "Limited Project", Status: server.StatusActive},
		}},
		APIKeys: []bundle.APIKeyRef{{
			ExternalRef: bundle.ExternalRef{System: "tokenhub", ID: "key/limited"},
			ProjectRef:  "project/limited",
			Spec: server.APIKey{
				Name: "Limited Key", Status: server.StatusActive,
				RateLimitRPM: &rpm, TokenLimitTPM: &tpm,
			},
		}},
	}

	result, err := sink.Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("create limited API key: %v", err)
	}
	if result.Report.Created != 2 {
		t.Fatalf("expected project and API key creates, got %+v", result.Report)
	}
	assertLimits := func(wantRPM, wantTPM int64) {
		t.Helper()
		keys, err := client.ListAPIKeys(context.Background())
		if err != nil {
			t.Fatalf("list API keys: %v", err)
		}
		for _, key := range keys {
			if key.Name != "Limited Key" {
				continue
			}
			if key.RateLimitRPM == nil || *key.RateLimitRPM != wantRPM ||
				key.TokenLimitTPM == nil || *key.TokenLimitTPM != wantTPM {
				t.Fatalf("API key minute limits = rpm %v, tpm %v; want %d/%d", key.RateLimitRPM, key.TokenLimitTPM, wantRPM, wantTPM)
			}
			return
		}
		t.Fatal("limited API key not found")
	}
	assertLimits(rpm, tpm)

	rpm, tpm = 120, 20_000
	result, err = sink.Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("update limited API key: %v", err)
	}
	if result.Report.Updated != 1 {
		t.Fatalf("expected one API key update, got %+v", result.Report)
	}
	assertLimits(rpm, tpm)

	verify, err := NewHTTPSink(client, bundle.StaticResolver{}).Verify(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("verify limited API key: %v", err)
	}
	if !verify.OK {
		t.Fatalf("expected minute limits to verify, got %+v", verify.Issues)
	}
}

func TestHTTPSinkReturnsCheckpointAfterPartialApplyFailure(t *testing.T) {
	ts := newHTTPMigrationTestServerWithoutSMTP(t)
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	sink := NewHTTPSink(client, bundle.StaticResolver{})
	migrationBundle := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC),
		Providers: []bundle.ProviderRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "provider/partial"},
			Spec:        server.Provider{ID: "provider-partial", Name: "Partial Provider", Type: server.ProviderOpenAICompatible, Status: server.StatusActive, Healthy: true},
		}},
		Users: []bundle.UserRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "user/partial"},
			Spec:        server.AdminUser{Username: "partial", Name: "Partial User", Email: "partial@example.com", Role: "viewer", Status: server.StatusActive},
		}},
	}

	result, err := sink.Apply(context.Background(), migrationBundle)
	if err == nil || !strings.Contains(err.Error(), "email_notification_required") {
		t.Fatalf("expected email prerequisite failure, got result=%+v err=%v", result, err)
	}
	if result == nil {
		t.Fatal("expected a partial apply result with a rollback checkpoint")
	}
	if result.Report.Created != 1 || len(result.Checkpoint.Changes) != 1 {
		t.Fatalf("unexpected partial result: %+v", result)
	}
	if result.Checkpoint.Changes[0].Resource != "provider" || result.Checkpoint.Changes[0].Action != ActionCreate {
		t.Fatalf("expected provider create in partial checkpoint, got %+v", result.Checkpoint.Changes)
	}

	if _, err := sink.Rollback(context.Background(), result.Checkpoint); err != nil {
		t.Fatalf("rollback partial apply: %v", err)
	}
	providers, err := client.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("list providers after rollback: %v", err)
	}
	for _, provider := range providers {
		if provider.Name == "Partial Provider" {
			t.Fatalf("expected partial provider to be rolled back, got %+v", provider)
		}
	}
}

func TestHTTPSinkSeededModelConvergesAfterOneUpdate(t *testing.T) {
	ts := newHTTPMigrationTestServer(t)
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	migrationBundle := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 27, 9, 30, 0, 0, time.UTC),
		Models: []bundle.ModelRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "model/seeded"},
			Spec: server.Model{
				ID:       "source-specific-model-id",
				Name:     "seeded-test-model",
				Category: "llm",
				Family:   "openai",
				Modality: "text",
				Metadata: map[string]string{"mode": "chat"},
				Status:   server.StatusActive,
			},
		}},
	}

	first, err := NewHTTPSink(client, bundle.StaticResolver{}).Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if first.Report.Updated != 1 {
		t.Fatalf("expected one update for the seeded model, got %+v", first.Report)
	}
	verify, err := NewHTTPSink(client, bundle.StaticResolver{}).Verify(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !verify.OK {
		t.Fatalf("expected seeded model to verify after update, got %+v", verify.Issues)
	}
	second, err := NewHTTPSink(client, bundle.StaticResolver{}).Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if second.Report.Created != 0 || second.Report.Updated != 0 {
		t.Fatalf("expected seeded model re-apply to skip, got %+v", second.Report)
	}
}

func TestSameAdminUserEmptyDesiredFieldsMeanKeep(t *testing.T) {
	existing := server.AdminUser{Username: "alice", Name: "Alice", Email: "alice@example.com", Role: "user", TeamID: "team-eng", Status: server.StatusActive}

	// Empty desired fields keep current values on the server (PATCH
	// semantics), so diffing must treat them as equal or apply would report
	// an update forever without ever converging.
	desired := server.AdminUser{Username: "alice", TeamID: "team-eng"}
	if !sameAdminUser(existing, desired) {
		t.Fatal("expected empty desired fields to be treated as keep-current")
	}

	// TeamID is overwritten unconditionally by the server, so an empty
	// desired TeamID means "clear the team" and must not compare equal.
	desired.TeamID = ""
	if sameAdminUser(existing, desired) {
		t.Fatal("expected empty desired TeamID to require an update")
	}

	desired = server.AdminUser{Username: "alice", Name: "Alice Zhang", TeamID: "team-eng"}
	if sameAdminUser(existing, desired) {
		t.Fatal("expected changed non-empty field to require an update")
	}
}

func TestSameAPIKeyUsesResolvedProjectAndIgnoresServerMetadata(t *testing.T) {
	existing := server.APIKey{
		ID:        "key-1",
		ProjectID: "project-1",
		Name:      "migrated-key",
		Group:     "default",
		Allowed:   []string{"gpt-4o-mini"},
		Status:    server.StatusActive,
		Metadata:  map[string]string{"created_by": "usr_admin"},
	}
	desired := server.APIKey{
		ProjectID: "project-1",
		Name:      "migrated-key",
		Allowed:   []string{"gpt-4o-mini"},
		Status:    server.StatusActive,
		Metadata:  map[string]string{"litellm_team_id": "team-red"},
	}
	if !sameAPIKey(existing, desired) {
		t.Fatal("expected resolved project and server-owned metadata to converge")
	}
	desired.ProjectID = ""
	if sameAPIKey(existing, desired) {
		t.Fatal("expected an unresolved project ID to differ")
	}
}

// TestHTTPSinkApplyProviderAndRouteOnCleanTarget exercises the core remote
// migration path against a target with no demo data: provider, resource,
// model and route together. Creating a route requires the upstream model to
// be present in the provider's imported inventory, so this covers the whole
// chain rather than the model-only slice the other tests use.
func TestHTTPSinkApplyProviderAndRouteOnCleanTarget(t *testing.T) {
	config := server.Config{
		AdminToken:             "test-admin-token",
		BootstrapAdminPassword: "admin123456",
		ModelCatalogFile:       writeTestCatalog(t),
	}
	store := server.NewMemoryStoreWithConfig(config)
	if err := server.BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	ts := httptest.NewServer(server.NewWithConfig(store, config).Handler())
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	resolver := bundle.StaticResolver{
		"PROVIDER_API_KEY": "provider-secret",
		"PROVIDER_HEADER":  "provider-header-secret",
		"RESOURCE_HEADER":  "resource-header-secret",
	}
	sink := NewHTTPSink(client, resolver)

	migrationBundle := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Providers: []bundle.ProviderRef{{
			ExternalRef:   bundle.ExternalRef{System: "litellm", ID: "provider/openai"},
			APIKeySecret:  &bundle.SecretRef{Ref: "PROVIDER_API_KEY"},
			HeaderSecrets: map[string]bundle.SecretRef{"X-Tenant": {Ref: "PROVIDER_HEADER"}},
			Spec:          server.Provider{ID: "litellm-provider-openai", Name: "openai-migrated", Type: "openai", Status: server.StatusActive},
		}},
		ProviderResources: []bundle.ProviderResourceRef{{
			ExternalRef:   bundle.ExternalRef{System: "litellm", ID: "resource/openai-main"},
			ProviderRef:   "provider/openai",
			HeaderSecrets: map[string]bundle.SecretRef{"X-Resource-Tenant": {Ref: "RESOURCE_HEADER"}},
			Spec:          server.ProviderResource{ID: "litellm-resource-openai-main", Name: "openai-main", ResourceType: "openai", Status: server.StatusActive},
		}},
		Models: []bundle.ModelRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "model/gpt-4o-mini"},
			Spec:        server.Model{Name: "gpt-4o-mini", Family: "gpt-4o", Modality: "text", Status: server.StatusActive},
		}},
		Routes: []bundle.RouteRef{{
			ExternalRef:         bundle.ExternalRef{System: "litellm", ID: "route/gpt-4o-mini"},
			ModelRef:            "model/gpt-4o-mini",
			ProviderRef:         "provider/openai",
			ProviderResourceRef: "resource/openai-main",
			Spec:                server.ModelRoute{ModelName: "gpt-4o-mini", ProviderModel: "gpt-4o-mini", Status: server.StatusActive},
		}},
	}

	if _, err := sink.Apply(context.Background(), migrationBundle); err != nil {
		t.Fatalf("apply provider+route bundle on a clean target: %v", err)
	}
	provider, ok := store.GetProvider("litellm-provider-openai")
	if !ok || provider.Headers["X-Tenant"] != "provider-header-secret" || len(provider.SensitiveHeaders) != 1 {
		t.Fatalf("sensitive provider header was not resolved through the HTTP sink: %+v", provider)
	}
	resource, ok := store.GetProviderResource("litellm-resource-openai-main")
	if !ok || resource.Headers["X-Resource-Tenant"] != "resource-header-secret" || len(resource.SensitiveHeaders) != 1 {
		t.Fatalf("sensitive resource header was not resolved through the HTTP sink: %+v", resource)
	}

	routes, err := client.ListRoutes(context.Background())
	if err != nil {
		t.Fatalf("list routes: %v", err)
	}
	found := false
	for _, route := range routes {
		if route.ModelName == "gpt-4o-mini" && route.ProviderModel == "gpt-4o-mini" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the migrated route to exist, got %+v", routes)
	}

	verifyResult, err := sink.Verify(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !verifyResult.OK {
		t.Fatalf("expected verification to pass, got %+v", verifyResult.Issues)
	}

	if _, err := store.UpdateProvider("litellm-provider-openai", server.Provider{
		BaseURL: "https://runtime-provider.example/v1", Healthy: false,
	}); err != nil {
		t.Fatalf("set Provider runtime fields: %v", err)
	}
	if _, err := store.UpdateProviderResource("litellm-resource-openai-main", server.ProviderResource{
		BaseURL: "https://runtime-resource.example/v1", Region: "runtime-region", Environment: "runtime-environment",
		Healthy: false, RateLimitRPM: 81, TokenLimitTPM: 82, MaxConcurrency: 83,
	}); err != nil {
		t.Fatalf("set Resource runtime fields: %v", err)
	}
	resolver["PROVIDER_HEADER"] = "rotated-provider-header-secret"
	resolver["RESOURCE_HEADER"] = "rotated-resource-header-secret"
	second, err := sink.Apply(context.Background(), migrationBundle)
	if err != nil {
		t.Fatalf("apply rotated header secrets: %v", err)
	}
	for _, change := range second.Changes {
		if (change.Resource == "provider" || change.Resource == "provider_resource") && change.Action != ActionUpdate {
			t.Fatalf("rotated header secret action for %s = %s, want %s", change.Resource, change.Action, ActionUpdate)
		}
	}
	provider, ok = store.GetProvider("litellm-provider-openai")
	if !ok || provider.Headers["X-Tenant"] != "rotated-provider-header-secret" {
		t.Fatalf("HTTP sink did not rotate Provider header secret: %+v", provider)
	} else if provider.Healthy || provider.BaseURL != "https://runtime-provider.example/v1" {
		t.Fatalf("HTTP sink header rotation overwrote Provider runtime fields: %+v", provider)
	}
	resource, ok = store.GetProviderResource("litellm-resource-openai-main")
	if !ok || resource.Headers["X-Resource-Tenant"] != "rotated-resource-header-secret" {
		t.Fatalf("HTTP sink did not rotate Resource header secret: %+v", resource)
	} else if resource.Healthy || resource.BaseURL != "https://runtime-resource.example/v1" || resource.Region != "runtime-region" || resource.Environment != "runtime-environment" || resource.RateLimitRPM != 81 || resource.TokenLimitTPM != 82 || resource.MaxConcurrency != 83 {
		t.Fatalf("HTTP sink header rotation overwrote Resource runtime fields: %+v", resource)
	}
}

// TestHTTPSinkUpdatesRouteWhenInventoryMissing covers the update half of the
// imported-inventory rule: the target validates it on PATCH as well, so a
// re-apply that has to patch a route must restore the entry rather than fail.
func TestHTTPSinkUpdatesRouteWhenInventoryMissing(t *testing.T) {
	config := server.Config{
		AdminToken:             "test-admin-token",
		BootstrapAdminPassword: "admin123456",
		ModelCatalogFile:       writeTestCatalog(t),
	}
	store := server.NewMemoryStoreWithConfig(config)
	if err := server.BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	ts := httptest.NewServer(server.NewWithConfig(store, config).Handler())
	defer ts.Close()

	client, err := NewAdminAPIClient(ts.URL, "test-admin-token", nil)
	if err != nil {
		t.Fatalf("new admin api client: %v", err)
	}
	sink := NewHTTPSink(client, bundle.StaticResolver{"PROVIDER_API_KEY": "provider-secret"})

	newBundle := func(priority int) *bundle.CanonicalMigrationBundle {
		return &bundle.CanonicalMigrationBundle{
			SchemaVersion: bundle.SchemaVersion,
			Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
			GeneratedAt:   time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
			Providers: []bundle.ProviderRef{{
				ExternalRef:  bundle.ExternalRef{System: "litellm", ID: "provider/openai"},
				APIKeySecret: &bundle.SecretRef{Ref: "PROVIDER_API_KEY"},
				Spec:         server.Provider{ID: "litellm-provider-openai", Name: "openai-migrated", Type: "openai", BaseURL: "https://api.openai.com/v1", Status: server.StatusActive},
			}},
			ProviderResources: []bundle.ProviderResourceRef{{
				ExternalRef: bundle.ExternalRef{System: "litellm", ID: "resource/openai-main"},
				ProviderRef: "provider/openai",
				Spec:        server.ProviderResource{ID: "litellm-resource-openai-main", Name: "openai-main", ResourceType: "openai", BaseURL: "https://api.openai.com/v1", Status: server.StatusActive},
			}},
			Models: []bundle.ModelRef{{
				ExternalRef: bundle.ExternalRef{System: "litellm", ID: "model/gpt-4o-mini"},
				Spec:        server.Model{Name: "gpt-4o-mini", Family: "gpt-4o", Modality: "text", Status: server.StatusActive},
			}},
			Routes: []bundle.RouteRef{{
				ExternalRef:         bundle.ExternalRef{System: "litellm", ID: "route/gpt-4o-mini"},
				ModelRef:            "model/gpt-4o-mini",
				ProviderRef:         "provider/openai",
				ProviderResourceRef: "resource/openai-main",
				Spec:                server.ModelRoute{ModelName: "gpt-4o-mini", ProviderModel: "gpt-4o-mini", Priority: priority, Status: server.StatusActive},
			}},
		}
	}

	if _, err := sink.Apply(context.Background(), newBundle(1)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// Drop the imported inventory the way an operator pruning models would.
	for _, model := range store.ListProviderModels() {
		if err := store.DeleteProviderModel(model.ID); err != nil {
			t.Fatalf("delete provider model: %v", err)
		}
	}

	// The route now exists but its upstream model is no longer imported, so
	// this apply has to patch it and must restore the entry to do so.
	if _, err := sink.Apply(context.Background(), newBundle(7)); err != nil {
		t.Fatalf("re-apply with drifted route: %v", err)
	}

	routes, err := client.ListRoutes(context.Background())
	if err != nil {
		t.Fatalf("list routes: %v", err)
	}
	for _, route := range routes {
		if route.ModelName == "gpt-4o-mini" && route.Priority != 7 {
			t.Fatalf("expected the drifted priority to be applied, got %+v", route)
		}
	}
}
