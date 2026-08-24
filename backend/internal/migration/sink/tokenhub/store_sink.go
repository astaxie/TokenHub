package tokenhub

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"tokenhub/backend/internal/migration/bundle"
	"tokenhub/backend/internal/server"
)

type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionSkip   Action = "skip"
	ActionDelete Action = "delete"
)

// MigrationReport is a structured summary of an apply or plan operation.
type MigrationReport struct {
	Created int               `json:"created"`
	Updated int               `json:"updated"`
	Skipped int               `json:"skipped"`
	Errors  []string          `json:"errors,omitempty"`
	NewKeys map[string]string `json:"new_keys,omitempty"`
}

type Change struct {
	Resource string `json:"resource"`
	ID       string `json:"id"`
	Action   Action `json:"action"`
}

type Checkpoint struct {
	Changes []Change `json:"changes"`
}

type ApplyResult struct {
	Changes     []Change          `json:"changes"`
	Checkpoint  Checkpoint        `json:"checkpoint"`
	Report      MigrationReport   `json:"report"`
	NewKeys     map[string]string `json:"new_keys,omitempty"`
	Unsupported []string          `json:"unsupported,omitempty"`
}

type VerifyIssue struct {
	Resource string `json:"resource"`
	Ref      string `json:"ref"`
	Message  string `json:"message"`
}

type VerifyResult struct {
	OK     bool          `json:"ok"`
	Issues []VerifyIssue `json:"issues,omitempty"`
}

type RollbackResult struct {
	Changes []Change `json:"changes"`
}

type StoreSink struct {
	store    server.Store
	secrets  bundle.SecretResolver
	newKeys  map[string]string
	refIndex refIndex
}

type refIndex struct {
	providers map[string]string
	resources map[string]string
	projects  map[string]string
	users     map[string]string
	apiKeys   map[string]string
	models    map[string]string
	routes    map[string]string
	teams     map[string]string
}

func newRefIndex() refIndex {
	return refIndex{
		providers: map[string]string{},
		resources: map[string]string{},
		projects:  map[string]string{},
		users:     map[string]string{},
		apiKeys:   map[string]string{},
		models:    map[string]string{},
		routes:    map[string]string{},
		teams:     map[string]string{},
	}
}

func NewStoreSink(store server.Store, secrets bundle.SecretResolver) *StoreSink {
	return &StoreSink{
		store:    store,
		secrets:  secrets,
		newKeys:  map[string]string{},
		refIndex: newRefIndex(),
	}
}

func (s *StoreSink) Apply(b *bundle.CanonicalMigrationBundle) (*ApplyResult, error) {
	if err := bundle.Validate(b); err != nil {
		return nil, err
	}
	s.newKeys = map[string]string{}
	s.refIndex = newRefIndex()
	if len(b.QuotaPolicies) > 0 {
		return nil, fmt.Errorf("quota_policies are not implemented by the TokenHub sink foundation")
	}
	result := &ApplyResult{}

	for _, item := range b.Teams {
		change, err := s.applyTeam(item)
		if err != nil {
			return nil, err
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range b.Providers {
		change, err := s.applyProvider(item)
		if err != nil {
			return nil, err
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range b.ProviderResources {
		change, err := s.applyProviderResource(item)
		if err != nil {
			return nil, err
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range b.Models {
		change, err := s.applyModel(item)
		if err != nil {
			return nil, err
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range b.Users {
		change, err := s.applyUser(item)
		if err != nil {
			return nil, err
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range b.Projects {
		change, err := s.applyProject(item)
		if err != nil {
			return nil, err
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range b.APIKeys {
		change, err := s.applyAPIKey(item)
		if err != nil {
			return nil, err
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	for _, item := range b.Routes {
		change, err := s.applyRoute(item)
		if err != nil {
			return nil, err
		}
		result.Changes = append(result.Changes, change)
		result.Checkpoint.Changes = append(result.Checkpoint.Changes, change)
	}
	result.NewKeys = s.NewKeys()
	result.Report = s.buildReport(result.Changes)

	return result, nil
}

func (s *StoreSink) Verify(b *bundle.CanonicalMigrationBundle) (*VerifyResult, error) {
	if err := bundle.Validate(b); err != nil {
		return nil, err
	}
	result := &VerifyResult{OK: true}
	resolved, teamIssues := s.buildResolvedIndex(b)
	if len(teamIssues) > 0 {
		result.OK = false
		result.Issues = append(result.Issues, teamIssues...)
	}

	for _, item := range b.Providers {
		if _, found := findProviderByBusinessKey(s.store.ListProviders(), item.Spec.Name, item.Spec.Type); !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "provider", Ref: item.ExternalRef.ID, Message: "provider not found"})
		}
	}
	for _, item := range b.ProviderResources {
		providerID := resolved.providers[item.ProviderRef]
		if _, found := findProviderResourceByBusinessKey(s.store.ListProviderResources(), providerID, item.Spec.Name); !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "provider_resource", Ref: item.ExternalRef.ID, Message: "provider resource not found"})
		}
	}
	for _, item := range b.Models {
		if _, found := findModelByName(s.store.ListModels(), item.Spec.Name); !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "model", Ref: item.ExternalRef.ID, Message: "model not found"})
		}
	}
	for _, item := range b.Users {
		if _, found := findAdminUserByIdentity(s.store.ListAdminUsers(), item.Spec); !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "user", Ref: item.ExternalRef.ID, Message: "user not found"})
		}
	}
	for _, item := range b.Projects {
		teamID := resolved.teams[item.TeamRef]
		if _, found := findProjectByBusinessKey(s.store.ListProjects(), item.Spec.Name, teamID); !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "project", Ref: item.ExternalRef.ID, Message: "project not found"})
		}
	}
	for _, item := range b.APIKeys {
		projectID := resolved.projects[item.ProjectRef]
		if _, found := findAPIKeyByBusinessKey(s.store.ListAPIKeys(), projectID, item.Spec.Name); !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "api_key", Ref: item.ExternalRef.ID, Message: "api key not found"})
		}
	}
	for _, item := range b.Routes {
		modelName := resolved.models[item.ModelRef]
		resourceID := resolved.resources[item.ProviderResourceRef]
		if _, found := findRouteByBusinessKey(s.store.ListRoutes(), modelName, resourceID, item.Spec.ProviderModel); !found {
			result.OK = false
			result.Issues = append(result.Issues, VerifyIssue{Resource: "route", Ref: item.ExternalRef.ID, Message: "route not found"})
		}
	}

	return result, nil
}

