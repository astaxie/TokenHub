package server

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const (
	providerResourceReauthorizationRequiredOption = "oauth_reauthorization_required"
	openAIAccountReauthorizationRequiredOption    = providerResourceReauthorizationRequiredOption
	providerAccountCredentialSourceFallback       = "provider_account"
)

var providerAccountProtectedOptions = []string{
	"credential_source",
	"auth_type",
	"token_expires_at",
	"account_id",
	"account_email",
	"user_id",
	"organization_id",
	"plan_type",
	"has_refresh_token",
	providerResourceSupportedModelsOption,
	providerResourceModelsFetchedAtOption,
	providerResourceModelsETagOption,
	providerResourceModelCatalogOption,
	codexResourceSupportedModelsOption,
	codexResourceModelsFetchedAtOption,
	codexResourceModelsETagOption,
	codexResourceModelCatalogOption,
	providerResourceReauthorizationRequiredOption,
}

func (s *GormStore) prepareProviderResourceForCreate(providerType string, resource *ProviderResource) {
	if resource == nil || !s.IsProviderAccountResourceType(providerType, resource.ResourceType) {
		return
	}
	if !s.providerResourceCredentialInputOptional(providerType, resource.ResourceType) && !providerResourceCredentialInputPresent(*resource) {
		return
	}
	s.applyProviderResourceTypeDefaults(providerType, resource)
	if resource.Options == nil {
		resource.Options = map[string]string{}
	}
	s.mergeProviderAccountCredentials(providerType, resource, nil)
}

func (s *GormStore) prepareProviderResourceForUpdate(providerType string, resource *ProviderResource, patch ProviderResource) {
	if resource == nil || !s.IsProviderAccountResourceType(providerType, resource.ResourceType) {
		return
	}
	if !s.providerResourceCredentialInputOptional(providerType, resource.ResourceType) && !providerResourceCredentialInputPresent(patch) {
		return
	}
	s.applyProviderResourceTypeDefaults(providerType, resource)
	if resource.Options == nil {
		resource.Options = map[string]string{}
	}
	s.mergeProviderAccountCredentials(providerType, resource, &patch)
}

func (s *GormStore) ConfigureProviderResourceTypeDefaults(defaults map[string]map[string]string) {
	if s == nil {
		return
	}
	normalized := make(map[string]map[string]string, len(defaults))
	for resourceType, values := range defaults {
		resourceType = strings.ToLower(strings.TrimSpace(resourceType))
		if resourceType == "" || len(values) == 0 {
			continue
		}
		normalizedValues := map[string]string{}
		for key, value := range values {
			key = strings.ToLower(strings.TrimSpace(key))
			if key != "" {
				normalizedValues[key] = strings.TrimSpace(value)
			}
		}
		normalized[resourceType] = normalizedValues
	}
	if s.mu != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.providerResourceDefaults = normalized
}

func (s *GormStore) ConfigureProviderTypeDefaults(defaultBaseURLs map[string]string) {
	if s == nil {
		return
	}
	normalized := make(map[string]string, len(defaultBaseURLs))
	for providerType, baseURL := range defaultBaseURLs {
		providerType = strings.ToLower(strings.TrimSpace(providerType))
		baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if providerType == "" || baseURL == "" {
			continue
		}
		normalized[providerType] = baseURL
	}
	if s.mu != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.providerDefaultBaseURLs = normalized
}

func (s *GormStore) applyProviderTypeDefaults(provider *Provider) {
	if s == nil || provider == nil {
		return
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		if baseURL := s.providerTypeDefaultBaseURL(provider.Type); baseURL != "" {
			provider.BaseURL = baseURL
		}
	}
}

