package plugin

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	SchemaVersion int                   `yaml:"schema_version"`
	ID            string                `yaml:"id"`
	Name          string                `yaml:"name"`
	Version       string                `yaml:"version"`
	Description   string                `yaml:"description"`
	TokenHub      ManifestCompatibility `yaml:"tokenhub"`
	Kinds         []Kind                `yaml:"kinds"`
	Placement     []Placement           `yaml:"placement"`
	Entry         ManifestEntry         `yaml:"entry"`
	Capabilities  ManifestCapabilities  `yaml:"capabilities"`
	Permissions   ManifestPermissions   `yaml:"permissions"`
}

type ManifestCompatibility struct {
	PluginAPI string `yaml:"plugin_api"`
	MinCore   string `yaml:"min_core"`
	MaxCore   string `yaml:"max_core"`
}

type ManifestEntry struct {
	Backend  *ManifestBackendEntry  `yaml:"backend"`
	Frontend *ManifestFrontendEntry `yaml:"frontend"`
}

type ManifestBackendEntry struct {
	Command  string `yaml:"command"`
	Protocol string `yaml:"protocol"`
}

type ManifestFrontendEntry struct {
	Schema string `yaml:"schema"`
}

type ManifestCapabilities struct {
	ProviderTypes []string              `yaml:"provider_types"`
	Gateway       []string              `yaml:"gateway"`
	AdminUI       []string              `yaml:"admin_ui"`
	Hooks         []GatewayHookManifest `yaml:"hooks"`
}

type GatewayHookManifest struct {
	ID            string                   `yaml:"id"`
	Stage         GatewayHookStage         `yaml:"stage"`
	Priority      int                      `yaml:"priority"`
	Reads         []GatewayDataClass       `yaml:"reads"`
	Writes        []GatewayDataClass       `yaml:"writes"`
	FailurePolicy GatewayHookFailurePolicy `yaml:"failure_policy"`
	TimeoutMillis int                      `yaml:"timeout_millis"`
}

type ManifestPermissions struct {
	Network ManifestNetworkPermissions `yaml:"network"`
	Data    ManifestDataPermissions    `yaml:"data"`
}

type ManifestNetworkPermissions struct {
	Allow []string `yaml:"allow"`
}

type ManifestDataPermissions struct {
	Read  []GatewayDataClass `yaml:"read"`
	Write []GatewayDataClass `yaml:"write"`
}

func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, manifest.Validate()
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported plugin manifest schema_version %d", m.SchemaVersion)
	}
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("plugin id is required")
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("plugin name is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("plugin version is required")
	}
	if strings.TrimSpace(m.TokenHub.PluginAPI) == "" {
		return fmt.Errorf("tokenhub.plugin_api is required")
	}
	if len(NormalizeDescriptor(Descriptor{Kinds: m.Kinds}).Kinds) == 0 {
		return fmt.Errorf("at least one plugin kind is required")
	}
	for _, kind := range m.Kinds {
		if !validKind(kind) {
			return fmt.Errorf("unsupported plugin kind %q", kind)
		}
	}
	for _, placement := range m.Placement {
		if !validPlacement(placement) {
			return fmt.Errorf("unsupported plugin placement %q", placement)
		}
	}
	for _, hook := range m.Capabilities.Hooks {
		if strings.TrimSpace(hook.ID) == "" {
			return fmt.Errorf("gateway hook id is required")
		}
		if !validGatewayHookStage(hook.Stage) {
			return fmt.Errorf("unsupported gateway hook stage %q", hook.Stage)
		}
		if hook.FailurePolicy != "" && !validGatewayFailurePolicy(hook.FailurePolicy) {
			return fmt.Errorf("unsupported gateway hook failure policy %q", hook.FailurePolicy)
		}
		if err := validateGatewayDataClasses(hook.Reads); err != nil {
			return fmt.Errorf("gateway hook %s reads: %w", hook.ID, err)
		}
		if err := validateGatewayDataClasses(hook.Writes); err != nil {
			return fmt.Errorf("gateway hook %s writes: %w", hook.ID, err)
		}
		if err := validateHookDataPermissions(hook, m.Permissions.Data); err != nil {
			return err
		}
	}
	return nil
}