// buildResolvedIndex maps bundle refs onto the IDs present on the target and
// reports the teams that could not be resolved, which Verify surfaces as drift.
func (s *StoreSink) buildResolvedIndex(b *bundle.CanonicalMigrationBundle) (refIndex, []VerifyIssue) {
	index := refIndex{
		providers: map[string]string{},
		resources: map[string]string{},
		projects:  map[string]string{},
		users:     map[string]string{},
		apiKeys:   map[string]string{},
		models:    map[string]string{},
		routes:    map[string]string{},
		teams:     map[string]string{},
	}
	teamIDs, teamIssues := verifyTeams(s.store.ListResources("teams"), b.Teams)
	index.teams = teamIDs
	for _, item := range b.Providers {
		if existing, found := findProviderByBusinessKey(s.store.ListProviders(), item.Spec.Name, item.Spec.Type); found {
			index.providers[item.ExternalRef.ID] = existing.ID
		}
	}
	for _, item := range b.ProviderResources {
		providerID := item.Spec.ProviderID
		if mapped := index.providers[item.ProviderRef]; mapped != "" {
			providerID = mapped
		}
		if existing, found := findProviderResourceByBusinessKey(s.store.ListProviderResources(), providerID, item.Spec.Name); found {
			index.resources[item.ExternalRef.ID] = existing.ID
		}
	}
	for _, item := range b.Models {
		if existing, found := findModelByName(s.store.ListModels(), item.Spec.Name); found {
			index.models[item.ExternalRef.ID] = existing.Name
		}
	}
	for _, item := range b.Users {
		if existing, found := findAdminUserByIdentity(s.store.ListAdminUsers(), item.Spec); found {
			index.users[item.ExternalRef.ID] = existing.ID
		}
	}
	for _, item := range b.Projects {
		teamID := item.Spec.TeamID
		if mapped := index.teams[item.TeamRef]; mapped != "" {
			teamID = mapped
		}
		if existing, found := findProjectByBusinessKey(s.store.ListProjects(), item.Spec.Name, teamID); found {
			index.projects[item.ExternalRef.ID] = existing.ID
		}
	}
	for _, item := range b.APIKeys {
		projectID := item.Spec.ProjectID
		if mapped := index.projects[item.ProjectRef]; mapped != "" {
			projectID = mapped
		}
		if existing, found := findAPIKeyByBusinessKey(s.store.ListAPIKeys(), projectID, item.Spec.Name); found {
			index.apiKeys[item.ExternalRef.ID] = existing.ID
		}
	}
	for _, item := range b.Routes {
		modelName := item.Spec.ModelName
		if mapped := index.models[item.ModelRef]; mapped != "" {
			modelName = mapped
		}
		resourceID := item.Spec.ProviderResourceID
		if mapped := index.resources[item.ProviderResourceRef]; mapped != "" {
			resourceID = mapped
		}
		if existing, found := findRouteByBusinessKey(s.store.ListRoutes(), modelName, resourceID, item.Spec.ProviderModel); found {
			index.routes[item.ExternalRef.ID] = existing.ID
		}
	}
	return index, teamIssues
}

func (s *StoreSink) Rollback(checkpoint Checkpoint) (*RollbackResult, error) {
	result := &RollbackResult{}
	for index := len(checkpoint.Changes) - 1; index >= 0; index-- {
		change := checkpoint.Changes[index]
		if change.Action != ActionCreate {
			continue
		}
		var err error
		switch change.Resource {
		case "api_key":
			err = s.store.DeleteAPIKey(change.ID)
		case "project":
			err = s.store.DeleteProject(change.ID)
		case "user":
			err = s.store.DeleteAdminUser(change.ID)
		case "route":
			err = s.store.DeleteRoute(change.ID)
		case "model":
			err = s.store.DeleteModel(change.ID)
		case "provider_resource":
			err = s.store.DeleteProviderResource(change.ID)
		case "provider":
			err = s.store.DeleteProvider(change.ID)
		case "team":
			// Teams are removed last because the store refuses to delete a
			// team that still has projects or users attached. A team that
			// pre-existing resources were moved onto therefore fails here
			// rather than being silently left behind.
			err = s.store.DeleteTeam(change.ID)
		}
		if err != nil {
			return nil, fmt.Errorf("rollback %s %s: %w", change.Resource, change.ID, err)
		}
		result.Changes = append(result.Changes, Change{Resource: change.Resource, ID: change.ID, Action: ActionDelete})
	}
	return result, nil
}

// applyTeam ensures the bundle team exists on the target. Teams are not just
// ID mappings: the store rejects user and project mutations that name a team
// it does not know, so a team the source owns has to be created before any
// resource can reference it.
func (s *StoreSink) applyTeam(item bundle.TeamRef) (Change, error) {
	resolution, err := resolveTeam(s.store.ListResources("teams"), item)
	if err != nil {
		return Change{}, err
	}
	if resolution.Exists {
		s.refIndex.teams[item.ExternalRef.ID] = resolution.ID
		return Change{Resource: "team", ID: resolution.ID, Action: ActionSkip}, nil
	}
	created := s.store.CreateResource("teams", desiredTeamResource(item))
	s.refIndex.teams[item.ExternalRef.ID] = created.ID
	return Change{Resource: "team", ID: created.ID, Action: ActionCreate}, nil
}

