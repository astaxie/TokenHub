package plugin

import (
	"fmt"
	"sort"
	"strings"
)

const (
	DefaultGatewayHookTimeoutMillis = 5000
	MaxGatewayHookTimeoutMillis     = 60000
)

type GatewayHookStage string

const (
	StageAuthContext      GatewayHookStage = "auth_context"
	StageDecodeNormalize  GatewayHookStage = "decode_normalize"
	StageAdmission        GatewayHookStage = "admission"
	StagePrivacyPre       GatewayHookStage = "privacy_pre"
	StageGuardrailPre     GatewayHookStage = "guardrail_pre"
	StageContextOptimize  GatewayHookStage = "context_optimize"
	StageCacheLookup      GatewayHookStage = "cache_lookup"
	StageRouteCandidates  GatewayHookStage = "route_candidates"
	StageRouteRank        GatewayHookStage = "route_rank"
	StageRequestTransform GatewayHookStage = "request_transform"
	StageProviderCall     GatewayHookStage = "provider_call"
	StageStreamTransform  GatewayHookStage = "stream_transform"
	StageResponsePost     GatewayHookStage = "response_post"
	StageGuardrailPost    GatewayHookStage = "guardrail_post"
	StageUsageAttribution GatewayHookStage = "usage_attribution"
	StageCacheWrite       GatewayHookStage = "cache_write"
	StageSettlement       GatewayHookStage = "settlement"
	StageTraceExport      GatewayHookStage = "trace_export"
)

type GatewayHookFailurePolicy string

const (
	FailurePolicyFailClosed     GatewayHookFailurePolicy = "fail_closed"
	FailurePolicyFailOpen       GatewayHookFailurePolicy = "fail_open"
	FailurePolicySkipRoute      GatewayHookFailurePolicy = "skip_route"
	FailurePolicyReturnFallback GatewayHookFailurePolicy = "return_fallback"
	FailurePolicyObserveOnly    GatewayHookFailurePolicy = "observe_only"
)

type GatewayDataClass string

const (
	DataAuthContext         GatewayDataClass = "auth_context"
	DataProjectMetadata     GatewayDataClass = "project_metadata"
	DataAPIKeyMetadata      GatewayDataClass = "api_key_metadata"
	DataRequestHeaders      GatewayDataClass = "request_headers"
	DataRequestBody         GatewayDataClass = "request_body"
	DataNormalizedText      GatewayDataClass = "normalized_text"
	DataFileMetadata        GatewayDataClass = "file_metadata"
	DataFileContent         GatewayDataClass = "file_content"
	DataImageMetadata       GatewayDataClass = "image_metadata"
	DataImageContent        GatewayDataClass = "image_content"
	DataToolSchema          GatewayDataClass = "tool_schema"
	DataRouteCandidates     GatewayDataClass = "route_candidates"
	DataProviderCredentials GatewayDataClass = "provider_credentials"
	DataProviderRequest     GatewayDataClass = "provider_request"
	DataProviderResponse    GatewayDataClass = "provider_response"
	DataStreamEvents        GatewayDataClass = "stream_events"
	DataUsage               GatewayDataClass = "usage"
	DataAudit               GatewayDataClass = "audit"
	DataCacheKey            GatewayDataClass = "cache_key"
	DataCacheValue          GatewayDataClass = "cache_value"
)

type GatewayHookDescriptor struct {
	PluginID      string                   `json:"plugin_id"`
	HookID        string                   `json:"hook_id"`
	Stage         GatewayHookStage         `json:"stage"`
	Priority      int                      `json:"priority"`
	Subject       string                   `json:"subject,omitempty"`
	Metadata      map[string]string        `json:"metadata,omitempty"`
	Scope         GatewayHookScope         `json:"scope,omitempty"`
	Reads         []GatewayDataClass       `json:"reads,omitempty"`
	Writes        []GatewayDataClass       `json:"writes,omitempty"`
	FailurePolicy GatewayHookFailurePolicy `json:"failure_policy"`
	TimeoutMillis int                      `json:"timeout_millis"`
	Mandatory     bool                     `json:"mandatory"`
}

