package server

import (
	"encoding/json"
	"strings"
)

// Only explicit account replacements track omitted settings. Other resource
// updates retain their existing replacement semantics, including zero values.
func (r *ProviderResource) UnmarshalJSON(data []byte) error {
	type resourceAlias ProviderResource
	var decoded resourceAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = ProviderResource(decoded)
	if r.Credentials != nil && r.Credentials.Replace {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		r.replacementFields = make(map[string]bool, len(fields))
		for key := range fields {
			r.replacementFields[strings.ToLower(key)] = true
		}
	}
	return nil
}

// Apply omissions after loading the current resource under the mutation lease,
// so reauthorizing an account cannot restore stale client-side resource settings.
func retainCredentialReplacementSettings(patch *ProviderResource, current ProviderResource) {
	if patch.Credentials == nil || !patch.Credentials.Replace || patch.replacementFields == nil {
		return
	}
	if !patch.replacementFields["base_url"] {
		patch.BaseURL = current.BaseURL
	}
	if !patch.replacementFields["region"] {
		patch.Region = current.Region
	}
	if !patch.replacementFields["environment"] {
		patch.Environment = current.Environment
	}
	if !patch.replacementFields["healthy"] {
		patch.Healthy = current.Healthy
	}
	if !patch.replacementFields["rate_limit_rpm"] {
		patch.RateLimitRPM = current.RateLimitRPM
	}
	if !patch.replacementFields["token_limit_tpm"] {
		patch.TokenLimitTPM = current.TokenLimitTPM
	}
	if !patch.replacementFields["max_concurrency"] {
		patch.MaxConcurrency = current.MaxConcurrency
	}
}

// An explicit account replacement must not combine the new authentication with
// old secrets or metadata. Ordinary partial edits and refreshes still merge.
func resetProviderAccountCredentials(resource *ProviderResource) {
	resource.APIKey = ""
	resource.CredentialBlob = ""
	for _, key := range providerAccountProtectedOptions {
		delete(resource.Options, key)
	}
	delete(resource.Options, "scopes")
}