func (s *StoreSink) resolveTeamRef(ref string) string {
	if ref == "" {
		return ""
	}
	if mapped := s.refIndex.teams[ref]; mapped != "" {
		return mapped
	}
	return ref
}

func (s *StoreSink) NewKeys() map[string]string {
	copyMap := make(map[string]string, len(s.newKeys))
	for key, value := range s.newKeys {
		copyMap[key] = value
	}
	return copyMap
}

// buildReport creates a MigrationReport from a list of changes.
func (s *StoreSink) buildReport(changes []Change) MigrationReport {
	var report MigrationReport
	for _, c := range changes {
		switch c.Action {
		case ActionCreate:
			report.Created++
		case ActionUpdate:
			report.Updated++
		case ActionSkip:
			report.Skipped++
		}
	}
	report.NewKeys = s.NewKeys()
	return report
}

// Plan performs a dry-run apply and returns a MigrationReport without
// writing any resources to the store.
func (s *StoreSink) Plan(b *bundle.CanonicalMigrationBundle) (*MigrationReport, error) {
	if err := bundle.Validate(b); err != nil {
		return nil, err
	}
	// Build a temporary ref index so we can resolve provider and project refs
	// for nested resources. This mirrors the ref index built during Apply.
	planIndex := newRefIndex()
	teamIDs, teamChanges, err := planTeams(s.store.ListResources("teams"), b.Teams)
	if err != nil {
		return nil, err
	}
	planIndex.teams = teamIDs
	// Resolve project refs for Plan
	projects := s.store.ListProjects()
	for _, item := range b.Projects {
		teamID := planIndex.teams[item.TeamRef]
		_, found := findProjectByBusinessKey(projects, item.Spec.Name, teamID)
		if found {
			planIndex.projects[item.ExternalRef.ID] = "existing"
		}
	}

	var report MigrationReport
	for _, change := range teamChanges {
		if change.Action == ActionCreate {
			report.Created++
		} else {
			report.Skipped++
		}
	}
	providers := s.store.ListProviders()
	for _, item := range b.Providers {
		existing, found := findProviderByBusinessKey(providers, item.Spec.Name, item.Spec.Type)
		if found {
			// Record the mapping so provider resources below resolve against the
			// existing provider instead of always planning a create.
			planIndex.providers[item.ExternalRef.ID] = existing.ID
			report.Updated++
		} else {
			report.Created++
		}
	}
	resources := s.store.ListProviderResources()
	for _, item := range b.ProviderResources {
		providerID := planIndex.providers[item.ProviderRef]
		_, found := findProviderResourceByBusinessKey(resources, providerID, item.Spec.Name)
		if found {
			report.Updated++
		} else {
			report.Created++
		}
	}
	models := s.store.ListModels()
	for _, item := range b.Models {
		_, found := findModelByName(models, item.Spec.Name)
		if found {
			report.Updated++
		} else {
			report.Created++
		}
	}
	users := s.store.ListAdminUsers()
	for _, item := range b.Users {
		_, found := findAdminUserByIdentity(users, item.Spec)
		if found {
			report.Updated++
		} else {
			report.Created++
		}
	}
	for _, item := range b.Projects {
		teamID := planIndex.teams[item.TeamRef]
		_, found := findProjectByBusinessKey(projects, item.Spec.Name, teamID)
		if found {
			report.Updated++
		} else {
			report.Created++
		}
	}
	keys := s.store.ListAPIKeys()
	for _, item := range b.APIKeys {
		projectID := planIndex.projects[item.ProjectRef]
		_, found := findAPIKeyByBusinessKey(keys, projectID, item.Spec.Name)
		if found {
			report.Updated++
		} else {
			report.Created++
		}
	}
	routes := s.store.ListRoutes()
	for _, item := range b.Routes {
		_, found := findRouteByBusinessKey(routes, item.Spec.ModelName, item.Spec.ProviderResourceID, item.Spec.ProviderModel)
		if found {
			report.Updated++
		} else {
			report.Created++
		}
	}
	return &report, nil
}

func (s *StoreSink) applyProvider(item bundle.ProviderRef) (Change, error) {
	spec := item.Spec
	resolvedHeaders, sensitiveHeaders, err := resolveHeaderSecrets(s.secrets, spec.Headers, item.HeaderSecrets)
	if err != nil {
		return Change{}, fmt.Errorf("resolve provider header secret %s: %w", item.ExternalRef.ID, err)
	}
	spec.Headers = resolvedHeaders
	spec.SensitiveHeaders = sensitiveHeaders
	if item.APIKeySecret != nil && !item.APIKeySecret.IsZero() {
		secret, err := s.secrets.Resolve(*item.APIKeySecret)
		if err != nil {
			return Change{}, fmt.Errorf("resolve provider secret %s: %w", item.ExternalRef.ID, err)
		}
		spec.APIKey = secret
	}
	if err := server.ValidateProviderHeaderConfigForWrite(&spec); err != nil {
		return Change{}, fmt.Errorf("validate provider headers %s: %w", item.ExternalRef.ID, err)
	}
	// The sink writes straight to the store, bypassing the admin HTTP layer
	// where base URLs are normally validated; apply the same SSRF guard here
	// so a bundle cannot persist a provider that dials the internal network.
	if err := server.ValidateProviderUpstreamBaseURL(spec.BaseURL); err != nil {
		return Change{}, fmt.Errorf("provider %s: %w", item.ExternalRef.ID, err)
	}

	for _, existing := range s.store.ListProviders() {
		if existing.Name == spec.Name && existing.Type == spec.Type {
			// Header secrets are write-only on the target and their resolver values
			// may rotate without changing the bundle, so every apply reasserts them.
			if len(item.HeaderSecrets) == 0 && sameProvider(existing, spec) {
				s.refIndex.providers[item.ExternalRef.ID] = existing.ID
				return Change{Resource: "provider", ID: existing.ID, Action: ActionSkip}, nil
			}
			updated, err := s.store.UpdateProvider(existing.ID, providerUpdateSpec(existing, spec))
			if err != nil {
				return Change{}, err
			}
			s.refIndex.providers[item.ExternalRef.ID] = updated.ID
			return Change{Resource: "provider", ID: updated.ID, Action: ActionUpdate}, nil
		}
	}
	created := s.store.AddProvider(spec)
	s.refIndex.providers[item.ExternalRef.ID] = created.ID
	return Change{Resource: "provider", ID: created.ID, Action: ActionCreate}, nil
}

