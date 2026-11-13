package server

import "strconv"

// Generic API-key replacement changes wallet identity too. OAuth token rotation
// within a subscription account keeps its identity and allowance configuration.
func (s *GormStore) prepareAllowanceCredentialMutation(providerType string, before ProviderResource, beforeCreds ProviderResourceCredentials, after *ProviderResource, afterCreds ProviderResourceCredentials, patch ProviderResource, generation int64) bool {
	explicit := patch.Credentials != nil && patch.Credentials.Replace
	moved := before.ProviderID != after.ProviderID || before.ResourceType != after.ResourceType
	keyed := !s.IsProviderAccountResourceType(providerType, after.ResourceType) && beforeCreds.AccessToken != afterCreds.AccessToken
	if !explicit && !moved && !keyed {
		return false
	}
	if after.Options == nil {
		after.Options = map[string]string{}
	}
	after.Options[providerCredentialReplacementGenerationOption] = strconv.FormatInt(generation+1, 10)
	return true
}
