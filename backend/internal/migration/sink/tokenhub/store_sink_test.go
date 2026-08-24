package tokenhub

import (
	"slices"
	"testing"
	"time"

	"tokenhub/backend/internal/migration/bundle"
	"tokenhub/backend/internal/server"
)

func TestStoreSinkApplyRefreshesSecretBackedHeadersWithoutDuplicates(t *testing.T) {
	store := server.NewMemoryStore()
	resolver := bundle.StaticResolver{
		"PROVIDER_API_KEY": "provider-secret",
		"RESOURCE_API_KEY": "resource-secret",
		"PROVIDER_HEADER":  "provider-header-secret",
		"RESOURCE_HEADER":  "resource-header-secret",
		"CLIENT_API_KEY":   "client-secret",
	}
	sink := NewStoreSink(store, resolver)

	migrationBundle := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source: bundle.Source{
			Adapter:        "litellm",
			AdapterVersion: "1.60.0",
		},
		GeneratedAt: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
		Providers: []bundle.ProviderRef{{
			ExternalRef:   bundle.ExternalRef{System: "litellm", ID: "provider/openai"},
			Spec:          server.Provider{ID: "prv_litellm_openai", Name: "OpenAI", Type: server.ProviderOpenAICompatible, Status: server.StatusActive, Healthy: true},
			APIKeySecret:  &bundle.SecretRef{Ref: "PROVIDER_API_KEY"},
			HeaderSecrets: map[string]bundle.SecretRef{"X-Provider-Secret": {Ref: "PROVIDER_HEADER"}},
		}},
		ProviderResources: []bundle.ProviderResourceRef{{
			ExternalRef:   bundle.ExternalRef{System: "litellm", ID: "resource/openai/default"},
			ProviderRef:   "provider/openai",
			Spec:          server.ProviderResource{ID: "rsrc_litellm_openai_default", Name: "OpenAI Default", ResourceType: "openai", Status: server.StatusActive, Healthy: true, Weight: 100},
			APIKeySecret:  &bundle.SecretRef{Ref: "RESOURCE_API_KEY"},
			HeaderSecrets: map[string]bundle.SecretRef{"X-Resource-Secret": {Ref: "RESOURCE_HEADER"}},
		}},
		Models: []bundle.ModelRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "model/gpt-4o-mini"},
			Spec:        server.Model{Name: "gpt-4o-mini", Family: "gpt-4o", Modality: "text", Status: server.StatusActive},
		}},
		Routes: []bundle.RouteRef{{
			ExternalRef:         bundle.ExternalRef{System: "litellm", ID: "route/gpt-4o-mini/openai"},
			ModelRef:            "model/gpt-4o-mini",
			ProviderRef:         "provider/openai",
			ProviderResourceRef: "resource/openai/default",
			Spec:                server.ModelRoute{ID: "route_litellm_openai_gpt4omini", ProviderModel: "gpt-4o-mini", Status: server.StatusActive, Weight: 100, Priority: 1, Tags: []string{"internal", "compliant"}},
		}},
		Users: []bundle.UserRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "user/admin"},
			Spec:        server.AdminUser{Username: "admin", Name: "Admin", Email: "admin@example.com", Role: "owner", Status: server.StatusActive},
		}},
		Projects: []bundle.ProjectRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "project/default"},
			Spec: server.Project{
				Name: "Default Project", Status: server.StatusActive, OwnerUserID: "user/admin",
				ModelAccessMode: server.ModelAccessModeRestricted, AllowedModels: []string{"gpt-4o-mini"},
			},
		}},
		APIKeys: []bundle.APIKeyRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "key/default"},
			ProjectRef:  "project/default",
			Spec:        server.APIKey{Name: "Default Key", Status: server.StatusActive, ModelAccessMode: server.ModelAccessModeRestricted, Allowed: []string{}},
			KeySecret:   &bundle.SecretRef{Ref: "CLIENT_API_KEY"},
		}},
	}

	first, err := sink.Apply(migrationBundle)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if len(first.Changes) != 7 {
		t.Fatalf("unexpected change count on first apply: %d", len(first.Changes))
	}
	for _, change := range first.Changes {
		if change.Action != ActionCreate {
			t.Fatalf("expected first apply to create all resources, got %s for %s", change.Action, change.Resource)
		}
	}
	if len(store.ListProviders()) != 1 || len(store.ListProviderResources()) != 1 || len(store.ListModels()) != 1 || len(store.ListRoutes()) != 1 || len(store.ListAdminUsers()) != 1 || len(store.ListProjects()) != 1 || len(store.ListAPIKeys()) != 1 {
		t.Fatal("expected all resources to be created once")
	}
	if provider, ok := store.GetProvider("prv_litellm_openai"); !ok || provider.Headers["X-Provider-Secret"] != "provider-header-secret" {
		t.Fatalf("provider sensitive header was not encrypted and recoverable: %+v", provider)
	}
	if resource, ok := store.GetProviderResource("rsrc_litellm_openai_default"); !ok || resource.Headers["X-Resource-Secret"] != "resource-header-secret" {
		t.Fatalf("resource sensitive header was not encrypted and recoverable: %+v", resource)
	}
	if project := store.ListProjects()[0]; project.ModelAccessMode != server.ModelAccessModeRestricted || !slices.Equal(project.AllowedModels, []string{"gpt-4o-mini"}) {
		t.Fatalf("project model access was not migrated: %+v", project)
	}
	if key := store.ListAPIKeys()[0]; key.ModelAccessMode != server.ModelAccessModeRestricted || len(key.Allowed) != 0 {
		t.Fatalf("restricted-empty API key access was not migrated: %+v", key)
	}
	if route := store.ListRoutes()[0]; !slices.Equal(route.Tags, []string{"internal", "compliant"}) {
		t.Fatalf("route tags were not migrated: %+v", route)
	}
	verifyFirst, err := sink.Verify(migrationBundle)
	if err != nil {
		t.Fatalf("verify after first apply: %v", err)
	}
	if !verifyFirst.OK {
		t.Fatalf("expected verify success after first apply, got %+v", verifyFirst)
	}

	second, err := sink.Apply(migrationBundle)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(store.ListProviders()) != 1 || len(store.ListProviderResources()) != 1 || len(store.ListModels()) != 1 || len(store.ListRoutes()) != 1 || len(store.ListAdminUsers()) != 1 || len(store.ListProjects()) != 1 || len(store.ListAPIKeys()) != 1 {
		t.Fatal("expected apply to be idempotent")
	}
	for _, change := range second.Changes {
		want := ActionSkip
		if change.Resource == "provider" || change.Resource == "provider_resource" {
			want = ActionUpdate
		}
		if change.Action != want {
			t.Fatalf("second apply action for %s = %s, want %s", change.Resource, change.Action, want)
		}
	}
	if got := first.NewKeys["key/default"]; got != "client-secret" {
		t.Fatalf("expected resolved key secret to be recorded, got %q", got)
	}
	if len(second.NewKeys) != 0 {
		t.Fatalf("expected second apply to emit no new keys, got %+v", second.NewKeys)
	}

	if _, err := store.UpdateProvider("prv_litellm_openai", server.Provider{
		BaseURL: "https://runtime-provider.example/v1", Healthy: false,
	}); err != nil {
		t.Fatalf("set Provider runtime fields: %v", err)
	}
	if _, err := store.UpdateProviderResource("rsrc_litellm_openai_default", server.ProviderResource{
		BaseURL: "https://runtime-resource.example/v1", Region: "runtime-region", Environment: "runtime-environment",
		Healthy: false, RateLimitRPM: 91, TokenLimitTPM: 92, MaxConcurrency: 93,
	}); err != nil {
		t.Fatalf("set Resource runtime fields: %v", err)
	}
	resolver["PROVIDER_HEADER"] = "rotated-provider-header-secret"
	resolver["RESOURCE_HEADER"] = "rotated-resource-header-secret"
	third, err := sink.Apply(migrationBundle)
	if err != nil {
		t.Fatalf("apply rotated header secrets: %v", err)
	}
	for _, change := range third.Changes {
		if (change.Resource == "provider" || change.Resource == "provider_resource") && change.Action != ActionUpdate {
			t.Fatalf("rotated header secret action for %s = %s, want %s", change.Resource, change.Action, ActionUpdate)
		}
	}
	if provider, ok := store.GetProvider("prv_litellm_openai"); !ok || provider.Headers["X-Provider-Secret"] != "rotated-provider-header-secret" {
		t.Fatalf("provider sensitive header did not rotate: %+v", provider)
	} else if provider.Healthy || provider.BaseURL != "https://runtime-provider.example/v1" {
		t.Fatalf("provider header rotation overwrote runtime fields: %+v", provider)
	}
	if resource, ok := store.GetProviderResource("rsrc_litellm_openai_default"); !ok || resource.Headers["X-Resource-Secret"] != "rotated-resource-header-secret" {
		t.Fatalf("resource sensitive header did not rotate: %+v", resource)
	} else if resource.Healthy || resource.BaseURL != "https://runtime-resource.example/v1" || resource.Region != "runtime-region" || resource.Environment != "runtime-environment" || resource.RateLimitRPM != 91 || resource.TokenLimitTPM != 92 || resource.MaxConcurrency != 93 {
		t.Fatalf("resource header rotation overwrote runtime fields: %+v", resource)
	}
}