func (s *StoreSink) applyProviderResource(item bundle.ProviderResourceRef) (Change, error) {
	spec := item.Spec
	resolvedHeaders, sensitiveHeaders, err := resolveHeaderSecrets(s.secrets, spec.Headers, item.HeaderSecrets)
	if err != nil {
		return Change{}, fmt.Errorf("resolve provider resource header secret %s: %w", item.ExternalRef.ID, err)
	}
	spec.Headers = resolvedHeaders
	spec.SensitiveHeaders = sensitiveHeaders
	if providerID := s.refIndex.providers[item.ProviderRef]; providerID != "" {
		spec.ProviderID = providerID
	}
	if item.APIKeySecret != nil && !item.APIKeySecret.IsZero() {
		secret, err := s.secrets.Resolve(*item.APIKeySecret)
		if err != nil {
			return Change{}, fmt.Errorf("resolve provider resource secret %s: %w", item.ExternalRef.ID, err)
		}
		spec.APIKey = secret
	}
	for _, provider := range s.store.ListProviders() {
		if provider.ID == spec.ProviderID {
			if err := server.ValidateProviderHeaderSupportForWrite(provider.Type, spec.Headers); err != nil {
				return Change{}, fmt.Errorf("validate provider resource headers %s: %w", item.ExternalRef.ID, err)
			}
			break
		}
	}

	for _, existing := range s.store.ListProviderResources() {
		if existing.ProviderID == spec.ProviderID && existing.Name == spec.Name {
			// See applyProvider: a SecretRef is authoritative on every apply.
			if len(item.HeaderSecrets) == 0 && sameProviderResource(existing, spec) {
				s.refIndex.resources[item.ExternalRef.ID] = existing.ID
				return Change{Resource: "provider_resource", ID: existing.ID, Action: ActionSkip}, nil
			}
			updated, err := s.store.UpdateProviderResource(existing.ID, providerResourceUpdateSpec(existing, spec))
			if err != nil {
				return Change{}, err
			}
			s.refIndex.resources[item.ExternalRef.ID] = updated.ID
			return Change{Resource: "provider_resource", ID: updated.ID, Action: ActionUpdate}, nil
		}
	}
	created, err := s.store.AddProviderResource(spec)
	if err != nil {
		return Change{}, err
	}
	s.refIndex.resources[item.ExternalRef.ID] = created.ID
	return Change{Resource: "provider_resource", ID: created.ID, Action: ActionCreate}, nil
}

func (s *StoreSink) applyModel(item bundle.ModelRef) (Change, error) {
	spec := item.Spec
	for _, existing := range s.store.ListModels() {
		if existing.Name == spec.Name {
			if sameModel(existing, spec) {
				s.refIndex.models[item.ExternalRef.ID] = existing.Name
				return Change{Resource: "model", ID: existing.Name, Action: ActionSkip}, nil
			}
			updated, err := s.store.UpdateModel(existing.Name, spec)
			if err != nil {
				return Change{}, err
			}
			s.refIndex.models[item.ExternalRef.ID] = updated.Name
			return Change{Resource: "model", ID: updated.Name, Action: ActionUpdate}, nil
		}
	}
	created := s.store.AddModel(spec)
	s.refIndex.models[item.ExternalRef.ID] = created.Name
	return Change{Resource: "model", ID: created.Name, Action: ActionCreate}, nil
}

func (s *StoreSink) applyRoute(item bundle.RouteRef) (Change, error) {
	spec := item.Spec
	if modelName := s.refIndex.models[item.ModelRef]; modelName != "" {
		spec.ModelName = modelName
	}
	if providerID := s.refIndex.providers[item.ProviderRef]; providerID != "" {
		spec.ProviderID = providerID
	}
	if resourceID := s.refIndex.resources[item.ProviderResourceRef]; resourceID != "" {
		spec.ProviderResourceID = resourceID
	}
	for _, existing := range s.store.ListRoutes() {
		if existing.ModelName == spec.ModelName && existing.ProviderResourceID == spec.ProviderResourceID && existing.ProviderModel == spec.ProviderModel {
			if sameRoute(existing, spec) {
				s.refIndex.routes[item.ExternalRef.ID] = existing.ID
				return Change{Resource: "route", ID: existing.ID, Action: ActionSkip}, nil
			}
			updated, err := s.store.UpdateRoute(existing.ID, spec)
			if err != nil {
				return Change{}, err
			}
			s.refIndex.routes[item.ExternalRef.ID] = updated.ID
			return Change{Resource: "route", ID: updated.ID, Action: ActionUpdate}, nil
		}
	}
	created := s.store.AddRoute(spec)
	s.refIndex.routes[item.ExternalRef.ID] = created.ID
	return Change{Resource: "route", ID: created.ID, Action: ActionCreate}, nil
}

