package plugin

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

type PermissionKind string

const (
	PermissionKindData    PermissionKind = "data"
	PermissionKindNetwork PermissionKind = "network"
)

type PermissionAccess string

const (
	PermissionAccessRead    PermissionAccess = "read"
	PermissionAccessWrite   PermissionAccess = "write"
	PermissionAccessConnect PermissionAccess = "connect"
)

type PermissionSensitivity string

const (
	PermissionSensitivityPublic    PermissionSensitivity = "public"
	PermissionSensitivityInternal  PermissionSensitivity = "internal"
	PermissionSensitivitySensitive PermissionSensitivity = "sensitive"
	PermissionSensitivitySecret    PermissionSensitivity = "secret"
)

type PermissionDescriptor struct {
	Kind        PermissionKind        `json:"kind"`
	Name        string                `json:"name"`
	Access      PermissionAccess      `json:"access"`
	Sensitivity PermissionSensitivity `json:"sensitivity"`
}

func SupportedPermissionKinds() []string {
	return []string{string(PermissionKindData), string(PermissionKindNetwork)}
}

func SupportedPermissionSensitivities() []string {
	return []string{
		string(PermissionSensitivityPublic),
		string(PermissionSensitivityInternal),
		string(PermissionSensitivitySensitive),
		string(PermissionSensitivitySecret),
	}
}

func SupportedGatewayDataClasses() []string {
	classes := make([]string, 0, len(allGatewayDataClasses()))
	for _, dataClass := range allGatewayDataClasses() {
		classes = append(classes, string(dataClass))
	}
	sort.Strings(classes)
	return classes
}

func ValidateManifestPermissions(permissions ManifestPermissions) error {
	if err := validateGatewayDataClasses(permissions.Data.Read); err != nil {
		return fmt.Errorf("permissions.data.read: %w", err)
	}
	if err := validateGatewayDataClasses(permissions.Data.Write); err != nil {
		return fmt.Errorf("permissions.data.write: %w", err)
	}
	for _, target := range permissions.Network.Allow {
		if !validNetworkPermissionTarget(target) {
			return fmt.Errorf("permissions.network.allow contains invalid target %q", target)
		}
	}
	return nil
}

func ValidatePermissionDescriptor(permission PermissionDescriptor) error {
	kind := PermissionKind(strings.TrimSpace(string(permission.Kind)))
	name := strings.TrimSpace(permission.Name)
	access := PermissionAccess(strings.TrimSpace(string(permission.Access)))
	sensitivity := PermissionSensitivity(strings.TrimSpace(string(permission.Sensitivity)))
	if kind == "" || name == "" || access == "" {
		return pluginContractErrorf(PluginErrorPermissionRequired, "plugin permission kind, name, and access are required")
	}
	switch kind {
	case PermissionKindData:
		if access != PermissionAccessRead && access != PermissionAccessWrite {
			return pluginContractErrorf(PluginErrorPermissionUnsupported, "unsupported data permission access %q", permission.Access)
		}
		if !validGatewayDataClass(GatewayDataClass(name)) {
			return pluginContractErrorf(PluginErrorPermissionUnsupported, "unsupported data permission %q", permission.Name)
		}
	case PermissionKindNetwork:
		if access != PermissionAccessConnect {
			return pluginContractErrorf(PluginErrorPermissionUnsupported, "unsupported network permission access %q", permission.Access)
		}
		if !validNetworkPermissionTarget(name) {
			return pluginContractErrorf(PluginErrorPermissionUnsupported, "unsupported network permission target %q", permission.Name)
		}
	default:
		return pluginContractErrorf(PluginErrorPermissionUnsupported, "unsupported plugin permission kind %q", permission.Kind)
	}
	if !validPermissionSensitivity(sensitivity) {
		return pluginContractErrorf(PluginErrorPermissionUnsupported, "unsupported plugin permission sensitivity %q", permission.Sensitivity)
	}
	return nil
}