func (s *GormStore) ConfigureProviderResourceTypePolicy(resourceTypes map[string][]string) {
	if s == nil {
		return
	}
	normalized := make(map[string]map[string]struct{}, len(resourceTypes))
	for providerType, values := range resourceTypes {
		providerType = strings.ToLower(strings.TrimSpace(providerType))
		if providerType == "" {
			continue
		}
		normalized[providerType] = map[string]struct{}{}
		for _, resourceType := range values {
			resourceType = strings.ToLower(strings.TrimSpace(resourceType))
			if resourceType == "" || resourceType == ProviderResourceAPIKey {
				continue
			}
			normalized[providerType][resourceType] = struct{}{}
		}
	}
	if s.mu != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.providerResourceTypes = normalized
}

func (s *GormStore) ConfigureProviderResourceCredentialIdentityProfiles(profiles map[string]string) {
	if s == nil {
		return
	}
	normalized := make(map[string]string, len(profiles))
	for resourceType, profile := range profiles {
		resourceType = strings.ToLower(strings.TrimSpace(resourceType))
		profile = strings.ToLower(strings.TrimSpace(profile))
		if resourceType != "" && profile != "" {
			normalized[resourceType] = profile
		}
	}
	if s.mu != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.providerResourceIdentity = normalized
}

func (s *GormStore) ConfigureProviderResourceCredentialInputOptional(resourceTypes map[string]bool) {
	if s == nil {
		return
	}
	normalized := make(map[string]bool, len(resourceTypes))
	for resourceType, optional := range resourceTypes {
		resourceType = strings.ToLower(strings.TrimSpace(resourceType))
		if resourceType != "" && optional {
			normalized[resourceType] = true
		}
	}
	if s.mu != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.providerResourceOptional = normalized
}

type providerResourceCredentialRefreshHandler func(context.Context, ProviderResourceCredentials) (ProviderResourceCredentials, error)

type providerResourceCredentialRefreshRegistration struct {
	ProviderType        string
	Profile             string
	RefreshLead         time.Duration
	AuthenticationEqual func(ProviderResourceCredentials, ProviderResourceCredentials) bool
	Refresh             providerResourceCredentialRefreshHandler
}

func (registration providerResourceCredentialRefreshRegistration) normalized(defaultProviderType string) providerResourceCredentialRefreshRegistration {
	registration.ProviderType = strings.ToLower(strings.TrimSpace(firstNonEmpty(registration.ProviderType, defaultProviderType)))
	registration.Profile = strings.ToLower(strings.TrimSpace(registration.Profile))
	if registration.AuthenticationEqual == nil {
		registration.AuthenticationEqual = providerResourceAuthenticationEqual
	}
	return registration
}

func (registration providerResourceCredentialRefreshRegistration) key() string {
	return providerResourceScopedKey(registration.ProviderType, registration.Profile)
}

func (s *GormStore) ConfigureProviderCredentialRefreshHandlers(registrations []providerResourceCredentialRefreshRegistration) {
	if s == nil {
		return
	}
	normalized := make(map[string]providerResourceCredentialRefreshRegistration, len(registrations))
	for _, registration := range registrations {
		registration = registration.normalized("")
		if registration.ProviderType != "" && registration.Profile != "" && registration.Refresh != nil {
			normalized[registration.key()] = registration
		}
	}
	if s.mu != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	s.providerCredentialRefreshers = normalized
}

func (s *GormStore) providerCredentialRefreshRegistration(provider Provider) (providerResourceCredentialRefreshRegistration, bool) {
	if s == nil {
		return providerResourceCredentialRefreshRegistration{}, false
	}
	profile := providerCredentialRefreshProfile(provider)
	if profile == "" {
		return providerResourceCredentialRefreshRegistration{}, false
	}
	registration, ok := s.providerCredentialRefreshers[providerResourceScopedKey(provider.Type, profile)]
	return registration, ok
}

func (s *GormStore) IsProviderAccountResourceType(providerType string, resourceType string) bool {
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	if resourceType == "" || resourceType == ProviderResourceAPIKey {
		return false
	}
	if s != nil {
		providerType = strings.ToLower(strings.TrimSpace(providerType))
		if accountTypes, configured := s.providerResourceTypes[providerType]; configured {
			_, ok := accountTypes[resourceType]
			return ok
		}
	}
	return isProviderAccountResource(resourceType)
}