func TestStoreSinkApplyRejectsSecretBackedAPIKeyUpdate(t *testing.T) {
	store := server.NewMemoryStore()
	sink := NewStoreSink(store, bundle.StaticResolver{"CLIENT_API_KEY": "client-secret"})

	first := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC),
		Users: []bundle.UserRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "user/admin"},
			Spec:        server.AdminUser{Username: "admin", Name: "Admin", Email: "admin@example.com", Role: "owner", Status: server.StatusActive},
		}},
		Projects: []bundle.ProjectRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "project/default"},
			Spec:        server.Project{Name: "Default Project", Status: server.StatusActive, OwnerUserID: "user/admin"},
		}},
		APIKeys: []bundle.APIKeyRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "key/default"},
			ProjectRef:  "project/default",
			Spec:        server.APIKey{Name: "Default Key", Status: server.StatusActive},
			KeySecret:   &bundle.SecretRef{Ref: "CLIENT_API_KEY"},
		}},
	}
	if _, err := sink.Apply(first); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	second := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 24, 10, 5, 0, 0, time.UTC),
		Users:         first.Users,
		Projects:      first.Projects,
		APIKeys: []bundle.APIKeyRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "key/default"},
			ProjectRef:  "project/default",
			Spec: server.APIKey{
				Name:   "Default Key",
				Status: server.StatusDisabled,
			},
			KeySecret: &bundle.SecretRef{Ref: "CLIENT_API_KEY"},
		}},
	}
	if _, err := sink.Apply(second); err == nil {
		t.Fatal("expected secret-backed api key update to be rejected")
	}
}