func (s *StoreSink) applyUser(item bundle.UserRef) (Change, error) {
	spec := item.Spec
	spec.TeamID = s.resolveTeamRef(item.TeamRef)
	for _, existing := range s.store.ListAdminUsers() {
		if sameUser(existing, spec) {
			if sameAdminUser(existing, spec) {
				s.refIndex.users[item.ExternalRef.ID] = existing.ID
				return Change{Resource: "user", ID: existing.ID, Action: ActionSkip}, nil
			}
			// The bundle carries a single team ref, so it does not own the
			// user's full team membership. The store rewrites TeamIDs from the
			// patch unconditionally, so the existing memberships have to be
			// carried over or an unrelated field change would drop them.
			spec.TeamIDs = existing.TeamIDs
			updated, err := s.store.UpdateAdminUser(existing.ID, spec, "")
			if err != nil {
				return Change{}, err
			}
			s.refIndex.users[item.ExternalRef.ID] = updated.ID
			return Change{Resource: "user", ID: updated.ID, Action: ActionUpdate}, nil
		}
	}
	created, err := s.store.CreateAdminUser(spec, "migration-password-placeholder")
	if err != nil {
		return Change{}, err
	}
	s.refIndex.users[item.ExternalRef.ID] = created.ID
	return Change{Resource: "user", ID: created.ID, Action: ActionCreate}, nil
}

func (s *StoreSink) applyProject(item bundle.ProjectRef) (Change, error) {
	spec := item.Spec
	spec.TeamID = s.resolveTeamRef(item.TeamRef)
	if userID := s.refIndex.users[spec.OwnerUserID]; userID != "" {
		spec.OwnerUserID = userID
	}
	for _, existing := range s.store.ListProjects() {
		if existing.Name == spec.Name && existing.TeamID == spec.TeamID {
			if sameProject(existing, spec) {
				s.refIndex.projects[item.ExternalRef.ID] = existing.ID
				return Change{Resource: "project", ID: existing.ID, Action: ActionSkip}, nil
			}
			updated, err := s.store.UpdateProject(existing.ID, spec)
			if err != nil {
				return Change{}, err
			}
			s.refIndex.projects[item.ExternalRef.ID] = updated.ID
			return Change{Resource: "project", ID: updated.ID, Action: ActionUpdate}, nil
		}
	}
	created, err := s.store.CreateProjectChecked(spec)
	if err != nil {
		return Change{}, err
	}
	s.refIndex.projects[item.ExternalRef.ID] = created.ID
	return Change{Resource: "project", ID: created.ID, Action: ActionCreate}, nil
}

func (s *StoreSink) applyAPIKey(item bundle.APIKeyRef) (Change, error) {
	spec := item.Spec
	projectID := spec.ProjectID
	if mapped := s.refIndex.projects[item.ProjectRef]; mapped != "" {
		projectID = mapped
	}
	spec.ProjectID = projectID
	for _, existing := range s.store.ListAPIKeys() {
		if existing.ProjectID == projectID && existing.Name == spec.Name {
			if sameAPIKey(existing, spec) {
				s.refIndex.apiKeys[item.ExternalRef.ID] = existing.ID
				return Change{Resource: "api_key", ID: existing.ID, Action: ActionSkip}, nil
			}
			if item.KeySecret != nil && !item.KeySecret.IsZero() {
				return Change{}, fmt.Errorf("api key %s already exists with different metadata; secret-backed updates are not supported", item.ExternalRef.ID)
			}
			updated, err := s.store.UpdateAPIKey(existing.ID, spec)
			if err != nil {
				return Change{}, err
			}
			s.refIndex.apiKeys[item.ExternalRef.ID] = updated.ID
			return Change{Resource: "api_key", ID: updated.ID, Action: ActionUpdate}, nil
		}
	}

	rawSecret := ""
	if item.KeySecret != nil && !item.KeySecret.IsZero() {
		resolved, err := s.secrets.Resolve(*item.KeySecret)
		if err != nil {
			return Change{}, fmt.Errorf("resolve api key secret %s: %w", item.ExternalRef.ID, err)
		}
		rawSecret = resolved
	}
	spec.ProjectID = projectID
	created, _, err := s.store.CreateAPIKey(projectID, spec, rawSecret)
	if err != nil {
		return Change{}, err
	}
	if rawSecret != "" {
		s.newKeys[item.ExternalRef.ID] = rawSecret
	}
	s.refIndex.apiKeys[item.ExternalRef.ID] = created.ID
	return Change{Resource: "api_key", ID: created.ID, Action: ActionCreate}, nil
}

func sameUser(left server.AdminUser, right server.AdminUser) bool {
	if strings.EqualFold(strings.TrimSpace(left.Email), strings.TrimSpace(right.Email)) && strings.TrimSpace(right.Email) != "" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(left.Username), strings.TrimSpace(right.Username)) && strings.TrimSpace(right.Username) != "" {
		return true
	}
	return false
}

func findProviderByBusinessKey(items []server.Provider, name string, providerType string) (server.Provider, bool) {
	for _, item := range items {
		if item.Name == name && item.Type == providerType {
			return item, true
		}
	}
	return server.Provider{}, false
}

func findProviderResourceByBusinessKey(items []server.ProviderResource, providerID string, name string) (server.ProviderResource, bool) {
	for _, item := range items {
		if item.ProviderID == providerID && item.Name == name {
			return item, true
		}
	}
	return server.ProviderResource{}, false
}

func findModelByName(items []server.Model, name string) (server.Model, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return server.Model{}, false
}

func findAdminUserByIdentity(items []server.AdminUser, spec server.AdminUser) (server.AdminUser, bool) {
	for _, item := range items {
		if sameUser(item, spec) {
			return item, true
		}
	}
	return server.AdminUser{}, false
}

func findProjectByBusinessKey(items []server.Project, name string, teamID string) (server.Project, bool) {
	for _, item := range items {
		if item.Name == name && item.TeamID == teamID {
			return item, true
		}
	}
	return server.Project{}, false
}

func findAPIKeyByBusinessKey(items []server.APIKey, projectID string, name string) (server.APIKey, bool) {
	for _, item := range items {
		if item.ProjectID == projectID && item.Name == name {
			return item, true
		}
	}
	return server.APIKey{}, false
}

func findRouteByBusinessKey(items []server.ModelRoute, modelName string, resourceID string, providerModel string) (server.ModelRoute, bool) {
	for _, item := range items {
		if item.ModelName == modelName && item.ProviderResourceID == resourceID && item.ProviderModel == providerModel {
			return item, true
		}
	}
	return server.ModelRoute{}, false
}