func (s *GormStore) providerResourceCredentialIdentityProfile(providerType string, resourceType string) string {
	if s == nil {
		return ""
	}
	return firstNonEmpty(
		s.providerResourceIdentity[providerResourceScopedKey(providerType, resourceType)],
		s.providerResourceIdentity[strings.ToLower(strings.TrimSpace(resourceType))],
	)
}

func (s *GormStore) providerResourceCredentialInputOptional(providerType string, resourceType string) bool {
	if s == nil {
		return false
	}
	return s.providerResourceOptional[providerResourceScopedKey(providerType, resourceType)] ||
		s.providerResourceOptional[strings.ToLower(strings.TrimSpace(resourceType))]
}

func (s *GormStore) applyProviderResourceTypeDefaults(providerType string, resource *ProviderResource) {
	if s == nil || resource == nil {
		return
	}
	defaults := s.providerResourceTypeDefaults(providerType, resource.ResourceType)
	if strings.TrimSpace(resource.BaseURL) == "" {
		if baseURL := strings.TrimSpace(defaults["base_url"]); baseURL != "" {
			resource.BaseURL = baseURL
		}
	}
	if authType := strings.TrimSpace(defaults["auth_type"]); authType != "" {
		if resource.Options == nil {
			resource.Options = map[string]string{}
		}
		if strings.TrimSpace(resource.Options["auth_type"]) == "" {
			resource.Options["auth_type"] = authType
		}
	}
	if resource.MaxConcurrency == 0 {
		if maxConcurrency, err := strconv.ParseInt(strings.TrimSpace(defaults["max_concurrency"]), 10, 64); err == nil && maxConcurrency > 0 {
			resource.MaxConcurrency = maxConcurrency
		}
	}
}

func (s *GormStore) preserveProviderAccountProtectedOptions(current map[string]string, patch ProviderResource, resourceType string) map[string]string {
	protectedOptions := s.providerAccountProtectedOptionKeys(resourceType)
	options := make(map[string]string, len(patch.Options)+len(protectedOptions))
	for key, value := range patch.Options {
		options[key] = value
	}
	for _, key := range protectedOptions {
		if openAIAccountAuthenticationPatch(patch.Credentials) && (key == "has_refresh_token" || key == providerResourceReauthorizationRequiredOption) {
			delete(options, key)
			continue
		}
		if value, ok := current[key]; ok {
			options[key] = value
		} else {
			delete(options, key)
		}
	}
	return options
}

func (s *GormStore) providerAccountProtectedOptionKeys(resourceType string) []string {
	options := append([]string{}, providerAccountProtectedOptions...)
	normalizedResourceType := strings.TrimSpace(resourceType)
	for _, profile := range s.providerImageCapabilityRouteProfiles() {
		if profile.ResourceType != "" && profile.ResourceType != normalizedResourceType {
			continue
		}
		profile.withDefaults()
		options = append(options, profile.CapabilityOption, profile.CapabilityCheckedAtOption, profile.RouteBackfillOption)
	}
	return uniqueStrings(options)
}