func TestStoreSinkRejectsProjectAllowlistWithUnknownModel(t *testing.T) {
	store := server.NewMemoryStore()
	sink := NewStoreSink(store, bundle.StaticResolver{})
	b := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "tokenhub", AdapterVersion: "1.1.0"},
		GeneratedAt:   time.Date(2026, 8, 2, 1, 0, 0, 0, time.UTC),
		Projects: []bundle.ProjectRef{{
			ExternalRef: bundle.ExternalRef{System: "tokenhub", ID: "project/invalid-model"},
			Spec: server.Project{
				Name: "Invalid Model Project", Status: server.StatusActive,
				ModelAccessMode: server.ModelAccessModeRestricted, AllowedModels: []string{"missing-model"},
			},
		}},
	}

	if _, err := sink.Apply(b); err == nil {
		t.Fatal("expected unknown project allowlist model to fail migration")
	}
	if len(store.ListProjects()) != 0 {
		t.Fatalf("failed project migration must not persist a project: %+v", store.ListProjects())
	}
}

func TestStoreSinkApplyResolvesTeamAndOwnerReferences(t *testing.T) {
	store := server.NewMemoryStore()
	sink := NewStoreSink(store, bundle.StaticResolver{})

	b := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
		Teams: []bundle.TeamRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "team/core"},
			ID:          "team-core",
			Name:        "Core",
		}},
		Users: []bundle.UserRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "user/admin"},
			TeamRef:     "team/core",
			Spec: server.AdminUser{
				Username: "admin",
				Name:     "Admin",
				Email:    "admin@example.com",
				Role:     "owner",
				Status:   server.StatusActive,
			},
		}},
		Projects: []bundle.ProjectRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "project/default"},
			TeamRef:     "team/core",
			Spec: server.Project{
				Name:        "Default Project",
				Status:      server.StatusActive,
				OwnerUserID: "user/admin",
			},
		}},
	}

	if _, err := sink.Apply(b); err != nil {
		t.Fatalf("apply bundle: %v", err)
	}
	users := store.ListAdminUsers()
	if len(users) != 1 || users[0].TeamID != "team-core" {
		t.Fatalf("expected user team ref to resolve, got %+v", users)
	}
	projects := store.ListProjects()
	if len(projects) != 1 || projects[0].TeamID != "team-core" || projects[0].OwnerUserID != users[0].ID {
		t.Fatalf("expected project refs to resolve, got %+v user=%+v", projects, users[0])
	}
}