// sameProvider reports whether two providers are equivalent for idempotency
// checks. Credentials are cleared and CreatedAt is aliased so that only the
// remaining, migration-managed fields participate in the comparison.
// sameProvider reports whether applying desired onto existing would be a
// no-op. Like sameRoute it compares the fields the bundle owns instead of
// deep-equalling the struct: the target fills Healthy and Priority with
// defaults on create (true and 10), which would otherwise report drift on a
// provider that was just migrated and turn every re-apply into an update.
// Healthy is never compared at all — it is a runtime probe result, not
// migratable state. APIKey is write-only on the target and cannot be read
// back for comparison.
func sameProvider(existing server.Provider, desired server.Provider) bool {
	if desired.ID != "" && existing.ID != desired.ID {
		return false
	}
	if existing.Name != desired.Name || existing.Type != desired.Type {
		return false
	}
	if desired.BaseURL != "" && existing.BaseURL != desired.BaseURL {
		return false
	}
	if desired.Status != "" && existing.Status != desired.Status {
		return false
	}
	if desired.Priority != 0 && existing.Priority != desired.Priority {
		return false
	}
	return reflect.DeepEqual(normalizeHeaderMap(existing.Headers, existing.SensitiveHeaders), normalizeHeaderMap(desired.Headers, desired.SensitiveHeaders)) &&
		reflect.DeepEqual(normalizeHeaderNames(existing.SensitiveHeaders), normalizeHeaderNames(desired.SensitiveHeaders)) &&
		reflect.DeepEqual(migrationProviderOptions(existing.Options), migrationProviderOptions(desired.Options))
}

func providerUpdateSpec(existing server.Provider, desired server.Provider) server.Provider {
	merged := desired
	merged.Healthy = existing.Healthy
	if desired.BaseURL == "" {
		merged.BaseURL = existing.BaseURL
	}
	return merged
}

// sameProviderResource follows sameProvider: only the fields the bundle owns
// take part. The target defaults Healthy to true and Weight to 100 on create,
// and credential material, timestamps and observation state are either
// write-only or runtime-owned.
func sameProviderResource(existing server.ProviderResource, desired server.ProviderResource) bool {
	if desired.ID != "" && existing.ID != desired.ID {
		return false
	}
	if existing.Name != desired.Name || existing.ProviderID != desired.ProviderID {
		return false
	}
	if desired.ResourceType != "" && existing.ResourceType != desired.ResourceType {
		return false
	}
	if desired.BaseURL != "" && existing.BaseURL != desired.BaseURL {
		return false
	}
	if desired.Status != "" && existing.Status != desired.Status {
		return false
	}
	if desired.Group != "" && existing.Group != desired.Group {
		return false
	}
	if desired.Region != "" && existing.Region != desired.Region {
		return false
	}
	if desired.Environment != "" && existing.Environment != desired.Environment {
		return false
	}
	if desired.Priority != 0 && existing.Priority != desired.Priority {
		return false
	}
	if desired.Weight != 0 && existing.Weight != desired.Weight {
		return false
	}
	if desired.RateLimitRPM != 0 && existing.RateLimitRPM != desired.RateLimitRPM {
		return false
	}
	if desired.TokenLimitTPM != 0 && existing.TokenLimitTPM != desired.TokenLimitTPM {
		return false
	}
	if desired.MaxConcurrency != 0 && existing.MaxConcurrency != desired.MaxConcurrency {
		return false
	}
	return reflect.DeepEqual(normalizeHeaderMap(existing.Headers, existing.SensitiveHeaders), normalizeHeaderMap(desired.Headers, desired.SensitiveHeaders)) &&
		reflect.DeepEqual(normalizeHeaderNames(existing.SensitiveHeaders), normalizeHeaderNames(desired.SensitiveHeaders)) &&
		reflect.DeepEqual(normalizeStringMap(existing.Options), normalizeStringMap(desired.Options))
}

func providerResourceUpdateSpec(existing server.ProviderResource, desired server.ProviderResource) server.ProviderResource {
	merged := desired
	merged.Healthy = existing.Healthy
	if desired.BaseURL == "" {
		merged.BaseURL = existing.BaseURL
	}
	if desired.Region == "" {
		merged.Region = existing.Region
	}
	if desired.Environment == "" {
		merged.Environment = existing.Environment
	}
	if desired.RateLimitRPM == 0 {
		merged.RateLimitRPM = existing.RateLimitRPM
	}
	if desired.TokenLimitTPM == 0 {
		merged.TokenLimitTPM = existing.TokenLimitTPM
	}
	if desired.MaxConcurrency == 0 {
		merged.MaxConcurrency = existing.MaxConcurrency
	}
	return merged
}

// metadataContains reports whether every key the bundle declares is present on
// the target with the same value. The target owns metadata keys of its own —
// it stamps directory_role onto a migrated model, for instance — so comparing
// the maps for equality would report drift forever and make every re-apply an
// update that changes nothing.
func metadataContains(existing map[string]string, desired map[string]string) bool {
	current := normalizeStringMap(existing)
	for key, want := range normalizeStringMap(desired) {
		if got, ok := current[key]; !ok || got != want {
			return false
		}
	}
	return true
}

