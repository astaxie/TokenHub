package plugin

import (
	"fmt"
	"strings"
)

var supportedPluginSettingScopes = map[string]struct{}{
	"browser":       {},
	"administrator": {},
	"organization":  {},
	"project":       {},
	"provider":      {},
}

func supportedManifestSchemaPair(schemaVersion int, pluginAPI string) bool {
	switch strings.TrimSpace(pluginAPI) {
	case PluginAPIV1:
		return schemaVersion == PluginManifestSchemaV1
	case PluginAPIV2:
		return schemaVersion == PluginManifestSchemaV2
	default:
		return schemaVersion == PluginManifestSchemaV1 || schemaVersion == PluginManifestSchemaV2
	}
}

func validateManifestV2Metadata(manifest Manifest) error {
	if manifest.TokenHub.PluginAPI != PluginAPIV2 {
		return nil
	}
	if !validCategory(manifest.Category) {
		return fmt.Errorf("plugin API v2 category must be one of provider_integration, request_pipeline, ui_template, or automation")
	}
	if strings.TrimSpace(manifest.Summary) == "" {
		return fmt.Errorf("plugin API v2 summary is required")
	}
	if manifestHasKind(manifest.Kinds, KindProvider) && manifest.Entry.Backend == nil && strings.TrimSpace(manifest.HostAdapter) == "" {
		return fmt.Errorf("declarative provider plugin host_adapter is required")
	}
	seenDependencies := map[string]struct{}{}
	for _, dependency := range manifest.Dependencies {
		id := strings.TrimSpace(dependency.ID)
		if id == "" || id == strings.TrimSpace(manifest.ID) {
			return fmt.Errorf("plugin dependency id must name another plugin")
		}
		if !safePluginContractToken(id) {
			return fmt.Errorf("plugin dependency id %q is invalid", dependency.ID)
		}
		if _, exists := seenDependencies[id]; exists {
			return fmt.Errorf("plugin dependency %q is duplicated", id)
		}
		if err := ValidatePluginVersionConstraint(dependency.Version); err != nil {
			return fmt.Errorf("plugin dependency %q version: %w", id, err)
		}
		seenDependencies[id] = struct{}{}
	}
	seenScopes := map[string]struct{}{}
	for _, rawScope := range manifest.Settings.Scopes {
		scope := strings.TrimSpace(rawScope)
		if _, supported := supportedPluginSettingScopes[scope]; !supported {
			return fmt.Errorf("unsupported plugin settings scope %q", rawScope)
		}
		if _, exists := seenScopes[scope]; exists {
			return fmt.Errorf("plugin settings scope %q is duplicated", scope)
		}
		seenScopes[scope] = struct{}{}
	}
	return nil
}

func validCategory(category Category) bool {
	switch category {
	case CategoryProviderIntegration, CategoryRequestPipeline, CategoryUITemplate, CategoryAutomation:
		return true
	default:
		return false
	}
}
