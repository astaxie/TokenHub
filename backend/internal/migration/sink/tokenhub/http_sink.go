package tokenhub

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strings"

	"tokenhub/backend/internal/migration/bundle"
	"tokenhub/backend/internal/server"
)

type HTTPSink struct {
	client  *AdminAPIClient
	secrets bundle.SecretResolver
	newKeys map[string]string
	// providerModels caches the provider inventory keyed by provider and
	// upstream model. It is nil until the first route needs it, so a bundle
	// without routes never pays for the lookup.
	providerModels map[string]bool
	refIndex       refIndex
}

func NewHTTPSink(client *AdminAPIClient, secrets bundle.SecretResolver) *HTTPSink {
	return &HTTPSink{
		client:   client,
		secrets:  secrets,
		newKeys:  map[string]string{},
		refIndex: newRefIndex(),
	}
}

func (s *HTTPSink) Apply(ctx context.Context, migrationBundle *bundle.CanonicalMigrationBundle) (*ApplyResult, error) {
	if err := bundle.Validate(migrationBundle); err != nil {
		return nil, err
	}
	s.newKeys = map[string]string{}
	s.refIndex = newRefIndex()
	s.providerModels = nil
	providers, err := s.client.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	resources, err := s.client.ListProviderResources(ctx)
	if err != nil {
		return nil, err
	}
	models, err := s.client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.client.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := s.client.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := s.client.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}

	result := &ApplyResult{}
	existingTeams, err := s.client.ListResources(ctx, "teams")
	if err != nil {
		return nil, err
	}
	for _, team := range migrationBundle.Teams {
		change, created, err := s.applyTeamHTTP(ctx, existingTeams, team)
		if err != nil {
			return partialApplyFailure(result, s.newKeys, err)
		}
		if created.ID != "" {
			existingTeams = append(existingTeams, created)
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range migrationBundle.Providers {
		change, created, err := s.applyProviderHTTP(ctx, providers, item)
		if err != nil {
			return partialApplyFailure(result, s.newKeys, err)
		}
		if created.ID != "" {
			providers = append(providers, created)
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range migrationBundle.ProviderResources {
		change, created, err := s.applyProviderResourceHTTP(ctx, resources, item)
		if err != nil {
			return partialApplyFailure(result, s.newKeys, err)
		}
		if created.ID != "" {
			resources = append(resources, created)
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range migrationBundle.Models {
		change, created, err := s.applyModelHTTP(ctx, models, item)
		if err != nil {
			return partialApplyFailure(result, s.newKeys, err)
		}
		if created.Name != "" {
			models = append(models, created)
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range migrationBundle.Users {
		change, err := s.applyUserHTTP(ctx, users, item)
		if err != nil {
			return partialApplyFailure(result, s.newKeys, err)
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	users, err = s.client.ListUsers(ctx)
	if err != nil {
		return partialApplyFailure(result, s.newKeys, fmt.Errorf("list users after import: %w", err))
	}
	// Backfill refIndex for newly created users so project owner refs resolve.
	for _, item := range migrationBundle.Users {
		if _, already := s.refIndex.users[item.ExternalRef.ID]; !already {
			if found, ok := findAdminUserByIdentity(users, item.Spec); ok {
				s.refIndex.users[item.ExternalRef.ID] = found.ID
			}
		}
	}
	for _, item := range migrationBundle.Projects {
		change, created, err := s.applyProjectHTTP(ctx, projects, users, item)
		if err != nil {
			return partialApplyFailure(result, s.newKeys, err)
		}
		if created.ID != "" {
			projects = append(projects, created)
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range migrationBundle.APIKeys {
		change, err := s.applyAPIKeyHTTP(ctx, keys, item)
		if err != nil {
			return partialApplyFailure(result, s.newKeys, err)
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range migrationBundle.Routes {
		change, created, err := s.applyRouteHTTP(ctx, routes, item)
		if err != nil {
			return partialApplyFailure(result, s.newKeys, err)
		}
		if created.ID != "" {
			routes = append(routes, created)
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	return finalizeApplyResult(result, s.newKeys), nil
}

func finalizeApplyResult(result *ApplyResult, newKeys map[string]string) *ApplyResult {
	if result == nil {
		return nil
	}
	result.NewKeys = make(map[string]string, len(newKeys))
	for ref, secret := range newKeys {
		result.NewKeys[ref] = secret
	}
	result.Report = buildReportFromChanges(result.Changes, result.NewKeys)
	return result
}

// partialApplyFailure preserves rollback and one-time-secret artifacts after
// a non-transactional remote apply has already mutated at least one resource.
func partialApplyFailure(result *ApplyResult, newKeys map[string]string, applyErr error) (*ApplyResult, error) {
	result = finalizeApplyResult(result, newKeys)
	for _, change := range result.Changes {
		if change.Action == ActionCreate || change.Action == ActionUpdate {
			return result, applyErr
		}
	}
	if len(result.NewKeys) > 0 {
		return result, applyErr
	}
	return nil, applyErr
}

func (s *HTTPSink) Plan(ctx context.Context, migrationBundle *bundle.CanonicalMigrationBundle) (*MigrationReport, error) {
	if err := bundle.Validate(migrationBundle); err != nil {
		return nil, err
	}
	providers, err := s.client.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	resources, err := s.client.ListProviderResources(ctx)
	if err != nil {
		return nil, err
	}
	models, err := s.client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.client.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := s.client.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := s.client.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	// Plan must not mutate sink state: resolve refs through a local index so
	// a later Apply on the same instance starts from a clean slate.
	planIndex := newRefIndex()
	var changes []Change
	existingTeams, err := s.client.ListResources(ctx, "teams")
	if err != nil {
		return nil, err
	}
	teamIDs, teamChanges, err := planTeams(existingTeams, migrationBundle.Teams)
	if err != nil {
		return nil, err
	}
	planIndex.teams = teamIDs
	changes = append(changes, teamChanges...)
	for _, item := range migrationBundle.Providers {
		spec := item.Spec
		spec.Headers, spec.SensitiveHeaders, err = headerSecretComparisonConfig(spec.Headers, item.HeaderSecrets)
		if err != nil {
			return nil, fmt.Errorf("compare provider header secret %s: %w", item.ExternalRef.ID, err)
		}
		if existing, found := findProviderByBusinessKey(providers, item.Spec.Name, item.Spec.Type); found {
			planIndex.providers[item.ExternalRef.ID] = existing.ID
			changes = append(changes, Change{Resource: "provider", ID: existing.ID, Action: chooseActionProvider(existing, spec, len(item.HeaderSecrets) > 0)})
		} else {
			changes = append(changes, Change{Resource: "provider", ID: item.Spec.ID, Action: ActionCreate})
		}
	}
	for _, item := range migrationBundle.ProviderResources {
		providerID := planIndex.providers[item.ProviderRef]
		spec := item.Spec
		spec.ProviderID = providerID
		spec.Headers, spec.SensitiveHeaders, err = headerSecretComparisonConfig(spec.Headers, item.HeaderSecrets)
		if err != nil {
			return nil, fmt.Errorf("compare provider resource header secret %s: %w", item.ExternalRef.ID, err)
		}
		if existing, found := findProviderResourceByBusinessKey(resources, providerID, item.Spec.Name); found {
			planIndex.resources[item.ExternalRef.ID] = existing.ID
			changes = append(changes, Change{Resource: "provider_resource", ID: existing.ID, Action: chooseActionResource(existing, spec, len(item.HeaderSecrets) > 0)})
		} else {
			changes = append(changes, Change{Resource: "provider_resource", ID: item.Spec.ID, Action: ActionCreate})
		}
	}
	for _, item := range migrationBundle.Models {
		if existing, found := findModelByName(models, item.Spec.Name); found {
			planIndex.models[item.ExternalRef.ID] = existing.ID
			changes = append(changes, Change{Resource: "model", ID: existing.Name, Action: chooseActionModel(existing, item.Spec)})
		} else {
			changes = append(changes, Change{Resource: "model", ID: item.Spec.Name, Action: ActionCreate})
		}
	}
	for _, item := range migrationBundle.Users {
		spec := item.Spec
		spec.TeamID = resolveTeamRef(planIndex, item.TeamRef)
		if existing, found := findAdminUserByIdentity(users, spec); found {
			planIndex.users[item.ExternalRef.ID] = existing.ID
			changes = append(changes, Change{Resource: "user", ID: existing.ID, Action: chooseActionUser(existing, spec)})
		} else {
			changes = append(changes, Change{Resource: "user", ID: spec.Email, Action: ActionCreate})
		}
	}
	for _, item := range migrationBundle.Projects {
		teamID := planIndex.teams[item.TeamRef]
		if existing, found := findProjectByBusinessKey(projects, item.Spec.Name, teamID); found {
			planIndex.projects[item.ExternalRef.ID] = existing.ID
			changes = append(changes, Change{Resource: "project", ID: existing.ID, Action: chooseActionProject(existing, item.Spec)})
		} else {
			changes = append(changes, Change{Resource: "project", ID: item.Spec.Name, Action: ActionCreate})
		}
	}
	for _, item := range migrationBundle.APIKeys {
		projectID := planIndex.projects[item.ProjectRef]
		spec := item.Spec
		spec.ProjectID = projectID
		if existing, found := findAPIKeyByBusinessKey(keys, projectID, item.Spec.Name); found {
			planIndex.apiKeys[item.ExternalRef.ID] = existing.ID
			changes = append(changes, Change{Resource: "api_key", ID: existing.ID, Action: chooseActionAPIKey(existing, spec)})
		} else {
			changes = append(changes, Change{Resource: "api_key", ID: item.Spec.Name, Action: ActionCreate})
		}
	}
	for _, item := range migrationBundle.Routes {
		resourceID := planIndex.resources[item.ProviderResourceRef]
		modelName := item.Spec.ModelName
		if strings.TrimSpace(modelName) == "" {
			if modelID := planIndex.models[item.ModelRef]; modelID != "" {
				modelName = modelID
			}
		}
		if existing, found := findRouteByBusinessKey(routes, modelName, resourceID, item.Spec.ProviderModel); found {
			planIndex.routes[item.ExternalRef.ID] = existing.ID
			changes = append(changes, Change{Resource: "route", ID: existing.ID, Action: chooseActionRoute(existing, item.Spec, resourceID, modelName)})
		} else {
			changes = append(changes, Change{Resource: "route", ID: item.Spec.ID, Action: ActionCreate})
		}
	}
	report := buildReportFromChanges(changes, nil)
	return &report, nil
}

func (s *HTTPSink) Verify(ctx context.Context, migrationBundle *bundle.CanonicalMigrationBundle) (*VerifyResult, error) {
	if err := bundle.Validate(migrationBundle); err != nil {
		return nil, err
	}
	providers, err := s.client.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	resources, err := s.client.ListProviderResources(ctx)
	if err != nil {
		return nil, err
	}
	models, err := s.client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.client.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := s.client.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	routes, err := s.client.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}

	resolved := newRefIndex()
	existingTeams, err := s.client.ListResources(ctx, "teams")
	if err != nil {
		return nil, err
	}
	teamIDs, teamIssues := verifyTeams(existingTeams, migrationBundle.Teams)
	resolved.teams = teamIDs

	result := &VerifyResult{OK: true}
	if len(teamIssues) > 0 {
		result.OK = false
		result.Issues = append(result.Issues, teamIssues...)
	}

	for _, item := range migrationBundle.Providers {
		spec := item.Spec
		spec.Headers, spec.SensitiveHeaders, err = headerSecretComparisonConfig(spec.Headers, item.HeaderSecrets)
		if err != nil {
			return nil, fmt.Errorf("compare provider header secret %s: %w", item.ExternalRef.ID, err)
		}
		existing, found := findProviderByBusinessKey(providers, item.Spec.Name, item.Spec.Type)
		if !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "provider", Ref: item.ExternalRef.ID, Message: "provider not found"})
			continue
		}
		resolved.providers[item.ExternalRef.ID] = existing.ID
		if !sameProvider(existing, spec) {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "provider", Ref: item.ExternalRef.ID, Message: "provider differs from expected spec"})
		}
	}
	for _, item := range migrationBundle.ProviderResources {
		providerID := resolved.providers[item.ProviderRef]
		spec := item.Spec
		spec.ProviderID = providerID
		spec.Headers, spec.SensitiveHeaders, err = headerSecretComparisonConfig(spec.Headers, item.HeaderSecrets)
		if err != nil {
			return nil, fmt.Errorf("compare provider resource header secret %s: %w", item.ExternalRef.ID, err)
		}
		existing, found := findProviderResourceByBusinessKey(resources, providerID, item.Spec.Name)
		if !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "provider_resource", Ref: item.ExternalRef.ID, Message: "provider resource not found"})
			continue
		}
		resolved.resources[item.ExternalRef.ID] = existing.ID
		if !sameProviderResource(existing, spec) {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "provider_resource", Ref: item.ExternalRef.ID, Message: "provider resource differs from expected spec"})
		}
	}
	for _, item := range migrationBundle.Models {
		existing, found := findModelByName(models, item.Spec.Name)
		if !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "model", Ref: item.ExternalRef.ID, Message: "model not found"})
			continue
		}
		resolved.models[item.ExternalRef.ID] = existing.Name
		if !sameModel(existing, item.Spec) {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "model", Ref: item.ExternalRef.ID, Message: "model differs from expected spec"})
		}
	}
	for _, item := range migrationBundle.Users {
		spec := item.Spec
		spec.TeamID = strings.TrimSpace(item.TeamRef)
		if mapped := resolved.teams[spec.TeamID]; mapped != "" {
			spec.TeamID = mapped
		}
		existing, found := findAdminUserByIdentity(users, spec)
		if !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "user", Ref: item.ExternalRef.ID, Message: "user not found"})
			continue
		}
		resolved.users[item.ExternalRef.ID] = existing.ID
		if !sameAdminUser(existing, spec) {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "user", Ref: item.ExternalRef.ID, Message: "user differs from expected spec"})
		}
	}
	for _, item := range migrationBundle.Projects {
		teamID := resolved.teams[item.TeamRef]
		spec := item.Spec
		spec.TeamID = teamID
		if ownerRef := strings.TrimSpace(spec.OwnerUserID); ownerRef != "" {
			if resolvedOwner := resolved.users[ownerRef]; resolvedOwner != "" {
				spec.OwnerUserID = resolvedOwner
			}
		}
		existing, found := findProjectByBusinessKey(projects, item.Spec.Name, teamID)
		if !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "project", Ref: item.ExternalRef.ID, Message: "project not found"})
			continue
		}
		resolved.projects[item.ExternalRef.ID] = existing.ID
		if !sameProject(existing, spec) {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "project", Ref: item.ExternalRef.ID, Message: "project differs from expected spec"})
		}
	}
	for _, item := range migrationBundle.APIKeys {
		projectID := resolved.projects[item.ProjectRef]
		spec := item.Spec
		spec.ProjectID = projectID
		existing, found := findAPIKeyByBusinessKey(keys, projectID, item.Spec.Name)
		if !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "api_key", Ref: item.ExternalRef.ID, Message: "api key not found"})
			continue
		}
		resolved.apiKeys[item.ExternalRef.ID] = existing.ID
		if !sameAPIKey(existing, spec) {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "api_key", Ref: item.ExternalRef.ID, Message: "api key differs from expected spec"})
		}
	}
	for _, item := range migrationBundle.Routes {
		modelName := item.Spec.ModelName
		if modelName == "" {
			modelName = resolved.models[item.ModelRef]
		}
		resourceID := resolved.resources[item.ProviderResourceRef]
		if resourceID == "" {
			resourceID = item.Spec.ProviderResourceID
		}
		spec := item.Spec
		spec.ModelName = modelName
		spec.ProviderResourceID = resourceID
		if spec.ProviderID == "" {
			spec.ProviderID = resolved.providers[item.ProviderRef]
		}
		existing, found := findRouteByBusinessKey(routes, modelName, resourceID, spec.ProviderModel)
		if !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "route", Ref: item.ExternalRef.ID, Message: "route not found"})
			continue
		}
		resolved.routes[item.ExternalRef.ID] = existing.ID
		if !sameRoute(existing, spec) {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "route", Ref: item.ExternalRef.ID, Message: "route differs from expected spec"})
		}
	}
	return result, nil
}