type GatewayHookScope struct {
	ProjectIDs     []string `json:"project_ids,omitempty" yaml:"project_ids"`
	APIKeyIDs      []string `json:"api_key_ids,omitempty" yaml:"api_key_ids"`
	ProviderTypes  []string `json:"provider_types,omitempty" yaml:"provider_types"`
	ProviderIDs    []string `json:"provider_ids,omitempty" yaml:"provider_ids"`
	ResourceIDs    []string `json:"resource_ids,omitempty" yaml:"resource_ids"`
	ResourceTypes  []string `json:"resource_types,omitempty" yaml:"resource_types"`
	RouteProtocols []string `json:"route_protocols,omitempty" yaml:"route_protocols"`
	Operations     []string `json:"operations,omitempty" yaml:"operations"`
}

type GatewayHookScopeTarget struct {
	ProjectID     string
	APIKeyID      string
	ProviderType  string
	ProviderID    string
	ResourceID    string
	ResourceType  string
	RouteProtocol string
	Operation     string
}

type GatewayChainPlan struct {
	Stages    []GatewayHookStage             `json:"stages"`
	Envelopes []GatewayStageEnvelopeContract `json:"envelopes"`
	Hooks     []GatewayHookDescriptor        `json:"hooks"`
}

type GatewayChainRegistry struct {
	hooks map[GatewayHookStage][]GatewayHookDescriptor
}

type GatewayHookStagePolicy struct {
	DefaultFailurePolicy GatewayHookFailurePolicy
	AllowedFailurePolicy []GatewayHookFailurePolicy
	Reads                []GatewayDataClass
	Writes               []GatewayDataClass
	AllowsDeny           bool
	AllowsShortCircuit   bool
}

type GatewayStageEnvelopeContract struct {
	Stage              GatewayHookStage           `json:"stage"`
	Reads              []GatewayDataClass         `json:"reads"`
	Writes             []GatewayDataClass         `json:"writes"`
	Preserves          []GatewayDataClass         `json:"preserves"`
	AllowsDeny         bool                       `json:"allows_deny"`
	AllowsShortCircuit bool                       `json:"allows_short_circuit"`
	DefaultFailure     GatewayHookFailurePolicy   `json:"default_failure_policy"`
	AllowedFailures    []GatewayHookFailurePolicy `json:"allowed_failure_policies"`
	DefaultTimeoutMS   int                        `json:"default_timeout_ms"`
	MaxTimeoutMS       int                        `json:"max_timeout_ms"`
}

func NewGatewayChainRegistry() *GatewayChainRegistry {
	return &GatewayChainRegistry{hooks: map[GatewayHookStage][]GatewayHookDescriptor{}}
}

func (r *GatewayChainRegistry) RegisterHook(descriptor GatewayHookDescriptor) error {
	if r == nil {
		return fmt.Errorf("gateway chain registry is not configured")
	}
	descriptor = NormalizeGatewayHookDescriptor(descriptor)
	if descriptor.PluginID == "" {
		return fmt.Errorf("gateway hook plugin id is required")
	}
	if descriptor.HookID == "" {
		return fmt.Errorf("gateway hook id is required")
	}
	if descriptor.Stage == "" {
		return fmt.Errorf("gateway hook stage is required")
	}
	policy, ok := GatewayStagePolicy(descriptor.Stage)
	if !ok {
		return fmt.Errorf("unsupported gateway hook stage %q", descriptor.Stage)
	}
	if !gatewayFailurePolicyAllowed(descriptor.FailurePolicy, policy.AllowedFailurePolicy) {
		return fmt.Errorf("gateway hook stage %q does not allow failure policy %q", descriptor.Stage, descriptor.FailurePolicy)
	}
	if err := validateGatewayDataClasses(descriptor.Reads); err != nil {
		return err
	}
	if err := validateGatewayDataClasses(descriptor.Writes); err != nil {
		return err
	}
	if err := validateGatewayStageDataClasses(descriptor.Stage, "read", descriptor.Reads, policy.Reads); err != nil {
		return err
	}
	if err := validateGatewayStageDataClasses(descriptor.Stage, "write", descriptor.Writes, policy.Writes); err != nil {
		return err
	}
	if descriptor.TimeoutMillis < 0 {
		return fmt.Errorf("gateway hook %s/%s timeout_millis cannot be negative", descriptor.PluginID, descriptor.HookID)
	}
	if descriptor.TimeoutMillis > MaxGatewayHookTimeoutMillis {
		return fmt.Errorf("gateway hook %s/%s timeout_millis cannot exceed %d", descriptor.PluginID, descriptor.HookID, MaxGatewayHookTimeoutMillis)
	}
	r.hooks[descriptor.Stage] = append(r.hooks[descriptor.Stage], descriptor)
	sortGatewayHooks(r.hooks[descriptor.Stage])
	return nil
}

