package server

import pluginmeta "tokenhub/backend/internal/plugin"

func registerBuiltinGatewayChainPlugins(registry *pluginmeta.Registry, chain *pluginmeta.GatewayChainRegistry, runners ...*pluginmeta.GatewayHookRunner) {
	mustRegisterPlugin(registry, pluginmeta.Descriptor{
		ID:         "tokenhub.chain.core",
		Name:       "TokenHub Core Gateway Chain",
		Version:    "built-in",
		Source:     pluginmeta.SourceBuiltIn,
		Kinds:      []pluginmeta.Kind{pluginmeta.KindExtension},
		Placements: []pluginmeta.Placement{pluginmeta.PlacementGatewayChain},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "gateway_chain", Name: string(pluginmeta.StageDecodeNormalize)},
			{Kind: "gateway_chain", Name: string(pluginmeta.StageAdmission)},
			{Kind: "gateway_chain", Name: string(pluginmeta.StagePrivacyPre)},
			{Kind: "gateway_chain", Name: string(pluginmeta.StageGuardrailPre)},
			{Kind: "gateway_chain", Name: string(pluginmeta.StageRouteCandidates)},
			{Kind: "gateway_chain", Name: string(pluginmeta.StageRouteRank)},
			{Kind: "gateway_chain", Name: string(pluginmeta.StageUsageAttribution)},
			{Kind: "gateway_chain", Name: string(pluginmeta.StageTraceExport)},
		},
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
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProjectMetadata, pluginmeta.DataNormalizedText, pluginmeta.DataRequestBody},
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
			HookID:        "route_candidates",
			Stage:         pluginmeta.StageRouteCandidates,
			Priority:      100,
			Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext, pluginmeta.DataProjectMetadata, pluginmeta.DataRequestBody},
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