func (s *HTTPSink) Rollback(ctx context.Context, checkpoint Checkpoint) (*RollbackResult, error) {
	result := &RollbackResult{}
	for index := len(checkpoint.Changes) - 1; index >= 0; index-- {
		change := checkpoint.Changes[index]
		if change.Action != ActionCreate {
			continue
		}
		var err error
		switch change.Resource {
		case "provider":
			err = s.client.DeleteProvider(ctx, change.ID)
		case "provider_resource":
			err = s.client.DeleteProviderResource(ctx, change.ID)
		case "model":
			err = s.client.DeleteModel(ctx, change.ID)
		case "route":
			err = s.client.DeleteRoute(ctx, change.ID)
		case "project":
			err = s.client.DeleteProject(ctx, change.ID)
		case "api_key":
			err = s.client.DeleteAPIKey(ctx, change.ID)
		case "user":
			err = s.client.DeleteAdminUser(ctx, change.ID)
		case "team":
			// Teams are removed last: the Admin API refuses to delete a team
			// that still has projects or users attached.
			err = s.client.DeleteResource(ctx, "teams", change.ID)
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		result.Changes = append(result.Changes, Change{Resource: change.Resource, ID: change.ID, Action: ActionDelete})
	}
	return result, nil
}

func buildReportFromChanges(changes []Change, newKeys map[string]string) MigrationReport {
	var report MigrationReport
	for _, change := range changes {
		switch change.Action {
		case ActionCreate:
			report.Created++
		case ActionUpdate:
			report.Updated++
		case ActionSkip:
			report.Skipped++
		}
	}
	if len(newKeys) > 0 {
		report.NewKeys = newKeys
	}
	return report
}

func chooseActionProvider(existing server.Provider, desired server.Provider, refreshHeaderSecrets bool) Action {
	// The Admin API only returns masked values, and resolver material may rotate
	// without a bundle diff. A HeaderSecrets entry therefore forces a refresh.
	if refreshHeaderSecrets {
		return ActionUpdate
	}
	if sameProvider(existing, desired) {
		return ActionSkip
	}
	return ActionUpdate
}

func chooseActionResource(existing server.ProviderResource, desired server.ProviderResource, refreshHeaderSecrets bool) Action {
	if refreshHeaderSecrets {
		return ActionUpdate
	}
	if sameProviderResource(existing, desired) {
		return ActionSkip
	}
	return ActionUpdate
}

func chooseActionModel(existing server.Model, desired server.Model) Action {
	if sameModel(existing, desired) {
		return ActionSkip
	}
	return ActionUpdate
}

func chooseActionUser(existing server.AdminUser, desired server.AdminUser) Action {
	if sameAdminUser(existing, desired) {
		return ActionSkip
	}
	return ActionUpdate
}

func chooseActionProject(existing server.Project, desired server.Project) Action {
	if sameProject(existing, desired) {
		return ActionSkip
	}
	return ActionUpdate
}

func chooseActionAPIKey(existing server.APIKey, desired server.APIKey) Action {
	if sameAPIKey(existing, desired) {
		return ActionSkip
	}
	return ActionUpdate
}

func chooseActionRoute(existing server.ModelRoute, desired server.ModelRoute, resourceID string, modelName string) Action {
	desired.ProviderResourceID = resourceID
	desired.ModelName = modelName
	if sameRoute(existing, desired) {
		return ActionSkip
	}
	return ActionUpdate
}

func (s *HTTPSink) applyProviderHTTP(ctx context.Context, existing []server.Provider, item bundle.ProviderRef) (Change, server.Provider, error) {
	spec := item.Spec
	resolvedHeaders, sensitiveHeaders, err := resolveHeaderSecrets(s.secrets, spec.Headers, item.HeaderSecrets)
	if err != nil {
		return Change{}, server.Provider{}, fmt.Errorf("resolve provider header secret %s: %w", item.ExternalRef.ID, err)
	}
	spec.Headers = resolvedHeaders
	spec.SensitiveHeaders = sensitiveHeaders
	if item.APIKeySecret != nil && !item.APIKeySecret.IsZero() {
		resolved, err := s.secrets.Resolve(*item.APIKeySecret)
		if err != nil {
			return Change{}, server.Provider{}, err
		}
		spec.APIKey = resolved
	}
	if current, found := findProviderByBusinessKey(existing, spec.Name, spec.Type); found {
		s.refIndex.providers[item.ExternalRef.ID] = current.ID
		action := chooseActionProvider(current, spec, len(item.HeaderSecrets) > 0)
		if action == ActionSkip {
			return Change{Resource: "provider", ID: current.ID, Action: ActionSkip}, server.Provider{}, nil
		}
		updated, err := s.client.UpdateProvider(ctx, current.ID, providerUpdateSpec(current, spec))
		return Change{Resource: "provider", ID: current.ID, Action: ActionUpdate}, updated, err
	}
	created, err := s.client.CreateProvider(ctx, spec)
	if err != nil {
		return Change{}, server.Provider{}, err
	}
	s.refIndex.providers[item.ExternalRef.ID] = created.ID
	return Change{Resource: "provider", ID: created.ID, Action: ActionCreate}, created, nil
}

func (s *HTTPSink) applyProviderResourceHTTP(ctx context.Context, existing []server.ProviderResource, item bundle.ProviderResourceRef) (Change, server.ProviderResource, error) {
	spec := item.Spec
	resolvedHeaders, sensitiveHeaders, err := resolveHeaderSecrets(s.secrets, spec.Headers, item.HeaderSecrets)
	if err != nil {
		return Change{}, server.ProviderResource{}, fmt.Errorf("resolve provider resource header secret %s: %w", item.ExternalRef.ID, err)
	}
	spec.Headers = resolvedHeaders
	spec.SensitiveHeaders = sensitiveHeaders
	spec.ProviderID = s.refIndex.providers[item.ProviderRef]
	if spec.ProviderID == "" {
		return Change{}, server.ProviderResource{}, fmt.Errorf("missing provider ref for %s", item.ExternalRef.ID)
	}
	if item.APIKeySecret != nil && !item.APIKeySecret.IsZero() {
		resolved, err := s.secrets.Resolve(*item.APIKeySecret)
		if err != nil {
			return Change{}, server.ProviderResource{}, err
		}
		spec.APIKey = resolved
	}
	if current, found := findProviderResourceByBusinessKey(existing, spec.ProviderID, spec.Name); found {
		s.refIndex.resources[item.ExternalRef.ID] = current.ID
		action := chooseActionResource(current, spec, len(item.HeaderSecrets) > 0)
		if action == ActionSkip {
			return Change{Resource: "provider_resource", ID: current.ID, Action: ActionSkip}, server.ProviderResource{}, nil
		}
		updated, err := s.client.UpdateProviderResource(ctx, current.ID, providerResourceUpdateSpec(current, spec))
		return Change{Resource: "provider_resource", ID: current.ID, Action: ActionUpdate}, updated, err
	}
	created, err := s.client.CreateProviderResource(ctx, spec)
	if err != nil {
		return Change{}, server.ProviderResource{}, err
	}
	s.refIndex.resources[item.ExternalRef.ID] = created.ID
	return Change{Resource: "provider_resource", ID: created.ID, Action: ActionCreate}, created, nil
}

func (s *HTTPSink) applyModelHTTP(ctx context.Context, existing []server.Model, item bundle.ModelRef) (Change, server.Model, error) {
	spec := item.Spec
	if current, found := findModelByName(existing, spec.Name); found {
		s.refIndex.models[item.ExternalRef.ID] = current.Name
		action := chooseActionModel(current, spec)
		if action == ActionSkip {
			return Change{Resource: "model", ID: current.Name, Action: ActionSkip}, server.Model{}, nil
		}
		updated, err := s.client.UpdateModel(ctx, current.Name, spec)
		return Change{Resource: "model", ID: current.Name, Action: ActionUpdate}, updated, err
	}
	created, err := s.client.CreateModel(ctx, spec)
	if err != nil {
		return Change{}, server.Model{}, err
	}
	s.refIndex.models[item.ExternalRef.ID] = created.Name
	return Change{Resource: "model", ID: created.Name, Action: ActionCreate}, created, nil
}

func (s *HTTPSink) applyUserHTTP(ctx context.Context, existing []server.AdminUser, item bundle.UserRef) (Change, error) {
	spec := item.Spec
	spec.TeamID = resolveTeamRef(s.refIndex, item.TeamRef)
	if current, found := findAdminUserByIdentity(existing, spec); found {
		s.refIndex.users[item.ExternalRef.ID] = current.ID
		if chooseActionUser(current, spec) == ActionSkip {
			return Change{Resource: "user", ID: current.ID, Action: ActionSkip}, nil
		}
		// The bundle carries a single team ref, so it does not own the user's
		// full team membership. The server rewrites team_ids from the payload
		// unconditionally, so existing memberships have to be carried over or
		// an unrelated field change would drop them.
		spec.TeamIDs = current.TeamIDs
		if _, err := s.client.UpdateAdminUser(ctx, current.ID, spec); err != nil {
			return Change{}, err
		}
		return Change{Resource: "user", ID: current.ID, Action: ActionUpdate}, nil
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"username", "name", "email", "role", "team_id", "status"})
	_ = w.Write([]string{spec.Username, spec.Name, spec.Email, spec.Role, spec.TeamID, spec.Status})
	w.Flush()
	if err := s.client.ImportUsersCSV(ctx, buf.String()); err != nil {
		return Change{}, err
	}
	// Refetch so the change carries the server-assigned user ID and later
	// owner refs resolve through refIndex.
	users, err := s.client.ListUsers(ctx)
	if err != nil {
		return Change{}, err
	}
	created, ok := findAdminUserByIdentity(users, spec)
	if !ok {
		return Change{}, fmt.Errorf("user %s not found after import", item.ExternalRef.ID)
	}
	s.refIndex.users[item.ExternalRef.ID] = created.ID
	return Change{Resource: "user", ID: created.ID, Action: ActionCreate}, nil
}

