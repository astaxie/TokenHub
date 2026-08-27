package plugin

import (
	"fmt"
	"sort"
	"strings"
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
	StageCacheWrite       GatewayHookStage = "cache_write"
	StageUsageAttribution GatewayHookStage = "usage_attribution"
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
	Reads         []GatewayDataClass       `json:"reads,omitempty"`
	Writes        []GatewayDataClass       `json:"writes,omitempty"`
	FailurePolicy GatewayHookFailurePolicy `json:"failure_policy"`
	TimeoutMillis int                      `json:"timeout_millis"`
	Mandatory     bool                     `json:"mandatory"`
}

type GatewayChainPlan struct {
	Hooks []GatewayHookDescriptor `json:"hooks"`
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

func NewGatewayChainRegistry() *GatewayChainRegistry {
	return &GatewayChainRegistry{hooks: map[GatewayHookStage][]GatewayHookDescriptor{}}
}

func (r *GatewayChainRegistry) RegisterHook(descriptor GatewayHookDescriptor) error {
	if r == nil {
		return fmt.Errorf("gateway chain registry is not configured")
	}
	descriptor.PluginID = strings.TrimSpace(descriptor.PluginID)
	descriptor.HookID = strings.TrimSpace(descriptor.HookID)
	descriptor.Subject = strings.TrimSpace(descriptor.Subject)
	descriptor.Metadata = normalizeStringMap(descriptor.Metadata)
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
	if descriptor.FailurePolicy == "" {
		descriptor.FailurePolicy = policy.DefaultFailurePolicy
	}
	if !gatewayFailurePolicyAllowed(descriptor.FailurePolicy, policy.AllowedFailurePolicy) {
		return fmt.Errorf("gateway hook stage %q does not allow failure policy %q", descriptor.Stage, descriptor.FailurePolicy)
	}
	descriptor.Reads = normalizeDataClasses(descriptor.Reads)
	descriptor.Writes = normalizeDataClasses(descriptor.Writes)
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
		return GatewayChainPlan{}
	}
	var hooks []GatewayHookDescriptor
	for _, stage := range orderedGatewayStages() {
		hooks = append(hooks, r.Hooks(stage)...)
	}
	return GatewayChainPlan{Hooks: hooks}
}

func orderedGatewayStages() []GatewayHookStage {
	return []GatewayHookStage{
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
		StageCacheWrite,
		StageUsageAttribution,
		StageTraceExport,
	}
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
		return hooks[i].HookID < hooks[j].HookID
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