func (r *GatewayChainRegistry) Hooks(stage GatewayHookStage) []GatewayHookDescriptor {
	if r == nil {
		return nil
	}
	hooks := append([]GatewayHookDescriptor(nil), r.hooks[stage]...)
	sortGatewayHooks(hooks)
	return hooks
}

func (r *GatewayChainRegistry) Plan() GatewayChainPlan {
	if r == nil {
		return GatewayChainPlan{
			Stages:    OrderedGatewayStages(),
			Envelopes: GatewayStageEnvelopeContracts(),
		}
	}
	var hooks []GatewayHookDescriptor
	for _, stage := range OrderedGatewayStages() {
		hooks = append(hooks, r.Hooks(stage)...)
	}
	return GatewayChainPlan{
		Stages:    OrderedGatewayStages(),
		Envelopes: GatewayStageEnvelopeContracts(),
		Hooks:     hooks,
	}
}

func OrderedGatewayStages() []GatewayHookStage {
	return append([]GatewayHookStage(nil), canonicalGatewayStages...)
}

func GatewayStageEnvelopeContractFor(stage GatewayHookStage) (GatewayStageEnvelopeContract, bool) {
	policy, ok := GatewayStagePolicy(stage)
	if !ok {
		return GatewayStageEnvelopeContract{}, false
	}
	return GatewayStageEnvelopeContract{
		Stage:              stage,
		Reads:              append([]GatewayDataClass(nil), policy.Reads...),
		Writes:             append([]GatewayDataClass(nil), policy.Writes...),
		Preserves:          preservedGatewayDataClasses(policy.Reads, policy.Writes),
		AllowsDeny:         policy.AllowsDeny,
		AllowsShortCircuit: policy.AllowsShortCircuit,
		DefaultFailure:     policy.DefaultFailurePolicy,
		AllowedFailures:    append([]GatewayHookFailurePolicy(nil), policy.AllowedFailurePolicy...),
		DefaultTimeoutMS:   DefaultGatewayHookTimeoutMillis,
		MaxTimeoutMS:       MaxGatewayHookTimeoutMillis,
	}, true
}

func GatewayStageEnvelopeContracts() []GatewayStageEnvelopeContract {
	stages := OrderedGatewayStages()
	contracts := make([]GatewayStageEnvelopeContract, 0, len(stages))
	for _, stage := range stages {
		contract, ok := GatewayStageEnvelopeContractFor(stage)
		if ok {
			contracts = append(contracts, contract)
		}
	}
	return contracts
}

func orderedGatewayStages() []GatewayHookStage {
	return OrderedGatewayStages()
}

