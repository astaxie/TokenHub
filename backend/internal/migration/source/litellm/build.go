package litellm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"tokenhub/backend/internal/migration/bundle"
	"tokenhub/backend/internal/server"
)

func mustMintID(strategy bundle.IDStrategy, externalRef string) string {
	id, err := bundle.MintID(strategy, AdapterName, externalRef)
	if err != nil {
		panic(err)
	}
	return id
}

type BuildOptions struct {
	OriginURL  string
	Metadata   map[string]string
	IDStrategy bundle.IDStrategy
}

func BuildBundle(config *Config, opts BuildOptions) (*bundle.CanonicalMigrationBundle, error) {
	if config == nil {
		return nil, fmt.Errorf("litellm: config is required")
	}
	if err := ValidateSupportedVersion(config.LitellmSettings.Version); err != nil {
		return nil, err
	}
	idStrategy := opts.IDStrategy
	if idStrategy == "" {
		idStrategy = bundle.IDStrategyPrefixed
	}
	if !idStrategy.Valid() {
		return nil, fmt.Errorf("litellm: invalid ID strategy %q", idStrategy)
	}

	b := &bundle.CanonicalMigrationBundle{
		SchemaVersion: bundle.SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Source: bundle.Source{
			Adapter:        AdapterName,
			AdapterVersion: AdapterVersion,
			OriginURL:      opts.OriginURL,
			Metadata:       cloneMetadata(opts.Metadata),
		},
	}

	b.Teams = buildTeams(config, idStrategy)
	b.Users = buildUsers(config, b, idStrategy)
	b.Projects = buildProjects(config, b, idStrategy)
	b.APIKeys = buildAPIKeys(config, b, idStrategy)
	providers, resources, models, routes := buildModelArtifacts(config, b, idStrategy)
	b.Providers = providers
	b.ProviderResources = resources
	b.Models = models
	b.Routes = routes

	if len(config.KeyManagement.Budgets) > 0 {
		warn(b, "litellm_budget_ignored", "key_management_settings.budgets is not mapped yet", "key_management_settings.budgets")
	}
	if len(config.GeneralSettings) > 0 {
		warn(b, "litellm_general_settings_partial", "general_settings is not fully migrated", "general_settings")
	}
	if len(config.RouterSettings) > 0 {
		warn(b, "litellm_router_settings_partial", "router_settings is only partially represented via routes", "router_settings")
	}

	if err := bundle.Validate(b); err != nil {
		return nil, err
	}
	return b, nil
}

func buildTeams(config *Config, idStrategy bundle.IDStrategy) []bundle.TeamRef {
	teams := make([]bundle.TeamRef, 0, len(config.KeyManagement.Teams))
	seen := map[string]struct{}{}
	add := func(teamID string, name string) {
		teamID = strings.TrimSpace(teamID)
		if teamID == "" {
			return
		}
		if _, ok := seen[teamID]; ok {
			return
		}
		seen[teamID] = struct{}{}
		externalRef := teamExternalRef(teamID)
		teams = append(teams, bundle.TeamRef{
			ExternalRef: bundle.ExternalRef{System: AdapterName, ID: externalRef},
			ID:          mustMintID(idStrategy, externalRef),
			Name:        fallback(name, teamID),
		})
	}
	for _, team := range config.KeyManagement.Teams {
		add(team.TeamID, team.TeamAlias)
	}
	for _, user := range config.KeyManagement.Users {
		add(user.TeamID, "")
	}
	for _, key := range config.KeyManagement.VirtualKeys {
		add(key.TeamID, "")
	}
	return teams
}

