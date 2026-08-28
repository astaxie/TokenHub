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
	if privacy.DefaultTimeoutMS != DefaultGatewayHookTimeoutMillis || privacy.MaxTimeoutMS != MaxGatewayHookTimeoutMillis {
		t.Fatalf("privacy_pre timeout contract = %+v, want default/max timeout policy", privacy)
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

func TestGatewayChainRegistryNormalizesTimeoutPolicyAndScope(t *testing.T) {
	registry := NewGatewayChainRegistry()

	err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID: " tokenhub.router ",
		HookID:   " rank ",
		Stage:    StageRouteRank,
		Subject:  "OpenAI_Codex",
		Metadata: map[string]string{
			"protocol":   "codex/responses, openai/chat",
			"project_id": "prj_a",
		},
		Scope: GatewayHookScope{
			ProviderTypes: []string{"openai_codex"},
			ProviderIDs:   []string{" prv_a ", "prv_a"},
		},
		Writes:        []GatewayDataClass{DataRouteCandidates},
		FailurePolicy: FailurePolicyObserveOnly,
	})
	if err != nil {
		t.Fatalf("register hook: %v", err)
	}

	hook := registry.Hooks(StageRouteRank)[0]
	if hook.PluginID != "tokenhub.router" || hook.HookID != "rank" {
		t.Fatalf("hook identity was not trimmed: %+v", hook)
	}
	if hook.TimeoutMillis != DefaultGatewayHookTimeoutMillis {
		t.Fatalf("timeout = %d, want default %d", hook.TimeoutMillis, DefaultGatewayHookTimeoutMillis)
	}
	if len(hook.Writes) != 0 {
		t.Fatalf("observe-only hook writes = %v, want none", hook.Writes)
	}
	if got, want := hook.Scope.ProviderTypes, []string{"openai_codex"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("provider types = %v, want %v", got, want)
	}
	if got, want := hook.Scope.ProviderIDs, []string{"prv_a"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("provider ids = %v, want %v", got, want)
	}
	if got := hook.Scope.ProjectIDs; len(got) != 1 || got[0] != "prj_a" {
		t.Fatalf("project ids = %v, want prj_a", got)
	}
	if got := hook.Scope.RouteProtocols; len(got) != 2 || got[0] != "codex/responses" || got[1] != "openai/chat" {
		t.Fatalf("route protocols = %v, want normalized protocols", got)
	}
}

func TestGatewayChainRegistryRejectsInvalidTimeoutPolicy(t *testing.T) {
	registry := NewGatewayChainRegistry()

	if err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID:      "tokenhub.slow",
		HookID:        "negative",
		Stage:         StageContextOptimize,
		TimeoutMillis: -1,
	}); err == nil {
		t.Fatal("gateway hook accepted a negative timeout")
	}
	if err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID:      "tokenhub.slow",
		HookID:        "too-long",
		Stage:         StageContextOptimize,
		TimeoutMillis: MaxGatewayHookTimeoutMillis + 1,
	}); err == nil {
		t.Fatal("gateway hook accepted a timeout above the maximum")
	}
}

func TestGatewayChainRegistryOrdersEqualPriorityDeterministically(t *testing.T) {
	registry := NewGatewayChainRegistry()
	hooks := []GatewayHookDescriptor{
		{PluginID: "tokenhub.z", HookID: "b", Stage: StageContextOptimize, Priority: 1000},
		{PluginID: "tokenhub.a", HookID: "z", Stage: StageContextOptimize, Priority: 1000},
		{PluginID: "tokenhub.a", HookID: "a", Stage: StageContextOptimize, Priority: 1000},
	}
	for _, hook := range hooks {
		if err := registry.RegisterHook(hook); err != nil {
			t.Fatalf("register hook: %v", err)
		}
	}
	gotHooks := registry.Hooks(StageContextOptimize)
	got := []string{gotHooks[0].PluginID + "/" + gotHooks[0].HookID, gotHooks[1].PluginID + "/" + gotHooks[1].HookID, gotHooks[2].PluginID + "/" + gotHooks[2].HookID}
	want := []string{"tokenhub.a/a", "tokenhub.a/z", "tokenhub.z/b"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("hook order = %v, want %v", got, want)
		}
	}

	gotHooks[0].HookID = "mutated"
	if next := registry.Hooks(StageContextOptimize); next[0].HookID != "a" {
		t.Fatalf("hooks returned mutable registry state: %+v", next)
	}
}

func TestGatewayHookScopeMatchesDeclaredDimensions(t *testing.T) {
	hook := GatewayHookDescriptor{
		PluginID: "tokenhub.scoped",
		HookID:   "policy",
		Stage:    StageContextOptimize,
		Scope: GatewayHookScope{
			ProjectIDs:     []string{"prj_a"},
			APIKeyIDs:      []string{"key_a"},
			ProviderTypes:  []string{"openai_codex"},
			ProviderIDs:    []string{"prv_a"},
			ResourceIDs:    []string{"res_a"},
			ResourceTypes:  []string{"subscription"},
			RouteProtocols: []string{"codex/responses"},
			Operations:     []string{"context_optimize"},
		},
	}
	target := GatewayHookScopeTarget{
		ProjectID:     "prj_a",
		APIKeyID:      "key_a",
		ProviderType:  "OPENAI_CODEX",
		ProviderID:    "prv_a",
		ResourceID:    "res_a",
		ResourceType:  "SUBSCRIPTION",
		RouteProtocol: "CODEX/RESPONSES",
		Operation:     "CONTEXT_OPTIMIZE",
	}
	if !GatewayHookScopeMatches(hook, target) {
		t.Fatalf("scope should match target")
	}
	target.ProviderID = "prv_other"
	if GatewayHookScopeMatches(hook, target) {
		t.Fatalf("scope matched the wrong provider id")
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
