package server

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
