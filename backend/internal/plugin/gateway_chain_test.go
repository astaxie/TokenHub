package plugin

import "testing"

func TestGatewayChainRegistryPlansHooksByStageAndPriority(t *testing.T) {
	registry := NewGatewayChainRegistry()

	for _, hook := range []GatewayHookDescriptor{
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
	want := []string{"admission", "privacy", "rank", "trace"}
	if len(got) != len(want) {
		t.Fatalf("planned hooks = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("planned hooks = %v, want %v", got, want)
		}
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

	err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID:      "tokenhub.exporter",
		HookID:        "trace",
		Stage:         StageTraceExport,
		FailurePolicy: FailurePolicyFailClosed,
	})
	if err == nil {
		t.Fatal("trace_export hook was allowed to fail closed")
	}
}
