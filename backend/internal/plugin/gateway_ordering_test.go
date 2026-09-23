package plugin

import (
	"strings"
	"testing"
)

func TestGatewayChainOrdersPluginAPIV2HooksWithBeforeAfter(t *testing.T) {
	registry := NewGatewayChainRegistry()
	first := GatewayHookDescriptor{
		PluginID: "example.redact", HookID: "redact", PluginAPI: PluginAPIV2,
		Stage: StagePrivacyPre, Before: []string{"example.audit/audit"},
		Writes: []GatewayDataClass{DataRequestBody}, FailurePolicy: FailurePolicyFailClosed,
	}
	second := GatewayHookDescriptor{
		PluginID: "example.audit", HookID: "audit", PluginAPI: PluginAPIV2,
		Stage: StagePrivacyPre, Writes: []GatewayDataClass{DataRequestBody},
		FailurePolicy: FailurePolicyFailClosed,
	}
	if err := registry.RegisterHook(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterHook(second); err != nil {
		t.Fatal(err)
	}
	hooks := registry.Hooks(StagePrivacyPre)
	if len(hooks) != 2 || hooks[0].HookID != "redact" || hooks[1].HookID != "audit" {
		t.Fatalf("ordered hooks = %+v", hooks)
	}
}

func TestGatewayChainRejectsPluginAPIV2OrderingCycle(t *testing.T) {
	registry := NewGatewayChainRegistry()
	if err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID: "example.first", HookID: "first", PluginAPI: PluginAPIV2,
		Stage: StageAdmission, After: []string{"example.second/second"}, FailurePolicy: FailurePolicyFailClosed,
	}); err != nil {
		t.Fatal(err)
	}
	err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID: "example.second", HookID: "second", PluginAPI: PluginAPIV2,
		Stage: StageAdmission, After: []string{"example.first/first"}, FailurePolicy: FailurePolicyFailClosed,
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v, want cycle rejection", err)
	}
}

func TestGatewayChainRejectsPluginAPIV2ExclusiveStageConflict(t *testing.T) {
	registry := NewGatewayChainRegistry()
	for index, pluginID := range []string{"example.rank-one", "example.rank-two"} {
		err := registry.RegisterHook(GatewayHookDescriptor{
			PluginID: pluginID, HookID: "rank", PluginAPI: PluginAPIV2,
			Stage: StageRouteRank, FailurePolicy: FailurePolicyFailOpen,
		})
		if index == 0 && err != nil {
			t.Fatal(err)
		}
		if index == 1 && (err == nil || !strings.Contains(err.Error(), "exclusive")) {
			t.Fatalf("error = %v, want exclusive conflict", err)
		}
	}
}

func TestGatewayChainRejectsUnorderedPluginAPIV2WriteCollision(t *testing.T) {
	registry := NewGatewayChainRegistry()
	if err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID: "example.first", HookID: "first", PluginAPI: PluginAPIV2,
		Stage: StagePrivacyPre, Writes: []GatewayDataClass{DataRequestBody}, FailurePolicy: FailurePolicyFailClosed,
	}); err != nil {
		t.Fatal(err)
	}
	err := registry.RegisterHook(GatewayHookDescriptor{
		PluginID: "example.second", HookID: "second", PluginAPI: PluginAPIV2,
		Stage: StagePrivacyPre, Writes: []GatewayDataClass{DataRequestBody}, FailurePolicy: FailurePolicyFailClosed,
	})
	if err == nil || !strings.Contains(err.Error(), "without before/after ordering") {
		t.Fatalf("error = %v, want write collision", err)
	}
}