func (s *GormStore) mergeProviderAccountCredentials(providerType string, resource *ProviderResource, patch *ProviderResource) {
	if patch != nil && patch.Credentials == nil && strings.TrimSpace(patch.APIKey) == "" {
		resource.Credentials = nil
		return
	}
	creds := ProviderResourceCredentials{}
	if patch != nil {
		creds = s.providerResourceCredentialsForRuntimeForProvider(providerType, *resource)
	} else if resource.Credentials != nil {
		creds = *resource.Credentials
	}
	if patch != nil && patch.Credentials != nil {
		mergeProviderResourceCredentials(&creds, *patch.Credentials)
	}
	if strings.TrimSpace(creds.AccessToken) == "" && resource.APIKey != "" && !strings.HasPrefix(resource.APIKey, "enc:v1:") {
		creds.AccessToken = resource.APIKey
	}
	if strings.TrimSpace(creds.AuthType) == "" {
		creds.AuthType = firstNonEmpty(resource.Options["auth_type"], s.providerResourceTypeDefault(providerType, resource.ResourceType, "auth_type"), "oauth")
	}
	if identity, ok := s.providerCredentialIdentityRegistration(providerType, resource.ResourceType); ok {
		creds = identity.Resolve(creds)
	}
	if strings.TrimSpace(creds.AccessToken) != "" {
		resource.APIKey = creds.AccessToken
	}
	if hasProviderResourceCredentialSecret(creds) {
		resource.CredentialBlob = s.encryptProviderResourceCredentialBlob(creds)
	} else if patch == nil && resource.CredentialBlob == "" {
		resource.CredentialBlob = ""
	}
	applyProviderAccountOptions(resource.Options, resource.ResourceType, creds)
	if patch != nil && openAIAccountAuthenticationPatch(patch.Credentials) {
		delete(resource.Options, providerResourceReauthorizationRequiredOption)
	}
	resource.Credentials = nil
}

func openAIAccountAuthenticationPatch(creds *ProviderResourceCredentials) bool {
	if creds == nil {
		return false
	}
	return strings.TrimSpace(creds.AccessToken) != "" ||
		strings.TrimSpace(creds.RefreshToken) != "" ||
		strings.TrimSpace(creds.IDToken) != "" ||
		strings.TrimSpace(creds.ClientID) != ""
}

func openAIAccountAuthenticationEqual(left ProviderResourceCredentials, right ProviderResourceCredentials) bool {
	return providerResourceAuthenticationEqual(left, right)
}

func providerResourceAuthenticationEqual(left ProviderResourceCredentials, right ProviderResourceCredentials) bool {
	return strings.TrimSpace(left.AuthType) == strings.TrimSpace(right.AuthType) &&
		strings.TrimSpace(left.AccessToken) == strings.TrimSpace(right.AccessToken) &&
		strings.TrimSpace(left.RefreshToken) == strings.TrimSpace(right.RefreshToken) &&
		strings.TrimSpace(left.IDToken) == strings.TrimSpace(right.IDToken) &&
		strings.TrimSpace(left.ClientID) == strings.TrimSpace(right.ClientID)
}

func mergeRefreshedProviderResourceCredentials(
	original ProviderResourceCredentials,
	current ProviderResourceCredentials,
	refreshed ProviderResourceCredentials,
) ProviderResourceCredentials {
	current.AuthType = firstNonEmpty(refreshed.AuthType, current.AuthType)
	current.AccessToken = firstNonEmpty(refreshed.AccessToken, current.AccessToken)
	current.RefreshToken = firstNonEmpty(refreshed.RefreshToken, current.RefreshToken)
	current.IDToken = firstNonEmpty(refreshed.IDToken, current.IDToken)
	current.ClientID = firstNonEmpty(refreshed.ClientID, current.ClientID)
	if current.Scopes == original.Scopes {
		current.Scopes = firstNonEmpty(refreshed.Scopes, current.Scopes)
	}
	if current.TokenType == original.TokenType {
		current.TokenType = firstNonEmpty(refreshed.TokenType, current.TokenType)
	}
	if current.AccountID == original.AccountID {
		current.AccountID = firstNonEmpty(refreshed.AccountID, current.AccountID)
	}
	if current.UserID == original.UserID {
		current.UserID = firstNonEmpty(refreshed.UserID, current.UserID)
	}
	if current.Email == original.Email {
		current.Email = firstNonEmpty(refreshed.Email, current.Email)
	}
	if current.OrganizationID == original.OrganizationID {
		current.OrganizationID = firstNonEmpty(refreshed.OrganizationID, current.OrganizationID)
	}
	if current.PlanType == original.PlanType {
		current.PlanType = firstNonEmpty(refreshed.PlanType, current.PlanType)
	}
	current.ExpiresAt = firstNonEmpty(refreshed.ExpiresAt, current.ExpiresAt)
	return current
}

