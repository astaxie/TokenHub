package server

import pluginmeta "tokenhub/backend/internal/plugin"

func registerBuiltinGatewayChainPlugins(registry *pluginmeta.Registry, chain *pluginmeta.GatewayChainRegistry, runners ...*pluginmeta.GatewayHookRunner) {
	mustRegisterPlugin(registry, pluginmeta.Descriptor{
		ID:           "tokenhub.chain.core",
		Name:         "TokenHub Core Gateway Chain",
		Version:      "built-in",
		Source:       pluginmeta.SourceBuiltIn,
		Kinds:        []pluginmeta.Kind{pluginmeta.KindExtension},
		Placements:   []pluginmeta.Placement{pluginmeta.PlacementGatewayChain},
		Capabilities: builtinGatewayChainCapabilities(),
	})
	var runner *pluginmeta.GatewayHookRunner
	if len(runners) > 0 {
		runner = runners[0]
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "decode_normalize",
			Stage:         pluginmeta.StageDecodeNormalize,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataRequestHeaders},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataNormalizedText},
			FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "admission",
			Stage:         pluginmeta.StageAdmission,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext, pluginmeta.DataProjectMetadata, pluginmeta.DataAPIKeyMetadata, pluginmeta.DataRequestBody},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataAudit},
			FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "guardrail_pre",
			Stage:         pluginmeta.StageGuardrailPre,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext, pluginmeta.DataProjectMetadata, pluginmeta.DataAPIKeyMetadata, pluginmeta.DataNormalizedText, pluginmeta.DataRequestBody},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataAudit},
			FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "privacy_pre",
			Stage:         pluginmeta.StagePrivacyPre,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestHeaders, pluginmeta.DataRequestBody},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataAudit},
			FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "cache_lookup",
			Stage:         pluginmeta.StageCacheLookup,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext, pluginmeta.DataProjectMetadata, pluginmeta.DataAPIKeyMetadata, pluginmeta.DataRequestBody},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey, pluginmeta.DataCacheValue, pluginmeta.DataProviderResponse, pluginmeta.DataUsage, pluginmeta.DataAudit},
			FailurePolicy: pluginmeta.FailurePolicyFailOpen,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "route_candidates",
			Stage:         pluginmeta.StageRouteCandidates,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext, pluginmeta.DataProjectMetadata, pluginmeta.DataRequestBody, pluginmeta.DataRouteCandidates},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
			FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "route_rank",
			Stage:         pluginmeta.StageRouteRank,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates, pluginmeta.DataAPIKeyMetadata},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
			FailurePolicy: pluginmeta.FailurePolicyFailOpen,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "guardrail_post",
			Stage:         pluginmeta.StageGuardrailPost,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext, pluginmeta.DataProjectMetadata, pluginmeta.DataAPIKeyMetadata, pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataAudit},
			FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "provider_call",
			Stage:         pluginmeta.StageProviderCall,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext, pluginmeta.DataProjectMetadata, pluginmeta.DataProviderCredentials, pluginmeta.DataProviderRequest, pluginmeta.DataRouteCandidates},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage, pluginmeta.DataAudit},
			FailurePolicy: pluginmeta.FailurePolicySkipRoute,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "cache_write",
			Stage:         pluginmeta.StageCacheWrite,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext, pluginmeta.DataProjectMetadata, pluginmeta.DataAPIKeyMetadata, pluginmeta.DataRequestBody, pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey, pluginmeta.DataCacheValue, pluginmeta.DataAudit},
			FailurePolicy: pluginmeta.FailurePolicyFailOpen,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "usage_attribution",
			Stage:         pluginmeta.StageUsageAttribution,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataUsage, pluginmeta.DataProviderResponse},
			Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataAudit},
			FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
		{
			PluginID:      "tokenhub.chain.core",
			HookID:        "trace_export",
			Stage:         pluginmeta.StageTraceExport,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAudit, pluginmeta.DataUsage},
			FailurePolicy: pluginmeta.FailurePolicyObserveOnly,
			TimeoutMillis: 0,
			Mandatory:     true,
		},
	} {
		mustRegisterGatewayHook(chain, hook)
		if runner != nil {
			mustRegisterGatewayHookHandler(runner, hook, pluginmeta.NoopGatewayHookHandler())
		}
	}
}

func builtinGatewayChainCapabilities() []pluginmeta.CapabilityDescriptor {
	stages := []pluginmeta.GatewayHookStage{
		pluginmeta.StageAuthContext,
		pluginmeta.StageDecodeNormalize,
		pluginmeta.StageAdmission,
		pluginmeta.StagePrivacyPre,
		pluginmeta.StageGuardrailPre,
		pluginmeta.StageContextOptimize,
		pluginmeta.StageCacheLookup,
		pluginmeta.StageRouteCandidates,
		pluginmeta.StageRouteRank,
		pluginmeta.StageRequestTransform,
		pluginmeta.StageProviderCall,
		pluginmeta.StageStreamTransform,
		pluginmeta.StageResponsePost,
		pluginmeta.StageGuardrailPost,
		pluginmeta.StageCacheWrite,
		pluginmeta.StageUsageAttribution,
		pluginmeta.StageTraceExport,
	}
	capabilities := make([]pluginmeta.CapabilityDescriptor, 0, len(stages))
	for _, stage := range stages {
		capabilities = append(capabilities, pluginmeta.CapabilityDescriptor{Kind: "gateway_chain", Name: string(stage)})
	}
	return capabilities
}

func mustRegisterPlugin(registry *pluginmeta.Registry, descriptor pluginmeta.Descriptor) {
	if err := registry.Register(descriptor); err != nil {
		panic(err)
	}
}

func mustRegisterGatewayHook(registry *pluginmeta.GatewayChainRegistry, descriptor pluginmeta.GatewayHookDescriptor) {
	if err := registry.RegisterHook(descriptor); err != nil {
		panic(err)
	}
}

func mustRegisterGatewayHookHandler(registry *pluginmeta.GatewayHookRunner, descriptor pluginmeta.GatewayHookDescriptor, handler pluginmeta.GatewayHookHandler) {
	if err := registry.RegisterHandler(descriptor, handler); err != nil {
		panic(err)
	}
}