// applyTeamHTTP ensures the bundle team exists on the target before any user
// or project references it: the Admin API rejects mutations naming a team it
// does not know.
func (s *HTTPSink) applyTeamHTTP(ctx context.Context, existing []server.AdminResource, item bundle.TeamRef) (Change, server.AdminResource, error) {
	resolution, err := resolveTeam(existing, item)
	if err != nil {
		return Change{}, server.AdminResource{}, err
	}
	if resolution.Exists {
		s.refIndex.teams[item.ExternalRef.ID] = resolution.ID
		return Change{Resource: "team", ID: resolution.ID, Action: ActionSkip}, server.AdminResource{}, nil
	}
	created, err := s.client.CreateResource(ctx, "teams", desiredTeamResource(item))
	if err != nil {
		return Change{}, server.AdminResource{}, err
	}
	s.refIndex.teams[item.ExternalRef.ID] = created.ID
	return Change{Resource: "team", ID: created.ID, Action: ActionCreate}, created, nil
}

// resolveTeamRef maps a bundle team ref onto the target team ID, falling
// back to the literal ref for teams that already exist on the target.
func resolveTeamRef(idx refIndex, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if mapped := idx.teams[ref]; mapped != "" {
		return mapped
	}
	return ref
}

func (s *HTTPSink) applyProjectHTTP(ctx context.Context, existingProjects []server.Project, existingUsers []server.AdminUser, item bundle.ProjectRef) (Change, server.Project, error) {
	spec := item.Spec
	spec.TeamID = s.refIndex.teams[item.TeamRef]
	if ownerRef := strings.TrimSpace(spec.OwnerUserID); ownerRef != "" {
		if ownerID := s.refIndex.users[ownerRef]; ownerID != "" {
			spec.OwnerUserID = ownerID
		} else {
			for _, user := range existingUsers {
				if user.Email == ownerRef || user.ID == ownerRef || user.Username == ownerRef {
					spec.OwnerUserID = user.ID
					break
				}
			}
		}
	}
	if current, found := findProjectByBusinessKey(existingProjects, spec.Name, spec.TeamID); found {
		s.refIndex.projects[item.ExternalRef.ID] = current.ID
		action := chooseActionProject(current, spec)
		if action == ActionSkip {
			return Change{Resource: "project", ID: current.ID, Action: ActionSkip}, server.Project{}, nil
		}
		updated, err := s.client.UpdateProject(ctx, current.ID, spec)
		return Change{Resource: "project", ID: current.ID, Action: ActionUpdate}, updated, err
	}
	created, err := s.client.CreateProject(ctx, spec)
	if err != nil {
		return Change{}, server.Project{}, err
	}
	s.refIndex.projects[item.ExternalRef.ID] = created.ID
	return Change{Resource: "project", ID: created.ID, Action: ActionCreate}, created, nil
}

