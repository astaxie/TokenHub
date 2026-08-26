package plugin

import (
	"encoding/json"
	"fmt"
	"net/url"
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
	Distribution  ManifestDistribution  `yaml:"distribution"`
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

type ManifestDistribution struct {
	MarketplaceURL string `json:"marketplace_url,omitempty" yaml:"marketplace_url"`
	RepositoryURL  string `json:"repository_url,omitempty" yaml:"repository_url"`
	DownloadURL    string `json:"download_url,omitempty" yaml:"download_url"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty" yaml:"checksum_sha256"`
	SignatureURL   string `json:"signature_url,omitempty" yaml:"signature_url"`
	HomepageURL    string `json:"homepage_url,omitempty" yaml:"homepage_url"`
	License        string `json:"license,omitempty" yaml:"license"`
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
	ProviderTypes []string                       `yaml:"provider_types"`
	ResourceTypes []ManifestProviderResourceType `yaml:"provider_resource_types"`
	Provider      ManifestProvider               `yaml:"provider"`
	Gateway       []string                       `yaml:"gateway"`
	AdminUI       []string                       `yaml:"admin_ui"`
	Hooks         []GatewayHookManifest          `yaml:"hooks"`
	Actions       []ActionManifest               `yaml:"actions"`
	Background    []BackgroundJobManifest        `yaml:"background_jobs"`
}

type ManifestProvider struct {
	RouteProtocols        []string                `yaml:"route_protocols"`
	SupportsCustomHeaders *bool                   `yaml:"supports_custom_headers"`
	Catalog               ManifestProviderCatalog `yaml:"catalog"`
}

type ManifestProviderResourceType struct {
	Type        string            `json:"type" yaml:"type"`
	DisplayName string            `json:"display_name,omitempty" yaml:"display_name"`
	AuthModes   []string          `json:"auth_modes,omitempty" yaml:"auth_modes"`
	Defaults    map[string]string `json:"defaults,omitempty" yaml:"defaults"`
	Default     bool              `json:"default,omitempty" yaml:"default"`
}

func (resourceType *ManifestProviderResourceType) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		resourceType.Type = strings.TrimSpace(value.Value)
		return nil
	case yaml.MappingNode:
		type alias ManifestProviderResourceType
		var parsed alias
		if err := value.Decode(&parsed); err != nil {
			return err
		}
		*resourceType = ManifestProviderResourceType(parsed)
		resourceType.Type = strings.TrimSpace(resourceType.Type)
		resourceType.DisplayName = strings.TrimSpace(resourceType.DisplayName)
		resourceType.AuthModes = normalizeStrings(resourceType.AuthModes)
		resourceType.Defaults = normalizeStringMap(resourceType.Defaults)
		return nil
	default:
		return fmt.Errorf("provider resource type must be a string or object")
	}
}

type ManifestProviderCatalog struct {
	ID          string                         `json:"id,omitempty" yaml:"id"`
	Name        string                         `json:"name,omitempty" yaml:"name"`
	DisplayName string                         `json:"display_name,omitempty" yaml:"display_name"`
	Type        string                         `json:"type,omitempty" yaml:"type"`
	BaseURL     string                         `json:"base_url,omitempty" yaml:"base_url"`
	DocURL      string                         `json:"doc_url,omitempty" yaml:"doc_url"`
	Categories  []string                       `json:"categories,omitempty" yaml:"categories"`
	ModelsCount int                            `json:"models_count,omitempty" yaml:"models_count"`
	Models      []ManifestProviderCatalogModel `json:"models,omitempty" yaml:"models"`
	ETag        string                         `json:"etag,omitempty" yaml:"etag"`
}

