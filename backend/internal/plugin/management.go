package plugin

import "strings"

type Category string

const (
	CategoryProviderIntegration Category = "provider_integration"
	CategoryRequestPipeline     Category = "request_pipeline"
	CategoryUITemplate          Category = "ui_template"
	CategoryAutomation          Category = "automation"
)

type LifecycleFacts struct {
	Available       bool                `json:"available"`
	Installed       bool                `json:"installed"`
	Enabled         bool                `json:"enabled"`
	Configured      bool                `json:"configured"`
	InUse           bool                `json:"in_use"`
	SetupRequired   bool                `json:"setup_required"`
	Health          PackageHealthStatus `json:"health"`
	DesiredVersion  string              `json:"desired_version,omitempty"`
	ActiveVersion   string              `json:"active_version,omitempty"`
	DesiredEnabled  bool                `json:"desired_enabled"`
	ActiveEnabled   bool                `json:"active_enabled"`
	RestartRequired bool                `json:"restart_required"`
}

type LifecycleFactsInput struct {
	Available      bool
	Installed      bool
	Configured     bool
	InUse          bool
	DesiredState   PackageState
	ActiveStatus   Status
	DesiredVersion string
	ActiveVersion  string
}

func PrimaryCategory(descriptor Descriptor) Category {
	if validCategory(descriptor.Category) {
		return descriptor.Category
	}
	for _, kind := range descriptor.Kinds {
		if kind == KindProvider {
			return CategoryProviderIntegration
		}
	}
	for _, placement := range descriptor.Placements {
		if placement == PlacementGatewayChain {
			return CategoryRequestPipeline
		}
	}
	for _, kind := range descriptor.Kinds {
		if kind == KindSIM || kind == KindAdminUI {
			return CategoryUITemplate
		}
	}
	for _, placement := range descriptor.Placements {
		if placement == PlacementPresentation {
			return CategoryUITemplate
		}
	}
	return CategoryAutomation
}

func CatalogOnlyProvider(descriptor Descriptor) bool {
	hasCatalog := false
	hasRuntimeProvider := false
	for _, capability := range descriptor.Capabilities {
		switch capability.Kind {
		case CapabilityKindProviderCatalog:
			hasCatalog = hasCatalog || capability.Name == ProviderCatalogEntry
		case CapabilityKindProvider, CapabilityKindProviderType, CapabilityKindProviderResourceType:
			hasRuntimeProvider = true
		}
	}
	return hasCatalog && !hasRuntimeProvider
}

func DeriveLifecycleFacts(input LifecycleFactsInput) LifecycleFacts {
	desired, err := NormalizePackageState(input.DesiredState)
	if err != nil {
		desired = PackageState{Status: StatusFailedValidation, Health: PackageHealthUnhealthy}
	}
	active, err := NormalizePackageState(PackageState{Status: input.ActiveStatus})
	if err != nil {
		active = PackageState{Status: StatusDisabled, Health: PackageHealthUnknown}
	}
	desiredVersion := strings.TrimSpace(input.DesiredVersion)
	activeVersion := strings.TrimSpace(input.ActiveVersion)
	desiredEnabled := input.Installed && desired.Loadable()
	activeEnabled := input.Installed && active.Loadable()
	restartRequired := false
	if input.Installed && !desired.FailedValidation() && !desired.FailedStartup() {
		restartRequired = desired.RestartRequired || desiredEnabled != activeEnabled
		if desiredVersion != "" && activeVersion != "" && desiredVersion != activeVersion {
			restartRequired = true
		}
	}
	configured := input.Installed && input.Configured
	inUse := configured && input.InUse
	return LifecycleFacts{
		Available:       input.Available,
		Installed:       input.Installed,
		Enabled:         desiredEnabled,
		Configured:      configured,
		InUse:           inUse,
		SetupRequired:   desiredEnabled && !configured,
		Health:          desired.Health,
		DesiredVersion:  desiredVersion,
		ActiveVersion:   activeVersion,
		DesiredEnabled:  desiredEnabled,
		ActiveEnabled:   activeEnabled,
		RestartRequired: restartRequired,
	}
}