func (s *HTTPSink) applyAPIKeyHTTP(ctx context.Context, existingKeys []server.APIKey, item bundle.APIKeyRef) (Change, error) {
	projectID := s.refIndex.projects[item.ProjectRef]
	if projectID == "" {
		return Change{}, fmt.Errorf("missing project ref for %s", item.ExternalRef.ID)
	}
	spec := item.Spec
	spec.ProjectID = projectID
	if current, found := findAPIKeyByBusinessKey(existingKeys, projectID, item.Spec.Name); found {
		s.refIndex.apiKeys[item.ExternalRef.ID] = current.ID
		action := chooseActionAPIKey(current, spec)
		if action == ActionSkip {
			return Change{Resource: "api_key", ID: current.ID, Action: ActionSkip}, nil
		}
		_, err := s.client.UpdateAPIKey(ctx, current.ID, spec)
		return Change{Resource: "api_key", ID: current.ID, Action: ActionUpdate}, err
	}
	payload := map[string]any{
		"name":              spec.Name,
		"group":             spec.Group,
		"allowed_models":    spec.Allowed,
		"model_access_mode": spec.ModelAccessMode,
		"ip_allowlist":      spec.IPAllowlist,
		"limits":            spec.Limits,
		"rate_limit_rpm":    spec.RateLimitRPM,
		"token_limit_tpm":   spec.TokenLimitTPM,
		"expires_at":        spec.ExpiresAt,
	}
	created, err := s.client.CreateProjectKey(ctx, projectID, payload)
	if err != nil {
		return Change{}, err
	}
	s.refIndex.apiKeys[item.ExternalRef.ID] = created.ID
	if created.APIKey != "" {
		s.newKeys[item.ExternalRef.ID] = created.APIKey
	}
	return Change{Resource: "api_key", ID: created.ID, Action: ActionCreate}, nil
}