func TestStoreSinkVerifyAndRollback(t *testing.T) {
	store := server.NewMemoryStore()
	sink := NewStoreSink(store, bundle.StaticResolver{"CLIENT_API_KEY": "client-secret"})

	b := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		Users: []bundle.UserRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "user/admin"},
			Spec:        server.AdminUser{Username: "admin", Name: "Admin", Email: "admin@example.com", Role: "owner", Status: server.StatusActive},
		}},
		Projects: []bundle.ProjectRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "project/default"},
			Spec:        server.Project{Name: "Default Project", Status: server.StatusActive, OwnerUserID: "user/admin"},
		}},
		APIKeys: []bundle.APIKeyRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "key/default"},
			ProjectRef:  "project/default",
			Spec:        server.APIKey{Name: "Default Key", Status: server.StatusActive},
			KeySecret:   &bundle.SecretRef{Ref: "CLIENT_API_KEY"},
		}},
	}

	applyResult, err := sink.Apply(b)
	if err != nil {
		t.Fatalf("apply bundle: %v", err)
	}
	verifyResult, err := sink.Verify(b)
	if err != nil {
		t.Fatalf("verify bundle: %v", err)
	}
	if !verifyResult.OK || len(verifyResult.Issues) != 0 {
		t.Fatalf("expected verify success, got %+v", verifyResult)
	}

	rollbackResult, err := sink.Rollback(applyResult.Checkpoint)
	if err != nil {
		t.Fatalf("rollback bundle: %v", err)
	}
	if len(rollbackResult.Changes) != 3 {
		t.Fatalf("unexpected rollback change count: %d", len(rollbackResult.Changes))
	}
	for _, change := range rollbackResult.Changes {
		if change.Action != ActionDelete {
			t.Fatalf("expected rollback to delete created resources, got %s for %s", change.Action, change.Resource)
		}
	}
	if len(store.ListAdminUsers()) != 0 || len(store.ListProjects()) != 0 || len(store.ListAPIKeys()) != 0 {
		t.Fatal("expected created resources to be deleted by rollback")
	}

	postRollbackVerify, err := sink.Verify(b)
	if err != nil {
		t.Fatalf("verify after rollback: %v", err)
	}
	if postRollbackVerify.OK || len(postRollbackVerify.Issues) == 0 {
		t.Fatalf("expected verify to fail after rollback, got %+v", postRollbackVerify)
	}
}

