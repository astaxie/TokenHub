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
				"id": "resource-setup",
				"slot": "provider.resource.form.section",
				"provider_types": ["openai_codex"],
				"resource_types": ["openai_subscription", "openai_subscription", " "]
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
	if len(manifest.Contributions) != 4 {
		t.Fatalf("contributions = %d, want 4", len(manifest.Contributions))
	}
	if manifest.Contributions[0].PluginID != "tokenhub.codex" || manifest.Contributions[0].ID != "catalog-card" {
		t.Fatalf("first contribution = %+v", manifest.Contributions[0])
	}
	if manifest.Contributions[0].Slot != SlotProviderCatalogCard {
		t.Fatalf("catalog card slot = %q, want %q", manifest.Contributions[0].Slot, SlotProviderCatalogCard)
	}
	gotProviderTypes := manifest.Contributions[3].ProviderTypes
	if len(gotProviderTypes) != 1 || gotProviderTypes[0] != "openai_codex" {
		t.Fatalf("provider types = %v, want openai_codex", gotProviderTypes)
	}
	if manifest.Contributions[3].Action != "codex.quota.read" {
		t.Fatalf("action = %q, want trimmed action", manifest.Contributions[3].Action)
	}
	gotResourceTypes := manifest.Contributions[2].ResourceTypes
	if len(gotResourceTypes) != 1 || gotResourceTypes[0] != "openai_subscription" {
		t.Fatalf("resource types = %v, want openai_subscription", gotResourceTypes)
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
						{"name": "connect", "type": "oauth_button", "action": "oauth.start"}
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

func TestParseAdminUIManifestAcceptsProviderModelPanel(t *testing.T) {
	manifest, err := ParseAdminUIManifest("tokenhub.images", []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "image-capability",
				"slot": "provider.model.panel",
				"provider_types": ["kimi_subscription"],
				"action": "kimi.image_capability.configure",
				"schema": {"layout": "image_capability"}
			}
		]
	}`))
	if err != nil {
		t.Fatalf("parse provider model panel contribution: %v", err)
	}
	if len(manifest.Contributions) != 1 || manifest.Contributions[0].Slot != SlotProviderModelPanel {
		t.Fatalf("provider model panel contribution was not preserved: %+v", manifest.Contributions)
	}
}

func TestParseAdminUIManifestRejectsUnsafeContributionSchema(t *testing.T) {
	for _, payload := range []string{
		`{"fields":[{"name":"script","type":"remote_script"}]}`,
		`{"remote_script_url":"https://plugins.example/plugin.js"}`,
		`{"fields":[{"type":"text"}]}`,
		`{"fields":[{"name":"connect","type":"oauth_button"}]}`,
		`{"tokens":{"accent":"#2563eb"}}`,
		`{"preset":{"density":"compact"}}`,
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

func TestParseAdminUIManifestAcceptsThemeAndLayoutContributions(t *testing.T) {
	manifest, err := ParseAdminUIManifest("tokenhub.sim.enterprise", []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "enterprise-theme",
				"slot": "theme.tokens",
				"title": "Enterprise Theme",
				"schema": {
					"mode": "light",
					"default": true,
					"tokens": {
						"accent": "#2563eb",
						"--surface": "#ffffff",
						"accent-weak": "color-mix(in srgb, #2563eb 12%, transparent)"
					}
				}
			},
			{
				"id": "operator-layout",
				"slot": "layout.preset",
				"title": "Operator Layout",
				"schema": {
					"default": true,
					"preset": {
						"navigation": "sidebar",
						"density": "compact",
						"content_width": "fluid"
					}
				}
			}
		]
	}`))
	if err != nil {
		t.Fatalf("parse admin UI theme/layout manifest: %v", err)
	}
	if len(manifest.Contributions) != 2 {
		t.Fatalf("contributions = %d, want 2", len(manifest.Contributions))
	}
	if manifest.Contributions[1].Slot != SlotThemeTokens {
		t.Fatalf("theme contribution slot = %q, want %q", manifest.Contributions[1].Slot, SlotThemeTokens)
	}
}

func TestParseAdminUIManifestRejectsUnsafeThemeAndLayoutContributions(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		slot    string
		payload string
	}{
		{name: "unknown token", slot: "theme.tokens", payload: `{"tokens":{"unknown":"#111111"}}`},
		{name: "remote asset", slot: "theme.tokens", payload: `{"tokens":{"accent":"url(https://example.test/theme.css)"}}`},
		{name: "css injection", slot: "theme.tokens", payload: `{"tokens":{"accent":"#111111; color: red"}}`},
		{name: "invalid mode", slot: "theme.tokens", payload: `{"mode":"system","tokens":{"accent":"#111111"}}`},
		{name: "invalid default", slot: "theme.tokens", payload: `{"default":"yes","tokens":{"accent":"#111111"}}`},
		{name: "invalid density", slot: "layout.preset", payload: `{"preset":{"density":"dense"}}`},
		{name: "extra layout key", slot: "layout.preset", payload: `{"preset":{"density":"compact","script":"load"}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseAdminUIManifest("tokenhub.bad-sim", []byte(`{
				"schema_version": 1,
				"contributions": [
					{"id": "bad", "slot": "`+testCase.slot+`", "schema": `+testCase.payload+`}
				]
			}`))
			if err == nil {
				t.Fatalf("admin UI manifest with unsafe %s schema parsed successfully", testCase.slot)
			}
		})
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