func (s *HTTPSink) applyRouteHTTP(ctx context.Context, existing []server.ModelRoute, item bundle.RouteRef) (Change, server.ModelRoute, error) {
	spec := item.Spec
	if spec.ModelName == "" {
		spec.ModelName = s.refIndex.models[item.ModelRef]
	}
	if item.ProviderResourceRef != "" {
		spec.ProviderResourceID = s.refIndex.resources[item.ProviderResourceRef]
	}
	if spec.ProviderID == "" {
		spec.ProviderID = s.refIndex.providers[item.ProviderRef]
	}
	if current, found := findRouteByBusinessKey(existing, spec.ModelName, spec.ProviderResourceID, spec.ProviderModel); found {
		s.refIndex.routes[item.ExternalRef.ID] = current.ID
		action := chooseActionRoute(current, spec, spec.ProviderResourceID, spec.ModelName)
		if action == ActionSkip {
			return Change{Resource: "route", ID: current.ID, Action: ActionSkip}, server.ModelRoute{}, nil
		}
		// The target validates the imported inventory on update too, so a
		// route whose model is missing there cannot be patched either.
		if err := s.ensureProviderModel(ctx, spec.ProviderID, spec.ProviderModel); err != nil {
			return Change{}, server.ModelRoute{}, err
		}
		updated, err := s.client.UpdateRoute(ctx, current.ID, spec)
		return Change{Resource: "route", ID: current.ID, Action: ActionUpdate}, updated, err
	}
	if err := s.ensureProviderModel(ctx, spec.ProviderID, spec.ProviderModel); err != nil {
		return Change{}, server.ModelRoute{}, err
	}
	created, err := s.client.CreateRoute(ctx, spec)
	if err != nil {
		return Change{}, server.ModelRoute{}, err
	}
	s.refIndex.routes[item.ExternalRef.ID] = created.ID
	return Change{Resource: "route", ID: created.ID, Action: ActionCreate}, created, nil
}