type ManifestProviderCatalogModel struct {
	ID                        string            `json:"id" yaml:"id"`
	Name                      string            `json:"name,omitempty" yaml:"name"`
	DisplayName               string            `json:"display_name,omitempty" yaml:"display_name"`
	CanonicalName             string            `json:"canonical_name,omitempty" yaml:"canonical_name"`
	Category                  string            `json:"category,omitempty" yaml:"category"`
	Family                    string            `json:"family,omitempty" yaml:"family"`
	Type                      string            `json:"type,omitempty" yaml:"type"`
	ContextWindow             int64             `json:"context_window,omitempty" yaml:"context_window"`
	MaxOutputTokens           int64             `json:"max_output_tokens,omitempty" yaml:"max_output_tokens"`
	InputPriceUSDPer1M        float64           `json:"input_price_usd_per_1m,omitempty" yaml:"input_price_usd_per_1m"`
	CacheReadPriceUSDPer1M    float64           `json:"cache_read_price_usd_per_1m,omitempty" yaml:"cache_read_price_usd_per_1m"`
	CacheWritePriceUSDPer1M   float64           `json:"cache_write_price_usd_per_1m,omitempty" yaml:"cache_write_price_usd_per_1m"`
	CacheWrite5mPriceUSDPer1M float64           `json:"cache_write_5m_price_usd_per_1m,omitempty" yaml:"cache_write_5m_price_usd_per_1m"`
	CacheWrite1hPriceUSDPer1M float64           `json:"cache_write_1h_price_usd_per_1m,omitempty" yaml:"cache_write_1h_price_usd_per_1m"`
	OutputPriceUSDPer1M       float64           `json:"output_price_usd_per_1m,omitempty" yaml:"output_price_usd_per_1m"`
	InputModalities           []string          `json:"input_modalities,omitempty" yaml:"input_modalities"`
	OutputModalities          []string          `json:"output_modalities,omitempty" yaml:"output_modalities"`
	Capabilities              []string          `json:"capabilities,omitempty" yaml:"capabilities"`
	SupportedParameters       []string          `json:"supported_parameters,omitempty" yaml:"supported_parameters"`
	LastUpdated               string            `json:"last_updated,omitempty" yaml:"last_updated"`
	Metadata                  map[string]string `json:"metadata,omitempty" yaml:"metadata"`
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

type ActionManifest struct {
	ID           string            `yaml:"id"`
	Kind         ActionKind        `yaml:"kind"`
	Title        string            `yaml:"title"`
	Capability   string            `yaml:"capability"`
	Subject      string            `yaml:"subject"`
	Metadata     map[string]string `yaml:"metadata"`
	InputSchema  map[string]any    `yaml:"input_schema"`
	OutputSchema map[string]any    `yaml:"output_schema"`
}

type BackgroundJobManifest struct {
	ID             string                   `yaml:"id"`
	Title          string                   `yaml:"title"`
	Capability     string                   `yaml:"capability"`
	Subject        string                   `yaml:"subject"`
	Schedule       string                   `yaml:"schedule"`
	TimeoutMillis  int                      `yaml:"timeout_millis"`
	MaxConcurrency int                      `yaml:"max_concurrency"`
	Retry          BackgroundJobRetryPolicy `yaml:"retry"`
	InputSchema    map[string]any           `yaml:"input_schema"`
	OutputSchema   map[string]any           `yaml:"output_schema"`
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
	if err := m.Distribution.Validate(); err != nil {
		return err
	}
	if m.Entry.Backend != nil {
		protocol := strings.TrimSpace(m.Entry.Backend.Protocol)
		if protocol != "" && protocol != BackendProtocolStdioJSONV1 {
			return fmt.Errorf("unsupported backend protocol %q", m.Entry.Backend.Protocol)
		}
	}
	if len(NormalizeDescriptor(Descriptor{Kinds: m.Kinds}).Kinds) == 0 {
		return fmt.Errorf("at least one plugin kind is required")
	}
	for _, kind := range m.Kinds {
		if !validKind(kind) {
			return fmt.Errorf("unsupported plugin kind %q", kind)
		}
	}
	for _, resourceType := range m.Capabilities.ResourceTypes {
		if strings.TrimSpace(resourceType.Type) == "" {
			return fmt.Errorf("provider resource type is required")
		}
	}
	if err := m.validateProviderCatalog(); err != nil {
		return err
	}
	for _, placement := range m.Placement {
		if !validPlacement(placement) {
			return fmt.Errorf("unsupported plugin placement %q", placement)
		}
	}
	if (m.hasFrontendSchema() || len(m.Capabilities.AdminUI) > 0) && !manifestHasPlacement(m.Placement, PlacementPresentation) {
		return fmt.Errorf("admin UI capabilities require presentation placement")
	}
	for _, hook := range m.Capabilities.Hooks {
		if strings.TrimSpace(hook.ID) == "" {
			return fmt.Errorf("gateway hook id is required")
		}
		if !manifestHasPlacement(m.Placement, PlacementGatewayChain) {
			return fmt.Errorf("gateway hook %s requires gateway_chain placement", hook.ID)
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
	for _, action := range m.Capabilities.Actions {
		descriptor := NormalizeActionDescriptor(ActionDescriptor{
			PluginID:     m.ID,
			ActionID:     action.ID,
			Kind:         action.Kind,
			Title:        action.Title,
			Capability:   action.Capability,
			Subject:      action.Subject,
			Metadata:     action.Metadata,
			InputSchema:  action.InputSchema,
			OutputSchema: action.OutputSchema,
		})
		if descriptor.ActionID == "" {
			return fmt.Errorf("plugin action id is required")
		}
		if !validActionKind(descriptor.Kind) {
			return fmt.Errorf("unsupported plugin action kind %q", descriptor.Kind)
		}
		if !manifestHasPlacement(m.Placement, PlacementManagementAction) {
			return fmt.Errorf("plugin action %s requires management_action placement", descriptor.ActionID)
		}
	}
	for _, job := range m.Capabilities.Background {
		descriptor := NormalizeBackgroundJobDescriptor(BackgroundJobDescriptor{
			PluginID:       m.ID,
			JobID:          job.ID,
			Title:          job.Title,
			Capability:     job.Capability,
			Subject:        job.Subject,
			Schedule:       job.Schedule,
			TimeoutMillis:  job.TimeoutMillis,
			MaxConcurrency: job.MaxConcurrency,
			Retry:          job.Retry,
			InputSchema:    job.InputSchema,
			OutputSchema:   job.OutputSchema,
		})
		if descriptor.JobID == "" {
			return fmt.Errorf("plugin background job id is required")
		}
		if descriptor.Schedule == "" {
			return fmt.Errorf("plugin background job %s schedule is required", descriptor.JobID)
		}
		if descriptor.TimeoutMillis < 0 {
			return fmt.Errorf("plugin background job %s timeout_millis cannot be negative", descriptor.JobID)
		}
		if descriptor.MaxConcurrency <= 0 {
			return fmt.Errorf("plugin background job %s max_concurrency must be positive", descriptor.JobID)
		}
		if descriptor.Retry.MaxAttempts < 0 {
			return fmt.Errorf("plugin background job %s retry max_attempts cannot be negative", descriptor.JobID)
		}
		if descriptor.Retry.BackoffMillis < 0 {
			return fmt.Errorf("plugin background job %s retry backoff_millis cannot be negative", descriptor.JobID)
		}
		if !manifestHasPlacement(m.Placement, PlacementBackground) {
			return fmt.Errorf("plugin background job %s requires background placement", descriptor.JobID)
		}
	}
	return nil
}

func (m Manifest) validateProviderCatalog() error {
	if !m.Capabilities.Provider.Catalog.Configured() {
		return nil
	}
	if len(m.Capabilities.ProviderTypes) == 0 {
		return fmt.Errorf("provider catalog requires at least one provider type")
	}
	if !manifestHasKind(m.Kinds, KindProvider) {
		return fmt.Errorf("provider catalog requires provider kind")
	}
	catalog := m.Capabilities.Provider.Catalog
	if strings.TrimSpace(catalog.ID) != "" && strings.Contains(strings.TrimSpace(catalog.ID), "/") {
		return fmt.Errorf("provider catalog id must not contain /")
	}
	if catalog.ModelsCount < 0 {
		return fmt.Errorf("provider catalog models_count cannot be negative")
	}
	for index, model := range catalog.Models {
		if strings.TrimSpace(model.ID) == "" {
			return fmt.Errorf("provider catalog model %d id is required", index)
		}
	}
	return nil
}

func (m Manifest) hasFrontendSchema() bool {
	return m.Entry.Frontend != nil && strings.TrimSpace(m.Entry.Frontend.Schema) != ""
}

func (m Manifest) Descriptor() Descriptor {
	descriptor := Descriptor{
		ID:           m.ID,
		Name:         m.Name,
		Version:      m.Version,
		Source:       SourceLocalFile,
		Distribution: m.Distribution.Descriptor(),
		Kinds:        m.Kinds,
		Placements:   m.Placement,
	}
	for _, providerType := range m.Capabilities.ProviderTypes {
		descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
			Kind: "provider_type",
			Name: providerType,
		})
		if m.Capabilities.Provider.SupportsCustomHeaders != nil {
			descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
				Kind:    "provider_policy",
				Name:    "supports_custom_headers",
				Subject: providerType,
				Value:   fmt.Sprintf("%t", *m.Capabilities.Provider.SupportsCustomHeaders),
			})
		}
		for _, protocol := range m.Capabilities.Provider.RouteProtocols {
			protocol = strings.TrimSpace(protocol)
			if protocol == "" {
				continue
			}
			descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
				Kind:    "provider_policy",
				Name:    "route_protocol",
				Subject: providerType,
				Value:   protocol,
			})
		}
		for _, resourceType := range m.Capabilities.ResourceTypes {
			descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
				Kind:    "provider_resource_type",
				Name:    resourceType.Type,
				Subject: providerType,
				Value:   resourceType.CapabilityValue(),
			})
		}
		for _, capability := range m.Capabilities.Gateway {
			descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
				Kind:    "provider",
				Name:    capability,
				Subject: providerType,
			})
		}
		if catalogCapability, ok := m.Capabilities.Provider.Catalog.CapabilityValue(providerType); ok {
			descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
				Kind:    "provider_catalog",
				Name:    "entry",
				Subject: providerType,
				Value:   catalogCapability,
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
	for _, action := range m.Capabilities.Actions {
		descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
			Kind: "management_action",
			Name: strings.TrimSpace(action.ID),
		})
	}
	for _, job := range m.Capabilities.Background {
		descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
			Kind:    "background_job",
			Name:    strings.TrimSpace(job.ID),
			Subject: strings.TrimSpace(job.Subject),
			Value:   strings.TrimSpace(job.Capability),
		})
	}
	return NormalizeDescriptor(descriptor)
}

