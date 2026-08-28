package plugin

import "testing"

func TestOrderedGatewayStagesDefinesCanonicalRequestLifecycle(t *testing.T) {
	got := OrderedGatewayStages()
	want := []GatewayHookStage{
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
	if !gatewayStageSlicesEqual(got, want) {
		t.Fatalf("ordered gateway stages = %v, want %v", got, want)
	}

	got[0] = StageTraceExport
	if next := OrderedGatewayStages(); !gatewayStageSlicesEqual(next, want) {
		t.Fatalf("ordered gateway stages were mutated through returned slice: %v", next)
	}
}

func TestGatewayStagePolicySupportsEveryCanonicalStage(t *testing.T) {
	for _, stage := range OrderedGatewayStages() {
		if _, ok := GatewayStagePolicy(stage); !ok {
			t.Fatalf("stage %q has no policy", stage)
		}
	}
	settlement, ok := GatewayStagePolicy(StageSettlement)
	if !ok {
		t.Fatal("settlement stage has no policy")
	}
	if settlement.DefaultFailurePolicy != FailurePolicyObserveOnly || settlement.AllowsDeny || settlement.AllowsShortCircuit || len(settlement.Writes) != 0 {
		t.Fatalf("settlement policy = %+v, want observe-only non-mutating policy", settlement)
	}
}

func TestGatewayStageEnvelopeContractsExposeMutationLimits(t *testing.T) {
	contracts := GatewayStageEnvelopeContracts()
	if len(contracts) != len(OrderedGatewayStages()) {
		t.Fatalf("contracts = %d, want %d", len(contracts), len(OrderedGatewayStages()))
	}
	byStage := map[GatewayHookStage]GatewayStageEnvelopeContract{}
	for index, contract := range contracts {
		if contract.Stage != OrderedGatewayStages()[index] {
			t.Fatalf("contract %d stage = %q, want %q", index, contract.Stage, OrderedGatewayStages()[index])
		}
		byStage[contract.Stage] = contract
	}
	privacy := byStage[StagePrivacyPre]
	if !gatewayDataClassIn(privacy.Reads, DataRequestBody) || !gatewayDataClassIn(privacy.Writes, DataRequestBody) {
		t.Fatalf("privacy_pre contract = %+v, want request body read/write", privacy)
	}
	routeRank := byStage[StageRouteRank]
	if !gatewayDataClassIn(routeRank.Reads, DataRouteCandidates) || !gatewayDataClassIn(routeRank.Writes, DataRouteCandidates) {
		t.Fatalf("route_rank contract = %+v, want route candidate read/write", routeRank)
	}
	settlement := byStage[StageSettlement]
	if len(settlement.Writes) != 0 || !gatewayDataClassIn(settlement.Preserves, DataUsage) || !gatewayDataClassIn(settlement.Preserves, DataAudit) {
		t.Fatalf("settlement contract = %+v, want non-mutating preserved usage/audit", settlement)
	}
	contracts[0].Reads[0] = DataProviderCredentials
	next, _ := GatewayStageEnvelopeContractFor(StageAuthContext)
	if gatewayDataClassIn(next.Reads, DataProviderCredentials) {
		t.Fatalf("stage envelope contract leaked mutable slices: %+v", next)
	}
}

func TestGatewayChainRegistryPlansHooksByStageAndPriority(t *testing.T) {
	registry := NewGatewayChainRegistry()

	for _, hook := range []GatewayHookDescriptor{
		{PluginID: "tokenhub.core", HookID: "settlement", Stage: StageSettlement, Priority: 100},
		{PluginID: "tokenhub.core", HookID: "usage", Stage: StageUsageAttribution, Priority: 100},
		{PluginID: "tokenhub.core", HookID: "cache-write", Stage: StageCacheWrite, Priority: 100},
		{PluginID: "tokenhub.third", HookID: "rank", Stage: StageRouteRank, Priority: 2500},
		{PluginID: "tokenhub.core", HookID: "admission", Stage: StageAdmission, Priority: 100},
		{PluginID: "tokenhub.official", HookID: "privacy", Stage: StagePrivacyPre, Priority: 1200},
		{PluginID: "tokenhub.core", HookID: "trace", Stage: StageTraceExport, Priority: 100},
	} {
		if err := registry.RegisterHook(hook); err != nil {
			t.Fatalf("register hook %q: %v", hook.HookID, err)
		}
	}

	plan := registry.Plan()
	got := make([]string, 0, len(plan.Hooks))
	for _, hook := range plan.Hooks {
		got = append(got, hook.HookID)
	}
	want := []string{"admission", "privacy", "rank", "usage", "cache-write", "settlement", "trace"}
	if len(got) != len(want) {
		t.Fatalf("planned hooks = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("planned hooks = %v, want %v", got, want)
		}
	}
	if !gatewayStageSlicesEqual(plan.Stages, OrderedGatewayStages()) {
		t.Fatalf("planned stages = %v, want canonical stages", plan.Stages)
	}
}

func TestGatewayChainRegistryNormalizesHookPermissions(t *testing.T) {
	registry := NewGatewayChainRegistry()

	err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID: "tokenhub.privacy",
		HookID:   "mask",
		Stage:    StagePrivacyPre,
		Reads:    []GatewayDataClass{DataRequestBody, DataRequestBody, ""},
		Writes:   []GatewayDataClass{DataAudit, DataRequestBody, DataAudit},
	})
	if err != nil {
		t.Fatalf("register hook: %v", err)
	}

	hooks := registry.Hooks(StagePrivacyPre)
	if len(hooks) != 1 {
		t.Fatalf("hooks = %d, want 1", len(hooks))
	}
	if hooks[0].FailurePolicy != FailurePolicyFailClosed {
		t.Fatalf("failure policy = %q, want %q", hooks[0].FailurePolicy, FailurePolicyFailClosed)
	}
	if got, want := hooks[0].Reads, []GatewayDataClass{DataRequestBody}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("reads = %v, want %v", got, want)
	}
	if got, want := hooks[0].Writes, []GatewayDataClass{DataAudit, DataRequestBody}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("writes = %v, want %v", got, want)
	}
}

func gatewayStageSlicesEqual(left []GatewayHookStage, right []GatewayHookStage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func gatewayDataClassIn(items []GatewayDataClass, want GatewayDataClass) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestGatewayChainRegistryRejectsStageDataClassViolations(t *testing.T) {
	registry := NewGatewayChainRegistry()

	err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID: "tokenhub.router",
		HookID:   "rank",
		Stage:    StageRouteRank,
		Reads:    []GatewayDataClass{DataProviderCredentials},
	})
	if err == nil {
		t.Fatal("route_rank hook was allowed to read provider credentials")
	}
}

func TestGatewayChainRegistryRejectsStageFailurePolicyViolations(t *testing.T) {
	registry := NewGatewayChainRegistry()

	if err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID:      "tokenhub.exporter",
		HookID:        "trace",
		Stage:         StageTraceExport,
		FailurePolicy: FailurePolicyFailClosed,
	}); err == nil {
		t.Fatal("trace_export hook was allowed to fail closed")
	}
	if err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID:      "tokenhub.router",
		HookID:        "rank",
		Stage:         StageRouteRank,
		FailurePolicy: FailurePolicyFailClosed,
	}); err == nil {
		t.Fatal("route_rank hook was allowed to fail closed")
	}
}