// ensureProviderModel imports the route's upstream model into the provider's
// inventory when it is missing. The Admin API refuses to create a route for a
// model that was never imported for that provider, so a migration into a
// target without pre-existing inventory would otherwise fail outright.
func (s *HTTPSink) ensureProviderModel(ctx context.Context, providerID string, upstreamModel string) error {
	providerID = strings.TrimSpace(providerID)
	upstreamModel = strings.TrimSpace(upstreamModel)
	if providerID == "" || upstreamModel == "" {
		return nil
	}
	if s.providerModels == nil {
		existing, err := s.client.ListProviderModels(ctx)
		if err != nil {
			return err
		}
		s.providerModels = map[string]bool{}
		for _, model := range existing {
			s.providerModels[providerModelKey(model.ProviderID, model.UpstreamModel)] = true
		}
	}
	key := providerModelKey(providerID, upstreamModel)
	if s.providerModels[key] {
		return nil
	}
	if _, err := s.client.ImportProviderModels(ctx, providerID, []string{upstreamModel}); err != nil {
		return fmt.Errorf("import upstream model %s for provider %s: %w", upstreamModel, providerID, err)
	}
	s.providerModels[key] = true
	return nil
}

func providerModelKey(providerID string, upstreamModel string) string {
	return strings.TrimSpace(providerID) + "\x00" + strings.TrimSpace(upstreamModel)
}
