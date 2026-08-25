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
			},
			{
				"id": "catalog-card",
				"slot": "provider.catalog.card",
				"title": "Codex"
			}
		]
	}`))
	if err != nil {
		t.Fatalf("parse admin UI manifest: %v", err)
	}
	if len(manifest.Contributions) != 3 {
		t.Fatalf("contributions = %d, want 3", len(manifest.Contributions))
	}
	if manifest.Contributions[0].PluginID != "tokenhub.codex" || manifest.Contributions[0].ID != "catalog-card" {
		t.Fatalf("first contribution = %+v", manifest.Contributions[0])
	}
	if manifest.Contributions[0].Slot != SlotProviderCatalogCard {
		t.Fatalf("catalog card slot = %q, want %q", manifest.Contributions[0].Slot, SlotProviderCatalogCard)
	}
	gotProviderTypes := manifest.Contributions[2].ProviderTypes
	if len(gotProviderTypes) != 1 || gotProviderTypes[0] != "openai_codex" {
		t.Fatalf("provider types = %v, want openai_codex", gotProviderTypes)
	}
	if manifest.Contributions[2].Action != "codex.quota.read" {
		t.Fatalf("action = %q, want trimmed action", manifest.Contributions[2].Action)
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

func TestParseAdminUIManifestValidatesContributionSchema(t *testing.T) {
	manifest, err := ParseAdminUIManifest("tokenhub.forms", []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "setup",
				"slot": "provider.form.section",
				"schema": {
					"fields": [
						{"name": "base_url", "type": "url"},
						{"name": "credential", "type": "secret"},
						{"name": "connect", "type": "oauth_button"}
					]
				}
			}
		]
	}`))
	if err != nil {
		t.Fatalf("parse admin UI manifest with schema: %v", err)
	}
	if len(manifest.Contributions) != 1 || len(manifest.Contributions[0].Schema) == 0 {
		t.Fatalf("schema was not preserved: %+v", manifest.Contributions)
	}
}

func TestParseAdminUIManifestRejectsUnsafeContributionSchema(t *testing.T) {
	for _, payload := range []string{
		`{"fields":[{"name":"script","type":"remote_script"}]}`,
		`{"remote_script_url":"https://plugins.example/plugin.js"}`,
		`{"fields":[{"type":"text"}]}`,
	} {
		_, err := ParseAdminUIManifest("tokenhub.bad-schema", []byte(`{
			"schema_version": 1,
			"contributions": [
				{"id": "setup", "slot": "provider.form.section", "schema": `+payload+`}
			]
		}`))
		if err == nil {
			t.Fatalf("admin UI manifest with unsafe schema parsed successfully: %s", payload)
		}
	}
}

func TestAdminUIRegistryListsContributionsDeterministically(t *testing.T) {
	registry := NewAdminUIRegistry()
	for _, contribution := range []AdminUIContribution{
		{PluginID: "tokenhub.b", ID: "settings", Slot: SlotSettingsPanel},
		{PluginID: "tokenhub.a", ID: "quota", Slot: SlotProviderResourcePanel},
		{PluginID: "tokenhub.a", ID: "setup", Slot: SlotProviderFormSection},
		{PluginID: "tokenhub.a", ID: "catalog-card", Slot: SlotProviderCatalogCard},
	} {
		if err := registry.Register(contribution); err != nil {
			t.Fatalf("register contribution %+v: %v", contribution, err)
		}
	}
	contributions := registry.List()
	if got := contributions[0].ID; got != "catalog-card" {
		t.Fatalf("first contribution = %q, want catalog-card", got)
	}
	if got := contributions[3].ID; got != "settings" {
		t.Fatalf("last contribution = %q, want settings", got)
	}
}