func (distribution ManifestDistribution) Descriptor() *Distribution {
	distribution = distribution.Normalized()
	if distribution.MarketplaceURL == "" && distribution.RepositoryURL == "" && distribution.DownloadURL == "" &&
		distribution.ChecksumSHA256 == "" && distribution.SignatureURL == "" && distribution.HomepageURL == "" && distribution.License == "" {
		return nil
	}
	return &Distribution{
		MarketplaceURL: distribution.MarketplaceURL,
		RepositoryURL:  distribution.RepositoryURL,
		DownloadURL:    distribution.DownloadURL,
		ChecksumSHA256: distribution.ChecksumSHA256,
		SignatureURL:   distribution.SignatureURL,
		HomepageURL:    distribution.HomepageURL,
		License:        distribution.License,
	}
}

func (distribution ManifestDistribution) Normalized() ManifestDistribution {
	return ManifestDistribution{
		MarketplaceURL: strings.TrimSpace(distribution.MarketplaceURL),
		RepositoryURL:  strings.TrimSpace(distribution.RepositoryURL),
		DownloadURL:    strings.TrimSpace(distribution.DownloadURL),
		ChecksumSHA256: strings.ToLower(strings.TrimSpace(distribution.ChecksumSHA256)),
		SignatureURL:   strings.TrimSpace(distribution.SignatureURL),
		HomepageURL:    strings.TrimSpace(distribution.HomepageURL),
		License:        strings.TrimSpace(distribution.License),
	}
}