func TestStoreSinkVerifyWorksWithFreshSinkInstance(t *testing.T) {
	store := server.NewMemoryStore()
	applySink := NewStoreSink(store, bundle.StaticResolver{"CLIENT_API_KEY": "client-secret"})

	b := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		Providers: []bundle.ProviderRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "provider/openai"},
			Spec:        server.Provider{Name: "OpenAI", Type: server.ProviderOpenAICompatible, Status: server.StatusActive, Healthy: true},
		}},
		ProviderResources: []bundle.ProviderResourceRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "resource/openai/default"},
			ProviderRef: "provider/openai",
			Spec:        server.ProviderResource{Name: "OpenAI Default", ResourceType: "openai", Status: server.StatusActive, Healthy: true, Weight: 100},
		}},
		Models: []bundle.ModelRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "model/gpt-4o-mini"},
			Spec:        server.Model{Name: "gpt-4o-mini", Family: "gpt-4o", Modality: "text", Status: server.StatusActive},
		}},
		Routes: []bundle.RouteRef{{
			ExternalRef:         bundle.ExternalRef{System: "litellm", ID: "route/gpt-4o-mini/openai"},
			ModelRef:            "model/gpt-4o-mini",
			ProviderRef:         "provider/openai",
			ProviderResourceRef: "resource/openai/default",
			Spec:                server.ModelRoute{ProviderModel: "gpt-4o-mini", Status: server.StatusActive, Weight: 100, Priority: 1},
		}},
	}

	if _, err := applySink.Apply(b); err != nil {
		t.Fatalf("apply bundle: %v", err)
	}
	freshSink := NewStoreSink(store, bundle.StaticResolver{})
	verifyResult, err := freshSink.Verify(b)
	if err != nil {
		t.Fatalf("verify bundle on fresh sink: %v", err)
	}
	if !verifyResult.OK {
		t.Fatalf("expected fresh sink verify to succeed, got %+v", verifyResult)
	}
}

func TestStoreSinkPlanResolvesExistingProviderForResources(t *testing.T) {
	store := server.NewMemoryStore()
	applySink := NewStoreSink(store, bundle.StaticResolver{})

	b := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC),
		Providers: []bundle.ProviderRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "provider/openai"},
			Spec:        server.Provider{Name: "OpenAI", Type: server.ProviderOpenAICompatible, Status: server.StatusActive, Healthy: true},
		}},
		ProviderResources: []bundle.ProviderResourceRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "resource/openai/default"},
			ProviderRef: "provider/openai",
			Spec:        server.ProviderResource{Name: "OpenAI Default", ResourceType: "openai", Status: server.StatusActive, Healthy: true, Weight: 100},
		}},
	}

	if _, err := applySink.Apply(b); err != nil {
		t.Fatalf("apply bundle: %v", err)
	}
	// Plan against the populated store must resolve the resource under the
	// existing provider instead of reporting a spurious create.
	report, err := NewStoreSink(store, bundle.StaticResolver{}).Plan(b)
	if err != nil {
		t.Fatalf("plan bundle: %v", err)
	}
	if report.Created != 0 || report.Updated != 2 {
		t.Fatalf("expected provider and resource to plan as updates, got %+v", report)
	}
}

func TestStoreSinkApplyRejectsQuotaPolicies(t *testing.T) {
	store := server.NewMemoryStore()
	sink := NewStoreSink(store, bundle.StaticResolver{})

	b := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		QuotaPolicies: []bundle.QuotaPolicyRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "quota/default"},
			Name:        "default",
		}},
	}

	if _, err := sink.Apply(b); err == nil {
		t.Fatal("expected quota policies to be rejected")
	}
}