func hasProviderResourceCredentialSecret(creds ProviderResourceCredentials) bool {
	return strings.TrimSpace(creds.RefreshToken) != "" ||
		strings.TrimSpace(creds.IDToken) != "" ||
		strings.TrimSpace(creds.ClientID) != "" ||
		strings.TrimSpace(creds.Scopes) != "" ||
		strings.TrimSpace(creds.TokenType) != "" ||
		strings.TrimSpace(creds.ExpiresAt) != ""
}

func (s *GormStore) encryptProviderResourceCredentialBlob(creds ProviderResourceCredentials) string {
	secret := map[string]string{}
	addNonEmpty(secret, "auth_type", creds.AuthType)
	addNonEmpty(secret, "refresh_token", creds.RefreshToken)
	addNonEmpty(secret, "id_token", creds.IDToken)
	addNonEmpty(secret, "client_id", creds.ClientID)
	addNonEmpty(secret, "scopes", creds.Scopes)
	addNonEmpty(secret, "token_type", creds.TokenType)
	addNonEmpty(secret, "expires_at", creds.ExpiresAt)
	addNonEmpty(secret, "account_id", creds.AccountID)
	addNonEmpty(secret, "user_id", creds.UserID)
	addNonEmpty(secret, "email", creds.Email)
	addNonEmpty(secret, "organization_id", creds.OrganizationID)
	addNonEmpty(secret, "plan_type", creds.PlanType)
	data, err := json.Marshal(secret)
	if err != nil {
		return ""
	}
	return s.encryptSecret(string(data))
}

func applyOpenAIAccountOptions(options map[string]string, creds ProviderResourceCredentials) {
	applyProviderAccountOptions(options, ProviderResourceOpenAISubscription, creds)
}

func applyProviderAccountOptions(options map[string]string, resourceType string, creds ProviderResourceCredentials) {
	if options == nil {
		return
	}
	for _, key := range []string{
		"access_token",
		"refresh_token",
		"id_token",
		"api_key",
		"credential_blob",
	} {
		delete(options, key)
	}
	options["credential_source"] = firstNonEmpty(strings.TrimSpace(resourceType), providerAccountCredentialSourceFallback)
	options["auth_type"] = firstNonEmpty(creds.AuthType, options["auth_type"], "oauth")
	if strings.TrimSpace(creds.RefreshToken) != "" {
		options["has_refresh_token"] = "true"
	} else if options["has_refresh_token"] == "" {
		options["has_refresh_token"] = "false"
	}
	setOptionIfValue(options, "token_expires_at", creds.ExpiresAt)
	setOptionIfValue(options, "account_id", creds.AccountID)
	setOptionIfValue(options, "account_email", creds.Email)
	setOptionIfValue(options, "user_id", creds.UserID)
	setOptionIfValue(options, "organization_id", creds.OrganizationID)
	setOptionIfValue(options, "plan_type", creds.PlanType)
}

func isOpenAIAccountResource(resourceType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(resourceType))
	return normalized == ProviderResourceOpenAISubscription ||
		normalized == "openai_oauth" ||
		normalized == "openai_account"
}

func isProviderAccountResource(resourceType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(resourceType))
	return normalized != "" && normalized != ProviderResourceAPIKey
}

func providerResourceCredentialInputPresent(resource ProviderResource) bool {
	return resource.Credentials != nil || strings.TrimSpace(resource.APIKey) != ""
}

func (s *GormStore) providerResourceCredentialSummary(providerType string, resource ProviderResource) map[string]string {
	if !s.IsProviderAccountResourceType(providerType, resource.ResourceType) {
		return nil
	}
	return providerAccountCredentialSummaryFromOptions(resource.Options)
}

func providerResourceCredentialSummary(resource ProviderResource) map[string]string {
	if !isProviderAccountResource(resource.ResourceType) {
		return nil
	}
	return providerAccountCredentialSummaryFromOptions(resource.Options)
}