func NormalizeGatewayHookDescriptor(descriptor GatewayHookDescriptor) GatewayHookDescriptor {
	descriptor.PluginID = strings.TrimSpace(descriptor.PluginID)
	descriptor.HookID = strings.TrimSpace(descriptor.HookID)
	descriptor.Subject = strings.TrimSpace(descriptor.Subject)
	descriptor.Metadata = normalizeStringMap(descriptor.Metadata)
	if descriptor.FailurePolicy == "" {
		if policy, ok := GatewayStagePolicy(descriptor.Stage); ok {
			descriptor.FailurePolicy = policy.DefaultFailurePolicy
		}
	}
	if descriptor.TimeoutMillis == 0 {
		descriptor.TimeoutMillis = DefaultGatewayHookTimeoutMillis
	}
	descriptor.Reads = normalizeDataClasses(descriptor.Reads)
	descriptor.Writes = normalizeDataClasses(descriptor.Writes)
	descriptor.Scope = normalizeGatewayHookScope(mergeLegacyGatewayHookScope(descriptor.Scope, descriptor.Subject, descriptor.Metadata))
	if descriptor.FailurePolicy == FailurePolicyObserveOnly {
		descriptor.Writes = nil
	}
	return descriptor
}

func GatewayHookScopeMatches(hook GatewayHookDescriptor, target GatewayHookScopeTarget) bool {
	scope := NormalizeGatewayHookDescriptor(hook).Scope
	if !gatewayScopeListMatches(scope.ProjectIDs, target.ProjectID, false) {
		return false
	}
	if !gatewayScopeListMatches(scope.APIKeyIDs, target.APIKeyID, false) {
		return false
	}
	if !gatewayScopeListMatches(scope.ProviderTypes, target.ProviderType, true) {
		return false
	}
	if !gatewayScopeListMatches(scope.ProviderIDs, target.ProviderID, false) {
		return false
	}
	if !gatewayScopeListMatches(scope.ResourceIDs, target.ResourceID, false) {
		return false
	}
	if !gatewayScopeListMatches(scope.ResourceTypes, target.ResourceType, true) {
		return false
	}
	if !gatewayScopeListMatches(scope.RouteProtocols, target.RouteProtocol, true) {
		return false
	}
	return gatewayScopeListMatches(scope.Operations, target.Operation, true)
}

var canonicalGatewayStages = []GatewayHookStage{
	StageAuthContext,
	StageDecodeNormalize,
	StageAdmission,
	StagePrivacyPre,
	StageGuardrailPre,
	StageContextOptimize,
	StageCacheLookup,
	StageRouteCandidates,
	StageRouteRank,
	StageRequestTransform,
	StageProviderCall,
	StageStreamTransform,
	StageResponsePost,
	StageGuardrailPost,
	StageUsageAttribution,
	StageCacheWrite,
	StageSettlement,
	StageTraceExport,
}