// TestStoreSinkApplyPreservesExtraTeamMemberships guards against silent data
// loss: the bundle carries a single team ref, but the store rewrites a user's
// full team list from every update patch. Applying a drifted user must not
// drop the team memberships the migration does not own.
func TestStoreSinkApplyPreservesExtraTeamMemberships(t *testing.T) {
	store := server.NewMemoryStore()
	sink := NewStoreSink(store, bundle.StaticResolver{})

	for _, id := range []string{"team-core", "team-extra"} {
		store.CreateResource("teams", server.AdminResource{ID: id, Name: id, Status: server.StatusActive})
	}
	existing, err := store.CreateAdminUser(server.AdminUser{
		Username: "alice",
		Name:     "Alice",
		Email:    "alice@example.com",
		Role:     "user",
		Status:   server.StatusActive,
		TeamID:   "team-core",
		TeamIDs:  []string{"team-core", "team-extra"},
	}, "placeholder-password")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	b := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC),
		Teams: []bundle.TeamRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "team/core"},
			ID:          "team-core",
			Name:        "Core",
		}},
		Users: []bundle.UserRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "user/alice"},
			TeamRef:     "team/core",
			Spec: server.AdminUser{
				Username: "alice",
				// Drifted display name forces an update of the existing user.
				Name:   "Alice Liddell",
				Email:  "alice@example.com",
				Role:   "user",
				Status: server.StatusActive,
			},
		}},
	}

	if _, err := sink.Apply(b); err != nil {
		t.Fatalf("apply bundle: %v", err)
	}

	var updated server.AdminUser
	for _, user := range store.ListAdminUsers() {
		if user.ID == existing.ID {
			updated = user
		}
	}
	if updated.Name != "Alice Liddell" {
		t.Fatalf("expected the drifted user to be updated, got %+v", updated)
	}
	if !slices.Contains(updated.TeamIDs, "team-extra") {
		t.Fatalf("expected extra team membership to survive the update, got %+v", updated.TeamIDs)
	}
}

// TestStoreSinkVerifyReportsMissingTeam guards Plan/Apply/Verify agreement:
// a bundle whose team is absent from the target is drift, not a converged
// state, so verification must fail instead of silently passing.
func TestStoreSinkVerifyReportsMissingTeam(t *testing.T) {
	store := server.NewMemoryStore()
	sink := NewStoreSink(store, bundle.StaticResolver{})

	b := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source:        bundle.Source{Adapter: "litellm", AdapterVersion: "1.60.0"},
		GeneratedAt:   time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
		Teams: []bundle.TeamRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "team/core"},
			ID:          "team-core",
			Name:        "Core",
		}},
	}

	result, err := sink.Verify(b)
	if err != nil {
		t.Fatalf("verify bundle: %v", err)
	}
	if result.OK {
		t.Fatal("expected verification to fail for a team that is not on the target")
	}
	if len(result.Issues) != 1 || result.Issues[0].Resource != "team" || result.Issues[0].Ref != "team/core" {
		t.Fatalf("expected a team issue, got %+v", result.Issues)
	}

	if _, err := sink.Apply(b); err != nil {
		t.Fatalf("apply bundle: %v", err)
	}
	after, err := sink.Verify(b)
	if err != nil {
		t.Fatalf("verify after apply: %v", err)
	}
	if !after.OK {
		t.Fatalf("expected verification to pass after apply, got %+v", after.Issues)
	}
}

// TestSameModelIgnoresTargetOwnedMetadata pins the convergence rule that a
// real migration exposed: the target stamps its own metadata keys onto a
// migrated model, so comparing the maps for equality reported drift forever
// and made every re-apply an update that changed nothing.
func TestSameModelIgnoresTargetOwnedMetadata(t *testing.T) {
	existing := server.Model{
		Name: "gpt-4o-mini", Family: "openai", Modality: "text", Status: server.StatusActive,
		ContextWindow: 128000,
		Metadata:      map[string]string{"mode": "chat", "directory_role": "external"},
	}
	desired := server.Model{
		Name: "gpt-4o-mini", Family: "openai", Modality: "text", Status: server.StatusActive,
		Metadata: map[string]string{"mode": "chat"},
	}
	if !sameModel(existing, desired) {
		t.Fatal("expected a model carrying extra target-owned metadata to converge")
	}

	// A key the bundle does own must still be compared.
	drifted := desired
	drifted.Metadata = map[string]string{"mode": "embedding"}
	if sameModel(existing, drifted) {
		t.Fatal("expected a drifted bundle-owned metadata value to be reported")
	}
}