func (distribution *ManifestDistribution) Validate() error {
	*distribution = distribution.Normalized()
	for label, value := range map[string]string{
		"marketplace_url": distribution.MarketplaceURL,
		"repository_url":  distribution.RepositoryURL,
		"download_url":    distribution.DownloadURL,
		"signature_url":   distribution.SignatureURL,
		"homepage_url":    distribution.HomepageURL,
	} {
		if value == "" {
			continue
		}
		if err := validatePluginDistributionURL(label, value); err != nil {
			return err
		}
	}
	if distribution.ChecksumSHA256 != "" && !isHexSHA256(distribution.ChecksumSHA256) {
		return fmt.Errorf("distribution.checksum_sha256 must be a lowercase SHA-256 hex digest")
	}
	return nil
}

func validatePluginDistributionURL(label string, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("distribution.%s must be an absolute URL", label)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("distribution.%s must use HTTPS", label)
	}
	return nil
}

func isHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func (resourceType ManifestProviderResourceType) CapabilityValue() string {
	if strings.TrimSpace(resourceType.DisplayName) == "" && len(resourceType.AuthModes) == 0 && len(resourceType.Defaults) == 0 && !resourceType.Default {
		return ""
	}
	resourceType.Type = strings.TrimSpace(resourceType.Type)
	resourceType.DisplayName = strings.TrimSpace(resourceType.DisplayName)
	resourceType.AuthModes = normalizeStrings(resourceType.AuthModes)
	resourceType.Defaults = normalizeStringMap(resourceType.Defaults)
	data, err := json.Marshal(resourceType)
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizeStringMap(items map[string]string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(items))
	for key, value := range items {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		normalized[key] = strings.TrimSpace(value)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (catalog ManifestProviderCatalog) Configured() bool {
	return strings.TrimSpace(catalog.ID) != "" ||
		strings.TrimSpace(catalog.Name) != "" ||
		strings.TrimSpace(catalog.DisplayName) != "" ||
		strings.TrimSpace(catalog.Type) != "" ||
		strings.TrimSpace(catalog.BaseURL) != "" ||
		strings.TrimSpace(catalog.DocURL) != "" ||
		strings.TrimSpace(catalog.ETag) != "" ||
		len(catalog.Categories) > 0 ||
		catalog.ModelsCount > 0 ||
		len(catalog.Models) > 0
}

func (catalog ManifestProviderCatalog) CapabilityValue(providerType string) (string, bool) {
	if !catalog.Configured() {
		return "", false
	}
	catalog.Type = firstNonEmptyString(catalog.Type, providerType)
	if catalog.ID == "" {
		catalog.ID = catalog.Type
	}
	if catalog.Name == "" {
		catalog.Name = firstNonEmptyString(catalog.DisplayName, catalog.ID)
	}
	if catalog.DisplayName == "" {
		catalog.DisplayName = catalog.Name
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		return "", false
	}
	return string(data), true
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

func (m Manifest) Actions() []ActionDescriptor {
	actions := make([]ActionDescriptor, 0, len(m.Capabilities.Actions))
	for _, action := range m.Capabilities.Actions {
		actions = append(actions, NormalizeActionDescriptor(ActionDescriptor{
			PluginID:     m.ID,
			ActionID:     action.ID,
			Kind:         action.Kind,
			Title:        action.Title,
			Capability:   action.Capability,
			Subject:      action.Subject,
			Metadata:     action.Metadata,
			InputSchema:  action.InputSchema,
			OutputSchema: action.OutputSchema,
		}))
	}
	return actions
}

func (m Manifest) BackgroundJobs() []BackgroundJobDescriptor {
	jobs := make([]BackgroundJobDescriptor, 0, len(m.Capabilities.Background))
	for _, job := range m.Capabilities.Background {
		jobs = append(jobs, NormalizeBackgroundJobDescriptor(BackgroundJobDescriptor{
			PluginID:       m.ID,
			JobID:          job.ID,
			Title:          job.Title,
			Capability:     job.Capability,
			Subject:        job.Subject,
			Schedule:       job.Schedule,
			TimeoutMillis:  job.TimeoutMillis,
			MaxConcurrency: job.MaxConcurrency,
			Retry:          job.Retry,
			InputSchema:    job.InputSchema,
			OutputSchema:   job.OutputSchema,
		}))
	}
	return jobs
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

func manifestHasPlacement(placements []Placement, want Placement) bool {
	for _, placement := range placements {
		if placement == want {
			return true
		}
	}
	return false
}

func manifestHasKind(kinds []Kind, want Kind) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
