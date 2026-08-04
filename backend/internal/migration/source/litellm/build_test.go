package litellm

import (
	"os"
	"path/filepath"
	"testing"

	"tokenhub/backend/internal/server"
)

func TestSplitProviderModelPreservesNestedModelPath(t *testing.T) {
	providerType, providerName := splitProviderModel("openai/BohrClaw/qwen3.5-flash")
	if providerType != "openai" {
		t.Fatalf("unexpected provider type %q", providerType)
	}
	if providerName != "BohrClaw/qwen3.5-flash" {
		t.Fatalf("unexpected provider name %q", providerName)
	}
}

// TestRouteStrategyIsAcceptedByServer pins the strategy the adapter emits.
// A real migration failed with invalid_route_strategy because the adapter
// used "weighted", which is not one of the strategies TokenHub accepts.
func TestRouteStrategyIsAcceptedByServer(t *testing.T) {
	accepted := map[string]bool{
		server.RouteStrategyBalanced:         true,
		server.RouteStrategyAdaptive:         true,
		server.RouteStrategyCost:             true,
		server.RouteStrategyQuality:          true,
		server.RouteStrategyPriorityWeighted: true,
		server.RouteStrategyPriorityOnly:     true,
	}
	data, err := os.ReadFile(filepath.Join("testdata", "config-basic.yaml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	config, err := ParseConfig(data)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	b, err := BuildBundle(config, BuildOptions{})
	if err != nil {
		t.Fatalf("build bundle: %v", err)
	}
	if len(b.Routes) == 0 {
		t.Fatal("expected the fixture to produce routes")
	}
	for _, route := range b.Routes {
		if strategy := route.Spec.Strategy; strategy != "" && !accepted[strategy] {
			t.Fatalf("route %s uses strategy %q, which the server rejects", route.ExternalRef.ID, strategy)
		}
	}
}
