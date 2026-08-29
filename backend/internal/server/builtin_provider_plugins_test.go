package server

import (
	"encoding/json"
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

func TestBuiltinOpenAICodexReferencePluginDescriptor(t *testing.T) {
	server := New(NewMemoryStore())
	descriptor, ok := server.pluginRegistry.Describe("tokenhub.provider.openai-codex")
	if !ok {
		t.Fatal("OpenAI Codex provider plugin descriptor is missing")
	}

	for _, kind := range []pluginmeta.Kind{pluginmeta.KindProvider, pluginmeta.KindAdminUI} {
		if !pluginKindExists(descriptor.Kinds, kind) {
			t.Fatalf("OpenAI Codex plugin kinds = %v, want %q", descriptor.Kinds, kind)
		}
	}
	for _, placement := range []pluginmeta.Placement{
		pluginmeta.PlacementGatewayChain,
		pluginmeta.PlacementManagementAction,
		pluginmeta.PlacementBackground,
		pluginmeta.PlacementPresentation,
	} {
		if !pluginPlacementExists(descriptor.Placements, placement) {
			t.Fatalf("OpenAI Codex plugin placements = %v, want %q", descriptor.Placements, placement)
		}
	}

	codex, ok := server.adapterRegistry.Describe(ProviderOpenAICodex)
	if !ok {
		t.Fatal("OpenAI Codex adapter descriptor is missing")
	}
	if codex.PluginID != descriptor.ID {
		t.Fatalf("OpenAI Codex adapter plugin id = %q, want %q", codex.PluginID, descriptor.ID)
	}
	if codex.ProviderPolicy.APIKeyRequired {
		t.Fatal("OpenAI Codex provider plugin should declare provider API keys optional")
	}
	if codex.ProviderPolicy.CredentialsScope != providerCredentialsScopeResource {
		t.Fatalf("OpenAI Codex credentials scope = %q, want resource", codex.ProviderPolicy.CredentialsScope)
	}
	if codex.ProviderPolicy.CredentialRefreshProfile != providerCredentialRefreshProfileOpenAIAccountOAuth {
		t.Fatalf("OpenAI Codex credential refresh profile = %q, want %q", codex.ProviderPolicy.CredentialRefreshProfile, providerCredentialRefreshProfileOpenAIAccountOAuth)
	}
	if codex.ProviderPolicy.SessionAffinityKind != AffinityKindCodexSession {
		t.Fatalf("OpenAI Codex session affinity kind = %q, want %q", codex.ProviderPolicy.SessionAffinityKind, AffinityKindCodexSession)
	}
	if codex.ProviderPolicy.DefaultBaseURL != openAICodexBaseURL {
		t.Fatalf("OpenAI Codex default base URL = %q, want %q", codex.ProviderPolicy.DefaultBaseURL, openAICodexBaseURL)
	}
	if !reflect.DeepEqual(codex.ProviderPolicy.RouteProtocols, []string{providerRouteProtocolCodexResponses, providerRouteProtocolResponses}) {
		t.Fatalf("OpenAI Codex route protocols = %v", codex.ProviderPolicy.RouteProtocols)
	}

	for _, capability := range []AdapterCapability{
		AdapterCapabilityResponses,
		AdapterCapabilityResponseStream,
		AdapterCapabilityModels,
		AdapterCapabilityProbe,
		AdapterCapabilityQuota,
		AdapterCapabilityOAuth,
		AdapterCapabilityAffinity,
		AdapterCapabilityCompact,
		AdapterCapabilityImageGenerate,
	} {
		if !descriptorHasPluginCapability(descriptor, pluginmeta.CapabilityDescriptor{
			Kind:    pluginmeta.CapabilityKindProvider,
			Name:    string(capability),
			Subject: ProviderOpenAICodex,
		}) {
			t.Fatalf("OpenAI Codex plugin does not expose provider capability %q", capability)
		}
	}
	for _, capability := range []pluginmeta.CapabilityDescriptor{
		{Kind: pluginmeta.CapabilityKindProviderType, Name: ProviderOpenAICodex},
		{Kind: pluginmeta.CapabilityKindProviderPolicy, Name: providerAPIKeyRequiredOption, Subject: ProviderOpenAICodex, Value: "false"},
		{Kind: pluginmeta.CapabilityKindProviderPolicy, Name: providerRouteRequiresResourceOption, Subject: ProviderOpenAICodex, Value: "true"},
		{Kind: pluginmeta.CapabilityKindProviderPolicy, Name: providerCredentialsScopeOption, Subject: ProviderOpenAICodex, Value: providerCredentialsScopeResource},
		{Kind: pluginmeta.CapabilityKindProviderPolicy, Name: providerCredentialRefreshProfileOption, Subject: ProviderOpenAICodex, Value: providerCredentialRefreshProfileOpenAIAccountOAuth},
		{Kind: pluginmeta.CapabilityKindProviderPolicy, Name: "session_affinity_kind", Subject: ProviderOpenAICodex, Value: AffinityKindCodexSession},
		{Kind: pluginmeta.CapabilityKindProviderPolicy, Name: "supports_custom_headers", Subject: ProviderOpenAICodex, Value: "false"},
		{Kind: pluginmeta.CapabilityKindProviderPolicy, Name: "route_protocol", Subject: ProviderOpenAICodex, Value: providerRouteProtocolCodexResponses},
		{Kind: pluginmeta.CapabilityKindProviderPolicy, Name: "route_protocol", Subject: ProviderOpenAICodex, Value: providerRouteProtocolResponses},
	} {
		if !descriptorHasPluginCapability(descriptor, capability) {
			t.Fatalf("OpenAI Codex plugin capability is missing: %+v", capability)
		}
	}

	for _, action := range builtinOpenAICodexProviderPluginActions() {
		if !descriptorHasPluginCapability(descriptor, pluginmeta.CapabilityDescriptor{
			Kind:    pluginmeta.CapabilityKindManagementAction,
			Name:    action.id,
			Subject: ProviderOpenAICodex,
			Value:   action.capability,
		}) {
			t.Fatalf("OpenAI Codex plugin action capability %q is missing", action.id)
		}
		if _, ok := server.pluginActions.Describe(descriptor.ID, action.id); !ok {
			t.Fatalf("OpenAI Codex plugin action %q is not registered", action.id)
		}
	}
	for _, job := range builtinOpenAICodexProviderPluginBackgroundJobs() {
		if !descriptorHasPluginCapability(descriptor, pluginmeta.CapabilityDescriptor{
			Kind:    pluginmeta.CapabilityKindBackgroundJob,
			Name:    job.id,
			Subject: ProviderOpenAICodex,
			Value:   job.capability,
		}) {
			t.Fatalf("OpenAI Codex plugin background job capability %q is missing", job.id)
		}
		if _, ok := server.pluginBackgroundJobs.Describe(descriptor.ID, job.id); !ok {
			t.Fatalf("OpenAI Codex plugin background job %q is not registered", job.id)
		}
	}

	resourceType := decodeCodexResourceTypeCapability(t, descriptor)
	if resourceType.Type != ProviderResourceOpenAISubscription ||
		resourceType.DisplayName != "OpenAI Codex Subscription" ||
		!resourceType.Default ||
		!resourceType.CredentialInputOptional ||
		resourceType.CredentialIdentityProfile != providerResourceIdentityProfileOpenAIIDToken {
		t.Fatalf("OpenAI Codex resource type metadata = %+v", resourceType)
	}
	if !reflect.DeepEqual(resourceType.AuthModes, []string{"oauth", "personal_access_token"}) {
		t.Fatalf("OpenAI Codex resource auth modes = %v", resourceType.AuthModes)
	}
	if resourceType.Defaults["auth_type"] != "oauth" ||
		resourceType.Defaults["base_url"] != openAICodexBaseURL ||
		resourceType.Defaults["max_concurrency"] != "3" {
		t.Fatalf("OpenAI Codex resource defaults = %+v", resourceType.Defaults)
	}

	catalog, ok := providerCatalogEntryFromPluginCapability(descriptor, codex)
	if !ok {
		t.Fatal("OpenAI Codex provider catalog capability is missing")
	}
	if catalog.ID != codexProviderCatalogID ||
		catalog.Type != ProviderOpenAICodex ||
		catalog.BaseURL != openAICodexBaseURL ||
		catalog.Source != "openai-codex-live" {
		t.Fatalf("OpenAI Codex catalog entry = %+v", catalog)
	}
	manifestCatalog := decodeCodexCatalogCapability(t, descriptor)
	if manifestCatalog.ModelsAccountRequiredErrorCode != "codex_account_required" {
		t.Fatalf("OpenAI Codex account-required catalog policy = %+v", manifestCatalog)
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

func decodeCodexResourceTypeCapability(t *testing.T, descriptor pluginmeta.Descriptor) pluginmeta.ManifestProviderResourceType {
	t.Helper()
	for _, capability := range descriptor.Capabilities {
		if capability.Kind != pluginmeta.CapabilityKindProviderResourceType ||
			capability.Name != ProviderResourceOpenAISubscription ||
			capability.Subject != ProviderOpenAICodex {
			continue
		}
		var resourceType pluginmeta.ManifestProviderResourceType
		if err := json.Unmarshal([]byte(capability.Value), &resourceType); err != nil {
			t.Fatalf("decode OpenAI Codex resource type capability: %v", err)
		}
		return resourceType
	}
	t.Fatalf("OpenAI Codex resource type capability is missing: %+v", descriptor.Capabilities)
	return pluginmeta.ManifestProviderResourceType{}
}

func decodeCodexCatalogCapability(t *testing.T, descriptor pluginmeta.Descriptor) pluginProviderCatalogEntry {
	t.Helper()
	for _, capability := range descriptor.Capabilities {
		if capability.Kind != pluginmeta.CapabilityKindProviderCatalog ||
			capability.Name != pluginmeta.ProviderCatalogEntry ||
			capability.Subject != ProviderOpenAICodex {
			continue
		}
		var entry pluginProviderCatalogEntry
		if err := json.Unmarshal([]byte(capability.Value), &entry); err != nil {
			t.Fatalf("decode OpenAI Codex catalog capability: %v", err)
		}
		return entry
	}
	t.Fatalf("OpenAI Codex catalog capability is missing: %+v", descriptor.Capabilities)
	return pluginProviderCatalogEntry{}
}

func pluginPlacementExists(items []pluginmeta.Placement, want pluginmeta.Placement) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