func GatewayStagePolicy(stage GatewayHookStage) (GatewayHookStagePolicy, bool) {
	switch stage {
	case StageAuthContext:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailOpen,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailOpen, FailurePolicyFailClosed},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataRequestHeaders},
			Writes:               []GatewayDataClass{DataAuthContext, DataAudit},
		}, true
	case StageDecodeNormalize:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailClosed,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailClosed},
			Reads:                []GatewayDataClass{DataRequestHeaders, DataRequestBody},
			Writes:               []GatewayDataClass{DataRequestBody, DataNormalizedText, DataFileMetadata, DataImageMetadata, DataToolSchema, DataAudit},
			AllowsDeny:           true,
		}, true
	case StageAdmission:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailClosed,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailClosed, FailurePolicyFailOpen},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataRequestHeaders, DataRequestBody, DataNormalizedText, DataUsage},
			Writes:               []GatewayDataClass{DataUsage, DataAudit},
			AllowsDeny:           true,
		}, true
	case StagePrivacyPre:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailClosed,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailClosed, FailurePolicyFailOpen},
			Reads:                []GatewayDataClass{DataRequestHeaders, DataRequestBody, DataNormalizedText, DataFileMetadata, DataFileContent, DataImageMetadata, DataImageContent, DataToolSchema},
			Writes:               []GatewayDataClass{DataRequestBody, DataNormalizedText, DataFileContent, DataImageContent, DataCacheKey, DataAudit},
			AllowsDeny:           true,
		}, true
	case StageGuardrailPre:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailClosed,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailClosed, FailurePolicyFailOpen},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataRequestBody, DataNormalizedText, DataFileMetadata, DataFileContent, DataImageMetadata, DataImageContent, DataToolSchema},
			Writes:               []GatewayDataClass{DataRequestBody, DataNormalizedText, DataAudit},
			AllowsDeny:           true,
		}, true
	case StageContextOptimize:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailOpen,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailOpen, FailurePolicyFailClosed},
			Reads:                []GatewayDataClass{DataProjectMetadata, DataRequestBody, DataNormalizedText, DataFileMetadata, DataFileContent, DataImageMetadata, DataImageContent, DataToolSchema, DataRouteCandidates},
			Writes:               []GatewayDataClass{DataRequestBody, DataNormalizedText, DataFileContent, DataImageContent, DataToolSchema, DataAudit},
			AllowsDeny:           true,
		}, true
	case StageCacheLookup:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailOpen,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailOpen, FailurePolicyReturnFallback},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataRequestBody, DataNormalizedText, DataCacheKey},
			Writes:               []GatewayDataClass{DataCacheKey, DataCacheValue, DataProviderResponse, DataUsage, DataAudit},
			AllowsShortCircuit:   true,
		}, true
	case StageRouteCandidates:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailOpen,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailOpen, FailurePolicyFailClosed},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataRequestBody, DataNormalizedText, DataRouteCandidates, DataUsage},
			Writes:               []GatewayDataClass{DataRouteCandidates, DataAudit},
			AllowsDeny:           true,
		}, true
	case StageRouteRank:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailOpen,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailOpen, FailurePolicyObserveOnly},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataNormalizedText, DataRouteCandidates, DataUsage},
			Writes:               []GatewayDataClass{DataRouteCandidates, DataAudit},
		}, true
	case StageRequestTransform:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicySkipRoute,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicySkipRoute, FailurePolicyFailClosed, FailurePolicyFailOpen},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataProviderCredentials, DataProviderRequest, DataRouteCandidates},
			Writes:               []GatewayDataClass{DataProviderRequest, DataAudit},
			AllowsDeny:           true,
		}, true
	case StageProviderCall:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicySkipRoute,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicySkipRoute, FailurePolicyFailClosed},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataProviderCredentials, DataProviderRequest, DataRouteCandidates},
			Writes:               []GatewayDataClass{DataProviderResponse, DataStreamEvents, DataUsage, DataAudit},
			AllowsDeny:           true,
			AllowsShortCircuit:   true,
		}, true
	case StageStreamTransform:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailOpen,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailOpen, FailurePolicyFailClosed},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataProviderCredentials, DataProviderResponse, DataStreamEvents, DataUsage},
			Writes:               []GatewayDataClass{DataProviderResponse, DataStreamEvents, DataUsage, DataAudit},
			AllowsDeny:           true,
		}, true
	case StageResponsePost:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailOpen,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailOpen, FailurePolicyFailClosed},
			Reads:                []GatewayDataClass{DataProviderResponse, DataStreamEvents},
			Writes:               []GatewayDataClass{DataProviderResponse, DataStreamEvents, DataAudit},
			AllowsDeny:           true,
		}, true
	case StageGuardrailPost:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailClosed,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailClosed, FailurePolicyFailOpen},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataProviderResponse, DataStreamEvents, DataUsage},
			Writes:               []GatewayDataClass{DataProviderResponse, DataAudit},
			AllowsDeny:           true,
		}, true
	case StageCacheWrite:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailOpen,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailOpen, FailurePolicyObserveOnly},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataRequestBody, DataNormalizedText, DataProviderResponse, DataUsage, DataCacheKey, DataCacheValue},
			Writes:               []GatewayDataClass{DataCacheKey, DataCacheValue, DataAudit},
		}, true
	case StageUsageAttribution:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyFailOpen,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyFailOpen, FailurePolicyFailClosed},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataProviderResponse, DataUsage},
			Writes:               []GatewayDataClass{DataUsage, DataAudit},
		}, true
	case StageSettlement:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyObserveOnly,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyObserveOnly, FailurePolicyFailOpen},
			Reads:                []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataProviderResponse, DataUsage, DataAudit},
		}, true
	case StageTraceExport:
		return GatewayHookStagePolicy{
			DefaultFailurePolicy: FailurePolicyObserveOnly,
			AllowedFailurePolicy: []GatewayHookFailurePolicy{FailurePolicyObserveOnly, FailurePolicyFailOpen},
			Reads:                []GatewayDataClass{DataAudit, DataUsage},
		}, true
	default:
		return GatewayHookStagePolicy{}, false
	}
}

