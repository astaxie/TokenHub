package plugin

import "testing"

func TestParseAdminUIManifestNormalizesContributions(t *testing.T) {
	manifest, err := ParseAdminUIManifest("tokenhub.codex", []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "quota",
				"slot": "provider.resource.panel",
				"title": "Quota",
				"provider_types": ["openai_codex", "openai_codex", " "],
				"action": " codex.quota.read "
			},
			{
				"id": "setup",
				"slot": "provider.form.section"
			}
		]
	}`))
	if err != nil {
		t.Fatalf("parse admin UI manifest: %v", err)
	}
	if len(manifest.Contributions) != 2 {
		t.Fatalf("contributions = %d, want 2", len(manifest.Contributions))
	}
	if manifest.Contributions[0].PluginID != "tokenhub.codex" || manifest.Contributions[0].ID != "setup" {
		t.Fatalf("first contribution = %+v", manifest.Contributions[0])
	}
	gotProviderTypes := manifest.Contributions[1].ProviderTypes
	if len(gotProviderTypes) != 1 || gotProviderTypes[0] != "openai_codex" {
		t.Fatalf("provider types = %v, want openai_codex", gotProviderTypes)
	}
	if manifest.Contributions[1].Action != "codex.quota.read" {
		t.Fatalf("action = %q, want trimmed action", manifest.Contributions[1].Action)
	}
}

func TestParseAdminUIManifestRejectsUnsupportedSlot(t *testing.T) {
	_, err := ParseAdminUIManifest("tokenhub.bad", []byte(`{
		"schema_version": 1,
		"contributions": [
			{"id": "bad", "slot": "unsafe.remote.script"}
		]
	}`))
	if err == nil {
		t.Fatal("admin UI manifest with unsupported slot parsed successfully")
	}
}

func TestAdminUIRegistryListsContributionsDeterministically(t *testing.T) {
	registry := NewAdminUIRegistry()
	for _, contribution := range []AdminUIContribution{
		{PluginID: "tokenhub.b", ID: "settings", Slot: SlotSettingsPanel},
		{PluginID: "tokenhub.a", ID: "quota", Slot: SlotProviderResourcePanel},
		{PluginID: "tokenhub.a", ID: "setup", Slot: SlotProviderFormSection},
	} {
		if err := registry.Register(contribution); err != nil {
			t.Fatalf("register contribution %+v: %v", contribution, err)
		}
	}
	contributions := registry.List()
	if got := contributions[0].ID; got != "setup" {
		t.Fatalf("first contribution = %q, want setup", got)
	}
	if got := contributions[2].ID; got != "settings" {
		t.Fatalf("last contribution = %q, want settings", got)
	}
}