func sameModel(left server.Model, right server.Model) bool {
	keepsString := func(current, desired string) bool {
		desired = strings.TrimSpace(desired)
		return desired == "" || strings.TrimSpace(current) == desired
	}
	if !keepsString(left.Name, right.Name) ||
		!keepsString(left.Family, right.Family) ||
		!keepsString(left.Modality, right.Modality) ||
		(right.ContextWindow != 0 && left.ContextWindow != right.ContextWindow) ||
		left.InputPriceUSDPer1M != right.InputPriceUSDPer1M ||
		left.OutputPriceUSDPer1M != right.OutputPriceUSDPer1M ||
		left.EmbeddingPriceUSDPer1M != right.EmbeddingPriceUSDPer1M ||
		!keepsString(left.Status, right.Status) {
		return false
	}
	expectedCacheReadPrice := right.CacheReadPriceUSDPer1M
	effectiveModality := strings.TrimSpace(right.Modality)
	if effectiveModality == "" {
		effectiveModality = strings.TrimSpace(left.Modality)
	}
	if effectiveModality == "embedding" {
		expectedCacheReadPrice = 0
	}
	if left.CacheReadPriceUSDPer1M != expectedCacheReadPrice {
		return false
	}
	if right.InputModalities != nil && !reflect.DeepEqual(normalizeStringSlice(left.InputModalities), normalizeStringSlice(right.InputModalities)) {
		return false
	}
	if right.OutputModalities != nil && !reflect.DeepEqual(normalizeStringSlice(left.OutputModalities), normalizeStringSlice(right.OutputModalities)) {
		return false
	}
	if right.Capabilities != nil && !reflect.DeepEqual(normalizeStringSlice(left.Capabilities), normalizeStringSlice(right.Capabilities)) {
		return false
	}
	if right.SupportedParameters != nil && !reflect.DeepEqual(normalizeStringSlice(left.SupportedParameters), normalizeStringSlice(right.SupportedParameters)) {
		return false
	}
	if right.Metadata != nil && !metadataContains(left.Metadata, right.Metadata) {
		return false
	}
	// ID, Category and CreatedAt are not mutable through PATCH /api/admin/models
	// and therefore cannot participate in convergence checks.
	return true
}

// mergeRouteUpdate mirrors the store's UpdateRoute merge rules: a zero value
// in the desired spec means "keep the current value", not "reset it".
func mergeRouteUpdate(existing server.ModelRoute, desired server.ModelRoute) server.ModelRoute {
	merged := existing
	if desired.ModelName != "" {
		merged.ModelName = desired.ModelName
	}
	if desired.ProviderID != "" {
		merged.ProviderID = desired.ProviderID
	}
	if desired.ProviderModel != "" {
		merged.ProviderModel = desired.ProviderModel
	}
	if desired.Status != "" {
		merged.Status = desired.Status
	}
	if desired.Strategy != "" {
		merged.Strategy = desired.Strategy
	}
	if desired.Priority != 0 {
		merged.Priority = desired.Priority
	}
	// A non-positive weight is what AddRoute normalizes to 1, so it is treated
	// as "unspecified" here instead of following UpdateRoute literally, which
	// would let a negative weight through and make create and update disagree.
	if desired.Weight > 0 {
		merged.Weight = desired.Weight
	}
	if desired.QualityScore != 0 {
		merged.QualityScore = desired.QualityScore
	}
	if desired.CostScore != 0 {
		merged.CostScore = desired.CostScore
	}
	merged.ProviderResourceID = desired.ProviderResourceID
	merged.ResourceGroup = desired.ResourceGroup
	merged.StickySession = desired.StickySession
	if desired.Tags != nil {
		merged.Tags = desired.Tags
	}
	// ProjectScope and ProjectIDs are deliberately left untouched: the bundle
	// schema does not carry them, so the migration does not own them. Defaulting
	// an unspecified scope to "all" here would widen project access on any
	// target route already scoped to include/exclude.
	return merged
}

// sameRoute reports whether applying desired onto existing would be a no-op.
//
// It compares the merged result field by field rather than deep-equalling the
// whole struct: ModelRoute carries server-owned fields the bundle never sets
// (and gains more over time, as ProjectScope/ProjectIDs did), and the store
// fills those with defaults on create. Deep equality therefore reported drift
// on every re-apply and broke idempotence.
func sameRoute(existing server.ModelRoute, desired server.ModelRoute) bool {
	merged := mergeRouteUpdate(existing, desired)
	return existing.ModelName == merged.ModelName &&
		existing.ProviderID == merged.ProviderID &&
		existing.ProviderResourceID == merged.ProviderResourceID &&
		existing.ResourceGroup == merged.ResourceGroup &&
		existing.StickySession == merged.StickySession &&
		existing.ProviderModel == merged.ProviderModel &&
		existing.Priority == merged.Priority &&
		existing.Weight == merged.Weight &&
		existing.QualityScore == merged.QualityScore &&
		existing.CostScore == merged.CostScore &&
		existing.Status == merged.Status &&
		existing.Strategy == merged.Strategy &&
		existing.ProjectScope == merged.ProjectScope &&
		slices.Equal(existing.ProjectIDs, merged.ProjectIDs) &&
		slices.Equal(existing.Tags, merged.Tags)
}

// sameAdminUser reports whether applying desired onto existing would be a
// no-op. Empty desired fields mean "keep the current value", mirroring the
// server-side update semantics — except TeamID, which the server overwrites
// unconditionally and therefore must match exactly.
func sameAdminUser(existing server.AdminUser, desired server.AdminUser) bool {
	keeps := func(current, want string) bool {
		want = strings.TrimSpace(want)
		return want == "" || strings.TrimSpace(current) == want
	}
	return keeps(existing.Username, desired.Username) &&
		keeps(existing.Name, desired.Name) &&
		keeps(existing.Email, desired.Email) &&
		(normalizeMigrationAdminRole(desired.Role) == "" || normalizeMigrationAdminRole(existing.Role) == normalizeMigrationAdminRole(desired.Role)) &&
		strings.TrimSpace(existing.TeamID) == strings.TrimSpace(desired.TeamID) &&
		keeps(existing.Status, desired.Status)
}

func normalizeMigrationAdminRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "security":
		return "security_admin"
	case "project_admin", "teamlead":
		return "team_leader"
	case "viewer", "readonly", "read_only", "member":
		return "user"
	default:
		return role
	}
}