func buildUsers(config *Config, b *bundle.CanonicalMigrationBundle, idStrategy bundle.IDStrategy) []bundle.UserRef {
	users := make([]bundle.UserRef, 0, len(config.KeyManagement.Users))
	seen := map[string]struct{}{}
	for _, user := range config.KeyManagement.Users {
		userID := strings.TrimSpace(user.UserID)
		if userID == "" {
			warn(b, "litellm_user_skipped", "user without user_id was skipped", "key_management_settings.users")
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		email := strings.TrimSpace(user.UserEmail)
		if email == "" {
			warn(b, "litellm_user_skipped", "user without email was skipped", "key_management_settings.users")
			continue
		}
		username := sanitizeUsername(firstNonEmpty(user.UserAlias, email, userID))
		users = append(users, bundle.UserRef{
			ExternalRef: bundle.ExternalRef{System: AdapterName, ID: userExternalRef(userID)},
			TeamRef:     optionalTeamRef(user.TeamID),
			Spec: server.AdminUser{
				ID:       mustMintID(idStrategy, userExternalRef(userID)),
				Username: username,
				Name:     firstNonEmpty(user.UserAlias, username),
				Email:    email,
				Role:     "viewer",
				Status:   "active",
			},
		})
	}
	return users
}

func buildProjects(config *Config, b *bundle.CanonicalMigrationBundle, idStrategy bundle.IDStrategy) []bundle.ProjectRef {
	projects := make([]bundle.ProjectRef, 0, len(config.KeyManagement.Teams))
	seen := map[string]struct{}{}
	teamAliases := map[string]string{}
	for _, team := range config.KeyManagement.Teams {
		teamID := strings.TrimSpace(team.TeamID)
		if teamID != "" {
			teamAliases[teamID] = strings.TrimSpace(team.TeamAlias)
		}
	}
	for _, team := range config.KeyManagement.Teams {
		teamID := strings.TrimSpace(team.TeamID)
		if teamID == "" {
			continue
		}
		projectID := teamProjectExternalRef(teamID)
		if _, ok := seen[projectID]; ok {
			continue
		}
		seen[projectID] = struct{}{}
		projects = append(projects, bundle.ProjectRef{
			ExternalRef: bundle.ExternalRef{System: AdapterName, ID: projectID},
			TeamRef:     teamExternalRef(teamID),
			Spec: server.Project{
				ID:     mustMintID(idStrategy, projectID),
				Name:   firstNonEmpty(team.TeamAlias, teamID),
				Status: "active",
			},
		})
	}
	for _, user := range config.KeyManagement.Users {
		teamID := strings.TrimSpace(user.TeamID)
		if teamID == "" {
			continue
		}
		projectID := teamProjectExternalRef(teamID)
		if _, ok := seen[projectID]; ok {
			continue
		}
		seen[projectID] = struct{}{}
		projects = append(projects, bundle.ProjectRef{
			ExternalRef: bundle.ExternalRef{System: AdapterName, ID: projectID},
			TeamRef:     teamExternalRef(teamID),
			Spec: server.Project{
				ID:     mustMintID(idStrategy, projectID),
				Name:   firstNonEmpty(teamAliases[teamID], teamID),
				Status: "active",
			},
		})
	}
	for _, key := range config.KeyManagement.VirtualKeys {
		teamID := strings.TrimSpace(key.TeamID)
		if teamID == "" {
			continue
		}
		projectID := teamProjectExternalRef(teamID)
		if _, ok := seen[projectID]; ok {
			continue
		}
		seen[projectID] = struct{}{}
		projects = append(projects, bundle.ProjectRef{
			ExternalRef: bundle.ExternalRef{System: AdapterName, ID: projectID},
			TeamRef:     teamExternalRef(teamID),
			Spec: server.Project{
				ID:     mustMintID(idStrategy, projectID),
				Name:   firstNonEmpty(teamAliases[teamID], teamID),
				Status: "active",
			},
		})
	}
	for _, key := range config.KeyManagement.VirtualKeys {
		if strings.TrimSpace(key.TeamID) != "" {
			continue
		}
		alias := keyIdentifier(key)
		if strings.TrimSpace(alias) == "" {
			warn(b, "litellm_key_project_skipped", "virtual key without alias or token was skipped", "key_management_settings.virtual_keys")
			continue
		}
		projectID := keyProjectExternalRef(alias)
		if _, ok := seen[projectID]; ok {
			continue
		}
		seen[projectID] = struct{}{}
		projects = append(projects, bundle.ProjectRef{
			ExternalRef: bundle.ExternalRef{System: AdapterName, ID: projectID},
			Spec: server.Project{
				ID:     mustMintID(idStrategy, projectID),
				Name:   alias,
				Status: "active",
			},
		})
	}
	return projects
}

func buildAPIKeys(config *Config, b *bundle.CanonicalMigrationBundle, idStrategy bundle.IDStrategy) []bundle.APIKeyRef {
	keys := make([]bundle.APIKeyRef, 0, len(config.KeyManagement.VirtualKeys))
	seen := map[string]struct{}{}
	for _, key := range config.KeyManagement.VirtualKeys {
		externalID := keyIdentifier(key)
		externalID = strings.TrimSpace(externalID)
		if externalID == "" {
			warn(b, "litellm_key_skipped", "virtual key without alias or token was skipped", "key_management_settings.virtual_keys")
			continue
		}
		if _, ok := seen[externalID]; ok {
			continue
		}
		seen[externalID] = struct{}{}
		metadata := map[string]string{}
		if key.TeamID != "" {
			metadata["litellm_team_id"] = key.TeamID
		}
		if key.UserID != "" {
			metadata["litellm_user_id"] = key.UserID
			metadata["litellm_user_ref"] = userExternalRef(key.UserID)
		}
		if key.SpendDuration != "" {
			metadata["litellm_budget_duration"] = key.SpendDuration
		}
		projectRef := keyProjectRef(key)
		apiKey := bundle.APIKeyRef{
			ExternalRef: bundle.ExternalRef{System: AdapterName, ID: apiKeyExternalRef(externalID)},
			ProjectRef:  projectRef,
			Spec: server.APIKey{
				ID:        mustMintID(idStrategy, apiKeyExternalRef(externalID)),
				Name:      externalID,
				Allowed:   cloneStrings(key.Models),
				Status:    "active",
				Limits:    buildQuotaLimits(key),
				Metadata:  metadata,
				ExpiresAt: parseTimestamp(key.Expires, b, "key_management_settings.virtual_keys.expires"),
			},
		}
		if strings.TrimSpace(key.Token) != "" {
			apiKey.KeySecret = &bundle.SecretRef{Ref: secretName("litellm_key", externalID)}
		}
		keys = append(keys, apiKey)
	}
	return keys
}

func buildModelArtifacts(config *Config, b *bundle.CanonicalMigrationBundle, idStrategy bundle.IDStrategy) ([]bundle.ProviderRef, []bundle.ProviderResourceRef, []bundle.ModelRef, []bundle.RouteRef) {
	providerIndex := map[string]int{}
	resourceIndex := map[string]int{}
	modelIndex := map[string]int{}
	providers := []bundle.ProviderRef{}
	resources := []bundle.ProviderResourceRef{}
	models := []bundle.ModelRef{}
	routes := make([]bundle.RouteRef, 0, len(config.ModelList))

	for idx, item := range config.ModelList {
		modelName := strings.TrimSpace(item.ModelName)
		providerModel := strings.TrimSpace(item.LitellmParams.Model)
		providerType, providerName := splitProviderModel(providerModel)
		upstreamModel := providerName
		if modelName == "" || providerType == "" || providerName == "" {
			warn(b, "litellm_model_skipped", "model entry missing model_name or litellm_params.model", fmt.Sprintf("model_list[%d]", idx))
			continue
		}

		providerRefID := providerExternalRef(providerType, item.LitellmParams.APIBase, item.LitellmParams.APIKey)
		providerPos, ok := providerIndex[providerRefID]
		if !ok {
			providerSecretRef, providerSecretWarning := providerSecretRef(item.LitellmParams.APIKey, providerRefID)
			provider := bundle.ProviderRef{
				ExternalRef: bundle.ExternalRef{System: AdapterName, ID: providerRefID},
				Spec: server.Provider{
					ID:       mustMintID(idStrategy, providerRefID),
					Name:     providerType,
					Type:     providerType,
					BaseURL:  strings.TrimSpace(item.LitellmParams.APIBase),
					Status:   "active",
					Healthy:  true,
					Priority: 100,
					Options:  stringMap(item.ModelInfo),
				},
			}
			if providerSecretRef != nil {
				provider.APIKeySecret = providerSecretRef
			}
			if providerSecretWarning != "" {
				warn(b, "litellm_secret_partial", providerSecretWarning, fmt.Sprintf("model_list[%d].litellm_params.api_key", idx))
			}
			providerPos = len(providers)
			providerIndex[providerRefID] = providerPos
			providers = append(providers, provider)
		}

		resourceRefID := providerResourceExternalRef(providerType, providerName, item.LitellmParams.APIBase, item.LitellmParams.RegionName)
		resourcePos, hasResource := resourceIndex[resourceRefID]
		if item.LitellmParams.APIBase != "" || item.LitellmParams.RegionName != "" || item.LitellmParams.RPM > 0 || item.LitellmParams.TPM > 0 {
			if !hasResource {
				resourceSecretRef, resourceSecretWarning := providerSecretRef(item.LitellmParams.APIKey, resourceRefID)
				resource := bundle.ProviderResourceRef{
					ExternalRef: bundle.ExternalRef{System: AdapterName, ID: resourceRefID},
					ProviderRef: providerRefID,
					Spec: server.ProviderResource{
						ID:            mustMintID(idStrategy, resourceRefID),
						Name:          providerName,
						ResourceType:  "model-endpoint",
						BaseURL:       strings.TrimSpace(item.LitellmParams.APIBase),
						Region:        strings.TrimSpace(item.LitellmParams.RegionName),
						Status:        "active",
						Healthy:       true,
						Priority:      100,
						Weight:        defaultWeight(item.LitellmParams.Weight),
						RateLimitRPM:  item.LitellmParams.RPM,
						TokenLimitTPM: item.LitellmParams.TPM,
						Options:       stringMap(item.LitellmParams.Extra),
					},
				}
				if resourceSecretRef != nil {
					resource.APIKeySecret = resourceSecretRef
				}
				if resourceSecretWarning != "" {
					warn(b, "litellm_secret_partial", resourceSecretWarning, fmt.Sprintf("model_list[%d].litellm_params.api_key", idx))
				}
				resourcePos = len(resources)
				resourceIndex[resourceRefID] = resourcePos
				resources = append(resources, resource)
				hasResource = true
			}
		}

		modelRefID := modelExternalRef(modelName)
		if _, ok := modelIndex[modelRefID]; !ok {
			models = append(models, bundle.ModelRef{
				ExternalRef: bundle.ExternalRef{System: AdapterName, ID: modelRefID},
				Spec: server.Model{
					ID:       mustMintID(idStrategy, modelRefID),
					Name:     modelName,
					Family:   providerType,
					Modality: inferModality(modelName),
					Category: inferCategory(modelName),
					Status:   "active",
					Metadata: stringMap(item.ModelInfo),
				},
			})
			modelIndex[modelRefID] = len(models) - 1
		}

		route := bundle.RouteRef{
			ExternalRef: bundle.ExternalRef{System: AdapterName, ID: routeExternalRef(modelName, providerModel, resourceRefID)},
			ModelRef:    modelRefID,
			ProviderRef: providerRefID,
			Spec: server.ModelRoute{
				ID:            mustMintID(idStrategy, routeExternalRef(modelName, providerModel, resourceRefID)),
				ModelName:     modelName,
				ProviderModel: upstreamModel,
				Priority:      100,
				Weight:        defaultWeight(item.LitellmParams.Weight),
				Status:        "active",
				// The route carries both a priority and a weight, so it maps
				// onto the priority_weighted strategy. "weighted" is not one
				// of the strategies TokenHub accepts and the Admin API
				// rejects it outright.
				Strategy: server.RouteStrategyPriorityWeighted,
			},
		}
		if hasResource {
			route.ProviderResourceRef = resourceRefID
			route.Spec.ResourceGroup = resources[resourcePos].Spec.Group
		}
		if item.LitellmParams.RPM == 0 && item.LitellmParams.TPM == 0 && item.LitellmParams.APIBase == "" {
			_ = providers[providerPos]
		}
		routes = append(routes, route)
	}

	return providers, resources, models, routes
}

func buildQuotaLimits(key VirtualKeyConfig) server.QuotaLimits {
	limits := server.QuotaLimits{}
	// LiteLLM rpm/tpm are deliberately not mapped here: they are rate limits,
	// not fixed daily quotas. They are preserved as metadata warnings
	// elsewhere rather than rewritten into different semantics.
	if key.MaxBudget > 0 {
		limits.MonthlyCostUSD = key.MaxBudget
	}
	return limits
}

func parseTimestamp(raw string, b *bundle.CanonicalMigrationBundle, path string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05", time.DateOnly}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			t := parsed.UTC()
			return &t
		}
	}
	warn(b, "litellm_time_unparsed", "timestamp could not be parsed and was ignored", path)
	return nil
}

