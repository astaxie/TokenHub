package server

// Normal token rotation preserves account identity and replacement generation.
// Reusing an ID or moving a resource must invalidate in-flight observations too.
func sameProviderAccountSnapshot(current, expected ProviderResource) bool {
	return current.ID == expected.ID && current.ProviderID == expected.ProviderID &&
		current.ResourceType == expected.ResourceType && current.CreatedAt.Equal(expected.CreatedAt) &&
		providerCredentialReplacementGeneration(current.Options) == providerCredentialReplacementGeneration(expected.Options)
}