// TestSameProviderIgnoresTargetDefaults covers the same rule for providers:
// the target fills Healthy and Priority on create.
func TestSameProviderIgnoresTargetDefaults(t *testing.T) {
	existing := server.Provider{
		ID: "prv-1", Name: "openai", Type: "openai", BaseURL: "https://api.openai.com/v1",
		Status: server.StatusActive,
		// Defaults the target fills in on create.
		Healthy: true, Priority: 10,
	}
	desired := server.Provider{
		ID: "prv-1", Name: "openai", Type: "openai", BaseURL: "https://api.openai.com/v1",
		Status: server.StatusActive,
	}
	if !sameProvider(existing, desired) {
		t.Fatal("expected target-filled Healthy/Priority defaults to converge")
	}
	drifted := desired
	drifted.BaseURL = "https://other.example.com/v1"
	if sameProvider(existing, drifted) {
		t.Fatal("expected a drifted base URL to be reported")
	}

	preserved := existing
	preserved.Options = map[string]string{"claude_code_attribution_policy": "preserve"}
	if !sameProvider(preserved, desired) {
		t.Fatal("expected an explicit preserve policy to equal the legacy missing option")
	}
	stripped := existing
	stripped.Options = map[string]string{"claude_code_attribution_policy": "strip"}
	if sameProvider(stripped, desired) {
		t.Fatal("expected an explicit strip policy to differ from the legacy missing option")
	}
}

func TestStoreSinkApplyRejectsSSRFProviderBaseURL(t *testing.T) {
	store := server.NewMemoryStore()
	sink := NewStoreSink(store, bundle.StaticResolver{})

	migrationBundle := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		Source: bundle.Source{
			Adapter:        "litellm",
			AdapterVersion: "1.60.0",
		},
		GeneratedAt: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
		Providers: []bundle.ProviderRef{{
			ExternalRef: bundle.ExternalRef{System: "litellm", ID: "provider/evil"},
			Spec: server.Provider{
				ID: "prv_litellm_evil", Name: "Evil", Type: server.ProviderOpenAICompatible,
				Status: server.StatusActive, Healthy: true,
				BaseURL: "http://169.254.169.254/latest/meta-data",
			},
		}},
	}

	if _, err := sink.Apply(migrationBundle); err == nil {
		t.Fatal("expected the sink to reject a link-local provider base URL")
	}
	if providers := store.ListProviders(); len(providers) != 0 {
		t.Fatalf("expected no provider to be persisted, got %d", len(providers))
	}
}

func TestProviderEqualityIncludesSensitiveHeaders(t *testing.T) {
	existing := server.Provider{ID: "prv-1", Name: "openai", Type: "openai", Headers: map[string]string{"X-Tenant": "••••••••"}, SensitiveHeaders: []string{"X-Tenant"}}
	desired := server.Provider{ID: "prv-1", Name: "openai", Type: "openai", Headers: map[string]string{"x-tenant": "tenant-secret"}, SensitiveHeaders: []string{"x-tenant"}}
	if !sameProvider(existing, desired) {
		t.Fatal("expected case-insensitive sensitive Provider headers to converge")
	}
	desired.SensitiveHeaders = nil
	if sameProvider(existing, desired) {
		t.Fatal("expected Provider sensitivity drift to require an update")
	}

	existingResource := server.ProviderResource{ID: "rsrc-1", ProviderID: "prv-1", Name: "account", Headers: existing.Headers, SensitiveHeaders: existing.SensitiveHeaders}
	desiredResource := server.ProviderResource{ID: "rsrc-1", ProviderID: "prv-1", Name: "account", Headers: desired.Headers, SensitiveHeaders: []string{"x-tenant"}}
	if !sameProviderResource(existingResource, desiredResource) {
		t.Fatal("expected case-insensitive sensitive Resource headers to converge")
	}
	desiredResource.SensitiveHeaders = nil
	if sameProviderResource(existingResource, desiredResource) {
		t.Fatal("expected Resource sensitivity drift to require an update")
	}
}