func warn(b *bundle.CanonicalMigrationBundle, code string, message string, path string) {
	b.Warnings = append(b.Warnings, bundle.Warning{Severity: bundle.SeverityWarn, Code: code, Message: message, Path: path})
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func stringMap(values map[string]any) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range values {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func splitProviderModel(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func inferModality(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "embed"):
		return "embedding"
	case strings.Contains(name, "image"), strings.Contains(name, "vision"):
		return "multimodal"
	default:
		return "text"
	}
}

func inferCategory(name string) string {
	name = strings.ToLower(name)
	if strings.Contains(name, "embed") {
		return "embedding"
	}
	return "llm"
}

func defaultWeight(weight int) int {
	if weight <= 0 {
		return 100
	}
	return weight
}

func sanitizeUsername(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	replacer := strings.NewReplacer("@", "-", ".", "-", "_", "-", " ", "-")
	raw = replacer.Replace(raw)
	raw = strings.Trim(raw, "-")
	if raw == "" {
		return "user"
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func fallback(primary string, secondary string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(secondary)
}

func providerExternalRef(providerType string, apiBase string, apiKey string) string {
	key := providerType
	if strings.TrimSpace(apiBase) != "" {
		key += ":" + strings.TrimSpace(apiBase)
	}
	if ref, _ := providerSecretRef(apiKey, ""); ref != nil {
		key += ":" + ref.Ref
	}
	return "provider:" + key
}
func providerResourceExternalRef(providerType string, providerName string, apiBase string, region string) string {
	key := providerType + ":" + providerName
	if strings.TrimSpace(apiBase) != "" {
		key += ":" + strings.TrimSpace(apiBase)
	}
	if strings.TrimSpace(region) != "" {
		key += ":" + strings.TrimSpace(region)
	}
	return "resource:" + key
}
func modelExternalRef(modelName string) string { return "model:" + modelName }
func routeExternalRef(modelName string, providerModel string, resourceRef string) string {
	return fmt.Sprintf("route:%s:%s:%s", modelName, providerModel, resourceRef)
}
func teamExternalRef(teamID string) string        { return "team:" + strings.TrimSpace(teamID) }
func userExternalRef(userID string) string        { return "user:" + strings.TrimSpace(userID) }
func teamProjectExternalRef(teamID string) string { return "project:team:" + strings.TrimSpace(teamID) }
func keyProjectExternalRef(alias string) string   { return "project:key:" + strings.TrimSpace(alias) }
func apiKeyExternalRef(alias string) string       { return "api_key:" + strings.TrimSpace(alias) }
func optionalTeamRef(teamID string) string {
	if strings.TrimSpace(teamID) == "" {
		return ""
	}
	return teamExternalRef(teamID)
}
func keyProjectRef(key VirtualKeyConfig) string {
	if strings.TrimSpace(key.TeamID) != "" {
		return teamProjectExternalRef(key.TeamID)
	}
	return keyProjectExternalRef(keyIdentifier(key))
}

// keyIdentifier returns a non-secret identifier for a virtual key. It prefers
// the operator-provided alias; when the alias is absent it derives a synthetic
// identifier from a hash of the token so the raw secret never appears in
// external refs, resource IDs, names, or secret env names.
func keyIdentifier(key VirtualKeyConfig) string {
	if alias := strings.TrimSpace(key.KeyAlias); alias != "" {
		return alias
	}
	token := strings.TrimSpace(key.Token)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return "key-" + hex.EncodeToString(sum[:6])
}
func secretName(prefix string, value string) string {
	value = strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(value, ":", "_"), "-", "_"), "/", "_"))
	return "TOKENHUB_MIGRATION_" + strings.ToUpper(prefix) + "_" + value
}

func providerSecretRef(raw string, fallbackValue string) (*bundle.SecretRef, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}
	if strings.HasPrefix(raw, "os.environ/") {
		envName := strings.TrimSpace(strings.TrimPrefix(raw, "os.environ/"))
		if envName == "" {
			return nil, "empty os.environ secret reference"
		}
		return &bundle.SecretRef{Ref: envName}, ""
	}
	return &bundle.SecretRef{Ref: secretName("provider", fallbackValue)}, "inline api_key requires operator-provided secret materialization"
}