func (m Manifest) Descriptor() Descriptor {
	descriptor := Descriptor{
		ID:         m.ID,
		Name:       m.Name,
		Version:    m.Version,
		Source:     SourceLocalFile,
		Kinds:      m.Kinds,
		Placements: m.Placement,
	}
	for _, providerType := range m.Capabilities.ProviderTypes {
		descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
			Kind: "provider_type",
			Name: providerType,
		})
		for _, capability := range m.Capabilities.Gateway {
			descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
				Kind:    "provider",
				Name:    capability,
				Subject: providerType,
			})
		}
	}
	for _, capability := range m.Capabilities.AdminUI {
		descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
			Kind: "admin_ui",
			Name: capability,
		})
	}
	for _, hook := range m.Capabilities.Hooks {
		descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
			Kind: "gateway_chain",
			Name: string(hook.Stage),
		})
	}
	return NormalizeDescriptor(descriptor)
}

func (m Manifest) GatewayHooks() []GatewayHookDescriptor {
	hooks := make([]GatewayHookDescriptor, 0, len(m.Capabilities.Hooks))
	for _, hook := range m.Capabilities.Hooks {
		hooks = append(hooks, GatewayHookDescriptor{
			PluginID:      m.ID,
			HookID:        hook.ID,
			Stage:         hook.Stage,
			Priority:      hook.Priority,
			Reads:         hook.Reads,
			Writes:        hook.Writes,
			FailurePolicy: hook.FailurePolicy,
			TimeoutMillis: hook.TimeoutMillis,
		})
	}
	return hooks
}

func validKind(kind Kind) bool {
	switch kind {
	case KindProvider, KindAdminUI, KindSIM, KindExtension:
		return true
	default:
		return false
	}
}

func validPlacement(placement Placement) bool {
	switch placement {
	case PlacementPresentation, PlacementGatewayChain, PlacementBackground, PlacementManagementAction:
		return true
	default:
		return false
	}
}

func validGatewayHookStage(stage GatewayHookStage) bool {
	for _, candidate := range orderedGatewayStages() {
		if candidate == stage {
			return true
		}
	}
	return false
}

func validGatewayFailurePolicy(policy GatewayHookFailurePolicy) bool {
	switch policy {
	case FailurePolicyFailClosed, FailurePolicyFailOpen, FailurePolicySkipRoute, FailurePolicyReturnFallback, FailurePolicyObserveOnly:
		return true
	default:
		return false
	}
}

func validateGatewayDataClasses(dataClasses []GatewayDataClass) error {
	for _, dataClass := range dataClasses {
		if !validGatewayDataClass(dataClass) {
			return fmt.Errorf("unsupported data class %q", dataClass)
		}
	}
	return nil
}

func validateHookDataPermissions(hook GatewayHookManifest, permissions ManifestDataPermissions) error {
	readAllowed := gatewayDataClassSet(permissions.Read)
	writeAllowed := gatewayDataClassSet(permissions.Write)
	for _, dataClass := range hook.Reads {
		if _, ok := readAllowed[dataClass]; !ok {
			return fmt.Errorf("gateway hook %s reads %q without permissions.data.read", hook.ID, dataClass)
		}
	}
	for _, dataClass := range hook.Writes {
		if _, ok := writeAllowed[dataClass]; !ok {
			return fmt.Errorf("gateway hook %s writes %q without permissions.data.write", hook.ID, dataClass)
		}
	}
	return nil
}

func gatewayDataClassSet(dataClasses []GatewayDataClass) map[GatewayDataClass]struct{} {
	allowed := map[GatewayDataClass]struct{}{}
	for _, dataClass := range dataClasses {
		allowed[dataClass] = struct{}{}
	}
	return allowed
}