func ManifestPermissionDescriptors(permissions ManifestPermissions) []PermissionDescriptor {
	descriptors := make([]PermissionDescriptor, 0, len(permissions.Data.Read)+len(permissions.Data.Write)+len(permissions.Network.Allow))
	for _, dataClass := range normalizeDataClasses(permissions.Data.Read) {
		descriptors = append(descriptors, dataPermissionDescriptor(dataClass, PermissionAccessRead))
	}
	for _, dataClass := range normalizeDataClasses(permissions.Data.Write) {
		descriptors = append(descriptors, dataPermissionDescriptor(dataClass, PermissionAccessWrite))
	}
	for _, target := range normalizeStrings(permissions.Network.Allow) {
		if target == "" {
			continue
		}
		descriptors = append(descriptors, PermissionDescriptor{
			Kind:        PermissionKindNetwork,
			Name:        target,
			Access:      PermissionAccessConnect,
			Sensitivity: PermissionSensitivityInternal,
		})
	}
	return NormalizePermissionDescriptors(descriptors)
}

func NormalizePermissionDescriptors(items []PermissionDescriptor) []PermissionDescriptor {
	seen := map[PermissionDescriptor]struct{}{}
	normalized := make([]PermissionDescriptor, 0, len(items))
	for _, item := range items {
		item.Kind = PermissionKind(strings.TrimSpace(string(item.Kind)))
		item.Name = strings.TrimSpace(item.Name)
		item.Access = PermissionAccess(strings.TrimSpace(string(item.Access)))
		item.Sensitivity = PermissionSensitivity(strings.TrimSpace(string(item.Sensitivity)))
		if item.Kind == "" || item.Name == "" || item.Access == "" {
			continue
		}
		if item.Sensitivity == "" {
			item.Sensitivity = PermissionSensitivityFor(item.Kind, item.Name)
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Kind != normalized[j].Kind {
			return normalized[i].Kind < normalized[j].Kind
		}
		if normalized[i].Name != normalized[j].Name {
			return normalized[i].Name < normalized[j].Name
		}
		return normalized[i].Access < normalized[j].Access
	})
	return normalized
}

func PermissionSensitivityFor(kind PermissionKind, name string) PermissionSensitivity {
	if kind != PermissionKindData {
		return PermissionSensitivityInternal
	}
	switch GatewayDataClass(name) {
	case DataProviderCredentials:
		return PermissionSensitivitySecret
	case DataAuthContext,
		DataAPIKeyMetadata,
		DataRequestHeaders,
		DataRequestBody,
		DataNormalizedText,
		DataFileContent,
		DataImageContent,
		DataProviderRequest,
		DataProviderResponse,
		DataStreamEvents,
		DataCacheKey,
		DataCacheValue:
		return PermissionSensitivitySensitive
	case DataProjectMetadata,
		DataFileMetadata,
		DataImageMetadata,
		DataToolSchema,
		DataRouteCandidates,
		DataUsage,
		DataAudit:
		return PermissionSensitivityInternal
	default:
		return PermissionSensitivityInternal
	}
}

func dataPermissionDescriptor(dataClass GatewayDataClass, access PermissionAccess) PermissionDescriptor {
	return PermissionDescriptor{
		Kind:        PermissionKindData,
		Name:        string(dataClass),
		Access:      access,
		Sensitivity: PermissionSensitivityFor(PermissionKindData, string(dataClass)),
	}
}

func validNetworkPermissionTarget(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if target == "*" {
		return true
	}
	for _, char := range target {
		if unicode.IsControl(char) || unicode.IsSpace(char) {
			return false
		}
	}
	if strings.Contains(target, "..") || strings.Contains(target, "\\") || strings.ContainsAny(target, "<>\"'") {
		return false
	}
	parsed, err := url.Parse(target)
	if err == nil && parsed.Scheme != "" {
		return (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
	}
	return safePluginContractToken(strings.TrimPrefix(target, "*."))
}

func allGatewayDataClasses() []GatewayDataClass {
	return []GatewayDataClass{
		DataAPIKeyMetadata,
		DataAudit,
		DataAuthContext,
		DataCacheKey,
		DataCacheValue,
		DataFileContent,
		DataFileMetadata,
		DataImageContent,
		DataImageMetadata,
		DataNormalizedText,
		DataProjectMetadata,
		DataProviderCredentials,
		DataProviderRequest,
		DataProviderResponse,
		DataRequestBody,
		DataRequestHeaders,
		DataRouteCandidates,
		DataStreamEvents,
		DataToolSchema,
		DataUsage,
	}
}

func validPermissionSensitivity(sensitivity PermissionSensitivity) bool {
	switch sensitivity {
	case PermissionSensitivityPublic,
		PermissionSensitivityInternal,
		PermissionSensitivitySensitive,
		PermissionSensitivitySecret:
		return true
	default:
		return false
	}
}
