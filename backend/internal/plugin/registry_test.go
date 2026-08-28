package plugin

import (
	"reflect"
	"testing"
)

func TestRegistryNormalizesAndListsDescriptors(t *testing.T) {
	registry := NewRegistry()

	err := registry.Register(Descriptor{
		ID:         "tokenhub.test",
		Name:       "Test Plugin",
		Version:    "1.0.0",
		Source:     SourceBuiltIn,
		Kinds:      []Kind{KindProvider, KindProvider, KindAdminUI, ""},
		Placements: []Placement{PlacementManagementAction, PlacementGatewayChain, PlacementGatewayChain},
		Capabilities: []CapabilityDescriptor{
			{Kind: "provider", Name: "chat", Subject: "test"},
			{Kind: "provider", Name: "chat", Subject: "test"},
			{Kind: "provider", Name: "responses", Subject: "test"},
			{Kind: "", Name: "ignored"},
		},
	})
	if err != nil {
		t.Fatalf("register descriptor: %v", err)
	}

	descriptor, ok := registry.Describe("tokenhub.test")
	if !ok {
		t.Fatal("registered descriptor was not found")
	}
	if want := []Kind{KindAdminUI, KindProvider}; !reflect.DeepEqual(descriptor.Kinds, want) {
		t.Fatalf("kinds = %v, want %v", descriptor.Kinds, want)
	}
	if want := []Placement{PlacementGatewayChain, PlacementManagementAction}; !reflect.DeepEqual(descriptor.Placements, want) {
		t.Fatalf("placements = %v, want %v", descriptor.Placements, want)
	}
	if want := []CapabilityDescriptor{
		{Kind: "provider", Name: "chat", Subject: "test"},
		{Kind: "provider", Name: "responses", Subject: "test"},
	}; !reflect.DeepEqual(descriptor.Capabilities, want) {
		t.Fatalf("capabilities = %v, want %v", descriptor.Capabilities, want)
	}
}

func TestRegisterRejectsBlankID(t *testing.T) {
	registry := NewRegistry()

	if err := registry.Register(Descriptor{ID: " "}); err == nil {
		t.Fatal("registering a descriptor with a blank id succeeded")
	}
}

func TestRegistryMergesDescriptorsForSamePlugin(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Descriptor{
		ID:      "tokenhub.codex",
		Name:    "Codex",
		Version: "built-in",
		Source:  SourceBuiltIn,
		Marketplace: &MarketplaceMetadata{
			Summary:    "Official Codex subscription provider.",
			Categories: []string{"provider"},
		},
		Kinds:      []Kind{KindProvider},
		Placements: []Placement{PlacementGatewayChain},
		Capabilities: []CapabilityDescriptor{
			{Kind: "provider", Name: "responses", Subject: "openai_codex"},
		},
	}); err != nil {
		t.Fatalf("register provider descriptor: %v", err)
	}
	if err := registry.Register(Descriptor{
		ID:         "tokenhub.codex",
		Kinds:      []Kind{KindAdminUI},
		Placements: []Placement{PlacementPresentation},
		Capabilities: []CapabilityDescriptor{
			{Kind: "admin_ui", Name: "provider_form"},
		},
	}); err != nil {
		t.Fatalf("register UI descriptor: %v", err)
	}

	descriptor, ok := registry.Describe("tokenhub.codex")
	if !ok {
		t.Fatal("merged descriptor was not found")
	}
	if descriptor.Name != "Codex" || descriptor.Version != "built-in" || descriptor.Source != SourceBuiltIn {
		t.Fatalf("descriptor identity changed: %+v", descriptor)
	}
	if want := []Kind{KindAdminUI, KindProvider}; !reflect.DeepEqual(descriptor.Kinds, want) {
		t.Fatalf("kinds = %v, want %v", descriptor.Kinds, want)
	}
	if len(descriptor.Capabilities) != 2 {
		t.Fatalf("capabilities = %v, want 2 entries", descriptor.Capabilities)
	}
	if descriptor.Marketplace == nil || descriptor.Marketplace.Summary != "Official Codex subscription provider." {
		t.Fatalf("marketplace metadata was not preserved: %+v", descriptor.Marketplace)
	}
}
