package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestListStagesRejectSelectedRouteScopes(t *testing.T) {
	for _, stage := range []GatewayHookStage{StageRouteCandidates, StageRouteRank} {
		for _, scope := range []GatewayHookScope{{ProviderIDs: []string{"provider-b"}}, {ProviderTypes: []string{"mock"}}, {ResourceIDs: []string{"resource"}}, {ResourceTypes: []string{"subscription"}}} {
			if err := NewGatewayChainRegistry().RegisterHook(GatewayHookDescriptor{PluginID: "test", HookID: "list", Stage: stage, Scope: scope}); err == nil {
				t.Fatalf("stage %s accepted selected-route scope %+v", stage, scope)
			}
		}
	}
}

func TestListStageScopeChecksAllRouteCandidates(t *testing.T) {
	hook := GatewayHookDescriptor{PluginID: "test", HookID: "rank", Stage: StageRouteRank, Scope: GatewayHookScope{ProviderIDs: []string{"provider-b"}}}
	runner := NewGatewayHookRunner(NewGatewayChainRegistry())
	calls := 0
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		calls++
		return GatewayHookResult{Decision: HookDecisionContinue}, nil
	})); err != nil {
		t.Fatal(err)
	}
	_, err := runner.RunStageHooks(t.Context(), StageRouteRank, GatewayHookInput{
		Envelope: GatewayEnvelope{Protocol: "gateway", Operation: "route_rank"},
		Data:     GatewayHookData{DataRouteCandidates: json.RawMessage(`[{"provider_id":"provider-a"},{"provider_id":"provider-b"}]`)},
	}, []GatewayHookDescriptor{hook})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("scope matched %d times, want 1", calls)
	}
}

func TestConstrainedScopeRequiresKnownDimension(t *testing.T) {
	hook := GatewayHookDescriptor{Scope: GatewayHookScope{RouteProtocols: []string{"images/generations"}}}
	if GatewayHookScopeMatches(hook, GatewayHookScopeTarget{}) {
		t.Fatal("missing endpoint protocol matched a constrained hook")
	}
	chain := NewGatewayChainRegistry()
	hook.PluginID, hook.HookID, hook.Stage = "test", "images", StageRouteRank
	if err := chain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	runner := NewGatewayHookRunner(chain)
	calls := 0
	if err := runner.RegisterHandler(hook, GatewayHookHandlerFunc(func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		calls++
		return GatewayHookResult{Decision: HookDecisionContinue}, nil
	})); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"", "chat/completions", "images/generations"} {
		_, err := runner.RunStage(t.Context(), StageRouteRank, GatewayHookInput{Envelope: GatewayEnvelope{Protocol: "gateway", RouteProtocol: protocol}, Data: GatewayHookData{DataRouteCandidates: json.RawMessage(`[{"provider_id":"a"},{"provider_id":"b"}]`)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("image-only hook ran %d times", calls)
	}
}

func TestStreamTransformRejectsUnsupportedResponseAndUsageData(t *testing.T) {
	for _, data := range []GatewayDataClass{DataProviderResponse, DataUsage} {
		for _, write := range []bool{false, true} {
			hook := GatewayHookDescriptor{PluginID: "test", HookID: "stream", Stage: StageStreamTransform}
			if write {
				hook.Writes = []GatewayDataClass{data}
			} else {
				hook.Reads = []GatewayDataClass{data}
			}
			if err := NewGatewayChainRegistry().RegisterHook(hook); err == nil {
				t.Fatalf("stream transform accepted unsupported class %s write=%v", data, write)
			}
		}
	}
}

func TestBackgroundSchedulesRejectUnsupportedAndOverflowValues(t *testing.T) {
	for _, schedule := range []string{"0m", "-1s", "daily", "0 0 * * *", "*/0 * * * *", "*/99999999999999999999999999 * * * *", "*/99999999999999 * * * *"} {
		if err := validateBackgroundJobSchedule(schedule); err == nil {
			t.Fatalf("accepted unsupported schedule %q", schedule)
		}
	}
	for _, schedule := range []string{"@startup", "1ms", "2h", "*/5 * * * *"} {
		if err := validateBackgroundJobSchedule(schedule); err != nil {
			t.Fatalf("rejected supported schedule %q: %v", schedule, err)
		}
	}
}

func TestMarketplaceKeepsCompatibleReleaseAlongsideArchivedAndFuture(t *testing.T) {
	index, err := DecodeMarketplaceIndex(readMarketplaceFixture(t, "official-valid"))
	if err != nil {
		t.Fatal(err)
	}
	index.Plugins[0].Releases[0].Artifacts[0].Target = "any"
	release := index.Plugins[0].Releases[0]
	for _, tc := range []struct{ version, min, max string }{{"0.9.0", "0.1.0", "0.6.0"}, {"9.0.0", "9.0.0", ""}} {
		other := release
		other.Version, other.Compatibility.MinCore, other.Compatibility.MaxCore = tc.version, tc.min, tc.max
		// Keep immutable artifact paths consistent with each release.
		raw, _ := json.Marshal(other)
		raw = []byte(strings.ReplaceAll(string(raw), release.Version, other.Version))
		other.Artifacts = nil
		if err := json.Unmarshal(raw, &other); err != nil {
			t.Fatal(err)
		}
		index.Plugins[0].Releases = append(index.Plugins[0].Releases, other)
	}
	index.Plugins[0].Latest = "9.0.0"
	descriptors, err := MarketplaceDescriptorsFromChannelIndex(index)
	if err != nil || len(descriptors) != 1 || descriptors[0].Version != release.Version || descriptors[0].Marketplace.Compatibility.Verdict != MarketplaceCompatibilityCompatible {
		t.Fatalf("compatible release was lost: %v %v", descriptors, err)
	}
	index.Plugins[0].Latest = release.Version
	for i := range index.Plugins[0].Releases {
		if index.Plugins[0].Releases[i].Version == release.Version {
			index.Plugins[0].Releases[i].Compatibility.RequiredFeatures = []string{"future_feature"}
		}
	}
	descriptors, err = MarketplaceDescriptorsFromChannelIndex(index)
	if err != nil || len(descriptors) != 1 || descriptors[0].Version != release.Version || descriptors[0].Marketplace.Compatibility.Verdict != MarketplaceCompatibilityIncompatible {
		t.Fatalf("unknown feature compatibility: %v %v", descriptors, err)
	}
}

func TestMarketplaceUpdatesUseSemanticPrecedence(t *testing.T) {
	for _, tc := range []struct {
		available, installed string
		want                 bool
	}{{"1.9.0", "2.0.0", false}, {"1.10.0", "1.9.0", true}, {"2.0.0-rc.1", "2.0.0", false}, {"2.0.0", "2.0.0-rc.1", true}, {"2.0.0+build.2", "2.0.0+build.1", false}, {"2.0.0", "custom", false}} {
		if got := MarketplaceVersionGreater(tc.available, tc.installed); got != tc.want {
			t.Fatalf("%s > %s = %v", tc.available, tc.installed, got)
		}
	}
}