func providerAccountCredentialSummaryFromOptions(options map[string]string) map[string]string {
	if len(options) == 0 {
		return nil
	}
	summary := map[string]string{}
	for _, key := range []string{
		"credential_source",
		"auth_type",
		"account_email",
		"account_id",
		"user_id",
		"organization_id",
		"plan_type",
		"token_expires_at",
		"has_refresh_token",
		providerResourceReauthorizationRequiredOption,
	} {
		if value := strings.TrimSpace(options[key]); value != "" {
			summary[key] = value
		}
	}
	if len(summary) == 0 {
		return nil
	}
	return summary
}

func (s *GormStore) redactProviderResourceSecrets(providerType string, resource *ProviderResource) {
	if resource == nil {
		return
	}
	resource.APIKey = ""
	resource.CredentialBlob = ""
	resource.Credentials = nil
	resource.CredentialSummary = s.providerResourceCredentialSummary(providerType, *resource)
}

func redactProviderResourceSecrets(resource *ProviderResource) {
	if resource == nil {
		return
	}
	resource.APIKey = ""
	resource.CredentialBlob = ""
	resource.Credentials = nil
	resource.CredentialSummary = providerResourceCredentialSummary(*resource)
}

func (s *GormStore) RefreshProviderResourceCredentials(ctx context.Context, resourceID string, force bool) (ProviderResourceCredentials, error) {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return ProviderResourceCredentials{}, nil
	}

	var result ProviderResourceCredentials
	err := s.withClusterLease(ctx, "credential-refresh:"+resourceID, func(leaseCtx context.Context) error {
		s.mu.Lock()
		var resource ProviderResource
		if err := s.db.WithContext(leaseCtx).First(&resource, "id = ?", resourceID).Error; err != nil {
			s.mu.Unlock()
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		var provider Provider
		if err := s.db.WithContext(leaseCtx).First(&provider, "id = ?", resource.ProviderID).Error; err != nil {
			s.mu.Unlock()
			return notFound(err, "provider_not_found", "Provider not found")
		}
		creds := s.providerResourceCredentialsForRuntimeForProvider(provider.Type, resource)
		authentication := creds
		refreshRegistration, refreshSupported := s.providerCredentialRefreshRegistration(provider)
		if !refreshSupported || !s.IsProviderAccountResourceType(provider.Type, resource.ResourceType) {
			result = creds
			s.mu.Unlock()
			return nil
		}
		if resource.Options[providerResourceReauthorizationRequiredOption] == "true" {
			result = creds
			s.mu.Unlock()
			return NewHTTPError(409, "provider_resource_reauthorization_required", "Provider resource account session has ended. Reauthorize the account.")
		}
		needsRefresh, expired := providerResourceCredentialsNeedRefresh(creds, refreshRegistration.RefreshLead)
		if !force && !needsRefresh {
			result = creds
			s.mu.Unlock()
			return nil
		}
		if strings.TrimSpace(creds.RefreshToken) == "" {
			result = creds
			s.mu.Unlock()
			if expired {
				return NewHTTPError(503, "provider_resource_token_expired", "Provider resource access token expired and no refresh token is available")
			}
			return nil
		}
		s.mu.Unlock()

		refreshed, err := refreshRegistration.Refresh(leaseCtx, creds)
		if err != nil {
			if httpErr := AsHTTPError(err); httpErr != nil && httpErr.Code == "provider_resource_reauthorization_required" {
				persistErr := s.withClusterLease(leaseCtx, providerResourceMutationLeaseName(resourceID), func(mutationCtx context.Context) error {
					s.mu.Lock()
					defer s.mu.Unlock()
					var current ProviderResource
					if loadErr := s.db.WithContext(mutationCtx).First(&current, "id = ?", resourceID).Error; loadErr != nil {
						return loadErr
					}
					if !s.supportsNativeProviderResourceCredentialRefresh(provider, current) ||
						!refreshRegistration.AuthenticationEqual(s.providerResourceCredentialsForRuntimeForProvider(provider.Type, current), authentication) {
						return nil
					}
					if current.Options == nil {
						current.Options = map[string]string{}
					}
					current.Options[providerResourceReauthorizationRequiredOption] = "true"
					current.UpdatedAt = time.Now().UTC()
					return updateExistingProviderResourceColumns(s.db.WithContext(mutationCtx), &current, "options", "updated_at")
				})
				if persistErr != nil {
					return persistErr
				}
			}
			return err
		}

		return s.withClusterLease(leaseCtx, providerResourceMutationLeaseName(resourceID), func(mutationCtx context.Context) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			var current ProviderResource
			if err := s.db.WithContext(mutationCtx).First(&current, "id = ?", resourceID).Error; err != nil {
				return notFound(err, "provider_resource_not_found", "Provider resource not found")
			}
			if !s.supportsNativeProviderResourceCredentialRefresh(provider, current) {
				result = refreshed
				return nil
			}
			currentCredentials := s.providerResourceCredentialsForRuntimeForProvider(provider.Type, current)
			if !refreshRegistration.AuthenticationEqual(currentCredentials, authentication) {
				result = currentCredentials
				return nil
			}
			refreshed = mergeRefreshedProviderResourceCredentials(authentication, currentCredentials, refreshed)
			if current.Options == nil {
				current.Options = map[string]string{}
			}
			delete(current.Options, providerResourceReauthorizationRequiredOption)
			current.Credentials = &refreshed
			s.mergeProviderAccountCredentials(provider.Type, &current, &ProviderResource{Credentials: &refreshed})
			if strings.TrimSpace(current.APIKey) != "" {
				current.APIKey = s.encryptSecret(current.APIKey)
			}
			current.UpdatedAt = time.Now().UTC()
			if err := updateExistingProviderResourceColumns(
				s.db.WithContext(mutationCtx),
				&current,
				"api_key", "credential_blob", "options", "updated_at",
			); err != nil {
				return err
			}
			result = s.providerResourceCredentialsForRuntimeForProvider(provider.Type, current)
			return nil
		})
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func (s *GormStore) supportsNativeProviderResourceCredentialRefresh(provider Provider, resource ProviderResource) bool {
	if s == nil {
		return false
	}
	if _, ok := s.providerCredentialRefreshRegistration(provider); !ok {
		return false
	}
	return s.IsProviderAccountResourceType(provider.Type, resource.ResourceType)
}

func (s *GormStore) SupportsNativeProviderResourceCredentialRefresh(resource ProviderResource) bool {
	if s == nil {
		return false
	}
	provider, ok := s.GetProvider(resource.ProviderID)
	if !ok {
		return false
	}
	return s.supportsNativeProviderResourceCredentialRefresh(provider, resource)
}

func (s *GormStore) providerResourceCredentialsForRuntime(resource ProviderResource) ProviderResourceCredentials {
	providerType := ""
	if s != nil && strings.TrimSpace(resource.ProviderID) != "" {
		var provider Provider
		if err := s.db.First(&provider, "id = ?", resource.ProviderID).Error; err == nil {
			providerType = provider.Type
		}
	}
	return s.providerResourceCredentialsForRuntimeForProvider(providerType, resource)
}

func (s *GormStore) providerResourceCredentialsForRuntimeForProvider(providerType string, resource ProviderResource) ProviderResourceCredentials {
	creds := ProviderResourceCredentials{}
	if strings.TrimSpace(resource.APIKey) != "" {
		creds.AccessToken = s.decryptSecret(resource.APIKey)
	}
	if strings.TrimSpace(resource.CredentialBlob) != "" {
		if secret := s.decryptSecret(resource.CredentialBlob); strings.TrimSpace(secret) != "" {
			var blob ProviderResourceCredentials
			if err := json.Unmarshal([]byte(secret), &blob); err == nil {
				mergeProviderResourceCredentials(&creds, blob)
			}
		}
	}
	if resource.Options != nil {
		creds.AuthType = firstNonEmpty(creds.AuthType, resource.Options["auth_type"], s.providerResourceTypeDefault(providerType, resource.ResourceType, "auth_type"), "oauth")
		creds.ExpiresAt = firstNonEmpty(creds.ExpiresAt, resource.Options["token_expires_at"])
		creds.AccountID = firstNonEmpty(creds.AccountID, resource.Options["account_id"])
		creds.UserID = firstNonEmpty(creds.UserID, resource.Options["user_id"])
		creds.Email = firstNonEmpty(creds.Email, resource.Options["account_email"])
		creds.OrganizationID = firstNonEmpty(creds.OrganizationID, resource.Options["organization_id"])
		creds.PlanType = firstNonEmpty(creds.PlanType, resource.Options["plan_type"])
		creds.Scopes = firstNonEmpty(creds.Scopes, resource.Options["scopes"])
	}
	if strings.TrimSpace(creds.AuthType) == "" {
		creds.AuthType = firstNonEmpty(s.providerResourceTypeDefault(providerType, resource.ResourceType, "auth_type"), "oauth")
	}
	return creds
}

func mergeProviderResourceCredentials(target *ProviderResourceCredentials, source ProviderResourceCredentials) {
	target.AuthType = firstNonEmpty(source.AuthType, target.AuthType)
	target.AccessToken = firstNonEmpty(source.AccessToken, target.AccessToken)
	target.RefreshToken = firstNonEmpty(source.RefreshToken, target.RefreshToken)
	target.IDToken = firstNonEmpty(source.IDToken, target.IDToken)
	target.ClientID = firstNonEmpty(source.ClientID, target.ClientID)
	target.Scopes = firstNonEmpty(source.Scopes, target.Scopes)
	target.TokenType = firstNonEmpty(source.TokenType, target.TokenType)
	target.ExpiresAt = firstNonEmpty(source.ExpiresAt, target.ExpiresAt)
	target.AccountID = firstNonEmpty(source.AccountID, target.AccountID)
	target.UserID = firstNonEmpty(source.UserID, target.UserID)
	target.Email = firstNonEmpty(source.Email, target.Email)
	target.OrganizationID = firstNonEmpty(source.OrganizationID, target.OrganizationID)
	target.PlanType = firstNonEmpty(source.PlanType, target.PlanType)
}

func providerResourceCredentialsNeedRefresh(creds ProviderResourceCredentials, refreshLead time.Duration) (bool, bool) {
	expiresAt, ok := parseCredentialExpiry(creds.ExpiresAt)
	if !ok {
		return false, false
	}
	now := time.Now().UTC()
	if !expiresAt.After(now) {
		return true, true
	}
	return time.Until(expiresAt) < refreshLead, false
}

func parseCredentialExpiry(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func setOptionIfValue(options map[string]string, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		options[key] = value
	}
}

func (s *GormStore) providerResourceTypeDefault(providerType string, resourceType string, key string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.providerResourceTypeDefaults(providerType, resourceType)[strings.ToLower(strings.TrimSpace(key))])
}

func (s *GormStore) providerResourceTypeDefaults(providerType string, resourceType string) map[string]string {
	if s == nil {
		return nil
	}
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	if defaults := s.providerResourceDefaults[providerResourceScopedKey(providerType, resourceType)]; len(defaults) > 0 {
		return defaults
	}
	return s.providerResourceDefaults[resourceType]
}

func providerResourceScopedKey(providerType string, resourceType string) string {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	if providerType == "" {
		return resourceType
	}
	return providerType + "\x00" + resourceType
}

func (s *GormStore) providerTypeDefaultBaseURL(providerType string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.providerDefaultBaseURLs[strings.ToLower(strings.TrimSpace(providerType))])
}

func addNonEmpty(values map[string]string, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		values[key] = value
	}
}
