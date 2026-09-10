package plugin

import "testing"

func TestPrimaryCategoryUsesOneUserFacingTaxonomy(t *testing.T) {
	tests := []struct {
		name       string
		descriptor Descriptor
		want       Category
	}{
		{name: "provider wins over implementation placement", descriptor: Descriptor{Kinds: []Kind{KindProvider}, Placements: []Placement{PlacementGatewayChain}}, want: CategoryProviderIntegration},
		{name: "pipeline", descriptor: Descriptor{Kinds: []Kind{KindExtension}, Placements: []Placement{PlacementGatewayChain}}, want: CategoryRequestPipeline},
		{name: "interface template", descriptor: Descriptor{Kinds: []Kind{KindSIM}, Placements: []Placement{PlacementPresentation}}, want: CategoryUITemplate},
		{name: "automation", descriptor: Descriptor{Kinds: []Kind{KindExtension}, Placements: []Placement{PlacementBackground}}, want: CategoryAutomation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := PrimaryCategory(test.descriptor); got != test.want {
				t.Fatalf("primary category = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCatalogOnlyProviderDoesNotClaimRuntimeExecution(t *testing.T) {
	catalog := Descriptor{Capabilities: []CapabilityDescriptor{{Kind: CapabilityKindProviderCatalog, Name: ProviderCatalogEntry}}}
	if !CatalogOnlyProvider(catalog) {
		t.Fatal("catalog-only provider was treated as an executable provider")
	}
	catalog.Capabilities = append(catalog.Capabilities, CapabilityDescriptor{Kind: CapabilityKindProvider, Name: "chat"})
	if CatalogOnlyProvider(catalog) {
		t.Fatal("runtime provider was treated as catalog-only")
	}
}

func TestLifecycleFactsKeepLifecycleDimensionsIndependent(t *testing.T) {
	facts := DeriveLifecycleFacts(LifecycleFactsInput{
		Available:      true,
		Installed:      true,
		Configured:     false,
		InUse:          true,
		DesiredState:   PackageState{Status: StatusEnabled, Health: PackageHealthHealthy},
		ActiveStatus:   StatusDisabled,
		DesiredVersion: "2.0.0",
		ActiveVersion:  "1.0.0",
	})
	if !facts.Available || !facts.Installed || !facts.Enabled || facts.Configured || facts.InUse || !facts.SetupRequired {
		t.Fatalf("lifecycle facts = %+v", facts)
	}
	if !facts.RestartRequired || facts.ActiveEnabled || !facts.DesiredEnabled {
		t.Fatalf("activation facts = %+v", facts)
	}
}

func TestLifecycleFactsNeverEnableAnAvailableOnlyPlugin(t *testing.T) {
	facts := DeriveLifecycleFacts(LifecycleFactsInput{
		Available:    true,
		Installed:    false,
		Configured:   true,
		InUse:        true,
		DesiredState: PackageState{Status: StatusEnabled},
		ActiveStatus: StatusEnabled,
	})
	if facts.Installed || facts.Enabled || facts.Configured || facts.InUse || facts.SetupRequired || facts.RestartRequired {
		t.Fatalf("available-only lifecycle facts = %+v", facts)
	}
}

func TestLifecycleFactsPreserveExplicitRestartRequirement(t *testing.T) {
	facts := DeriveLifecycleFacts(LifecycleFactsInput{
		Available:    true,
		Installed:    true,
		Configured:   true,
		DesiredState: PackageState{Status: StatusEnabled, RestartRequired: true},
		ActiveStatus: StatusEnabled,
	})
	if !facts.Enabled || !facts.ActiveEnabled || !facts.RestartRequired {
		t.Fatalf("lifecycle facts = %+v, want enabled plugin with restart required", facts)
	}
}

func TestLifecycleFactsDoNotSuggestRestartForFailedFallback(t *testing.T) {
	facts := DeriveLifecycleFacts(LifecycleFactsInput{
		Available:      true,
		Installed:      true,
		Configured:     true,
		DesiredState:   PackageState{Status: StatusFailedStartup, Health: PackageHealthUnhealthy},
		ActiveStatus:   StatusEnabled,
		DesiredVersion: "2.0.0",
		ActiveVersion:  "1.0.0",
	})
	if facts.DesiredEnabled || !facts.ActiveEnabled || facts.RestartRequired {
		t.Fatalf("failed fallback lifecycle facts = %+v", facts)
	}
}
