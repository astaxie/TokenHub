package server

import "context"

// ProviderModelDiscoverer handles vendor-specific model-list wire formats.
type ProviderModelDiscoverer interface {
	DiscoverModels(context.Context, ProviderCreateRequest) (ProviderCatalogEntry, error)
}

func typeSafeBuiltinAdapter(adapter any) builtinProviderAdapter {
	return builtinProviderAdapter{
		providerType:          providerTypeSafe,
		adapter:               adapter,
		apiKeyRequired:        boolPointer(true),
		reasoningConfigurable: boolPointer(false),
		managedHeaders:        []string{"authorization"},
		routeProtocols:        []string{providerRouteProtocolSystemOne},
		modelDiscovery:        AdapterModelDiscoveryPolicy{Path: "/models", Auth: "bearer_header"},
		modelCategories: []providerModelCategoryDefinition{{
			Key: "typesafe", Label: "TypeSafe", Order: 90,
			Aliases: []string{"typesafe", "jev"}, FamilyPrefixes: []string{"jev"}, CanonicalPrefixes: []string{"jev"},
		}},
		catalogEntry: builtinProviderPluginCatalogEntry(
			providerTypeSafe, "TypeSafe", providerTypeSafe, typeSafeBaseURL, typeSafeDocURL,
			[]string{providerTypeSafe}, nil,
		),
		capabilities: []AdapterCapability{AdapterCapabilitySystemOne, AdapterCapabilityModels, AdapterCapabilityProbe},
	}
}
