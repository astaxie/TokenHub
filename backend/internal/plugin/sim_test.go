package plugin

import (
	"encoding/json"
	"testing"
)

func TestParseManifestBuildsSIMCapabilities(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.sim.enterprise
name: Enterprise SIM
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - sim
placement:
  - presentation
capabilities:
  sim:
    theme_tokens:
      - id: enterprise-light
        mode: light
        default: true
        tokens:
          accent: "#2563eb"
          surface: "#ffffff"
    shell_layouts:
      - id: operator-shell
        navigation: sidebar
        density: compact
        content_width: fluid
        default: true
    page_templates:
      - id: provider-detail
        target: provider.detail
        layout: two_column
        regions:
          - main
          - side
    dashboard_compositions:
      - id: operations
        layout: grid
        default: true
        cards:
          - contribution_id: cost-overview
            region: main
            size: wide
            order: 20
          - contribution_id: health
            region: side
            size: medium
            order: 10
`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	descriptor := manifest.Descriptor()
	if !descriptorHasCapabilityName(descriptor, "sim", "theme_tokens", "enterprise-light") {
		t.Fatalf("descriptor is missing SIM theme token capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapabilityName(descriptor, "sim", "shell_layout", "operator-shell") {
		t.Fatalf("descriptor is missing SIM shell layout capability: %+v", descriptor.Capabilities)
	}
	if !descriptorHasCapabilityName(descriptor, "sim", "page_template", "provider-detail") {
		t.Fatalf("descriptor is missing SIM page template capability: %+v", descriptor.Capabilities)
	}
	var dashboardValue string
	for _, capability := range descriptor.Capabilities {
		if capability.Kind == "sim" && capability.Name == "dashboard_composition" && capability.Subject == "operations" {
			dashboardValue = capability.Value
			break
		}
	}
	if dashboardValue == "" {
		t.Fatalf("descriptor is missing SIM dashboard composition capability: %+v", descriptor.Capabilities)
	}
	var dashboard ManifestSIMDashboardComposition
	if err := json.Unmarshal([]byte(dashboardValue), &dashboard); err != nil {
		t.Fatalf("decode dashboard composition capability: %v", err)
	}
	if len(dashboard.Cards) != 2 || dashboard.Cards[0].ContributionID != "health" || dashboard.Cards[1].ContributionID != "cost-overview" {
		t.Fatalf("dashboard cards were not normalized deterministically: %+v", dashboard.Cards)
	}
}

func descriptorHasCapabilityName(descriptor Descriptor, kind string, name string, subject string) bool {
	for _, candidate := range descriptor.Capabilities {
		if candidate.Kind == kind && candidate.Name == name && candidate.Subject == subject {
			return true
		}
	}
	return false
}

func TestParseManifestRejectsInvalidSIMCapabilities(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		kinds     string
		placement string
		payload   string
	}{
		{
			name:      "missing sim kind",
			kinds:     "- admin_ui",
			placement: "- presentation",
			payload: `
    theme_tokens:
      - id: theme
        tokens:
          accent: "#2563eb"
`,
		},
		{
			name:      "missing presentation placement",
			kinds:     "- sim",
			placement: "- management_action",
			payload: `
    theme_tokens:
      - id: theme
        tokens:
          accent: "#2563eb"
`,
		},
		{
			name:      "unsafe token",
			kinds:     "- sim",
			placement: "- presentation",
			payload: `
    theme_tokens:
      - id: theme
        tokens:
          accent: "url(https://example.test/theme.css)"
`,
		},
		{
			name:      "invalid layout",
			kinds:     "- sim",
			placement: "- presentation",
			payload: `
    shell_layouts:
      - id: shell
        density: dense
`,
		},
		{
			name:      "invalid dashboard card",
			kinds:     "- sim",
			placement: "- presentation",
			payload: `
    dashboard_compositions:
      - id: ops
        cards:
          - contribution_id: "../unsafe"
`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.bad-sim
name: Bad SIM
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
` + testCase.kinds + `
placement:
` + testCase.placement + `
capabilities:
  sim:
` + testCase.payload))
			if err == nil {
				t.Fatal("manifest with invalid SIM capabilities parsed successfully")
			}
		})
	}
}

func TestParseAdminUIManifestAcceptsSIMTemplateContributions(t *testing.T) {
	manifest, err := ParseAdminUIManifest("tokenhub.sim.enterprise", []byte(`{
		"schema_version": 1,
		"contributions": [
			{
				"id": "provider-template",
				"slot": "page.template",
				"schema": {
					"default": true,
					"template": {
						"id": "provider-detail",
						"target": "provider.detail",
						"layout": "two_column",
						"regions": ["main", "side"]
					}
				}
			},
			{
				"id": "ops-dashboard",
				"slot": "dashboard.composition",
				"schema": {
					"default": true,
					"composition": {
						"id": "ops",
						"layout": "grid",
						"cards": [
							{"contribution_id": "cost-overview", "region": "main", "size": "wide"}
						]
					}
				}
			}
		]
	}`))
	if err != nil {
		t.Fatalf("parse admin UI SIM template manifest: %v", err)
	}
	if len(manifest.Contributions) != 2 || manifest.Contributions[0].Slot != SlotDashboardComposition || manifest.Contributions[1].Slot != SlotPageTemplate {
		t.Fatalf("SIM template contributions were not preserved deterministically: %+v", manifest.Contributions)
	}
}

func TestParseAdminUIManifestRejectsUnsafeSIMTemplateContributions(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		slot    string
		payload string
	}{
		{name: "wrong page key", slot: "page.template", payload: `{"composition":{"id":"wrong","cards":[{"contribution_id":"x"}]}}`},
		{name: "unsafe target", slot: "page.template", payload: `{"template":{"id":"x","target":"../providers"}}`},
		{name: "missing dashboard cards", slot: "dashboard.composition", payload: `{"composition":{"id":"ops"}}`},
		{name: "unsafe dashboard contribution", slot: "dashboard.composition", payload: `{"composition":{"id":"ops","cards":[{"contribution_id":"https://example.test"}]}}`},
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
