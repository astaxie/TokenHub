package server

import (
	"reflect"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type builtinPluginCapabilityExpectation struct {
	name    string
	subject string
	value   string
}

func TestBuiltinProviderPluginPackagesExposeProviderTypes(t *testing.T) {
	server := New(NewMemoryStore())

	for providerType, pluginID := range builtinAdapterPlugins {
		descriptor, ok := server.pluginRegistry.Describe(pluginID)
		if !ok {
			t.Fatalf("built-in provider plugin %q for %q is missing", pluginID, providerType)
		}
		if descriptor.Source != pluginmeta.SourceBuiltIn {
			t.Fatalf("plugin %q source = %q, want built_in", pluginID, descriptor.Source)
		}
		if !pluginKindExists(descriptor.Kinds, pluginmeta.KindProvider) {
			t.Fatalf("plugin %q kinds = %v, want provider", pluginID, descriptor.Kinds)
		}
		if !pluginPlacementExists(descriptor.Placements, pluginmeta.PlacementGatewayChain) {
			t.Fatalf("plugin %q placements = %v, want gateway_chain", pluginID, descriptor.Placements)
		}
		if !descriptorHasPluginCapability(descriptor, pluginmeta.CapabilityDescriptor{
			Kind: pluginmeta.CapabilityKindProviderType,
			Name: providerType,
		}) {
			t.Fatalf("plugin %q does not expose provider_type capability for %q", pluginID, providerType)
		}
	}
}

func TestBuiltinProviderPluginPackagesMirrorRegisteredActions(t *testing.T) {
	server := New(NewMemoryStore())
	wantByPlugin := map[string][]builtinPluginCapabilityExpectation{}
	for _, action := range server.pluginActions.List() {
		if _, ok := builtinProviderPluginIDs()[action.PluginID]; !ok {
			continue
		}
		wantByPlugin[action.PluginID] = append(wantByPlugin[action.PluginID], builtinPluginCapabilityExpectation{
			name:    action.ActionID,
			subject: action.Subject,
			value:   action.Capability,
		})
	}
	if len(wantByPlugin) == 0 {
		t.Fatal("built-in provider actions were not registered")
	}

	for pluginID, want := range wantByPlugin {
		descriptor, ok := server.pluginRegistry.Describe(pluginID)
		if !ok {
			t.Fatalf("plugin %q for built-in actions is missing", pluginID)
		}
		if !pluginPlacementExists(descriptor.Placements, pluginmeta.PlacementManagementAction) {
			t.Fatalf("plugin %q placements = %v, want management_action", pluginID, descriptor.Placements)
		}
		got := builtinPluginCapabilityExpectations(descriptor, pluginmeta.CapabilityKindManagementAction)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("plugin %q management action capabilities = %+v, want %+v", pluginID, got, want)
		}
	}
}

func TestBuiltinProviderPluginPackagesMirrorRegisteredBackgroundJobs(t *testing.T) {
	server := New(NewMemoryStore())
	wantByPlugin := map[string][]builtinPluginCapabilityExpectation{}
	for _, job := range server.pluginBackgroundJobs.List() {
		if _, ok := builtinProviderPluginIDs()[job.PluginID]; !ok {
			continue
		}
		wantByPlugin[job.PluginID] = append(wantByPlugin[job.PluginID], builtinPluginCapabilityExpectation{
			name:    job.JobID,
			subject: job.Subject,
			value:   job.Capability,
		})
	}
	if len(wantByPlugin) == 0 {
		t.Fatal("built-in provider background jobs were not registered")
	}

	for pluginID, want := range wantByPlugin {
		descriptor, ok := server.pluginRegistry.Describe(pluginID)
		if !ok {
			t.Fatalf("plugin %q for built-in background jobs is missing", pluginID)
		}
		if !pluginPlacementExists(descriptor.Placements, pluginmeta.PlacementBackground) {
			t.Fatalf("plugin %q placements = %v, want background", pluginID, descriptor.Placements)
		}
		got := builtinPluginCapabilityExpectations(descriptor, pluginmeta.CapabilityKindBackgroundJob)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("plugin %q background job capabilities = %+v, want %+v", pluginID, got, want)
		}
	}
}

func builtinProviderPluginIDs() map[string]struct{} {
	ids := map[string]struct{}{}
	for _, pluginID := range builtinAdapterPlugins {
		ids[pluginID] = struct{}{}
	}
	return ids
}

func builtinPluginCapabilityExpectations(descriptor pluginmeta.Descriptor, kind string) []builtinPluginCapabilityExpectation {
	items := []builtinPluginCapabilityExpectation{}
	for _, capability := range descriptor.Capabilities {
		if capability.Kind != kind {
			continue
		}
		items = append(items, builtinPluginCapabilityExpectation{
			name:    capability.Name,
			subject: capability.Subject,
			value:   capability.Value,
		})
	}
	return items
}

func pluginKindExists(items []pluginmeta.Kind, want pluginmeta.Kind) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func pluginPlacementExists(items []pluginmeta.Placement, want pluginmeta.Placement) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