func sameProject(left server.Project, right server.Project) bool {
	if strings.TrimSpace(right.ModelAccessMode) != "" || right.AllowedModels != nil {
		leftMode, leftAllowed := normalizedMigrationModelAccess(left.ModelAccessMode, left.AllowedModels)
		rightMode, rightAllowed := normalizedMigrationModelAccess(right.ModelAccessMode, right.AllowedModels)
		if leftMode != rightMode || !slices.Equal(leftAllowed, rightAllowed) {
			return false
		}
	}
	return strings.TrimSpace(left.Name) == strings.TrimSpace(right.Name) &&
		strings.TrimSpace(left.TeamID) == strings.TrimSpace(right.TeamID) &&
		strings.TrimSpace(left.OwnerUserID) == strings.TrimSpace(right.OwnerUserID) &&
		strings.TrimSpace(left.CostCenter) == strings.TrimSpace(right.CostCenter) &&
		strings.TrimSpace(left.Status) == strings.TrimSpace(right.Status) &&
		strings.TrimSpace(left.DefaultQuotaRef) == strings.TrimSpace(right.DefaultQuotaRef)
}

func sameAPIKey(left server.APIKey, right server.APIKey) bool {
	if right.Group == "" {
		right.Group = "default"
	}
	if right.Status == "" {
		right.Status = server.StatusActive
	}
	if strings.TrimSpace(left.ProjectID) != strings.TrimSpace(right.ProjectID) ||
		strings.TrimSpace(left.Name) != strings.TrimSpace(right.Name) ||
		strings.TrimSpace(left.Group) != strings.TrimSpace(right.Group) ||
		strings.TrimSpace(left.Status) != strings.TrimSpace(right.Status) {
		return false
	}
	if right.Allowed != nil && !reflect.DeepEqual(normalizeStringSlice(left.Allowed), normalizeStringSlice(right.Allowed)) {
		return false
	}
	if strings.TrimSpace(right.ModelAccessMode) != "" {
		leftMode, leftAllowed := normalizedMigrationModelAccess(left.ModelAccessMode, left.Allowed)
		rightMode, rightAllowed := normalizedMigrationModelAccess(right.ModelAccessMode, right.Allowed)
		if leftMode != rightMode || !slices.Equal(leftAllowed, rightAllowed) {
			return false
		}
	}
	if right.IPAllowlist != nil && !reflect.DeepEqual(normalizeStringSlice(left.IPAllowlist), normalizeStringSlice(right.IPAllowlist)) {
		return false
	}
	if right.Limits != (server.QuotaLimits{}) && !reflect.DeepEqual(left.Limits, right.Limits) {
		return false
	}
	if right.RateLimitRPM != nil && !reflect.DeepEqual(left.RateLimitRPM, right.RateLimitRPM) {
		return false
	}
	if right.TokenLimitTPM != nil && !reflect.DeepEqual(left.TokenLimitTPM, right.TokenLimitTPM) {
		return false
	}
	if right.ExpiresAt != nil && !reflect.DeepEqual(left.ExpiresAt, right.ExpiresAt) {
		return false
	}
	// Metadata is server-owned for Admin API key writes (created_by,
	// created_by_role) and UpdateAPIKey does not accept metadata changes.
	return true
}

func normalizeStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	return input
}

func normalizeStringSlice(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	return input
}

func normalizeHeaderNames(input []string) []string {
	seen := make(map[string]bool, len(input))
	result := make([]string, 0, len(input))
	for _, value := range input {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	slices.Sort(result)
	if len(result) == 0 {
		return nil
	}
	return result
}

func resolveHeaderSecrets(resolver bundle.SecretResolver, headers map[string]string, refs map[string]bundle.SecretRef) (map[string]string, []string, error) {
	resolved, sensitive, err := headerSecretComparisonConfig(headers, refs)
	if err != nil {
		return nil, nil, err
	}
	for _, name := range sensitive {
		secret, err := resolver.Resolve(refs[name])
		if err != nil {
			return nil, nil, fmt.Errorf("resolve header %q: %w", name, err)
		}
		resolved[name] = secret
	}
	return resolved, sensitive, nil
}

func headerSecretComparisonConfig(headers map[string]string, refs map[string]bundle.SecretRef) (map[string]string, []string, error) {
	resolved := make(map[string]string, len(headers)+len(refs))
	for name, value := range headers {
		resolved[name] = value
	}
	if len(refs) == 0 {
		if len(resolved) == 0 {
			return nil, nil, nil
		}
		return resolved, nil, nil
	}
	names := make([]string, 0, len(refs))
	for name := range refs {
		names = append(names, name)
	}
	slices.Sort(names)
	sensitive := make([]string, 0, len(names))
	for _, name := range names {
		for existing := range resolved {
			if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(name)) {
				return nil, nil, fmt.Errorf("header %q is declared as both plaintext and sensitive", name)
			}
		}
		resolved[name] = "<sensitive>"
		sensitive = append(sensitive, name)
	}
	return resolved, sensitive, nil
}

func normalizeHeaderMap(headers map[string]string, sensitive []string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	sensitiveSet := make(map[string]bool, len(sensitive))
	for _, name := range normalizeHeaderNames(sensitive) {
		sensitiveSet[name] = true
	}
	result := make(map[string]string, len(headers))
	for name, value := range headers {
		name = strings.ToLower(strings.TrimSpace(name))
		if sensitiveSet[name] {
			value = "<sensitive>"
		}
		result[name] = value
	}
	return result
}

func normalizedMigrationModelAccess(mode string, allowed []string) (string, []string) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	seen := map[string]bool{}
	values := make([]string, 0, len(allowed))
	for _, value := range allowed {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	slices.Sort(values)
	if mode == "" {
		if len(values) > 0 {
			mode = server.ModelAccessModeRestricted
		} else {
			mode = server.ModelAccessModeInherit
		}
	}
	if mode == server.ModelAccessModeInherit {
		values = nil
	}
	return mode, values
}