func sortGatewayHooks(hooks []GatewayHookDescriptor) {
	sort.Slice(hooks, func(i, j int) bool {
		if hooks[i].Priority != hooks[j].Priority {
			return hooks[i].Priority < hooks[j].Priority
		}
		if hooks[i].PluginID != hooks[j].PluginID {
			return hooks[i].PluginID < hooks[j].PluginID
		}
		if hooks[i].HookID != hooks[j].HookID {
			return hooks[i].HookID < hooks[j].HookID
		}
		return gatewayHookScopeSortKey(hooks[i]) < gatewayHookScopeSortKey(hooks[j])
	})
}

func normalizeDataClasses(items []GatewayDataClass) []GatewayDataClass {
	seen := map[GatewayDataClass]struct{}{}
	normalized := make([]GatewayDataClass, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized
}

func validGatewayDataClass(dataClass GatewayDataClass) bool {
	switch dataClass {
	case DataAuthContext,
		DataProjectMetadata,
		DataAPIKeyMetadata,
		DataRequestHeaders,
		DataRequestBody,
		DataNormalizedText,
		DataFileMetadata,
		DataFileContent,
		DataImageMetadata,
		DataImageContent,
		DataToolSchema,
		DataRouteCandidates,
		DataProviderCredentials,
		DataProviderRequest,
		DataProviderResponse,
		DataStreamEvents,
		DataUsage,
		DataAudit,
		DataCacheKey,
		DataCacheValue:
		return true
	default:
		return false
	}
}

func gatewayFailurePolicyAllowed(policy GatewayHookFailurePolicy, allowed []GatewayHookFailurePolicy) bool {
	for _, candidate := range allowed {
		if candidate == policy {
			return true
		}
	}
	return false
}

func validateGatewayStageDataClasses(stage GatewayHookStage, direction string, requested []GatewayDataClass, allowed []GatewayDataClass) error {
	allowedSet := map[GatewayDataClass]struct{}{}
	for _, dataClass := range allowed {
		allowedSet[dataClass] = struct{}{}
	}
	for _, dataClass := range requested {
		if _, ok := allowedSet[dataClass]; !ok {
			return fmt.Errorf("gateway hook stage %q cannot %s data class %q", stage, direction, dataClass)
		}
	}
	return nil
}

func preservedGatewayDataClasses(reads []GatewayDataClass, writes []GatewayDataClass) []GatewayDataClass {
	writable := map[GatewayDataClass]struct{}{}
	for _, dataClass := range writes {
		writable[dataClass] = struct{}{}
	}
	preserved := make([]GatewayDataClass, 0, len(reads))
	for _, dataClass := range reads {
		if _, ok := writable[dataClass]; ok {
			continue
		}
		preserved = append(preserved, dataClass)
	}
	return preserved
}

func normalizeGatewayHookScope(scope GatewayHookScope) GatewayHookScope {
	return GatewayHookScope{
		ProjectIDs:     normalizeStrings(scope.ProjectIDs),
		APIKeyIDs:      normalizeStrings(scope.APIKeyIDs),
		ProviderTypes:  normalizeLowerStrings(scope.ProviderTypes),
		ProviderIDs:    normalizeStrings(scope.ProviderIDs),
		ResourceIDs:    normalizeStrings(scope.ResourceIDs),
		ResourceTypes:  normalizeLowerStrings(scope.ResourceTypes),
		RouteProtocols: normalizeLowerStrings(scope.RouteProtocols),
		Operations:     normalizeLowerStrings(scope.Operations),
	}
}

func mergeLegacyGatewayHookScope(scope GatewayHookScope, subject string, metadata map[string]string) GatewayHookScope {
	if strings.TrimSpace(subject) != "" && len(scope.ProviderTypes) == 0 {
		scope.ProviderTypes = []string{subject}
	}
	if len(metadata) == 0 {
		return scope
	}
	if len(scope.RouteProtocols) == 0 {
		scope.RouteProtocols = append(scope.RouteProtocols, splitGatewayScopeMetadata(metadata["protocol"])...)
		scope.RouteProtocols = append(scope.RouteProtocols, splitGatewayScopeMetadata(metadata["route_protocol"])...)
	}
	if len(scope.ProjectIDs) == 0 {
		scope.ProjectIDs = append(scope.ProjectIDs, splitGatewayScopeMetadata(metadata["project_id"])...)
		scope.ProjectIDs = append(scope.ProjectIDs, splitGatewayScopeMetadata(metadata["project_ids"])...)
	}
	if len(scope.APIKeyIDs) == 0 {
		scope.APIKeyIDs = append(scope.APIKeyIDs, splitGatewayScopeMetadata(metadata["api_key_id"])...)
		scope.APIKeyIDs = append(scope.APIKeyIDs, splitGatewayScopeMetadata(metadata["api_key_ids"])...)
	}
	if len(scope.ProviderTypes) == 0 {
		scope.ProviderTypes = append(scope.ProviderTypes, splitGatewayScopeMetadata(metadata["provider_type"])...)
		scope.ProviderTypes = append(scope.ProviderTypes, splitGatewayScopeMetadata(metadata["provider_types"])...)
	}
	if len(scope.ProviderIDs) == 0 {
		scope.ProviderIDs = append(scope.ProviderIDs, splitGatewayScopeMetadata(metadata["provider_id"])...)
		scope.ProviderIDs = append(scope.ProviderIDs, splitGatewayScopeMetadata(metadata["provider_ids"])...)
	}
	if len(scope.ResourceIDs) == 0 {
		scope.ResourceIDs = append(scope.ResourceIDs, splitGatewayScopeMetadata(metadata["resource_id"])...)
		scope.ResourceIDs = append(scope.ResourceIDs, splitGatewayScopeMetadata(metadata["resource_ids"])...)
	}
	if len(scope.ResourceTypes) == 0 {
		scope.ResourceTypes = append(scope.ResourceTypes, splitGatewayScopeMetadata(metadata["resource_type"])...)
		scope.ResourceTypes = append(scope.ResourceTypes, splitGatewayScopeMetadata(metadata["resource_types"])...)
	}
	if len(scope.Operations) == 0 {
		scope.Operations = append(scope.Operations, splitGatewayScopeMetadata(metadata["operation"])...)
		scope.Operations = append(scope.Operations, splitGatewayScopeMetadata(metadata["operations"])...)
	}
	return scope
}

func gatewayScopeListMatches(allowed []string, value string, caseInsensitive bool) bool {
	if len(allowed) == 0 {
		return true
	}
	value = strings.TrimSpace(value)
	if caseInsensitive {
		value = strings.ToLower(value)
	}
	if value == "" {
		return true
	}
	for _, candidate := range allowed {
		if candidate == value {
			return true
		}
	}
	return false
}

func gatewayHookScopeSortKey(hook GatewayHookDescriptor) string {
	scope := normalizeGatewayHookScope(hook.Scope)
	parts := []string{
		strings.Join(scope.ProjectIDs, ","),
		strings.Join(scope.APIKeyIDs, ","),
		strings.Join(scope.ProviderTypes, ","),
		strings.Join(scope.ProviderIDs, ","),
		strings.Join(scope.ResourceIDs, ","),
		strings.Join(scope.ResourceTypes, ","),
		strings.Join(scope.RouteProtocols, ","),
		strings.Join(scope.Operations, ","),
	}
	return strings.Join(parts, "|")
}

func normalizeLowerStrings(items []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Strings(normalized)
	return normalized
}

func splitGatewayScopeMetadata(value string) []string {
	var items []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';'
	}) {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}
